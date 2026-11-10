package queue

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func TestIsolatedRedisLeaseStreamReclaimAndAck(t *testing.T) {
	if os.Getenv("JMETER_EXECUTION_IT") != "1" {
		t.Skip("enable isolated Redis integration")
	}
	b, e := os.ReadFile("../../../runtime/local-secrets/redis-password")
	if e != nil {
		t.Fatal(e)
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	prefix := "jmeter:it:" + hex.EncodeToString(nonce[:])
	q, e := New(Config{"127.0.0.1:26379", strings.TrimSpace(string(b)), 0, prefix})
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	ctx := context.Background()
	defer q.db.Del(ctx, q.stream("c"), prefix+":lease")
	if e = q.EnsureGroup(ctx, "c"); e != nil {
		t.Fatal(e)
	}
	s, _ := protocol.SignTask(protocol.Task{TaskID: "job", ClusterID: "c"}, "token")
	id, e := q.Publish(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	m, e := q.Read(ctx, "c", "first")
	if e != nil || len(m) != 1 || m[0].ID != id || protocol.VerifyTask(m[0].SignedTask, "token") != nil {
		t.Fatal("stream delivery failed")
	}
	m, _, e = q.Reclaim(ctx, "c", "replacement", "0-0", time.Millisecond)
	if len(m) == 0 {
		time.Sleep(5 * time.Millisecond)
		m, _, e = q.Reclaim(ctx, "c", "replacement", "0-0", time.Millisecond)
	}
	if e != nil || len(m) != 1 || m[0].ID != id {
		t.Fatal("reclaim failed")
	}
	if ok, e := q.Acquire(ctx, "old"); e != nil || !ok {
		t.Fatal("acquire failed")
	}
	if ok, _ := q.Acquire(ctx, "new"); ok {
		t.Fatal("double ownership")
	}
	if ok, _ := q.Renew(ctx, "new"); ok {
		t.Fatal("renewed someone else's lease")
	}
	q.Release(ctx, "new")
	if ok, _ := q.LeaseOwned(ctx, "old"); !ok {
		t.Fatal("foreign release")
	}
	if ok, e := q.Renew(ctx, "old"); e != nil || !ok {
		t.Fatal("renew failed")
	}
	q.Release(ctx, "old")
	if ok, _ := q.Acquire(ctx, "new"); !ok {
		t.Fatal("next lease unavailable")
	}
	q.Release(ctx, "old")
	if ok, _ := q.LeaseOwned(ctx, "new"); !ok {
		t.Fatal("stale release")
	}
	if e = q.Ack(ctx, "c", id); e != nil {
		t.Fatal(e)
	}
	p, e := q.db.XPending(ctx, q.stream("c"), Group).Result()
	if e != nil || p.Count != 0 || q.db.XLen(ctx, q.stream("c")).Val() != 0 {
		t.Fatal("secret payload retained or pending")
	}
}

// A TCP peer that accepts but never speaks Redis models a hung dependency.
func TestRedisProbeHonoursContextDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer conn.Close(); <-done }()
		}
	}()
	q, err := New(Config{Addr: listener.Addr().String(), Prefix: "test:deadline"})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err = q.Ping(ctx); err == nil {
		t.Fatal("hung Redis reported ready")
	}
	if time.Since(start) > 350*time.Millisecond {
		t.Fatalf("context deadline ignored: %s", time.Since(start))
	}
}

func TestRedisInternalWarningsDoNotExposeDialTarget(t *testing.T) {
	var output bytes.Buffer
	original := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(original)
	q, err := New(Config{Addr: "127.0.0.1:1", Password: "private-password", Prefix: "private-prefix"})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = q.Ping(ctx); err == nil {
		t.Fatal("refused endpoint reported ready")
	}
	if !strings.Contains(output.String(), "event=redis_client_warning") || strings.Contains(output.String(), "127.0.0.1") || strings.Contains(output.String(), "private-") {
		t.Fatal("Redis warning missing or sensitive dial data leaked")
	}
}
