package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareSecretsPreservesExistingValuesAndPrivateModes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runtime/secrets/platform/admin-token")
	writeFixture(t, path, "existing-private-token\n")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := prepareSecrets(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"admin-token", "session-key", "agent-token", "minio-access-key", "minio-secret-key"} {
		path := filepath.Join(root, "runtime/secrets/platform", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(strings.TrimSpace(string(body))) < 20 {
			t.Fatalf("%s is too short", name)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s is not private: %v", name, err)
		}
		if name == "admin-token" && string(body) != "existing-private-token\n" {
			t.Fatal("existing secret was replaced")
		}
		if name == "minio-access-key" && !strings.HasPrefix(string(body), "jmeter-") {
			t.Fatal("MinIO account format changed")
		}
	}
	before, _ := os.ReadFile(path)
	if err := prepareSecrets(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("repeat preparation replaced a secret")
	}
}

func TestPrepareSecretsRejectsSymlinksWithoutChangingTheirTargets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	writeFixture(t, target, "do-not-change")
	path := filepath.Join(root, "runtime/secrets/platform/admin-token")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := prepareSecrets(root); err == nil {
		t.Fatal("symlink accepted")
	}
	body, _ := os.ReadFile(target)
	if string(body) != "do-not-change" {
		t.Fatal("symlink target was changed")
	}
}

func TestJSONFieldExtractsHyphenatedKeysAndRejectsNonStrings(t *testing.T) {
	var out bytes.Buffer
	if err := jsonField(strings.NewReader(`{"services":{"controller-a":{"image":"example:v1"}}}`), &out, []string{"services", "controller-a", "image"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "example:v1\n" {
		t.Fatalf("unexpected output %q", out.String())
	}
	for _, input := range []string{`{"id":null}`, `{"id":{}}`, `{"other":"private-value"}`, `{"id":""}`, `{"id":"x"} trailing`} {
		out.Reset()
		if err := jsonField(strings.NewReader(input), &out, []string{"id"}); err == nil || out.Len() != 0 {
			t.Fatalf("invalid extraction accepted: %q", input)
		}
	}
}

func smokeCSV(perWorker int, workers int) string {
	var body strings.Builder
	body.WriteString("\ufefftimeStamp,responseCode,success,Hostname\n")
	for worker := 1; worker <= workers; worker++ {
		for row := 0; row < perWorker; row++ {
			fmt.Fprintf(&body, "1,200,true,worker-%d\n", worker)
		}
	}
	return body.String()
}

func TestCheckSmokeWritesOnlyAfterCompleteValidation(t *testing.T) {
	for _, failure := range []string{"", "sample-count", "failed-sample", "response-code", "worker-distribution", "missing-html"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			body := smokeCSV(5, 2)
			switch failure {
			case "sample-count":
				body = smokeCSV(4, 2)
			case "failed-sample":
				body = strings.Replace(body, ",true,", ",false,", 1)
			case "response-code":
				body = strings.Replace(body, ",200,", ",500,", 1)
			case "worker-distribution":
				body = strings.Replace(body, "worker-2", "worker-3", 1)
			}
			writeFixture(t, filepath.Join(root, "results.jtl"), body)
			if failure != "missing-html" {
				writeFixture(t, filepath.Join(root, "html/index.html"), "fixture")
			}
			path := filepath.Join(root, "smoke-validation.json")
			writeFixture(t, path, "previous-validation")
			var out bytes.Buffer
			err := checkSmoke(root, &out)
			if failure != "" {
				if err == nil {
					t.Fatal("invalid smoke data accepted")
				}
				previous, _ := os.ReadFile(path)
				if string(previous) != "previous-validation" || out.Len() != 0 {
					t.Fatal("failed validation published success")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Passed  bool           `json:"passed"`
				Samples int            `json:"samples"`
				Errors  int            `json:"errors"`
				Engines map[string]int `json:"samples_by_engine"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if !report.Passed || report.Samples != 10 || report.Errors != 0 || report.Engines["worker-1"] != 5 || report.Engines["worker-2"] != 5 {
				t.Fatalf("invalid report: %+v", report)
			}
			file, _ := os.ReadFile(path)
			if !json.Valid(file) {
				t.Fatal("invalid saved JSON")
			}
		})
	}
}

type fixtureTransport struct{ target *url.URL }

func (transport fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.URL.Scheme, copy.URL.Host = transport.target.Scheme, transport.target.Host
	return http.DefaultTransport.RoundTrip(copy)
}

func nodeFixture(t *testing.T, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	digest := sha256.Sum256([]byte(body))
	meta := map[string]string{"version": "22.23.4", "filename": "node-v22.23.4-linux-x64.tar.xz", "url": "https://nodejs.org/dist/v22.23.4/node-v22.23.4-linux-x64.tar.xz", "sha256": hex.EncodeToString(digest[:])}
	raw, _ := json.Marshal(meta)
	writeFixture(t, filepath.Join(root, "vendor/node-source.json"), string(raw))
	return root, filepath.Join(root, "vendor", meta["filename"])
}

func TestFetchNodeVerifiesDownloadAndReusesVerifiedCache(t *testing.T) {
	const body = "official-runtime-fixture"
	root, path := nodeFixture(t, body)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; io.WriteString(w, body) }))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: fixtureTransport{target}}
	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		if err := fetchNode(context.Background(), root, client, &out); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("valid cache not reused: %d requests", requests)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != body || strings.Count(out.String(), "Verified Node 22.23.4") != 2 {
		t.Fatal("download or receipt incorrect")
	}
}

func TestFetchNodeRejectsInvalidDownloadWithoutReplacingCache(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			root, path := nodeFixture(t, "expected-runtime")
			writeFixture(t, path, "previous-cache")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, "corrupt-or-error")
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			var out bytes.Buffer
			if err := fetchNode(context.Background(), root, &http.Client{Transport: fixtureTransport{target}}, &out); err == nil {
				t.Fatal("bad download accepted")
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != "previous-cache" || out.Len() != 0 {
				t.Fatal("failed download changed cache or printed success")
			}
			entries, _ := os.ReadDir(filepath.Dir(path))
			if len(entries) != 2 {
				t.Fatal("partial download left behind")
			}
		})
	}
}
