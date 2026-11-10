package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCanonicalStringsPreserveEscapesAndInvalidUTF8(t *testing.T) {
	// Reference the previous rune-based contract, including malformed UTF-8
	// replacement. Ciphertext, hashes and historical signatures depend on it.
	reference := func(s string) string {
		var b bytes.Buffer
		b.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 32 {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	values := []string{"", "plain <>&中文😀\u2028\u2029", "\xff\xc0\x80a\xe2\x82", strings.Repeat("large plain body 中文", 1000)}
	for i := 0; i < 256; i++ {
		values = append(values, "前"+string([]byte{byte(i)})+"后\n")
	}
	for _, value := range values {
		if got := string(encoded(value)); got != reference(value) || !json.Valid([]byte(got)) {
			t.Fatalf("canonical string mismatch: %q", value)
		}
	}
}

func TestPythonCanonicalJSON(t *testing.T) {
	v := Obj{"中文": "<>&\u2028\u2029\x01\n", "properties": Obj{"key": "值"}, "workers": []any{"worker-1"}, "name": "x"}
	expected := "{\"name\":\"x\",\"properties\":{\"key\":\"值\"},\"workers\":[\"worker-1\"],\"中文\":\"<>&\u2028\u2029\\u0001\\n\"}"
	if got := string(encoded(v)); got != expected {
		t.Fatalf("canonical mismatch: %q", got)
	}
	if !json.Valid(encoded(v)) {
		t.Fatal("invalid JSON")
	}
}
func TestRawPrefixNanosecondsAndShanghaiDate(t *testing.T) {
	tests := []struct{ value, date string }{{"2026-11-05T15:59:59.999999999Z", "2026-11-05"}, {"2026-11-05T16:00:00.000000001Z", "2026-11-06"}, {"2026-11-05T16:00:00.506822718+00:00", "2026-11-06"}}
	for _, test := range tests {
		run := Obj{"id": "run-x", "started_at": test.value, "created_at": "2026-01-01T00:00:00Z"}
		prefix, e := rawPrefix(run)
		if e != nil || prefix != test.date+"/run-x/raw/" {
			t.Fatalf("prefix %q: %v", prefix, e)
		}
	}
	if _, e := rawPrefix(Obj{"id": "bad", "created_at": "no"}); e == nil {
		t.Fatal("invalid timestamp accepted")
	}
}
func TestSafePaths(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	if e := os.WriteFile(filepath.Join(base, "ok.log"), []byte("ok"), 0644); e != nil {
		t.Fatal(e)
	}
	for _, relative := range []string{"../bad", "/etc/passwd", "%2e%2e/bad", "%252e%252e/bad", "a\\b", "a\x00b", "./ok.log"} {
		if _, e := safePath(base, relative, false); e == nil {
			t.Errorf("accepted unsafe path %q", relative)
		}
	}
	if _, e := safePath(base, "ok.log", true); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(base, "ok.log"), filepath.Join(base, "link")); e != nil {
		t.Fatal(e)
	}
	if _, e := safePath(base, "link", true); e == nil {
		t.Fatal("served symlink")
	}
	if _, e := safePath(filepath.Join(base, "link"), "child", false); e == nil {
		t.Fatal("served ancestor symlink")
	}
}
func TestJSONBodyLimitsAndShape(t *testing.T) {
	tests := []struct {
		body   string
		status int
	}{{"[]", 400}, {"null", 400}, {"{} {}", 400}, {"{bad}", 400}, {"", 0}, {"{}", 0}}
	for _, test := range tests {
		r := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
		_, e := readJSON(r)
		if test.status == 0 && e != nil {
			t.Fatalf("%q: %v", test.body, e)
		}
		if test.status != 0 && e == nil {
			t.Fatalf("accepted %q", test.body)
		}
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	r.TransferEncoding = []string{"chunked"}
	if _, e := readJSON(r); e == nil {
		t.Fatal("chunked accepted")
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	r.ContentLength = 6*1024*1024 + 1
	if _, e := readJSON(r); e == nil {
		t.Fatal("oversize accepted")
	}
}
func TestSessionHashPythonFormat(t *testing.T) {
	if got := sessionHash("test-session-key", "abc"); got != "b350e99163562cc12897e1aa46ae392018e2e777d7b54049ab1a37d6b3f57f18" {
		t.Fatal(got)
	}
	sid := token()
	if len(sid) != 43 || strings.ContainsAny(sid, "+/=") {
		t.Fatal("incompatible URL-safe session")
	}
}
func TestSupervisorRestartAndCancellation(t *testing.T) {
	// A missing restart leaves only one PID; missing cancellation cleanup leaves
	// the second real child alive. The fixture touches only this test's temp dir.
	dir := t.TempDir()
	pidsPath, marker := filepath.Join(dir, "pids"), filepath.Join(dir, "started-once")
	command := []string{"/bin/sh", "-c", `printf '%s\n' "$$" >> "$1"; if [ ! -e "$2" ]; then : > "$2"; exit 0; fi; exec /bin/sleep 60`, "supervisor-fixture", pidsPath, marker}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); supervise(ctx, command, "", 0) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("supervisor did not stop during cleanup")
		}
	})
	var pids []int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidsPath)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		pids = nil
		for _, field := range strings.Fields(string(raw)) {
			pid, err := strconv.Atoi(field)
			if err != nil || pid <= 0 {
				t.Fatalf("invalid fixture PID %q: %v", field, err)
			}
			pids = append(pids, pid)
		}
		if len(pids) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pids) != 2 || pids[0] == pids[1] {
		t.Fatalf("expected a replacement child after first child exited, got PIDs %v", pids)
	}
	if err := syscall.Kill(pids[0], 0); err != syscall.ESRCH {
		t.Fatalf("first child was not reaped: %v", err)
	}
	if err := syscall.Kill(pids[1], 0); err != nil {
		t.Fatalf("replacement child is not running: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	if err := syscall.Kill(pids[1], 0); err != syscall.ESRCH {
		t.Fatalf("replacement child survived cancellation: %v", err)
	}
}
func TestAgentContractBoundedAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer agent-secret" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/large" {
			io.WriteString(w, strings.Repeat(" ", 2*1024*1024+1))
			return
		}
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, `{"healthy":true,"generation":3}`)
	}))
	defer server.Close()
	client := newClient(Config{AgentToken: "agent-secret", AgentTimeout: time.Second})
	m, e := client.request(server.URL, "/status", nil)
	if e != nil || !boolean(m["healthy"]) || number(m["generation"]) != 3 {
		t.Fatal(m, e)
	}
	if _, e = client.request(server.URL, "/large", nil); e == nil {
		t.Fatal("unbounded response accepted")
	}
	if _, e = client.request(server.URL, "/missing", nil); agentStatus(e) != 404 {
		t.Fatal(e)
	}
}
