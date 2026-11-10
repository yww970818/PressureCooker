package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func workspacePath(path string) bool {
	return path == "/api/environments" || strings.HasPrefix(path, "/api/environments/") || path == "/api/workflows" || strings.HasPrefix(path, "/api/workflows/") || path == "/api/api-collections" || strings.HasPrefix(path, "/api/api-collections/")
}
func (h Handler) workspaceRoute(w http.ResponseWriter, r *http.Request, method string, body Obj) (bool, error) {
	if !workspacePath(r.URL.Path) {
		return false, nil
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	table := "workflows"
	if parts[1] == "environments" {
		table = "environments"
	}
	if parts[1] == "api-collections" {
		table = "api_collections"
	}
	send := func(status int, value any, e error) (bool, error) {
		if e != nil {
			return true, e
		}
		sendJSON(w, r, status, value)
		return true, nil
	}
	if len(parts) == 2 {
		switch method {
		case "GET":
			var value Obj
			var e error
			if r.URL.Query().Get("view") == "summary" && table != "environments" {
				value, e = h.P.workspaceSummaryList(table, r.URL.Query())
			} else {
				value, e = h.P.workspaceList(table)
			}
			return send(200, value, e)
		case "POST":
			value, e := h.P.workspaceCreate(table, body)
			return send(201, value, e)
		}
		return true, api(405, "method_not_allowed", "此接口不支持该请求方法")
	}
	id := parts[2]
	if table == "api_collections" && id == "import" && len(parts) == 3 && method == "POST" {
		input, e := parseCollectionImport(body)
		if e != nil {
			return true, e
		}
		value, e := h.P.workspaceCreate(table, input, true)
		return send(201, value, e)
	}
	if table == "workflows" && id == "preview" && len(parts) == 3 {
		if method != "POST" {
			return true, api(405, "method_not_allowed", "此接口不支持该请求方法")
		}
		graph, ok := body["graph"].(map[string]any)
		if !ok {
			return true, api(400, "invalid_graph", "工作流图数据必须是对象")
		}
		if e := h.P.validateAPIReferences(h.P.DB, graph); e != nil {
			return true, e
		}
		content, e := compileWorkflow(graph)
		if e != nil {
			return true, e
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.WriteHeader(200)
		_, e = w.Write([]byte(content))
		return true, e
	}
	if table == "workflows" && id == "import" && len(parts) == 3 && method == "POST" {
		value, e := h.P.importWorkflow(body)
		if e == nil && r.URL.Query().Get("view") == "outline" {
			return send(201, workflowOutline(value), nil)
		}
		if e == nil && r.URL.Query().Get("view") == "editor" {
			return send(201, editorReadPayload(value), nil)
		}
		return send(201, value, e)
	}
	if validID(id) != nil {
		return true, api(400, "invalid_id", "内容ID无效")
	}
	if len(parts) == 3 {
		switch method {
		case "GET":
			var value any
			var e error
			if table == "workflows" && r.URL.Query().Get("view") == "outline" {
				var full Obj
				full, e = h.P.workspaceGet(table, id)
				if e == nil {
					value = workflowOutline(full)
				}
			} else if table == "workflows" && r.URL.Query().Get("view") == "editor" {
				value, e = h.P.workflowEditorRead(id)
			} else {
				value, e = h.P.workspaceGet(table, id)
			}
			return send(200, value, e)
		case "PUT":
			value, e := h.P.workspaceUpdate(table, id, body)
			return send(200, value, e)
		case "DELETE":
			value, e := h.P.workspaceDelete(table, id)
			return send(200, value, e)
		}
		return true, api(405, "method_not_allowed", "此接口不支持该请求方法")
	}
	if table == "workflows" && len(parts) == 4 && parts[3] == "nodes" && method == "GET" {
		value, e := h.P.workflowNodeRead(id, r.URL.Query().Get("revision"), r.URL.Query().Get("ids"))
		if e != nil {
			return true, e
		}
		return send(200, editorReadPayload(value), nil)
	}
	if table == "workflows" && len(parts) == 5 && parts[3] == "versions" && method == "GET" && r.URL.Query().Get("view") == "editor" {
		revision, e := strconv.ParseInt(parts[4], 10, 64)
		if e != nil || revision < 1 {
			return true, api(400, "invalid_revision", "历史版本号无效")
		}
		value, e := h.P.workflowRevision(id, revision)
		if e != nil {
			return true, e
		}
		return send(200, editorReadPayload(value), nil)
	}
	if table == "workflows" && len(parts) == 4 && parts[3] == "plans" && method == "GET" {
		value, e := h.P.workflowPlans(id)
		return send(200, value, e)
	}
	if table != "environments" && parts[3] == "versions" && method == "GET" && len(parts) <= 5 {
		revision := int64(0)
		if len(parts) == 5 {
			var e error
			revision, e = strconv.ParseInt(parts[4], 10, 64)
			if e != nil || revision < 1 {
				return true, api(400, "invalid_revision", "历史版本号无效")
			}
		}
		var value Obj
		var e error
		if revision == 0 && r.URL.Query().Get("view") == "summary" {
			value, e = h.P.workspaceVersionSummaries(table, id, r.URL.Query())
		} else {
			value, e = h.P.workspaceVersions(table, id, revision)
		}
		return send(200, value, e)
	}
	if table == "workflows" && len(parts) == 4 {
		if parts[3] == "export" && method == "GET" {
			workflow, e := h.P.workspaceGet(table, id)
			if e != nil {
				return true, e
			}
			content, e := compileWorkflow(obj(workflow["graph"]))
			if e != nil {
				return true, e
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.jmx"`)
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			w.WriteHeader(200)
			if r.Method != "HEAD" {
				_, e = w.Write([]byte(content))
			}
			return true, e
		}
		if parts[3] == "publish" && method == "POST" {
			value, e := h.P.publishWorkflow(id, body)
			return send(201, value, e)
		}
	}
	if table == "api_collections" && (len(parts) == 4 || len(parts) == 5) && parts[3] == "collection-environments" {
		envID := ""
		if len(parts) == 5 {
			envID = parts[4]
		}
		value, e := h.P.collectionEnvironmentResourcesRequest(id, envID, method, body)
		status := 200
		if method == "POST" {
			status = 201
		}
		return send(status, value, e)
	}
	if table == "api_collections" && len(parts) == 4 {
		switch parts[3] {
		case "settings", "environments", "requests":
			if method == "PUT" {
				value, e := h.P.collectionPatch(id, body, parts[3])
				return send(200, value, e)
			}
		case "debug":
			if method == "POST" {
				value, e := h.P.collectionDebug(r.Context(), id, body)
				return send(200, value, e)
			}
		case "export":
			if method == "GET" {
				collection, e := h.P.workspaceGet(table, id)
				if e != nil {
					return true, e
				}
				format := r.URL.Query().Get("format")
				if format == "" {
					format = "openapi"
				}
				var value Obj
				switch format {
				case "openapi":
					value = collectionDocumentWithAuth(collection)
					value["x-jmeter-client-state"] = clone(obj(collection["client_state"]))
				case "postman":
					value, e = exportPostman(collection)
				default:
					e = api(400, "invalid_export", "导出格式须为openapi或postman")
				}
				if e != nil {
					return true, e
				}
				w.Header().Set("Content-Disposition", `attachment; filename="`+id+`-`+format+`.json"`)
				return send(200, value, nil)
			}
		}
	}
	return true, api(404, "not_found", "接口不存在")
}
