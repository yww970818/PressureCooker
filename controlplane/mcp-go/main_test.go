package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func runnerFixture(t *testing.T) (config, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	// Launchers such as Volta spawn the runtime under a different PID. Resolve
	// the executable first so the death test kills Node itself, not its launcher.
	runtimePath, err := exec.Command(node, "-p", "process.execPath").Output()
	if err != nil {
		t.Fatal("cannot resolve Node runtime", err)
	}
	node = strings.TrimSpace(string(runtimePath))
	script, err := filepath.Abs("../mcp/node_modules/jmeter-mcp-server/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(script); err != nil {
		t.Skip("upstream npm dependency unavailable")
	}
	root := t.TempDir()
	pidFile := filepath.Join(root, "pid")
	wrapper := filepath.Join(root, "node-wrapper")
	// This real process wrapper records the upstream PID before exec preserves it.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	text := "#!/bin/sh\nprintf '%s' $$ > " + quote(pidFile) + "\nexec " + quote(node) + " \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	return config{address: "127.0.0.1:0", token: "test-token", workspace: root, plans: root, node: wrapper, upstream: script, api: managerClient{}}, pidFile
}

func startRunner(t *testing.T, cfg config) (context.CancelFunc, <-chan error, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, writer); writer.Close() }()
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
		reader.Close()
	}()
	select {
	case line := <-ready:
		if !strings.HasPrefix(line, "MCP_LISTENING ") {
			t.Fatalf("server did not listen: %q", line)
		}
		return cancel, done, "http://127.0.0.1:" + strings.TrimPrefix(line, "MCP_LISTENING ")
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timed out")
	}
	return nil, nil, ""
}

func TestRunnerShutsDownHTTPAndUpstreamOnCancellation(t *testing.T) {
	cfg, pidFile := runnerFixture(t)
	cancel, done, url := startRunner(t, cfg)
	response, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner shutdown stalled")
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("upstream PID %d survived shutdown", pid)
	}
}

func TestRunnerFailsWhenUpstreamDies(t *testing.T) {
	cfg, pidFile := runnerFixture(t)
	_, done, _ := startRunner(t, cfg)
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("upstream death did not fail proxy")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy stayed alive after upstream death")
	}
}
