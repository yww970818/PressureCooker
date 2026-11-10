package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
	"pressurecooker/manager/reporting"
)

func independentThreadEvidence() protocol.NodeEvidence {
	e := protocol.NodeEvidence{NodeID: "worker-1", TaskID: "run-1", AttemptID: "attempt-1", Generation: 2, Status: "COLLECTED", PreparedAt: "2026-11-08T01:00:00Z", ThreadObservationsStatus: "ARCHIVED"}
	sha := strings.Repeat("a", 64)
	key := protocol.ThreadObservationsKey(e, sha)
	e.ThreadObservations = &protocol.ObjectRef{Name: "thread-observations.csv", Bucket: "jmeter-reports", Key: key, SHA256: sha, Size: 100, URL: "s3://jmeter-reports/" + key}
	return e
}

func TestIndependentThreadCallbackEvidenceRejectsUnresolvedAndForeignReferences(t *testing.T) {
	run := Obj{"id": "run-1", "attempt_id": "attempt-1", "workers": []any{"worker-1"}}
	valid := independentThreadEvidence()
	if _, err := pressureValidateNodeEvidence(run, []protocol.NodeEvidence{valid}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*protocol.NodeEvidence){
		"pending":            func(e *protocol.NodeEvidence) { e.ThreadObservations = nil; e.ThreadObservationsStatus = "PENDING" },
		"status":             func(e *protocol.NodeEvidence) { e.ThreadObservationsStatus = "MISSING" },
		"foreign-generation": func(e *protocol.NodeEvidence) { e.Generation++ },
		"foreign-attempt":    func(e *protocol.NodeEvidence) { e.AttemptID = "attempt-2" },
		"noncanonical-name":  func(e *protocol.NodeEvidence) { e.ThreadObservations.Name = "results.jtl" },
		"oversize":           func(e *protocol.NodeEvidence) { e.ThreadObservations.Size = (4 << 20) + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := valid
			ref := *e.ThreadObservations
			e.ThreadObservations = &ref
			mutate(&e)
			if _, err := pressureValidateNodeEvidence(run, []protocol.NodeEvidence{e}); err == nil {
				t.Fatal("invalid independent observation evidence accepted")
			}
		})
	}
}

func independentThreadPlatform(t *testing.T) *Platform {
	t.Helper()
	return &Platform{Config: Config{ArchiveEndpoint: runnerObjectPeer(t).URL, ArchiveBucket: "jmeter-reports", ArchiveAccessKey: "fixture-access", ArchiveSecretKey: "fixture-secret", ArchiveTimeout: time.Second, StateDir: t.TempDir()}}
}
func independentThreadPut(t *testing.T, p *Platform, e protocol.NodeEvidence, body string) protocol.NodeEvidence {
	t.Helper()
	sha := digest([]byte(body))
	key := protocol.ThreadObservationsKey(e, sha)
	store, err := p.objectStore()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), key, strings.NewReader(body), int64(len(body)), sha, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	ref.Name = "thread-observations.csv"
	ref.URL = "s3://" + ref.Bucket + "/" + ref.Key
	e.ThreadObservations = &ref
	return e
}
func independentThreadCSV(e protocol.NodeEvidence, tail string) string {
	return fmt.Sprintf("pressurecooker_thread_observations_v1,%s,%s,%s,%d,1000\ntimestamp_ms,active_threads\n1000,3\n2000,0\n%s", e.TaskID, e.AttemptID, e.NodeID, e.Generation, tail)
}

func TestIndependentThreadReceiptVerifiesHeadAndBucket(t *testing.T) {
	p := independentThreadPlatform(t)
	run := Obj{"id": "run-1"}
	e := independentThreadEvidence()
	if _, err := p.pressureThreadArtifactReceipts(context.Background(), run, []protocol.NodeEvidence{e}); err == nil {
		t.Fatal("absent object accepted")
	}
	e = independentThreadPut(t, p, e, independentThreadCSV(e, "END,2000\n"))
	files, err := p.pressureThreadArtifactReceipts(context.Background(), run, []protocol.NodeEvidence{e})
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if f := obj(files[0]); f["name"] != "thread-observations-worker-1.csv" || f["download_url"] != "/api/runs/run-1/artifacts/thread-observations-worker-1.csv" {
		t.Fatal(f)
	}
	ref := *e.ThreadObservations
	ref.Size++
	e.ThreadObservations = &ref
	if _, err = p.pressureThreadArtifactReceipts(context.Background(), run, []protocol.NodeEvidence{e}); err == nil {
		t.Fatal("HEAD size mismatch accepted")
	}
	e.ThreadObservations.Bucket = "foreign"
	if _, err = p.pressureThreadArtifactReceipts(context.Background(), run, []protocol.NodeEvidence{e}); err == nil {
		t.Fatal("foreign bucket accepted")
	}
}

func TestIndependentThreadSourcesVerifiedAtomicAndLegacy(t *testing.T) {
	p := independentThreadPlatform(t)
	e := independentThreadEvidence()
	e = independentThreadPut(t, p, e, independentThreadCSV(e, "END,2000\n"))
	run := Obj{"id": "run-1", "attempt_id": "attempt-1", "workers": []any{"worker-1", "worker-2"}, "node_evidence": []protocol.NodeEvidence{e}}
	a := reporting.Analysis{Metrics: reporting.Metrics{SampleCount: 1}, Dashboard: reporting.Dashboard{Scopes: []reporting.DashboardScope{{Points: []reporting.DashboardPoint{{TimestampMS: 9000, SampleCount: 1}}}, {StepKey: "request", Points: []reporting.DashboardPoint{{TimestampMS: 9000}}}}}}
	before := string(encoded(a.Dashboard.Scopes[0].Points))
	sources, err := p.pressureLoadThreadObservations(context.Background(), run, &a)
	if err != nil || len(sources) != 2 {
		t.Fatal(sources, err)
	}
	s := obj(sources[0])
	if s["kind"] != "thread_observations" || s["download_status"] != "VERIFIED" || number(s["valid_rows"]) != 2 {
		t.Fatal(s)
	}
	if a.Dashboard.Scopes[0].ThreadObservations.Status != "PARTIAL" || a.Dashboard.Scopes[1].ThreadObservations != nil || a.Metrics.SampleCount != 1 || before != string(encoded(a.Dashboard.Scopes[0].Points)) {
		t.Fatal("periodic evidence changed request/step interpretation")
	}
	// A malformed tail must not leak the valid prefix into an accepted series.
	bad := independentThreadPut(t, p, e, independentThreadCSV(e, "broken\n"))
	run["node_evidence"] = []protocol.NodeEvidence{bad}
	a = reporting.Analysis{Dashboard: reporting.Dashboard{Scopes: []reporting.DashboardScope{{}}}}
	sources, err = p.pressureLoadThreadObservations(context.Background(), run, &a)
	if err != nil || len(a.Dashboard.Scopes[0].ThreadObservations.Points) != 0 || a.Dashboard.Scopes[0].ThreadObservations.Status != "MISSING" || obj(sources[0])["status"] != "PARSE_FAILED" {
		t.Fatal("malformed evidence partially accepted", sources, err)
	}
	legacy := reporting.Analysis{Dashboard: reporting.Dashboard{Scopes: []reporting.DashboardScope{{}}}}
	delete(run, "node_evidence")
	if sources, err = p.pressureLoadThreadObservations(context.Background(), run, &legacy); err != nil || len(sources) != 0 || legacy.Dashboard.Scopes[0].ThreadObservations != nil {
		t.Fatal("legacy report changed", sources, err)
	}
}

func TestIndependentThreadSourceIdentityAndReadModelRoundTrip(t *testing.T) {
	run := Obj{"id": "run-1", "attempt_id": "attempt-1"}
	before := pressureSourceIdentity(run)
	run["node_evidence"] = []protocol.NodeEvidence{independentThreadEvidence()}
	if before == pressureSourceIdentity(run) {
		t.Fatal("independent source missing from report identity")
	}
	b := reporting.NewThreadObservationBuilder([]string{"worker-1"})
	b.AddNode("worker-1", []reporting.ThreadObservation{{TimestampMS: 1000, ActiveThreads: 0}}, true)
	d := reporting.Dashboard{}
	b.Apply(&d)
	payload, err := packReadModel(d.Scopes[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := unpackReadModel(payload)
	if err != nil || obj(scope["thread_observations"])["source"] != "PERIODIC" {
		t.Fatal("normalized read model dropped periodic series", err)
	}
}

func TestIndependentThreadAuthorizedArtifactDownload(t *testing.T) {
	p, run, cb := pressureFixture(t)
	e := independentThreadEvidence()
	e.TaskID = str(run["id"])
	e.AttemptID = str(run["attempt_id"])
	content := independentThreadCSV(e, "END,2000\n")
	e = independentThreadPut(t, p, e, content)
	cb["node_evidence"] = []protocol.NodeEvidence{e}
	missing := *e.ThreadObservations
	missing.Key = protocol.ThreadObservationsKey(e, strings.Repeat("f", 64))
	missing.SHA256 = strings.Repeat("f", 64)
	missing.URL = "s3://" + missing.Bucket + "/" + missing.Key
	unresolved := e
	unresolved.ThreadObservations = &missing
	cb["node_evidence"] = []protocol.NodeEvidence{unresolved}
	if _, err := p.runnerCallback(context.Background(), "controller-a", cb); err == nil {
		t.Fatal("callback accepted nonexistent capture object")
	}
	cb["node_evidence"] = []protocol.NodeEvidence{e}
	if _, err := p.runnerCallback(context.Background(), "controller-a", cb); err != nil {
		t.Fatal(err)
	}
	name := "thread-observations-worker-1.csv"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+e.TaskID+"/artifacts/"+name, nil)
	handled, err := p.streamRunnerArtifact(rec, req, e.TaskID, name)
	if err != nil || !handled || !bytes.Equal(rec.Body.Bytes(), []byte(content)) {
		t.Fatal("authorized raw capture unavailable", err, rec.Code)
	}
	if _, err = p.forProject("foreign-project").streamRunnerArtifact(httptest.NewRecorder(), req, e.TaskID, name); err == nil {
		t.Fatal("cross-project artifact readable")
	}
	c, ok, err := p.claimPressure()
	if err != nil || !ok {
		t.Fatal("report not queued", err)
	}
	p.workPressure(context.Background(), c)
	report, err := p.pressureLatest(e.TaskID)
	if err != nil || report["status"] != "READY" || number(obj(report["metrics"])["sample_count"]) != 2 {
		t.Fatal("periodic evidence changed request report", err, report["status"])
	}
	dashboard := obj(report["dashboard"])
	if err = p.loadDashboardScopes(str(report["id"]), dashboard); err != nil {
		t.Fatal(err)
	}
	if len(arr(dashboard["scopes"])) == 0 || obj(obj(arr(dashboard["scopes"])[0])["thread_observations"])["source"] != "PERIODIC" {
		t.Fatal("normalized analytics lost periodic curve")
	}
	// An allowed alias cannot be redirected to a different object by archive metadata.
	foreign := independentThreadPut(t, p, e, independentThreadCSV(e, "END,3000\n"))
	if err = p.transaction(func(tx *Tx) error {
		stored, err := get(tx, "runs", e.TaskID)
		if err != nil {
			return err
		}
		for _, value := range arr(obj(stored["raw_archive"])["files"]) {
			f := obj(value)
			if f["name"] == name {
				f["key"] = foreign.ThreadObservations.Key
				f["sha256"] = foreign.ThreadObservations.SHA256
				f["size"] = foreign.ThreadObservations.Size
			}
		}
		return p.saveRun(tx, stored)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.streamRunnerArtifact(httptest.NewRecorder(), req, e.TaskID, name); err == nil {
		t.Fatal("download alias redirected away from owned evidence")
	}
}

func TestIndependentThreadRedactionDoesNotCollapseRawNodeTotals(t *testing.T) {
	p := independentThreadPlatform(t)
	a := independentThreadEvidence()
	a = independentThreadPut(t, p, a, independentThreadCSV(a, "END,2000\n"))
	b := independentThreadEvidence()
	b.NodeID = "worker-2"
	b = independentThreadPut(t, p, b, strings.ReplaceAll(independentThreadCSV(b, "END,2000\n"), "1000,3", "1000,9"))
	run := Obj{"id": "run-1", "attempt_id": "attempt-1", "workers": []any{"worker-1", "worker-2"}, "properties": Obj{"token": "worker-1", "password": "worker-2"}, "node_evidence": []protocol.NodeEvidence{a, b}}
	analysis := reporting.Analysis{Dashboard: reporting.Dashboard{Scopes: []reporting.DashboardScope{{}}}}
	_, err := p.pressureLoadThreadObservations(context.Background(), run, &analysis)
	if err != nil {
		t.Fatal(err)
	}
	g := analysis.Dashboard.Scopes[0].ThreadObservations
	if g == nil || len(g.Points) != 2 || g.Points[0].ThreadsExpectedNodes != 2 || g.Points[0].ThreadsObservedNodes != 2 || *g.Points[0].ActiveThreads != 12 {
		t.Fatalf("redacted labels collapsed physical node evidence: %+v", g)
	}
	if raw := string(encoded(analysis.Dashboard)); strings.Contains(raw, "worker-1") || strings.Contains(raw, "worker-2") {
		t.Fatal("raw node secrets leaked")
	}
}
