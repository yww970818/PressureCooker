package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

func importError(message string) error       { return api(400, "invalid_import", message) }
func unsupportedImport(message string) error { return api(400, "unsupported_import", message) }

// This is a text lexer, never a shell. Shell operators are rejected and quoted
// substitutions remain literal request text.
func curlWords(content string) ([]string, error) {
	words := []string{}
	var word strings.Builder
	quote := rune(0)
	escaped, started := false, false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	chars := []rune(content)
	for i, c := range chars {
		if escaped {
			if c != '\n' {
				word.WriteRune(c)
				started = true
			}
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			if quote == '"' && i+1 < len(chars) && !strings.ContainsRune("$`\"\\\n", chars[i+1]) {
				word.WriteRune(c)
				started = true
				continue
			}
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			started = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			started = true
			continue
		}
		if unicode.IsSpace(c) {
			if c == '\n' && i < len(chars)-1 && strings.TrimSpace(string(chars[i+1:])) != "" {
				return nil, unsupportedImport("cURL只支持一条命令；多行请用反斜杠续行")
			}
			flush()
			continue
		}
		if strings.ContainsRune(";|&<>`", c) {
			return nil, unsupportedImport("cURL含Shell操作符；请把请求值放入引号，禁止执行命令")
		}
		if c == '$' && i+1 < len(chars) && chars[i+1] == '(' {
			return nil, unsupportedImport("cURL不执行Shell命令替换")
		}
		word.WriteRune(c)
		started = true
	}
	if quote != 0 || escaped {
		return nil, importError("cURL引号或转义未闭合")
	}
	flush()
	if len(words) < 2 || words[0] != "curl" {
		return nil, importError("须提供一条curl命令")
	}
	return words, nil
}
func requestURL(raw string) (string, string, []any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n#") {
		return "", "", nil, importError("请求URL无效或含片段")
	}
	server, rest := "", raw
	if i := strings.Index(raw, "://"); i >= 0 {
		if raw[:i] != "http" && raw[:i] != "https" {
			return "", "", nil, unsupportedImport("仅支持HTTP或HTTPS请求")
		}
		end := strings.IndexAny(raw[i+3:], "/?")
		if end < 0 {
			server = raw
			rest = "/"
		} else {
			end += i + 3
			server = raw[:end]
			rest = raw[end:]
		}
		if strings.Contains(server, "@") {
			return "", "", nil, unsupportedImport("URL内嵌凭据请改为Basic认证")
		}
	} else if strings.HasPrefix(raw, "{{") {
		end := strings.Index(raw, "}}")
		if end < 0 {
			return "", "", nil, importError("URL变量未闭合")
		}
		server = raw[:end+2]
		rest = raw[end+2:]
	} else {
		return "", "", nil, importError("请求URL须包含HTTP地址或{{baseUrl}}变量")
	}
	if strings.HasPrefix(rest, "?") {
		rest = "/" + rest
	}
	path, query, _ := strings.Cut(rest, "?")
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "", "", nil, importError("请求路径必须以/开头")
	}
	params := []any{}
	if query != "" {
		for _, part := range strings.Split(query, "&") {
			key, value, _ := strings.Cut(part, "=")
			k, e := url.QueryUnescape(key)
			if e != nil {
				return "", "", nil, importError("查询参数编码无效")
			}
			v, e := url.QueryUnescape(value)
			if e != nil {
				return "", "", nil, importError("查询参数编码无效")
			}
			params = append(params, Obj{"name": k, "in": "query", "schema": Obj{"type": "string"}, "example": v})
		}
	}
	return server, path, params, nil
}
func addImportHeader(operation Obj, name, value string) {
	operation["parameters"] = append(arr(operation["parameters"]), Obj{"name": name, "in": "header", "schema": Obj{"type": "string"}, "example": value})
}
func rawImportBody(operation Obj, body, contentType string) {
	media := Obj{"schema": Obj{"type": "string"}, "example": body}
	if strings.Contains(strings.ToLower(contentType), "json") {
		var value any
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil && json.Valid([]byte(body)) {
			delete(media, "example")
			media["examples"] = Obj{"default": Obj{"value": value, "dataValue": value, "serializedValue": body}}
			delete(media, "schema")
		}
	}
	operation["requestBody"] = Obj{"content": Obj{contentType: media}}
}
func emptyImportDocument(name string) Obj {
	return Obj{"openapi": "3.1.0", "info": Obj{"title": name, "version": "1.0.0"}, "paths": Obj{}}
}
func curlDocument(content, name string) (Obj, Obj, error) {
	words, e := curlWords(content)
	if e != nil {
		return nil, nil, e
	}
	method, raw, body, contentType := "", "", "", ""
	bodySet, getData := false, false
	headers := [][2]string{}
	user, bearer := "", ""
	for i := 1; i < len(words); i++ {
		arg := words[i]
		flag, value, hasValue := arg, "", false
		if strings.HasPrefix(arg, "--") {
			flag, value, hasValue = strings.Cut(arg, "=")
		} else if len(arg) > 2 && contains([]string{"-X", "-H", "-d", "-u", "-A", "-b"}, arg[:2]) {
			flag, value, hasValue = arg[:2], arg[2:], true
		}
		take := func() (string, error) {
			if hasValue {
				return value, nil
			}
			i++
			if i >= len(words) {
				return "", importError("cURL参数缺少值：" + flag)
			}
			return words[i], nil
		}
		switch flag {
		case "-X", "--request":
			method, e = take()
			method = strings.ToLower(method)
		case "-H", "--header":
			var h string
			h, e = take()
			if e == nil {
				key, value, ok := strings.Cut(h, ":")
				if !ok || strings.TrimSpace(key) == "" {
					return nil, nil, importError("cURL请求头须包含冒号")
				}
				headers = append(headers, [2]string{strings.TrimSpace(key), strings.TrimSpace(value)})
			}
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii", "--data-urlencode":
			var value string
			value, e = take()
			if strings.HasPrefix(value, "@") && flag != "--data-raw" {
				return nil, nil, unsupportedImport("不读取cURL本地文件，请粘贴请求体")
			}
			if flag == "--data-urlencode" {
				key, v, ok := strings.Cut(value, "=")
				if !ok {
					return nil, nil, unsupportedImport("data-urlencode须使用name=value")
				}
				value = url.QueryEscape(key) + "=" + url.QueryEscape(v)
			}
			if bodySet {
				body += "&"
			}
			body += value
			bodySet = true
		case "--json":
			body, e = take()
			bodySet = true
			contentType = "application/json"
			headers = append(headers, [2]string{"Accept", "application/json"})
		case "-u", "--user":
			user, e = take()
		case "--oauth2-bearer":
			bearer, e = take()
		case "-A", "--user-agent":
			var v string
			v, e = take()
			headers = append(headers, [2]string{"User-Agent", v})
		case "-b", "--cookie":
			var v string
			v, e = take()
			if !strings.Contains(v, "=") {
				return nil, nil, unsupportedImport("不读取cURL Cookie文件")
			}
			headers = append(headers, [2]string{"Cookie", v})
		case "--url":
			raw, e = take()
		case "-G", "--get":
			getData = true
		case "-I", "--head":
			method = "head"
		case "--compressed", "-s", "--silent", "-S", "--show-error": // Transfer presentation has no request semantics.
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, nil, unsupportedImport("不支持的cURL参数：" + flag)
			}
			if raw != "" {
				return nil, nil, unsupportedImport("每次仅能导入一个cURL请求")
			}
			raw = arg
		}
		if e != nil {
			return nil, nil, e
		}
	}
	if getData && bodySet {
		sep := "?"
		if strings.Contains(raw, "?") {
			sep = "&"
		}
		raw += sep + body
		bodySet = false
	}
	if method == "" {
		method = "get"
		if bodySet {
			method = "post"
		}
	}
	if !apiMethod(method) {
		return nil, nil, unsupportedImport("请求方法不支持")
	}
	server, path, params, e := requestURL(raw)
	if e != nil {
		return nil, nil, e
	}
	operation := Obj{"summary": name, "parameters": params, "responses": Obj{"default": Obj{"description": "响应"}}}
	for _, h := range headers {
		if strings.EqualFold(h[0], "Content-Type") {
			contentType = h[1]
		}
		addImportHeader(operation, h[0], h[1])
	}
	if user != "" && bearer != "" {
		return nil, nil, unsupportedImport("同一请求不能同时使用Basic和Bearer认证")
	}
	document := emptyImportDocument(name)
	if user != "" {
		username, password, ok := strings.Cut(user, ":")
		if !ok {
			return nil, nil, unsupportedImport("Basic认证须提供username:password，不能交互输入密码")
		}
		if apiVariableRE.MatchString(user) {
			importBasicScheme(document, operation, username, password)
		} else {
			addImportHeader(operation, "Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user)))
		}
	}
	if bearer != "" {
		addImportHeader(operation, "Authorization", "Bearer "+bearer)
	}
	if bodySet {
		if contentType == "" {
			contentType = "application/x-www-form-urlencoded"
		}
		rawImportBody(operation, body, contentType)
	}
	document["servers"] = []any{Obj{"url": server}}
	obj(document["paths"])[path] = Obj{method: operation}
	return document, Obj{}, nil
}

func postmanPairs(value any) ([][2]string, error) {
	out := [][2]string{}
	if value != nil {
		if _, ok := value.([]any); !ok {
			return nil, unsupportedImport("Postman参数列表须为数组，未转换其他结构")
		}
	}
	for _, v := range arr(value) {
		m := obj(v)
		if m["disabled"] == true {
			continue
		}
		key, ok := m["key"].(string)
		if !ok {
			return nil, importError("Postman参数缺少key")
		}
		value, ok := m["value"].(string)
		if !ok && m["value"] != nil {
			return nil, unsupportedImport("Postman参数值须为文本")
		}
		if m["type"] == "file" {
			return nil, unsupportedImport("文件参数需要手工配置，未导入")
		}
		out = append(out, [2]string{key, value})
	}
	return out, nil
}
func postmanAuth(document, operation Obj, auth Obj) error {
	kind := str(auth["type"])
	if kind == "" {
		return nil
	}
	if kind == "noauth" {
		operation["security"] = []any{}
		return nil
	}
	pairs, e := postmanPairs(auth[kind])
	if e != nil {
		return e
	}
	values := map[string]string{}
	for _, pair := range pairs {
		values[pair[0]] = pair[1]
	}
	switch kind {
	case "basic":
		if apiVariableRE.MatchString(values["username"] + values["password"]) {
			importBasicScheme(document, operation, values["username"], values["password"])
		} else {
			addImportHeader(operation, "Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(values["username"]+":"+values["password"])))
		}
	case "bearer":
		addImportHeader(operation, "Authorization", "Bearer "+values["token"])
	case "apikey":
		location := values["in"]
		if location == "" {
			location = "header"
		}
		if location != "query" && location != "header" {
			return unsupportedImport("API Key位置仅支持header/query")
		}
		operation["parameters"] = append(arr(operation["parameters"]), Obj{"in": location, "name": values["key"], "schema": Obj{"type": "string"}, "example": values["value"]})
	default:
		return unsupportedImport("暂不支持Postman认证类型：" + kind + "，请转换为显式请求头")
	}
	return nil
}
func postmanDocument(source Obj, name string) (Obj, Obj, error) {
	schema := str(obj(source["info"])["schema"])
	if !strings.Contains(schema, "schema.getpostman.com/json/collection/v2.") {
		return nil, nil, importError("仅支持Postman Collection v2或v2.1")
	}
	if name == "" {
		name = str(obj(source["info"])["name"])
	}
	if name == "" {
		name = "导入集合"
	}
	document := emptyImportDocument(name)
	document["x-postman-source"] = clone(source)
	envPairs, e := postmanPairs(source["variable"])
	if e != nil {
		return nil, nil, e
	}
	variables := []any{}
	for _, pair := range envPairs {
		variables = append(variables, Obj{"name": pair[0], "value": pair[1]})
	}
	client := Obj{}
	if len(variables) > 0 {
		document["x-scalar-environments"] = Obj{"imported": Obj{"color": "#008889", "variables": variables}}
		client["active_environment"] = "imported"
	}
	if environments, ok := source["x-jmeter-scalar-environments"].(map[string]any); ok {
		document["x-scalar-environments"] = clone(environments)
	}
	if state, ok := source["x-jmeter-client-state"].(map[string]any); ok {
		client = clone(state)
	}
	count := 0
	var walk func([]any, Obj, []any) error
	walk = func(items []any, inheritedAuth Obj, folders []any) error {
		for _, v := range items {
			item := obj(v)
			if len(arr(item["event"])) > 0 {
				return unsupportedImport("Postman前置/测试脚本不能转换为HTTP请求，请先移除脚本")
			}
			if nested, ok := item["item"].([]any); ok {
				auth := inheritedAuth
				if a, ok := item["auth"].(map[string]any); ok {
					auth = a
				}
				if e := walk(nested, auth, append(append([]any{}, folders...), str(item["name"]))); e != nil {
					return e
				}
				continue
			}
			request, ok := item["request"].(map[string]any)
			if !ok {
				return unsupportedImport("Postman请求必须使用JSON请求对象")
			}
			if request["certificate"] != nil || request["proxy"] != nil || len(obj(item["protocolProfileBehavior"])) > 0 || len(obj(request["protocolProfileBehavior"])) > 0 {
				return unsupportedImport("Postman证书或代理配置不能自动转换")
			}
			method := strings.ToLower(str(request["method"]))
			if !apiMethod(method) {
				return unsupportedImport("Postman请求方法不支持")
			}
			raw, ok := request["url"].(string)
			urlObject := obj(request["url"])
			if !ok {
				raw = str(urlObject["raw"])
			}
			if raw == "" {
				return importError("Postman URL缺少raw字段")
			}
			server, path, params, e := requestURL(raw)
			if e != nil {
				return e
			}
			if query, ok := urlObject["query"].([]any); ok {
				params = []any{}
				pairs, e := postmanPairs(query)
				if e != nil {
					return e
				}
				for _, pair := range pairs {
					params = append(params, Obj{"in": "query", "name": pair[0], "schema": Obj{"type": "string"}, "example": pair[1]})
				}
			}
			// Colon path variables map to OpenAPI path parameters while preserving values.
			pathValues, e := postmanPairs(urlObject["variable"])
			if e != nil {
				return e
			}
			for _, pair := range pathValues {
				path = strings.ReplaceAll(path, ":"+pair[0], "{"+pair[0]+"}")
				params = append(params, Obj{"in": "path", "name": pair[0], "required": true, "schema": Obj{"type": "string"}, "example": pair[1]})
			}
			operation := Obj{"summary": str(item["name"]), "parameters": params, "servers": []any{Obj{"url": server}}, "responses": Obj{"default": Obj{"description": "响应"}}, "x-postman-request": clone(request)}
			if len(folders) > 0 {
				operation["tags"] = folders
			}
			if desc, ok := request["description"].(string); ok {
				operation["description"] = desc
			}
			headers, e := postmanPairs(request["header"])
			if e != nil {
				return e
			}
			contentType := ""
			for _, pair := range headers {
				if strings.EqualFold(pair[0], "Content-Type") {
					contentType = pair[1]
				}
				addImportHeader(operation, pair[0], pair[1])
			}
			auth := inheritedAuth
			if a, ok := request["auth"].(map[string]any); ok {
				auth = a
			}
			if e = postmanAuth(document, operation, auth); e != nil {
				return e
			}
			body := obj(request["body"])
			if body["disabled"] != true && len(body) > 0 {
				switch str(body["mode"]) {
				case "raw":
					if contentType == "" {
						contentType = "text/plain"
						if obj(obj(body["options"])["raw"])["language"] == "json" {
							contentType = "application/json"
						}
					}
					text, ok := body["raw"].(string)
					if !ok {
						return importError("Postman raw请求体须为文本")
					}
					rawImportBody(operation, text, contentType)
				case "urlencoded":
					pairs, e := postmanPairs(body["urlencoded"])
					if e != nil {
						return e
					}
					parts := []string{}
					for _, pair := range pairs {
						parts = append(parts, url.QueryEscape(pair[0])+"="+url.QueryEscape(pair[1]))
					}
					rawImportBody(operation, strings.Join(parts, "&"), "application/x-www-form-urlencoded")
				default:
					return unsupportedImport("Postman请求体模式暂不支持：" + str(body["mode"]))
				}
			}
			paths := obj(document["paths"])
			entry := obj(paths[path])
			if len(entry) == 0 {
				entry = Obj{}
				paths[path] = entry
			}
			if entry[method] != nil {
				return unsupportedImport("多个请求使用相同路径和方法，无法无损转换：" + method + " " + path)
			}
			entry[method] = operation
			count++
		}
		return nil
	}
	if len(arr(source["event"])) > 0 {
		return nil, nil, unsupportedImport("Postman集合脚本不能自动转换")
	}
	if e = walk(arr(source["item"]), obj(source["auth"]), nil); e != nil {
		return nil, nil, e
	}
	if count == 0 {
		return nil, nil, importError("Postman集合没有HTTP请求")
	}
	return document, client, nil
}

func parseCollectionImport(body Obj) (Obj, error) {
	format := str(body["format"])
	if format == "" {
		format = "auto"
	}
	if !contains([]string{"auto", "curl", "postman", "apifox", "openapi"}, format) {
		return nil, importError("不支持的导入格式")
	}
	content, ok := body["content"].(string)
	if !ok {
		if value, ok := body["content"].(map[string]any); ok {
			content = string(encoded(value))
		} else {
			return nil, importError("导入内容须为文本或JSON对象")
		}
	}
	if len(content) > workspaceLimit {
		return nil, api(413, "workspace_too_large", "导入内容超过5MiB")
	}
	name := str(body["name"])
	var document, client Obj
	var e error
	if format == "curl" || (format == "auto" && strings.HasPrefix(strings.TrimSpace(content), "curl ")) {
		if name == "" {
			name = "导入cURL请求"
		}
		document, client, e = curlDocument(content, name)
	} else {
		source, err := decode([]byte(content))
		if err != nil || source == nil {
			return nil, importError("导入内容须为有效JSON对象")
		}
		if source["swagger"] != nil {
			return nil, unsupportedImport("此导入接口需要OpenAPI 3.0/3.1，请先将Swagger转换为OpenAPI 3")
		}
		if (format == "auto" || format == "openapi" || format == "apifox") && source["openapi"] != nil {
			version := str(source["openapi"])
			if !strings.HasPrefix(version, "3.0.") && !strings.HasPrefix(version, "3.1.") {
				return nil, unsupportedImport("仅支持OpenAPI 3.0/3.1")
			}
			if _, ok := source["paths"].(map[string]any); !ok {
				return nil, importError("OpenAPI须包含paths对象")
			}
			document = clone(source)
			delete(document, "x-jmeter-client-state")
			client = Obj{}
			if state, ok := source["x-jmeter-client-state"].(map[string]any); ok {
				client = clone(state)
			}
			if name == "" {
				name = str(obj(source["info"])["title"])
			}
		} else if format == "auto" || format == "postman" || format == "apifox" {
			document, client, e = postmanDocument(source, name)
			if e != nil && (format == "apifox" || source["apifoxProject"] != nil || source["apiCollection"] != nil || source["apifoxProjectId"] != nil) {
				return nil, unsupportedImport("暂不支持Apifox原生项目结构，请从Apifox导出OpenAPI或Postman v2.1后导入")
			}
		} else {
			return nil, importError("导入内容与所选格式不匹配")
		}
	}
	if e != nil {
		return nil, e
	}
	if raw, exists := document["x-scalar-environments"]; exists {
		environments, ok := raw.(map[string]any)
		if !ok {
			return nil, api(400, "invalid_environments", "environments须为对象")
		}
		normalized, e := canonicalScalarEnvironments(environments)
		if e != nil {
			return nil, e
		}
		document["x-scalar-environments"] = normalized
	}
	if name == "" {
		name = str(obj(document["info"])["title"])
	}
	if name == "" {
		name = "导入集合"
	}
	return Obj{"name": name, "description": str(body["description"]), "document": document, "client_state": client}, nil
}

func exportPostman(collection Obj) (Obj, error) {
	document := collectionDocumentWithAuth(collection)
	items := []any{}
	for _, path := range objectKeys(obj(document["paths"])) {
		entry := obj(obj(document["paths"])[path])
		for _, method := range objectKeys(entry) {
			if !apiMethod(method) {
				continue
			}
			operation := obj(entry[method])
			descriptor, e := operationInterfaceProtocol(document, operation)
			if e != nil {
				return nil, e
			}
			if e = requireInterfaceProtocolExecution(descriptor); e != nil {
				return nil, e
			}
			if descriptor.Protocol == "websocket" {
				return nil, unsupportedImport("WS 调试配置请导出 JSON 或 YAML；不能转换为 Postman HTTP 请求")
			}
			server, e := operationServer(document, entry, operation)
			if e != nil {
				return nil, e
			}
			request := Obj{"method": strings.ToUpper(method), "header": []any{}}
			requestPath := path
			pathVariables := []any{}
			query := []any{}
			params := append(append([]any{}, arr(entry["parameters"])...), arr(operation["parameters"])...)
			for _, v := range params {
				param := obj(v)
				value, e := parameterValue(param)
				if e != nil {
					return nil, e
				}
				switch str(param["in"]) {
				case "header":
					request["header"] = append(arr(request["header"]), Obj{"key": param["name"], "value": value})
				case "query":
					query = append(query, Obj{"key": param["name"], "value": value})
				case "path":
					requestPath = strings.ReplaceAll(requestPath, "{"+str(param["name"])+"}", ":"+str(param["name"]))
					pathVariables = append(pathVariables, Obj{"key": param["name"], "value": value})
				case "cookie":
					return nil, unsupportedImport("Cookie参数暂不能导出为Postman，请改为Cookie请求头")
				}
			}
			body, contentType, e := operationBody(operation)
			if e != nil {
				return nil, e
			}
			if contentType != "" {
				request["body"] = Obj{"mode": "raw", "raw": body}
				request["header"] = append(arr(request["header"]), Obj{"key": "Content-Type", "value": contentType})
			}
			raw := strings.TrimSuffix(server, "/") + requestPath
			parts := []string{}
			for _, q := range query {
				m := obj(q)
				parts = append(parts, url.QueryEscape(str(m["key"]))+"="+url.QueryEscape(str(m["value"])))
			}
			if len(parts) > 0 {
				raw += "?" + strings.Join(parts, "&")
			}
			request["url"] = Obj{"raw": raw, "query": query, "variable": pathVariables}
			if auth, e := postmanSecurity(document, operation); e != nil {
				return nil, e
			} else if len(auth) > 0 {
				request["auth"] = auth
			}
			name := str(operation["summary"])
			if name == "" {
				name = strings.ToUpper(method) + " " + path
			}
			items = append(items, Obj{"name": name, "request": request})
		}
	}
	result := Obj{"x-jmeter-scalar-environments": document["x-scalar-environments"], "x-jmeter-client-state": clone(obj(collection["client_state"])), "info": Obj{"name": collection["name"], "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"}, "item": items}
	environment := selectedEnvironment(document, obj(collection["client_state"]), "")
	variables, e := environmentVariables(environment)
	if e != nil {
		return nil, e
	}
	pairs := []any{}
	for _, key := range sortedKeys(variables) {
		pairs = append(pairs, Obj{"key": key, "value": variables[key], "type": "string"})
	}
	if len(pairs) > 0 {
		result["variable"] = pairs
	}
	return result, nil
}

func postmanSecurity(document, operation Obj) (Obj, error) {
	security, exists := operation["security"]
	if !exists {
		security = document["security"]
	}
	requirements := arr(security)
	if len(requirements) == 0 {
		return nil, nil
	}
	if len(requirements) != 1 || len(obj(requirements[0])) != 1 {
		return nil, unsupportedImport("组合或候选认证暂不能无损导出Postman")
	}
	schemes := obj(obj(document["components"])["securitySchemes"])
	for name := range obj(requirements[0]) {
		scheme := obj(schemes[name])
		switch str(scheme["type"]) {
		case "http":
			switch strings.ToLower(str(scheme["scheme"])) {
			case "basic":
				return Obj{"type": "basic", "basic": []any{Obj{"key": "username", "value": str(scheme["x-scalar-secret-username"])}, Obj{"key": "password", "value": str(scheme["x-scalar-secret-password"])}}}, nil
			case "bearer":
				return Obj{"type": "bearer", "bearer": []any{Obj{"key": "token", "value": str(scheme["x-scalar-secret-token"])}}}, nil
			}
		case "apiKey":
			return Obj{"type": "apikey", "apikey": []any{Obj{"key": "key", "value": scheme["name"]}, Obj{"key": "in", "value": scheme["in"]}, Obj{"key": "value", "value": str(scheme["x-scalar-secret-token"])}}}, nil
		}
		return nil, unsupportedImport(fmt.Sprint("暂不能导出认证：", name))
	}
	return nil, nil
}

func importBasicScheme(document, operation Obj, username, password string) {
	components := obj(document["components"])
	if len(components) == 0 {
		components = Obj{}
		document["components"] = components
	}
	schemes := obj(components["securitySchemes"])
	if len(schemes) == 0 {
		schemes = Obj{}
		components["securitySchemes"] = schemes
	}
	name := "imported-basic-" + digest([]byte(username + "\x00" + password))[:12]
	schemes[name] = Obj{"type": "http", "scheme": "basic", "x-scalar-secret-username": username, "x-scalar-secret-password": password}
	operation["security"] = []any{Obj{name: []any{}}}
}
