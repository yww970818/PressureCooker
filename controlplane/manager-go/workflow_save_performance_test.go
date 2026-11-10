package main

import (
	"bytes"
	"os"
	"testing"
	"time"

	"pressurecooker/manager/internal/workspace"
)

// Opt-in synthetic acceptance isolates CPU preparation, source/history writes,
// derived data and commit. No scheduler or business endpoint is contacted.
func TestMySQLWorkflowSaveStageAcceptance(t *testing.T) {
	if os.Getenv("JMETER_WORKFLOW_IMPORT_ACCEPTANCE") != "1" {
		t.Skip("explicit workflow save stage acceptance")
	}
	p, _ := mysqlFixture(t)
	content, err := compileWorkflow(obj(syntheticEditableSuite()["graph"]))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := parseWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	for sample := 0; sample < 3; sample++ {
		start := time.Now()
		data, err := validateWorkspace("workflows", Obj{"name": "synthetic save stages", "graph": graph})
		if err != nil {
			t.Fatal(err)
		}
		validated := time.Now()
		id := newID("workflow-")
		data["id"], data["project_id"], data["revision"] = id, p.projectID(), 1
		data["created_at"], data["updated_at"] = now(), now()
		payload, err := p.sealWorkspace("workflows", id, 1, data)
		if err != nil {
			t.Fatal(err)
		}
		sealed := time.Now()
		tx, err := p.DB.BeginTx(p.requestContext(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		record := workspace.Record{ID: id, ProjectID: p.projectID(), Revision: 1, CreatedAt: str(data["created_at"]), UpdatedAt: str(data["updated_at"]), Payload: []byte(payload)}
		if err = workspace.New(p.projectID()).Save(p.requestContext(), tx, workspace.Workflows, record, true); err != nil {
			t.Fatal(err)
		}
		sourceWritten := time.Now()
		if err = p.writeWorkspaceProjection(tx, "workflows", data); err != nil {
			t.Fatal(err)
		}
		projected := time.Now()
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		committed := time.Now()
		read, err := p.workspaceGet("workflows", id)
		if err != nil || !bytes.Equal(encoded(read["graph"]), encoded(graph)) {
			t.Fatal("saved graph differs", err)
		}
		ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
		t.Logf("sample=%d payload_bytes=%d validate_ms=%.3f seal_ms=%.3f source_history_ms=%.3f projection_ms=%.3f commit_ms=%.3f total_ms=%.3f", sample+1, len(payload), ms(validated.Sub(start)), ms(sealed.Sub(validated)), ms(sourceWritten.Sub(sealed)), ms(projected.Sub(sourceWritten)), ms(committed.Sub(projected)), ms(committed.Sub(start)))
	}
}
