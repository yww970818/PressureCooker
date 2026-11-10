package main

import (
	"database/sql"
	"errors"
	"strings"
)

// Only ownership and the exact source revision are checked. A collection edit
// never changes the graph's execution config or a published JMX snapshot.
func (p *Platform) validateAPIReferences(q queryer, graph Obj) error {
	if e := p.validateCollectionEnvironmentReferences(q, graph); e != nil {
		return e
	}
	// A large flow often references the same frozen collection many times.
	// Cache only within this validation/transaction so ownership, deletion and
	// source revisions are checked afresh on every save.
	type versionKey struct {
		id       string
		revision int64
	}
	type sourceVersion struct {
		document Obj
		names    []string
	}
	collections := map[string]bool{}
	versions := map[versionKey]sourceVersion{}
	for _, value := range arr(graph["nodes"]) {
		properties := obj(obj(value)["properties"])
		raw, present := properties["api_reference"]
		if !present || raw == nil {
			continue
		}
		ref, ok := raw.(map[string]any)
		if !ok || (str(properties["component"]) != "HTTPSamplerProxy" && str(properties["component"]) != "WebSocketSampler") {
			return api(400, "invalid_api_reference", "仅 HTTP 和 WS 请求节点可引用接口")
		}
		id, path, method := str(ref["collection_id"]), str(ref["path"]), strings.ToLower(str(ref["method"]))
		revision, ok := integer(ref["revision"])
		if !ok || revision < 1 || !apiMethod(method) || !strings.HasPrefix(path, "/") {
			return api(400, "invalid_api_reference", "接口引用须包含集合、版本、路径和方法")
		}
		if !collections[id] {
			collection, e := p.workspaceGetQ(q, "api_collections", id, false)
			if e != nil {
				return e
			}
			if objectProject(collection) != p.projectID() {
				return api(404, "not_found", "引用集合不存在")
			}
			collections[id] = true
		}
		key := versionKey{id, revision}
		source, cached := versions[key]
		if !cached {
			var payload string
			e := q.QueryRow("SELECT payload FROM api_collection_versions WHERE object_id=? AND revision=?", id, revision).Scan(&payload)
			if errors.Is(e, sql.ErrNoRows) {
				return api(400, "invalid_api_reference", "引用的接口版本不存在")
			}
			if e != nil {
				return e
			}
			snapshot, e := p.openWorkspace("api_collections", id, revision, payload)
			if e != nil {
				return e
			}
			source = sourceVersion{document: obj(snapshot["document"]), names: collectionOverrideNames(snapshot)}
			versions[key] = source
		}
		properties["api_environment_variables"] = stringsToAny(source.names)
		operation, ok := obj(obj(source.document["paths"])[path])[method].(map[string]any)
		if !ok {
			return api(400, "invalid_api_reference", "引用的接口在此版本中不存在")
		}
		descriptor, e := operationInterfaceProtocol(source.document, operation)
		if e != nil {
			return e
		}
		if e = requireInterfaceProtocolExecution(descriptor); e != nil {
			return e
		}
		if (descriptor.Protocol == "websocket") != (properties["component"] == "WebSocketSampler") {
			return api(400, "invalid_api_reference", "引用接口与 HTTP / WS 节点类型不一致")
		}
	}
	return nil
}
func apiMethod(method string) bool {
	return contains([]string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}, strings.ToLower(method))
}

func stringsToAny(values []string) []any {
	out := []any{}
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
