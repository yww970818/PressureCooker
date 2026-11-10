package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"pressurecooker/execution/protocol"
	"pressurecooker/manager/internal/nodes"
)

func (h Handler) nodeReportRoute(w http.ResponseWriter, r *http.Request) error {
	if r.URL.RawQuery != "" || r.URL.RawPath != "" {
		return api(400, "invalid_node_report_path", "Node reports cannot contain query or encoded paths")
	}
	if r.URL.Path != "/api/nodes/report" {
		return api(404, "not_found", "Node endpoint not found")
	}
	if r.Method != "POST" {
		return api(405, "method_not_allowed", "Method not allowed")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxCoreNodeReportBytes+1))
	if err != nil || len(raw) > protocol.MaxCoreNodeReportBytes {
		return api(413, "body_too_large", "Core node report exceeds 64KiB")
	}
	signer, err := protocol.VerifyRequest(r, raw, h.P.Config.AgentToken, time.Now())
	if err != nil {
		return api(401, "node_auth_required", "Invalid node authentication")
	}
	var report protocol.CoreNodeReport
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || decoder.Decode(new(any)) != io.EOF {
		return api(400, "invalid_node_report", "Invalid Core node report")
	}
	if signer != report.ID || report.Role != "controller" || h.P.Config.ControllerURLs[signer] == "" {
		return api(403, "node_identity_mismatch", "Only configured PressureCooker-Core identities may report nodes")
	}
	for _, worker := range report.Workers {
		if worker.Role != "worker" || h.P.Config.WorkerURLs[worker.ID] == "" {
			return api(403, "worker_identity_mismatch", "Reported worker is not configured")
		}
	}
	if protocol.ValidateCoreNodeReport(report, time.Now()) != nil {
		return api(400, "invalid_node_report", "Invalid Core node report")
	}
	// Return only server-owned inventory; reported metadata cannot assign execution
	// members, endpoints or permissions.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	group, err := h.P.findClusterQ(contextQueryer{ctx: ctx, db: h.P.DB}, clusterID(signer))
	if err != nil {
		return err
	}
	members := []string{}
	for _, id := range arr(group["worker_ids"]) {
		members = append(members, str(id))
	}
	workers := []protocol.Worker{}
	for _, id := range sortedKeys(h.P.Config.WorkerURLs) {
		workers = append(workers, protocol.Worker{ID: id, AgentURL: h.P.Config.WorkerURLs[id], RMIHost: id})
	}
	q, err := h.P.runnerQueue()
	if err != nil {
		return api(503, "node_registry_unavailable", "Node registry unavailable")
	}
	defer q.Close()
	registration, err := (nodes.Registry{Store: q}).Register(ctx, report, time.Now().UnixMilli())
	if err != nil {
		return api(503, "node_registry_unavailable", "Node registry unavailable")
	}
	sendJSON(w, r, 200, protocol.NodeRegistrationResponse{Accepted: registration.Accepted, Workers: workers, ClusterWorkerIDs: members, ServerTime: time.Now().UTC().Format(time.RFC3339Nano), ObservationEpoch: registration.ObservationEpoch})
	return nil
}

func (p *Platform) missingNode(id, role, reason string) Obj {
	return nodeAPI(nodes.Missing(nodes.ConfiguredNode{ID: id, DisplayName: p.nodeDisplayName(id, role)}, role, reason))
}
