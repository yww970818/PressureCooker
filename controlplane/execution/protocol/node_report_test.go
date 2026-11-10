package protocol

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func validNodeReport(now time.Time) NodeReport {
	connected := true
	return NodeReport{Version: 1, ID: "controller-a", Role: "controller", DisplayName: "PressureCooker-Core-1", BootID: strings.Repeat("a", 32), BootStartedAtMS: now.Add(-time.Second).UnixMilli(), Sequence: 1, ObservedAt: now.UTC().Format(time.RFC3339Nano), QueueConnected: &connected, Status: "idle", Healthy: true, Machine: NodeMachine{ResourceScope: "unknown"}}
}
func TestNodeReportValidationAndUnknownMachineValues(t *testing.T) {
	now := time.Now()
	r := validNodeReport(now)
	if err := ValidateNodeReport(r, now); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	machine := decoded["machine"].(map[string]any)
	for _, name := range []string{"cpu_limit_cores", "cpu_usage_percent", "memory_limit_bytes", "memory_used_bytes", "disk_total_bytes"} {
		if v, ok := machine[name]; !ok || v != nil {
			t.Fatal("unknown machine field not explicit null", name, v)
		}
	}
	for name, mutate := range map[string]func(*NodeReport){
		"version": func(r *NodeReport) { r.Version = 2 }, "identity": func(r *NodeReport) { r.ID = "../node" }, "role": func(r *NodeReport) { r.Role = "fixture" }, "boot": func(r *NodeReport) { r.BootID = "bad" }, "future-boot": func(r *NodeReport) { r.BootStartedAtMS = now.Add(121 * time.Second).UnixMilli() }, "sequence": func(r *NodeReport) { r.Sequence = 0 }, "unsafe-sequence": func(r *NodeReport) { r.Sequence = 9007199254740992 }, "old-observation": func(r *NodeReport) { r.ObservedAt = now.Add(-121 * time.Second).Format(time.RFC3339Nano) }, "status": func(r *NodeReport) { r.Status = "registered" }, "core-queue-unknown": func(r *NodeReport) { r.QueueConnected = nil }, "generation": func(r *NodeReport) { r.Generation = -1 }, "job": func(r *NodeReport) { r.Job = &NodeJob{ID: "../run", Kind: "run", State: "RUNNING"} }, "negative-memory": func(r *NodeReport) { n := int64(-1); r.Machine.MemoryUsedBytes = &n }, "nonfinite-cpu": func(r *NodeReport) { n := math.Inf(1); r.Machine.CPULimitCores = &n },
	} {
		t.Run(name, func(t *testing.T) {
			r := validNodeReport(now)
			mutate(&r)
			if ValidateNodeReport(r, now) == nil {
				t.Fatal("invalid node report accepted")
			}
		})
	}
}
func TestCoreNodeReportRequiresFreshWorkerEpochAndBoundedInventory(t *testing.T) {
	now := time.Now()
	core := CoreNodeReport{NodeReport: validNodeReport(now), ObservationEpoch: NodeObservationEpoch("lease-private")}
	worker := validNodeReport(now)
	worker.ID = "worker-1"
	worker.Role = "worker"
	worker.DisplayName = "worker-1"
	worker.BootID = ""
	worker.BootStartedAtMS = 0
	worker.QueueConnected = nil
	core.Workers = []NodeReport{worker}
	if err := ValidateCoreNodeReport(core, now); err != nil {
		t.Fatal("legacy worker observation rejected", err)
	}
	core.ObservationEpoch = ""
	if ValidateCoreNodeReport(core, now) == nil {
		t.Fatal("worker observation without manager epoch accepted")
	}
	core.Workers = nil
	if err := ValidateCoreNodeReport(core, now); err != nil {
		t.Fatal("initial core-only registration rejected", err)
	}
	core.ObservationEpoch = NodeObservationEpoch("")
	core.Workers = []NodeReport{worker, worker}
	if ValidateCoreNodeReport(core, now) == nil {
		t.Fatal("duplicate worker accepted")
	}
	core.Workers = []NodeReport{worker}
	core.Workers[0].Status = "unreachable"
	core.Workers[0].Healthy = false
	if err := ValidateCoreNodeReport(core, now); err != nil {
		t.Fatal("honest unreachable probe rejected", err)
	}
	if strings.Contains(core.ObservationEpoch, "lease-private") || len(core.ObservationEpoch) != 64 {
		t.Fatal("raw lease exposed")
	}
}
