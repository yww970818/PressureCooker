package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type boundedDatasetWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedDatasetWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, api(413, "dataset_too_large", "生成CSV超过配置的大小限制")
	}
	n, e := w.writer.Write(data)
	w.remaining -= int64(n)
	return n, e
}
func (p *Platform) datasetGenerationInput(workflowID string, body Obj) (Obj, string, rune, error) {
	workflow, e := p.workspaceGet("workflows", workflowID)
	if e != nil {
		return nil, "", 0, e
	}
	nodeID := str(body["node_id"])
	var req Obj
	var physicalColumns []string
	for _, value := range arr(obj(workflow["graph"])["nodes"]) {
		node := obj(value)
		props := obj(node["properties"])
		if node["id"] == nodeID && props["component"] == "CSVDataSet" && props["enabled"] != false {
			req = datasetRequirement(node)
			physicalColumns = stringList(props["dataset_columns"])
			break
		}
	}
	if req == nil {
		return nil, "", 0, api(400, "dataset_node_required", "须选择当前流程CSV节点")
	}
	delimiter, e := csvDelimiter(str(req["delimiter"]))
	if e != nil {
		return nil, "", 0, e
	}
	body = clone(body)
	if len(arr(body["columns"])) == 0 {
		columns := []any{}
		for _, name := range stringList(req["columns"]) {
			columns = append(columns, Obj{"name": name, "generator": "sequence"})
		}
		body["columns"] = columns
	}
	name := str(body["name"])
	if name == "" {
		name = "生成数据.csv"
	}
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return nil, "", 0, api(400, "invalid_dataset", "文件名称须为1至200字")
	}
	columns := []string{}
	for _, spec := range arr(body["columns"]) {
		columns = append(columns, str(obj(spec)["name"]))
	}
	if len(physicalColumns) > 0 && strings.Join(physicalColumns, "\x00") != strings.Join(columns, "\x00") {
		return nil, "", 0, api(400, "dataset_schema_mismatch", "CSV表头与节点列定义不一致")
	}
	if names := stringList(req["variable_names"]); len(names) > 0 && len(names) != len(columns) {
		return nil, "", 0, api(400, "dataset_schema_mismatch", "CSV列数与变量数量不一致")
	}
	return body, name, delimiter, nil
}

func (p *Platform) datasetGenerate(ctx context.Context, workflowID string, body Obj) (Obj, error) {
	body, name, delimiter, e := p.datasetGenerationInput(workflowID, body)
	if e != nil {
		return nil, e
	}
	dir := filepath.Join(p.Config.StateDir, "dataset-cache")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(dir, "generate-*")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	defer os.Remove(f.Name())
	_ = f.Chmod(0600)
	if e = generateDatasetCSVTo(ctx, body, delimiter, f, p.datasetByteLimit()); e != nil {
		return nil, e
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	return p.datasetCreate(ctx, workflowID, str(body["node_id"]), name, f)
}

func (p *Platform) datasetGeneratePreview(ctx context.Context, workflowID string, body Obj) (Obj, error) {
	body, _, delimiter, e := p.datasetGenerationInput(workflowID, body)
	if e != nil {
		return nil, e
	}
	var output bytes.Buffer
	tooLarge := api(413, "dataset_preview_too_large", "生成预览CSV或JSON超过1MiB，请减小预览单元格或列数")
	if e = generateDatasetCSVTo(ctx, body, delimiter, &output, min(p.datasetByteLimit(), int64(datasetPreviewByteLimit)), 10); e != nil {
		if status, _, _ := requestFailure(e); status == 413 {
			return nil, tooLarge
		}
		return nil, e
	}
	r := newDatasetOnlineCSVReader(&output, delimiter, datasetPreviewByteLimit, tooLarge)
	columns, e := r.Read()
	if e != nil {
		return nil, e
	}
	total, exists := body["rows"]
	if !exists {
		total = 100
	}
	rows := [][]string{}
	result := Obj{"columns": columns, "rows": rows, "total_rows": total, "delimiter": string(delimiter)}
	responseBytes := len(encoded(result))
	if responseBytes > datasetPreviewByteLimit {
		return nil, tooLarge
	}
	for len(rows) < 10 {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		responseBytes += datasetOnlineRowBytes(row, len(rows))
		if responseBytes > datasetPreviewByteLimit {
			return nil, tooLarge
		}
		rows = append(rows, row)
	}
	result["rows"] = rows
	return result, nil
}
