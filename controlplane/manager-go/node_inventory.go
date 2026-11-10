package main

import (
	"context"
	"time"

	"pressurecooker/manager/internal/nodes"
)

// Node inventory is exclusively a projection of authenticated Core reports.
// The registry has no endpoint transport; this boundary only adapts typed state
// to the existing manager API and private cluster projection.
func (p *Platform) nodeInventory(ctx context.Context) ([]any, []any, error) {
	configured := p.nodeRegistryConfiguration()
	if len(configured.Controllers) == 0 && len(configured.Workers) == 0 {
		return []any{}, []any{}, nil
	}
	q, err := p.runnerQueue()
	if err != nil {
		return nil, nil, err
	}
	defer q.Close()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	inventory, err := (nodes.Registry{Store: q}).Inventory(ctx, configured, time.Now)
	if err != nil {
		return nil, nil, err
	}
	cores, workers := make([]any, 0, len(inventory.Controllers)), make([]any, 0, len(inventory.Workers))
	for _, node := range inventory.Controllers {
		cores = append(cores, nodeAPI(node))
	}
	for _, node := range inventory.Workers {
		workers = append(workers, nodeAPI(node))
	}
	return cores, workers, nil
}

func (p *Platform) nodeRegistryConfiguration() nodes.Configuration {
	configured := nodes.Configuration{Controllers: make([]nodes.ConfiguredNode, 0, len(p.Config.ControllerURLs)), Workers: make([]nodes.ConfiguredNode, 0, len(p.Config.WorkerURLs))}
	for id, endpoint := range p.Config.ControllerURLs {
		configured.Controllers = append(configured.Controllers, nodes.ConfiguredNode{ID: id, DisplayName: p.nodeDisplayName(id, "controller"), ReportEnabled: endpoint != ""})
	}
	for id, endpoint := range p.Config.WorkerURLs {
		configured.Workers = append(configured.Workers, nodes.ConfiguredNode{ID: id, DisplayName: p.nodeDisplayName(id, "worker"), ReportEnabled: endpoint != ""})
	}
	return configured
}

// Missing fields are intentionally explicit nulls. Registered reports preserve
// the original JSON number representation and absent registration_reason key.
func nodeAPI(node nodes.State) Obj {
	if node.Report == nil {
		return Obj{"id": node.ID, "role": node.Role, "display_name": node.DisplayName, "healthy": false, "status": "unreported", "generation": nil, "job": nil, "reset_operation": nil, "machine": nil, "registration_status": string(node.Registration), "registration_reason": node.Reason, "status_source": string(node.Source), "reported_by": nil, "last_reported_at": nil, "observed_at": nil, "queue_connected": nil}
	}
	out, _ := decode(encoded(*node.Report))
	out["display_name"], out["healthy"], out["status"] = node.DisplayName, node.Healthy, node.Status
	out["reported_by"], out["status_source"], out["registration_status"] = node.ReportedBy, string(node.Source), string(node.Registration)
	out["last_reported_at"] = time.UnixMilli(node.ReceivedAtMS).UTC().Format(time.RFC3339Nano)
	out["execution_status"], out["observation_epoch"] = node.Report.Status, node.ObservationEpoch
	if node.Reason != "" {
		out["registration_reason"] = node.Reason
	}
	if node.WorkerStatuses != nil {
		ownWorkers := Obj{}
		for id, worker := range node.WorkerStatuses {
			ownWorkers[id] = nodeAPI(worker)
		}
		out["_worker_statuses"] = ownWorkers
	}
	return out
}
