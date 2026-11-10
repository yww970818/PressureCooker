package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

func monitoringSpecs() []managerSpec {
	value := map[string]any{"type": []string{"number", "null"}, "minimum": -1e15, "maximum": 1e15}
	key := strSchema(`^[A-Za-z][A-Za-z0-9_.-]{0,99}$`, 1, 100)
	text := func(max int) map[string]any { return strSchema("", 0, max) }
	array := func(item map[string]any, max int) map[string]any {
		return map[string]any{"type": "array", "maxItems": max, "items": item}
	}
	metric := objectSchema(map[string]any{"key": key, "label": text(200), "value": value, "unit": text(32)}, "key", "value")
	point := objectSchema(map[string]any{"timestamp_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 253402300799999}, "value": value}, "timestamp_ms", "value")
	points := array(point, 2000)
	points["minItems"] = 1
	series := objectSchema(map[string]any{"key": key, "label": text(200), "unit": text(32), "points": points}, "key", "points")
	finding := objectSchema(map[string]any{"level": enumSchema("INFO", "WARNING", "CRITICAL"), "title": strSchema(`\S`, 1, 500), "detail": text(4000)}, "level", "title")
	write := func(middleware bool) map[string]any {
		props := map[string]any{
			"reportId": pressureReportIDSchema(), "revision": intSchema(0, 128),
			"idempotencyKey": strSchema(`^[^\x00-\x1f\x7f]*\S[^\x00-\x1f\x7f]*$`, 1, 200),
			"instance":       strSchema(`\S`, 1, 200),
			"source":         objectSchema(map[string]any{"name": strSchema(`\S`, 1, 200), "reference": text(2000), "collected_at": strSchema(`\S`, 1, 64)}, "name"),
			"window":         objectSchema(map[string]any{"start": strSchema(`\S`, 1, 64), "end": strSchema(`\S`, 1, 64)}, "start", "end"),
			"summary":        text(4000), "health": enumSchema("UNKNOWN", "NORMAL", "WARNING", "CRITICAL"),
			"metrics": array(metric, 64), "series": array(series, 12), "findings": array(finding, 32),
		}
		required := []string{"reportId", "revision", "idempotencyKey", "instance", "source", "window"}
		if middleware {
			props["component"] = enumSchema("es", "mysql", "redis", "kafka")
			required = append(required, "component")
		}
		schema := scopedSchema(props, required...)
		choices := []any{}
		for _, name := range []string{"metrics", "series", "findings"} {
			choices = append(choices, map[string]any{"required": []string{name}, "properties": map[string]any{name: map[string]any{"minItems": 1}}})
		}
		schema["anyOf"] = choices
		return schema
	}
	single := write(false)
	entryProps := map[string]any{}
	for _, field := range []string{"instance", "source", "window", "summary", "health", "metrics", "series", "findings"} {
		entryProps[field] = single["properties"].(map[string]any)[field]
	}
	entry := objectSchema(entryProps, "instance", "source", "window")
	entry["anyOf"] = single["anyOf"]
	entries := array(entry, 200)
	entries["minItems"] = 1
	batch := scopedSchema(map[string]any{"reportId": pressureReportIDSchema(), "revision": intSchema(0, 128), "idempotencyKey": single["properties"].(map[string]any)["idempotencyKey"], "entries": entries}, "reportId", "revision", "idempotencyKey", "entries")
	limit, offset := intSchema(1, 200), intSchema(0, 1000000)
	limit["default"], offset["default"] = 20, 0
	return []managerSpec{
		{name: "write_pressure_report_middleware", description: "向本项目READY报告写入ES/MySQL/Redis/Kafka外部监控指标、趋势、发现和来源。须携带当前监控revision（首次0）与稳定idempotencyKey；仅替换相同component+instance并保留补充历史，不修改JTL指标或启动采集/压测。时间范围须与压测重叠。", schema: write(true)},
		{name: "write_pressure_report_jvm", description: "向本项目READY报告写入被测JVM实例的指标、GC/线程趋势、发现和证据来源，组件固定jvm。须携带当前监控revision（首次0）与稳定idempotencyKey；相同请求重试复用原revision/键/参数，新补充用新键。保存独立监控版本，不改写原报告结论或执行压测。", schema: write(false)},
		{name: "write_pressure_report_jvm_batch", description: "批量写入本项目READY报告的JVM监控，适用于上百实例。一次1至200条entries，组件固定jvm；每批只新增一个监控revision，全批校验和保存原子完成，保留其他实例及中间件。同请求重试复用原revision/idempotencyKey/参数。单批4MiB、60000总点，快照最多1000实例/8MiB；返回写入摘要，不回传全部时序。", schema: batch},
		{name: "get_pressure_report_monitoring", description: "分页读取本项目固定报告的外部监控及历史版本元数据，默认limit=20/offset=0，最多200条；可按component和实例名称search筛选。省略revision读最新，正整数读指定补充版本。返回total/instance_count/component_counts区分筛选结果与全量实例；null仍是缺少数据，外部结论不代表JTL验收结果。", schema: scopedSchema(map[string]any{"reportId": pressureReportIDSchema(), "revision": intSchema(1, 128), "component": enumSchema("jvm", "es", "mysql", "redis", "kafka", "host", "k8s"), "search": text(200), "limit": limit, "offset": offset}, "reportId"), readOnly: true},
	}
}

func (m *managerTools) monitoringTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	project, _ := args["projectId"].(string)
	report, _ := args["reportId"].(string)
	path := "/api/pressure-reports/" + url.PathEscape(report) + "/monitoring"
	if name == "get_pressure_report_monitoring" {
		if search, _ := args["search"].(string); len(search) > 200 {
			return nil, errors.New("Monitoring search exceeds 200 UTF-8 bytes")
		}
		query := url.Values{"limit": {"20"}, "offset": {"0"}}
		for _, field := range []string{"revision", "component", "search", "limit", "offset"} {
			if value, ok := args[field]; ok {
				query.Set(field, queryValue(value))
			}
		}
		return m.api.requestProject(ctx, http.MethodGet, path+"?"+query.Encode(), nil, "", project)
	}
	batch := name == "write_pressure_report_jvm_batch"
	if name != "write_pressure_report_middleware" && name != "write_pressure_report_jvm" && !batch {
		return nil, errors.New("Unknown monitoring tool")
	}
	body := map[string]any{}
	for k, v := range args {
		if k != "projectId" && k != "reportId" && k != "idempotencyKey" {
			body[k] = v
		}
	}
	if name == "write_pressure_report_jvm" {
		body["component"] = "jvm"
	}
	maximum := 512 * 1024
	if batch {
		maximum = 4 * 1024 * 1024
		path += "/jvm-batch"
	}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > maximum {
		return nil, errors.New("Monitoring request exceeds bounded payload size")
	}
	// Normalize only this validation copy; forward the original precise numbers.
	var normalized map[string]any
	if json.Unmarshal(raw, &normalized) != nil {
		return nil, errors.New("Invalid monitoring data")
	}
	if batch {
		seen, totalPoints := map[string]bool{}, 0
		for _, value := range normalized["entries"].([]any) {
			entry := value.(map[string]any)
			instance := entry["instance"].(string)
			entryBytes, e := json.Marshal(entry)
			points, valid := monitoringTextAndPointBounds(entry)
			if e != nil || len(entryBytes) > 512*1024 || seen[instance] || !valid || points > 6000 {
				return nil, errors.New("Invalid or oversized JVM batch entry")
			}
			seen[instance] = true
			totalPoints += points
		}
		if totalPoints > 60000 {
			return nil, errors.New("JVM batch exceeds 60000 points")
		}
	} else if points, valid := monitoringTextAndPointBounds(normalized); !valid || points > 6000 {
		return nil, errors.New("Monitoring data exceeds text or point limits")
	}
	key, _ := args["idempotencyKey"].(string)
	return m.api.requestProject(ctx, http.MethodPost, path, body, key, project)
}

func monitoringTextAndPointBounds(value any) (int, bool) {
	// JSON Schema lengths count characters. The management API limits text bytes.
	limits := map[string]int{"instance": 200, "name": 200, "reference": 2000, "collected_at": 64, "start": 64, "end": 64, "label": 200, "unit": 32, "summary": 4000, "title": 500, "detail": 4000}
	points := 0
	var check func(any) bool
	check = func(value any) bool {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if text, ok := child.(string); ok && limits[key] > 0 && len(text) > limits[key] {
					return false
				}
				if key == "points" {
					if list, ok := child.([]any); ok {
						points += len(list)
					}
				}
				if !check(child) {
					return false
				}
			}
		case []any:
			for _, child := range value {
				if !check(child) {
					return false
				}
			}
		}
		return true
	}
	valid := check(value)
	return points, valid
}
