package main

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"pressurecooker/execution/protocol"
)

func respond(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func reject(w http.ResponseWriter, code int) {
	respond(w, code, map[string]any{"error": map[string]string{"code": "agent_error", "message": http.StatusText(code)}})
}
func (s *service) handler(w http.ResponseWriter, r *http.Request) {
	if s.c.role == "fixture" {
		fixture(w, r)
		return
	}
	if r.URL.RawPath != "" || r.URL.RawQuery != "" {
		reject(w, 400)
		return
	}
	status := func() map[string]any {
		v := s.engineStatus()
		v["id"] = s.c.id
		v["role"] = s.c.role
		v["boot_id"] = s.boot
		return v
	}
	if r.Method == "GET" && r.URL.Path == "/healthz" {
		st := status()
		respond(w, 200, map[string]any{"ok": true, "ready": st["healthy"], "id": s.c.id, "role": s.c.role})
		return
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if e != nil {
		reject(w, 413)
		return
	}
	id, sigerr := protocol.VerifyRequest(r, b, s.c.token, time.Now())
	bearer := hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.c.token))
	if sigerr != nil && !bearer {
		reject(w, 401)
		return
	}
	p := r.URL.Path
	if r.Method == "GET" && p == "/status" {
		respond(w, 200, s.statusSnapshot(r.Context()))
		return
	}
	if r.Method == "GET" && strings.HasPrefix(p, "/jobs/") && s.runner != nil {
		jobID := strings.TrimPrefix(p, "/jobs/")
		if id, ok := strings.CutSuffix(jobID, "/log"); ok {
			if !protocol.ValidID(id) {
				reject(w, 400)
				return
			}
			txt, e := s.runner.logTail(id)
			if e != nil {
				reject(w, 500)
				return
			}
			respond(w, 200, map[string]string{"text": txt})
			return
		}
		if !protocol.ValidID(jobID) {
			reject(w, 400)
			return
		}
		var j journal
		if load(s.runner.file(jobID), &j) != nil {
			reject(w, 404)
			return
		}
		respond(w, 200, map[string]any{"id": jobID, "state": j.Stage, "exit_code": j.ExitCode})
		return
	}
	if r.Method != "POST" {
		reject(w, 404)
		return
	}
	if s.worker != nil && (p == "/prepare" || p == "/reset" || p == "/cleanup") {
		var a assignment
		if json.Unmarshal(b, &a) != nil {
			reject(w, 400)
			return
		}
		if sigerr != nil || id != a.RunnerID || !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || a.LeaseToken == "" {
			reject(w, 401)
			return
		}
		if p == "/cleanup" {
			if s.worker.cleanup(a) != nil {
				reject(w, 409)
				return
			}
			respond(w, 200, map[string]bool{"cleared": true})
			return
		}
		if p == "/prepare" {
			g, e := s.worker.prepare(r.Context(), a)
			if e != nil {
				reject(w, 409)
				return
			}
			respond(w, 200, map[string]any{"generation": g})
			return
		}
		out, e := s.worker.reset(r.Context(), a)
		if e != nil {
			reject(w, 409)
			return
		}
		respond(w, 200, out)
		return
	}
	if s.runner != nil && strings.HasPrefix(p, "/runs/") && strings.HasSuffix(p, "/stop") {
		jobID := strings.TrimSuffix(strings.TrimPrefix(p, "/runs/"), "/stop")
		if !protocol.ValidID(jobID) {
			reject(w, 400)
			return
		}
		var req struct {
			Force     bool
			Mode      string
			AttemptID string `json:"attempt_id"`
		}
		if len(b) > 0 && json.Unmarshal(b, &req) != nil {
			reject(w, 400)
			return
		}
		if !s.runner.stopMode(jobID, req.AttemptID, !req.Force && req.Mode != "now") {
			reject(w, 404)
			return
		}
		respond(w, 202, map[string]any{"id": jobID, "state": "STOPPING"})
		return
	}
	reject(w, 404)
}
func fixture(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		respond(w, 200, map[string]bool{"ok": true, "ready": true})
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		reject(w, 405)
		return
	}
	switch r.URL.Path {
	case "/jmeter-smoke":
		respond(w, 200, map[string]bool{"ok": true})
	case "/login":
		respond(w, 200, map[string]any{"token": "fixture-token", "data": map[string]string{"token": "fixture-token"}})
	case "/check":
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			reject(w, 401)
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			var v map[string]any
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			d.UseNumber()
			if d.Decode(&v) == nil {
				switch x := v["id"].(type) {
				case string:
					id = x
				case json.Number:
					id = string(x)
				}
			}
		}
		if !protocol.ValidID(id) || id == "NOT_FOUND" {
			reject(w, 422)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "id": id})
	default:
		reject(w, 404)
	}
}
func run() error {
	s, e := configure()
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	var g sync.WaitGroup
	start := func(f func(context.Context)) { g.Add(1); go func() { defer g.Done(); f(ctx) }() }
	if s.c.role != "fixture" {
		defer s.q.Close()
		defer s.http.CloseIdleConnections()
		lock, e := os.OpenFile(filepath.Join(s.c.state, "agent.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		defer lock.Close()
		if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			return errors.New("agent identity already running")
		}
		if s.c.role == "worker" {
			s.worker = &worker{s: s}
			if e = load(s.worker.file(), &s.worker.rec); e != nil && !os.IsNotExist(e) {
				return errors.New("worker journal unreadable")
			}
			s.worker.rec.Ready = false
			start(s.worker.loop)
		} else {
			// No task is live before the consumer starts. Reap the legacy Core
			// identity and every reserved slot left by a previous supervisor.
			if s.c.isolated {
				for slot := 0; slot <= maxCoreRuntimeSlots; slot++ {
					if e = reapEngine(engineUID + slot); e != nil {
						return e
					}
				}
			}
			s.runner = &runner{s: s}
			if e = directory(filepath.Join(s.c.state, "jobs")); e != nil {
				return e
			}
			start(s.runner.loop)
			start(s.runner.deliveryLoop)
			start(s.reportNodes)
		}
	}
	server := http.Server{Addr: ":" + s.c.port, Handler: http.HandlerFunc(s.handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		c, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		_ = server.Shutdown(c)
	}()
	e = server.ListenAndServe()
	cancel()
	g.Wait()
	if e != nil && !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	return nil
}
func main() {
	syscall.Umask(0077)
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "executor startup failed:", e)
		os.Exit(1)
	}
}
