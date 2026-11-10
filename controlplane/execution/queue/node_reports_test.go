package queue

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func isolatedNodeRedis(t *testing.T) *Client {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("local redis-server unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cmd := exec.Command(binary, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--loglevel", "warning")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	q, err := New(Config{Addr: fmt.Sprintf("127.0.0.1:%d", port), Prefix: "node-report-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for q.Ping(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("isolated redis did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return q
}
func nodeReportFixture(now time.Time) protocol.CoreNodeReport {
	connected := true
	return protocol.CoreNodeReport{NodeReport: protocol.NodeReport{Version: 1, ID: "controller-a", Role: "controller", DisplayName: "PressureCooker-Core-1", BootID: strings.Repeat("a", 32), BootStartedAtMS: now.Add(-time.Second).UnixMilli(), Sequence: 1, ObservedAt: now.Format(time.RFC3339Nano), QueueConnected: &connected, Status: "idle", Healthy: true, Machine: protocol.NodeMachine{ResourceScope: "unknown"}}, Workers: []protocol.NodeReport{}, ObservationEpoch: protocol.NodeObservationEpoch("")}
}
func TestNodeReportsAtomicallyFenceBootAndSequenceWithoutRefreshingOldPackets(t *testing.T) {
	q := isolatedNodeRedis(t)
	ctx := context.Background()
	now := time.Now()
	report := nodeReportFixture(now)
	if ok, err := q.StoreNodeReport(ctx, report, now.UnixMilli()); err != nil || !ok {
		t.Fatal("first registration rejected", ok, err)
	}
	keys := q.db.Keys(ctx, q.prefix+":*").Val()
	if len(keys) != 1 {
		t.Fatal("node report did not use one isolated key", keys)
	}
	ttl := q.db.PTTL(ctx, keys[0]).Val()
	if ttl < 23*time.Hour || ttl > 24*time.Hour {
		t.Fatal("wrong report retention", ttl)
	}
	q.db.PExpire(ctx, keys[0], time.Minute)
	if ok, err := q.StoreNodeReport(ctx, report, now.Add(time.Millisecond).UnixMilli()); err != nil || ok {
		t.Fatal("duplicate report refreshed", ok, err)
	}
	if ttl = q.db.PTTL(ctx, keys[0]).Val(); ttl > time.Minute {
		t.Fatal("rejected packet refreshed TTL", ttl)
	}
	stored, err := q.NodeReports(ctx, []string{report.ID, "controller-b"})
	if err != nil || len(stored) != 1 || stored[report.ID].ReceivedAtMS != now.UnixMilli() {
		t.Fatal("duplicate changed server receive time", stored, err)
	}
	report.Sequence = 2
	if ok, err := q.StoreNodeReport(ctx, report, now.Add(time.Second).UnixMilli()); err != nil || !ok {
		t.Fatal("fresh sequence rejected", ok, err)
	}
	older := report
	older.BootID = strings.Repeat("b", 32)
	older.BootStartedAtMS--
	older.Sequence = 999
	if ok, err := q.StoreNodeReport(ctx, older, now.Add(2*time.Second).UnixMilli()); err != nil || ok {
		t.Fatal("old boot overwrote current registration", ok, err)
	}
	newer := report
	newer.BootID = strings.Repeat("c", 32)
	newer.BootStartedAtMS++
	newer.Sequence = 1
	if ok, err := q.StoreNodeReport(ctx, newer, now.Add(2*time.Second).UnixMilli()); err != nil || !ok {
		t.Fatal("new boot rejected", ok, err)
	}
	if ok, err := q.StoreNodeReport(ctx, report, now.Add(3*time.Second).UnixMilli()); err != nil || ok {
		t.Fatal("old process reclaimed node identity", ok, err)
	}
	stored, err = q.NodeReports(ctx, []string{report.ID})
	if err != nil || stored[report.ID].Report.BootID != newer.BootID || stored[report.ID].Report.ObservationEpoch != newer.ObservationEpoch {
		t.Fatal("parent envelope or epoch lost", stored, err)
	}
	if _, err = q.NodeReports(ctx, []string{"../bad"}); err == nil {
		t.Fatal("invalid node ID read")
	}
}
func TestNodeObservationEpochNeverRevealsLeaseAndChangesWithOwnership(t *testing.T) {
	q := isolatedNodeRedis(t)
	ctx := context.Background()
	value, err := q.NodeObservationEpoch(ctx)
	if err != nil || value != protocol.NodeObservationEpoch("") {
		t.Fatal("missing lease epoch incorrect", value, err)
	}
	if ok, err := q.Acquire(ctx, "fixture-private-lease"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	value, err = q.NodeObservationEpoch(ctx)
	if err != nil || value != protocol.NodeObservationEpoch("fixture-private-lease") || strings.Contains(value, "private") {
		t.Fatal("lease epoch leaked or did not change", value, err)
	}
}

func TestNodeReportsConcurrentSequencesCannotClobberLatest(t *testing.T) {
	q := isolatedNodeRedis(t)
	ctx := context.Background()
	now := time.Now()
	var wait sync.WaitGroup
	for sequence := int64(1); sequence <= 64; sequence++ {
		wait.Add(1)
		go func(sequence int64) {
			defer wait.Done()
			report := nodeReportFixture(now)
			report.Sequence = sequence
			if _, err := q.StoreNodeReport(ctx, report, now.UnixMilli()+sequence); err != nil {
				t.Error(err)
			}
		}(sequence)
	}
	wait.Wait()
	stored, err := q.NodeReports(ctx, []string{"controller-a"})
	if err != nil || stored["controller-a"].Report.Sequence != 64 || stored["controller-a"].ReceivedAtMS != now.UnixMilli()+64 {
		t.Fatal("parallel report overwrote newer evidence", stored, err)
	}
}
