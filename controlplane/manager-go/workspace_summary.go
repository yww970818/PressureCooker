package main

import "strings"

const workspaceSummaryFormat = 1

// Lists carry navigation metadata, never authored bodies or runtime values.
// Walk only component tags; do not clone/serialize a graph to count its nodes.
func workspaceSummary(table string, data Obj) Obj {
	out := Obj{"summary": true, "summary_format": workspaceSummaryFormat}
	for _, key := range []string{"id", "project_id", "name", "revision", "created_at", "updated_at", "source_plan_id", "sha256", "size", "requires_environment", "requires_project_environment"} {
		if value, ok := data[key]; ok {
			out[key] = value
		}
	}
	description := []rune(str(data["description"]))
	if len(description) > 512 {
		description = description[:512]
		out["description_truncated"] = true
	}
	out["description"] = string(description)
	switch table {
	case "workflows", "workflow_versions":
		graph := obj(data["graph"])
		nodes := arr(graph["nodes"])
		out["node_count"], out["edge_count"] = len(nodes), len(arr(graph["edges"]))
		requests, datasets := 0, 0
		for _, v := range nodes {
			switch str(obj(obj(v)["properties"])["component"]) {
			case "HTTPSamplerProxy", "WebSocketSampler":
				requests++
			case "CSVDataSet":
				datasets++
			}
		}
		out["request_count"], out["dataset_count"] = requests, datasets
	case "api_collections", "api_collection_versions":
		count := 0
		for _, v := range obj(obj(data["document"])["paths"]) {
			for method, operation := range obj(v) {
				if apiMethod(strings.ToLower(method)) && obj(operation) != nil {
					count++
				}
			}
		}
		out["request_count"] = count
	case "plans":
		out["dataset_count"] = len(arr(data["dataset_requirements"]))
	case "environments":
		out["base_host"] = data["base_host"]
		names := []any{}
		for _, v := range arr(data["variables"]) {
			if name := str(obj(v)["name"]); name != "" {
				names = append(names, name)
			}
		}
		out["variable_names"] = names
	}
	return out
}

func (p *Platform) sealWorkspaceSummary(table, id string, revision int64, data Obj) (string, error) {
	return p.sealWorkspace("summary/"+table, id, revision, workspaceSummary(table, data))
}
func (p *Platform) openWorkspaceSummary(table, id string, revision int64, payload string) (Obj, error) {
	return p.openWorkspace("summary/"+table, id, revision, payload)
}
