package main

import (
	"context"
	"time"

	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
	"pressurecooker/manager/internal/tasks"
)

// Retired task records are evidence. Reading them never dispatches, stops or
// repairs an engine. A separate explicit operation may close old active metadata.
func (p *Platform) retiredExecutions() (Obj, error) {
	rows, err := p.DB.Query("SELECT id,project_id,status,data FROM runs WHERE COALESCE(JSON_UNQUOTE(JSON_EXTRACT(data,'$.execution_mode')),'')<>'redis' AND status NOT IN ('COMPLETED','FAILED','STOPPED','INTERRUPTED') ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, project, status, raw string
		if err = rows.Scan(&id, &project, &status, &raw); err != nil {
			return nil, err
		}
		items = append(items, Obj{"id": id, "project_id": project, "status": status, "fingerprint": digest([]byte(raw))})
	}
	return Obj{"items": items, "total": len(items)}, rows.Err()
}

func (p *Platform) retireExecution(ctx context.Context, id, fingerprint string) (Obj, error) {
	if validID(id) != nil || !objectSHA.MatchString(fingerprint) {
		return nil, api(400, "invalid_retirement", "须提供任务ID和审计指纹")
	}
	if len(p.Config.WorkerURLs) == 0 {
		return nil, api(409, "workers_unverified", "须配置并确认全部执行节点空闲")
	}
	q, err := p.runnerQueue()
	if err != nil {
		return nil, err
	}
	defer q.Close()
	lease := queue.Token("retirement", id, "metadata", newID(""))
	ok, err := q.Acquire(ctx, lease)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api(409, "execution_busy", "执行租约仍被占用")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = q.Release(c, lease)
	}()
	workers := []protocol.Worker{}
	for _, wid := range sortedKeys(p.Config.WorkerURLs) {
		workers = append(workers, protocol.Worker{ID: wid})
	}
	wait, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		_, statuses, e := p.nodeInventory(wait)
		if e != nil {
			return nil, e
		}
		if reportedRecoveryWorkersReady(workers, statuses, protocol.NodeObservationEpoch(lease)) {
			break
		}
		select {
		case <-wait.Done():
			return nil, api(409, "workers_unverified", "未收到当前租约下全部执行节点的空闲证据")
		case <-tick.C:
		}
	}
	var result Obj
	err = p.transactionContext(wait, func(tx *Tx) error {
		if e := lockClusterConfigurations(tx); e != nil {
			return e
		}
		record, e := (tasks.Repository{DB: tx}).Load(wait, id, "", true)
		if e != nil {
			return e
		}
		run, e := decode(record.Data)
		if e != nil {
			return e
		}
		if record.Status.Terminal() && str(obj(run["retirement"])["source_fingerprint"]) == fingerprint {
			result = Obj{"id": id, "status": string(record.Status), "source_fingerprint": fingerprint}
			return nil
		}
		if digest(record.Data) != fingerprint {
			return api(409, "retirement_conflict", "任务已变化，请重新审计")
		}
		if str(run["execution_mode"]) == "redis" || record.Status.Terminal() {
			return api(409, "execution_current", "仅可退役旧非终态执行记录")
		}
		owned, e := q.Renew(wait, lease)
		if e != nil {
			return e
		}
		if !owned {
			return api(409, "execution_busy", "退役租约已失效")
		}
		run["status"] = "INTERRUPTED"
		run["finished_at"] = now()
		run["retirement"] = Obj{"reason": "execution_protocol_retired", "prior_status": string(record.Status), "source_fingerprint": fingerprint, "at": now()}
		if e = (tasks.Repository{DB: tx}).Save(wait, runRecord(run, encoded(run))); e != nil {
			return e
		}
		_, e = tx.ExecContext(wait, "INSERT INTO audit(created_at,action,object_id) VALUES(?,?,?)", now(), "execution.retire", id)
		result = Obj{"id": id, "status": "INTERRUPTED", "source_fingerprint": fingerprint}
		return e
	})
	return result, err
}
