package main

import (
	"database/sql"
	"errors"
	"net/url"
)

// Library state belongs to the directory. The immutable plan and every exact
// reference to it remain untouched, including archived historical run copies.
func (p *Platform) archivePlan(id string, body Obj) (Obj, error) {
	archived, ok := body["archived"].(bool)
	if !ok {
		return nil, api(400, "invalid_archive", "archived须为布尔值")
	}
	if err := validID(id); err != nil {
		return nil, err
	}
	var result Obj
	err := p.transaction(func(tx *Tx) error {
		if err := p.requireActiveQ(tx); err != nil {
			return err
		}
		plan, err := get(tx, "plans", id)
		if err != nil {
			return err
		}
		if objectProject(plan) != p.projectID() {
			return api(404, "not_found", "脚本不存在")
		}
		var current bool
		var updated string
		err = tx.QueryRow("SELECT archived,updated_at FROM plan_library_state WHERE id=? AND project_id=?", id, p.projectID()).Scan(&current, &updated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != archived {
			updated = now()
			if _, err = tx.Exec("INSERT INTO plan_library_state(id,project_id,archived,updated_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE archived=VALUES(archived),updated_at=VALUES(updated_at)", id, p.projectID(), archived, updated); err != nil {
				return err
			}
			action := "plan.restore"
			if archived {
				action = "plan.archive"
			}
			if _, err = tx.Exec("INSERT INTO audit(created_at,action,object_id) VALUES(?,?,?)", updated, action, id); err != nil {
				return err
			}
		}
		result = Obj{"id": id, "project_id": p.projectID(), "archived": archived, "updated_at": updated}
		return nil
	})
	return result, err
}

func planIncludeArchived(q url.Values) (bool, error) {
	values, present := q["include_archived"]
	if !present {
		return false, nil
	}
	if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
		return false, api(400, "invalid_archive", "include_archived须为true或false")
	}
	return values[0] == "true", nil
}

func (p *Platform) planLibraryList(q url.Values, summary bool) (Obj, error) {
	include, err := planIncludeArchived(q)
	if err != nil {
		return nil, err
	}
	paged := summary || q.Has("limit") || q.Has("offset")
	limit, offset := 0, 0
	if paged {
		limit, offset, err = pageWindow(q)
		if err != nil {
			return nil, err
		}
	}
	// Both count and page apply the same project/archive filter in SQL. State is
	// joined by its primary key, and plans scan the project/sequence index.
	from := " FROM plans p LEFT JOIN plan_library_state s ON s.id=p.id AND s.project_id=p.project_id WHERE p.project_id=?"
	if !include {
		from += " AND (s.id IS NULL OR s.archived=0)"
	}
	var total int
	if err = p.DB.QueryRow("SELECT COUNT(*)"+from, p.projectID()).Scan(&total); err != nil {
		return nil, err
	}
	projection := "p.data,NULL"
	if summary {
		projection = "IF(p.summary_payload IS NULL,p.data,NULL),p.summary_payload"
	}
	query := "SELECT p.id," + projection + ",COALESCE(s.archived,0)" + from + " ORDER BY p.seq DESC"
	args := []any{p.projectID()}
	if paged {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	rows, err := p.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id string
		var raw, payload sql.NullString
		var archived bool
		if err = rows.Scan(&id, &raw, &payload, &archived); err != nil {
			return nil, err
		}
		var item Obj
		if summary && payload.Valid {
			item, err = p.openWorkspaceSummary("plans", id, 1, payload.String)
		} else {
			item, err = decode([]byte(raw.String))
			if err == nil && summary {
				item = workspaceSummary("plans", item)
			}
		}
		if err != nil {
			return nil, err
		}
		item["project_id"] = p.projectID()
		if _, exists := item["requires_environment"]; !exists {
			item["requires_environment"] = false
		}
		item["archived"] = archived
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := Obj{"project_id": p.projectID(), "items": items, "total": total}
	if paged {
		result["limit"] = limit
		result["offset"] = offset
	}
	return result, nil
}
