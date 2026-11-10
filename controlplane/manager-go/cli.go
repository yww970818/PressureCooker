package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func cliArgs(args []string) ([]string, map[string][]string, error) {
	positionals := []string{}
	options := map[string][]string{}
	for i := 0; i < len(args); i++ {
		s := args[i]
		if !strings.HasPrefix(s, "--") {
			positionals = append(positionals, s)
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(s, "--"), "=", 2)
		key := parts[0]
		value := ""
		if len(parts) == 2 {
			value = parts[1]
		} else {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("option --%s requires a value", key)
			}
			i++
			value = args[i]
		}
		options[key] = append(options[key], value)
	}
	return positionals, options, nil
}
func runCLI(args []string) error {
	if len(args) > 0 && args[0] == "api" {
		return runCLI(append(args[1:], "--api-mode", "1"))
	}
	if len(args) == 1 && args[0] == "health" {
		return healthCLI()
	}
	if len(args) == 1 && args[0] == "init-minio" {
		c, e := loadConfig()
		if e != nil {
			return e
		}
		if c.ArchiveEndpoint == "" {
			return fmt.Errorf("archive endpoint required")
		}
		a := &S3Archive{c, newArchiveHTTPClient(c.ArchiveTimeout)}
		status, _, e := a.request("PUT", "", bytes.NewReader(nil), 0, digest(nil), "application/octet-stream")
		if e != nil {
			return e
		}
		if status != 200 && status != 201 && status != 204 && status != 409 {
			return fmt.Errorf("bucket initialization failed: HTTP %d", status)
		}
		fmt.Println("Private MinIO bucket ready: " + c.ArchiveBucket)
		return nil
	}

	pos, opts, e := cliArgs(args)
	if e != nil {
		return e
	}
	if len(pos) == 0 {
		return fmt.Errorf("command required")
	}
	command := pos[0]
	pos = pos[1:]
	option := func(key, def string) string {
		v := opts[key]
		if len(v) > 0 {
			return v[len(v)-1]
		}
		return def
	}
	need := func(count int) error {
		if len(pos) != count {
			return fmt.Errorf("%s requires %d positional arguments", command, count)
		}
		return nil
	}
	var result any
	switch command {
	case "convert-script-identities":
		if e = need(2); e != nil {
			return e
		}
		content, e := readLimitedFile(pos[0], 5<<20)
		if e != nil {
			return e
		}
		converted, e := convertScriptIdentitySource(string(content))
		if e != nil {
			return e
		}
		f, e := os.OpenFile(pos[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString(converted)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		result = Obj{"source_sha256": digest(content), "output_sha256": digest([]byte(converted)), "output": pos[1], "next": "核对新源码及属性命名后，显式上传为新脚本版本"}
	case "restore":
		if e = need(2); e != nil {
			return e
		}
		path, e := restoreArchive(pos[0], pos[1])
		if e != nil {
			return e
		}
		result = Obj{"restored": path, "next": "Configure a separate empty MySQL database and run import-state " + filepath.Join(path, "state", "manager.json")}
	case "convert-monitoring", "retired-executions", "retire-execution", "backfill-run-environments", "rebuild-read-models", "db-check", "import-state", "export-state", "backup", "import-history":
		count := 1
		if command == "convert-monitoring" || command == "retired-executions" || command == "db-check" || command == "rebuild-read-models" || command == "backfill-run-environments" {
			count = 0
		}
		if e = need(count); e != nil {
			return e
		}
		project := ""
		if command == "import-history" {
			project = option("project", "")
			if validID(project) != nil {
				return fmt.Errorf("import-history requires an explicit valid --project")
			}
		}
		c, e := loadConfig()
		if e != nil {
			return e
		}
		if command == "db-check" {
			db, e := openMySQL(c)
			if e != nil {
				return e
			}
			defer db.Close()
			var user, version, database string
			if e = db.QueryRow("SELECT CURRENT_USER(),VERSION(),DATABASE()").Scan(&user, &version, &database); e != nil {
				return fmt.Errorf("MySQL check failed: %T", e)
			}
			result = Obj{"ok": true, "user": user, "version": version, "database": database}
			break
		}
		p, e := newPlatform(c)
		if e != nil {
			return e
		}
		defer p.Close()
		switch command {
		case "convert-monitoring":
			result, e = p.convertMonitoring()
		case "retired-executions":
			result, e = p.retiredExecutions()
		case "retire-execution":
			result, e = p.retireExecution(context.Background(), pos[0], option("fingerprint", ""))
		case "backfill-run-environments":
			result, e = p.rebuildRunEnvironmentSummaries(50)
			if e != nil {
				return e
			}
		case "rebuild-read-models":
			result, e = p.rebuildPageReadModels(16)
			if e != nil {
				return e
			}
		case "import-state":
			state, e := loadJSONFile(pos[0])
			if e != nil {
				return e
			}
			if e = p.importState(state); e != nil {
				return e
			}
			result = Obj{"imported": true, "tables": portableTables}
		case "export-state":
			state, e := p.exportState()
			if e != nil {
				return e
			}
			if e = atomicWrite(pos[0], encoded(state)); e != nil {
				return e
			}
			result = Obj{"exported": pos[0]}
		case "backup":
			path, e := p.backup(pos[0])
			if e != nil {
				return e
			}
			result = Obj{"backup": path}
		case "import-history":
			result, e = p.forProject(project).importHistory(pos[0])
			if e != nil {
				return e
			}
		}
		if e != nil {
			return e
		}
	default:
		if strings.HasPrefix(command, "dataset-") {
			return datasetCLI(command, pos, option)
		}
		endpoint := strings.TrimRight(option("url", env("JMETER_API_URL", "http://127.0.0.1:"+env("JMETER_MANAGER_PORT", "8080"))), "/")
		path := ""
		var body Obj
		key := ""
		switch command {
		case "health":
			path = "/healthz"
		case "cluster":
			path = "/api/cluster"
		case "plans":
			path = "/api/plans"
		case "runs":
			path = "/api/runs"
		case "openapi":
			path = "/api/openapi.json"
		case "run", "report-status", "artifacts":
			if e = need(1); e != nil {
				return e
			}
			id := url.PathEscape(pos[0])
			path = "/api/runs/" + id
			if command == "artifacts" {
				path += "/artifacts"
			}
			if command == "report-status" {
				path = "/api/reports/" + id
			}
		case "upload":
			if e = need(1); e != nil {
				return e
			}
			sourcePath := pos[0]
			if strings.HasPrefix(sourcePath, "plans/") {
				sourcePath = "/" + sourcePath
			}
			b, e := os.ReadFile(sourcePath)
			if e != nil {
				return e
			}
			name := strings.TrimSuffix(filepath.Base(pos[0]), filepath.Ext(pos[0]))
			body = Obj{"name": option("name", name), "content": string(b), "description": option("description", "")}
			path = "/api/plans"
		case "start":
			if e = need(1); e != nil {
				return e
			}
			properties := Obj{}
			for _, property := range opts["property"] {
				parts := strings.SplitN(property, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("--property requires key=value")
				}
				properties[parts[0]] = parts[1]
			}
			name, keyOption := option("name", ""), option("key", "")
			if name == "" || keyOption == "" || option("cluster", "") == "" || option("environment", "") == "" {
				return fmt.Errorf("start requires --name, --key, --cluster and --environment")
			}
			key = keyOption
			body = Obj{"cluster_id": option("cluster", ""), "name": name, "plan_id": pos[0], "properties": properties, "notes": option("notes", "")}
			if selected := option("workers", ""); selected != "" {
				workers := []any{}
				for _, w := range strings.Split(selected, ",") {
					workers = append(workers, w)
				}
				body["workers"] = workers
			}
			if environmentID := option("environment", ""); environmentID != "" {
				body["environment_id"] = environmentID
			}
			if value := option("datasets-file", ""); value != "" {
				raw, e := readLimitedFile(value, workspaceLimit)
				if e != nil {
					return e
				}
				var selections []any
				if e = json.Unmarshal(raw, &selections); e != nil {
					return fmt.Errorf("--datasets-file must contain dataset selections array")
				}
				body["dataset_selections"] = selections
			}
			if value := option("timeout", ""); value != "" {
				n, e := strconv.Atoi(value)
				if e != nil || n < 10 || n > 86400 {
					return fmt.Errorf("--timeout must be10..86400")
				}
				body["timeout_seconds"] = n
			}
			path = "/api/runs"
		case "stop", "report":
			if e = need(1); e != nil {
				return e
			}
			path = "/api/runs/" + url.PathEscape(pos[0])
			if command == "stop" {
				path += "/stop"
				body = Obj{"mode": option("mode", "graceful")}
			} else {
				path += "/reports"
				body = Obj{}
				key = option("key", "")
			}
		case "compare":
			if len(pos) < 1 {
				return fmt.Errorf("compare requires run IDs")
			}
			path = "/api/compare?" + url.Values{"runs": {strings.Join(pos, ",")}, "baseline": {option("baseline", pos[0])}}.Encode()
		default:
			return fmt.Errorf("unknown command")
		}
		var source io.Reader
		method := "GET"
		if body != nil {
			method = "POST"
			source = bytes.NewReader(encoded(body))
		}
		request, e := http.NewRequest(method, endpoint+path, source)
		if e != nil {
			return e
		}
		request.Header.Set("Content-Type", "application/json")
		projectID := option("project", "")
		if projectID == "" && command != "health" && !globalProjectPath(strings.SplitN(path, "?", 2)[0]) {
			return fmt.Errorf("%s requires --project PROJECT_ID", command)
		}
		if projectID != "" {
			if e := validID(projectID); e != nil {
				return e
			}
			request.Header.Set("X-Project-ID", projectID)
		}
		if command != "health" {
			b, e := os.ReadFile(option("token-file", env("JMETER_ADMIN_TOKEN_FILE", "/run/platform/admin-token")))
			if e != nil {
				return fmt.Errorf("token file unavailable")
			}
			request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(b)))
		}
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response, e := (&http.Client{Timeout: 60 * time.Second}).Do(request)
		if e != nil {
			return fmt.Errorf("API connection failed")
		}
		defer response.Body.Close()
		b, e := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
		if e != nil {
			return e
		}
		if response.StatusCode >= 400 {
			if json.Valid(b) {
				os.Stderr.Write(b)
				os.Stderr.WriteString("\n")
			}
			return fmt.Errorf("HTTP %d", response.StatusCode)
		}
		if e = json.Unmarshal(b, &result); e != nil {
			return e
		}
	}
	b, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		return e
	}
	_, e = os.Stdout.Write(append(b, '\n'))
	return e
}

func healthCLI() error {
	targets := []string{"http://127.0.0.1:" + env("JMETER_MANAGER_PORT", "8080") + "/healthz"}
	if env("JMETER_MCP_ENABLED", "0") == "1" {
		targets = append(targets, "http://127.0.0.1:"+env("JMETER_MCP_PORT", "8081")+"/healthz")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, target := range targets {
		response, e := client.Get(target)
		if e != nil {
			return fmt.Errorf("local health endpoint unreachable")
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 65536))
		response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("local health endpoint HTTP %d", response.StatusCode)
		}
	}
	fmt.Println(`{"ok":true}`)
	return nil
}
