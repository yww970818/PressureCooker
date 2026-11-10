package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type wsSessionDriver struct{ proxySessionDriver }
type wsSessionConn struct{ proxySessionConn }
type wsProjectRows struct{ done bool }

func (wsSessionDriver) Open(string) (driver.Conn, error) { return wsSessionConn{}, nil }
func (wsSessionConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(q, "SELECT data FROM projects") {
		return &wsProjectRows{done: len(args) != 1 || args[0].Value != defaultProjectID}, nil
	}
	return proxySessionConn{}.QueryContext(ctx, q, args)
}
func (*wsProjectRows) Columns() []string { return []string{"data"} }
func (*wsProjectRows) Close() error      { return nil }
func (r *wsProjectRows) Next(v []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	v[0] = `{"id":"project-default","archived":false}`
	return nil
}
func init() { sql.Register("ws-test-session", wsSessionDriver{}) }
func wsPlatform(t *testing.T, target *httptest.Server) *Platform {
	p := proxyPlatform(t, target)
	db, e := sql.Open("ws-test-session", "")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	p.DB = &Database{DB: db}
	return p
}
func wsConnect(t *testing.T, server *httptest.Server, config Obj) *websocket.Conn {
	t.Helper()
	header := http.Header{"Origin": []string{server.URL}, "Cookie": []string{"jmeter_session=valid-cookie"}}
	c, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/ws-proxy", header)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	c.SetReadDeadline(time.Now().Add(4 * time.Second))
	if e = c.WriteJSON(config); e != nil {
		t.Fatal(e)
	}
	return c
}
func TestWebSocketTargetValidation(t *testing.T) {
	for _, raw := range []string{"https://example.test/ws", "file:///tmp/x", "ws://user:pass@example.test/ws", "ws://example.test/ws#x", "ws://example.test:0/ws"} {
		if _, e := wsTarget(raw); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, e := wsTarget("wss://example.test/socket.io/1/websocket/fresh"); e != nil {
		t.Fatal(e)
	}
}
func TestWebSocketRejectsManagedHandshakeHeadersAndUnsafeIPs(t *testing.T) {
	for _, name := range []string{"Sec-WebSocket-Key", "Upgrade", "Connection", "X-JMeter-CSRF-Token"} {
		var spec wsConnectSpec
		spec.Headers = append(spec.Headers, struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{name, "synthetic"})
		if _, e := wsHeaders(spec); e == nil {
			t.Fatalf("accepted managed header %s", name)
		}
	}
	p := &Platform{}
	for _, raw := range []string{"ws://127.0.0.1/ws", "ws://169.254.169.254/ws"} {
		target, e := wsTarget(raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.wsDial(context.Background(), target, http.Header{}, nil); e == nil {
			t.Fatal("unsafe target dialed")
		}
	}
}
func TestWebSocketCannotExportAsPostmanHTTP(t *testing.T) {
	collection := Obj{"name": "WS", "document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "https://example.test"}}, "paths": Obj{"/ws": Obj{"get": Obj{"x-pressure-ws": Obj{"url": "wss://example.test/ws"}}}}}}
	if _, e := exportPostman(collection); e == nil {
		t.Fatal("WS silently exported as HTTP")
	}
}
func TestWebSocketProxyAuthenticationAndCSRF(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer target.Close()
	p := wsPlatform(t, target)
	server := httptest.NewServer(Handler{p})
	defer server.Close()
	r := httptest.NewRequest("GET", "/api/ws-proxy", nil)
	r.Header.Set("Authorization", "Bearer platform-admin")
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("Bearer permitted: %d", w.Code)
	}
	u, _ := url.Parse(target.URL)
	for _, test := range []struct{ csrf, project string }{{"bad", defaultProjectID}, {"csrf-secret", "missing-project"}} {
		c := wsConnect(t, server, Obj{"kind": "connect", "csrf_token": test.csrf, "project_id": test.project, "url": "ws://service.internal:" + u.Port() + "/ws"})
		var event Obj
		if e := c.ReadJSON(&event); e != nil {
			t.Fatal(e)
		}
		if event["kind"] != "error" {
			t.Fatalf("expected auth error: %v", event)
		}
		c.Close()
	}
	if calls.Load() != 0 {
		t.Fatal("target contacted before authorization")
	}
}
func TestWebSocketProxyFreshSocketIOAndBidirectionalFrames(t *testing.T) {
	var handshakes, connections atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://app.example.test" || r.Header.Get("Cookie") != "app_test=synthetic" {
			t.Error("application headers lost")
		}
		if strings.Contains(r.Header.Get("Cookie"), "jmeter_session") || r.Header.Get("X-CSRF-Token") != "" {
			t.Error("platform credentials leaked")
		}
		if r.URL.Path == "/socket.io/1/" {
			handshakes.Add(1)
			io.WriteString(w, "fresh-"+strconv.Itoa(int(handshakes.Load()))+":60:60:websocket")
			return
		}
		connections.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/socket.io/1/websocket/fresh-") {
			t.Errorf("stale id: %s", r.URL.Path)
		}
		conn, e := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		conn.WriteMessage(websocket.TextMessage, []byte("2::"))
		_, body, e := conn.ReadMessage()
		if e != nil || string(body) != "2::" {
			t.Error("Socket.IO heartbeat not echoed")
			return
		}
		_, body, e = conn.ReadMessage()
		if e != nil {
			return
		}
		conn.WriteMessage(websocket.TextMessage, append([]byte("echo:"), body...))
		conn.ReadMessage()
	}))
	defer target.Close()
	p := wsPlatform(t, target)
	server := httptest.NewServer(Handler{p})
	defer server.Close()
	u, _ := url.Parse(target.URL)
	for iteration := range 2 {
		c := wsConnect(t, server, Obj{"kind": "connect", "csrf_token": "csrf-secret", "project_id": defaultProjectID, "url": "ws://service.internal:" + u.Port() + "/socket.io/1/websocket/{socket_id}", "handshake_url": "http://service.internal:" + u.Port() + "/socket.io/1/", "socketio": true, "headers": []any{Obj{"name": "Origin", "value": "https://app.example.test"}, Obj{"name": "Cookie", "value": "app_test=synthetic"}}})
		opened, echo, sent := false, false, false
		for range 8 {
			var event Obj
			if e := c.ReadJSON(&event); e != nil {
				t.Fatal(e)
			}
			if event["kind"] == "error" {
				t.Fatalf("proxy error: %v", event)
			}
			if event["state"] == "open" {
				if event["socket_id"] != "fresh-"+strconv.Itoa(iteration+1) {
					t.Fatalf("missing fresh transport ID: %v", event)
				}
				opened = true
			}
			if event["direction"] == "sent" && event["data"] == "2::" && !sent {
				sent = true
				c.WriteJSON(Obj{"kind": "send", "data": "hello"})
			}
			if event["direction"] == "received" && event["data"] == "echo:hello" {
				echo = true
				break
			}
		}
		if !opened || !echo {
			t.Fatal("missing state or response")
		}
		c.WriteJSON(Obj{"kind": "close"})
		c.Close()
	}
	if handshakes.Load() != 2 || connections.Load() != 2 {
		t.Fatal("temporary ID was reused")
	}
}
func TestWebSocketControlEnvelopePreservesPayloadLimit(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, e := conn.ReadMessage()
			if e != nil {
				return
			}
			received.Add(1)
			if e = conn.WriteMessage(kind, data); e != nil {
				return
			}
		}
	}))
	defer target.Close()
	p := wsPlatform(t, target)
	server := httptest.NewServer(Handler{p})
	defer server.Close()
	u, _ := url.Parse(target.URL)
	client := wsConnect(t, server, Obj{"kind": "connect", "csrf_token": "csrf-secret", "project_id": defaultProjectID, "url": "ws://service.internal:" + u.Port() + "/ws"})
	var event Obj
	if e := client.ReadJSON(&event); e != nil || event["state"] != "open" {
		t.Fatal("not open", e)
	}
	for _, body := range []string{strings.Repeat("\"", 140*1024), strings.Repeat("\\", 140*1024), strings.Repeat("a", wsFrameLimit)} {
		if e := client.WriteJSON(Obj{"kind": "send", "data": body}); e != nil {
			t.Fatal(e)
		}
		echoed := false
		for range 3 {
			if e := client.ReadJSON(&event); e != nil {
				t.Fatal(e)
			}
			if event["kind"] == "error" {
				t.Fatal("valid bounded payload rejected")
			}
			if event["direction"] == "received" {
				if event["data"] != body {
					t.Fatal("payload changed")
				}
				echoed = true
				break
			}
		}
		if !echoed {
			t.Fatal("no echo")
		}
	}
	client.WriteJSON(Obj{"kind": "send", "data": strings.Repeat("a", wsFrameLimit+1)})
	if e := client.ReadJSON(&event); e != nil || event["kind"] != "error" {
		t.Fatal("oversized decoded payload accepted", e)
	}
	if received.Load() != 3 {
		t.Fatal("oversized payload forwarded")
	}
}
