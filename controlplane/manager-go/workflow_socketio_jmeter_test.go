package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWorkflowWebSocketSocketIOVariablesAreConnectionScoped(t *testing.T) {
	home, java := localWsTraceJava(t)
	var mu sync.Mutex
	handshakes, cleared := 0, 0
	received := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/socket.io/1/" {
			mu.Lock()
			handshakes++
			id := fmt.Sprintf("fresh-%d", handshakes)
			mu.Unlock()
			fmt.Fprintf(w, "%s:60:60:websocket", id)
			return
		}
		if r.URL.Path == "/cleared" {
			mu.Lock()
			cleared++
			mu.Unlock()
			io.WriteString(w, "cleared")
			return
		}
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		var request map[string]any
		if err = conn.ReadJSON(&request); err != nil {
			return
		}
		id, _ := request["socket_id"].(string)
		mu.Lock()
		received = append(received, id)
		mu.Unlock()
		if err = conn.WriteJSON(request); err != nil {
			return
		}
		conn.ReadMessage()
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	graph := Obj{"nodes": []any{graphNode("root", "TestPlan", nil), graphNode("group", "ThreadGroup", Obj{"numThreads": "1", "rampTimeSeconds": "0", "loops": "2"})}, "edges": []any{graphEdge("root", "group", 0)}}
	add := func(id, kind, parent string, order int, config Obj) {
		graph["nodes"] = append(arr(graph["nodes"]), graphNode(id, kind, config))
		graph["edges"] = append(arr(graph["edges"]), graphEdge(parent, id, order))
	}
	add("connect", "WebSocketSampler", "group", 0, Obj{"action": "connect", "url": "ws" + strings.TrimPrefix(server.URL, "http") + "/socket.io/1/websocket/{socket_id}", "handshakeUrl": server.URL + "/socket.io/1/", "socketio": true})
	add("echo", "WebSocketSampler", "group", 1, Obj{"action": "request", "message": `{"kind":"echo","socket_id":"{{socket_id}}"}`, "receiveMode": "json", "matchJson": `{"$.frame.socket_id":"{{socket_id}}"}`})
	add("close", "WebSocketSampler", "group", 2, Obj{"action": "close"})
	add("scope-cleared", "IfController", "group", 3, Obj{"condition": "${__groovy(vars.get('socket_id') == null)}"})
	add("verify-cleared", "HTTPSamplerProxy", "scope-cleared", 0, Obj{"domain": u.Hostname(), "port": u.Port(), "path": "/cleared"})
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plan, result := filepath.Join(dir, "plan.jmx"), filepath.Join(dir, "results.jtl")
	if err = os.WriteFile(plan, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(home, "bin", "jmeter"), "-n", "-t", plan, "-l", result, "-j", filepath.Join(dir, "jmeter.log"))
	command.Env = append(os.Environ(), "JAVA_HOME="+java)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("local Socket.IO JMeter process: %v\n%s", err, out)
	}
	f, err := os.Open(result)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 9 {
		t.Fatal("missing socket/close verification samples", len(rows), err)
	}
	success := -1
	for index, name := range rows[0] {
		if name == "success" {
			success = index
		}
	}
	if success < 0 {
		t.Fatal("missing JTL success column")
	}
	for _, row := range rows[1:] {
		if row[success] != "true" {
			t.Fatal("Socket.IO transport sampler failed", row)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if handshakes != 2 || cleared != 2 || len(received) != 2 || received[0] != "fresh-1" || received[1] != "fresh-2" {
		t.Fatal("handshake ID leaked across connection lifetime", handshakes, cleared, received)
	}
}
