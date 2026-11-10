package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func runtimeFixture(t *testing.T) *Platform {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("runtime context contacted a target") }))
	t.Cleanup(peer.Close)
	return proxyPlatform(t, peer)
}
func runtimeRequest() *http.Request {
	r := httptest.NewRequest("GET", "/api/environment-runtime", nil)
	r.Header.Set("Cookie", "jmeter_session=valid-cookie")
	r.Header.Set("X-Project-ID", "project-private-context")
	return r
}
func TestEnvironmentRuntimeUsesOnlyActualPeerAndRequiresProjectAuthentication(t *testing.T) {
	p := runtimeFixture(t)
	for _, tc := range []struct{ peer, want string }{{"203.0.113.7:54321", "203.0.113.7"}, {"[2001:db8::8]:1234", "2001:db8::8"}, {"[::ffff:192.0.2.9]:9876", "192.0.2.9"}, {"", ""}, {"untrusted.invalid:1234", ""}} {
		r := runtimeRequest()
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", "198.51.100.99")
		r.Header.Set("Forwarded", "for=198.51.100.99")
		w := httptest.NewRecorder()
		before := time.Now().UnixMilli()
		Handler{p}.ServeHTTP(w, r)
		value, err := decode(w.Body.Bytes())
		ms, parseErr := strconv.ParseInt(str(value["timestamp_ms"]), 10, 64)
		if w.Code != 200 || err != nil || value["client_ip"] != tc.want || parseErr != nil || ms < before || ms > time.Now().UnixMilli() {
			t.Fatalf("invalid runtime context: %d %s", w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		cookie, project string
		want            int
	}{{"", "project-private-context", 401}, {"jmeter_session=valid-cookie", "project-missing", 404}} {
		r := runtimeRequest()
		r.Header.Set("Cookie", tc.cookie)
		r.Header.Set("X-Project-ID", tc.project)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal("runtime bypassed authentication/project", w.Code, w.Body.String())
		}
	}
	r := runtimeRequest()
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r.WithContext(ctx))
	if w.Code != 499 {
		t.Fatal("canceled runtime request completed", w.Code)
	}
}
func TestEnvironmentRuntimeAcceptsOnlyAuthenticatedSignedCallerMetadata(t *testing.T) {
	p := runtimeFixture(t)
	for _, tc := range []struct {
		name, ip string
		bearer   bool
		mutate   func(*http.Request)
		want     int
	}{
		{"trusted MCP", "198.51.100.4", true, nil, 200}, {"unknown MCP", "", true, nil, 200},
		{"cookie cannot attribute", "198.51.100.4", false, nil, 400},
		{"tampered IP", "198.51.100.4", true, func(r *http.Request) { r.Header.Set(protocol.CallerIPHeader, "198.51.100.5") }, 400},
		{"partial", "198.51.100.4", true, func(r *http.Request) { r.Header.Del(protocol.CallerTimeHeader) }, 400},
		{"duplicate", "198.51.100.4", true, func(r *http.Request) {
			r.Header.Add(protocol.CallerSignatureHeader, r.Header.Get(protocol.CallerSignatureHeader))
		}, 400},
		{"project changed", "198.51.100.4", true, func(r *http.Request) { r.Header.Set("X-Project-ID", defaultProjectID) }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runtimeRequest()
			r.RemoteAddr = "127.0.0.1:4321"
			if tc.bearer {
				r.Header.Set("Authorization", "Bearer "+p.Config.AdminToken)
			}
			if err := protocol.SignCallerContext(r, tc.ip, p.Config.AdminToken, time.Now()); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(r)
			}
			w := httptest.NewRecorder()
			Handler{p}.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal("caller attribution acceptance changed", w.Code, w.Body.String())
			}
			if tc.want == 200 {
				value, _ := decode(w.Body.Bytes())
				if value["client_ip"] != tc.ip {
					t.Fatal("MCP source became manager peer", value)
				}
			}
		})
	}
}

func TestEnvironmentRuntimeAppearsInAPIDiscovery(t *testing.T) {
	get := obj(obj(obj(openAPI()["paths"])["/api/environment-runtime"])["get"])
	if str(get["summary"]) == "" {
		t.Fatal("runtime context is missing from API discovery")
	}
}
