package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
)

type AgentError struct {
	Status  int
	Message string
}

func (e *AgentError) Error() string { return e.Message }

type AgentClient struct {
	Config Config
	HTTP   *http.Client
}

func newClient(c Config) *AgentClient { return &AgentClient{c, &http.Client{Timeout: c.AgentTimeout}} }
func (c *AgentClient) request(endpoint, path string, payload Obj) (Obj, error) {
	if endpoint == "" {
		return nil, &AgentError{0, "Agent endpoint not configured"}
	}
	method := "GET"
	var source io.Reader
	if payload != nil {
		method = "POST"
		source = bytes.NewReader(encoded(payload))
	}
	r, e := http.NewRequest(method, strings.TrimRight(endpoint, "/")+path, source)
	if e != nil {
		return nil, &AgentError{0, "Invalid agent endpoint"}
	}
	r.Header.Set("Authorization", "Bearer "+c.Config.AgentToken)
	r.Header.Set("Content-Type", "application/json")
	res, e := c.HTTP.Do(r)
	if e != nil {
		return nil, &AgentError{0, "Agent connection failed"}
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return nil, &AgentError{res.StatusCode, "Agent rejected request"}
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if e != nil || len(b) > 2*1024*1024 {
		return nil, &AgentError{0, "Invalid agent response"}
	}
	var m Obj
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&m); e != nil || m == nil {
		return nil, &AgentError{0, "Invalid agent response"}
	}
	return m, nil
}
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func (p *Platform) clusterContext(ctx context.Context) (Obj, error) {
	controllers, workers, registryError := p.nodeInventory(ctx)
	if registryError != nil {
		// Keep configuration visible on registry failure, with no fabricated
		// availability and no direct HTTP fallback.
		controllers, workers = []any{}, []any{}
		for _, id := range sortedKeys(p.Config.ControllerURLs) {
			node := p.missingNode(id, "controller", "registry_unavailable")
			node["registration_status"] = "unknown"
			controllers = append(controllers, node)
		}
		for _, id := range sortedKeys(p.Config.WorkerURLs) {
			node := p.missingNode(id, "worker", "registry_unavailable")
			node["registration_status"] = "unknown"
			workers = append(workers, node)
		}
	}
	q := contextQueryer{ctx: ctx, db: p.DB}
	rows, e := q.Query("SELECT id,role,heartbeat_at,heartbeat_epoch FROM instances ORDER BY id")
	if e != nil {
		return nil, e
	}
	instances := []any{}
	var leader any
	for rows.Next() {
		var id, role, at string
		var heartbeat float64
		if e = rows.Scan(&id, &role, &at, &heartbeat); e != nil {
			rows.Close()
			return nil, e
		}
		healthy := epoch()-heartbeat < p.Config.WatchdogTimeout.Seconds()
		instances = append(instances, Obj{"id": id, "role": role, "healthy": healthy, "heartbeat_at": at})
		if role == "leader" && healthy && leader == nil {
			leader = id
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	counts, e := q.Query("SELECT status,COUNT(*) FROM runs WHERE JSON_UNQUOTE(JSON_EXTRACT(data,'$.execution_mode'))='redis' GROUP BY status")
	if e != nil {
		return nil, e
	}
	queued, running := 0, 0
	for counts.Next() {
		var status string
		var n int
		if e = counts.Scan(&status, &n); e != nil {
			counts.Close()
			return nil, e
		}
		if status == "QUEUED" {
			queued += n
		}
		if contains(active, status) && status != "CALLBACK_PENDING" && status != "UPLOADING" {
			running += n
		}
	}
	e = counts.Err()
	counts.Close()
	if e != nil {
		return nil, e
	}
	groups, unassigned, e := p.attachClusterStatusQ(q, controllers, workers)
	if e != nil {
		return nil, e
	}
	for _, value := range controllers {
		delete(obj(value), "_worker_statuses")
	}
	return Obj{"clusters": groups, "unassigned_workers": unassigned, "manager": Obj{"leader": leader, "instances": instances}, "controllers": controllers, "workers": workers, "queue": Obj{"queued": queued, "running": running}, "server_time": now(), "registration_error": registryError != nil}, nil
}
func agentStatus(e error) int {
	if a, ok := e.(*AgentError); ok {
		return a.Status
	}
	return 0
}
