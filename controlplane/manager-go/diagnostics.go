package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type loopProgress struct {
	At     time.Time
	Failed bool
}

// Project-scoped Platform copies share process observations through this pointer.
// No task payload, dependency address or error text is retained here.
type platformRuntime struct {
	mu                         sync.Mutex
	loops                      map[string]loopProgress
	published, retried, failed atomic.Uint64
}

func (p *Platform) recordLoop(name string, err error) {
	if p.runtime == nil {
		return
	}
	p.runtime.mu.Lock()
	defer p.runtime.mu.Unlock()
	if p.runtime.loops == nil {
		p.runtime.loops = map[string]loopProgress{}
	}
	p.runtime.loops[name] = loopProgress{time.Now(), err != nil}
}
func (p *Platform) recordDispatch(outcome string) {
	if p.runtime == nil {
		return
	}
	switch outcome {
	case "":
		p.runtime.published.Add(1)
	case "redis_unavailable":
		p.runtime.retried.Add(1)
	default:
		p.runtime.failed.Add(1)
	}
}
func (p *Platform) loopChecks(at time.Time) Obj {
	checks := Obj{}
	schedulerTTL := p.Config.WatchdogTimeout
	if schedulerTTL <= 0 {
		schedulerTTL = 90 * time.Second
	}
	schedulerTTL = max(schedulerTTL, 3*p.Config.Tick)
	for name, ttl := range map[string]time.Duration{"dispatcher": 30 * time.Second, "scheduler": schedulerTTL} {
		state := "starting"
		if p.runtime != nil {
			p.runtime.mu.Lock()
			progress, exists := p.runtime.loops[name]
			p.runtime.mu.Unlock()
			if exists {
				switch {
				case at.Sub(progress.At) > ttl:
					state = "stale"
				case progress.Failed:
					state = "failed"
				default:
					state = "ok"
				}
			}
		}
		checks[name] = state
	}
	return checks
}
func (p *Platform) readiness(parent context.Context) Obj {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	checks := p.loopChecks(time.Now())
	checks["mysql"], checks["redis"] = "unavailable", "unavailable"
	// Both probes share one deadline. An exhausted pool must not leave readiness
	// waiting indefinitely, and Redis honours the same context deadline.
	if p.DB != nil && p.DB.PingContext(ctx) == nil {
		checks["mysql"] = "ok"
	}
	if q, e := p.runnerQueue(); e == nil {
		if q.Ping(ctx) == nil {
			checks["redis"] = "ok"
		}
		q.Close()
	}
	ok := true
	for _, state := range checks {
		if state != "ok" {
			ok = false
		}
	}
	return Obj{"ok": ok, "checks": checks}
}
func (p *Platform) diagnostics(parent context.Context) (Obj, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	var pending, ready, retrying, failed int
	var oldest string
	err := p.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(status='PENDING'),0),COALESCE(SUM(status='PENDING' AND next_attempt_at<=?),0),COALESCE(SUM(status='PENDING' AND attempts>0),0),COALESCE(SUM(status='FAILED'),0),COALESCE(MIN(CASE WHEN status='PENDING' THEN created_at END),'') FROM task_dispatches WHERE project_id=? AND status IN ('PENDING','FAILED')`, epoch(), p.projectID()).Scan(&pending, &ready, &retrying, &failed, &oldest)
	if err != nil {
		return nil, err
	}
	var queued, processing, retry, reportFailed int
	err = p.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(status='QUEUED'),0),COALESCE(SUM(status='PROCESSING'),0),COALESCE(SUM(status='RETRY'),0),COALESCE(SUM(status='FAILED'),0) FROM pressure_report_jobs WHERE project_id=? AND status IN ('QUEUED','PROCESSING','RETRY','FAILED')`, p.projectID()).Scan(&queued, &processing, &retry, &reportFailed)
	if err != nil {
		return nil, err
	}
	stats := p.DB.Stats()
	process := Obj{"dispatch_published": uint64(0), "dispatch_retried": uint64(0), "dispatch_failed": uint64(0)}
	if p.runtime != nil {
		process["dispatch_published"] = p.runtime.published.Load()
		process["dispatch_retried"] = p.runtime.retried.Load()
		process["dispatch_failed"] = p.runtime.failed.Load()
	}
	return Obj{
		"project_id":       p.projectID(),
		"dispatch":         Obj{"pending": pending, "due": ready, "attempted_pending": retrying, "failed": failed, "oldest_pending_at": oldest},
		"pressure_reports": Obj{"queued": queued, "processing": processing, "retry": retry, "failed": reportFailed},
		"database_pool":    Obj{"max_open": stats.MaxOpenConnections, "open": stats.OpenConnections, "in_use": stats.InUse, "idle": stats.Idle, "wait_count": stats.WaitCount, "wait_seconds": stats.WaitDuration.Seconds()},
		"loops":            p.loopChecks(time.Now()), "process": process,
	}, nil
}
