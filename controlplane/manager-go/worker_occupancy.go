package main

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
)

// Callers that create or claim runs hold the cluster configuration lock. Use a
// current read there: a transaction may have read its project/idempotency row
// before waiting for that lock and therefore have an older MySQL snapshot.
func occupiedWorkerIDsQ(q clusterQueryer, except string, lock, includeQueued bool) (map[string]bool, error) {
	statuses := "'PREPARING','RUNNING','STOPPING','RECOVERING'"
	if includeQueued {
		statuses += ",'QUEUED'"
	}
	query := "SELECT JSON_EXTRACT(data,'$.workers') FROM runs WHERE id<>? AND JSON_UNQUOTE(JSON_EXTRACT(data,'$.execution_mode'))='redis' AND status IN (" + statuses + ")"
	if lock {
		query += " FOR UPDATE"
	}
	rows, err := q.Query(query, except)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	occupied := map[string]bool{}
	for rows.Next() {
		var raw sql.NullString
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if !raw.Valid || raw.String == "null" {
			continue
		}
		var ids []string
		if err = json.Unmarshal([]byte(raw.String), &ids); err != nil {
			return nil, err
		}
		for _, id := range ids {
			occupied[id] = true
		}
	}
	return occupied, rows.Err()
}

func requireWorkersUnoccupied(tx *Tx, workers []any, except string, includeQueued bool) error {
	occupied, err := occupiedWorkerIDsQ(tx, except, true, includeQueued)
	if err != nil {
		return err
	}
	conflicts := []string{}
	for _, worker := range workers {
		if id := str(worker); occupied[id] {
			conflicts = append(conflicts, id)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return api(409, "worker_busy", "执行节点 "+strings.Join(conflicts, "、")+" 已被占用，请选择空闲节点或等待任务结束")
}
