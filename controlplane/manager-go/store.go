package main

import (
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"pressurecooker/manager/internal/schema"
	"pressurecooker/manager/internal/tasks"
)

type Platform struct {
	Config       Config
	DB           *Database
	ProjectID    string
	runtime      *platformRuntime
	dependencies *platformDependencies
	borrowed     bool
	requestCtx   context.Context
}
type queryer interface{ QueryRow(string, ...any) *sql.Row }

func newPlatform(c Config) (*Platform, error) {
	for _, path := range []string{c.StateDir, filepath.Join(c.PlansDir, "snapshots"), c.ResultsDir} {
		if e := os.MkdirAll(path, 0755); e != nil {
			return nil, e
		}
	}
	db, e := openMySQL(c)
	if e != nil {
		return nil, e
	}
	p := &Platform{Config: c, DB: db, runtime: &platformRuntime{}, dependencies: &platformDependencies{}}
	migrationCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	e = p.migrateSchema(migrationCtx)
	cancel()
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = p.ensureDefaultProject(); e != nil {
		db.Close()
		return nil, e
	}
	if e = p.transaction(func(tx *Tx) error { return p.convertExtensionRegistriesQ(tx.Context(), tx) }); e != nil {
		db.Close()
		return nil, e
	}
	cacheCtx, cacheCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	if cacheErr := p.rebuildWorkflowEditorReads(cacheCtx); cacheErr != nil {
		log.Printf("event=workflow_editor_rebuild_deferred error_type=%T", cacheErr)
	}
	cacheCancel()
	// Preserve historical READY bytes and retire only unfinished generation intents.
	e = p.transaction(func(tx *Tx) error {
		rows, e := tx.Query("SELECT id,data FROM reports WHERE status NOT IN ('READY','FAILED')")
		if e != nil {
			return e
		}
		var reports []Obj
		for rows.Next() {
			var id, raw string
			if e = rows.Scan(&id, &raw); e != nil {
				rows.Close()
				return e
			}
			m, e := decode([]byte(raw))
			if e != nil {
				rows.Close()
				return e
			}
			reports = append(reports, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, r := range reports {
			r["status"] = "FAILED"
			r["finished_at"] = now()
			r["error"] = "Report generation retired; original artifacts only"
			if _, e = tx.Exec("UPDATE reports SET status=?,data=? WHERE id=?", "FAILED", string(encoded(r)), r["id"]); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		db.Close()
		return nil, e
	}
	return p, nil
}
func (p *Platform) migrateSchema(ctx context.Context) error {
	return schema.Apply(ctx, p.DB.DB, p.DB.Prefix)
}

func (p *Platform) transaction(fn func(*Tx) error) error {
	return p.transactionContext(p.requestContext(), fn)
}
func (p *Platform) transactionContext(ctx context.Context, fn func(*Tx) error) error {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		tx, e := p.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		e = fn(tx)
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e == nil {
			return nil
		}
		tx.Rollback()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		last = e
		var mysqlError *mysql.MySQLError
		if !errors.As(e, &mysqlError) || (mysqlError.Number != 1213 && mysqlError.Number != 1205 && mysqlError.Number != 1062) {
			return e
		}
		if !pause(ctx, time.Duration(attempt+1)*10*time.Millisecond) {
			return ctx.Err()
		}
	}
	return last
}

func get(q queryer, table, id string) (Obj, error) {
	if e := validID(id); e != nil {
		return nil, e
	}
	var raw string
	suffix := ""
	if _, ok := q.(*Tx); ok {
		suffix = " FOR UPDATE"
	}
	e := q.QueryRow("SELECT data FROM "+table+" WHERE id=?"+suffix, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "not_found", strings.ToUpper(table[:1])+strings.TrimSuffix(table[1:], "s")+" not found")
	}
	if e != nil {
		return nil, e
	}
	return decode([]byte(raw))
}
func (p *Platform) get(table, id string) (Obj, error) {
	var data Obj
	var e error
	if table == "runs" {
		data, e = p.loadRunQ(p.requestContext(), p.DB, id, false)
	} else {
		data, e = get(p.DB, table, id)
	}
	if e != nil {
		return nil, e
	}
	if e = p.checkProject(data); e != nil {
		return nil, e
	}
	if table == "runs" {
		data, e = p.openStoredRun(data)
		if e != nil {
			return nil, e
		}
	}
	data["project_id"] = objectProject(data)
	if table == "plans" {
		if _, exists := data["requires_project_environment"]; !exists {
			data["requires_project_environment"] = true
		}
		if _, exists := data["requires_environment"]; !exists {
			data["requires_environment"] = false
		}
	}
	return data, nil
}

func (p *Platform) loadRunQ(ctx context.Context, q tasks.DB, id string, lock bool) (Obj, error) {
	if e := validID(id); e != nil {
		return nil, e
	}
	record, e := (tasks.Repository{DB: q}).Load(ctx, id, p.ProjectID, lock)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "not_found", "Run not found")
	}
	if e != nil {
		return nil, e
	}
	return decode(record.Data)
}

func runRecord(m Obj, payload []byte) tasks.Record {
	return tasks.Record{ID: str(m["id"]), Status: tasks.Status(str(m["status"])), CreatedAt: str(m["created_at"]), ProjectID: objectProject(m), Data: payload}
}

func (p *Platform) saveRun(tx *Tx, m Obj) error {
	var deleted string
	err := tx.QueryRow("SELECT id FROM deleted_runs WHERE id=? FOR UPDATE", m["id"]).Scan(&deleted)
	if err == nil {
		return api(404, "run_deleted", "Run has been deleted")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	repo := tasks.Repository{DB: tx}
	ctx := tx.Context()
	stored, e := p.runForStorage(m)
	if e != nil {
		return e
	}
	return repo.Save(ctx, runRecord(m, encoded(stored)))
}
func (p *Platform) audit(action string, id any) error {
	_, e := p.DB.Exec("INSERT INTO audit(created_at,action,object_id) VALUES(?,?,?)", now(), action, id)
	return e
}
func (p *Platform) mutateRun(id string, fn func(Obj) error) (Obj, error) {
	var run Obj
	e := p.transaction(func(tx *Tx) error {
		var e error
		run, e = p.loadRunQ(tx.Context(), tx, id, true)
		if e != nil {
			return e
		}
		if e = p.checkProject(run); e != nil {
			return e
		}
		run, e = p.openStoredRun(run)
		if e != nil {
			return e
		}
		run["project_id"] = objectProject(run)
		if e = fn(run); e != nil {
			return e
		}
		return p.saveRun(tx, run)
	})
	return run, e
}
func (p *Platform) updateRun(id string, updates Obj) (Obj, error) {
	return p.mutateRun(id, func(r Obj) error {
		for k, v := range updates {
			if k == "recovery" && str(r["stop_mode"]) != "" {
				recovery := obj(v)
				recovery["terminal"] = "STOPPED"
				r[k] = recovery
			} else {
				r[k] = v
			}
		}
		return nil
	})
}

var unsafeXML = regexp.MustCompile(`(?i)<!\s*(DOCTYPE|ENTITY)`)

func (p *Platform) preparePlan(m Obj) (Obj, error) {
	return p.preparePlanWithLimit(m, 5*1024*1024)
}

// Workflow publication uses the configured authoring budget. Standalone plan
// upload retains its existing 5 MiB contract and HTTP body boundary.
func (p *Platform) preparePlanWithLimit(m Obj, limit int) (Obj, error) {
	name, nameOK := m["name"].(string)
	content, contentOK := m["content"].(string)
	description, ok := m["description"]
	if !ok {
		description = ""
	}
	desc, descOK := description.(string)
	if !nameOK || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 200 || !descOK || utf8.RuneCountInString(desc) > 10000 {
		return nil, api(400, "invalid_plan", "Name and description are invalid")
	}
	if !contentOK || limit <= 0 || len(content) > limit {
		return nil, api(400, "invalid_plan", fmt.Sprintf("XML content must be at most %d MiB", limit/(1024*1024)))
	}
	if unsafeXML.MatchString(content) {
		return nil, api(400, "unsafe_xml", "DOCTYPE and ENTITY are forbidden")
	}
	d := xml.NewDecoder(strings.NewReader(content))
	depth, roots := 0, 0
	for {
		token, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, api(400, "invalid_xml", "XML cannot be parsed")
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return nil, api(400, "invalid_xml", "XML cannot be parsed")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return nil, api(400, "invalid_xml", "XML cannot be parsed")
	}
	argumentRules, e := workflowArgumentGenerationRules(content)
	if e != nil {
		return nil, e
	}
	cookieAccounts, e := planCookieAccountMetadata(content)
	if e != nil {
		return nil, e
	}
	identities, e := scriptIdentityMetadata(content)
	if e != nil {
		return nil, e
	}
	if identities != nil && len(cookieAccounts) > 0 {
		return nil, api(400, "invalid_script_identities", "不能同时声明新旧身份协议")
	}
	hash := digest([]byte(content))
	path, e := safePath(p.Config.PlansDir, "snapshots/"+hash+".jmx", false)
	if e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if os.IsExist(e) {
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		if digest(b) != hash {
			return nil, api(500, "corrupt_plan", "Existing plan hash does not match")
		}
	} else if e != nil {
		return nil, e
	} else {
		_, e = f.WriteString(content)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
	}
	bindings, e := workflowCollectionMetadata(content)
	if e != nil {
		return nil, e
	}
	required, names := workflowEnvironmentMetadata(content)
	requirements, e := workflowDatasetMetadata(content)
	if e != nil {
		return nil, e
	}
	plan := Obj{"dataset_requirements": requirements, "api_environment_bindings": bindings, "requires_environment": required, "requires_project_environment": requiresProjectEnvironment(content, bindings), "environment_variables": names, "project_id": p.projectID(), "id": newID("plan-"), "name": name, "sha256": hash, "size": len(content), "created_at": now(), "description": desc}
	plan["cookie_accounts"] = cookieAccounts
	plan["script_identities"] = identities
	if len(argumentRules) > 0 {
		plan["argument_generation_rules"] = argumentRules
	}
	if p.Config.ArchiveEndpoint != "" {
		ref, e := p.putPlanObject(plan, content)
		if e != nil {
			return nil, e
		}
		plan["object"] = ref
	}
	return plan, nil
}
func (p *Platform) createPlan(m Obj) (Obj, error) {
	plan, e := p.preparePlan(m)
	if e != nil {
		return nil, e
	}
	e = p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		e := p.savePlan(tx, plan)
		return e
	})
	if e != nil {
		return nil, e
	}
	if e = p.audit("plan.create", plan["id"]); e != nil {
		return nil, e
	}
	return plan, nil
}
func (p *Platform) listPlans(query ...url.Values) (Obj, error) {
	q := url.Values{}
	if len(query) > 0 {
		q = query[0]
	}
	return p.planLibraryList(q, false)
}
func (p *Platform) list(query string, args ...any) ([]any, error) {
	rows, e := p.DB.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		v, e := decode([]byte(raw))
		if e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

var propRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,80}$`)

func (p *Platform) createRun(payload Obj, key string) (Obj, bool, error) {
	operation := environmentOperationFromContext(p.requestContext())
	p = p.forRequest(withEnvironmentOperation(p.requestContext(), operation), p.projectID())
	resolver := newEnvironmentRuleResolver(nil).withOperation(operation)
	copyOptions, e := parseRunCopy(payload)
	if e != nil {
		return nil, false, e
	}
	plan, e := p.get("plans", str(payload["plan_id"]))
	if e != nil {
		return nil, false, e
	}
	if objectProject(plan) != p.projectID() {
		return nil, false, api(404, "not_found", "脚本不存在")
	}
	name, nok := payload["name"].(string)
	notes, noteOK := "", true
	if v, ok := payload["notes"]; ok {
		notes, noteOK = v.(string)
	}
	if !nok || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 200 || !noteOK || utf8.RuneCountInString(notes) > 10000 {
		return nil, false, api(400, "invalid_run", "Name or notes invalid")
	}
	requestedWorkers, workersPresent := payload["workers"]
	cluster, clusterPresent := payload["cluster_id"].(string)
	if _, present := payload["cluster_id"]; present && !clusterPresent {
		return nil, false, api(400, "invalid_cluster", "cluster_id 须为集群标识")
	}
	props := Obj{}
	if v, ok := payload["properties"]; ok {
		props, ok = v.(map[string]any)
		if !ok {
			return nil, false, api(400, "invalid_properties", "Properties object required (maximum 100 keys)")
		}
	}
	if len(props) > 100 {
		return nil, false, api(400, "invalid_properties", "Properties object required (maximum 100 keys)")
	}
	for k, v := range props {
		s, ok := v.(string)
		if !propRE.MatchString(k) || !ok || utf8.RuneCountInString(s) > 4096 || strings.ContainsRune(s, 0) {
			return nil, false, api(400, "invalid_properties", "Invalid property key or value")
		}
		if k == "base_host" || reservedRuntimeProperty(k) {
			return nil, false, api(400, "reserved_property", "Runtime property is reserved")
		}

	}
	if e := validateCookieAccounts(props); e != nil {
		return nil, false, e
	}
	if strings.TrimSpace(key) == "" || utf8.RuneCountInString(key) > 200 || strings.ContainsRune(key, 0) {
		return nil, false, api(400, "idempotency_required", "Idempotency-Key (1..200 characters) is required")
	}
	environmentID := ""
	if value, exists := payload["environment_id"]; exists {
		var ok bool
		environmentID, ok = value.(string)
		if !ok || (environmentID != "" && validID(environmentID) != nil) {
			return nil, false, api(400, "invalid_environment", "environment_id无效")
		}
	}
	timeout := int64(3600)
	if v, present := payload["timeout_seconds"]; present {
		n, ok := integer(v)
		if !ok || n < 10 || n > 86400 {
			return nil, false, api(400, "invalid_timeout", "timeout_seconds须为10至86400")
		}
		timeout = n
	}
	selections := payload["dataset_selections"]
	if selections == nil {
		selections = []any{}
	}
	if _, ok := selections.([]any); !ok {
		return nil, false, api(400, "invalid_dataset_selection", "dataset_selections须为数组")
	}
	targets, e := pressureTargets(payload["report_targets"])
	if e != nil {
		return nil, false, e
	}
	normalized := Obj{"name": name, "plan_id": plan["id"], "workers": requestedWorkers, "properties": props, "notes": notes}
	if copyOptions.SourceID != "" {
		normalized["copy_source_run_id"] = copyOptions.SourceID
		normalized["copy_environment_snapshot"] = copyOptions.HistoricalEnvironment
	}
	if _, present := payload["timeout_seconds"]; present {
		normalized["timeout_seconds"] = timeout
	}
	if _, present := payload["dataset_selections"]; present {
		normalized["dataset_selections"] = selections
	}
	if environmentID != "" {
		normalized["environment_id"] = environmentID
	}
	if clusterPresent {
		normalized["cluster_id"] = cluster
	}
	if !workersPresent {
		delete(normalized, "workers")
	}
	if _, present := payload["report_targets"]; present {
		normalized["report_targets"] = targets
	}
	hash := digest(encoded(normalized))
	if p.projectID() != defaultProjectID {
		key = p.projectID() + "\x00" + digest([]byte(key))
	}
	var run Obj
	created := false
	e = p.transaction(func(tx *Tx) error {
		created = false
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		var oldHash, id string
		e := tx.QueryRow("SELECT digest,object_id FROM idempotency WHERE kind=? AND `key`=?", "run", key).Scan(&oldHash, &id)
		if e == nil {
			if oldHash != hash {
				return api(409, "idempotency_conflict", "Idempotency key already used with another payload")
			}
			run, e = get(tx, "runs", id)
			if e != nil {
				return e
			}
			if e = p.checkProject(run); e != nil {
				return e
			}
			run, e = p.openStoredRun(run)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		source, e := p.runCopySourceQ(tx, copyOptions)
		if e != nil {
			return e
		}
		clusterSnapshot, workers, e := p.selectClusterRunQ(tx, cluster, requestedWorkers, workersPresent)
		if e != nil {
			return e
		}
		copyClusterCallback(source, cluster, clusterSnapshot)
		if environmentID == "" && plan["requires_project_environment"] != false {
			return api(400, "environment_required", "该脚本须明确选择项目环境")
		}
		runtimeProperties := clone(props)
		var snapshot any
		if plan["requires_project_environment"] != false && environmentID == "" {
			return api(400, "environment_required", "该脚本须明确选择项目环境")
		}
		var historicalBindings Obj
		if copyOptions.HistoricalEnvironment && len(arr(obj(source["environment_snapshot"])["collection_environments"])) > 0 {
			if source["plan_id"] != plan["id"] {
				return api(400, "invalid_copy_environment", "沿用集合环境快照须使用原脚本版本")
			}
			historicalBindings = clone(obj(source["environment_snapshot"]))
		}
		if environmentID != "" {
			environment, e := p.workspaceGetQ(tx, "environments", environmentID, true)
			if e != nil {
				return e
			}
			environment, e = copiedEnvironment(copyOptions, source, environment)
			if e != nil {
				return e
			}
			if e = requireEnvironmentVariables(plan, environment); e != nil {
				return e
			}
			snap, injected, e := environmentSnapshotWithResolver(environment, resolver, props)
			if e != nil {
				return e
			}
			frozenProps, e := frozenTaskEnvironmentProperties(props, snap, resolver)
			if e != nil {
				return e
			}
			runtimeProperties = clone(frozenProps)
			var collectionProps Obj
			if historicalBindings != nil {
				collectionProps = restoreFrozenCollectionProperties(historicalBindings, snap)
			} else {
				collectionProps, e = p.collectionRuntimePropertiesWithResolver(tx, plan, environment, snap, resolver, frozenProps)
			}
			if e != nil {
				return e
			}
			for key, value := range collectionProps {
				injected[key] = value
			}
			argumentProps, e := argumentRuntimeProperties(plan, snap, frozenProps, resolver)
			if e != nil {
				return e
			}
			for key, value := range argumentProps {
				injected[key] = value
			}
			snapshot = snap
			for key, value := range frozenProps {
				if _, exists := injected[key]; exists && key != "base_host" {
					injected[key] = value
					injected[environmentProperty(key)] = value
				}
			}
			for key, value := range injected {
				if _, overridden := frozenProps[key]; overridden && key != "base_host" && !strings.HasPrefix(key, "jmeter.env.") {
					continue
				}
				runtimeProperties[key] = value
			}
		}
		if environmentID == "" {
			snap := Obj{"id": "", "project_id": p.projectID()}
			var injected Obj
			if historicalBindings != nil {
				injected = restoreFrozenCollectionProperties(historicalBindings, snap)
			} else {
				injected, e = p.collectionRuntimePropertiesWithResolver(tx, plan, nil, snap, resolver, props)
			}
			if e != nil {
				return e
			}
			for key, value := range injected {
				runtimeProperties[key] = value
			}
			snapshot = snap
		}
		if e := validateCookieAccounts(runtimeProperties); e != nil {
			return e
		}
		if e := validateExecutionIdentities(plan, runtimeProperties); e != nil {
			return e
		}
		datasets, e := p.selectDatasetsQ(tx, plan, selections)
		if e != nil {
			return e
		}
		run = Obj{"execution_mode": "redis", "attempt_id": newID("attempt-"), "timeout_seconds": timeout, "dataset_selections": selections, "datasets": datasets, "plan_object": plan["object"], "callback_delivery": Obj{"status": "PENDING", "attempts": 0}, "cluster_id": cluster, "cluster_snapshot": clusterSnapshot, "environment_id": environmentID, "environment_snapshot": snapshot, "project_id": p.projectID(), "id": newID("run-"), "name": name, "status": "QUEUED", "plan_id": plan["id"], "plan_name": plan["name"], "plan_sha256": plan["sha256"], "properties": runtimeProperties, "workers": workers, "controller_id": nil, "created_at": now(), "started_at": nil, "finished_at": nil, "notes": notes, "latest_report": nil, "summary": nil, "raw_archive": nil}
		run["task_property_keys"] = objectKeys(props)
		if copyOptions.SourceID != "" {
			run["copied_from_run_id"] = copyOptions.SourceID
		}
		run["report_targets"] = targets
		if e = p.enqueueDispatch(tx, run, plan); e != nil {
			return e
		}
		if e = p.saveRun(tx, run); e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO idempotency VALUES(?,?,?,?)", "run", key, hash, run["id"])
		created = e == nil
		return e
	})
	if e != nil {
		return nil, false, e
	}
	if created {
		if e = p.audit("run.create", run["id"]); e != nil {
			return nil, false, e
		}
	}
	public, e := p.getRun(str(run["id"]))
	return public, created, e
}
func (p *Platform) runList(statuses []string) ([]any, error) {
	wanted := make([]tasks.Status, len(statuses))
	for i, status := range statuses {
		wanted[i] = tasks.Status(status)
	}
	records, e := (tasks.Repository{DB: p.DB}).List(p.requestContext(), p.ProjectID, wanted)
	if e != nil {
		return nil, e
	}
	items := make([]any, 0, len(records))
	for _, record := range records {
		run, e := decode(record.Data)
		if e != nil {
			return nil, e
		}
		run, e = p.openStoredRun(run)
		if e != nil {
			return nil, e
		}
		items = append(items, run)
	}
	return items, nil
}
func (p *Platform) getRun(id string) (Obj, error) {
	run, e := p.get("runs", id)
	if e != nil {
		return nil, e
	}
	reports, e := p.list("SELECT data FROM reports WHERE run_id=? AND project_id=? ORDER BY created_at,id", id, objectProject(run))
	if e != nil {
		return nil, e
	}
	delivery, e := p.webhookDelivery(id)
	if e != nil {
		return nil, e
	}
	var dispatch Obj
	if str(run["execution_mode"]) == "redis" {
		var status, lastError string
		var attempts int
		e := p.DB.QueryRow("SELECT status,attempts,last_error FROM task_dispatches WHERE id=?", id).Scan(&status, &attempts, &lastError)
		if e == nil {
			dispatch = Obj{"status": status, "attempts": attempts, "last_error": lastError}
		}
	}
	return p.publicRun(run, reports, delivery, dispatch), nil
}

// Shared by detail and paged history so frozen values and private runner fields
// receive the same public projection after either single or batched reads.
func (p *Platform) publicRun(run Obj, reports []any, delivery, dispatch Obj) Obj {
	// Request/response bodies are read only through the fixed report drilldown.
	if evidence := arr(run["node_evidence"]); evidence != nil {
		public := make([]any, 0, len(evidence))
		for _, value := range evidence {
			row := Obj{}
			for key, field := range obj(value) {
				if key != "request_samples" {
					row[key] = field
				}
			}
			public = append(public, row)
		}
		run["node_evidence"] = public
	}
	if _, ok := run["environment_id"]; !ok {
		run["environment_id"] = ""
	}
	if _, ok := run["environment_snapshot"]; !ok {
		run["environment_snapshot"] = nil
	}
	if _, ok := run["raw_archive"]; !ok {
		run["raw_archive"] = nil
	}
	if reports == nil {
		reports = []any{}
	}
	run["webhook_delivery"] = delivery
	if str(run["execution_mode"]) == "redis" {
		delete(run, "webhook_delivery")
	}
	if snapshot := obj(run["cluster_snapshot"]); len(snapshot) > 0 {
		public := clone(snapshot)
		delete(public, "callback_url")
		run["cluster_snapshot"] = public
	}
	if str(run["execution_mode"]) == "redis" && dispatch != nil {
		run["dispatch"] = dispatch
	}
	run["reports"] = reports
	run["latest_report"] = nil
	run["summary"] = nil
	for _, r := range reports {
		report := obj(r)
		if p.checkProject(report) != nil {
			continue
		}
		report["project_id"] = objectProject(report)
		delete(report, "validation_pending")
		if str(report["status"]) == "READY" {
			run["latest_report"] = report
			run["summary"] = report["summary"]
		}
	}
	sensitive := sensitiveRunProperties(run)
	props := Obj{}
	for k, v := range obj(run["properties"]) {
		if sensitive[k] {
			props[k] = "***"
		} else {
			props[k] = v
		}
	}
	if snapshot, ok := run["environment_snapshot"].(map[string]any); ok {
		public := clone(snapshot)
		for _, value := range arr(public["variables"]) {
			pair := obj(value)
			if sensitiveRE.MatchString(str(pair["name"])) {
				pair["value"] = "***"
			}
		}
		for _, value := range arr(public["api_overrides"]) {
			pair := obj(value)
			if sensitiveRE.MatchString(str(pair["name"])) {
				pair["value"] = "***"
			}
		}
		redactCollectionEnvironmentSnapshots(public)
		run["environment_snapshot"] = public
	}
	run["properties"] = props
	for _, k := range []string{"runner_lease_token", "recovery", "stop_requested_at", "stop_mode", "stop_sent", "automatic_report"} {
		delete(run, k)
	}
	return run
}
func (p *Platform) stopRun(id, mode string) (Obj, error) {
	if mode != "graceful" && mode != "now" {
		return nil, api(400, "invalid_stop_mode", "Mode must be graceful or now")
	}
	_, e := p.mutateRun(id, func(r Obj) error {
		if str(r["execution_mode"]) != "redis" {
			return api(409, "execution_retired", "历史执行协议已退役；请使用显式退役工具处理旧非终态记录")
		}
		decision := tasks.Status(str(r["status"])).Stop(str(r["stop_mode"]), mode)
		if decision.Finish {
			r["status"] = string(decision.Status)
			r["finished_at"] = now()
			receipt, e := newReceipt(r)
			if e != nil {
				return e
			}
			if str(r["execution_mode"]) == "redis" {
				receipt["status"] = "EMPTY"
				r["callback_delivery"] = nil
			}
			r["raw_archive"] = receipt
		} else if decision.RecordRequest {
			r["status"] = string(decision.Status)
			r["stop_mode"] = decision.Mode
			if r["stop_requested_at"] == nil {
				r["stop_requested_at"] = epoch()
			}
			if recovery := obj(r["recovery"]); len(recovery) > 0 {
				recovery["terminal"] = "STOPPED"
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if run, e2 := p.get("runs", id); e2 == nil && str(run["execution_mode"]) == "redis" && str(run["status"]) == "STOPPING" {
		_, _ = newClient(p.Config).request(p.Config.ControllerURLs[str(obj(run["cluster_snapshot"])["controller_id"])], "/runs/"+id+"/stop", Obj{"task_id": id, "attempt_id": run["attempt_id"], "mode": mode})
	}
	if e = p.audit("run.stop", id); e != nil {
		return nil, e
	}
	return p.getRun(id)
}
func (p *Platform) report(id string) (Obj, error) {
	r, e := p.get("reports", id)
	if e == nil {
		delete(r, "validation_pending")
	}
	return r, e
}
func (p *Platform) reportFile(id, kind, relative string) (string, error) {
	r, e := p.report(id)
	if e != nil {
		return "", e
	}
	base, e := safePath(p.Config.ResultsDir, str(r["run_id"])+"/reports/"+id, false)
	if e != nil {
		return "", e
	}
	switch kind {
	case "html":
		html := filepath.Join(base, "html")
		if legacy := str(r["legacy_html_path"]); legacy != "" {
			html, e = safePath(p.Config.ResultsDir, legacy, false)
			if e != nil {
				return "", e
			}
		}
		if relative == "" {
			relative = "index.html"
		}
		return safePath(html, relative, true)
	case "analysis":
		return safePath(base, "analysis.md", true)
	case "summary":
		return safePath(base, "summary.json", true)
	}
	return "", api(404, "not_found", "File not found")
}
func (p *Platform) heartbeat(id string, leader bool) error {
	return p.transaction(func(tx *Tx) error {
		role := "standby"
		if leader {
			role = "leader"
			if _, e := tx.Exec("UPDATE instances SET role='standby' WHERE id<>? AND role='leader'", id); e != nil {
				return e
			}
		}
		_, e := tx.Exec("INSERT INTO instances VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE role=VALUES(role),heartbeat_at=VALUES(heartbeat_at),heartbeat_epoch=VALUES(heartbeat_epoch)", id, role, now(), epoch())
		return e
	})
}
func openMySQL(c Config) (*Database, error) {
	if e := validatePrefix(c.MySQLTablePrefix); e != nil {
		return nil, e
	}
	dsn := mysql.Config{User: c.MySQLUser, Passwd: c.MySQLPassword, Net: "tcp", Addr: fmt.Sprintf("%s:%d", c.MySQLHost, c.MySQLPort), DBName: c.MySQLDatabase, Collation: "utf8mb4_unicode_ci", Timeout: c.MySQLTimeout, ReadTimeout: c.MySQLTimeout, WriteTimeout: c.MySQLTimeout, AllowNativePasswords: true, Params: map[string]string{"charset": "utf8mb4"}}
	db, e := sql.Open("mysql", dsn.FormatDSN())
	if e != nil {
		return nil, fmt.Errorf("invalid MySQL configuration")
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(5 * time.Minute)
	if e = db.Ping(); e != nil {
		db.Close()
		return nil, fmt.Errorf("MySQL connection failed (check configured host, database, grants, and secret file)")
	}
	return &Database{DB: db, Prefix: c.MySQLTablePrefix}, nil
}
