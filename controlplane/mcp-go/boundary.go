package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Workflow APIs default to 32MiB and can be configured up to 64MiB. Reserve
// envelope space only for tools that carry a workflow; other calls retain 6MiB.
const maxMCPBody = 65 * 1024 * 1024
const standardMCPBody = 6 * 1024 * 1024
const maxAPIResponse = 16 * 1024 * 1024
const maxWorkflowAPIResponse = 80 * 1024 * 1024

var draftPattern = regexp.MustCompile(`^plan_[a-f0-9]{8}$`)

// An allowlist limits the upstream process to authoring, even if it adds tools later.
var authoringNames = strings.Fields(`create_test_plan list_test_plans get_test_plan add_thread_group
add_http_sampler add_json_extractor add_header_manager add_response_assertion add_csv_data_set
add_user_defined_variables add_constant_timer add_regex_extractor add_transaction_controller
add_loop_controller add_if_controller add_jdbc_connection_configuration add_jdbc_request
add_jsr223_sampler add_ftp_request add_tcp_sampler add_http_request_defaults add_cookie_manager
add_while_controller add_random_controller add_interleave_controller add_once_only_controller
add_setup_thread_group add_teardown_thread_group add_xpath_extractor add_jsr223_preprocessor
add_jsr223_postprocessor add_user_parameters add_json_assertion add_duration_assertion
add_size_assertion add_uniform_random_timer add_constant_throughput_timer remove_element
update_element rename_element move_element reorder_children set_element_enabled get_test_plan_xml import_test_plan`)

func allowedAuthoring(name string) bool {
	for _, candidate := range authoringNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func validateAuthoring(name string, args map[string]any, roots []string) error {
	if value, ok := args["planId"]; ok {
		id, valid := value.(string)
		if !valid || !draftPattern.MatchString(id) {
			return errors.New("Invalid draft plan ID")
		}
	}
	if name != "import_test_plan" {
		return nil
	}
	path, ok := args["filePath"].(string)
	if !ok || !filepath.IsAbs(path) {
		return errors.New("Import path must be an absolute container path")
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return errors.New("Import file is unavailable")
	}
	info, err := os.Stat(actual)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 5*1024*1024 {
		return errors.New("Import must be a regular JMX file of at most 5 MiB")
	}
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(resolved, actual)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			args["filePath"] = actual
			return nil
		}
	}
	return errors.New("Import path must be inside /plans or the MCP workspace")
}

func secureHandler(token string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ok":true}`)
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"Authentication required"}`)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		r = withCallerSource(r)
		if r.ContentLength >= 0 && r.ContentLength <= standardMCPBody {
			r.Body = http.MaxBytesReader(w, r.Body, standardMCPBody)
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxMCPBody {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		original := r.Body
		raw, e := io.ReadAll(http.MaxBytesReader(w, original, maxMCPBody))
		_ = original.Close()
		if e != nil || (len(raw) > standardMCPBody && !workflowMCPEnvelope(raw)) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		next.ServeHTTP(w, r)
	})
}

func workflowMCPEnvelope(raw []byte) bool {
	var envelope struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Method != "tools/call" {
		return false
	}
	switch envelope.Params.Name {
	case "create_workflow", "update_workflow", "import_workflow", "preview_workflow", "download_dataset_template":
		return true
	}
	return false
}

type managerClient struct {
	baseURL, tokenFile, publicURL string
	client                        *http.Client
}

// Arbitrary OpenAPI examples and extension fields may contain integers beyond
// float64 precision. Keep JSON numbers intact through both forwarding paths.
func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("Invalid trailing JSON data")
	}
	return nil
}

func (c managerClient) requestProject(ctx context.Context, method, path string, body any, key, project string) (map[string]any, error) {
	raw, err := c.requestRaw(ctx, method, path, body, key, project)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if decodeJSON(raw, &result) != nil || result == nil {
		return nil, errors.New("Manager API returned invalid JSON")
	}
	return result, nil
}

func (c managerClient) requestRaw(ctx context.Context, method, path string, body any, key, project string) ([]byte, error) {
	token, err := os.ReadFile(c.tokenFile)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return nil, errors.New("Manager API credential is unavailable")
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("Invalid manager API payload")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("Invalid manager API configuration")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	if project != "" {
		req.Header.Set("X-Project-ID", project)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if err := signManagerCaller(req, strings.TrimSpace(string(token))); err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 20 * time.Second}
	if c.client != nil {
		client = *c.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Manager API is unavailable or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Manager API HTTP %d", response.StatusCode)
	}
	limit := maxAPIResponse
	if route, e := url.Parse(path); e == nil {
		parts := strings.Split(strings.Trim(route.Path, "/"), "/")
		if route.Path == "/api/workflows" || strings.HasPrefix(route.Path, "/api/workflows/") || (len(parts) == 4 && parts[0] == "api" && parts[1] == "plans" && parts[3] == "content") {
			limit = maxWorkflowAPIResponse
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return nil, errors.New("Manager API response is unavailable or too large")
	}
	return raw, nil
}
