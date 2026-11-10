package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"pressurecooker/manager/internal/workflowcatalog"
	"strings"
	"testing"
)

func workflowExtensionFixture() Obj {
	return Obj{"name": "Generic fixture", "component": "example.CustomSampler", "version": "1.0.0", "description": "fixture", "source_url": "https://example.invalid/source", "code": "function adapt(){return {fields:[{property:'message',label:'Message',kind:'text'}]}}", "template_xml": `<example.CustomSampler testname="custom"><stringProp name="message">original</stringProp></example.CustomSampler>`}
}

func TestWorkflowNativeCatalogAndLosslessLegacyAdaptation(t *testing.T) {
	if len(workflowcatalog.Native) < 100 {
		t.Fatal("missing official component catalog")
	}
	for _, c := range workflowcatalog.Native {
		if _, err := parseNativeProperties(c.TemplateProperties); err != nil {
			t.Fatalf("%s defaults: %v", c.Component, err)
		}
		if _, ok := workflowDefaults[c.Component]; !ok {
			t.Fatal("native unsupported", c.Component)
		}
		g := basicWorkflowGraph()
		n := graphNode("native", c.Component, Obj{})
		switch c.Kind {
		case "plan":
			continue
		case "thread":
			g["nodes"] = []any{arr(g["nodes"])[0], n}
			g["edges"] = []any{graphEdge("root", "native", 0)}
		default:
			g["nodes"] = append(arr(g["nodes"]), n)
			parent := "group"
			if c.Component == "TestFragmentController" {
				parent = "root"
			}
			g["edges"] = append(arr(g["edges"]), graphEdge(parent, "native", 1))
		}
		if _, e := compileWorkflow(g); e != nil {
			t.Fatalf("%s cannot compile: %v", c.Component, e)
		}
	}
	// Complete Java class names must obey the same scopes as their save aliases.
	for _, c := range workflowcatalog.Native {
		if c.Kind == "plan" {
			continue
		}
		g := basicWorkflowGraph()
		n := graphNode("qualified", c.ClassName, nil)
		parent := "group"
		if c.Kind == "thread" || c.Component == "TestFragmentController" {
			parent = "root"
		}
		g["nodes"] = append(arr(g["nodes"]), n)
		g["edges"] = append(arr(g["edges"]), graphEdge(parent, "qualified", 1))
		if _, e := compileWorkflow(g); e != nil {
			t.Fatalf("%s scope: %v", c.ClassName, e)
		}
	}

	raw := `<jmeterTestPlan><hashTree><TestPlan testname="plan"/><hashTree><ThreadGroup testname="group"/><hashTree><JSR223Sampler testname="script" custom="kept"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">log.info('retain-original')</stringProp><stringProp name="plugin-extra">kept</stringProp></JSR223Sampler><hashTree><JSR223Assertion testname="assert"><stringProp name="script">assert true</stringProp></JSR223Assertion><hashTree/></hashTree><ConstantThroughputTimer testname="rate"><doubleProp name="throughput">12.5</doubleProp><intProp name="calcMode">4</intProp></ConstantThroughputTimer><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	g, e := importWorkflowGraph(raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(g["nodes"])) != 5 {
		t.Fatal("native children not expanded")
	}
	script := obj(findGraphNode(g, "JSR223Sampler")["properties"])
	if obj(script["config"])["script"] != "log.info('retain-original')" {
		t.Fatal("script not editable")
	}
	obj(script["config"])["script"] = "log.info('edited')"
	compiled, e := compileWorkflow(g)
	if e != nil || !strings.Contains(compiled, "edited") || !strings.Contains(compiled, `custom="kept"`) || !strings.Contains(compiled, "plugin-extra") {
		t.Fatal("native patch lost metadata", e)
	}
	// Before adaptation these nodes stored empty config and an opaque subtree.
	script["config"] = Obj{}
	script["raw_xml"] = `<JSR223Sampler testname="script"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">keep-old-script</stringProp></JSR223Sampler><hashTree><JSR223Assertion testname="nested"><stringProp name="script">assert true</stringProp></JSR223Assertion><hashTree/></hashTree>`
	edges := []any{}
	for _, v := range arr(g["edges"]) {
		if str(obj(v)["sourceNodeId"]) != str(findGraphNode(g, "JSR223Sampler")["id"]) {
			edges = append(edges, v)
		}
	}
	g["edges"] = edges
	nodes := []any{}
	for _, v := range arr(g["nodes"]) {
		if str(obj(obj(v)["properties"])["component"]) != "JSR223Assertion" {
			nodes = append(nodes, v)
		}
	}
	g["nodes"] = nodes
	compiled, e = compileWorkflow(g)
	if e != nil || !strings.Contains(compiled, "keep-old-script") || !strings.Contains(compiled, `testname="nested"`) {
		t.Fatal("legacy script/subtree lost", e)
	}
}

func TestWorkflowNativePropertiesAndCustomAdapterPatch(t *testing.T) {
	for _, raw := range []string{`<!DOCTYPE x><stringProp name="x">a</stringProp>`, `<HTTPSamplerProxy/>`, `<stringProp>x</stringProp>`, `</properties><properties><stringProp name="x"/></properties><properties>`} {
		if _, e := parseNativeProperties(raw); e == nil {
			t.Fatal("invalid property accepted", raw)
		}
	}
	g := basicWorkflowGraph()
	n := graphNode("custom", "example.CustomSampler", Obj{"properties_xml": `<stringProp name="message">edited &amp; safe</stringProp>`})
	obj(n["properties"])["raw_xml"] = `<example.CustomSampler testname="custom" vendor="preserved"><stringProp name="message">old</stringProp></example.CustomSampler><hashTree><example.Nested testname="nested"/><hashTree/></hashTree>`
	g["nodes"] = append(arr(g["nodes"]), n)
	g["edges"] = append(arr(g["edges"]), graphEdge("group", "custom", 1))
	xml, e := compileWorkflow(g)
	if e != nil || !strings.Contains(xml, "edited &amp; safe") || !strings.Contains(xml, `vendor="preserved"`) || !strings.Contains(xml, "example.Nested") {
		t.Fatal("adapter patch lost component subtree", e)
	}
	generic := graphNode("generic", "RegexExtractor", Obj{"properties_xml": `<stringProp name="RegexExtractor.regex">x(.*)</stringProp><collectionProp name="extra"><stringProp name="nested">kept</stringProp></collectionProp>`})
	g["nodes"] = append(arr(g["nodes"]), generic)
	g["edges"] = append(arr(g["edges"]), graphEdge("http", "generic", 0))
	if _, e := compileWorkflow(g); e != nil {
		t.Fatal(e)
	}
}

func TestWorkflowExtensionDefinitionsAndProvenance(t *testing.T) {
	d, e := validateWorkflowExtension(workflowExtensionFixture(), "custom-fixture")
	if e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(Obj){func(d Obj) { d["component"] = "JSR223Sampler" }, func(d Obj) { d["component"] = "WebSocketSampler" }, func(d Obj) { d["template_xml"] = "<wrong/>" }, func(d Obj) { d["code"] = strings.Repeat("x", 61<<10) }, func(d Obj) { d["network"] = true }} {
		bad := workflowExtensionFixture()
		mutate(bad)
		if _, e := validateWorkflowExtension(bad, "custom-fixture"); e == nil {
			t.Fatal("bad extension accepted")
		}
	}
	p := &Platform{Config: Config{SessionKey: "fixture-only"}}
	sealed, e := p.sealWorkspace("workflow_extensions", "project-one/custom-fixture", 1, d)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.openWorkspace("workflow_extensions", "project-two/custom-fixture", 1, sealed); e == nil {
		t.Fatal("cross project decrypted")
	}
	state := Obj{"revision": 1, "definitions": Obj{"custom-fixture": Obj{"metadata": workflowExtensionMetadata(d), "payload": sealed, "installed": true}}}
	snapshot := workflowExtensionSnapshot("project-one", state)
	encoded := string(encoded(snapshot))
	if strings.Contains(encoded, "function adapt") || strings.Contains(encoded, "payload") {
		t.Fatal("catalog leaks source")
	}
	sources := map[string]int{}
	for _, v := range arr(snapshot["items"]) {
		sources[str(obj(v)["source"])]++
	}
	if sources["jmeter"] < 100 || sources["platform"] != 2 || sources["project"] != 1 {
		t.Fatal(sources)
	}
}

func TestMySQLWorkflowExtensionsLifecycleIsolationCASAndHTTP(t *testing.T) {
	p, _ := mysqlFixture(t)
	ctx := context.Background()
	created, e := p.mutateWorkflowExtension(ctx, "", "POST", Obj{"revision": 0, "definition": workflowExtensionFixture(), "installed": true})
	if e != nil || number(created["revision"]) != 1 {
		t.Fatal(created, e)
	}
	id := str(created["created_id"])
	noChange, err := p.mutateWorkflowExtension(ctx, id, "PUT", Obj{"revision": 1, "installed": true})
	if err != nil || number(noChange["revision"]) != 1 {
		t.Fatal("no-op install advanced revision", noChange, err)
	}
	project, err := p.projectGetQ(p.DB, p.projectID(), false)
	if err != nil || strings.Contains(string(encoded(project)), "function adapt") {
		t.Fatal("source stored as plaintext", err)
	}

	read, e := p.getWorkflowExtension(ctx, id)
	if e != nil || obj(read["definition"])["id"] != id {
		t.Fatal(read, e)
	}
	other, e := p.projectCreate(Obj{"name": "other"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.forProject(str(other["id"])).getWorkflowExtension(ctx, id); e == nil {
		t.Fatal("cross project extension read")
	}
	if _, e := p.mutateWorkflowExtension(ctx, id, "PUT", Obj{"revision": 0, "installed": false}); e == nil {
		t.Fatal("stale update accepted")
	}
	if _, err := p.projectUpdate(str(other["id"]), Obj{"revision": other["revision"]}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.forProject(str(other["id"])).mutateWorkflowExtension(ctx, "", "POST", Obj{"revision": 0, "definition": workflowExtensionFixture()}); err == nil {
		t.Fatal("archived project mutation accepted")
	}
	_, call := workspaceHTTP(t, p)
	if _, err := p.workspaceCreate("workflows", Obj{"name": "usage fixture", "graph": basicWorkflowGraph()}); err != nil {
		t.Fatal(err)
	}
	if code, value := call("GET", "/api/workflow-extensions/usage", nil); code != 200 || value["project_id"] != p.projectID() || len(arr(value["items"])) != 3 {
		t.Fatal(code, value)
	}
	if code, value := call("POST", "/api/workflow-extensions/expand", Obj{"component": "JSR223Sampler", "raw_xml": `<JSR223Sampler/><hashTree><JSR223Assertion/><hashTree/></hashTree>`}); code != 200 || value["graph"] == nil {
		t.Fatal(code, value)
	}

	if code, value := call("PUT", "/api/workflow-extensions/"+id, Obj{"revision": 1, "installed": false}); code != 200 || number(value["revision"]) != 2 {
		t.Fatal(code, value)
	}
	if code, value := call("DELETE", "/api/workflow-extensions/"+id, Obj{"revision": 1}); code != 409 {
		t.Fatal(code, value)
	}
	if code, value := call("DELETE", "/api/workflow-extensions/"+id, Obj{"revision": 2}); code != 200 || number(value["revision"]) != 3 {
		t.Fatal(code, value)
	}
}

func TestWorkflowNativeOfficialDeserialization(t *testing.T) {
	home := os.Getenv("JMETER_NATIVE_TEST_HOME")
	if home == "" {
		t.Skip("set JMETER_NATIVE_TEST_HOME to pinned official JMeter for deserialize-only acceptance")
	}
	dir := t.TempDir()
	for _, c := range workflowcatalog.Native {
		if c.Kind == "plan" {
			continue
		}
		g := basicWorkflowGraph()
		n := graphNode("native", c.Component, nil)
		parent := "group"
		if c.Kind == "thread" || c.Component == "TestFragmentController" {
			parent = "root"
		}
		g["nodes"] = append(arr(g["nodes"]), n)
		g["edges"] = append(arr(g["edges"]), graphEdge(parent, "native", 1))
		xml, e := compileWorkflow(g)
		if e != nil {
			t.Fatal(c.Component, e)
		}
		if e = os.WriteFile(filepath.Join(dir, c.Component+".jmx"), []byte(xml), 0600); e != nil {
			t.Fatal(e)
		}
	}
	cp := filepath.Join(home, "lib", "*") + string(os.PathListSeparator) + filepath.Join(home, "lib", "ext", "*")
	compile := exec.Command("javac", "-cp", cp, "-d", dir, "../../tools/jmeter-catalog/VerifyNative.java")
	if out, e := compile.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	verify := exec.Command("java", "-Djava.awt.headless=true", "-cp", dir+string(os.PathListSeparator)+cp, "VerifyNative", home, dir)
	if out, e := verify.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	} else {
		t.Log(string(out))
	}
}

func TestWorkflowExpandNativeLegacySubtree(t *testing.T) {
	result, e := expandNativeWorkflowNode(Obj{"component": "JSR223Sampler", "raw_xml": `<JSR223Sampler testname="parent"><stringProp name="script">keep</stringProp></JSR223Sampler><hashTree><JSR223Assertion testname="assert"><stringProp name="script">assert true</stringProp></JSR223Assertion><hashTree/></hashTree>`})
	if e != nil {
		t.Fatal(e)
	}
	g := obj(result["graph"])
	if len(arr(g["nodes"])) != 4 || str(result["node_id"]) == "" {
		t.Fatal("nested component not expanded")
	}
	if _, e = compileWorkflow(g); e != nil {
		t.Fatal(e)
	}
	if _, e = expandNativeWorkflowNode(Obj{"component": "vendor.Unknown", "raw_xml": "<vendor.Unknown/><hashTree/>"}); e == nil {
		t.Fatal("custom subtree expanded without native support")
	}
}

func TestWorkflowExtensionUsageRetainedSubtrees(t *testing.T) {
	graph := Obj{"nodes": []any{Obj{"properties": Obj{"component": "example.Custom", "raw_xml": `<example.Custom/><hashTree><JSR223Sampler/><hashTree><JSR223Assertion/><hashTree/></hashTree></hashTree>`}}}}
	components := workflowUsageComponents(graph)
	if strings.Join(components, ",") != "example.Custom,JSR223Sampler,JSR223Assertion" {
		t.Fatal(components)
	}
}

func TestWorkflowNativeStructuredDouble(t *testing.T) {
	roots, err := parseXMLFragment(`<ConstantThroughputTimer><doubleProp><name>throughput</name><value>12.5</value><savedValue>0.0</savedValue></doubleProp><intProp name="calcMode">4</intProp></ConstantThroughputTimer>`)
	if err != nil {
		t.Fatal(err)
	}
	config := readComponentConfig(roots[0], "ConstantThroughputTimer")
	if config["throughput"] != "12.5" {
		t.Fatal(config)
	}
	roots[0].setProp("doubleProp", "throughput", "99.5")
	if roots[0].propText("throughput") != "99.5" || !strings.Contains(elementXML(roots[0]), "<savedValue>0.0</savedValue>") {
		t.Fatal(elementXML(roots[0]))
	}
}

func TestWorkflowCatalogDoesNotClaimRuntimeVerification(t *testing.T) {
	for _, value := range arr(workflowExtensionSnapshot("project-one", Obj{"revision": 0, "definitions": Obj{}})["items"]) {
		item := obj(value)
		if item["editor_enabled"] != true || item["definition_available"] != true || item["runtime_status"] != "unknown" {
			t.Fatal("catalog confuses editor availability and runtime verification", item)
		}
	}
}
