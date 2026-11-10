package main

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Serve waits for graceful HTTP drain before its caller releases process pools.
// A failed listener also cancels and joins the shutdown goroutine.
func serveHTTP(parent context.Context, server *http.Server, listener net.Listener) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if server.Shutdown(deadline) != nil {
			// Closing active connections also cancels their request contexts.
			server.Close()
		}
	}()
	err := server.Serve(listener)
	cancel()
	<-done
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
