package main

import "strings"

// Copying reads configuration only. A new execution is created exclusively by
// the existing POST /runs path, with its own idempotency key and attempt.
type runCopyOptions struct {
	SourceID              string
	HistoricalEnvironment bool
}

func parseRunCopy(payload Obj) (runCopyOptions, error) {
	var copy runCopyOptions
	if value, exists := payload["copy_source_run_id"]; exists {
		id, ok := value.(string)
		if !ok || id == "" || validID(id) != nil {
			return copy, api(400, "invalid_copy_source", "复制来源任务无效")
		}
		copy.SourceID = id
	}
	if value, exists := payload["copy_environment_snapshot"]; exists {
		var ok bool
		copy.HistoricalEnvironment, ok = value.(bool)
		if !ok || (copy.HistoricalEnvironment && copy.SourceID == "") {
			return copy, api(400, "invalid_copy_source", "历史环境参数须关联原任务")
		}
	}
	return copy, nil
}

func (p *Platform) runCopySourceQ(q queryer, copy runCopyOptions) (Obj, error) {
	if copy.SourceID == "" {
		return nil, nil
	}
	source, err := get(q, "runs", copy.SourceID)
	if err != nil {
		return nil, err
	}
	if err = p.checkProject(source); err != nil {
		return nil, err
	}
	return p.openStoredRun(source)
}

func runCopyDraft(source Obj) Obj {
	snapshot := obj(source["environment_snapshot"])
	inherited := Obj{}
	for _, value := range arr(snapshot["variables"]) {
		pair := obj(value)
		inherited[str(pair["name"])] = pair["value"]
	}
	properties := Obj{}
	explicit := map[string]bool{}
	keys, provenanceKnown := source["task_property_keys"]
	if provenanceKnown {
		for _, key := range stringList(keys) {
			explicit[key] = true
		}
	}
	for key, value := range obj(source["properties"]) {
		if key == "base_host" || reservedRuntimeProperty(key) {
			continue
		}
		if _, ok := value.(string); !ok {
			continue
		}
		if provenanceKnown {
			if !explicit[key] {
				continue
			}
		} else {
			// Older tasks stored only merged values. Their original environment
			// snapshot reproduces execution; switching defaults requires a review.
			if original, exists := inherited[key]; exists && original == value {
				continue
			}
		}
		properties[key] = value
	}
	for key, value := range obj(snapshot["task_property_rules"]) {
		if _, explicit := properties[key]; explicit {
			if kind, _, e := parseEnvironmentRandomRule(str(value)); e == nil && kind != "" {
				properties[key] = value
			}
		}
	}
	preview := clone(snapshot)
	for _, field := range []string{"variables", "api_overrides"} {
		for _, value := range arr(preview[field]) {
			pair := obj(value)
			if sensitiveRE.MatchString(str(pair["name"])) {
				pair["value"] = "***"
			}
		}
	}
	redactCollectionEnvironmentSnapshots(preview)
	name := []rune(str(source["name"]))
	suffix := "（副本）"
	if len(name) > 200-len([]rune(suffix)) {
		name = name[:200-len([]rune(suffix))]
	}
	cluster := str(source["cluster_id"])
	if cluster == "" {
		cluster = str(obj(source["cluster_snapshot"])["id"])
	}
	if cluster == "" {
		controller := str(source["controller_id"])
		if controller == "" {
			controller = str(obj(source["cluster_snapshot"])["controller_id"])
		}
		if controller != "" {
			cluster = clusterID(controller)
		}
	}
	draft := Obj{"copy_source_run_id": source["id"], "copy_environment_snapshot": false,
		"name": string(name) + suffix, "plan_id": source["plan_id"], "plan_name": source["plan_name"], "plan_sha256": source["plan_sha256"],
		"environment_id": source["environment_id"], "environment_snapshot": preview, "cluster_id": cluster,
		"workers": source["workers"], "properties": properties, "dataset_selections": source["dataset_selections"],
		"notes": source["notes"], "timeout_seconds": source["timeout_seconds"]}
	if draft["dataset_selections"] == nil {
		draft["dataset_selections"] = []any{}
	}
	if draft["workers"] == nil {
		draft["workers"] = []any{}
	}
	if draft["timeout_seconds"] == nil {
		draft["timeout_seconds"] = 3600
	}
	if draft["notes"] == nil {
		draft["notes"] = ""
	}
	draft["copy_property_provenance"] = "legacy_merged"
	if provenanceKnown {
		draft["copy_property_provenance"] = "explicit"
	}
	if value := source["report_targets"]; value != nil {
		draft["report_targets"] = value
	}
	return clone(draft)
}

func (p *Platform) runCopyConfig(id string) (Obj, error) {
	source, err := p.get("runs", id)
	if err != nil {
		return nil, err
	}
	return runCopyDraft(source), nil
}

func copiedEnvironment(copy runCopyOptions, source, current Obj) (Obj, error) {
	if !copy.HistoricalEnvironment {
		return current, nil
	}
	snapshot := obj(source["environment_snapshot"])
	if len(snapshot) == 0 || str(snapshot["id"]) != str(current["id"]) || objectProject(snapshot) != objectProject(current) {
		return nil, api(400, "invalid_copy_environment", "原环境快照不可用；请使用当前环境配置或重新选择环境")
	}
	return clone(snapshot), nil
}

func copyClusterCallback(source Obj, cluster string, snapshot Obj) {
	if source != nil && strings.TrimSpace(cluster) != "" && cluster == str(source["cluster_id"]) {
		if callback, exists := obj(source["cluster_snapshot"])["callback_url"]; exists {
			snapshot["callback_url"] = callback
		}
	}
}
