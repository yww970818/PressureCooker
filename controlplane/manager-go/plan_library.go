package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// The preview is derived only from this immutable snapshot. It never opens a
// mutable workflow, persists an imported draft, or records a publishing event.
func (p *Platform) planGraph(id string) (Obj, error) {
	plan, e := p.get("plans", id)
	if e != nil {
		return nil, e
	}
	path, e := safePath(p.Config.PlansDir, "snapshots/"+str(plan["sha256"])+".jmx", true)
	if e != nil {
		return nil, e
	}
	raw, e := readLimitedFile(path, workflowByteLimit())
	if e != nil {
		return nil, api(422, "graph_preview_unavailable", "此脚本暂不支持流程预览，可切换至JMX源码")
	}
	hash := digest(raw)
	if hash != str(plan["sha256"]) {
		return nil, api(422, "graph_preview_unavailable", "脚本快照校验失败，可查看JMX源码并检查文件完整性")
	}
	cacheEnabled := p.Config.SessionKey != ""
	var cached string
	if cacheEnabled {
		if e = p.DB.QueryRow("SELECT payload FROM plan_graph_cache WHERE id=? AND project_id=? AND sha256=? AND format_version=1", id, p.projectID(), hash).Scan(&cached); e == nil {
			if header, err := p.openWorkspace("plan-cache/"+hash, id, 1, cached); err == nil {
				if value, err := unpackReadModel(str(header["packed"])); err == nil {
					return value, nil
				}
			}
		}
	}
	graph, e := importWorkflowGraph(string(raw))
	if e != nil {
		return nil, api(422, "graph_preview_unavailable", "此脚本暂不支持流程预览，可切换至JMX源码")
	}
	stablePlanGraphIDs(graph, hash)
	value := Obj{"project_id": objectProject(plan), "plan_id": id, "sha256": hash, "graph": graph, "read_only": true}
	if cacheEnabled {
		if packed, err := packReadModel(value); err == nil {
			if sealed, err := p.sealWorkspace("plan-cache/"+hash, id, 1, Obj{"packed": packed}); err == nil {
				_, _ = p.DB.Exec("INSERT INTO plan_graph_cache(id,project_id,sha256,format_version,payload) VALUES(?,?,?,1,?) ON DUPLICATE KEY UPDATE sha256=VALUES(sha256),format_version=1,payload=VALUES(payload)", id, p.projectID(), hash, sealed)
			}
		}
	}
	return value, nil
}
func stablePlanGraphIDs(graph Obj, hash string) {
	nodeIDs := map[string]string{}
	seed := hash + "\x00"
	for i, value := range arr(graph["nodes"]) {
		node := obj(value)
		id := "preview-node-" + digest([]byte(seed + fmt.Sprint(i)))[:32]
		nodeIDs[str(node["id"])] = id
		node["id"] = id
	}
	for i, value := range arr(graph["edges"]) {
		edge := obj(value)
		edge["id"] = "preview-edge-" + digest([]byte(seed + fmt.Sprint(i)))[:32]
		edge["sourceNodeId"] = nodeIDs[str(edge["sourceNodeId"])]
		edge["targetNodeId"] = nodeIDs[str(edge["targetNodeId"])]
	}
}

func insertPlanWorkflowLink(tx *Tx, planID, workflowID string, revision int64, relation string) error {
	_, e := tx.Exec("INSERT INTO plan_workflow_links(id,plan_id,workflow_id,workflow_revision,relation,created_at) VALUES(?,?,?,?,?,?)", newID("link-"), planID, workflowID, revision, relation, now())
	return e
}
func (p *Platform) planWorkflows(planID string) (Obj, error) {
	if _, e := p.get("plans", planID); e != nil {
		return nil, e
	}
	return p.workflowLinks("plan_id", planID, false)
}
func (p *Platform) workflowPlans(workflowID string) (Obj, error) {
	if e := validID(workflowID); e != nil {
		return nil, e
	}
	var revision int64
	var projectID string
	e := p.DB.QueryRow("SELECT revision,project_id FROM workflows WHERE id=?", workflowID).Scan(&revision, &projectID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "not_found", "流程不存在")
	}
	if e != nil {
		return nil, e
	}
	if p.ProjectID != "" && projectID != p.ProjectID {
		return nil, api(404, "not_found", "流程不存在")
	}
	return p.workflowLinks("workflow_id", workflowID, true)
}
func (p *Platform) workflowLinks(field, id string, includePlan bool) (Obj, error) {
	columns := "l.plan_id,l.workflow_id,l.workflow_revision,l.relation,l.created_at,w.revision,w.deleted"
	join := " LEFT JOIN plans p ON p.id=l.plan_id"
	if includePlan {
		columns += ",p.data"
		join = " LEFT JOIN plans p ON p.id=l.plan_id"
	}
	order := "l.created_at DESC,l.id DESC"
	if includePlan {
		order = "l.workflow_revision DESC," + order
	}
	rows, e := p.DB.Query("SELECT "+columns+" FROM plan_workflow_links l LEFT JOIN workflows w ON w.id=l.workflow_id"+join+" WHERE l."+field+"=? AND p.project_id=? AND w.project_id=? ORDER BY "+order, id, p.projectID(), p.projectID())
	if e != nil {
		return nil, e
	}
	items := []any{}
	for rows.Next() {
		var planID, workflowID, relation, created string
		var linkedRevision int64
		var currentRevision, deleted sql.NullInt64
		var planRaw sql.NullString
		values := []any{&planID, &workflowID, &linkedRevision, &relation, &created, &currentRevision, &deleted}
		if includePlan {
			values = append(values, &planRaw)
		}
		if e = rows.Scan(values...); e != nil {
			rows.Close()
			return nil, e
		}
		var current any
		if currentRevision.Valid {
			current = currentRevision.Int64
		}
		item := Obj{"project_id": p.projectID(), "plan_id": planID, "workflow_id": workflowID, "workflow_revision": linkedRevision, "relation": relation, "created_at": created, "workflow_current_revision": current, "workflow_deleted": !currentRevision.Valid || !deleted.Valid || deleted.Int64 != 0}
		if includePlan {
			item["plan"] = nil
			if planRaw.Valid {
				metadata, e := decode([]byte(planRaw.String))
				if e != nil {
					rows.Close()
					return nil, e
				}
				metadata["project_id"] = p.projectID()
				item["plan"] = metadata
			}
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if e = p.addLinkWorkflowNames(items); e != nil {
		return nil, e
	}
	return Obj{"project_id": p.projectID(), "items": items, "total": len(items)}, nil
}

// Names live inside encrypted workflow payloads. Read each distinct workflow
// only once rather than repeating its potentially large graph for every link.
func (p *Platform) addLinkWorkflowNames(items []any) error {
	ids := map[string]string{}
	for _, value := range items {
		ids[str(obj(value)["workflow_id"])] = ""
	}
	keys := sortedKeys(ids)
	names := map[string]string{}
	for offset := 0; offset < len(keys); offset += 200 {
		end := offset + 200
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[offset:end]
		args := []any{}
		for _, id := range batch {
			args = append(args, id)
		}
		query := "SELECT id,revision,COALESCE(summary_payload,payload),summary_payload IS NOT NULL FROM workflows WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + ")"
		rows, e := p.DB.Query(query, args...)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id, payload string
			var revision int64
			var projected bool
			if e = rows.Scan(&id, &revision, &payload, &projected); e != nil {
				rows.Close()
				return e
			}
			var workflow Obj
			var e error
			if projected {
				workflow, e = p.openWorkspaceSummary("workflows", id, revision, payload)
			} else {
				workflow, e = p.openWorkspace("workflows", id, revision, payload)
			}
			if e == nil && str(workflow["name"]) != "" {
				names[id] = str(workflow["name"])
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	for _, value := range items {
		item := obj(value)
		if name, ok := names[str(item["workflow_id"])]; ok {
			item["workflow_name"] = name
		}
	}
	return nil
}
