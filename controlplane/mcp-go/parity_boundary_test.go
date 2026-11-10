package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPAllowsWorkflowSizedBodyAndStillBoundsEnvelope(t *testing.T) {
	h := secureHandler("token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, e := io.Copy(io.Discard, r.Body); e != nil {
			w.WriteHeader(413)
			return
		}
		w.WriteHeader(204)
	}))
	for _, tc := range []struct {
		name       string
		size, want int
	}{{"create_workflow", 40 << 20, 204}, {"get_project", 7 << 20, 413}, {"create_workflow", 66 << 20, 413}} {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tc.name + `","arguments":{"projectId":"project-one","name":"large","graph":{"description":"` + strings.Repeat("x", tc.size) + `"}}}}`
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("body bytes=%d status=%d want=%d", tc.size, w.Code, tc.want)
		}
	}
}

func TestWorkflowResponseSupportsLargeGraphWithoutExpandingOtherAPIs(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("secret"), 0600); e != nil {
		t.Fatal(e)
	}
	large := strings.Repeat("x", 17<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"graph":{"description":"`+large+`"}}`)
	}))
	defer server.Close()
	c := managerClient{baseURL: server.URL, tokenFile: token, client: server.Client()}
	got, e := c.requestProject(context.Background(), "GET", "/api/workflows/workflow-one", nil, "", "project-one")
	if e != nil {
		t.Fatal("valid large workflow response hit old limit", e)
	}
	if len(got["graph"].(map[string]any)["description"].(string)) != 17<<20 {
		t.Fatal("workflow graph truncated")
	}
	if _, e := c.requestProject(context.Background(), "GET", "/api/runs/run-one", nil, "", "project-one"); e == nil {
		t.Fatal("unrelated API response limit was expanded")
	}
}
