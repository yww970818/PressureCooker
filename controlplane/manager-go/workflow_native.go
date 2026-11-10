package main

import (
	"pressurecooker/manager/internal/workflowcatalog"
	"strings"
)

var genericNative = map[string]bool{}
var newlyAdaptedNative = map[string]bool{}

func init() {
	for name := range workflowcatalog.ByName {
		if _, exists := workflowDefaults[name]; !exists {
			newlyAdaptedNative[name] = true
		}
	}

	for _, name := range []string{"JSR223Sampler", "JSR223Assertion", "JSR223PreProcessor", "JSR223PostProcessor", "JSR223Timer", "JSR223Listener"} {
		workflowDefaults[name] = Obj{"scriptLanguage": "groovy", "script": "", "filename": "", "parameters": "", "cacheKey": "true"}
		componentFields[name] = []componentField{{"scriptLanguage", "scriptLanguage", "stringProp", false}, {"script", "script", "stringProp", false}, {"filename", "filename", "stringProp", false}, {"parameters", "parameters", "stringProp", false}, {"cacheKey", "cacheKey", "stringProp", false}}
		componentGUI[name] = "TestBeanGUI"
	}
	workflowDefaults["ConstantThroughputTimer"] = Obj{"throughput": "60", "calcMode": "1"}
	componentFields["ConstantThroughputTimer"] = []componentField{{"throughput", "throughput", "doubleProp", false}, {"calcMode", "calcMode", "intProp", false}}
	componentGUI["ConstantThroughputTimer"] = "TestBeanGUI"
	for name, c := range workflowcatalog.ByName {
		if _, exists := workflowDefaults[name]; !exists {
			workflowDefaults[name] = Obj{"properties_xml": c.TemplateProperties}
			genericNative[name] = true
			componentGUI[name] = c.GUI
		}
	}
	for _, c := range workflowcatalog.Native {
		for _, name := range []string{c.Component, c.ClassName} {
			if c.Kind == "controller" {
				workflowControllers = append(workflowControllers, name)
			}
			if c.Kind != "controller" && c.Kind != "thread" && c.Kind != "sampler" && c.Kind != "plan" {
				workflowLeaves = append(workflowLeaves, name)
			}
		}
	}
}

func workflowKind(name string) string {
	if c, ok := workflowcatalog.ByName[name]; ok {
		return c.Kind
	}
	if name == "WebSocketSampler" {
		return "sampler"
	}
	return "custom"
}

func workflowComponentNameIs(name, component string) bool {
	if c, ok := workflowcatalog.ByName[name]; ok {
		return c.Component == component
	}
	return name == component
}

func nativePropertyXML(e *xmlElement) string {
	copy := e.copy()
	copy.Name.Local = "properties"
	copy.Attrs = nil
	raw := elementXML(copy)
	start, end := strings.Index(raw, ">"), strings.LastIndex(raw, "</properties>")
	if start < 0 || end < start {
		return ""
	}
	return raw[start+1 : end]
}

func parseNativeProperties(raw string) (*xmlElement, error) {
	root, err := parseXMLFragment("<properties>" + raw + "</properties>")
	if err != nil {
		return nil, err
	}
	if len(root) != 1 || root[0].Name.Local != "properties" {
		return nil, api(400, "invalid_component_properties", "属性不能包含额外根元素")
	}
	for _, child := range root[0].children() {

		if !contains([]string{"stringProp", "boolProp", "intProp", "longProp", "doubleProp", "floatProp", "FloatProperty", "collectionProp", "elementProp", "mapProp", "objProp"}, child.Name.Local) || child.propertyName() == "" {
			return nil, api(400, "invalid_component_properties", "属性树只接受带name的JMeter属性元素")
		}
	}
	return root[0], nil
}

func patchNativeProperties(e *xmlElement, config Obj) {
	if raw, supplied := config["properties_xml"].(string); supplied && raw != nativePropertyXML(e) {
		// Validation already parsed this inert XML. Preserve root attributes and
		// the independently retained hashTree, replacing only property contents.
		root, err := parseNativeProperties(raw)
		if err == nil {
			e.Parts = root.Parts
		}
	}
}

// Explicit editor operation. Historical workflow bytes are never rewritten on
// read; this returns a replacement node/subgraph for a user-authored edit.
func expandNativeWorkflowNode(body Obj) (Obj, error) {
	component := str(body["component"])
	if !newlyAdaptedNative[component] {
		return nil, api(400, "invalid_component", "此组件无需展开原生子流程")
	}
	roots, e := parseXMLFragment(str(body["raw_xml"]))
	if e != nil {
		return nil, e
	}
	if len(roots) != 2 || xmlName(roots[0].Name) != component || roots[1].Name.Local != "hashTree" {
		return nil, api(400, "invalid_component", "未找到保留的原生子流程")
	}
	wrapper := element("jmeterTestPlan")
	top := element("hashTree")
	wrapper.append(top)
	top.append(newComponent("TestPlan"))
	scope := element("hashTree")
	top.append(scope)
	if workflowKind(component) == "thread" || workflowComponentNameIs(component, "TestFragmentController") {
		scope.append(roots[0])
		scope.append(roots[1])
	} else {
		scope.append(newComponent("ThreadGroup"))
		nested := element("hashTree")
		nested.append(roots[0])
		nested.append(roots[1])
		scope.append(nested)
	}
	graph, e := importWorkflowGraph(elementXML(wrapper))
	if e != nil {
		return nil, e
	}
	for _, v := range arr(graph["nodes"]) {
		n := obj(v)
		if str(obj(n["properties"])["component"]) == component {
			return Obj{"graph": graph, "node_id": n["id"]}, nil
		}
	}
	return nil, api(400, "invalid_component", "无法展开组件")
}
