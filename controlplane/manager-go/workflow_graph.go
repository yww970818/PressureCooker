package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

var workflowDefaults = map[string]Obj{
	"WebSocketSampler": {"action": "connect", "connectionId": "ws-main", "url": "", "socketio": false, "handshakeUrl": "", "headers": []any{}, "protocols": "", "message": "", "receiveMode": "any", "matchText": "", "matchJson": "{}", "timeoutMs": "10000", "heartbeatEnabled": false, "heartbeatIntervalMs": "8000", "heartbeatMessage": ""},
	"TestPlan":         {}, "ThreadGroup": {"numThreads": "1", "rampTimeSeconds": "1", "loops": "1", "durationSeconds": "", "delaySeconds": ""},
	"HTTPSamplerProxy": {"method": "GET", "protocol": "http", "domain": "", "port": "", "path": "/", "bodyJson": ""}, "LoopController": {"loops": "1"}, "IfController": {"condition": "", "evaluateAll": false}, "TransactionController": {"includeTimers": false}, "HeaderManager": {"headers": []any{}}, "Arguments": {"variables": []any{}}, "CSVDataSet": {"filename": "", "variableNames": "", "delimiter": ",", "ignoreFirstLine": false, "recycle": true, "stopThread": false}, "ConstantTimer": {"delayMs": "0"}, "ResponseAssertion": {"testField": "response_data", "matchType": "contains", "patterns": []any{}, "not": false}, "JSONPostProcessor": {"referenceName": "", "jsonPathExpr": "", "defaultValue": "NOT_FOUND"},
}
var workflowControllers = []string{"LoopController", "IfController", "TransactionController"}
var workflowLeaves = []string{"HeaderManager", "Arguments", "CSVDataSet", "ConstantTimer", "ResponseAssertion", "JSONPostProcessor"}

type workflowNode struct {
	ID, Component, Name, Raw string
	Enabled                  bool
	Config                   Obj
	Source                   Obj
	Children                 []*workflowNode
}
type workflowTree struct {
	Root  *workflowNode
	Nodes map[string]*workflowNode
}

func componentConfig(component string, config Obj) (Obj, error) {
	defaults, known := workflowDefaults[component]
	if !known {
		if len(config) == 0 {
			return Obj{}, nil
		}
		if len(config) > 0 && (len(config) != 1 || config["properties_xml"] == nil) {
			return nil, api(400, "opaque_component", "未知组件仅支持原始XML保留")
		}
		defaults = Obj{"properties_xml": ""}
	}
	out := clone(defaults)
	for key, value := range config {
		expected, ok := defaults[key]
		if !ok {
			return nil, api(400, "invalid_config", "组件配置字段不受支持："+key)
		}
		switch expected.(type) {
		case string:
			s, ok := value.(string)
			if !ok || !validXMLText(s) || len(s) > workspaceLimit {
				return nil, api(400, "invalid_config", "配置值须为不含空字符的字符串")
			}
		case bool:
			if _, ok := value.(bool); !ok {
				return nil, api(400, "invalid_config", "开关配置必须是布尔值")
			}
		case []any:
			values, ok := value.([]any)
			if !ok || len(values) > 1000 {
				return nil, api(400, "invalid_config", "列表配置无效")
			}
			sourceIDs := map[string]bool{}
			for _, item := range values {
				if key == "patterns" {
					if text, ok := item.(string); !ok || !validXMLText(text) {
						return nil, api(400, "invalid_config", "断言模式必须是字符串")
					}
				} else {
					pair, ok := item.(map[string]any)
					if !ok {
						return nil, api(400, "invalid_config", "变量和请求头必须包含name/value")
					}
					if text, ok := pair["name"].(string); !ok || !validXMLText(text) {
						return nil, api(400, "invalid_config", "变量和请求头名称必须是字符串")
					}

					if source, exists := pair["_source_id"]; exists {
						sourceID, ok := source.(string)
						if !ok || len(sourceID) > 128 || sourceIDs[sourceID] {
							return nil, api(400, "invalid_config", "变量或请求头来源标识无效或重复")
						}
						if sourceID != "" {
							sourceIDs[sourceID] = true
						}
					}
					if text, ok := pair["value"].(string); !ok || !validXMLText(text) {
						return nil, api(400, "invalid_config", "变量和请求头值必须是字符串")
					}
				}
			}
		}
		if key == "properties_xml" {
			if _, err := parseNativeProperties(str(value)); err != nil {
				return nil, err
			}
		}
		out[key] = value
	}
	if component == "HTTPSamplerProxy" && contains([]string{"rpc", "grpc", "protobuf"}, strings.ToLower(str(out["protocol"]))) {
		return nil, api(400, "interface_protocol_reserved", "RPC、gRPC与Protobuf尚未接入HTTP采样器执行")
	}
	if component == "ResponseAssertion" && !contains([]string{"contains", "matches", "equals", "substring"}, str(out["matchType"])) {
		return nil, api(400, "invalid_config", "断言匹配方式无效")
	}
	if component == "WebSocketSampler" {
		if e := websocketConfigValidate(out); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func graphNumber(v any) bool {
	switch v.(type) {
	case json.Number, float64, int, int64:
		f := number(v)
		return !math.IsInf(f, 0) && !math.IsNaN(f)
	}
	return false
}
func validateWorkflowGraph(graph Obj) (*workflowTree, error) {
	capacity, e := workflowCapacity()
	if e != nil {
		return nil, api(500, "workflow_capacity_config", "工作流容量配置无效")
	}
	nodes, ok := graph["nodes"].([]any)
	if !ok || len(nodes) < 1 || len(nodes) > capacity.Nodes {
		return nil, api(400, "invalid_graph", fmt.Sprintf("工作流须包含1至%d个节点", capacity.Nodes))
	}
	edges, ok := graph["edges"].([]any)
	if !ok || len(edges) > capacity.Edges {
		return nil, api(400, "invalid_graph", "工作流连线数据无效")
	}
	tree := &workflowTree{Nodes: map[string]*workflowNode{}}
	for _, value := range nodes {
		source, ok := value.(map[string]any)
		if !ok {
			return nil, api(400, "invalid_graph", "节点数据必须是对象")
		}
		id := str(source["id"])
		if validID(id) != nil || tree.Nodes[id] != nil {
			return nil, api(400, "invalid_graph", "节点ID无效或重复")
		}
		if source["type"] != "jmeter-node" || !graphNumber(source["x"]) || !graphNumber(source["y"]) {
			return nil, api(400, "invalid_graph", "节点类型或坐标无效")
		}
		props, ok := source["properties"].(map[string]any)
		if !ok {
			return nil, api(400, "invalid_graph", "节点缺少组件属性")
		}
		if _, partial := props["editor_lazy"]; partial {
			return nil, api(400, "incomplete_graph", "节点参数尚未加载，不能保存或编译不完整流程")
		}
		component, name := str(props["component"]), str(props["name"])
		enabled, ok := props["enabled"].(bool)
		if component == "" || strings.TrimSpace(name) == "" || !validXMLText(name) || utf8.RuneCountInString(name) > 200 || !ok {
			return nil, api(400, "invalid_graph", "组件类型、名称或启用状态无效")
		}
		config, ok := props["config"].(map[string]any)
		if !ok {
			return nil, api(400, "invalid_graph", "组件配置必须是对象")
		}
		normalized, e := componentConfig(component, config)
		if e != nil {
			return nil, e
		}
		raw, hasRaw := props["raw_xml"]
		if hasRaw {
			if _, ok := raw.(string); !ok {
				return nil, api(400, "invalid_graph", "raw_xml必须是字符串")
			}
		}
		if _, known := workflowDefaults[component]; !known && str(raw) == "" {
			return nil, api(400, "opaque_component", "未知组件必须保留原始XML")
		}
		node := &workflowNode{ID: id, Component: component, Name: name, Raw: str(raw), Enabled: enabled, Config: normalized, Source: source}
		tree.Nodes[id] = node
		if component == "TestPlan" {
			if tree.Root != nil {
				return nil, api(400, "invalid_graph", "只能存在一个TestPlan根节点")
			}
			tree.Root = node
		}
	}
	if tree.Root == nil {
		return nil, api(400, "invalid_graph", "工作流缺少TestPlan根节点")
	}
	incoming := map[string]int{}
	orders := map[string]map[int64]bool{}
	edgeIDs := map[string]bool{}
	type ordered struct {
		order int64
		node  *workflowNode
	}
	children := map[string][]ordered{}
	for _, value := range edges {
		edge, ok := value.(map[string]any)
		if !ok {
			return nil, api(400, "invalid_graph", "连线必须是对象")
		}
		id := str(edge["id"])
		if validID(id) != nil || edgeIDs[id] || edge["type"] != "polyline" {
			return nil, api(400, "invalid_graph", "连线ID或类型无效")
		}
		edgeIDs[id] = true
		parent, child := tree.Nodes[str(edge["sourceNodeId"])], tree.Nodes[str(edge["targetNodeId"])]
		if parent == nil || child == nil || parent == child {
			return nil, api(400, "invalid_graph", "连线端点无效")
		}
		order, ok := integer(obj(edge["properties"])["order"])
		if !ok || order < 0 || order > 1000000 {
			return nil, api(400, "invalid_graph", "连线顺序必须是非负整数")
		}
		if orders[parent.ID] == nil {
			orders[parent.ID] = map[int64]bool{}
		}
		if orders[parent.ID][order] {
			return nil, api(400, "invalid_graph", "同一父节点的连线顺序不可重复")
		}
		orders[parent.ID][order] = true
		incoming[child.ID]++
		if child.Component == "TestPlan" || incoming[child.ID] > 1 {
			return nil, api(400, "invalid_graph", "根节点不能有入线，其他节点只能有一个父节点")
		}
		_, parentKnown := workflowDefaults[parent.Component]
		if !parentKnown || (contains(workflowLeaves, parent.Component) && workflowKind(parent.Component) != "sampler") {
			return nil, api(400, "invalid_graph", "配置、处理器和未知组件必须是叶节点")
		}
		if workflowKind(child.Component) == "thread" && parent.Component != "TestPlan" {
			return nil, api(400, "invalid_graph", "线程组只能位于TestPlan之下")
		}
		if workflowComponentNameIs(child.Component, "TestFragmentController") {
			if parent.Component != "TestPlan" {
				return nil, api(400, "invalid_graph", "测试片段只能位于TestPlan之下")
			}
		} else if workflowKind(child.Component) == "sampler" || contains(workflowControllers, child.Component) {
			if workflowKind(parent.Component) != "thread" && !contains(workflowControllers, parent.Component) {
				return nil, api(400, "invalid_graph", "控制器、HTTP 和 WS 请求须位于线程组或控制器之下")
			}
		}
		children[parent.ID] = append(children[parent.ID], ordered{order, child})
	}
	for id, node := range tree.Nodes {
		if node != tree.Root && incoming[id] != 1 {
			return nil, api(400, "invalid_graph", "存在未连接到TestPlan的孤立节点")
		}
		orderedChildren := children[id]
		sort.Slice(orderedChildren, func(i, j int) bool { return orderedChildren[i].order < orderedChildren[j].order })
		for _, entry := range orderedChildren {
			node.Children = append(node.Children, entry.node)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(*workflowNode) error
	visit = func(node *workflowNode) error {
		if visiting[node.ID] {
			return api(400, "invalid_graph", "工作流不允许循环连线")
		}
		if visited[node.ID] {
			return nil
		}
		visiting[node.ID] = true
		for _, child := range node.Children {
			if e := visit(child); e != nil {
				return e
			}
		}
		visiting[node.ID] = false
		visited[node.ID] = true
		return nil
	}
	if e := visit(tree.Root); e != nil {
		return nil, e
	}
	if len(visited) != len(tree.Nodes) {
		return nil, api(400, "invalid_graph", "存在循环或无法从根节点到达的节点")
	}
	return tree, nil
}
