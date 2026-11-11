package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

type reportingQueue struct {
	connected  atomic.Bool
	groupReady atomic.Bool
}

func (q *reportingQueue) Ping(context.Context) error {
	if !q.connected.Load() {
		return errors.New("unavailable")
	}
	return nil
}
func (q *reportingQueue) EnsureGroup(context.Context, string) error {
	if !q.groupReady.Load() {
		return errors.New("group unavailable")
	}
	return nil
}
func reporterFixture(t *testing.T, endpoint string, q *reportingQueue) *nodeReporter {
	t.Helper()
	started := time.Now().Add(-time.Second).UnixMilli()
	return &nodeReporter{queue: q, endpoint: endpoint, token: "fixture-agent-token", cluster: "cluster-a", id: "controller-a", interval: 20 * time.Millisecond, requestTimeout: 100 * time.Millisecond, probeTimeout: 100 * time.Millisecond, snapshot: func(now time.Time, seq int64, connected bool) protocol.NodeReport {
		return protocol.NodeReport{Version: 1, ID: "controller-a", Role: "controller", DisplayName: "PressureCooker-Core-1", BootID: strings.Repeat("a", 32), BootStartedAtMS: started, Sequence: seq, ObservedAt: now.Format(time.RFC3339Nano), QueueConnected: &connected, Status: "idle", Healthy: true, Machine: protocol.NodeMachine{ResourceScope: "unknown"}}
	}}
}
func waitNodeCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("node reporter condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestCoreReporterWaitsForQueueAndRetriesHTTPWithoutInventingRegistration(t *testing.T) {
	q := &reportingQueue{}
	var calls atomic.Int32
	var mu sync.Mutex
	reports := []protocol.CoreNodeReport{}
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		id, err := protocol.VerifyRequest(r, raw, "fixture-agent-token", time.Now())
		if err != nil || id != "controller-a" || r.URL.Path != "/api/nodes/report" {
			t.Error("unsigned or misrouted registration")
		}
		var report protocol.CoreNodeReport
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Error(err)
		}
		mu.Lock()
		reports = append(reports, report)
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(protocol.NodeRegistrationResponse{Accepted: true, Workers: []protocol.Worker{}, ClusterWorkerIDs: []string{}, ServerTime: timestamp(), ObservationEpoch: protocol.NodeObservationEpoch("")})
	}))
	defer manager.Close()
	r := reporterFixture(t, manager.URL+"/api/nodes/report", q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.run(ctx) }()
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("registration sent before Redis ready")
	}
	q.connected.Store(true)
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("registration sent before consumer group ready")
	}
	q.groupReady.Store(true)
	waitNodeCondition(t, func() bool { return calls.Load() >= 2 })
	q.connected.Store(false)
	waitNodeCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, report := range reports {
			if report.QueueConnected != nil && !*report.QueueConnected {
				return true
			}
		}
		return false
	})
	q.connected.Store(true)
	waitNodeCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reports) > 2 && reports[len(reports)-1].QueueConnected != nil && *reports[len(reports)-1].QueueConnected
	})
	mu.Lock()
	defer mu.Unlock()
	for i, report := range reports {
		if report.Role != "controller" || !report.Healthy || i > 0 && report.Sequence <= reports[i-1].Sequence {
			t.Fatal("status semantics or monotonic sequence changed")
		}
	}
}
func TestCoreReporterEpochChangeNeverRelabelsOlderWorkerObservations(t *testing.T) {
	token := "fixture-agent-token"
	epochA, epochB := protocol.NodeObservationEpoch("first-lease"), protocol.NodeObservationEpoch("next-lease")
	var probes atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, err := protocol.VerifyRequest(r, nil, token, time.Now()); err != nil || id != "controller-a" || r.URL.Path != "/status" {
			t.Error("worker probe was not authenticated")
		}
		n := probes.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"version": 1, "id": "worker-1", "role": "worker", "healthy": true, "status": "idle", "generation": n, "job": nil, "reset_operation": nil})
	}))
	defer worker.Close()
	var mu sync.Mutex
	reports := []protocol.CoreNodeReport{}
	changed := false
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report protocol.CoreNodeReport
		json.NewDecoder(r.Body).Decode(&report)
		mu.Lock()
		reports = append(reports, report)
		epoch := epochA
		if len(report.Workers) > 0 {
			changed = true
		}
		if changed {
			epoch = epochB
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(protocol.NodeRegistrationResponse{Accepted: true, Workers: []protocol.Worker{{ID: "worker-1", AgentURL: worker.URL}}, ClusterWorkerIDs: []string{"worker-1"}, ServerTime: timestamp(), ObservationEpoch: epoch})
	}))
	defer manager.Close()
	q := &reportingQueue{}
	q.connected.Store(true)
	q.groupReady.Store(true)
	r := reporterFixture(t, manager.URL+"/api/nodes/report", q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.run(ctx) }()
	defer func() { cancel(); <-done }()
	waitNodeCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, report := range reports {
			if report.ObservationEpoch == epochB && len(report.Workers) == 1 {
				return true
			}
		}
		return false
	})
	mu.Lock()
	defer mu.Unlock()
	var a, b int64
	for _, report := range reports {
		if len(report.Workers) == 0 {
			continue
		}
		if report.ObservationEpoch == epochA && a == 0 {
			a = report.Workers[0].Generation
		}
		if report.ObservationEpoch == epochB && b == 0 {
			b = report.Workers[0].Generation
		}
		if report.Workers[0].QueueConnected != nil {
			t.Fatal("worker's unknown MQ status became false")
		}
	}
	if len(reports[0].Workers) != 0 || reports[0].ObservationEpoch != "" || a < 1 || b <= a {
		t.Fatal("old worker result reused after ACK epoch changed", a, b)
	}
}
func TestCoreReporterRejectsWorkerIdentityRedirectAndUnboundedResponses(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wrong/status":
			io.WriteString(w, `{"id":"worker-other","role":"worker","healthy":true,"status":"idle","generation":1,"job":null}`)
		case "/redirect/status":
			http.Redirect(w, r, destination.URL, 302)
		case "/large/status":
			io.WriteString(w, strings.Repeat("x", protocol.MaxNodeReportBytes+1))
		case "/wait/status":
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	r := reporterFixture(t, "http://unused", &reportingQueue{})
	for _, path := range []string{"/wrong", "/redirect", "/large", "/wait"} {
		worker := r.probe(context.Background(), protocol.Worker{ID: "worker-1", AgentURL: server.URL + path}, 1)
		if worker.Status != "unreachable" || worker.Healthy || worker.QueueConnected != nil || worker.Machine.MemoryUsedBytes != nil {
			t.Fatal("untrusted/failed probe became healthy", path, worker)
		}
	}
	if leaked.Load() != 0 {
		t.Fatal("worker redirect leaked agent credentials")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	r.run(ctx)
	if time.Since(started) > time.Second {
		t.Fatal("canceled reporter did not stop")
	}
}

func TestCoreReporterBoundsManagerTimeoutAndStopsInFlightOnCancellation(t *testing.T) {
	entered := make(chan struct{}, 2)
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer manager.Close()
	r := reporterFixture(t, manager.URL+"/api/nodes/report", &reportingQueue{})
	r.requestTimeout = 30 * time.Millisecond
	report := protocol.CoreNodeReport{NodeReport: r.snapshot(time.Now(), 1, true), Workers: []protocol.NodeReport{}}
	started := time.Now()
	if _, err := r.post(context.Background(), report); err == nil || time.Since(started) > 500*time.Millisecond {
		t.Fatal("hung manager did not respect registration request budget", err)
	}
	<-entered
	r.requestTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := r.post(ctx, report); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled registration reported success")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancellation did not stop in-flight registration")
	}
}

func TestCoreReporterEpochChurnHasBoundedImmediateRetries(t *testing.T) {
	var calls atomic.Int32
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		json.NewEncoder(w).Encode(protocol.NodeRegistrationResponse{Accepted: true, Workers: []protocol.Worker{}, ClusterWorkerIDs: []string{}, ServerTime: timestamp(), ObservationEpoch: protocol.NodeObservationEpoch(string(rune(n)))})
	}))
	defer manager.Close()
	q := &reportingQueue{}
	q.connected.Store(true)
	q.groupReady.Store(true)
	r := reporterFixture(t, manager.URL+"/api/nodes/report", q)
	r.interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.run(ctx) }()
	defer func() { cancel(); <-done }()
	waitNodeCondition(t, func() bool { return calls.Load() >= 3 })
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 3 {
		t.Fatal("epoch churn caused unbounded immediate retry loop", calls.Load())
	}
}
