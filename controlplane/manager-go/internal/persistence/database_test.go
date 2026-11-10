package persistence

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fixtureConnector struct {
	mu    sync.Mutex
	query string
}

func (f *fixtureConnector) Connect(context.Context) (driver.Conn, error) { return fixtureConn{f}, nil }
func (f *fixtureConnector) Driver() driver.Driver                        { return fixtureDriver{f} }

type fixtureDriver struct{ f *fixtureConnector }

func (d fixtureDriver) Open(string) (driver.Conn, error) { return fixtureConn{d.f}, nil }

type fixtureConn struct{ f *fixtureConnector }

func (fixtureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (fixtureConn) Close() error                        { return nil }
func (fixtureConn) Begin() (driver.Tx, error)           { return fixtureTx{}, nil }
func (c fixtureConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	c.f.mu.Lock()
	c.f.query = q
	c.f.mu.Unlock()
	return &fixtureRows{}, nil
}
func (c fixtureConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	c.f.mu.Lock()
	c.f.query = q
	c.f.mu.Unlock()
	return driver.RowsAffected(1), nil
}

type fixtureTx struct{}

func (fixtureTx) Commit() error   { return nil }
func (fixtureTx) Rollback() error { return nil }

type fixtureRows struct{ read bool }

func (*fixtureRows) Columns() []string { return []string{"id"} }
func (*fixtureRows) Close() error      { return nil }
func (r *fixtureRows) Next(v []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	v[0] = "fixture"
	return nil
}
func fixtureDB(t *testing.T) (*Database, *fixtureConnector) {
	t.Helper()
	f := &fixtureConnector{}
	raw := sql.OpenDB(f)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	return &Database{DB: raw, Prefix: "fixture_"}, f
}

// An absent context binding leaves these calls stuck behind the held connection.
func TestScopedLegacyOperationsRespectCancellation(t *testing.T) {
	for _, op := range []string{"query", "row", "exec", "begin"} {
		t.Run(op, func(t *testing.T) {
			db, _ := fixtureDB(t)
			held, e := db.DB.Conn(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			defer held.Close()
			ctx, cancel := context.WithCancel(context.Background())
			scoped := db.WithContext(ctx)
			done := make(chan error, 1)
			go func() {
				var err error
				switch op {
				case "query":
					var rows *sql.Rows
					rows, err = scoped.Query("SELECT id FROM runs")
					if rows != nil {
						rows.Close()
					}
				case "row":
					var id string
					err = scoped.QueryRow("SELECT id FROM runs").Scan(&id)
				case "exec":
					_, err = scoped.Exec("UPDATE runs SET status='STOPPED'")
				case "begin":
					var tx *Tx
					tx, err = scoped.BeginTx(scoped.Context(), nil)
					if tx != nil {
						tx.Rollback()
					}
				}
				done <- err
			}()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v, want cancellation", err)
				}
			case <-time.After(500 * time.Millisecond):
				held.Close()
				<-done
				t.Fatal("request cancellation did not stop pool wait")
			}
		})
	}
}
func TestScopedViewsPreserveOwnerAndNamespace(t *testing.T) {
	db, f := fixtureDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	a, b := db.WithContext(ctx), db.WithContext(context.Background())
	cancel()
	if e := a.QueryRow("SELECT id FROM runs").Scan(new(string)); !errors.Is(e, context.Canceled) {
		t.Fatalf("first request: %v", e)
	}
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	var value string
	if e := b.QueryRow("SELECT id FROM runs WHERE id=?", "fixture").Scan(&value); e != nil || value != "fixture" {
		t.Fatalf("other request: %q %v", value, e)
	}
	f.mu.Lock()
	query := f.query
	f.mu.Unlock()
	if query != "SELECT id FROM fixture_runs WHERE id=?" {
		t.Fatalf("query lost namespace: %s", query)
	}
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	if e := b.QueryRow("SELECT id FROM runs").Scan(&value); e == nil {
		t.Fatal("owner shutdown left pool open")
	}
}
