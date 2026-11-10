package main

import (
	"database/sql"
	"errors"
	"net/url"
	"strconv"
)

// Dataset management reads metadata only. Immutable objects and task snapshots
// remain owned by the existing dataset APIs.
func (p *Platform) datasetManagementList(values url.Values) (Obj, error) {
	invalid := func() error {
		return api(400, "invalid_dataset_filter", "数据文件分页或来源筛选参数无效")
	}
	for _, key := range []string{"page", "page_size", "q", "workflow_id", "node_id"} {
		if v, ok := values[key]; ok && len(v) != 1 {
			return nil, invalid()
		}
	}
	page, pageSize := 1, 20
	for _, field := range []struct {
		key   string
		value *int
	}{{"page", &page}, {"page_size", &pageSize}} {
		if v, ok := values[field.key]; ok {
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 {
				return nil, invalid()
			}
			*field.value = n
		}
	}
	if pageSize > 100 || page-1 > int(^uint(0)>>1)/pageSize {
		return nil, invalid()
	}
	workflowID, nodeID := values.Get("workflow_id"), values.Get("node_id")
	if workflowID != "" && validID(workflowID) != nil || nodeID != "" && (validID(nodeID) != nil || workflowID == "") {
		return nil, invalid()
	}
	if workflowID != "" {
		var count int
		if e := p.DB.QueryRow("SELECT COUNT(*) FROM workflows WHERE id=? AND project_id=?", workflowID, p.projectID()).Scan(&count); e != nil {
			return nil, e
		}
		if count == 0 {
			return nil, invalid()
		}
	}
	where := " WHERE d.project_id=? AND d.deleted=0"
	args := []any{p.projectID()}
	if workflowID != "" {
		where += " AND d.workflow_id=?"
		args = append(args, workflowID)
	}
	if nodeID != "" {
		where += " AND d.node_id=?"
		args = append(args, nodeID)
	}
	// LOCATE is a literal substring match: %, _ and backslash have no wildcard
	// meaning, and the same predicate is used for the count and page query.
	if q := values.Get("q"); q != "" {
		where += " AND LOCATE(?,JSON_UNQUOTE(JSON_EXTRACT(d.data,'$.name')))>0"
		args = append(args, q)
	}
	var total int64
	if e := p.DB.QueryRow("SELECT COUNT(*) FROM workflow_datasets d"+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	query := "SELECT d.data FROM workflow_datasets d" + where + " ORDER BY d.created_at DESC,d.id DESC LIMIT ? OFFSET ?"
	pageArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	rows, e := p.DB.Query(query, pageArgs...)
	if e != nil {
		return nil, e
	}
	items := []any{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		item, e := decode([]byte(raw))
		if e != nil {
			rows.Close()
			return nil, e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Finish the metadata cursor before loading sources: this also works with
	// a one-connection pool. Cache only a small summary per distinct workflow.
	sources := map[string]datasetManagementSource{}
	for _, value := range items {
		item := obj(value)
		id := str(item["workflow_id"])
		source, exists := sources[id]
		if !exists {
			source, e = p.datasetManagementSource(id)
			if e != nil {
				return nil, e
			}
			sources[id] = source
		}
		status := "workflow_deleted"
		if source.live {
			status = "node_removed"
			if source.nodes[str(item["node_id"])] {
				status = "available"
			}
		}
		item["workflow_name"] = source.name
		item["source_available"] = status == "available"
		item["source_status"] = status
	}
	return Obj{"items": items, "total": total, "page": page, "page_size": pageSize}, nil
}

// This summary deliberately cannot retain a workflow graph or its non-CSV
// configuration. Every source is read once and checked in the current project.
type datasetManagementSource struct {
	name  string
	live  bool
	nodes map[string]bool
}

func (p *Platform) legacyDatasetManagementSource(id string) (datasetManagementSource, error) {
	source := datasetManagementSource{nodes: map[string]bool{}}
	var payload string
	var revision int64
	var deleted int
	e := p.DB.QueryRow("SELECT revision,payload,deleted FROM workflows WHERE id=? AND project_id=?", id, p.projectID()).Scan(&revision, &payload, &deleted)
	if errors.Is(e, sql.ErrNoRows) {
		return source, nil
	}
	if e != nil {
		return source, e
	}
	workflow, e := p.openWorkspace("workflows", id, revision, payload)
	if e != nil {
		return source, e
	}
	source.name = str(workflow["name"])
	source.live = deleted == 0
	if source.live {
		for _, value := range arr(obj(workflow["graph"])["nodes"]) {
			node := obj(value)
			props := obj(node["properties"])
			if props["component"] == "CSVDataSet" && props["enabled"] != false {
				source.nodes[str(node["id"])] = true
			}
		}
	}
	return source, nil
}

// Missing projections are rebuilt in bounded keyset batches. Existing projected
// workflows never have their full source decrypted by this catalog.
func (p *Platform) missingDatasetManagementCatalog() (Obj, error) {
	ctx := p.requestContext()
	items := []any{}
	type implicitNode struct {
		workflowID string
		node       Obj
	}
	pending := []implicitNode{}
	cursor := ""
	for {
		rows, e := p.DB.QueryContext(ctx, "SELECT id,revision,payload FROM workflows WHERE project_id=? AND deleted=0 AND summary_payload IS NULL AND id>? ORDER BY id LIMIT 32", p.projectID(), cursor)
		if e != nil {
			return nil, e
		}
		type source struct {
			id, payload string
			revision    int64
		}
		batch := []source{}
		for rows.Next() {
			var v source
			if e = rows.Scan(&v.id, &v.revision, &v.payload); e != nil {
				rows.Close()
				return nil, e
			}
			batch = append(batch, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if len(batch) == 0 {
			break
		}
		for _, v := range batch {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			workflow, e := p.openWorkspace("workflows", v.id, v.revision, v.payload)
			if e != nil {
				return nil, e
			}
			nodes := []any{}
			for _, value := range arr(obj(workflow["graph"])["nodes"]) {
				node := obj(value)
				props := obj(node["properties"])
				if props["component"] != "CSVDataSet" || props["enabled"] == false {
					continue
				}
				requirement := datasetRequirement(node)
				entry := Obj{}
				for _, key := range []string{"node_id", "node_name", "columns", "variable_names", "delimiter"} {
					entry[key] = requirement[key]
				}
				nodes = append(nodes, entry)
				if len(stringList(props["dataset_columns"])) == 0 {
					pending = append(pending, implicitNode{v.id, entry})
				}
			}
			if len(nodes) > 0 {
				items = append(items, Obj{"id": v.id, "name": workflow["name"], "nodes": nodes})
			}
		}
		cursor = batch[len(batch)-1].id
	}
	// Infer from the same first active file metadata as enrichDatasetGraph,
	// after closing the workflow cursor so no nested DB connection is needed.
	for _, entry := range pending {
		var raw string
		e := p.DB.QueryRowContext(ctx, "SELECT data FROM workflow_datasets WHERE project_id=? AND workflow_id=? AND node_id=? AND deleted=0 ORDER BY created_at,id LIMIT 1", p.projectID(), entry.workflowID, entry.node["node_id"]).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return nil, e
		}
		data, e := decode([]byte(raw))
		if e != nil {
			return nil, e
		}
		columns := stringList(data["columns"])
		if len(columns) > 0 {
			entry.node["columns"] = columns
			if len(stringList(entry.node["variable_names"])) == 0 {
				entry.node["variable_names"] = append([]string{}, columns...)
			}
		}
	}
	return Obj{"workflows": items}, nil
}

func (p *Platform) datasetManagementSource(id string) (datasetManagementSource, error) {
	source := datasetManagementSource{nodes: map[string]bool{}}
	snapshot, e := p.readWorkflowCSVSnapshot(p.DB, id)
	if errors.Is(e, sql.ErrNoRows) {
		return source, nil
	}
	if e != nil {
		return source, e
	}
	if !snapshot.summary.Valid {
		return p.legacyDatasetManagementSource(id)
	}
	head, e := p.openWorkspaceSummary("workflows", id, snapshot.revision, snapshot.summary.String)
	if e != nil {
		return source, e
	}
	source.name = str(head["name"])
	source.live = snapshot.deleted == 0
	if source.live {
		for _, node := range snapshot.nodes {
			source.nodes[node.id] = true
		}
	}
	return source, nil
}
func (p *Platform) datasetManagementCatalog() (Obj, error) {
	ctx := p.requestContext()
	rows, e := p.DB.QueryContext(ctx, "SELECT w.id,w.revision,w.summary_payload,c.node_id,c.payload,d.data FROM workflows w JOIN workflow_csv_nodes c ON c.workflow_id=w.id AND c.revision=w.revision AND c.project_id=w.project_id LEFT JOIN workflow_datasets d ON d.id=(SELECT f.id FROM workflow_datasets f WHERE f.project_id=w.project_id AND f.workflow_id=w.id AND f.node_id=c.node_id AND f.deleted=0 ORDER BY f.created_at,f.id LIMIT 1) WHERE w.project_id=? AND w.deleted=0 AND w.summary_payload IS NOT NULL ORDER BY w.updated_at DESC,w.id DESC,c.position", p.projectID())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []any{}
	byID := map[string]Obj{}
	outdated := map[string]bool{}
	for rows.Next() {
		var id, headRaw, nodeID, raw string
		var rev int64
		var file sql.NullString
		if e = rows.Scan(&id, &rev, &headRaw, &nodeID, &raw, &file); e != nil {
			return nil, e
		}
		item := byID[id]
		if item == nil {
			head, e := p.openWorkspaceSummary("workflows", id, rev, headRaw)
			if e != nil {
				return nil, e
			}
			item = Obj{"id": id, "name": head["name"], "nodes": []any{}}
			byID[id] = item
			items = append(items, item)
		}
		node, e := p.openWorkspace("csv-node/"+id, nodeID, rev, raw)
		if e != nil {
			return nil, e
		}
		if _, versioned := node["columns_declared"].(bool); !versioned {
			outdated[id] = true
		}
		entry := Obj{}
		for _, k := range []string{"node_id", "node_name", "columns", "variable_names", "delimiter"} {
			entry[k] = node[k]
		}
		if (node["columns_declared"] == false || len(stringList(entry["columns"])) == 0) && file.Valid {
			data, e := decode([]byte(file.String))
			if e != nil {
				return nil, e
			}
			columns := stringList(data["columns"])
			if len(columns) > 0 {
				entry["columns"] = columns
				if len(stringList(entry["variable_names"])) == 0 {
					entry["variable_names"] = columns
				}
			}
		}
		item["nodes"] = append(arr(item["nodes"]), entry)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Older CSV projections lack explicit column provenance. Read only those
	// workflow sources after closing the cursor; never infer authored columns.
	for id := range outdated {
		workflow, err := p.workspaceGet("workflows", id)
		if err != nil {
			return nil, err
		}
		graph := obj(workflow["graph"])
		if err = p.enrichDatasetGraph(contextQueryer{ctx, p.DB}, graph, id); err != nil {
			return nil, err
		}
		byID[id] = Obj{"id": id, "name": workflow["name"], "nodes": csvRequirements(graph)}
	}
	missing, e := p.missingDatasetManagementCatalog()
	if e != nil {
		return nil, e
	}
	for _, item := range arr(missing["workflows"]) {
		byID[str(obj(item)["id"])] = obj(item)
	}
	// Ordering reads metadata only and also covers sources missing a projection.
	order, e := p.DB.QueryContext(ctx, "SELECT id FROM workflows WHERE project_id=? AND deleted=0 ORDER BY updated_at DESC,id DESC", p.projectID())
	if e != nil {
		return nil, e
	}
	defer order.Close()
	items = []any{}
	for order.Next() {
		var id string
		if e = order.Scan(&id); e != nil {
			return nil, e
		}
		if item := byID[id]; item != nil {
			items = append(items, item)
		}
	}
	return Obj{"workflows": items}, order.Err()
}
