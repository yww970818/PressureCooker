package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func TestNodeRegistrationConfigDefaultsAndBoundedURLs(t *testing.T) {
	for _, tc := range []struct{ role, id, want string }{{"controller", "controller-a", "PressureCooker-Core-1"}, {"controller", "controller-b", "PressureCooker-Core-2"}, {"worker", "worker-4", "worker-4"}} {
		c := config{role: tc.role, id: tc.id}
		if err := c.nodeSettings(); err != nil || c.nodeName != tc.want || c.managerURL != "http://manager:8080" {
			t.Fatalf("unexpected node defaults: %+v %v", c, err)
		}
	}
	for _, raw := range []string{"ftp://manager", "http://user:secret@manager", "http://manager/?token=hidden", "http://manager/#fragment", "http://manager/%61", "http://manager/../hidden"} {
		t.Setenv("PRESSURECOOKER_MANAGER_URL", raw)
		c := config{id: "controller-a", role: "controller"}
		if c.nodeSettings() == nil {
			t.Fatal("invalid manager endpoint accepted", raw)
		}
	}
	t.Setenv("PRESSURECOOKER_MANAGER_URL", "https://manager.example/platform/")
	t.Setenv("PRESSURECOOKER_NODE_NAME", "本机 Core")
	c := config{id: "controller-a", role: "controller"}
	if err := c.nodeSettings(); err != nil || c.managerURL != "https://manager.example/platform" || c.nodeName != "本机 Core" {
		t.Fatal("explicit node settings lost", c, err)
	}
	t.Setenv("PRESSURECOOKER_NODE_NAME", strings.Repeat("名", 201))
	if c.nodeSettings() == nil {
		t.Fatal("unbounded display name accepted")
	}
}

func TestWorkerStatusAddsReadOnlyMetadataWithoutChangingEngineHealth(t *testing.T) {
	s := &service{c: config{id: "worker-1", role: "worker", token: "fixture-agent-token", nodeName: "worker-1"}, boot: strings.Repeat("b", 32), bootStartedAtMS: time.Now().Add(-time.Second).UnixMilli(), machine: machineFixture(t, nil)}
	s.worker = &worker{s: s, rec: engineRecord{Generation: 7}}
	for i := int64(1); i <= 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/status", nil)
		protocol.SignRequest(r, nil, "controller-a", s.c.token, time.Now())
		w := httptest.NewRecorder()
		s.handler(w, r)
		var report protocol.NodeReport
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &report) != nil || protocol.ValidateNodeReport(report, time.Now()) != nil {
			t.Fatalf("invalid enriched status: %d %s", w.Code, w.Body.String())
		}
		if report.Sequence != i || report.BootID != s.boot || report.BootStartedAtMS != s.bootStartedAtMS || report.QueueConnected != nil || report.Healthy || report.Status != "recovering" || report.Generation != 7 {
			t.Fatal("status invented engine/queue readiness or changed its identity", report)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.handler(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ready":false`) {
		t.Fatal("health check semantics changed", w.Body.String())
	}
}

func TestWorkerNeverRunsManagerReporter(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	s := &service{c: config{role: "worker", managerURL: server.URL}}
	s.reportNodes(t.Context())
	if called {
		t.Fatal("worker attempted direct manager registration")
	}
}
