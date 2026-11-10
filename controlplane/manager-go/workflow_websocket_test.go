package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func websocketWorkflowGraph() Obj {
	g := basicWorkflowGraph()
	p := obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])
	p["component"] = "WebSocketSampler"
	p["name"] = "WS 测试"
	p["config"] = Obj{"action": "request", "connectionId": "main", "url": "ws://fixture:8080/ws", "message": `{"value":"{{value}}"}`, "timeoutMs": "1000", "receiveMode": "json", "matchJson": `{"$.frame.event":"reply"}`}
	return g
}

// Opt-in local artifact generation for a real JMeter run against the isolated
// synthetic fixture. It never calls or mutates a business environment.
func TestWorkflowWebSocketFixturePlans(t *testing.T) {
	dir := os.Getenv("PC_WS_FIXTURE_DIR")
	if dir == "" {
		t.Skip("synthetic runtime verification is opt-in")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	pure := strings.Split(workflowWebSocketRuntime, "\nMap config=PCWorkflowWs.parse")[0] + `
def p=new Properties()
def quoted='a"b\\c\n中文'
def message=PCWorkflowWs.message('{"q":"{{q}}","n":{{n}}}',[q:quoted,n:'7'],p)
assert PCWorkflowWs.parse(message)==[q:quoted,n:7]
assert PCWorkflowWs.parse(PCWorkflowWs.message('{"SER":"{{ser}}","time":"{{now}}","custom":"{{custom_id}}"}',[ser:'3',now:'123456789',custom_id:'42'],p))==[SER:3,time:123456789,custom:"42"]
assert PCWorkflowWs.parse(PCWorkflowWs.message('{"custom":"{{custom_id}}"}',[custom_id:'42'],p,false)).custom=='42'
def nested=JsonOutput.toJson([v:' \n{"question":"{{q}}","number":{{n}}}'])
assert PCWorkflowWs.parse(PCWorkflowWs.parse(PCWorkflowWs.message(nested,[q:quoted,n:'7'],p)).v)==[question:quoted,number:7]
['1,2',''].each { value -> assert PCWorkflowWs.parse(PCWorkflowWs.message('{"v":{{value}}}',[value:value],p)).v==value }
assert PCWorkflowWs.message(' raw\tframe\n',[:],p)==' raw\tframe\n'
def c=new PCWsConnection('pure',false,1000,p)
c.variables=[custom_id:'own-id',value:'own']
assert c.values(false).custom_id=='own-id'
c.lastSent=[message_id:'same',now:'same-time',ser:'9']; c.runtimeValues.last_ser='9'
assert c.values(false).message_id=='same' && c.values(false).ser=='9'
assert c.values(true).message_id!='same' && c.values(false).message_id=='same'
c.enqueue([raw:'notice',frame:[event:'notice']]); c.enqueue([raw:'reply',frame:[event:'reply']])
assert c.receive([mode:'json',rules:['$.frame.event':'reply']],0,100).raw=='reply'
assert c.receive([mode:'any'],-1,100).raw=='notice'
assert c.queueBytes.get()==0
c.dispose(false)
println 'Runtime pure assertions passed'
`
	if err := os.WriteFile(filepath.Join(dir, "runtime-pure.groovy"), []byte(pure), 0600); err != nil {
		t.Fatal(err)
	}
	base := func() Obj {
		return Obj{"nodes": []any{graphNode("root", "TestPlan", nil), graphNode("group", "ThreadGroup", Obj{"numThreads": "2", "rampTimeSeconds": "0", "loops": "1"})}, "edges": []any{graphEdge("root", "group", 0)}}
	}
	add := func(g Obj, id, kind, parent string, order int, config Obj) {
		g["nodes"] = append(arr(g["nodes"]), graphNode(id, kind, config))
		g["edges"] = append(arr(g["edges"]), graphEdge(parent, id, order))
	}
	assert := func(g Obj, id, parent, pattern string) {
		add(g, id, "ResponseAssertion", parent, 0, Obj{"testField": "response_data", "matchType": "substring", "patterns": []any{pattern}})
	}
	save := func(name string, g Obj) {
		content, err := compileWorkflow(g)
		if err != nil {
			t.Fatal(name, err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".jmx"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, name+".graph.json"), encoded(g), 0600)
	}
	g := base()
	add(g, "vars", "Arguments", "group", 0, Obj{"variables": []any{Obj{"name": "value", "value": "thread-${__threadNum}"}, Obj{"name": "quoted", "value": "a\"b\\c\n中文"}}})
	add(g, "connect", "WebSocketSampler", "group", 1, Obj{"action": "connect", "url": "ws://workflow-ws-fixture:8080/ws"})
	add(g, "request", "WebSocketSampler", "group", 2, Obj{"action": "request", "message": `{"command":"scenario","value":"{{value}}","quoted":"{{quoted}}"}`, "receiveMode": "json", "matchJson": `{"$.frame.event":"reply","$.frame.value":"{{value}}"}`})
	add(g, "extract", "JSONPostProcessor", "request", 0, Obj{"referenceName": "ws_value", "jsonPathExpr": "$.frame.value"})
	add(g, "http", "HTTPSamplerProxy", "group", 3, Obj{"domain": "workflow-ws-fixture", "port": "8080", "path": "/echo?value=${ws_value}"})
	assert(g, "assert-http", "http", `"value":"${value}"`)
	add(g, "read", "WebSocketSampler", "group", 4, Obj{"action": "read", "receiveMode": "json", "matchJson": `{"$.frame.event":"notice"}`})
	assert(g, "assert-notice", "read", `"event":"notice"`)
	add(g, "send", "WebSocketSampler", "group", 5, Obj{"action": "send", "message": " raw\tframe\n"})
	add(g, "raw", "WebSocketSampler", "group", 6, Obj{"action": "read", "receiveMode": "regex", "matchText": "^ raw\\tframe\\n$"})
	add(g, "close", "WebSocketSampler", "group", 7, Obj{"action": "close"})
	save("echo", g)
	g = base()
	add(g, "connect", "WebSocketSampler", "group", 0, Obj{"url": "ws://workflow-ws-fixture:8080/ws"})
	add(g, "timeout", "WebSocketSampler", "group", 1, Obj{"action": "read", "timeoutMs": "150"})
	save("timeout", g)
	g = base()
	add(g, "connect", "WebSocketSampler", "group", 0, Obj{"url": "ws://workflow-ws-fixture:8080/ws"})
	add(g, "request", "WebSocketSampler", "group", 1, Obj{"action": "request", "message": `{"value":"auto-cleanup"}`})
	add(g, "observer", "ThreadGroup", "root", 1, Obj{"numThreads": "1", "rampTimeSeconds": "0", "loops": "1"})
	add(g, "stats", "HTTPSamplerProxy", "observer", 0, Obj{"domain": "workflow-ws-fixture", "port": "8080", "path": "/stats"})
	add(g, "wait-cleanup", "ConstantTimer", "stats", 0, Obj{"delayMs": "5000"})
	add(g, "assert-cleanup", "ResponseAssertion", "stats", 1, Obj{"testField": "response_data", "matchType": "substring", "patterns": []any{`"active":0`}})
	save("cleanup", g)
}

func TestWorkflowWebSocketNativeCompileAndRoundTrip(t *testing.T) {
	g := websocketWorkflowGraph()
	content, err := compileWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "<JSR223Sampler") || !strings.Contains(content, `name="pc.websocket.version"`) || !strings.Contains(content, "java.net.http.WebSocket") {
		t.Fatal("missing executable native WebSocket sampler")
	}
	if strings.Contains(content, "<WebSocketSampler") {
		t.Fatal("compiled an unavailable JMeter plugin")
	}
	restored, err := importWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	node := findGraphNode(restored, "WebSocketSampler")
	if node == nil {
		t.Fatal("WS node not restored")
	}
	config := obj(obj(node["properties"])["config"])
	if config["message"] != `{"value":"{{value}}"}` || config["receiveMode"] != "json" {
		t.Fatal("authored template not preserved", config)
	}
	if _, err = compileWorkflow(restored); err != nil {
		t.Fatal(err)
	}
	// Templates are data, never interpolated into the cached Groovy source.
	roots, _ := parseXMLFragment(content)
	var walk func(*xmlElement)
	walk = func(e *xmlElement) {
		if e.Name.Local == "JSR223Sampler" {
			if strings.Contains(e.propText("script"), "${") {
				t.Error("JMeter will pre-expand cached script variables")
			}
			b, err := base64.StdEncoding.DecodeString(e.propText("pc.websocket.config"))
			if err != nil || !strings.Contains(string(b), "value") {
				t.Error("invalid config payload")
			}
		}
		for _, child := range e.children() {
			walk(child)
		}
	}
	walk(roots[0])
}

func TestWorkflowWebSocketScopeAndValidation(t *testing.T) {
	g := websocketWorkflowGraph()
	g["nodes"] = append(arr(g["nodes"]), graphNode("extract", "JSONPostProcessor", Obj{"referenceName": "ws_value", "jsonPathExpr": "$.frame.value", "defaultValue": "NOT_FOUND"}), graphNode("next", "HTTPSamplerProxy", Obj{"path": "/echo?value=${ws_value}"}))
	g["edges"] = append(arr(g["edges"]), graphEdge("http", "extract", 0), graphEdge("group", "next", 1))
	if _, err := compileWorkflow(g); err != nil {
		t.Fatal(err)
	}
	if !producedForNode(g, "next", "ws_value") {
		t.Fatal("WS JSON output not visible to next HTTP")
	}
	obj(arr(g["edges"])[3])["sourceNodeId"] = "http"
	if _, err := compileWorkflow(g); err == nil {
		t.Fatal("nested sampler accepted under WS")
	}
	for _, mutate := range []Obj{{"action": "oops"}, {"receiveMode": "oops"}, {"connectionId": ""}, {"timeoutMs": "-1"}} {
		g = websocketWorkflowGraph()
		for k, v := range mutate {
			obj(obj(findGraphNode(g, "WebSocketSampler")["properties"])["config"])[k] = v
		}
		if _, err := compileWorkflow(g); err == nil {
			t.Errorf("bad WS config accepted: %v", mutate)
		}
	}
}

func TestWorkflowWebSocketCollectionBindingInEncodedConfig(t *testing.T) {
	g := websocketWorkflowGraph()
	g["environment_binding"] = "base_host"
	g["environment_variables"] = []any{"value"}
	p := obj(findGraphNode(g, "WebSocketSampler")["properties"])
	p["api_reference"] = Obj{"collection_id": "collection-test", "revision": 1, "path": "/ws", "method": "get"}
	p["api_environment_variables"] = []any{"value", "ser", "session_id", "socket_id"}
	obj(p["config"])["message"] = `{"value":"{{value}}","ser":{{ser}},"session":{{session_id}},"transport":"{{socket_id}}"}`
	content, err := compileWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := workflowCollectionMetadata(content)
	if err != nil || len(bindings) != 1 {
		t.Fatal("missing WS binding", err)
	}
	b := obj(bindings[0])
	if b["transport"] != "ws" || string(encoded(b["names"])) != `["value","session_id"]` {
		t.Fatal("incorrect WS scope", b)
	}
	roots, _ := parseXMLFragment(content)
	if !strings.Contains(executableXMLText(roots[0]), apiEnvironmentReference("http", "value")) {
		t.Fatal("encoded config did not use scoped property")
	}
	restored, err := importWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	p = obj(findGraphNode(restored, "WebSocketSampler")["properties"])
	if obj(p["api_reference"])["collection_id"] != "collection-test" || !strings.Contains(str(obj(p["config"])["message"]), "{{value}}") {
		t.Fatal("WS reference lost", p)
	}
}

func TestWorkflowWebSocketOnlyActiveTemplatesRequireEnvironment(t *testing.T) {
	g := websocketWorkflowGraph()
	g["environment_binding"] = "base_host"
	p := obj(findGraphNode(g, "WebSocketSampler")["properties"])
	p["api_reference"] = Obj{"collection_id": "collection-test", "revision": 1, "path": "/ws", "method": "get"}
	p["config"] = Obj{"action": "connect", "url": "ws://fixture/ws", "heartbeatEnabled": false, "heartbeatIntervalMs": "180000", "heartbeatMessage": "{{unused_hb}}", "message": "{{unused_send}}", "headers": []any{Obj{"name": "Authorization", "value": "{{old_token}}", "enabled": false}}}
	content, err := compileWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := workflowCollectionMetadata(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(arr(obj(bindings[0])["names"])) != 0 {
		t.Fatal("disabled templates created false requirements", bindings)
	}
	obj(p["config"])["heartbeatEnabled"] = true
	obj(p["config"])["heartbeatMessage"] = "{}"
	if _, err = compileWorkflow(g); err != nil {
		t.Fatal("valid API debug heartbeat cannot be referenced", err)
	}
	obj(p["config"])["heartbeatIntervalMs"] = "300001"
	if _, err = compileWorkflow(g); err == nil {
		t.Fatal("unbounded heartbeat interval accepted")
	}
	obj(p["config"])["action"] = "send"
	obj(p["config"])["message"] = `{"token":"{{session_token}}","staff":"{{staff_id}}"}`
	content, err = compileWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err = workflowCollectionMetadata(content)
	if err != nil || len(arr(obj(bindings[0])["names"])) != 2 {
		t.Fatal("user-defined variables were not bound as environment", bindings, err)
	}
}
