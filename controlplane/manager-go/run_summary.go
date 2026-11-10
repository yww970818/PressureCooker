package main

import (
	"context"
	"database/sql"
)

// A task row never needs parameter secrets, drilldown evidence or report bodies.
// Full detail and copy endpoints retain their original immutable snapshots.
func (p *Platform) listRunSummaries(ctx context.Context, status, search string, limit, offset int) (Obj, error) {
	if limit < 1 || limit > 200 || offset < 0 {
		return nil, api(400, "invalid_pagination", "每页条数须为1至200，偏移不得为负数")
	}
	where, args := runListPredicate(p.projectID(), status, search)
	tx, e := p.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var total int
	if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs WHERE "+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := readRunPage(ctx, tx, where, args, limit, offset, "summary_data")
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	items := []any{}
	for _, row := range rows {
		if e := p.openRunEnvironmentDisplay(row); e != nil {
			return nil, e
		}
		items = append(items, row)
	}
	return Obj{"project_id": p.projectID(), "items": items, "total": total, "limit": limit, "offset": offset}, nil
}

func (p *Platform) openRunEnvironmentDisplay(row Obj) error {
	sealed := str(row[runEnvironmentDisplay])
	delete(row, runEnvironmentDisplay)
	if sealed == "" {
		return nil
	}
	display, err := p.openWorkspace("run-display", runEnvironmentContext(row), 1, sealed)
	if err != nil || display["id"] != row["environment_id"] || objectProject(display) != objectProject(row) {
		return api(500, "run_environment_decryption_failed", "无法读取任务环境摘要")
	}
	row["environment_snapshot"] = display
	return nil
}

// Keyset batches preserve original snapshots and can be resumed. Only task
// records lacking the new encrypted display projection are rewritten.
func (p *Platform) rebuildRunEnvironmentSummaries(batch int) (Obj, error) {
	if batch < 1 || batch > 200 {
		return nil, api(400, "invalid_batch", "回填批量须为1至200")
	}
	updated, last := 0, ""
	for {
		rows, err := p.DB.Query("SELECT id,project_id FROM runs WHERE id>? AND JSON_EXTRACT(data,'$.environment_display_envelope') IS NULL AND JSON_EXTRACT(data,'$.environment_envelope') IS NOT NULL AND JSON_UNQUOTE(JSON_EXTRACT(data,'$.environment_id'))<>'' ORDER BY id LIMIT ?", last, batch)
		if err != nil {
			return nil, err
		}
		type candidate struct{ id, project string }
		candidates := []candidate{}
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.id, &c.project); err != nil {
				rows.Close()
				return nil, err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, c := range candidates {
			changed, err := p.forProject(c.project).backfillRunEnvironmentDisplay(c.id)
			if err != nil {
				return nil, err
			}
			last = c.id
			if changed {
				updated++
			}
		}
	}
	return Obj{"updated": updated}, nil
}

func (p *Platform) backfillRunEnvironmentDisplay(id string) (bool, error) {
	changed := false
	err := p.transaction(func(tx *Tx) error {
		var raw string
		if err := tx.QueryRow("SELECT data FROM runs WHERE id=? FOR UPDATE", id).Scan(&raw); err != nil {
			return err
		}
		stored, err := decode([]byte(raw))
		if err != nil {
			return err
		}
		if err = p.checkProject(stored); err != nil {
			return err
		}
		if str(stored[runEnvironmentDisplay]) != "" {
			return nil
		}
		run, err := p.openStoredRun(clone(stored))
		if err != nil {
			return err
		}
		display, err := p.sealRunEnvironmentDisplay(run)
		if err != nil || display == "" {
			return err
		}
		// Only add this projection. In particular, do not re-seal or replace
		// environment_envelope, signed dispatches, timestamps or execution data.
		_, err = tx.Exec("UPDATE runs SET data=JSON_SET(data,'$.environment_display_envelope',?) WHERE id=?", display, id)
		changed = err == nil
		return err
	})
	return changed, err
}
