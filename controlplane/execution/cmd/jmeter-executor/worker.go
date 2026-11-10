package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"pressurecooker/execution/protocol"
)

type assignment struct {
	TaskID      string    `json:"task_id"`
	AttemptID   string    `json:"attempt_id"`
	RunnerID    string    `json:"runner_id"`
	LeaseToken  string    `json:"lease_token"`
	Inputs      []binding `json:"inputs,omitempty"`
	Generation  int64     `json:"expected_generation,omitempty"`
	OperationID string    `json:"operation_id,omitempty"`
}
type engineRecord struct {
	Generation     int64
	Owner          *assignment
	LastReset      string
	Ready          bool
	Prepared       bool
	PreparedAt     string
	LogCursors     map[string]logCursor
	LastResetOwner *assignment
}
type worker struct {
	s    *service
	op   sync.Mutex
	mu   sync.Mutex
	rec  engineRecord
	proc *process
}

func (w *worker) file() string   { return filepath.Join(w.s.c.state, "worker.json") }
func (w *worker) persist() error { return save(w.file(), w.rec) }
func (w *worker) status() map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	healthy := w.rec.Ready && w.proc.alive()
	state := "recovering"
	if healthy {
		state = "idle"
	}
	var job any
	if w.rec.Owner != nil {
		state = "running"
		job = map[string]string{"id": w.rec.Owner.TaskID, "state": "RUNNING", "kind": "run"}
	}
	var reset any
	if w.rec.LastReset != "" {
		reset = map[string]any{"id": w.rec.LastReset, "state": "COMPLETED", "to_generation": w.rec.Generation}
	}
	return map[string]any{"healthy": healthy, "status": state, "generation": w.rec.Generation, "job": job, "reset_operation": reset}
}

// restart runs under op lock. A new engine must bind both ports after the old
// process has exited. The persisted generation advances before it can be used.
func (w *worker) restart(ctx context.Context, op string) error {
	w.mu.Lock()
	old := w.proc
	oldOwner := w.rec.Owner
	preparedAt, cursors := w.rec.PreparedAt, w.rec.LogCursors
	w.rec.Ready = false
	w.rec.Prepared = false
	w.rec.Generation++
	w.proc = nil
	gen := w.rec.Generation
	e := w.persist()
	w.mu.Unlock()
	old.kill()
	// Recovery can load an owner without an in-memory process handle. Finish
	// the existing private-namespace sweep before reading any engine output.
	if w.s.c.isolated {
		if se := reapEngine(); se != nil {
			return se
		}
	}
	if oldOwner != nil {
		// Kill and wait first, then preserve this generation before either log
		// can be cleared for the replacement JVM. Collection cannot block reset.
		w.captureEvidence(*oldOwner, preparedAt, cursors)
		if se := w.s.seal(oldOwner.TaskID); se != nil {
			return se
		}
	}
	if e != nil {
		return e
	}
	dir := filepath.Join(w.s.c.work, "engine")
	logFile := filepath.Join(dir, "jmeter.log")
	if e = writeFile(logFile, nil); e != nil {
		return e
	}
	if e = writeFile(filepath.Join(dir, "console.log"), nil); e != nil {
		return e
	}
	if w.s.c.isolated {
		if e = grant(dir, true, 0710); e != nil {
			return e
		}
		if e = grant(logFile, false, 0660); e != nil {
			return e
		}
	}
	args := append(w.s.c.threadObserverEngineArgs(gen), "-s", "-Jserver_port=1099", "-Jserver.rmi.localport=4000", "-Jserver.exitaftertest=false", "-Jengine_id="+w.s.c.id, "-j", filepath.Join(dir, "jmeter.log"))
	p, e := launch(w.s.c, args, dir, filepath.Join(dir, "console.log"))
	if e != nil {
		return e
	}
	w.mu.Lock()
	w.proc = p
	w.mu.Unlock()
	deadline, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-p.done:
			return errors.New("engine exited during startup")
		case <-deadline.Done():
			p.kill()
			return errors.New("engine readiness timed out")
		case <-tick.C:
			// Both RMI sockets exist before the registry binding is necessarily ready.
			// JMeter 5.6.3 logs this INFO message only after Registry.rebind returns.
			if !registryBound(logFile) {
				continue
			}
			if w.s.c.rmiPortsReady("1099", "4000") {
				w.mu.Lock()
				if w.rec.Generation == gen {

					w.rec.Owner = nil
					w.rec.Ready = true
					w.rec.LastReset = op
					w.rec.LastResetOwner = oldOwner
					e = w.persist()
				}
				w.mu.Unlock()
				return e
			}
		}
	}
}
func (w *worker) loop(ctx context.Context) {
	defer func() {
		w.op.Lock()
		defer w.op.Unlock()
		w.mu.Lock()
		p := w.proc
		w.rec.Ready = false
		w.mu.Unlock()
		p.kill()
	}()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		w.op.Lock()
		if ctx.Err() != nil {
			w.op.Unlock()
			break
		}
		w.mu.Lock()
		owner := w.rec.Owner
		ready := w.rec.Ready && w.proc.alive()
		w.mu.Unlock()
		if ready && owner != nil {
			leaseCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			ok, e := w.s.q.LeaseOwned(leaseCtx, owner.LeaseToken)
			cancel()
			if !ok || e != nil {
				ready = false
			}
		}
		if !ready {
			_ = w.restart(ctx, "")
		}
		w.op.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
func sameOwner(a, b *assignment) bool {
	return a != nil && b != nil && a.TaskID == b.TaskID && a.AttemptID == b.AttemptID && a.LeaseToken == b.LeaseToken && a.RunnerID == b.RunnerID
}
func (w *worker) prepare(ctx context.Context, a assignment) (int64, error) {
	w.op.Lock()
	defer w.op.Unlock()
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || !protocol.ValidID(a.RunnerID) || !strings.HasPrefix(a.LeaseToken, a.RunnerID+"/"+a.TaskID+"/"+a.AttemptID+"/") {
		return 0, errors.New("invalid owner")
	}
	ok, e := w.s.q.LeaseOwned(ctx, a.LeaseToken)
	if e != nil || !ok {
		return 0, errors.New("lease expired")
	}
	w.mu.Lock()
	ready := w.rec.Ready && w.proc.alive()
	owner := w.rec.Owner
	prepared, previousGeneration := w.rec.Prepared, w.rec.Generation
	w.mu.Unlock()
	if !ready {
		return 0, errors.New("engine not ready")
	}
	if sameOwner(owner, &a) && prepared {
		if !reflect.DeepEqual(owner.Inputs, a.Inputs) {
			return 0, errors.New("immutable worker manifest changed")
		}
		return previousGeneration, nil
	}
	if owner != nil && !sameOwner(owner, &a) {
		if e = w.restart(ctx, ""); e != nil {
			return 0, e
		}
	}
	// Persist ownership even before downloads: cleanup covers interrupted prepare.
	w.mu.Lock()
	a.Generation = w.rec.Generation
	w.rec.Owner = &a
	w.rec.Prepared = false
	w.rec.PreparedAt = timestamp()
	w.rec.LogCursors = engineLogCursors(filepath.Join(w.s.c.work, "engine"))
	e = w.persist()
	gen := w.rec.Generation
	w.mu.Unlock()
	if e != nil {
		return 0, e
	}
	if e = save(filepath.Join(w.s.taskDir(a.TaskID), "owner.json"), a); e != nil {
		return 0, e
	}
	if e = w.initializeRequestCapture(a); e != nil {
		return 0, e
	}
	if e = w.initializeThreadObservations(a); e != nil {
		return 0, e
	}
	for _, b := range a.Inputs {
		if !protocol.ValidID(b.NodeID) {
			return 0, errors.New("invalid binding")
		}
		dst := w.s.csvPath(a.TaskID, b.NodeID)
		if e = directory(filepath.Dir(dst)); e != nil {
			return 0, e
		}
		if e = w.s.s.Download(ctx, b.Ref, dst); e != nil {
			return 0, e
		}
		if e = w.s.grantInput(a.TaskID, dst); e != nil {
			return 0, e
		}
	}
	if ok, e = w.s.q.LeaseOwned(ctx, a.LeaseToken); !ok || e != nil {
		return 0, errors.New("lease expired while preparing")
	}
	w.mu.Lock()
	w.rec.Prepared = true
	e = w.persist()
	w.mu.Unlock()
	return gen, e
}
func (w *worker) reset(ctx context.Context, a assignment) (map[string]any, error) {
	w.op.Lock()
	defer w.op.Unlock()
	w.mu.Lock()
	owner, gen, prior, priorOwner := w.rec.Owner, w.rec.Generation, w.rec.LastReset, w.rec.LastResetOwner
	w.mu.Unlock()
	if !protocol.ValidID(a.OperationID) || !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || !protocol.ValidID(a.RunnerID) || a.LeaseToken == "" {
		return nil, errors.New("invalid reset operation")
	}
	out := func(id string) map[string]any {
		v := map[string]any{"id": w.s.c.id, "operation_id": id, "state": "COMPLETED", "generation": gen}
		if evidence := w.publishThreadObservations(ctx, a); evidence != nil {
			v["node_evidence"] = evidence
		} else {
			v["node_evidence"] = missingNodeEvidence(w.s.c.id, a.TaskID, a.AttemptID, a.Generation)
		}
		return v
	}
	if prior == a.OperationID && sameOwner(priorOwner, &a) && (a.Generation == 0 || a.Generation == priorOwner.Generation) {
		return out(prior), nil
	}
	if !sameOwner(owner, &a) || a.Generation != 0 && a.Generation != gen {
		return out("superseded"), nil
	}
	if e := w.restart(ctx, a.OperationID); e != nil {
		return nil, fmt.Errorf("reset failed: %w", e)
	}
	gen++
	return out(a.OperationID), nil
}

func (w *worker) cleanup(a assignment) error {
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) {
		return errors.New("invalid owner")
	}
	w.op.Lock()
	defer w.op.Unlock()
	w.mu.Lock()
	owner := w.rec.Owner
	w.mu.Unlock()
	if owner != nil && owner.TaskID == a.TaskID {
		return errors.New("task still active")
	}
	dir := w.s.taskDir(a.TaskID)
	var saved assignment
	e := load(filepath.Join(dir, "owner.json"), &saved)
	if os.IsNotExist(e) {
		removeEvidence(w, a)
		return nil
	}
	if e != nil {
		return e
	}
	if !sameOwner(&a, &saved) {
		return nil
	}
	if e = os.RemoveAll(dir); e != nil {
		return e
	}
	removeEvidence(w, a)
	return nil
}

func registryBound(path string) bool {
	f, e := openRegular(path)
	if e != nil {
		return false
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return false
	}
	if info.Size() > 64<<10 {
		_, _ = f.Seek(info.Size()-(64<<10), io.SeekStart)
	}
	b, e := io.ReadAll(io.LimitReader(f, 64<<10))
	return e == nil && bytes.Contains(b, []byte("Bound to RMI registry on port 1099"))
}

func (c config) rmiPortsReady(ports ...string) bool {
	for _, port := range ports {
		sock, e := net.DialTimeout("tcp", net.JoinHostPort(c.id, port), 300*time.Millisecond)
		if e != nil {
			return false
		}
		sock.Close()
	}
	return true
}
