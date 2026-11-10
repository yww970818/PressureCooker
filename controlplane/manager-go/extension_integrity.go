package main

import "fmt"

func (p *Platform) validateExtensionImport(tx *Tx) error {
	for _, table := range []string{"extension_registries", "extension_versions"} {
		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM " + table + " e LEFT JOIN projects p ON p.id=e.project_id WHERE p.id IS NULL OR e.kind NOT IN ('workflow_extensions','interface_plugins')").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("invalid imported extension ownership")
		}
	}
	rows, err := tx.Query("SELECT project_id,kind,extension_id,version,sha256,data FROM extension_versions")
	if err != nil {
		return err
	}
	for rows.Next() {
		var project, kind, id, version, sha, raw string
		if err = rows.Scan(&project, &kind, &id, &version, &sha, &raw); err != nil {
			rows.Close()
			return err
		}
		record, e := decode([]byte(raw))
		if e != nil {
			rows.Close()
			return e
		}
		var definition Obj
		if kind == "interface_plugins" {
			definition, e = p.openInterfacePluginDefinition(project, id, record)
		} else {
			definition, e = p.openWorkspace("workflow_extensions", project+"/"+id, 1, str(record["payload"]))
		}
		if e != nil {
			rows.Close()
			return e
		}
		metadata := obj(record["metadata"])
		if str(definition["id"]) != id || str(definition["version"]) != version || digest(encoded(definition)) != sha || str(metadata["content_sha256"]) != sha {
			rows.Close()
			return fmt.Errorf("invalid imported immutable extension")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	cursor := ""
	for {
		rows, err := tx.Query("SELECT id,project_id,kind,data,revision FROM extension_registries WHERE id>? ORDER BY id LIMIT 32", cursor)
		if err != nil {
			return err
		}
		type registryRow struct {
			id, project, kind, raw string
			revision               int64
		}
		batch := []registryRow{}
		for rows.Next() {
			var row registryRow
			if err = rows.Scan(&row.id, &row.project, &row.kind, &row.raw, &row.revision); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			state, e := decode([]byte(row.raw))
			if e != nil {
				return e
			}
			var validated Obj
			if row.kind == "interface_plugins" {
				validated, e = interfacePluginState(Obj{row.kind: state})
			} else {
				validated, e = workflowExtensionState(Obj{row.kind: state})
			}
			if e != nil {
				return e
			}
			actual, valid := integer(validated["revision"])
			if !valid || actual != row.revision || row.revision < 0 {
				return fmt.Errorf("invalid imported extension registry revision")
			}
			for id, value := range obj(state["definitions"]) {
				record := clone(obj(value))
				metadata := obj(record["metadata"])
				version, sha := str(metadata["version"]), str(metadata["content_sha256"])
				if version == "" || sha == "" {
					continue
				} // Unknown historical references stay inert.
				var raw, immutableSHA string
				if err = tx.QueryRow("SELECT data,sha256 FROM extension_versions WHERE project_id=? AND kind=? AND extension_id=? AND version=?", row.project, row.kind, id, version).Scan(&raw, &immutableSHA); err != nil {
					return fmt.Errorf("missing imported current extension version")
				}
				immutable, e := decode([]byte(raw))
				if e != nil {
					return e
				}
				// AES-GCM uses a fresh nonce when an existing version is
				// explicitly selected again. Compare authenticated plaintext.
				var currentDefinition, immutableDefinition Obj
				if row.kind == "interface_plugins" {
					currentDefinition, e = p.openInterfacePluginDefinition(row.project, id, record)
					if e == nil {
						immutableDefinition, e = p.openInterfacePluginDefinition(row.project, id, immutable)
					}
				} else {
					currentDefinition, e = p.openWorkspace("workflow_extensions", row.project+"/"+id, 1, str(record["payload"]))
					if e == nil {
						immutableDefinition, e = p.openWorkspace("workflow_extensions", row.project+"/"+id, 1, str(immutable["payload"]))
					}
				}
				if e != nil {
					return e
				}
				if sha != immutableSHA || digest(encoded(currentDefinition)) != sha || digest(encoded(immutableDefinition)) != sha || str(currentDefinition["id"]) != id || str(currentDefinition["version"]) != version {
					return fmt.Errorf("current extension differs from immutable version")
				}
			}
		}
		cursor = batch[len(batch)-1].id
	}
	return nil
}
