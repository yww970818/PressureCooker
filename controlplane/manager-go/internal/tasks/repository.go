package tasks

import (
	"context"
	"database/sql"
	"strings"
)

// DB is implemented by a database or transaction, including the application's
// prefix and request-context adapters. The repository does not own connections.
type DB interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Record contains queryable row fields and the caller's exact durable payload.
// Data can contain canonical JSON or encrypted fields; it is never interpreted.
type Record struct {
	ID        string
	Status    Status
	CreatedAt string
	ProjectID string
	Data      []byte
}

type Repository struct{ DB DB }

func (r Repository) Load(ctx context.Context, id, project string, lock bool) (Record, error) {
	query := "SELECT id,status,created_at,data,project_id FROM runs WHERE id=?"
	args := []any{id}
	if project != "" {
		query += " AND project_id=?"
		args = append(args, project)
	}
	if lock {
		query += " FOR UPDATE"
	}
	var record Record
	err := r.DB.QueryRowContext(ctx, query, args...).Scan(&record.ID, &record.Status, &record.CreatedAt, &record.Data, &record.ProjectID)
	return record, err
}

// List materializes all rows before returning so callers may decrypt or perform
// follow-up reads even when the process pool has only one connection.
func (r Repository) List(ctx context.Context, project string, statuses []Status) ([]Record, error) {
	query := "SELECT id,status,created_at,data,project_id FROM runs"
	filters := []string{}
	args := []any{}
	if len(statuses) > 0 {
		filters = append(filters, "status IN ("+strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",")+")")
		for _, status := range statuses {
			args = append(args, string(status))
		}
	}
	if project != "" {
		filters = append(filters, "project_id=?")
		args = append(args, project)
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	rows, err := r.DB.QueryContext(ctx, query+" ORDER BY created_at,id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	for rows.Next() {
		var record Record
		if err = rows.Scan(&record.ID, &record.Status, &record.CreatedAt, &record.Data, &record.ProjectID); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, rows.Close()
}

// Save retains the original insert/upsert behavior: existing creation time and
// project ownership never change when task metadata or its status is updated.
func (r Repository) Save(ctx context.Context, record Record) error {
	_, err := r.DB.ExecContext(ctx, "INSERT INTO runs(id,status,created_at,data,project_id) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE status=VALUES(status),data=VALUES(data)", record.ID, string(record.Status), record.CreatedAt, string(record.Data), record.ProjectID)
	return err
}
