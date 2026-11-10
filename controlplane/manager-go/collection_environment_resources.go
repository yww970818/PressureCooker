package main

import (
	"strings"
	"unicode/utf8"
)

const collectionEnvironmentsKey = "collection_environments"

// Resources remain part of encrypted, versioned collection state. An explicit
// empty array is authoritative and prevents deleted legacy projections returning.
func validateCollectionEnvironmentResources(client Obj) error {
	raw, exists := client[collectionEnvironmentsKey]
	if !exists {
		return nil
	}
	rows, ok := raw.([]any)
	if !ok || len(rows) > 200 {
		return api(400, "invalid_collection_environments", "集合环境须为最多200项数组")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		id, name := str(row["id"]), strings.TrimSpace(str(row["name"]))
		if !ok || validID(id) != nil || name == "" || utf8.RuneCountInString(name) > 200 || ids[id] || names[strings.ToLower(name)] {
			return api(400, "invalid_collection_environment", "集合环境须具有唯一ID和名称")
		}
		ids[id] = true
		names[strings.ToLower(name)] = true
		parent := str(row["parent_environment_id"])
		if parent != "" && validID(parent) != nil {
			return api(400, "invalid_collection_environment", "父项目环境ID无效")
		}
		base, hasBase := row["base_host"]
		if hasBase || parent == "" {
			if _, e := parseEnvironmentBase(str(base)); e != nil {
				return e
			}
		}
		variables := row["variables"]
		if variables == nil {
			variables = []any{}
		}
		if e := validateCollectionOverrides(Obj{collectionOverridesKey: Obj{id: Obj{"variables": variables}}}); e != nil {
			return e
		}
		for _, v := range arr(variables) {
			if reservedRuntimeProperty(str(obj(v)["name"])) {
				return api(400, "reserved_property", "集合环境不能使用平台保留变量")
			}
		}
	}
	return nil
}
func collectionEnvironmentResources(collection Obj, parents []any) []any {
	client := obj(collection["client_state"])
	if raw, exists := client[collectionEnvironmentsKey]; exists {
		return arr(raw)
	}
	out := []any{}
	overrides := obj(client[collectionOverridesKey])
	for _, raw := range parents {
		parent := obj(raw)
		id := str(parent["id"])
		entry, present := overrides[id]
		if !present {
			continue
		}
		out = append(out, Obj{"id": "legacy-" + id, "name": parent["name"], "parent_environment_id": id, "variables": obj(entry)["variables"]})
	}
	return out
}
func collectionEnvironmentResource(collection Obj, id string) (Obj, error) {
	for _, raw := range arr(obj(collection["client_state"])[collectionEnvironmentsKey]) {
		row := obj(raw)
		if row["id"] == id {
			return row, nil
		}
	}
	return nil, api(404, "collection_environment_not_found", "集合环境不存在，请重新选择")
}
func resolveCollectionEnvironment(resource, parent Obj, explicit Obj, explicitLast bool) (Obj, error) {
	parentID := str(resource["parent_environment_id"])
	if parentID != "" && (parent == nil || str(parent["id"]) != parentID) {
		return nil, api(400, "invalid_collection_environment_parent", "父项目环境已失效，请修复集合环境")
	}
	variables, e := environmentVariables(parent)
	if e != nil {
		return nil, e
	}
	custom, e := environmentVariables(resource)
	if e != nil {
		return nil, e
	}
	merge := func(values Obj) error {
		for name, raw := range values {
			if name == "base_host" {
				continue
			}
			value, e := environmentVariableText(raw)
			if e != nil {
				return e
			}
			variables[name] = value
		}
		return nil
	}
	if !explicitLast {
		if e = merge(explicit); e != nil {
			return nil, e
		}
	}
	for name, value := range custom {
		if name != "base_host" {
			variables[name] = value
		}
	}
	if explicitLast {
		if e = merge(explicit); e != nil {
			return nil, e
		}
	}
	base := str(parent["base_host"])
	if raw, exists := resource["base_host"]; exists {
		base = str(raw)
	}
	if _, e = parseEnvironmentBase(base); e != nil {
		return nil, e
	}
	rows := []any{}
	for _, name := range sortedKeys(variables) {
		rows = append(rows, Obj{"name": name, "value": variables[name]})
	}
	result := Obj{"id": resource["id"], "name": resource["name"], "base_host": base, "variables": rows, "collection_environment_id": resource["id"]}
	if parentID != "" {
		result["parent_environment_id"] = parentID
		result["parent_environment_revision"] = parent["revision"]
	}
	return result, nil
}
func (p *Platform) resolveCollectionEnvironmentQ(q queryer, collection Obj, id string, explicit Obj, explicitLast bool) (Obj, error) {
	resource, e := collectionEnvironmentResource(collection, id)
	if e != nil {
		return nil, e
	}
	var parent Obj
	if parentID := str(resource["parent_environment_id"]); parentID != "" {
		parent, e = p.workspaceGetQ(q, "environments", parentID, false)
		if e != nil {
			return nil, api(400, "invalid_collection_environment_parent", "父项目环境已失效或不属于当前项目")
		}
	}
	result, e := resolveCollectionEnvironment(resource, parent, explicit, explicitLast)
	if e != nil {
		return nil, e
	}
	result["collection_id"] = collection["id"]
	result["collection_revision"] = collection["revision"]
	return result, nil
}
func (p *Platform) validateCollectionResourceParents(q queryer, previous, current Obj) error {
	for _, raw := range arr(current[collectionEnvironmentsKey]) {
		row := obj(raw)
		parent := str(row["parent_environment_id"])
		if parent == "" {
			continue
		}
		unchanged := false
		for _, old := range arr(previous[collectionEnvironmentsKey]) {
			if obj(old)["id"] == row["id"] && obj(old)["parent_environment_id"] == parent {
				unchanged = true
				break
			}
		}
		if unchanged {
			continue
		}
		if _, e := p.workspaceGetQ(q, "environments", parent, false); e != nil {
			return api(400, "invalid_collection_environment_parent", "新增或改选的父项目环境须属于当前项目且有效")
		}
	}
	return nil
}
func (p *Platform) collectionEnvironmentResourcesRequest(collectionID, environmentID, method string, body Obj) (Obj, error) {
	collection, e := p.workspaceGet("api_collections", collectionID)
	if e != nil {
		return nil, e
	}
	parents, e := p.workspaceList("environments")
	if e != nil {
		return nil, e
	}
	rows := collectionEnvironmentResources(collection, arr(parents["items"]))
	client := obj(collection["client_state"])
	if client == nil {
		client = Obj{}
		collection["client_state"] = client
	}
	client[collectionEnvironmentsKey] = rows
	if method == "GET" {
		if environmentID != "" {
			row, e := collectionEnvironmentResource(collection, environmentID)
			if e != nil {
				return nil, e
			}
			result := clone(row)
			effective, err := p.resolveCollectionEnvironmentQ(p.DB, collection, environmentID, nil, false)
			result["available"] = err == nil
			if err == nil {
				result["effective"] = effective
			}
			result["collection_revision"] = collection["revision"]
			return result, nil
		}
		items := []any{}
		for _, raw := range rows {
			row := clone(obj(raw))
			_, err := p.resolveCollectionEnvironmentQ(p.DB, collection, str(row["id"]), nil, false)
			row["available"] = err == nil
			items = append(items, row)
		}
		return Obj{"items": items, "total": len(items), "collection_id": collectionID, "revision": collection["revision"]}, nil
	}
	if method != "POST" && method != "PUT" && method != "DELETE" {
		return nil, api(405, "method_not_allowed", "此接口不支持该请求方法")
	}
	if method == "POST" && environmentID != "" {
		return nil, api(405, "method_not_allowed", "请向集合环境列表创建资源")
	}
	if method != "POST" && environmentID == "" {
		return nil, api(400, "collection_environment_required", "须指定集合环境ID")
	}
	revision, ok := integer(body["revision"])
	if !ok || revision < 1 {
		return nil, api(400, "revision_required", "保存时必须携带集合版本号")
	}
	actual, _ := integer(collection["revision"])
	if revision != actual {
		return nil, api(409, "revision_conflict", "集合已更新，请重新读取并合并修改")
	}
	index := -1
	for i, raw := range rows {
		if obj(raw)["id"] == environmentID {
			index = i
			break
		}
	}
	if method != "POST" && index < 0 {
		return nil, api(404, "collection_environment_not_found", "集合环境不存在")
	}
	var result Obj
	if method == "DELETE" {
		rows = append(rows[:index], rows[index+1:]...)
	} else {
		result = Obj{"id": environmentID, "name": strings.TrimSpace(str(body["name"])), "variables": []any{}}
		for _, key := range []string{"parent_environment_id", "base_host", "variables"} {
			if value, exists := body[key]; exists {
				result[key] = value
			}
		}
		if method == "POST" {
			result["id"] = newID("collection-env-")
			rows = append(rows, result)
		} else {
			rows[index] = result
		}
	}
	client[collectionEnvironmentsKey] = rows
	collection["revision"] = body["revision"]
	saved, e := p.workspaceUpdate("api_collections", collectionID, collection)
	if e != nil {
		return nil, e
	}
	return Obj{"collection": saved, "environment": result, "deleted_id": environmentID}, nil
}
