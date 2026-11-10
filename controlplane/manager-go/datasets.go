package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"pressurecooker/execution/objectstore"
	"pressurecooker/execution/protocol"
)

const datasetLimit = 256 * 1024 * 1024
const datasetMarker = "jmeter.platform.dataset"
const datasetNodeMarker = "jmeter.platform.node_id"

func (p *Platform) objectStore() (*objectstore.Client, error) {
	c := p.Config
	if c.ArchiveEndpoint == "" {
		return nil, api(503, "object_store_unavailable", "对象存储尚未配置")
	}
	return p.objectClient(objectstore.Config{Endpoint: c.ArchiveEndpoint, Bucket: c.ArchiveBucket, AccessKey: c.ArchiveAccessKey, SecretKey: c.ArchiveSecretKey, Region: c.ArchiveRegion, Timeout: c.ArchiveTimeout})
}
func dtoObject(v any) Obj { var out Obj; _ = json.Unmarshal(encoded(v), &out); return out }
func objectRef(v any) protocol.ObjectRef {
	var ref protocol.ObjectRef
	_ = json.Unmarshal(encoded(v), &ref)
	return ref
}
func csvDelimiter(value string) (rune, error) {
	if value == "" {
		return ',', nil
	}
	runes := []rune(value)
	if len(runes) != 1 || runes[0] == '\r' || runes[0] == '\n' || runes[0] == '"' || runes[0] == 0 {
		return 0, api(400, "invalid_delimiter", "CSV分隔符须为一个有效字符")
	}
	return runes[0], nil
}
func stringList(v any) []string {
	if values, ok := v.([]string); ok {
		return append([]string{}, values...)
	}
	out := []string{}
	for _, x := range arr(v) {
		out = append(out, str(x))
	}
	return out
}
func csvNames(value string) []string {
	out := []string{}
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}
func inspectDatasetCSV(input io.Reader, delimiter rune) ([]string, int, error) {
	r := datasetCSVReader(input, delimiter)
	r.ReuseRecord = true
	cols, e := r.Read()
	if e != nil || len(cols) == 0 || len(cols) > 100 {
		return nil, 0, api(400, "invalid_csv", "CSV须包含有效表头")
	}
	cols = append([]string(nil), cols...)
	seen := map[string]bool{}
	for _, col := range cols {
		if !utf8.ValidString(col) || !validEnvironmentName(col) || len(col) > 200 || seen[col] {
			return nil, 0, api(400, "invalid_csv", "CSV列名须有效且唯一")
		}
		seen[col] = true
	}
	count := 0
	for {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, 0, api(400, "invalid_csv", "CSV行列数不一致或格式无效")
		}
		for _, s := range row {
			if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
				return nil, 0, api(400, "invalid_csv", "CSV须为UTF-8文本")
			}
		}
		count++
	}
	if count == 0 {
		return nil, 0, api(400, "invalid_csv", "CSV至少包含一行数据")
	}
	return cols, count, nil
}
func datasetRequirement(node Obj) Obj {
	props := obj(node["properties"])
	config := obj(props["config"])
	columns := stringList(props["dataset_columns"])
	names := csvNames(str(config["variableNames"]))
	if len(columns) == 0 {
		columns = append([]string{}, names...)
	}
	if len(names) == 0 {
		names = append([]string{}, columns...)
	}
	delimiter := str(config["delimiter"])
	if delimiter == "" {
		delimiter = ","
	}
	return Obj{"node_id": node["id"], "node_name": props["name"], "columns": columns, "variable_names": names, "delimiter": delimiter, "recycle": config["recycle"] != false, "stop_thread": boolean(config["stopThread"])}
}
func csvRequirements(graph Obj) []any {
	out := []any{}
	for _, n := range arr(graph["nodes"]) {
		node := obj(n)
		if obj(node["properties"])["component"] == "CSVDataSet" && obj(node["properties"])["enabled"] != false {
			out = append(out, datasetRequirement(node))
		}
	}
	return out
}
func graphProducers(graph Obj) map[string]bool {
	out := map[string]bool{}
	for _, value := range arr(graph["nodes"]) {
		props := obj(obj(value)["properties"])
		if props["enabled"] == false {
			continue
		}
		cfg := obj(props["config"])
		if props["component"] == "JSONPostProcessor" {
			for _, s := range strings.Split(str(cfg["referenceName"]), ";") {
				if s = strings.TrimSpace(s); s != "" {
					out[s] = true
				}
			}
		}
		if props["component"] == "CSVDataSet" {
			for _, s := range stringList(datasetRequirement(obj(value))["variable_names"]) {
				out[s] = true
			}
		}
	}
	return out
}
func workflowDatasetMetadata(content string) ([]any, error) {
	roots, e := parseXMLFragment(content)
	if e != nil {
		return nil, e
	}
	out := []any{}
	seen := map[string]bool{}
	var walk func(*xmlElement) error
	walk = func(n *xmlElement) error {
		if raw := n.propText(datasetMarker); raw != "" {
			d, e := decode([]byte(raw))
			id := str(d["node_id"])
			if e != nil || n.Name.Local != "CSVDataSet" || validID(id) != nil || seen[id] || len(stringList(d["columns"])) == 0 || len(stringList(d["columns"])) != len(stringList(d["variable_names"])) {
				return api(400, "invalid_dataset_binding", "CSV节点绑定元数据无效")
			}
			seen[id] = true
			out = append(out, d)
		}
		for _, c := range n.children() {
			if e := walk(c); e != nil {
				return e
			}
		}
		return nil
	}
	for _, n := range roots {
		if e := walk(n); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (p *Platform) datasetGetQ(q queryer, id string, includeDeleted bool) (Obj, error) {
	if e := validID(id); e != nil {
		return nil, e
	}
	var raw string
	var deleted int
	if e := q.QueryRow("SELECT data,deleted FROM workflow_datasets WHERE id=? AND project_id=?", id, p.projectID()).Scan(&raw, &deleted); e != nil {
		return nil, api(404, "not_found", "数据文件不存在")
	}
	if deleted != 0 && !includeDeleted {
		return nil, api(404, "not_found", "数据文件已删除")
	}
	return decode([]byte(raw))
}
func (p *Platform) datasetList(workflowID string) (Obj, error) {
	nodes, e := p.csvNodeRequirements(workflowID)
	if e != nil {
		return nil, e
	}
	items, e := p.list("SELECT data FROM workflow_datasets WHERE project_id=? AND workflow_id=? AND deleted=0 ORDER BY created_at,id", p.projectID(), workflowID)
	if e != nil {
		return nil, e
	}
	byNode := map[string][]any{}
	for _, f := range items {
		key := str(obj(f)["node_id"])
		byNode[key] = append(byNode[key], f)
	}
	for _, v := range nodes {
		n := obj(v)
		files := byNode[str(n["node_id"])]
		if files == nil {
			files = []any{}
		}
		n["datasets"] = files
	}
	return Obj{"nodes": nodes, "items": items}, nil
}

// datasetLineage is internal provenance, never caller-supplied arbitrary metadata.
// It is inserted with the new immutable file in the same transaction.
type datasetLineage struct {
	parentID, rootID, baseSHA string
	version                   int64
}

func (p *Platform) datasetCreate(ctx context.Context, workflowID, nodeID, name string, input io.Reader, lineage ...datasetLineage) (Obj, error) {
	workflow, e := p.workspaceGet("workflows", workflowID)
	if e != nil {
		return nil, e
	}
	var requirement Obj
	var explicitColumns []string
	for _, value := range arr(obj(workflow["graph"])["nodes"]) {
		n := obj(value)
		if str(n["id"]) == nodeID {
			explicitColumns = stringList(obj(n["properties"])["dataset_columns"])
		}
	}
	for _, v := range csvRequirements(obj(workflow["graph"])) {
		if obj(v)["node_id"] == nodeID {
			requirement = obj(v)
		}
	}
	if requirement == nil {
		return nil, api(400, "dataset_node_required", "须选择当前流程CSV节点")
	}
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return nil, api(400, "invalid_dataset", "文件名称须为1至200字")
	}
	delimiter, e := csvDelimiter(str(requirement["delimiter"]))
	if e != nil {
		return nil, e
	}
	spoolDir := filepath.Join(p.Config.StateDir, "dataset-cache")
	if e := os.MkdirAll(spoolDir, 0700); e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(spoolDir, "jmeter-dataset-*")
	if e != nil {
		return nil, e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	_ = f.Chmod(0600)
	sha, size, e := copyDataset(f, input, p.datasetByteLimit())
	if e != nil {
		return nil, e
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	cols, rows, e := inspectDatasetCSV(f, delimiter)
	if e != nil {
		return nil, e
	}
	expected := explicitColumns
	if len(expected) > 0 && strings.Join(cols, "\x00") != strings.Join(expected, "\x00") {
		return nil, api(400, "dataset_schema_mismatch", "CSV表头与节点列定义不一致")
	}
	if names := stringList(requirement["variable_names"]); len(names) > 0 && len(names) != len(cols) {
		return nil, api(400, "dataset_schema_mismatch", "CSV列数与变量数量不一致")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	store, e := p.objectStore()
	if e != nil {
		return nil, e
	}
	refName := name
	id := newID("dataset-")
	key := "datasets/" + p.projectID() + "/" + workflowID + "/" + nodeID + "/" + id + "/" + sha + ".csv"
	ref, e := store.Put(ctx, key, f, size, sha, "text/csv; charset=utf-8")
	if e != nil {
		return nil, e
	}
	ref.Name = refName
	ref.URL = "s3://" + ref.Bucket + "/" + ref.Key
	data := Obj{"id": id, "project_id": p.projectID(), "workflow_id": workflowID, "node_id": nodeID, "node_name": requirement["node_name"], "name": name, "version": 1, "columns": cols, "rows": rows, "delimiter": string(delimiter), "object": ref, "size": size, "sha256": sha, "created_at": now()}
	if len(lineage) > 0 {
		data["parent_dataset_id"], data["root_dataset_id"], data["version"] = lineage[0].parentID, lineage[0].rootID, lineage[0].version
	}
	e = p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		if len(lineage) > 0 {
			parent, e := p.datasetGetQ(tx, lineage[0].parentID, false)
			if e != nil {
				return e
			}
			if parent["sha256"] != lineage[0].baseSHA || parent["workflow_id"] != workflowID || parent["node_id"] != nodeID {
				return api(409, "dataset_version_conflict", "原数据文件已变化，请重新加载")
			}
			if parent["delimiter"] != string(delimiter) {
				return api(409, "dataset_schema_changed", "原数据文件分隔符与CSV节点不一致，请重新加载")
			}
		}
		current, e := p.workspaceGetQ(tx, "workflows", workflowID, true)
		if e != nil {
			return e
		}
		found := false
		for _, value := range arr(obj(current["graph"])["nodes"]) {
			n := obj(value)
			props := obj(n["properties"])
			if n["id"] == nodeID && props["component"] == "CSVDataSet" && props["enabled"] != false {
				found = true
				known := stringList(props["dataset_columns"])
				if len(known) > 0 && strings.Join(known, "\x00") != strings.Join(cols, "\x00") {
					return api(409, "dataset_schema_changed", "CSV节点列定义已修改，请重新上传")
				}
				currentReq := datasetRequirement(n)
				if currentReq["delimiter"] != string(delimiter) || len(stringList(currentReq["variable_names"])) > 0 && len(stringList(currentReq["variable_names"])) != len(cols) {
					return api(409, "dataset_schema_changed", "CSV节点分隔符或变量数量已修改，请重新上传")
				}
			}
		}
		if !found {
			return api(409, "dataset_node_changed", "CSV节点已删除或停用，请重新选择")
		}

		_, e = tx.Exec("INSERT INTO workflow_datasets(id,project_id,workflow_id,node_id,created_at,data) VALUES(?,?,?,?,?,?)", id, p.projectID(), workflowID, nodeID, data["created_at"], string(encoded(data)))
		return e
	})
	return data, e
}
func copyDataset(dst io.Writer, src io.Reader, limits ...int64) (string, int64, error) {
	limit := int64(datasetLimit)
	if len(limits) > 0 {
		limit = limits[0]
	}
	raw := io.LimitReader(src, limit+1)
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(dst, h), raw)
	if e != nil {
		return "", 0, e
	}
	if n > limit {
		return "", 0, api(413, "dataset_too_large", "CSV最多256MiB")
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
func generateDatasetCSVTo(ctx context.Context, body Obj, delimiter rune, output io.Writer, limit int64, maxRows ...int64) error {
	if _, exists := body["rows"]; !exists {
		body = clone(body)
		body["rows"] = 100
	}
	rows, ok := integer(body["rows"])
	if !ok || rows < 1 || rows > 100000 {
		return api(400, "invalid_generation", "rows须为1至100000")
	}
	requestedRows := rows
	if len(maxRows) > 0 && rows > maxRows[0] {
		rows = maxRows[0]
	}
	specs := arr(body["columns"])
	if len(specs) < 1 || len(specs) > 100 {
		return api(400, "invalid_generation", "须指定1至100列")
	}
	bound := &boundedDatasetWriter{writer: output, remaining: limit}
	w := csv.NewWriter(bound)
	w.Comma = delimiter
	headers := []string{}
	seen := map[string]bool{}
	for _, v := range specs {
		s := obj(v)
		name := str(s["name"])
		if !validEnvironmentName(name) || len(name) > 200 || seen[name] {
			return api(400, "invalid_generation", "列名须有效且唯一")
		}
		seen[name] = true
		headers = append(headers, name)
		if !contains([]string{"sequence", "uuid", "constant", "random_int", "template"}, str(s["generator"])) {
			return api(400, "invalid_generation", "不支持的生成器")
		}
		if s["generator"] == "sequence" {
			if raw, exists := s["start"]; exists {
				start, ok := integer(raw)
				if !ok || start > int64(1<<63-1)-(requestedRows-1) {
					return api(400, "invalid_generation", "序列起始值须为整数，且完整请求行数不能超出64位整数范围")
				}
			}
		}
		if s["generator"] == "template" {
			value, ok := s["value"].(string)
			if !ok || validateDatasetValueTemplate(value) != nil {
				return api(400, "invalid_generation", "模板须为64KiB以内UTF-8文本，且只支持{{row}}与{{uuid}}占位符")
			}
		}
	}
	_ = w.Write(headers)
	seed, _ := integer(body["seed"])
	rng := mathrand.New(mathrand.NewSource(seed))
	for i := int64(0); i < rows; i++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		row := []string{}
		var rowBytes int64
		for _, v := range specs {
			s := obj(v)
			value := ""
			switch s["generator"] {
			case "sequence":
				start := int64(1)
				if n, ok := integer(s["start"]); ok {
					start = n
				}
				value = str(s["prefix"]) + strconv.FormatInt(start+i, 10)
			case "uuid":
				var e error
				value, e = datasetUUID()
				if e != nil {
					return e
				}
			case "template":
				value = str(s["value"])
				uuid := ""
				if strings.Contains(value, "{{uuid}}") {
					var e error
					uuid, e = datasetUUID()
					if e != nil {
						return e
					}
				}
				value = strings.NewReplacer("{{row}}", strconv.FormatInt(i+1, 10), "{{uuid}}", uuid).Replace(value)
			case "constant":
				value = str(s["value"])
			case "random_int":
				min, _ := integer(s["min"])
				max, ok := integer(s["max"])
				if !ok {
					max = 100
				}
				if max < min || float64(max)-float64(min) > 1e12 || max-min < 0 {
					return api(400, "invalid_generation", "随机数范围无效")
				}
				value = strconv.FormatInt(min+rng.Int63n(max-min+1), 10)
			}
			if !utf8.ValidString(value) || s["generator"] != "template" && len(value) > 4096 || strings.ContainsRune(value, 0) {
				return api(400, "invalid_generation", "生成值无效")
			}
			// Bound row construction as well as writes: expanded templates must
			// not accumulate a very large row before the preview writer rejects it.
			rowBytes += int64(len(value)) + 1
			if rowBytes > bound.remaining {
				return api(413, "dataset_too_large", "生成CSV超过配置的大小限制")
			}
			row = append(row, value)
		}
		if e := writeDatasetCSVRecord(w, bound, row); e != nil {
			return e
		}
		w.Flush()
		if e := w.Error(); e != nil {
			return e
		}
	}
	w.Flush()
	return w.Error()
}

// encoding/csv otherwise serializes a sole empty string as a blank line, which
// CSV readers skip. Explicitly quoting this record preserves an actual data row.
func writeDatasetCSVRecord(w *csv.Writer, output io.Writer, record []string) error {
	if len(record) == 1 && record[0] == "" {
		w.Flush()
		if e := w.Error(); e != nil {
			return e
		}
		_, e := io.WriteString(output, "\"\"\n")
		return e
	}
	return w.Write(record)
}

func datasetUUID() (string, error) {
	raw := make([]byte, 16)
	if _, e := rand.Read(raw); e != nil {
		return "", e
	}
	raw[6] = raw[6]&15 | 64
	raw[8] = raw[8]&63 | 128
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(raw[:4]), hex.EncodeToString(raw[4:6]), hex.EncodeToString(raw[6:8]), hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:])), nil
}

func validateDatasetValueTemplate(value string) error {
	if !utf8.ValidString(value) || len(value) > 64*1024 || strings.ContainsRune(value, 0) {
		return api(400, "invalid_generation", "生成模板无效")
	}
	for _, placeholder := range []string{"{{row}}", "{{uuid}}"} {
		value = strings.ReplaceAll(value, placeholder, "")
	}
	if strings.Contains(value, "{{") || strings.Contains(value, "}}") {
		return api(400, "invalid_generation", "不支持的模板占位符")
	}
	return nil
}
func (p *Platform) planDatasets(id string) (Obj, error) {
	plan, e := p.get("plans", id)
	if e != nil {
		return nil, e
	}
	nodes := arr(plan["dataset_requirements"])
	for _, v := range nodes {
		n := obj(v)
		query := "SELECT data FROM workflow_datasets WHERE project_id=? AND node_id=? AND deleted=0"
		args := []any{p.projectID(), n["node_id"]}
		if str(n["workflow_id"]) != "" {
			query += " AND workflow_id=?"
			args = append(args, n["workflow_id"])
		}
		files, e := p.list(query+" ORDER BY created_at,id", args...)
		if e != nil {
			return nil, e
		}
		available := []any{}
		for _, f := range files {
			if strings.Join(stringList(obj(f)["columns"]), "\x00") == strings.Join(stringList(n["columns"]), "\x00") && obj(f)["delimiter"] == n["delimiter"] {
				available = append(available, f)
			}
		}
		n["datasets"] = available
	}
	items := []any{}
	for _, v := range nodes {
		items = append(items, arr(obj(v)["datasets"])...)
	}
	return Obj{"nodes": nodes, "requirements": nodes, "items": items, "script_identities": plan["script_identities"], "cookie_accounts": arr(plan["cookie_accounts"])}, nil
}
func (h Handler) datasetRoute(w http.ResponseWriter, r *http.Request, method string, body Obj) (bool, error) {
	if method == "GET" && (r.URL.Path == "/api/datasets" || r.URL.Path == "/api/datasets/catalog") {
		values, parseError := url.ParseQuery(r.URL.RawQuery)
		if parseError != nil {
			return true, api(400, "invalid_dataset_filter", "数据文件分页或来源筛选参数无效")
		}
		var v Obj
		var e error
		if r.URL.Path == "/api/datasets/catalog" {
			v, e = h.P.datasetManagementCatalog()
		} else {
			v, e = h.P.datasetManagementList(values)
		}
		if e == nil {
			sendJSON(w, r, 200, v)
		}
		return true, e
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[1] == "plans" && parts[3] == "datasets" && method == "GET" {
		v, e := h.P.planDatasets(parts[2])
		if e == nil {
			sendJSON(w, r, 200, v)
		}
		return true, e
	}
	if len(parts) >= 4 && parts[1] == "workflows" && parts[3] == "datasets" {
		id := parts[2]
		if len(parts) == 5 && parts[4] == "template" && method == "POST" {
			v, e := h.P.datasetTemplate(id, body)
			if e == nil {
				sendJSON(w, r, 200, v)
			}
			return true, e
		}
		if len(parts) == 4 && method == "GET" {
			v, e := h.P.datasetList(id)
			if e == nil {
				sendJSON(w, r, 200, v)
			}
			return true, e
		}
		if method == "POST" {
			var v Obj
			var e error
			if len(parts) == 6 && parts[4] == "generate" && parts[5] == "preview" {
				v, e = h.P.datasetGeneratePreview(r.Context(), id, body)
				if e == nil {
					sendJSON(w, r, 200, v)
				}
				return true, e
			} else if len(parts) == 5 && parts[4] == "generate" {
				v, e = h.P.datasetGenerate(r.Context(), id, body)
			} else if len(parts) == 4 {
				v, e = h.P.datasetUpload(w, r, id)
			} else {
				return false, nil
			}
			if e == nil {
				sendJSON(w, r, 201, v)
			}
			return true, e
		}
	}
	if len(parts) >= 3 && parts[1] == "datasets" {
		id := parts[2]
		data, e := h.P.datasetGetQ(h.P.DB, id, false)
		if e != nil {
			return true, e
		}
		if len(parts) == 3 && method == "GET" {
			sendJSON(w, r, 200, data)
			return true, nil
		}
		if len(parts) == 4 && method == "GET" && parts[3] == "table" {
			v, e := h.P.datasetTable(r.Context(), data, r.URL.RawQuery)
			if e == nil {
				sendJSON(w, r, 200, v)
			}
			return true, e
		}
		if len(parts) == 4 && method == "POST" && parts[3] == "edits" {
			v, e := h.P.datasetEdits(r.Context(), data, body)
			if e == nil {
				sendJSON(w, r, 201, v)
			}
			return true, e
		}
		if len(parts) == 3 && method == "DELETE" {
			e = h.P.transaction(func(tx *Tx) error {
				if e := h.P.requireActiveQ(tx); e != nil {
					return e
				}
				_, e := tx.Exec("UPDATE workflow_datasets SET deleted=1 WHERE id=? AND project_id=?", id, h.P.projectID())
				return e
			})
			if e == nil {
				sendJSON(w, r, 200, Obj{"id": id, "deleted": true})
			}
			return true, e
		}
		if len(parts) == 4 && method == "GET" && (parts[3] == "content" || parts[3] == "preview") {
			store, e := h.P.objectStore()
			if e != nil {
				return true, e
			}
			reader, e := store.Get(r.Context(), objectRef(data["object"]))
			if e != nil {
				return true, e
			}
			defer reader.Close()
			if parts[3] == "content" {
				w.Header().Set("Content-Type", "text/csv; charset=utf-8")
				w.Header().Set("Content-Disposition", "attachment; filename=\"dataset.csv\"")
				if r.Method != "HEAD" {
					_, e = io.Copy(w, reader)
				}
				return true, e
			}
			limit := 10
			if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 && n <= 100 {
				limit = n
			}
			delimiter, _ := csvDelimiter(str(data["delimiter"]))
			csvr := datasetCSVReader(io.LimitReader(reader, 1024*1024), delimiter)
			header, e := csvr.Read()
			if e != nil {
				return true, e
			}
			rows := [][]string{}
			for len(rows) < limit {
				row, e := csvr.Read()
				if e == io.EOF {
					break
				}
				if e != nil {
					return true, e
				}
				rows = append(rows, row)
			}
			sendJSON(w, r, 200, Obj{"id": id, "columns": header, "rows": rows, "total_rows": data["rows"]})
			return true, nil
		}
	}
	return false, nil
}

func validateCSVProducerScopes(tree *workflowTree) error {
	type producer struct{ scope, name, node string }
	all := []producer{}
	parents := map[string]string{}
	var index func(*workflowNode, string)
	index = func(n *workflowNode, parent string) {
		parents[n.ID] = parent
		for _, c := range n.Children {
			index(c, n.ID)
		}
	}
	index(tree.Root, "")
	ancestor := func(a, b string) bool {
		for b != "" {
			if a == b {
				return true
			}
			b = parents[b]
		}
		return false
	}
	var walk func(*workflowNode) error
	walk = func(node *workflowNode) error {
		if !node.Enabled {
			return nil
		}
		if node.Component == "CSVDataSet" {
			req := datasetRequirement(node.Source)
			cols := stringList(req["columns"])
			names := stringList(req["variable_names"])
			if len(cols) != len(names) {
				return api(400, "dataset_schema_mismatch", "CSV列数与变量数量不一致："+node.Name)
			}
			seen := map[string]bool{}
			scope := parents[node.ID]
			for _, name := range names {
				if !validEnvironmentName(name) || seen[name] {
					return api(400, "dataset_variable_conflict", "CSV变量名无效或重复："+node.Name)
				}
				seen[name] = true
				for _, old := range all {
					if old.name == name && (ancestor(old.scope, scope) || ancestor(scope, old.scope)) {
						return api(400, "dataset_variable_conflict", "CSV变量冲突："+old.node+" / "+node.Name+"："+name)
					}
				}
				all = append(all, producer{scope, name, node.Name})
			}
		}
		for _, child := range node.Children {
			if e := walk(child); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(tree.Root)
}

// Infer headers only from this workflow's immutable files, preserving separately
// configured variable aliases. Published markers freeze this inferred schema.
func (p *Platform) enrichDatasetGraph(q queryer, graph Obj, workflowID string) error {
	for _, value := range arr(graph["nodes"]) {
		node := obj(value)
		props := obj(node["properties"])
		if props["component"] != "CSVDataSet" || props["enabled"] == false || len(stringList(props["dataset_columns"])) > 0 {
			continue
		}
		var raw string
		e := q.QueryRow("SELECT data FROM workflow_datasets WHERE project_id=? AND workflow_id=? AND node_id=? AND deleted=0 ORDER BY created_at,id LIMIT 1", p.projectID(), workflowID, node["id"]).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		data, e := decode([]byte(raw))
		if e != nil {
			return e
		}
		props["dataset_columns"] = data["columns"]
	}
	return nil
}

func (p *Platform) datasetUpload(w http.ResponseWriter, r *http.Request, workflowID string) (Obj, error) {
	r.Body = http.MaxBytesReader(w, r.Body, p.datasetByteLimit()+1024*1024)
	parts, e := r.MultipartReader()
	if e != nil {
		return nil, api(400, "invalid_upload", "须使用multipart上传CSV")
	}
	dir := filepath.Join(p.Config.StateDir, "dataset-cache")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	var file *os.File
	defer func() {
		if file != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	fields := map[string]string{}
	filename := "dataset.csv"
	for count := 0; ; count++ {
		if count > 8 {
			return nil, api(400, "invalid_upload", "上传字段过多")
		}
		part, e := parts.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, api(400, "invalid_upload", "上传格式无效或文件过大")
		}
		name := part.FormName()
		if name == "file" {
			if file != nil {
				part.Close()
				return nil, api(400, "invalid_upload", "只能上传一个CSV文件")
			}
			filename = part.FileName()
			file, e = os.CreateTemp(dir, "upload-*")
			if e != nil {
				part.Close()
				return nil, e
			}
			_ = file.Chmod(0600)
			size, e := io.Copy(file, io.LimitReader(part, p.datasetByteLimit()+1))
			part.Close()
			if e != nil || size > p.datasetByteLimit() {
				return nil, api(413, "dataset_too_large", "CSV超过大小限制")
			}
		} else {
			if name != "name" && name != "node_id" {
				part.Close()
				return nil, api(400, "invalid_upload", "上传字段无效")
			}
			if _, exists := fields[name]; exists {
				part.Close()
				return nil, api(400, "invalid_upload", "上传字段重复")
			}
			data, e := io.ReadAll(io.LimitReader(part, 4097))
			part.Close()
			if e != nil || len(data) > 4096 || !utf8.Valid(data) {
				return nil, api(400, "invalid_upload", "上传字段无效")
			}
			fields[name] = string(data)
		}
	}
	if file == nil {
		return nil, api(400, "file_required", "须上传CSV文件")
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	name := fields["name"]
	if name == "" {
		name = filename
	}
	return p.datasetCreate(r.Context(), workflowID, fields["node_id"], name, file)
}
func (p *Platform) datasetByteLimit() int64 {
	if p.Config.DatasetMaxBytes > 0 {
		return p.Config.DatasetMaxBytes
	}
	return datasetLimit
}
func (p *Platform) taskInputByteLimit() int64 {
	if p.Config.TaskInputMaxBytes > 0 {
		return p.Config.TaskInputMaxBytes
	}
	return 1024 * 1024 * 1024
}

func datasetCSVReader(input io.Reader, delimiter rune) *csv.Reader {
	buffer := bufio.NewReader(input)
	if prefix, e := buffer.Peek(3); e == nil && bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = buffer.Discard(3)
	}
	r := csv.NewReader(buffer)
	r.Comma = delimiter
	return r
}
