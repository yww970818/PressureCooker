package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

func datasetCLI(command string, pos []string, option func(string, string) string) error {
	if len(pos) != 1 || validID(pos[0]) != nil {
		return fmt.Errorf("%s requires one valid workflow/plan/dataset ID", command)
	}
	method := http.MethodGet
	path := "/api/datasets/" + pos[0]
	var source io.Reader
	contentType := "application/json"
	var file *os.File
	switch command {
	case "dataset-list":
		path = "/api/workflows/" + pos[0] + "/datasets"
	case "dataset-plan":
		path = "/api/plans/" + pos[0] + "/datasets"
	case "dataset-get":
	case "dataset-preview":
		path += "/preview?limit=" + option("limit", "10")
	case "dataset-delete":
		method = http.MethodDelete
	case "dataset-download":
		path += "/content"
		if option("output", "") == "" {
			return fmt.Errorf("dataset-download requires --output")
		}
	case "dataset-generate":
		raw, e := readLimitedFile(option("json-file", ""), workspaceLimit)
		if e != nil {
			return e
		}
		var body Obj
		if e = json.Unmarshal(raw, &body); e != nil {
			return fmt.Errorf("invalid generation JSON")
		}
		if id := option("node", ""); id != "" {
			body["node_id"] = id
		}
		source = bytes.NewReader(encoded(body))
		method = http.MethodPost
		path = "/api/workflows/" + pos[0] + "/datasets/generate"
	case "dataset-upload":
		f, e := os.Open(option("file", ""))
		if e != nil {
			return e
		}
		file = f
		defer file.Close()
		if validID(option("node", "")) != nil {
			return fmt.Errorf("dataset-upload requires --node")
		}
		reader, writer := io.Pipe()
		form := multipart.NewWriter(writer)
		contentType = form.FormDataContentType()
		source = reader
		defer reader.Close()
		go func() {
			e := form.WriteField("node_id", option("node", ""))
			if e == nil {
				e = form.WriteField("name", option("name", "dataset.csv"))
			}
			if e == nil {
				var part io.Writer
				part, e = form.CreateFormFile("file", "dataset.csv")
				if e == nil {
					_, e = io.Copy(part, file)
				}
			}
			if e == nil {
				e = form.Close()
			}
			_ = writer.CloseWithError(e)
		}()
		method = http.MethodPost
		path = "/api/workflows/" + pos[0] + "/datasets"
	default:
		return fmt.Errorf("unknown dataset command")
	}
	endpoint := strings.TrimRight(option("url", env("JMETER_API_URL", "http://127.0.0.1:8080")), "/")
	req, e := http.NewRequest(method, endpoint+path, source)
	if e != nil {
		return e
	}
	token, e := os.ReadFile(option("token-file", env("JMETER_ADMIN_TOKEN_FILE", "/run/platform/admin-token")))
	if e != nil {
		return fmt.Errorf("token file unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", contentType)
	projectID := option("project", "")
	if projectID == "" {
		return fmt.Errorf("%s requires --project PROJECT_ID", command)
	}
	if e := validID(projectID); e != nil {
		return e
	}
	req.Header.Set("X-Project-ID", projectID)
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("API connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if command == "dataset-download" {
		out, e := os.OpenFile(option("output", ""), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, resp.Body)
		ce := out.Close()
		if e != nil {
			return e
		}
		return ce
	}
	_, e = io.Copy(os.Stdout, io.LimitReader(resp.Body, 8*1024*1024))
	return e
}
