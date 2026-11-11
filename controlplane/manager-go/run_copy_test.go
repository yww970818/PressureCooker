package main

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRunCopyDraftKeepsEditableParametersAndHidesRuntimeState(t *testing.T) {
	source := Obj{"id": "run-source", "name": "查询", "plan_id": "plan-old", "plan_name": "原脚本", "plan_sha256": "abc", "environment_id": "environment-old", "cluster_id": "cluster-old", "workers": []any{"worker-2", "worker-1"}, "timeout_seconds": 42, "notes": "保留备注", "status": "COMPLETED", "attempt_id": "old-attempt", "report_targets": Obj{"min_rps": 0}, "dataset_selections": []any{Obj{"node_id": "csv-1", "dataset_ids": []any{"file-2", "file-1"}, "distribution": "shard"}}, "properties": Obj{"base_host": "https://old.example", "jmeter.env.var.hidden": "secret", "mode": "internal", "threads": "7", "cookie": "session=original", "from_environment": "inherited", "empty": "", "payload": " a=b\nvalue "}, "environment_snapshot": Obj{"id": "environment-old", "base_host": "https://old.example", "variables": []any{Obj{"name": "from_environment", "value": "inherited"}, Obj{"name": "cookie", "value": "environment-cookie"}}}}
	draft := runCopyDraft(source)
	if draft["name"] != "查询（副本）" || draft["copy_source_run_id"] != source["id"] || boolean(draft["copy_environment_snapshot"]) || draft["plan_id"] != "plan-old" {
		t.Fatal("copy identity or immutable plan missing")
	}
	want := Obj{"threads": "7", "cookie": "session=original", "empty": "", "payload": " a=b\nvalue "}
	if !reflect.DeepEqual(obj(draft["properties"]), want) || string(encoded(draft["dataset_selections"])) != string(encoded(source["dataset_selections"])) || !reflect.DeepEqual(draft["workers"], source["workers"]) || string(encoded(draft["report_targets"])) != string(encoded(source["report_targets"])) {
		t.Fatal("editable settings were not preserved")
	}
	if obj(arr(obj(draft["environment_snapshot"])["variables"])[1])["value"] != "***" || obj(arr(obj(source["environment_snapshot"])["variables"])[1])["value"] != "environment-cookie" {
		t.Fatal("preview must mask without mutating source")
	}
	for _, key := range []string{"status", "attempt_id", "summary", "raw_archive", "id", "idempotency_key"} {
		if _, ok := draft[key]; ok {
			t.Fatalf("copied execution state: %s", key)
		}
	}
	source["name"] = strings.Repeat("字", 200)
	if len([]rune(str(runCopyDraft(source)["name"]))) != 200 {
		t.Fatal("copy name exceeds limit")
	}
}

func TestRunCopyOptionsRejectInvalidOrUnboundSnapshots(t *testing.T) {
	for _, payload := range []Obj{{"copy_source_run_id": 1}, {"copy_environment_snapshot": true}, {"copy_source_run_id": "run-source", "copy_environment_snapshot": "true"}} {
		if _, err := parseRunCopy(payload); err == nil {
			t.Fatal("invalid copy accepted", payload)
		}
	}
	if _, err := parseRunCopy(Obj{}); err != nil {
		t.Fatal(err)
	}
}

func TestRunCopyLegacyControllerStillSelectsTheMatchingCluster(t *testing.T) {
	draft := runCopyDraft(Obj{"id": "run-legacy", "name": "legacy", "controller_id": "controller-b", "workers": []any{"worker-1"}})
	if draft["cluster_id"] != clusterID("controller-b") {
		t.Fatal("legacy controller mapping missing")
	}
	if boolean(draft["copy_environment_snapshot"]) {
		t.Fatal("legacy task invents an environment snapshot")
	}
}

func TestRunCopyKeepsExplicitOverridesEvenWhenEqualToEnvironmentDefaults(t *testing.T) {
	source := Obj{"id": "run-source", "name": "name", "properties": Obj{"region": "same", "inherited": "default"}, "task_property_keys": []any{"region"}, "environment_snapshot": Obj{"variables": []any{Obj{"name": "region", "value": "same"}, Obj{"name": "inherited", "value": "default"}}}}
	draft := runCopyDraft(source)
	if obj(draft["properties"])["region"] != "same" || len(obj(draft["properties"])) != 1 || draft["copy_property_provenance"] != "explicit" {
		t.Fatal("equal explicit override lost")
	}
	delete(source, "task_property_keys")
	if runCopyDraft(source)["copy_property_provenance"] != "legacy_merged" {
		t.Fatal("legacy provenance not disclosed")
	}
}

func TestRunCopySnapshotAndCallbackStayBoundToTheirOriginalResources(t *testing.T) {
	source := Obj{"cluster_id": "cluster-old", "cluster_snapshot": Obj{"callback_url": "http://old.example/api/runner/callback"}, "environment_snapshot": Obj{"id": "env-old", "project_id": "project-default", "base_host": "https://old.example"}}
	current := Obj{"id": "env-old", "project_id": "project-default", "base_host": "https://new.example"}
	value, err := copiedEnvironment(runCopyOptions{SourceID: "run-old", HistoricalEnvironment: true}, source, current)
	if err != nil || value["base_host"] != "https://old.example" {
		t.Fatal("historical environment lost", err)
	}
	current["id"] = "env-other"
	if _, err = copiedEnvironment(runCopyOptions{SourceID: "run-old", HistoricalEnvironment: true}, source, current); err == nil {
		t.Fatal("snapshot bound to different environment")
	}
	for _, cluster := range []string{"cluster-old", "cluster-other"} {
		snapshot := Obj{"callback_url": "http://new.example/api/runner/callback"}
		copyClusterCallback(source, cluster, snapshot)
		want := "http://new.example/api/runner/callback"
		if cluster == "cluster-old" {
			want = "http://old.example/api/runner/callback"
		}
		if snapshot["callback_url"] != want {
			t.Fatal("wrong cluster callback")
		}
	}
}

func TestRunCopyUsesOriginalSnapshotAndCreatesANewTask(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	source, _, err := p.createRun(Obj{"name": "original", "cluster_id": clusterID("controller-a"), "plan_id": plan["id"], "environment_id": environment["id"], "workers": []any{"worker-2"}, "properties": Obj{"cookie": "original-cookie", "custom": "override", "token": "private-token"}, "notes": "original notes", "timeout_seconds": 50}, "copy-original")
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.listRuns("", "", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, authenticated := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/runs/"+str(source["id"])+"/copy-config", nil)
		if authenticated {
			req.Header.Set("Authorization", "Bearer admin-secret")
			req.Header.Set("X-Project-ID", p.projectID())
		}
		response := httptest.NewRecorder()
		Handler{p}.ServeHTTP(response, req)
		if !authenticated {
			if response.Code != 401 {
				t.Fatal("unauthenticated copy allowed")
			}
		} else {
			if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("copy API failed", response.Code)
			}
			config, e := decode(response.Body.Bytes())
			if e != nil || obj(config["properties"])["cookie"] != "original-cookie" {
				t.Fatal("copy API did not return editable values", e)
			}
		}
	}
	after, err := p.listRuns("", "", 20, 0)
	if err != nil || number(before["total"]) != number(after["total"]) {
		t.Fatal("copy configuration GET created a task", err)
	}
	draft, err := p.runCopyConfig(str(source["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if obj(draft["properties"])["token"] != "private-token" {
		t.Fatal("stored explicit property provenance lost")
	}
	_, err = p.workspaceUpdate("environments", str(environment["id"]), Obj{"revision": environment["revision"], "name": "changed", "base_host": "https://new.example", "variables": []any{Obj{"name": "token", "value": "new-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.stopRun(str(source["id"]), "now"); err != nil {
		t.Fatal(err)
	}
	draft["copy_environment_snapshot"] = true // Explicit historical replay remains supported.
	copy, created, err := p.createRun(draft, "copy-new")
	if err != nil || !created {
		t.Fatal(created, err)
	}
	raw, err := p.get("runs", str(copy["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if copy["id"] == source["id"] || raw["copied_from_run_id"] != source["id"] || obj(raw["properties"])["base_host"] != "http://fixture:8083" || obj(raw["properties"])["token"] != "private-token" || obj(raw["properties"])["cookie"] != "original-cookie" || number(raw["timeout_seconds"]) != 50 {
		t.Fatal("original task configuration not reproduced")
	}
	if obj(copy["properties"])["cookie"] != "***" {
		t.Fatal("public task exposes copied credential")
	}
	again, created, err := p.createRun(draft, "copy-new")
	if err != nil || created || again["id"] != copy["id"] {
		t.Fatal("copy retry not idempotent", err)
	}
	if _, err = p.stopRun(str(copy["id"]), "now"); err != nil {
		t.Fatal(err)
	}
	draft["copy_environment_snapshot"] = false
	fresh, _, err := p.createRun(draft, "copy-fresh-env")
	if err != nil || obj(fresh["environment_snapshot"])["base_host"] != "https://new.example" {
		t.Fatal("current environment not used", err)
	}
	other := *p
	other.ProjectID = "project-other"
	if _, err = other.runCopyConfig(str(source["id"])); err == nil {
		t.Fatal("cross-project copy allowed")
	}
}
