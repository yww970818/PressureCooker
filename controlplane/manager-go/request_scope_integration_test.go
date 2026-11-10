package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

// This detail path uses a legacy non-Context store method. Binding its database
// view must make the request deadline cancel a SQL pool wait without global harm.
func TestMySQLRequestScopeLegacyReadHonorsDeadline(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	held, e := p.DB.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/plans/"+str(plan["id"]), nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+p.Config.AdminToken)
	req.Header.Set("X-Project-ID", p.projectID())
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); Handler{p}.ServeHTTP(response, req) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		held.Close()
		<-done
		t.Fatal("request deadline did not cancel a legacy repository pool wait")
	}
	if response.Code != 504 {
		t.Fatalf("HTTP %d, want request timeout", response.Code)
	}
	if p.requestContext().Err() != nil {
		t.Fatal("request cancellation leaked into global platform")
	}
}
