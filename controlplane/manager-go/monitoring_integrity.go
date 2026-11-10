package main

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// Restore validates normalized ownership and joins before accepting any rows.
// Hashes are over original stored JSON bytes, without re-encoding evidence.
func validateMonitoringImport(tx *Tx) error {
	for _, query := range []string{
		"SELECT COUNT(*) FROM monitoring_memberships m LEFT JOIN monitoring_revisions r ON r.project_id=m.project_id AND r.report_id=m.report_id AND r.revision=m.revision LEFT JOIN monitoring_evidence e ON e.id=m.evidence_id AND e.project_id=m.project_id AND e.report_id=m.report_id WHERE r.id IS NULL OR e.id IS NULL OR m.position<0 OR m.entry_id<>COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.data,'$.entry_id')),'') OR m.component<>COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.data,'$.component')),'') OR m.instance<>COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.data,'$.instance')),'')",
		"SELECT COUNT(*) FROM monitoring_revisions WHERE revision<1 OR revision>128 OR project_id<>COALESCE(JSON_UNQUOTE(JSON_EXTRACT(data,'$.project_id')),'') OR report_id<>COALESCE(JSON_UNQUOTE(JSON_EXTRACT(data,'$.report_id')),'') OR revision<>COALESCE(JSON_EXTRACT(data,'$.revision'),0)",
		"SELECT COUNT(*) FROM (SELECT project_id,report_id,revision,COUNT(*) n,MIN(position) lo,MAX(position) hi,COUNT(DISTINCT position) distinct_positions FROM monitoring_memberships GROUP BY project_id,report_id,revision) positions WHERE lo<>0 OR hi<>n-1 OR distinct_positions<>n",
	} {
		var count int
		if err := tx.QueryRow(query).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("invalid imported monitoring association")
		}
	}
	rows, err := tx.Query("SELECT sha256,data FROM monitoring_evidence")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sha, raw string
		if err = rows.Scan(&sha, &raw); err != nil {
			return err
		}
		if !objectSHA.MatchString(sha) || digest([]byte(raw)) != sha {
			return fmt.Errorf("invalid imported monitoring evidence hash")
		}
		if _, err = decode([]byte(raw)); err != nil {
			return fmt.Errorf("invalid imported monitoring evidence")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Close each bounded cursor before composing a revision, so restore also
	// works with a single database connection and checks durable replay data.
	cursor := ""
	for {
		rows, err := tx.Query("SELECT id,project_id,report_id,revision,key_hash,request_hash,source_hash FROM monitoring_revisions WHERE id>? ORDER BY id LIMIT 32", cursor)
		if err != nil {
			return err
		}
		type revisionRow struct {
			id, project, report, key, request, source string
			revision                                  int64
		}
		batch := []revisionRow{}
		for rows.Next() {
			var row revisionRow
			if err = rows.Scan(&row.id, &row.project, &row.report, &row.revision, &row.key, &row.request, &row.source); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			if !objectSHA.MatchString(row.key) || !objectSHA.MatchString(row.request) || (row.source != "" && !objectSHA.MatchString(row.source)) {
				return fmt.Errorf("invalid imported monitoring revision hash")
			}
			snapshot, err := monitoringReadConvertedQ(tx, row.project, row.report, row.revision)
			if err != nil {
				return err
			}
			summary := obj(snapshot["monitoring_summary"])
			entries := arr(snapshot["entries"])
			if number(summary["revision"]) != float64(row.revision) || number(summary["instance_count"]) != float64(len(entries)) {
				return fmt.Errorf("invalid imported monitoring summary")
			}
			components := []string{}
			seen := map[string]bool{}
			for _, value := range entries {
				kind := str(obj(value)["component"])
				if !seen[kind] {
					components = append(components, kind)
					seen[kind] = true
				}
			}
			sort.Strings(components)
			actual := stringList(summary["components"])
			sort.Strings(actual)
			if string(encoded(components)) != string(encoded(actual)) {
				return fmt.Errorf("invalid imported monitoring components")
			}
			if row.source != "" {
				var raw, key, request string
				err = tx.QueryRow("SELECT data,metric_key,label FROM pressure_report_metrics WHERE project_id=? AND report_id=? AND kind='monitoring_snapshot' AND position=?", row.project, row.report, row.revision).Scan(&raw, &key, &request)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("missing imported monitoring source")
				}
				if err != nil {
					return err
				}
				if digest([]byte(raw)) != row.source || key != row.key || request != row.request {
					return fmt.Errorf("invalid imported monitoring source fingerprint")
				}
				original, err := decode([]byte(raw))
				if err != nil {
					return err
				}
				if digest(encoded(snapshot)) != digest(encoded(original)) {
					return fmt.Errorf("imported monitoring differs from conversion source")
				}
			}
			if receipt, present := snapshot["write_result"]; present {
				expected := pressureMonitoringWriteResult(snapshot)
				if string(encoded(receipt)) != string(encoded(expected)) {
					return fmt.Errorf("invalid imported monitoring receipt")
				}
			}
		}
		cursor = batch[len(batch)-1].id
	}
}
