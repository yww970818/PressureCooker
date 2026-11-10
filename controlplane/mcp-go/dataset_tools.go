package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

func datasetSpecs() []managerSpec {
	return []managerSpec{
		{name: "list_workflow_datasets", description: "列出本项目流程各CSV节点及不可变数据文件。", schema: scopedSchema(map[string]any{"workflowId": projectSchema()}, "workflowId"), readOnly: true},
		{name: "list_plan_datasets", description: "列出本项目已发布脚本CSV节点要求及兼容数据文件。", schema: scopedSchema(map[string]any{"managerPlanId": projectSchema()}, "managerPlanId"), readOnly: true},
		{name: "upload_dataset", description: "上传UTF8 CSV文本到本项目流程CSV节点，文件不可变；MCP文本最多5MiB，大文件使用控制台上传。", schema: scopedSchema(map[string]any{"workflowId": projectSchema(), "nodeId": projectSchema(), "name": strSchema("", 1, 200), "content": strSchema("", 1, 5*1024*1024)}, "workflowId", "nodeId", "name", "content")},
		{name: "generate_dataset", description: "为CSV节点生成并保存数据文件，最多100000行；支持sequence/uuid/constant/random_int。", schema: scopedSchema(map[string]any{"workflowId": projectSchema(), "nodeId": projectSchema(), "name": strSchema("", 0, 200), "rows": intSchema(1, 100000), "columns": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": anyObject()}, "seed": map[string]any{"type": "integer"}}, "workflowId", "nodeId", "rows", "columns")},
		{name: "get_dataset", description: "读取本项目不可变CSV文件元数据。", schema: scopedSchema(map[string]any{"datasetId": projectSchema()}, "datasetId"), readOnly: true},
		{name: "preview_dataset", description: "预览本项目CSV前1至100行。", schema: scopedSchema(map[string]any{"datasetId": projectSchema(), "limit": intSchema(1, 100)}, "datasetId"), readOnly: true},
		{name: "download_dataset", description: "返回本项目CSV元数据及认证下载URL，文件内容不进入MCP响应。", schema: scopedSchema(map[string]any{"datasetId": projectSchema()}, "datasetId"), readOnly: true},
		{name: "delete_dataset", description: "软删除本项目CSV文件，新任务不可再选；保留已提交任务的对象快照。", schema: scopedSchema(map[string]any{"datasetId": projectSchema()}, "datasetId")},
	}
}
func (m *managerTools) datasetTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	s := func(k string) string { v, _ := args[k].(string); return v }
	project := s("projectId")
	method, path, body := http.MethodGet, "/api/datasets/"+s("datasetId"), any(nil)
	switch name {
	case "list_workflow_datasets":
		path = "/api/workflows/" + s("workflowId") + "/datasets"
	case "list_plan_datasets":
		path = "/api/plans/" + s("managerPlanId") + "/datasets"
	case "upload_dataset":
		return m.api.uploadDataset(ctx, project, s("workflowId"), s("nodeId"), s("name"), s("content"))
	case "generate_dataset":
		method = http.MethodPost
		path = "/api/workflows/" + s("workflowId") + "/datasets/generate"
		v := map[string]any{"node_id": s("nodeId")}
		for _, k := range []string{"rows", "name", "columns", "seed"} {
			if value, present := args[k]; present {
				v[k] = value
			}
		}
		body = v
	case "preview_dataset":
		path += "/preview"
		if n, present := args["limit"]; present {
			path += "?limit=" + queryValue(n)
		}
	case "delete_dataset":
		method = http.MethodDelete
	case "download_dataset":
		metadata, e := m.api.requestProject(ctx, http.MethodGet, path, nil, "", project)
		if e != nil {
			return nil, e
		}
		link, e := m.api.scopedDownloadURL(path+"/content", project)
		if e != nil {
			return nil, e
		}
		metadata["download_url"] = link
		metadata["download_path"] = path + "/content"
		metadata["project_id"] = project
		metadata["requires_authentication"] = true
		metadata["request_headers"] = map[string]string{"X-Project-ID": project}
		return metadata, nil
	}
	return m.api.requestProject(ctx, method, path, body, "", project)
}
func (c managerClient) uploadDataset(ctx context.Context, project, workflow, node, name, content string) (map[string]any, error) {
	var b bytes.Buffer
	form := multipart.NewWriter(&b)
	_ = form.WriteField("node_id", node)
	_ = form.WriteField("name", name)
	part, e := form.CreateFormFile("file", "dataset.csv")
	if e != nil {
		return nil, e
	}
	_, _ = io.WriteString(part, content)
	_ = form.Close()
	token, e := os.ReadFile(c.tokenFile)
	if e != nil {
		return nil, errors.New("Manager credential unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/api/workflows/"+workflow+"/datasets", &b)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("X-Project-ID", project)
	req.Header.Set("Content-Type", form.FormDataContentType())
	if err := signManagerCaller(req, strings.TrimSpace(string(token))); err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if c.client != nil {
		client.Transport = c.client.Transport
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("Manager API unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Manager API HTTP %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse+1))
	if e != nil || len(raw) > maxAPIResponse {
		return nil, errors.New("Manager API response too large")
	}
	var result map[string]any
	if e = json.Unmarshal(raw, &result); e != nil {
		return nil, e
	}
	return result, nil
}
