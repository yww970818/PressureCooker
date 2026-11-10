package schema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

var driverNonce atomic.Int64
var privateDriverFailure = errors.New("private-database-address-and-sql")

type faultDriver struct {
	mode   string
	closed atomic.Int64
}
type faultConn struct{ owner *faultDriver }

func (d *faultDriver) Open(string) (driver.Conn, error)  { return &faultConn{d}, nil }
func (c *faultConn) Close() error                        { c.owner.closed.Add(1); return nil }
func (c *faultConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *faultConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *faultConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (c *faultConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT DATABASE()"):
		return &faultRows{values: []driver.Value{"isolated"}}, nil
	case strings.Contains(query, "GET_LOCK"):
		if c.owner.mode == "acquire" {
			return nil, privateDriverFailure
		}
		return &faultRows{values: []driver.Value{int64(1)}}, nil
	case strings.Contains(query, "RELEASE_LOCK"):
		if c.owner.mode == "release" {
			return nil, privateDriverFailure
		}
		return &faultRows{values: []driver.Value{int64(1)}}, nil
	default:
		if c.owner.mode == "history" {
			return &faultRows{err: privateDriverFailure}, nil
		}
		return &faultRows{}, nil
	}
}

type faultRows struct {
	values []driver.Value
	err    error
}

func (r *faultRows) Columns() []string {
	if len(r.values) > 0 {
		return []string{"value"}
	}
	return []string{"version", "name", "checksum", "state", "plan"}
}
func (r *faultRows) Close() error { return nil }
func (r *faultRows) Next(dest []driver.Value) error {
	if r.err != nil {
		return r.err
	}
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values)
	r.values = nil
	return nil
}
func faultDatabase(t *testing.T, mode string) (*sql.DB, *faultDriver) {
	t.Helper()
	d := &faultDriver{mode: mode}
	name := fmt.Sprintf("schema_fault_%d", driverNonce.Add(1))
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, d
}

func TestUncertainNamedLockSessionsAreDiscarded(t *testing.T) {
	for _, mode := range []string{"acquire", "release"} {
		t.Run(mode, func(t *testing.T) {
			db, d := faultDatabase(t, mode)
			err := Run(context.Background(), db, "", []Migration{{Version: 1, Name: "probe"}})
			if err == nil {
				t.Fatal("fault was ignored")
			}
			if d.closed.Load() != 1 || db.Stats().Idle != 0 {
				t.Fatal("possibly lock-owning physical session returned to the connection pool")
			}
			if strings.Contains(err.Error(), "private-") || !errors.Is(err, privateDriverFailure) {
				t.Fatal("fault leaked or cause was lost")
			}
		})
	}
}
func TestHistoryReadErrorsAreRedacted(t *testing.T) {
	db, _ := faultDatabase(t, "history")
	err := Run(context.Background(), db, "", []Migration{{Version: 1, Name: "probe"}})
	if err == nil || strings.Contains(err.Error(), "private-") || !strings.Contains(err.Error(), "stage=history") || !errors.Is(err, privateDriverFailure) {
		t.Fatalf("history failure was not safely classified: %v", err)
	}
}
func TestFrozenCatalogAndValidation(t *testing.T) {
	catalog, err := Catalog()
	if err != nil || len(catalog) == 0 {
		t.Fatal("catalog", err)
	}
	if err = validate("test_", catalog); err != nil {
		t.Fatal(err)
	}
	again, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range catalog {
		if m.checksum() != again[i].checksum() {
			t.Fatal("unstable schema checksum")
		}
	}
	for _, tc := range []struct {
		prefix string
		list   []Migration
	}{
		{"unsafe-prefix", catalog}, {"", nil}, {"", []Migration{{Version: 2, Name: "skip"}}}, {"", []Migration{{Version: 1, Name: "bad name"}}},
		{"", []Migration{{Version: 1, Name: "duplicate", Columns: []Column{{Table: "t", Name: "c", Definition: "INT"}, {Table: "t", Name: "c", Definition: "INT"}}}}},
		{"", []Migration{{Version: 1, Name: "unsafe", Indexes: []Index{{Table: "t", Name: "i", Columns: "a);DROP"}}}}},
	} {
		if err := Run(context.Background(), nil, tc.prefix, tc.list); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
}
