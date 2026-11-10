package main

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestGracefulStopUsesShutdownAndForceEscalates(t *testing.T) {
	listener, err := net.ListenPacket("udp", "127.0.0.1:4445")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r := runner{active: &activeJob{id: "run-stop", attempt: "attempt-stop", state: "RUNNING", cancel: cancel}}
	defer func() {
		if r.active.stopTimer != nil {
			r.active.stopTimer.Stop()
		}
	}()
	if r.stopMode("run-stop", "attempt-other", true) {
		t.Fatal("foreign attempt stopped active process")
	}
	if !r.stopMode("run-stop", "attempt-stop", true) {
		t.Fatal("graceful stop rejected")
	}
	if err = listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 100)
	n, _, err := listener.ReadFrom(raw)
	if err != nil || string(raw[:n]) != "Shutdown" {
		t.Fatal("official graceful command missing", string(raw[:n]), err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("graceful stop killed active samples immediately")
	default:
	}
	timer := r.active.stopTimer
	if timer == nil || !r.stopMode("run-stop", "attempt-stop", true) || timer != r.active.stopTimer {
		t.Fatal("repeated graceful request reset stop budget")
	}
	if !r.stopMode("run-stop", "attempt-stop", false) || context.Cause(ctx) != errStop {
		t.Fatal("force stop did not escalate immediately")
	}
}

func TestStopBeforeRunningCancelsOwnedAssignment(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r := runner{active: &activeJob{id: "run-prepare", attempt: "attempt-prepare", state: "PREPARING", cancel: cancel}}
	if r.stopMode("run-other", "", false) {
		t.Fatal("unowned task stopped active assignment")
	}
	if !r.stopMode("run-prepare", "attempt-prepare", true) || context.Cause(ctx) != errStop || r.active.stopTimer != nil {
		t.Fatal("preparing assignment did not cancel immediately")
	}
}
