package main

import (
	"database/sql"
	"errors"
	"fmt"
)

func init() {
	portableTables = append(portableTables, monitoringTables...)
	tableColumns["monitoring_revisions"] = []string{"id", "project_id", "report_id", "revision", "created_at", "key_hash", "request_hash", "source_hash", "data"}
	tableColumns["monitoring_evidence"] = []string{"id", "project_id", "report_id", "created_at", "sha256", "data"}
	tableColumns["monitoring_memberships"] = []string{"id", "project_id", "report_id", "revision", "created_at", "position", "entry_id", "component", "instance", "evidence_id"}
}

var monitoringTables = []string{"monitoring_revisions", "monitoring_evidence", "monitoring_memberships"}

type monitoringQueryer interface {
	queryer
	Query(string, ...any) (*sql.Rows, error)
}

func monitoringNeedsConversion(q queryer, project, report string) error {
	var count int
	err := q.QueryRow("SELECT COUNT(*) FROM pressure_report_metrics m WHERE m.project_id=? AND m.report_id=? AND m.kind='monitoring_snapshot' AND NOT EXISTS(SELECT 1 FROM monitoring_revisions r WHERE r.project_id=m.project_id AND r.report_id=m.report_id AND r.revision=m.position)", project, report).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return api(409, "monitoring_conversion_required", "历史监控须先运行 convert-monitoring 核对转换")
	}
	return nil
}
func monitoringMetadata(q queryer, project, report string, revision int64) (Obj, error) {
	if err := monitoringNeedsConversion(q, project, report); err != nil {
		return nil, err
	}
	query := "SELECT data FROM monitoring_revisions WHERE project_id=? AND report_id=?"
	args := []any{project, report}
	if revision > 0 {
		query += " AND revision=?"
		args = append(args, revision)
	}
	var raw string
	err := q.QueryRow(query+" ORDER BY revision DESC LIMIT 1", args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		if revision > 0 {
			return nil, api(404, "monitoring_revision_missing", "监控补充版本不存在")
		}
		return emptyPressureMonitoring(project, report), nil
	}
	if err != nil {
		return nil, err
	}
	return decode([]byte(raw))
}
func monitoringReadQ(q monitoringQueryer, project, report string, revision int64) (Obj, error) {
	snapshot, err := monitoringMetadata(q, project, report, revision)
	if err != nil {
		return nil, err
	}
	snapshot["entries"] = []any{}
	if number(snapshot["revision"]) == 0 {
		return snapshot, nil
	}
	rows, err := q.Query("SELECT e.data FROM monitoring_memberships m JOIN monitoring_evidence e ON e.id=m.evidence_id AND e.project_id=m.project_id AND e.report_id=m.report_id WHERE m.project_id=? AND m.report_id=? AND m.revision=? ORDER BY m.position", project, report, snapshot["revision"])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		entry, e := decode([]byte(raw))
		if e != nil {
			return nil, e
		}
		snapshot["entries"] = append(arr(snapshot["entries"]), entry)
	}
	return snapshot, rows.Err()
}
func storeMonitoringRevision(tx *Tx, project, report, keyHash, requestHash, sourceHash string, snapshot Obj) error {
	revision := int64(number(snapshot["revision"]))
	metadata := clone(snapshot)
	delete(metadata, "entries")
	if sourceHash == "" {
		metadata["write_result"] = pressureMonitoringWriteResult(snapshot)
	}
	for position, value := range arr(snapshot["entries"]) {
		entry := obj(value)
		raw := encoded(entry)
		sha := digest(raw)
		evidenceID := "evidence-" + digest([]byte(project+"/"+report+"/"+sha))
		if _, err := tx.Exec("INSERT IGNORE INTO monitoring_evidence(id,project_id,report_id,created_at,sha256,data) VALUES(?,?,?,?,?,?)", evidenceID, project, report, snapshot["updated_at"], sha, string(raw)); err != nil {
			return err
		}
		memberID := "member-" + digest([]byte(fmt.Sprintf("%s/%s/%d/%s", project, report, revision, str(entry["entry_id"]))))
		if _, err := tx.Exec("INSERT INTO monitoring_memberships(id,project_id,report_id,revision,created_at,position,entry_id,component,instance,evidence_id) VALUES(?,?,?,?,?,?,?,?,?,?)", memberID, project, report, revision, snapshot["updated_at"], position, entry["entry_id"], entry["component"], entry["instance"], evidenceID); err != nil {
			return err
		}
	}
	id := "monitoring-" + digest([]byte(fmt.Sprintf("%s/%s/%d", project, report, revision)))
	_, err := tx.Exec("INSERT INTO monitoring_revisions(id,project_id,report_id,revision,created_at,key_hash,request_hash,source_hash,data) VALUES(?,?,?,?,?,?,?,?,?)", id, project, report, revision, snapshot["updated_at"], keyHash, requestHash, sourceHash, string(encoded(metadata)))
	return err
}
func (p *Platform) monitoringSQLPage(report string, revision int64, component, search string, limit, offset int) (Obj, error) {
	if _, err := p.pressureSelect(report, "JSON_OBJECT('id',id)"); err != nil {
		return nil, err
	}
	snapshot, err := monitoringMetadata(p.DB, p.projectID(), report, revision)
	if err != nil {
		return nil, err
	}
	snapshot = publicPressureMonitoring(snapshot)
	snapshot["entries"] = []any{}
	counts := Obj{"es": 0, "mysql": 0, "redis": 0, "kafka": 0, "jvm": 0, "host": 0, "k8s": 0}
	rows, err := p.DB.QueryContext(p.requestContext(), "SELECT component,COUNT(*) FROM monitoring_memberships WHERE project_id=? AND report_id=? AND revision=? GROUP BY component", p.projectID(), report, snapshot["revision"])
	if err != nil {
		return nil, err
	}
	totalInstances := 0
	for rows.Next() {
		var kind string
		var count int
		if err = rows.Scan(&kind, &count); err != nil {
			rows.Close()
			return nil, err
		}
		counts[kind] = count
		totalInstances += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	where := "m.project_id=? AND m.report_id=? AND m.revision=?"
	args := []any{p.projectID(), report, snapshot["revision"]}
	if component != "" {
		where += " AND m.component=?"
		args = append(args, component)
	}
	if search != "" {
		where += " AND LOCATE(LOWER(?),LOWER(m.instance))>0"
		args = append(args, search)
	}
	var total int
	if err = p.DB.QueryRowContext(p.requestContext(), "SELECT COUNT(*) FROM monitoring_memberships m WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, limit, offset)
	rows, err = p.DB.QueryContext(p.requestContext(), "SELECT e.data FROM monitoring_memberships m JOIN monitoring_evidence e ON e.id=m.evidence_id AND e.project_id=m.project_id AND e.report_id=m.report_id WHERE "+where+" ORDER BY m.position LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		entry, e := decode([]byte(raw))
		if e != nil {
			return nil, e
		}
		snapshot["entries"] = append(arr(snapshot["entries"]), entry)
	}
	snapshot["total"], snapshot["limit"], snapshot["offset"], snapshot["instance_count"], snapshot["component_counts"] = total, limit, offset, totalInstances, counts
	return snapshot, rows.Err()
}

// Explicit, resumable conversion keeps original snapshot bytes and records their
// fingerprint. Normal reads never silently rewrite historical evidence.
func (p *Platform) convertMonitoring() (Obj, error) {
	cursor := ""
	converted := 0
	for {
		rows, err := p.DB.QueryContext(p.requestContext(), "SELECT id FROM pressure_report_metrics WHERE kind='monitoring_snapshot' AND id>? ORDER BY id LIMIT 32", cursor)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			err = p.transaction(func(tx *Tx) error {
				var project, report, raw, key, request string
				var revision int
				if err := tx.QueryRow("SELECT project_id,report_id,position,data,metric_key,label FROM pressure_report_metrics WHERE id=? FOR UPDATE", id).Scan(&project, &report, &revision, &raw, &key, &request); err != nil {
					return err
				}
				var lockID string
				if err := tx.QueryRow("SELECT id FROM pressure_reports WHERE id=? AND project_id=? FOR UPDATE", report, project).Scan(&lockID); err != nil {
					return err
				}
				var source string
				err := tx.QueryRow("SELECT source_hash FROM monitoring_revisions WHERE project_id=? AND report_id=? AND revision=?", project, report, revision).Scan(&source)
				sha := digest([]byte(raw))
				if err == nil {
					if source != sha {
						return api(409, "monitoring_conversion_conflict", "历史修订指纹冲突")
					}
					return nil
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				snapshot, err := decode([]byte(raw))
				if err != nil {
					return err
				}
				if str(snapshot["project_id"]) != project || str(snapshot["report_id"]) != report || int(number(snapshot["revision"])) != revision {
					return api(409, "monitoring_conversion_conflict", "历史修订归属冲突")
				}
				if err = storeMonitoringRevision(tx, project, report, key, request, sha, snapshot); err != nil {
					return err
				}
				restored, err := monitoringReadConvertedQ(tx, project, report, int64(revision))
				if err != nil {
					return err
				}
				if digest(encoded(restored)) != digest(encoded(snapshot)) {
					return api(409, "monitoring_conversion_conflict", "历史修订内容校验失败")
				}
				converted++
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		cursor = ids[len(ids)-1]
	}
	return Obj{"converted": converted}, nil
}

// Conversion validates the current row before all other revisions are converted.
func monitoringReadConvertedQ(q monitoringQueryer, project, report string, revision int64) (Obj, error) {
	var raw string
	if err := q.QueryRow("SELECT data FROM monitoring_revisions WHERE project_id=? AND report_id=? AND revision=?", project, report, revision).Scan(&raw); err != nil {
		return nil, err
	}
	snapshot, err := decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	snapshot["entries"] = []any{}
	rows, err := q.Query("SELECT e.data FROM monitoring_memberships m JOIN monitoring_evidence e ON e.id=m.evidence_id WHERE m.project_id=? AND m.report_id=? AND m.revision=? ORDER BY m.position", project, report, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			return nil, err
		}
		entry, e := decode([]byte(text))
		if e != nil {
			return nil, e
		}
		snapshot["entries"] = append(arr(snapshot["entries"]), entry)
	}
	return snapshot, rows.Err()
}
