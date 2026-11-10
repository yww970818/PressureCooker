package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

// Catches a typed adapter inventing absent report metadata or converting the
// legacy explicit null values into omitted keys or zero-valued report fields.
func TestNodeInventoryMissingLegacyJSONShape(t *testing.T) {
	p := &Platform{Config: Config{ControllerURLs: map[string]string{"controller-a": "http://unused"}, WorkerURLs: map[string]string{"worker-1": "http://unused"}}}
	nodeRedisFixture(t, p)
	cores, workers, err := p.nodeInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected := Obj{"id": "worker-1", "role": "worker", "display_name": "worker-1", "healthy": false, "status": "unreported", "generation": nil, "job": nil, "reset_operation": nil, "machine": nil, "registration_status": "unregistered", "registration_reason": "worker_missing", "status_source": "none", "reported_by": nil, "last_reported_at": nil, "observed_at": nil, "queue_connected": nil}
	if !reflect.DeepEqual(obj(workers[0]), expected) {
		t.Fatalf("missing node API shape: %s", encoded(workers[0]))
	}
	core := obj(cores[0])
	if core["display_name"] != "PressureCooker-Core-1" || core["registration_reason"] != "core_missing" {
		t.Fatalf("missing Core API: %s", encoded(core))
	}
}

// Catches number representation drift and extra/omitted keys when typed report
// facts are overlaid with manager-owned registration metadata.
func TestNodeInventoryRegisteredLegacyJSONShape(t *testing.T) {
	p := &Platform{Config: Config{ControllerURLs: map[string]string{"controller-a": "http://unused"}, WorkerURLs: map[string]string{"worker-1": "http://unused"}}}
	nodeRedisFixture(t, p)
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	worker := nodeReportFixture("worker-1", "worker")
	worker.DisplayName = "  "
	worker.QueueConnected = nil
	worker.Job = &protocol.NodeJob{ID: "run-1", Kind: "run", State: "RUNNING"}
	report := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller"), Workers: []protocol.NodeReport{worker}, ObservationEpoch: protocol.NodeObservationEpoch("contract")}
	received := time.Now().UnixMilli()
	if accepted, err := q.StoreNodeReport(context.Background(), report, received); err != nil || !accepted {
		t.Fatal(accepted, err)
	}
	cores, workers, err := p.nodeInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	actual := obj(workers[0])
	if len(actual) != 21 {
		t.Fatalf("registered keys (%d): %s", len(actual), encoded(actual))
	}
	for _, key := range []string{"registration_reason", "_worker_statuses"} {
		if _, exists := actual[key]; exists {
			t.Fatalf("unexpected %s: %s", key, encoded(actual))
		}
	}
	for _, key := range []string{"queue_connected", "reset_operation"} {
		if v, exists := actual[key]; !exists || v != nil {
			t.Fatalf("expected explicit null %s: %s", key, encoded(actual))
		}
	}
	if actual["generation"] != json.Number("1") || actual["version"] != json.Number("1") || actual["display_name"] != "worker-1" || actual["registration_status"] != "registered" || actual["execution_status"] != "idle" || actual["status_source"] != "core_report" || actual["reported_by"] != "controller-a" || actual["observation_epoch"] != report.ObservationEpoch || actual["last_reported_at"] != time.UnixMilli(received).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("registered metadata: %s", encoded(actual))
	}
	if obj(actual["job"])["id"] != "run-1" || obj(actual["machine"])["memory_used_bytes"] != nil {
		t.Fatalf("nested report facts: %s", encoded(actual))
	}
	own := obj(obj(cores[0])["_worker_statuses"])
	if !reflect.DeepEqual(obj(own["worker-1"]), actual) {
		t.Fatalf("Core worker evidence differs: %s", encoded(cores[0]))
	}
}
