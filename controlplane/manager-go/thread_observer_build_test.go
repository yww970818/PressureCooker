package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestThreadObserverBuildPreflight(t *testing.T) {
	script, err := os.ReadFile("../../bin/check-thread-observer.sh")
	if err != nil {
		t.Fatal(err)
	}
	const source = "tools/jmeter-thread-observer/src/io/pressurecooker/jmeter/ThreadObservationListener.java"
	const builder = "tools/jmeter-thread-observer/build.sh"
	for _, mode := range []string{"valid", "source-changed", "jar-changed", "jar-missing", "source-manifest-missing"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "build with spaces")
			for _, p := range []string{"bin", "vendor", filepath.Dir(source), filepath.Dir(builder)} {
				if err := os.MkdirAll(filepath.Join(root, p), 0700); err != nil {
					t.Fatal(err)
				}
			}
			put := func(name string, data []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			put("bin/check-thread-observer.sh", script)
			put(source, []byte("official-runtime-extension-source"))
			put(builder, []byte("javac then jar"))
			put("vendor/pressurecooker-thread-observer.jar", []byte("compiled-artifact"))
			var sources string
			for _, name := range []string{builder, source} {
				b, _ := os.ReadFile(filepath.Join(root, name))
				sources += fmt.Sprintf("%x  %s\n", sha256.Sum256(b), name)
			}
			put("vendor/thread-observer-sources.sha256", []byte(sources))
			put("vendor/thread-observer.sha256", []byte(fmt.Sprintf("%x  pressurecooker-thread-observer.jar\n", sha256.Sum256([]byte("compiled-artifact")))))
			switch mode {
			case "source-changed":
				put(source, []byte("updated-source"))
			case "jar-changed":
				put("vendor/pressurecooker-thread-observer.jar", []byte("stale-or-corrupt"))
			case "jar-missing":
				os.Remove(filepath.Join(root, "vendor/pressurecooker-thread-observer.jar"))
			case "source-manifest-missing":
				os.Remove(filepath.Join(root, "vendor/thread-observer-sources.sha256"))
			}
			out, err := exec.Command("bash", filepath.Join(root, "bin/check-thread-observer.sh")).CombinedOutput()
			if (err == nil) != (mode == "valid") {
				t.Fatalf("mode %s result %v: %s", mode, err, out)
			}
		})
	}
}
