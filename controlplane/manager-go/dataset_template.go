package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// datasetTemplate is read-only: ownership is checked against the saved workflow
// even when the caller provides an unsaved graph snapshot. Only column metadata
// is read; neither file contents nor environment values enter the template.
func (p *Platform) datasetTemplate(workflowID string, body Obj) (Obj, error) {
	workflow, err := p.workspaceGet("workflows", workflowID)
	if err != nil {
		return nil, err
	}
	nodeID := str(body["node_id"])
	if err := validID(nodeID); err != nil {
		return nil, err
	}
	graph := obj(workflow["graph"])
	if snapshot, present := body["graph"]; present {
		var ok bool
		graph, ok = snapshot.(map[string]any)
		if !ok {
			return nil, api(400, "invalid_graph", "流程快照须为 graph 对象")
		}
	}
	saved, err := p.datasetList(workflowID)
	if err != nil {
		return nil, err
	}
	node, err := datasetTemplateNode(graph, nodeID, arr(saved["nodes"]))
	if err != nil {
		return nil, err
	}
	result, err := renderDatasetTemplate(node)
	if err != nil {
		return nil, err
	}
	result["node_id"], result["workflow_id"], result["project_id"] = nodeID, workflowID, p.projectID()
	return result, nil
}

// Match WorkflowDatasets.vue: current physical columns, then an actual saved
// file's columns, then current variableNames. Requirement columns alone may be
// inferred aliases, and therefore cannot establish a physical file schema.
func datasetTemplateNode(graph Obj, nodeID string, savedNodes []any) (Obj, error) {
	var selected Obj
	for _, value := range arr(graph["nodes"]) {
		node := obj(value)
		if str(node["id"]) != nodeID {
			continue
		}
		if selected != nil {
			return nil, api(400, "invalid_graph", "CSV 节点标识不能重复")
		}
		selected = node
	}
	if selected == nil || obj(selected["properties"])["component"] != "CSVDataSet" {
		return nil, api(404, "not_found", "CSV 节点不存在")
	}
	props := obj(selected["properties"])
	config := obj(props["config"])
	columns := props["dataset_columns"]
	physical, valid := datasetTemplateColumns(columns)
	if !valid {
		return nil, api(400, "invalid_csv_template", "CSV 文件列须为文本数组")
	}
	if len(physical) == 0 {
		for _, value := range savedNodes {
			saved := obj(value)
			if str(saved["node_id"]) != nodeID {
				continue
			}
			for _, file := range arr(saved["datasets"]) {
				candidate := obj(file)["columns"]
				if names, ok := datasetTemplateColumns(candidate); !ok {
					return nil, api(400, "invalid_csv_template", "CSV 文件列须为文本数组")
				} else if len(names) > 0 {
					columns = candidate
					break
				}
			}
			break
		}
	}
	names := []string{}
	for _, name := range strings.Split(str(config["variableNames"]), ",") {
		if name = datasetTemplateTrim(name); name != "" {
			names = append(names, name)
		}
	}
	delimiter := config["delimiter"]
	if delimiter == nil || delimiter == "" {
		delimiter = ","
	}
	name := str(props["name"])
	if name == "" {
		name = "CSV 数据集"
	}
	return Obj{"node_id": nodeID, "node_name": name, "columns": columns, "variable_names": names, "delimiter": delimiter}, nil
}

func datasetTemplateColumns(value any) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	if names, ok := value.([]string); ok {
		return names, true
	}
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	names := make([]string, len(values))
	for i, value := range values {
		if names[i], ok = value.(string); !ok {
			return nil, false
		}
	}
	return names, true
}

func renderDatasetTemplate(node Obj) (Obj, error) {
	invalid := func(message string) (Obj, error) { return nil, api(400, "invalid_csv_template", message) }
	physical, ok := datasetTemplateColumns(node["columns"])
	if !ok {
		return invalid("CSV 文件列须为文本数组")
	}
	names, ok := datasetTemplateColumns(node["variable_names"])
	if !ok {
		return invalid("CSV 变量名称须为文本数组")
	}
	columns := physical
	if len(columns) == 0 {
		columns = names
	}
	if len(columns) == 0 {
		return invalid("请先配置 CSV 节点的变量名称，或上传文件识别列名后再下载模板。")
	}
	if len(columns) > 100 {
		return invalid("CSV 列名最多 100 列。")
	}
	seen := map[string]bool{}
	for _, name := range columns {
		trimmed := datasetTemplateTrim(name)
		if !utf8.ValidString(name) || trimmed == "" || len(name) > 200 || strings.ContainsAny(name, "${},") || strings.ContainsAny(trimmed[:1], "=+-@") {
			return invalid("CSV 列名须为有效文本，请检查空列名、特殊字符或公式前缀。")
		}
		for _, r := range name {
			if r < 32 || r == 127 {
				return invalid("CSV 列名不能包含控制字符。")
			}
		}
		if seen[name] {
			return invalid("CSV 列名不能重复，请检查节点配置。")
		}
		seen[name] = true
	}
	if len(physical) > 0 && len(names) > 0 && len(physical) != len(names) {
		return invalid("文件列与运行时变量数量不一致，请先调整 CSV 节点配置。")
	}
	delimiter := ","
	if value, present := node["delimiter"]; present && value != nil && value != "" {
		var ok bool
		if delimiter, ok = value.(string); !ok {
			return invalid("CSV 分隔符须为一个有效字符。")
		}
	}
	if !utf8.ValidString(delimiter) || utf8.RuneCountInString(delimiter) != 1 || strings.ContainsAny(delimiter, "\r\n\"\x00\uFFFD") {
		return invalid("CSV 分隔符须为一个有效字符。")
	}
	header := make([]string, len(columns))
	for i, name := range columns {
		if strings.Contains(name, delimiter) || strings.Contains(name, "\"") {
			header[i] = "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
		} else {
			header[i] = name
		}
	}
	return Obj{"filename": datasetTemplateFilename(str(node["node_name"])) + "-" + datasetTemplateFilename(str(node["node_id"])) + "-模板.csv", "content": "\uFEFF" + strings.Join(header, delimiter) + "\r\n", "columns": columns, "delimiter": delimiter}, nil
}

func datasetTemplateTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
}

func datasetTemplateFilename(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune("<>:\"/\\|?*", r) {
			return '_'
		}
		return r
	}, value)
	chars := []rune(datasetTemplateTrim(value))
	if len(chars) > 60 {
		chars = chars[:60]
	}
	value = strings.TrimRight(string(chars), ". ")
	if value == "" {
		return "数据集"
	}
	return value
}
