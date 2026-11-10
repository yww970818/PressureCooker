package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

const apiEnvironmentProp = "jmeter.api_environment"

func apiEnvironmentProperty(nodeID, name string) string {
	a := sha256.Sum256([]byte(nodeID))
	b := sha256.Sum256([]byte(name))
	return fmt.Sprintf("jmeter.env.api.%x.%x", a[:8], b[:8])
}
func apiEnvironmentAddressProperty(nodeID, name string) string {
	return apiEnvironmentProperty(nodeID, "\x00address:"+name)
}
func apiEnvironmentAddressReference(nodeID, name string) string {
	return "${__P(" + apiEnvironmentAddressProperty(nodeID, name) + ",)}"
}

var collectionAddressKeys = []string{"base_host", "protocol", "host", "port", "base_path"}

func apiEnvironmentReference(nodeID, name string) string {
	return "${__P(" + apiEnvironmentProperty(nodeID, name) + ",)}"
}
func replaceXMLText(node *xmlElement, replacements map[string]string) {
	if websocketComponent(node) {
		if p := node.prop(websocketConfigProp); p != nil {
			replaceXMLText(p, replacements)
		}
		return
	}
	if node.Name.Local == "stringProp" {
		if node.attr("name") == websocketAuthoredProp {
			return
		}
		if node.attr("name") == websocketConfigProp {
			value := websocketConfigText(node)
			for old, next := range replacements {
				value = strings.ReplaceAll(value, old, next)
			}
			node.setText(base64.StdEncoding.EncodeToString([]byte(value)))
			return
		}
	}
	for i := range node.Parts {
		part := &node.Parts[i]
		if part.Node != nil {
			replaceXMLText(part.Node, replacements)
		} else {
			for old, value := range replacements {
				part.Text = strings.ReplaceAll(part.Text, old, value)
			}
		}
	}
}

// Metadata is descriptive, not executable variable usage.
func executableXMLText(node *xmlElement) string {
	if websocketComponent(node) {
		if p := node.prop(websocketConfigProp); p != nil {
			return websocketExecutableText(p)
		}
		return ""
	}
	if node.Name.Local == "stringProp" && node.attr("name") == apiEnvironmentProp {
		return ""
	}
	var out strings.Builder
	for _, part := range node.Parts {
		if part.Node != nil {
			out.WriteString(executableXMLText(part.Node))
		} else {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}
func collectionBindingMetadata(node *xmlElement) Obj {
	data, e := decode([]byte(node.propText(apiEnvironmentProp)))
	if e != nil {
		return nil
	}
	return data
}

// Compile template rows into direct property lookups in each HTTP subtree. No
// shared JMeter variable is assigned, so adjacent samplers cannot leak values.
func bindCollectionNode(node *workflowNode, component, children *xmlElement, graph Obj) {
	props := obj(node.Source["properties"])
	ref := obj(props["api_reference"])
	environmentRef := obj(props["collection_environment_reference"])
	// Imported XML is a template, not the authority for the current graph source.
	// Restore old lookups before rewriting or detaching an imported reference.
	if old := collectionBindingMetadata(component); old != nil {
		replacements := map[string]string{}
		for _, value := range arr(old["names"]) {
			name := str(value)
			replacements[apiEnvironmentReference(str(old["node_id"]), name)] = "${" + name + "}"
		}
		replaceXMLText(component, replacements)
		replaceXMLText(children, replacements)
		if len(obj(old["environment_source"])) > 0 {
			addressReplacements := map[string]string{}
			for _, key := range collectionAddressKeys {
				addressReplacements[apiEnvironmentAddressReference(str(old["node_id"]), key)] = "${__P(jmeter.env." + key + ",)}"
			}
			replaceXMLText(component, addressReplacements)
			replaceXMLText(children, addressReplacements)
		}
		component.removeProp(apiEnvironmentProp)
	}
	if (node.Component != "HTTPSamplerProxy" && node.Component != "WebSocketSampler") || (len(environmentRef) == 0 && (str(graph["environment_binding"]) != "base_host" || len(ref) == 0)) {
		return
	}
	candidates, exists := props["api_environment_variables"]
	if !exists {
		candidates = graph["environment_variables"]
	}
	names := []any{}
	replacements := map[string]string{}
	source := executableXMLText(component) + executableXMLText(children)
	if node.Component == "WebSocketSampler" || len(environmentRef) > 0 {
		known := map[string]bool{}
		values := []any{}
		for _, v := range arr(candidates) {
			n := str(v)
			if !known[n] {
				known[n] = true
				values = append(values, n)
			}
		}
		for _, m := range websocketVariable.FindAllStringSubmatch(source, -1) {
			if !known[m[1]] {
				known[m[1]] = true
				values = append(values, m[1])
			}
		}
		candidates = values
	}
	for _, value := range arr(candidates) {
		name := str(value)
		if name == "base_host" || !validEnvironmentName(name) || producedForNode(graph, node.ID, name) || (node.Component == "WebSocketSampler" && websocketAutoVariable(name)) {
			continue
		}
		if strings.Contains(source, "${"+name+"}") || strings.Contains(source, environmentReference(name)) {
			names = append(names, name)
			replacements["${"+name+"}"] = apiEnvironmentReference(node.ID, name)
			replacements[environmentReference(name)] = apiEnvironmentReference(node.ID, name)
		}
	}
	if len(environmentRef) > 0 {
		if node.Component == "HTTPSamplerProxy" {
			bindEnvironmentXML(component, nil)
		}
		for _, key := range collectionAddressKeys {
			replacements["${__P(jmeter.env."+key+",)}"] = apiEnvironmentAddressReference(node.ID, key)
		}
		replacements["${base_host}"] = apiEnvironmentAddressReference(node.ID, "base_host")
	}
	replaceXMLText(component, replacements)
	replaceXMLText(children, replacements)
	metadata := Obj{"node_id": node.ID, "source": ref, "names": names}
	if len(environmentRef) > 0 {
		metadata["environment_source"] = environmentRef
	}
	if node.Component == "WebSocketSampler" {
		metadata["transport"] = "ws"
	}
	component.setProp("stringProp", apiEnvironmentProp, string(encoded(metadata)))
}
func workflowCollectionMetadata(content string) ([]any, error) {
	roots, e := parseXMLFragment(content)
	if e != nil {
		return nil, e
	}
	bindings := []any{}
	seen := map[string]bool{}
	var walk func(*xmlElement) error
	walk = func(node *xmlElement) error {
		if node.prop(apiEnvironmentProp) != nil {
			data := collectionBindingMetadata(node)
			ref := obj(data["source"])
			id := str(data["node_id"])
			environmentRef := obj(data["environment_source"])
			identity := ref
			if len(environmentRef) > 0 {
				identity = environmentRef
			}
			revision, ok := integer(identity["revision"])
			names, nok := data["names"].([]any)
			validTransport := (node.Name.Local == "HTTPSamplerProxy" && str(data["transport"]) == "") || (websocketComponent(node) && data["transport"] == "ws")
			if !validTransport || validID(id) != nil || seen[id] || validID(str(identity["collection_id"])) != nil || !ok || revision < 1 || !nok || len(names) > 100 || (len(environmentRef) == 0 && (!apiMethod(str(ref["method"])) || !strings.HasPrefix(str(ref["path"]), "/"))) || (len(environmentRef) > 0 && validID(str(environmentRef["collection_environment_id"])) != nil) {
				return api(400, "invalid_api_reference", "JMX集合环境引用元数据无效")
			}
			unique := map[string]bool{}
			for _, v := range names {
				name := str(v)
				if !validEnvironmentName(name) || name == "base_host" || unique[name] {
					return api(400, "invalid_api_reference", "JMX集合环境变量名无效")
				}
				unique[name] = true
			}
			seen[id] = true
			bindings = append(bindings, data)
		}
		for _, child := range node.children() {
			if e := walk(child); e != nil {
				return e
			}
		}
		return nil
	}
	for _, root := range roots {
		if e := walk(root); e != nil {
			return nil, e
		}
	}
	declared := map[string]bool{}
	for _, value := range bindings {
		data := obj(value)
		if len(obj(data["environment_source"])) > 0 {
			for _, key := range collectionAddressKeys {
				declared[apiEnvironmentAddressProperty(str(data["node_id"]), key)] = true
			}
		}
		for _, name := range arr(data["names"]) {
			declared[apiEnvironmentProperty(str(data["node_id"]), str(name))] = true
		}
	}
	pattern := regexp.MustCompile(`\$\{__P\((jmeter\.env\.api\.[a-f0-9]{16}\.[a-f0-9]{16}),\)\}`)
	for _, root := range roots {
		for _, match := range pattern.FindAllStringSubmatch(executableXMLText(root), -1) {
			if !declared[match[1]] {
				return nil, api(400, "invalid_api_reference", "JMX集合环境运行属性缺少来源元数据")
			}
		}
	}
	return bindings, nil
}
func restoreCollectionGraph(graph Obj, templates map[string][]*xmlElement) {
	replacements := map[string]string{}
	for _, value := range arr(graph["nodes"]) {
		props := obj(obj(value)["properties"])
		roots, e := importedNodeXML(obj(value), templates)
		if e != nil || len(roots) != 1 {
			continue
		}
		data := collectionBindingMetadata(roots[0])
		if data == nil {
			continue
		}
		if len(obj(data["source"])) > 0 {
			props["api_reference"] = data["source"]
		}
		if len(obj(data["environment_source"])) > 0 {
			props["collection_environment_reference"] = data["environment_source"]
			for _, key := range collectionAddressKeys {
				replacements[apiEnvironmentAddressReference(str(data["node_id"]), key)] = "${__P(jmeter.env." + key + ",)}"
			}
		}
		props["api_environment_variables"] = data["names"]
		for _, name := range arr(data["names"]) {
			replacements[apiEnvironmentReference(str(data["node_id"]), str(name))] = "${" + str(name) + "}"
		}
	}
	if len(replacements) == 0 {
		return
	}
	var restore func(any) any
	restore = func(value any) any {
		switch v := value.(type) {
		case string:
			for old, next := range replacements {
				v = strings.ReplaceAll(v, old, next)
			}
			return v
		case map[string]any:
			for k, item := range v {
				if k != "api_reference" {
					v[k] = restore(item)
				}
			}
		case []any:
			for i, item := range v {
				v[i] = restore(item)
			}
		}
		return value
	}
	for _, value := range arr(graph["nodes"]) {
		restore(obj(obj(value)["properties"])["config"])
		props := obj(obj(value)["properties"])
		// Raw XML must be rewritten as decoded text, then escaped again. Literal
		// substitution of names containing & or < would corrupt the template.
		roots, e := importedNodeXML(obj(value), templates)
		if e == nil {
			for _, root := range roots {
				replaceXMLText(root, replacements)
			}
			props["raw_xml"] = fragmentXML(roots...)
		}
	}
}
func (p *Platform) collectionRuntimeProperties(q queryer, plan, environment Obj, snapshot Obj, defaults ...Obj) (Obj, error) {
	resolver := newEnvironmentRuleResolver(snapshot, defaults...).withOperation(environmentOperationFromContext(p.requestContext()))
	return p.collectionRuntimePropertiesWithResolver(q, plan, environment, snapshot, resolver, defaults...)
}
func (p *Platform) collectionRuntimePropertiesWithResolver(q queryer, plan, environment, snapshot Obj, resolver environmentRuleResolver, defaults ...Obj) (Obj, error) {
	out := Obj{}
	frozen := []any{}
	resolver.seedSnapshot(snapshot, defaults...)
	for _, value := range arr(plan["api_environment_bindings"]) {
		binding := obj(value)
		if environmentRef := obj(binding["environment_source"]); len(environmentRef) > 0 {
			scoped, err := p.collectionResourceRuntime(q, binding, resolver, defaults...)
			if err != nil {
				return nil, err
			}
			for key, value := range obj(scoped["properties"]) {
				out[key] = value
			}
			frozen = append(frozen, arr(scoped["api_overrides"])...)
			snapshot["collection_environments"] = append(arr(snapshot["collection_environments"]), scoped["environment"])
			continue
		}
		ref := obj(binding["source"])
		id := str(ref["collection_id"])
		revision, _ := integer(ref["revision"])
		// Read history even if the current collection was removed. Its project row is
		// still authoritative for isolation; immutable published revisions remain usable.
		var projectID, payload string
		if e := q.QueryRow("SELECT project_id FROM api_collections WHERE id=?", id).Scan(&projectID); e != nil {
			return nil, api(400, "invalid_api_reference", "引用集合历史不存在")
		}
		if projectID != p.projectID() {
			return nil, api(400, "invalid_api_reference", "引用集合不属于当前项目")
		}
		if e := q.QueryRow("SELECT payload FROM api_collection_versions WHERE object_id=? AND revision=?", id, revision).Scan(&payload); e != nil {
			return nil, api(400, "invalid_api_reference", "引用集合版本不存在")
		}
		collection, e := p.openWorkspace("api_collections", id, revision, payload)
		if e != nil {
			return nil, e
		}
		operation, ok := obj(obj(obj(collection["document"])["paths"])[str(ref["path"])])[strings.ToLower(str(ref["method"]))].(map[string]any)
		if !ok {
			return nil, api(400, "invalid_api_reference", "引用的历史接口不存在")
		}
		descriptor, e := operationInterfaceProtocol(obj(collection["document"]), operation)
		if e != nil {
			return nil, e
		}
		if e = requireInterfaceProtocolExecution(descriptor); e != nil {
			return nil, e
		}
		if (descriptor.Protocol == "websocket") != (binding["transport"] == "ws") {
			return nil, api(400, "invalid_api_reference", "引用接口与采样器的 HTTP / WS 类型不一致")
		}
		effective := snapshot
		if len(effective) == 0 {
			effective = environment
		}
		if len(defaults) > 0 {
			effective = clone(effective)
			variables, e := environmentVariables(effective)
			if e != nil {
				return nil, e
			}
			for key, value := range defaults[0] {
				if key != "base_host" && !reservedRuntimeProperty(key) {
					variables[key] = str(value)
				}
			}
			pairs := []any{}
			for key, value := range variables {
				if key != "base_host" {
					pairs = append(pairs, Obj{"name": key, "value": value})
				}
			}
			effective["variables"] = pairs
		}
		definitions, e := collectionVariableDefinitions(collection, effective, nil)
		if e != nil {
			return nil, e
		}
		variables, e := resolver.resolve(definitions)
		if e != nil {
			return nil, e
		}
		for _, v := range arr(binding["names"]) {
			name := str(v)
			text, exists := variables[name]
			if !exists {
				return nil, api(400, "environment_variable_required", "所选环境缺少引用接口变量："+name)
			}
			key := apiEnvironmentProperty(str(binding["node_id"]), name)
			out[key] = text
			trace := Obj{"node_id": binding["node_id"], "collection_id": id, "revision": revision, "name": name, "property": key, "value": text}
			if kind, _, _ := parseEnvironmentRandomRule(definitions[name]); kind != "" {
				trace["generation_rule"] = definitions[name]
			}
			frozen = append(frozen, trace)
		}
	}
	if len(frozen) > 0 {
		snapshot["api_overrides"] = frozen
	}
	return out, nil
}
