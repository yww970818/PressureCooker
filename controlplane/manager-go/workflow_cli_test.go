package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompileWorkflowCLIOfflineExclusiveOutput(t *testing.T) {
	d := t.TempDir()
	in := filepath.Join(d, "graph.json")
	out := filepath.Join(d, "plan.jmx")
	graph := basicWorkflowGraph()
	if err := os.WriteFile(in, encoded(graph), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runCLI([]string{"compile-workflow", in, out}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || len(b) == 0 {
		t.Fatal(err)
	}
	if _, err := parseWorkflowGraph(string(b)); err != nil {
		t.Fatal(err)
	}
	if err := runCLI([]string{"compile-workflow", in, out}); err == nil {
		t.Fatal("overwrote artifact")
	}
}
