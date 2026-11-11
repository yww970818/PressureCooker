package main

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const scriptIdentitiesMarker = "pressurecooker.script_identities"
const scriptIdentityPrefix = "script_identity."
const maxScriptIdentities = 64
const maxScriptIdentityFields = 20
const maxScriptIdentitiesBytes = 64 << 10

func normalizeScriptIdentities(raw any) (Obj, error) {
	declaration := obj(raw)
	if err := interfacePluginFields(declaration, "schema_version", "identities"); err != nil {
		return nil, api(400, "invalid_script_identities", "身份声明包含未知字段")
	}
	version, validVersion := integer(declaration["schema_version"])
	if !validVersion || version != 1 {
		return nil, api(400, "invalid_script_identities", "脚本身份须声明 schema_version=1")
	}
	rows, ok := declaration["identities"].([]any)
	if !ok || len(rows) > maxScriptIdentities {
		return nil, api(400, "invalid_script_identities", "脚本身份须为最多64条的数组")
	}
	out := []any{}
	seen := map[string]bool{}
	for _, value := range rows {
		row := obj(value)
		if err := interfacePluginFields(row, "name", "label", "purpose", "required", "fields"); err != nil {
			return nil, api(400, "invalid_script_identities", "身份包含未知字段")
		}
		name := str(row["name"])
		if !planIdentityNameRE.MatchString(name) || seen[name] || strings.TrimSpace(str(row["label"])) == "" || len(str(row["label"])) > 320 || strings.TrimSpace(str(row["purpose"])) == "" || len(str(row["purpose"])) > 2000 {
			return nil, api(400, "invalid_script_identities", "身份须有唯一名称、显示名称及用途")
		}
		seen[name] = true
		fields, ok := row["fields"].([]any)
		if !ok || len(fields) > maxScriptIdentityFields {
			return nil, api(400, "invalid_script_identities", "身份字段须为最多20项的数组")
		}
		normalized := []any{}
		names := map[string]bool{}
		for _, value := range fields {
			field := obj(value)
			if err := interfacePluginFields(field, "name", "type", "secret", "required"); err != nil {
				return nil, api(400, "invalid_script_identities", "身份字段包含未知配置")
			}
			key := str(field["name"])
			kind := str(field["type"])
			secret, sok := field["secret"].(bool)
			required, rok := field["required"].(bool)
			if !validIdentityField(key) || names[key] || !contains([]string{"string", "number", "boolean"}, kind) || !sok || !rok {
				return nil, api(400, "invalid_script_identities", "字段须声明唯一name、type及布尔secret/required")
			}
			names[key] = true
			normalized = append(normalized, Obj{"name": key, "type": kind, "secret": secret, "required": required})
		}
		required, ok := row["required"].(bool)
		if !ok {
			return nil, api(400, "invalid_script_identities", "身份须显式声明required")
		}
		out = append(out, Obj{"name": name, "label": row["label"], "purpose": row["purpose"], "required": required, "fields": normalized})
	}
	result := Obj{"schema_version": 1, "identities": out}
	if len(encoded(result)) > maxScriptIdentitiesBytes {
		return nil, api(400, "invalid_script_identities", "身份声明超过64KiB")
	}
	return result, nil
}
func scriptIdentityMetadata(content string) (Obj, error) {
	roots, err := parseXMLFragment(content)
	if err != nil {
		return nil, err
	}
	var result Obj
	var walk func(*xmlElement) error
	walk = func(node *xmlElement) error {
		if node.Name.Local == "TestPlan" {
			for _, child := range node.children() {
				if child.Name.Local != "stringProp" || child.attr("name") != scriptIdentitiesMarker {
					continue
				}
				if result != nil || len(child.text()) > maxScriptIdentitiesBytes {
					return api(400, "invalid_script_identities", "身份声明重复或过大")
				}
				var value any
				if json.Unmarshal([]byte(child.text()), &value) != nil {
					return api(400, "invalid_script_identities", "身份声明须为JSON对象")
				}
				result, err = normalizeScriptIdentities(value)
				if err != nil {
					return err
				}
			}
		}
		for _, child := range node.children() {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err = walk(root); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func validateExecutionIdentities(plan, properties Obj) error {
	if len(arr(plan["cookie_accounts"])) > 0 {
		return api(409, "script_identity_upgrade_required", "旧Cookie身份脚本须显式转换并重新发布后执行")
	}
	declared := map[string]Obj{}
	for _, value := range arr(obj(plan["script_identities"])["identities"]) {
		identity := obj(value)
		name := str(identity["name"])
		active := identity["required"] == true
		for _, v := range arr(identity["fields"]) {
			field := obj(v)
			key := scriptIdentityPrefix + name + "." + str(field["name"])
			declared[key] = field
			active = active || str(properties[key]) != ""
		}
		for _, v := range arr(identity["fields"]) {
			field := obj(v)
			key := scriptIdentityPrefix + name + "." + str(field["name"])
			text := str(properties[key])
			if active && field["required"] == true && strings.TrimSpace(text) == "" {
				return api(400, "invalid_script_identities", str(identity["label"])+"缺少"+str(field["name"]))
			}
			if text == "" {
				continue
			}
			switch str(field["type"]) {
			case "boolean":
				if text != "true" && text != "false" {
					return api(400, "invalid_script_identities", "布尔字段只接受true或false")
				}
			case "number":
				n, e := strconv.ParseFloat(text, 64)
				if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					return api(400, "invalid_script_identities", "数值字段须为有限数字")
				}
			}
		}
	}
	for key := range properties {
		if strings.HasPrefix(key, cookieAccountPrefix) {
			return api(409, "script_identity_upgrade_required", "旧身份属性不再执行，请使用显式转换后的脚本和属性")
		}
		if strings.HasPrefix(key, scriptIdentityPrefix) && declared[key] == nil {
			return api(400, "invalid_script_identities", "身份字段必须由脚本声明")
		}
	}
	return nil
}

// Explicit source conversion produces a new script. It never edits a published
// object or guesses how authentication values should be placed on the wire.
func convertScriptIdentitySource(content string) (string, error) {
	roots, err := parseXMLFragment(content)
	if err != nil {
		return "", err
	}
	if existing, err := scriptIdentityMetadata(content); err != nil || existing != nil {
		return "", api(409, "identity_conversion_conflict", "源脚本已包含新版身份声明或声明损坏")
	}
	converted := 0
	var walk func(*xmlElement) error
	walk = func(node *xmlElement) error {
		if node.Name.Local == "TestPlan" {
			for _, child := range node.children() {
				if child.Name.Local != "stringProp" || child.attr("name") != cookieAccountsMarker {
					continue
				}
				var value any
				if json.Unmarshal([]byte(child.text()), &value) != nil {
					return api(400, "invalid_script_identities", "历史身份声明损坏")
				}
				roles, e := normalizePlanCookieAccounts(value)
				if e != nil {
					return e
				}
				identities := []any{}
				for _, v := range roles {
					role := obj(v)
					fields := []any{}
					for _, name := range stringList(role["fields"]) {
						fields = append(fields, Obj{"name": name, "type": "string", "secret": true, "required": contains(stringList(role["required_fields"]), name)})
					}
					identities = append(identities, Obj{"name": role["name"], "label": role["label"], "purpose": role["purpose"], "required": role["required"], "fields": fields})
				}
				declaration := Obj{"schema_version": 1, "identities": identities}
				node.setProp("stringProp", scriptIdentitiesMarker, string(encoded(declaration)))
				converted++
			}
		}
		for _, child := range node.children() {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err = walk(root); err != nil {
			return "", err
		}
	}
	if converted != 1 {
		return "", api(400, "invalid_script_identities", "转换须有且仅有一份历史身份声明")
	}
	// This opt-in source converter replaces the identity namespace text. The caller reviews
	// the new source and fingerprint before a separate upload/publication.
	output := fragmentXML(roots...)
	output = strings.ReplaceAll(output, cookieAccountPrefix, scriptIdentityPrefix)
	// Remove the old declaration from the new source only.
	parsed, err := parseXMLFragment(output)
	if err != nil {
		return "", err
	}
	var strip func(*xmlElement)
	strip = func(node *xmlElement) {
		for _, child := range node.children() {
			if child.Name.Local == "stringProp" && child.attr("name") == cookieAccountsMarker {
				child.setAttr("name", "pressurecooker.retired_identity_declaration")
			}
			strip(child)
		}
	}
	for _, root := range parsed {
		strip(root)
	}
	return fragmentXML(parsed...), nil
}
