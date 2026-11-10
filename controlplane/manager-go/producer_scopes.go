package main

import "strings"

const producerVisibilityKey = "_compiler_producer_visibility"

// CSV configuration applies within its parent scope. A postprocessor's output
// becomes visible to subsequent HTTP samplers in the same JMeter thread group.
func producerVisibility(tree *workflowTree) Obj {
	parents := map[string]string{}
	groups := map[string]string{}
	httpOwner := map[string]string{}
	order := map[string]int{}
	enabled := map[string]bool{}
	sequence := 0
	samplers := []*workflowNode{}
	var walk func(*workflowNode, string, string, string, bool)
	walk = func(n *workflowNode, parent, group, http string, active bool) {
		active = active && n.Enabled
		if n.Component == "ThreadGroup" {
			group = n.ID
		}
		if n.Component == "HTTPSamplerProxy" || n.Component == "WebSocketSampler" {
			http = n.ID
			samplers = append(samplers, n)
		}
		parents[n.ID] = parent
		groups[n.ID] = group
		httpOwner[n.ID] = http
		order[n.ID] = sequence
		sequence++
		enabled[n.ID] = active
		for _, c := range n.Children {
			walk(c, n.ID, group, http, active)
		}
	}
	walk(tree.Root, "", "", "", true)
	ancestor := func(scope, target string) bool {
		for id := target; id != ""; id = parents[id] {
			if id == scope {
				return true
			}
		}
		return false
	}
	httpVars := map[string]Obj{}
	for _, http := range samplers {
		vars := Obj{}
		httpVars[http.ID] = vars
		if !enabled[http.ID] {
			continue
		}
		for _, n := range tree.Nodes {
			if !enabled[n.ID] {
				continue
			}
			names := []string{}
			visible := false
			switch n.Component {
			case "CSVDataSet":
				names = stringList(datasetRequirement(n.Source)["variable_names"])
				visible = ancestor(parents[n.ID], http.ID)
			case "JSONPostProcessor":
				names = strings.Split(str(n.Config["referenceName"]), ";")
				if owner := httpOwner[n.ID]; owner != "" {
					visible = groups[n.ID] == groups[http.ID] && order[owner] < order[http.ID]
				} else if ancestor(parents[n.ID], http.ID) {
					for _, earlier := range samplers {
						if enabled[earlier.ID] && groups[earlier.ID] == groups[http.ID] && order[earlier.ID] < order[http.ID] && ancestor(parents[n.ID], earlier.ID) {
							visible = true
							break
						}
					}
				}
			}
			if visible {
				for _, name := range names {
					if name = strings.TrimSpace(name); name != "" {
						vars[name] = true
					}
				}
			}
		}
	}
	out := Obj{}
	for id := range tree.Nodes {
		out[id] = httpVars[httpOwner[id]]
	}
	return out
}
func visibleProducers(graph Obj) Obj {
	if cached, ok := graph[producerVisibilityKey].(map[string]any); ok {
		return cached
	}
	tree, e := validateWorkflowGraph(graph)
	if e != nil {
		return Obj{}
	}
	return producerVisibility(tree)
}
func producedForNode(graph Obj, nodeID, name string) bool {
	return boolean(obj(visibleProducers(graph)[nodeID])[name])
}
func producerHasStaticUse(graph Obj, name string) bool {
	visibility := visibleProducers(graph)
	for _, v := range arr(graph["nodes"]) {
		n := obj(v)
		p := obj(n["properties"])
		if p["enabled"] == false || p["component"] == "TestPlan" {
			continue
		}
		if boolean(obj(visibility[str(n["id"])])[name]) {
			continue
		}
		source := string(encoded(p["config"]))
		if p["component"] == "WebSocketSampler" {
			source = websocketTemplate.ReplaceAllString(source, "$${$1}")
		}
		if p["component"] != "Arguments" {
			source += " " + str(p["raw_xml"])
		}
		if strings.Contains(source, "${"+name+"}") || strings.Contains(source, environmentReference(name)) {
			return true
		}
	}
	return false
}
