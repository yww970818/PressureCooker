package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func TestProjectHandleClosePreservesProcessDependencies(t *testing.T) {
	p := &Platform{dependencies: &platformDependencies{}}
	nodeRedisFixture(t, p)
	defer p.Close()
	if e := p.forProject("project-a").Close(); e != nil {
		t.Fatal(e)
	}
	q, e := p.forProject("project-b").runnerQueue()
	if e != nil {
		t.Fatal("closing a borrowed project killed the process pool:", e)
	}
	defer q.Close()
	if e = q.Ping(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestQueueConfigurationChangeCannotReadPreviousNamespace(t *testing.T) {
	p := &Platform{dependencies: &platformDependencies{}}
	nodeRedisFixture(t, p)
	defer p.Close()
	q, e := p.runnerQueue()
	if e != nil {
		t.Fatal(e)
	}
	r := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller")}
	if _, e = q.StoreNodeReport(context.Background(), r, time.Now().UnixMilli()); e != nil {
		t.Fatal(e)
	}
	other := p.forProject("project-b")
	other.Config.RedisPrefix += "-other"
	otherQueue, e := other.runnerQueue()
	if e != nil {
		return
	} // Explicit immutable-config rejection is safe.
	got, e := otherQueue.NodeReports(context.Background(), []string{"controller-a"})
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 0 {
		t.Fatal("changed dependency configuration reused the previous Redis namespace")
	}
}
func TestObjectStoreConnectionsReusedAcrossRequests(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.Header().Set("X-Amz-Meta-Sha256", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
		w.WriteHeader(200)
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	p := &Platform{Config: Config{ArchiveEndpoint: server.URL, ArchiveBucket: "fixture", ArchiveAccessKey: "fixture", ArchiveSecretKey: "fixture"}, dependencies: &platformDependencies{}}
	defer p.Close()
	ref := protocol.ObjectRef{Bucket: "fixture", Key: "test/data", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Size: 0}
	for i := 0; i < 4; i++ {
		store, e := p.forProject("project-a").objectStore()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.Head(context.Background(), ref); e != nil {
			t.Fatal(e)
		}
	}
	if n := connections.Load(); n != 1 {
		t.Fatalf("opened %d object-store connections for repeated requests, want one reusable connection", n)
	}
}
