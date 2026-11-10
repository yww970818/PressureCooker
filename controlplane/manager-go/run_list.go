package main

import (
	"context"
	"database/sql"
	"strings"
)

func (p *Platform) listRunsContext(ctx context.Context, status, search string, limit, offset int) (Obj, error) {
	if limit < 1 || limit > 200 || offset < 0 {
		return nil, api(400, "invalid_pagination", "Limit must be 1..200 and offset nonnegative")
	}
	where, args := runListPredicate(p.projectID(), status, search)
	// Count, page and related records share a read-only snapshot. Concurrent
	// submissions or callbacks cannot mix page metadata from different versions.
	tx, err := p.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	runs, err := readRunPage(ctx, tx, where, args, limit, offset)
	if err != nil {
		return nil, err
	}
	reports, deliveries, dispatches, err := readRunRelations(ctx, tx, p.projectID(), runs)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	items := make([]any, 0, len(runs))
	scoped := p.forProject(p.projectID())
	for _, run := range runs {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if err = scoped.checkProject(run); err != nil {
			return nil, err
		}
		run, err = p.openStoredRun(run)
		if err != nil {
			return nil, err
		}
		run["project_id"] = objectProject(run)
		id := str(run["id"])
		items = append(items, scoped.publicRun(run, reports[id], deliveries[id], dispatches[id]))
	}
	return Obj{"project_id": p.projectID(), "items": items, "total": total}, nil
}

func readRunPage(ctx context.Context, tx *Tx, where string, args []any, limit, offset int, column ...string) ([]Obj, error) {
	pageArgs := append(append([]any{}, args...), limit, offset)
	selected := "data"
	if len(column) > 0 && column[0] == "summary_data" {
		// Transfer only the small encrypted display projection, never the full
		// environment envelope or parameter snapshot used by the executor.
		selected = "JSON_SET(summary_data,'$.environment_display_envelope',JSON_EXTRACT(data,'$.environment_display_envelope'),'$.environment_id',JSON_EXTRACT(data,'$.environment_id'))"
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+selected+" FROM runs WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", pageArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Obj{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		run, err := decode([]byte(raw))
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func readRunRelations(ctx context.Context, tx *Tx, project string, runs []Obj) (map[string][]any, map[string]Obj, map[string]Obj, error) {
	reports, deliveries, dispatches := map[string][]any{}, map[string]Obj{}, map[string]Obj{}
	ids, legacyIDs, redisIDs := []any{}, []any{}, []any{}
	for _, run := range runs {
		id := run["id"]
		ids = append(ids, id)
		if str(run["execution_mode"]) == "redis" {
			redisIDs = append(redisIDs, id)
		} else {
			legacyIDs = append(legacyIDs, id)
		}
	}
	if len(ids) == 0 {
		return reports, deliveries, dispatches, nil
	}
	read := func(query string, args []any, visit func(*sql.Rows) error) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err = visit(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	placeholders := func(ids []any) string { return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") }
	err := read("SELECT run_id,data FROM reports WHERE project_id=? AND run_id IN ("+placeholders(ids)+") ORDER BY created_at,id", append([]any{project}, ids...), func(rows *sql.Rows) error {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		report, err := decode([]byte(raw))
		if err == nil {
			reports[id] = append(reports[id], report)
		}
		return err
	})
	if err == nil && len(legacyIDs) > 0 {
		err = read("SELECT run_id,event_id,status,attempts,last_status_code,last_error,next_attempt_at FROM webhook_deliveries WHERE run_id IN ("+placeholders(legacyIDs)+")", legacyIDs, func(rows *sql.Rows) error {
			var id, event, status, lastError string
			var attempts, code int
			var next float64
			if err := rows.Scan(&id, &event, &status, &attempts, &code, &lastError, &next); err != nil {
				return err
			}
			deliveries[id] = publicWebhookDelivery(event, status, attempts, code, lastError, next)
			return nil
		})
	}
	if err == nil && len(redisIDs) > 0 {
		err = read("SELECT id,status,attempts,last_error FROM task_dispatches WHERE project_id=? AND id IN ("+placeholders(redisIDs)+")", append([]any{project}, redisIDs...), func(rows *sql.Rows) error {
			var id, status, lastError string
			var attempts int
			if err := rows.Scan(&id, &status, &attempts, &lastError); err != nil {
				return err
			}
			dispatches[id] = Obj{"status": status, "attempts": attempts, "last_error": lastError}
			return nil
		})
	}
	return reports, deliveries, dispatches, err
}

func runListPredicate(project, status, search string) (string, []any) {
	where, args := "project_id=?", []any{project}
	if status != "" {
		// Keep the indexed equality and the old exact status comparison even
		// on existing databases whose status column uses a case-insensitive collation.
		where += " AND status=? AND CAST(status AS BINARY)=CAST(? AS BINARY)"
		args = append(args, status, status)
	}
	if search != "" {
		// LOCATE keeps %, _ and backslashes literal. Normalize both operands
		// with the same Unicode rules, then use binary matching to retain accents.
		// The 5.2 collation also handles supplementary letters such as Deseret.
		// Non-string legacy JSON fields match str(value)=="", rather than "null".
		text := "LOWER(search_text COLLATE utf8mb4_unicode_520_ci) COLLATE utf8mb4_bin"
		where += " AND LOCATE(LOWER(CONVERT(? USING utf8mb4) COLLATE utf8mb4_unicode_520_ci) COLLATE utf8mb4_bin," + text + ")>0"
		args = append(args, search)
	}
	return where, args
}
