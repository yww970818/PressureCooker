package main

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const environmentBindingProp = "jmeter.environment_binding"
const environmentVariablesProp = "jmeter.environment_variables"
const environmentPathProp = "jmeter.environment_request_path"
const environmentBasePathRef = "${__P(jmeter.env.base_path,)}"

func environmentProperty(name string) string {
	if name == "base_host" {
		return "jmeter.env.base_host"
	}
	return "jmeter.env.var." + digest([]byte(name))[:16]
}
func environmentReference(name string) string { return "${__P(" + environmentProperty(name) + ",)}" }

func parseEnvironmentBase(value string) (Obj, error) {
	u, e := url.Parse(value)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") || strings.ContainsAny(value, "\r\n\x00") || strings.TrimSpace(value) != value || len(value) > 8192 {
		return nil, api(400, "invalid_base_host", "base_host须为不含认证、查询或片段的完整HTTP(S)地址")
	}
	if strings.Contains(u.Hostname(), ":") && (net.ParseIP(u.Hostname()) == nil || !strings.HasPrefix(u.Host, "[")) {
		return nil, api(400, "invalid_base_host", "IPv6地址须使用方括号")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, api(400, "invalid_base_host", "base_host端口须为1至65535")
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, api(400, "invalid_base_host", "base_host端口无效")
	}
	basePath := strings.TrimRight(u.EscapedPath(), "/")
	return Obj{"base_host": value, "protocol": u.Scheme, "host": u.Hostname(), "port": u.Port(), "base_path": basePath}, nil
}
func validEnvironmentName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 200 && validXMLText(name) && !strings.ContainsAny(name, "\r\n${},")
}
func validateEnvironment(body Obj) (Obj, error) {
	metadata := Obj{"name": strings.TrimSpace(str(body["name"]))}
	if description, exists := body["description"]; exists {
		metadata["description"] = description
	}
	name, _, e := workspaceMetadata(metadata)
	if e != nil {
		return nil, e
	}
	base, ok := body["base_host"].(string)
	if !ok {
		return nil, api(400, "invalid_base_host", "须提供base_host")
	}
	if _, e = parseEnvironmentBase(base); e != nil {
		return nil, e
	}
	variables := []any{}
	if raw, exists := body["variables"]; exists {
		var ok bool
		variables, ok = raw.([]any)
		if !ok || len(variables) > 100 {
			return nil, api(400, "invalid_variables", "variables须为最多100项的name/value数组")
		}
	}
	seen := map[string]bool{}
	for _, value := range variables {
		pair, ok := value.(map[string]any)
		if !ok {
			return nil, api(400, "invalid_variables", "变量须包含name/value")
		}
		name, nok := pair["name"].(string)
		text, vok := pair["value"].(string)
		if !nok || !validEnvironmentName(name) || reservedRuntimeProperty(name) || name == "base_host" || seen[name] || !vok || utf8.RuneCountInString(text) > 4096 || !validXMLText(text) {
			return nil, api(400, "invalid_variables", "变量名须唯一且不能为base_host，值须为最多4096字的文本")
		}
		if e := validateEnvironmentRuleDeclaration(text); e != nil {
			return nil, e
		}
		seen[name] = true
	}
	clean := clone(Obj{"v": variables})["v"]
	for _, raw := range arr(clean) {
		// This provenance is created only by the platform when freezing a run.
		delete(obj(raw), "generation_rule")
	}
	return Obj{"name": name, "base_host": base, "variables": clean}, nil
}
func environmentVariableNames(graph Obj) ([]string, error) {
	names := map[string]bool{}
	if raw, exists := graph["environment_variables"]; exists {
		values, ok := raw.([]any)
		if !ok || len(values) > 100 {
			return nil, api(400, "invalid_environment_binding", "environment_variables须为最多100项变量名数组")
		}
		for _, value := range values {
			name, ok := value.(string)
			if !ok || !validEnvironmentName(name) || name == "base_host" {
				return nil, api(400, "invalid_environment_binding", "环境变量名无效")
			}
			names[name] = true
		}
	}
	for _, value := range arr(graph["nodes"]) {
		props := obj(obj(value)["properties"])
		if boolean(props["http_environment"]) && props["component"] == "Arguments" {
			for _, pair := range arr(obj(props["config"])["variables"]) {
				name := str(obj(pair)["name"])
				if name != "base_host" && validEnvironmentName(name) {
					names[name] = true
				}
			}
		}
	}
	out := []string{}
	producers := graphProducers(graph)
	for name := range names {
		if producers[name] && !producerHasStaticUse(graph, name) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > 100 {
		return nil, api(400, "invalid_environment_binding", "环境变量最多100项")
	}
	return out, nil
}
func environmentSnapshot(data Obj, defaults ...Obj) (Obj, Obj, error) {
	return environmentSnapshotWithResolver(data, newEnvironmentRuleResolver(nil), defaults...)
}
func environmentSnapshotWithResolver(data Obj, resolver environmentRuleResolver, defaults ...Obj) (Obj, Obj, error) {
	address, e := parseEnvironmentBase(str(data["base_host"]))
	if e != nil {
		return nil, nil, e
	}
	snapshot := clone(data)
	delete(snapshot, "created_at")
	delete(snapshot, "updated_at")
	props := Obj{}
	for _, key := range []string{"base_host", "protocol", "host", "port", "base_path"} {
		snapshot[key] = address[key]
		props["jmeter.env."+key] = address[key]
		if key == "base_host" {
			props[key] = address[key]
		}
	}
	variables, e := environmentVariables(data)
	if e != nil {
		return nil, nil, e
	}
	if len(defaults) > 0 {
		for name := range variables {
			if value, exists := defaults[0][name]; exists {
				text, e := environmentVariableText(value)
				if e != nil {
					return nil, nil, e
				}
				variables[name] = text
			}
		}
	}
	resolved, e := resolver.resolve(variables)
	if e != nil {
		return nil, nil, e
	}
	for _, value := range arr(snapshot["variables"]) {
		pair := obj(value)
		if reservedRuntimeProperty(str(pair["name"])) {
			return nil, nil, api(400, "reserved_property", "环境包含平台保留属性，请重命名变量")
		}
		name := str(pair["name"])
		if variables[name] != str(pair["value"]) {
			delete(pair, "generation_rule")
		}
		if kind, _, _ := parseEnvironmentRandomRule(variables[name]); kind != "" {
			pair["generation_rule"] = variables[name]
		}
		pair["value"] = resolved[name]
		props[environmentProperty(name)] = pair["value"]
		props[name] = pair["value"]
	}
	return snapshot, props, nil
}

// Bind addresses after component patching so raw imported samplers and opaque
// plugin subtrees obey exactly the same project environment as visible nodes.
func bindEnvironmentXML(component *xmlElement, names []string) {
	if component.Name.Local == "HTTPSamplerProxy" && len(obj(collectionBindingMetadata(component)["environment_source"])) == 0 {
		path := component.propText("HTTPSampler.path")
		if original := component.prop(environmentPathProp); original != nil && strings.HasPrefix(path, environmentBasePathRef) {
			path = original.text()
		}
		path = strings.TrimPrefix(path, environmentBasePathRef)
		path = strings.TrimPrefix(path, "${base_host}")
		path = strings.TrimPrefix(path, environmentReference("base_host"))
		if u, e := url.Parse(path); e == nil && u.IsAbs() && (u.Scheme == "http" || u.Scheme == "https") {
			path = u.EscapedPath()
			if u.RawQuery != "" {
				path += "?" + u.RawQuery
			}
		}
		if path == "" || !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		component.setProp("stringProp", environmentPathProp, path)
		component.setProp("stringProp", "HTTPSampler.protocol", "${__P(jmeter.env.protocol,)}")
		component.setProp("stringProp", "HTTPSampler.domain", "${__P(jmeter.env.host,)}")
		component.setProp("stringProp", "HTTPSampler.port", "${__P(jmeter.env.port,)}")
		component.setProp("stringProp", "HTTPSampler.path", environmentBasePathRef+path)
	}
	for _, child := range component.children() {
		bindEnvironmentXML(child, names)
	}
}
func applyEnvironmentBinding(root, children *xmlElement, graph Obj) error {
	binding, exists := graph["environment_binding"]
	if exists && binding != "" && binding != "base_host" {
		return api(400, "invalid_environment_binding", "environment_binding仅支持base_host")
	}
	if binding != "base_host" {
		if required, _ := environmentMetadataFromRoots([]*xmlElement{root, children}); required {
			return api(400, "invalid_environment_binding", "导入脚本仍包含环境运行属性，无法恢复固定地址；请保持项目环境绑定，或新建固定地址流程")
		}
		return nil
	}
	names, e := environmentVariableNames(graph)
	if e != nil {
		return e
	}
	root.setProp("stringProp", environmentBindingProp, "base_host")
	root.setProp("stringProp", environmentVariablesProp, string(encoded(names)))
	variables := ensureElementProp(root, "TestPlan.user_defined_variables", "Arguments")
	pairs := readPairs(variables, "Arguments.arguments", "Argument", "Argument")
	bound := map[string]bool{"base_host": true}
	for _, name := range names {
		bound[name] = true
	}
	retained := []any{}
	for _, value := range pairs {
		if !bound[str(obj(value)["name"])] {
			retained = append(retained, value)
		}
	}
	retained = append(retained, Obj{"name": "base_host", "value": environmentReference("base_host")})
	for _, name := range names {
		retained = append(retained, Obj{"name": name, "value": environmentReference(name)})
	}
	patchPairs(variables, "Arguments.arguments", "Argument", "Argument", retained)
	// Legacy local environment Arguments must never shadow the runtime values.
	var patchLegacy func(*xmlElement)
	patchLegacy = func(node *xmlElement) {
		if node.Name.Local == "Arguments" && node.propText("jmeter.environment_managed") == "true" {
			// Scoped declarations have their own frozen property, even when a
			// project variable shares the name. Final JMX validation checks the
			// metadata and exact reference before accepting compilation.
			generated := map[string]string{}
			metadata, _ := argumentGenerationMetadata(node)
			for _, raw := range arr(metadata["rules"]) {
				rule := obj(raw)
				generated[str(rule["name"])] = argumentGenerationReference(str(metadata["node_id"]), str(rule["name"]))
			}
			pairs := readPairs(node, "Arguments.arguments", "Argument", "Argument")
			for _, value := range pairs {
				pair := obj(value)
				name := str(pair["name"])
				if bound[name] && (generated[name] == "" || pair["value"] != generated[name]) {
					pair["value"] = environmentReference(name)
				}
			}
			patchPairs(node, "Arguments.arguments", "Argument", "Argument", pairs)
		}
		for _, child := range node.children() {
			patchLegacy(child)
		}
	}
	patchLegacy(children)
	bindEnvironmentXML(children, names)
	return nil
}
func workflowEnvironmentMetadata(content string) (bool, []string) {
	roots, e := parseXMLFragment(content)
	if e != nil {
		return false, nil
	}
	return environmentMetadataFromRoots(roots)
}

func environmentMetadataFromRoots(roots []*xmlElement) (bool, []string) {
	required := false
	names := []string{}
	var walk func(*xmlElement)
	walk = func(node *xmlElement) {
		if node.Name.Local == "TestPlan" {
			if variables := node.prop("TestPlan.user_defined_variables"); variables != nil {
				for _, value := range readPairs(variables, "Arguments.arguments", "Argument", "Argument") {
					pair := obj(value)
					name := str(pair["name"])
					if name != "base_host" && str(pair["value"]) == environmentReference(name) {
						names = append(names, name)
					}
				}
			}
			if node.propText(environmentBindingProp) == "base_host" {
				required = true
				if decoded, e := decode([]byte(`{"names":` + node.propText(environmentVariablesProp) + `}`)); e == nil {
					for _, value := range arr(decoded["names"]) {
						names = append(names, str(value))
					}
				}
			}
		}
		if node.Name.Local == "stringProp" && strings.Contains(collectionRuntimeReferenceRE.ReplaceAllString(argumentGenerationReferenceRE.ReplaceAllString(node.text(), ""), ""), "${__P(jmeter.env.") {
			required = true
		}
		for _, child := range node.children() {
			walk(child)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	unique := map[string]bool{}
	for _, name := range names {
		unique[name] = true
	}
	names = []string{}
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return required, names
}
func restoreEnvironmentGraph(graph Obj, root *xmlElement, templates map[string][]*xmlElement) {
	bound, inferred := environmentMetadataFromRoots([]*xmlElement{root})
	if !bound {
		for _, value := range arr(graph["nodes"]) {
			if strings.Contains(collectionRuntimeReferenceRE.ReplaceAllString(argumentGenerationReferenceRE.ReplaceAllString(str(obj(obj(value)["properties"])["raw_xml"]), ""), ""), "${__P(jmeter.env.") {
				bound = true
				break
			}
		}
	}
	if !bound {
		return
	}
	graph["environment_binding"] = "base_host"
	names := []any{}
	for _, name := range inferred {
		names = append(names, name)
	}
	if decoded, e := decode([]byte(`{"names":` + root.propText(environmentVariablesProp) + `}`)); e == nil {
		names = arr(decoded["names"])
	}
	graph["environment_variables"] = names
	for _, value := range arr(graph["nodes"]) {
		props := obj(obj(value)["properties"])
		if props["component"] == "Arguments" {
			raw, e := importedNodeXML(obj(value), templates)
			if e == nil && len(raw) > 0 && raw[0].propText("jmeter.environment_managed") == "true" {
				props["http_environment"] = true
			}
		}
		if props["component"] == "HTTPSamplerProxy" {
			config := obj(props["config"])
			raw, e := importedNodeXML(obj(value), templates)
			if e != nil || len(raw) == 0 {
				continue
			}
			if original := raw[0].prop(environmentPathProp); original != nil {
				config["path"] = original.text()
			} else {
				config["path"] = strings.TrimPrefix(str(config["path"]), environmentBasePathRef)
			}
		}
	}
}
func requireEnvironmentVariables(plan, environment Obj) error {
	names := arr(plan["environment_variables"])
	present := map[string]bool{}
	for _, value := range arr(environment["variables"]) {
		present[str(obj(value)["name"])] = true
	}
	for _, value := range names {
		name := str(value)
		if !present[name] {
			return api(400, "environment_variable_required", fmt.Sprint("所选环境缺少变量：", name))
		}
	}
	return nil
}

// Old Scalar snapshots merged their server base path into the sampler path.
// Remove it only when the local, managed base_host proves its provenance.
func normalizeLegacyEnvironmentPaths(tree *workflowTree) {
	for _, node := range tree.Nodes {
		if node.Component != "HTTPSamplerProxy" {
			continue
		}
		path := str(node.Config["path"])
		for _, child := range node.Children {
			if child.Component != "Arguments" || !boolean(obj(child.Source["properties"])["http_environment"]) {
				continue
			}
			for _, value := range arr(child.Config["variables"]) {
				pair := obj(value)
				if pair["name"] != "base_host" {
					continue
				}
				base, e := parseEnvironmentBase(str(pair["value"]))
				if e != nil {
					continue
				}
				prefix := str(base["base_path"])
				if prefix != "" && (path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix+"?")) {
					path = strings.TrimPrefix(path, prefix)
					if path == "" || strings.HasPrefix(path, "?") {
						path = "/" + path
					}
				}
			}
		}
		node.Config["path"] = path
	}
}

// Project mutations already hold the project's FOR UPDATE lock. Comparing the
// encrypted payloads under that lock keeps concurrent creates/renames unique
// without exposing environment names or rewriting existing ciphertext.
func (p *Platform) uniqueEnvironmentName(tx *Tx, id, name string) error {
	rows, e := tx.Query("SELECT id,revision,payload FROM environments WHERE project_id=? AND deleted=0", p.projectID())
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var otherID, payload string
		var revision int64
		if e = rows.Scan(&otherID, &revision, &payload); e != nil {
			return e
		}
		if otherID == id {
			continue
		}
		other, e := p.openWorkspace("environments", otherID, revision, payload)
		if e != nil {
			return e
		}
		if strings.TrimSpace(str(other["name"])) == name {
			return api(409, "environment_name_conflict", "当前项目已存在同名环境，请使用其他名称")
		}
	}
	return rows.Err()
}

// Environment properties deliberately hash their variable names. Retain the
// existing public/log masking policy by recovering sensitivity from the frozen
// snapshot's original names, never from today's mutable environment resource.
func sensitiveRunProperties(run Obj) map[string]bool {
	sensitive := map[string]bool{}
	for key := range obj(run["properties"]) {
		if sensitiveRE.MatchString(key) {
			sensitive[key] = true
		}
	}
	for _, value := range arr(obj(run["environment_snapshot"])["variables"]) {
		name := str(obj(value)["name"])
		if sensitiveRE.MatchString(name) {
			sensitive[environmentProperty(name)] = true
		}
	}
	for _, value := range arr(obj(run["environment_snapshot"])["api_overrides"]) {
		pair := obj(value)
		if sensitiveRE.MatchString(str(pair["name"])) {
			sensitive[str(pair["property"])] = true
		}
	}
	return sensitive
}
