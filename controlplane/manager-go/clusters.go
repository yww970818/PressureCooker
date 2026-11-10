package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"pressurecooker/execution/protocol"
	"sort"
	"strings"
	"unicode/utf8"
)

// Cluster identities belong to the configured controller inventory, not projects.
func clusterID(controller string) string { return "cluster-" + digest([]byte(controller))[:24] }
func defaultCluster(controller string, workers []string) Obj {
	members := []any{}
	for _, id := range workers {
		members = append(members, id)
	}
	return Obj{"id": clusterID(controller), "name": "集群 · " + controller, "controller_id": controller, "worker_ids": members, "revision": 0, "configured": false, "registration_source": "pending_configuration", "registered": true, "callback_url": ""}
}

type clusterQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

func (p *Platform) clusterConfigurationsQ(q clusterQueryer) ([]any, error) {
	groups := map[string]Obj{}
	for _, controller := range sortedKeys(p.Config.ControllerURLs) {
		members, mapped := p.Config.DefaultClusterWorkers[controller]
		if !mapped {
			members = nil
		}
		group := defaultCluster(controller, members)
		group["name"] = p.nodeDisplayName(controller, "controller")
		if mapped {
			group["registration_source"] = "deployment_default"
		}
		groups[clusterID(controller)] = group
	}
	query := "SELECT data FROM clusters ORDER BY id"
	if _, locked := q.(*Tx); locked {
		query += " FOR UPDATE"
	}
	rows, e := q.Query(query)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		m, e := decode([]byte(raw))
		if e != nil {
			return nil, e
		}
		if _, present := m["callback_url"]; !present {
			m["callback_url"] = ""
		}
		m["configured"] = true
		_, registered := p.Config.ControllerURLs[str(m["controller_id"])]
		m["registered"] = registered
		groups[str(m["id"])] = m
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	result := []any{}
	for _, m := range groups {
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool {
		return str(obj(result[i])["controller_id"]) < str(obj(result[j])["controller_id"])
	})
	return result, nil
}
func (p *Platform) clusterConfigurations() ([]any, error) { return p.clusterConfigurationsQ(p.DB) }

// The baseline migration row exists independently of user projects and
// serializes cluster edits, run snapshots, and claims across managers.
func lockClusterConfigurations(tx *Tx) error {
	var version int
	return tx.QueryRow("SELECT version FROM schema_migrations WHERE version=1 AND state='APPLIED' FOR UPDATE").Scan(&version)
}
func (p *Platform) findClusterQ(q clusterQueryer, id string) (Obj, error) {
	groups, e := p.clusterConfigurationsQ(q)
	if e != nil {
		return nil, e
	}
	for _, group := range groups {
		if str(obj(group)["id"]) == id {
			return obj(group), nil
		}
	}
	return nil, api(404, "cluster_not_found", "集群不存在")
}
func clusterMembers(value any, known map[string]string) ([]any, error) {
	members, ok := value.([]any)
	if !ok || len(members) > protocol.MaxNodeReportWorkers {
		return nil, api(400, "invalid_workers", "集群最多关联 64 个已配置执行节点")
	}
	result := []any{}
	seen := map[string]bool{}
	for _, member := range members {
		id, ok := member.(string)
		_, exists := known[id]
		if !ok || !exists || !protocol.ValidID(id) || seen[id] {
			return nil, api(400, "invalid_workers", "执行节点必须已配置且不能重复")
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}
func sameMembers(a, b any) bool {
	left, right := arr(a), arr(b)
	if len(left) != len(right) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range left {
		seen[str(v)] = true
	}
	for _, v := range right {
		if !seen[str(v)] {
			return false
		}
	}
	return true
}
func (p *Platform) updateCluster(id string, body Obj) (Obj, error) {
	name, ok := body["name"].(string)
	name = strings.TrimSpace(name)
	if !ok || name == "" || utf8.RuneCountInString(name) > 100 {
		return nil, api(400, "invalid_cluster", "集群名称须为 1 至 100 个字符")
	}
	revision, exists := body["revision"]
	n := number(revision)
	if !exists || revision == nil || n < 0 || n != math.Trunc(n) {
		return nil, api(400, "invalid_revision", "须提供有效集群修订号")
	}
	// Strings and booleans must not be silently accepted as revision numbers.
	switch revision.(type) {
	case int, int64, float64, json.Number:
	default:
		return nil, api(400, "invalid_revision", "须提供有效集群修订号")
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n > 9007199254740991 {
		return nil, api(400, "invalid_revision", "须提供有效集群修订号")
	}
	var saved Obj
	e := p.transaction(func(tx *Tx) error {
		if e := lockClusterConfigurations(tx); e != nil {
			return e
		}
		current, e := p.findClusterQ(tx, id)
		if e != nil {
			return e
		}
		if !boolean(current["registered"]) {
			return api(409, "cluster_unregistered", "控制器已移出配置清单，只能查看")
		}
		if number(current["revision"]) != n {
			return api(409, "revision_conflict", "集群已修改，请刷新后重试")
		}
		saved = clone(current)
		saved["name"] = name
		saved["revision"] = n + 1
		saved["configured"] = true
		if raw, present := body["callback_url"]; present {
			text, ok := raw.(string)
			if !ok {
				return api(400, "invalid_callback_url", "回调地址须为 HTTP 或 HTTPS 地址")
			}
			text = strings.TrimSpace(text)
			if strings.TrimSpace(text) == "" {
				text = p.Config.RunnerCallbackURL
				if text == "" {
					text = "http://manager:8080/api/runner/callback"
				}
			}
			normalized, e := protocol.NormalizeCallbackURL(text)
			if e != nil {
				return api(400, "invalid_callback_url", "Webhook须为控制台HTTP(S)地址或/api/runner/callback接口")
			}
			saved["callback_url"] = normalized
		}
		if value, present := body["worker_ids"]; present {
			members, e := clusterMembers(value, p.Config.WorkerURLs)
			if e != nil {
				return e
			}
			if !sameMembers(current["worker_ids"], members) {
				rows, e := tx.Query("SELECT data FROM runs WHERE JSON_UNQUOTE(JSON_EXTRACT(data,'$.execution_mode'))='redis' AND status IN ('QUEUED','PREPARING','RUNNING','STOPPING','RECOVERING') FOR UPDATE")
				if e != nil {
					return e
				}
				blocked := false
				for rows.Next() {
					var raw string
					if e = rows.Scan(&raw); e != nil {
						rows.Close()
						return e
					}
					run, e := decode([]byte(raw))
					if e != nil {
						rows.Close()
						return e
					}
					snap := obj(run["cluster_snapshot"])
					associated := str(run["cluster_id"]) == id || str(snap["controller_id"]) == str(current["controller_id"]) || str(run["controller_id"]) == str(current["controller_id"])
					if len(snap) == 0 && str(run["controller_id"]) == "" {
						for _, w := range arr(run["workers"]) {
							for _, member := range arr(current["worker_ids"]) {
								associated = associated || str(w) == str(member)
							}
						}
					}
					blocked = blocked || associated
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return e
				}
				if blocked {
					return api(409, "cluster_busy", "集群有排队或运行任务，暂不能修改执行节点")
				}
			}
			saved["worker_ids"] = members
			saved["registration_source"] = "configured"
		}
		raw := string(encoded(saved))
		if boolean(current["configured"]) {
			_, e = tx.Exec("UPDATE clusters SET data=? WHERE id=?", raw, id)
		} else {
			_, e = tx.Exec("INSERT INTO clusters(id,data,created_at) VALUES(?,?,?)", id, raw, now())
		}
		return e
	})
	return saved, e
}
func unreachableNode(id, role string) Obj {
	return Obj{"id": id, "role": role, "healthy": false, "status": "unreachable", "registered": false, "job": nil, "generation": nil, "reset_operation": nil}
}
func (p *Platform) attachClusterStatusQ(q clusterQueryer, controllers, workers []any) ([]any, []any, error) {
	groups, e := p.clusterConfigurationsQ(q)
	if e != nil {
		return nil, nil, e
	}
	cs, ws := map[string]Obj{}, map[string]Obj{}
	counts := map[string]int{}
	for _, v := range controllers {
		cs[str(obj(v)["id"])] = obj(v)
	}
	for _, v := range workers {
		ws[str(obj(v)["id"])] = obj(v)
	}
	for _, v := range groups {
		for _, w := range arr(obj(v)["worker_ids"]) {
			counts[str(w)]++
		}
	}
	for _, v := range groups {
		g := obj(v)
		id := str(g["controller_id"])
		c := cs[id]
		if c == nil {
			c = unreachableNode(id, "controller")
		}
		g["controller"] = c
		members, shared := []any{}, []any{}
		for _, w := range arr(g["worker_ids"]) {
			id := str(w)
			status := ws[id]
			if str(c["registration_status"]) == "registered" {
				status = obj(obj(c["_worker_statuses"])[id])
				if len(status) == 0 {
					status = p.missingNode(id, "worker", "worker_missing")
					status["reported_by"] = str(g["controller_id"])
				}
			}
			if status == nil {
				status = unreachableNode(id, "worker")
			}
			members = append(members, status)
			if counts[id] > 1 {
				shared = append(shared, id)
			}
		}
		g["workers"] = members
		g["shared_worker_ids"] = shared
	}
	unassigned := []any{}
	for _, v := range workers {
		if counts[str(obj(v)["id"])] == 0 {
			unassigned = append(unassigned, v)
		}
	}
	return groups, unassigned, nil
}
func (p *Platform) selectClusterRunQ(tx *Tx, id string, requested any, present bool) (Obj, []any, error) {
	if id == "" {
		return nil, nil, api(400, "cluster_required", "创建任务必须选择集群")
	}
	if e := lockClusterConfigurations(tx); e != nil {
		return nil, nil, e
	}
	group, e := p.findClusterQ(tx, id)
	if e != nil {
		return nil, nil, e
	}
	if !boolean(group["registered"]) {
		return nil, nil, api(409, "cluster_unregistered", "控制器已移出配置清单")
	}
	workers := arr(group["worker_ids"])
	if present {
		var ok bool
		workers, ok = requested.([]any)
		if !ok {
			return nil, nil, api(400, "invalid_workers", "须选择集群内执行节点")
		}
	}
	if len(workers) < 1 || len(workers) > protocol.MaxNodeReportWorkers {
		return nil, nil, api(400, "invalid_workers", "请选择集群内 1 至 64 个执行节点")
	}
	members := map[string]bool{}
	for _, w := range arr(group["worker_ids"]) {
		members[str(w)] = true
	}
	seen := map[string]bool{}
	for _, w := range workers {
		id, ok := w.(string)
		_, registered := p.Config.WorkerURLs[id]
		if !ok || !protocol.ValidID(id) || !registered || !members[id] || seen[id] {
			return nil, nil, api(400, "invalid_workers", "执行节点须属于所选集群、仍已配置且不能重复")
		}
		seen[id] = true
	}
	snapshot := Obj{}
	for _, key := range []string{"id", "name", "revision", "controller_id", "worker_ids", "callback_url"} {
		snapshot[key] = group[key]
	}
	return snapshot, workers, nil
}
