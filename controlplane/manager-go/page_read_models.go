package main

import (
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"pressurecooker/manager/internal/workspace"
)

var derivedTables = []string{"workflow_csv_nodes", "plan_graph_cache"}

func pageWindow(q url.Values) (int, int, error) {
	limit, e := parseInt(q.Get("limit"), 200)
	if e != nil || limit < 1 || limit > 1000 {
		return 0, 0, api(400, "invalid_pagination", "每页条数须为1至1000")
	}
	offset, e := parseInt(q.Get("offset"), 0)
	if e != nil || offset < 0 || offset > 1000000 {
		return 0, 0, api(400, "invalid_pagination", "分页偏移无效")
	}
	return limit, offset, nil
}
func (p *Platform) workspaceSummaryList(table string, q url.Values) (Obj, error) {
	limit, offset, e := pageWindow(q)
	if e != nil {
		return nil, e
	}
	repo := workspace.New(p.projectID())
	heads, total, e := repo.Summaries(p.requestContext(), p.DB, workspace.Table(table), limit, offset)
	if e != nil {
		return nil, e
	}
	legacy, e := repo.MissingSummaryRecords(p.requestContext(), p.DB, workspace.Table(table), heads)
	if e != nil {
		return nil, e
	}
	items := []any{}
	for _, h := range heads {
		var item Obj
		if h.HasPayload {
			item, e = p.openWorkspaceSummary(table, h.ID, h.Revision, string(h.Payload))
		} else {
			record, present := legacy[h.ID]
			if !present {
				return nil, api(404, "not_found", "内容不存在或已删除")
			}
			item, e = p.openWorkspace(table, record.ID, record.Revision, string(record.Payload))
			if e == nil {
				item = workspaceSummary(table, item)
			}
		}
		if e != nil {
			return nil, e
		}
		item["project_id"] = p.projectID()
		items = append(items, item)
	}
	return Obj{"items": items, "total": total, "limit": limit, "offset": offset, "project_id": p.projectID()}, nil
}
func (p *Platform) workspaceVersionSummaries(table, id string, q url.Values) (Obj, error) {
	repo := workspace.New(p.projectID())
	owner, e := repo.Owner(p.requestContext(), p.DB, workspace.Table(table), id)
	if e != nil || owner != p.projectID() {
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		return nil, api(404, "not_found", "内容不存在")
	}
	limit, offset, e := pageWindow(q)
	if e != nil {
		return nil, e
	}
	heads, total, e := repo.VersionSummaries(p.requestContext(), p.DB, workspace.Table(table), id, limit, offset)
	if e != nil {
		return nil, e
	}
	legacy, e := repo.MissingSummaryVersions(p.requestContext(), p.DB, workspace.Table(table), id, heads)
	if e != nil {
		return nil, e
	}
	items := []any{}
	for _, h := range heads {
		var item Obj
		if h.HasPayload {
			item, e = p.openWorkspaceSummary(table, id, h.Revision, string(h.Payload))
		} else {
			version, present := legacy[h.Revision]
			if !present {
				return nil, sql.ErrNoRows
			}
			item, e = p.openWorkspace(table, id, version.Revision, string(version.Payload))
			if e == nil {
				item = workspaceSummary(table, item)
			}
		}
		if e != nil {
			return nil, e
		}
		item["project_id"] = owner
		items = append(items, item)
	}
	return Obj{"items": items, "total": total, "limit": limit, "offset": offset, "project_id": owner}, nil
}
func (p *Platform) writeWorkspaceProjection(tx *Tx, table string, data Obj) error {
	if table == "environments" {
		return nil
	}
	id := str(data["id"])
	rev, _ := integer(data["revision"])
	summary, e := p.sealWorkspaceSummary(table, id, rev, data)
	if e != nil {
		return e
	}
	// Backfill and import may traverse multiple projects on an unscoped Platform.
	current, e := workspace.New(objectProject(data)).WriteSummary(p.requestContext(), tx, workspace.Table(table), id, rev, []byte(summary))
	if e != nil || !current {
		return e
	} // a newer revision won; never replace its derived rows.
	if table != "workflows" {
		return nil
	}
	if _, e = tx.Exec("DELETE FROM workflow_csv_nodes WHERE workflow_id=?", id); e != nil {
		return e
	}
	values := []string{}
	args := []any{}
	flush := func() error {
		if len(values) == 0 {
			return nil
		}
		_, err := tx.Exec("INSERT INTO workflow_csv_nodes(workflow_id,node_id,project_id,revision,position,payload) VALUES"+strings.Join(values, ","), args...)
		values, args = nil, nil
		return err
	}
	batchBytes := 0
	for position, v := range arr(obj(data["graph"])["nodes"]) {
		node := obj(v)
		props := obj(node["properties"])
		if props["component"] != "CSVDataSet" || props["enabled"] == false {
			continue
		}
		requirement := datasetRequirement(node)
		requirement["columns_declared"] = len(stringList(props["dataset_columns"])) > 0
		payload, e := p.sealWorkspace("csv-node/"+id, str(node["id"]), rev, requirement)
		if e != nil {
			return e
		}
		if len(values) >= 100 || batchBytes+len(payload) > 1<<20 {
			if e = flush(); e != nil {
				return e
			}
			batchBytes = 0
		}
		values = append(values, "(?,?,?,?,?,?)")
		args = append(args, id, node["id"], objectProject(data), rev, position, payload)
		batchBytes += len(payload)
	}
	if e = flush(); e != nil {
		return e
	}
	return p.writeWorkflowEditorRead(tx, data)
}
func (p *Platform) savePlan(tx *Tx, plan Obj) error {
	summary, e := p.sealWorkspaceSummary("plans", str(plan["id"]), 1, plan)
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO plans(id,data,project_id,summary_payload) VALUES(?,?,?,?)", plan["id"], string(encoded(plan)), objectProject(plan), summary)
	return e
}
func (p *Platform) listPlanSummaries(q url.Values) (Obj, error) {
	return p.planLibraryList(q, true)
}

// Bounded, restartable maintenance. Original payloads stay byte-for-byte intact.
// Row locks make the projection match the current revision even during edits.
func (p *Platform) rebuildPageReadModels(batch int) (Obj, error) {
	if batch < 1 || batch > 1000 {
		batch = 16
	}
	counts := Obj{}
	for _, table := range []string{"workflows", "api_collections", "workflow_versions", "api_collection_versions", "plans"} {
		completed := 0
		for {
			var ids []string
			rows, e := p.DB.Query("SELECT id FROM "+table+" WHERE summary_payload IS NULL LIMIT ?", batch)
			if e != nil {
				return nil, e
			}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return nil, e
				}
				ids = append(ids, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				e = p.transaction(func(tx *Tx) error {
					if table == "plans" {
						var raw string
						if e := tx.QueryRow("SELECT data FROM plans WHERE id=? AND summary_payload IS NULL FOR UPDATE", id).Scan(&raw); errors.Is(e, sql.ErrNoRows) {
							return nil
						} else if e != nil {
							return e
						}
						data, e := decode([]byte(raw))
						if e != nil {
							return e
						}
						summary, e := p.sealWorkspaceSummary("plans", id, 1, data)
						if e != nil {
							return e
						}
						_, e = tx.Exec("UPDATE plans SET summary_payload=? WHERE id=? AND summary_payload IS NULL", summary, id)
						return e
					}
					var raw, rootID, projectID string
					var rev int64
					root := table
					columns := "payload,id,revision,project_id"
					if table == "workflow_versions" {
						root = "workflows"
						columns = "payload,object_id,revision,''"
					}
					if table == "api_collection_versions" {
						root = "api_collections"
						columns = "payload,object_id,revision,''"
					}
					if e := tx.QueryRow("SELECT "+columns+" FROM "+table+" WHERE id=? AND summary_payload IS NULL FOR UPDATE", id).Scan(&raw, &rootID, &rev, &projectID); errors.Is(e, sql.ErrNoRows) {
						return nil
					} else if e != nil {
						return e
					}
					data, e := p.openWorkspace(root, rootID, rev, raw)
					if e != nil {
						return e
					}
					if root == table {
						// Legacy opaque payloads may omit project_id; storage owns
						// the scope. Only the derived in-memory view is enriched.
						data["project_id"] = projectID
						return p.writeWorkspaceProjection(tx, root, data)
					}
					summary, e := p.sealWorkspaceSummary(root, rootID, rev, data)
					if e != nil {
						return e
					}
					_, e = tx.Exec("UPDATE "+table+" SET summary_payload=? WHERE id=? AND summary_payload IS NULL", summary, id)
					return e
				})
				if e != nil {
					return nil, e
				}
				completed++
			}
		}
		counts[table] = completed
	}
	csvCount, e := p.rebuildWorkflowCSVProjections(batch)
	if e != nil {
		return nil, e
	}
	counts["workflow_csv_projections"] = csvCount
	dashboardCount, e := p.rebuildEmbeddedDashboards(batch)
	if e != nil {
		return nil, e
	}
	counts["dashboard_reports"] = dashboardCount
	return counts, nil
}

func (p *Platform) csvNodeRequirements(id string) ([]any, error) {
	snapshot, e := p.readWorkflowCSVSnapshot(p.DB, id)
	if errors.Is(e, sql.ErrNoRows) || e == nil && snapshot.deleted != 0 {
		return nil, api(404, "not_found", "流程不存在")
	}
	if e != nil {
		return nil, e
	}
	if !snapshot.summary.Valid {
		workflow, e := p.workspaceGet("workflows", id)
		if e != nil {
			return nil, e
		}
		return csvRequirements(obj(workflow["graph"])), nil
	}
	items := []any{}
	for _, node := range snapshot.nodes {
		data, e := p.openWorkspace("csv-node/"+id, node.id, snapshot.revision, node.payload)
		if e != nil {
			return nil, e
		}
		items = append(items, data)
	}
	return items, nil
}

// Explicit maintenance scans only compact CSV projections. Read source payloads
// for missing provenance or corrupt projection bytes after closing each cursor.
func (p *Platform) rebuildWorkflowCSVProjections(batch int) (int, error) {
	cursor := ""
	completed := 0
	for {
		rows, err := p.DB.Query("SELECT id,revision FROM workflows WHERE id>? AND deleted=0 AND summary_payload IS NOT NULL ORDER BY id LIMIT ?", cursor, batch)
		if err != nil {
			return completed, err
		}
		type head struct {
			id       string
			revision int64
		}
		heads := []head{}
		for rows.Next() {
			var h head
			if err = rows.Scan(&h.id, &h.revision); err != nil {
				rows.Close()
				return completed, err
			}
			heads = append(heads, h)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return completed, err
		}
		if len(heads) == 0 {
			return completed, nil
		}
		for _, h := range heads {
			rows, err = p.DB.Query("SELECT node_id,payload FROM workflow_csv_nodes WHERE workflow_id=? AND revision=?", h.id, h.revision)
			if err != nil {
				return completed, err
			}
			needs := false
			for rows.Next() {
				var id, raw string
				if err = rows.Scan(&id, &raw); err != nil {
					rows.Close()
					return completed, err
				}
				node, e := p.openWorkspace("csv-node/"+h.id, id, h.revision, raw)
				_, versioned := node["columns_declared"].(bool)
				if e != nil || !versioned {
					needs = true
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return completed, err
			}
			if !needs {
				continue
			}
			err = p.transaction(func(tx *Tx) error {
				var raw, project string
				var revision int64
				if e := tx.QueryRow("SELECT payload,project_id,revision FROM workflows WHERE id=? FOR UPDATE", h.id).Scan(&raw, &project, &revision); e != nil {
					return e
				}
				data, e := p.openWorkspace("workflows", h.id, revision, raw)
				if e != nil {
					return e
				}
				data["project_id"] = project
				return p.writeWorkspaceProjection(tx, "workflows", data)
			})
			if err != nil {
				return completed, err
			}
			completed++
		}
		cursor = heads[len(heads)-1].id
	}
}
