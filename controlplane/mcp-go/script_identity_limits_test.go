package main

import (
	"strings"
	"testing"
)

func TestScriptIdentityLimitCatalog(t *testing.T) {
	for _, spec := range makeManagerSpecs() {
		if spec.name == "submit_console_task" {
			for _, want := range []string{"64条身份", "100项", "显式转换并重新发布"} {
				if !strings.Contains(spec.description, want) {
					t.Fatalf("missing contract %q", want)
				}
			}
			return
		}
	}
	t.Fatal("submission contract absent")
}
