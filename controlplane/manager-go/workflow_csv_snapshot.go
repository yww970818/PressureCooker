package main

import "database/sql"

type csvSnapshotQueryer interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}
type workflowCSVSnapshot struct {
	revision int64
	summary  sql.NullString
	deleted  int
	nodes    []csvSnapshotNode
}
type csvSnapshotNode struct{ id, payload string }

// Header and CSV nodes must come from one MVCC statement. A concurrent save
// atomically replaces the node projection, so separate autocommit reads race.
func (p *Platform) readWorkflowCSVSnapshot(q csvSnapshotQueryer, id string) (workflowCSVSnapshot, error) {
	snapshot := workflowCSVSnapshot{nodes: []csvSnapshotNode{}}
	rows, e := q.Query("SELECT w.revision,w.summary_payload,w.deleted,c.node_id,c.payload FROM workflows w LEFT JOIN workflow_csv_nodes c ON c.workflow_id=w.id AND c.project_id=w.project_id AND c.revision=w.revision WHERE w.id=? AND w.project_id=? ORDER BY c.position", id, p.projectID())
	if e != nil {
		return snapshot, e
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var node, payload sql.NullString
		if e = rows.Scan(&snapshot.revision, &snapshot.summary, &snapshot.deleted, &node, &payload); e != nil {
			return snapshot, e
		}
		if node.Valid && payload.Valid {
			snapshot.nodes = append(snapshot.nodes, csvSnapshotNode{node.String, payload.String})
		}
	}
	if e = rows.Err(); e != nil {
		return snapshot, e
	}
	if !found {
		return snapshot, sql.ErrNoRows
	}
	return snapshot, nil
}
