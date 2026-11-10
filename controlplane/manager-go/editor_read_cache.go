package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// These columns contain a disposable, encrypted projection of the same complete
// authored revision. Neither the source ciphertext nor version history changes.
func (p *Platform) writeWorkflowEditorRead(tx *Tx, data Obj) error {
	id := str(data["id"])
	rev, _ := integer(data["revision"])
	packed, e := packReadModel(editorReadPayload(data))
	if e != nil {
		return nil
	} // Oversized projections still use the normal read.
	sealed, e := p.sealWorkspace("editor-cache/workflows", id, rev, Obj{"packed": packed})
	if e != nil {
		return e
	}
	_, e = tx.Exec("UPDATE workflows SET editor_payload=?,editor_revision=? WHERE id=? AND revision=? AND project_id=? AND deleted=0", sealed, rev, id, rev, objectProject(data))
	return e
}

func (p *Platform) workflowEditorRead(id string) (any, error) {
	if e := validID(id); e != nil {
		return nil, e
	}
	var rev int64
	var cacheRev sql.NullInt64
	var payload sql.NullString
	e := p.DB.QueryRow("SELECT revision,editor_revision,editor_payload FROM workflows WHERE id=? AND project_id=? AND deleted=0", id, p.projectID()).Scan(&rev, &cacheRev, &payload)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "not_found", "内容不存在")
	}
	if e == nil && payload.Valid && cacheRev.Valid && cacheRev.Int64 == rev {
		if header, err := p.openWorkspace("editor-cache/workflows", id, rev, payload.String); err == nil {
			if value, err := unpackEditorRead(str(header["packed"])); err == nil {
				return value, nil
			}
		}
	}
	// Missing/stale/corrupt projections and old schemas retain the original read.
	full, e := p.workspaceGet("workflows", id)
	if e != nil {
		return nil, e
	}
	return editorReadPayload(full), nil
}

// Keyset batches visit each legacy draft once, even when a projection is too
// large to cache. Conditional writes cannot overwrite a newer saved revision.
func (p *Platform) rebuildWorkflowEditorReads(ctx context.Context) error {
	cursor := ""
	for {
		rows, e := p.DB.QueryContext(ctx, "SELECT id FROM workflows WHERE id>? AND deleted=0 AND (editor_payload IS NULL OR editor_revision IS NULL OR editor_revision<>revision) ORDER BY id LIMIT 100", cursor)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			e = p.transactionContext(ctx, func(tx *Tx) error {
				var rev int64
				var source, project string
				e := tx.QueryRowContext(ctx, "SELECT revision,payload,project_id FROM workflows WHERE id=? AND deleted=0 AND (editor_payload IS NULL OR editor_revision IS NULL OR editor_revision<>revision) FOR UPDATE", id).Scan(&rev, &source, &project)
				if errors.Is(e, sql.ErrNoRows) {
					return nil
				}
				if e != nil {
					return e
				}
				data, e := p.openWorkspace("workflows", id, rev, source)
				if e != nil {
					return nil
				} // A damaged original remains visible to normal reads.
				data["project_id"] = project
				return p.writeWorkflowEditorRead(tx, data)
			})
			if e != nil {
				return e
			}
		}
		cursor = ids[len(ids)-1]
	}
}

// Keep the cached JSON as bytes: parsing it into a second object tree and then
// encoding that same tree again adds latency without changing the response.
func unpackEditorRead(packed string) (json.RawMessage, error) {
	if len(packed) < 6 || packed[:6] != "gzip1:" {
		return nil, fmt.Errorf("invalid editor cache format")
	}
	compressed, e := base64.RawStdEncoding.DecodeString(packed[6:])
	if e != nil {
		return nil, e
	}
	reader, e := gzip.NewReader(bytes.NewReader(compressed))
	if e != nil {
		return nil, e
	}
	defer reader.Close()
	raw, e := io.ReadAll(io.LimitReader(reader, (64<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(raw) > 64<<20 || len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return nil, fmt.Errorf("invalid editor cache JSON")
	}
	return json.RawMessage(raw), nil
}
