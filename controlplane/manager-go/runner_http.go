package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pressurecooker/execution/protocol"
)

var objectSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (h Handler) runnerRoute(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "POST" {
		return api(405, "method_not_allowed", "Method not allowed")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024+1))
	if e != nil || len(raw) > 4*1024*1024 {
		return api(413, "body_too_large", "Runner body exceeds 4MiB")
	}
	runner, e := protocol.VerifyRequest(r, raw, h.P.Config.AgentToken, time.Now())
	if e != nil {
		return api(401, "runner_auth_required", "Invalid runner authentication")
	}
	body, e := decode(raw)
	if e != nil {
		return api(400, "invalid_json", "Invalid runner JSON")
	}
	var result Obj
	switch r.URL.Path {
	case "/api/runner/claim":
		result, e = h.P.runnerClaim(r.Context(), runner, body)
	case "/api/runner/heartbeat":
		result, e = h.P.runnerHeartbeat(runner, body)
	case protocol.CallbackPath:
		result, e = h.P.runnerCallback(r.Context(), runner, body)
	default:
		return api(404, "not_found", "Runner endpoint not found")
	}
	if e != nil {
		if deleted, found, deletedErr := h.P.deletedRunnerResult(runner, body); deletedErr != nil {
			e = deletedErr
		} else if found {
			result = deleted
			e = nil
		}
	}
	if e == nil {
		sendJSON(w, r, 200, result)
	}
	return e
}
func runnerIdentity(run Obj, runner string, body Obj) error {
	if str(run["execution_mode"]) != "redis" || str(body["attempt_id"]) != str(run["attempt_id"]) || str(obj(run["cluster_snapshot"])["controller_id"]) != runner {
		return api(409, "runner_fenced", "Task attempt or controller no longer matches")
	}
	return nil
}
func (p *Platform) runnerClaim(ctx context.Context, runner string, body Obj) (Obj, error) {
	if result, found, e := p.deletedRunnerResult(runner, body); e != nil || found {
		return result, e
	}
	id := str(body["task_id"])
	if validID(id) != nil {
		return nil, api(400, "invalid_task", "Invalid task")
	}
	lease := str(body["lease_token"])
	if !strings.HasPrefix(lease, runner+"/"+id+"/"+str(body["attempt_id"])+"/") {
		return nil, api(409, "lease_not_owned", "Execution lease is not owned")
	}
	q, e := p.runnerQueue()
	if e != nil {
		return nil, e
	}
	defer q.Close()
	owned, e := q.LeaseOwned(ctx, lease)
	if e != nil {
		return nil, api(503, "lease_unavailable", "Execution lease cannot be verified")
	}
	if !owned {
		return nil, api(409, "lease_not_owned", "Execution lease is not owned")
	}
	result := Obj{"execute": false}
	e = p.transaction(func(tx *Tx) error {
		if e := lockClusterConfigurations(tx); e != nil {
			return e
		}
		run, e := get(tx, "runs", id)
		if e != nil {
			return e
		}
		if e = runnerIdentity(run, runner, body); e != nil {
			return e
		}
		owned, e := q.OwnsWorkers(ctx, lease, stringList(run["workers"])...)
		if e != nil {
			return api(503, "lease_unavailable", "Execution lease cannot be verified")
		}
		if !owned {
			return api(409, "lease_not_owned", "Execution lease does not cover the selected workers")
		}
		result["status"] = run["status"]
		result["started_at"] = run["started_at"]
		result["previous_lease_token"] = run["runner_lease_token"]
		result["result_required"] = boolean(run["runner_reconciled"]) && str(run["runner_event_id"]) == ""
		if str(run["status"]) != "QUEUED" {
			return nil
		}
		// Older installations may already have overlapping queued runs. The
		// worker lease serializes them; only physical owners block a claim.
		if e = requireWorkersUnoccupied(tx, arr(run["workers"]), id, false); e != nil {
			return e
		}
		started := str(body["started_at"])
		if started == "" {
			started = now()
		}
		if t, e := time.Parse(time.RFC3339Nano, started); e != nil || t.Before(time.Now().Add(-10*time.Minute)) || t.After(time.Now().Add(5*time.Minute)) {
			return api(400, "invalid_started_at", "Invalid claim timestamp")
		}
		run, e = p.openStoredRun(run)
		if e != nil {
			return e
		}
		run["controller_id"] = runner
		run["status"] = "PREPARING"
		run["runner_lease_token"] = lease
		run["started_at"] = started
		run["runner_heartbeat_at"] = now()
		if e = p.saveRun(tx, run); e != nil {
			return e
		}
		result = Obj{"execute": true, "status": "PREPARING"}
		return nil
	})
	return result, e
}

var runnerRank = map[string]int{"QUEUED": 0, "PREPARING": 1, "RUNNING": 2, "STOPPING": 3, "UPLOADING": 4, "CALLBACK_PENDING": 5}

func (p *Platform) runnerHeartbeat(runner string, body Obj) (Obj, error) {
	if result, found, e := p.deletedRunnerResult(runner, body); e != nil || found {
		return result, e
	}
	id := str(body["task_id"])
	var result Obj
	e := p.transaction(func(tx *Tx) error {
		run, e := get(tx, "runs", id)
		if e != nil {
			return e
		}
		if e = runnerIdentity(run, runner, body); e != nil {
			return e
		}
		run, e = p.openStoredRun(run)
		if e != nil {
			return e
		}
		current := str(run["status"])
		next := str(body["status"])
		rank, ok := runnerRank[next]
		if !ok || next == "QUEUED" {
			return api(400, "invalid_runner_state", "Invalid runner state")
		}
		if contains(terminal, current) {
			result = Obj{"accepted": false, "status": current, "stop": true}
			return nil
		}
		if rank >= runnerRank[current] {
			run["status"] = next
		}
		run["runner_heartbeat_at"] = now()
		if d, ok := body["callback_delivery"].(map[string]any); ok {
			run["callback_delivery"] = safeCallbackDelivery(d)
		}
		if e = p.saveRun(tx, run); e != nil {
			return e
		}
		result = Obj{"accepted": true, "status": run["status"], "stop": str(run["stop_mode"]) != ""}
		return nil
	})
	return result, e
}
func safeCallbackDelivery(d Obj) Obj {
	status := str(d["status"])
	if !contains([]string{"PENDING", "DELIVERING", "RETRY", "DELIVERED", "FAILED"}, status) {
		status = "PENDING"
	}
	errorCode := str(d["last_error"])
	if errorCode != "" {
		errorCode = "callback_delivery_failed"
	}
	next := str(d["next_attempt_at"])
	if next != "" {
		if _, e := time.Parse(time.RFC3339Nano, next); e != nil {
			next = ""
		}
	}
	return Obj{"status": status, "attempts": min(max(int(number(d["attempts"])), 0), 1000000), "last_status_code": min(max(int(number(d["last_status_code"])), 0), 599), "last_error": errorCode, "next_attempt_at": next}
}
func (p *Platform) runnerCallback(ctx context.Context, runner string, body Obj) (Obj, error) {
	if result, found, e := p.deletedRunnerResult(runner, body); e != nil || found {
		return result, e
	}
	var cb protocol.Callback
	if e := decodeInto(body, &cb); e != nil || validID(cb.TaskID) != nil || validID(cb.EventID) != nil || cb.RunnerID != runner || !contains(terminal, cb.Status) {
		return nil, api(400, "invalid_callback", "Invalid completion event")
	}
	run, e := p.get("runs", cb.TaskID)
	if e != nil {
		return nil, e
	}
	if e = runnerIdentity(run, runner, body); e != nil {
		return nil, e
	}
	hash := digest(encoded(body))
	var old string
	e = p.DB.QueryRow("SELECT digest FROM runner_events WHERE id=?", cb.EventID).Scan(&old)
	if e == nil {
		if old != hash {
			return nil, api(409, "event_conflict", "Completion event content changed")
		}
		return Obj{"accepted": true, "duplicate": true}, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if cb.StartedAt == "" {
		cb.StartedAt = str(run["started_at"])
	}
	if cb.StartedAt == "" {
		cb.StartedAt = str(run["created_at"])
	}
	if _, e = time.Parse(time.RFC3339Nano, cb.StartedAt); e != nil {
		return nil, api(400, "invalid_callback", "Invalid start timestamp")
	}
	if _, e = time.Parse(time.RFC3339Nano, cb.FinishedAt); e != nil {
		return nil, api(400, "invalid_callback", "Invalid finish timestamp")
	}
	if str(run["started_at"]) != "" && cb.StartedAt != str(run["started_at"]) {
		return nil, api(409, "timestamp_conflict", "Start timestamp differs from claimed task")
	}
	receiptRun := clone(run)
	receiptRun["started_at"] = cb.StartedAt
	prefix, e := rawPrefix(receiptRun)
	if e != nil {
		return nil, e
	}
	if len(cb.Artifacts) > 3 || len(cb.MissingFiles) > 3 || len(cb.InputArtifacts) > len(arr(run["datasets"]))*max(1, len(arr(run["workers"]))+1) {
		return nil, api(400, "invalid_receipt", "Too many receipt files")
	}
	nodeEvidence, e := pressureValidateNodeEvidence(run, cb.NodeEvidence)
	if e != nil {
		return nil, e
	}
	store, e := p.objectStore()
	if e != nil {
		return nil, e
	}
	files := []any{}
	seen := map[string]bool{}
	for _, ref := range cb.Artifacts {
		name := path.Base(ref.Key)
		if ref.Name != "" && ref.Name != name {
			return nil, api(400, "invalid_receipt", "Artifact name mismatch")
		}
		if !contains(rawFiles, name) || seen[name] || ref.Bucket != p.Config.ArchiveBucket || ref.Key != prefix+name || !objectSHA.MatchString(ref.SHA256) || ref.Size < 0 {
			return nil, api(400, "invalid_receipt", "Artifact object does not match task")
		}
		seen[name] = true
		verifyCtx, cancel := context.WithTimeout(ctx, p.storeTimeout())
		verified, e := store.Head(verifyCtx, ref)
		cancel()
		if e != nil || verified.SHA256 != ref.SHA256 || verified.Size != ref.Size {
			return nil, api(409, "receipt_unavailable", "Artifact object cannot be verified")
		}
		ref.ContentType = verified.ContentType
		item := dtoObject(ref)
		item["name"] = name
		item["object_key"] = ref.Key
		item["status"] = "ARCHIVED"
		item["download_url"] = "/api/runs/" + cb.TaskID + "/artifacts/" + name
		files = append(files, item)
	}
	for _, ref := range cb.InputArtifacts {
		if ref.Bucket != p.Config.ArchiveBucket || !strings.HasPrefix(ref.Key, "task-inputs/"+objectProject(run)+"/"+cb.TaskID+"/") || strings.Contains(ref.Key, "..") || !objectSHA.MatchString(ref.SHA256) || ref.Size < 0 {
			return nil, api(400, "invalid_receipt", "Input object does not match task")
		}
		verifyCtx, cancel := context.WithTimeout(ctx, p.storeTimeout())
		_, e := store.Head(verifyCtx, ref)
		cancel()
		if e != nil {
			return nil, api(409, "receipt_unavailable", "Input object cannot be verified")
		}
	}
	missing := []any{}
	for _, name := range rawFiles {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for _, name := range cb.MissingFiles {
		if !contains(rawFiles, name) || seen[name] {
			return nil, api(400, "invalid_receipt", "Invalid missing file")
		}
	}
	archiveStatus := "ARCHIVED"
	threadFiles, e := p.pressureThreadArtifactReceipts(ctx, run, nodeEvidence)
	if e != nil {
		return nil, e
	}
	files = append(files, threadFiles...)
	if len(files) == 0 {
		archiveStatus = "EMPTY"
	} else if len(missing) > 0 {
		archiveStatus = "PARTIAL"
	}
	e = p.transaction(func(tx *Tx) error {
		current, e := get(tx, "runs", cb.TaskID)
		if e != nil {
			return e
		}
		if e = runnerIdentity(current, runner, body); e != nil {
			return e
		}
		var prior string
		e = tx.QueryRow("SELECT digest FROM runner_events WHERE id=? FOR UPDATE", cb.EventID).Scan(&prior)
		if e == nil {
			if prior != hash {
				return api(409, "event_conflict", "Completion event content changed")
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if str(current["runner_event_id"]) != "" {
			return api(409, "completion_conflict", "Task already has a completion event")
		}
		if contains(terminal, str(current["status"])) && str(current["status"]) != "STOPPED" && !boolean(current["runner_reconciled"]) {
			return api(409, "completion_conflict", "Task is already terminal")
		}
		current, e = p.openStoredRun(current)
		if e != nil {
			return e
		}
		status := cb.Status
		if boolean(current["runner_reconciled"]) {
			status = str(current["status"])
			current["runner_reported_status"] = cb.Status
		}
		if str(current["stop_mode"]) != "" || str(current["status"]) == "STOPPED" {
			status = "STOPPED"
		}
		current["status"] = status
		current["started_at"] = cb.StartedAt
		current["finished_at"] = cb.FinishedAt
		current["exit_code"] = cb.ExitCode
		current["runner_event_id"] = cb.EventID
		current["input_artifacts"] = cb.InputArtifacts
		storedEvidence := []any{}
		for _, evidence := range nodeEvidence {
			storedEvidence = append(storedEvidence, dtoObject(evidence))
		}
		current["node_evidence"] = storedEvidence
		current["callback_delivery"] = Obj{"status": "DELIVERED", "attempts": max(1, int(number(obj(current["callback_delivery"])["attempts"]))), "last_status_code": 200, "last_error": "", "next_attempt_at": ""}
		current["raw_archive"] = Obj{"status": archiveStatus, "prefix": prefix, "files": files, "missing_files": missing, "attempts": 1, "archived_at": now()}
		delete(current, "runner_lease_token")
		if e = p.saveRun(tx, current); e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO runner_events(id,task_id,created_at,digest) VALUES(?,?,?,?)", cb.EventID, cb.TaskID, now(), hash)
		if e != nil {
			return e
		}
		targets, e := pressureTargets(current["report_targets"])
		if e != nil {
			return e
		}
		_, e = p.enqueuePressureQ(tx, current, targets, "TASK", "")
		return e
	})
	return Obj{"accepted": true}, e
}
func (p *Platform) streamRunnerArtifact(w http.ResponseWriter, r *http.Request, id, name string) (bool, error) {
	run, e := p.get("runs", id)
	if e != nil {
		return true, e
	}
	if str(run["execution_mode"]) != "redis" {
		return false, nil
	}
	if !contains(terminal, str(run["status"])) {
		return true, api(409, "run_active", "Original file downloads require a terminal run")
	}
	var threadRef *protocol.ObjectRef
	for _, evidence := range pressureStoredNodeEvidence(run) {
		if protocol.ValidThreadObservations(evidence) && evidence.ThreadObservations != nil && evidence.TaskID == id && evidence.AttemptID == str(run["attempt_id"]) && contains(stringList(run["workers"]), evidence.NodeID) && name == pressureThreadArtifactName(evidence.NodeID) {
			threadRef = evidence.ThreadObservations
		}
	}
	// Historical immutable receipts remain downloadable without interpreting
	// retired application-specific node evidence or accepting arbitrary targets.
	if validID(name) != nil {
		return true, api(400, "invalid_artifact", "Invalid original artifact name")
	}
	for _, v := range arr(obj(run["raw_archive"])["files"]) {
		f := obj(v)
		if str(f["name"]) != name {
			continue
		}
		store, e := p.objectStore()
		if e != nil {
			return true, e
		}
		ref := objectRef(f)
		// Node captures may be prepared on the next calendar day. Ownership
		// follows the task segment, while ordinary engine files retain their
		// exact run-start prefix/name and thread captures retain their receipt.
		parts := strings.SplitN(ref.Key, "/", 4)
		scoped := len(parts) == 4 && parts[1] == id && parts[2] == "raw"
		if scoped {
			_, dateErr := time.Parse("2006-01-02", parts[0])
			scoped = dateErr == nil
		}
		if contains(rawFiles, name) {
			prefix, prefixErr := rawPrefix(run)
			scoped = scoped && prefixErr == nil && ref.Key == prefix+name
		}
		if !scoped || ref.Bucket != p.Config.ArchiveBucket || path.Clean(ref.Key) != ref.Key || strings.ContainsAny(ref.Key, "\\%") || !objectSHA.MatchString(ref.SHA256) || ref.Size < 0 {
			return true, api(409, "invalid_artifact", "Original artifact receipt does not match task")
		}
		if threadRef != nil && (ref.Bucket != threadRef.Bucket || ref.Key != threadRef.Key || ref.SHA256 != threadRef.SHA256 || ref.Size != threadRef.Size) {
			return true, api(409, "invalid_artifact", "Thread observation receipt differs from owned evidence")
		}
		reader, e := store.Get(r.Context(), ref)
		if e != nil {
			return true, e
		}
		defer reader.Close()
		w.Header().Set("Content-Type", ref.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(ref.Size, 10))
		w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
		if r.Method != "HEAD" {
			_, e = io.Copy(w, reader)
		}
		return true, e
	}
	return true, api(404, "artifact_missing", "Original file was not produced")
}

type tailBuffer struct {
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= b.limit {
		b.data = append(b.data[:0], p[n-b.limit:]...)
	} else {
		overflow := len(b.data) + n - b.limit
		if overflow > 0 {
			copy(b.data, b.data[overflow:])
			b.data = b.data[:len(b.data)-overflow]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
