package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Scheduler struct {
	P              *Platform
	ID             string
	leader         bool
	lock           *os.File
	closed         bool
	recoveryCursor string
}

func newScheduler(p *Platform, id string) *Scheduler {
	return &Scheduler{P: p, ID: id}
}
func (s *Scheduler) acquire() (bool, error) {
	if s.closed {
		return false, nil
	}
	if s.leader {
		return true, nil
	}
	f, e := os.OpenFile(filepath.Join(s.P.Config.StateDir, "scheduler.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if e != nil {
		return false, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		if e == syscall.EWOULDBLOCK {
			return false, nil
		}
		return false, e
	}
	s.lock = f
	s.leader = true
	return true, s.P.audit("scheduler.leader", s.ID)
}
func (s *Scheduler) close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.lock != nil {
		syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		s.lock.Close()
		s.lock = nil
	}
	s.leader = false
	s.P.heartbeat(s.ID, false)
}
func (s *Scheduler) loop(ctx context.Context) {
	defer s.close()
	for {
		e := s.tickContext(ctx)
		s.P.recordLoop("scheduler", e)
		if e != nil {
			log.Printf("scheduler reconciliation failed: %T", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.P.Config.Tick):
		}
	}
}
func (s *Scheduler) tickContext(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if s.closed {
		return nil
	}
	leader, e := s.acquire()
	if e != nil {
		return e
	}
	if leader {
		runs, e := s.P.currentRecoveryRuns(ctx)
		if e != nil {
			return e
		}
		if e = s.reconcileRedisRunsContext(ctx, runs); e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("runner owner reconciliation failed: %T", e)
		}
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if e = s.P.heartbeat(s.ID, s.leader); e != nil {
		return e
	}
	return atomicWrite(filepath.Join(s.P.Config.StateDir, "heartbeat-"+s.ID+".json"), encoded(Obj{"id": s.ID, "leader": s.leader, "pid": os.Getpid(), "epoch": epoch(), "heartbeat_at": now()}))
}

// Report indexes are shared by the current owner recovery and node views.
func reportedStatusByID(values []any) map[string]Obj {
	result := map[string]Obj{}
	for _, value := range values {
		status := obj(value)
		if id := str(status["id"]); id != "" {
			result[id] = status
		}
	}
	return result
}
