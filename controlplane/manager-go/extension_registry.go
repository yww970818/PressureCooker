package main

import (
	"context"
	"database/sql"
	"errors"
)

func init() {
	portableTables = append(portableTables, "extension_registries", "extension_versions")
	tableColumns["extension_registries"] = []string{"id", "project_id", "kind", "revision", "created_at", "data"}
	tableColumns["extension_versions"] = []string{"id", "project_id", "kind", "extension_id", "version", "sha256", "created_at", "data"}
}

// The project JSON is a historical import source only. Once a registry exists,
// all reads use its independent revision and mutations retain immutable versions.
func (p *Platform) extensionProject(ctx context.Context, q queryer, kind string, lock bool) (Obj, error) {
	projectQuery := q
	if lock {
		if tx, ok := q.(*Tx); ok {
			projectQuery = extensionProjectReader{tx}
		}
	}
	project, err := p.projectGetQ(projectQuery, p.projectID(), false)
	if err != nil {
		return nil, err
	}
	suffix := ""
	if lock {
		tx, ok := q.(*Tx)
		if !ok {
			return nil, api(500, "extension_transaction_required", "扩展修改须使用事务")
		}
		initial := Obj{"revision": 0, "definitions": Obj{}}
		if kind == "interface_plugins" {
			initial["installed"] = []any{}
		}
		id := "registry-" + digest([]byte(p.projectID()+"/"+kind))
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO extension_registries(id,project_id,kind,revision,created_at,data) VALUES(?,?,?,0,?,?)", id, p.projectID(), kind, now(), string(encoded(initial))); err != nil {
			return nil, err
		}
		suffix = " FOR UPDATE"
	}
	var raw string
	err = q.QueryRow("SELECT data FROM extension_registries WHERE project_id=? AND kind=?"+suffix, p.projectID(), kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		delete(project, kind)
		return project, nil
	}
	if err != nil {
		return nil, err
	}
	state, err := decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	project[kind] = state
	return project, ctx.Err()
}
func (p *Platform) saveExtensionRegistry(tx *Tx, kind string, state Obj) error {
	id := "registry-" + digest([]byte(p.projectID()+"/"+kind))
	for extensionID, value := range obj(state["definitions"]) {
		record := obj(value)
		metadata := obj(record["metadata"])
		version := str(metadata["version"])
		sha := str(metadata["content_sha256"])
		if version == "" || sha == "" {
			continue
		} // opaque historical references remain inert
		var existing string
		err := tx.QueryRow("SELECT sha256 FROM extension_versions WHERE project_id=? AND kind=? AND extension_id=? AND version=?", p.projectID(), kind, extensionID, version).Scan(&existing)
		if err == nil {
			if existing != sha {
				return api(409, "extension_version_conflict", "已发布扩展版本不可改写，请增加版本号")
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		versionID := "extension-" + digest([]byte(p.projectID()+"/"+kind+"/"+extensionID+"/"+version))
		data := clone(record)
		delete(data, "installed")
		_, err = tx.Exec("INSERT INTO extension_versions(id,project_id,kind,extension_id,version,sha256,created_at,data) VALUES(?,?,?,?,?,?,?,?)", versionID, p.projectID(), kind, extensionID, version, sha, now(), string(encoded(data)))
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec("INSERT INTO extension_registries(id,project_id,kind,revision,created_at,data) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE revision=VALUES(revision),data=VALUES(data)", id, p.projectID(), kind, state["revision"], now(), string(encoded(state)))
	return err
}

func (p *Platform) convertExtensionRegistriesQ(ctx context.Context, tx *Tx) error {
	cursor := ""
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id,data FROM projects WHERE id>? ORDER BY id LIMIT 32", cursor)
		if err != nil {
			return err
		}
		projects := []Obj{}
		for rows.Next() {
			var id, raw string
			if err = rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			project, e := decode([]byte(raw))
			if e != nil {
				rows.Close()
				return e
			}
			if str(project["id"]) != id {
				rows.Close()
				return api(409, "extension_conversion_conflict", "项目归属不一致")
			}
			projects = append(projects, project)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			break
		}
		for _, project := range projects {
			if err = ctx.Err(); err != nil {
				return err
			}
			for _, kind := range []string{"workflow_extensions", "interface_plugins"} {
				if project[kind] == nil {
					continue
				}
				var count int
				if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM extension_registries WHERE project_id=? AND kind=?", project["id"], kind).Scan(&count); err != nil {
					return err
				}
				if count == 0 {
					if err = p.forProject(str(project["id"])).saveExtensionRegistry(tx, kind, obj(project[kind])); err != nil {
						return err
					}
				}
			}
		}
		cursor = str(projects[len(projects)-1]["id"])
	}
	return nil
}

// A shared project lock keeps archival atomic without serializing unrelated
// extension kinds with each other. Each registry row owns its own CAS lock.
type extensionProjectReader struct{ tx *Tx }

func (q extensionProjectReader) QueryRow(query string, args ...any) *sql.Row {
	return q.tx.QueryRow(query+" LOCK IN SHARE MODE", args...)
}
