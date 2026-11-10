package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func wrapperFixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "deployment with spaces")
	script := filepath.Join(root, "bin/platform-ops.sh")
	body, err := os.ReadFile("../../bin/platform-ops.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, script, string(body))
	return root, script
}

func executableFixture(t *testing.T, path, body string) {
	t.Helper()
	writeFixture(t, path, body)
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestWrapperPrefersMatchingPrebuiltBinaryAndPreservesArguments(t *testing.T) {
	root, script := wrapperFixture(t)
	binary := filepath.Join(root, "vendor/pressurecooker-ops-"+runtime.GOOS+"-"+runtime.GOARCH)
	executableFixture(t, binary, "#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
	command := exec.Command("/bin/bash", script, "check-smoke", "folder with spaces")
	command.Env = append(os.Environ(), "GO=/nonexistent/compiler")
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("prebuilt dispatch: %v: %s", err, body)
	}
	if string(body) != "--root\n"+root+"\ncheck-smoke\nfolder with spaces\n" {
		t.Fatalf("arguments changed: %q", body)
	}
}

func TestWrapperSourceFallbackAndMissingCompilerError(t *testing.T) {
	root, script := wrapperFixture(t)
	compiler := filepath.Join(root, "tools with spaces/go")
	executableFixture(t, compiler, "#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
	command := exec.Command("/bin/bash", script, "json-field", "controller-a")
	command.Env = append(os.Environ(), "GO="+compiler)
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("source dispatch: %v: %s", err, body)
	}
	if string(body) != "-C\n"+root+"/tools/platform-ops\nrun\n.\n--root\n"+root+"\njson-field\ncontroller-a\n" {
		t.Fatalf("source arguments changed: %q", body)
	}
	command = exec.Command("/bin/bash", script, "json-field", "id")
	command.Env = append(os.Environ(), "GO=/nonexistent/compiler")
	body, err = command.CombinedOutput()
	if err == nil || !strings.Contains(string(body), "Missing host utility:") || !strings.Contains(string(body), "bin/build-master.sh") {
		t.Fatalf("missing compiler not explained: %v %s", err, body)
	}
}

func manageFixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "deployment with spaces")
	body, err := os.ReadFile("../../manage.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "manage.sh")
	writeFixture(t, script, string(body))
	executableFixture(t, filepath.Join(root, "bin/platform-ops.sh"), `#!/bin/bash
case "$1" in
 prepare-secrets) ;;
 json-field) cat >/dev/null; printf '%s\n' fixture-plan ;;
 *) exit 1 ;;
esac
`)
	executableFixture(t, filepath.Join(root, "fixture-bin/docker"), `#!/bin/bash
printf '%s\n' CALL "$@" >> "$FIXTURE_DOCKER_LOG"
printf '%s\n' '{"id":"fixture-plan"}'
`)
	return root, script
}

func TestManageRunRequiresAndPreservesProjectSelection(t *testing.T) {
	root, script := manageFixture(t)
	log := filepath.Join(root, "docker.log")
	invoke := func(args ...string) ([]byte, error) {
		cmd := exec.Command("/bin/bash", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "fixture-bin")+":"+os.Getenv("PATH"), "FIXTURE_DOCKER_LOG="+log)
		return cmd.CombinedOutput()
	}
	if out, err := invoke("run", "plan with spaces.jmx", "--cluster", "cluster-one", "--environment", "environment-one"); err == nil {
		t.Fatalf("missing project accepted: %s", out)
	}
	if body, _ := os.ReadFile(log); len(body) != 0 {
		t.Fatal("missing scope reached Docker")
	}
	out, err := invoke("run", "plan with spaces.jmx", "--project", "project-one", "--cluster", "cluster-one", "--environment", "environment-one", "-Gvalue=literal $value with spaces")
	if err != nil {
		t.Fatalf("explicit project rejected: %v: %s", err, out)
	}
	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(body)), "CALL\n")
	if len(calls) != 3 {
		t.Fatalf("expected upload and start: %q", body)
	}
	prefix := "compose\n--env-file\ncluster.env\n-f\ncompose.yaml\nexec\n-T\nmanager\n/app/bin/jmeter-manager\napi\n"
	if calls[1] != prefix+"upload\nplans/plan with spaces.jmx\n--project\nproject-one\n--name\nplan with spaces\n" {
		t.Fatalf("upload scope/arguments changed: %q", calls[1])
	}
	args := strings.Split(strings.TrimSuffix(calls[2], "\n"), "\n")
	start := strings.Split(strings.TrimSuffix(prefix, "\n"), "\n")
	start = append(start, "start", "fixture-plan", "--project", "project-one", "--cluster", "cluster-one", "--environment", "environment-one", "--name")
	if len(args) != len(start)+5 || !reflect.DeepEqual(args[:len(start)], start) || args[len(start)+1] != "--key" || args[len(start)] != args[len(start)+2] || args[len(start)+3] != "--property" || args[len(start)+4] != "value=literal $value with spaces" {
		t.Fatalf("start scope/arguments changed: %q", calls[2])
	}
}

func TestManageSmokePassesExplicitScope(t *testing.T) {
	root, script := manageFixture(t)
	log := filepath.Join(root, "docker.log")
	smokeLog := filepath.Join(root, "smoke.log")
	executableFixture(t, filepath.Join(root, "bin/platform-smoke.sh"), "#!/bin/bash\nprintf '%s\\n' \"$@\" > \"$FIXTURE_SMOKE_LOG\"\n")
	cmd := exec.Command("/bin/bash", script, "smoke", "project-one", "cluster-one", "environment-one")
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "fixture-bin")+":"+os.Getenv("PATH"), "FIXTURE_DOCKER_LOG="+log, "FIXTURE_SMOKE_LOG="+smokeLog)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scoped smoke rejected: %v: %s", err, out)
	}
	body, err := os.ReadFile(smokeLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(body), "--project\nproject-one\n--cluster\ncluster-one\n--environment\nenvironment-one\n") {
		t.Fatalf("self-test lost scope: %q", body)
	}
}
