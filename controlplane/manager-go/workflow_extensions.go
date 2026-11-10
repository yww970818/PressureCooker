package main

import (
	"context"
	"net/http"
	"pressurecooker/manager/internal/workflowcatalog"
	"regexp"
	"strings"
)

var workflowComponentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.$]{0,239}$`)

func workflowExtensionMetadata(d Obj) Obj {
	out := Obj{}
	for _, key := range []string{"id", "name", "component", "version", "description", "source_url"} {
		out[key] = d[key]
	}
	out["source"] = "project"
	out["content_sha256"] = digest(encoded(d))
	return out
}
func validateWorkflowExtension(d Obj, id string) (Obj, error) {
	if !customInterfacePluginID.MatchString(id) || len(encoded(d)) > 128<<10 {
		return nil, api(400, "invalid_workflow_extension", "扩展标识无效或定义超过128KiB")
	}
	if e := interfacePluginFields(d, "id", "name", "component", "version", "description", "source_url", "code", "template_xml"); e != nil {
		return nil, e
	}
	if v, ok := d["id"]; ok && v != id {
		return nil, api(400, "invalid_workflow_extension", "扩展标识不一致")
	}
	out := Obj{"id": id}
	for _, field := range []struct {
		k        string
		min, max int
	}{{"name", 1, 200}, {"component", 1, 240}, {"version", 1, 64}, {"description", 0, 4000}, {"source_url", 0, 2000}, {"code", 1, 60 << 10}, {"template_xml", 0, 60 << 10}} {
		value := d[field.k]
		if value == nil && field.min == 0 {
			value = ""
		}
		text, ok := interfacePluginText(value, field.min, field.max)
		if !ok {
			return nil, api(400, "invalid_workflow_extension", "扩展字段无效："+field.k)
		}
		out[field.k] = text
	}
	if !workflowComponentName.MatchString(str(out["component"])) || !interfacePluginVersion.MatchString(str(out["version"])) {
		return nil, api(400, "invalid_workflow_extension", "组件类型或版本无效")
	}
	if workflowKind(str(out["component"])) != "custom" {
		return nil, api(400, "invalid_workflow_extension", "原生和平台组件使用平台维护的适配器")
	}
	if raw := str(out["template_xml"]); raw != "" {
		roots, e := parseXMLFragment(raw)
		if e != nil {
			return nil, e
		}
		if len(roots) != 1 || xmlName(roots[0].Name) != str(out["component"]) {
			return nil, api(400, "invalid_workflow_extension", "模板必须是对应组件的单个XML元素")
		}
	}
	return out, nil
}
func workflowExtensionState(project Obj) (Obj, error) {
	if project["workflow_extensions"] == nil {
		return Obj{"revision": int64(0), "definitions": Obj{}}, nil
	}
	state := obj(project["workflow_extensions"])
	revision, ok := integer(state["revision"])
	_, validDefinitions := state["definitions"].(map[string]any)
	if !ok || revision < 0 || revision > interfacePluginMaxRevision || !validDefinitions {
		return nil, api(500, "invalid_workflow_extension_state", "画布扩展状态无效")
	}
	return state, nil
}
func workflowExtensionSnapshot(projectID string, state Obj) Obj {
	items := []any{}
	for _, c := range workflowcatalog.Native {
		items = append(items, Obj{"id": c.Component, "component": c.Component, "class_name": c.ClassName, "name": c.Title, "kind": c.Kind, "source": "jmeter", "version": c.Version, "source_url": "https://jmeter.apache.org/usermanual/component_reference.html", "adapter": "native", "installed": true})
	}
	items = append(items, Obj{"id": "platform-websocket", "component": "WebSocketSampler", "name": "WebSocket 请求", "source": "platform", "source_url": "controlplane/manager-go/workflow_websocket.go", "adapter": "native", "installed": true, "description": "平台维护的通用WebSocket采样器，编译为JSR223脚本"}, Obj{"id": "platform-thread-observer", "component": "io.pressurecooker.jmeter.ThreadObservationListener", "name": "实际线程数观测", "source": "platform", "source_url": "tools/jmeter-thread-observer", "adapter": "runtime", "installed": true, "description": "pressurecooker-thread-observer.jar；由执行器注入，只观测线程数，不产生采样请求。目录声明构建依赖，不代表已核验远端JAR安装状态。"})
	for _, id := range objectKeys(obj(state["definitions"])) {
		record := obj(obj(state["definitions"])[id])
		m := clone(obj(record["metadata"]))
		m["installed"] = record["installed"] == true
		m["adapter"] = "javascript"
		items = append(items, m)
	}
	for _, value := range items {
		item := obj(value)
		item["editor_enabled"] = item["installed"] == true
		item["definition_available"] = true
		item["runtime_status"] = "unknown"
		item["runtime_notice"] = "编辑定义可用；尚无目标节点运行依赖的有效验证证据"
	}
	return Obj{"project_id": projectID, "revision": state["revision"], "items": items}
}
func (p *Platform) listWorkflowExtensions(ctx context.Context) (Obj, error) {
	project, e := p.extensionProject(ctx, contextQueryer{ctx, p.DB}, "workflow_extensions", false)
	if e != nil {
		return nil, e
	}
	state, e := workflowExtensionState(project)
	if e != nil {
		return nil, e
	}
	return workflowExtensionSnapshot(p.projectID(), state), nil
}
func (p *Platform) getWorkflowExtension(ctx context.Context, id string) (Obj, error) {
	project, e := p.extensionProject(ctx, contextQueryer{ctx, p.DB}, "workflow_extensions", false)
	if e != nil {
		return nil, e
	}
	state, e := workflowExtensionState(project)
	if e != nil {
		return nil, e
	}
	record := obj(obj(state["definitions"])[id])
	if len(record) == 0 {
		return nil, api(404, "not_found", "自定义画布适配器不存在")
	}
	definition, e := p.openWorkspace("workflow_extensions", p.projectID()+"/"+id, 1, str(record["payload"]))
	if e != nil {
		return nil, e
	}
	definition, e = validateWorkflowExtension(definition, id)
	if e != nil {
		return nil, e
	}
	if digest(encoded(definition)) != str(obj(record["metadata"])["content_sha256"]) {
		return nil, api(500, "invalid_workflow_extension_state", "扩展内容校验失败")
	}
	return Obj{"definition": definition, "revision": state["revision"], "project_id": p.projectID(), "content_sha256": digest(encoded(definition))}, nil
}
func (p *Platform) mutateWorkflowExtension(ctx context.Context, id, method string, body Obj) (Obj, error) {
	revision, ok := integer(body["revision"])
	if !ok || revision < 0 {
		return nil, api(400, "revision_required", "须提供当前画布扩展版本")
	}
	if e := interfacePluginFields(body, "revision", "definition", "installed"); e != nil {
		return nil, e
	}
	if v, supplied := body["installed"]; supplied {
		if _, ok := v.(bool); !ok {
			return nil, api(400, "invalid_workflow_extension", "installed必须是布尔值")
		}
	}
	created := method == "POST"
	if created {
		id = newID("custom-")
	}
	var definition Obj
	var e error
	if method != "DELETE" && body["definition"] != nil {
		definition, e = validateWorkflowExtension(obj(body["definition"]), id)
		if e != nil {
			return nil, e
		}
	}
	if created && definition == nil {
		return nil, api(400, "invalid_workflow_extension", "须提供完整适配器定义")
	}
	var out Obj
	e = p.transactionContext(ctx, func(tx *Tx) error {
		project, e := p.extensionProject(ctx, tx, "workflow_extensions", true)
		if e != nil {
			return e
		}
		if project["archived"] == true {
			return api(409, "project_archived", "归档项目不能修改画布扩展")
		}
		state, e := workflowExtensionState(project)
		if e != nil {
			return e
		}
		actual, _ := integer(state["revision"])
		if actual != revision {
			return api(409, "revision_conflict", "画布扩展已更新，请刷新后重试")
		}
		definitions := obj(state["definitions"])
		record := obj(definitions[id])
		if !created && len(record) == 0 {
			return api(404, "not_found", "自定义画布适配器不存在")
		}
		if created && len(definitions) >= 100 {
			return api(400, "extension_limit", "每项目最多100个画布适配器")
		}
		before := string(encoded(definitions))
		if method == "DELETE" {
			delete(definitions, id)
		} else {
			if definition != nil && digest(encoded(definition)) != str(obj(record["metadata"])["content_sha256"]) {
				sealed, e := p.sealWorkspace("workflow_extensions", p.projectID()+"/"+id, 1, definition)
				if e != nil {
					return e
				}
				record = Obj{"metadata": workflowExtensionMetadata(definition), "payload": sealed, "installed": record["installed"] == true}
			}
			if value, supplied := body["installed"]; supplied {
				record["installed"] = value
			}
			definitions[id] = record
		}
		if before != string(encoded(definitions)) {
			if actual == interfacePluginMaxRevision {
				return api(409, "revision_exhausted", "扩展版本已达上限")
			}
			state["revision"] = actual + 1
			project["workflow_extensions"] = state
			if e = p.saveExtensionRegistry(tx, "workflow_extensions", state); e != nil {
				return e
			}
		}
		out = workflowExtensionSnapshot(p.projectID(), state)
		if created {
			out["created_id"] = id
		}
		return nil
	})
	return out, e
}
func (p *Platform) workflowExtensionUsage(ctx context.Context) (Obj, error) {
	index := Obj{}
	visit := func(w Obj) {
		seen := map[string]bool{}
		for _, component := range workflowUsageComponents(obj(w["graph"])) {
			value, exists := index[component]
			item := obj(value)
			if !exists {
				source := "external"
				name := component
				if c, ok := workflowcatalog.ByName[component]; ok {
					source = "jmeter"
					name = c.Title
				}
				if component == "WebSocketSampler" || component == "io.pressurecooker.jmeter.ThreadObservationListener" {
					source = "platform"
				}
				item = Obj{"component": component, "name": name, "source": source, "node_count": 0, "workflows": []any{}}
				index[component] = item
			}
			item["node_count"] = int(number(item["node_count"])) + 1
			if !seen[component] {
				item["workflows"] = append(arr(item["workflows"]), Obj{"id": w["id"], "name": w["name"], "revision": w["revision"]})
				seen[component] = true
			}
		}
	}
	// Statistics must include older workflows beyond the ordinary list's page.
	// Bound each read and release its cursor before decrypting large graphs.
	cursor := ""
	for {
		rows, e := p.DB.QueryContext(ctx, "SELECT id,revision,payload FROM workflows WHERE project_id=? AND deleted=0 AND id>? ORDER BY id LIMIT 32", p.projectID(), cursor)
		if e != nil {
			return nil, e
		}
		type record struct {
			id, payload string
			revision    int64
		}
		batch := []record{}
		for rows.Next() {
			var item record
			if e = rows.Scan(&item.id, &item.revision, &item.payload); e != nil {
				rows.Close()
				return nil, e
			}
			batch = append(batch, item)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if len(batch) == 0 {
			break
		}
		for _, item := range batch {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			w, e := p.openWorkspace("workflows", item.id, item.revision, item.payload)
			if e != nil {
				return nil, e
			}
			visit(w)
		}
		cursor = batch[len(batch)-1].id
	}
	items := []any{}
	for _, key := range objectKeys(index) {
		items = append(items, index[key])
	}
	return Obj{"project_id": p.projectID(), "items": items}, nil
}

// Include components retained inside opaque hashTrees without rewriting them.
func workflowUsageComponents(graph Obj) []string {
	components := []string{}
	var visit func(*xmlElement, int)
	visit = func(tree *xmlElement, depth int) {
		if depth > 256 {
			return
		}
		pairs, err := pairedComponents(tree)
		if err != nil {
			return
		}
		for _, pair := range pairs {
			name := xmlName(pair[0].Name)
			if websocketComponent(pair[0]) {
				name = "WebSocketSampler"
			}
			components = append(components, name)
			visit(pair[1], depth+1)
		}
	}
	for _, entry := range arr(graph["nodes"]) {
		properties := obj(obj(entry)["properties"])
		if component := str(properties["component"]); component != "" {
			components = append(components, component)
		}
		roots, err := parseXMLFragment(str(properties["raw_xml"]))
		if err == nil && len(roots) == 2 && roots[1].Name.Local == "hashTree" {
			visit(roots[1], 0)
		}
	}
	return components
}
func workflowExtensionPath(path string) bool {
	return path == "/api/workflow-extensions" || strings.HasPrefix(path, "/api/workflow-extensions/")
}
func (h Handler) workflowExtensionRoute(w http.ResponseWriter, r *http.Request, method string, body Obj) (bool, error) {
	if !workflowExtensionPath(r.URL.Path) {
		return false, nil
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var out Obj
	var e error
	switch {
	case len(parts) == 3 && parts[2] == "expand" && method == "POST":
		out, e = expandNativeWorkflowNode(body)
	case len(parts) == 2 && method == "GET":
		out, e = h.P.listWorkflowExtensions(r.Context())
	case len(parts) == 2 && method == "POST":
		out, e = h.P.mutateWorkflowExtension(r.Context(), "", method, body)
	case len(parts) == 3 && parts[2] == "usage" && method == "GET":
		out, e = h.P.workflowExtensionUsage(r.Context())
	case len(parts) == 3 && method == "GET":
		if version := r.URL.Query().Get("version"); version != "" {
			out, e = h.P.extensionVersion(r.Context(), "workflow_extensions", parts[2], version)
		} else {
			out, e = h.P.getWorkflowExtension(r.Context(), parts[2])
		}
	case len(parts) == 3 && (method == "PUT" || method == "DELETE"):
		out, e = h.P.mutateWorkflowExtension(r.Context(), parts[2], method, body)
	default:
		return true, api(405, "method_not_allowed", "画布扩展不支持此操作")
	}
	if e == nil {
		sendJSON(w, r, http.StatusOK, out)
	}
	return true, e
}
