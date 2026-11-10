package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func TestCallerMetadataNeverBecomesTargetHeaders(t *testing.T) {
	header := http.Header{"Authorization": []string{"Bearer target-only"}, protocol.CallerIPHeader: []string{"198.51.100.3"}, protocol.CallerTimeHeader: []string{"123"}, protocol.CallerSignatureHeader: []string{"synthetic"}}
	clean := proxyRequestHeaders(header)
	for _, name := range []string{protocol.CallerIPHeader, protocol.CallerTimeHeader, protocol.CallerSignatureHeader} {
		if clean.Get(name) != "" {
			t.Fatal("HTTP proxy forwarded caller metadata", name)
		}
	}
	var spec wsConnectSpec
	rows := []any{}
	for name, values := range header {
		rows = append(rows, Obj{"name": name, "value": values[0]})
	}
	if err := json.Unmarshal(encoded(Obj{"headers": rows}), &spec); err != nil {
		t.Fatal(err)
	}
	clean, err := wsHeaders(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{protocol.CallerIPHeader, protocol.CallerTimeHeader, protocol.CallerSignatureHeader} {
		if clean.Get(name) != "" {
			t.Fatal("WS handshake forwarded caller metadata", name)
		}
	}
	if clean.Get("Authorization") != "Bearer target-only" || len(header[protocol.CallerIPHeader]) == 0 {
		t.Fatal("sanitizing changed business auth or source header")
	}
}

func TestAutomaticLegacyHTTPDebugResolvesOnceAndStripsInternalHeaders(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Source") != "198.51.100.7" || r.Header.Get("X-Clock") != "1794212345678" || r.URL.Query().Get("clock") != "1794212345678" {
			t.Error("HTTP operation context changed", r.Header, r.URL.RawQuery)
		}
		for _, name := range []string{protocol.CallerIPHeader, protocol.CallerTimeHeader, protocol.CallerSignatureHeader} {
			if r.Header.Get(name) != "" {
				t.Error("collection target received internal context", name)
			}
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	parameters := []any{Obj{"in": "header", "name": "X-Source", "example": "{{ip_field}}"}, Obj{"in": "header", "name": "X-Clock", "example": "{{clock_a}}"}, Obj{"in": "query", "name": "clock", "example": "{{clock_b}}"}}
	for _, name := range []string{protocol.CallerIPHeader, protocol.CallerTimeHeader, protocol.CallerSignatureHeader} {
		parameters = append(parameters, Obj{"in": "header", "name": name, "example": "not-target-data"})
	}
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": fmt.Sprintf("http://service.internal:%s", u.Port())}}, "paths": Obj{"/request": Obj{"get": Obj{"parameters": parameters}}}, "x-scalar-environments": Obj{"test": Obj{"variables": Obj{"ip_field": "{{$auto.client_ip}}", "clock_a": "{{$auto.timestamp_ms}}", "clock_b": "{{$auto.timestamp_ms}}"}}}}, "client_state": Obj{"active_environment": "test"}}
	p := auditDebugPlatform(t, target, collection)
	ctx := withEnvironmentOperation(context.Background(), newEnvironmentOperation("198.51.100.7", time.UnixMilli(1794212345678)))
	result, err := p.collectionDebug(ctx, str(collection["id"]), Obj{"target_source": "document_server", "path": "/request", "method": "GET", "variables": Obj{"ip_field": "{{$auto.client_ip}}", "clock_a": "{{$auto.timestamp_ms}}", "clock_b": "{{$auto.timestamp_ms}}"}})
	if err != nil || number(result["status"]) != 204 || calls != 1 {
		t.Fatal("automatic debug failed", result, err, calls)
	}
}

func TestCallerMetadataStrippedOnActualHTTPAndWebSocketHops(t *testing.T) {
	seen := make(chan http.Header, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		if r.URL.Path == "/socket" {
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(time.Second))
			conn.ReadMessage()
			return
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	p := proxyPlatform(t, target)
	r := makeProxyRequest(target, "GET", nil)
	for _, name := range []string{protocol.CallerIPHeader, protocol.CallerTimeHeader, protocol.CallerSignatureHeader} {
		r.Header.Set(name, "must-not-forward")
	}
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("HTTP proxy failed", w.Code, w.Body.String())
	}
	u, _ := url.Parse(target.URL)
	socketURL, _ := url.Parse("ws://service.internal:" + u.Port() + "/socket")
	conn, err := p.wsDial(context.Background(), socketURL, r.Header.Clone(), nil)
	if err != nil {
		t.Fatal("WS local target failed", err)
	}
	conn.Close()
	for i := 0; i < 2; i++ {
		header := <-seen
		for name := range header {
			if callerContextHeader(name) {
				t.Error("actual target received private source context", name)
			}
		}
	}
}
