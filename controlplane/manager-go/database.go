package main

import (
	"context"
	"database/sql"
	"pressurecooker/manager/internal/persistence"
)

// Compatibility names keep transport and legacy fixtures on the same typed
// persistence implementation while domain repositories depend on narrow SQL APIs.
type Database = persistence.Database
type Tx = persistence.Tx

// Adapt legacy read helpers without losing request cancellation or table prefixes.
type contextQueryer struct {
	ctx context.Context
	db  *Database
}

func (q contextQueryer) Query(query string, args ...any) (*sql.Rows, error) {
	return q.db.QueryContext(q.ctx, query, args...)
}

func (q contextQueryer) QueryRow(query string, args ...any) *sql.Row {
	return q.db.QueryRowContext(q.ctx, query, args...)
}

func validatePrefix(prefix string) error { return persistence.ValidatePrefix(prefix) }
