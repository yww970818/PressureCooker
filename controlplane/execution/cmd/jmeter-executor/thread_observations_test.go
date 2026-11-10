package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"pressurecooker/execution/objectstore"
	"pressurecooker/execution/protocol"
)

func threadWorker(t *testing.T) (*worker, assignment) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &worker{s: &service{c: config{id: "worker", work: filepath.Join(base, "work"), state: filepath.Join(base, "state")}}}, assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/local-owner-secret", Generation: 3, OperationID: "reset-attempt"}
}

func TestThreadObservationsOfficialRuntimeIndependentOfSamples(t *testing.T) {
	if os.Getenv("JMETER_THREAD_OBSERVATIONS_IT") != "1" {
		t.Skip("enable isolated official JMeter lifecycle fixture")
	}
	home, err := filepath.Abs("../../../../runtime/toolcache/apache-jmeter-5.6.3")
	if err != nil {
		t.Fatal(err)
	}
	tool, err := filepath.Abs("../../../../tools/jmeter-thread-observer/build.sh")
	if err != nil {
		t.Fatal(err)
	}
	java := os.Getenv("JMETER_THREAD_TEST_JAVA")
	if java == "" {
		java = "java"
	}
	jar := filepath.Join(t.TempDir(), "pressurecooker-thread-observer.jar")
	build := exec.Command("bash", tool, home, jar)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("observer build: %v %s", err, out)
	}
	for _, mode := range []string{"normal", "kill", "wrong-generation"} {
		t.Run(mode, func(t *testing.T) {
			w, a := threadWorker(t)
			if err := w.initializeThreadObservations(a); err != nil {
				t.Fatal(err)
			}
			planTask := protocol.Task{TaskID: a.TaskID, AttemptID: a.AttemptID}
			owner := evidenceOwnerKey(a)
			plan, err := injectThreadObservations([]byte(officialThreadObservationPlan), planTask, owner)
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Dir(w.s.c.work)
			path := filepath.Join(base, "plan.jmx")
			if err = os.WriteFile(path, plan, 0600); err != nil {
				t.Fatal(err)
			}
			jtl := filepath.Join(base, "results.jtl")
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			generation := a.Generation
			if mode == "wrong-generation" {
				generation++
			}
			cmd := exec.CommandContext(ctx, java, "-Djava.awt.headless=true", "-Dpressurecooker.capture.tasks_root="+filepath.Join(w.s.c.work, "tasks"), "-Dpressurecooker.capture.generation="+strconv.FormatInt(generation, 10), "-Dpressurecooker.capture.node_id="+w.s.c.id, "-jar", filepath.Join(home, "bin", "ApacheJMeter.jar"), "-n", "-Jsearch_paths="+jar, "-Jjmeter.save.saveservice.timestamp_format=ms", "-Jsampleresult.timestamp.start=false", "-t", path, "-j", filepath.Join(base, "jmeter.log"), "-l", jtl)
			cmd.Dir = base
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if mode != "wrong-generation" {
				// Observe several active points while the only sampler is still asleep.
				deadline := time.Now().Add(20 * time.Second)
				seen := false
				for time.Now().Before(deadline) {
					b, _ := w.threadBinding(a)
					source, _ := os.ReadFile(filepath.Join(w.threadCaptureDir(a), b.File))
					active := 0
					_, parseErr := protocol.ReadThreadObservations(bytes.NewReader(source), protocol.NodeEvidence{TaskID: a.TaskID, AttemptID: a.AttemptID, NodeID: w.s.c.id, Generation: a.Generation}, func(p protocol.ThreadObservation) error {
						if p.ActiveThreads == 1 {
							active++
						}
						return nil
					})
					if parseErr == nil && active >= 3 {
						seen = true
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if !seen {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatalf("no three periodic active readings before sampler completion: %s", output.String())
				}
				if raw, _ := os.ReadFile(jtl); bytes.Contains(raw, []byte("Only business result")) {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatal("business result completed before independent observations")
				}
			}
			if mode == "kill" {
				if err = cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			err = cmd.Wait()
			if mode != "kill" && err != nil {
				t.Fatalf("official runtime failed: %v %s", err, output.String())
			}
			w.captureEvidence(a, "2026-11-08T00:00:00Z", nil)
			e := w.savedEvidence(a)
			if mode == "wrong-generation" {
				if e.ThreadObservationsStatus != "MISSING" {
					t.Fatal("wrong binding produced evidence", e.ThreadObservationsStatus)
				}
				return
			}
			source, err := os.ReadFile(w.threadArchiveFile(a))
			if err != nil {
				t.Fatal(err)
			}
			var points []protocol.ThreadObservation
			complete, err := protocol.ReadThreadObservations(bytes.NewReader(source), *e, func(p protocol.ThreadObservation) error { points = append(points, p); return nil })
			if err != nil {
				t.Fatal(err)
			}
			if len(points) < 4 || complete != (mode == "normal") {
				t.Fatalf("wrong periodic lifecycle: points=%d complete=%v", len(points), complete)
			}
			if mode == "kill" {
				if points[len(points)-1].ActiveThreads != 1 {
					t.Fatal("kill fabricated final zero")
				}
				keepThreadFixture(t, mode, source, jtl, *e, points, 0, 0)
				return
			}
			f, err := os.Open(jtl)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(f).ReadAll()
			f.Close()
			if err != nil || len(rows) != 2 {
				t.Fatalf("observer added request samples: rows=%d err=%v", len(rows), err)
			}
			endIndex := 0
			for i, name := range rows[0] {
				if name == "timeStamp" {
					endIndex = i
				}
			}
			end, _ := strconv.ParseInt(rows[1][endIndex], 10, 64)
			before := 0
			for _, p := range points {
				if p.ActiveThreads == 1 && p.TimestampMS < end {
					before++
				}
			}
			if before < 3 || points[len(points)-1].ActiveThreads != 0 {
				t.Fatalf("missing independent active/final observations: before=%d points=%+v", before, points)
			}
			t.Logf("official JMeter: %d periodic observations, %d active before the single JTL completion; final actual threads=0", len(points), before)
			keepThreadFixture(t, mode, source, jtl, *e, points, before, end)
		})
	}
}

func keepThreadFixture(t *testing.T, mode string, source []byte, jtl string, e protocol.NodeEvidence, points []protocol.ThreadObservation, before int, resultEnd int64) {
	t.Helper()
	dir := os.Getenv("JMETER_THREAD_OBSERVATIONS_ACCEPTANCE_DIR")
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("acceptance path must be absolute")
	}
	dir = filepath.Join(dir, mode)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thread-observations.csv"), source, 0600); err != nil {
		t.Fatal(err)
	}
	requestRows := 0
	if raw, err := os.ReadFile(jtl); err == nil {
		if err = os.WriteFile(filepath.Join(dir, "results.jtl"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll(); err == nil && len(rows) > 0 {
			requestRows = len(rows) - 1
		}
	}
	complete, err := protocol.ReadThreadObservations(bytes.NewReader(source), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"task_id": e.TaskID, "attempt_id": e.AttemptID, "node_id": e.NodeID, "generation": e.Generation, "prepared_at": e.PreparedAt, "interval_ms": 1000, "points": len(points), "active_points_before_only_result": before, "only_result_completed_ms": resultEnd, "jtl_request_samples": requestRows, "complete": complete, "last_active_threads": points[len(points)-1].ActiveThreads}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "metadata.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

const officialThreadObservationPlan = `<?xml version="1.0"?><jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree><TestPlan guiclass="TestPlanGui" testclass="TestPlan" testname="Thread observation fixture" enabled="true"><elementProp name="TestPlan.user_defined_variables" elementType="Arguments"><collectionProp name="Arguments.arguments"/></elementProp></TestPlan><hashTree><ThreadGroup guiclass="ThreadGroupGui" testclass="ThreadGroup" testname="Single synthetic sleeper" enabled="true"><stringProp name="ThreadGroup.on_sample_error">continue</stringProp><elementProp name="ThreadGroup.main_controller" elementType="LoopController"><boolProp name="LoopController.continue_forever">false</boolProp><stringProp name="LoopController.loops">1</stringProp></elementProp><stringProp name="ThreadGroup.num_threads">1</stringProp><stringProp name="ThreadGroup.ramp_time">0</stringProp></ThreadGroup><hashTree><JavaSampler guiclass="JavaTestSamplerGui" testclass="JavaSampler" testname="Only business result" enabled="true"><elementProp name="arguments" elementType="Arguments"><collectionProp name="Arguments.arguments"><elementProp name="SleepTime" elementType="Argument"><stringProp name="Argument.name">SleepTime</stringProp><stringProp name="Argument.value">6500</stringProp><stringProp name="Argument.metadata">=</stringProp></elementProp><elementProp name="SleepMask" elementType="Argument"><stringProp name="Argument.name">SleepMask</stringProp><stringProp name="Argument.value">0</stringProp><stringProp name="Argument.metadata">=</stringProp></elementProp></collectionProp></elementProp><stringProp name="classname">org.apache.jmeter.protocol.java.test.SleepTest</stringProp></JavaSampler><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`

func appendThreadCSV(t *testing.T, w *worker, a assignment, tail string) string {
	t.Helper()
	b, err := w.threadBinding(a)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.threadCaptureDir(a), b.File)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(tail); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestThreadObservationsInjectionLifecycleOnly(t *testing.T) {
	raw := []byte(`<jmeterTestPlan><hashTree><TestPlan testname="keep"/><hashTree><ThreadGroup testname="unchanged"/><hashTree/></hashTree></hashTree></jmeterTestPlan>`)
	got, err := injectThreadObservations(raw, protocol.Task{TaskID: "task", AttemptID: "attempt"}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(threadObserverClass)) || !bytes.Contains(got, []byte(`<ThreadGroup testname="unchanged"/>`)) || bytes.Contains(got, []byte("Sampler")) || bytes.Contains(got, []byte("SampleListener")) {
		t.Fatal("observer altered sampler graph", string(got))
	}
	if _, err = injectThreadObservations(got, protocol.Task{TaskID: "task", AttemptID: "attempt"}, strings.Repeat("a", 64)); err == nil {
		t.Fatal("duplicate observer accepted")
	}
}

func TestThreadObservationsWorkerJVMGeneration(t *testing.T) {
	args := (config{id: "worker", work: "/supervisor/work"}).threadObserverEngineArgs(7)
	joined := strings.Join(args, "\n")
	for _, want := range []string{"-Dpressurecooker.capture.generation=7", "-Dpressurecooker.capture.node_id=worker"} {
		if !strings.Contains(joined, want) {
			t.Fatal("missing trusted worker identity", want)
		}
		if strings.Index(joined, want) > strings.Index(joined, "-jar") {
			t.Fatal("identity was passed to JMeter instead of JVM")
		}
	}
}

func TestThreadObservationsPrepareRetryRepairsReadOnlyBinding(t *testing.T) {
	w, a := threadWorker(t)
	if err := w.initializeThreadObservations(a); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.threadCaptureDir(a), "binding.properties")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := w.initializeThreadObservations(a); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || !bytes.Contains(raw, []byte("generation=3\n")) {
		t.Fatal("interrupted prepare did not repair binding", err)
	}
}

func TestThreadObservationsRejectCorruptArchiveBeforeUpload(t *testing.T) {
	w, a := threadWorker(t)
	if err := w.initializeThreadObservations(a); err != nil {
		t.Fatal(err)
	}
	appendThreadCSV(t, w, a, "1000,1\n2000,0\nEND,2000\n")
	w.captureEvidence(a, "2026-11-08T00:00:00Z", nil)
	if err := os.WriteFile(w.threadArchiveFile(a), []byte("corrupted private snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	puts := 0
	store := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			rw.WriteHeader(404)
			return
		}
		puts++
		rw.WriteHeader(200)
	}))
	defer store.Close()
	var err error
	w.s.s, err = objectstore.New(objectstore.Config{Endpoint: store.URL, Bucket: "reports", AccessKey: "local", SecretKey: "local"})
	if err != nil {
		t.Fatal(err)
	}
	e := w.publishThreadObservations(context.Background(), a)
	if e.ThreadObservationsStatus != "INVALID" || e.ThreadObservations != nil || puts != 0 {
		t.Fatalf("corrupt source uploaded or remained unresolved: status=%s puts=%d", e.ThreadObservationsStatus, puts)
	}
}

func TestThreadObservationsOwnerInodeAndBounds(t *testing.T) {
	for _, mode := range []string{"complete", "interrupted", "empty", "wrong-generation", "symlink", "replaced", "hardlink", "bad-tail", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			w, a := threadWorker(t)
			if err := w.initializeThreadObservations(a); err != nil {
				t.Fatal(err)
			}
			path := appendThreadCSV(t, w, a, "1000,2\n2000,2\n")
			if err := w.initializeThreadObservations(a); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "complete":
				appendThreadCSV(t, w, a, "3000,0\nEND,3000\n")
			case "empty":
				data, _ := os.ReadFile(path)
				os.WriteFile(path, data[:bytes.Index(data, []byte("1000,2"))], 0600)
			case "wrong-generation":
				a.Generation++
			case "symlink":
				os.Rename(path, path+".old")
				os.Symlink(path+".old", path)
			case "replaced":
				data, _ := os.ReadFile(path)
				os.Rename(path, path+".old")
				os.WriteFile(path, data, 0600)
			case "hardlink":
				os.Link(path, path+".other")
			case "bad-tail":
				appendThreadCSV(t, w, a, "corrupted\n")
			case "oversized":
				appendThreadCSV(t, w, a, strings.Repeat("1", int(protocol.MaxThreadObservationBytes)))
			}
			w.captureEvidence(a, "2026-11-08T00:00:00Z", nil)
			e := w.savedEvidence(a)
			if e == nil {
				t.Fatal("no durable receipt")
			}
			want := "INVALID"
			if mode == "complete" || mode == "interrupted" {
				want = "PENDING"
			}
			if mode == "empty" || mode == "wrong-generation" {
				want = "MISSING"
			}
			if e.ThreadObservationsStatus != want || e.ThreadObservations != nil {
				t.Fatalf("status=%s want=%s ref=%v", e.ThreadObservationsStatus, want, e.ThreadObservations)
			}
			if want == "PENDING" {
				data, err := os.ReadFile(w.threadArchiveFile(a))
				if err != nil || bytes.Contains(data, []byte(a.LeaseToken)) || bytes.Contains(data, []byte(evidenceOwnerKey(a))) {
					t.Fatal("invalid archived CSV", err)
				}
			}
		})
	}
}

func TestThreadObservationsArchiveRetryAndNoStaleReuse(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			w, a := threadWorker(t)
			if err := w.initializeThreadObservations(a); err != nil {
				t.Fatal(err)
			}
			tail := "1000,1\n2000,1\n"
			if complete {
				tail += "3000,0\nEND,3000\n"
			}
			path := appendThreadCSV(t, w, a, tail)
			w.captureEvidence(a, "2026-11-08T00:00:00Z", nil)
			expected, _ := os.ReadFile(path)
			// A new prepared generation may replace the node-local binding and CSV.
			next := a
			next.Generation++
			if err := w.initializeThreadObservations(next); err != nil {
				t.Fatal(err)
			}
			appendThreadCSV(t, w, next, "9000,99\n")
			var uploaded []byte
			fail := true
			puts := 0
			store := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if r.Method == "HEAD" {
					if uploaded == nil {
						rw.WriteHeader(404)
					} else {
						sum := sha256.Sum256(uploaded)
						rw.Header().Set("Content-Length", fmt.Sprint(len(uploaded)))
						rw.Header().Set("X-Amz-Meta-Sha256", fmt.Sprintf("%x", sum))
						rw.WriteHeader(200)
					}
					return
				}
				if fail {
					rw.WriteHeader(503)
					return
				}
				puts++
				uploaded, _ = io.ReadAll(r.Body)
				rw.WriteHeader(200)
			}))
			defer store.Close()
			var err error
			w.s.s, err = objectstore.New(objectstore.Config{Endpoint: store.URL, Bucket: "reports", AccessKey: "local", SecretKey: "local"})
			if err != nil {
				t.Fatal(err)
			}
			w.rec = engineRecord{Generation: 4, LastReset: a.OperationID, LastResetOwner: &a}
			if _, err = w.reset(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			if w.savedEvidence(a).ThreadObservationsStatus != "PENDING" {
				t.Fatal("retry state lost")
			}
			fail = false
			if _, err = w.reset(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			e := w.savedEvidence(a)
			want := "INCOMPLETE"
			if complete {
				want = "ARCHIVED"
			}
			if e.ThreadObservationsStatus != want || !protocol.ValidThreadObservations(*e) || !bytes.Equal(uploaded, expected) {
				t.Fatal("wrong archive", e.ThreadObservationsStatus, len(uploaded))
			}
			if _, err = w.reset(context.Background(), a); err != nil || puts != 1 {
				t.Fatal("archive uploaded twice", err, puts)
			}
			foreign := a
			foreign.LeaseToken += "other"
			if w.publishThreadObservations(context.Background(), foreign) != nil {
				t.Fatal("foreign owner archive reused")
			}
			if err = w.cleanup(a); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(w.threadArchiveFile(a)); !os.IsNotExist(err) {
				t.Fatal("archive remained after cleanup", err)
			}
		})
	}
}
