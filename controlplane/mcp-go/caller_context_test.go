package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"pressurecooker/execution/protocol"
)

func callerManagerFixture(t *testing.T, wantIP string) managerClient {
	t.Helper()
	const token = "caller-test-admin-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, err := protocol.VerifyCallerContext(r, token, time.Now())
		if err != nil || ip != wantIP || r.Header.Get("X-Project-ID") != "project-one" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("caller attribution: got=%q want=%q err=%v manager-peer=%q", ip, wantIP, err, r.RemoteAddr)
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" {
			t.Error("untrusted forwarding metadata reached manager")
		}
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	return managerClient{baseURL: server.URL, tokenFile: file, client: server.Client()}
}

func TestSecureHandlerCallerSourceSignsActualPeerAndIgnoresSpoofedHeaders(t *testing.T) {
	for _, tc := range []struct{ remote, ip string }{{"203.0.113.7:4545", "203.0.113.7"}, {"[2001:db8::4]:4545", "2001:db8::4"}, {"[::ffff:192.0.2.8]:4545", "192.0.2.8"}, {"unavailable", ""}} {
		t.Run(tc.remote, func(t *testing.T) {
			api := callerManagerFixture(t, tc.ip)
			h := secureHandler("incoming-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := api.requestProject(r.Context(), "PUT", "/api/environments/one?probe=1", map[string]any{"value": "{{$auto.client_ip}}"}, "", "project-one"); err != nil {
					t.Error(err)
				}
				if _, err := api.uploadDataset(r.Context(), "project-one", "workflow-one", "node-one", "fixture", "id\n1\n"); err != nil {
					t.Error(err)
				}
			}))
			r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
			r.RemoteAddr = tc.remote
			r.Header.Set("Authorization", "Bearer incoming-token")
			r.Header.Set("X-Forwarded-For", "198.51.100.9")
			r.Header.Set("Forwarded", "for=198.51.100.9")
			r.Header.Set(protocol.CallerIPHeader, "198.51.100.9")
			r.Header.Set(protocol.CallerTimeHeader, "1")
			r.Header.Set(protocol.CallerSignatureHeader, "forged")
			h.ServeHTTP(httptest.NewRecorder(), r)
		})
	}
}

func TestManagerRequestsWithoutHTTPSourceSignExplicitUnavailableIP(t *testing.T) {
	api := callerManagerFixture(t, "")
	if _, err := api.requestProject(context.Background(), "GET", "/api/environments", nil, "", "project-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.uploadDataset(context.Background(), "project-one", "workflow-one", "node-one", "fixture", "id\n1\n"); err != nil {
		t.Fatal(err)
	}
}

type callerHeaderTransport struct{ base http.RoundTripper }

func (t callerHeaderTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer incoming-token")
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	r.Header.Set(protocol.CallerIPHeader, "198.51.100.9")
	return t.base.RoundTrip(r)
}

func TestCallerSourceSurvivesRealStatelessSDKHTTPContext(t *testing.T) {
	api := callerManagerFixture(t, "2001:db8::abcd")
	server := mcp.NewServer(&mcp.Implementation{Name: "caller-fixture", Version: "1"}, nil)
	managerTools{api: api}.register(server)
	sdk := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	secured := secureHandler("incoming-token", sdk)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A test-only observed peer differs from the manager's actual loopback peer.
		r.RemoteAddr = "[2001:db8::abcd]:4545"
		secured.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "caller-client", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Transport: callerHeaderTransport{http.DefaultTransport}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "get_cluster_status", Arguments: map[string]any{"projectId": "project-one"}})
	if err != nil || result.IsError {
		t.Fatalf("stateless SDK lost source context: result=%+v err=%v", result, err)
	}
}

func TestAutomaticEnvironmentRulesReachBothManagerWritersUnchanged(t *testing.T) {
	variables := []any{map[string]any{"name": "source_addr", "value": "{{$auto.client_ip}}"}, map[string]any{"name": "issued_at", "value": "{{$auto.timestamp_ms}}"}}
	var calls int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		want, _ := json.Marshal(variables)
		got, _ := json.Marshal(payload["variables"])
		if string(got) != string(want) {
			t.Error("automatic rule definitions were expanded or renamed")
		}
		io.WriteString(w, `{"revision":2}`)
	}))
	defer peer.Close()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("manager-token"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := managerTools{api: managerClient{baseURL: peer.URL, tokenFile: file}}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"create_project_environment", map[string]any{"projectId": "project-one", "name": "fixture", "base_host": "https://example.invalid", "variables": variables}},
		{"set_api_environments", map[string]any{"projectId": "project-one", "collectionId": "api-one", "revision": 1, "environmentId": "environment-cccccccccccccccccccccccccccccccc", "variables": variables}},
	} {
		if _, err := tools.call(context.Background(), tc.name, tc.args); err != nil {
			t.Fatal(tc.name, err)
		}
	}
	if calls != 2 {
		t.Fatal("writer requests missing")
	}
}

func TestCancelledCallerRequestDoesNotStartManagerIO(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer peer.Close()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("manager-token"), 0600); err != nil {
		t.Fatal(err)
	}
	api := managerClient{baseURL: peer.URL, tokenFile: file}
	h := secureHandler("incoming-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := api.requestProject(r.Context(), "GET", "/api/environments", nil, "", "project-one"); err == nil {
			t.Error("cancelled request succeeded")
		}
	}))
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
	r.RemoteAddr = "203.0.113.7:4545"
	r.Header.Set("Authorization", "Bearer incoming-token")
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	h.ServeHTTP(httptest.NewRecorder(), r.WithContext(ctx))
	if calls.Load() != 0 {
		t.Fatal("cancelled caller started manager request")
	}
}
