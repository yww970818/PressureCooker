package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var portableTables = []string{"deleted_runs", "artifact_cleanups", "workflow_datasets", "task_dispatches", "runner_events", "webhook_deliveries", "clusters", "projects", "plans", "runs", "reports", "idempotency", "audit", "workflows", "api_collections", "environments", "workflow_versions", "api_collection_versions", "plan_workflow_links", "plan_library_state"}
var tableColumns = map[string][]string{"clusters": {"id", "data", "created_at"}, "plans": {"id", "data"}, "runs": {"id", "status", "created_at", "data"}, "reports": {"id", "run_id", "status", "created_at", "data"}, "idempotency": {"kind", "key", "digest", "object_id"}, "audit": {"id", "created_at", "action", "object_id"}}

func init() {
	tableColumns["deleted_runs"] = []string{"id", "project_id", "created_at", "data"}
	tableColumns["artifact_cleanups"] = []string{"id", "project_id", "created_at", "data", "attempts", "next_attempt_at", "lease_token", "lease_until", "last_error"}

	tableColumns["plan_library_state"] = []string{"id", "project_id", "archived", "updated_at"}
	tableColumns["workflow_datasets"] = []string{"id", "project_id", "workflow_id", "node_id", "deleted", "created_at", "data"}
	tableColumns["task_dispatches"] = []string{"id", "project_id", "status", "created_at", "attempts", "next_attempt_at", "payload", "last_error"}
	tableColumns["runner_events"] = []string{"id", "task_id", "created_at", "digest"}
	tableColumns["webhook_deliveries"] = webhookTableColumns
	tableColumns["projects"] = []string{"id", "data", "created_at"}
	for _, table := range projectOwnedTables {
		tableColumns[table] = append(tableColumns[table], "project_id")
	}
	tableColumns["plan_workflow_links"] = []string{"id", "plan_id", "workflow_id", "workflow_revision", "relation", "created_at"}
	for _, table := range []string{"workflows", "api_collections", "environments"} {
		tableColumns[table] = []string{"id", "revision", "deleted", "created_at", "updated_at", "payload", "project_id"}
	}
	for _, table := range []string{"workflow_versions", "api_collection_versions"} {
		tableColumns[table] = []string{"id", "object_id", "revision", "created_at", "payload"}
	}
}

func (p *Platform) exportState() (Obj, error) {
	result := Obj{"version": 2, "storage": "mysql", "created_at": now()}
	e := p.transaction(func(tx *Tx) error {
		for _, table := range portableTables {
			columns := tableColumns[table]
			quoted := []string{}
			for _, column := range columns {
				quoted = append(quoted, "`"+column+"`")
			}
			query := "SELECT " + strings.Join(quoted, ",") + " FROM " + table
			if table == "webhook_deliveries" {
				query += " ORDER BY created_at,event_id"
			} else if table == "plans" {
				query += " ORDER BY seq"
			} else if table == "plan_library_state" {
				query += " ORDER BY updated_at,id"
			} else if table == "audit" {
				query += " ORDER BY id"
			} else if table == "idempotency" {
				query += " ORDER BY kind,`key`"
			} else if table == "pressure_dashboard_scopes" {
				query += " ORDER BY report_id,position,id"
			} else {
				query += " ORDER BY created_at,id"
			}
			rows, e := tx.Query(query)
			if e != nil {
				return e
			}
			items := []any{}
			for rows.Next() {
				values := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for i := range values {
					pointers[i] = &values[i]
				}
				if e = rows.Scan(pointers...); e != nil {
					rows.Close()
					return e
				}
				item := Obj{}
				for i, c := range columns {
					value := values[i]
					if b, ok := value.([]byte); ok {
						value = string(b)
					}
					item[c] = value
				}
				items = append(items, item)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			result[table] = items
		}
		return nil
	})
	return result, e
}
func stateData(v any) (string, error) {
	if s, ok := v.(string); ok {
		if !json.Valid([]byte(s)) {
			return "", fmt.Errorf("invalid imported data JSON")
		}
		m, e := decode([]byte(s))
		if e != nil || m == nil {
			return "", fmt.Errorf("imported data must be object")
		}
		return s, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing imported data")
	}
	return string(encoded(m)), nil
}
func (p *Platform) importState(state Obj) error {
	for _, table := range portableTables {
		if _, exists := state[table]; !exists {
			if contains(monitoringTables, table) || table == "extension_registries" || table == "extension_versions" || table == "deleted_runs" || table == "artifact_cleanups" || contains(pressureTables, table) || table == "plan_library_state" || table == "pressure_dashboard_scopes" || contains(workspaceTables, table) || (table == "projects" || table == "clusters" || table == "webhook_deliveries" || table == "workflow_datasets" || table == "task_dispatches" || table == "runner_events") {
				state[table] = []any{}
			} else {
				return fmt.Errorf("import missing table %s", table)
			}
		}
		if _, ok := state[table].([]any); !ok {
			return fmt.Errorf("invalid import table %s", table)
		}
	}
	return p.transaction(func(tx *Tx) error {
		for _, table := range portableTables {
			var count int
			if e := tx.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil {
				return e
			}
			if table == "projects" {
				var defaults int
				if e := tx.QueryRow("SELECT COUNT(*) FROM projects WHERE id=?", defaultProjectID).Scan(&defaults); e != nil {
					return e
				}
				count -= defaults
			}
			if count != 0 {
				return fmt.Errorf("import destination must be empty; %s has rows", table)
			}
		}
		for _, table := range portableTables {
			if table == "projects" && len(arr(state[table])) > 0 {
				if _, e := tx.Exec("DELETE FROM projects WHERE id=?", defaultProjectID); e != nil {
					return e
				}
			}
			columns := tableColumns[table]
			quoted := []string{}
			for _, c := range columns {
				quoted = append(quoted, "`"+c+"`")
			}
			query := "INSERT INTO " + table + "(" + strings.Join(quoted, ",") + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
			for _, row := range arr(state[table]) {
				m := clone(obj(row))
				if table == "deleted_runs" || table == "artifact_cleanups" {
					raw, e := stateData(m["data"])
					if e != nil {
						return e
					}
					intent, e := decode([]byte(raw))
					if e != nil {
						return e
					}
					if _, present := intent["remote_required"]; !present {
						intent["remote_required"] = true
					}
					if _, ok := intent["remote_required"].(bool); !ok {
						return fmt.Errorf("invalid imported remote cleanup requirement")
					}
					m["data"] = string(encoded(intent))
				}
				if table == "artifact_cleanups" {
					m["lease_token"] = ""
					m["lease_until"] = 0
					m["next_attempt_at"] = 0
				}
				if table == "pressure_report_jobs" {
					m["lease_token"] = ""
					m["lease_until"] = 0
					m["next_attempt_at"] = 0
					if m["status"] == "PROCESSING" {
						m["status"] = "RETRY"
					}
				}
				if table == "pressure_reports" {
					raw, e := stateData(m["data"])
					if e != nil {
						return e
					}
					report, e := decode([]byte(raw))
					if e != nil {
						return e
					}
					if report["status"] == "PROCESSING" {
						report["status"] = "RETRY"
						m["status"] = "RETRY"
					}
					report["originals_status"] = "NOT_CHECKED_AFTER_RESTORE"
					report["originals_notice"] = "数据库备份不包含MinIO原件；下载和重新解析前需确认对象已单独恢复"
					m["data"] = string(encoded(report))
				}
				if len(m) == 0 {
					return fmt.Errorf("invalid %s row", table)
				}
				values := make([]any, len(columns))
				for i, c := range columns {
					v := m[c]
					if c == "project_id" && v == nil {
						v = defaultProjectID
					}
					if c == "data" && !contains(pressureTables, table) && table != "extension_registries" && table != "extension_versions" && !contains(monitoringTables, table) {
						var e error
						v, e = stateData(v)
						if e != nil {
							return e
						}
						data, _ := decode([]byte(v.(string)))
						if str(data["id"]) != str(m["id"]) {
							return fmt.Errorf("import %s has inconsistent object ID", table)
						}
						if contains(projectOwnedTables, table) {
							projectID := str(m["project_id"])
							if projectID == "" {
								projectID = defaultProjectID
							}
							if objectProject(data) != projectID {
								return fmt.Errorf("import %s has inconsistent project ownership", table)
							}
						}
					}
					if n, ok := v.(json.Number); ok {
						v = string(n)
					}
					values[i] = v
				}
				if _, e := tx.Exec(query, values...); e != nil {
					return fmt.Errorf("import %s failed: %T", table, e)
				}
			}
		}
		if e := validateHistoryImport(tx); e != nil {
			return e
		}
		if e := validatePressureImport(tx); e != nil {
			return e
		}
		if e := validateMonitoringImport(tx); e != nil {
			return e
		}
		if e := validateImportedProjects(tx); e != nil {
			return e
		}
		if e := p.convertExtensionRegistriesQ(tx.Context(), tx); e != nil {
			return e
		}
		return p.validateExtensionImport(tx)
	})
}
func loadJSONFile(path string) (Obj, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	return decode(b)
}
func ensureEmpty(path string) error {
	if info, e := os.Lstat(path); e == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("destination must be a new or empty directory")
		}
		entries, e := os.ReadDir(path)
		if e != nil {
			return e
		}
		if len(entries) > 0 {
			return fmt.Errorf("destination must be a new or empty directory")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return os.MkdirAll(path, 0755)
}
func copyTree(source, target string) error {
	info, e := os.Lstat(source)
	if e != nil {
		return e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("copy refuses symbolic links")
	}
	if info.IsDir() {
		if e = os.MkdirAll(target, 0755); e != nil {
			return e
		}
		entries, e := os.ReadDir(source)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if e = copyTree(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); e != nil {
				return e
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("copy refuses special files")
	}
	if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		return e
	}
	input, e := os.Open(source)
	if e != nil {
		return e
	}
	defer input.Close()
	output, e := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if e != nil {
		return e
	}
	_, e = io.Copy(output, input)
	closeErr := output.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func fileHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	hash, _, e := hashStream(f)
	return hash, e
}
func nested(path, base string) bool {
	relative, e := filepath.Rel(base, path)
	return e == nil && (relative == "." || (!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && relative != ".."))
}
func (p *Platform) backup(destination string) (string, error) {
	destination, e := filepath.Abs(destination)
	if e != nil {
		return "", e
	}
	for _, root := range []string{p.Config.StateDir, p.Config.PlansDir, p.Config.ResultsDir} {
		if nested(destination, root) {
			return "", fmt.Errorf("backup destination must be outside live data roots")
		}
	}
	if e = ensureEmpty(destination); e != nil {
		return "", e
	}
	state, e := p.exportState()
	if e != nil {
		return "", e
	}
	if e = atomicWrite(filepath.Join(destination, "state", "manager.json"), encoded(state)); e != nil {
		return "", e
	}
	for _, v := range arr(state["plans"]) {
		data, _ := stateData(obj(v)["data"])
		plan, e := decode([]byte(data))
		if e != nil {
			return "", e
		}
		rel := "snapshots/" + str(plan["sha256"]) + ".jmx"
		source, e := safePath(p.Config.PlansDir, rel, true)
		if e != nil {
			return "", e
		}
		if e = copyTree(source, filepath.Join(destination, "plans", rel)); e != nil {
			return "", e
		}
	}
	terminals := map[string]bool{}
	for _, v := range arr(state["runs"]) {
		data, _ := stateData(obj(v)["data"])
		run, e := decode([]byte(data))
		if e != nil {
			return "", e
		}
		if !contains(terminal, str(run["status"])) {
			continue
		}
		terminals[str(run["id"])] = true
		rel := str(run["id"]) + "/raw"
		source, e := safePath(p.Config.ResultsDir, rel, false)
		if e != nil {
			return "", e
		}
		if _, e = os.Stat(source); e == nil {
			if e = copyTree(source, filepath.Join(destination, "results", rel)); e != nil {
				return "", e
			}
		} else if !os.IsNotExist(e) {
			return "", e
		}
	}
	for _, v := range arr(state["reports"]) {
		data, _ := stateData(obj(v)["data"])
		r, e := decode([]byte(data))
		if e != nil {
			return "", e
		}
		if str(r["status"]) != "READY" || !terminals[str(r["run_id"])] {
			continue
		}
		rel := str(r["legacy_html_path"])
		if rel == "" {
			rel = str(r["run_id"]) + "/reports/" + str(r["id"])
		}
		source, e := safePath(p.Config.ResultsDir, rel, false)
		if e != nil {
			return "", e
		}
		if _, e = os.Stat(source); e == nil {
			if e = copyTree(source, filepath.Join(destination, "results", rel)); e != nil {
				return "", e
			}
		} else if !os.IsNotExist(e) {
			return "", e
		}
	}
	files := Obj{}
	e = filepath.WalkDir(destination, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(destination, path)
		if e != nil {
			return e
		}
		hash, e := fileHash(path)
		if e != nil {
			return e
		}
		files[filepath.ToSlash(rel)] = hash
		return nil
	})
	if e != nil {
		return "", e
	}
	e = atomicWrite(filepath.Join(destination, "manifest.json"), encoded(Obj{"version": 2, "storage": "mysql", "created_at": now(), "files": files, "note": "Contains no sessions, instance heartbeats, or database credentials. Restore imports into a separate empty configured MySQL database."}))
	return destination, e
}
func restoreArchive(source, destination string) (string, error) {
	source, e := filepath.Abs(source)
	if e != nil {
		return "", e
	}
	destination, e = filepath.Abs(destination)
	if e != nil {
		return "", e
	}
	if nested(destination, source) {
		return "", fmt.Errorf("restore target must be separate from archive")
	}
	manifestPath, e := safePath(source, "manifest.json", true)
	if e != nil {
		return "", e
	}
	manifest, e := loadJSONFile(manifestPath)
	if e != nil {
		return "", e
	}
	if number(manifest["version"]) != 2 {
		return "", fmt.Errorf("unsupported backup manifest")
	}
	files := obj(manifest["files"])
	if files["state/manager.json"] == nil {
		return "", fmt.Errorf("backup database JSON missing")
	}
	keys := []string{}
	for relative, expected := range files {
		if !(strings.HasPrefix(relative, "plans/") || strings.HasPrefix(relative, "results/") || relative == "state/manager.json") {
			return "", fmt.Errorf("unexpected backup path")
		}
		path, e := safePath(source, relative, true)
		if e != nil {
			return "", e
		}
		hash, e := fileHash(path)
		if e != nil {
			return "", e
		}
		if !constant(hash, str(expected)) {
			return "", fmt.Errorf("backup checksum mismatch: %s", relative)
		}
		keys = append(keys, relative)
	}
	sort.Strings(keys)
	state, e := loadJSONFile(filepath.Join(source, "state", "manager.json"))
	if e != nil {
		return "", e
	}
	for _, v := range arr(state["runs"]) {
		row := obj(v)
		raw, e := stateData(row["data"])
		if e != nil {
			return "", e
		}
		run, e := decode([]byte(raw))
		if e != nil {
			return "", e
		}
		if !contains(terminal, str(run["status"])) {
			run["status"] = "INTERRUPTED"
			run["finished_at"] = now()
			run["notes"] = str(run["notes"]) + " [异机恢复：旧在途批次已中断]"
			delete(run, "recovery")
		}
		if receipt := obj(run["raw_archive"]); str(run["execution_mode"]) != "redis" && len(receipt) > 0 && str(receipt["status"]) != "EMPTY" {
			receipt["status"] = "PENDING"
			receipt["attempts"] = 0
			receipt["next_retry_at"] = nil
			receipt["error"] = nil
			receipt["archived_at"] = nil
			for _, f := range arr(receipt["files"]) {
				item := obj(f)
				item["status"] = "PENDING"
				delete(item, "error")
			}
		}
		row["status"] = run["status"]
		row["data"] = string(encoded(run))
	}
	for _, v := range arr(state["reports"]) {
		row := obj(v)
		raw, e := stateData(row["data"])
		if e != nil {
			return "", e
		}
		report, e := decode([]byte(raw))
		if e != nil {
			return "", e
		}
		if str(report["status"]) != "READY" {
			report["status"] = "FAILED"
			report["finished_at"] = now()
			report["error"] = "Legacy report interrupted; new report generation is retired"
		}
		row["status"] = report["status"]
		row["data"] = string(encoded(report))
	}
	if e = ensureEmpty(destination); e != nil {
		return "", e
	}
	for _, relative := range keys {
		input, e := safePath(source, relative, true)
		if e != nil {
			return "", e
		}
		output, e := safePath(destination, relative, false)
		if e != nil {
			return "", e
		}
		if e = copyTree(input, output); e != nil {
			return "", e
		}
	}
	if e = atomicWrite(filepath.Join(destination, "state", "manager.json"), encoded(state)); e != nil {
		return "", e
	}
	os.MkdirAll(filepath.Join(destination, "plans", "snapshots"), 0755)
	os.MkdirAll(filepath.Join(destination, "results"), 0755)
	return destination, nil
}
