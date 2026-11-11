package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"pressurecooker/execution/queue"
)

// Relay the real isolated Redis protocol, but hold XADD until the test releases
// it. No credentials or task bytes enter diagnostics.
func heldRedisPublish(t *testing.T, target string) (string, <-chan struct{}, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	seen, release := make(chan struct{}), make(chan struct{})
	var seenOnce, releaseOnce sync.Once
	var mu sync.Mutex
	var sockets []net.Conn
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range sockets {
			c.Close()
		}
	})
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			upstream, err := net.DialTimeout("tcp", target, time.Second)
			if err != nil {
				client.Close()
				continue
			}
			mu.Lock()
			sockets = append(sockets, client, upstream)
			mu.Unlock()
			go func() { _, _ = io.Copy(client, upstream) }()
			go func() {
				defer client.Close()
				defer upstream.Close()
				buf := make([]byte, 32*1024)
				for {
					n, err := client.Read(buf)
					if n > 0 {
						if bytes.Contains(bytes.ToLower(buf[:n]), []byte("xadd")) {
							seenOnce.Do(func() { close(seen) })
							<-release
						}
						if _, e := upstream.Write(buf[:n]); e != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String(), seen, unblock
}

func TestDispatchNetworkDoesNotLockStoppedRun(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run, _, err := p.createRun(Obj{"name": "slow dispatch", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a")}, "slow-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	id := str(run["id"])
	realRedis := p.Config.RedisAddr
	proxy, seen, release := heldRedisPublish(t, realRedis)
	p.Config.RedisAddr = proxy
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	published := make(chan error, 1)
	go func() { published <- p.publishDispatches(ctx) }()
	select {
	case <-seen:
	case <-time.After(4 * time.Second):
		release()
		t.Fatal("publisher did not reach Redis")
	}
	stopped := make(chan error, 1)
	go func() { _, e := p.stopRun(id, "now"); stopped <- e }()
	blocked := false
	select {
	case err = <-stopped:
	case <-time.After(350 * time.Millisecond):
		blocked = true
	}
	release()
	if e := <-published; e != nil {
		t.Fatal(e)
	}
	if blocked {
		err = <-stopped
	}
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("stopping a queued run waited for Redis while its row was locked")
	}
	var status string
	if e := p.DB.QueryRow("SELECT status FROM task_dispatches WHERE id=?", id).Scan(&status); e != nil || status != "CANCELLED" {
		t.Fatalf("late publish confirmation: status=%s err=%v", status, e)
	}
	p.Config.RedisAddr = realRedis
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	lease := queue.Token("controller-a", id, str(run["attempt_id"]), newID(""))
	if owned, e := q.Acquire(ctx, lease); e != nil || !owned {
		t.Fatal("isolated execution lease", e)
	}
	defer q.Release(context.Background(), lease)
	claim, err := p.runnerClaim(ctx, "controller-a", Obj{"task_id": id, "attempt_id": run["attempt_id"], "lease_token": lease})
	if err != nil || claim["execute"] != false || claim["status"] != "STOPPED" {
		t.Fatal("late queue message authorized execution", err)
	}
}

func TestRunListRequestCancellationStopsPoolWait(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	conn, err := p.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	timer := time.AfterFunc(500*time.Millisecond, func() { conn.Close() })
	defer timer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/runs", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer admin-secret")
	r.Header.Set("X-Project-ID", p.projectID())
	w := httptest.NewRecorder()
	start := time.Now()
	Handler{p}.ServeHTTP(w, r)
	if time.Since(start) > 350*time.Millisecond || w.Code != 504 {
		t.Fatalf("canceled list waited for connection: elapsed=%s status=%d", time.Since(start), w.Code)
	}
}

func TestDispatchClaimExpiryAndConfirmationFencing(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run, _, err := p.createRun(Obj{"name": "claim recovery", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a")}, "claim-recovery")
	if err != nil {
		t.Fatal(err)
	}
	id := str(run["id"])
	ctx := context.Background()
	// Concurrent managers may scan the same ID; only one may own its deadline.
	var wg sync.WaitGroup
	claims := make(chan dispatchClaim, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ok, e := p.claimDispatch(ctx, id)
			if e != nil {
				failures <- e
			}
			if ok {
				claims <- c
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	if len(claims) != 1 {
		t.Fatalf("owners=%d", len(claims))
	}
	first := <-claims
	if first.Attempt != 1 {
		t.Fatalf("initial attempt=%d", first.Attempt)
	}
	if _, err = p.DB.Exec("UPDATE task_dispatches SET next_attempt_at=0 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	second, ok, err := p.claimDispatch(ctx, id)
	if err != nil || !ok || second.Attempt != 2 {
		t.Fatalf("expired claim: %v %v %d", err, ok, second.Attempt)
	}
	// A delayed success from the crashed/old owner cannot acknowledge a newer claim.
	if state, e := p.finishDispatch(ctx, first, ""); e != nil || state != "" {
		t.Fatalf("stale confirmation: %q %v", state, e)
	}
	if state, e := p.finishDispatch(ctx, second, "redis_unavailable"); e != nil || state != "PENDING" {
		t.Fatalf("retry confirmation: %q %v", state, e)
	}
	var attempts int
	var due float64
	var status, lastError string
	if err = p.DB.QueryRow("SELECT status,attempts,next_attempt_at,last_error FROM task_dispatches WHERE id=?", id).Scan(&status, &attempts, &due, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != "PENDING" || attempts != 2 || due <= epoch() || lastError != "redis_unavailable" {
		t.Fatal("retry lost durable intent or backoff")
	}
	// Prefix-aware context operations must read/update only this fixture's tables.
	if err = p.transactionContext(ctx, func(tx *Tx) error {
		rows, e := tx.QueryContext(ctx, "SELECT id FROM task_dispatches WHERE id=?", id)
		if e != nil {
			return e
		}
		defer rows.Close()
		if !rows.Next() {
			return fmt.Errorf("prefixed transaction missed dispatch")
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = p.claimDispatch(cancelled, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestDispatchUnconfirmedPublishRecoversAndInvalidPayloadFailsSafely(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run, _, err := p.createRun(Obj{"name": "unconfirmed", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a")}, "unconfirmed")
	if err != nil {
		t.Fatal(err)
	}
	id := str(run["id"])
	first, ok, err := p.claimDispatch(context.Background(), id)
	if err != nil || !ok {
		t.Fatal("claim", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.finishDispatch(cancelled, first, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("unconfirmed publish was accepted: %v", err)
	}
	if _, err = p.DB.Exec("UPDATE task_dispatches SET next_attempt_at=0 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err = p.publishDispatches(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err = p.DB.QueryRow("SELECT status,attempts FROM task_dispatches WHERE id=?", id).Scan(&status, &attempts); err != nil || status != "PUBLISHED" || attempts != 2 {
		t.Fatalf("recovered confirmation: %s %d %v", status, attempts, err)
	}
	if _, err = p.stopRun(id, "now"); err != nil {
		t.Fatal(err)
	}
	invalid, _, err := p.createRun(Obj{"name": "invalid snapshot", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a")}, "invalid-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("UPDATE task_dispatches SET payload='sensitive-invalid-ciphertext' WHERE id=?", invalid["id"]); err != nil {
		t.Fatal(err)
	}
	if err = p.publishDispatches(context.Background()); err != nil {
		t.Fatal(err)
	}
	var code string
	if err = p.DB.QueryRow("SELECT status,last_error FROM task_dispatches WHERE id=?", invalid["id"]).Scan(&status, &code); err != nil || status != "FAILED" || code != "dispatch_decryption_failed" {
		t.Fatalf("invalid payload: %s %s %v", status, code, err)
	}
}

func TestCookieAuthenticationCancellationStopsPoolWait(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	conn, err := p.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	timer := time.AfterFunc(500*time.Millisecond, func() { conn.Close() })
	defer timer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "/api/runs", nil).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: "jmeter_session", Value: "private-session"})
	w := httptest.NewRecorder()
	start := time.Now()
	Handler{p}.ServeHTTP(w, r)
	if w.Code != 504 || time.Since(start) > 350*time.Millisecond {
		t.Fatalf("cookie cancellation: status=%d elapsed=%s", w.Code, time.Since(start))
	}
}

func TestTransactionContextCancellationRollsBackWrites(t *testing.T) {
	p, _ := mysqlFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	id := newID("rolled-back-")
	start := time.Now()
	err := p.transactionContext(ctx, func(tx *Tx) error {
		if _, e := tx.ExecContext(ctx, "INSERT INTO audit(created_at,action,object_id) VALUES(?,?,?)", now(), "cancelled.transaction", id); e != nil {
			return e
		}
		var value int
		return tx.QueryRowContext(ctx, "SELECT SLEEP(2)").Scan(&value)
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("transaction deadline: elapsed=%s err=%v", time.Since(start), err)
	}
	var count int
	if err = p.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM audit WHERE object_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancelled transaction committed: count=%d err=%v", count, err)
	}
}
