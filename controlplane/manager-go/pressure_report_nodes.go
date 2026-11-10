package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"pressurecooker/execution/protocol"
	"pressurecooker/manager/reporting"
)

// Evidence is optional for old agents. Validate its ownership before redacting a
// copy; the caller still fingerprints the original signed callback for retries.
func pressureValidateNodeEvidence(run Obj, items []protocol.NodeEvidence) ([]protocol.NodeEvidence, error) {
	bad := func() ([]protocol.NodeEvidence, error) {
		return nil, api(400, "invalid_node_evidence", "执行节点日志证据的身份、范围或大小无效")
	}
	if len(items) > len(arr(run["workers"])) || len(items) > 64 || len(encoded(items)) > 3<<20 {
		return bad()
	}
	allowed, seen := map[string]bool{}, map[string]bool{}
	for _, v := range arr(run["workers"]) {
		allowed[str(v)] = true
	}
	redact := pressureRedactor(run)
	out := make([]protocol.NodeEvidence, 0, len(items))
	total := 0
	for _, v := range items {
		if !protocol.ValidThreadObservations(v) || v.ThreadObservationsStatus == "PENDING" {
			return bad()
		}
		if !allowed[v.NodeID] || seen[v.NodeID] || v.TaskID != str(run["id"]) || v.AttemptID != str(run["attempt_id"]) || v.Generation < 0 || (v.Status != "COLLECTED" && v.Status != "MISSING") || (v.Status == "COLLECTED" && v.Generation == 0) || len(v.Logs) > 2 {
			return bad()
		}
		seen[v.NodeID] = true
		for _, stamp := range []string{v.PreparedAt, v.FinishedAt} {
			if stamp != "" {
				if _, e := time.Parse(time.RFC3339Nano, stamp); e != nil {
					return bad()
				}
			}
		}
		logs := make([]protocol.NodeLog, 0, len(v.Logs))
		names := map[string]bool{}
		for _, log := range v.Logs {
			if (log.Name != "jmeter.log" && log.Name != "console.log") || names[log.Name] || (log.Status != "COLLECTED" && log.Status != "MISSING") || len(log.Content) > 32<<10 || !utf8.ValidString(log.Content) || log.Size < int64(len(log.Content)) || log.LineFrom < 0 || log.LineTo < log.LineFrom || (log.Status == "MISSING" && log.Content != "") || (v.Status == "MISSING" && log.Status != "MISSING") {
				return bad()
			}
			names[log.Name] = true
			total += len(log.Content)
			if total > 2<<20 {
				return bad()
			}
			log.Content = redact(log.Content)
			// Redaction may expand very short secrets; preserve the transport limit.
			if len(log.Content) > 32<<10 {
				log.Content = pressureUTF8Prefix(log.Content, 32<<10)
				log.Truncated = true
			}
			logs = append(logs, log)
		}
		v.Logs = logs
		if e := pressureValidateRequestSamples(&v, redact); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	pressureBoundStoredNodeEvidence(out)
	return out, nil
}

func pressureUTF8Prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func pressureStoredNodeEvidence(run Obj) []protocol.NodeEvidence {
	var out []protocol.NodeEvidence
	_ = json.Unmarshal(encoded(run["node_evidence"]), &out)
	return out
}

func pressureNodeLogIssues(a *reporting.Analysis, run Obj) {
	redact := pressureRedactor(run)
	for _, v := range pressureStoredNodeEvidence(run) {
		for _, log := range v.Logs {
			if log.Status != "COLLECTED" || log.Content == "" {
				continue
			}
			issues, _, err := reporting.AnalyzeLogs(context.Background(), strings.NewReader(log.Content), log.Name, redact)
			if err != nil {
				a.Quality.Warnings = append(a.Quality.Warnings, "执行节点日志异常摘要达到解析边界，仅展示部分明细；请结合已保存日志内容核对")
			}
			for _, item := range issues {
				item.NodeID = redact(v.NodeID)
				item.SourceKind = "worker_log"
				if log.LineFrom > 0 {
					item.LineFrom += log.LineFrom - 1
					item.LineTo += log.LineFrom - 1
				}
				a.Issues = append(a.Issues, item)
			}
		}
	}
}

func pressureNodeDetailData(reportID string, a reporting.Analysis, run Obj) ([]any, []any) {
	redact := pressureRedactor(run)
	nodes, logs := []any{}, []any{}
	evidence := map[string]protocol.NodeEvidence{}
	for _, v := range pressureStoredNodeEvidence(run) {
		evidence[redact(v.NodeID)] = v
	}
	seen := map[string]bool{}
	for _, n := range a.Nodes {
		nodes = append(nodes, dtoObject(n))
		seen[n.NodeID] = true
	}
	// Even a run without a usable JTL retains the expected worker cards.
	for _, v := range arr(run["workers"]) {
		id := redact(str(v))
		if !seen[id] {
			nodes = append(nodes, dtoObject(reporting.Node{NodeID: id, Label: id, Expected: true, Status: "MISSING", Metrics: reporting.Metrics{SampleScope: "ordinary_samples"}}))
			seen[id] = true
		}
	}
	for _, value := range nodes {
		n := obj(value)
		id := str(n["node_id"])
		ev := evidence[id]
		got := 0
		empty := 0
		truncated := false
		for _, name := range []string{"jmeter.log", "console.log"} {
			row := Obj{"node_id": id, "step_key": "", "scope": "node", "source_kind": "worker_log", "source_name": name, "status": "MISSING", "content": "", "truncated": false, "line_from": 0, "line_to": 0, "size": 0, "count": 0, "first_at": nil, "last_at": nil}
			for _, l := range ev.Logs {
				if l.Name == name {
					row["status"] = l.Status
					row["content"] = l.Content
					row["truncated"] = l.Truncated
					truncated = truncated || l.Truncated
					row["line_from"] = l.LineFrom
					row["line_to"] = l.LineTo
					row["size"] = l.Size
					if l.Status == "COLLECTED" {
						got++
						if l.Content == "" {
							empty++
							row["status"] = "EMPTY"
						}
					}
				}
			}
			row["generation"] = ev.Generation
			row["prepared_at"] = ev.PreparedAt
			row["finished_at"] = ev.FinishedAt
			row["id"] = pressureEvidenceID(reportID, "node_logs", len(logs))
			logs = append(logs, row)
		}
		status := "MISSING"
		if got == 2 && !truncated {
			status = "COMPLETE"
		} else if got > 0 {
			status = "PARTIAL"
		}
		if empty == 2 && !truncated {
			status = "EMPTY"
		}
		n["log_status"] = status
	}
	for _, v := range a.Issues {
		if v.Category != "sample_failure" && v.SourceKind != "log" {
			continue
		}
		scope := "controller"
		if v.StepKey != "" {
			scope = "step"
		}
		kind := v.SourceKind
		if kind == "log" {
			kind = "jmeter_log"
		}
		logs = append(logs, Obj{"id": pressureEvidenceID(reportID, "node_logs", len(logs)), "node_id": v.NodeID, "step_key": v.StepKey, "scope": scope, "source_kind": kind, "source_name": v.SourceName, "status": "COLLECTED", "content": v.Message, "truncated": false, "line_from": v.LineFrom, "line_to": v.LineTo, "size": len(v.Message), "count": v.Count, "first_at": v.FirstAt, "last_at": v.LastAt})
	}
	return nodes, logs
}

func (p *Platform) pressureNodeChildren(id, kind string, r *http.Request) (Obj, error) {
	column := "JSON_REMOVE(data,'$.dashboard','$.request_samples')"
	if kind == "nodes" {
		column = "JSON_REMOVE(data,'$.dashboard','$.node_logs','$.request_samples')"
	}
	report, e := p.pressureSelect(id, column)
	if e != nil {
		return nil, e
	}
	version := number(report["node_detail_version"])
	if kind == "nodes" {
		items := arr(report["nodes"])
		if items == nil {
			items = []any{}
		}
		return Obj{"items": items, "total": len(items), "detail_version": version}, nil
	}
	if kind != "logs" {
		return nil, api(400, "invalid_kind", "无效报告明细")
	}
	limit, offset, e := pressurePage(r)
	if e != nil {
		return nil, e
	}
	items := []any{}
	node, step := r.URL.Query().Get("node_id"), r.URL.Query().Get("step_key")
	for _, v := range arr(report["node_logs"]) {
		row := obj(v)
		if node != "" && str(row["node_id"]) != node {
			continue
		}
		if step != "" && str(row["step_key"]) != step {
			continue
		}
		items = append(items, row)
	}
	total := len(items)
	start := min(offset, total)
	end := min(start+limit, total)
	return Obj{"items": items[start:end], "total": total, "limit": limit, "offset": offset, "detail_version": version}, nil
}
