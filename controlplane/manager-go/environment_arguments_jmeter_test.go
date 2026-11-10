package main

import (
	"context"
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Only two local Groovy assertions execute. HTTP samplers are replaced before
// the plan is written, so this fixture cannot contact any target service.
func TestManagedArgumentsOfficialJMeterUsesFrozenValues(t *testing.T) {
	home, java := localWsTraceJava(t)
	graph := managedGenerationGraph()
	second := graphNode("second-http", "HTTPSamplerProxy", Obj{"bodyJson": "${address_value}"})
	args := graphNode("second-values", "Arguments", Obj{"variables": []any{Obj{"name": "address_value", "value": "{{$auto.timestamp_ms}}"}}})
	obj(args["properties"])["http_environment"] = true
	graph["nodes"] = append(arr(graph["nodes"]), second, args)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", "second-http", 1), graphEdge("second-http", "second-values", 0))
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := workflowArgumentGenerationRules(content)
	if err != nil {
		t.Fatal(err)
	}
	resolver := newEnvironmentRuleResolver(nil).withOperation(newEnvironmentOperation("198.51.100.7", time.UnixMilli(1794212345678)))
	properties, err := argumentRuntimeProperties(Obj{"argument_generation_rules": rules}, Obj{}, nil, resolver)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := parseXMLFragment(content)
	if err != nil {
		t.Fatal(err)
	}
	negative := os.Getenv("AUTO_ENV_ARGUMENT_LITERAL_FIXTURE") == "1"
	var rewrite func(*xmlElement)
	rewrite = func(node *xmlElement) {
		if negative && node.Name.Local == "Arguments" {
			metadata, err := argumentGenerationMetadata(node)
			if err != nil {
				t.Fatal(err)
			}
			pairs := readPairs(node, "Arguments.arguments", "Argument", "Argument")
			for _, raw := range arr(metadata["rules"]) {
				rule := obj(raw)
				for _, pair := range pairs {
					if obj(pair)["name"] == rule["name"] {
						obj(pair)["value"] = rule["generation_rule"]
					}
				}
			}
			patchPairs(node, "Arguments.arguments", "Argument", "Argument", pairs)
		}
		if node.Name.Local == "HTTPSamplerProxy" {
			body := bodyJSON(node)
			want := str(properties[argumentGenerationProperty("second-values", "address_value")])
			if strings.Contains(body, "|") {
				want = str(properties[argumentGenerationProperty("managed-values", "address_value")]) + "|" + str(properties[argumentGenerationProperty("managed-values", "clock_value")]) + "|" + str(properties[argumentGenerationProperty("managed-values", "random_value")])
			}
			node.Name.Local = "JSR223Sampler"
			node.setAttr("testclass", "JSR223Sampler")
			node.setAttr("guiclass", "TestBeanGUI")
			node.Parts = nil
			node.setProp("stringProp", "scriptLanguage", "groovy")
			node.setProp("stringProp", "script", "def actual="+strconv.Quote(body)+"; if (actual != "+strconv.Quote(want)+" || vars.get('clock_value') != '1794212345678' || vars.get('random_value') != "+strconv.Quote(str(properties[argumentGenerationProperty("managed-values", "random_value")]))+") throw new IllegalStateException('unexpected frozen Argument values: ' + vars.get('clock_value')); SampleResult.setResponseData('asserted','UTF-8'); SampleResult.setResponseCode('200')")
		}
		for _, child := range node.children() {
			rewrite(child)
		}
	}
	rewrite(roots[0])
	dir := t.TempDir()
	plan, result, log := filepath.Join(dir, "assertions.jmx"), filepath.Join(dir, "results.jtl"), filepath.Join(dir, "jmeter.log")
	if err = os.WriteFile(plan, []byte(fragmentXML(roots...)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	commandArgs := []string{"-n", "-t", plan, "-l", result, "-j", log, "-Jclock_value=wrong-plain-property", "-Jjmeterengine.nongui.port=0"}
	for key, value := range properties {
		commandArgs = append(commandArgs, "-J"+key+"="+str(value))
	}
	command := exec.CommandContext(ctx, filepath.Join(home, "bin/jmeter"), commandArgs...)
	command.Env = append(os.Environ(), "JAVA_HOME="+java)
	output, err := command.CombinedOutput()
	if acceptance := os.Getenv("AUTO_ENV_ACCEPTANCE_DIR"); acceptance != "" {
		suffix := "green"
		if negative {
			suffix = "red"
		}
		os.WriteFile(filepath.Join(acceptance, "arguments-native-"+suffix+".stdout"), output, 0600)
		if raw, e := os.ReadFile(result); e == nil {
			os.WriteFile(filepath.Join(acceptance, "arguments-native-"+suffix+".jtl"), raw, 0600)
		}
		if raw, e := os.ReadFile(log); e == nil {
			os.WriteFile(filepath.Join(acceptance, "arguments-native-"+suffix+".jmeter-log"), raw, 0600)
		}
	}
	if err != nil {
		t.Fatal("local native assertion process failed", err)
	}
	f, err := os.Open(result)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatal("native assertion sample count", len(rows), err)
	}
	success := -1
	for i, name := range rows[0] {
		if name == "success" {
			success = i
		}
	}
	for _, row := range rows[1:] {
		if success < 0 || row[success] != "true" {
			t.Fatal("native JMeter observed literal/unscoped Argument values")
		}
	}
}
