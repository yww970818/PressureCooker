package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func replaceTreePairs(tree *xmlElement, components ...*xmlElement) {
	parts := []xmlPart{}
	index := 0
	for _, part := range tree.Parts {
		if part.Node != nil {
			if index < len(components) {
				parts = append(parts, xmlPart{Node: components[index]})
				index++
			}
		} else {
			parts = append(parts, part)
		}
	}
	for index < len(components) {
		parts = append(parts, xmlPart{Node: components[index]})
		index++
	}
	tree.Parts = parts
}

func workflowGraphHeader(graph Obj) Obj {
	out := make(Obj, len(graph))
	for key, value := range graph {
		out[key] = value
	}
	return out
}

func compileWorkflow(graph Obj) (string, error) {
	// Only derived top-level metadata is authored during compilation. Component
	// configs are normalized into private maps by validateWorkflowGraph; raw XML
	// is parsed into a private tree. Avoid full JSON copies of all request bodies.
	graph = workflowGraphHeader(graph)
	tree, e := validateWorkflowGraph(graph)
	if e != nil {
		return "", e
	}
	graph[producerVisibilityKey] = producerVisibility(tree)
	if str(graph["environment_binding"]) == "base_host" {
		normalizeLegacyEnvironmentPaths(tree)
	}
	if e := validateCSVProducerScopes(tree); e != nil {
		return "", e
	}
	detachedNames := map[string]bool{}
	globalUses := map[string]bool{}
	candidates, _ := environmentVariableNames(graph)
	for _, v := range arr(graph["nodes"]) {
		for _, n := range arr(obj(obj(v)["properties"])["api_environment_variables"]) {
			candidates = append(candidates, str(n))
		}
	}
	var wrapper *xmlElement
	var build func(*workflowNode) (*xmlElement, *xmlElement, error)
	build = func(node *workflowNode) (*xmlElement, *xmlElement, error) {
		component, opaque, template, e := componentElement(node)
		if e != nil {
			return nil, nil, e
		}
		if e := bindManagedArgumentGeneration(node, component); e != nil {
			return nil, nil, e
		}
		if str(graph["environment_binding"]) == "base_host" && node.Component == "Arguments" && boolean(obj(node.Source["properties"])["http_environment"]) {
			component.setProp("boolProp", "jmeter.environment_managed", "true")
		}
		if node.Component == "CSVDataSet" && node.Enabled {
			req := datasetRequirement(node.Source)
			if graph["workflow_id"] != nil {
				req["workflow_id"] = graph["workflow_id"]
			}
			if len(stringList(req["columns"])) > 0 || str(node.Config["filename"]) == "" {
				component.setProp("stringProp", datasetNodeMarker, node.ID)
				component.setProp("stringProp", datasetMarker, string(encoded(req)))
				component.setProp("boolProp", "ignoreFirstLine", "true")
				component.setProp("boolProp", "quotedData", "true")
				component.setProp("stringProp", "fileEncoding", "UTF-8")
				component.setProp("stringProp", "variableNames", strings.Join(stringList(req["variable_names"]), ","))
			}
		}
		if template != nil {
			wrapper = template
		}
		if opaque != nil {
			return component, opaque, nil
		}
		children := element("hashTree")
		for _, child := range node.Children {
			entry, subtree, e := build(child)
			if e != nil {
				return nil, nil, e
			}
			children.append(entry)
			children.append(subtree)
		}
		bindCollectionNode(node, component, children, graph)
		if e := bindManagedArgumentUses(node, component, children, graph); e != nil {
			return nil, nil, e
		}
		if node.Component != "TestPlan" {
			source := executableXMLText(component)
			for _, name := range candidates {
				if !producedForNode(graph, node.ID, name) && (strings.Contains(source, "${"+name+"}") || strings.Contains(source, environmentReference(name))) {
					globalUses[name] = true
				}
			}
		}
		props := obj(node.Source["properties"])
		if (node.Component == "HTTPSamplerProxy" || node.Component == "WebSocketSampler") && len(obj(props["api_reference"])) == 0 {
			// Source metadata contains an all-environment candidate union. Only
			// this node's remaining executable template can require a project key.
			executable := component.copy()
			executable.removeProp(environmentPathProp) // descriptive original path
			source := executableXMLText(executable) + executableXMLText(children)
			for _, value := range arr(props["api_environment_variables"]) {
				name := str(value)
				if name != "base_host" && validEnvironmentName(name) && !producedForNode(graph, node.ID, name) && (strings.Contains(source, "${"+name+"}") || strings.Contains(source, environmentReference(name))) {
					detachedNames[name] = true
				}
			}
		}
		return component, children, nil
	}
	root, children, e := build(tree.Root)
	if e != nil {
		return "", e
	}
	if declarations, present := obj(tree.Root.Source["properties"])["script_identities"]; present {
		normalized, err := normalizeScriptIdentities(declarations)
		if err != nil {
			return "", err
		}
		root.setProp("stringProp", scriptIdentitiesMarker, string(encoded(normalized)))
	}
	// Historical source remains readable; publishing does not rewrite its bytes or
	// identity namespace. Execution requires an explicit converted publication.
	if declarations, present := obj(tree.Root.Source["properties"])["cookie_accounts"]; present {
		roles, err := normalizePlanCookieAccounts(declarations)
		if err != nil {
			return "", err
		}
		root.setProp("stringProp", cookieAccountsMarker, string(encoded(roles)))
	}

	if wrapper == nil {
		wrapper = element("jmeterTestPlan", "version", "1.2", "properties", "5.0", "jmeter", "5.6.3")
		wrapper.append(element("hashTree"))
	}
	var top *xmlElement
	for _, entry := range wrapper.children() {
		if entry.Name.Local == "hashTree" {
			top = entry
			break
		}
	}
	if top == nil {
		return "", api(400, "invalid_jmx", "JMX根模板缺少hashTree")
	}
	// Collection lookups no longer need a global project variable unless another
	// component still references that name. Preserve explicit unscoped uses.
	bindingGraph := workflowGraphHeader(graph)
	if graph["environment_binding"] == "base_host" {
		// Validate explicit declarations before adding only deduplicated names
		// still used by detached templates. Repeated nodes consume one slot.
		names, err := environmentVariableNames(graph)
		if err != nil {
			return "", err
		}
		candidates := map[string]bool{}
		for _, name := range names {
			candidates[name] = true
		}
		for name := range detachedNames {
			candidates[name] = true
		}
		values := []any{}
		for name := range candidates {
			values = append(values, name)
		}
		bindingGraph["environment_variables"] = values
		names, err = environmentVariableNames(bindingGraph)
		if err != nil {
			return "", err
		}

		scoped := map[string]bool{}
		var gather func(*xmlElement)
		gather = func(n *xmlElement) {
			for _, value := range arr(collectionBindingMetadata(n)["names"]) {
				scoped[str(value)] = true
			}
			for _, child := range n.children() {
				gather(child)
			}
		}
		gather(children)
		retained := []any{}
		for _, name := range names {
			if !scoped[name] || globalUses[name] {
				retained = append(retained, name)
			}
		}
		bindingGraph["environment_variables"] = retained
	}
	if e = applyEnvironmentBinding(root, children, bindingGraph); e != nil {
		return "", e
	}
	replaceTreePairs(top, root, children)
	if e = validateGeneratedXMLStructure(wrapper); e != nil {
		return "", e
	}
	if _, e = argumentGenerationRulesFromRoots([]*xmlElement{wrapper}); e != nil {
		return "", e
	}
	content := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" + elementXML(wrapper) + "\n"
	if unsafeXML.MatchString(content) {
		return "", api(400, "unsafe_xml", "XML禁止DOCTYPE或ENTITY")
	}
	if len(content) > workflowByteLimit() {
		return "", api(413, "workflow_too_large", "生成的JMX超过工作流容量配置")
	}
	return content, nil
}
func importWorkflowGraph(content string) (Obj, error) {
	graph, e := parseWorkflowGraph(content)
	if e != nil {
		return nil, e
	}
	if _, e = compileWorkflow(graph); e != nil {
		return nil, e
	}
	return graph, nil
}

// Creation validates and compiles the parsed graph in validateWorkspace. Other
// callers use importWorkflowGraph to retain validation without persisting it.
func parseWorkflowGraph(content string) (Obj, error) {
	roots, e := parseXMLFragment(content)
	if e != nil {
		return nil, e
	}
	if _, e := argumentGenerationRulesFromRoots(roots); e != nil {
		return nil, e
	}
	if len(roots) != 1 || roots[0].Name.Local != "jmeterTestPlan" {
		return nil, api(400, "invalid_jmx", "导入内容必须是JMeter测试计划")
	}
	wrapper := roots[0]
	var top *xmlElement
	for _, child := range wrapper.children() {
		if child.Name.Local == "hashTree" {
			top = child
			break
		}
	}
	if top == nil {
		return nil, api(400, "invalid_jmx", "JMX缺少hashTree")
	}
	pairs, e := pairedComponents(top)
	if e != nil || len(pairs) != 1 || pairs[0][0].Name.Local != "TestPlan" {
		return nil, api(400, "invalid_jmx", "JMX必须包含唯一TestPlan根组件")
	}
	graph := Obj{"nodes": []any{}, "edges": []any{}}
	templates := map[string][]*xmlElement{}
	index := 0
	var add func(*xmlElement, *xmlElement, string, int, int) error
	add = func(component, subtree *xmlElement, parentID string, order, depth int) error {
		if depth > 256 {
			return api(400, "invalid_jmx", "JMX层级过深")
		}
		kind := xmlName(component.Name)
		if websocketComponent(component) {
			kind = "WebSocketSampler"
			if _, e := decodeWebSocketConfig(component); e != nil {
				return e
			}
		}
		name := component.attr("testname")
		if name == "" {
			name = kind
		}
		enabled := !strings.EqualFold(component.attr("enabled"), "false")
		id := newID("node-")
		if kind == "Arguments" {
			metadata, err := argumentGenerationMetadata(component)
			if err != nil {
				return err
			}
			if metadata != nil {
				id = str(metadata["node_id"])
			}
		}
		if kind == "CSVDataSet" && validID(component.propText(datasetNodeMarker)) == nil {
			id = component.propText(datasetNodeMarker)
		}
		if meta := collectionBindingMetadata(component); meta != nil && validID(str(meta["node_id"])) == nil {
			id = str(meta["node_id"])
		}
		raw := elementXML(component)
		parsed := []*xmlElement{component}
		_, known := workflowDefaults[kind]
		if parentID == "" {
			// Keep wrapper metadata without copying the whole imported graph
			// just to remove it from the root component's stored template.
			template := workflowRootTemplate(wrapper, top)
			var templateTree *xmlElement
			for _, entry := range template.children() {
				if entry.Name.Local == "hashTree" {
					templateTree = entry
					break
				}
			}
			replaceTreePairs(templateTree, component.copy(), element("hashTree"))
			raw = elementXML(template)
			parsed = []*xmlElement{template}
		} else if !known {
			raw = fragmentXML(component, subtree)
			parsed = []*xmlElement{component, subtree}
		}
		templates[id] = parsed
		node := Obj{"id": id, "type": "jmeter-node", "x": 160 + depth*280, "y": 100 + index*140, "text": Obj{"value": name}, "properties": Obj{"component": kind, "name": name, "enabled": enabled, "config": readComponentConfig(component, kind), "raw_xml": raw}}
		if kind == "CSVDataSet" {
			if d, e := decode([]byte(component.propText(datasetMarker))); e == nil {
				obj(node["properties"])["dataset_columns"] = d["columns"]
			}
		}
		index++
		graph["nodes"] = append(arr(graph["nodes"]), node)
		if parentID != "" {
			graph["edges"] = append(arr(graph["edges"]), Obj{"id": newID("edge-"), "type": "polyline", "sourceNodeId": parentID, "targetNodeId": id, "properties": Obj{"order": order}})
		}
		if !known {
			return nil
		}
		children, e := pairedComponents(subtree)
		if e != nil {
			return e
		}
		for i, pair := range children {
			if e = add(pair[0], pair[1], id, i, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	if e = add(pairs[0][0], pairs[0][1], "", 0, 0); e != nil {
		return nil, e
	}
	restoreCollectionGraph(graph, templates)
	if e := restoreManagedArgumentGraph(graph, templates); e != nil {
		return nil, e
	}
	restoreEnvironmentGraph(graph, pairs[0][0], templates)
	return graph, nil
}
func (p *Platform) importWorkflow(body Obj) (Obj, error) {
	name, description, e := workspaceMetadata(body)
	if e != nil {
		return nil, e
	}
	content, hasContent := body["content"].(string)
	planID, hasPlan := body["plan_id"].(string)
	if hasContent == hasPlan {
		return nil, api(400, "invalid_import", "须且只能提供JMX内容或已保存的计划ID")
	}
	if hasPlan {
		plan, e := p.get("plans", planID)
		if e != nil {
			return nil, e
		}
		path, e := safePath(p.Config.PlansDir, "snapshots/"+str(plan["sha256"])+".jmx", true)
		if e != nil {
			return nil, e
		}
		raw, e := readLimitedFile(path, workflowByteLimit())
		if e != nil {
			return nil, e
		}
		if digest(raw) != str(plan["sha256"]) {
			return nil, api(422, "graph_preview_unavailable", "脚本快照校验失败，无法保存编辑草稿")
		}
		content = string(raw)
	}
	if value, exists := body["graph"]; exists {
		// A local edit starts from the selected immutable preview. Persist its
		// complete edited graph and server-verified origin in one transaction,
		// rather than creating a throwaway import before the first edit.
		graph, ok := value.(map[string]any)
		if !hasPlan || !ok {
			return nil, api(400, "invalid_import", "编辑图须为完整对象并指定源计划ID")
		}
		return p.workspaceCreateFromPlan("workflows", Obj{"name": name, "description": description, "graph": graph}, planID)
	}
	graph, e := parseWorkflowGraph(content)
	if e != nil {
		return nil, e
	}
	sourcePlanID := ""
	if hasPlan {
		sourcePlanID = planID
	}
	return p.workspaceCreateFromPlan("workflows", Obj{"name": name, "description": description, "graph": graph}, sourcePlanID)
}
func (p *Platform) publishWorkflow(id string, body Obj) (Obj, error) {
	revision, ok := integer(body["revision"])
	if !ok || revision < 1 {
		return nil, api(400, "revision_required", "发布时必须携带当前版本号")
	}
	var plan Obj
	e := p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		workflow, e := p.workspaceGetQ(tx, "workflows", id, true)
		if e != nil {
			return e
		}
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		if e := p.validateAPIReferences(tx, obj(workflow["graph"])); e != nil {
			return e
		}
		current, _ := integer(workflow["revision"])
		if current != revision {
			return api(409, "revision_conflict", "工作流已更新，请保存最新版本后发布")
		}
		publishGraph := clone(obj(workflow["graph"]))
		publishGraph["workflow_id"] = id
		if e = p.enrichDatasetGraph(tx, publishGraph, id); e != nil {
			return e
		}
		content, e := compileWorkflow(publishGraph)
		if e != nil {
			return e
		}
		plan, e = p.preparePlanWithLimit(Obj{"name": workflow["name"], "description": workflow["description"], "content": content}, workflowByteLimit())
		if e != nil {
			return e
		}
		if e = p.savePlan(tx, plan); e != nil {
			return e
		}
		if e = insertPlanWorkflowLink(tx, str(plan["id"]), id, revision, "published"); e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO audit(created_at,action,object_id) VALUES(?,?,?)", now(), "workflow.publish", id)
		return e
	})
	if e != nil {
		return nil, e
	}
	return Obj{"project_id": p.projectID(), "managerPlanId": plan["id"], "plan": plan, "workflow_id": id, "revision": revision, "message": fmt.Sprint("已发布为不可变测试计划")}, nil
}

func readLimitedFile(path string, limit int) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil {
		return nil, e
	}
	if len(raw) > limit {
		return nil, api(413, "file_too_large", "文件超过允许大小")
	}
	return raw, nil
}
