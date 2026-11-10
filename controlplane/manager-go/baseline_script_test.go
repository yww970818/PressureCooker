package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real entrypoint with inert tools: rejected configurations must
// stop before Go/npm can run, and the default path must disable integration.
func TestControlplaneBaselinePreflight(t *testing.T) {
	script, err := os.ReadFile("../../bin/test-controlplane.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mysql, redis              string
		integration, mcp, node, allowed bool
	}{
		{"default ignores inherited integration", "remote.invalid", "remote.invalid:6379", false, true, true, true},
		{"local integration", "127.0.0.1", "127.0.0.1:26379", true, true, true, true},
		{"remote MySQL", "remote.invalid", "127.0.0.1:26379", true, true, true, false},
		{"remote Redis", "127.0.0.1", "remote.invalid:6379", true, true, true, false},
		{"wrong Redis test port", "127.0.0.1", "127.0.0.1:6379", true, true, true, false},
		{"missing MCP runtime", "127.0.0.1", "127.0.0.1:26379", false, false, true, false},
		{"missing Node runtime", "127.0.0.1", "127.0.0.1:26379", false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "checkout with spaces")
			bin := filepath.Join(root, "bin")
			for _, directory := range []string{bin, filepath.Join(root, "controlplane/manager-go"), filepath.Join(root, "controlplane/execution"), filepath.Join(root, "controlplane/mcp-go"), filepath.Join(root, "tools/platform-ops"), filepath.Join(root, "controlplane/frontend")} {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			entry := filepath.Join(bin, "test-controlplane.sh")
			if err := os.WriteFile(entry, script, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(root, "executed")
			for _, tool := range []string{"go", "npm"} {
				if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$JMETER_GO_INTEGRATION\" \"$JMETER_EXECUTION_IT\" >> \"$PC_TEST_MARKER\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.node {
				if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			dirname, err := exec.LookPath("dirname")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(dirname, filepath.Join(bin, "dirname")); err != nil {
				t.Fatal(err)
			}
			if tc.mcp {
				path := filepath.Join(root, "controlplane/mcp/node_modules/jmeter-mcp-server/dist/index.js")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("// inert preflight fixture\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{entry}
			if tc.integration {
				args = append(args, "--integration")
			}
			cmd := exec.Command("/bin/bash", args...)
			cmd.Env = []string{"PATH=" + bin, "GO=" + filepath.Join(bin, "go"), "TMPDIR=" + root, "PC_TEST_MARKER=" + marker, "JMETER_MYSQL_HOST=" + tc.mysql, "JMETER_MYSQL_DATABASE=pressurecooker_test", "JMETER_REDIS_ADDR=" + tc.redis, "JMETER_GO_INTEGRATION=1", "JMETER_EXECUTION_IT=1"}
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v, error=%v, output=%s", tc.allowed, err, output)
			}
			calls, readErr := os.ReadFile(marker)
			if !tc.allowed {
				if !os.IsNotExist(readErr) {
					t.Fatal("rejected configuration started test tools")
				}
				return
			}
			flag := "0|0\n"
			if tc.integration {
				flag = "1|1\n"
			}
			if readErr != nil || string(calls) != strings.Repeat(flag, 6) {
				t.Fatalf("unexpected test flags/calls: %q (%v)", calls, readErr)
			}
		})
	}
}
