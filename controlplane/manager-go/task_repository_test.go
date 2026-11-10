package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"pressurecooker/manager/internal/tasks"
)

// A repository must never parse, normalize or encrypt payloads. Existing
// canonical JSON and encrypted workspace envelopes belong to the caller.
func TestTaskRepositoryKeepsOpaqueBytesAndProjectScope(t *testing.T) {
	p, _ := mysqlFixture(t)
	repo := tasks.Repository{DB: p.DB}
	ctx := context.Background()
	first := tasks.Record{ID: newID("run-"), Status: tasks.Running, CreatedAt: "2026-01-02T03:04:05Z", ProjectID: defaultProjectID, Data: []byte(" {\"encrypted\":\"opaque\", \"future\": [3, 1]}\n")}
	second := tasks.Record{ID: newID("run-"), Status: tasks.Queued, CreatedAt: "2026-01-02T03:04:06Z", ProjectID: "project-other", Data: []byte("opaque historical envelope")}
	for _, record := range []tasks.Record{first, second} {
		if err := repo.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	p.DB.SetMaxOpenConns(1)
	got, err := repo.Load(ctx, first.ID, defaultProjectID, false)
	if err != nil || got.ID != first.ID || got.Status != tasks.Running || got.ProjectID != first.ProjectID || got.CreatedAt != first.CreatedAt || !bytes.Equal(got.Data, first.Data) {
		t.Fatalf("changed opaque record: %+v, %v", got, err)
	}
	if _, err = repo.Load(ctx, second.ID, defaultProjectID, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project load: %v", err)
	}
	rows, err := repo.List(ctx, defaultProjectID, []tasks.Status{tasks.Running})
	if err != nil || len(rows) != 1 || rows[0].ID != first.ID || !bytes.Equal(rows[0].Data, first.Data) {
		t.Fatalf("scoped list: %+v, %v", rows, err)
	}
	// A materialized list must release the sole database connection.
	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = p.DB.PingContext(deadline); err != nil {
		t.Fatal("list retained SQL cursor", err)
	}
	first.Status, first.Data = tasks.Stopped, []byte("\t{\"retained\":true}\n")
	if err = repo.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Load(ctx, first.ID, "", false)
	if err != nil || got.Status != tasks.Stopped || !bytes.Equal(got.Data, first.Data) {
		t.Fatalf("changed opaque update: %+v, %v", got, err)
	}
	all, err := repo.List(ctx, "", nil)
	if err != nil || len(all) != 2 || all[0].ID != first.ID || all[1].ID != second.ID {
		t.Fatalf("global scheduling list: %+v, %v", all, err)
	}
}

func TestTaskRepositoryContextCancelsPoolWaitAndRowLock(t *testing.T) {
	p, _ := mysqlFixture(t)
	ctx := context.Background()
	repo := tasks.Repository{DB: p.DB}
	record := tasks.Record{ID: newID("run-"), Status: tasks.Running, CreatedAt: now(), ProjectID: defaultProjectID, Data: []byte(`{"status":"RUNNING"}`)}
	if err := repo.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	p.DB.SetMaxOpenConns(1)
	conn, err := p.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"load", func(ctx context.Context) error { _, err := repo.Load(ctx, record.ID, "", false); return err }},
		{"list", func(ctx context.Context) error { _, err := repo.List(ctx, "", nil); return err }},
		{"save", func(ctx context.Context) error { return repo.Save(ctx, record) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			deadline, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
			defer cancel()
			if err := operation.run(deadline); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("pool wait ignored context: %v", err)
			}
		})
	}
	conn.Close()
	p.DB.SetMaxOpenConns(2)
	owner, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Rollback()
	if _, err = (tasks.Repository{DB: owner}).Load(ctx, record.ID, "", true); err != nil {
		t.Fatal(err)
	}
	waiter, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Rollback()
	deadline, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	_, err = (tasks.Repository{DB: waiter}).Load(deadline, record.ID, "", true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("task lock ignored context: %v", err)
	}
}
