package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"pressurecooker/execution/protocol"
)

var rawNames = []string{"results.jtl", "jmeter.log", "console.log"}

// Delivery workers may remain pending during a network partition. They hold
// neither the Redis engine lease nor the controller execution slot.
func (r *runner) deliveryLoop(ctx context.Context) {
	limit := make(chan struct{}, 4)
	var group sync.WaitGroup
	defer group.Wait()
	for ctx.Err() == nil {
		entries, _ := os.ReadDir(filepath.Join(r.s.c.state, "jobs"))
		for _, e := range entries {
			id := strings.TrimSuffix(e.Name(), ".json")
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || !protocol.ValidID(id) {
				continue
			}
			if _, ok := r.delivery.LoadOrStore(id, true); ok {
				continue
			}
			var j journal
			if load(r.file(id), &j) != nil || j.Task.TaskID != id || (j.Stage != "UPLOADING" && j.Stage != "CALLBACK_PENDING" && j.Stage != "ACKNOWLEDGED") || time.Now().Before(j.Next) {
				r.delivery.Delete(id)
				continue
			}
			select {
			case limit <- struct{}{}:
				group.Add(1)
				go func() { defer group.Done(); defer func() { <-limit; r.delivery.Delete(id) }(); r.deliver(ctx, &j) }()
			default:
				r.delivery.Delete(id)
			}
		}
		wait(ctx, time.Second)
	}
}
func (r *runner) upload(ctx context.Context, j *journal) error {
	t := j.Task
	start, e := time.Parse(time.RFC3339Nano, j.Started)
	if e != nil {
		return errors.New("invalid journal timestamp")
	}
	base := start.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") + "/" + t.TaskID + "/raw/"
	cb := protocol.Callback{EventID: "event-" + uuid(), TaskID: t.TaskID, AttemptID: t.AttemptID, RunnerID: r.s.c.id, Status: j.Outcome, ExitCode: j.ExitCode, StartedAt: j.Started, FinishedAt: j.Finished, InputArtifacts: j.Inputs, Artifacts: []protocol.ObjectRef{}, MissingFiles: []string{}, NodeEvidence: boundedNodeEvidence(j.NodeEvidence)}
	for _, name := range rawNames {
		p := filepath.Join(r.raw(t.TaskID), name)
		if _, e = os.Lstat(p); os.IsNotExist(e) {
			cb.MissingFiles = append(cb.MissingFiles, name)
			continue
		} else if e != nil {
			return e
		}
		ctype := "text/plain"
		if name == "results.jtl" {
			ctype = "text/csv"
		}
		ref, e := r.s.putFile(ctx, base+name, p, ctype)
		if e != nil {
			return e
		}
		ref.URL = strings.TrimSuffix(endpoint(t, "callback"), protocol.CallbackPath) + "/api/runs/" + t.TaskID + "/artifacts/" + name + "?project_id=" + url.QueryEscape(t.ProjectID)
		cb.Artifacts = append(cb.Artifacts, ref)
	}
	j.Callback, e = json.Marshal(cb)
	if e != nil {
		return e
	}
	if len(j.Callback) > protocol.MaxCallbackBytes {
		return errors.New("callback exceeds size limit")
	}
	j.Stage = "CALLBACK_PENDING"
	j.Attempts = 0
	j.Next = time.Time{}
	return r.persist(j)
}
func (r *runner) retry(ctx context.Context, j *journal, code int) {
	delay := time.Second * (1 << min(j.Attempts, 8))
	j.Next = time.Now().UTC().Add(delay)
	if r.persist(j) != nil {
		return
	}
	_, _ = r.beat(ctx, j.Task, j.Stage, map[string]any{"status": "RETRY", "attempts": j.Attempts, "last_status_code": code, "last_error": "delivery_failed", "next_attempt_at": j.Next.Format(time.RFC3339Nano)})
}
func (r *runner) deliver(ctx context.Context, j *journal) {
	if j.Stage == "ACKNOWLEDGED" {
		r.cleanup(ctx, j)
		return
	}
	if j.Stage == "UPLOADING" {
		_, _ = r.beat(ctx, j.Task, "UPLOADING", nil)
		if e := r.upload(ctx, j); e != nil {
			j.Attempts++
			r.retry(ctx, j, 0)
			return
		}
	}
	if j.Stage != "CALLBACK_PENDING" || len(j.Callback) == 0 {
		return
	}
	j.Attempts++
	if e := r.persist(j); e != nil {
		return
	}
	_, _ = r.beat(ctx, j.Task, "CALLBACK_PENDING", map[string]any{"status": "DELIVERING", "attempts": j.Attempts})
	var ans struct{ Accepted bool }
	code, e := r.s.post(ctx, endpoint(j.Task, "callback"), j.Callback, &ans)
	if e != nil || code != 200 || !ans.Accepted {
		r.retry(ctx, j, code)
		return
	}

	j.Stage = "ACKNOWLEDGED"
	j.Attempts = 0
	if r.persist(j) == nil {
		r.cleanup(ctx, j)
	}
}
func (r *runner) cleanup(ctx context.Context, j *journal) {
	// XACK is idempotent if the process stops between Redis and the journal.
	if r.s.q.Ack(ctx, r.s.c.cluster, j.MessageID) != nil {
		return
	}
	a := assignment{TaskID: j.Task.TaskID, AttemptID: j.Task.AttemptID, RunnerID: r.s.c.id, LeaseToken: j.Lease}
	for _, worker := range j.Task.Workers {
		if !slices.Contains(j.Touched, worker.ID) {
			continue
		}
		_, e := r.s.post(ctx, strings.TrimSuffix(worker.AgentURL, "/")+"/cleanup", body(a), nil)
		if e != nil {
			j.Next = time.Now().UTC().Add(time.Minute)
			_ = r.persist(j)
			return
		}
	}
	if os.RemoveAll(r.s.taskDir(j.Task.TaskID)) == nil {
		_ = os.Remove(r.file(j.Task.TaskID))
	}
}
