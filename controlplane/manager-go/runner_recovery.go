package main

import (
	"context"
	"time"

	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
	"pressurecooker/manager/internal/tasks"
)

func (s *Scheduler) reconcileRedisRunsContext(ctx context.Context, runs []any) error {
	grace := s.P.Config.RunnerStaleAfter
	if grace <= 0 {
		grace = 2 * queue.LeaseTTL
	}
	candidates := []Obj{}
	for _, v := range runs {
		run := obj(v)
		if str(run["execution_mode"]) != "redis" || !tasks.Status(str(run["status"])).Physical() || str(run["runner_lease_token"]) == "" {
			continue
		}
		stamp := str(run["runner_heartbeat_at"])
		if stamp == "" {
			stamp = str(run["started_at"])
		}
		at, e := time.Parse(time.RFC3339Nano, stamp)
		if e != nil || time.Since(at) < grace {
			continue
		}
		candidates = append(candidates, run)
	}
	if len(candidates) == 0 {
		s.recoveryCursor = ""
		return nil
	}
	next := 0
	for i, run := range candidates {
		if str(run["id"]) == s.recoveryCursor {
			next = (i + 1) % len(candidates)
			break
		}
	}
	// Resolve at most one owner per tick, keeping its existing 12-second
	// budget. Advance even on timeout so one unreachable worker group cannot
	// indefinitely reserve unrelated groups behind it.
	run := candidates[next]
	s.recoveryCursor = str(run["id"])
	return s.resolveRedisOwnerContext(ctx, run)
}
func (p *Platform) frozenRunWorkersContext(ctx context.Context, run Obj) ([]protocol.Worker, error) {
	var workers []protocol.Worker
	if run["worker_snapshot"] != nil {
		if e := decodeInto(run["worker_snapshot"], &workers); e != nil {
			return nil, e
		}
	} else {
		var payload string
		if e := p.DB.QueryRowContext(ctx, "SELECT payload FROM task_dispatches WHERE id=?", run["id"]).Scan(&payload); e != nil {
			return nil, e
		}
		data, e := p.openWorkspace("task_dispatches", str(run["id"]), 1, payload)
		if e != nil {
			return nil, e
		}
		var signed protocol.SignedTask
		if e = decodeInto(data, &signed); e != nil {
			return nil, e
		}
		if signed.Task.AttemptID != str(run["attempt_id"]) || signed.Task.TaskID != str(run["id"]) {
			return nil, api(409, "runner_fenced", "Frozen task differs from recovery owner")
		}
		workers = signed.Task.Workers
	}
	if len(workers) != len(arr(run["workers"])) || len(workers) == 0 || len(workers) > protocol.MaxNodeReportWorkers {
		return nil, api(409, "worker_snapshot_missing", "Frozen worker identities unavailable")
	}
	selected := map[string]bool{}
	for _, id := range arr(run["workers"]) {
		selected[str(id)] = true
	}
	for _, w := range workers {
		if !selected[w.ID] || w.AgentURL == "" {
			return nil, api(409, "worker_snapshot_mismatch", "Frozen worker identities differ")
		}
		delete(selected, w.ID)
	}
	return workers, nil
}
func reportedRecoveryWorkersReady(workers []protocol.Worker, statuses []any, observationEpoch string) bool {
	if len(workers) == 0 || observationEpoch == "" {
		return false
	}
	byID := reportedStatusByID(statuses)
	for _, worker := range workers {
		status := byID[worker.ID]
		if !boolean(status["healthy"]) || str(status["status"]) != "idle" || status["job"] != nil || number(status["generation"]) <= 0 || str(status["observation_epoch"]) != observationEpoch {
			return false
		}
	}
	return true
}
func (s *Scheduler) resolveRedisOwnerContext(parent context.Context, run Obj) error {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	workers, e := s.P.frozenRunWorkersContext(ctx, run)
	if e != nil {
		return e
	}
	q, e := s.P.runnerQueue()
	if e != nil {
		return e
	}
	defer q.Close()
	lease := queue.Token("manager-recovery", str(run["id"]), str(run["attempt_id"]), newID(""))
	owned, e := q.Acquire(ctx, lease, stringList(run["workers"])...)
	if e != nil || !owned {
		return e
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = q.Release(ctx, lease)
	}()
	// A Core must probe again after learning this lease epoch from its ACK.
	// Cross-machine observed_at clocks cannot establish that causal boundary.
	observationEpoch := ""
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		owned, err := q.LeaseOwned(ctx, lease)
		if err != nil || !owned {
			return err
		}
		observationEpoch, err = q.NodeObservationEpoch(ctx)
		if err != nil {
			return err
		}
		_, statuses, err := s.P.nodeInventory(ctx)
		if err != nil {
			return err
		}
		if reportedRecoveryWorkersReady(workers, statuses, observationEpoch) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return s.P.transactionContext(ctx, func(tx *Tx) error {
		if e := lockClusterConfigurations(tx); e != nil {
			return e
		}
		repo := tasks.Repository{DB: tx}
		record, e := repo.Load(ctx, str(run["id"]), "", true)
		if e != nil {
			return e
		}
		current, e := decode(record.Data)
		if e != nil {
			return e
		}
		if !tasks.Status(str(current["status"])).Physical() || str(current["attempt_id"]) != str(run["attempt_id"]) || str(current["runner_lease_token"]) != str(run["runner_lease_token"]) || str(current["runner_heartbeat_at"]) != str(run["runner_heartbeat_at"]) {
			return nil
		}
		ok, e := q.Renew(ctx, lease)
		if e != nil || !ok {
			return e
		}
		currentEpoch, e := q.NodeObservationEpoch(ctx)
		if e != nil || currentEpoch != observationEpoch {
			return e
		}
		current, e = s.P.openStoredRun(current)
		if e != nil {
			return e
		}
		state := tasks.Status(str(current["status"])).LostRunner(str(current["stop_mode"]) != "")
		receipt, e := newReceipt(current)
		if e != nil {
			return e
		}
		receipt["status"] = "EMPTY"
		receipt["missing_files"] = rawFiles
		receipt["error"] = "runner_unavailable"
		current["raw_archive"] = receipt
		current["status"] = string(state)
		current["finished_at"] = now()
		current["runner_reconciled"] = true
		current["runner_recovery_reason"] = "owner_lease_expired_workers_idle"
		current["callback_delivery"] = Obj{"status": "FAILED", "attempts": 0, "last_error": "runner_unavailable", "next_attempt_at": ""}
		// Redis terminal callbacks use their existing delivery contract, not the
		// legacy terminal webhook. Preserve the same encrypted storage projection
		// while keeping every SQL operation under the recovery deadline.
		stored, e := s.P.runForStorage(current)
		if e != nil {
			return e
		}
		return repo.Save(ctx, runRecord(current, encoded(stored)))
	})
}

func (p *Platform) currentRecoveryRuns(ctx context.Context) ([]any, error) {
	rows, err := p.DB.QueryContext(ctx, "SELECT data FROM runs WHERE JSON_UNQUOTE(JSON_EXTRACT(data,'$.execution_mode'))='redis' AND status IN ('PREPARING','RUNNING','STOPPING','RECOVERING') ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	raw := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			rows.Close()
			return nil, err
		}
		raw = append(raw, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	runs := []any{}
	for _, value := range raw {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		run, e := decode([]byte(value))
		if e != nil {
			return nil, e
		}
		run, e = p.openStoredRun(run)
		if e != nil {
			return nil, e
		}
		runs = append(runs, run)
	}
	return runs, nil
}
