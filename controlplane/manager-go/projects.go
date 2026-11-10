package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const defaultProjectID = "project-default"

var projectOwnedTables = []string{"plans", "runs", "reports", "workflows", "api_collections", "environments"}

func (p *Platform) projectID() string {
	if p.ProjectID != "" {
		return p.ProjectID
	}
	return defaultProjectID
}

// A request gets a value copy. The scheduler's shared Platform always remains
// unscoped and can recover and schedule work belonging to every project.
func (p *Platform) forProject(id string) *Platform {
	next := *p
	next.ProjectID = id
	next.borrowed = true
	return &next
}
func objectProject(data Obj) string {
	if id := str(data["project_id"]); id != "" {
		return id
	}
	return defaultProjectID
}
func (p *Platform) checkProject(data Obj) error {
	if p.ProjectID != "" && objectProject(data) != p.ProjectID {
		return api(404, "not_found", "内容不存在")
	}
	return nil
}
func (p *Platform) ensureDefaultProject() error {
	// Bootstrap an empty installation only. Existing projects are authoritative;
	// restarting must not recreate a project the administrator removed.
	var count int
	if e := p.DB.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return nil
	}
	data := Obj{"id": defaultProjectID, "name": "默认项目", "description": "既有数据", "revision": 1, "archived": false, "created_at": now(), "updated_at": now()}
	_, e := p.DB.Exec("INSERT IGNORE INTO projects(id,data,created_at) VALUES(?,?,?)", defaultProjectID, string(encoded(data)), data["created_at"])
	return e
}
func (p *Platform) projectGetQ(q queryer, id string, lock bool) (Obj, error) {
	if e := validID(id); e != nil {
		return nil, e
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var raw string
	e := q.QueryRow("SELECT data FROM projects WHERE id=?"+suffix, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "not_found", "项目不存在")
	}
	if e != nil {
		return nil, e
	}
	return decode([]byte(raw))
}
func (p *Platform) requireActiveQ(q queryer) error {
	project, e := p.projectGetQ(q, p.projectID(), true)
	if e != nil {
		return e
	}
	if project["archived"] == true {
		return api(409, "project_archived", "项目已归档，不能新增或修改内容")
	}
	return nil
}
func (p *Platform) projectCreate(body Obj) (Obj, error) {
	name, description, e := workspaceMetadata(body)
	if e != nil {
		return nil, e
	}
	data := Obj{"id": newID("project-"), "name": name, "description": description, "revision": 1, "archived": false, "created_at": now(), "updated_at": now()}
	_, e = p.DB.Exec("INSERT INTO projects(id,data,created_at) VALUES(?,?,?)", data["id"], string(encoded(data)), data["created_at"])
	return data, e
}
func (p *Platform) projectUpdate(id string, body Obj, archive bool) (Obj, error) {
	var updated Obj
	e := p.transaction(func(tx *Tx) error {
		current, e := p.projectGetQ(tx, id, true)
		if e != nil {
			return e
		}
		revision, ok := integer(body["revision"])
		actual, _ := integer(current["revision"])
		if !ok || revision < 1 {
			return api(400, "revision_required", "须提供项目当前版本号")
		}
		if revision != actual {
			return api(409, "revision_conflict", "项目已更新，请重新加载")
		}
		updated = clone(current)
		if archive {
			updated["archived"] = true
		} else {
			input := clone(current)
			for _, key := range []string{"name", "description"} {
				if value, exists := body[key]; exists {
					input[key] = value
				}
			}
			name, desc, e := workspaceMetadata(input)
			if e != nil {
				return e
			}
			updated["name"] = name
			updated["description"] = desc
		}
		updated["revision"] = actual + 1
		updated["updated_at"] = now()
		_, e = tx.Exec("UPDATE projects SET data=? WHERE id=?", string(encoded(updated)), id)
		return e
	})
	return updated, e
}
func projectPath(path string) bool {
	return path == "/api/projects" || strings.HasPrefix(path, "/api/projects/")
}

// Native links cannot attach request headers. Query selection is deliberately
// limited to file responses; it cannot change the context of lists or writes.
func nativeProjectDownloadPath(path string) bool {
	if _, _, _, ok := projectReportHTMLPath(path); ok {
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" {
		return false
	}
	switch parts[1] {
	case "runs":
		return len(parts) == 5 && parts[3] == "artifacts"
	case "reports":
		return parts[3] == "html" || (len(parts) == 4 && contains([]string{"download", "analysis", "summary"}, parts[3]))
	case "plans", "datasets":
		return len(parts) == 4 && parts[3] == "content"
	case "workflows", "api-collections":
		return len(parts) == 4 && parts[3] == "export"
	case "pressure-reports":
		return len(parts) == 4 && parts[3] == "export.html"
	}
	return false
}

// Platform inventory and session endpoints have no project owner.
func globalProjectPath(path string) bool {
	return projectPath(path) || strings.HasPrefix(path, "/api/clusters/") || contains([]string{"/api/session", "/api/logout", "/api/cluster", "/api/mcp/tools", "/api/openapi.json"}, path)
}
func requestProjectID(r *http.Request) (string, error) {
	headerID := r.Header.Get("X-Project-ID")
	projectID := headerID
	if pathID, _, _, ok := projectReportHTMLPath(r.URL.Path); ok {
		if e := validID(pathID); e != nil {
			return "", e
		}
		if projectID != "" && projectID != pathID {
			return "", api(400, "project_scope_conflict", "项目请求头与报告路径不一致")
		}
		projectID = pathID
	}
	if values, present := r.URL.Query()["project_id"]; present {
		if (r.Method != "GET" && r.Method != "HEAD") || !nativeProjectDownloadPath(r.URL.Path) {
			return "", api(400, "invalid_project_scope", "project_id查询参数仅适用于只读文件下载")
		}
		if len(values) != 1 || values[0] == "" {
			return "", api(400, "invalid_project_scope", "须提供唯一非空project_id查询参数")
		}
		if projectID != "" && projectID != values[0] {
			return "", api(400, "project_scope_conflict", "项目上下文与下载参数不一致")
		}
		projectID = values[0]
	}
	if projectID == "" {
		if globalProjectPath(r.URL.Path) {
			return "", nil
		}
		return "", api(400, "project_required", "项目资源请求须显式提供 X-Project-ID；文件下载可使用 project_id")
	}
	if e := validID(projectID); e != nil {
		return "", e
	}
	return projectID, nil
}
func (h Handler) projectRoute(w http.ResponseWriter, r *http.Request, method string, body Obj) (bool, error) {
	if !projectPath(r.URL.Path) {
		return false, nil
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	send := func(code int, value Obj, e error) (bool, error) {
		if e != nil {
			return true, e
		}
		sendJSON(w, r, code, value)
		return true, nil
	}
	if len(parts) == 2 {
		if method == "GET" {
			items, e := h.P.list("SELECT data FROM projects ORDER BY created_at,id")
			return send(200, Obj{"items": items, "total": len(items)}, e)
		}
		if method == "POST" {
			data, e := h.P.projectCreate(body)
			return send(201, data, e)
		}
	}
	if len(parts) == 3 {
		if method == "GET" {
			data, e := h.P.projectGetQ(h.P.DB, parts[2], false)
			return send(200, data, e)
		}
		if method == "PUT" {
			data, e := h.P.projectUpdate(parts[2], body, false)
			return send(200, data, e)
		}
	}
	if len(parts) == 4 && parts[3] == "archive" && method == "POST" {
		data, e := h.P.projectUpdate(parts[2], body, true)
		return send(200, data, e)
	}
	return true, api(405, "method_not_allowed", "项目接口不支持该操作")
}

func validateImportedProjects(tx *Tx) error {
	checks := []string{}
	for _, table := range projectOwnedTables {
		checks = append(checks, "SELECT COUNT(*) FROM "+table+" o LEFT JOIN projects p ON p.id=o.project_id WHERE p.id IS NULL")
	}
	// Legacy relations live in JSON; validate without rewriting snapshot bytes.
	checks = append(checks,
		"SELECT COUNT(*) FROM plan_library_state s LEFT JOIN plans p ON p.id=s.id WHERE p.id IS NULL OR p.project_id<>s.project_id OR s.archived NOT IN (0,1)",
		"SELECT COUNT(*) FROM workflow_datasets d LEFT JOIN workflows w ON w.id=d.workflow_id LEFT JOIN projects p ON p.id=d.project_id WHERE w.id IS NULL OR p.id IS NULL OR d.project_id<>w.project_id OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.data,'$.project_id')),'')<>d.project_id OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.data,'$.workflow_id')),'')<>d.workflow_id OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.data,'$.node_id')),'')<>d.node_id",
		"SELECT COUNT(*) FROM task_dispatches d LEFT JOIN runs r ON r.id=d.id WHERE r.id IS NULL OR d.project_id<>r.project_id",
		"SELECT COUNT(*) FROM runner_events e LEFT JOIN runs r ON r.id=e.task_id WHERE r.id IS NULL",
		"SELECT COUNT(*) FROM runs r LEFT JOIN plans p ON p.id=JSON_UNQUOTE(JSON_EXTRACT(r.data,'$.plan_id')) WHERE (p.id IS NULL OR r.project_id<>p.project_id) AND NOT (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(r.data,'$.plan_id')),'null') IN ('null','') AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(r.data,'$.metadata_incomplete')),'false')='true')",
		"SELECT COUNT(*) FROM reports r LEFT JOIN runs u ON u.id=r.run_id WHERE u.id IS NULL OR r.project_id<>u.project_id",
		"SELECT COUNT(*) FROM plan_workflow_links l LEFT JOIN plans p ON p.id=l.plan_id LEFT JOIN workflows w ON w.id=l.workflow_id WHERE p.id IS NULL OR w.id IS NULL OR p.project_id<>w.project_id",
		"SELECT COUNT(*) FROM workflow_versions v LEFT JOIN workflows w ON w.id=v.object_id WHERE w.id IS NULL",
		"SELECT COUNT(*) FROM api_collection_versions v LEFT JOIN api_collections c ON c.id=v.object_id WHERE c.id IS NULL",
	)
	for _, query := range checks {
		var count int
		if e := tx.QueryRow(query).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			return fmt.Errorf("import contains a missing or cross-project association")
		}
	}
	return nil
}
