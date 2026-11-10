package main

import (
	"context"
	"database/sql"
	"errors"
)

// Historical versions are available only while the definition still exists in
// the project registry. Deleting it leaves authored references inert.
func (p *Platform) extensionVersion(ctx context.Context, kind, id, version string) (Obj, error) {
	if !interfacePluginVersion.MatchString(version) {
		return nil, api(400, "invalid_extension_version", "扩展版本无效")
	}
	project, err := p.extensionProject(ctx, contextQueryer{ctx, p.DB}, kind, false)
	if err != nil {
		return nil, err
	}
	state := obj(project[kind])
	if obj(state["definitions"])[id] == nil {
		return nil, api(404, "extension_unavailable", "扩展定义不可用")
	}
	var raw, sha string
	err = p.DB.QueryRowContext(ctx, "SELECT data,sha256 FROM extension_versions WHERE project_id=? AND kind=? AND extension_id=? AND version=?", p.projectID(), kind, id, version).Scan(&raw, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, api(404, "extension_version_missing", "扩展版本不存在")
	}
	if err != nil {
		return nil, err
	}
	record, err := decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	var definition Obj
	if kind == "interface_plugins" {
		definition, err = p.openInterfacePluginDefinition(p.projectID(), id, record)
	} else {
		definition, err = p.openWorkspace("workflow_extensions", p.projectID()+"/"+id, 1, str(record["payload"]))
	}
	if err != nil {
		return nil, err
	}
	if digest(encoded(definition)) != sha || str(definition["version"]) != version {
		return nil, api(500, "extension_integrity_failed", "扩展版本内容校验失败")
	}
	return Obj{"project_id": p.projectID(), "revision": state["revision"], "content_sha256": sha, "definition": definition}, nil
}
