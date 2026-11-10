// Package nodes derives node availability exclusively from authenticated Core
// reports. It does not contact node endpoints or own transport connections.
package nodes

import (
	"context"
	"sort"
	"strings"
	"time"

	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
)

const ReportFreshFor = 20 * time.Second

type RegistrationStatus string

const (
	Unregistered RegistrationStatus = "unregistered"
	Registered   RegistrationStatus = "registered"
	Stale        RegistrationStatus = "stale"
)

type ReportSource string

const (
	NoReport   ReportSource = "none"
	CoreReport ReportSource = "core_report"
)

// ConfiguredNode is server-owned identity and fallback display metadata.
// Disabled worker endpoints remain visible, but reports cannot activate them.
type ConfiguredNode struct {
	ID            string
	DisplayName   string
	ReportEnabled bool
}

type Configuration struct {
	Controllers []ConfiguredNode
	Workers     []ConfiguredNode
}

// State separates immutable report facts from the manager's availability view.
// Report is nil for an unreported node. WorkerStatuses keeps each Core's own
// evidence separate from the globally selected worker observation.
type State struct {
	ID               string
	Role             string
	DisplayName      string
	Report           *protocol.NodeReport
	Healthy          bool
	Status           string
	Registration     RegistrationStatus
	Reason           string
	Source           ReportSource
	ReportedBy       string
	ReceivedAtMS     int64
	ObservationEpoch string
	WorkerStatuses   map[string]State
}

type Inventory struct {
	Controllers []State
	Workers     []State
}

// ReportStore is the complete registry dependency. Ordering, observation epochs
// and 24-hour report retention are provided atomically by the existing queue.
// The composition root owns the store's lifecycle.
type ReportStore interface {
	NodeReports(context.Context, []string) (map[string]queue.StoredNodeReport, error)
	StoreNodeReport(context.Context, protocol.CoreNodeReport, int64) (bool, error)
	NodeObservationEpoch(context.Context) (string, error)
}

type Registry struct{ Store ReportStore }

type Registration struct {
	Accepted         bool
	ObservationEpoch string
}

func (r Registry) Register(ctx context.Context, report protocol.CoreNodeReport, receivedAtMS int64) (Registration, error) {
	accepted, err := r.Store.StoreNodeReport(ctx, report, receivedAtMS)
	if err != nil {
		return Registration{}, err
	}
	epoch, err := r.Store.NodeObservationEpoch(ctx)
	if err != nil {
		return Registration{}, err
	}
	return Registration{Accepted: accepted, ObservationEpoch: epoch}, nil
}

func (r Registry) Inventory(ctx context.Context, configured Configuration, now func() time.Time) (Inventory, error) {
	ids := make([]string, 0, len(configured.Controllers))
	for _, node := range configured.Controllers {
		ids = append(ids, node.ID)
	}
	sort.Strings(ids)
	if len(ids) == 0 && len(configured.Workers) == 0 {
		return Inventory{Controllers: []State{}, Workers: []State{}}, nil
	}
	reports, err := r.Store.NodeReports(ctx, ids)
	if err != nil {
		return Inventory{}, err
	}
	return Project(configured, reports, now()), nil
}

// Project is a deterministic projection at the manager receipt clock. Report
// observed_at values are compared only to timestamps from the same Core clock.
func Project(configured Configuration, reports map[string]queue.StoredNodeReport, at time.Time) Inventory {
	controllers := sortedNodes(configured.Controllers)
	workers := sortedNodes(configured.Workers)
	out := Inventory{Controllers: make([]State, 0, len(controllers)), Workers: make([]State, 0, len(workers))}
	workerConfig := make(map[string]ConfiguredNode, len(workers))
	for _, configured := range workers {
		workerConfig[configured.ID] = configured
	}
	best := make(map[string]State, len(workers))
	for _, configured := range controllers {
		stored, exists := reports[configured.ID]
		report := stored.Report
		if !exists || stored.ReceivedAtMS <= 0 || report.ID != configured.ID || report.Role != "controller" || protocol.ValidateCoreNodeReport(report, time.UnixMilli(stored.ReceivedAtMS)) != nil {
			out.Controllers = append(out.Controllers, Missing(configured, "controller", "core_missing"))
			continue
		}
		core := registered(report.NodeReport, configured, configured.ID, stored.ReceivedAtMS, report.ObservationEpoch, at)
		core.WorkerStatuses = make(map[string]State)
		parentAt, _ := time.Parse(time.RFC3339Nano, report.ObservedAt)
		receiptAge := at.Sub(time.UnixMilli(stored.ReceivedAtMS))
		if receiptAge < 0 {
			receiptAge = 0
		}
		for _, worker := range report.Workers {
			configuredWorker, configured := workerConfig[worker.ID]
			if worker.Role != "worker" || !configured || !configuredWorker.ReportEnabled {
				continue
			}
			node := registered(worker, configuredWorker, report.ID, stored.ReceivedAtMS, report.ObservationEpoch, at)
			workerAt, _ := time.Parse(time.RFC3339Nano, worker.ObservedAt)
			sampleAge := parentAt.Sub(workerAt)
			if sampleAge < 0 {
				sampleAge = 0
			}
			if sampleAge+receiptAge > ReportFreshFor {
				node.Healthy, node.Status, node.Registration = false, "report_timeout", Stale
				node.Reason = "worker_observation_stale"
			}
			core.WorkerStatuses[worker.ID] = node
			current, exists := best[worker.ID]
			fresh, priorFresh := node.Registration == Registered, current.Registration == Registered
			if !exists || fresh && !priorFresh || fresh == priorFresh && node.ReceivedAtMS > current.ReceivedAtMS {
				best[worker.ID] = node
			}
		}
		out.Controllers = append(out.Controllers, core)
	}
	for _, configured := range workers {
		node, exists := best[configured.ID]
		if !exists {
			node = Missing(configured, "worker", "worker_missing")
		}
		out.Workers = append(out.Workers, node)
	}
	return out
}

func Missing(configured ConfiguredNode, role, reason string) State {
	name := configured.DisplayName
	if name == "" {
		name = configured.ID
	}
	return State{ID: configured.ID, Role: role, DisplayName: name, Status: "unreported", Registration: Unregistered, Reason: reason, Source: NoReport}
}

func registered(report protocol.NodeReport, configured ConfiguredNode, core string, receivedAt int64, observationEpoch string, at time.Time) State {
	name := strings.TrimSpace(report.DisplayName)
	if name == "" {
		name = configured.DisplayName
	}
	if name == "" {
		name = report.ID
	}
	node := State{ID: report.ID, Role: report.Role, DisplayName: name, Report: &report, Healthy: report.Healthy, Status: report.Status, Registration: Registered, Source: CoreReport, ReportedBy: core, ReceivedAtMS: receivedAt, ObservationEpoch: observationEpoch}
	if at.UnixMilli()-receivedAt > ReportFreshFor.Milliseconds() || receivedAt > at.Add(5*time.Second).UnixMilli() {
		node.Healthy, node.Status, node.Registration = false, "report_timeout", Stale
	} else if report.QueueConnected != nil && !*report.QueueConnected {
		node.Healthy, node.Status = false, "queue_disconnected"
	}
	return node
}

func sortedNodes(configured []ConfiguredNode) []ConfiguredNode {
	ordered := append([]ConfiguredNode(nil), configured...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	return ordered
}
