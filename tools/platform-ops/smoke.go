package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type smokeAPI struct {
	base    string
	project string
	token   string
	client  *http.Client
}

func (api smokeAPI) request(ctx context.Context, path string, payload any, key string) (*http.Response, error) {
	parsed, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || parsed.IsAbs() || parsed.Host != "" {
		return nil, errors.New("API/download URL must be a path on the selected platform")
	}
	var body io.Reader
	method := http.MethodGet
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body, method = bytes.NewReader(raw), http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, api.base+path, body)
	if err != nil {
		return nil, errors.New("invalid platform request URL")
	}
	request.Header.Set("Authorization", "Bearer "+api.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Project-ID", api.project)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := api.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("platform request failed: %s %s", method, parsed.EscapedPath())
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("platform returned HTTP %d: %s %s", response.StatusCode, method, parsed.EscapedPath())
	}
	return response, nil
}

func (api smokeAPI) call(ctx context.Context, path string, payload any, key string, target any) error {
	response, err := api.request(ctx, path, payload, key)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decodeJSON(response.Body, target)
}

type runView struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"`
	RawArchive map[string]any `json:"raw_archive"`
	Reports    any            `json:"reports"`
	Summary    any            `json:"summary"`
}

var smokeID = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)

func hasContent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case map[string]any:
		return len(v) != 0
	case []any:
		return len(v) != 0
	case string:
		return v != ""
	case bool:
		return v
	case json.Number:
		return v != "0"
	default:
		return true
	}
}

func platformSmoke(ctx context.Context, args []string, out io.Writer) error {
	options := flag.NewFlagSet("platform-smoke", flag.ContinueOnError)
	options.SetOutput(out)
	base := options.String("url", "http://127.0.0.1:18090", "platform API base URL")
	tokenFile := options.String("token-file", "", "private bearer token file (required)")
	planFile := options.String("plan", "", "internal smoke-target JMX file (required)")
	project := options.String("project", "", "project ID (required)")
	cluster := options.String("cluster", "", "registered cluster ID (required)")
	environment := options.String("environment", "", "project environment ID (required)")
	if err := options.Parse(args); err != nil {
		return usageError{err}
	}
	if options.NArg() != 0 || *tokenFile == "" || *planFile == "" || *cluster == "" || *environment == "" || !smokeID.MatchString(*project) {
		return usageError{errors.New("platform-smoke requires --token-file, --plan, a valid --project, --cluster and --environment")}
	}
	parsed, err := url.Parse(*base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return usageError{errors.New("--url must be an HTTP(S) platform URL without credentials, query or fragment")}
	}
	token, err := os.ReadFile(*tokenFile)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(token)) == "" {
		return errors.New("token file is empty")
	}
	content, err := os.ReadFile(*planFile)
	if err != nil {
		return err
	}
	if !bytes.Contains(content, []byte(`<stringProp name="HTTPSampler.domain">smoke-target</stringProp>`)) {
		return errors.New("self-test plan must use the Docker internal smoke-target")
	}
	api := smokeAPI{base: strings.TrimRight(*base, "/"), project: *project, token: strings.TrimSpace(string(token)), client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	var clusters struct {
		Clusters []struct {
			ID         string   `json:"id"`
			Registered bool     `json:"registered"`
			Workers    []string `json:"worker_ids"`
		} `json:"clusters"`
	}
	if err := api.call(ctx, "/api/cluster", nil, "", &clusters); err != nil {
		return err
	}
	var workers []string
	for _, candidate := range clusters.Clusters {
		if candidate.ID != *cluster || !candidate.Registered {
			continue
		}
		if len(candidate.Workers) < 1 || len(candidate.Workers) > 64 {
			break
		}
		seen := map[string]bool{}
		for _, worker := range candidate.Workers {
			if !smokeID.MatchString(worker) || seen[worker] {
				return errors.New("selected cluster contains invalid or duplicate workers; no task was submitted")
			}
			seen[worker] = true
		}
		workers = candidate.Workers
		break
	}
	if len(workers) == 0 {
		return errors.New("self-test requires a registered cluster with 1 to 64 workers; no task was submitted")
	}
	var plan struct {
		ID string `json:"id"`
	}
	if err := api.call(ctx, "/api/plans", map[string]any{"name": "集群原始文件自测", "content": string(content), "description": "只请求Docker内部smoke-target"}, "", &plan); err != nil {
		return err
	}
	if plan.ID == "" {
		return errors.New("platform did not return a plan ID")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	key := hex.EncodeToString(random)
	var run runView
	if err := api.call(ctx, "/api/runs", map[string]any{"name": "集群原始文件自测", "plan_id": plan.ID, "cluster_id": *cluster, "environment_id": *environment, "workers": workers, "properties": map[string]string{"threads": "1", "loops": "5", "delay_ms": "20"}}, key, &run); err != nil {
		return err
	}
	if run.ID == "" {
		return errors.New("platform did not return a run ID")
	}
	runID := run.ID
	waitContext, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	for {
		run = runView{}
		if err := api.call(waitContext, "/api/runs/"+url.PathEscape(runID), nil, "", &run); err != nil {
			return err
		}
		switch run.Status {
		case "FAILED", "STOPPED", "INTERRUPTED":
			return fmt.Errorf("self-test run failed: %s", run.Status)
		}
		archiveStatus, _ := run.RawArchive["status"].(string)
		if run.Status == "COMPLETED" && (archiveStatus == "ARCHIVED" || archiveStatus == "DISABLED") {
			if err := validateArtifacts(waitContext, api, runID, len(workers)); err != nil {
				return err
			}
			if hasContent(run.Reports) || hasContent(run.Summary) {
				return errors.New("new self-test must not generate reports or summaries")
			}
			encoder := json.NewEncoder(out)
			encoder.SetIndent("", "  ")
			return encoder.Encode(map[string]any{"run_id": runID, "acceptance_samples": 5 * len(workers), "acceptance_errors": 0, "acceptance_engines": len(workers), "archive": run.RawArchive})
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-waitContext.Done():
			timer.Stop()
			return errors.New("self-test timed out or cancelled; inspect task through API")
		case <-timer.C:
		}
	}
}

func validateArtifacts(ctx context.Context, api smokeAPI, runID string, workerCount int) error {
	var artifacts struct {
		Items []struct {
			Name        string `json:"name"`
			Size        int64  `json:"size"`
			SHA256      string `json:"sha256"`
			DownloadURL string `json:"download_url"`
		} `json:"items"`
	}
	if err := api.call(ctx, "/api/runs/"+url.PathEscape(runID)+"/artifacts", nil, "", &artifacts); err != nil {
		return err
	}
	wanted := map[string]bool{"results.jtl": true, "jmeter.log": true, "console.log": true}
	if len(artifacts.Items) != 3 {
		return errors.New("self-test must archive exactly results.jtl, jmeter.log and console.log")
	}
	for _, artifact := range artifacts.Items {
		if !wanted[artifact.Name] {
			return errors.New("self-test artifact is unexpected or duplicated")
		}
		delete(wanted, artifact.Name)
		response, err := api.request(ctx, artifact.DownloadURL, nil, "")
		if err != nil {
			return err
		}
		digest := sha256.New()
		var raw bytes.Buffer
		var sink io.Writer = digest
		if artifact.Name == "results.jtl" {
			sink = io.MultiWriter(digest, &raw)
		}
		size, copyErr := io.Copy(sink, response.Body)
		response.Body.Close()
		if copyErr != nil {
			return fmt.Errorf("read self-test artifact %s failed", artifact.Name)
		}
		if size != artifact.Size || hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
			return fmt.Errorf("self-test artifact size/SHA256 mismatch: %s", artifact.Name)
		}
		if artifact.Name == "results.jtl" {
			rows, err := readJTL(&raw)
			if err != nil {
				return err
			}
			if len(rows) != 5*workerCount {
				return fmt.Errorf("self-test requires %d JTL samples", 5*workerCount)
			}
			engines := map[string]int{}
			for _, row := range rows {
				if !strings.EqualFold(row["success"], "true") {
					return errors.New("self-test contains a failed sample")
				}
				if strings.TrimSpace(row["hostname"]) == "" {
					return errors.New("self-test sample has no engine hostname")
				}
				engines[row["hostname"]]++
			}
			if len(engines) != workerCount {
				return fmt.Errorf("self-test must include %d distinct engines", workerCount)
			}
			for _, count := range engines {
				if count != 5 {
					return errors.New("self-test requires five samples from each engine")
				}
			}
		}
	}
	return nil
}
