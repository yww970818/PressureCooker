package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"pressurecooker/manager/internal/workspace"
)

// These bytes deliberately are neither JSON nor valid encrypted workspace data.
// The repository must persist opaque payloads without decoding or normalizing.
func TestMySQLWorkspaceRepositoryOpaquePayloadTenantAndCAS(t *testing.T) {
	p, _ := mysqlFixture(t)
	repo := workspace.New(p.projectID())
	ctx := context.Background()
	id := newID("collection-")
	payload := []byte("gcm1:opaque\x00\n{not JSON}  ")
	record := workspace.Record{ID: id, ProjectID: p.projectID(), Revision: 1, CreatedAt: now(), UpdatedAt: now(), Payload: payload}
	if err := p.transaction(func(tx *Tx) error { return repo.Save(ctx, tx, workspace.APICollections, record, true) }); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, p.DB, workspace.APICollections, id, false)
	if err != nil || !bytes.Equal(got.Payload, payload) {
		t.Fatalf("opaque payload changed: %v", err)
	}
	version, err := repo.Version(ctx, p.DB, workspace.APICollections, id, 1)
	if err != nil || !bytes.Equal(version.Payload, payload) {
		t.Fatalf("immutable version payload changed: %v", err)
	}
	foreign := workspace.New("project-foreign")
	if _, err = foreign.Get(ctx, p.DB, workspace.APICollections, id, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project get returned %v", err)
	}
	if _, err = foreign.Version(ctx, p.DB, workspace.APICollections, id, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project version returned %v", err)
	}
	record.Revision = 2
	record.Payload = []byte("gcm1:next exact payload\n")
	if err = p.transaction(func(tx *Tx) error { return repo.Save(ctx, tx, workspace.APICollections, record, false) }); err != nil {
		t.Fatal(err)
	}
	if err = p.transaction(func(tx *Tx) error { return repo.Save(ctx, tx, workspace.APICollections, record, false) }); !errors.Is(err, workspace.ErrRevisionConflict) {
		t.Fatalf("stale revision saved: %v", err)
	}
	version, err = repo.Version(ctx, p.DB, workspace.APICollections, id, 1)
	if err != nil || !bytes.Equal(version.Payload, payload) {
		t.Fatalf("old version rewritten: %v", err)
	}
	if err = foreign.Save(ctx, p.DB, workspace.APICollections, record, false); !errors.Is(err, workspace.ErrProjectMismatch) {
		t.Fatalf("foreign write returned %v", err)
	}
	if _, err = repo.Get(ctx, p.DB, workspace.Table("api_collections WHERE 1=1 --"), id, false); !errors.Is(err, workspace.ErrInvalidTable) {
		t.Fatalf("unlisted table accepted: %v", err)
	}
}

func TestMySQLWorkspaceRepositoryCancellationStopsPoolWait(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	held, err := p.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	repo := workspace.New(p.projectID())
	for _, kind := range []string{"get", "list", "summaries", "versions", "save"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch kind {
				case "get":
					_, err = repo.Get(ctx, p.DB, workspace.Workflows, "workflow-wait", false)
				case "list":
					_, err = repo.List(ctx, p.DB, workspace.Workflows)
				case "summaries":
					_, _, err = repo.Summaries(ctx, p.DB, workspace.Workflows, 1, 0)
				case "versions":
					_, err = repo.Versions(ctx, p.DB, workspace.Workflows, "workflow-wait")
				case "save":
					err = repo.Save(ctx, p.DB, workspace.Environments, workspace.Record{ID: "environment-wait", ProjectID: p.projectID(), Revision: 1, CreatedAt: now(), UpdatedAt: now(), Payload: []byte("opaque")}, true)
				}
				done <- err
			}()
			select {
			case err = <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				held.Close()
				<-done
				t.Fatal("repository kept waiting for a connection after deadline")
			}
		})
	}
}

func TestMySQLWorkspaceRequestScopeCancellationStopsPoolWait(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	held, err := p.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, kind := range []string{"get", "list", "summaries", "version", "version summaries", "create", "update", "delete"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancel()
			scoped := p.forRequest(ctx, p.projectID())
			done := make(chan error, 1)
			go func() {
				var err error
				switch kind {
				case "get":
					_, err = scoped.workspaceGet("api_collections", "collection-wait")
				case "list":
					_, err = scoped.workspaceList("api_collections")
				case "summaries":
					_, err = scoped.workspaceSummaryList("api_collections", url.Values{})
				case "version":
					_, err = scoped.workspaceVersions("api_collections", "collection-wait", 1)
				case "version summaries":
					_, err = scoped.workspaceVersionSummaries("api_collections", "collection-wait", url.Values{})
				case "create":
					_, err = scoped.workspaceCreate("api_collections", Obj{"name": "wait", "document": Obj{}})
				case "update":
					_, err = scoped.workspaceUpdate("api_collections", "collection-wait", Obj{"name": "wait", "document": Obj{}, "revision": 1})
				case "delete":
					_, err = scoped.workspaceDelete("api_collections", "collection-wait")
				}
				done <- err
			}()
			select {
			case err = <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("request scope lost cancellation: %v", err)
				}
			case <-time.After(time.Second):
				held.Close()
				<-done
				t.Fatal("workspace request kept waiting after deadline")
			}
		})
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = p.workspaceList("api_collections"); err != nil {
		t.Fatalf("cancelled child scope affected owner: %v", err)
	}
}

func TestMySQLWorkspaceLegacySummariesSingleConnectionPagination(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, err := p.projectCreate(Obj{"name": "other workspace"})
	if err != nil {
		t.Fatal(err)
	}
	other := p.forProject(str(project["id"]))
	for i := 0; i < 5; i++ {
		if _, err = p.workspaceCreate("api_collections", Obj{"name": fmt.Sprintf("local-%d", i), "document": Obj{}}); err != nil {
			t.Fatal(err)
		}
		if _, err = other.workspaceCreate("api_collections", Obj{"name": fmt.Sprintf("foreign-%d", i), "document": Obj{}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.DB.Exec("UPDATE api_collections SET summary_payload=NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("UPDATE api_collection_versions SET summary_payload=NULL"); err != nil {
		t.Fatal(err)
	}
	p.DB.SetMaxOpenConns(1)
	done := make(chan error, 1)
	go func() {
		page, err := p.workspaceSummaryList("api_collections", url.Values{"limit": {"2"}, "offset": {"1"}})
		if err != nil {
			done <- err
			return
		}
		if number(page["total"]) != 5 || len(arr(page["items"])) != 2 {
			done <- fmt.Errorf("wrong page: %v", page)
			return
		}
		for _, value := range arr(page["items"]) {
			item := obj(value)
			if str(item["project_id"]) != p.projectID() || item["document"] != nil {
				done <- fmt.Errorf("foreign/body item in page")
				return
			}
			versions, err := p.workspaceVersionSummaries("api_collections", str(item["id"]), url.Values{})
			if err != nil || len(arr(versions["items"])) != 1 {
				done <- fmt.Errorf("legacy version summaries: %v", err)
				return
			}
		}
		done <- nil
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		p.DB.SetMaxOpenConns(4)
		<-done
		t.Fatal("summary fallback held the only SQL connection while issuing nested reads")
	}
}

type workspaceCountedReader struct {
	*Database
	queries int
}

func (q *workspaceCountedReader) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q.queries++
	return q.Database.QueryRowContext(ctx, query, args...)
}

func (q *workspaceCountedReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.queries++
	return q.Database.QueryContext(ctx, query, args...)
}

func TestMySQLWorkspaceSummaryFallbackBatchesOnlyMissingPagePayloads(t *testing.T) {
	p, _ := mysqlFixture(t)
	for i := 0; i < 6; i++ {
		if _, err := p.workspaceCreate("api_collections", Obj{"name": fmt.Sprintf("batch-%d", i), "document": Obj{}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.DB.Exec("UPDATE api_collections SET summary_payload=NULL"); err != nil {
		t.Fatal(err)
	}
	p.DB.SetMaxOpenConns(1)
	q := &workspaceCountedReader{Database: p.DB}
	repo := workspace.New(p.projectID())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	heads, total, err := repo.Summaries(ctx, q, workspace.APICollections, 4, 1)
	if err != nil || total != 6 || len(heads) != 4 {
		t.Fatalf("summary pagination: total %d rows %d error %v", total, len(heads), err)
	}
	if p.DB.Stats().InUse != 0 {
		t.Fatal("summary cursor still holds a connection")
	}
	legacy, err := repo.MissingSummaryRecords(ctx, q, workspace.APICollections, heads)
	if err != nil || len(legacy) != 4 {
		t.Fatalf("legacy batch rows %d: %v", len(legacy), err)
	}
	if q.queries != 3 {
		t.Fatalf("legacy page used %d queries, want count + page + one fallback", q.queries)
	}
	for _, head := range heads {
		if _, present := legacy[head.ID]; !present {
			t.Fatal("fallback returned different page rows")
		}
	}
	for i := range heads {
		heads[i].HasPayload = true
	}
	legacy, err = repo.MissingSummaryRecords(ctx, q, workspace.APICollections, heads)
	if err != nil || len(legacy) != 0 || q.queries != 3 {
		t.Fatalf("complete projections read full payloads: queries %d rows %d error %v", q.queries, len(legacy), err)
	}
	// History uses the same single batched fallback rather than one query per
	// revision, while its owner remains restricted to this project.
	id := heads[0].ID
	for revision := 1; revision < 4; revision++ {
		if _, err := p.workspaceUpdate("api_collections", id, Obj{"name": "history", "document": Obj{}, "revision": revision}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.DB.Exec("UPDATE api_collection_versions SET summary_payload=NULL WHERE object_id=?", id); err != nil {
		t.Fatal(err)
	}
	q.queries = 0
	heads, total, err = repo.VersionSummaries(ctx, q, workspace.APICollections, id, 3, 1)
	if err != nil || total != 4 || len(heads) != 3 {
		t.Fatalf("version page total %d rows %d: %v", total, len(heads), err)
	}
	versions, err := repo.MissingSummaryVersions(ctx, q, workspace.APICollections, id, heads)
	if err != nil || len(versions) != 3 || q.queries != 3 {
		t.Fatalf("history fallback queries %d rows %d: %v", q.queries, len(versions), err)
	}
	for _, revision := range []int64{1, 2, 3} {
		if _, present := versions[revision]; !present {
			t.Fatalf("version page missed revision %d", revision)
		}
	}
}

func TestMySQLWorkspaceBackfillUsesStoredProjectForLegacyOpaquePayload(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, err := p.projectCreate(Obj{"name": "legacy owner"})
	if err != nil {
		t.Fatal(err)
	}
	scoped := p.forProject(str(project["id"]))
	collection, err := scoped.workspaceCreate("api_collections", Obj{"name": "legacy", "document": Obj{}})
	if err != nil {
		t.Fatal(err)
	}
	id := str(collection["id"])
	delete(collection, "project_id")
	payload, err := p.sealWorkspace("api_collections", id, 1, collection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("UPDATE api_collections SET payload=?,summary_payload=NULL WHERE id=?", payload, id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err = p.forRequest(ctx, p.projectID()).rebuildPageReadModels(1); err != nil {
		t.Fatalf("global backfill failed to finish legacy foreign-project row: %v", err)
	}
	var raw string
	var summary sql.NullString
	if err = p.DB.QueryRow("SELECT payload,summary_payload FROM api_collections WHERE id=?", id).Scan(&raw, &summary); err != nil {
		t.Fatal(err)
	}
	if raw != payload || !summary.Valid {
		t.Fatal("backfill rewrote opaque source or missed legacy owner projection")
	}
	page, err := scoped.workspaceSummaryList("api_collections", url.Values{})
	if err != nil || len(arr(page["items"])) != 1 || obj(arr(page["items"])[0])["project_id"] != project["id"] {
		t.Fatalf("legacy owner summary: %v", err)
	}
}
