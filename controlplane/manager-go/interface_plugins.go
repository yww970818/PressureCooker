package main

import (
	"context"
	"net/http"
	"strings"
)

const interfacePluginMaxRevision int64 = 9007199254740991

// Project plugin definitions are inert encrypted data interpreted only in a browser sandbox.
func interfacePluginState(project Obj) (Obj, error) {
	raw, exists := project["interface_plugins"]
	if !exists {
		return Obj{"revision": int64(0), "installed": []any{}, "definitions": Obj{}}, nil
	}
	state, ok := raw.(map[string]any)
	revision, validRevision := integer(state["revision"])
	installed, validInstalled := state["installed"].([]any)
	invalid := func() (Obj, error) {
		return nil, api(500, "invalid_interface_plugin_state", "项目接口插件状态无效")
	}
	if !ok || !validRevision || revision < 0 || revision > interfacePluginMaxRevision || !validInstalled {
		return invalid()
	}
	for _, id := range installed {
		if _, ok := id.(string); !ok {
			return invalid()
		}
	}
	if definitions, present := state["definitions"]; present {
		records, ok := definitions.(map[string]any)
		if !ok || len(records) > interfacePluginCountLimit {
			return invalid()
		}
		for id, value := range records {
			record, ok := value.(map[string]any)
			if !ok || !customInterfacePluginID.MatchString(id) || obj(record["metadata"])["id"] != id || str(record["payload"]) == "" {
				return invalid()
			}
		}
	}
	return state, nil
}
func interfacePluginSnapshot(projectID string, state Obj) Obj {
	installed := Obj{}
	for _, id := range arr(state["installed"]) {
		installed[str(id)] = true
	}
	unavailable := []any{}
	for _, id := range objectKeys(installed) {
		if obj(state["definitions"])[id] == nil {
			unavailable = append(unavailable, id)
		}
	}
	items := []any{}
	for _, id := range objectKeys(obj(state["definitions"])) {
		plugin := clone(obj(obj(obj(state["definitions"])[id])["metadata"]))
		plugin["installed"] = boolean(installed[id])
		items = append(items, plugin)
	}
	return Obj{"project_id": projectID, "revision": state["revision"], "items": items, "unavailable": unavailable}
}
func (p *Platform) listInterfacePlugins(ctx context.Context) (Obj, error) {
	project, e := p.extensionProject(ctx, contextQueryer{ctx, p.DB}, "interface_plugins", false)
	if e != nil {
		return nil, e
	}
	state, e := interfacePluginState(project)
	if e != nil {
		return nil, e
	}
	return interfacePluginSnapshot(p.projectID(), state), nil
}
func interfacePluginRevision(body Obj, fields ...string) (int64, error) {
	if e := interfacePluginFields(body, fields...); e != nil {
		return 0, e
	}
	revision, ok := integer(body["revision"])
	if !ok || revision < 0 || revision > interfacePluginMaxRevision {
		return 0, api(400, "revision_required", "须提供当前接口插件版本号，首次为0")
	}
	return revision, nil
}

// Plugin writes lock their own registry; the shared project lock guards archival.
// Project metadata revisions and authored collection/history references are intact.
func (p *Platform) mutateInterfacePlugins(ctx context.Context, revision int64, mutate func(Obj) (bool, error)) (Obj, error) {
	var result Obj
	e := p.transactionContext(ctx, func(tx *Tx) error {

		project, e := p.extensionProject(ctx, tx, "interface_plugins", true)
		if e != nil {
			return e
		}
		if project["archived"] == true {
			return api(409, "project_archived", "项目已归档，不能修改接口展示插件")
		}
		state, e := interfacePluginState(project)
		if e != nil {
			return e
		}
		actual, _ := integer(state["revision"])
		if revision != actual {
			return api(409, "revision_conflict", "接口插件已更新，请重新加载")
		}
		changed, e := mutate(state)
		if e != nil {
			return e
		}
		if changed {
			if actual == interfacePluginMaxRevision {
				return api(409, "revision_exhausted", "接口插件版本号已达到上限")
			}
			state["revision"] = actual + 1
			project["interface_plugins"] = state
			if e = p.saveExtensionRegistry(tx, "interface_plugins", state); e != nil {
				return e
			}
		}
		result = interfacePluginSnapshot(p.projectID(), state)
		return nil
	})
	return result, e
}
func (p *Platform) updateInterfacePlugin(ctx context.Context, id string, body Obj) (Obj, error) {
	revision, e := interfacePluginRevision(body, "revision", "installed")
	if e != nil {
		return nil, e
	}
	installed, ok := body["installed"].(bool)
	if !ok {
		return nil, api(400, "invalid_interface_plugin", "installed须为布尔值")
	}
	return p.mutateInterfacePlugins(ctx, revision, func(state Obj) (bool, error) {
		return setInterfacePluginInstalled(state, id, installed)
	})
}
func (p *Platform) getInterfacePluginDefinition(ctx context.Context, id string) (Obj, error) {

	project, e := p.extensionProject(ctx, contextQueryer{ctx, p.DB}, "interface_plugins", false)
	if e != nil {
		return nil, e
	}
	state, e := interfacePluginState(project)
	if e != nil {
		return nil, e
	}
	record, exists := obj(state["definitions"])[id]
	if !exists {
		return nil, api(404, "interface_plugin_not_found", "接口展示插件不存在")
	}
	definition, e := p.openInterfacePluginDefinition(p.projectID(), id, obj(record))
	if e != nil {
		return nil, e
	}
	return Obj{"project_id": p.projectID(), "revision": state["revision"], "content_sha256": obj(obj(record)["metadata"])["content_sha256"], "definition": definition}, nil
}
func (p *Platform) createInterfacePlugin(ctx context.Context, body Obj) (Obj, error) {
	revision, e := interfacePluginRevision(body, "revision", "definition", "installed")
	if e != nil {
		return nil, e
	}
	raw, ok := body["definition"].(map[string]any)
	if !ok {
		return nil, api(400, "invalid_interface_plugin", "definition须为对象")
	}
	installed, ok := body["installed"].(bool)
	if !ok {
		return nil, api(400, "invalid_interface_plugin", "installed须为布尔值")
	}
	id := str(raw["id"])
	if id == "" {
		id = newID("custom-")
	}
	definition, e := validateInterfacePluginDefinition(raw, id)
	if e != nil {
		return nil, e
	}
	record, e := p.sealInterfacePluginDefinition(p.projectID(), definition)
	if e != nil {
		return nil, e
	}
	result, e := p.mutateInterfacePlugins(ctx, revision, func(state Obj) (bool, error) {
		definitions := obj(state["definitions"])
		if definitions[id] != nil {
			return false, api(409, "interface_plugin_exists", "该插件标识已存在")
		}
		if len(definitions) >= interfacePluginCountLimit {
			return false, api(400, "interface_plugin_limit", "每个项目最多100个自定义插件")
		}
		definitions[id] = record
		state["definitions"] = definitions
		if installed {
			state["installed"] = append(arr(state["installed"]), id)
		}
		return true, nil
	})
	if e == nil {
		result["created_id"] = id
	}
	return result, e
}
func (p *Platform) saveInterfacePluginDefinition(ctx context.Context, id string, body Obj) (Obj, error) {

	revision, e := interfacePluginRevision(body, "revision", "definition")
	if e != nil {
		return nil, e
	}
	raw, ok := body["definition"].(map[string]any)
	if !ok {
		return nil, api(400, "invalid_interface_plugin", "definition须为对象")
	}
	definition, e := validateInterfacePluginDefinition(raw, id)
	if e != nil {
		return nil, e
	}
	return p.mutateInterfacePlugins(ctx, revision, func(state Obj) (bool, error) {
		definitions := obj(state["definitions"])
		previous, exists := definitions[id]
		if !exists {
			return false, api(404, "interface_plugin_not_found", "接口展示插件不存在")
		}
		if obj(obj(previous)["metadata"])["content_sha256"] == interfacePluginMetadata(definition)["content_sha256"] {
			return false, nil
		}
		record, e := p.sealInterfacePluginDefinition(p.projectID(), definition)
		if e != nil {
			return false, e
		}
		definitions[id] = record
		return true, nil
	})
}
func (p *Platform) deleteInterfacePlugin(ctx context.Context, id string, body Obj) (Obj, error) {

	revision, e := interfacePluginRevision(body, "revision")
	if e != nil {
		return nil, e
	}
	return p.mutateInterfacePlugins(ctx, revision, func(state Obj) (bool, error) {
		return removeInterfacePlugin(state, id)
	})
}

// Missing definitions are inert references, removable only by an explicit write.
func setInterfacePluginInstalled(state Obj, id string, installed bool) (bool, error) {
	current, next := false, []any{}
	for _, existing := range arr(state["installed"]) {
		if existing == id {
			current = true
		} else {
			next = append(next, existing)
		}
	}
	if obj(state["definitions"])[id] == nil && (installed || !current) {
		return false, api(404, "interface_plugin_not_found", "接口展示插件不存在")
	}
	if installed == current {
		return false, nil
	}
	if installed {
		next = append(next, id)
	}
	state["installed"] = next
	return true, nil
}
func removeInterfacePlugin(state Obj, id string) (bool, error) {
	definitions := obj(state["definitions"])
	exists := definitions[id] != nil
	installed := []any{}
	for _, existing := range arr(state["installed"]) {
		if existing == id {
			exists = true
		} else {
			installed = append(installed, existing)
		}
	}
	if !exists {
		return false, api(404, "interface_plugin_not_found", "接口展示插件不存在")
	}
	delete(definitions, id)
	state["installed"] = installed
	return true, nil
}
func interfacePluginPath(path string) bool {
	return path == "/api/interface-plugins" || strings.HasPrefix(path, "/api/interface-plugins/")
}
func (h Handler) interfacePluginRoute(w http.ResponseWriter, r *http.Request, method string, body Obj) (bool, error) {
	if r.URL.Path == "/api/interface-protocols" {
		if method != "GET" {
			return true, api(405, "method_not_allowed", "协议目录仅支持读取")
		}
		sendJSON(w, r, http.StatusOK, interfaceProtocolCatalog())
		return true, nil
	}
	if !interfacePluginPath(r.URL.Path) {
		return false, nil
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var value Obj
	var e error
	switch {
	case len(parts) == 2 && method == "GET":
		value, e = h.P.listInterfacePlugins(r.Context())
	case len(parts) == 2 && method == "POST":
		value, e = h.P.createInterfacePlugin(r.Context(), body)
	case len(parts) == 3 && method == "PUT":
		value, e = h.P.updateInterfacePlugin(r.Context(), parts[2], body)
	case len(parts) == 3 && method == "DELETE":
		value, e = h.P.deleteInterfacePlugin(r.Context(), parts[2], body)
	case len(parts) == 4 && parts[3] == "definition" && method == "GET":
		if version := r.URL.Query().Get("version"); version != "" {
			value, e = h.P.extensionVersion(r.Context(), "interface_plugins", parts[2], version)
		} else {
			value, e = h.P.getInterfacePluginDefinition(r.Context(), parts[2])
		}
	case len(parts) == 4 && parts[3] == "definition" && method == "PUT":
		value, e = h.P.saveInterfacePluginDefinition(r.Context(), parts[2], body)
	default:
		return true, api(405, "method_not_allowed", "接口插件不支持该操作")
	}
	if e == nil {
		sendJSON(w, r, http.StatusOK, value)
	}
	return true, e
}
