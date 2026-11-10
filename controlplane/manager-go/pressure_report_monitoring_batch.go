package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

const monitoringBatchBodyLimit = 4 * 1024 * 1024

func validatePressureMonitoringBatch(body, run Obj, capturedAt time.Time) ([]any, int64, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, monitoringInvalid()
	}
	if len(raw) > monitoringBatchBodyLimit {
		return nil, 0, api(413, "monitoring_too_large", "JVM批量监控正文不能超过4MiB")
	}
	if _, err := monitoringFields(body, []string{"revision", "entries"}, nil); err != nil {
		return nil, 0, err
	}
	revision, ok := integer(body["revision"])
	if !ok || revision < 0 || revision > 128 {
		return nil, 0, monitoringInvalid()
	}
	values, ok := body["entries"].([]any)
	if !ok || len(values) < 1 || len(values) > 200 {
		return nil, 0, monitoringInvalid()
	}
	entries, seen, totalPoints := []any{}, map[string]bool{}, 0
	for _, value := range values {
		input, err := monitoringFields(value, []string{"instance", "source", "window"}, []string{"summary", "health", "metrics", "series", "findings"})
		if err != nil {
			return nil, 0, err
		}
		// Validate through the existing single-entry contract without mutating
		// the request object used for its durable idempotency digest.
		request := Obj{"revision": revision, "component": "jvm"}
		for key, value := range input {
			request[key] = value
		}
		entry, _, err := validatePressureMonitoring(request, run, capturedAt)
		if err != nil {
			return nil, 0, err
		}
		id := str(entry["entry_id"])
		if seen[id] {
			return nil, 0, api(400, "duplicate_monitoring_instance", "同一JVM批次不能包含重复实例")
		}
		seen[id] = true
		for _, series := range arr(entry["series"]) {
			totalPoints += len(arr(obj(series)["points"]))
		}
		if totalPoints > 60000 {
			return nil, 0, api(400, "invalid_monitoring", "JVM批次最多60000个时序点")
		}
		entries = append(entries, entry)
	}
	return entries, revision, nil
}

func mergePressureMonitoringEntries(current Obj, written []any, timestamp string, batch bool) (Obj, error) {
	revision := int64(number(current["revision"]))
	if revision >= 128 {
		return nil, api(409, "monitoring_limit", "每份报告最多128个监控补充版本")
	}
	replacements := map[string]Obj{}
	for _, value := range written {
		entry := obj(value)
		replacements[str(entry["entry_id"])] = entry
	}
	entries := []any{}
	for _, value := range arr(current["entries"]) {
		id := str(obj(value)["entry_id"])
		if entry, present := replacements[id]; present {
			entries = append(entries, entry)
			delete(replacements, id)
		} else {
			entries = append(entries, value)
		}
	}
	for _, value := range written {
		if _, pending := replacements[str(obj(value)["entry_id"])]; pending {
			entries = append(entries, value)
		}
	}
	if len(entries) > 1000 {
		return nil, api(409, "monitoring_limit", "当前监控快照最多1000个组件实例")
	}
	revision++
	first := obj(written[0])
	metadata := Obj{"revision": revision, "created_at": timestamp, "component": first["component"], "instance": first["instance"]}
	if batch {
		metadata["component"] = "jvm"
		metadata["instance"] = fmt.Sprintf("JVM批量 %d 个实例", len(written))
		metadata["written_count"] = len(written)
	}
	history := append([]any{metadata}, arr(current["history"])...)
	components, seen := []string{}, map[string]bool{}
	for _, value := range entries {
		component := str(obj(value)["component"])
		if !seen[component] {
			seen[component] = true
			components = append(components, component)
		}
	}
	sort.Strings(components)
	snapshot := Obj{"schema_version": "1", "project_id": current["project_id"], "report_id": current["report_id"], "revision": revision, "updated_at": timestamp, "entries": entries, "history": history, "monitoring_summary": Obj{"revision": revision, "updated_at": timestamp, "instance_count": len(entries), "components": components}}
	if batch {
		summaries := []any{}
		for _, value := range written {
			entry := obj(value)
			summaries = append(summaries, Obj{"entry_id": entry["entry_id"], "instance": entry["instance"]})
		}
		snapshot["batch_result"] = Obj{"schema_version": "1", "project_id": current["project_id"], "report_id": current["report_id"], "revision": revision, "updated_at": timestamp, "written_count": len(written), "instance_count": len(entries), "written_entries": summaries}
	} else {
		snapshot["written_entry_id"] = first["entry_id"]
	}
	if len(encoded(snapshot)) > monitoringSnapshotLimit {
		return nil, api(413, "monitoring_too_large", "当前完整监控快照不能超过8MiB")
	}
	return snapshot, nil
}

func (p *Platform) writePressureMonitoringJVMBatch(reportID, key string, body Obj) (Obj, error) {
	return p.writePressureMonitoringRequest(reportID, key, body, true)
}

// Reads always page in SQL; full composition requires explicit export=1.
func (p *Platform) pressureMonitoringPage(reportID string, values url.Values) (Obj, error) {
	revision, err := monitoringQueryRevision(values)
	if err != nil {
		return nil, err
	}
	component := values.Get("component")
	if _, present := values["component"]; present && !contains([]string{"es", "mysql", "redis", "kafka", "jvm", "host", "k8s"}, component) {
		return nil, monitoringInvalid()
	}
	search := values.Get("search")
	if !utf8.ValidString(search) || len(search) > 200 {
		return nil, monitoringInvalid()
	}
	limit, offset := 20, 0
	for _, spec := range []struct {
		name     string
		target   *int
		min, max int
	}{{"limit", &limit, 1, 200}, {"offset", &offset, 0, 1000000}} {
		if text, present := values[spec.name]; present {
			n, err := strconv.Atoi(text[0])
			if err != nil || n < spec.min || n > spec.max {
				return nil, monitoringInvalid()
			}
			*spec.target = n
		}
	}
	if values.Get("export") == "1" {
		for _, key := range []string{"component", "search", "limit", "offset"} {
			if _, present := values[key]; present {
				return nil, monitoringInvalid()
			}
		}
		return p.pressureMonitoring(reportID, revision)
	}
	return p.monitoringSQLPage(reportID, revision, component, search, limit, offset)
}
