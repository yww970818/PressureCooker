package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

func (p *Platform) collectionPatch(id string, body Obj, kind string) (Obj, error) {
	current, e := p.workspaceGet("api_collections", id)
	if e != nil {
		return nil, e
	}
	input := clone(current)
	input["revision"] = body["revision"]
	document := obj(input["document"])
	client := obj(input["client_state"])
	switch kind {
	case "settings":
		for _, key := range []string{"name", "description", "client_state"} {
			if value, exists := body[key]; exists {
				input[key] = value
			}
		}
		if servers, exists := body["servers"]; exists {
			if _, ok := servers.([]any); !ok {
				return nil, api(400, "invalid_servers", "servers须为数组")
			}
			for _, server := range arr(servers) {
				if _, ok := obj(server)["url"].(string); !ok {
					return nil, api(400, "invalid_servers", "每个服务器须包含url")
				}
			}
			document["servers"] = servers
		}
	case "environments":
		id := str(body["environment_id"])
		if validID(id) != nil {
			return nil, api(400, "invalid_environment_overrides", "须提供项目environment_id")
		}
		if _, e := p.workspaceGet("environments", id); e != nil {
			return nil, e
		}
		rows, ok := body["variables"].([]any)
		if !ok {
			return nil, api(400, "invalid_environment_overrides", "variables须为数组")
		}
		overrides := obj(client[collectionOverridesKey])
		if overrides == nil {
			overrides = Obj{}
		}
		overrides[id] = Obj{"variables": rows}
		client[collectionOverridesKey] = overrides
		input["client_state"] = client
	case "requests":
		path, method := str(body["path"]), strings.ToLower(str(body["method"]))
		operation, ok := body["operation"].(map[string]any)
		if !ok || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\r\n\x00") || !apiMethod(method) {
			return nil, api(400, "invalid_operation", "须提供以/开头的path、有效method和operation对象")
		}
		paths, ok := document["paths"].(map[string]any)
		if !ok {
			paths = Obj{}
			document["paths"] = paths
		}
		previousPath, previousMethod := str(body["previous_path"]), strings.ToLower(str(body["previous_method"]))
		if previousPath != "" || previousMethod != "" {
			if previousPath == "" || !apiMethod(previousMethod) || obj(paths[previousPath])[previousMethod] == nil {
				return nil, api(400, "invalid_operation", "原接口路径或方法不存在")
			}
			if (previousPath != path || previousMethod != method) && obj(paths[path])[method] != nil {
				return nil, api(409, "operation_exists", "目标路径和方法已存在")
			}
			delete(obj(paths[previousPath]), previousMethod)
			if len(obj(paths[previousPath])) == 0 {
				delete(paths, previousPath)
			}
		}
		entry, ok := paths[path].(map[string]any)
		if !ok {
			entry = Obj{}
			paths[path] = entry
		}
		entry[method] = clone(operation)
	}
	return p.workspaceUpdate("api_collections", id, input)
}

func selectedEnvironment(document, client Obj, name string) Obj {
	if name == "" {
		name = str(client["active_environment"])
	}
	return obj(obj(document["x-scalar-environments"])[name])
}
func environmentVariableText(value any) (string, error) {
	if text, ok := value.(string); ok {
		e := validateEnvironmentRuleDeclaration(text)
		return text, e
	}
	if metadata, ok := value.(map[string]any); ok {
		if text, ok := metadata["default"].(string); ok {
			e := validateEnvironmentRuleDeclaration(text)
			return text, e
		}
	}
	return "", api(400, "invalid_environments", "环境变量值须为文本或包含文本default的对象")
}
func environmentVariables(environment Obj) (map[string]string, error) {
	out := map[string]string{}
	value, exists := environment["variables"]
	if !exists {
		return out, nil
	}
	switch variables := value.(type) {
	case map[string]any:
		for key, v := range variables {
			s, e := environmentVariableText(v)
			if e != nil {
				return nil, e
			}
			out[key] = s
		}
	case []any:
		for _, v := range variables {
			m := obj(v)
			name, ok := m["name"].(string)
			if !ok {
				name, ok = m["key"].(string)
			}
			if !ok || name == "" {
				return nil, api(400, "invalid_environments", "环境变量须包含name")
			}
			value, e := environmentVariableText(m["value"])
			if e != nil {
				return nil, e
			}
			out[name] = value
		}
	default:
		return nil, api(400, "invalid_environments", "环境variables须为对象或数组")
	}
	return out, nil
}

// Accept legacy/MCP variable maps, but persist the iterable shape expected by
// native Scalar. Existing array entries and environment metadata remain intact.
func canonicalScalarEnvironments(input Obj) (Obj, error) {
	environments := clone(input)
	for _, value := range environments {
		environment, ok := value.(map[string]any)
		if !ok {
			return nil, api(400, "invalid_environments", "每个环境须为对象")
		}
		variables, e := environmentVariables(environment)
		if e != nil {
			return nil, e
		}
		switch source := environment["variables"].(type) {
		case map[string]any:
			entries := []any{}
			for _, name := range sortedKeys(variables) {
				entries = append(entries, Obj{"name": name, "value": source[name]})
			}
			environment["variables"] = entries
		case []any:
			for _, value := range source {
				entry := obj(value)
				if _, hasName := entry["name"]; !hasName {
					entry["name"] = entry["key"]
				}
			}
		default:
			environment["variables"] = []any{}
		}
	}
	return environments, nil
}

var apiVariableRE = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}|\$\{([^{}]+)\}`)

func expandAPIVariables(value string, variables map[string]string) (string, error) {
	missing := false
	result := apiVariableRE.ReplaceAllStringFunc(value, func(match string) string {
		groups := apiVariableRE.FindStringSubmatch(match)
		key := groups[1]
		if key == "" {
			key = groups[2]
		}
		v, ok := variables[key]
		if !ok {
			missing = true
			return match
		}
		return v
	})
	if missing {
		return "", api(400, "unresolved_variable", "请求包含未定义环境变量")
	}
	return result, nil
}
func operationServer(document, pathItem, operation Obj) (string, error) {
	servers := arr(operation["servers"])
	if len(servers) == 0 {
		servers = arr(pathItem["servers"])
	}
	if len(servers) == 0 {
		servers = arr(document["servers"])
	}
	if len(servers) == 0 {
		return "", api(400, "server_required", "请求需要明确的HTTP服务器地址")
	}
	server := obj(servers[0])
	value, ok := server["url"].(string)
	if !ok || value == "" {
		return "", api(400, "invalid_servers", "服务器地址无效")
	}
	for name, v := range obj(server["variables"]) {
		if def, ok := obj(v)["default"].(string); ok {
			value = strings.ReplaceAll(value, "{"+name+"}", def)
		}
	}
	return value, nil
}
func scalarValue(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return string(v), nil
	case bool:
		return fmt.Sprint(v), nil
	case float64, int, int64:
		return fmt.Sprint(v), nil
	case nil:
		return "", nil
	default:
		return "", api(400, "unsupported_operation", "复杂参数须提供显式字符串示例")
	}
}

// Native editor state lives in the selected default example. It takes
// precedence over a deprecated top-level example left behind by an import.
func debugParameterExample(param Obj) (Obj, any, bool) {
	examples := obj(param["examples"])
	if value, exists := examples["default"]; exists {
		example := obj(value)
		value, exists := example["value"]
		return example, value, exists
	}
	for _, key := range objectKeys(examples) {
		example := obj(examples[key])
		if value, exists := example["value"]; exists {
			return example, value, true
		}
	}
	if value, exists := param["example"]; exists {
		return nil, value, true
	}
	// Only explicitly authored schema values are usable, never generated enum
	// suggestions or values guessed from a type/schema.
	schema := obj(param["schema"])
	for _, key := range []string{"example", "default"} {
		if value, exists := schema[key]; exists {
			return nil, value, true
		}
	}
	return nil, nil, false
}

func debugParameterDisabled(param Obj) bool {
	if !contains([]string{"query", "header", "cookie", "path"}, str(param["in"])) {
		return false
	}
	example, value, exists := debugParameterExample(param)
	if disabled, explicit := example["x-disabled"].(bool); explicit {
		return disabled
	}
	// Content-based serialization is not supported here. Keep its explicit
	// error instead of silently dropping a populated value we cannot inspect.
	if param["content"] != nil || param["required"] == true || param["in"] == "path" {
		return false
	}
	return !exists || value == nil || value == ""
}

func parameterValue(param Obj) (string, error) {
	location := str(param["in"])
	style := str(param["style"])
	expected := "form"
	if location == "path" || location == "header" {
		expected = "simple"
	}
	if (style != "" && style != expected) || param["allowReserved"] == true || param["content"] != nil {
		return "", api(400, "unsupported_operation", "参数序列化尚不能可靠转换，请提供简单字符串参数")
	}
	if param["$ref"] != nil {
		return "", api(400, "unsupported_operation", "参数引用须先解析为内联参数")
	}
	_, value, exists := debugParameterExample(param)
	if exists {
		return scalarValue(value)
	}
	if param["required"] == true {
		return "", api(400, "parameter_value_required", "必填参数缺少示例值："+str(param["name"]))
	}
	return "", api(400, "parameter_value_required", "参数缺少明确示例值："+str(param["name"]))
}
func operationBody(operation Obj) (string, string, error) {
	return operationBodyWithVariables(operation, nil)
}

func operationBodyWithVariables(operation Obj, variables map[string]string) (string, string, error) {
	raw, exists := operation["requestBody"]
	if !exists {
		return "", "", nil
	}
	body := obj(raw)
	if body["$ref"] != nil {
		return "", "", api(400, "unsupported_operation", "请求体引用须先解析")
	}
	content := obj(body["content"])
	keys := objectKeys(content)
	if len(keys) != 1 {
		return "", "", api(400, "unsupported_operation", "请求体须明确选择一种媒体类型")
	}
	contentType := keys[0]
	if strings.HasPrefix(contentType, "multipart/") {
		return "", "", api(400, "unsupported_operation", "文件或multipart请求请使用Scalar调试")
	}
	media := obj(content[contentType])
	// Match Scalar's native example precedence. Its editors clear serializedValue
	// and dataValue on edits, so preserved import bytes never override later edits.
	var value any
	exists = false
	examples := obj(media["examples"])
	keys = objectKeys(examples)
	if len(keys) > 0 {
		selected := keys[0]
		if _, ok := examples["default"]; ok {
			selected = "default"
		}
		example := obj(examples[selected])
		if raw, present := example["serializedValue"]; present {
			serialized, ok := raw.(string)
			if !ok {
				return "", "", api(400, "unsupported_operation", "序列化请求体示例须为文本")
			}
			return serialized, contentType, nil
		}
		if data, present := example["dataValue"]; present {
			value, exists = data, true
			if strings.Contains(strings.ToLower(contentType), "json") {
				return string(encoded(data)), contentType, nil
			}
		}
		if !exists {
			value, exists = example["value"]
		}
	}
	if !exists {
		value, exists = media["example"]
	}
	if !exists {
		value, exists = obj(media["schema"])["example"]
	}
	if !exists {
		value, exists = obj(media["schema"])["default"]
	}
	if !exists {
		return "", "", api(400, "unsupported_operation", "请求体须提供明确示例，不能凭schema猜测执行内容")
	}
	if text, ok := value.(string); ok {
		return text, contentType, nil
	}
	if strings.Contains(strings.ToLower(contentType), "json") {
		return string(encoded(value)), contentType, nil
	}
	// Edited rows need environment expansion before encoding. The legacy
	// operationBody/export caller has no bindings and must keep rejecting this
	// shape rather than percent-encoding templates into an unusable raw body.
	if rows, ok := value.([]any); ok && variables != nil {
		mediaType, _, e := mime.ParseMediaType(contentType)
		if e == nil && strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
			text, e := debugFormRows(rows, variables)
			return text, contentType, e
		}
	}
	return "", "", api(400, "unsupported_operation", "非JSON请求体须提供字符串示例或URL编码表单行")
}

// URLSearchParams uses the form encoding set: unlike Go QueryEscape it encodes
// '~' and leaves '*' literal. Keep edited rows, duplicate keys, and empty values.
func debugFormRows(rows []any, variables map[string]string) (string, error) {
	parts := []string{}
	escape := func(value string) string {
		return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(value), "~", "%7E"), "%2A", "*")
	}
	invalid := func() error {
		return api(400, "unsupported_operation", "URL编码表单行须包含文本name/value及布尔isDisabled")
	}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return "", invalid()
		}
		if value, exists := row["isDisabled"]; exists {
			disabled, ok := value.(bool)
			if !ok {
				return "", invalid()
			}
			if disabled {
				continue
			}
		}
		name, ok := row["name"].(string)
		if !ok {
			return "", invalid()
		}
		if name == "" {
			continue
		}
		text, ok := row["value"].(string)
		if !ok {
			return "", invalid()
		}
		if variables != nil {
			var e error
			name, e = expandAPIVariables(name, variables)
			if e != nil {
				return "", e
			}
			text, e = expandAPIVariables(text, variables)
			if e != nil {
				return "", e
			}
		}
		parts = append(parts, escape(name)+"="+escape(text))
	}
	return strings.Join(parts, "&"), nil
}

func (p *Platform) collectionDebug(ctx context.Context, id string, body Obj) (Obj, error) {
	resolver := newEnvironmentRuleResolver(nil).withOperation(environmentOperationFromContext(ctx))
	collection, e := p.workspaceGet("api_collections", id)
	if e != nil {
		return nil, e
	}
	project, e := p.projectGetQ(p.DB, p.projectID(), false)
	if e != nil {
		return nil, e
	}
	if project["archived"] == true {
		return nil, api(409, "project_archived", "归档项目不能调试接口")
	}
	document := collectionDocumentWithAuth(collection)
	path, method := str(body["path"]), strings.ToLower(str(body["method"]))
	pathItem := obj(obj(document["paths"])[path])
	operation, ok := pathItem[method].(map[string]any)
	if !ok || !apiMethod(method) {
		return nil, api(404, "not_found", "接口不存在")
	}
	descriptor, e := operationInterfaceProtocol(document, operation)
	if e != nil {
		return nil, e
	}
	if e = requireInterfaceProtocolExecution(descriptor); e != nil {
		return nil, e
	}
	if descriptor.Protocol == "websocket" {
		return nil, api(400, "websocket_debug_required", "WS 接口请通过控制台长连接面板调试，不能作为 HTTP 请求发送")
	}
	targetSource := str(body["target_source"])
	if targetSource != "project_environment" && targetSource != "document_server" && targetSource != "collection_environment" {
		return nil, api(400, "target_source_required", "须明确选择 collection_environment、project_environment 或 document_server")
	}
	if _, exists := body["environment"]; exists {
		return nil, api(400, "retired_environment", "旧集合环境不再参与执行；请显式选择项目环境或文档服务地址")
	}
	environmentID := str(body["environment_id"])
	if targetSource == "project_environment" {
		if validID(environmentID) != nil {
			return nil, api(400, "invalid_environment", "项目环境目标须提供 environment_id")
		}
	} else if _, exists := body["environment_id"]; exists {
		return nil, api(400, "target_conflict", "文档服务地址目标不能同时指定项目环境")
	}
	collectionEnvironmentID := str(body["collection_environment_id"])
	if targetSource == "collection_environment" {
		if validID(collectionEnvironmentID) != nil {
			return nil, api(400, "collection_environment_required", "须指定 collection_environment_id")
		}
	} else if _, present := body["collection_environment_id"]; present {
		return nil, api(400, "target_conflict", "集合环境与旧目标参数互斥")
	}
	variables := map[string]string{}
	if raw, exists := body["variables"]; exists {
		values, ok := raw.(map[string]any)
		if !ok {
			return nil, api(400, "invalid_variables", "variables须为值对象")
		}
		for k, v := range values {
			s, e := environmentVariableText(v)
			if e != nil {
				return nil, api(400, "invalid_variables", "变量值须为文本或包含文本default的对象")
			}
			variables[k] = s
		}
	}
	targetInfo := Obj{"source": targetSource, "collection_revision": collection["revision"]}
	var server string
	if targetSource == "collection_environment" {
		if _, exists := obj(collection["client_state"])[collectionEnvironmentsKey]; !exists {
			parents, err := p.workspaceList("environments")
			if err != nil {
				return nil, err
			}
			obj(collection["client_state"])[collectionEnvironmentsKey] = collectionEnvironmentResources(collection, arr(parents["items"]))
		}
		selected, err := p.resolveCollectionEnvironmentQ(p.DB, collection, collectionEnvironmentID, obj(body["variables"]), true)
		if err != nil {
			return nil, err
		}
		definitions, err := environmentVariables(selected)
		if err != nil {
			return nil, err
		}
		variables, e = resolver.resolve(definitions)
		if e != nil {
			return nil, e
		}
		server = str(selected["base_host"])
		variables["base_host"] = server
		for _, key := range []string{"collection_id", "collection_environment_id", "parent_environment_id", "parent_environment_revision"} {
			if value, ok := selected[key]; ok {
				targetInfo[key] = value
			}
		}
	} else if environmentID != "" {
		selected, e := p.workspaceGet("environments", environmentID)
		if e != nil {
			return nil, e
		}
		selectedVariables, e := mergedCollectionVariables(collection, selected, obj(body["variables"]), resolver)
		if e != nil {
			return nil, e
		}
		variables = selectedVariables
		targetInfo["environment_id"] = environmentID
		targetInfo["environment_revision"] = selected["revision"]
		server = str(selected["base_host"])
		variables["base_host"] = server
	} else {
		variables, e = resolver.resolve(variables)
		if e != nil {
			return nil, e
		}
		server, e = operationServer(document, pathItem, operation)
		if e != nil {
			return nil, e
		}
		server, e = expandAPIVariables(server, variables)
		if e != nil {
			return nil, e
		}
	}
	// Expand environment placeholders before replacing OpenAPI {parameters}.
	// This also avoids matching {name} inside an imported {{name}} placeholder.
	path, e = expandAPIVariables(path, variables)
	if e != nil {
		return nil, e
	}
	query := []string{}
	headers := http.Header{}
	cookies := []string{}
	params := []any{}
	positions := map[string]int{}
	pathParams := arr(pathItem["parameters"])
	resolved := []Obj{}
	operationHeaders := map[string]bool{}
	for index, v := range append(append([]any{}, pathParams...), arr(operation["parameters"])...) {
		param := obj(v)
		if ref := str(param["$ref"]); ref != "" {
			if !strings.HasPrefix(ref, "#/components/parameters/") {
				return nil, api(400, "unsupported_operation", "仅支持本地参数引用")
			}
			name := strings.TrimPrefix(ref, "#/components/parameters/")
			param = obj(obj(obj(document["components"])["parameters"])[name])
			if len(param) == 0 {
				return nil, api(400, "unsupported_operation", "参数引用不存在")
			}
		}
		resolved = append(resolved, param)
		if index >= len(pathParams) && param["in"] == "header" {
			operationHeaders[strings.ToLower(str(param["name"]))] = true
		}
	}
	for index, param := range resolved {
		location := str(param["in"])
		if location == "header" {
			// HTTP names are case-insensitive. Operation-level definitions replace
			// inherited path headers, while repeated fields within one level keep
			// their original order (including imported cURL Cookie/header fields).
			if index < len(pathParams) && operationHeaders[strings.ToLower(str(param["name"]))] {
				continue
			}
			params = append(params, param)
			continue
		}
		key := location + "\x00" + str(param["name"])
		if index, exists := positions[key]; exists && location != "query" {
			params[index] = param
		} else {
			positions[key] = len(params)
			params = append(params, param)
		}
	}
	for _, v := range params {
		param := obj(v)
		if debugParameterDisabled(param) {
			continue
		}
		name := str(param["name"])
		value, e := parameterValue(param)
		if e != nil {
			return nil, e
		}
		value, e = expandAPIVariables(value, variables)
		if e != nil {
			return nil, e
		}
		switch str(param["in"]) {
		case "path":
			path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
		case "query":
			query = append(query, url.QueryEscape(name)+"="+url.QueryEscape(value))
		case "header":
			if !validAPIHeader(name, value) {
				return nil, api(400, "invalid_operation", "请求头名称或值无效")
			}
			if contains([]string{"host", "content-length", "connection", "transfer-encoding", "trailer", "upgrade", "keep-alive", "proxy-connection", "proxy-authorization"}, strings.ToLower(name)) {
				return nil, api(400, "unsupported_operation", "此请求头不能安全转发："+name)
			}
			headers.Add(name, value)
		case "cookie":
			cookies = append(cookies, url.QueryEscape(name)+"="+url.QueryEscape(value))
		default:
			return nil, api(400, "unsupported_operation", "不支持的参数位置")
		}
	}
	if strings.ContainsAny(path, "{}") {
		return nil, api(400, "parameter_value_required", "路径参数尚未填写")
	}
	target, e := proxyTarget(strings.TrimSuffix(server, "/") + path)
	if e != nil {
		return nil, e
	}
	if len(query) > 0 {
		if target.RawQuery != "" {
			query = append([]string{target.RawQuery}, query...)
		}
		target.RawQuery = strings.Join(query, "&")
	}
	content, contentType, e := operationBodyWithVariables(operation, variables)
	if e != nil {
		return nil, e
	}
	content, e = expandAPIVariables(content, variables)
	if e != nil {
		return nil, e
	}
	if len(content) > proxyRequestLimit {
		return nil, api(413, "proxy_request_too_large", "请求体超过6MiB")
	}
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	if len(cookies) > 0 {
		headers.Add("Cookie", strings.Join(cookies, "; "))
	}
	if e = applyDebugSecurity(document, operation, headers, &query, variables); e != nil {
		return nil, e
	}
	if len(query) > 0 {
		target.RawQuery = strings.Join(query, "&")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, strings.ToUpper(method), target.String(), strings.NewReader(content))
	if e != nil {
		return nil, api(400, "invalid_operation", "HTTP请求格式无效")
	}
	request.Header = headers
	stripHopHeaders(request.Header)
	started := time.Now()
	response, raw, e := p.proxyRoundTrip(ctx, request)
	if e != nil {
		return nil, e
	}
	responseHeaders := response.Header.Clone()
	stripHopHeaders(responseHeaders)
	// Headers are JSON data, never applied to the manager's HTTP response.
	return Obj{"project_id": p.projectID(), "collection_id": id, "revision": collection["revision"], "target": targetInfo, "status": response.StatusCode, "headers": responseHeaders, "body": string(raw), "duration_ms": time.Since(started).Milliseconds(), "size": len(raw)}, nil
}

// Scalar stores edited credentials and auth selections outside OpenAPI. Merge
// those documented SDK shapes into a copy for export/debug, without modifying
// either the encrypted document or the caller's graph snapshot.
func collectionDocumentWithAuth(collection Obj) Obj {
	document := clone(obj(collection["document"]))
	auth := obj(obj(obj(collection["client_state"])["auth"])["collection"])
	schemes := obj(obj(document["components"])["securitySchemes"])
	for name, value := range obj(auth["secrets"]) {
		stored := obj(value)
		scheme := obj(schemes[name])
		if len(scheme) == 0 || stored["type"] != scheme["type"] {
			continue
		}
		cleared := map[string]bool{}
		for _, key := range arr(stored["x-scalar-secret-cleared-fields"]) {
			cleared[str(key)] = true
		}
		for key, value := range stored {
			if strings.HasPrefix(key, "x-scalar-secret-") && key != "x-scalar-secret-cleared-fields" {
				if text, ok := value.(string); ok && (text != "" || cleared[key]) {
					scheme[key] = text
				}
			}
		}
		for key := range cleared {
			scheme[key] = ""
		}
		if scheme["type"] == "apiKey" {
			if name, ok := stored["name"].(string); ok {
				scheme["name"] = name
			}
		}
	}
	selected := obj(auth["selected"])
	for path, value := range obj(document["paths"]) {
		entry := obj(value)
		for method, value := range entry {
			if !apiMethod(method) {
				continue
			}
			operation := obj(value)
			choice := obj(obj(obj(selected["path"])[path])[method])
			if len(choice) == 0 {
				choice = obj(selected["document"])
			}
			if len(choice) == 0 {
				continue
			}
			index, ok := integer(choice["selectedIndex"])
			if !ok {
				continue
			}
			schemes := arr(choice["selectedSchemes"])
			if index < 0 {
				operation["security"] = []any{}
			} else if index < int64(len(schemes)) {
				operation["security"] = []any{clone(obj(schemes[index]))}
			}
		}
	}
	return document
}

var apiHeaderNameRE = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func validAPIHeader(name, value string) bool {
	return apiHeaderNameRE.MatchString(name) && !strings.ContainsAny(value, "\r\n\x00")
}
func applyDebugSecurity(document, operation Obj, headers http.Header, query *[]string, variables map[string]string) error {
	security, exists := operation["security"]
	if !exists {
		security = document["security"]
	}
	requirements := arr(security)
	if len(requirements) == 0 || headers.Get("Authorization") != "" {
		return nil
	}
	if len(requirements) != 1 {
		return api(400, "unsupported_operation", "请明确选择单一认证方案")
	}
	schemes := obj(obj(document["components"])["securitySchemes"])
	for name := range obj(requirements[0]) {
		scheme := obj(schemes[name])
		token, e := expandAPIVariables(str(scheme["x-scalar-secret-token"]), variables)
		if e != nil {
			return e
		}
		switch str(scheme["type"]) {
		case "http":
			switch strings.ToLower(str(scheme["scheme"])) {
			case "bearer":
				if token == "" {
					return api(400, "authentication_required", "Bearer认证缺少凭据")
				}
				headers.Set("Authorization", "Bearer "+token)
			case "basic":
				if _, userSet := scheme["x-scalar-secret-username"]; !userSet {
					if _, passSet := scheme["x-scalar-secret-password"]; !passSet {
						return api(400, "authentication_required", "Basic认证缺少配置的用户名和密码")
					}
				}
				user, e := expandAPIVariables(str(scheme["x-scalar-secret-username"]), variables)
				if e != nil {
					return e
				}
				pass, e := expandAPIVariables(str(scheme["x-scalar-secret-password"]), variables)
				if e != nil {
					return e
				}
				headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
			default:
				return api(400, "unsupported_operation", "HTTP认证类型暂不支持")
			}
		case "apiKey":
			if token == "" {
				return api(400, "authentication_required", "API Key缺少凭据")
			}
			switch str(scheme["in"]) {
			case "header":
				key := str(scheme["name"])
				if !validAPIHeader(key, token) {
					return api(400, "invalid_operation", "API Key请求头无效")
				}
				headers.Set(key, token)
			case "query":
				*query = append(*query, url.QueryEscape(str(scheme["name"]))+"="+url.QueryEscape(token))
			default:
				return api(400, "unsupported_operation", "API Key位置暂不支持")
			}
		default:
			return api(400, "unsupported_operation", "认证类型暂不支持，请用Scalar配置显式认证请求头")
		}
	}
	return nil
}

func objectKeys(value Obj) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
