package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"pressurecooker/manager/reporting"
	"strconv"
)

// The large series live in independently addressable rows. Headers and every
// status read stay small; raw artifacts and exact aggregation values are kept.
func packReadModel(value any) (string, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return "", e
	}
	if len(raw) > 64<<20 {
		return "", fmt.Errorf("read model exceeds bounded decode size")
	}
	var b bytes.Buffer
	z, e := gzip.NewWriterLevel(&b, gzip.BestSpeed)
	if e != nil {
		return "", e
	}
	if _, e = z.Write(raw); e != nil {
		return "", e
	}
	if e = z.Close(); e != nil {
		return "", e
	}
	return "gzip1:" + base64.RawStdEncoding.EncodeToString(b.Bytes()), nil
}
func unpackReadModel(payload string) (Obj, error) {
	if len(payload) < 6 || payload[:6] != "gzip1:" {
		return nil, fmt.Errorf("unsupported read model format")
	}
	compressed, e := base64.RawStdEncoding.DecodeString(payload[6:])
	if e != nil {
		return nil, e
	}
	z, e := gzip.NewReader(bytes.NewReader(compressed))
	if e != nil {
		return nil, e
	}
	defer z.Close()
	raw, e := io.ReadAll(io.LimitReader(z, (64<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(raw) > 64<<20 {
		return nil, fmt.Errorf("read model exceeds bounded decode size")
	}
	return decode(raw)
}
func (p *Platform) storeDashboard(tx *Tx, id, project string, d reporting.Dashboard) (Obj, error) {
	if _, e := tx.Exec("DELETE FROM pressure_dashboard_scopes WHERE report_id=? AND project_id=?", id, project); e != nil {
		return nil, e
	}
	for position, scope := range d.Scopes {
		payload, e := packReadModel(scope)
		if e != nil {
			return nil, e
		}
		metadata := Obj{"node_id": scope.NodeID, "step_key": scope.StepKey, "label": scope.Label, "sample_kind": scope.SampleKind}
		if _, e = tx.Exec("INSERT INTO pressure_dashboard_scopes(id,project_id,report_id,position,node_hash,step_key,metadata,payload) VALUES(?,?,?,?,?,?,?,?)", pressureEvidenceID(id, "scope", position), project, id, position, digest([]byte(scope.NodeID)), scope.StepKey, string(encoded(metadata)), payload); e != nil {
			return nil, e
		}
	}
	return Obj{"version": d.Version, "options": d.Options, "capabilities": d.Capabilities, "warnings": d.Warnings, "external_scopes": true, "scope_count": len(d.Scopes)}, nil
}
func (p *Platform) loadDashboardScopes(id string, dashboard Obj) error {
	if dashboard["external_scopes"] != true {
		return nil
	}
	rows, e := p.DB.Query("SELECT payload FROM pressure_dashboard_scopes WHERE report_id=? AND project_id=? ORDER BY position", id, p.projectID())
	if e != nil {
		return e
	}
	defer rows.Close()
	scopes := []any{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return e
		}
		scope, e := unpackReadModel(raw)
		if e != nil {
			return e
		}
		scopes = append(scopes, scope)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	dashboard["scopes"] = scopes
	return nil
}
func (p *Platform) normalizedAnalytics(report Obj, r *http.Request) (Obj, error) {
	id := str(report["id"])
	q := r.URL.Query()
	node, step := q.Get("node_id"), q.Get("step_key")
	limit, offset := 500, 0 // preserve legacy response; the console requests bounded pages.
	var e error
	if q.Has("series_limit") {
		limit, e = strconv.Atoi(q.Get("series_limit"))
		if e != nil || limit < 1 || limit > 100 {
			return nil, api(400, "invalid_pagination", "趋势每页条数须为1至100")
		}
	}
	if q.Has("series_offset") {
		offset, e = strconv.Atoi(q.Get("series_offset"))
		if e != nil || offset < 0 || offset > 1000000 {
			return nil, api(400, "invalid_pagination", "趋势偏移无效")
		}
	}
	d := obj(report["dashboard"])
	out := Obj{"detail_version": d["version"], "options": d["options"], "capabilities": d["capabilities"], "nodes": report["nodes"], "warnings": append(append([]any{}, arr(d["warnings"])...), arr(obj(report["quality"])["warnings"])...), "steps": []any{}, "series": []any{}, "scope": nil, "series_limit": limit, "series_offset": offset}
	rows, e := p.DB.Query("SELECT metadata FROM pressure_dashboard_scopes WHERE project_id=? AND report_id=? AND node_hash=? AND step_key<>'' ORDER BY position", p.projectID(), id, digest(nil))
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		m, e := decode([]byte(raw))
		if e != nil {
			rows.Close()
			return nil, e
		}
		key := str(m["step_key"])
		if str(m["node_id"]) == "" && !seen[key] {
			seen[key] = true
			out["steps"] = append(arr(out["steps"]), m)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// A hash is only an index accelerator; compare the complete identity too.
	rows, e = p.DB.Query("SELECT payload FROM pressure_dashboard_scopes WHERE project_id=? AND report_id=? AND node_hash=? AND step_key=? ORDER BY position DESC", p.projectID(), id, digest([]byte(node)), step)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		scope, e := unpackReadModel(raw)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if str(scope["node_id"]) == node {
			out["scope"] = scope
			break
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	where := "project_id=? AND report_id=? AND node_hash=? AND step_key<>''"
	args := []any{p.projectID(), id, digest([]byte(node))}
	if step != "" {
		where += " AND step_key=?"
		args = append(args, step)
	}
	var total int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM pressure_dashboard_scopes WHERE "+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	out["series_total"] = total
	rows, e = p.DB.Query("SELECT payload FROM pressure_dashboard_scopes WHERE "+where+" ORDER BY position LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		scope, e := unpackReadModel(raw)
		if e != nil {
			return nil, e
		}
		if str(scope["node_id"]) == node {
			out["series"] = append(arr(out["series"]), scope)
		}
	}
	return out, rows.Err()
}

func (p *Platform) rebuildEmbeddedDashboards(batch int) (int, error) {
	total := 0
	for {
		rows, e := p.DB.Query("SELECT id FROM pressure_reports WHERE status='READY' AND CASE WHEN JSON_VALID(data) THEN JSON_TYPE(JSON_EXTRACT(data,'$.dashboard.scopes'))='ARRAY' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(data,'$.dashboard.scopes_projected')),'false')<>'true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(data,'$.dashboard.external_scopes')),'false')<>'true' ELSE false END LIMIT ?", batch)
		if e != nil {
			return total, e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return total, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return total, e
		}
		if len(ids) == 0 {
			return total, nil
		}
		for _, id := range ids {
			e = p.transaction(func(tx *Tx) error {
				var raw, owner string
				if e := tx.QueryRow("SELECT data,project_id FROM pressure_reports WHERE id=? FOR UPDATE", id).Scan(&raw, &owner); e != nil {
					return e
				}
				report, e := decode([]byte(raw))
				if e != nil {
					return e
				}
				d := obj(report["dashboard"])
				if d["external_scopes"] == true || d["scopes_projected"] == true {
					return nil
				}
				if _, e = tx.Exec("DELETE FROM pressure_dashboard_scopes WHERE report_id=? AND project_id=?", id, owner); e != nil {
					return e
				}
				for position, v := range arr(d["scopes"]) {
					scope := obj(v)
					payload, e := packReadModel(scope)
					if e != nil {
						return e
					}
					metadata := Obj{}
					for _, key := range []string{"node_id", "step_key", "label", "sample_kind"} {
						metadata[key] = scope[key]
					}
					if _, e = tx.Exec("INSERT INTO pressure_dashboard_scopes(id,project_id,report_id,position,node_hash,step_key,metadata,payload) VALUES(?,?,?,?,?,?,?,?)", pressureEvidenceID(id, "scope", position), owner, id, position, digest([]byte(str(scope["node_id"]))), str(scope["step_key"]), string(encoded(metadata)), payload); e != nil {
						return e
					}
				}
				// Preserve every original embedded field for old exports/clients. New page
				// readers use the small generated summary plus these indexed scope rows.
				_, e = tx.Exec("UPDATE pressure_reports SET data=JSON_SET(data,'$.dashboard.scopes_projected',CAST('true' AS JSON)) WHERE id=?", id)
				return e
			})
			if e != nil {
				return total, e
			}
			total++
		}
	}
}

func validateDashboardImport(tx *Tx) error {
	rows, e := tx.Query("SELECT id,project_id,JSON_UNQUOTE(JSON_EXTRACT(data,'$.dashboard.scope_count')),JSON_LENGTH(JSON_EXTRACT(data,'$.dashboard.scopes')),JSON_UNQUOTE(JSON_EXTRACT(data,'$.dashboard.external_scopes')) FROM pressure_reports WHERE COALESCE(JSON_UNQUOTE(JSON_EXTRACT(data,'$.dashboard.external_scopes')),'false')='true' OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(data,'$.dashboard.scopes_projected')),'false')='true'")
	if e != nil {
		return e
	}
	type expectedScope struct {
		id, project string
		count       int64
	}
	reports := []expectedScope{}
	for rows.Next() {
		var id, project string
		var declared, external sql.NullString
		var embedded sql.NullInt64
		if e = rows.Scan(&id, &project, &declared, &embedded, &external); e != nil {
			rows.Close()
			return e
		}
		count := embedded.Int64
		if external.String == "true" {
			if !declared.Valid {
				rows.Close()
				return fmt.Errorf("normalized report is missing scope count")
			}
			count, e = strconv.ParseInt(declared.String, 10, 64)
			if e != nil || count < 0 {
				rows.Close()
				return fmt.Errorf("invalid normalized scope count")
			}
		}
		reports = append(reports, expectedScope{id, project, count})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, report := range reports {
		var count, distinct int64
		var min, max sql.NullInt64
		if e = tx.QueryRow("SELECT COUNT(*),COUNT(DISTINCT position),MIN(position),MAX(position) FROM pressure_dashboard_scopes WHERE report_id=? AND project_id=?", report.id, report.project).Scan(&count, &distinct, &min, &max); e != nil {
			return e
		}
		if count != report.count || distinct != count || count > 0 && (!min.Valid || min.Int64 != 0 || !max.Valid || max.Int64 != count-1) {
			return fmt.Errorf("normalized report scopes are missing or inconsistent")
		}
	}
	return nil
}
