package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestWebhookURLValidation(t *testing.T) {
	for _, value := range []string{"", "  ", " http://10.0.0.8:9000/hook?q=private ", "https://hooks.internal/task", "http://[::1]:8080/hook"} {
		got, err := normalizeCallbackURL(value)
		if err != nil || got != strings.TrimSpace(value) {
			t.Fatal("valid historical callback rejected", err)
		}
	}
	for _, value := range []string{"ftp://host/hook", "//host/hook", "https://u:p@host/hook", "http://host/#secret", "http://host/#", "http:///hook", "http://ho\nst/", "https://host/" + strings.Repeat("x", 2048)} {
		if _, err := normalizeCallbackURL(value); err == nil {
			t.Fatal("invalid callback accepted")
		}
	}
}
func webhookRun(target string) Obj {
	return Obj{"id": newID("run-"), "name": "通知任务", "project_id": defaultProjectID, "status": "COMPLETED", "plan_id": "plan-1", "created_at": "2026-11-06T01:00:00.000Z", "started_at": "2026-11-06T01:01:00.000Z", "finished_at": "2026-11-06T01:02:00.000Z", "workers": []any{"worker-1"}, "properties": Obj{"secret": "must-not-leak"}, "notes": "must-not-leak", "environment_snapshot": Obj{"secret": "must-not-leak"}, "cluster_snapshot": Obj{"id": "cluster-1", "name": "冻结集群", "controller_id": "controller-a", "callback_url": target}}
}
func enqueueWebhook(t *testing.T, p *Platform, run Obj) {
	t.Helper()
	if err := p.transaction(func(tx *Tx) error {
		_, err := tx.Exec("INSERT INTO webhook_deliveries(event_id,run_id,target_url,payload,status,attempts,next_attempt_at,lease_token,lease_until,last_status_code,last_error,created_at) VALUES(?,?,?,'{}','PENDING',0,0,'',0,0,'',?)", newID("event-"), run["id"], "https://fixture.invalid", now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func webhookMeta(t *testing.T, p *Platform, id string) Obj {
	t.Helper()
	m, err := p.webhookDelivery(id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func storedLegacyWebhook(t *testing.T, p *Platform, id string) Obj {
	t.Helper()
	var event, target, payload, status string
	var attempts int
	if err := p.DB.QueryRow("SELECT event_id,target_url,payload,status,attempts FROM webhook_deliveries WHERE run_id=?", id).Scan(&event, &target, &payload, &status, &attempts); err != nil {
		t.Fatal(err)
	}
	data, err := decode([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	return Obj{"event_id": event, "target_url": target, "payload": data, "status": status, "attempts": attempts}
}

func TestLegacyWebhookPublicMetadataRedactsImportedErrors(t *testing.T) {
	for _, status := range []string{"PENDING", "RETRY", "DELIVERED", "FAILED", "DELIVERING"} {
		value := publicWebhookDelivery("legacy-event", status, 2, 502, "https://user:private@receiver.invalid/?token=private", 123)
		if value["status"] != status || value["last_error"] != "request_failed" || len(value) != 6 || strings.Contains(string(encoded(value)), "private") {
			t.Fatal("legacy read exposed unsafe imported metadata")
		}
	}
}
func clearImportedFixture(t *testing.T, p *Platform) {
	t.Helper()
	for _, table := range portableTables {
		if table != "projects" {
			if _, err := p.DB.Exec("DELETE FROM " + table); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestMySQLRedisTerminalNeverCreatesOrReplaysLegacyNotification(t *testing.T) {
	p, plan := mysqlFixture(t)
	historical := legacyClusterRunFixture(t, p, plan, "", "legacy-notification")
	historical["cluster_snapshot"] = Obj{"callback_url": "https://receiver.invalid/history"}
	historical["status"], historical["finished_at"] = "COMPLETED", now()
	if err := p.transaction(func(tx *Tx) error { return p.saveRun(tx, historical) }); err != nil {
		t.Fatal(err)
	}
	enqueueWebhook(t, p, historical)
	before := storedLegacyWebhook(t, p, str(historical["id"]))
	run := runFixture(t, p, plan, "redis-no-notification")
	if _, err := p.updateRun(str(run["id"]), Obj{"status": "COMPLETED", "finished_at": now()}); err != nil {
		t.Fatal(err)
	}
	if webhookMeta(t, p, str(run["id"])) != nil || !reflect.DeepEqual(before, storedLegacyWebhook(t, p, str(historical["id"]))) {
		t.Fatal("Redis terminal created or rewrote a legacy notification")
	}
	peer, err := newPlatform(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if webhookMeta(t, peer, str(run["id"])) != nil || !reflect.DeepEqual(before, storedLegacyWebhook(t, peer, str(historical["id"]))) {
		t.Fatal("restart replayed historical notification")
	}
}
