package main

var pressureTables = []string{"pressure_reports", "pressure_report_jobs", "pressure_report_sources", "pressure_report_steps", "pressure_report_timeline", "pressure_report_issues", "pressure_report_metrics", "pressure_report_findings"}

func init() {
	portableTables = append(portableTables, pressureTables...)
	tableColumns["pressure_reports"] = []string{"id", "project_id", "run_id", "version", "status", "created_at", "finished_at", "identity_hash", "verdict_status", "sample_count", "success_count", "mean_ms", "min_ms", "max_ms", "p50_ms", "p90_ms", "bytes_received", "bytes_sent", "transaction_count", "sample_scope", "failure_count", "success_rate", "rps", "p95_ms", "p99_ms", "duration_seconds", "data"}
	tableColumns["pressure_report_jobs"] = []string{"id", "project_id", "run_id", "status", "created_at", "attempts", "next_attempt_at", "lease_until", "lease_token", "last_error"}
	for _, table := range pressureTables[2:] {
		tableColumns[table] = []string{"id", "project_id", "report_id", "created_at", "position", "kind", "label", "count_value", "number_value", "data"}
		for _, column := range pressureExtraColumns[table] {
			tableColumns[table] = append(tableColumns[table], column.Name)
		}
	}
	portableTables = append(portableTables, "pressure_dashboard_scopes")
	tableColumns["pressure_dashboard_scopes"] = []string{"id", "project_id", "report_id", "position", "node_hash", "step_key", "metadata", "payload"}
}

type pressureColumn struct{ Name, Type string }

var pressureExtraColumns = map[string][]pressureColumn{
	"pressure_report_sources":  {{"name", "VARCHAR(100) NOT NULL"}, {"status", "VARCHAR(32) NOT NULL"}, {"bucket", "VARCHAR(200) NOT NULL"}, {"object_key", "VARCHAR(1000) NOT NULL"}, {"sha256", "VARCHAR(64) NOT NULL"}, {"size", "BIGINT NOT NULL"}, {"valid_rows", "BIGINT NOT NULL"}, {"invalid_rows", "BIGINT NOT NULL"}},
	"pressure_report_steps":    {{"sample_kind", "VARCHAR(64) NOT NULL"}, {"sample_count", "BIGINT NOT NULL"}, {"failure_count", "BIGINT NOT NULL"}, {"success_rate", "DOUBLE NULL"}, {"mean_ms", "DOUBLE NULL"}, {"p95_ms", "DOUBLE NULL"}, {"p99_ms", "DOUBLE NULL"}},
	"pressure_report_timeline": {{"timestamp_ms", "BIGINT NOT NULL"}, {"width_ms", "BIGINT NOT NULL"}, {"sample_count", "BIGINT NOT NULL"}, {"failure_count", "BIGINT NOT NULL"}, {"rps", "DOUBLE NULL"}, {"mean_ms", "DOUBLE NULL"}, {"p95_ms", "DOUBLE NULL"}},
	"pressure_report_issues":   {{"source_kind", "VARCHAR(64) NOT NULL"}, {"source_name", "VARCHAR(100) NOT NULL"}, {"category", "VARCHAR(100) NOT NULL"}, {"level", "VARCHAR(32) NOT NULL"}, {"message", "TEXT NOT NULL"}, {"first_at", "BIGINT NULL"}, {"last_at", "BIGINT NULL"}, {"line_from", "BIGINT NOT NULL"}, {"line_to", "BIGINT NOT NULL"}},
	"pressure_report_metrics":  {{"metric_key", "VARCHAR(100) NOT NULL"}, {"origin", "VARCHAR(64) NOT NULL"}, {"schema_version", "VARCHAR(32) NOT NULL"}},
	"pressure_report_findings": {{"origin", "VARCHAR(64) NOT NULL"}, {"level", "VARCHAR(32) NOT NULL"}, {"title", "VARCHAR(500) NOT NULL"}},
}
