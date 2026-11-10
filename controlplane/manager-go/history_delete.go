package main

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

func historyDeletePath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 3 && parts[0] == "api" && (parts[1] == "runs" || parts[1] == "pressure-reports")
}

func historySelection(body Obj, clear bool) ([]string, error) {
	if clear {
		if len(body) != 1 || body["confirm"] != true {
			return nil, api(400, "confirmation_required", "清空当前项目须显式确认 confirm=true")
		}
		return nil, nil
	}
	values, ok := body["ids"].([]any)
	if !ok || len(body) != 1 || len(values) < 1 || len(values) > 100 {
		return nil, api(400, "invalid_ids", "请选择1至100条记录")
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		id, ok := value.(string)
		if !ok || validID(id) != nil || seen[id] {
			return nil, api(400, "invalid_ids", "记录标识无效或重复")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
func (h Handler) historyDeleteRoute(w http.ResponseWriter, r *http.Request, method string, body Obj) (bool, error) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "api" || (parts[1] != "runs" && parts[1] != "pressure-reports") {
		return false, nil
	}
	clear := parts[2] == "clear"
	var ids []string
	var e error
	switch {
	case method == "POST" && (clear || parts[2] == "delete"):
		ids, e = historySelection(body, clear)
	case method == "DELETE" && parts[2] != "delete" && !clear:
		if e = validID(parts[2]); e == nil {
			ids = []string{parts[2]}
		}
	default:
		return false, nil
	}
	if e != nil {
		return true, e
	}
	result, e := h.P.deleteHistory(parts[1] == "runs", ids, clear)
	if e == nil {
		sendJSON(w, r, 200, result)
	}
	return true, e
}
func (p *Platform) deleteHistory(runs bool, ids []string, clear bool) (Obj, error) {
	result := Obj{}
	err := p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		table := "pressure_reports"
		if runs {
			table = "runs"
		}
		query := "SELECT id,status,data FROM " + table + " WHERE project_id=?"
		args := []any{p.projectID()}
		if !clear {
			query += " AND id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
			for _, id := range ids {
				args = append(args, id)
			}
		}
		rows, e := tx.Query(query+" ORDER BY id FOR UPDATE", args...)
		if e != nil {
			return e
		}
		type selected struct {
			id, status string
			data       Obj
		}
		items := []selected{}
		for rows.Next() {
			var item selected
			var raw string
			if e = rows.Scan(&item.id, &item.status, &raw); e != nil {
				break
			}
			item.data, e = decode([]byte(raw))
			if e != nil {
				break
			}
			items = append(items, item)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		if !clear && len(items) != len(ids) {
			return api(404, "not_found", "所选记录不存在或不属于当前项目")
		}
		if runs && !clear {
			for _, item := range items {
				if !contains(terminal, item.status) {
					return api(409, "run_active", "只能删除已结束的任务")
				}
			}
		}
		deleted, skipped := 0, 0
		for _, item := range items {
			if runs && !contains(terminal, item.status) {
				skipped++
				continue
			}
			if runs {
				item.data["status"] = item.status
				e = p.deleteRunHistoryQ(tx, item.id, item.data)
			} else {
				e = deletePressureHistoryQ(tx, item.id)
			}
			if e != nil {
				return e
			}
			deleted++
		}
		pending := 0
		if runs {
			pending = deleted
		}
		result = Obj{"deleted_count": deleted, "skipped_count": skipped, "cleanup_pending": pending}
		return nil
	})
	return result, err
}

func deletePressureHistoryQ(tx *Tx, id string) error {
	// Revoking the job row fences publishPressure/failPressure and its heartbeat.
	if _, e := tx.Exec("DELETE FROM pressure_report_jobs WHERE id=?", id); e != nil {
		return e
	}
	for _, table := range append(append(append([]string{}, pressureTables[2:]...), monitoringTables...), "pressure_dashboard_scopes") {
		if _, e := tx.Exec("DELETE FROM "+table+" WHERE report_id=?", id); e != nil {
			return e
		}
	}
	if _, e := tx.Exec("DELETE FROM idempotency WHERE kind='pressure' AND object_id=?", id); e != nil {
		return e
	}
	_, e := tx.Exec("DELETE FROM pressure_reports WHERE id=?", id)
	return e
}
func (p *Platform) deleteRunHistoryQ(tx *Tx, id string, run Obj) error {
	// Persist only task identity and bounded relative prefixes, never task secrets,
	// endpoint credentials, shared inputs or absolute machine paths.
	intent := historyCleanupIntent(run, id, p.projectID(), p.Config.ArchiveEndpoint != "")
	if _, e := tx.Exec("INSERT INTO deleted_runs(id,project_id,created_at,data) VALUES(?,?,?,?)", id, p.projectID(), now(), string(encoded(intent))); e != nil {
		return e
	}
	if e := enqueueHistoryCleanupQ(tx, intent, epoch()+60); e != nil {
		return e
	}
	rows, e := tx.Query("SELECT id FROM pressure_reports WHERE run_id=? ORDER BY id", id)
	if e != nil {
		return e
	}
	reports := []string{}
	for rows.Next() {
		var rid string
		if e = rows.Scan(&rid); e != nil {
			break
		}
		reports = append(reports, rid)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	for _, rid := range reports {
		if e = deletePressureHistoryQ(tx, rid); e != nil {
			return e
		}
	}
	// Legacy report versions may still have old idempotency records.
	if _, e = tx.Exec("DELETE FROM idempotency WHERE object_id IN (SELECT id FROM reports WHERE run_id=?)", id); e != nil {
		return e
	}
	for _, q := range []string{"DELETE FROM reports WHERE run_id=?", "DELETE FROM webhook_deliveries WHERE run_id=?", "DELETE FROM runner_events WHERE task_id=?", "DELETE FROM task_dispatches WHERE id=?", "DELETE FROM idempotency WHERE object_id=?", "DELETE FROM runs WHERE id=?"} {
		if _, e = tx.Exec(q, id); e != nil {
			return e
		}
	}
	return nil
}
func historyCleanupIntent(run Obj, id, project string, configuredStore bool) Obj {
	prefixes := []any{}
	seen := map[string]bool{}
	add := func(prefix string) {
		if validHistoryObjectPrefix(id, project, prefix) && !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	for _, field := range []string{"started_at", "created_at"} {
		stamp, e := time.Parse(time.RFC3339Nano, str(run[field]))
		if e == nil {
			add(stamp.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") + "/" + id + "/")
		}
	}
	receipt := str(obj(run["raw_archive"])["prefix"])
	if strings.HasSuffix(receipt, "raw/") {
		add(strings.TrimSuffix(receipt, "raw/"))
	}
	add("task-inputs/" + project + "/" + id + "/")
	remoteRequired := configuredStore || str(run["execution_mode"]) == "redis" || len(arr(run["input_artifacts"])) > 0 || len(arr(run["node_evidence"])) > 0
	archive := obj(run["raw_archive"])
	for _, v := range arr(archive["files"]) {
		ref := obj(v)
		// archiveRun names a prospective object before checking whether storage is
		// enabled. Its explicit DISABLED receipt/file pair carries only a local
		// placeholder; actual object refs and ambiguous states remain conservative.
		localPlaceholder := archive["status"] == "DISABLED" && ref["status"] == "DISABLED"
		if str(ref["key"]) != "" || str(ref["bucket"]) != "" || str(ref["url"]) != "" || str(ref["sha256"]) != "" || (str(ref["object_key"]) != "" && !localPlaceholder) {
			remoteRequired = true
		}
	}
	return Obj{"remote_required": remoteRequired, "id": id, "project_id": project, "prefixes": prefixes, "attempt_id": run["attempt_id"], "controller_id": obj(run["cluster_snapshot"])["controller_id"], "status": run["status"]}
}
func validHistoryObjectPrefix(id, project, prefix string) bool {
	if validID(id) != nil || validID(project) != nil {
		return false
	}
	if prefix == "task-inputs/"+project+"/"+id+"/" {
		return true
	}
	parts := strings.Split(prefix, "/")
	if len(parts) != 3 || parts[1] != id || parts[2] != "" {
		return false
	}
	stamp, e := time.Parse("2006-01-02", parts[0])
	return e == nil && stamp.Format("2006-01-02") == parts[0]
}
func enqueueHistoryCleanupQ(tx *Tx, intent Obj, next float64) error {
	_, e := tx.Exec("INSERT INTO artifact_cleanups(id,project_id,created_at,data,next_attempt_at) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE next_attempt_at=GREATEST(next_attempt_at,VALUES(next_attempt_at)),lease_token='',lease_until=0", intent["id"], intent["project_id"], now(), string(encoded(intent)), next)
	return e
}

// Authenticated late producer requests acknowledge deletion and re-sweep the
// task prefixes after upload completion. The retained tombstone also fences
// direct upserts and legacy orphan adoption after cleanup has finished.
func (p *Platform) deletedRunnerResult(runner string, body Obj) (Obj, bool, error) {
	id := str(body["task_id"])
	if validID(id) != nil {
		return nil, false, nil
	}
	var result Obj
	found := false
	e := p.transaction(func(tx *Tx) error {
		var raw string
		e := tx.QueryRow("SELECT data FROM deleted_runs WHERE id=? FOR UPDATE", id).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		intent, e := decode([]byte(raw))
		if e != nil {
			return e
		}
		if str(intent["attempt_id"]) == "" || str(intent["attempt_id"]) != str(body["attempt_id"]) || str(intent["controller_id"]) != runner {
			return api(409, "runner_fenced", "Task attempt or controller no longer matches")
		}
		if e = enqueueHistoryCleanupQ(tx, intent, epoch()+60); e != nil {
			return e
		}
		result = Obj{"accepted": true, "deleted": true, "execute": false, "stop": true, "status": intent["status"]}
		found = true
		return nil
	})
	return result, found, e
}

func validateHistoryImport(tx *Tx) error {
	for _, table := range []string{"deleted_runs", "artifact_cleanups"} {
		rows, e := tx.Query("SELECT id,project_id,data FROM " + table)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id, project, raw string
			if e = rows.Scan(&id, &project, &raw); e != nil {
				break
			}
			intent, decodeErr := decode([]byte(raw))
			if decodeErr != nil {
				e = errors.New("invalid imported deletion intent")
				break
			}
			prefixes, ok := intent["prefixes"].([]any)
			_, remoteOK := intent["remote_required"].(bool)
			if !remoteOK || validID(id) != nil || validID(project) != nil || str(intent["id"]) != id || str(intent["project_id"]) != project || !contains(terminal, str(intent["status"])) || !ok || len(prefixes) < 1 || len(prefixes) > 4 {
				e = errors.New("invalid imported deletion scope")
				break
			}
			for _, v := range prefixes {
				prefix, ok := v.(string)
				if !ok || !validHistoryObjectPrefix(id, project, prefix) {
					e = errors.New("invalid imported deletion prefix")
					break
				}
			}
			if e != nil {
				break
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM deleted_runs d LEFT JOIN projects p ON p.id=d.project_id LEFT JOIN runs r ON r.id=d.id WHERE p.id IS NULL OR r.id IS NOT NULL",
		"SELECT COUNT(*) FROM artifact_cleanups c LEFT JOIN deleted_runs d ON d.id=c.id WHERE d.id IS NULL OR c.project_id<>d.project_id",
	} {
		var n int
		if e := tx.QueryRow(query).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return errors.New("import contains inconsistent deleted task associations")
		}
	}
	return nil
}
