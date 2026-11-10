package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
)

func TestRunEnvironmentDisplayBackfillPreservesFrozenExecution(t *testing.T) {
	p, plan, env := newRunnerFixture(t)
	run, _, err := p.createRun(Obj{"name": "旧任务", "cluster_id": clusterID("controller-a"), "plan_id": plan["id"], "environment_id": env["id"], "workers": []any{"worker-1"}}, "old-display-backfill")
	if err != nil {
		t.Fatal(err)
	}
	id := str(run["id"])
	before, err := p.get("runs", id)
	if err != nil {
		t.Fatal(err)
	}
	var originalEnvelope string
	if err = p.DB.QueryRow("SELECT JSON_UNQUOTE(JSON_EXTRACT(data,'$.environment_envelope')) FROM runs WHERE id=?", id).Scan(&originalEnvelope); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("UPDATE runs SET data=JSON_REMOVE(data,'$.environment_display_envelope') WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err = p.workspaceUpdate("environments", str(env["id"]), Obj{"revision": env["revision"], "name": "新配置", "base_host": "https://new.example"}); err != nil {
		t.Fatal(err)
	}
	result, err := p.rebuildRunEnvironmentSummaries(1)
	if err != nil || number(result["updated"]) != 1 {
		t.Fatal("old environment display was not backfilled", err)
	}
	after, err := p.get("runs", id)
	if err != nil || string(encoded(after)) != string(encoded(before)) {
		t.Fatal("display backfill modified frozen execution", err)
	}
	var currentEnvelope string
	if err = p.DB.QueryRow("SELECT JSON_UNQUOTE(JSON_EXTRACT(data,'$.environment_envelope')) FROM runs WHERE id=?", id).Scan(&currentEnvelope); err != nil || currentEnvelope != originalEnvelope {
		t.Fatal("display backfill replaced historical ciphertext", err)
	}
	list, err := p.listRunSummaries(context.Background(), "", "旧任务", 20, 0)
	if err != nil || obj(obj(arr(list["items"])[0])["environment_snapshot"])["base_host"] != "http://fixture:8083" {
		t.Fatal("historical display used current environment", err)
	}
	again, err := p.rebuildRunEnvironmentSummaries(1)
	if err != nil || number(again["updated"]) != 0 {
		t.Fatal("display backfill is not idempotent", err)
	}
}

func TestRunDatasetTableMasksFrozenRuntimeVariableAliases(t *testing.T) {
	p, _, dataset := onlineDatasetFixture(t)
	plan, err := p.createPlan(Obj{"name": "alias fixture", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	run := runFixture(t, p, plan, "alias-sensitive-csv")
	_, err = p.updateRun(str(run["id"]), Obj{"datasets": []any{Obj{"node_id": "csv-online", "columns": []any{"id", "name"}, "variable_names": []any{"id", "authCookie"}, "delimiter": ";", "files": []any{dataset["object"]}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, call := workspaceHTTP(t, p)
	path := "/api/runs/" + str(run["id"]) + "/datasets/csv-online/files/0/table"
	code, table := call("GET", path, nil)
	if code != 200 || table["sensitive"] != true || string(encoded(arr(table["rows"])[0])) != `["0001","***"]` {
		t.Fatal("sensitive runtime alias exposes the physical CSV value", code)
	}
	code, actual := call("GET", path+"?reveal=true", nil)
	if code != 200 || string(encoded(arr(actual["rows"])[0])) != `["0001","含;分隔符"]` {
		t.Fatal("explicit reveal must preserve actual CSV bytes", code)
	}
}

func TestRunDatasetSensitiveJSONIsExplicitAndDoesNotCorruptNumericText(t *testing.T) {
	p, workflow, _ := onlineDatasetFixture(t)
	raw := `{"accounts":[{"token":"1"}],"iterations":1}`
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Comma = ';'
	_ = writer.WriteAll([][]string{{"id", "name"}, {"0001", raw}})
	dataset, err := p.datasetCreate(context.Background(), str(workflow["id"]), "csv-online", "敏感配置.csv", &buf)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.createPlan(Obj{"name": "no traffic", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	run := runFixture(t, p, plan, "sensitive-csv-display")
	_, err = p.updateRun(str(run["id"]), Obj{"datasets": []any{Obj{"node_id": "csv-online", "columns": []any{"id", "name"}, "delimiter": ";", "files": []any{dataset["object"]}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, call := workspaceHTTP(t, p)
	path := "/api/runs/" + str(run["id"]) + "/datasets/csv-online/files/0/table"
	code, masked := call("GET", path, nil)
	if code != 200 || masked["sensitive"] != true || string(encoded(masked["rows"])) != `[["0001","***"]]` {
		t.Fatal("JSON credential cell was not hidden without changing IDs", code)
	}
	code, actual := call("GET", path+"?reveal=true", nil)
	row := arr(arr(actual["rows"])[0])
	if code != 200 || row[0] != "0001" || row[1] != raw {
		t.Fatal("revealed CSV differs from actual immutable bytes", code)
	}
}

func TestRunCopyDefaultsToSelectedCurrentEnvironment(t *testing.T) {
	source := Obj{"id": "run-source", "environment_id": "env-qa", "environment_snapshot": Obj{"id": "env-qa", "name": "测试", "base_host": "https://old.example"}}
	draft := runCopyDraft(source)
	if boolean(draft["copy_environment_snapshot"]) {
		t.Fatal("copy unexpectedly freezes the old address instead of the selected current environment")
	}
	if obj(draft["environment_snapshot"])["base_host"] != "https://old.example" {
		t.Fatal("historical snapshot must remain available for explicit replay")
	}
}

func TestRunSummaryKeepsEncryptedEnvironmentWithoutReadingFullSnapshot(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run := runFixture(t, p, plan, "environment-display")
	// Use a named environment so the test detects the former null projection.
	run, _, err := p.createRun(Obj{"name": "summary", "cluster_id": clusterID("controller-a"), "plan_id": plan["id"], "environment_id": environment["id"], "workers": []any{"worker-1"}}, "environment-display-named")
	if err != nil {
		t.Fatal(err)
	}
	id := str(run["id"])
	var stored string
	if err = p.DB.QueryRow("SELECT data FROM runs WHERE id=?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "http://fixture:8083") || strings.Contains(stored, "private-token") {
		t.Fatal("environment descriptor was stored in plaintext")
	}
	// The small list projection must be independent of full parameter decoding.
	if _, err = p.DB.Exec("UPDATE runs SET data=JSON_SET(data,'$.environment_envelope','broken-full-snapshot') WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	list, err := p.listRunSummaries(context.Background(), "", "summary", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := arr(list["items"])
	if len(items) != 1 || obj(obj(items[0])["environment_snapshot"])["name"] != "test" || obj(obj(items[0])["environment_snapshot"])["base_host"] != "http://fixture:8083" {
		t.Fatal("selected environment lost in task list")
	}
	if len(encoded(list)) > 8192 || strings.Contains(string(encoded(list)), "private-token") || obj(items[0])["properties"] != nil {
		t.Fatal("list reads private or heavy parameters")
	}
}

func TestRunParameterTablesReturnFrozenValuesAndRequireExplicitReveal(t *testing.T) {
	p, plan, env := newRunnerFixture(t)
	run, _, err := p.createRun(Obj{"name": "参数表", "cluster_id": clusterID("controller-a"), "plan_id": plan["id"], "environment_id": env["id"], "workers": []any{"worker-1"}, "properties": Obj{"threads": "7", "cookie": "actual-cookie", "custom": "task-value"}}, "parameter-table")
	if err != nil {
		t.Fatal(err)
	}
	request, call := workspaceHTTP(t, p)
	path := "/api/runs/" + str(run["id"]) + "/parameters"
	code, headers, _ := request("GET", path, nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatal("parameter endpoint is not authenticated", code)
	}
	code, table := call("GET", path, nil)
	if code != 200 {
		t.Fatal("parameter table unavailable", code)
	}
	if strings.Contains(string(encoded(table)), "actual-cookie") || strings.Contains(string(encoded(table)), "private-token") {
		t.Fatal("sensitive value shown without explicit reveal")
	}
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("parameters must not be cached")
	}
	find := func(rows []any, name string) Obj {
		for _, row := range rows {
			if obj(row)["name"] == name {
				return obj(row)
			}
		}
		return nil
	}
	if find(arr(table["properties"]), "threads")["value"] != "7" || find(arr(table["properties"]), "custom")["source"] != "task" {
		t.Fatal("actual global properties or provenance missing")
	}
	if _, err = p.workspaceUpdate("environments", str(env["id"]), Obj{"revision": env["revision"], "name": "已更新", "base_host": "https://changed.example"}); err != nil {
		t.Fatal(err)
	}
	code, revealed := call("GET", path+"?reveal=true", nil)
	if code != 200 || obj(revealed["environment"])["base_host"] != "http://fixture:8083" || find(arr(revealed["properties"]), "cookie")["value"] != "actual-cookie" || find(arr(revealed["environment_variables"]), "token")["value"] != "private-token" {
		t.Fatal("historical actual parameters replaced by current environment")
	}
	project, err := p.projectCreate(Obj{"name": "other parameter project"})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ = request("GET", path+"?reveal=true", nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])})
	if code != 404 {
		t.Fatal("cross-project parameters exposed", code)
	}
}

func TestRunDatasetTableUsesFrozenObjectAfterEditingAndDeletingDataset(t *testing.T) {
	p, _, dataset := onlineDatasetFixture(t)
	plan, err := p.createPlan(Obj{"name": "no execution", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	run := runFixture(t, p, plan, "frozen-csv-table")
	_, err = p.updateRun(str(run["id"]), Obj{"datasets": []any{Obj{"node_id": "csv-online", "node_name": "业务数据", "columns": []any{"id", "name"}, "variable_names": []any{"requestId", "requestName"}, "delimiter": ";", "distribution": "copy", "files": []any{dataset["object"]}}}, "dataset_selections": []any{Obj{"node_id": "csv-online", "dataset_ids": []any{dataset["id"]}, "distribution": "copy"}}})
	if err != nil {
		t.Fatal(err)
	}
	request, call := workspaceHTTP(t, p)
	path := "/api/runs/" + str(run["id"]) + "/datasets/csv-online/files/0/table"
	code, first := call("GET", path+"?page_size=2", nil)
	if code != 200 || string(encoded(first["rows"])) != `[["0001","含;分隔符"],["0002","第一行\n第二行"]]` || first["has_more"] != true {
		t.Fatal("actual frozen CSV not displayed", code)
	}
	if _, err = p.datasetEdits(context.Background(), dataset, Obj{"base_sha256": dataset["sha256"], "updates": []any{Obj{"row_index": 0, "values": []any{"changed", "new-version"}}}}); err != nil {
		t.Fatal(err)
	}
	code, _ = call("DELETE", "/api/datasets/"+str(dataset["id"]), nil)
	if code != 200 {
		t.Fatal(code)
	}
	code, second := call("GET", path+"?page=2&page_size=2", nil)
	if code != 200 || string(encoded(second["rows"])) != `[["0003","第三行"]]` || second["has_more"] != false || second["sha256"] != dataset["sha256"] {
		t.Fatal("history followed an edited/deleted current file", code)
	}
	for _, query := range []string{"page=0", "page_size=201", "page=1&page=2", "reveal=bad"} {
		if c, _ := call("GET", path+"?"+query, nil); c != 400 {
			t.Fatal("invalid paging accepted", query, c)
		}
	}
	if c, _ := call("GET", strings.Replace(path, "/files/0/", "/files/9/", 1), nil); c != 404 {
		t.Fatal("foreign snapshot file exposed", c)
	}
	project, err := p.projectCreate(Obj{"name": "other CSV project"})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ = request("GET", path, nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])})
	if code != 404 {
		t.Fatal("cross-project CSV exposed", code)
	}
}
