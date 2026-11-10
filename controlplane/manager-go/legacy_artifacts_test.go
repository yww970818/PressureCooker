package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMySQLHistoricalOriginalReceiptRemainsScopedAndImmutable(t *testing.T) {
	p, plan := mysqlFixture(t)
	run := Obj{"id": "run-original", "project_id": defaultProjectID, "plan_id": plan["id"], "status": "COMPLETED", "execution_mode": "redis", "attempt_id": "attempt-original", "workers": []any{"worker-1"}, "created_at": "2026-11-06T01:00:00Z", "started_at": "2026-11-06T01:00:00Z", "node_evidence": []any{Obj{"node_id": "worker-1", "legacy_extension": Obj{"status": "retired", "id": "opaque-reference"}}}}
	prefix, err := rawPrefix(run)
	if err != nil {
		t.Fatal(err)
	}
	store, err := p.objectStore()
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("unchanged historical original\n")
	ref, err := store.Put(context.Background(), "2026-11-07/"+str(run["id"])+"/raw/extensions/worker-1/original.log", bytes.NewReader(content), int64(len(content)), digest(content), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	name := "custom-output.log"
	receipt := dtoObject(ref)
	receipt["name"] = name
	run["raw_archive"] = Obj{"status": "ARCHIVED", "files": []any{receipt}}
	saveRun := func() {
		t.Helper()
		if err := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); err != nil {
			t.Fatal(err)
		}
	}
	storedData := func() string {
		t.Helper()
		var value string
		if err := p.DB.QueryRow("SELECT data FROM runs WHERE id=?", run["id"]).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	saveRun()
	original := storedData()
	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-original/artifacts/"+name, nil)
	rec := httptest.NewRecorder()
	handled, err := p.streamRunnerArtifact(rec, request, str(run["id"]), name)
	if err != nil || !handled || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatal("historical receipt unavailable", err)
	}
	if storedData() != original {
		t.Fatal("download rewrote historical data")
	}
	if _, err = p.forProject("foreign-project").streamRunnerArtifact(httptest.NewRecorder(), request, str(run["id"]), name); err == nil {
		t.Fatal("cross-project original readable")
	}
	for _, mutate := range []func(Obj){
		func(f Obj) { f["bucket"] = "foreign" },
		func(f Obj) { f["key"] = "2026-99-99/" + str(run["id"]) + "/raw/original.log" },
		func(f Obj) { f["key"] = "2026-11-06/run-other/raw/original.log" },
		func(f Obj) { f["key"] = prefix + "../other/original.log" },
		func(f Obj) { f["key"] = prefix + "extensions/%2e%2e/original.log" },
		func(f Obj) { f["sha256"] = strings.Repeat("f", 64) },
		func(f Obj) { f["size"] = len(content) + 1 },
	} {
		altered := clone(receipt)
		mutate(altered)
		run["raw_archive"] = Obj{"status": "ARCHIVED", "files": []any{altered}}
		saveRun()
		if _, err = p.streamRunnerArtifact(httptest.NewRecorder(), request, str(run["id"]), name); err == nil {
			t.Fatal("foreign or changed receipt accepted", altered)
		}
	}
}
