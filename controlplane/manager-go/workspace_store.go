package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"pressurecooker/manager/internal/workspace"
)

const workspaceLimit = 5 * 1024 * 1024
const compressedWorkflowLimit = 65 << 20

var workspaceTables = []string{"workflows", "api_collections", "environments", "workflow_versions", "api_collection_versions", "plan_workflow_links"}

func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, e := strconv.ParseInt(string(n), 10, 64)
		return i, e == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		i := int64(n)
		return i, float64(i) == n
	}
	return 0, false
}
func (p *Platform) workspaceCipher() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("jmeter-workspace-v1\x00" + p.Config.SessionKey))
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func workspaceAAD(table, id string, revision int64) []byte {
	return []byte(table + "/" + id + "/" + strconv.FormatInt(revision, 10))
}
func (p *Platform) sealWorkspace(table, id string, revision int64, data Obj) (string, error) {
	plain := encoded(data)
	format := "gcm1:"
	aad := workspaceAAD(table, id, revision)
	// Large authored flows repeat request bodies in config and lossless XML.
	// Compress only new workflow revisions, before encryption; old ciphertext
	// stays readable and history is never rewritten. Skip incompressible data.
	if table == "workflows" && len(plain) >= 64<<10 && len(plain) <= compressedWorkflowLimit {
		var compressed bytes.Buffer
		z, e := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		if e != nil {
			return "", e
		}
		if _, e = z.Write(plain); e != nil {
			return "", e
		}
		if e = z.Close(); e != nil {
			return "", e
		}
		if compressed.Len() < len(plain)*7/8 {
			plain = compressed.Bytes()
			format = "gcm2:"
			aad = append(aad, []byte("\x00gzip")...)
		}
	}
	aead, e := p.workspaceCipher()
	if e != nil {
		return "", e
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return "", e
	}
	sealed := aead.Seal(nonce, nonce, plain, aad)
	return format + base64.RawStdEncoding.EncodeToString(sealed), nil
}
func (p *Platform) openWorkspace(table, id string, revision int64, payload string) (Obj, error) {
	failure := func() (Obj, error) {
		return nil, api(500, "workspace_decryption_failed", "无法读取加密数据，请检查原始会话密钥是否保留")
	}
	compressed := strings.HasPrefix(payload, "gcm2:")
	if !strings.HasPrefix(payload, "gcm1:") && !compressed {
		return failure()
	}
	if compressed && table != "workflows" {
		return failure()
	}
	raw, e := base64.RawStdEncoding.DecodeString(payload[5:])
	if e != nil {
		return failure()
	}
	aead, e := p.workspaceCipher()
	if e != nil {
		return nil, e
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return failure()
	}
	nonce := raw[:aead.NonceSize()]
	aad := workspaceAAD(table, id, revision)
	if compressed {
		aad = append(aad, []byte("\x00gzip")...)
	}
	plain, e := aead.Open(nil, nonce, raw[aead.NonceSize():], aad)
	if e != nil {
		return failure()
	}
	if compressed {
		// Read old revisions regardless of today's lower workflow capacity.
		// The maximum authored body is 64 MiB, plus server-owned metadata.
		source := bytes.NewReader(plain)
		z, err := gzip.NewReader(source)
		if err != nil {
			return failure()
		}
		z.Multistream(false)
		plain, err = io.ReadAll(io.LimitReader(z, compressedWorkflowLimit+1))
		closeErr := z.Close()
		if err != nil || closeErr != nil || source.Len() != 0 || len(plain) > compressedWorkflowLimit {
			return failure()
		}
	}
	data, e := decode(plain)
	if e != nil {
		return failure()
	}
	return data, nil
}
func workspaceMetadata(body Obj) (string, string, error) {
	name, ok := body["name"].(string)
	description, exists := body["description"]
	if !exists {
		description = ""
	}
	desc, descOK := description.(string)
	if !ok || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 200 || !descOK || utf8.RuneCountInString(desc) > 10000 {
		return "", "", api(400, "invalid_workspace", "名称须为1至200字，说明最多10000字")
	}
	return name, desc, nil
}
func (p *Platform) workspaceGetQ(q queryer, table, id string, lock bool) (Obj, error) {
	if e := validID(id); e != nil {
		return nil, e
	}
	reader, ok := q.(workspace.RowReader)
	if !ok {
		reader = workspaceLegacyRowReader{q}
	}
	record, e := workspace.New(p.projectID()).Get(p.requestContext(), reader, workspace.Table(table), id, lock)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "not_found", "内容不存在或已删除")
	}
	if e != nil {
		return nil, e
	}
	if record.ProjectID != p.projectID() {
		return nil, api(404, "not_found", "内容不存在")
	}
	data, e := p.openWorkspace(table, id, record.Revision, string(record.Payload))
	if e == nil {
		data["project_id"] = record.ProjectID
	}
	return data, e
}

// Compatibility for legacy helpers which expose QueryRow only. Production
// Database and Tx implement RowReader directly and always receive the context.
type workspaceLegacyRowReader struct{ queryer }

func (q workspaceLegacyRowReader) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	return q.QueryRow(query, args...)
}

func (p *Platform) workspaceGet(table, id string) (Obj, error) {
	return p.workspaceGetQ(p.DB, table, id, false)
}
func (p *Platform) workspaceList(table string) (Obj, error) {
	records, e := workspace.New(p.projectID()).List(p.requestContext(), p.DB, workspace.Table(table))
	if e != nil {
		return nil, e
	}
	items := []any{}
	for _, record := range records {
		data, e := p.openWorkspace(table, record.ID, record.Revision, string(record.Payload))
		if e != nil {
			return nil, e
		}
		data["project_id"] = p.projectID()
		items = append(items, data)
	}
	return Obj{"project_id": p.projectID(), "items": items, "total": len(items)}, nil
}
func validateWorkspace(table string, body Obj) (Obj, error) {
	if table == "environments" {
		return validateEnvironment(body)
	}
	name, description, e := workspaceMetadata(body)
	if e != nil {
		return nil, e
	}
	data := Obj{"name": name, "description": description}
	if table == "workflows" {
		graph, ok := body["graph"].(map[string]any)
		if !ok {
			return nil, api(400, "invalid_graph", "工作流图数据必须是对象")
		}
		if _, e := compileWorkflow(graph); e != nil {
			return nil, e
		}
		data["graph"] = graph
	} else {
		document, ok := body["document"].(map[string]any)
		if !ok {
			return nil, api(400, "invalid_document", "OpenAPI文档必须是JSON对象")
		}
		client := Obj{}
		if value, exists := body["client_state"]; exists {
			var ok bool
			client, ok = value.(map[string]any)
			if !ok {
				return nil, api(400, "invalid_client_state", "客户端配置必须是JSON对象")
			}
		}
		if mode, exists := client["request_mode"]; exists && mode != "direct" && mode != "master" {
			return nil, api(400, "invalid_client_state", "请求方式须为direct或master")
		}
		if e := validateCollectionEnvironmentResources(client); e != nil {
			return nil, e
		}
		if e := validateCollectionOverrides(client); e != nil {
			return nil, e
		}
		if e := validateCollectionInterfaceProtocols(document); e != nil {
			return nil, e
		}
		document = clone(document)
		delete(document, "x-jmeter-client-state")
		data["document"] = document
		data["client_state"] = client
	}
	limit := workspaceLimit
	if table == "workflows" {
		limit = workflowByteLimit()
	}
	raw := encoded(data)
	if len(raw) > limit {
		return nil, api(413, "workspace_too_large", fmt.Sprintf("内容超过%dMiB", limit/(1024*1024)))
	}
	// Preserve the private JSON-normalized copy without encoding it twice.
	return decode(raw)
}
func (p *Platform) saveWorkspace(tx *Tx, table string, data Obj, payload string, create bool) error {
	revision, _ := integer(data["revision"])
	record := workspace.Record{ID: str(data["id"]), ProjectID: objectProject(data), Revision: revision, CreatedAt: str(data["created_at"]), UpdatedAt: str(data["updated_at"]), Payload: []byte(payload)}
	e := workspace.New(p.projectID()).Save(p.requestContext(), tx, workspace.Table(table), record, create)
	if errors.Is(e, workspace.ErrRevisionConflict) {
		return api(409, "revision_conflict", "内容已被其他会话更新，请重新加载后再保存")
	}
	if e != nil {
		return e
	}
	if table == "environments" {
		return nil
	}
	return p.writeWorkspaceProjection(tx, table, data)
}
func (p *Platform) workspaceCreate(table string, body Obj, imported ...bool) (Obj, error) {
	return p.workspaceCreateFromPlan(table, body, "", imported...)
}
func (p *Platform) workspaceCreateFromPlan(table string, body Obj, sourcePlanID string, imported ...bool) (Obj, error) {
	data, e := validateWorkspace(table, body)
	if e != nil {
		return nil, e
	}
	prefix := "collection-"
	if table == "environments" {
		prefix = "environment-"
	}
	if table == "workflows" {
		prefix = "workflow-"
	}
	data["project_id"] = p.projectID()
	data["id"] = newID(prefix)
	data["revision"] = 1
	if sourcePlanID != "" {
		data["source_plan_id"] = sourcePlanID
	}
	data["created_at"] = now()
	data["updated_at"] = data["created_at"]
	e = p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		if table == "api_collections" {
			if len(imported) == 0 || !imported[0] {
				if e := p.validateCollectionResourceParents(tx, nil, obj(data["client_state"])); e != nil {
					return e
				}
			}
			if e := p.validateCollectionEnvironmentIDs(tx, obj(data["client_state"])); e != nil {
				return e
			}
		}
		if table == "environments" {
			if e := p.uniqueEnvironmentName(tx, "", str(data["name"])); e != nil {
				return e
			}
		}
		if table == "workflows" {
			if e := p.validateAPIReferences(tx, obj(data["graph"])); e != nil {
				return e
			}
		}
		if sourcePlanID != "" {
			source, e := get(tx, "plans", sourcePlanID)
			if e != nil {
				return e
			}
			if objectProject(source) != p.projectID() {
				return api(404, "not_found", "源脚本不存在")
			}
		}
		payload, e := p.sealWorkspace(table, str(data["id"]), 1, data)
		if e != nil {
			return e
		}
		if e := p.saveWorkspace(tx, table, data, payload, true); e != nil {
			return e
		}
		if sourcePlanID != "" {
			return insertPlanWorkflowLink(tx, sourcePlanID, str(data["id"]), 1, "imported")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	p.audit(table+".create", data["id"])
	return data, nil
}
func (p *Platform) workspaceUpdate(table, id string, body Obj) (Obj, error) {
	revision, ok := integer(body["revision"])
	if !ok || revision < 1 {
		return nil, api(400, "revision_required", "保存时必须携带当前版本号")
	}
	data, e := validateWorkspace(table, body)
	if e != nil {
		return nil, e
	}
	var updated Obj
	e = p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		current, e := p.workspaceGetQ(tx, table, id, true)
		if e != nil {
			return e
		}
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		if table == "environments" {
			if e := p.uniqueEnvironmentName(tx, id, str(data["name"])); e != nil {
				return e
			}
		}
		if table == "workflows" {
			if e := p.validateAPIReferences(tx, obj(data["graph"])); e != nil {
				return e
			}
		}
		actual, _ := integer(current["revision"])
		if revision != actual {
			return api(409, "revision_conflict", "内容已被其他会话更新，请重新加载后再保存")
		}
		updated = data // validateWorkspace already returned a private copy
		if table == "workflows" && str(current["source_plan_id"]) != "" {
			updated["source_plan_id"] = current["source_plan_id"]
		}
		if table == "api_collections" {
			if _, present := body["client_state"]; !present {
				updated["client_state"] = current["client_state"]
			}
		}
		if table == "api_collections" {
			if e := p.validateCollectionResourceParents(tx, obj(current["client_state"]), obj(updated["client_state"])); e != nil {
				return e
			}
			if e := p.validateCollectionEnvironmentIDs(tx, obj(updated["client_state"])); e != nil {
				return e
			}
		}
		updated["project_id"] = objectProject(current)
		updated["id"] = id
		updated["revision"] = revision + 1
		updated["created_at"] = current["created_at"]
		updated["updated_at"] = now()
		payload, e := p.sealWorkspace(table, id, revision+1, updated)
		if e != nil {
			return e
		}
		return p.saveWorkspace(tx, table, updated, payload, false)
	})
	if e != nil {
		return nil, e
	}
	p.audit(table+".update", id)
	return updated, nil
}
func (p *Platform) workspaceDelete(table, id string) (Obj, error) {
	e := p.transaction(func(tx *Tx) error {
		if e := validID(id); e != nil {
			return e
		}
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		repo := workspace.New(p.projectID())
		deleted, e := repo.DeletionState(p.requestContext(), tx, workspace.Table(table), id)
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return api(404, "not_found", "内容不存在")
			}
			return e
		}
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		if deleted {
			return nil
		}
		if table == "environments" {
			if e := p.clearCollectionEnvironment(tx, id); e != nil {
				return e
			}
		}
		return repo.Delete(p.requestContext(), tx, workspace.Table(table), id, now())
	})
	if e != nil {
		return nil, e
	}
	p.audit(table+".delete", id)
	return Obj{"id": id, "project_id": p.projectID(), "deleted": true}, nil
}
func (p *Platform) workspaceVersions(table, id string, revision int64) (Obj, error) {
	if _, e := p.workspaceGet(table, id); e != nil {
		return nil, e
	}
	repo := workspace.New(p.projectID())
	if revision > 0 {
		version, e := repo.Version(p.requestContext(), p.DB, workspace.Table(table), id, revision)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, api(404, "not_found", "历史版本不存在")
		}
		if e != nil {
			return nil, e
		}
		data, e := p.openWorkspace(table, id, revision, string(version.Payload))
		if e == nil {
			data["project_id"] = p.projectID()
		}
		return data, e
	}
	versions, e := repo.Versions(p.requestContext(), p.DB, workspace.Table(table), id)
	if e != nil {
		return nil, e
	}
	items := []any{}
	for _, version := range versions {
		data, e := p.openWorkspace(table, id, version.Revision, string(version.Payload))
		if e != nil {
			return nil, e
		}
		data["project_id"] = p.projectID()
		items = append(items, data)
	}
	return Obj{"project_id": p.projectID(), "items": items, "total": len(items)}, nil
}
