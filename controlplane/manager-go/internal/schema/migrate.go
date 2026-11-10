// Package schema owns versioned, recoverable MySQL schema changes. It has no
// dependency on platform DTOs, secrets, business services or global table rewrites.
package schema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

//go:embed migrations/*.json
var files embed.FS

type Column struct {
	Table      string `json:"table"`
	Name       string `json:"name"`
	Definition string `json:"definition"`
	Backfill   string `json:"backfill,omitempty"`
}
type Index struct {
	Table   string `json:"table"`
	Name    string `json:"name"`
	Columns string `json:"columns"`
}
type Migration struct {
	Version    int      `json:"version"`
	Name       string   `json:"name"`
	Statements []string `json:"statements,omitempty"`
	Columns    []Column `json:"columns,omitempty"`
	Indexes    []Index  `json:"indexes,omitempty"`
}

func (m Migration) checksum() string {
	b, _ := json.Marshal(m)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Error keeps actionable stage/version information while preventing a database
// error from copying SQL, JSON, connection addresses or credentials into logs.
type Error struct {
	Version int
	Stage   string
	cause   error
}

func (e *Error) Error() string {
	return fmt.Sprintf("schema migration version=%d stage=%s error_type=%T", e.Version, e.Stage, e.cause)
}
func (e *Error) Unwrap() error                           { return e.cause }
func failure(version int, stage string, err error) error { return &Error{version, stage, err} }

var identifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var prefixPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{0,40}$`)

func quote(s string) string { return "`" + s + "`" }

func Catalog() ([]Migration, error) {
	entries, err := files.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	catalog := []Migration{}
	for _, entry := range entries {
		b, e := files.ReadFile("migrations/" + entry.Name())
		if e != nil {
			return nil, e
		}
		var m Migration
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.DisallowUnknownFields()
		if e = d.Decode(&m); e != nil {
			return nil, e
		}
		catalog = append(catalog, m)
	}
	return catalog, nil
}
func Apply(ctx context.Context, db *sql.DB, prefix string) error {
	catalog, err := Catalog()
	if err != nil {
		return failure(0, "catalog", err)
	}
	return Run(ctx, db, prefix, catalog)
}
func validate(prefix string, catalog []Migration) error {
	if !prefixPattern.MatchString(prefix) || len(catalog) == 0 {
		return errors.New("invalid schema namespace or catalog")
	}
	for i, m := range catalog {
		if m.Version != i+1 || !identifier.MatchString(m.Name) || len(m.Name) > 100 {
			return errors.New("invalid migration order or name")
		}
		seen := map[string]bool{}
		for _, c := range m.Columns {
			key := c.Table + "." + c.Name
			if !identifier.MatchString(c.Table) || !identifier.MatchString(c.Name) || c.Definition == "" || seen[key] || len(prefix+c.Table) > 64 {
				return errors.New("invalid migration column")
			}
			seen[key] = true
		}
		for _, index := range m.Indexes {
			if !identifier.MatchString(index.Table) || !identifier.MatchString(index.Name) || len(prefix+index.Table) > 64 {
				return errors.New("invalid migration index")
			}
			for _, column := range strings.Split(index.Columns, ",") {
				if !identifier.MatchString(column) {
					return errors.New("invalid index column")
				}
			}
		}
	}
	return nil
}

type history struct {
	Name, Checksum, State string
	Plan                  []string
}

func Run(ctx context.Context, db *sql.DB, prefix string, catalog []Migration) (result error) {
	if err := validate(prefix, catalog); err != nil {
		return failure(0, "catalog", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return failure(0, "connection", err)
	}
	defer conn.Close()
	var database string
	if err = conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		return failure(0, "database", err)
	}
	sum := sha256.Sum256([]byte(database + "\x00" + prefix))
	lock := "pressurecooker-schema:" + hex.EncodeToString(sum[:20])
	if err = acquire(ctx, conn, lock); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return failure(0, "lock", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var released sql.NullInt64
		err := conn.QueryRowContext(cleanup, "SELECT RELEASE_LOCK(?)", lock).Scan(&released)
		if err != nil || !released.Valid || released.Int64 != 1 {
			// A named lock belongs to the physical session. Never return a possibly
			// locked session to the shared pool after a failed release.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			if err == nil {
				err = errors.New("schema lock release failed")
			}
			result = errors.Join(result, failure(0, "unlock", err))
		}
	}()
	table := quote(prefix + "schema_migrations")
	_, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+"(version INT NOT NULL PRIMARY KEY,name VARCHAR(100) NOT NULL,checksum CHAR(64) NOT NULL,state VARCHAR(16) NOT NULL,plan LONGTEXT NOT NULL,applied_at TIMESTAMP(6) NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	if err != nil {
		return failure(0, "history", err)
	}
	prior, err := readHistory(ctx, conn, table, catalog)
	if err != nil {
		return err
	}
	for _, m := range catalog {
		h, exists := prior[m.Version]
		if exists && h.State == "APPLIED" {
			continue
		}
		if !exists {
			plan, e := missingColumns(ctx, conn, prefix, m.Columns)
			if e != nil {
				return failure(m.Version, "plan", e)
			}
			encoded, _ := json.Marshal(plan)
			_, e = conn.ExecContext(ctx, "INSERT INTO "+table+"(version,name,checksum,state,plan) VALUES(?,?,?,'APPLYING',?)", m.Version, m.Name, m.checksum(), string(encoded))
			if e != nil {
				return failure(m.Version, "journal", e)
			}
			h.Plan = plan
		}
		if err = applyMigration(ctx, conn, prefix, m, h.Plan); err != nil {
			return failure(m.Version, "apply", err)
		}
		if _, err = conn.ExecContext(ctx, "UPDATE "+table+" SET state='APPLIED',applied_at=CURRENT_TIMESTAMP(6) WHERE version=?", m.Version); err != nil {
			return failure(m.Version, "complete", err)
		}
	}
	return nil
}
func acquire(ctx context.Context, conn *sql.Conn, name string) error {
	for {
		var owned sql.NullInt64
		if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&owned); err != nil {
			return err
		}
		if !owned.Valid {
			return errors.New("schema lock unavailable")
		}
		if owned.Int64 == 1 {
			return nil
		}
		timer := time.NewTimer(30 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func readHistory(ctx context.Context, conn *sql.Conn, table string, catalog []Migration) (map[int]history, error) {
	rows, err := conn.QueryContext(ctx, "SELECT version,name,checksum,state,plan FROM "+table+" ORDER BY version")
	if err != nil {
		return nil, failure(0, "history", err)
	}
	defer rows.Close()
	prior := map[int]history{}
	for rows.Next() {
		var version int
		var h history
		var raw string
		if err = rows.Scan(&version, &h.Name, &h.Checksum, &h.State, &raw); err != nil {
			return nil, failure(0, "history", err)
		}
		if version != len(prior)+1 || version > len(catalog) || (h.State != "APPLIED" && h.State != "APPLYING") {
			return nil, failure(version, "history", errors.New("unknown or inconsistent schema version"))
		}
		m := catalog[version-1]
		if h.Name != m.Name || h.Checksum != m.checksum() {
			return nil, failure(version, "checksum", errors.New("migration definition changed"))
		}
		if version > 1 && prior[version-1].State != "APPLIED" {
			return nil, failure(version, "history", errors.New("unfinished predecessor"))
		}
		if err = json.Unmarshal([]byte(raw), &h.Plan); err != nil {
			return nil, failure(version, "plan", err)
		}
		allowed := map[string]bool{}
		for _, c := range m.Columns {
			allowed[c.Table+"."+c.Name] = true
		}
		seen := map[string]bool{}
		for _, key := range h.Plan {
			if !allowed[key] || seen[key] {
				return nil, failure(version, "plan", errors.New("invalid saved column plan"))
			}
			seen[key] = true
		}
		prior[version] = h
	}
	if err = rows.Err(); err != nil {
		return nil, failure(0, "history", err)
	}
	return prior, nil
}
func columns(ctx context.Context, conn *sql.Conn, table string) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=?", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		found[name] = true
	}
	return found, rows.Err()
}
func missingColumns(ctx context.Context, conn *sql.Conn, prefix string, list []Column) ([]string, error) {
	cache := map[string]map[string]bool{}
	missing := []string{}
	for _, column := range list {
		if cache[column.Table] == nil {
			found, err := columns(ctx, conn, prefix+column.Table)
			if err != nil {
				return nil, err
			}
			cache[column.Table] = found
		}
		if !cache[column.Table][column.Name] {
			missing = append(missing, column.Table+"."+column.Name)
		}
	}
	return missing, nil
}
func applyMigration(ctx context.Context, conn *sql.Conn, prefix string, m Migration, plan []string) error {
	for _, statement := range m.Statements {
		if _, err := conn.ExecContext(ctx, strings.ReplaceAll(statement, "{{prefix}}", prefix)); err != nil {
			return err
		}
	}
	planned := map[string]bool{}
	for _, key := range plan {
		planned[key] = true
	}
	tables := []string{}
	groups := map[string][]Column{}
	for _, c := range m.Columns {
		if planned[c.Table+"."+c.Name] {
			if groups[c.Table] == nil {
				tables = append(tables, c.Table)
			}
			groups[c.Table] = append(groups[c.Table], c)
		}
	}
	for _, table := range tables {
		found, err := columns(ctx, conn, prefix+table)
		if err != nil {
			return err
		}
		additions, updates := []string{}, []string{}
		for _, c := range groups[table] {
			if !found[c.Name] {
				additions = append(additions, "ADD COLUMN "+quote(c.Name)+" "+c.Definition)
			}
			if c.Backfill != "" {
				updates = append(updates, quote(c.Name)+"="+c.Backfill)
			}
		}
		if len(additions) > 0 {
			if _, err = conn.ExecContext(ctx, "ALTER TABLE "+quote(prefix+table)+" "+strings.Join(additions, ",")); err != nil {
				return err
			}
		}
		if len(updates) > 0 {
			if _, err = conn.ExecContext(ctx, "UPDATE "+quote(prefix+table)+" SET "+strings.Join(updates, ",")); err != nil {
				return err
			}
		}
	}
	for _, index := range m.Indexes {
		var count int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?", prefix+index.Table, index.Name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := conn.ExecContext(ctx, "CREATE INDEX "+quote(index.Name)+" ON "+quote(prefix+index.Table)+"("+index.Columns+")"); err != nil {
				return err
			}
		}
	}
	return nil
}
