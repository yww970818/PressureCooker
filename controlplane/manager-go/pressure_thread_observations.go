package main

import (
	"context"
	"fmt"

	"pressurecooker/execution/protocol"
	"pressurecooker/manager/reporting"
)

func pressureThreadArtifactName(node string) string { return "thread-observations-" + node + ".csv" }

// Metadata validation precedes any object-store access. The signed callback is
// not enough: require the declared immutable object to exist with the exact SHA
// and size before committing its receipt with the completion event.
func (p *Platform) pressureThreadArtifactReceipts(ctx context.Context, run Obj, items []protocol.NodeEvidence) ([]any, error) {
	files := []any{}
	for _, evidence := range items {
		if evidence.ThreadObservations == nil {
			continue
		}
		if !protocol.ValidThreadObservations(evidence) || evidence.ThreadObservations.Bucket != p.Config.ArchiveBucket {
			return nil, api(400, "invalid_receipt", "Thread observation reference does not match task")
		}
		store, err := p.objectStore()
		if err != nil {
			return nil, err
		}
		ref := *evidence.ThreadObservations
		verifyCtx, cancel := context.WithTimeout(ctx, p.storeTimeout())
		verified, err := store.Head(verifyCtx, ref)
		cancel()
		if err != nil || verified.SHA256 != ref.SHA256 || verified.Size != ref.Size {
			return nil, api(409, "receipt_unavailable", "Thread observations cannot be verified")
		}
		ref.ContentType = verified.ContentType
		name := pressureThreadArtifactName(evidence.NodeID)
		item := dtoObject(ref)
		item["name"], item["object_key"], item["status"] = name, ref.Key, "ARCHIVED"
		item["download_url"] = "/api/runs/" + str(run["id"]) + "/artifacts/" + name
		files = append(files, item)
	}
	return files, nil
}

// Legacy receipts omit the feature entirely. Once any node reports capture
// status, every expected node receives a separate periodic coverage status.
func (p *Platform) pressureLoadThreadObservations(ctx context.Context, run Obj, a *reporting.Analysis) ([]any, error) {
	evidence := pressureStoredNodeEvidence(run)
	enabled := false
	for _, v := range evidence {
		enabled = enabled || v.ThreadObservationsStatus != "" || v.ThreadObservations != nil
	}
	if !enabled {
		return nil, nil
	}
	redact := pressureRedactor(run)
	expected := []string{}
	allowed := map[string]bool{}
	for _, node := range arr(run["workers"]) {
		id := str(node)
		allowed[id] = true
		expected = append(expected, id)
	}
	builder := reporting.NewThreadObservationBuilder(expected)
	byNode := map[string]protocol.NodeEvidence{}
	for _, v := range evidence {
		if allowed[v.NodeID] {
			byNode[v.NodeID] = v
		}
	}
	sources := []any{}
	for _, node := range arr(run["workers"]) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := str(node)
		e, exists := byNode[id]
		src := Obj{"kind": "thread_observations", "source_kind": "thread_observations", "name": "thread-observations.csv", "node_id": reporting.PublicNodeID(id, redact), "status": "MISSING", "error": "执行节点未提供独立线程观测对象", "interval_ms": protocol.ThreadObservationIntervalMS, "valid_rows": 0, "invalid_rows": 0}
		if !exists || e.ThreadObservations == nil {
			if exists && e.ThreadObservationsStatus != "" {
				src["capture_status"] = e.ThreadObservationsStatus
			}
			sources = append(sources, src)
			continue
		}
		src["capture_status"], src["generation"] = e.ThreadObservationsStatus, e.Generation
		if e.TaskID != str(run["id"]) || e.AttemptID != str(run["attempt_id"]) || !protocol.ValidThreadObservations(e) || e.ThreadObservations.Bucket != p.Config.ArchiveBucket {
			src["status"], src["error"] = "INVALID", "线程观测对象身份或范围无效，未采用该证据"
			sources = append(sources, src)
			continue
		}
		ref := *e.ThreadObservations
		src["bucket"], src["key"], src["sha256"], src["size"] = ref.Bucket, ref.Key, ref.SHA256, ref.Size
		src["download_url"] = "/api/runs/" + str(run["id"]) + "/artifacts/" + pressureThreadArtifactName(id)
		store, err := p.objectStore()
		if err != nil {
			return nil, err
		}
		reader, err := store.Get(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("read thread observations: %w", err)
		}
		points := make([]reporting.ThreadObservation, 0, min(3600, protocol.MaxThreadObservationPoints))
		// The protocol parser reads through verifiedReader EOF before emitting any
		// record; both a bad SHA and a malformed suffix reject the complete source.
		complete, err := protocol.ReadThreadObservations(reader, e, func(p protocol.ThreadObservation) error {
			points = append(points, reporting.ThreadObservation{TimestampMS: p.TimestampMS, ActiveThreads: p.ActiveThreads})
			return nil
		})
		reader.Close()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			src["status"], src["error"], src["invalid_rows"] = "PARSE_FAILED", "线程观测下载校验或完整解析失败，未采用任何前缀记录", 1
			sources = append(sources, src)
			continue
		}
		src["download_status"], src["valid_rows"], src["error"] = "VERIFIED", len(points), ""
		src["status"] = "VERIFIED"
		if !complete || e.ThreadObservationsStatus != "ARCHIVED" {
			src["status"] = "INCOMPLETE"
			src["error"] = "采集未正常结束，尾部观测可能缺失"
		}
		builder.AddNode(id, points, complete && e.ThreadObservationsStatus == "ARCHIVED")
		sources = append(sources, src)
	}
	builder.Apply(&a.Dashboard, redact)
	return sources, nil
}
