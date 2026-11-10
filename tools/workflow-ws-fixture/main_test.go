package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWSEchoPreservesPayloadAndConnectionOwnership(t *testing.T) {
	server := httptest.NewServer(newFixture())
	defer server.Close()
	first := dialSocket(t, server.URL, "/ws")
	second := dialSocket(t, server.URL, "/ws")
	input := map[string]any{"value": "quoted \"value\"\nline", "nested": map[string]any{"ok": true}}
	writeJSON(t, first, input)
	reply := readJSON(t, first)
	if reply["event"] != "reply" || reply["value"] != input["value"] || !reflect.DeepEqual(reply["received"], input) {
		t.Fatalf("echo did not preserve the input: %#v", reply)
	}
	owner, _ := reply["owner"].(string)
	if owner == "" {
		t.Fatal("connection owner is empty")
	}
	writeJSON(t, second, map[string]any{"value": "second"})
	if other := readJSON(t, second)["owner"]; other == owner || other == "" {
		t.Fatalf("separate connection reused owner: %v", other)
	}
	writeJSON(t, first, map[string]any{"command": "scenario", "value": "chained"})
	if notice := readJSON(t, first); notice["event"] != "notice" {
		t.Fatalf("scenario must deliver notice before reply: %#v", notice)
	}
	reply = readJSON(t, first)
	if reply["event"] != "reply" || reply["owner"] != owner || reply["value"] != "chained" {
		t.Fatalf("scenario reply: %#v", reply)
	}
	response, err := http.Get(server.URL + "/echo?value=" + url.QueryEscape(reply["value"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var chained map[string]any
	if err := json.NewDecoder(response.Body).Decode(&chained); err != nil || chained["code"] != float64(200) || chained["value"] != "chained" {
		t.Fatalf("HTTP chaining response: %#v, %v", chained, err)
	}
	raw := "not json \"quoted\"\nraw"
	if err := first.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, first); got != raw {
		t.Fatalf("raw echo = %q, want %q", got, raw)
	}
	first.Close()
	second.Close()
	waitStats(t, server.URL, map[string]int64{"active": 0, "opened": 2, "closed": 2})
}

func TestWSEchoHonorsBoundedDelay(t *testing.T) {
	server := httptest.NewServer(newFixture())
	defer server.Close()
	conn := dialSocket(t, server.URL, "/ws")
	started := time.Now()
	writeJSON(t, conn, map[string]any{"value": "delayed", "delay_ms": 40})
	if reply := readJSON(t, conn); reply["value"] != "delayed" {
		t.Fatalf("delayed reply: %#v", reply)
	}
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
		t.Fatalf("delay_ms was ignored: %s", elapsed)
	}
	started = time.Now()
	writeJSON(t, conn, map[string]any{"value": "clamped", "delay_ms": 100000})
	if reply := readJSON(t, conn); reply["value"] != "clamped" {
		t.Fatalf("clamped reply: %#v", reply)
	}
	if elapsed := time.Since(started); elapsed > 1800*time.Millisecond {
		t.Fatalf("delay_ms exceeded the one-second bound: %s", elapsed)
	}
}

func TestSocketIOFreshOneUseIDsAndHeartbeatEcho(t *testing.T) {
	server := httptest.NewServer(newFixture())
	defer server.Close()
	firstID, secondID := handshake(t, server.URL), handshake(t, server.URL)
	if firstID == secondID {
		t.Fatal("handshake reused an ID")
	}
	first := dialSocket(t, server.URL, "/socket.io/1/websocket/"+firstID)
	if got := readText(t, first); got != "1::" {
		t.Fatalf("Socket.IO connect packet = %q", got)
	}
	for _, id := range []string{firstID, "never-issued"} {
		conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/socket.io/1/websocket/"+id, nil)
		if conn != nil {
			conn.Close()
		}
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("invalid/reused ID %q accepted: %v, %#v", id, err, response)
		}
	}
	second := dialSocket(t, server.URL, "/socket.io/1/websocket/"+secondID)
	if got := readText(t, second); got != "1::" {
		t.Fatalf("second Socket.IO connect packet = %q", got)
	}
	if got := readText(t, first); got != "2::" {
		t.Fatalf("Socket.IO heartbeat = %q", got)
	}
	if err := first.WriteMessage(websocket.TextMessage, []byte("2::")); err != nil {
		t.Fatal(err)
	}
	waitStats(t, server.URL, map[string]int64{"active": 2, "opened": 2, "socketio_handshakes": 2, "heartbeat_echoes": 1})
	first.Close()
	second.Close()
	waitStats(t, server.URL, map[string]int64{"active": 0, "opened": 2, "closed": 2})
}

func TestSocketIOApplicationFramesRemainOpaque(t *testing.T) {
	server := httptest.NewServer(newFixture())
	defer server.Close()
	conn := dialSocket(t, server.URL, "/socket.io/1/websocket/"+handshake(t, server.URL))
	if readText(t, conn) != "1::" {
		t.Fatal("missing Socket.IO connect frame")
	}
	for _, want := range []string{`3:::{"kind":"opaque","nested":{"number":7}}`, "3:::application payload is not JSON", "4:::raw application data"} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
			t.Fatal(err)
		}
		for {
			got := readText(t, conn)
			if got == "2::" {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(got)); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if got != want {
				t.Fatalf("application frame was interpreted or changed: got %q, want %q", got, want)
			}
			break
		}
	}
	want := []byte{0, 1, 2, 255}
	if err := conn.WriteMessage(websocket.BinaryMessage, want); err != nil {
		t.Fatal(err)
	}
	for {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		kind, got, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.TextMessage && string(got) == "2::" {
			if err := conn.WriteMessage(kind, got); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if kind != websocket.BinaryMessage || !bytes.Equal(got, want) {
			t.Fatal("binary application frame changed type or bytes")
		}
		break
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("0::")); err != nil {
		t.Fatal(err)
	}
	waitStats(t, server.URL, map[string]int64{"active": 0, "closed": 1})
}

func TestHealthAndStatsExposeOnlyAggregateData(t *testing.T) {
	server := httptest.NewServer(newFixture())
	defer server.Close()
	response, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	stats := readStats(t, server.URL)
	if len(stats) != 5 {
		t.Fatalf("stats should contain five aggregate counters: %#v", stats)
	}
	for _, name := range []string{"active", "opened", "closed", "socketio_handshakes", "heartbeat_echoes"} {
		if value, exists := stats[name]; !exists || value != 0 {
			t.Fatalf("initial counter %q = %d, exists %v", name, value, exists)
		}
	}
}

func dialSocket(t *testing.T, base, path string) *websocket.Conn {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+path, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readText(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.TextMessage {
		t.Fatalf("read text: kind %d, error %v", kind, err)
	}
	return string(data)
}

func writeJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	if err := conn.WriteJSON(value); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(readText(t, conn)), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func handshake(t *testing.T, base string) string {
	t.Helper()
	response, err := http.Get(base + "/socket.io/1/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	parts := strings.Split(string(body), ":")
	if err != nil || response.StatusCode != http.StatusOK || len(parts) != 4 || parts[0] == "" || strings.Join(parts[1:], ":") != "60:60:websocket" {
		t.Fatalf("Socket.IO handshake: %q, status %d, error %v", body, response.StatusCode, err)
	}
	return parts[0]
}

func readStats(t *testing.T, base string) map[string]int64 {
	t.Helper()
	response, err := http.Get(base + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var stats map[string]int64
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("stats status %d, error %v", response.StatusCode, err)
	}
	return stats
}

func waitStats(t *testing.T, base string, expected map[string]int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		stats, matches := readStats(t, base), true
		for key, value := range expected {
			matches = matches && stats[key] == value
		}
		if matches {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats = %#v, want %#v", stats, expected)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
