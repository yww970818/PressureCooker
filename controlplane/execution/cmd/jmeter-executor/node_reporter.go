package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"pressurecooker/execution/protocol"
)

type nodeReportQueue interface {
	Ping(context.Context) error
	EnsureGroup(context.Context, string) error
}
type nodeReporter struct {
	queue                                  nodeReportQueue
	endpoint, token, cluster, id           string
	interval, requestTimeout, probeTimeout time.Duration
	snapshot                               func(time.Time, int64, bool) protocol.NodeReport
	client                                 *http.Client
}

func (r *nodeReporter) httpClient() *http.Client {
	var client http.Client
	if r.client != nil {
		client = *r.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
func (r *nodeReporter) run(ctx context.Context) {
	interval := r.interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	connectedOnce := false
	var sequence int64
	epoch := ""
	workers := []protocol.Worker{}
	cycle := func() {
		if ctx.Err() != nil || r.queue == nil {
			return
		}
		check, end := context.WithTimeout(ctx, time.Second)
		err := r.queue.Ping(check)
		end()
		if err == nil {
			check, end = context.WithTimeout(ctx, time.Second)
			err = r.queue.EnsureGroup(check, r.cluster)
			end()
		}
		connected := err == nil
		if !connectedOnce && !connected {
			return
		}
		connectedOnce = connectedOnce || connected
		// At most two immediate refreshes follow an ACK/config change. If ownership
		// keeps changing, the next regular tick starts with fresh probes again.
		for attempt := 0; attempt < 3 && ctx.Err() == nil; attempt++ {
			sequence++
			own := r.snapshot(time.Now().UTC(), sequence, connected)
			report := protocol.CoreNodeReport{NodeReport: own, Workers: []protocol.NodeReport{}, ObservationEpoch: epoch}
			if epoch != "" {
				report.Workers = r.probeWorkers(ctx, workers, sequence)
			}
			response, err := r.post(ctx, report)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("event=node_registration_failed error_type=%T", err)
				}
				return
			}
			changed := response.ObservationEpoch != epoch || !sameProbeInventory(workers, response.Workers)
			epoch = response.ObservationEpoch
			workers = append([]protocol.Worker{}, response.Workers...)
			if !changed {
				return
			}
		}
	}
	cycle()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			cycle()
		}
	}
}
func sameProbeInventory(a, b []protocol.Worker) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].AgentURL != b[i].AgentURL {
			return false
		}
	}
	return true
}
func nodeAgentURL(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == ""
}
func (r *nodeReporter) post(parent context.Context, report protocol.CoreNodeReport) (protocol.NodeRegistrationResponse, error) {
	var response protocol.NodeRegistrationResponse
	if err := protocol.ValidateCoreNodeReport(report, time.Now()); err != nil {
		return response, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return response, err
	}
	budget := r.requestTimeout
	if budget <= 0 {
		budget = 2 * time.Second
	}
	ctx, end := context.WithTimeout(parent, budget)
	defer end()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(raw))
	if err != nil {
		return response, errors.New("invalid node registration endpoint")
	}
	request.Header.Set("Content-Type", "application/json")
	protocol.SignRequest(request, raw, r.id, r.token, time.Now())
	reply, err := r.httpClient().Do(request)
	if err != nil {
		return response, errors.New("node registration unavailable")
	}
	defer reply.Body.Close()
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		return response, errors.New("node registration rejected")
	}
	body, err := io.ReadAll(io.LimitReader(reply.Body, 256*1024+1))
	if err != nil || len(body) > 256*1024 || json.Unmarshal(body, &response) != nil {
		return response, errors.New("invalid node registration response")
	}
	if len(response.ObservationEpoch) != 64 || response.ObservationEpoch != strings.ToLower(response.ObservationEpoch) {
		return response, errors.New("invalid node observation epoch")
	}
	for _, ch := range response.ObservationEpoch {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return response, errors.New("invalid node observation epoch")
		}
	}
	if _, err = time.Parse(time.RFC3339Nano, response.ServerTime); err != nil || len(response.Workers) > protocol.MaxNodeReportWorkers || len(response.ClusterWorkerIDs) > protocol.MaxNodeReportWorkers {
		return response, errors.New("invalid node registration inventory")
	}
	seen := map[string]bool{}
	for _, worker := range response.Workers {
		if !protocol.ValidID(worker.ID) || worker.ID == r.id || seen[worker.ID] || !nodeAgentURL(worker.AgentURL) {
			return response, errors.New("invalid node registration inventory")
		}
		seen[worker.ID] = true
	}
	members := map[string]bool{}
	for _, id := range response.ClusterWorkerIDs {
		if !seen[id] || members[id] {
			return response, errors.New("invalid cluster worker inventory")
		}
		members[id] = true
	}
	return response, nil
}
func (r *nodeReporter) probeWorkers(ctx context.Context, workers []protocol.Worker, sequence int64) []protocol.NodeReport {
	out := make([]protocol.NodeReport, len(workers))
	var wait sync.WaitGroup
	for i, worker := range workers {
		wait.Add(1)
		go func(i int, worker protocol.Worker) { defer wait.Done(); out[i] = r.probe(ctx, worker, sequence) }(i, worker)
	}
	wait.Wait()
	return out
}
func (r *nodeReporter) probe(parent context.Context, worker protocol.Worker, sequence int64) protocol.NodeReport {
	failed := func() protocol.NodeReport {
		return protocol.NodeReport{Version: 1, ID: worker.ID, Role: "worker", DisplayName: worker.ID, Sequence: sequence, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "unreachable", Healthy: false, Machine: protocol.NodeMachine{ResourceScope: "unknown"}}
	}
	if !protocol.ValidID(worker.ID) || !nodeAgentURL(worker.AgentURL) {
		return failed()
	}
	budget := r.probeTimeout
	if budget <= 0 {
		budget = 1500 * time.Millisecond
	}
	ctx, end := context.WithTimeout(parent, budget)
	defer end()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(worker.AgentURL, "/")+"/status", nil)
	if err != nil {
		return failed()
	}
	protocol.SignRequest(request, nil, r.id, r.token, time.Now())
	request.Header.Set("Authorization", "Bearer "+r.token)
	reply, err := r.httpClient().Do(request)
	if err != nil {
		return failed()
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return failed()
	}
	raw, err := io.ReadAll(io.LimitReader(reply.Body, protocol.MaxNodeReportBytes+1))
	if err != nil || len(raw) > protocol.MaxNodeReportBytes {
		return failed()
	}
	var report protocol.NodeReport
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &report) != nil || json.Unmarshal(raw, &fields) != nil || report.ID != worker.ID || report.Role != "worker" || (report.Version != 0 && report.Version != 1) {
		return failed()
	}
	for _, name := range []string{"healthy", "status", "generation", "job"} {
		value, ok := fields[name]
		if !ok || name != "job" && string(value) == "null" {
			return failed()
		}
	}
	report.Version = 1
	report.Sequence = sequence
	report.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if report.DisplayName == "" {
		report.DisplayName = worker.ID
	}
	if report.Machine.ResourceScope == "" {
		report.Machine.ResourceScope = "unknown"
	}
	if protocol.ValidateNodeReport(report, time.Now()) != nil || report.Status == "unreachable" {
		return failed()
	}
	return report
}
func (s *service) reportNodes(ctx context.Context) {
	if s.c.role != "controller" {
		return
	}
	r := nodeReporter{queue: s.q, endpoint: strings.TrimRight(s.c.managerURL, "/") + "/api/nodes/report", token: s.c.token, cluster: s.c.cluster, id: s.c.id, client: s.http, snapshot: func(now time.Time, seq int64, connected bool) protocol.NodeReport {
		return s.nodeSnapshot(now, seq, &connected)
	}}
	r.run(ctx)
}
