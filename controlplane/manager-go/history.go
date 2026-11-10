package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pressurecooker/execution/protocol"
)

func regular(path string) bool { info, e := os.Lstat(path); return e == nil && info.Mode().IsRegular() }
func (p *Platform) importHistory(root string) (Obj, error) {
	if e := p.transaction(func(tx *Tx) error { return p.requireActiveQ(tx) }); e != nil {
		return nil, e
	}
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil, e
	}
	imported := []any{}
	for _, entry := range entries {
		added, err := p.importHistoryEntry(root, entry)
		if err != nil {
			return nil, err
		}
		if added {
			imported = append(imported, entry.Name())
		}
	}
	if e = p.audit("history.import", nil); e != nil {
		return nil, e
	}
	return Obj{"project_id": p.projectID(), "items": imported, "total": len(imported)}, nil
}

// The per-run archive/cleanup lock spans every copied artifact. Deletion may
// commit while importing, but its cleanup cannot sweep ahead of a late copy.
// Recheck under the lock and fence the final report insert in the database.
func (p *Platform) importHistoryEntry(root string, entry os.DirEntry) (bool, error) {
	var e error
	if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || validID(entry.Name()) != nil {
		return false, nil
	}
	id := entry.Name()
	directory := filepath.Join(root, id)
	legacy, raw, meta := filepath.Join(directory, "results.jtl"), filepath.Join(directory, "raw", "results.jtl"), filepath.Join(directory, "run.meta")
	if !regular(legacy) && !regular(raw) && !regular(meta) {
		return false, nil
	}
	release, e := p.lockHistoryArtifacts(p.requestContext(), id)
	if e != nil {
		return false, e
	}
	defer release()
	var exists int
	if e = p.DB.QueryRow("SELECT (SELECT COUNT(*) FROM runs WHERE id=?)+(SELECT COUNT(*) FROM deleted_runs WHERE id=?)", id, id).Scan(&exists); e != nil {
		return false, e
	}
	if exists != 0 {
		return false, nil
	}
	metadata := Obj{}
	if regular(meta) {
		b, e := os.ReadFile(meta)
		if e != nil {
			return false, e
		}
		if e = json.Unmarshal(b, &metadata); e != nil || metadata == nil {
			metadata = Obj{}
			for _, line := range strings.Split(string(b), "\n") {
				pieces := strings.SplitN(line, "=", 2)
				if len(pieces) == 2 {
					metadata[pieces[0]] = pieces[1]
				}
			}
		}
	}
	source := legacy
	if regular(raw) {
		source = raw
	}
	if regular(source) {
		target, e := safePath(p.Config.ResultsDir, id+"/raw/results.jtl", false)
		if e != nil {
			return false, e
		}
		if source != target {
			if e = copyTree(source, target); e != nil {
				return false, e
			}
		}
	}
	for _, name := range []string{"jmeter.log", "console.log"} {
		logSource := filepath.Join(directory, name)
		if regular(logSource) {
			target, e := safePath(p.Config.ResultsDir, id+"/raw/"+name, false)
			if e != nil {
				return false, e
			}
			if logSource != target {
				if e = copyTree(logSource, target); e != nil {
					return false, e
				}
			}
		}
	}
	complete := false
	if info, e := os.Stat(source); e == nil {
		complete = info.Mode().IsRegular() && info.Size() > 0
	}
	defaultStatus := "FAILED"
	exit := "0"
	if v, ok := metadata["jmeter_exit_code"]; ok {
		exit = fmt.Sprint(v)
	}
	if complete && exit == "0" {
		defaultStatus = "COMPLETED"
	}
	status := strings.ToUpper(defaultString(metadata, "status", defaultStatus))
	if !contains(terminal, status) {
		status = "INTERRUPTED"
	}
	workers := []any{}
	seenWorkers := map[string]bool{}
	for _, engine := range strings.Split(str(metadata["engines"]), ",") {
		parts := strings.Split(strings.TrimSpace(engine), ":")
		if len(parts) > 2 || !protocol.ValidID(parts[0]) || seenWorkers[parts[0]] {
			continue
		}
		if len(parts) == 2 {
			port, err := strconv.Atoi(parts[1])
			if err != nil || port < 1 || port > 65535 {
				continue
			}
		}
		workers = append(workers, parts[0])
		seenWorkers[parts[0]] = true
	}
	created := defaultString(metadata, "created_at", defaultString(metadata, "started_utc", now()))
	started := metadata["started_at"]
	if started == nil {
		started = metadata["started_utc"]
	}
	finished := defaultString(metadata, "finished_at", defaultString(metadata, "finished_utc", now()))
	run := Obj{"project_id": p.projectID(), "id": id, "name": defaultString(metadata, "name", id), "status": status, "plan_id": nil, "plan_name": defaultString(metadata, "plan", "历史脚本"), "plan_sha256": metadata["plan_sha256"], "properties": Obj{}, "workers": workers, "controller_id": metadata["controller"], "created_at": created, "started_at": started, "finished_at": finished, "notes": "历史导入；元数据可能不完整。", "latest_report": nil, "summary": nil, "metadata_incomplete": true}
	if e = p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		return p.saveRun(tx, run)
	}); e != nil {
		return false, e
	}
	for _, name := range []string{"html", "report"} {
		html := filepath.Join(directory, name)
		if !regular(filepath.Join(html, "index.html")) {
			continue
		}
		if info, e := os.Lstat(html); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		target, e := safePath(p.Config.ResultsDir, id+"/legacy-html", false)
		if e != nil {
			return false, e
		}
		if html != target {
			if e = copyTree(html, target); e != nil {
				return false, e
			}
		}
		reportID := "legacy-" + digest([]byte(id))[:32]
		report := Obj{"project_id": p.projectID(), "id": reportID, "run_id": id, "status": "READY", "created_at": created, "finished_at": finished, "summary": nil, "html_url": "/api/reports/" + reportID + "/html/", "analysis_url": nil, "error": nil, "legacy_html_path": id + "/legacy-html"}
		if e = p.transaction(func(tx *Tx) error {
			var deleted string
			err := tx.QueryRow("SELECT id FROM deleted_runs WHERE id=? FOR UPDATE", id).Scan(&deleted)
			if err == nil {
				return api(404, "run_deleted", "Run has been deleted")
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			current, err := p.loadRunQ(tx.Context(), tx, id, true)
			if err != nil {
				return err
			}
			if err = p.checkProject(current); err != nil {
				return err
			}
			_, err = tx.Exec("INSERT IGNORE INTO reports(id,run_id,status,created_at,data,project_id) VALUES(?,?,?,?,?,?)", reportID, id, "READY", created, string(encoded(report)), p.projectID())
			return err
		}); e != nil {
			return false, e
		}
		break
	}
	if _, e = p.ensureRawArchive(id); e != nil {
		return false, e
	}
	return true, nil
}

func defaultString(m Obj, key, value string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return value
}
