package main

import (
	"fmt"
	"pressurecooker/manager/internal/workflowcatalog"
	"strconv"
	"strings"
)

type componentField struct {
	Key, Prop, Tag string
	Optional       bool
}

var componentFields = map[string][]componentField{
	"ThreadGroup":      {{"numThreads", "ThreadGroup.num_threads", "stringProp", false}, {"rampTimeSeconds", "ThreadGroup.ramp_time", "stringProp", false}, {"durationSeconds", "ThreadGroup.duration", "stringProp", true}, {"delaySeconds", "ThreadGroup.delay", "stringProp", true}},
	"HTTPSamplerProxy": {{"method", "HTTPSampler.method", "stringProp", false}, {"protocol", "HTTPSampler.protocol", "stringProp", false}, {"domain", "HTTPSampler.domain", "stringProp", false}, {"port", "HTTPSampler.port", "stringProp", true}, {"path", "HTTPSampler.path", "stringProp", false}},
	"LoopController":   {{"loops", "LoopController.loops", "stringProp", false}}, "IfController": {{"condition", "IfController.condition", "stringProp", false}, {"evaluateAll", "IfController.evaluateAll", "boolProp", false}}, "TransactionController": {{"includeTimers", "TransactionController.includeTimers", "boolProp", false}},
	"CSVDataSet":    {{"filename", "filename", "stringProp", false}, {"variableNames", "variableNames", "stringProp", false}, {"delimiter", "delimiter", "stringProp", false}, {"ignoreFirstLine", "ignoreFirstLine", "boolProp", false}, {"recycle", "recycle", "boolProp", false}, {"stopThread", "stopThread", "boolProp", false}},
	"ConstantTimer": {{"delayMs", "ConstantTimer.delay", "stringProp", false}}, "JSONPostProcessor": {{"referenceName", "JSONPostProcessor.referenceNames", "stringProp", false}, {"jsonPathExpr", "JSONPostProcessor.jsonPathExprs", "stringProp", false}, {"defaultValue", "JSONPostProcessor.defaultValues", "stringProp", false}},
}
var componentGUI = map[string]string{"TestPlan": "TestPlanGui", "ThreadGroup": "ThreadGroupGui", "HTTPSamplerProxy": "HttpTestSampleGui", "LoopController": "LoopControlPanel", "IfController": "IfControllerPanel", "TransactionController": "TransactionControllerGui", "HeaderManager": "HeaderPanel", "Arguments": "ArgumentsPanel", "CSVDataSet": "TestBeanGUI", "ConstantTimer": "ConstantTimerGui", "ResponseAssertion": "AssertionGui", "JSONPostProcessor": "JSONPostProcessorGui"}

func ensureElementProp(parent *xmlElement, name, elementType string) *xmlElement {
	if e := parent.prop(name); e != nil {
		return e
	}
	e := element("elementProp", "name", name, "elementType", elementType)
	parent.append(e)
	return e
}
func ensureCollection(parent *xmlElement, name string) *xmlElement {
	if e := parent.prop(name); e != nil {
		return e
	}
	e := element("collectionProp", "name", name)
	parent.append(e)
	return e
}
func newComponent(component string) *xmlElement {
	if component == "WebSocketSampler" {
		return element("JSR223Sampler", "guiclass", "TestBeanGUI", "testclass", "JSR223Sampler", "testname", component, "enabled", "true")
	}
	e := element(component, "guiclass", componentGUI[component], "testclass", component, "testname", component, "enabled", "true")
	if genericNative[component] {
		if c, ok := workflowcatalog.ByName[component]; ok {
			if props, err := parseNativeProperties(c.TemplateProperties); err == nil {
				e.Parts = props.Parts
			}
		}
		return e
	}
	switch component {
	case "TestPlan":
		e.setProp("boolProp", "TestPlan.functional_mode", "false")
		e.setProp("boolProp", "TestPlan.tearDown_on_shutdown", "true")
		e.setProp("boolProp", "TestPlan.serialize_threadgroups", "false")
		variables := ensureElementProp(e, "TestPlan.user_defined_variables", "Arguments")
		ensureCollection(variables, "Arguments.arguments")
	case "ThreadGroup":
		e.setProp("stringProp", "ThreadGroup.on_sample_error", "continue")
		controller := ensureElementProp(e, "ThreadGroup.main_controller", "LoopController")
		controller.setProp("boolProp", "LoopController.continue_forever", "false")
		e.setProp("boolProp", "ThreadGroup.same_user_on_next_iteration", "true")
	case "HTTPSamplerProxy":
		arguments := ensureElementProp(e, "HTTPsampler.Arguments", "Arguments")
		ensureCollection(arguments, "Arguments.arguments")
		e.setProp("boolProp", "HTTPSampler.follow_redirects", "true")
		e.setProp("boolProp", "HTTPSampler.auto_redirects", "false")
		e.setProp("boolProp", "HTTPSampler.use_keepalive", "true")
		e.setProp("boolProp", "HTTPSampler.DO_MULTIPART_POST", "false")
	case "LoopController":
		e.setProp("boolProp", "LoopController.continue_forever", "false")
	case "IfController":
		e.setProp("boolProp", "IfController.useExpression", "true")
	case "TransactionController":
		e.setProp("boolProp", "TransactionController.parent", "false")
	case "CSVDataSet":
		e.setProp("stringProp", "fileEncoding", "")
		e.setProp("boolProp", "quotedData", "false")
		e.setProp("stringProp", "shareMode", "shareMode.all")
	case "ResponseAssertion":
		e.setProp("boolProp", "Assertion.assume_success", "false")
		e.setProp("stringProp", "Assertion.custom_message", "")
	case "JSONPostProcessor":
		e.setProp("stringProp", "JSONPostProcessor.match_numbers", "1")
		e.setProp("boolProp", "JSONPostProcessor.compute_concat", "false")
	}
	return e
}
func pairSourceID(index int, item *xmlElement) string {
	return fmt.Sprintf("p%d-%s", index, digest([]byte(elementXML(item))))
}
func readPairs(parent *xmlElement, collectionName, itemType, prefix string) []any {
	out := []any{}
	collection := parent.prop(collectionName)
	if collection == nil {
		return out
	}
	for _, item := range collection.children() {
		if item.Name.Local == "elementProp" && item.attr("elementType") == itemType {
			out = append(out, Obj{"name": item.propText(prefix + ".name"), "value": item.propText(prefix + ".value"), "_source_id": pairSourceID(len(out), item)})
		}
	}
	return out
}
func patchPairs(parent *xmlElement, collectionName, itemType, prefix string, pairs []any) {
	collection := ensureCollection(parent, collectionName)
	existing := map[string]*xmlElement{}
	sourceIndex := 0
	for _, item := range collection.children() {
		if item.Name.Local == "elementProp" && item.attr("elementType") == itemType {
			existing[pairSourceID(sourceIndex, item)] = item
			sourceIndex++
		}
	}
	replacement := []*xmlElement{}
	used := map[string]bool{}
	for _, value := range pairs {
		pair := obj(value)
		sourceID := str(pair["_source_id"])
		source := existing[sourceID]
		retained := source != nil && !used[sourceID]
		var item *xmlElement
		if retained {
			item = source.copy()
			used[sourceID] = true
		} else {
			item = element("elementProp", "name", str(pair["name"]), "elementType", itemType)
		}
		item.setProp("stringProp", prefix+".name", str(pair["name"]))
		item.setProp("stringProp", prefix+".value", str(pair["value"]))
		if itemType == "Argument" {
			item.setAttr("name", str(pair["name"]))
			if !retained {
				item.setProp("stringProp", "Argument.metadata", "=")
			}
		}
		replacement = append(replacement, item)
	}
	parts := []xmlPart{}
	index := 0
	for _, part := range collection.Parts {
		if part.Node != nil && part.Node.Name.Local == "elementProp" && part.Node.attr("elementType") == itemType {
			if index < len(replacement) {
				parts = append(parts, xmlPart{Node: replacement[index]})
				index++
			}
		} else {
			parts = append(parts, part)
		}
	}
	for index < len(replacement) {
		parts = append(parts, xmlPart{Node: replacement[index]})
		index++
	}
	collection.Parts = parts
}
func bodyJSON(e *xmlElement) string {
	if e.propText("HTTPSampler.postBodyRaw") != "true" {
		return ""
	}
	arguments := e.prop("HTTPsampler.Arguments")
	if arguments == nil {
		return ""
	}
	collection := arguments.prop("Arguments.arguments")
	if collection == nil {
		return ""
	}
	var b strings.Builder
	for _, entry := range collection.children() {
		if entry.attr("elementType") == "HTTPArgument" {
			b.WriteString(entry.propText("Argument.value"))
		}
	}
	return b.String()
}
func patchBodyJSON(e *xmlElement, body string) {
	e.setProp("boolProp", "HTTPSampler.postBodyRaw", boolXML(body != ""))
	arguments := ensureElementProp(e, "HTTPsampler.Arguments", "Arguments")
	collection := ensureCollection(arguments, "Arguments.arguments")
	first := true
	for _, item := range collection.children() {
		if item.attr("elementType") == "HTTPArgument" {
			value := ""
			if first {
				value = body
				first = false
			}
			item.setProp("stringProp", "Argument.value", value)
		}
	}
	if first && body != "" {
		argument := element("elementProp", "name", "", "elementType", "HTTPArgument")
		argument.setProp("boolProp", "HTTPArgument.always_encode", "false")
		argument.setProp("stringProp", "Argument.value", body)
		argument.setProp("stringProp", "Argument.metadata", "=")
		argument.setProp("boolProp", "HTTPArgument.use_equals", "true")
		collection.append(argument)
	}
}

var assertionModes = map[string]int{"contains": 2, "matches": 1, "equals": 8, "substring": 16}

func readComponentConfig(e *xmlElement, component string) Obj {
	if component == "WebSocketSampler" {
		return readWebSocketConfig(e)
	}
	if genericNative[component] {
		return Obj{"properties_xml": nativePropertyXML(e)}
	}
	defaults, known := workflowDefaults[component]
	if !known {
		return Obj{}
	}
	config := clone(defaults)
	for _, field := range componentFields[component] {
		if value := e.prop(field.Prop); value != nil {
			if field.Tag == "boolProp" {
				config[field.Key] = strings.EqualFold(value.text(), "true")
			} else {
				config[field.Key] = value.propertyText()
			}
		}
	}
	switch component {
	case "ThreadGroup":
		if controller := e.prop("ThreadGroup.main_controller"); controller != nil {
			if loops := controller.prop("LoopController.loops"); loops != nil {
				config["loops"] = loops.text()
			}
		}
	case "HTTPSamplerProxy":
		config["bodyJson"] = bodyJSON(e)
	case "HeaderManager":
		config["headers"] = readPairs(e, "HeaderManager.headers", "Header", "Header")
	case "Arguments":
		config["variables"] = readPairs(e, "Arguments.arguments", "Argument", "Argument")
	case "ResponseAssertion":
		if field := e.propText("Assertion.test_field"); field != "" {
			config["testField"] = strings.TrimPrefix(field, "Assertion.")
		}
		value, _ := strconv.Atoi(e.propText("Assertion.test_type"))
		config["not"] = value&4 != 0
		for mode, mask := range assertionModes {
			if value & ^(4|32) == mask {
				config["matchType"] = mode
			}
		}
		patterns := []any{}
		if collection := e.prop("Asserion.test_strings"); collection != nil {
			for _, pattern := range collection.children() {
				if pattern.Name.Local == "stringProp" {
					patterns = append(patterns, pattern.text())
				}
			}
		}
		config["patterns"] = patterns
	}
	return config
}
func patchPatterns(e *xmlElement, patterns []any) {
	collection := ensureCollection(e, "Asserion.test_strings")
	existing := []*xmlElement{}
	for _, item := range collection.children() {
		if item.Name.Local == "stringProp" {
			existing = append(existing, item)
		}
	}
	parts := []xmlPart{}
	index := 0
	for _, part := range collection.Parts {
		if part.Node != nil && part.Node.Name.Local == "stringProp" {
			if index < len(patterns) {
				copy := part.Node.copy()
				copy.setText(str(patterns[index]))
				parts = append(parts, xmlPart{Node: copy})
				index++
			}
		} else {
			parts = append(parts, part)
		}
	}
	for index < len(patterns) {
		parts = append(parts, xmlPart{Node: property("stringProp", fmt.Sprint(index), str(patterns[index]))})
		index++
	}
	collection.Parts = parts
}
func patchComponent(e *xmlElement, node *workflowNode, fresh bool) {
	if genericNative[node.Component] {
		if _, supplied := obj(obj(node.Source["properties"])["config"])["properties_xml"]; supplied {
			patchNativeProperties(e, node.Config)
		}
	}
	before := readComponentConfig(e, node.Component)
	changed := func(key string) bool {
		return fresh || string(encoded(before[key])) != string(encoded(node.Config[key]))
	}
	for _, field := range componentFields[node.Component] {
		if !changed(field.Key) {
			continue
		}
		value := node.Config[field.Key]
		text := str(value)
		if field.Tag == "boolProp" {
			text = boolXML(boolean(value))
		}
		if field.Optional && text == "" {
			e.removeProp(field.Prop)
		} else {
			e.setProp(field.Tag, field.Prop, text)
		}
	}
	switch node.Component {
	case "WebSocketSampler":
		patchWebSocketComponent(e, node)
	case "ThreadGroup":
		if changed("loops") {
			controller := ensureElementProp(e, "ThreadGroup.main_controller", "LoopController")
			controller.setProp("stringProp", "LoopController.loops", str(node.Config["loops"]))
		}
		if fresh || changed("durationSeconds") || changed("delaySeconds") {
			e.setProp("boolProp", "ThreadGroup.scheduler", boolXML(str(node.Config["durationSeconds"]) != "" || str(node.Config["delaySeconds"]) != ""))
		}
	case "HTTPSamplerProxy":
		if changed("bodyJson") {
			patchBodyJSON(e, str(node.Config["bodyJson"]))
		}
	case "HeaderManager":
		if changed("headers") {
			patchPairs(e, "HeaderManager.headers", "Header", "Header", arr(node.Config["headers"]))
		}
	case "Arguments":
		if changed("variables") {
			patchPairs(e, "Arguments.arguments", "Argument", "Argument", arr(node.Config["variables"]))
		}
	case "ResponseAssertion":
		if changed("testField") {
			e.setProp("stringProp", "Assertion.test_field", "Assertion."+str(node.Config["testField"]))
		}
		if changed("matchType") || changed("not") {
			mode := assertionModes[str(node.Config["matchType"])]
			if boolean(node.Config["not"]) {
				mode |= 4
			}
			old, _ := strconv.Atoi(e.propText("Assertion.test_type"))
			mode |= old & 32
			e.setProp("intProp", "Assertion.test_type", strconv.Itoa(mode))
		}
		if changed("patterns") {
			patchPatterns(e, arr(node.Config["patterns"]))
		}
	}
	e.setAttr("testname", node.Name)
	e.setAttr("enabled", boolXML(node.Enabled))
}
func componentElement(node *workflowNode) (*xmlElement, *xmlElement, *xmlElement, error) {
	if node.Raw == "" {
		element := newComponent(node.Component)
		patchComponent(element, node, true)
		return element, nil, nil, nil
	}
	if node.Component == "TestPlan" {
		component, wrapper, e := testPlanTemplate(node.Raw)
		if e != nil {
			return nil, nil, nil, e
		}
		patchComponent(component, node, false)
		return component, nil, wrapper, nil
	}
	roots, e := parseXMLFragment(node.Raw)
	if e != nil {
		return nil, nil, nil, e
	}
	if len(roots) < 1 || len(roots) > 2 || (xmlName(roots[0].Name) != node.Component && !(node.Component == "WebSocketSampler" && websocketComponent(roots[0]))) {
		return nil, nil, nil, xmlFailure(node.Component)
	}
	if node.Component == "WebSocketSampler" {
		if _, e := decodeWebSocketConfig(roots[0]); e != nil {
			return nil, nil, nil, e
		}
	}
	var subtree *xmlElement
	if len(roots) == 2 {
		if roots[1].Name.Local != "hashTree" {
			return nil, nil, nil, xmlFailure(node.Component)
		}
		subtree = roots[1]
	}
	if newlyAdaptedNative[node.Component] {
		copy := *node
		copy.Config = readComponentConfig(roots[0], node.Component)
		for key, value := range obj(obj(node.Source["properties"])["config"]) {
			copy.Config[key] = value
		}
		node = &copy
	}
	if _, known := workflowDefaults[node.Component]; known {
		if subtree != nil && len(subtree.children()) > 0 && !newlyAdaptedNative[node.Component] {
			return nil, nil, nil, api(400, "invalid_component", "已知组件的子流程必须使用画布连线表示")
		}
		if subtree != nil && len(subtree.children()) > 0 && len(node.Children) > 0 {
			return nil, nil, nil, api(400, "opaque_subtree", "请先展开导入子流程，再添加子组件")
		}
		patchComponent(roots[0], node, false)
		if !newlyAdaptedNative[node.Component] || subtree == nil || len(subtree.children()) == 0 {
			subtree = nil
		}
	} else {
		patchNativeProperties(roots[0], node.Config)
		roots[0].setAttr("testname", node.Name)
		roots[0].setAttr("enabled", boolXML(node.Enabled))
		if subtree != nil {
			if _, e := pairedComponents(subtree); e != nil {
				return nil, nil, nil, e
			}
		}
	}
	return roots[0], subtree, nil, nil
}
