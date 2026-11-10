package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"unicode/utf8"

	"pressurecooker/execution/protocol"
	"pressurecooker/manager/reporting"
)

// Keep sample bodies private to the explicit, project-scoped drilldown. The
// receipt is immutable once accepted; keys are bound before labels are masked.
func pressureValidateRequestSamples(v *protocol.NodeEvidence, redact func(string) string) error {
	bad := func() error {
		return api(400, "invalid_request_samples", "接口留样的身份、内容或大小无效")
	}
	if len(v.RequestSamples) > 64 || len(encoded(v.RequestSamples)) > 128<<10 ||
		(!v.RequestSamplesCaptured && len(v.RequestSamples) > 0) ||
		(v.Status == "MISSING" && v.RequestSamplesCaptured) {
		return bad()
	}
	rows := make([]protocol.RequestSample, 0, len(v.RequestSamples))
	for _, row := range v.RequestSamples {
		if row.SampleKind == "" {
			row.SampleKind = "unknown"
		}
		if row.SampleKind != "unknown" && row.SampleKind != "ordinary" && row.SampleKind != "transaction" {
			return bad()
		}
		if row.Label == "" || len(row.Label) > 4096 || !utf8.ValidString(row.Label) || row.TimestampMS < 0 || row.TimestampMS > 1e16 || row.ElapsedMS < 0 || row.ElapsedMS > 1e12 || len(row.Assertions) > 16 || (!row.AssertionsCaptured && len(row.Assertions) > 0) {
			return bad()
		}
		for _, field := range []struct {
			value *string
			max   int
		}{{row.Method, 32}, {row.URL, 4096}, {row.RequestParams, 4096}, {row.ResponseCode, 256}, {row.ResponseBody, 8192}} {
			if field.value != nil && (len(*field.value) > field.max || !utf8.ValidString(*field.value)) {
				return bad()
			}
		}
		for _, assertion := range row.Assertions {
			if len(assertion.Name) > 128 || len(assertion.Message) > 512 || !utf8.ValidString(assertion.Name) || !utf8.ValidString(assertion.Message) {
				return bad()
			}
		}
		var sample reporting.RequestSample
		if json.Unmarshal(encoded(row), &sample) != nil {
			return bad()
		}
		sample.StepKey = reporting.StableStepKey(row.SampleKind, row.Label)
		sample = reporting.SanitizeRequestSample(sample, redact)
		var cleaned protocol.RequestSample
		if json.Unmarshal(encoded(sample), &cleaned) != nil {
			return bad()
		}
		rows = append(rows, cleaned)
	}
	for len(rows) > 0 && len(encoded(rows)) > 128<<10 {
		rows = rows[:len(rows)-1]
		v.RequestSamplesTruncated = true
	}
	v.RequestSamples = rows
	return nil
}

// Masking can expand a short credential into [REDACTED]. Keep the final stored
// encoded evidence bounded too; preserve log provenance when clipping content.
func pressureBoundStoredNodeEvidence(items []protocol.NodeEvidence) {
	for len(encoded(items)) > protocol.MaxNodeEvidenceBytes {
		largest, bytes := -1, 0
		for i, item := range items {
			if n := len(encoded(item.RequestSamples)); len(item.RequestSamples) > 0 && n > bytes {
				largest, bytes = i, n
			}
		}
		if largest < 0 {
			node, logIndex, longest := -1, -1, 0
			for i, item := range items {
				for j, log := range item.Logs {
					if len(log.Content) > longest {
						node, logIndex, longest = i, j, len(log.Content)
					}
				}
			}
			if node < 0 {
				return
			}
			log := &items[node].Logs[logIndex]
			log.Content = pressureUTF8Prefix(log.Content, longest/2)
			log.Truncated = true
			continue
		}
		samples := items[largest].RequestSamples
		items[largest].RequestSamples = samples[:len(samples)-1]
		items[largest].RequestSamplesTruncated = true
	}
}

func pressureMergeRequestSamples(a *reporting.Analysis, run Obj) bool {
	incoming := []reporting.RequestSample{}
	captured := len(a.RequestSamples) > 0
	redact := pressureRedactor(run)
	for _, receipt := range pressureStoredNodeEvidence(run) {
		captured = captured || receipt.RequestSamplesCaptured
		a.RequestSamplesTruncated = a.RequestSamplesTruncated || receipt.RequestSamplesTruncated
		for _, row := range receipt.RequestSamples {
			var sample reporting.RequestSample
			if json.Unmarshal(encoded(row), &sample) != nil {
				continue
			}
			sample.NodeID = redact(receipt.NodeID)
			sample.SourceKind = "worker_sample"
			sample.SourceName = "request-samples.json"
			incoming = append(incoming, sample)
		}
	}
	reporting.MergeRequestSamples(a, incoming, redact)
	return captured
}

func (p *Platform) pressureRequestSamples(id string, r *http.Request) (Obj, error) {
	report, e := p.pressureSelect(id, "JSON_OBJECT('request_samples',JSON_EXTRACT(data,'$.request_samples'),'request_samples_version',JSON_EXTRACT(data,'$.request_samples_version'),'request_samples_truncated',JSON_EXTRACT(data,'$.request_samples_truncated'))")
	if e != nil {
		return nil, e
	}
	limit, offset, e := pressurePage(r)
	if e != nil {
		return nil, e
	}
	node, step := r.URL.Query().Get("node_id"), r.URL.Query().Get("step_key")
	if step == "" || len(step) > 128 || len(node) > 4096 {
		return nil, api(400, "invalid_scope", "请选择要查看留样的接口")
	}
	items := []any{}
	for _, value := range arr(report["request_samples"]) {
		row := obj(value)
		if str(row["step_key"]) != step || node != "" && str(row["node_id"]) != node {
			continue
		}
		items = append(items, row)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := obj(items[i]), obj(items[j])
		at, bt := number(a["timestamp_ms"]), number(b["timestamp_ms"])
		if at != bt {
			return at > bt
		}
		if str(a["node_id"]) != str(b["node_id"]) {
			return str(a["node_id"]) < str(b["node_id"])
		}
		return str(a["id"]) < str(b["id"])
	})
	total := len(items)
	start := min(offset, total)
	end := min(start+limit, total)
	return Obj{"items": items[start:end], "total": total, "limit": limit, "offset": offset, "detail_version": number(report["request_samples_version"]), "truncated": report["request_samples_truncated"] == true}, nil
}
