package main

import (
	"os"
	"strings"
	"testing"
)

func graphNode(id, component string, config Obj) Obj {
	if config == nil {
		config = clone(workflowDefaults[component])
	}
	return Obj{"id": id, "type": "jmeter-node", "x": 100, "y": 100, "text": Obj{"value": component}, "properties": Obj{"component": component, "name": component, "enabled": true, "config": config}}
}
func graphEdge(parent, child string, order int) Obj {
	return Obj{"id": parent + "-" + child, "type": "polyline", "sourceNodeId": parent, "targetNodeId": child, "properties": Obj{"order": order}}
}
func basicWorkflowGraph() Obj {
	return Obj{"nodes": []any{graphNode("root", "TestPlan", nil), graphNode("group", "ThreadGroup", nil), graphNode("http", "HTTPSamplerProxy", nil)}, "edges": []any{graphEdge("root", "group", 0), graphEdge("group", "http", 0)}}
}
func findGraphNode(graph Obj, component string) Obj {
	for _, value := range arr(graph["nodes"]) {
		node := obj(value)
		if obj(node["properties"])["component"] == component {
			return node
		}
	}
	return nil
}
func TestWorkflowCompileComponentsExpressionsAndEscaping(t *testing.T) {
	graph := basicWorkflowGraph()
	group := obj(findGraphNode(graph, "ThreadGroup")["properties"])
	group["config"] = Obj{"numThreads": "${__P(threads,2)}", "rampTimeSeconds": "${ramp}", "loops": "${__P(loops,1)}", "durationSeconds": "", "delaySeconds": ""}
	sampler := obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])
	sampler["name"] = "名称<>&\"中文"
	sampler["config"] = Obj{"method": "POST", "protocol": "http", "domain": "smoke-target", "port": "8080", "path": "/${path}?q=a&b=<c>", "bodyJson": "{\"value\":\"${variable}<>&中文\"}"}
	order := 1
	for _, component := range []string{"LoopController", "IfController", "TransactionController", "HeaderManager", "Arguments", "CSVDataSet", "ConstantTimer", "ResponseAssertion", "JSONPostProcessor"} {
		node := graphNode("extra-"+component, component, nil)
		parent := "group"
		if contains(workflowLeaves, component) {
			parent = "http"
		}
		graph["nodes"] = append(arr(graph["nodes"]), node)
		graph["edges"] = append(arr(graph["edges"]), graphEdge(parent, str(node["id"]), order))
		order++
	}
	assertion := obj(findGraphNode(graph, "ResponseAssertion")["properties"])
	assertion["config"] = Obj{"testField": "response_code", "matchType": "equals", "patterns": []any{"200", "<>&"}, "not": true}
	exported, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(exported, "${__P(threads,2)}") || !strings.Contains(exported, "&lt;&gt;&amp;") || strings.Contains(exported, `name="ThreadGroup.duration"`) || strings.Contains(exported, `name="ThreadGroup.delay"`) {
		t.Fatal("expression, escaping or optional value lost", exported)
	}
	roundTrip, e := importWorkflowGraph(exported)
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(roundTrip["nodes"])) != 12 {
		t.Fatal("missing component", len(arr(roundTrip["nodes"])))
	}
	restored := obj(findGraphNode(roundTrip, "HTTPSamplerProxy")["properties"])
	if string(encoded(restored["config"])) != string(encoded(sampler["config"])) || restored["name"] != sampler["name"] {
		t.Fatal("sampler config changed", restored)
	}
	if string(encoded(obj(findGraphNode(roundTrip, "ThreadGroup")["properties"])["config"])) != string(encoded(group["config"])) {
		t.Fatal("thread expressions changed")
	}
	if string(encoded(obj(findGraphNode(roundTrip, "ResponseAssertion")["properties"])["config"])) != string(encoded(assertion["config"])) {
		t.Fatal("assertion masks/field changed")
	}
}
func TestWorkflowLosslessKnownAndOpaqueUnknownImportExport(t *testing.T) {
	source := `<?xml version="1.0"?><jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3" custom="wrapper-kept" xmlns:p="urn:plugin"><hashTree><TestPlan testname="测试" enabled="true" custom="root"><stringProp name="TestPlan.comments">private &amp; preserved</stringProp></TestPlan><hashTree><ThreadGroup testname="线程" enabled="true" custom="group"><stringProp name="ThreadGroup.num_threads">${threads}</stringProp><elementProp name="ThreadGroup.main_controller" elementType="LoopController" custom="controller"><stringProp name="LoopController.loops">${loops}</stringProp><stringProp name="plugin.setting">unsupported-main</stringProp></elementProp></ThreadGroup><hashTree><HTTPSamplerProxy testname="HTTP" enabled="true" custom="sampler"><stringProp name="HTTPSampler.path">/old</stringProp><stringProp name="HTTPSampler.connect_timeout">4321</stringProp><elementProp name="HTTPsampler.Arguments" elementType="Arguments"><collectionProp name="Arguments.arguments"><elementProp name="query" elementType="HTTPArgument" custom="argument"><stringProp name="Argument.name">q</stringProp><stringProp name="Argument.value">keep</stringProp><stringProp name="plugin.extra">query-extra</stringProp></elementProp></collectionProp></elementProp></HTTPSamplerProxy><hashTree><HeaderManager testname="请求头" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp name="h" elementType="Header" custom="header"><stringProp name="Header.name">X-Name</stringProp><stringProp name="Header.value">old</stringProp><stringProp name="Plugin.header">header-extra</stringProp></elementProp></collectionProp></HeaderManager><hashTree></hashTree></hashTree><com.example.CustomController testname="插件" enabled="false" custom="opaque"><p:extension p:flag="kept">opaque &lt;value&gt;</p:extension></com.example.CustomController><hashTree><!-- keep plugin child --><com.example.Child testname="子组件" enabled="true"><stringProp name="secret">unchanged</stringProp></com.example.Child><hashTree></hashTree></hashTree></hashTree></hashTree></hashTree></jmeterTestPlan>`
	graph, e := importWorkflowGraph(source)
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(graph["nodes"])) != 5 {
		t.Fatal("opaque child exploded into graph", len(arr(graph["nodes"])))
	}
	httpNode := findGraphNode(graph, "HTTPSamplerProxy")
	obj(obj(httpNode["properties"])["config"])["path"] = "/changed?a=1&b=2"
	header := findGraphNode(graph, "HeaderManager")
	obj(arr(obj(obj(header["properties"])["config"])["headers"])[0])["value"] = "new"
	unknown := findGraphNode(graph, "com.example.CustomController")
	if unknown == nil || !strings.Contains(str(obj(unknown["properties"])["raw_xml"]), "com.example.Child") {
		t.Fatal("missing opaque subtree")
	}
	exported, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	for _, expected := range []string{`custom="wrapper-kept"`, `xmlns:p="urn:plugin"`, `custom="root"`, `custom="group"`, `custom="sampler"`, `custom="controller"`, "unsupported-main", "HTTPSampler.connect_timeout", "4321", "query-extra", "header-extra", `custom="header"`, "com.example.Child", "keep plugin child", `p:flag="kept"`, `enabled="false"`, `/changed?a=1&amp;b=2`} {
		if !strings.Contains(exported, expected) {
			t.Errorf("lost unsupported XML %q", expected)
		}
	}
	again, e := importWorkflowGraph(exported)
	if e != nil {
		t.Fatal(e)
	}
	if obj(obj(findGraphNode(again, "HTTPSamplerProxy")["properties"])["config"])["path"] != "/changed?a=1&b=2" {
		t.Fatal("edited config not applied")
	}
}
func TestWorkflowRejectsInvalidGraphsAndXML(t *testing.T) {
	cases := map[string]func(Obj){"orphan": func(g Obj) { g["edges"] = arr(g["edges"])[:1] }, "duplicate-order": func(g Obj) {
		g["nodes"] = append(arr(g["nodes"]), graphNode("http2", "HTTPSamplerProxy", nil))
		g["edges"] = append(arr(g["edges"]), graphEdge("group", "http2", 0))
	}, "bad-parent": func(g Obj) { obj(arr(g["edges"])[1])["sourceNodeId"] = "root" }, "duplicate-root": func(g Obj) { g["nodes"] = append(arr(g["nodes"]), graphNode("root2", "TestPlan", nil)) }, "bad-number-type": func(g Obj) { obj(obj(findGraphNode(g, "ThreadGroup")["properties"])["config"])["numThreads"] = 2 }, "unknown-no-raw": func(g Obj) { obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])["component"] = "missing.Plugin" }, "cycle": func(g Obj) {
		g["nodes"] = append(arr(g["nodes"]), graphNode("loop1", "LoopController", nil), graphNode("loop2", "LoopController", nil))
		g["edges"] = append(arr(g["edges"]), graphEdge("loop1", "loop2", 0), graphEdge("loop2", "loop1", 0))
	}, "unsafe-raw": func(g Obj) {
		obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])["raw_xml"] = `<!DOCTYPE x><HTTPSamplerProxy/>`
	}}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			g := basicWorkflowGraph()
			mutate(g)
			if _, e := compileWorkflow(g); e == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
	for _, raw := range []string{`<!DOCTYPE jmeterTestPlan><jmeterTestPlan/>`, `<jmeterTestPlan><hashTree><TestPlan/></hashTree></jmeterTestPlan>`, `<jmeterTestPlan><hashTree><TestPlan/><hashTree/></hashTree></bad>`} {
		if _, e := importWorkflowGraph(raw); e == nil {
			t.Fatal("invalid JMX accepted", raw)
		}
	}
}
func TestWorkflowImportsExistingInternalSmokePlan(t *testing.T) {
	source, e := os.ReadFile("../../plans/platform-smoke.jmx")
	if e != nil {
		t.Fatal(e)
	}
	graph, e := importWorkflowGraph(string(source))
	if e != nil {
		t.Fatal(e)
	}
	xml, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	for _, expected := range []string{"smoke-target", "/jmeter-smoke", "${__P(loops,5)}", "${__P(engine_id,unknown)}", "Assertion.response_data", "HTTPSampler.connect_timeout"} {
		if !strings.Contains(xml, expected) {
			t.Errorf("missing smoke property %s", expected)
		}
	}
}
func TestWorkspaceAuthenticatedEncryptionAndExactJSON(t *testing.T) {
	p := &Platform{Config: Config{SessionKey: "private-key"}}
	data := Obj{"id": "collection-x", "revision": 1, "document": Obj{"openapi": "3.1.0", "x-scalar-value": Obj{"nested": []any{Obj{"secret": "do-not-store-plain"}}}}, "client_state": Obj{"auth": Obj{"token": "business-secret"}, "request_mode": "master"}}
	one, e := p.sealWorkspace("api_collections", "collection-x", 1, data)
	if e != nil {
		t.Fatal(e)
	}
	two, e := p.sealWorkspace("api_collections", "collection-x", 1, data)
	if e != nil {
		t.Fatal(e)
	}
	if one == two || strings.Contains(one, "business-secret") || strings.Contains(one, "do-not-store-plain") {
		t.Fatal("encryption nonce or confidentiality failure")
	}
	read, e := p.openWorkspace("api_collections", "collection-x", 1, one)
	if e != nil || string(encoded(read)) != string(encoded(data)) {
		t.Fatal(read, e)
	}
	for _, test := range []struct {
		table, id string
		revision  int64
	}{{"workflows", "collection-x", 1}, {"api_collections", "collection-y", 1}, {"api_collections", "collection-x", 2}} {
		if _, e = p.openWorkspace(test.table, test.id, test.revision, one); e == nil {
			t.Fatal("ciphertext context not authenticated")
		}
	}
	p.Config.SessionKey = "other-key"
	if _, e = p.openWorkspace("api_collections", "collection-x", 1, one); e == nil {
		t.Fatal("wrong key accepted")
	}
}
