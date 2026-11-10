package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicit local performance acceptance includes encryption, MySQL versions,
// editor projection and compression. It never starts execution or contacts a SUT.
func TestMySQLWorkflowImportPerformanceAcceptance(t *testing.T) {
	if os.Getenv("JMETER_WORKFLOW_IMPORT_ACCEPTANCE") != "1" {
		t.Skip("explicit large workflow import acceptance")
	}
	p, _ := mysqlFixture(t)
	content, err := compileWorkflow(obj(syntheticEditableSuite()["graph"]))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"parse_and_create", "edited_graph_create"} {
		for i := 0; i < 3; i++ {
			started := time.Now()
			graph := parsed
			if mode == "parse_and_create" {
				graph, err = parseWorkflowGraph(content)
				if err != nil {
					t.Fatal(err)
				}
			}
			value, err := p.workspaceCreate("workflows", Obj{"name": "Synthetic import acceptance", "graph": graph})
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(started)
			saved, err := p.workspaceGet("workflows", str(value["id"]))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(encoded(graph), encoded(saved["graph"])) {
				t.Fatal("persisted graph changed")
			}
			var versions int
			if err = p.DB.QueryRow("SELECT COUNT(*) FROM workflow_versions WHERE object_id=?", value["id"]).Scan(&versions); err != nil || versions != 1 {
				t.Fatal("import created extra revisions", versions, err)
			}
			t.Logf("mode=%s sample=%d nodes=%d jmx_bytes=%d elapsed_ms=%.3f", mode, i+1, len(arr(graph["nodes"])), len(content), float64(elapsed.Microseconds())/1000)
		}
	}
}

func TestWorkflowCompileDoesNotMutateBoundSource(t *testing.T) {
	graph := managedGenerationGraph()
	graph["environment_binding"] = "base_host"
	graph["environment_variables"] = []any{"shared"}
	props := obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])
	props["api_reference"] = Obj{"collection_id": "collection-fixture", "revision": 1, "path": "/ping", "method": "get"}
	props["api_environment_variables"] = []any{"shared"}
	obj(props["config"])["path"] = "https://fixture.invalid/ping?value=${shared}"
	header := graphNode("headers", "HeaderManager", Obj{"headers": []any{Obj{"name": "X-Test", "value": "${shared}", "_source_id": "header-source"}}})
	opaque := graphNode("opaque", "fixture.Plugin", Obj{})
	obj(opaque["properties"])["raw_xml"] = `<fixture.Plugin testname="opaque"><stringProp name="value">${shared}</stringProp></fixture.Plugin><hashTree><!-- opaque --></hashTree>`
	graph["nodes"] = append(arr(graph["nodes"]), header, opaque)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("http", "headers", 1), graphEdge("group", "opaque", 1))
	for pass := 0; pass < 2; pass++ {
		before := encoded(graph)
		content, err := compileWorkflow(graph)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, encoded(graph)) {
			t.Fatal("compilation mutated source graph")
		}
		if _, err = workflowArgumentGenerationRules(content); err != nil {
			t.Fatal(err)
		}
		graph, err = parseWorkflowGraph(content)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkflowRootTemplatePreservesOrderedMetadata(t *testing.T) {
	source := `<jmeterTestPlan custom="kept"><?fixture keep?><hashTree custom="top"><!-- before --><TestPlan testname="root"/><!-- between --><hashTree><ThreadGroup testname="group"/><hashTree/></hashTree><!-- after --></hashTree><fixture.Metadata>kept</fixture.Metadata></jmeterTestPlan>`
	graph, err := parseWorkflowGraph(source)
	if err != nil {
		t.Fatal(err)
	}
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for _, marker := range []string{`custom="kept"`, `<?fixture keep?>`, `custom="top"`, `<!-- before -->`, `<TestPlan`, `<!-- between -->`, `<hashTree><ThreadGroup`, `<!-- after -->`, `<fixture.Metadata>kept</fixture.Metadata>`} {
		i := strings.Index(content, marker)
		if i <= last {
			t.Fatalf("root metadata order changed at %q", marker)
		}
		last = i
	}
}

func TestWorkflowGeneratedXMLRetainsStructureLimits(t *testing.T) {
	for _, depth := range []int{256, 257} {
		root := element("root")
		leaf := root
		for i := 1; i < depth; i++ {
			next := element("child")
			leaf.append(next)
			leaf = next
		}
		_, parseErr := parseXMLFragment(elementXML(root))
		if got := validateGeneratedXMLStructure(root); (got == nil) != (parseErr == nil) {
			t.Fatal("XML depth validation differs", depth)
		}
	}
	root := element("root")
	for i := 0; i < 125000; i++ {
		root.append(element("child"))
	}
	if err := validateGeneratedXMLStructure(root); err == nil {
		t.Fatal("XML token limit bypassed")
	}
}

func TestWorkflowCreationValidatesParsedImport(t *testing.T) {
	content, e := compileWorkflow(basicWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	graph, e := parseWorkflowGraph(content)
	if e != nil {
		t.Fatal(e)
	}
	data, e := validateWorkspace("workflows", Obj{"name": "导入草稿", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(obj(data["graph"])["nodes"])) != 3 || str(obj(findGraphNode(obj(data["graph"]), "HTTPSamplerProxy")["properties"])["raw_xml"]) == "" {
		t.Fatal("imported parameters or original XML lost")
	}
	// Parsing is not an authorization to persist: creation must still reject a
	// sampler outside its thread scope, just as non-persisting import does.
	invalid := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><HTTPSamplerProxy/><hashTree/></hashTree></hashTree></jmeterTestPlan>`
	graph, e = parseWorkflowGraph(invalid)
	if e != nil {
		t.Fatal("scope should be rejected by compilation", e)
	}
	if _, e = validateWorkspace("workflows", Obj{"name": "无效作用域", "graph": graph}); e == nil {
		t.Fatal("creation skipped import validation")
	}
	if _, e = importWorkflowGraph(invalid); e == nil {
		t.Fatal("preview skipped import validation")
	}
}

// Compare the old parse/compile/create-validation chain against the same
// creation validation after parsing, using only the synthetic capacity suite.
func BenchmarkWorkflowImportPreparation(b *testing.B) {
	content, e := compileWorkflow(obj(syntheticEditableSuite()["graph"]))
	if e != nil {
		b.Fatal(e)
	}
	for _, variant := range []struct {
		name  string
		parse func(string) (Obj, error)
	}{{"previous", importWorkflowGraph}, {"single_compile", parseWorkflowGraph}} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				graph, e := variant.parse(content)
				if e != nil {
					b.Fatal(e)
				}
				if _, e = validateWorkspace("workflows", Obj{"name": "合成导入", "graph": graph}); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}

func BenchmarkWorkflowImportStages(b *testing.B) {
	content, err := compileWorkflow(obj(syntheticEditableSuite()["graph"]))
	if err != nil {
		b.Fatal(err)
	}
	graph, err := parseWorkflowGraph(content)
	if err != nil {
		b.Fatal(err)
	}
	document := Obj{"name": "Synthetic import", "graph": graph}
	p := &Platform{Config: Config{SessionKey: "synthetic-benchmark-key"}}
	for _, stage := range []struct {
		name string
		run  func() error
	}{
		{"parse", func() error { _, err := parseWorkflowGraph(content); return err }},
		{"compile", func() error { _, err := compileWorkflow(graph); return err }},
		{"validate_workspace", func() error { _, err := validateWorkspace("workflows", document); return err }},
		{"clone", func() error { clone(document); return nil }},
		{"encrypt", func() error { _, err := p.sealWorkspace("workflows", "benchmark", 1, document); return err }},
	} {
		b.Run(stage.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := stage.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
