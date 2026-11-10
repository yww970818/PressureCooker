package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const monitoringSnapshotKind = "monitoring_snapshot"
const monitoringBodyLimit = 512 * 1024
const monitoringSnapshotLimit = 8 * 1024 * 1024

var monitoringMetricKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,99}$`)

func monitoringInvalid() error {
	return api(400, "invalid_monitoring", "监控字段、数值、时间范围或数量无效，请检查监控写入契约")
}
func monitoringFields(value any, required, optional []string) (Obj, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, monitoringInvalid()
	}
	allowed := map[string]bool{}
	for _, name := range append(append([]string{}, required...), optional...) {
		allowed[name] = true
	}
	for name := range m {
		if !allowed[name] {
			return nil, monitoringInvalid()
		}
	}
	for _, name := range required {
		if _, present := m[name]; !present {
			return nil, monitoringInvalid()
		}
	}
	return m, nil
}
func monitoringText(m Obj, key string, required bool, max int, fallback string, redact func(string) string) (string, error) {
	value, present := m[key]
	if !present && !required {
		return redact(fallback), nil
	}
	s, ok := value.(string)
	if !ok || !utf8.ValidString(s) || len(s) > max || required && strings.TrimSpace(s) == "" {
		return "", monitoringInvalid()
	}
	s = redact(s)
	if len(s) > max {
		return "", monitoringInvalid()
	}
	return s, nil
}
func monitoringValue(m Obj) (any, error) {
	value, present := m["value"]
	if !present {
		return nil, monitoringInvalid()
	}
	if value == nil {
		return nil, nil
	}
	var n float64
	switch v := value.(type) {
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case float64:
		n = v
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return nil, monitoringInvalid()
		}
	default:
		return nil, monitoringInvalid()
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e15 {
		return nil, monitoringInvalid()
	}
	return value, nil
}
func monitoringArray(body Obj, key string, max int) ([]any, error) {
	value, present := body[key]
	if !present {
		return []any{}, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > max {
		return nil, monitoringInvalid()
	}
	return items, nil
}

func validatePressureMonitoring(body, run Obj, capturedAt time.Time) (Obj, int64, error) {
	raw, encodeError := json.Marshal(body)
	if encodeError != nil {
		return nil, 0, monitoringInvalid()
	}
	if len(raw) > monitoringBodyLimit {
		return nil, 0, api(413, "monitoring_too_large", "单次监控正文不能超过512KiB")
	}
	if _, err := monitoringFields(body, []string{"revision", "component", "instance", "source", "window"}, []string{"summary", "health", "metrics", "series", "findings"}); err != nil {
		return nil, 0, err
	}
	revision, ok := integer(body["revision"])
	if !ok || revision < 0 || revision > 128 || !contains([]string{"es", "mysql", "redis", "kafka", "jvm"}, str(body["component"])) {
		return nil, 0, monitoringInvalid()
	}
	redact := pressureRedactor(run)
	instance, err := monitoringText(body, "instance", true, 200, "", redact)
	if err != nil {
		return nil, 0, err
	}
	source, err := monitoringFields(body["source"], []string{"name"}, []string{"reference", "collected_at"})
	if err != nil {
		return nil, 0, err
	}
	sourceName, err := monitoringText(source, "name", true, 200, "", redact)
	if err != nil {
		return nil, 0, err
	}
	reference, err := monitoringText(source, "reference", false, 2000, "", redact)
	if err != nil {
		return nil, 0, err
	}
	collectedAt := capturedAt.UTC()
	if value, present := source["collected_at"]; present {
		s, ok := value.(string)
		if !ok || len(s) > 64 {
			return nil, 0, monitoringInvalid()
		}
		if collectedAt, err = time.Parse(time.RFC3339Nano, s); err != nil {
			return nil, 0, monitoringInvalid()
		}
	}
	window, err := monitoringFields(body["window"], []string{"start", "end"}, nil)
	if err != nil {
		return nil, 0, err
	}
	if len(str(window["start"])) > 64 || len(str(window["end"])) > 64 {
		return nil, 0, monitoringInvalid()
	}
	start, err := time.Parse(time.RFC3339Nano, str(window["start"]))
	if err != nil {
		return nil, 0, monitoringInvalid()
	}
	end, err := time.Parse(time.RFC3339Nano, str(window["end"]))
	if err != nil || start.After(end) {
		return nil, 0, monitoringInvalid()
	}
	alignment, warnings := "CONFIRMED", []any{}
	runStart, startErr := time.Parse(time.RFC3339Nano, str(run["started_at"]))
	runEnd, endErr := time.Parse(time.RFC3339Nano, str(run["finished_at"]))
	if startErr != nil || endErr != nil || runStart.After(runEnd) {
		alignment = "UNCONFIRMED"
		warnings = append(warnings, "任务缺少有效起止时间，监控采集范围与任务时间的对齐尚未确认。")
	} else if end.Before(runStart) || start.After(runEnd) {
		return nil, 0, api(400, "monitoring_window_mismatch", "监控采集范围须与任务运行时间有交集")
	}
	summary, err := monitoringText(body, "summary", false, 4000, "", redact)
	if err != nil {
		return nil, 0, err
	}
	health := "UNKNOWN"
	if value, present := body["health"]; present {
		health, ok = value.(string)
		if !ok || !contains([]string{"UNKNOWN", "NORMAL", "WARNING", "CRITICAL"}, health) {
			return nil, 0, monitoringInvalid()
		}
	}
	entry := Obj{"entry_id": digest([]byte(str(body["component"]) + "\x00" + str(body["instance"]))), "component": body["component"], "instance": instance, "source": Obj{"name": sourceName, "reference": reference, "collected_at": collectedAt.UTC().Format(time.RFC3339Nano)}, "window": Obj{"start": start.UTC().Format(time.RFC3339Nano), "end": end.UTC().Format(time.RFC3339Nano)}, "summary": summary, "health": health, "time_alignment": alignment, "warnings": warnings}
	totalPoints, observationCount := 0, 0
	for _, spec := range []struct {
		name string
		max  int
	}{{"metrics", 64}, {"series", 12}, {"findings", 32}} {
		items, err := monitoringArray(body, spec.name, spec.max)
		if err != nil {
			return nil, 0, err
		}
		observationCount += len(items)
		out, keys := []any{}, map[string]bool{}
		for _, value := range items {
			if spec.name == "findings" {
				finding, err := monitoringFields(value, []string{"level", "title"}, []string{"detail"})
				if err != nil || !contains([]string{"INFO", "WARNING", "CRITICAL"}, str(finding["level"])) {
					return nil, 0, monitoringInvalid()
				}
				title, err := monitoringText(finding, "title", true, 500, "", redact)
				if err != nil {
					return nil, 0, err
				}
				detail, err := monitoringText(finding, "detail", false, 4000, "", redact)
				if err != nil {
					return nil, 0, err
				}
				out = append(out, Obj{"level": finding["level"], "title": title, "detail": detail})
				continue
			}
			required := []string{"key", "value"}
			if spec.name == "series" {
				required = []string{"key", "points"}
			}
			m, err := monitoringFields(value, required, []string{"label", "unit"})
			key := str(m["key"])
			if err != nil || !monitoringMetricKey.MatchString(key) || keys[key] {
				return nil, 0, monitoringInvalid()
			}
			keys[key] = true
			label, err := monitoringText(m, "label", false, 200, key, redact)
			if err != nil {
				return nil, 0, err
			}
			if label == "" {
				label = redact(key)
			}
			unit, err := monitoringText(m, "unit", false, 32, "", redact)
			if err != nil {
				return nil, 0, err
			}
			item := Obj{"key": redact(key), "label": label, "unit": unit}
			if spec.name == "metrics" {
				item["value"], err = monitoringValue(m)
				if err != nil {
					return nil, 0, err
				}
			} else {
				points, ok := m["points"].([]any)
				if !ok || len(points) == 0 || len(points) > 2000 {
					return nil, 0, monitoringInvalid()
				}
				totalPoints += len(points)
				if totalPoints > 6000 {
					return nil, 0, monitoringInvalid()
				}
				cleanPoints, last := []any{}, int64(0)
				for i, value := range points {
					point, err := monitoringFields(value, []string{"timestamp_ms", "value"}, nil)
					if err != nil {
						return nil, 0, err
					}
					timestamp, ok := integer(point["timestamp_ms"])
					instant := time.UnixMilli(timestamp)
					if !ok || i > 0 && timestamp <= last || instant.Before(start) || instant.After(end) {
						return nil, 0, monitoringInvalid()
					}
					last = timestamp
					numeric, err := monitoringValue(point)
					if err != nil {
						return nil, 0, err
					}
					cleanPoints = append(cleanPoints, Obj{"timestamp_ms": timestamp, "value": numeric})
				}
				item["points"] = cleanPoints
			}
			out = append(out, item)
		}
		entry[spec.name] = out
	}
	if observationCount == 0 {
		return nil, 0, monitoringInvalid()
	}
	if len(encoded(entry)) > monitoringBodyLimit {
		return nil, 0, api(413, "monitoring_too_large", "脱敏后的单次监控正文不能超过512KiB")
	}
	return entry, revision, nil
}

func emptyPressureMonitoring(projectID, reportID string) Obj {
	return Obj{"schema_version": "1", "project_id": projectID, "report_id": reportID, "revision": 0, "updated_at": "", "entries": []any{}, "history": []any{}}
}
func publicPressureMonitoring(snapshot Obj) Obj {
	out := Obj{}
	for _, name := range []string{"schema_version", "project_id", "report_id", "revision", "updated_at", "entries", "history"} {
		out[name] = snapshot[name]
	}
	return out
}
func pressureMonitoringWriteResult(snapshot Obj) Obj {
	if receipt, ok := snapshot["batch_result"].(map[string]any); ok {
		return receipt
	}
	out := Obj{}
	for _, name := range []string{"schema_version", "project_id", "report_id", "revision", "updated_at"} {
		out[name] = snapshot[name]
	}
	for _, value := range arr(snapshot["entries"]) {
		if obj(value)["entry_id"] == snapshot["written_entry_id"] {
			out["entry"] = value
			break
		}
	}
	return out
}
func (p *Platform) pressureMonitoring(reportID string, revision int64) (Obj, error) {
	if _, err := p.pressureSelect(reportID, "JSON_OBJECT('id',id)"); err != nil {
		return nil, err
	}
	snapshot, err := monitoringReadQ(p.DB, p.projectID(), reportID, revision)
	if err != nil {
		return nil, err
	}
	return publicPressureMonitoring(snapshot), nil
}
func monitoringQueryRevision(values url.Values) (int64, error) {
	for name, items := range values {
		if !contains([]string{"revision", "component", "search", "limit", "offset", "export"}, name) || len(items) != 1 {
			return 0, monitoringInvalid()
		}
	}
	if value, present := values["export"]; present && value[0] != "1" {
		return 0, monitoringInvalid()
	}
	items, present := values["revision"]
	if !present {
		return 0, nil
	}
	if len(items) != 1 {
		return 0, monitoringInvalid()
	}
	revision, err := strconv.ParseInt(items[0], 10, 64)
	if err != nil || revision < 1 || revision > 128 {
		return 0, monitoringInvalid()
	}
	return revision, nil
}
func (p *Platform) writePressureMonitoring(reportID, key string, body Obj) (Obj, error) {
	return p.writePressureMonitoringRequest(reportID, key, body, false)
}
func (p *Platform) writePressureMonitoringRequest(reportID, key string, body Obj, batch bool) (Obj, error) {
	if err := validID(reportID); err != nil {
		return nil, err
	}
	if !utf8.ValidString(key) || strings.TrimSpace(key) == "" || utf8.RuneCountInString(key) > 200 {
		return nil, api(400, "idempotency_required", "须提供稳定的 Idempotency-Key（1至200字）")
	}
	raw, encodeError := json.Marshal(body)
	if encodeError != nil {
		return nil, monitoringInvalid()
	}
	limit := monitoringBodyLimit
	if batch {
		limit = monitoringBatchBodyLimit
	}
	if len(raw) > limit {
		return nil, api(413, "monitoring_too_large", "监控正文超过当前写入接口的字节上限")
	}
	keyHash, requestHash := digest([]byte(key)), digest(encoded(body))
	if batch {
		// Keep the historical single-write digest unchanged. Batch requests use
		// a separate operation domain when sharing the same idempotency key.
		requestHash = digest(encoded(Obj{"operation": "jvm-batch", "body": body}))
	}
	var result Obj
	err := p.transaction(func(tx *Tx) error {
		if err := p.requireActiveQ(tx); err != nil {
			return err
		}
		var status, runID string
		err := tx.QueryRow("SELECT status,run_id FROM pressure_reports WHERE id=? AND project_id=? FOR UPDATE", reportID, p.projectID()).Scan(&status, &runID)
		if errors.Is(err, sql.ErrNoRows) {
			return api(404, "pressure_report_missing", "报告不存在或尚未生成")
		}
		if err != nil {
			return err
		}
		if status != "READY" {
			return api(409, "report_not_ready", "只能向READY报告写入监控补充数据")
		}
		var priorHash, priorRaw string
		err = tx.QueryRow("SELECT request_hash,data FROM monitoring_revisions WHERE project_id=? AND report_id=? AND key_hash=? LIMIT 1", p.projectID(), reportID, keyHash).Scan(&priorHash, &priorRaw)
		if err == nil {
			if priorHash != requestHash {
				return api(409, "idempotency_conflict", "此幂等键已用于另一监控请求")
			}
			prior, err := decode([]byte(priorRaw))
			if err != nil {
				return err
			}
			if receipt, ok := prior["write_result"].(map[string]any); ok {
				result = receipt
				return nil
			}
			prior, err = monitoringReadQ(tx, p.projectID(), reportID, int64(number(prior["revision"])))
			if err != nil {
				return err
			}
			result = pressureMonitoringWriteResult(prior)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var runRaw string
		if err = tx.QueryRow("SELECT data FROM runs WHERE id=? AND project_id=?", runID, p.projectID()).Scan(&runRaw); err != nil {
			return err
		}
		run, err := decode([]byte(runRaw))
		if err != nil {
			return err
		}
		run, err = p.openStoredRun(run)
		if err != nil {
			return err
		}
		capturedAt := time.Now().UTC()
		var entries []any
		var revision int64
		if batch {
			entries, revision, err = validatePressureMonitoringBatch(body, run, capturedAt)
		} else {
			var entry Obj
			entry, revision, err = validatePressureMonitoring(body, run, capturedAt)
			entries = []any{entry}
		}
		if err != nil {
			return err
		}
		current, err := monitoringReadQ(tx, p.projectID(), reportID, 0)
		if err != nil {
			return err
		}
		if int64(number(current["revision"])) != revision {
			return api(409, "monitoring_revision_conflict", "监控补充版本已更新，请读取最新版本后重试")
		}
		snapshot, err := mergePressureMonitoringEntries(current, entries, capturedAt.Format(time.RFC3339Nano), batch)
		if err != nil {
			return err
		}
		err = storeMonitoringRevision(tx, p.projectID(), reportID, keyHash, requestHash, "", snapshot)
		if err != nil {
			return err
		}
		result = pressureMonitoringWriteResult(snapshot)
		return nil
	})
	return result, err
}
func (p *Platform) pressureMonitoringSummary(reportID string) (Obj, error) {
	snapshot, err := monitoringMetadata(p.DB, p.projectID(), reportID, 0)
	if err != nil {
		return nil, err
	}
	if number(snapshot["revision"]) == 0 {
		return Obj{"revision": 0, "updated_at": "", "instance_count": 0, "components": []any{}}, nil
	}
	return obj(snapshot["monitoring_summary"]), nil
}
func pressureMonitoringExtensions(report, summary Obj) {
	extensions := clone(obj(report["extensions"]))
	for _, value := range arr(summary["components"]) {
		component := str(value)
		extensions[component] = Obj{"status": "AVAILABLE", "label": "已写入外部数据", "origin": "external_monitoring"}
		if component != "jvm" {
			extensions["middleware"] = Obj{"status": "AVAILABLE", "label": "已写入外部数据", "origin": "external_monitoring"}
		}
	}
	report["extensions"] = extensions
}
