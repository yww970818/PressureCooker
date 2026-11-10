// Package workspace persists project-scoped workspace records and immutable
// versions. Payloads and summaries are opaque: encryption, JSON and projections
// belong to the caller and are never decoded or reserialized here.
package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Table string

const (
	Workflows      Table = "workflows"
	APICollections Table = "api_collections"
	Environments   Table = "environments"
)

var (
	ErrInvalidTable     = errors.New("unsupported workspace table")
	ErrProjectMismatch  = errors.New("workspace project mismatch")
	ErrRevisionConflict = errors.New("workspace revision conflict")
)

// Only these names may become SQL identifiers. Values always use parameters.
func tables(table Table) (root, versions string, err error) {
	switch table {
	case Workflows:
		return "workflows", "workflow_versions", nil
	case APICollections:
		return "api_collections", "api_collection_versions", nil
	case Environments:
		return "environments", "", nil
	default:
		return "", "", ErrInvalidTable
	}
}

type RowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Reader interface {
	RowReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type Writer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Record struct {
	ID, ProjectID        string
	Revision             int64
	CreatedAt, UpdatedAt string
	Payload              []byte
}

type Version struct {
	ID, ObjectID string
	Revision     int64
	CreatedAt    string
	Payload      []byte
}

type Summary struct {
	ID         string
	Revision   int64
	Payload    []byte
	HasPayload bool
}

// Repository is immutable and safe to share. Its scope applies to every read
// and write, including history (whose table has no project_id column).
type Repository struct{ projectID string }

func New(projectID string) Repository { return Repository{projectID: projectID} }

func (r Repository) Get(ctx context.Context, q RowReader, table Table, id string, lock bool) (Record, error) {
	root, _, err := tables(table)
	if err != nil {
		return Record{}, err
	}
	if err = ctx.Err(); err != nil {
		return Record{}, err
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	record := Record{ID: id}
	err = q.QueryRowContext(ctx, "SELECT revision,payload,project_id FROM "+root+" WHERE id=? AND project_id=? AND deleted=0"+suffix, id, r.projectID).Scan(&record.Revision, &record.Payload, &record.ProjectID)
	return record, err
}

func (r Repository) Owner(ctx context.Context, q RowReader, table Table, id string) (string, error) {
	root, _, err := tables(table)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	var owner string
	err = q.QueryRowContext(ctx, "SELECT project_id FROM "+root+" WHERE id=? AND project_id=? AND deleted=0", id, r.projectID).Scan(&owner)
	return owner, err
}

// List materializes all rows and closes the cursor before returning. The caller
// may decrypt or perform nested reads even with a single SQL connection.
func (r Repository) List(ctx context.Context, q Reader, table Table) ([]Record, error) {
	root, _, err := tables(table)
	if err != nil {
		return nil, err
	}
	limit := " LIMIT 200"
	if table == Environments {
		limit = ""
	}
	rows, err := q.QueryContext(ctx, "SELECT id,revision,payload FROM "+root+" WHERE deleted=0 AND project_id=? ORDER BY updated_at DESC,id DESC"+limit, r.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Record{}
	for rows.Next() {
		record := Record{ProjectID: r.projectID}
		if err = rows.Scan(&record.ID, &record.Revision, &record.Payload); err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	return items, rows.Err()
}

// Save runs inside the caller's transaction so current rows, immutable history
// and caller-owned projections commit atomically. Row locking remains caller
// policy; the revision predicate also protects the write from stale callers.
func (r Repository) Save(ctx context.Context, q Writer, table Table, record Record, create bool) error {
	root, versions, err := tables(table)
	if err != nil {
		return err
	}
	if record.ProjectID != r.projectID {
		return ErrProjectMismatch
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if create {
		_, err = q.ExecContext(ctx, "INSERT INTO "+root+"(id,revision,deleted,created_at,updated_at,payload,project_id) VALUES(?,?,0,?,?,?,?)", record.ID, record.Revision, record.CreatedAt, record.UpdatedAt, string(record.Payload), r.projectID)
	} else {
		var result sql.Result
		result, err = q.ExecContext(ctx, "UPDATE "+root+" SET revision=?,updated_at=?,payload=? WHERE id=? AND project_id=? AND revision=? AND deleted=0", record.Revision, record.UpdatedAt, string(record.Payload), record.ID, r.projectID, record.Revision-1)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count == 0 {
				return ErrRevisionConflict
			}
		}
	}
	if err != nil || versions == "" {
		return err
	}
	// Copy the exact ciphertext inside the same transaction. Sending large
	// payloads to MySQL a second time adds transfer and allocation overhead.
	result, err := q.ExecContext(ctx, "INSERT INTO "+versions+"(id,object_id,revision,created_at,payload) SELECT ?,id,revision,?,payload FROM "+root+" WHERE id=? AND project_id=? AND revision=? AND deleted=0", fmt.Sprintf("%s-r%d", record.ID, record.Revision), record.UpdatedAt, record.ID, r.projectID, record.Revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrRevisionConflict
	}
	return err
}

func (r Repository) DeletionState(ctx context.Context, q RowReader, table Table, id string) (bool, error) {
	root, _, err := tables(table)
	if err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	var deleted int
	err = q.QueryRowContext(ctx, "SELECT deleted FROM "+root+" WHERE id=? AND project_id=? FOR UPDATE", id, r.projectID).Scan(&deleted)
	return deleted != 0, err
}

func (r Repository) Delete(ctx context.Context, q Writer, table Table, id, updatedAt string) error {
	root, _, err := tables(table)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE "+root+" SET deleted=1,updated_at=? WHERE id=? AND project_id=?", updatedAt, id, r.projectID)
	return err
}

func (r Repository) versionScope(root string) string {
	return " AND EXISTS (SELECT 1 FROM " + root + " WHERE id=? AND project_id=? AND deleted=0)"
}

func (r Repository) Version(ctx context.Context, q RowReader, table Table, id string, revision int64) (Version, error) {
	root, versions, err := tables(table)
	if err != nil {
		return Version{}, err
	}
	if versions == "" {
		return Version{}, ErrInvalidTable
	}
	if err = ctx.Err(); err != nil {
		return Version{}, err
	}
	v := Version{ObjectID: id, Revision: revision}
	err = q.QueryRowContext(ctx, "SELECT payload FROM "+versions+" WHERE object_id=? AND revision=?"+r.versionScope(root), id, revision, id, r.projectID).Scan(&v.Payload)
	return v, err
}

func (r Repository) Versions(ctx context.Context, q Reader, table Table, id string) ([]Version, error) {
	root, versions, err := tables(table)
	if err != nil {
		return nil, err
	}
	if versions == "" {
		return nil, ErrInvalidTable
	}
	rows, err := q.QueryContext(ctx, "SELECT revision,payload FROM "+versions+" WHERE object_id=?"+r.versionScope(root)+" ORDER BY revision DESC LIMIT 200", id, id, r.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Version{}
	for rows.Next() {
		v := Version{ObjectID: id}
		if err = rows.Scan(&v.Revision, &v.Payload); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func readSummaries(rows *sql.Rows, objectID string) ([]Summary, error) {
	defer rows.Close()
	items := []Summary{}
	for rows.Next() {
		h := Summary{ID: objectID}
		var raw sql.NullString
		var err error
		if objectID == "" {
			err = rows.Scan(&h.ID, &h.Revision, &raw)
		} else {
			err = rows.Scan(&h.Revision, &raw)
		}
		if err != nil {
			return nil, err
		}
		h.Payload, h.HasPayload = []byte(raw.String), raw.Valid
		items = append(items, h)
	}
	return items, rows.Err()
}

func (r Repository) Summaries(ctx context.Context, q Reader, table Table, limit, offset int) ([]Summary, int, error) {
	root, _, err := tables(table)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+root+" WHERE project_id=? AND deleted=0", r.projectID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, "SELECT id,revision,summary_payload FROM "+root+" WHERE project_id=? AND deleted=0 ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?", r.projectID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items, err := readSummaries(rows, "")
	return items, total, err
}

func (r Repository) VersionSummaries(ctx context.Context, q Reader, table Table, id string, limit, offset int) ([]Summary, int, error) {
	root, versions, err := tables(table)
	if err != nil {
		return nil, 0, err
	}
	if versions == "" {
		return nil, 0, ErrInvalidTable
	}
	var total int
	if err = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+versions+" WHERE object_id=?"+r.versionScope(root), id, id, r.projectID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, "SELECT revision,summary_payload FROM "+versions+" WHERE object_id=?"+r.versionScope(root)+" ORDER BY revision DESC LIMIT ? OFFSET ?", id, id, r.projectID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items, err := readSummaries(rows, id)
	return items, total, err
}

func placeholders(count int) string { return strings.TrimSuffix(strings.Repeat("?,", count), ",") }

// MissingSummaryRecords fetches only legacy rows in the requested page, in one
// batch. Completed projections never cause full payload reads.
func (r Repository) MissingSummaryRecords(ctx context.Context, q Reader, table Table, heads []Summary) (map[string]Record, error) {
	root, _, err := tables(table)
	if err != nil {
		return nil, err
	}
	items := map[string]Record{}
	args := []any{r.projectID}
	for _, head := range heads {
		if !head.HasPayload {
			args = append(args, head.ID)
		}
	}
	if len(args) == 1 {
		return items, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT id,revision,payload FROM "+root+" WHERE project_id=? AND deleted=0 AND id IN ("+placeholders(len(args)-1)+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		record := Record{ProjectID: r.projectID}
		if err = rows.Scan(&record.ID, &record.Revision, &record.Payload); err != nil {
			return nil, err
		}
		items[record.ID] = record
	}
	return items, rows.Err()
}

func (r Repository) MissingSummaryVersions(ctx context.Context, q Reader, table Table, id string, heads []Summary) (map[int64]Version, error) {
	root, versions, err := tables(table)
	if err != nil {
		return nil, err
	}
	if versions == "" {
		return nil, ErrInvalidTable
	}
	items := map[int64]Version{}
	args := []any{id, id, r.projectID}
	for _, head := range heads {
		if !head.HasPayload {
			args = append(args, head.Revision)
		}
	}
	if len(args) == 3 {
		return items, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT revision,payload FROM "+versions+" WHERE object_id=?"+r.versionScope(root)+" AND revision IN ("+placeholders(len(args)-3)+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v := Version{ObjectID: id}
		if err = rows.Scan(&v.Revision, &v.Payload); err != nil {
			return nil, err
		}
		items[v.Revision] = v
	}
	return items, rows.Err()
}

// WriteSummary protects the current row's revision before replacing derived
// rows. Immutable version payloads are never touched.
func (r Repository) WriteSummary(ctx context.Context, q Writer, table Table, id string, revision int64, payload []byte) (bool, error) {
	root, versions, err := tables(table)
	if err != nil {
		return false, err
	}
	if versions == "" {
		return false, nil
	}
	result, err := q.ExecContext(ctx, "UPDATE "+root+" SET summary_payload=? WHERE id=? AND revision=? AND project_id=?", string(payload), id, revision, r.projectID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return false, err
	}
	_, err = q.ExecContext(ctx, "UPDATE "+versions+" SET summary_payload=? WHERE object_id=? AND revision=?", string(payload), id, revision)
	return err == nil, err
}
