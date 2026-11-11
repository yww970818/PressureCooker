package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
)

type journal struct {
	Task                                                protocol.Task
	MessageID, Stage, Lease, Started, Finished, Outcome string
	ExitCode                                            int
	RuntimeSlot                                         int
	Generations                                         map[string]int64
	Touched                                             []string
	Inputs                                              []protocol.ObjectRef
	NodeEvidence                                        []protocol.NodeEvidence
	Callback                                            json.RawMessage
	Attempts                                            int
	Next                                                time.Time
}
type activeJob struct {
	id, attempt, state string
	stopRequested      bool
	stopTimer          *time.Timer
	cancel             context.CancelCauseFunc
}
type runner struct {
	s        *service
	mu       sync.Mutex
	active   *activeJob
	delivery sync.Map
	jobs     map[string]*runner
	slots    [maxCoreRuntimeSlots]bool
	tasks    sync.WaitGroup
}

var errStop = errors.New("stop requested")
var errFence = errors.New("lease lost")

func (r *runner) file(id string) string { return filepath.Join(r.s.c.state, "jobs", id+".json") }
func (r *runner) raw(id string) string  { return filepath.Join(r.s.c.results, id, "raw") }
func (r *runner) persist(j *journal) error {
	// Property secrets are available in the signed PEL message until XACK.
	// Recovery never needs a second copy in the local journal.
	safe := *j
	safe.Task.Properties = nil
	return save(r.file(j.Task.TaskID), &safe)
}
func (r *runner) status() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]any{"healthy": true, "status": "idle", "generation": 0, "job": nil, "reset_operation": nil}
	if len(r.jobs) > 0 {
		out["status"] = "running"
		// The existing node-report contract has one optional job. Pick a
		// deterministic live task; per-task control always uses the registry.
		ids := make([]string, 0, len(r.jobs))
		for id := range r.jobs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		child := r.jobs[ids[0]]
		child.mu.Lock()
		state := "CLAIMING"
		if child.active != nil {
			state = child.active.state
		}
		child.mu.Unlock()
		out["job"] = map[string]string{"id": ids[0], "kind": "run", "state": state}
	}
	if a := r.active; a != nil {
		out["status"] = "running"
		out["job"] = map[string]string{"id": a.id, "kind": "run", "state": a.state}
	}
	return out
}
func (r *runner) setState(id, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a := r.active; a != nil && a.id == id {
		a.state = state
	}
}

func (r *runner) stop(id string) bool { return r.stopMode(id, "", false) }
func (r *runner) stopMode(id, attempt string, graceful bool) bool {
	r.mu.Lock()
	if child := r.jobs[id]; child != nil {
		r.mu.Unlock()
		return child.stopMode(id, attempt, graceful)
	}
	defer r.mu.Unlock()
	a := r.active
	if a == nil || a.id != id || (attempt != "" && attempt != a.attempt) {
		return false
	}
	wasRunning := a.state == "RUNNING"
	a.state = "STOPPING"
	a.stopRequested = true
	if graceful && a.stopTimer != nil {
		return true
	}
	if graceful && wasRunning {
		// JMeter's official non-GUI Shutdown command lets the current samples finish.
		// A subsequent force request or an expired grace period still kills the group.
		conn, e := net.DialTimeout("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.stopPort())), time.Second)
		if e == nil {
			_, e = conn.Write([]byte("Shutdown"))
			conn.Close()
		}
		if e == nil {
			a.stopTimer = time.AfterFunc(30*time.Second, func() { a.cancel(errStop) })
			return true
		}
	}
	if a.stopTimer != nil {
		a.stopTimer.Stop()
	}
	a.cancel(errStop)
	return true
}
func (s *service) post(ctx context.Context, endpoint string, body []byte, response any) (int, error) {
	budget := 90 * time.Second
	if strings.HasSuffix(endpoint, "/prepare") {
		budget = 24 * time.Hour
	}
	ctx, end := context.WithTimeout(ctx, budget)
	defer end()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if e != nil {
		return 0, errors.New("invalid endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	protocol.SignRequest(req, body, s.c.id, s.c.token, time.Now())
	res, e := s.http.Do(req)
	if e != nil {
		return 0, errors.New("signed request failed")
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return res.StatusCode, fmt.Errorf("signed endpoint HTTP %d", res.StatusCode)
	}
	if response != nil {
		e = json.NewDecoder(io.LimitReader(res.Body, protocol.MaxCallbackBytes)).Decode(response)
	}
	return res.StatusCode, e
}
func endpoint(t protocol.Task, name string) string {
	u, _ := protocol.NormalizeCallbackURL(t.CallbackURL)
	return strings.TrimSuffix(u, protocol.CallbackPath) + "/api/runner/" + name
}
func body(v any) []byte { b, _ := json.Marshal(v); return b }
func (r *runner) beat(ctx context.Context, t protocol.Task, state string, delivery map[string]any) (bool, error) {
	v := map[string]any{"task_id": t.TaskID, "attempt_id": t.AttemptID, "runner_id": r.s.c.id, "status": state}
	if delivery != nil {
		v["callback_delivery"] = delivery
	}
	var ans struct{ Stop bool }
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, e := r.s.post(ctx, endpoint(t, "heartbeat"), body(v), &ans)
	return ans.Stop, e
}
func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (r *runner) loop(ctx context.Context) {
	defer r.tasks.Wait()
	consumer := r.s.c.id + "-" + r.s.boot
	cluster := r.s.c.cluster
	cursor := "0-0"
	for ctx.Err() == nil {
		if e := r.s.q.EnsureGroup(ctx, cluster); e != nil {
			wait(ctx, 2*time.Second)
			continue
		}
		msgs, next, e := r.s.q.Reclaim(ctx, cluster, consumer, cursor, 6*time.Second)
		if e != nil {
			wait(ctx, time.Second)
			continue
		}
		cursor = next
		for _, m := range msgs {
			r.dispatch(ctx, m)
		}
		msgs, e = r.s.q.Read(ctx, cluster, consumer)
		if e != nil {
			wait(ctx, time.Second)
			continue
		}
		for _, m := range msgs {
			r.dispatch(ctx, m)
		}
	}
}
func terminal(s string) bool {
	switch s {
	case "COMPLETED", "FAILED", "STOPPED", "INTERRUPTED":
		return true
	}
	return false
}
func (r *runner) stopPort() int {
	if r.s == nil {
		return 4445
	}
	return r.s.c.stopPort()
}

// Reserve before launching a goroutine so a reclaimed/duplicate stream message
// cannot claim the same local task or race its journal. A bounded identity pool
// also bounds concurrent Core JVMs; overflow stays pending in the stream.
func (r *runner) reserve(m queue.Message) (*runner, func()) {
	id := m.SignedTask.Task.TaskID
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs[id] != nil {
		return nil, nil
	}
	for i, used := range r.slots {
		if used {
			continue
		}
		c := r.s.c
		c.runtimeSlot = i + 1
		s := &service{c: c, q: r.s.q, s: r.s.s, http: r.s.http, boot: r.s.boot}
		child := &runner{s: s}
		if r.jobs == nil {
			r.jobs = map[string]*runner{}
		}
		r.jobs[id], r.slots[i] = child, true
		r.tasks.Add(1)
		return child, func() {
			r.mu.Lock()
			delete(r.jobs, id)
			r.slots[i] = false
			r.mu.Unlock()
			r.tasks.Done()
		}
	}
	return nil, nil
}
func (r *runner) dispatch(ctx context.Context, m queue.Message) {
	if ctx.Err() != nil {
		return
	}
	child, done := r.reserve(m)
	if child != nil {
		go func() { defer done(); child.handleTask(ctx, m) }()
	}
}
func (r *runner) handle(ctx context.Context, m queue.Message) {
	child, done := r.reserve(m)
	if child != nil {
		defer done()
		child.handleTask(ctx, m)
	}
}
func (r *runner) handleTask(ctx context.Context, m queue.Message) {
	s := r.s
	t := m.SignedTask.Task
	if m.Invalid || protocol.VerifyTask(m.SignedTask, s.c.token) != nil || !protocol.ValidID(t.TaskID) || !protocol.ValidID(t.AttemptID) || t.ControllerID != s.c.id || t.ClusterID != s.c.cluster {
		_ = s.q.Ack(ctx, s.c.cluster, m.ID)
		return
	}
	if _, e := protocol.NormalizeCallbackURL(t.CallbackURL); e != nil {
		return
	}
	j := journal{}
	readErr := load(r.file(t.TaskID), &j)
	if readErr != nil && !os.IsNotExist(readErr) {
		return
	}
	prior := readErr == nil
	if prior {
		if j.RuntimeSlot < 0 || j.RuntimeSlot > maxCoreRuntimeSlots {
			return
		}
		if j.Task.AttemptID != t.AttemptID {
			return
		}
		if j.Stage == "UPLOADING" || j.Stage == "CALLBACK_PENDING" || j.Stage == "ACKNOWLEDGED" {
			return
		}
	}
	if len(t.Workers) == 0 || len(t.Workers) > 64 {
		return
	}
	lease := queue.Token(s.c.id, t.TaskID, t.AttemptID, uuid())
	ok, e := s.q.Acquire(ctx, lease, workerIDs(t)...)
	if !ok || e != nil {
		return
	}
	leaseCtx, endLease := context.WithCancel(ctx)
	leaseDone := make(chan struct{})
	execCtx, cancel := context.WithCancelCause(ctx)
	go func() {
		defer close(leaseDone)
		for wait(leaseCtx, queue.LeaseTTL/4) {
			ok, e := s.q.Renew(leaseCtx, lease)
			if !ok || e != nil {
				cancel(errFence)
				return
			}
		}
	}()
	releaseSafe := true
	defer func() {
		endLease()
		<-leaseDone
		releaseCtx, c := context.WithTimeout(context.Background(), 4*time.Second)
		defer c()
		if releaseSafe {
			_ = s.q.Release(releaseCtx, lease)
		}
		cancel(nil)
	}()
	if !prior {
		j = journal{Task: t, MessageID: m.ID, Started: timestamp(), Stage: "CLAIMING", Lease: lease, ExitCode: -1, RuntimeSlot: s.c.runtimeSlot, Generations: map[string]int64{}}
	} else if j.Stage == "CLAIMING" { // A timed-out claim may already have committed; console returns its original timestamp.
		j.Started = timestamp()
		j.Task = t
	}
	if e = r.persist(&j); e != nil {
		return
	}
	var claim struct {
		Execute        bool
		Status         string
		StartedAt      string `json:"started_at"`
		PreviousLease  string `json:"previous_lease_token"`
		ResultRequired bool   `json:"result_required"`
	}
	claimBody := map[string]string{"task_id": t.TaskID, "attempt_id": t.AttemptID, "runner_id": s.c.id, "lease_token": lease, "started_at": j.Started}
	_, e = s.post(execCtx, endpoint(t, "claim"), body(claimBody), &claim)
	if e != nil {
		return
	}
	if !claim.Execute && terminal(claim.Status) && !claim.ResultRequired {
		if e = s.q.Ack(ctx, s.c.cluster, m.ID); e == nil {
			os.Remove(r.file(t.TaskID))
		}
		return
	}
	if !claim.Execute && claim.StartedAt != "" {
		j.Started = claim.StartedAt
	}
	if !claim.Execute && claim.Status == "QUEUED" {
		return
	}
	releaseSafe = false
	if !claim.Execute && j.Stage == "CLAIMING" && claim.PreviousLease != "" {
		// The signed message and SQL claim reconstruct the exact prior owner if
		// the controller's journal volume was lost. A never-prepared worker simply
		// reports superseded; a successor's generation is never reset.
		if !strings.HasPrefix(claim.PreviousLease, s.c.id+"/"+t.TaskID+"/"+t.AttemptID+"/") || validate(t, s.c) != nil {
			return
		}
		j.Lease = claim.PreviousLease
		for _, w := range t.Workers {
			j.Generations[w.ID] = 0
			j.Touched = append(j.Touched, w.ID)
		}
	}
	r.mu.Lock()
	r.active = &activeJob{id: t.TaskID, attempt: t.AttemptID, state: "PREPARING", cancel: cancel}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if timer := r.active.stopTimer; timer != nil {
			timer.Stop()
		}
		r.active = nil
		r.mu.Unlock()
	}()
	// A claim is necessary but insufficient: an existing local execution marker
	// independently prevents a second JVM launch after an uncertain restart.
	if claim.Execute && j.Stage == "CLAIMING" {
		j.Stage = "PREPARING"
		j.RuntimeSlot = s.c.runtimeSlot
		j.Lease = lease
		if e = r.persist(&j); e != nil {
			return
		}
		timeout, stopTimeout := context.WithTimeout(execCtx, time.Duration(t.TimeoutSeconds)*time.Second)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for wait(timeout, 5*time.Second) {
				r.mu.Lock()
				state := r.active.state
				r.mu.Unlock()
				stop, _ := r.beat(timeout, t, state, nil)
				if stop {
					r.mu.Lock()
					alreadyStopping := r.active.stopRequested
					r.mu.Unlock()
					if !alreadyStopping {
						r.stop(t.TaskID)
					}
					return
				}
			}
		}()
		e = r.execute(timeout, &j)
		cause := context.Cause(timeout)
		stopTimeout()
		<-done
		j.Outcome = "COMPLETED"
		if e != nil || cause != nil {
			j.Outcome = "FAILED"
		}
		switch {
		case errors.Is(cause, errStop):
			j.Outcome = "STOPPED"
		case errors.Is(cause, errFence) || ctx.Err() != nil:
			j.Outcome = "INTERRUPTED"
		}
		r.mu.Lock()
		if r.active.stopRequested {
			j.Outcome = "STOPPED"
		}
		r.mu.Unlock()
	} else if j.Stage != "RESETTING" {
		j.Outcome = "INTERRUPTED"
		j.ExitCode = -1
	}
	if j.Finished == "" {
		j.Finished = timestamp()
	}
	j.Stage = "RESETTING"
	if e = r.persist(&j); e != nil {
		return
	}
	// Stop/timeout/lease cancellation applies to JMeter, not to cleanup.
	// Keep a live lease while retrying a failed reset. If ownership has already
	// expired, the worker still accepts cleanup only for the exact prior owner.
	for {
		cleanupCtx, end := context.WithTimeout(ctx, time.Duration(len(j.Generations)+1)*55*time.Second)
		e = r.resetSelected(cleanupCtx, &j)
		end()
		if e == nil {
			break
		}
		r.setState(t.TaskID, "STOPPING")
		_, _ = r.beat(ctx, t, "STOPPING", nil)
		if !wait(ctx, 2*time.Second) {
			return
		}
		stillOwned, leaseErr := s.q.LeaseOwned(ctx, lease)
		if leaseErr != nil || !stillOwned {
			return
		}
	}
	if e = s.collectOutputsAs(t.TaskID, r.raw(t.TaskID), engineUID+j.RuntimeSlot); e != nil {
		if !errors.Is(e, errInvalidOutput) {
			return
		}
		j.Outcome = "FAILED"
	}
	if e = s.seal(t.TaskID); e != nil {
		return
	}
	releaseSafe = true
	j.Task.Properties = nil
	j.Task.Datasets = nil
	j.Stage = "UPLOADING"
	if e = r.persist(&j); e != nil {
		return
	}
	_, _ = r.beat(ctx, t, "UPLOADING", nil)
}
func (r *runner) execute(ctx context.Context, j *journal) error {
	t := j.Task
	s := r.s
	if e := validate(t, s.c); e != nil {
		return e
	}
	if e := directory(r.raw(t.TaskID)); e != nil {
		return e
	}
	files, e := os.ReadDir(r.raw(t.TaskID))
	if e != nil || len(files) != 0 {
		return errors.New("raw output directory must be empty")
	}
	inputs, refs, e := s.prepareInputs(ctx, t, j.Lease)
	if e != nil {
		return e
	}
	j.Inputs = refs
	for _, w := range t.Workers {
		// Record before sending so a lost response still requires fenced cleanup.
		j.Generations[w.ID] = 0
		j.Touched = append(j.Touched, w.ID)
		if e = r.persist(j); e != nil {
			return e
		}
		a := assignment{TaskID: t.TaskID, AttemptID: t.AttemptID, RunnerID: s.c.id, LeaseToken: j.Lease, Inputs: inputs[w.ID]}
		var ans struct{ Generation int64 }
		_, e = s.post(ctx, strings.TrimSuffix(w.AgentURL, "/")+"/prepare", body(a), &ans)
		if e != nil || ans.Generation <= 0 {
			return errors.New("worker preparation failed")
		}
		j.Generations[w.ID] = ans.Generation
	}
	j.Stage = "STARTED"
	if e = r.persist(j); e != nil {
		return e
	}
	r.setState(t.TaskID, "RUNNING")
	stop, e := r.beat(ctx, t, "RUNNING", nil)
	if e != nil {
		return e
	}
	if stop {
		r.stop(t.TaskID)
		return errStop
	}
	ok, e := s.q.LeaseOwned(ctx, j.Lease)
	if e != nil || !ok {
		return errFence
	}
	code, e := s.runProcess(ctx, t, r.raw(t.TaskID))
	j.ExitCode = code
	if e != nil {
		return e
	}
	f, e := openRegular(filepath.Join(r.raw(t.TaskID), "results.jtl"))
	if e != nil {
		return e
	}
	defer f.Close()
	rd := csv.NewReader(io.LimitReader(f, 2<<20))
	if _, e = rd.Read(); e != nil {
		return errors.New("missing JTL header")
	}
	if _, e = rd.Read(); e != nil {
		return errors.New("missing JTL samples")
	}
	return nil
}
func (r *runner) resetSelected(ctx context.Context, j *journal) error {

	type result struct {
		worker                      string
		generation, resetGeneration int64
		state, operation            string
		evidence                    *protocol.NodeEvidence
		err                         error
	}
	results := make(chan result, len(j.Task.Workers))
	count := 0
	// Send every engine its stop/reset immediately. One offline selected worker
	// must never prevent the healthy engines from stopping load.
	for _, w := range j.Task.Workers {
		gen, touched := j.Generations[w.ID]
		if !touched {
			continue
		}
		count++
		go func() {
			callCtx, end := context.WithTimeout(ctx, 55*time.Second)
			defer end()
			a := assignment{TaskID: j.Task.TaskID, AttemptID: j.Task.AttemptID, RunnerID: r.s.c.id, LeaseToken: j.Lease, Generation: gen, OperationID: "reset-" + j.Task.AttemptID}
			var ans struct {
				State       string
				OperationID string                 `json:"operation_id"`
				Generation  int64                  `json:"generation"`
				Evidence    *protocol.NodeEvidence `json:"node_evidence"`
			}
			_, e := r.s.post(callCtx, strings.TrimSuffix(w.AgentURL, "/")+"/reset", body(a), &ans)
			results <- result{worker: w.ID, generation: gen, resetGeneration: ans.Generation, state: ans.State, operation: ans.OperationID, evidence: ans.Evidence, err: e}
		}()
	}
	for i := 0; i < count; i++ {
		got := <-results
		if got.err != nil || got.state != "COMPLETED" || got.generation < 0 || got.resetGeneration < 1 {
			continue
		}
		if got.operation == "superseded" {
			// A successor or automatic restart has retired our generation. Its
			// state must never be attributed to successful execution of this task.
			if got.generation > 0 && got.resetGeneration <= got.generation {
				continue
			}
			if j.Outcome == "COMPLETED" {
				j.Outcome = "INTERRUPTED"
			}
		} else if got.operation != "reset-"+j.Task.AttemptID || got.generation > 0 && got.resetGeneration != got.generation+1 {
			// A reset advances exactly once. A later restart can retain an old
			// operation receipt while its new engine is still starting; accepting
			// that higher generation would falsely acknowledge our own reset.
			// Generation zero means the prepare response was lost, so only the
			// exact owner-bound reset operation and a positive generation apply.
			continue
		}
		evidence := missingNodeEvidence(got.worker, j.Task.TaskID, j.Task.AttemptID, got.generation)
		if got.evidence != nil && validNodeEvidence(*got.evidence, got.worker, j, got.generation) {
			evidence = *got.evidence
		}
		mergeNodeEvidence(j, got.worker, evidence)
		if evidence.ThreadObservationsStatus == "PENDING" {
			continue
		}
		delete(j.Generations, got.worker)
	}
	if e := r.persist(j); e != nil {
		return e
	}
	if len(j.Generations) > 0 {
		return errors.New("worker reset incomplete")
	}
	return nil
}

// Only the fixed raw log names can be tailed; the authenticated manager applies
// project-specific redaction before returning this private agent response.
func (r *runner) logTail(id string) (string, error) {
	if !protocol.ValidID(id) {
		return "", errors.New("invalid task")
	}
	for _, name := range []string{"console.log", "jmeter.log"} {
		f, e := openRegular(filepath.Join(r.raw(id), name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", e
		}
		st, e := f.Stat()
		if e == nil && st.Size() > 256<<10 {
			_, e = f.Seek(st.Size()-(256<<10), io.SeekStart)
		}
		var b []byte
		if e == nil {
			b, e = io.ReadAll(io.LimitReader(f, 256<<10))
		}
		f.Close()
		if e != nil {
			return "", e
		}
		if len(b) > 0 {
			return string(b), nil
		}
	}
	return "", nil
}

func workerIDs(t protocol.Task) []string {
	ids := make([]string, len(t.Workers))
	for i, worker := range t.Workers {
		ids[i] = worker.ID
	}
	return ids
}
