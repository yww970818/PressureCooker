package main

import (
	"context"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

func (p *Platform) runDatasetTable(ctx context.Context, id, nodeID, fileIndex, rawQuery string) (Obj, error) {
	reveal, err := runRevealQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	query, _ := url.ParseQuery(rawQuery)
	page, size := 1, 20
	for _, field := range []struct {
		name   string
		target *int
	}{{"page", &page}, {"page_size", &size}} {
		if values, exists := query[field.name]; exists {
			if len(values) != 1 {
				return nil, api(400, "invalid_dataset_page", "数据表分页参数无效")
			}
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 {
				return nil, api(400, "invalid_dataset_page", "数据表分页参数无效")
			}
			*field.target = n
		}
	}
	if size > 200 || page-1 > int(^uint(0)>>1)/size {
		return nil, api(400, "invalid_dataset_page", "数据表分页参数无效")
	}
	index, err := strconv.Atoi(fileIndex)
	if err != nil || index < 0 || strconv.Itoa(index) != fileIndex {
		return nil, api(404, "not_found", "任务数据文件不存在")
	}
	run, err := p.get("runs", id)
	if err != nil {
		return nil, err
	}
	nodes := arr(run["dataset_snapshot"])
	if len(nodes) == 0 {
		nodes = arr(run["datasets"])
	}
	var node, file Obj
	for _, v := range nodes {
		n := obj(v)
		if str(n["node_id"]) != nodeID {
			continue
		}
		files := arr(n["files"])
		if len(files) == 0 {
			files = arr(n["datasets"])
		}
		if index < len(files) {
			node, file = n, obj(files[index])
		}
		break
	}
	if file == nil {
		return nil, api(404, "not_found", "任务数据文件不存在")
	}
	ref := obj(file["object"])
	if len(ref) == 0 {
		ref = file
	}
	if str(ref["key"]) == "" || str(ref["sha256"]) == "" {
		return nil, api(409, "dataset_snapshot_missing", "历史任务未保存可读取的数据文件快照")
	}
	name := str(file["name"])
	if name == "" {
		name = filepath.Base(str(ref["key"]))
	}
	var total any = file["rows"]
	// Metadata is optional and may be soft-deleted. It is used only when it
	// identifies exactly the immutable object selected by this task.
	for _, v := range arr(run["dataset_selections"]) {
		s := obj(v)
		ids := arr(s["dataset_ids"])
		if str(s["node_id"]) != nodeID || index >= len(ids) {
			continue
		}
		if meta, e := p.datasetGetQ(p.DB, str(ids[index]), true); e == nil {
			pin := obj(meta["object"])
			if pin["key"] == ref["key"] && pin["sha256"] == ref["sha256"] && pin["bucket"] == ref["bucket"] {
				total = meta["rows"]
			}
		}
	}
	delimiter, err := csvDelimiter(str(node["delimiter"]))
	if err != nil {
		return nil, err
	}
	store, err := p.objectStore()
	if err != nil {
		return nil, err
	}
	input, err := store.Get(ctx, objectRef(ref))
	if err != nil {
		return nil, err
	}
	defer input.Close()
	tooLarge := api(413, "dataset_table_too_large", "数据表分页内容超过4MiB，请减小每页行数")
	reader := newDatasetOnlineCSVReader(input, delimiter, datasetTableByteLimit, tooLarge)
	columns, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if strings.Join(columns, "\x00") != strings.Join(stringList(node["columns"]), "\x00") {
		return nil, api(409, "dataset_schema_mismatch", "原始数据文件与任务冻结的列定义不一致")
	}
	rows := [][]string{}
	result := Obj{"run_id": id, "node_id": nodeID, "file_index": index, "name": name, "columns": columns, "variable_names": node["variable_names"], "page": page, "page_size": size, "total_rows": total, "rows": rows, "has_more": false, "sha256": ref["sha256"], "revealed": reveal, "sensitive": false}
	bytes := datasetOnlineJSONBytes(result) + 1
	offset := (page - 1) * size
	variables := stringList(node["variable_names"])
	for seen := 0; ; seen++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := reader.Read()
		if err == io.EOF {
			if total == nil {
				result["total_rows"] = seen
			}
			break
		}
		if err != nil {
			return nil, err
		}
		if len(row) != len(columns) {
			return nil, api(409, "dataset_schema_mismatch", "原始数据行与冻结列定义不一致")
		}
		if seen < offset {
			continue
		}
		if len(rows) == size {
			result["has_more"] = true
			break
		}
		for i, value := range row {
			if sensitiveParameterValue(columns[i], value) || i < len(variables) && sensitiveParameterValue(variables[i], value) {
				result["sensitive"] = true
				if !reveal {
					row[i] = "***"
				}
			}
		}
		bytes += datasetOnlineRowBytes(row, len(rows))
		if bytes > datasetTableByteLimit {
			return nil, tooLarge
		}
		rows = append(rows, row)
	}
	result["rows"] = rows
	if reveal {
		if err = p.audit("run.dataset.reveal", id); err != nil {
			return nil, err
		}
	}
	return result, nil
}
