package protocol

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignaturesBindAttemptRouteRunnerAndClock(t *testing.T) {
	key := "0123456789abcdef"
	task := Task{Version: 1, TaskID: "run", Properties: map[string]string{"z": "secret", "a": "value"}}
	s, e := SignTask(task, key)
	if e != nil || VerifyTask(s, key) != nil {
		t.Fatal("valid task failed")
	}
	s.Task.AttemptID = "other"
	if VerifyTask(s, key) == nil {
		t.Fatal("changed attempt accepted")
	}
	b := []byte(`{"task_id":"run"}`)
	now := time.Now()
	req, _ := http.NewRequest("POST", "http://console/api/runner/callback", strings.NewReader(string(b)))
	SignRequest(req, b, "controller-a", key, now)
	if _, e = VerifyRequest(req, b, key, now); e != nil {
		t.Fatal(e)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("raw authorization")
	}
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Method = "GET" }, func(r *http.Request) { r.URL.Path = "/other" }, func(r *http.Request) { r.Header.Set("X-JMeter-Runner", "controller-b") }} {
		r := req.Clone(req.Context())
		change(r)
		if _, e = VerifyRequest(r, b, key, now); e == nil {
			t.Fatal("changed request accepted")
		}
	}
	if _, e = VerifyRequest(req, b, key, now.Add(6*time.Minute)); e == nil {
		t.Fatal("stale timestamp accepted")
	}
}
func TestCallbackURLBoundaries(t *testing.T) {
	for _, s := range []string{"https://user:x@host", "ftp://host", "https://host/path", "http://host/api/runner/callback?token=x", "http://host/#x", "http://host/%61pi/runner/callback"} {
		if _, e := NormalizeCallbackURL(s); e == nil {
			t.Fatal("invalid callback accepted")
		}
	}
	for _, s := range []string{"http://manager:8080", "http://manager:8080/", "http://manager:8080" + CallbackPath} {
		u, e := NormalizeCallbackURL(s)
		if e != nil || u != "http://manager:8080"+CallbackPath {
			t.Fatal("canonical URL mismatch")
		}
	}
}

func TestReservedPropertiesHaveExactOrNamespaceBoundaries(t *testing.T) {
	for _, k := range []string{"model", "model_id", "modeSelector", "server_portal", "engine_id_label"} {
		if RuntimePropertyReserved(k) {
			t.Fatalf("custom key rejected: %s", k)
		}
	}
	for _, k := range []string{"MODE", "server_port", "system.properties", "client.rmi.localport", "jmeterengine.remote.system.exit", "javax.net.ssl.keyStore"} {
		if !RuntimePropertyReserved(k) {
			t.Fatalf("runtime key allowed: %s", k)
		}
	}
}
