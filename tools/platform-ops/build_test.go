package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMasterIncludesDeploymentUtilityInChecksums(t *testing.T) {
	root := filepath.Join(t.TempDir(), "build with spaces")
	body, err := os.ReadFile("../../bin/build-master.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "bin/build-master.sh")
	writeFixture(t, script, string(body))
	writeFixture(t, filepath.Join(root, "runtime/toolcache/apache-jmeter-5.6.3/lib/ext/ApacheJMeter_core.jar"), "official-fixture")
	writeFixture(t, filepath.Join(root, "tools/jmeter-thread-observer/build.sh"), "#!/bin/bash\nmkdir -p \"$(dirname -- \"$2\")\"\nprintf observer > \"$2\"\n")
	writeFixture(t, filepath.Join(root, "tools/jmeter-thread-observer/src/io/pressurecooker/jmeter/ThreadObservationListener.java"), "observer-source")
	writeFixture(t, filepath.Join(root, "bin/check-thread-observer.sh"), "#!/bin/bash\nexit 0\n")
	compiler := filepath.Join(root, "compiler with spaces/go")
	executableFixture(t, compiler, `#!/bin/bash
set -euo pipefail
if [[ $1 == env ]]; then
  case $2 in GOHOSTOS) printf darwin;; GOHOSTARCH) printf arm64;; *) exit 1;; esac
  exit 0
fi
while [[ $# -gt 0 ]]; do
  if [[ $1 == -o ]]; then
    printf 'fixture %s %s' "$GOOS" "$GOARCH" > "$2"
    chmod 755 "$2"
    exit 0
  fi
  shift
done
exit 1
`)
	command := exec.Command("/bin/bash", script)
	command.Env = append(os.Environ(), "GO="+compiler, "JMETER_HOME="+filepath.Join(root, "runtime/toolcache/apache-jmeter-5.6.3"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated build failed: %v: %s", err, output)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "vendor/master-binaries.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(string(manifest)), "\n")) != 4 || !strings.Contains(string(manifest), "pressurecooker-ops-linux-amd64") {
		t.Fatalf("deployment utility missing from manifest: %s", manifest)
	}
	for name, expected := range map[string]string{"pressurecooker-ops-linux-amd64": "fixture linux amd64", "pressurecooker-ops-darwin-arm64": "fixture darwin arm64"} {
		actual, err := os.ReadFile(filepath.Join(root, "vendor", name))
		if err != nil || string(actual) != expected {
			t.Fatalf("incorrect %s artifact: %v %q", name, err, actual)
		}
	}
}
