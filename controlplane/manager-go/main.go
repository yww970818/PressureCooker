package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if e := run(); e != nil {
		log.Printf("manager failed: %s", e)
		os.Exit(1)
	}
}
func run() error {
	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command != "" && command != "--serve" && command != "supervise" {
		return runCLI(os.Args[1:])
	}
	config, e := loadConfig()
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if command == "--serve" {
		return serve(ctx, config)
	}
	if e = os.MkdirAll(config.StateDir, 0755); e != nil {
		return e
	}
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	var wg sync.WaitGroup
	if env("JMETER_MCP_ENABLED", "0") == "1" {
		wg.Add(1)
		go func() { defer wg.Done(); supervise(ctx, []string{"/app/bin/jmeter-mcp"}, "", 0) }()
	}
	supervise(ctx, []string{executable, "--serve"}, filepath.Join(config.StateDir, "heartbeat-"+config.ManagerID+".json"), config.WatchdogTimeout)
	wg.Wait()
	return nil
}
func serve(ctx context.Context, c Config) error {
	p, e := newPlatform(c)
	if e != nil {
		return e
	}
	defer p.Close()
	scheduler := newScheduler(p, c.ManagerID)
	loopCtx, stop := context.WithCancel(ctx)
	defer stop()
	dispatchDone := make(chan struct{})
	go func() { defer close(dispatchDone); p.dispatchLoop(loopCtx) }()
	cleanupDone := make(chan struct{})
	go func() { defer close(cleanupDone); p.historyCleanupLoop(loopCtx) }()
	pressureDone := make(chan struct{})
	go func() { defer close(pressureDone); p.pressureLoop(loopCtx) }()
	done := make(chan struct{})
	go func() { defer close(done); scheduler.loop(loopCtx) }()
	server := &http.Server{Addr: net.JoinHostPort(c.Host, fmt.Sprint(c.Port)), Handler: Handler{p}, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 * 1024}
	log.Printf("Go manager listening on %s", server.Addr)
	listener, e := net.Listen("tcp", server.Addr)
	if e == nil {
		e = serveHTTP(ctx, server, listener)
	}
	stop()
	<-done
	<-dispatchDone
	<-pressureDone
	<-cleanupDone
	return e
}
func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
func terminateGroup(cmd *exec.Cmd, done <-chan error) {
	if cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
func supervise(ctx context.Context, command []string, heartbeat string, timeout time.Duration) {
	for ctx.Err() == nil {
		cmd := exec.Command(command[0], command[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if e := cmd.Start(); e != nil {
			log.Printf("child could not be started: %s", filepath.Base(command[0]))
			if !pause(ctx, 2*time.Second) {
				return
			}
			continue
		}
		started := time.Now()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		finished := false
		running := true
		for running {
			select {
			case <-ctx.Done():
				running = false
			case <-done:
				finished = true
				running = false
			case <-time.After(time.Second):
				if heartbeat != "" {
					last := started
					if raw, e := os.ReadFile(heartbeat); e == nil {
						if v, e := decode(raw); e == nil && int(number(v["pid"])) == cmd.Process.Pid {
							last = time.Unix(0, int64(number(v["epoch"])*1e9))
						}
					}
					if time.Since(last) > timeout {
						log.Printf("scheduler heartbeat expired; terminating child pid %d", cmd.Process.Pid)
						running = false
					}
				}
			}
		}
		if finished {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		} else {
			terminateGroup(cmd, done)
		}
		if !pause(ctx, 2*time.Second) {
			return
		}
	}
}
