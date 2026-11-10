package main

import (
	"go/ast"
	"go/parser"
	goToken "go/token"
	"strconv"
	"testing"
)

// Node health belongs to Core reports. Keep the scheduler and both recovery
// paths from silently restoring manager-initiated status polling.
func TestReportedSchedulerHasNoStatusProbe(t *testing.T) {
	for _, name := range []string{"scheduler.go", "runner_recovery.go"} {
		set := goToken.NewFileSet()
		file, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "request" {
				return true
			}
			path, ok := call.Args[1].(*ast.BasicLit)
			if !ok || path.Kind != goToken.STRING {
				return true
			}
			value, err := strconv.Unquote(path.Value)
			if err == nil && value == "/status" {
				t.Errorf("manager probes node status at %s", set.Position(call.Pos()))
			}
			return true
		})
	}
}
