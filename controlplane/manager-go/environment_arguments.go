package main

import (
	"encoding/base64"
	"regexp"
	"strings"
)

const argumentGenerationMetadataProp = "jmeter.environment_argument_generators"

var argumentGenerationReferenceRE = regexp.MustCompile(`\$\{__P\((jmeter\.env\.scalar\.[a-f0-9]{16}\.[a-f0-9]{16}),\)\}`)

func argumentGenerationProperty(nodeID, name string) string {
	return strings.Replace(apiEnvironmentProperty(nodeID, name), "jmeter.env.api.", "jmeter.env.scalar.", 1)
}
func argumentGenerationReference(nodeID, name string) string {
	return "${__P(" + argumentGenerationProperty(nodeID, name) + ",)}"
}
func invalidArgumentGeneration() error {
	return api(400, "invalid_argument_generation", "受管变量生成规则或JMX属性来源元数据无效，请重新保存接口环境声明")
}

// JMeter properties are name-keyed. Managed source metadata must have one
// interpretation even though the legacy XML editor preserves duplicate fields.
func validateManagedArgumentProperties(node *xmlElement) error {
	attributes := map[string]bool{}
	for _, attribute := range node.Attrs {
		name := xmlName(attribute.Name)
		if attributes[name] {
			return invalidArgumentGeneration()
		}
		attributes[name] = true
	}
	seen := map[string]bool{}
	for _, child := range node.children() {
		name := child.attr("name")
		switch name {
		case argumentGenerationMetadataProp, "jmeter.environment_managed", "Arguments.arguments", "Argument.name", "Argument.value":
			if seen[name] {
				return invalidArgumentGeneration()
			}
			seen[name] = true
		}
		if err := validateManagedArgumentProperties(child); err != nil {
			return err
		}
	}
	return nil
}

func argumentGenerationMetadata(component *xmlElement) (Obj, error) {
	// Inspect all attribute values so a duplicate XML name attribute cannot
	// hide the managed marker from the first-value legacy property reader.
	managed := false
	for _, child := range component.children() {
		for _, attribute := range child.Attrs {
			if xmlName(attribute.Name) == "name" && attribute.Value == argumentGenerationMetadataProp {
				managed = true
			}
		}
	}
	if managed {
		if err := validateManagedArgumentProperties(component); err != nil {
			return nil, err
		}
	}
	raw := component.propText(argumentGenerationMetadataProp)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 128*1024 {
		return nil, invalidArgumentGeneration()
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalidArgumentGeneration()
	}
	metadata, err := decode(decoded)
	version, ok := integer(metadata["version"])
	if err != nil || !ok || version != 1 || len(metadata) != 3 || validID(str(metadata["node_id"])) != nil {
		return nil, invalidArgumentGeneration()
	}
	rules, ok := metadata["rules"].([]any)
	if !ok || len(rules) == 0 || len(rules) > 100 {
		return nil, invalidArgumentGeneration()
	}
	seen := map[string]bool{}
	for _, value := range rules {
		rule := obj(value)
		name := str(rule["name"])
		kind, _, err := parseEnvironmentRandomRule(str(rule["generation_rule"]))
		if len(rule) != 3 || !validEnvironmentName(name) || name == "base_host" || reservedRuntimeProperty(name) || seen[name] || kind == "" || err != nil || rule["property"] != argumentGenerationProperty(str(metadata["node_id"]), name) {
			return nil, invalidArgumentGeneration()
		}
		seen[name] = true
	}
	return metadata, nil
}

func bindManagedArgumentGeneration(node *workflowNode, component *xmlElement) error {
	if node.Component != "Arguments" {
		return nil
	}
	component.removeProp(argumentGenerationMetadataProp)
	managed := boolean(obj(node.Source["properties"])["http_environment"])
	rules := []any{}
	pairs := readPairs(component, "Arguments.arguments", "Argument", "Argument")
	for _, value := range pairs {
		pair := obj(value)
		name := str(pair["name"])
		rule := str(pair["value"])
		kind, _, err := parseEnvironmentRandomRule(rule)
		if err != nil {
			return err
		}
		if kind == "" {
			continue
		}
		if !managed || !validEnvironmentName(name) || name == "base_host" || reservedRuntimeProperty(name) || len(rules) >= 100 {
			return invalidArgumentGeneration()
		}
		pair["value"] = argumentGenerationReference(node.ID, name)
		rules = append(rules, Obj{"name": name, "generation_rule": rule, "property": argumentGenerationProperty(node.ID, name)})
	}
	if len(rules) > 0 {
		patchPairs(component, "Arguments.arguments", "Argument", "Argument", pairs)
		component.setProp("boolProp", "jmeter.environment_managed", "true")
		component.setProp("stringProp", argumentGenerationMetadataProp, base64.StdEncoding.EncodeToString(encoded(Obj{"version": 1, "node_id": node.ID, "rules": rules})))
	}
	return nil
}

// Direct property lookups keep identically named declarations on neighboring
// requests independent of JMeter's thread-wide initial Arguments variables.
func bindManagedArgumentUses(node *workflowNode, component, children *xmlElement, graph Obj) error {
	if node.Component != "HTTPSamplerProxy" && node.Component != "WebSocketSampler" {
		return nil
	}
	replacements := map[string]string{}
	for _, child := range children.children() {
		if child.Name.Local != "Arguments" {
			continue
		}
		metadata, err := argumentGenerationMetadata(child)
		if err != nil {
			return err
		}
		for _, value := range arr(metadata["rules"]) {
			rule := obj(value)
			name := str(rule["name"])
			if !producedForNode(graph, node.ID, name) {
				replacements["${"+name+"}"] = argumentGenerationReference(str(metadata["node_id"]), name)
			}
		}
	}
	replaceXMLText(component, replacements)
	replaceXMLText(children, replacements)
	return nil
}

func workflowArgumentGenerationRules(content string) ([]any, error) {
	roots, err := parseXMLFragment(content)
	if err != nil {
		return nil, err
	}
	return argumentGenerationRulesFromRoots(roots)
}

// Both import and compilation already own a parsed tree. Validate the same
// metadata and references without serializing and parsing that tree again.
func argumentGenerationRulesFromRoots(roots []*xmlElement) ([]any, error) {
	rules := []any{}
	declared := map[string]bool{}
	var walk func(*xmlElement) error
	walk = func(node *xmlElement) error {
		metadata, err := argumentGenerationMetadata(node)
		if err != nil {
			return err
		}
		if metadata != nil {
			if node.Name.Local != "Arguments" || node.propText("jmeter.environment_managed") != "true" {
				return invalidArgumentGeneration()
			}
			values := map[string]string{}
			for _, value := range readPairs(node, "Arguments.arguments", "Argument", "Argument") {
				pair := obj(value)
				name := str(pair["name"])
				if _, ok := values[name]; ok {
					return invalidArgumentGeneration()
				}
				values[name] = str(pair["value"])
			}
			for _, value := range arr(metadata["rules"]) {
				rule := clone(obj(value))
				id := str(metadata["node_id"])
				key := str(rule["property"])
				if declared[key] || values[str(rule["name"])] != argumentGenerationReference(id, str(rule["name"])) {
					return invalidArgumentGeneration()
				}
				declared[key] = true
				rule["node_id"] = id
				rules = append(rules, rule)
			}
		}
		if node.Name.Local == "Arguments" || node.Name.Local == "elementProp" && node.attr("elementType") == "Arguments" {
			for _, value := range readPairs(node, "Arguments.arguments", "Argument", "Argument") {
				kind, _, err := parseEnvironmentRandomRule(str(obj(value)["value"]))
				if err != nil || kind != "" {
					return invalidArgumentGeneration()
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
		if err := walk(root); err != nil {
			return nil, err
		}
	}
	for _, root := range roots {
		for _, match := range argumentGenerationReferenceRE.FindAllStringSubmatch(executableXMLText(root), -1) {
			if !declared[match[1]] {
				return nil, invalidArgumentGeneration()
			}
		}
	}
	return rules, nil
}

func argumentRuntimeProperties(plan, snapshot, defaults Obj, resolver environmentRuleResolver) (Obj, error) {
	out := Obj{}
	frozen := []any{}
	seen := map[string]bool{}
	for _, value := range arr(snapshot["api_overrides"]) {
		if obj(value)["origin"] != "managed_arguments" {
			frozen = append(frozen, value)
		}
	}
	for _, value := range arr(plan["argument_generation_rules"]) {
		rule := obj(value)
		id, name, key := str(rule["node_id"]), str(rule["name"]), str(rule["property"])
		kind, _, err := parseEnvironmentRandomRule(str(rule["generation_rule"]))
		if len(rule) != 4 || validID(id) != nil || !validEnvironmentName(name) || reservedRuntimeProperty(name) || name == "base_host" || err != nil || kind == "" || key != argumentGenerationProperty(id, name) || seen[key] {
			return nil, invalidArgumentGeneration()
		}
		seen[key] = true
		definition := str(rule["generation_rule"])
		provenance := definition
		if value, exists := defaults[name]; exists {
			definition = str(value)
			provenance = str(obj(snapshot["task_property_rules"])[name])
		}
		resolved, err := resolver.resolve(map[string]string{name: definition})
		if err != nil {
			return nil, err
		}
		out[key] = resolved[name]
		trace := Obj{"node_id": id, "name": name, "property": key, "value": resolved[name], "origin": "managed_arguments"}
		if provenance != "" {
			trace["generation_rule"] = provenance
		}
		frozen = append(frozen, trace)
	}
	if len(frozen) > 0 || snapshot["api_overrides"] != nil {
		snapshot["api_overrides"] = frozen
	}
	return out, nil
}

func restoreManagedArgumentGraph(graph Obj, templates map[string][]*xmlElement) error {
	replacements := map[string]string{}
	for _, value := range arr(graph["nodes"]) {
		props := obj(obj(value)["properties"])
		if props["component"] != "Arguments" {
			continue
		}
		roots, err := importedNodeXML(obj(value), templates)
		if err != nil || len(roots) != 1 {
			continue
		}
		metadata, err := argumentGenerationMetadata(roots[0])
		if err != nil {
			return err
		}
		if metadata == nil {
			continue
		}
		props["http_environment"] = true
		for _, raw := range arr(metadata["rules"]) {
			rule := obj(raw)
			name := str(rule["name"])
			replacements[argumentGenerationReference(str(metadata["node_id"]), name)] = "${" + name + "}"
			for _, row := range arr(obj(props["config"])["variables"]) {
				if obj(row)["name"] == name {
					obj(row)["value"] = rule["generation_rule"]
				}
			}
		}
	}
	if len(replacements) == 0 {
		return nil
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
			for key, item := range v {
				v[key] = restore(item)
			}
		case []any:
			for i, item := range v {
				v[i] = restore(item)
			}
		}
		return value
	}
	for _, value := range arr(graph["nodes"]) {
		props := obj(obj(value)["properties"])
		restore(props["config"])
		roots, err := importedNodeXML(obj(value), templates)
		if err == nil {
			for _, root := range roots {
				replaceXMLText(root, replacements)
			}
			props["raw_xml"] = fragmentXML(roots...)
		}
	}
	return nil
}
