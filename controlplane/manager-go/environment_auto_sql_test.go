package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

// Exercise persisted definitions and the authenticated HTTP boundary together.
// The only debug destination is this test's in-process synthetic responder.
func TestMySQLAutomaticEnvironmentRulesHTTPStoreMergeAndSource(t *testing.T) {
	p, _ := mysqlFixture(t)
	type observation struct {
		query  url.Values
		source string
	}
	observations := make(chan observation, 8)
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		for name := range r.Header {
			if callerContextHeader(name) {
				t.Error("private caller metadata reached the synthetic target", name)
			}
		}
		observations <- observation{r.URL.Query(), r.Header.Get("X-Source")}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	p.Config.proxyTestResolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.123.0.1")}}, nil
	}
	p.Config.proxyTestDial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}
	project, err := p.projectCreate(Obj{"name": "automatic sources"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := str(project["id"])
	request, _ := workspaceHTTP(t, p)
	call := func(method, path string, body Obj, extra http.Header) (int, http.Header, Obj) {
		t.Helper()
		headers := map[string]string{"Authorization": "Bearer " + p.Config.AdminToken, "X-Project-ID": projectID}
		for name, values := range extra {
			if len(values) != 1 {
				t.Fatal("fixture requires one header value", name)
			}
			headers[name] = values[0]
		}
		status, responseHeaders, raw := request(method, path, body, headers)
		value, err := decode(raw)
		if err != nil {
			t.Fatal("invalid JSON response", status, err)
		}
		return status, responseHeaders, value
	}

	// A real browser-style cookie request proves the peer address is obtained
	// from the HTTP connection and never from caller-supplied forwarding headers.
	sessionID, _, err := p.login(p.Config.AdminToken)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UnixMilli()
	status, responseHeaders, raw := request("GET", "/api/environment-runtime", nil, map[string]string{
		"Cookie": "jmeter_session=" + sessionID, "X-Project-ID": projectID,
		"X-Forwarded-For": "198.51.100.123", "Forwarded": "for=198.51.100.123",
	})
	runtime, err := decode(raw)
	ms, parseErr := strconv.ParseInt(str(runtime["timestamp_ms"]), 10, 64)
	if status != 200 || err != nil || runtime["client_ip"] != "127.0.0.1" || parseErr != nil || ms < before || ms > time.Now().UnixMilli() || responseHeaders.Get("Cache-Control") != "no-store" {
		t.Fatal("actual HTTP runtime context is incorrect", status, runtime, err)
	}

	ipRule, timeRule := "{{$auto.client_ip}}", "{{$auto.timestamp_ms}}"
	projectRows := []any{
		Obj{"name": "source_addr", "value": ipRule}, Obj{"name": "issued_at", "value": timeRule},
		Obj{"name": "selection", "value": "project"},
		Obj{"name": "client_ip", "value": "ordinary-fixed-name"},
		Obj{"name": "profile_update_time", "value": "ordinary-fixed-time"},
	}
	status, _, environment := call("POST", "/api/environments", Obj{"name": "automatic", "base_host": "http://auto-fixture.internal/base", "variables": projectRows}, nil)
	if status != 201 || string(encoded(environment["variables"])) != string(encoded(projectRows)) {
		t.Fatal("project declarations were expanded while saving", status, environment)
	}
	environmentID := str(environment["id"])
	parameters := []any{}
	for _, name := range []string{"source_addr", "collection_addr", "issued_at", "issued_copy", "local_clock", "selection", "client_ip", "profile_update_time"} {
		parameters = append(parameters, Obj{"in": "query", "name": name, "example": "{{" + name + "}}"})
	}
	parameters = append(parameters, Obj{"in": "header", "name": "X-Source", "example": "{{source_addr}}"})
	collectionRows := []any{
		Obj{"name": "collection_addr", "value": ipRule}, Obj{"name": "issued_copy", "value": timeRule},
		Obj{"name": "local_clock", "value": timeRule}, Obj{"name": "selection", "value": "collection"},
	}
	body := Obj{"name": "automatic sources", "document": Obj{"openapi": "3.1.0", "paths": Obj{"/capture": Obj{"get": Obj{"parameters": parameters, "responses": Obj{}}}}},
		"client_state": Obj{collectionOverridesKey: Obj{environmentID: Obj{"variables": collectionRows}}}}
	status, _, collection := call("POST", "/api/api-collections", body, nil)
	if status != 201 || string(encoded(obj(obj(collection["client_state"])[collectionOverridesKey])[environmentID])) != string(encoded(Obj{"variables": collectionRows})) || targetCalls.Load() != 0 {
		t.Fatal("collection declarations were expanded while saving", status, collection)
	}
	collectionID := str(collection["id"])
	debugPath := "/api/api-collections/" + collectionID + "/debug"
	debug := Obj{"target_source": "project_environment", "path": "/capture", "method": "GET", "environment_id": environmentID, "variables": Obj{"selection": "explicit"}}
	assertObservation := func(wantSource string, started int64) {
		t.Helper()
		select {
		case got := <-observations:
			stamp, e := strconv.ParseInt(got.query.Get("issued_at"), 10, 64)
			if got.source != wantSource || got.query.Get("source_addr") != wantSource || got.query.Get("collection_addr") != wantSource || got.query.Get("selection") != "explicit" || got.query.Get("client_ip") != "ordinary-fixed-name" || got.query.Get("profile_update_time") != "ordinary-fixed-time" || e != nil || stamp < started || stamp > time.Now().UnixMilli() || got.query.Get("issued_at") != got.query.Get("issued_copy") || got.query.Get("issued_at") != got.query.Get("local_clock") {
				t.Fatal("effective operation variables are incorrect", got, e)
			}
		default:
			t.Fatal("debug request did not reach the synthetic target")
		}
	}

	before = time.Now().UnixMilli()
	status, _, result := call("POST", debugPath, debug, http.Header{"X-Forwarded-For": []string{"198.51.100.123"}})
	if status != 200 || number(result["status"]) != 204 {
		t.Fatal("actual-peer debug failed", status, result)
	}
	assertObservation("127.0.0.1", before)

	// Model the exact internal metadata contract on a real HTTP manager hop.
	// Its signed caller differs from the loopback transport peer above.
	signedHeaders := func(ip string) http.Header {
		t.Helper()
		r := httptest.NewRequest("POST", debugPath, nil)
		r.Header.Set("X-Project-ID", projectID)
		if err := protocol.SignCallerContext(r, ip, p.Config.AdminToken, time.Now()); err != nil {
			t.Fatal(err)
		}
		return r.Header
	}
	before = time.Now().UnixMilli()
	status, _, result = call("POST", debugPath, debug, signedHeaders("2001:db8::42"))
	if status != 200 || number(result["status"]) != 204 {
		t.Fatal("signed-source debug failed", status, result)
	}
	assertObservation("2001:db8::42", before)
	status, _, result = call("POST", debugPath, debug, signedHeaders(""))
	if status != 400 || obj(result["error"])["code"] != "environment_client_ip_unavailable" || targetCalls.Load() != 2 {
		t.Fatal("missing source became loopback or sent a request", status, result, targetCalls.Load())
	}
	// Fixed winning overrides eliminate both IP rules. Timestamp-only automatic
	// values remain usable even when the signed caller source is unavailable.
	timestampOnly := clone(debug)
	timestampOnly["variables"] = Obj{"selection": "explicit", "source_addr": "fixed-value", "collection_addr": "fixed-value"}
	before = time.Now().UnixMilli()
	status, _, result = call("POST", debugPath, timestampOnly, signedHeaders(""))
	if status != 200 || number(result["status"]) != 204 {
		t.Fatal("timestamp-only operation unnecessarily required IP", status, result)
	}
	assertObservation("fixed-value", before)

	for _, tc := range []struct{ table, id, path string }{{"environments", environmentID, "/api/environments/" + environmentID}, {"api_collections", collectionID, "/api/api-collections/" + collectionID}} {
		var ciphertext string
		if err := p.DB.QueryRow("SELECT payload FROM "+tc.table+" WHERE id=?", tc.id).Scan(&ciphertext); err != nil || !strings.HasPrefix(ciphertext, "gcm1:") || strings.Contains(ciphertext, "$auto") {
			t.Fatal("automatic declarations were not encrypted at rest", tc.table, err)
		}
		status, _, saved := call("GET", tc.path, nil, nil)
		if status != 200 || number(saved["revision"]) != 1 {
			t.Fatal("debug mutated persisted environment configuration", tc.table, status)
		}
		if tc.table == "environments" && string(encoded(saved["variables"])) != string(encoded(projectRows)) {
			t.Fatal("debug wrote generated values into project definitions")
		}
		if tc.table == "api_collections" && string(encoded(obj(obj(saved["client_state"])[collectionOverridesKey])[environmentID])) != string(encoded(Obj{"variables": collectionRows})) {
			t.Fatal("debug wrote generated values into collection definitions")
		}
	}
}
