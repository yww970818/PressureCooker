package main

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type observedListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
}

func (l *observedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

// Releasing process pools before this barrier would fail in-flight handlers.
func TestHTTPShutdownWaitsForInflightRequests(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	l := &observedListener{Listener: listener, closed: make(chan struct{})}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(200) })}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, l) }()
	response := make(chan error, 1)
	go func() {
		r, e := http.Get("http://" + listener.Addr().String())
		if r != nil {
			r.Body.Close()
			if r.StatusCode != 200 {
				t.Errorf("HTTP %d", r.StatusCode)
			}
		}
		response <- e
	}()
	<-entered
	cancel()
	<-l.closed
	select {
	case e := <-done:
		t.Fatalf("server finished while handler still active: %v", e)
	case <-time.After(80 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if e := <-response; e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish after draining handler")
	}
}

func TestHTTPShutdownForcesRequestCancellationAfterDrainBudget(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	entered, ended := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(ended) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, l) }()
	go func() {
		r, _ := http.Get("http://" + l.Addr().String())
		if r != nil {
			r.Body.Close()
		}
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		server.Close()
		t.Fatal("shutdown exceeded bounded drain budget")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("forced connection close did not cancel request context")
	}
}
