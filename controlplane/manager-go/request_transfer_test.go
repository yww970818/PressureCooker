package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPlanObjectUploadHonorsRequestCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-r.Context().Done():
		case <-release:
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	defer close(release)
	p := &Platform{Config: Config{ArchiveEndpoint: server.URL, ArchiveBucket: "fixture", ArchiveAccessKey: "fixture", ArchiveSecretKey: "fixture", ArchiveTimeout: 5 * time.Second}, DB: &Database{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scoped := p.forRequest(ctx, defaultProjectID)
	done := make(chan error, 1)
	go func() {
		_, err := scoped.putPlanObject(Obj{"sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}, "")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upload did not reach storage")
	}
	cancel()
	select {
	case err := <-done:
		if status, code, _ := requestFailure(err); status != 499 || code != "request_cancelled" {
			t.Fatalf("cancelled transfer mapped to %d/%s, want 499/request_cancelled", status, code)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("request cancellation did not stop object upload")
	}
}

func TestObjectCancellationPreservesHTTPErrorMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("already cancelled request reached storage") }))
	defer server.Close()
	p := &Platform{Config: Config{ArchiveEndpoint: server.URL, ArchiveBucket: "fixture", ArchiveAccessKey: "fixture", ArchiveSecretKey: "fixture"}}
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{{"cancelled", 499, "request_cancelled"}, {"deadline", 504, "request_timeout"}} {
		t.Run(tc.name, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if tc.name == "cancelled" {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer cancel()
			_, err := p.forRequest(ctx, defaultProjectID).putPlanObject(Obj{"sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}, "")
			status, code, _ := requestFailure(err)
			if status != tc.status || code != tc.code {
				t.Fatalf("HTTP %d/%s, want %d/%s", status, code, tc.status, tc.code)
			}
		})
	}
}
