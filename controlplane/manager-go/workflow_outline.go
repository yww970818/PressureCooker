package main

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"pressurecooker/manager/internal/workspace"
)

// This is a view of a complete persisted revision, never a persistable graph.
// Small producer metadata keeps variable and CSV scopes available on the canvas.
func workflowOutline(value Obj) Obj {
	out := Obj{}
	for key, item := range value {
		out[key] = item
	}
	graph := obj(value["graph"])
	shell := Obj{}
	for key, item := range graph {
		shell[key] = item
	}
	nodes := []any{}
	for _, item := range arr(graph["nodes"]) {
		node := obj(item)
		properties := obj(node["properties"])
		p := Obj{"editor_lazy": true, "config": Obj{}}
		for _, key := range []string{"component", "name", "enabled", "api_reference", "http_environment", "dataset_columns"} {
			if item, exists := properties[key]; exists {
				p[key] = item
			}
		}
		if _, known := workflowDefaults[str(properties["component"])]; !known && str(properties["raw_xml"]) != "" {
			p["editor_opaque"] = true
		}
		config := obj(properties["config"])
		for _, key := range map[string][]string{"CSVDataSet": {"variableNames", "delimiter"}, "JSONPostProcessor": {"referenceName"}}[str(properties["component"])] {
			if item, exists := config[key]; exists {
				obj(p["config"])[key] = item
			}
		}
		n := Obj{}
		for key, item := range node {
			n[key] = item
		}
		n["properties"] = p
		nodes = append(nodes, n)
	}
	shell["nodes"] = nodes
	out["graph"] = shell
	return out
}

func (p *Platform) workflowRevision(id string, revision int64) (Obj, error) {
	// The repository scopes history to a live workflow in the current project.
	version, e := workspace.New(p.projectID()).Version(p.requestContext(), p.DB, workspace.Table("workflows"), id, revision)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "not_found", "流程不存在、已删除或历史版本不存在")
	}
	if e != nil {
		return nil, e
	}
	value, e := p.openWorkspace("workflows", id, revision, string(version.Payload))
	if e == nil {
		value["project_id"] = p.projectID()
	}
	return value, e
}

func (p *Platform) workflowNodeRead(id, revisionText, idsText string) (Obj, error) {
	revision, e := strconv.ParseInt(revisionText, 10, 64)
	if e != nil || revision < 1 {
		return nil, api(400, "revision_required", "读取节点须指定草稿版本")
	}
	ids := strings.Split(idsText, ",")
	capacity, e := workflowCapacity()
	if e != nil || len(ids) > capacity.Nodes {
		return nil, api(400, "invalid_nodes", "节点数量超过工作流容量")
	}
	requested := map[string]bool{}
	for _, nodeID := range ids {
		if e := validID(nodeID); e != nil {
			return nil, api(400, "invalid_nodes", "节点标识无效")
		}
		requested[nodeID] = true
	}
	value, e := p.workflowRevision(id, revision)
	if e != nil {
		return nil, e
	}
	nodes := []any{}
	for _, item := range arr(obj(value["graph"])["nodes"]) {
		if requested[str(obj(item)["id"])] {
			nodes = append(nodes, item)
		}
	}
	if len(nodes) != len(requested) {
		return nil, api(404, "not_found", "当前版本中不存在所选节点")
	}
	return Obj{"project_id": p.projectID(), "workflow_id": id, "revision": revision, "nodes": nodes}, nil
}
