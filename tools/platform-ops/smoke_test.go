package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPlatformSmokeValidatesClusterAndRawArtifacts(t *testing.T) {
	for _, scenario := range []string{"ARCHIVED", "DISABLED", "pending-archive", "unregistered", "two-workers", "four-workers", "seven-workers", "duplicate-worker", "empty-workers", "invalid-worker", "too-many-workers", "failed-run", "wrong-size", "wrong-hash", "extra-artifact", "sample-count", "failed-sample", "missing-engine", "uneven-engine", "empty-engine", "summary", "reports", "external-download", "http-error"} {
		t.Run(scenario, func(t *testing.T) {
			workers := []string{"worker-1", "worker-2", "worker-3", "worker-4", "worker-5", "worker-6"}
			switch scenario {
			case "two-workers":
				workers = []string{"load-east", "load-west"}
			case "four-workers":
				workers = workers[:4]
			case "seven-workers":
				workers = append(workers, "worker-7")
			case "duplicate-worker":
				workers[5] = "worker-1"
			case "empty-workers":
				workers = nil
			case "invalid-worker":
				workers[5] = "../worker"
			case "too-many-workers":
				workers = nil
				for i := 0; i < 65; i++ {
					workers = append(workers, fmt.Sprintf("worker-%d", i))
				}
			}
			root := t.TempDir()
			tokenFile := filepath.Join(root, "token")
			planFile := filepath.Join(root, "plan.jmx")
			writeFixture(t, tokenFile, "fixture-private-token\n")
			writeFixture(t, planFile, `<stringProp name="HTTPSampler.domain">smoke-target</stringProp>`)
			bodies := map[string]string{"results.jtl": smokeCSV(5, len(workers)), "jmeter.log": "jmeter-fixture", "console.log": "console-fixture"}
			switch scenario {
			case "sample-count":
				bodies["results.jtl"] = smokeCSV(4, 6)
			case "failed-sample":
				bodies["results.jtl"] = strings.Replace(bodies["results.jtl"], ",true,", ",false,", 1)
			case "uneven-engine":
				bodies["results.jtl"] = strings.Replace(bodies["results.jtl"], "worker-1", "worker-2", 1)
			case "empty-engine":
				bodies["results.jtl"] = strings.ReplaceAll(bodies["results.jtl"], "worker-6", "")
			case "missing-engine":
				bodies["results.jtl"] = strings.ReplaceAll(bodies["results.jtl"], "worker-6", "worker-5")
			}
			var plans, runs, polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("X-Project-ID") != "selected-project" {
					t.Error("project scope missing from request", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				if r.Header.Get("Authorization") != "Bearer fixture-private-token" {
					w.WriteHeader(401)
					return
				}
				switch {
				case r.URL.Path == "/api/cluster":
					json.NewEncoder(w).Encode(map[string]any{"clusters": []any{map[string]any{"id": "selected-cluster", "registered": scenario != "unregistered", "worker_ids": workers}}})
				case r.URL.Path == "/api/plans" && r.Method == "POST":
					plans.Add(1)
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if !strings.Contains(payload["content"].(string), "smoke-target") {
						t.Error("uploaded plan changed target")
					}
					io.WriteString(w, `{"id":"fixture-plan"}`)
				case r.URL.Path == "/api/runs" && r.Method == "POST":
					runs.Add(1)
					var payload struct {
						Plan        string            `json:"plan_id"`
						Cluster     string            `json:"cluster_id"`
						Environment string            `json:"environment_id"`
						Workers     []string          `json:"workers"`
						Properties  map[string]string `json:"properties"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload.Plan != "fixture-plan" || payload.Cluster != "selected-cluster" || payload.Environment != "selected-environment" || !reflect.DeepEqual(payload.Workers, workers) || payload.Properties["loops"] != "5" || payload.Properties["threads"] != "1" || payload.Properties["delay_ms"] != "20" || r.Header.Get("Idempotency-Key") == "" {
						t.Error("run selection/properties/idempotency contract changed")
					}
					io.WriteString(w, `{"id":"fixture-run","raw_archive":{"status":"ARCHIVED"}}`)
				case r.URL.Path == "/api/runs/fixture-run":
					if polls.Add(1) == 1 && scenario == "pending-archive" {
						io.WriteString(w, `{"id":"fixture-run","status":"COMPLETED"}`)
						return
					}
					if scenario == "http-error" {
						w.WriteHeader(500)
						io.WriteString(w, "fixture-private-token")
						return
					}
					archive, status := "ARCHIVED", "COMPLETED"
					if scenario == "DISABLED" {
						archive = "DISABLED"
					}
					if scenario == "failed-run" {
						status = "FAILED"
					}
					run := map[string]any{"id": "fixture-run", "status": status, "raw_archive": map[string]any{"status": archive}}
					if scenario == "summary" {
						run["summary"] = map[string]any{"samples": 30}
					}
					if scenario == "reports" {
						run["reports"] = []any{"report"}
					}
					json.NewEncoder(w).Encode(run)
				case r.URL.Path == "/api/runs/fixture-run/artifacts":
					if scenario == "pending-archive" && polls.Load() < 2 {
						t.Error("stale create response bypassed archive wait")
					}
					items := []any{}
					for _, name := range []string{"results.jtl", "jmeter.log", "console.log"} {
						digest := sha256.Sum256([]byte(bodies[name]))
						size, sum, download := len(bodies[name]), hex.EncodeToString(digest[:]), "/files/"+name
						if name == "jmeter.log" && scenario == "wrong-size" {
							size++
						}
						if name == "console.log" && scenario == "wrong-hash" {
							sum = strings.Repeat("0", 64)
						}
						if scenario == "external-download" {
							download = "https://example.invalid/file"
						}
						items = append(items, map[string]any{"name": name, "size": size, "sha256": sum, "download_url": download})
					}
					if scenario == "extra-artifact" {
						items = append(items, map[string]any{"name": "unexpected.log"})
					}
					json.NewEncoder(w).Encode(map[string]any{"items": items})
				case strings.HasPrefix(r.URL.Path, "/files/"):
					io.WriteString(w, bodies[strings.TrimPrefix(r.URL.Path, "/files/")])
				default:
					t.Errorf("unexpected fixture call: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			err := platformSmoke(context.Background(), []string{"--url", server.URL + "/", "--token-file", tokenFile, "--plan", planFile, "--project", "selected-project", "--cluster", "selected-cluster", "--environment", "selected-environment"}, &out)
			if scenario == "ARCHIVED" || scenario == "DISABLED" || scenario == "pending-archive" || scenario == "two-workers" || scenario == "four-workers" || scenario == "seven-workers" {
				if err != nil {
					t.Fatal(err)
				}
				var receipt struct {
					Run     string `json:"run_id"`
					Samples int    `json:"acceptance_samples"`
					Errors  int    `json:"acceptance_errors"`
					Engines int    `json:"acceptance_engines"`
				}
				if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
					t.Fatal(err)
				}
				if receipt.Run != "fixture-run" || receipt.Samples != 5*len(workers) || receipt.Errors != 0 || receipt.Engines != len(workers) {
					t.Fatalf("invalid receipt: %+v", receipt)
				}
			} else if err == nil || out.Len() != 0 {
				t.Fatal("failed acceptance published success")
			}
			if err != nil && strings.Contains(err.Error(), "fixture-private-token") {
				t.Fatal("HTTP error exposed token")
			}
			if (scenario == "unregistered" || scenario == "duplicate-worker" || scenario == "empty-workers" || scenario == "invalid-worker" || scenario == "too-many-workers") && (plans.Load() != 0 || runs.Load() != 0) {
				t.Fatal("invalid cluster still uploaded/submitted")
			}
		})
	}
}

func TestPlatformSmokeRejectsMissingSelectionAndNonInternalPlan(t *testing.T) {
	root := t.TempDir()
	tokenFile, planFile := filepath.Join(root, "token"), filepath.Join(root, "plan.jmx")
	writeFixture(t, tokenFile, "fixture-private-token")
	writeFixture(t, planFile, `<stringProp name="HTTPSampler.domain">business.example</stringProp>`)
	for _, selection := range [][]string{nil, {"--cluster", "cluster", "--environment", "environment"}, {"--project", "project", "--cluster", "cluster", "--environment", "environment"}} {
		args := append([]string{"--url", "http://127.0.0.1:1", "--token-file", tokenFile, "--plan", planFile}, selection...)
		if err := platformSmoke(context.Background(), args, io.Discard); err == nil {
			t.Fatal("unsafe or incomplete selection accepted")
		}
	}
}

func TestPlatformSmokeRequiresProjectBeforeReadingFiles(t *testing.T) {
	var out bytes.Buffer
	err := platformSmoke(context.Background(), []string{"--token-file", "/missing-token", "--plan", "/missing-plan", "--cluster", "cluster", "--environment", "environment"}, &out)
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("missing scope was not rejected before reading secrets: %v", err)
	}
}
