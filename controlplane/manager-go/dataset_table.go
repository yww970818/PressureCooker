package main

import (
	"context"
	"encoding/csv"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Pages retain only the requested rows. Reading a later page skips CSV records
// incrementally, including quoted records containing embedded newlines.
func (p *Platform) datasetTable(ctx context.Context, data Obj, rawQuery string) (Obj, error) {
	invalid := func() error { return api(400, "invalid_dataset_page", "数据表分页参数无效") }
	query, e := url.ParseQuery(rawQuery)
	if e != nil {
		return nil, invalid()
	}
	page, pageSize := 1, 50
	for _, field := range []struct {
		name  string
		value *int
	}{{"page", &page}, {"page_size", &pageSize}} {
		if values, exists := query[field.name]; exists {
			if len(values) != 1 {
				return nil, invalid()
			}
			n, e := strconv.Atoi(values[0])
			if e != nil || n < 1 {
				return nil, invalid()
			}
			*field.value = n
		}
	}
	if pageSize > 200 || page-1 > int(^uint(0)>>1)/pageSize {
		return nil, invalid()
	}
	delimiter, e := csvDelimiter(str(data["delimiter"]))
	if e != nil {
		return nil, e
	}
	store, e := p.objectStore()
	if e != nil {
		return nil, e
	}
	input, e := store.Get(ctx, objectRef(data["object"]))
	if e != nil {
		return nil, e
	}
	defer input.Close()
	tooLarge := api(413, "dataset_table_too_large", "在线数据表单条记录或分页JSON超过4MiB，请减小分页或下载文件查看")
	r := newDatasetOnlineCSVReader(input, delimiter, datasetTableByteLimit, tooLarge)
	columns, e := r.Read()
	if e != nil {
		return nil, e
	}
	rows := [][]string{}
	result := Obj{"id": data["id"], "name": data["name"], "columns": columns, "rows": rows, "page": page, "page_size": pageSize, "total_rows": data["rows"], "sha256": data["sha256"], "version": data["version"], "delimiter": string(delimiter)}
	responseBytes := datasetOnlineJSONBytes(result) + 1 // Gzip JSON encoder's final newline.
	if responseBytes > datasetTableByteLimit {
		return nil, tooLarge
	}
	offset := (page - 1) * pageSize
	for index := 0; len(rows) < pageSize; index++ {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if index >= offset {
			responseBytes += datasetOnlineRowBytes(row, len(rows))
			if responseBytes > datasetTableByteLimit {
				return nil, tooLarge
			}
			rows = append(rows, row)
		}
	}
	result["rows"] = rows
	return result, nil
}

type datasetEditSet struct {
	updates    map[int64][]string
	deletes    map[int64]bool
	appendRows [][]string
}

func parseDatasetEdits(body Obj, width int, rowCount int64) (datasetEditSet, error) {
	changes := datasetEditSet{updates: map[int64][]string{}, deletes: map[int64]bool{}}
	invalid := func() (datasetEditSet, error) {
		return changes, api(400, "invalid_dataset_edits", "数据变更须为有效行索引与等长文本数组，不能重复或删除全部数据")
	}
	count := 0
	for _, key := range []string{"updates", "delete_rows", "append_rows"} {
		if value, exists := body[key]; exists {
			rows, ok := value.([]any)
			if !ok {
				return invalid()
			}
			count += len(rows)
		}
	}
	if count < 1 || count > 10000 {
		return invalid()
	}
	textRow := func(value any) ([]string, bool) {
		row, ok := datasetTemplateColumns(value)
		if !ok || len(row) != width {
			return nil, false
		}
		for _, cell := range row {
			if !utf8.ValidString(cell) || strings.ContainsRune(cell, 0) {
				return nil, false
			}
		}
		return row, true
	}
	for _, value := range arr(body["updates"]) {
		update, ok := value.(map[string]any)
		if !ok {
			return invalid()
		}
		index, ok := integer(update["row_index"])
		if !ok || index < 0 || index >= rowCount || changes.updates[index] != nil {
			return invalid()
		}
		row, ok := textRow(update["values"])
		if !ok {
			return invalid()
		}
		changes.updates[index] = row
	}
	for _, value := range arr(body["delete_rows"]) {
		index, ok := integer(value)
		if !ok || index < 0 || index >= rowCount || changes.deletes[index] || changes.updates[index] != nil {
			return invalid()
		}
		changes.deletes[index] = true
	}
	for _, value := range arr(body["append_rows"]) {
		row, ok := textRow(value)
		if !ok {
			return invalid()
		}
		changes.appendRows = append(changes.appendRows, row)
	}
	if rowCount-int64(len(changes.deletes))+int64(len(changes.appendRows)) < 1 {
		return invalid()
	}
	return changes, nil
}

func (p *Platform) datasetEdits(ctx context.Context, original, body Obj) (Obj, error) {
	baseSHA, ok := body["base_sha256"].(string)
	if !ok || baseSHA == "" {
		return nil, api(400, "dataset_base_required", "须提供原数据文件的base_sha256")
	}
	if baseSHA != str(original["sha256"]) {
		return nil, api(409, "dataset_version_conflict", "原数据文件校验值不一致，请重新加载")
	}
	name := str(original["name"])
	if value, exists := body["name"]; exists {
		if name, ok = value.(string); !ok || strings.TrimSpace(name) == "" || len(name) > 200 {
			return nil, api(400, "invalid_dataset", "文件名称须为1至200字")
		}
	}
	project, e := p.projectGetQ(p.DB, p.projectID(), false)
	if e != nil {
		return nil, e
	}
	if project["archived"] == true {
		return nil, api(409, "project_archived", "项目已归档，不能新增或修改内容")
	}
	workflowID, nodeID := str(original["workflow_id"]), str(original["node_id"])
	workflow, e := p.workspaceGet("workflows", workflowID)
	if e != nil {
		return nil, e
	}
	var requirement Obj
	var physicalColumns []string
	for _, value := range arr(obj(workflow["graph"])["nodes"]) {
		node := obj(value)
		props := obj(node["properties"])
		if node["id"] == nodeID && props["component"] == "CSVDataSet" && props["enabled"] != false {
			requirement = datasetRequirement(node)
			physicalColumns = stringList(props["dataset_columns"])
			break
		}
	}
	if requirement == nil {
		return nil, api(400, "dataset_node_required", "须选择当前流程CSV节点")
	}
	columns := stringList(original["columns"])
	if requirement["delimiter"] != original["delimiter"] || len(physicalColumns) > 0 && strings.Join(physicalColumns, "\x00") != strings.Join(columns, "\x00") || len(stringList(requirement["variable_names"])) > 0 && len(stringList(requirement["variable_names"])) != len(columns) {
		return nil, api(409, "dataset_schema_changed", "CSV节点列定义、分隔符或变量数量已修改，请重新加载")
	}
	rowCount, ok := integer(original["rows"])
	if !ok || rowCount < 1 {
		return nil, api(409, "invalid_dataset", "原数据文件行数无效")
	}
	changes, e := parseDatasetEdits(body, len(columns), rowCount)
	if e != nil {
		return nil, e
	}
	delimiter, e := csvDelimiter(str(original["delimiter"]))
	if e != nil {
		return nil, e
	}
	store, e := p.objectStore()
	if e != nil {
		return nil, e
	}
	input, e := store.Get(ctx, objectRef(original["object"]))
	if e != nil {
		return nil, e
	}
	defer input.Close()
	dir := filepath.Join(p.Config.StateDir, "dataset-cache")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(dir, "edit-*")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	defer os.Remove(f.Name())
	_ = f.Chmod(0600)
	tooLarge := api(413, "dataset_edit_too_large", "在线编辑的原CSV单条记录超过4MiB，请下载文件编辑后上传")
	r := newDatasetOnlineCSVReader(input, delimiter, datasetTableByteLimit, tooLarge)
	r.ReuseRecord = true
	header, e := r.Read()
	if e != nil {
		return nil, e
	}
	if strings.Join(header, "\x00") != strings.Join(columns, "\x00") {
		return nil, api(409, "dataset_schema_changed", "原数据文件表头与元数据不一致")
	}
	bound := &boundedDatasetWriter{writer: f, remaining: p.datasetByteLimit()}
	w := csv.NewWriter(bound)
	w.Comma = delimiter
	if e = w.Write(header); e != nil {
		return nil, e
	}
	var index int64
	for ; ; index++ {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if changes.deletes[index] {
			continue
		}
		if update, exists := changes.updates[index]; exists {
			row = update
		}
		if e = writeDatasetCSVRecord(w, bound, row); e != nil {
			return nil, e
		}
	}
	if index != rowCount {
		return nil, api(409, "dataset_version_conflict", "原数据文件行数与元数据不一致")
	}
	for _, row := range changes.appendRows {
		if e = writeDatasetCSVRecord(w, bound, row); e != nil {
			return nil, e
		}
	}
	w.Flush()
	if e = w.Error(); e != nil {
		return nil, e
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	version, ok := integer(original["version"])
	if !ok || version < 1 {
		version = 1
	}
	rootID := str(original["root_dataset_id"])
	if rootID == "" {
		rootID = str(original["id"])
	}
	return p.datasetCreate(ctx, workflowID, nodeID, name, f, datasetLineage{parentID: str(original["id"]), rootID: rootID, baseSHA: baseSHA, version: version + 1})
}
