package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Repeated report/page/health operations must reuse the process Redis pool,
// including handlers scoped to different projects. Closing a request's handle
// must not close the next request's dependency.
func TestMySQLRedisPoolReusedAcrossProjectRequests(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	observer := redis.NewClient(&redis.Options{Addr: p.Config.RedisAddr})
	defer observer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connections := func() int {
		info, err := observer.Info(ctx, "stats").Result()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(info, "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "total_connections_received:"); ok {
				n, e := strconv.Atoi(value)
				if e != nil {
					t.Fatal(e)
				}
				return n
			}
		}
		t.Fatal("missing connection counter")
		return 0
	}
	before := connections()
	for i := 0; i < 4; i++ {
		scoped := p.forProject("project-fixture-" + strconv.Itoa(i))
		q, err := scoped.runnerQueue()
		if err != nil {
			t.Fatal(err)
		}
		if err = q.Ping(ctx); err != nil {
			t.Fatal(err)
		}
		if err = q.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if delta := connections() - before; delta > 1 {
		t.Fatalf("request-scoped queue clients opened %d connections; process pool must be reused", delta)
	}
}

func TestMySQLPlatformCloseReleasesSharedPoolAndRejectsNewRequests(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	peer, err := newPlatform(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	q, err := peer.forProject("project-fixture").runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err = peer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = q.Ping(ctx); err == nil {
		t.Fatal("process shutdown left its Redis pool usable")
	}
	if _, err = peer.forProject("project-after-close").runnerQueue(); err == nil {
		t.Fatal("closed platform recreated its Redis dependency")
	}
	if err = peer.Close(); err != nil {
		t.Fatal("platform close must be idempotent")
	}
}
