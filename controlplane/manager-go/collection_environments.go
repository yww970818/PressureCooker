package main

import (
	"sort"
	"unicode/utf8"
)

const collectionOverridesKey = "project_environment_overrides"

// Collection state stores only custom rows, addressed by the project resource ID.
func validateCollectionOverrides(client Obj) error {
	raw, exists := client[collectionOverridesKey]
	if !exists {
		return nil
	}
	overrides, ok := raw.(map[string]any)
	if !ok || len(overrides) > 200 {
		return api(400, "invalid_environment_overrides", "集合环境覆盖须为最多200项的环境ID映射")
	}
	for id, raw := range overrides {
		entry, ok := raw.(map[string]any)
		if validID(id) != nil || !ok || len(entry) != 1 {
			return api(400, "invalid_environment_overrides", "集合覆盖仅接受环境ID及variables")
		}
		rows, ok := entry["variables"].([]any)
		if !ok || len(rows) > 100 {
			return api(400, "invalid_environment_overrides", "集合覆盖variables须为最多100项数组")
		}
		seen := map[string]bool{}
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			name, nok := row["name"].(string)
			value, vok := row["value"].(string)
			if !ok || len(row) != 2 || !nok || !validEnvironmentName(name) || name == "base_host" || seen[name] || !vok || utf8.RuneCountInString(value) > 4096 || !validXMLText(value) {
				return api(400, "invalid_environment_overrides", "集合变量名须唯一且不可为base_host，值须为最多4096字文本")
			}
			if e := validateEnvironmentRuleDeclaration(value); e != nil {
				return e
			}
			seen[name] = true
		}
	}
	return nil
}
func (p *Platform) validateCollectionEnvironmentIDs(q queryer, client Obj) error {
	if e := validateCollectionOverrides(client); e != nil {
		return e
	}
	if _, authoritative := client[collectionEnvironmentsKey]; authoritative {
		return nil
	}
	for _, id := range objectKeys(obj(client[collectionOverridesKey])) {
		if _, e := p.workspaceGetQ(q, "environments", id, true); e != nil {
			return api(400, "invalid_environment_overrides", "集合覆盖引用的项目环境不存在或已删除，请重新加载环境配置")
		}
	}
	return nil
}
func mergedCollectionVariables(collection, environment Obj, explicit Obj, resolvers ...environmentRuleResolver) (map[string]string, error) {
	variables, e := collectionVariableDefinitions(collection, environment, explicit)
	if e != nil {
		return nil, e
	}
	resolver := newEnvironmentRuleResolver(nil)
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return resolver.resolve(variables)
}
func collectionVariableDefinitions(collection, environment, explicit Obj) (map[string]string, error) {
	variables, e := environmentVariables(environment)
	if e != nil {
		return nil, e
	}
	override := obj(obj(obj(collection["client_state"])[collectionOverridesKey])[str(environment["id"])])
	custom, e := environmentVariables(override)
	if e != nil {
		return nil, e
	}
	for name, value := range custom {
		if name != "base_host" {
			variables[name] = value
		}
	}
	for name, raw := range explicit {
		value, e := environmentVariableText(raw)
		if e != nil {
			return nil, e
		}
		if name != "base_host" {
			variables[name] = value
		}
	}
	variables["base_host"] = str(environment["base_host"])
	return variables, nil
}

// Called under the project lock, in the environment deletion transaction. History
// is append-only; incrementing current revisions also invalidates stale editors.
func (p *Platform) clearCollectionEnvironment(tx *Tx, environmentID string) error {
	rows, e := tx.Query("SELECT id,revision,payload FROM api_collections WHERE project_id=? AND deleted=0 ORDER BY id FOR UPDATE", p.projectID())
	if e != nil {
		return e
	}
	type stored struct {
		id, payload string
		revision    int64
	}
	items := []stored{}
	for rows.Next() {
		var item stored
		if e = rows.Scan(&item.id, &item.revision, &item.payload); e != nil {
			rows.Close()
			return e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, item := range items {
		data, e := p.openWorkspace("api_collections", item.id, item.revision, item.payload)
		if e != nil {
			return e
		}
		overrides := obj(obj(data["client_state"])[collectionOverridesKey])
		if _, present := overrides[environmentID]; !present {
			continue
		}
		delete(overrides, environmentID)
		data["revision"] = item.revision + 1
		data["updated_at"] = now()
		data["project_id"] = p.projectID()
		payload, e := p.sealWorkspace("api_collections", item.id, item.revision+1, data)
		if e != nil {
			return e
		}
		if e = p.saveWorkspace(tx, "api_collections", data, payload, false); e != nil {
			return e
		}
	}
	return nil
}

func collectionOverrideNames(collection Obj) []string {
	names := map[string]bool{}
	for _, value := range obj(obj(collection["client_state"])[collectionOverridesKey]) {
		for _, row := range arr(obj(value)["variables"]) {
			name := str(obj(row)["name"])
			if name != "base_host" && validEnvironmentName(name) {
				names[name] = true
			}
		}
	}
	out := []string{}
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
