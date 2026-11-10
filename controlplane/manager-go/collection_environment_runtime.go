package main

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var collectionRuntimeReferenceRE = regexp.MustCompile(`\$\{__P\(jmeter\.env\.api\.[a-f0-9]{16}\.[a-f0-9]{16},\)\}`)

func (p *Platform) collectionResourceVersion(q queryer, ref Obj) (Obj, error) {
	id := str(ref["collection_id"])
	revision, ok := integer(ref["revision"])
	if validID(id) != nil || !ok || revision < 1 || validID(str(ref["collection_environment_id"])) != nil {
		return nil, api(400, "invalid_collection_environment_reference", "集合环境引用须包含集合、精确版本及环境ID")
	}
	var projectID, payload string
	if e := q.QueryRow("SELECT project_id FROM api_collections WHERE id=?", id).Scan(&projectID); e != nil || projectID != p.projectID() {
		return nil, api(400, "invalid_collection_environment_reference", "引用集合不存在或不属于当前项目")
	}
	if e := q.QueryRow("SELECT payload FROM api_collection_versions WHERE object_id=? AND revision=?", id, revision).Scan(&payload); e != nil {
		return nil, api(400, "invalid_collection_environment_reference", "引用集合版本不存在")
	}
	collection, e := p.openWorkspace("api_collections", id, revision, payload)
	if e != nil {
		return nil, e
	}
	if _, e = collectionEnvironmentResource(collection, str(ref["collection_environment_id"])); e != nil {
		return nil, e
	}
	return collection, nil
}
func (p *Platform) validateCollectionEnvironmentReferences(q queryer, graph Obj) error {
	cache := map[string]Obj{}
	for _, raw := range arr(graph["nodes"]) {
		node := obj(raw)
		props := obj(node["properties"])
		value, present := props["collection_environment_reference"]
		if !present || value == nil {
			continue
		}
		ref, ok := value.(map[string]any)
		if !ok || (props["component"] != "HTTPSamplerProxy" && props["component"] != "WebSocketSampler") {
			return api(400, "invalid_collection_environment_reference", "仅HTTP/WS节点可绑定集合环境")
		}
		key := string(encoded(ref))
		collection := cache[key]
		if collection == nil {
			var e error
			collection, e = p.collectionResourceVersion(q, ref)
			if e != nil {
				return e
			}
			cache[key] = collection
		}
		resource, _ := collectionEnvironmentResource(collection, str(ref["collection_environment_id"]))
		names := []any{}
		for _, row := range arr(resource["variables"]) {
			names = append(names, obj(row)["name"])
		}
		props["api_environment_variables"] = names
	}
	return nil
}
func (p *Platform) collectionResourceRuntime(q queryer, binding Obj, resolver environmentRuleResolver, defaults ...Obj) (Obj, error) {
	ref := obj(binding["environment_source"])
	nodeID := str(binding["node_id"])
	collection, e := p.collectionResourceVersion(q, ref)
	if e != nil {
		return nil, e
	}
	explicit := Obj{}
	if len(defaults) > 0 {
		for key, value := range defaults[0] {
			if key != "base_host" && !reservedRuntimeProperty(key) {
				explicit[key] = value
			}
		}
	}
	effective, e := p.resolveCollectionEnvironmentQ(q, collection, str(ref["collection_environment_id"]), explicit, false)
	if e != nil {
		return nil, api(400, "invalid_collection_environment", "节点 "+nodeID+" 的集合环境或父环境不可用")
	}
	definitions, e := environmentVariables(effective)
	if e != nil {
		return nil, e
	}
	variables, e := resolver.resolve(definitions)
	if e != nil {
		return nil, e
	}
	address, e := parseEnvironmentBase(str(effective["base_host"]))
	if e != nil {
		return nil, e
	}
	address["ws_base_host"] = strings.Replace(strings.Replace(str(address["base_host"]), "https://", "wss://", 1), "http://", "ws://", 1)
	properties := Obj{}
	traces := []any{}
	sources := Obj{}
	appendTrace := func(name, key, value string) {
		trace := Obj{"node_id": nodeID, "collection_id": ref["collection_id"], "revision": ref["revision"], "collection_environment_id": ref["collection_environment_id"], "name": name, "property": key, "value": value}
		for _, field := range []string{"parent_environment_id", "parent_environment_revision"} {
			if value, present := effective[field]; present {
				trace[field] = value
			}
		}
		if kind, _, _ := parseEnvironmentRandomRule(definitions[name]); kind != "" {
			trace["generation_rule"] = definitions[name]
		}
		traces = append(traces, trace)
		properties[key] = value
		sources[key] = name
	}
	for _, key := range collectionAddressKeys {
		appendTrace(key, apiEnvironmentAddressProperty(nodeID, key), str(address[key]))
	}
	for _, raw := range arr(binding["names"]) {
		name := str(raw)
		value, present := variables[name]
		if !present {
			return nil, api(400, "environment_variable_required", fmt.Sprintf("节点 %s 的集合环境缺少变量：%s", nodeID, name))
		}
		appendTrace(name, apiEnvironmentProperty(nodeID, name), value)
	}
	rows := []any{}
	for _, name := range sortedKeys(variables) {
		rows = append(rows, Obj{"name": name, "value": variables[name]})
	}
	effective["variables"] = rows
	effective["node_id"] = nodeID
	effective["property_sources"] = sources
	return Obj{"properties": properties, "api_overrides": traces, "environment": effective}, nil
}

// Only native components with inspectable static configuration can prove there
// is no remaining task project-environment dependency. Unknown extensions keep
// the conservative requirement even when their source contains no obvious URL.
func requiresProjectEnvironment(content string, bindings []any) bool {
	if len(bindings) == 0 {
		return true
	}
	roots, e := parseXMLFragment(content)
	if e != nil {
		return true
	}
	targets := 0
	required := false
	var walk func(*xmlElement)
	walk = func(node *xmlElement) {
		if node.attr("enabled") == "false" {
			return
		}
		if node.Name.Local == "HTTPSamplerProxy" || websocketComponent(node) {
			targets++
			if len(obj(collectionBindingMetadata(node)["environment_source"])) == 0 {
				required = true
			}
		}
		// User scripts and opaque plugin components may access global environment
		// state programmatically; they cannot be proven independent from XML alone.
		if node.attr("testclass") != "" && componentGUI[node.Name.Local] == "" && !genericNative[node.Name.Local] && !websocketComponent(node) {
			required = true
		}
		if (strings.Contains(node.Name.Local, "JSR223") && !websocketComponent(node)) || strings.Contains(node.Name.Local, "BeanShell") || strings.Contains(node.Name.Local, ".") {
			required = true
		}
		if websocketComponent(node) {
			text := collectionRuntimeReferenceRE.ReplaceAllString(executableXMLText(node), "")
			if strings.Contains(text, "${__P(jmeter.env.") {
				required = true
			}
			return
		}
		if node.Name.Local == "stringProp" && node.attr("name") != apiEnvironmentProp {
			text := collectionRuntimeReferenceRE.ReplaceAllString(node.text(), "")
			text = argumentGenerationReferenceRE.ReplaceAllString(text, "")
			if strings.Contains(text, "${__P(jmeter.env.") || strings.Contains(text, "${base_host}") {
				required = true
			}
		}
		for _, child := range node.children() {
			// Compiler-authored global arguments do not imply a dependency unless an
			// executable component uses their names; metadata names are checked below.
			if node.Name.Local == "TestPlan" && (child.attr("name") == "TestPlan.user_defined_variables" || child.attr("name") == environmentBindingProp || child.attr("name") == environmentVariablesProp) {
				continue
			}
			walk(child)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	_, names := workflowEnvironmentMetadata(content)
	if len(names) > 0 {
		required = true
	}
	return required || targets == 0
}

func restoreFrozenCollectionProperties(source, snapshot Obj) Obj {
	properties := Obj{}
	snapshot["collection_environments"] = clone(Obj{"v": source["collection_environments"]})["v"]
	snapshot["api_overrides"] = clone(Obj{"v": source["api_overrides"]})["v"]
	for _, raw := range arr(source["api_overrides"]) {
		row := obj(raw)
		properties[str(row["property"])] = row["value"]
	}
	return properties
}
func redactCollectionEnvironmentSnapshots(snapshot Obj) {
	for _, raw := range arr(snapshot["collection_environments"]) {
		for _, value := range arr(obj(raw)["variables"]) {
			pair := obj(value)
			if sensitiveParameterValue(str(pair["name"]), pair["value"]) {
				pair["value"] = "***"
			}
		}
	}
}
func collectionEnvironmentParameterSnapshots(snapshot Obj, reveal bool) []any {
	copied := clone(Obj{"collection_environments": snapshot["collection_environments"]})
	if !reveal {
		redactCollectionEnvironmentSnapshots(copied)
	}
	return arr(copied["collection_environments"])
}

func bindCollectionWebSocketAddress(component *xmlElement, nodeID string) {
	property := component.prop(websocketConfigProp)
	if property == nil {
		return
	}
	config, e := decode([]byte(websocketConfigText(property)))
	if e != nil {
		return
	}
	for _, key := range []string{"url", "handshakeUrl"} {
		text := str(config[key])
		addressKey := "base_host"
		if key == "url" {
			addressKey = "ws_base_host"
		}
		if strings.HasPrefix(text, "${base_host}") {
			config[key] = apiEnvironmentAddressReference(nodeID, addressKey) + strings.TrimPrefix(text, "${base_host}")
			continue
		}
		if strings.HasPrefix(text, environmentReference("base_host")) {
			config[key] = apiEnvironmentAddressReference(nodeID, addressKey) + strings.TrimPrefix(text, environmentReference("base_host"))
			continue
		}
		if u, err := url.Parse(text); err == nil && u.Host != "" && !strings.ContainsAny(u.Host, "${}") {
			path := u.EscapedPath()
			path = strings.ReplaceAll(strings.ReplaceAll(path, "%7B", "{"), "%7D", "}")
			if u.RawQuery != "" {
				path += "?" + u.RawQuery
			}
			config[key] = apiEnvironmentAddressReference(nodeID, addressKey) + path
		}
	}
	property.setText(base64.StdEncoding.EncodeToString(encoded(config)))
}
