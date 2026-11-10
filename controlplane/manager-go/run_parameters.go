package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

func runRevealQuery(raw string) (bool, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return false, api(400, "invalid_parameters_query", "参数查询格式无效")
	}
	values, exists := q["reveal"]
	if !exists {
		return false, nil
	}
	if len(values) != 1 || values[0] != "true" && values[0] != "false" {
		return false, api(400, "invalid_parameters_query", "reveal须为true或false")
	}
	return values[0] == "true", nil
}

// JSON cells such as custom_config_json may contain credentials despite an ordinary
// column name. Hide the whole cell until explicitly revealed; never replace
// arbitrary short secret substrings inside JSON or identifiers.
func sensitiveParameterValue(name string, value any) bool {
	if sensitiveRE.MatchString(name) {
		return true
	}
	var inspect func(any, int) bool
	inspect = func(v any, depth int) bool {
		if depth > 24 {
			return true
		}
		switch data := v.(type) {
		case map[string]any:
			for key, child := range data {
				if sensitiveRE.MatchString(key) || inspect(child, depth+1) {
					return true
				}
			}
		case []any:
			for _, child := range data {
				if inspect(child, depth+1) {
					return true
				}
			}
		case string:
			trimmed := strings.TrimSpace(data)
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				var nested any
				if json.Unmarshal([]byte(trimmed), &nested) == nil {
					return inspect(nested, depth+1)
				}
			}
		}
		return false
	}
	return inspect(value, 0)
}

func parameterRow(name string, value any, source string, reveal, sensitive bool) Obj {
	row := Obj{"name": name, "value": value, "source": source, "sensitive": sensitive}
	if sensitive && !reveal {
		row["value"] = "***"
	}
	return row
}

func (p *Platform) runParameterTable(id string, reveal bool) (Obj, error) {
	run, err := p.get("runs", id)
	if err != nil {
		return nil, err
	}
	snapshot := obj(run["environment_snapshot"])
	environment := Obj{}
	for _, key := range []string{"id", "name", "revision", "base_host"} {
		if v, exists := snapshot[key]; exists {
			environment[key] = v
		}
	}
	explicit := map[string]bool{}
	for _, key := range stringList(run["task_property_keys"]) {
		explicit[key] = true
	}
	sensitive := sensitiveRunProperties(run)
	envNames, overrideNames := map[string]bool{"base_host": true}, map[string]bool{}
	envRows, overrides, properties := []any{}, []any{}, []any{}
	if str(snapshot["base_host"]) != "" {
		envRows = append(envRows, parameterRow("base_host", snapshot["base_host"], "project_environment", reveal, false))
	}
	for _, v := range arr(snapshot["variables"]) {
		pair := obj(v)
		name := str(pair["name"])
		envNames[name], envNames[environmentProperty(name)] = true, true
		envRows = append(envRows, parameterRow(name, pair["value"], "project_environment", reveal, sensitiveParameterValue(name, pair["value"])))
	}
	for _, v := range arr(snapshot["api_overrides"]) {
		pair := obj(v)
		name := str(pair["name"])
		overrideNames[str(pair["property"])] = true
		row := parameterRow(name, pair["value"], "api_effective", reveal, sensitiveParameterValue(name, pair["value"]))
		for _, key := range []string{"node_id", "collection_id", "collection_name", "node_name", "property", "revision", "collection_environment_id", "parent_environment_id", "parent_environment_revision"} {
			row[key] = pair[key]
		}
		overrides = append(overrides, row)
	}
	keys := objectKeys(obj(run["properties"]))
	sort.Strings(keys)
	for _, name := range keys {
		value := obj(run["properties"])[name]
		source := "runtime"
		if envNames[name] || strings.HasPrefix(name, "jmeter.env.") {
			source = "project_environment"
		}
		if overrideNames[name] {
			source = "api_effective"
		}
		if explicit[name] {
			source = "task"
		}
		properties = append(properties, parameterRow(name, value, source, reveal, sensitive[name] || sensitiveParameterValue(name, value)))
	}
	execution := Obj{}
	for _, key := range []string{"id", "name", "plan_id", "plan_name", "plan_sha256", "workers", "cluster_id", "timeout_seconds", "created_at", "started_at", "finished_at", "copied_from_run_id", "notes"} {
		execution[key] = run[key]
	}
	cluster := obj(run["cluster_snapshot"])
	execution["cluster_name"], execution["controller_id"], execution["callback_url"] = cluster["name"], cluster["controller_id"], cluster["callback_url"]
	var raw string
	err = p.DB.QueryRow("SELECT payload FROM task_dispatches WHERE id=?", id).Scan(&raw)
	if err == nil {
		opened, e := p.openWorkspace("task_dispatches", id, 1, raw)
		if e != nil {
			return nil, e
		}
		task := obj(opened["task"])
		if task["task_id"] == id && task["project_id"] == p.projectID() {
			execution["callback_url"] = task["callback_url"]
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if reveal {
		if err = p.audit("run.parameters.reveal", id); err != nil {
			return nil, err
		}
	}
	return Obj{"run_id": id, "environment": environment, "collection_environments": collectionEnvironmentParameterSnapshots(snapshot, reveal), "execution": execution, "environment_variables": envRows, "api_overrides": overrides, "properties": properties, "revealed": reveal}, nil
}
