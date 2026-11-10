package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"pressurecooker/execution/protocol"
)

func (p *Platform) putPlanObject(plan Obj, content string) (protocol.ObjectRef, error) {
	store, e := p.objectStore()
	if e != nil {
		return protocol.ObjectRef{}, e
	}
	ctx, cancel := context.WithCancel(p.requestContext())
	defer cancel()
	return store.Put(ctx, "plans/"+p.projectID()+"/"+str(plan["sha256"])+".jmx", strings.NewReader(content), int64(len(content)), str(plan["sha256"]), "application/xml")
}
func (p *Platform) storeTimeout() time.Duration {
	if p.Config.ArchiveTimeout <= 0 {
		return 30 * time.Second
	}
	return p.Config.ArchiveTimeout
}
func (p *Platform) selectDatasetsQ(q queryer, plan Obj, raw any) ([]protocol.DatasetInput, error) {
	selected := map[string]Obj{}
	for _, v := range arr(raw) {
		s := obj(v)
		id := str(s["node_id"])
		if validID(id) != nil || selected[id] != nil || !contains([]string{"copy", "shard"}, str(s["distribution"])) || len(arr(s["dataset_ids"])) == 0 || len(arr(s["dataset_ids"])) > 50 {
			return nil, api(400, "invalid_dataset_selection", "每个CSV节点须选择1至50个数据文件及copy或shard")
		}
		selected[id] = s
	}
	inputs := []protocol.DatasetInput{}
	var totalBytes int64
	for _, v := range arr(plan["dataset_requirements"]) {
		req := obj(v)
		nodeID := str(req["node_id"])
		s := selected[nodeID]
		if s == nil {
			return nil, api(400, "dataset_required", "须为CSV节点选择数据文件："+str(req["node_name"]))
		}
		delete(selected, nodeID)
		input := protocol.DatasetInput{NodeID: nodeID, NodeName: str(req["node_name"]), Columns: stringList(req["columns"]), VariableNames: stringList(req["variable_names"]), Delimiter: str(req["delimiter"]), Distribution: str(s["distribution"]), Recycle: boolean(req["recycle"]), StopThread: boolean(req["stop_thread"]), Files: []protocol.ObjectRef{}}
		seen := map[string]bool{}
		for _, idv := range arr(s["dataset_ids"]) {
			id := str(idv)
			if seen[id] {
				return nil, api(400, "invalid_dataset_selection", "不能重复选择同一文件")
			}
			seen[id] = true
			data, e := p.datasetGetQ(q, id, false)
			if e != nil {
				return nil, e
			}
			if (str(req["workflow_id"]) != "" && data["workflow_id"] != req["workflow_id"]) || data["node_id"] != nodeID || strings.Join(stringList(data["columns"]), "\x00") != strings.Join(input.Columns, "\x00") || str(data["delimiter"]) != input.Delimiter {
				return nil, api(400, "dataset_schema_mismatch", "数据文件不属于此节点或列定义已变化")
			}
			ref := objectRef(data["object"])
			totalBytes += ref.Size
			if totalBytes > p.taskInputByteLimit() {
				return nil, api(413, "task_inputs_too_large", "单任务数据文件最多1GiB")
			}
			input.Files = append(input.Files, ref)
		}
		inputs = append(inputs, input)
	}
	if len(selected) > 0 {
		return nil, api(400, "unexpected_dataset", "脚本不存在所选CSV节点")
	}
	return inputs, nil
}
func (p *Platform) enqueueDispatch(tx *Tx, run, plan Obj) error {
	ref := objectRef(plan["object"])
	if ref.Key == "" {
		path, e := safePath(p.Config.PlansDir, "snapshots/"+str(plan["sha256"])+".jmx", true)
		if e != nil {
			return e
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		ref, e = p.putPlanObject(plan, string(raw))
		if e != nil {
			return e
		}
		run["plan_object"] = ref
	}
	callback := str(obj(run["cluster_snapshot"])["callback_url"])
	if callback == "" {
		callback = p.Config.RunnerCallbackURL
	}
	if callback == "" {
		callback = "http://manager:8080/api/runner/callback"
	}
	callback, e := protocol.NormalizeCallbackURL(callback)
	if e != nil {
		return api(400, "invalid_callback_url", "集群Webhook须为控制台回传接口")
	}
	obj(run["cluster_snapshot"])["callback_url"] = callback
	task := protocol.Task{Version: 1, TaskID: str(run["id"]), AttemptID: str(run["attempt_id"]), ProjectID: objectProject(run), ClusterID: str(obj(run["cluster_snapshot"])["id"]), ControllerID: str(obj(run["cluster_snapshot"])["controller_id"]), Plan: ref, Workers: []protocol.Worker{}, Datasets: []protocol.DatasetInput{}, Properties: map[string]string{}, CallbackURL: callback, CreatedAt: str(run["created_at"]), TimeoutSeconds: int(number(run["timeout_seconds"]))}
	for _, idv := range arr(run["workers"]) {
		id := str(idv)
		agent := p.Config.WorkerURLs[id]
		u, e := url.Parse(agent)
		if e != nil || u.Hostname() == "" {
			return api(500, "invalid_worker", "执行节点地址无效")
		}
		task.Workers = append(task.Workers, protocol.Worker{ID: id, AgentURL: agent, RMIHost: u.Hostname()})
	}
	run["worker_snapshot"] = task.Workers
	for k, v := range obj(run["properties"]) {
		if k != "base_host" && !strings.HasPrefix(k, "jmeter.env.") && reservedRuntimeProperty(k) {
			return api(400, "reserved_property", "环境包含平台保留属性，请重命名变量")
		}
		task.Properties[k] = str(v)
	}
	for _, v := range arr(dtoObject(Obj{"datasets": run["datasets"]})["datasets"]) {
		var d protocol.DatasetInput
		_ = decodeInto(v, &d)
		task.Datasets = append(task.Datasets, d)
	}
	if len(encoded(task)) > 4*1024*1024-1024 {
		return api(413, "task_payload_too_large", "任务快照超过4MiB，请减少属性或数据文件数量")
	}
	signed, e := protocol.SignTask(task, p.Config.AgentToken)
	if e != nil {
		return e
	}
	payload, e := p.sealWorkspace("task_dispatches", task.TaskID, 1, dtoObject(signed))
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO task_dispatches(id,project_id,status,created_at,payload,last_error) VALUES(?,?,?,?,?,?)", task.TaskID, p.projectID(), "PENDING", now(), payload, "")
	return e
}

// The durable claim uses attempts as a fencing version. A crashed publisher's
// intent becomes eligible again after the deadline; execution still requires
// the runner's task/attempt and exclusive engine lease checks.
const dispatchClaimTTL = 30 * time.Second
const dispatchDBTimeout = 5 * time.Second

type dispatchClaim struct {
	ID, ProjectID, Payload string
	Attempt                int
}

func (p *Platform) claimDispatch(parent context.Context, id string) (claim dispatchClaim, ok bool, err error) {
	ctx, cancel := context.WithTimeout(parent, dispatchDBTimeout)
	defer cancel()
	err = p.transactionContext(ctx, func(tx *Tx) error {
		ok = false // A deadlock retry must not retain the previous transaction's claim.
		var status string
		var next float64
		e := tx.QueryRowContext(ctx, "SELECT project_id,payload,status,attempts,next_attempt_at FROM task_dispatches WHERE id=? FOR UPDATE", id).Scan(&claim.ProjectID, &claim.Payload, &status, &claim.Attempt, &next)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil || status != "PENDING" || next > epoch() {
			return e
		}
		var runStatus string
		if e = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=? FOR UPDATE", id).Scan(&runStatus); e != nil {
			return e
		}
		if contains(terminal, runStatus) {
			_, e = tx.ExecContext(ctx, "UPDATE task_dispatches SET status='CANCELLED',next_attempt_at=0,last_error='' WHERE id=?", id)
			return e
		}
		claim.ID = id
		claim.Attempt++
		_, e = tx.ExecContext(ctx, "UPDATE task_dispatches SET attempts=?,next_attempt_at=? WHERE id=?", claim.Attempt, epoch()+dispatchClaimTTL.Seconds(), id)
		ok = e == nil
		return e
	})
	return claim, ok, err
}

// outcome is a controlled code, never a network error or task property.
func (p *Platform) finishDispatch(parent context.Context, claim dispatchClaim, outcome string) (state string, err error) {
	ctx, cancel := context.WithTimeout(parent, dispatchDBTimeout)
	defer cancel()
	err = p.transactionContext(ctx, func(tx *Tx) error {
		state = ""
		var status string
		var attempt int
		e := tx.QueryRowContext(ctx, "SELECT status,attempts FROM task_dispatches WHERE id=? FOR UPDATE", claim.ID).Scan(&status, &attempt)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil || status != "PENDING" || attempt != claim.Attempt {
			return e
		}
		var runStatus string
		if e = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=? FOR UPDATE", claim.ID).Scan(&runStatus); e != nil {
			return e
		}
		next, lastError := float64(0), outcome
		switch {
		case contains(terminal, runStatus):
			status, lastError = "CANCELLED", ""
		case outcome == "":
			status = "PUBLISHED"
		case outcome == "redis_unavailable":
			status = "PENDING"
			next = epoch() + float64(int64(1)<<min(claim.Attempt-1, 6))
		default:
			status = "FAILED"
		}
		_, e = tx.ExecContext(ctx, "UPDATE task_dispatches SET status=?,last_error=?,next_attempt_at=? WHERE id=?", status, lastError, next, claim.ID)
		if e == nil {
			state = status
		}
		return e
	})
	return state, err
}

func (p *Platform) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		e := p.publishDispatches(ctx)
		p.recordLoop("dispatcher", e)
		if e != nil && ctx.Err() == nil {
			log.Printf("event=dispatch_scan_failed error_type=%T", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (p *Platform) publishDispatches(ctx context.Context) error {
	q, e := p.runnerQueue()
	if e != nil {
		return e
	}
	defer q.Close()
	scanCtx, cancel := context.WithTimeout(ctx, dispatchDBTimeout)
	rows, e := p.DB.QueryContext(scanCtx, "SELECT id FROM task_dispatches WHERE status='PENDING' AND next_attempt_at<=? ORDER BY created_at,id LIMIT 20", epoch())
	if e != nil {
		cancel()
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	cancel()
	if e != nil {
		return e
	}
	for _, id := range ids {
		claim, ok, e := p.claimDispatch(ctx, id)
		if e != nil {
			return e
		}
		if !ok {
			continue
		}
		outcome := ""
		var signed protocol.SignedTask
		data, e := p.openWorkspace("task_dispatches", id, 1, claim.Payload)
		switch {
		case e != nil:
			outcome = "dispatch_decryption_failed"
		case decodeInto(data, &signed) != nil:
			outcome = "dispatch_payload_invalid"
		case signed.Task.TaskID != id || signed.Task.ProjectID != claim.ProjectID || protocol.VerifyTask(signed, p.Config.AgentToken) != nil:
			outcome = "dispatch_payload_invalid"
		default:
			// No database transaction or connection is held during this network call.
			publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, e = q.Publish(publishCtx, signed)
			cancel()
			if e != nil {
				outcome = "redis_unavailable"
			}
		}
		state, e := p.finishDispatch(ctx, claim, outcome)
		if e != nil {
			return e
		} // A lost confirmation is recovered by claim expiry.
		if state != "" {
			if state == "PUBLISHED" || state == "FAILED" || state == "PENDING" {
				p.recordDispatch(outcome)
			}
			if outcome != "" {
				log.Printf("event=dispatch_failed task_id=%s attempt=%d code=%s", id, claim.Attempt, outcome)
			}
		}
		p.recordLoop("dispatcher", nil) // Long batches still show forward progress.
	}
	return ctx.Err()
}

func decodeInto(value any, out any) error { return json.Unmarshal(encoded(value), out) }

func reservedRuntimeProperty(key string) bool {
	return protocol.RuntimePropertyReserved(key) || strings.HasPrefix(strings.ToLower(key), "jmeter.env.")
}
