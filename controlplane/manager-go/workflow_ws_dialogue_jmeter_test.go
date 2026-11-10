package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type wsDialogueFixture struct {
	mu             sync.Mutex
	ids, payloads  []string
	opened, closed int
	mode           string
}

func genericDialogueGraph(url, csvFile string) Obj {
	g := Obj{"nodes": []any{graphNode("root", "TestPlan", nil), graphNode("group", "ThreadGroup", Obj{"numThreads": "1", "rampTimeSeconds": "0", "loops": "1"})}, "edges": []any{graphEdge("root", "group", 0)}}
	add := func(id, component, parent string, order int, config Obj) {
		node := graphNode(id, component, config)
		obj(node["properties"])["name"] = id
		g["nodes"] = append(arr(g["nodes"]), node)
		g["edges"] = append(arr(g["edges"]), graphEdge(parent, id, order))
	}
	add("csv", "CSVDataSet", "group", 0, Obj{"filename": csvFile, "variableNames": "payload_1,payload_2,payload_3", "ignoreFirstLine": true, "recycle": false, "stopThread": true})
	add("connect", "WebSocketSampler", "group", 1, Obj{"action": "connect", "url": url, "heartbeatEnabled": true, "heartbeatIntervalMs": "100", "heartbeatMessage": `{"kind":"heartbeat"}`})
	for turn := 1; turn <= 3; turn++ {
		add(fmt.Sprintf("request-%02d", turn), "WebSocketSampler", "group", 2*turn, Obj{"action": "request", "message": fmt.Sprintf(`{"kind":"request","message_id":"{{message_id}}","payload":"{{payload_%d}}"}`, turn), "receiveMode": "json", "matchJson": `{"$.frame.kind":"approval","$.frame.message_id":"{{message_id}}","$.frame.status":"approved"}`, "timeoutMs": "500"})
		guard := fmt.Sprintf("approved-%02d", turn)
		// A timeout disposes the connection; do not run a dependent receive.
		add(guard, "IfController", "group", 2*turn+1, Obj{"condition": "${JMeterThread.last_sample_ok}"})
		add(fmt.Sprintf("reply-%02d", turn), "WebSocketSampler", guard, 0, Obj{"action": "read", "receiveMode": "json", "matchJson": fmt.Sprintf(`{"$.frame.kind":"reply","$.frame.message_id":"{{message_id}}","$.frame.payload":"{{payload_%d}}"}`, turn), "timeoutMs": "500"})
	}
	add("close", "WebSocketSampler", "group", 8, Obj{"action": "close"})
	return g
}

// Compile and execute a generic JSON dialogue locally. Replies precede their
// approval, and repeated payloads must still have fresh correlation identifiers.
func TestWorkflowWebSocketTraceThreeTurnDialogue(t *testing.T) {
	home, java := localWsTraceJava(t)
	for _, mode := range []string{"pass", "reject_third_approval", "omit_third_approval", "old_third_approval", "wrong_third_reply"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			fixture := &wsDialogueFixture{mode: mode}
			server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
			defer server.Close()
			csvFile := filepath.Join(dir, "payloads.csv")
			payloads := []string{"PRIVATE_TRACE_FIXTURE first", "PRIVATE_TRACE_FIXTURE \"quoted\"\nsecond line", "PRIVATE_TRACE_FIXTURE first"}
			f, err := os.Create(csvFile)
			if err != nil {
				t.Fatal(err)
			}
			writer := csv.NewWriter(f)
			writer.WriteAll([][]string{{"payload_1", "payload_2", "payload_3"}, payloads})
			if err := writer.Error(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			content, err := compileWorkflow(genericDialogueGraph("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", csvFile))
			if err != nil {
				t.Fatal(err)
			}
			plan, result, logPath := filepath.Join(dir, "test.jmx"), filepath.Join(dir, "result.jtl"), filepath.Join(dir, "jmeter.log")
			if err = os.WriteFile(plan, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, filepath.Join(home, "bin", "jmeter"), "-n", "-t", plan, "-l", result, "-j", logPath, "-Jwebsocket_trace_events=true", "-Jsample_variables=message_id")
			command.Env = append(os.Environ(), "JAVA_HOME="+java)
			if _, err := command.CombinedOutput(); err != nil {
				t.Fatalf("local synthetic JMeter process failed: %v", err)
			}
			f, err = os.Open(result)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			reader := csv.NewReader(f)
			header, err := reader.Read()
			if err != nil {
				t.Fatal(err)
			}
			requests, replies, failures, targeted, closes := 0, 0, 0, 0, 0
			ids := map[string]bool{}
			for {
				values, err := reader.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				row := map[string]string{}
				for i, key := range header {
					row[key] = values[i]
				}
				if strings.HasPrefix(row["label"], "request-") {
					requests++
					if row["success"] == "true" {
						ids[row["message_id"]] = true
					}
				}
				if strings.HasPrefix(row["label"], "reply-") && row["success"] == "true" {
					replies++
				}
				if row["label"] == "close" && row["success"] == "true" {
					closes++
				}
				if row["success"] != "true" {
					failures++
					label := "request-03"
					if mode == "wrong_third_reply" {
						label = "reply-03"
					}
					if row["label"] == label && row["responseCode"] == "WS_TIMEOUT" {
						targeted++
					}
				}
			}
			wantFailures, wantReplies, wantIDs, wantMatches := 0, 3, 3, 6
			if mode != "pass" {
				wantFailures = 1
				wantReplies, wantIDs, wantMatches = 2, 2, 4
			}
			if mode == "wrong_third_reply" {
				wantIDs, wantMatches = 3, 5
			}
			if requests != 3 || len(ids) != wantIDs || ids[""] || ids["null"] || failures != wantFailures || targeted != wantFailures || replies != wantReplies || closes != 1 {
				t.Fatalf("correlated dialogue outcome: requests=%d uniqueIDs=%d replies=%d failures=%d targeted=%d closes=%d", requests, len(ids), replies, failures, targeted, closes)
			}
			logs, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(logs), "PRIVATE_TRACE_FIXTURE") {
				t.Fatal("diagnostics leaked private protocol payloads")
			}
			phases := map[string]int{}
			for _, record := range readWsTraceRecords(t, string(logs)) {
				phases[fmt.Sprint(record["phase"])]++
			}
			if phases["send"] != 3 || phases["timeout"] != wantFailures || phases["match"] != wantMatches {
				t.Fatalf("lost generic send, match or timeout evidence: %v", phases)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.ids) != 3 || fixture.opened != 1 || fixture.closed != 1 {
				t.Fatalf("connection lifecycle: opened=%d closed=%d requests=%d", fixture.opened, fixture.closed, len(fixture.ids))
			}
			fresh := map[string]bool{}
			for _, id := range fixture.ids {
				fresh[id] = true
			}
			if len(fresh) != 3 || fresh[""] {
				t.Fatal("repeated payloads reused a message identifier")
			}
			for i, payload := range payloads {
				if fixture.payloads[i] != payload {
					t.Fatal("CSV interpolation changed quoted or repeated data")
				}
			}
		})
	}
}

func (f *wsDialogueFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ws" {
		http.NotFound(w, r)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.opened++
	f.mu.Unlock()
	defer func() { conn.Close(); f.mu.Lock(); f.closed++; f.mu.Unlock() }()
	conn.SetReadLimit(262144)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var frame Obj
		if json.Unmarshal(raw, &frame) != nil {
			return
		}
		if frame["kind"] == "heartbeat" {
			continue
		}
		if frame["kind"] != "request" {
			return
		}
		id, payload := str(frame["message_id"]), str(frame["payload"])
		f.mu.Lock()
		f.ids, f.payloads = append(f.ids, id), append(f.payloads, payload)
		turn, firstID := len(f.ids), f.ids[0]
		f.mu.Unlock()
		if turn == 3 && f.mode == "wrong_third_reply" {
			payload = "unexpected synthetic value"
		}
		// Matching the approval must leave this earlier reply in the inbox.
		if conn.WriteJSON(Obj{"kind": "reply", "message_id": id, "payload": payload}) != nil {
			return
		}
		if turn == 3 {
			if conn.WriteJSON(Obj{"kind": "approval", "message_id": firstID, "status": "approved"}) != nil {
				return
			}
			if f.mode == "omit_third_approval" {
				continue
			}
			if f.mode == "old_third_approval" {
				id = firstID
			}
		}
		status := "approved"
		if turn == 3 && f.mode == "reject_third_approval" {
			status = "rejected"
		}
		if conn.WriteJSON(Obj{"kind": "approval", "message_id": id, "status": status}) != nil {
			return
		}
	}
}
