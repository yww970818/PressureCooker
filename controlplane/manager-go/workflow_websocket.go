package main

import (
	_ "embed"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
)

const websocketVersionProp = "pc.websocket.version"
const websocketConfigProp = "pc.websocket.config"
const websocketAuthoredProp = "pc.websocket.authored"

// JMeter's native JSR223 runtime executes this program on the existing JDK 21
// workers. Configuration is encoded separately to keep script compilation cached
// and prevent request/environment values from being interpreted as Groovy code.
//
//go:embed workflow_ws_runtime.groovy
var workflowWebSocketRuntime string

var websocketTemplate = regexp.MustCompile(`\{\{\s*([\p{L}\p{N}_.-]+)\s*\}\}`)
var websocketVariable = regexp.MustCompile(`\$\{([\p{L}\p{N}_.-]+)\}`)

func websocketComponent(e *xmlElement) bool {
	return e != nil && e.Name.Local == "JSR223Sampler" && e.propText(websocketVersionProp) == "1"
}
func websocketAutoVariable(name string) bool {
	return contains([]string{"ser", "now", "message_id", "last_ser", "socket_id"}, name)
}
func websocketConfigText(e *xmlElement) string {
	data, err := base64.StdEncoding.DecodeString(e.text())
	if err != nil {
		return ""
	}
	return websocketTemplate.ReplaceAllString(string(data), "$${$1}")
}
func websocketExecutableText(e *xmlElement) string {
	c, err := decode([]byte(websocketConfigText(e)))
	if err != nil {
		return ""
	}
	keys := []string{"action", "connectionId", "timeoutMs"}
	switch str(c["action"]) {
	case "connect":
		keys = append(keys, "url", "protocols", "headers")
		if boolean(c["socketio"]) {
			keys = append(keys, "handshakeUrl")
		}
		if boolean(c["heartbeatEnabled"]) {
			keys = append(keys, "heartbeatIntervalMs", "heartbeatMessage")
		}
	case "send":
		keys = append(keys, "message")
	case "request":
		keys = append(keys, "message", "receiveMode", "matchText", "matchJson")
	case "read":
		keys = append(keys, "receiveMode", "matchText", "matchJson")
	}
	if str(c["receiveMode"]) != "json" {
		delete(c, "matchJson")
	}
	if !contains([]string{"contains", "regex"}, str(c["receiveMode"])) {
		delete(c, "matchText")
	}
	selected := Obj{}
	for _, k := range keys {
		if v, ok := c[k]; ok {
			selected[k] = v
		}
	}
	if rows, ok := selected["headers"]; ok {
		active := []any{}
		for _, row := range arr(rows) {
			if obj(row)["enabled"] != false {
				active = append(active, row)
			}
		}
		selected["headers"] = active
	}
	return string(encoded(selected))
}
func websocketConfigValidate(config Obj) error {
	if !contains([]string{"connect", "send", "read", "request", "close"}, str(config["action"])) || !contains([]string{"any", "contains", "regex", "json"}, str(config["receiveMode"])) {
		return api(400, "invalid_config", "WS 操作或接收匹配方式无效")
	}
	if strings.TrimSpace(str(config["connectionId"])) == "" || len(str(config["connectionId"])) > 128 {
		return api(400, "invalid_config", "WS 连接名称须为 1 至 128 个字符")
	}
	for _, key := range []string{"timeoutMs", "heartbeatIntervalMs"} {
		maximum := 120000
		if key == "heartbeatIntervalMs" {
			if str(config["action"]) != "connect" || !boolean(config["heartbeatEnabled"]) {
				continue
			}
			maximum = 300000
		}
		s := str(config[key])
		if strings.Contains(s, "${") || strings.Contains(s, "{{") {
			continue
		}
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > maximum {
			return api(400, "invalid_config", "WS 超时须为 1 至 120000 毫秒，心跳间隔须为 1 至 300000 毫秒；也可使用变量表达式")
		}
	}
	return nil
}
func patchWebSocketComponent(e *xmlElement, node *workflowNode) {
	e.setProp("stringProp", "scriptLanguage", "groovy")
	e.setProp("stringProp", "cacheKey", "true")
	e.setProp("stringProp", "filename", "")
	e.setProp("stringProp", "parameters", "")
	e.setProp("stringProp", "script", workflowWebSocketRuntime)
	e.setProp("stringProp", websocketVersionProp, "1")
	data := base64.StdEncoding.EncodeToString(encoded(node.Config))
	e.setProp("stringProp", websocketConfigProp, data)
	e.setProp("stringProp", websocketAuthoredProp, data)
}
func readWebSocketConfig(e *xmlElement) Obj {
	out, _ := decodeWebSocketConfig(e)
	return out
}

// An existing declaration is authored data, not an invitation to create a new
// default connection when decoding fails. Callers validate it before patching.
func decodeWebSocketConfig(e *xmlElement) (Obj, error) {
	invalid := func() (Obj, error) {
		return nil, api(400, "invalid_websocket_config", "WebSocket配置声明损坏，不能使用默认值替换；请修复原始JMX配置")
	}
	s := e.propText(websocketAuthoredProp)
	if e.prop(websocketAuthoredProp) == nil {
		s = e.propText(websocketConfigProp)
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return invalid()
	}
	c, err := decode(data)
	if err != nil || c == nil {
		return invalid()
	}
	return componentConfig("WebSocketSampler", c)
}
