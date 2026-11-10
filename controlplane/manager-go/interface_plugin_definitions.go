package main

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"
)

const interfacePluginDefinitionLimit = 64 << 10
const interfacePluginCodeLimit = 60 << 10
const interfacePluginCountLimit = 100

var customInterfacePluginID = regexp.MustCompile(`^custom-[a-z0-9][a-z0-9-]{0,79}$`)
var interfacePluginVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
var interfacePluginAccent = regexp.MustCompile(`^#[a-fA-F0-9]{6}$`)
var interfacePluginPathRE = regexp.MustCompile(`^\$(?:(?:\.[A-Za-z_][A-Za-z0-9_-]*)|(?:\[[0-9]{1,4}\])|(?:\["[^"\\\x00-\x1f]{1,80}"\])|(?:\['[^'\\\x00-\x1f]{1,80}'\]))*$`)
var interfacePluginPathSegment = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_-]*)|\[(?:[0-9]+|"([^"]+)"|'([^']+)')\]`)

func validInterfacePluginPath(path string) bool {
	if len(path) > 256 || !interfacePluginPathRE.MatchString(path) {
		return false
	}
	segments := interfacePluginPathSegment.FindAllStringSubmatch(path, -1)
	if len(segments) > 16 {
		return false
	}
	for _, segment := range segments {
		for _, key := range segment[1:] {
			if key == "__proto__" || key == "prototype" || key == "constructor" {
				return false
			}
		}
	}
	return true
}
func interfacePluginFields(value Obj, allowed ...string) error {
	for key := range value {
		if !contains(allowed, key) {
			return api(400, "invalid_interface_plugin", "插件包含不支持的字段："+key)
		}
	}
	return nil
}
func interfacePluginText(value any, min, max int) (string, bool) {
	text, ok := value.(string)
	return text, ok && utf8.ValidString(text) && !strings.ContainsRune(text, 0) && utf8.RuneCountInString(strings.TrimSpace(text)) >= min && utf8.RuneCountInString(text) <= max
}

// Definitions are inert data. Only the browser sandbox may interpret uploaded
// JavaScript; the manager neither evaluates source nor fetches dependencies.
func validateInterfacePluginDefinition(raw Obj, id string) (Obj, error) {
	invalid := func() (Obj, error) {
		return nil, api(400, "invalid_interface_plugin", "插件定义、解析路径或展示配置无效")
	}
	if len(encoded(raw)) > interfacePluginDefinitionLimit {
		return nil, api(413, "interface_plugin_too_large", "插件定义最多64KiB，代码最多60KiB")
	}
	if e := interfacePluginFields(raw, "id", "kind", "name", "version", "description", "protocols", "parser", "presentation", "code"); e != nil {
		return nil, e
	}
	if !customInterfacePluginID.MatchString(id) {
		return invalid()
	}
	if rawID, exists := raw["id"]; exists && rawID != id {
		return invalid()
	}
	name, ok := interfacePluginText(raw["name"], 1, 200)
	if !ok {
		return invalid()
	}
	version, ok := raw["version"].(string)
	if !ok || !interfacePluginVersion.MatchString(version) {
		return invalid()
	}
	description := ""
	if value, exists := raw["description"]; exists {
		description, ok = interfacePluginText(value, 0, 4000)
		if !ok {
			return invalid()
		}
	}
	protocols, ok := raw["protocols"].([]any)
	if !ok || len(protocols) != 1 || protocols[0] != "websocket" {
		return invalid()
	}
	kind, ok := raw["kind"].(string)
	if !ok || (kind != "config" && kind != "code") {
		return invalid()
	}
	out := Obj{"id": id, "kind": kind, "name": strings.TrimSpace(name), "version": version, "description": description, "protocols": []any{"websocket"}}
	if kind == "config" {
		if _, exists := raw["code"]; exists {
			return invalid()
		}
		parser, ok := raw["parser"].(map[string]any)
		if !ok {
			return invalid()
		}
		if e := interfacePluginFields(parser, "format", "rootPath", "fields"); e != nil {
			return nil, e
		}
		format, ok := parser["format"].(string)
		if !ok || !contains([]string{"json", "socketio", "text"}, format) {
			return invalid()
		}
		root, ok := parser["rootPath"].(string)
		if !ok || !validInterfacePluginPath(root) {
			return invalid()
		}
		fields, ok := parser["fields"].(map[string]any)
		if !ok {
			return invalid()
		}
		if e := interfacePluginFields(fields, "text", "sender", "title", "type", "image", "link"); e != nil {
			return nil, e
		}
		for _, value := range fields {
			path, ok := value.(string)
			if !ok || (path != "" && !validInterfacePluginPath(path)) {
				return invalid()
			}
		}
		out["parser"] = clone(parser)
	} else {
		if _, exists := raw["parser"]; exists {
			return invalid()
		}
		code, ok := raw["code"].(map[string]any)
		if !ok {
			return invalid()
		}
		if e := interfacePluginFields(code, "language", "source"); e != nil {
			return nil, e
		}
		source, ok := interfacePluginText(code["source"], 1, interfacePluginCodeLimit)
		if !ok || len(source) > interfacePluginCodeLimit || code["language"] != "javascript" {
			return invalid()
		}
		out["code"] = Obj{"language": "javascript", "source": source}
	}
	presentation := Obj{"layout": "chat", "accent": "#3b82f6"}
	if value, exists := raw["presentation"]; exists {
		supplied, ok := value.(map[string]any)
		if !ok {
			return invalid()
		}
		if e := interfacePluginFields(supplied, "layout", "accent"); e != nil {
			return nil, e
		}
		layout, ok := supplied["layout"].(string)
		if !ok || !contains([]string{"chat", "cards", "compact"}, layout) {
			return invalid()
		}
		accent, ok := supplied["accent"].(string)
		if !ok || !interfacePluginAccent.MatchString(accent) {
			return invalid()
		}
		presentation = clone(supplied)
	} else if kind == "config" {
		return invalid()
	}
	out["presentation"] = presentation
	if len(encoded(out)) > interfacePluginDefinitionLimit {
		return nil, api(413, "interface_plugin_too_large", "插件定义最多64KiB")
	}
	return out, nil
}
func interfacePluginMetadata(definition Obj) Obj {
	metadata := Obj{}
	for _, key := range []string{"id", "kind", "name", "version", "description", "protocols"} {
		metadata[key] = definition[key]
	}
	metadata["capabilities"] = []any{"parse", "render"}
	metadata["renderer"] = "declarative"
	if definition["kind"] == "code" {
		metadata["renderer"] = "sandbox-code"
	}
	hash := sha256.Sum256(encoded(definition))
	metadata["content_sha256"] = hex.EncodeToString(hash[:])
	return metadata
}
func (p *Platform) sealInterfacePluginDefinition(projectID string, definition Obj) (Obj, error) {
	sealed, e := p.sealWorkspace("interface_plugins", projectID+"/"+str(definition["id"]), 1, definition)
	if e != nil {
		return nil, e
	}
	return Obj{"metadata": interfacePluginMetadata(definition), "payload": sealed}, nil
}
func (p *Platform) openInterfacePluginDefinition(projectID, id string, record Obj) (Obj, error) {
	definition, e := p.openWorkspace("interface_plugins", projectID+"/"+id, 1, str(record["payload"]))
	if e != nil {
		return nil, e
	}
	normalized, e := validateInterfacePluginDefinition(definition, id)
	if e != nil {
		return nil, api(500, "invalid_interface_plugin_state", "项目接口插件定义无效")
	}
	if str(interfacePluginMetadata(normalized)["content_sha256"]) != str(obj(record["metadata"])["content_sha256"]) {
		return nil, api(500, "invalid_interface_plugin_state", "项目接口插件内容校验失败")
	}
	return normalized, nil
}
