// Package persistence owns SQL connection scope, cancellation and table namespaces.
package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

// An optional validated prefix isolates integration tests in the provisioned
// database without dropping, deleting, or scheduling any live platform rows.
// Production uses the original table names with an empty prefix.
var tableNamesRE = regexp.MustCompile(`\b(monitoring_revisions|monitoring_evidence|monitoring_memberships|extension_registries|extension_versions|deleted_runs|artifact_cleanups|schema_migrations|workflow_csv_nodes|pressure_dashboard_scopes|plan_graph_cache|pressure_reports|pressure_report_jobs|pressure_report_sources|pressure_report_steps|pressure_report_timeline|pressure_report_issues|pressure_report_metrics|pressure_report_findings|workflow_datasets|task_dispatches|runner_events|webhook_deliveries|clusters|projects|plans|runs|reports|idempotency|sessions|instances|audit|workflows|api_collections|workflow_versions|api_collection_versions|environments|plan_workflow_links|plan_library_state)\b`)
var prefixRE = regexp.MustCompile(`^[A-Za-z0-9_]{0,48}$`)

type Database struct {
	*sql.DB
	Prefix string
	ctx    context.Context
	scoped bool
}
type Tx struct {
	*sql.Tx
	Prefix string
	ctx    context.Context
}

func RewriteTables(query, prefix string) string {
	if prefix == "" {
		return query
	}
	return tableNamesRE.ReplaceAllStringFunc(query, func(table string) string { return prefix + table })
}
func (d *Database) Exec(query string, args ...any) (sql.Result, error) {
	return d.ExecContext(d.Context(), query, args...)
}
func (d *Database) Query(query string, args ...any) (*sql.Rows, error) {
	return d.QueryContext(d.Context(), query, args...)
}
func (d *Database) QueryRow(query string, args ...any) *sql.Row {
	return d.QueryRowContext(d.Context(), query, args...)
}
func (d *Database) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, RewriteTables(query, d.Prefix), args...)
}
func (d *Database) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, RewriteTables(query, d.Prefix), args...)
}
func (d *Database) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, RewriteTables(query, d.Prefix), args...)
}
func (d *Database) BeginTx(ctx context.Context, options *sql.TxOptions) (*Tx, error) {
	tx, e := d.DB.BeginTx(ctx, options)
	if e != nil {
		return nil, e
	}
	return &Tx{Tx: tx, Prefix: d.Prefix, ctx: ctx}, nil
}
func (tx *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(tx.Context(), query, args...)
}
func (tx *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(tx.Context(), query, args...)
}
func (tx *Tx) QueryRow(query string, args ...any) *sql.Row {
	return tx.QueryRowContext(tx.Context(), query, args...)
}
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, RewriteTables(query, tx.Prefix), args...)
}
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, RewriteTables(query, tx.Prefix), args...)
}
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, RewriteTables(query, tx.Prefix), args...)
}
func ValidatePrefix(prefix string) error {
	if !prefixRE.MatchString(prefix) {
		return fmt.Errorf("invalid database table prefix")
	}
	return nil
}

// WithContext is a borrowed connection-pool view. It never mutates its owner.
func (d *Database) WithContext(ctx context.Context) *Database {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Database{DB: d.DB, Prefix: d.Prefix, ctx: ctx, scoped: true}
}
func (d *Database) Context() context.Context {
	if d != nil && d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}
func (tx *Tx) Context() context.Context {
	if tx != nil && tx.ctx != nil {
		return tx.ctx
	}
	return context.Background()
}
func (d *Database) Close() error {
	if d == nil || d.DB == nil || d.scoped {
		return nil
	}
	return d.DB.Close()
}
