package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMySQLClusterRequestCancellationStopsWaitingForDatabase(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	p.DB.SetMaxOpenConns(1)
	held, err := p.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", "/api/cluster", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+p.Config.AdminToken)
	done := make(chan struct{})
	waits := p.DB.Stats().WaitCount
	go func() { defer close(done); Handler{p}.ServeHTTP(httptest.NewRecorder(), request) }()
	deadline := time.Now().Add(time.Second)
	for p.DB.Stats().WaitCount == waits {
		if time.Now().After(deadline) {
			t.Fatal("cluster request did not reach the blocked database fixture")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		held.Close()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Fatal("cluster handler remained blocked after releasing fixture connection")
		}
		t.Fatal("cancelled cluster request kept waiting for a database connection")
	}
}
