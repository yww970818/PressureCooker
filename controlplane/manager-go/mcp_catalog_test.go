package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Empty session rows let the HTTP authentication contract be exercised without
// requiring a database or touching any platform data.
type emptySessionsDriver struct{}
type emptySessionsConn struct{}
type emptySessionsRows struct{}

func (emptySessionsDriver) Open(string) (driver.Conn, error) { return emptySessionsConn{}, nil }
func (emptySessionsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (emptySessionsConn) Close() error              { return nil }
func (emptySessionsConn) Begin() (driver.Tx, error) { return nil, errors.New("not supported") }
func (emptySessionsConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return emptySessionsRows{}, nil
}
func (emptySessionsRows) Columns() []string         { return []string{"csrf", "expires"} }
func (emptySessionsRows) Close() error              { return nil }
func (emptySessionsRows) Next([]driver.Value) error { return io.EOF }
func init()                                         { sql.Register("empty-mcp-sessions", emptySessionsDriver{}) }
func catalogPlatform(t *testing.T, endpoint string) *Platform {
	t.Helper()
	db, e := sql.Open("empty-mcp-sessions", "")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return &Platform{Config: Config{AdminToken: "catalog-admin-secret", SessionKey: "test-session-key", MCPPublicURL: "http://home.yanww.top:18091/mcp", mcpCatalogTestURL: endpoint}, DB: &Database{DB: db}}
}
func catalogRequest(t *testing.T, p *Platform, authorization string) (int, Obj) {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/mcp/tools?url=http://untrusted.invalid/should-not-dial", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	m, e := decode(w.Body.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	return w.Code, m
}
func TestMCPCatalogAuthenticationAndConnectedTools(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer catalog-admin-secret" || !strings.Contains(r.Header.Get("Accept"), "application/json") || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Error("invalid internal MCP request")
		}
		m, e := decode(mustRead(t, r.Body))
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		method := str(m["method"])
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		result := Obj{}
		switch method {
		case "initialize":
			if obj(m["params"])["protocolVersion"] != "2025-11-25" {
				t.Error("missing version negotiation")
			}
			w.Header().Set("Mcp-Session-Id", "catalog-session")
			result = Obj{"protocolVersion": "2025-11-25", "serverInfo": Obj{"name": "jmeter-mcp", "version": "1.2.3"}, "capabilities": Obj{"tools": Obj{}}}
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != "catalog-session" {
				t.Error("missing session header")
			}
			w.WriteHeader(202)
			return
		case "tools/list":
			if r.Header.Get("MCP-Protocol-Version") != "2025-11-25" {
				t.Error("missing negotiated header")
			}
			result = Obj{"tools": []any{Obj{"name": "platform_status", "title": "平台状态", "description": "auth catalog-admin-secret must never leak", "inputSchema": Obj{"type": "object", "properties": Obj{}}, "annotations": Obj{"readOnlyHint": true}}}}
		default:
			t.Errorf("unexpected method %s", method)
		}
		w.Write(encoded(Obj{"jsonrpc": "2.0", "id": m["id"], "result": result}))
	}))
	defer server.Close()
	p := catalogPlatform(t, server.URL+"/mcp")
	if status, _ := catalogRequest(t, p, ""); status != 401 {
		t.Fatal("anonymous catalog", status)
	}
	if status, _ := catalogRequest(t, p, "Bearer wrong"); status != 401 {
		t.Fatal("invalid bearer catalog", status)
	}
	status, result := catalogRequest(t, p, "Bearer catalog-admin-secret")
	if status != 200 || !boolean(result["available"]) || number(result["total"]) != 1 || str(result["endpoint"]) != "http://home.yanww.top:18091/mcp" || result["protocol"] != "streamable-http" || result["authentication"] != "bearer" {
		t.Fatal(status, result)
	}
	if strings.Contains(string(encoded(result)), "catalog-admin-secret") || obj(result["server"])["name"] != "jmeter-mcp" {
		t.Fatal("unsafe/incomplete catalog", result)
	}
	if len(arr(result["tools"])) != 1 || obj(arr(result["tools"])[0])["name"] != "platform_status" {
		t.Fatal(result)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "initialize,notifications/initialized,tools/list" {
		t.Fatal(methods)
	}
}
func mustRead(t *testing.T, source io.Reader) []byte {
	t.Helper()
	raw, e := io.ReadAll(source)
	if e != nil {
		t.Error(e)
	}
	return raw
}
func TestMCPCatalogSSEPaginationAndBoundedUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, _ := decode(mustRead(t, r.Body))
		method := str(m["method"])
		if method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		result := Obj{}
		switch method {
		case "initialize":
			result = Obj{"protocolVersion": "2026-07-28", "serverInfo": Obj{"name": "test", "version": "1"}}
		case "tools/list":
			if r.Header.Get("MCP-Protocol-Version") != "2026-07-28" {
				t.Error("version not negotiated")
			}
			name := "first"
			if str(obj(m["params"])["cursor"]) != "" {
				name = "second"
			}
			result = Obj{"tools": []any{Obj{"name": name, "inputSchema": Obj{"type": "object"}}}}
			if name == "first" {
				result["nextCursor"] = "page-2"
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": keepalive\n\nevent: message\ndata: ")
		w.Write(encoded(Obj{"jsonrpc": "2.0", "id": m["id"], "result": result}))
		io.WriteString(w, "\n\n")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	p := catalogPlatform(t, server.URL)
	result := p.mcpTools(context.Background())
	if !boolean(result["available"]) || number(result["total"]) != 2 {
		t.Fatal(result)
	}
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
	}{{"malformed", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "catalog-admin-secret bad JSON") }}, {"oversize", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("catalog-admin-secret", mcpCatalogLimit/8))
	}}, {"upstream-error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "catalog-admin-secret")
	}}, {"redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://untrusted.invalid/never-follow", 302)
	}}} {
		t.Run(test.name, func(t *testing.T) {
			peer := httptest.NewServer(test.handler)
			defer peer.Close()
			p := catalogPlatform(t, peer.URL)
			status, response := catalogRequest(t, p, "Bearer catalog-admin-secret")
			if status != 200 || boolean(response["available"]) || number(response["total"]) != 0 || len(arr(response["tools"])) != 0 || str(response["error"]) == "" {
				t.Fatal(status, response)
			}
			if raw := string(encoded(response)); len(raw) > 4096 || strings.Contains(raw, "catalog-admin-secret") || !strings.Contains(raw, "暂时不可用") {
				t.Fatal("unsafe unavailability output", raw)
			}
		})
	}
}
func TestMCPCatalogTimeoutAndNoArbitraryURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	p := catalogPlatform(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := p.mcpTools(ctx)
	if boolean(result["available"]) || !strings.Contains(str(result["error"]), "超时") || time.Since(started) > time.Second {
		t.Fatal(result, time.Since(started))
	}
	client := newMCPCatalogClient(Config{MCPPublicURL: "http://untrusted.invalid/mcp", MCPLocalPort: 18881})
	if client.endpoint != "http://127.0.0.1:18881/mcp" || client.http.Timeout != 5*time.Second {
		t.Fatal("public URL used as dial target", client.endpoint)
	}
	client.http.CloseIdleConnections()
	if obj(openAPI()["paths"])["/api/mcp/tools"] == nil {
		t.Fatal("missing OpenAPI discovery")
	}
}
