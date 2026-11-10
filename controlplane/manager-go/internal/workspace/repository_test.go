package workspace

import (
	"context"
	"errors"
	"testing"
)

func TestRepositoryRejectsUnlistedTablesBeforeSQL(t *testing.T) {
	r := New("project-one")
	ctx := context.Background()
	for _, table := range []Table{"", "plans", "workflow_versions", "workflows; DROP TABLE projects", "workflows /* bypass */"} {
		t.Run(string(table), func(t *testing.T) {
			checks := []struct {
				name string
				call func() error
			}{
				{"get", func() error { _, err := r.Get(ctx, nil, table, "id", true); return err }},
				{"owner", func() error { _, err := r.Owner(ctx, nil, table, "id"); return err }},
				{"list", func() error { _, err := r.List(ctx, nil, table); return err }},
				{"save", func() error { return r.Save(ctx, nil, table, Record{ProjectID: "project-one"}, true) }},
				{"deletion state", func() error { _, err := r.DeletionState(ctx, nil, table, "id"); return err }},
				{"delete", func() error { return r.Delete(ctx, nil, table, "id", "now") }},
				{"version", func() error { _, err := r.Version(ctx, nil, table, "id", 1); return err }},
				{"versions", func() error { _, err := r.Versions(ctx, nil, table, "id"); return err }},
				{"summaries", func() error { _, _, err := r.Summaries(ctx, nil, table, 1, 0); return err }},
				{"version summaries", func() error { _, _, err := r.VersionSummaries(ctx, nil, table, "id", 1, 0); return err }},
				{"legacy summaries", func() error { _, err := r.MissingSummaryRecords(ctx, nil, table, nil); return err }},
				{"legacy version summaries", func() error { _, err := r.MissingSummaryVersions(ctx, nil, table, "id", nil); return err }},
				{"write summary", func() error { _, err := r.WriteSummary(ctx, nil, table, "id", 1, nil); return err }},
			}
			for _, check := range checks {
				if err := check.call(); !errors.Is(err, ErrInvalidTable) {
					t.Errorf("%s accepted unlisted table: %v", check.name, err)
				}
			}
		})
	}
}

func TestRepositoryRejectsForeignWritesBeforeSQL(t *testing.T) {
	r := New("project-one")
	for _, create := range []bool{true, false} {
		if err := r.Save(context.Background(), nil, Workflows, Record{ProjectID: "project-two"}, create); !errors.Is(err, ErrProjectMismatch) {
			t.Fatalf("foreign write accepted: %v", err)
		}
	}
}
