package main

import (
	"context"
	"net/http"
	"net/url"
)

func interfacePluginDefinitionSchema() map[string]any {
	fields := map[string]any{}
	for _, key := range []string{"text", "sender", "title", "type", "image", "link"} {
		fields[key] = strSchema("", 0, 256)
	}
	schema := objectSchema(map[string]any{
		"id":   strSchema(`^custom-[a-z0-9][a-z0-9-]{0,79}$`, 0, 0),
		"kind": enumSchema("config", "code"), "name": strSchema(`\S`, 1, 200), "version": strSchema(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`, 1, 64), "description": strSchema("", 0, 4000),
		"protocols":    map[string]any{"type": "array", "minItems": 1, "maxItems": 1, "items": enumSchema("websocket")},
		"parser":       objectSchema(map[string]any{"format": enumSchema("json", "socketio", "text"), "rootPath": strSchema(`^\$`, 1, 256), "fields": objectSchema(fields)}, "format", "rootPath", "fields"),
		"presentation": objectSchema(map[string]any{"layout": enumSchema("chat", "cards", "compact"), "accent": strSchema(`^#[a-fA-F0-9]{6}$`, 7, 7)}, "layout", "accent"),
		"code":         objectSchema(map[string]any{"language": enumSchema("javascript"), "source": strSchema(`\S`, 1, 60<<10)}, "language", "source"),
	}, "kind", "name", "version", "protocols")
	schema["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "config"}}, "required": []string{"parser", "presentation"}, "not": map[string]any{"required": []string{"code"}}},
		map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "code"}}, "required": []string{"code"}, "not": map[string]any{"required": []string{"parser"}}},
	}
	return schema
}
func interfacePluginSpecs() []managerSpec {
	revision := func() map[string]any {
		return map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740991}
	}
	writeSchema := func() map[string]any {
		return scopedSchema(map[string]any{"pluginId": projectSchema(), "revision": revision()}, "pluginId", "revision")
	}
	return []managerSpec{
		{name: "list_interface_protocols", description: "读取接口协议和序列化能力目录：HTTP/WebSocket已支持，RPC/gRPC/Protobuf和二进制消息仅保留扩展，不执行或回退HTTP。", schema: scopedSchema(map[string]any{}), readOnly: true},
		{name: "list_interface_plugins", description: "读取本项目自定义接口展示插件元数据（新项目目录为空）、安装状态和独立revision；unavailable单独列出没有定义的失效安装ID，可显式卸载或删除，不能安装或执行；元数据不含代码正文。自定义插件仅解析和展示已有WebSocket消息。", schema: scopedSchema(map[string]any{}), readOnly: true},
		{name: "get_interface_plugin", description: "读取本项目自定义插件完整定义及当前插件revision；代码为惰性数据，仅可在浏览器受限沙箱解析，不在管理端执行。项目未创建的插件返回不存在。", schema: scopedSchema(map[string]any{"pluginId": projectSchema()}, "pluginId"), readOnly: true},
		{name: "create_interface_plugin", description: "按插件revision创建本项目配置式或JavaScript解析插件并设置installed，返回完整元数据目录和created_id。定义最多64KiB，JS最多60KiB；只允许parse/render，不能声明发送、网络或运行时权限，管理端加密保存且不执行代码。", schema: scopedSchema(map[string]any{"revision": revision(), "definition": interfacePluginDefinitionSchema(), "installed": map[string]any{"type": "boolean"}}, "revision", "definition", "installed")},
		{name: "update_interface_plugin", description: "按当前插件revision更新本项目自定义插件完整定义；保留安装状态，内容哈希用于刷新浏览器缓存。仅parse/render，归档项目只读。", schema: scopedSchema(map[string]any{"pluginId": projectSchema(), "revision": revision(), "definition": interfacePluginDefinitionSchema()}, "pluginId", "revision", "definition")},
		{name: "delete_interface_plugin", description: "按当前插件revision删除本项目自定义展示插件及安装状态，或删除失效安装ID；集合和历史引用保持原样并回退消息原文。归档项目只读。", schema: writeSchema()},
		{name: "install_interface_plugin", description: "按目录当前revision为本项目安装已创建的自定义插件；首次0，冲突后重新读取。安装仅变更展示偏好，不连接或发送业务请求。", schema: writeSchema()},
		{name: "uninstall_interface_plugin", description: "按目录当前revision卸载本项目展示插件或失效安装ID，保留现有自定义定义；接口回退原文，不修改消息和发送行为。归档项目只读，冲突后重新读取目录。", schema: writeSchema()},
	}
}
func (m managerTools) interfacePluginTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	project, _ := args["projectId"].(string)
	if name == "list_interface_plugins" {
		return m.api.requestProject(ctx, http.MethodGet, "/api/interface-plugins", nil, "", project)
	}
	if name == "list_interface_protocols" {
		return m.api.requestProject(ctx, http.MethodGet, "/api/interface-protocols", nil, "", project)
	}
	if name == "create_interface_plugin" {
		return m.api.requestProject(ctx, http.MethodPost, "/api/interface-plugins", map[string]any{"revision": args["revision"], "definition": args["definition"], "installed": args["installed"]}, "", project)
	}
	plugin, _ := args["pluginId"].(string)
	path := "/api/interface-plugins/" + url.PathEscape(plugin)
	switch name {
	case "get_interface_plugin":
		return m.api.requestProject(ctx, http.MethodGet, path+"/definition", nil, "", project)
	case "update_interface_plugin":
		return m.api.requestProject(ctx, http.MethodPut, path+"/definition", map[string]any{"revision": args["revision"], "definition": args["definition"]}, "", project)
	case "delete_interface_plugin":
		return m.api.requestProject(ctx, http.MethodDelete, path, map[string]any{"revision": args["revision"]}, "", project)
	default:
		return m.api.requestProject(ctx, http.MethodPut, path, map[string]any{"revision": args["revision"], "installed": name == "install_interface_plugin"}, "", project)
	}
}
