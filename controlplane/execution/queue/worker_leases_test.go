package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"pressurecooker/execution/protocol"
)

type scopedLeaseClient interface {
	Acquire(context.Context, string, ...string) (bool, error)
	OwnsWorkers(context.Context, string, ...string) (bool, error)
}

func scopedLeases(t *testing.T, q *Client) scopedLeaseClient {
	t.Helper()
	scoped, ok := any(q).(scopedLeaseClient)
	if !ok {
		t.Fatal("scoped worker lease operations are unavailable")
	}
	return scoped
}

func TestWorkerLeaseSetsAreExclusiveAndFailedAcquireIsAtomic(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	for _, lease := range []struct {
		token   string
		workers []string
	}{
		{"first", []string{"worker-b", "worker-a", "worker-a"}},
		{"second", []string{"worker-c"}},
	} {
		if ok, err := scoped.Acquire(ctx, lease.token, lease.workers...); err != nil || !ok {
			t.Fatalf("independent lease %s rejected: %v %v", lease.token, ok, err)
		}
	}
	if ok, err := scoped.Acquire(ctx, "overlap", "worker-d", "worker-b"); err != nil || ok {
		t.Fatalf("overlapping lease accepted: %v %v", ok, err)
	}
	if ok, err := scoped.Acquire(ctx, "remaining", "worker-d"); err != nil || !ok {
		t.Fatalf("failed acquire retained a partial set: %v %v", ok, err)
	}
	for _, check := range []struct {
		token   string
		workers []string
		want    bool
	}{
		{"first", []string{"worker-a"}, true},
		{"first", []string{"worker-b", "worker-a"}, true},
		{"first", []string{"worker-c"}, false},
		{"first", []string{"worker-a", "worker-c"}, false},
		{"second", []string{"worker-c"}, true},
		{"unknown", []string{"worker-a"}, false},
	} {
		if ok, err := scoped.OwnsWorkers(ctx, check.token, check.workers...); err != nil || ok != check.want {
			t.Fatalf("coverage %s %v: got %v %v", check.token, check.workers, ok, err)
		}
	}
	for _, token := range []string{"first", "second"} {
		if ok, err := q.LeaseOwned(ctx, token); err != nil || !ok {
			t.Fatalf("full lease not owned: %v %v", ok, err)
		}
		if ok, err := q.Renew(ctx, token); err != nil || !ok {
			t.Fatalf("full lease not renewed: %v %v", ok, err)
		}
	}
	if err := q.Release(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scoped.Acquire(ctx, "replacement", "worker-b", "worker-a"); err != nil || !ok {
		t.Fatalf("released workers unavailable: %v %v", ok, err)
	}
	if ok, err := q.LeaseOwned(ctx, "second"); err != nil || !ok {
		t.Fatalf("unrelated lease released: %v %v", ok, err)
	}
}

func TestWorkerLeaseExpiryFencesLateRenewAndRelease(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	if ok, err := scoped.Acquire(ctx, "expired", "worker-a", "worker-b"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// Expire this isolated namespace to model a dead owner without waiting 45s.
	keys, err := q.db.Keys(ctx, q.prefix+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if err := q.db.PExpire(ctx, key, time.Millisecond).Err(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if ok, err := scoped.Acquire(ctx, "new-owner", "worker-a", "worker-b"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := q.Renew(ctx, "expired"); err != nil || ok {
		t.Fatalf("stale lease renewed: %v %v", ok, err)
	}
	if err := q.Release(ctx, "expired"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scoped.OwnsWorkers(ctx, "new-owner", "worker-a", "worker-b"); err != nil || !ok {
		t.Fatalf("stale owner changed replacement: %v %v", ok, err)
	}
}

func TestWorkerLeasesRemainExclusiveWithLegacyGlobalClients(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	// This is the storage written by the previous global-lease implementation.
	if err := q.db.Set(ctx, q.prefix+":lease", "legacy", LeaseTTL).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := scoped.Acquire(ctx, "scoped", "worker-a"); err != nil || ok {
		t.Fatalf("scoped lease ignored legacy global owner: %v %v", ok, err)
	}
	if ok, err := scoped.OwnsWorkers(ctx, "legacy", "worker-a", "worker-b"); err != nil || !ok {
		t.Fatalf("legacy global coverage lost: %v %v", ok, err)
	}
	if ok, err := q.Renew(ctx, "legacy"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := q.Release(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scoped.Acquire(ctx, "scoped", "worker-a"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := q.db.SetNX(ctx, q.prefix+":lease", "old-client", LeaseTTL).Result(); err != nil || ok {
		t.Fatalf("old global client bypassed scoped lease: %v %v", ok, err)
	}
	if ok, err := scoped.Acquire(ctx, "global"); err != nil || ok {
		t.Fatalf("global lease bypassed scoped lease: %v %v", ok, err)
	}
	if err := q.Release(ctx, "scoped"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scoped.Acquire(ctx, "global"); err != nil || !ok {
		t.Fatalf("global lease unavailable after scoped release: %v %v", ok, err)
	}
	if ok, err := scoped.OwnsWorkers(ctx, "global", "worker-z"); err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestWorkerLeaseConcurrentOverlapsHaveOneWinnerAndNoPartialOwners(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	const contenders = 24
	var wait sync.WaitGroup
	start := make(chan struct{})
	results := make([]bool, contenders)
	for i := range results {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			ok, err := scoped.Acquire(ctx, fmt.Sprintf("contender-%d", i), "shared", fmt.Sprintf("private-%d", i))
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = ok
		}(i)
	}
	close(start)
	wait.Wait()
	winners := 0
	for i, won := range results {
		if won {
			winners++
			continue
		}
		if ok, err := scoped.Acquire(ctx, fmt.Sprintf("retry-%d", i), fmt.Sprintf("private-%d", i)); err != nil || !ok {
			t.Fatalf("failed contender retained private worker: %v %v", ok, err)
		}
	}
	if winners != 1 {
		t.Fatalf("overlapping leases produced %d winners", winners)
	}
}

func TestWorkerLeaseEpochReflectsCompleteActiveOwnershipDeterministically(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	empty := protocol.NodeObservationEpoch("")
	if ok, err := scoped.Acquire(ctx, "private-z", "worker-a"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	single, err := q.NodeObservationEpoch(ctx)
	if err != nil || single != protocol.NodeObservationEpoch("private-z") {
		t.Fatalf("single ownership epoch mismatch: %s %v", single, err)
	}
	if ok, err := scoped.Acquire(ctx, "private-a", "worker-b"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	combined, err := q.NodeObservationEpoch(ctx)
	if err != nil || combined == single || combined == empty || combined == protocol.NodeObservationEpoch("private-a") {
		t.Fatalf("parallel ownership was omitted from epoch: %s %v", combined, err)
	}
	if err := q.Release(ctx, "private-z"); err != nil {
		t.Fatal(err)
	}
	if err := q.Release(ctx, "private-a"); err != nil {
		t.Fatal(err)
	}
	if epoch, err := q.NodeObservationEpoch(ctx); err != nil || epoch != empty {
		t.Fatalf("empty epoch mismatch: %s %v", epoch, err)
	}
	if ok, err := scoped.Acquire(ctx, "private-a", "worker-b"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := scoped.Acquire(ctx, "private-z", "worker-a"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if epoch, err := q.NodeObservationEpoch(ctx); err != nil || epoch != combined {
		t.Fatalf("epoch depends on acquisition order: %s %v", epoch, err)
	}
}

func TestWorkerLeaseRejectsPartialOwnershipAndDuplicateToken(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	if ok, err := scoped.Acquire(ctx, "owner", "worker-a", "worker-b"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := scoped.Acquire(ctx, "owner", "worker-c"); err != nil || ok {
		t.Fatalf("token replaced its original marker: %v %v", ok, err)
	}
	// A missing original member invalidates the whole lease, including requests
	// for an otherwise intact subset and attempts to renew the remaining member.
	if err := q.db.HDel(ctx, q.prefix+":leases:workers", "worker-b").Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.LeaseOwned(ctx, "owner"); err != nil || ok {
		t.Fatalf("partial lease reported owned: %v %v", ok, err)
	}
	if ok, err := scoped.OwnsWorkers(ctx, "owner", "worker-a"); err != nil || ok {
		t.Fatalf("partial lease authorized a subset: %v %v", ok, err)
	}
	if ok, err := q.Renew(ctx, "owner"); err != nil || ok {
		t.Fatalf("partial lease renewed: %v %v", ok, err)
	}
	if epoch, err := q.NodeObservationEpoch(ctx); err != nil || epoch != protocol.NodeObservationEpoch("") {
		t.Fatalf("partial lease included in observation epoch: %s %v", epoch, err)
	}
	if err := q.Release(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scoped.Acquire(ctx, "replacement", "worker-a", "worker-b", "worker-c"); err != nil || !ok {
		t.Fatalf("partial release retained workers: %v %v", ok, err)
	}
}

func TestWorkerLeaseExpiryAndRenewalAreScopedToOriginalSet(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	for _, lease := range []struct{ token, worker string }{{"expires", "worker-a"}, {"retained", "worker-b"}} {
		if ok, err := scoped.Acquire(ctx, lease.token, lease.worker); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	identity := sha256.Sum256([]byte("expires"))
	// Move only one owner's server deadline into the past; unrelated ownership
	// and the shared Redis state must remain alive.
	if err := q.db.ZAdd(ctx, q.prefix+":leases:expires", redis.Z{Member: hex.EncodeToString(identity[:]), Score: float64(time.Now().Add(-time.Second).UnixMilli())}).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.LeaseOwned(ctx, "expires"); err != nil || ok {
		t.Fatalf("expired marker authorized workers: %v %v", ok, err)
	}
	if ok, err := q.LeaseOwned(ctx, "retained"); err != nil || !ok {
		t.Fatalf("one expiry removed another owner: %v %v", ok, err)
	}
	if ok, err := scoped.Acquire(ctx, "replacement", "worker-a"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := q.Release(ctx, "expires"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scoped.OwnsWorkers(ctx, "replacement", "worker-a"); err != nil || !ok {
		t.Fatalf("expired release removed replacement: %v %v", ok, err)
	}
	identity = sha256.Sum256([]byte("retained"))
	if err := q.db.ZAdd(ctx, q.prefix+":leases:expires", redis.Z{Member: hex.EncodeToString(identity[:]), Score: float64(time.Now().Add(100 * time.Millisecond).UnixMilli())}).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.Renew(ctx, "retained"); err != nil || !ok {
		t.Fatalf("valid marker could not renew: %v %v", ok, err)
	}
	time.Sleep(150 * time.Millisecond)
	if ok, err := scoped.OwnsWorkers(ctx, "retained", "worker-b"); err != nil || !ok {
		t.Fatalf("renew failed to extend original set: %v %v", ok, err)
	}
	if ok, err := scoped.OwnsWorkers(ctx, "replacement", "worker-a"); err != nil || !ok {
		t.Fatalf("renew changed unrelated ownership: %v %v", ok, err)
	}
}

func TestWorkerLeaseInvalidIDsDoNotReserveAnyWorkers(t *testing.T) {
	q := isolatedNodeRedis(t)
	scoped := scopedLeases(t, q)
	ctx := context.Background()
	if ok, err := scoped.Acquire(ctx, "invalid", "worker-a", "../bad"); err == nil || ok {
		t.Fatalf("invalid worker accepted: %v %v", ok, err)
	}
	if ok, err := scoped.Acquire(ctx, "", "worker-a"); err == nil || ok {
		t.Fatalf("empty token accepted: %v %v", ok, err)
	}
	if ok, err := scoped.Acquire(ctx, "valid", "worker-a"); err != nil || !ok {
		t.Fatalf("invalid request reserved workers: %v %v", ok, err)
	}
	if ok, err := scoped.OwnsWorkers(ctx, "valid", "../bad"); err == nil || ok {
		t.Fatalf("invalid coverage request accepted: %v %v", ok, err)
	}
}
