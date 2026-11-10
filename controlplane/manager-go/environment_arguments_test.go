package main

import (
	"strings"
	"testing"
	"time"
)

func managedGenerationGraph() Obj {
	g := basicWorkflowGraph()
	arg := graphNode("managed-values", "Arguments", Obj{"variables": []any{Obj{"name": "address_value", "value": "{{$auto.client_ip}}"}, Obj{"name": "clock_value", "value": "{{$auto.timestamp_ms}}"}, Obj{"name": "random_value", "value": "{{$random.hex(16)}}"}}})
	obj(arg["properties"])["http_environment"] = true
	g["nodes"] = append(arr(g["nodes"]), arg)
	g["edges"] = append(arr(g["edges"]), graphEdge("http", "managed-values", 0))
	obj(obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = "${address_value}|${clock_value}|${random_value}"
	return g
}

func TestManagedArgumentsTaskPropertiesRemainScopedAndExplicitOverridesWin(t *testing.T) {
	graph := managedGenerationGraph()
	other := graphNode("second-values", "Arguments", Obj{"variables": []any{Obj{"name": "address_value", "value": "{{$auto.timestamp_ms}}"}}})
	obj(other["properties"])["http_environment"] = true
	http := graphNode("second-http", "HTTPSamplerProxy", Obj{"path": "/${address_value}"})
	graph["nodes"] = append(arr(graph["nodes"]), http, other)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", "second-http", 1), graphEdge("second-http", "second-values", 0))
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := workflowArgumentGenerationRules(content)
	if err != nil || len(rules) != 4 {
		t.Fatal("missing managed declarations", rules, err)
	}
	plan := Obj{"argument_generation_rules": rules}
	snapshot := Obj{}
	resolver := newEnvironmentRuleResolver(snapshot).withOperation(newEnvironmentOperation("198.51.100.7", time.UnixMilli(1794212345678)))
	properties, err := argumentRuntimeProperties(plan, snapshot, nil, resolver)
	if err != nil || properties[argumentGenerationProperty("managed-values", "address_value")] != "198.51.100.7" || properties[argumentGenerationProperty("second-values", "address_value")] != "1794212345678" {
		t.Fatal("neighboring node rules aliased", properties, err)
	}
	if len(arr(snapshot["api_overrides"])) != 4 {
		t.Fatal("managed rule provenance was lost", snapshot)
	}
	properties, err = argumentRuntimeProperties(plan, snapshot, Obj{"address_value": "manual"}, resolver)
	if err != nil || properties[argumentGenerationProperty("managed-values", "address_value")] != "manual" || properties[argumentGenerationProperty("second-values", "address_value")] != "manual" || len(arr(snapshot["api_overrides"])) != 4 {
		t.Fatal("explicit override lost or provenance duplicated", properties, err)
	}
	corrupt := clone(plan)
	obj(arr(corrupt["argument_generation_rules"])[0])["property"] = "jmeter.env.scalar.forged"
	if _, err = argumentRuntimeProperties(corrupt, Obj{}, nil, resolver); err == nil {
		t.Fatal("forged generated property accepted")
	}
}

func TestManagedArgumentsRejectMismatchedOrMissingMetadata(t *testing.T) {
	content, err := compileWorkflow(managedGenerationGraph())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing metadata", "wrong value", "missing managed marker"} {
		roots, err := parseXMLFragment(content)
		if err != nil {
			t.Fatal(err)
		}
		var mutate func(*xmlElement)
		mutate = func(node *xmlElement) {
			if node.Name.Local == "Arguments" && node.propText(argumentGenerationMetadataProp) != "" {
				switch mode {
				case "missing metadata":
					node.removeProp(argumentGenerationMetadataProp)
				case "missing managed marker":
					node.removeProp("jmeter.environment_managed")
				case "wrong value":
					pairs := readPairs(node, "Arguments.arguments", "Argument", "Argument")
					obj(pairs[0])["value"] = "forged"
					patchPairs(node, "Arguments.arguments", "Argument", "Argument", pairs)
				}
			}
			for _, child := range node.children() {
				mutate(child)
			}
		}
		mutate(roots[0])
		bad := fragmentXML(roots...)
		if _, err = workflowArgumentGenerationRules(bad); err == nil {
			t.Fatal("bad declaration metadata accepted", mode)
		}
		if _, err = importWorkflowGraph(bad); err == nil {
			t.Fatal("bad declaration metadata was repaired by import", mode)
		}
	}
}

func TestManagedArgumentsCompileWithoutExecutableGeneratorLiterals(t *testing.T) {
	content, err := compileWorkflow(managedGenerationGraph())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "{{$auto.") || strings.Contains(content, "{{$random.") || !strings.Contains(content, "${__P(jmeter.env.scalar.") {
		t.Fatal("managed Argument declarations reached JMX as executable literals")
	}
	imported, err := importWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	arg := findGraphNode(imported, "Arguments")
	if obj(arg["properties"])["http_environment"] != true || obj(arr(obj(obj(arg["properties"])["config"])["variables"])[0])["value"] != "{{$auto.client_ip}}" || imported["environment_binding"] == "base_host" {
		t.Fatal("managed unbound declaration lost on import", imported)
	}
	second, err := compileWorkflow(imported)
	if err != nil || strings.Contains(second, "{{$auto.") {
		t.Fatal("reimported generated arguments became literals", err)
	}
}

func TestManagedArgumentsRemainScopedInsideProjectBinding(t *testing.T) {
	graph := managedGenerationGraph()
	graph["environment_binding"] = "base_host"
	graph["environment_variables"] = []any{"address_value", "clock_value", "random_value"}
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := workflowArgumentGenerationRules(content)
	if err != nil || len(rules) != 3 {
		t.Fatal("project binding replaced scoped generation references", err)
	}
	if _, err = importWorkflowGraph(content); err != nil {
		t.Fatal("mixed binding cannot round trip", err)
	}
}

func TestManagedArgumentsRejectAmbiguousCriticalXMLProperties(t *testing.T) {
	content, err := compileWorkflow(managedGenerationGraph())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"value", "name", "collection", "metadata", "marker", "attribute"} {
		t.Run(mode, func(t *testing.T) {
			roots, err := parseXMLFragment(content)
			if err != nil {
				t.Fatal(err)
			}
			var managed *xmlElement
			var find func(*xmlElement)
			find = func(node *xmlElement) {
				if node.Name.Local == "Arguments" && node.propText(argumentGenerationMetadataProp) != "" {
					managed = node
				}
				for _, child := range node.children() {
					find(child)
				}
			}
			find(roots[0])
			if managed == nil {
				t.Fatal("missing managed fixture")
			}
			pair := managed.prop("Arguments.arguments").children()[0]
			switch mode {
			case "value":
				pair.append(property("stringProp", "Argument.value", "{{$auto.client_ip}}"))
			case "name":
				pair.append(property("stringProp", "Argument.name", "different_name"))
			case "collection":
				managed.append(managed.prop("Arguments.arguments").copy())
			case "metadata":
				managed.append(managed.prop(argumentGenerationMetadataProp).copy())
			case "marker":
				managed.append(property("boolProp", "jmeter.environment_managed", "false"))
			case "attribute":
				value := pair.prop("Argument.value")
				value.Attrs = append(value.Attrs, value.Attrs[0])
			}
			bad := fragmentXML(roots...)
			if _, err = workflowArgumentGenerationRules(bad); err == nil {
				t.Fatal("ambiguous managed declaration passed metadata validation")
			}
			if _, err = importWorkflowGraph(bad); err == nil {
				t.Fatal("import silently repaired an ambiguous declaration")
			}
			p := &Platform{Config: Config{PlansDir: t.TempDir()}}
			if _, err = p.preparePlan(Obj{"name": "ambiguous", "content": bad}); err == nil {
				t.Fatal("upload admitted an ambiguous managed declaration")
			}
		})
	}
}

func TestManagedArgumentsLeaveUnmanagedLegacyDuplicatesCompatible(t *testing.T) {
	content := `<Arguments><collectionProp name="Arguments.arguments"><elementProp name="legacy" elementType="Argument"><stringProp name="Argument.name">legacy</stringProp><stringProp name="Argument.value">first</stringProp><stringProp name="Argument.value">second</stringProp></elementProp></collectionProp></Arguments>`
	if rules, err := workflowArgumentGenerationRules(content); err != nil || len(rules) != 0 {
		t.Fatal("managed validation expanded to ordinary legacy Arguments", err)
	}
}
