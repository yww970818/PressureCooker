package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Handler struct{ P *Platform }

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started, requestID := time.Now(), newID("req-")
	w.Header().Set("X-Request-ID", requestID)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		finish := progressRequestBody(w, r, h.P.Config.UploadIdleTimeout)
		defer finish()
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Server", "JMeterManager/2-Go")
	defer func() {
		if recovered := recover(); recovered != nil {
			logRequestFailure(r, requestID, "internal_error", recovered, started)
			sendJSON(w, r, 500, Obj{"error": Obj{"code": "internal_error", "message": "Internal server error"}})
		}
	}()
	if e := h.route(w, r); e != nil {
		status, code, message := requestFailure(e)
		logRequestFailure(r, requestID, code, e, started)
		sendJSON(w, r, status, Obj{"error": Obj{"code": code, "message": message}})
	}
}
func sendJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Only owned JSON responses; never wrap proxy, websocket, SSE or artifacts.
	if r.Method == "GET" && status == 200 && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.WriteHeader(status)
		z, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
		_ = json.NewEncoder(z).Encode(value)
		_ = z.Close()
		return
	}
	raw := encoded(value)
	w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(raw)
	}
}
func acceptsGzip(value string) bool {
	for _, part := range strings.Split(value, ",") {
		tokens := strings.Split(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(tokens[0]), "gzip") {
			for _, parameter := range tokens[1:] {
				pair := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
				if len(pair) == 2 && pair[0] == "q" {
					q, e := strconv.ParseFloat(pair[1], 64)
					if e != nil || q <= 0 {
						return false
					}
				}
			}
			return true
		}
	}
	return false
}

func sendFile(w http.ResponseWriter, r *http.Request, path, contentType string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.WriteHeader(200)
	if r.Method != "HEAD" {
		_, e = io.Copy(w, f)
	}
	return e
}
func readJSON(r *http.Request) (Obj, error) {
	limit := int64(6 * 1024 * 1024)
	if strings.HasPrefix(r.URL.Path, "/api/datasets/") && strings.HasSuffix(r.URL.Path, "/edits") {
		limit = 8 * 1024 * 1024
	}
	if r.URL.Path == "/api/workflows" || strings.HasPrefix(r.URL.Path, "/api/workflows/") {
		capacity, e := workflowCapacity()
		if e != nil {
			return nil, api(500, "workflow_capacity_config", "工作流容量配置无效")
		}
		limit = capacity.BodyBytes
	}
	if strings.HasPrefix(r.URL.Path, "/api/workflows/") && strings.HasSuffix(r.URL.Path, "/datasets/generate/preview") {
		limit = 8 * 1024 * 1024
	}
	if len(r.TransferEncoding) > 0 {
		return nil, api(400, "invalid_body", "Chunked request bodies are not accepted")
	}
	if r.ContentLength < 0 {
		return nil, api(400, "invalid_body", "Invalid Content-Length")
	}
	if r.ContentLength > limit {
		return nil, api(413, "body_too_large", fmt.Sprintf("Request exceeds %d MiB", limit/(1024*1024)))
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e != nil {
		return nil, api(400, "invalid_body", "Invalid body")
	}
	if int64(len(b)) > limit {
		return nil, api(413, "body_too_large", fmt.Sprintf("Request exceeds %d MiB", limit/(1024*1024)))
	}
	if len(b) == 0 {
		return Obj{}, nil
	}
	if !utf8.Valid(b) {
		return nil, api(400, "invalid_json", "Invalid JSON body")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m Obj
	if e = d.Decode(&m); e != nil || m == nil {
		return nil, api(400, "invalid_json", "JSON object required")
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return nil, api(400, "invalid_json", "Invalid JSON body")
	}
	return m, nil
}
func (h Handler) cookie(w http.ResponseWriter, sid string, clear bool) {
	max := h.P.Config.SessionSeconds
	if clear {
		max = -1
	}
	http.SetCookie(w, &http.Cookie{Name: "jmeter_session", Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: max, Secure: h.P.Config.SecureCookie})
}
func (h Handler) route(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/nodes/") {
		return h.nodeReportRoute(w, r)
	}
	if strings.HasPrefix(path, "/api/runner/") {
		return h.runnerRoute(w, r)
	}
	if strings.ContainsAny(path, "\\\x00") {
		return api(400, "unsafe_path", "Invalid path")
	}
	if path == "/api/api-proxy" {
		return h.P.apiProxy(w, r)
	}
	if path == "/api/ws-proxy" {
		return h.P.wsProxy(w, r)
	}
	method := r.Method
	if method == "HEAD" {
		method = "GET"
	}
	if method != "GET" && method != "POST" && !(method == "DELETE" && historyDeletePath(path)) && !((workspacePath(path) || projectPath(path) || interfacePluginPath(path) || workflowExtensionPath(path) || strings.HasPrefix(path, "/api/clusters/") || strings.HasPrefix(path, "/api/datasets/")) && (method == "PUT" || method == "DELETE")) {
		return api(405, "method_not_allowed", "Method not allowed")
	}
	if path == "/healthz" && method == "GET" {
		sendJSON(w, r, 200, Obj{"ok": true, "id": h.P.Config.ManagerID})
		return nil
	}
	if path == "/readyz" && method == "GET" {
		value := h.P.readiness(r.Context())
		status := http.StatusOK
		if value["ok"] != true {
			status = http.StatusServiceUnavailable
		}
		sendJSON(w, r, status, value)
		return nil
	}
	if path == "/api/login" && method == "POST" {
		body, e := readJSON(r)
		if e != nil {
			return e
		}
		sid, result, e := h.P.login(body["token"])
		if e != nil {
			return e
		}
		h.cookie(w, sid, false)
		sendJSON(w, r, 200, result)
		return nil
	}
	if !strings.HasPrefix(path, "/api/") {
		if method != "GET" {
			return api(405, "method_not_allowed", "Method not allowed")
		}
		rel := strings.TrimLeft(path, "/")
		if rel == "" {
			rel = "index.html"
		}
		target, e := safePath(h.P.Config.FrontendDir, rel, false)
		if e != nil {
			return e
		}
		info, e := os.Stat(target)
		if e != nil || !info.Mode().IsRegular() {
			if strings.Contains(filepath.Base(rel), ".") {
				return api(404, "not_found", "File not found")
			}
			rel = "index.html"
			target, e = safePath(h.P.Config.FrontendDir, rel, true)
			if e != nil {
				return e
			}
		}
		return sendFrontendFile(w, r, target, rel)
	}
	auth, e := h.P.authenticate(r, method != "GET")
	if e != nil {
		return e
	}
	operation, e := h.P.environmentOperationForRequest(r, auth)
	if e != nil {
		return e
	}
	r = r.WithContext(withEnvironmentOperation(r.Context(), operation))
	body := Obj{}
	if (method == "POST" || method == "PUT" || (method == "DELETE" && (interfacePluginPath(r.URL.Path) || workflowExtensionPath(r.URL.Path) || collectionEnvironmentResourcePath(r.URL.Path)))) && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		body, e = readJSON(r)
		if e != nil {
			return e
		}
	}
	// Scope a handler value, never the shared scheduler platform.
	projectID, e := requestProjectID(r)
	if e != nil {
		return e
	}
	h.P = h.P.forRequest(r.Context(), projectID)
	if projectID != "" {
		w.Header().Set("X-Project-ID", projectID)
	}
	if _, reportID, relative, ok := projectReportHTMLPath(path); ok {
		if method != "GET" {
			return api(405, "method_not_allowed", "历史报告仅支持读取")
		}
		if _, e := h.P.projectGetQ(contextQueryer{r.Context(), h.P.DB}, projectID, false); e != nil {
			return e
		}
		target, e := h.P.reportFile(reportID, "html", relative)
		if e != nil {
			return e
		}
		if relative == "" && !strings.HasSuffix(path, "/") {
			http.Redirect(w, r, projectReportHTMLURL(r, projectID, reportID, ""), http.StatusTemporaryRedirect)
			return nil
		}
		return sendFile(w, r, target, "")
	}
	if handled, e := h.projectRoute(w, r, method, body); handled {
		return e
	}
	if projectID != "" {
		if _, e := h.P.projectGetQ(contextQueryer{r.Context(), h.P.DB}, projectID, false); e != nil {
			return e
		}
	}
	if handled, e := h.workflowExtensionRoute(w, r, method, body); handled {
		return e
	}
	if handled, e := h.interfacePluginRoute(w, r, method, body); handled {
		return e
	}
	if handled, e := h.datasetRoute(w, r, method, body); handled {
		return e
	}
	if handled, e := h.historyDeleteRoute(w, r, method, body); handled {
		return e
	}
	if handled, e := h.pressureRoute(w, r, method, body); handled {
		return e
	}
	if handled, e := h.workspaceRoute(w, r, method, body); handled {
		return e
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "clusters" && method == "PUT" {
		v, e := h.P.updateCluster(parts[2], body)
		if e != nil {
			return e
		}
		sendJSON(w, r, 200, v)
		return nil
	}
	q := r.URL.Query()
	key := r.Header.Get("Idempotency-Key")
	switch path {
	case "/api/environment-runtime":
		if method != "GET" {
			return api(405, "method_not_allowed", "运行时环境取值仅支持GET")
		}
		if _, e := h.P.projectGetQ(contextQueryer{r.Context(), h.P.DB}, h.P.projectID(), false); e != nil {
			return e
		}
		sendJSON(w, r, 200, environmentOperationFromContext(r.Context()).values())
		return nil
	case "/api/diagnostics":
		if method == "GET" {
			value, e := h.P.diagnostics(r.Context())
			if e != nil {
				return e
			}
			sendJSON(w, r, 200, value)
			return nil
		}
	case "/api/session":
		if method == "GET" {
			sendJSON(w, r, 200, Obj{"authenticated": auth["authenticated"], "csrf_token": auth["csrf_token"]})
			return nil
		}
	case "/api/logout":
		if method == "POST" {
			if e = h.P.logout(auth); e != nil {
				return e
			}
			h.cookie(w, "", true)
			sendJSON(w, r, 200, Obj{"authenticated": false})
			return nil
		}
	case "/api/mcp/tools":
		if method == "GET" {
			sendJSON(w, r, 200, h.P.mcpTools(r.Context()))
			return nil
		}
	case "/api/cluster":
		if method == "GET" {
			v, e := h.P.clusterContext(r.Context())
			if e != nil {
				return e
			}
			sendJSON(w, r, 200, v)
			return nil
		}
	case "/api/openapi.json":
		if method == "GET" {
			sendJSON(w, r, 200, openAPI())
			return nil
		}
	case "/api/plans":
		if method == "GET" {
			var v Obj
			var e error
			if q.Get("view") == "summary" {
				v, e = h.P.listPlanSummaries(q)
			} else {
				v, e = h.P.listPlans(q)
			}
			if e != nil {
				return e
			}
			sendJSON(w, r, 200, v)
			return nil
		}
		if method == "POST" {
			v, e := h.P.createPlan(body)
			if e != nil {
				return e
			}
			sendJSON(w, r, 201, v)
			return nil
		}
	case "/api/runs":
		if method == "GET" {
			limit, e := parseInt(q.Get("limit"), 50)
			if e != nil {
				return api(400, "invalid_pagination", "Invalid pagination")
			}
			offset, e := parseInt(q.Get("offset"), 0)
			if e != nil {
				return api(400, "invalid_pagination", "Invalid pagination")
			}
			var v Obj
			if q.Get("view") == "summary" {
				v, e = h.P.listRunSummaries(r.Context(), q.Get("status"), q.Get("search"), limit, offset)
			} else {
				v, e = h.P.listRunsContext(r.Context(), q.Get("status"), q.Get("search"), limit, offset)
			}
			if e != nil {
				return e
			}
			sendJSON(w, r, 200, v)
			return nil
		}
		if method == "POST" {
			v, created, e := h.P.createRun(body, key)
			if e != nil {
				return e
			}
			status := 200
			if created {
				status = 201
			}
			sendJSON(w, r, status, v)
			return nil
		}
	case "/api/compare":
		if method == "GET" {
			return api(410, "comparison_retired", "Result comparison is disabled")
		}
	}
	if len(parts) >= 3 && parts[0] == "api" {
		id := parts[2]
		switch parts[1] {
		case "plans":
			if method == "POST" && len(parts) == 4 && parts[3] == "archive" {
				value, e := h.P.archivePlan(id, body)
				if e != nil {
					return e
				}
				sendJSON(w, r, 200, value)
				return nil
			}
			if method == "GET" && len(parts) == 4 && parts[3] == "workflows" {
				value, e := h.P.planWorkflows(id)
				if e != nil {
					return e
				}
				sendJSON(w, r, 200, value)
				return nil
			}

			if method == "GET" && len(parts) == 4 && parts[3] == "graph" {
				value, e := h.P.planGraph(id)
				if e != nil {
					return e
				}
				if r.URL.Query().Get("view") == "editor" {
					value = editorReadPayload(value)
				}
				sendJSON(w, r, 200, value)
				return nil
			}
			if method == "GET" && len(parts) == 3 {
				v, e := h.P.get("plans", id)
				if e != nil {
					return e
				}
				sendJSON(w, r, 200, v)
				return nil
			}
			if method == "GET" && len(parts) == 4 && parts[3] == "content" {
				v, e := h.P.get("plans", id)
				if e != nil {
					return e
				}
				target, e := safePath(h.P.Config.PlansDir, "snapshots/"+str(v["sha256"])+".jmx", true)
				if e != nil {
					return e
				}
				return sendFile(w, r, target, "application/xml; charset=utf-8")
			}
		case "runs":
			if method == "GET" && len(parts) == 8 && parts[3] == "datasets" && parts[5] == "files" && parts[7] == "table" {
				value, e := h.P.runDatasetTable(r.Context(), id, parts[4], parts[6], r.URL.RawQuery)
				if e != nil {
					return e
				}
				sendJSON(w, r, 200, value)
				return nil
			}
			if method == "GET" && len(parts) == 3 {
				v, e := h.P.getRun(id)
				if e != nil {
					return e
				}
				sendJSON(w, r, 200, v)
				return nil
			}
			if method == "GET" && len(parts) == 5 && parts[3] == "artifacts" {
				name := parts[4]
				if handled, e := h.P.streamRunnerArtifact(w, r, id, name); handled {
					return e
				}
				target, e := h.P.artifactFile(id, name)
				if e != nil {
					return e
				}
				contentType := "text/plain"
				if name == "results.jtl" {
					contentType = "text/csv"
				}
				w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
				return sendFile(w, r, target, contentType)
			}
			if len(parts) == 4 {
				switch parts[3] {
				case "parameters":
					if method == "GET" {
						reveal, e := runRevealQuery(r.URL.RawQuery)
						if e != nil {
							return e
						}
						value, e := h.P.runParameterTable(id, reveal)
						if e != nil {
							return e
						}
						sendJSON(w, r, 200, value)
						return nil
					}
				case "copy-config":
					if method == "GET" {
						v, e := h.P.runCopyConfig(id)
						if e != nil {
							return e
						}
						sendJSON(w, r, 200, v)
						return nil
					}
				case "artifacts":
					if method == "GET" {
						v, e := h.P.listArtifacts(id)
						if e != nil {
							return e
						}
						sendJSON(w, r, 200, v)
						return nil
					}
				case "stop":
					if method == "POST" {
						mode := "graceful"
						if v, ok := body["mode"]; ok {
							mode = str(v)
						}
						v, e := h.P.stopRun(id, mode)
						if e != nil {
							return e
						}
						sendJSON(w, r, 202, v)
						return nil
					}
				case "log":
					if method == "GET" {
						tail, e := parseInt(q.Get("tail"), 200)
						if e != nil {
							return api(400, "invalid_tail", "Invalid tail count")
						}
						v, e := h.P.runLog(id, tail)
						if e != nil {
							return e
						}
						sendJSON(w, r, 200, v)
						return nil
					}
				case "reports":
					if method == "POST" {
						if _, e := h.P.get("runs", id); e != nil {
							return e
						}
						return api(410, "reports_retired", "New reports are disabled; original JTL and logs are available")
					}
				}
			}
		case "reports":
			if method == "GET" && len(parts) == 3 {
				v, e := h.P.report(id)
				if e != nil {
					return e
				}
				sendJSON(w, r, 200, v)
				return nil
			}
			if method == "GET" && len(parts) >= 4 {
				kind := parts[3]
				if kind == "download" && len(parts) == 4 {
					return h.download(w, r, id)
				}
				if kind == "html" || ((kind == "analysis" || kind == "summary") && len(parts) == 4) {
					target, e := h.P.reportFile(id, kind, strings.Join(parts[4:], "/"))
					if e != nil {
						return e
					}
					if kind == "html" {
						http.Redirect(w, r, projectReportHTMLURL(r, projectID, id, strings.Join(parts[4:], "/")), http.StatusTemporaryRedirect)
						return nil
					}
					contentType := ""
					if kind == "analysis" {
						contentType = "text/markdown; charset=utf-8"
					}
					if kind == "summary" {
						contentType = "application/json; charset=utf-8"
					}
					return sendFile(w, r, target, contentType)
				}
			}
		}
	}
	return api(404, "not_found", "API endpoint not found")
}

var bearerLogRE = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)\S+`)

func (p *Platform) runLog(id string, tail int) (Obj, error) {
	run, e := p.get("runs", id)
	if e != nil {
		return nil, e
	}
	if tail < 1 {
		tail = 1
	}
	if tail > 2000 {
		tail = 2000
	}
	text := ""
	truncated := false
	if str(run["execution_mode"]) == "redis" {
		if !contains(terminal, str(run["status"])) && str(run["controller_id"]) != "" {
			remote, e := newClient(p.Config).request(p.Config.ControllerURLs[str(run["controller_id"])], "/jobs/"+id+"/log", nil)
			if e == nil {
				text = str(remote["text"])
			}
		}
		for _, name := range []string{"console.log", "jmeter.log"} {
			for _, v := range arr(obj(run["raw_archive"])["files"]) {
				f := obj(v)
				if str(f["name"]) != name {
					continue
				}
				store, e := p.objectStore()
				if e != nil {
					return nil, e
				}
				ctx, cancel := context.WithCancel(p.requestContext())
				reader, e := store.Get(ctx, objectRef(f))
				if e != nil {
					cancel()
					return nil, e
				}
				buffer := &tailBuffer{limit: 256 * 1024}
				copied, e := io.Copy(buffer, reader)
				reader.Close()
				cancel()
				if e != nil {
					return nil, e
				}
				text = string(buffer.data)
				truncated = copied > int64(buffer.limit)
				break
			}
			if text != "" {
				break
			}
		}
	} else {
		for _, name := range []string{"console.log", "jmeter.log"} {
			path, e := safePath(p.Config.ResultsDir, id+"/raw/"+name, true)
			if a, ok := e.(*APIError); ok && a.Status == 404 {
				continue
			}
			if e != nil {
				return nil, e
			}
			f, e := os.Open(path)
			if e != nil {
				return nil, e
			}
			info, e := f.Stat()
			if e != nil {
				f.Close()
				return nil, e
			}
			offset := info.Size() - 256*1024
			if offset < 0 {
				offset = 0
			}
			if _, e = f.Seek(offset, io.SeekStart); e != nil {
				f.Close()
				return nil, e
			}
			b, e := io.ReadAll(io.LimitReader(f, 256*1024))
			f.Close()
			if e != nil {
				return nil, e
			}
			text = string(b)
			truncated = offset > 0
			break
		}
	}
	text = publicRunLogText(run, text, truncated, tail)
	return Obj{"project_id": objectProject(run), "text": text}, nil
}
func (h Handler) download(w http.ResponseWriter, r *http.Request, id string) error {
	report, e := h.P.report(id)
	if e != nil {
		return e
	}
	if str(report["status"]) != "READY" {
		return api(409, "report_unavailable", "Report is not READY")
	}
	relative := str(report["legacy_html_path"])
	if relative == "" {
		relative = str(report["run_id"]) + "/reports/" + id
	}
	base, e := safePath(h.P.Config.ResultsDir, relative, false)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp("", "jmeter-report-*.zip")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	archive := zip.NewWriter(f)
	e = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkError error) error {
		if os.IsNotExist(walkError) {
			return nil
		}
		if walkError != nil {
			return walkError
		}
		if path == base {
			return nil
		}
		relative, e := filepath.Rel(base, path)
		if e != nil {
			return e
		}
		checked, e := safePath(base, filepath.ToSlash(relative), false)
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		info, e := os.Stat(checked)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return api(400, "unsafe_path", "Unsupported report file")
		}
		header, e := zip.FileInfoHeader(info)
		if e != nil {
			return e
		}
		header.Name = filepath.ToSlash(relative)
		header.Method = zip.Deflate
		output, e := archive.CreateHeader(header)
		if e != nil {
			return e
		}
		source, e := os.Open(checked)
		if e != nil {
			return e
		}
		_, e = io.Copy(output, source)
		source.Close()
		return e
	})
	closeErr := archive.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.zip"`)
	return sendFile(w, r, f.Name(), "application/zip")
}
func openAPI() Obj {
	descriptions := map[string]map[string]string{
		"/api/workflows/{id}/nodes":                          {"get": "按必填revision和逗号分隔ids读取指定版本节点参数；项目隔离；可返回editor-text-v1；不写入数据"},
		"/api/workflows/{id}/versions/{revision}":            {"get": "读取精确流程历史版本；view=editor返回完整编辑内容，可使用editor-text-v1编码"},
		"/api/pressure-reports/{id}/monitoring":              {"get": "读取监控补充分页，默认20条；可选revision，export=1显式全量；无数据revision=0", "post": "向READY报告追加脱敏监控快照；revision与Idempotency-Key必填，component为es/mysql/redis/kafka/jvm；不修改JTL指标或判定"},
		"/api/pressure-reports/{id}/monitoring/jvm-batch":    {"post": "原子批量写入JVM监控{revision,entries}；每批1至200实例、4MiB、60000时序点；Idempotency-Key必填，整个批次只增加一个监控revision并返回写入摘要"},
		"/api/runs/{id}/copy-config":                         {"get": "读取原任务可编辑配置，不创建或执行任务；提交副本时传copy_source_run_id及copy_environment_snapshot，使用新的Idempotency-Key"},
		"/api/runs/{id}/parameters":                          {"get": "读取任务冻结的执行配置、环境参数、接口生效值和全局属性；reveal=true显式查看敏感原值"},
		"/api/runs/{id}/datasets/{node}/files/{index}/table": {"get": "按任务固定MinIO对象分页读取原始CSV；page/page_size（最多200），reveal=true显式查看敏感单元格；不依赖当前文件是否已编辑或软删除"},
		"/api/plans/{id}/workflows":                          {"get": "读取此精确脚本的流程来源和版本关联"}, "/api/workflows/{id}/plans": {"get": "读取流程所有版本关联的不可变脚本"}, "/api/workflows/preview": {"post": "按未保存graph编译JMX源码预览，不写入数据或文件"},
		"/api/plans/{id}/graph":   {"get": "从不可变JMX脚本生成只读流程预览，不创建草稿"},
		"/api/plans/{id}/archive": {"post": "设置目录归档状态 {archived:boolean}；可恢复、幂等，须为当前活跃项目；Cookie会话须CSRF；不修改JMX、发布元数据或历史任务"},
		"/api/workflows":          {"get": "工作流列表", "post": "创建经过校验的JMeter工作流"}, "/api/workflows/{id}": {"get": "读取工作流", "put": "按revision版本号保存工作流", "delete": "软删除工作流"}, "/api/workflows/import": {"post": "从JMX内容或不可变计划导入工作流"}, "/api/workflows/{id}/export": {"get": "导出保留原始扩展配置的JMX"}, "/api/workflows/{id}/publish": {"post": "校验revision并发布为不可变计划，不执行请求"}, "/api/api-collections": {"get": "API集合列表", "post": "创建完整OpenAPI文档与Scalar客户端配置"}, "/api/api-collections/{id}": {"get": "读取API集合", "put": "按revision保存API集合", "delete": "软删除API集合"}, "/api/api-collections/{id}/versions": {"get": "API集合历史版本"}, "/api/api-collections/{id}/versions/{revision}": {"get": "读取单个历史版本"}, "/api/api-proxy": {"get": "Scalar主控代理；所有方法须会话Cookie和CSRF令牌", "post": "转发Scalar请求；业务Authorization仅用于目标认证"},
		"/api/mcp/tools": {"get": "读取当前MCP服务连接信息与工具目录"},
		"/api/login":     {"post": "Create HttpOnly session with token; returns csrf_token"}, "/api/session": {"get": "Authenticated session"}, "/api/logout": {"post": "Invalidate session"}, "/api/cluster": {"get": "Manager leadership, clusters and membership, controller/worker status, queue"}, "/api/clusters/{id}": {"put": "Update global cluster {revision,name,worker_ids?,callback_url?}; OCC; cookie sessions require CSRF"}, "/api/plans": {"get": "List immutable plan versions", "post": "Upload {name,content,description}; XML <=5MiB"}, "/api/plans/{id}": {"get": "Get metadata"}, "/api/plans/{id}/content": {"get": "Get immutable XML"}, "/api/runs": {"get": "List runs; status,search,limit,offset query parameters", "post": "Queue {name,plan_id,cluster_id,workers?,properties,notes,environment_id,dataset_selections?,timeout_seconds?}; environment required for every new task; Idempotency-Key required"}, "/api/runs/{id}": {"get": "Run details with reports and latest READY summary"}, "/api/runs/{id}/stop": {"post": "Request {mode:graceful|now}; asynchronous cleanup"}, "/api/runs/{id}/log": {"get": "Bounded redacted text; tail=200"}, "/api/runs/{id}/reports": {"post": "Retired: 410; original artifacts only"}, "/api/runs/{id}/artifacts": {"get": "Original JTL/log metadata and archive receipts; terminal run required"}, "/api/runs/{id}/artifacts/{filename}": {"get": "Authenticated streaming original file download"}, "/api/reports/{id}": {"get": "Report status and summary"}, "/api/reports/{id}/html/{path}": {"get": "Redirect authenticated legacy report files to explicit project HTML path"}, "/api/projects/{project_id}/reports/{id}/html/{path}": {"get": "Authenticated original report files with explicit project path; relative assets retain scope"}, "/api/reports/{id}/analysis": {"get": "Deterministic Markdown analysis"}, "/api/reports/{id}/summary": {"get": "Exact JTL summary JSON"}, "/api/reports/{id}/download": {"get": "ZIP report version"}, "/api/compare": {"get": "Retired: 410; original artifacts only"}, "/api/openapi.json": {"get": "API discovery"}}
	for path, methods := range map[string]map[string]string{
		"/api/workflows/{id}/datasets":          {"get": "CSV nodes and immutable files", "post": "Stream multipart node_id,name?,file; UTF8 CSV <=configured 256MiB default"},
		"/api/workflows/{id}/datasets/generate": {"post": "Generate {node_id,name?,rows?,columns,seed?}; max100000 rows"},
		"/api/plans/{id}/datasets":              {"get": "Frozen CSV node requirements and compatible files"},
		"/api/datasets":                         {"get": "当前项目CSV数据文件元数据分页列表，包含失效来源文件；{items,total,page,page_size}"},
		"/api/datasets/catalog":                 {"get": "当前项目有效流程与CSV节点目录；{workflows:[{id,name,nodes}]}，仅返回元数据"},
		"/api/datasets/{id}":                    {"get": "Project-owned immutable file metadata", "delete": "Soft-delete file from future selections"},
		"/api/datasets/{id}/preview":            {"get": "Bounded CSV preview; limit1..100"},
		"/api/datasets/{id}/content":            {"get": "Authenticated streaming immutable CSV"},
		"/api/nodes/report":                     {"post": "HMAC signed Core self and worker inventory report; no manager node probing"},
		"/api/runner/claim":                     {"post": "HMAC signed task/attempt/controller + Redis lease claim; no duplicate execution"},
		"/api/runner/heartbeat":                 {"post": "HMAC monotonic state and safe callback delivery progress"},
		"/api/runner/callback":                  {"post": "HMAC immutable completion event; verify MinIO receipts then commit, idempotent event_id"},
	} {
		descriptions[path] = methods
	}
	for path, methods := range map[string]map[string]string{
		"/api/workflow-extensions":        {"get": "读取原生、平台与项目画布扩展目录，含来源与独立revision", "post": "按revision创建项目JavaScript画布适配器"},
		"/api/workflow-extensions/usage":  {"get": "统计项目已保存画布及保留子流程中的组件来源和引用"},
		"/api/workflow-extensions/expand": {"post": "显式展开旧画布中保留的原生组件子流程，不保存或改写历史"},
		"/api/workflow-extensions/{id}":   {"get": "读取加密保存的项目画布适配定义", "put": "按revision更新定义或installed", "delete": "按revision删除适配器，保留流程XML与历史"},
		"/api/interface-protocols":        {"get": "读取协议与序列化能力；HTTP/WebSocket已支持，RPC/gRPC/Protobuf仅保留描述扩展"},
		"/api/interface-plugins":          {"get": "读取本项目内置与自定义展示插件元数据、安装状态和独立revision；不下发代码正文", "post": "按{revision,definition,installed}创建项目自定义配置或JavaScript解析插件；加密保存，服务端不执行代码"}, "/api/interface-plugins/{id}": {"put": "按{revision,installed:boolean}更新安装状态；归档只读、冲突409", "delete": "按{revision}删除自定义插件并移除安装状态；内置插件不可删除，不改写集合历史"},
		"/api/interface-plugins/{id}/definition": {"get": "读取项目自定义插件完整定义和当前插件revision", "put": "按{revision,definition}更新自定义插件定义；内置插件不可编辑"},
		"/api/environment-runtime":               {"get": "读取本次操作调用方来源IP与毫秒时间戳{client_ip,timestamp_ms}；须认证且项目存在；未知IP为空"},
		"/api/environments":                      {"get": "项目环境列表", "post": "创建加密项目环境{name,base_host,variables:[{name,value}]}"}, "/api/environments/{id}": {"get": "读取项目环境", "put": "按revision更新项目环境", "delete": "软删除项目环境并保留任务快照"},
		"/api/projects": {"get": "项目列表", "post": "创建项目"}, "/api/projects/{id}": {"get": "读取项目", "put": "按revision更新项目"}, "/api/projects/{id}/archive": {"post": "按revision归档项目并保留历史"},
		"/api/api-collections/import": {"post": "导入cURL、Postman、Apifox或OpenAPI"}, "/api/api-collections/{id}/export": {"get": "导出OpenAPI或Postman"}, "/api/api-collections/{id}/requests": {"put": "按revision新增或更新接口"}, "/api/api-collections/{id}/debug": {"post": "显式target_source=project_environment/document_server；项目环境须environment_id；旧environment不执行；有界安全HTTP调试"}, "/api/api-collections/{id}/environments": {"put": "按revision保存环境"}, "/api/api-collections/{id}/settings": {"put": "按revision保存集合设置"},
	} {
		descriptions[path] = methods
	}
	for _, resource := range []string{"runs", "pressure-reports"} {
		single := "/api/" + resource + "/{id}"
		if descriptions[single] == nil {
			descriptions[single] = map[string]string{}
		}
		descriptions[single]["delete"] = "删除当前项目单条历史；任务仅允许终态；返回deleted_count,skipped_count,cleanup_pending；Cookie须CSRF，归档项目只读"
		descriptions["/api/"+resource+"/delete"] = map[string]string{"post": "原子删除当前项目所选历史{ids:[...]}；1至100个不同ID；跨项目、无效ID或非终态任务整批拒绝"}
		descriptions["/api/"+resource+"/clear"] = map[string]string{"post": "清空当前项目全部历史{confirm:true}，忽略筛选分页；任务跳过活动记录，报告可删除全部状态"}
	}
	paths := Obj{}
	parameterRE := regexp.MustCompile(`\{([^}]+)\}`)
	for path, methods := range descriptions {
		m := Obj{}
		for method, description := range methods {
			entry := Obj{"summary": description, "responses": Obj{"200": Obj{"description": "Success"}, "400": Obj{"description": "JSON {error:{code,message}}"}, "401": Obj{"description": "Authentication required"}, "409": Obj{"description": "Conflict"}}}
			parameters := []any{Obj{"name": "X-Project-ID", "in": "header", "required": !globalProjectPath(path), "schema": Obj{"type": "string"}}}
			if path == "/api/pressure-reports/{id}/monitoring" {
				if method == "get" {
					parameters = append(parameters, Obj{"name": "export", "in": "query", "description": "仅值1启用显式全量导出，不与分页筛选混用", "schema": Obj{"type": "string", "enum": []string{"1"}}})
					parameters = append(parameters, Obj{"name": "revision", "in": "query", "description": "省略读取最新监控补充版本", "schema": Obj{"type": "integer", "minimum": 1, "maximum": 128}})
					parameters = append(parameters,
						Obj{"name": "component", "in": "query", "schema": Obj{"type": "string", "enum": []string{"es", "mysql", "redis", "kafka", "jvm", "host", "k8s"}}},
						Obj{"name": "search", "in": "query", "description": "实例名称包含匹配，最多200字节", "schema": Obj{"type": "string", "maxLength": 200}},
						Obj{"name": "limit", "in": "query", "schema": Obj{"type": "integer", "minimum": 1, "maximum": 200, "default": 20}},
						Obj{"name": "offset", "in": "query", "schema": Obj{"type": "integer", "minimum": 0, "maximum": 1000000, "default": 0}})
				} else if method == "post" {
					parameters = append(parameters, Obj{"name": "Idempotency-Key", "in": "header", "required": true, "schema": Obj{"type": "string", "minLength": 1, "maxLength": 200}})
				}
			}
			if path == "/api/pressure-reports/{id}/monitoring/jvm-batch" && method == "post" {
				parameters = append(parameters, Obj{"name": "Idempotency-Key", "in": "header", "required": true, "schema": Obj{"type": "string", "minLength": 1, "maxLength": 200}})
			}
			if path == "/api/datasets" && method == "get" {
				parameters = append(parameters,
					Obj{"name": "page", "in": "query", "schema": Obj{"type": "integer", "minimum": 1, "default": 1}},
					Obj{"name": "page_size", "in": "query", "schema": Obj{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}},
					Obj{"name": "q", "in": "query", "description": "文件名文字包含匹配", "schema": Obj{"type": "string"}},
					Obj{"name": "workflow_id", "in": "query", "description": "当前项目流程ID，允许已删除流程", "schema": Obj{"type": "string"}},
					Obj{"name": "node_id", "in": "query", "description": "稳定CSV节点ID，须同时提供workflow_id", "schema": Obj{"type": "string"}})
			}
			if nativeProjectDownloadPath(path) {
				parameters = append(parameters, Obj{"name": "project_id", "in": "query", "required": false, "description": "只读文件下载的项目上下文；不得与X-Project-ID冲突", "schema": Obj{"type": "string"}})
			}
			for _, match := range parameterRE.FindAllStringSubmatch(path, -1) {
				parameters = append(parameters, Obj{"name": match[1], "in": "path", "required": true, "schema": Obj{"type": "string"}})
			}
			if method == "post" && (path == "/api/runs" || path == "/api/runs/{id}/reports") {
				parameters = append(parameters, Obj{"name": "Idempotency-Key", "in": "header", "required": true, "schema": Obj{"type": "string"}})
			}

			if path == "/api/api-proxy" {
				entry["security"] = []any{Obj{"sessionAuth": []any{}}}
				parameters = append(parameters, Obj{"name": "scalar_url", "in": "query", "required": true, "schema": Obj{"type": "string", "format": "uri"}}, Obj{"name": "X-JMeter-CSRF-Token", "in": "header", "required": true, "schema": Obj{"type": "string"}})
			}
			if len(parameters) > 0 {
				entry["parameters"] = parameters
			}
			if method == "post" || method == "put" || method == "patch" {
				entry["requestBody"] = Obj{"content": Obj{"application/json": Obj{"schema": Obj{"type": "object"}}}}
			}
			if strings.HasPrefix(path, "/api/runner/") || strings.HasPrefix(path, "/api/nodes/") {
				entry["security"] = []any{}
				for _, name := range []string{"X-JMeter-Runner", "X-JMeter-Timestamp", "X-JMeter-Signature"} {
					parameters = append(parameters, Obj{"name": name, "in": "header", "required": true, "schema": Obj{"type": "string"}})
				}
				entry["parameters"] = parameters
			}
			if path == "/api/workflows/{id}/datasets" && method == "post" {
				entry["requestBody"] = Obj{"required": true, "content": Obj{"multipart/form-data": Obj{"schema": Obj{"type": "object", "required": []string{"node_id", "file"}, "properties": Obj{"node_id": Obj{"type": "string"}, "name": Obj{"type": "string"}, "file": Obj{"type": "string", "format": "binary"}}}}}}
			}
			if historyDeletePath(path) && (method == "delete" || strings.HasSuffix(path, "/delete") || strings.HasSuffix(path, "/clear")) {
				properties := Obj{}
				for _, field := range []string{"deleted_count", "skipped_count", "cleanup_pending"} {
					properties[field] = Obj{"type": "integer", "minimum": 0}
				}
				obj(entry["responses"])["200"] = Obj{"description": "Deletion committed; cleanup_pending counts durable physical cleanup intents", "content": Obj{"application/json": Obj{"schema": Obj{"type": "object", "required": []string{"deleted_count", "skipped_count", "cleanup_pending"}, "properties": properties}}}}
				if method == "post" {
					schema := Obj{"type": "object", "additionalProperties": false}
					if strings.HasSuffix(path, "/clear") {
						schema["required"] = []string{"confirm"}
						schema["properties"] = Obj{"confirm": Obj{"type": "boolean", "enum": []bool{true}}}
					} else {
						schema["required"] = []string{"ids"}
						schema["properties"] = Obj{"ids": Obj{"type": "array", "minItems": 1, "maxItems": 100, "uniqueItems": true, "items": Obj{"type": "string"}}}
					}
					entry["requestBody"] = Obj{"required": true, "content": Obj{"application/json": Obj{"schema": schema}}}
				}
			}
			m[method] = entry
		}
		paths[path] = m
	}
	return Obj{"openapi": "3.0.3", "info": Obj{"title": "JMeter Control Plane", "version": "2.0.0", "description": "Bearer token or HttpOnly session; session writes require X-CSRF-Token. Times UTC; latency ms; error_rate 0..1."}, "components": Obj{"securitySchemes": Obj{"bearerAuth": Obj{"type": "http", "scheme": "bearer"}, "sessionAuth": Obj{"type": "apiKey", "in": "cookie", "name": "jmeter_session"}}}, "security": []any{Obj{"bearerAuth": []any{}}, Obj{"sessionAuth": []any{}}}, "paths": paths}
}
