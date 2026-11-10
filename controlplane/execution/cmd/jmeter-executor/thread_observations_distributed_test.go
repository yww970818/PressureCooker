package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

// Verify real RMI serialization/classloading and independent node lifetimes.
// Both official workers run only the single built-in sleeping Java sampler.
func TestThreadObservationsOfficialRuntimeDistributedStopIsolation(t *testing.T) {
	if os.Getenv("JMETER_THREAD_OBSERVATIONS_IT") != "1" {
		t.Skip("enable isolated official JMeter RMI fixture")
	}
	home, _ := filepath.Abs("../../../../runtime/toolcache/apache-jmeter-5.6.3")
	tool, _ := filepath.Abs("../../../../tools/jmeter-thread-observer/build.sh")
	java := os.Getenv("JMETER_THREAD_TEST_JAVA")
	if java == "" {
		java = "java"
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	jar := filepath.Join(base, "pressurecooker-thread-observer.jar")
	if out, err := exec.Command("bash", tool, home, jar).CombinedOutput(); err != nil {
		t.Fatalf("observer build: %v %s", err, out)
	}
	workers := make([]*worker, 2)
	commands := make([]*exec.Cmd, 2)
	var owner assignment
	var hosts []string
	for i := range workers {
		w, a := threadWorker(t)
		owner = a
		w.s.c.id = fmt.Sprintf("worker-%d", i+1)
		workers[i] = w
		if err := w.initializeThreadObservations(a); err != nil {
			t.Fatal(err)
		}
		registry := threadFixturePort(t)
		engine := threadFixturePort(t)
		hosts = append(hosts, "127.0.0.1:"+registry)
		dir := filepath.Dir(w.s.c.work)
		cmd := exec.Command(java, "-Djava.awt.headless=true", "-Djava.rmi.server.hostname=127.0.0.1", "-Dpressurecooker.capture.tasks_root="+filepath.Join(w.s.c.work, "tasks"), "-Dpressurecooker.capture.generation="+strconv.FormatInt(a.Generation, 10), "-Dpressurecooker.capture.node_id="+w.s.c.id, "-jar", filepath.Join(home, "bin", "ApacheJMeter.jar"), "-s", "-Jsearch_paths="+jar, "-Jserver.rmi.ssl.disable=true", "-Jserver_port="+registry, "-Jserver.rmi.localport="+engine, "-Jserver.exitaftertest=false", "-j", filepath.Join(dir, "jmeter.log"))
		cmd.Dir = dir
		console, err := os.Create(filepath.Join(dir, "console.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = console
		cmd.Stderr = console
		if err = cmd.Start(); err != nil {
			console.Close()
			t.Fatal(err)
		}
		commands[i] = cmd
		defer func() { cmd.Process.Kill(); cmd.Wait(); console.Close() }()
	}
	deadline := time.Now().Add(30 * time.Second)
	for i, w := range workers {
		for {
			log, _ := os.ReadFile(filepath.Join(filepath.Dir(w.s.c.work), "jmeter.log"))
			if bytes.Contains(log, []byte("Bound to RMI registry on port")) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("worker %d RMI readiness timed out: %s", i, log)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	plan, err := injectThreadObservations([]byte(officialThreadObservationPlan), protocol.Task{TaskID: owner.TaskID, AttemptID: owner.AttemptID}, evidenceOwnerKey(owner))
	if err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(base, "plan.jmx")
	if err = os.WriteFile(planFile, plan, 0600); err != nil {
		t.Fatal(err)
	}
	jtl := filepath.Join(base, "results.jtl")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	controller := exec.CommandContext(ctx, java, "-Djava.awt.headless=true", "-Djava.rmi.server.hostname=127.0.0.1", "-jar", filepath.Join(home, "bin", "ApacheJMeter.jar"), "-n", "-Jsearch_paths="+jar, "-Jserver.rmi.ssl.disable=true", "-Jclient.rmi.localport=0", "-Jjmeter.save.saveservice.timestamp_format=ms", "-Jjmeter.save.saveservice.autoflush=true", "-t", planFile, "-R", strings.Join(hosts, ","), "-l", jtl, "-j", filepath.Join(base, "controller.log"))
	controller.Dir = base
	console, err := os.Create(filepath.Join(base, "controller-console.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	controller.Stdout = console
	controller.Stderr = console
	if err = controller.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { controller.Process.Kill(); controller.Wait() }()
	deadline = time.Now().Add(25 * time.Second)
	for {
		ready := true
		for _, w := range workers {
			points, _, _ := readThreadFixture(w, owner)
			active := 0
			for _, p := range points {
				if p.ActiveThreads == 1 {
					active++
				}
			}
			if active < 3 {
				ready = false
			}
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(base, "controller-console.log"))
			t.Fatalf("distributed periodic capture did not start: %s", log)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if raw, _ := os.ReadFile(jtl); bytes.Contains(raw, []byte("Only business result")) {
		t.Fatal("independent observations came after request completion")
	}
	if err = commands[0].Process.Kill(); err != nil {
		t.Fatal(err)
	}
	stoppedAt := time.Now().UnixMilli()
	deadline = time.Now().Add(15 * time.Second)
	for {
		_, complete, _ := readThreadFixture(workers[1], owner)
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stopping one worker stopped the other observer")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Reap both JVMs before preserving each generation, matching production reset.
	commands[0].Wait()
	commands[1].Process.Kill()
	commands[1].Wait()
	for i, w := range workers {
		w.captureEvidence(owner, "2026-11-08T00:00:00Z", nil)
		e := w.savedEvidence(owner)
		raw, err := os.ReadFile(w.threadArchiveFile(owner))
		if err != nil {
			t.Fatal(err)
		}
		var points []protocol.ThreadObservation
		complete, err := protocol.ReadThreadObservations(bytes.NewReader(raw), *e, func(p protocol.ThreadObservation) error { points = append(points, p); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if complete != (i == 1) {
			t.Fatalf("worker %d completion=%v", i, complete)
		}
		last := points[len(points)-1]
		if i == 0 && last.ActiveThreads != 1 {
			t.Fatal("killed worker fabricated a final zero")
		}
		if i == 1 {
			after := 0
			for _, p := range points {
				if p.TimestampMS > stoppedAt && p.ActiveThreads == 1 {
					after++
				}
			}
			if after < 2 || last.ActiveThreads != 0 {
				t.Fatalf("survivor did not continue independent 1Hz observations: after=%d last=%+v", after, last)
			}
			t.Logf("surviving worker retained %d active 1Hz observations after peer kill", after)
		}
		keepThreadFixture(t, fmt.Sprintf("distributed-worker-%d", i+1), raw, jtl, *e, points, 0, 0)
	}
	// The surviving worker contributes exactly its original one request result.
	deadline = time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(jtl)
		rows, parseErr := csv.NewReader(bytes.NewReader(raw)).ReadAll()
		if parseErr == nil && len(rows) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("distributed observer changed result count: rows=%d err=%v", len(rows), parseErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func threadFixturePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	return port
}

func readThreadFixture(w *worker, a assignment) ([]protocol.ThreadObservation, bool, error) {
	b, err := w.threadBinding(a)
	if err != nil {
		return nil, false, err
	}
	raw, err := os.ReadFile(filepath.Join(w.threadCaptureDir(a), b.File))
	if err != nil {
		return nil, false, err
	}
	var points []protocol.ThreadObservation
	complete, err := protocol.ReadThreadObservations(bytes.NewReader(raw), protocol.NodeEvidence{TaskID: a.TaskID, AttemptID: a.AttemptID, NodeID: w.s.c.id, Generation: a.Generation}, func(p protocol.ThreadObservation) error { points = append(points, p); return nil })
	return points, complete, err
}
