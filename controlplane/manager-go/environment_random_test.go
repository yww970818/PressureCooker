package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Losing validation here would persist unsupported rules that later reach a request.
func TestEnvironmentRandomRulesValidateAndPreserveDefinitions(t *testing.T) {
	for _, value := range []string{"", "  fixed ${token} {{name}}  ", "{{$random.uuid}}", "{{$random.hex(1)}}", "{{$random.hex(256)}}", "{{$random.alnum(16)}}", "{{$random.digits(12)}}"} {
		body := Obj{"name": "rules", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "device", "value": value}}}
		validated, err := validateEnvironment(body)
		if err != nil || obj(arr(validated["variables"])[0])["value"] != value {
			t.Fatalf("project definition changed or rejected %q: %v", value, err)
		}
		client := Obj{collectionOverridesKey: Obj{"environment-test": Obj{"variables": []any{Obj{"name": "device", "value": value}}}}}
		if err := validateCollectionOverrides(client); err != nil {
			t.Fatalf("collection definition rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"{{$random.UUID}}", "{{$random.uuid()}}", "{{$random.hex(0)}}", "{{$random.hex(257)}}", "{{$random.hex(01)}}", "{{$random.hex(+2)}}", "{{$random.hex(2.0)}}", "{{$random.hex( 2)}}", "{{$random.hex(２)}}", "prefix{{$random.uuid}}", "{{$random.uuid}}suffix", " {{$random.uuid}}", "{{$random.uuid", "$random.uuid", "{{ $random.uuid }}"} {
		t.Run(value, func(t *testing.T) {
			body := Obj{"name": "rules", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "device", "value": value}}}
			if _, err := validateEnvironment(body); err == nil {
				t.Fatal("invalid project rule accepted")
			}
			client := Obj{collectionOverridesKey: Obj{"environment-test": Obj{"variables": []any{Obj{"name": "device", "value": value}}}}}
			if err := validateCollectionOverrides(client); err == nil {
				t.Fatal("invalid collection rule accepted")
			}
			if _, err := canonicalScalarEnvironments(Obj{"legacy": Obj{"variables": Obj{"device": value}}}); err == nil {
				t.Fatal("invalid legacy collection rule accepted")
			}
			if _, _, err := environmentSnapshot(body); err == nil {
				t.Fatal("invalid execution rule accepted")
			}
		})
	}
}

// Generating before applying overrides would leave a directive or the wrong value.
func TestEnvironmentRandomMergeResolvesOnlyEffectiveDefinitions(t *testing.T) {
	collection := Obj{"client_state": Obj{collectionOverridesKey: Obj{"environment-test": Obj{"variables": []any{Obj{"name": "device", "value": "{{$random.hex(32)}}"}, Obj{"name": "discarded", "value": "fixed"}}}}}}
	environment := Obj{"id": "environment-test", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "device", "value": "{{$random.uuid}}"}, Obj{"name": "discarded", "value": "{{$random.uuid}}"}, Obj{"name": "static", "value": "  unchanged ${x}  "}}}
	before := string(encoded(environment)) + string(encoded(collection))
	first, err := mergedCollectionVariables(collection, environment, Obj{"explicit": "{{$random.digits(256)}}"})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(first["device"]) || !regexp.MustCompile(`^[0-9]{256}$`).MatchString(first["explicit"]) || first["discarded"] != "fixed" || first["static"] != "  unchanged ${x}  " || first["base_host"] != "https://fixture.invalid" {
		t.Fatal("merged runtime variables invalid")
	}
	second, err := mergedCollectionVariables(collection, environment, nil)
	if err != nil || first["device"] == second["device"] {
		t.Fatal("new operation did not generate a fresh value", err)
	}
	if before != string(encoded(environment))+string(encoded(collection)) {
		t.Fatal("runtime generation mutated source definitions")
	}
	if _, err := mergedCollectionVariables(collection, environment, Obj{"device": "prefix{{$random.uuid}}"}); err == nil {
		t.Fatal("invalid explicit execution rule accepted")
	}
}

// A frozen snapshot must inject one concrete value into every global alias.
func TestEnvironmentRandomSnapshotFreezesProperties(t *testing.T) {
	body := Obj{"id": "environment-test", "name": "rules", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "device", "value": "{{$random.uuid}}"}, Obj{"name": "alnum", "value": "{{$random.alnum(256)}}"}}}
	before := string(encoded(body))
	snapshot, props, err := environmentSnapshot(body)
	if err != nil {
		t.Fatal(err)
	}
	device := str(obj(arr(snapshot["variables"])[0])["value"])
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(device) || props["device"] != device || props[environmentProperty("device")] != device {
		t.Fatal("snapshot and runtime aliases did not share a UUID v4")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{256}$`).MatchString(str(props["alnum"])) {
		t.Fatal("invalid alphanumeric generation")
	}
	if obj(arr(snapshot["variables"])[0])["generation_rule"] != "{{$random.uuid}}" {
		t.Fatal("snapshot lost rule provenance")
	}
	if before != string(encoded(body)) {
		t.Fatal("snapshot mutated stored environment")
	}
	second, _, err := environmentSnapshot(body)
	if err != nil || obj(arr(second["variables"])[0])["value"] == device {
		t.Fatal("new snapshot reused a previous random value", err)
	}
	if strings.Contains(string(encoded(props)), "$random") {
		t.Fatal("directive leaked into executor properties")
	}
}

// The legacy HTTP path must resolve once before URL/header/body expansion.
func TestEnvironmentRandomLegacyHTTPUsesOneValuePerOperation(t *testing.T) {
	seen := make(chan string, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		device := r.Header.Get("X-Device")
		if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(device) || r.URL.Query().Get("id") != device || string(body) != device+"|"+device {
			t.Error("HTTP URL/header/body did not share one generated value")
		}
		seen <- device
		w.WriteHeader(204)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	operation := Obj{"parameters": []any{Obj{"in": "query", "name": "id", "example": "{{device}}"}, Obj{"in": "header", "name": "X-Device", "example": "{{device}}"}}}
	rawImportBody(operation, "{{device}}|{{device}}", "text/plain")
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port()}}, "paths": Obj{"/request": Obj{"post": operation}}, "x-scalar-environments": Obj{"test": Obj{"variables": Obj{"device": "{{$random.uuid}}"}}}}, "client_state": Obj{"active_environment": "test"}}
	p := auditDebugPlatform(t, target, collection)
	for i := 0; i < 2; i++ {
		result, err := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/request", "method": "POST", "variables": Obj{"device": "{{$random.hex(32)}}"}})
		if err != nil || number(result["status"]) != 204 {
			t.Fatal("HTTP operation failed", err)
		}
	}
	if len(seen) != 2 {
		t.Fatal("HTTP operation count changed")
	}
	first, second := <-seen, <-seen
	if first == second {
		t.Fatal("new HTTP operation reused generated identity")
	}
}

// Independent resolution at the project/collection boundary would create two
// identities, and resolving before task overrides would freeze the wrong value.
func TestMySQLEnvironmentRandomTaskSnapshotAndBindings(t *testing.T) {
	p, _ := mysqlFixture(t)
	environment, err := p.workspaceCreate("environments", Obj{"name": "random-task", "base_host": "http://fixture.invalid", "variables": []any{Obj{"name": "identity", "value": "{{$random.uuid}}"}, Obj{"name": "overridden", "value": "{{$random.uuid}}"}, Obj{"name": "task_rule", "value": "default"}}})
	if err != nil {
		t.Fatal(err)
	}
	collectionBody := collectionOverrideBody(str(environment["id"]), "unused")
	obj(obj(collectionBody["client_state"])[collectionOverridesKey])[str(environment["id"])] = Obj{"variables": []any{Obj{"name": "local", "value": "{{$random.hex(32)}}"}, Obj{"name": "identity", "value": "{{$random.uuid}}"}, Obj{"name": "extra_rule", "value": "{{$random.hex(8)}}"}}}
	collection, err := p.workspaceCreate("api_collections", collectionBody)
	if err != nil {
		t.Fatal(err)
	}
	g := basicWorkflowGraph()
	g["environment_binding"] = "base_host"
	g["environment_variables"] = []any{"identity", "overridden", "local", "task_rule", "extra_rule"}
	first := findGraphNode(g, "HTTPSamplerProxy")
	props := obj(first["properties"])
	props["config"] = Obj{"method": "GET", "path": "/ping?identity=${identity}&local=${local}&override=${overridden}&task=${task_rule}&extra=${extra_rule}"}
	props["api_reference"] = Obj{"collection_id": collection["id"], "revision": 1, "path": "/ping", "method": "get"}
	second := clone(first)
	second["id"] = "http-two"
	g["nodes"] = append(arr(g["nodes"]), second)
	g["edges"] = append(arr(g["edges"]), graphEdge("group", "http-two", 1))
	workflow, err := p.workspaceCreate("workflows", Obj{"name": "random-bindings", "graph": g})
	if err != nil {
		t.Fatal(err)
	}
	published, err := p.publishWorkflow(str(workflow["id"]), Obj{"revision": 1})
	if err != nil {
		t.Fatal(err)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "random-frozen", "plan_id": obj(published["plan"])["id"], "workers": []any{"worker-1"}, "environment_id": environment["id"], "properties": Obj{"overridden": "task-fixed", "task_rule": "{{$random.digits(12)}}", "extra_rule": "{{$random.hex(8)}}"}}
	run, created, err := p.createRun(payload, "random-snapshot")
	if err != nil || !created {
		t.Fatal("create random task", err)
	}
	internal, err := p.get("runs", str(run["id"]))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := obj(internal["environment_snapshot"])
	values := map[string]string{}
	for _, raw := range arr(snapshot["variables"]) {
		pair := obj(raw)
		values[str(pair["name"])] = str(pair["value"])
	}
	runtime := obj(internal["properties"])
	if runtime["identity"] != values["identity"] || runtime[environmentProperty("identity")] != values["identity"] || runtime[apiEnvironmentProperty(str(first["id"]), "identity")] != values["identity"] || runtime[apiEnvironmentProperty("http-two", "identity")] != values["identity"] {
		t.Fatal("global/snapshot/collection bindings generated inconsistent identities")
	}
	if values["overridden"] != "task-fixed" || runtime[environmentProperty("overridden")] != "task-fixed" {
		t.Fatal("task override not frozen before generation")
	}
	if !regexp.MustCompile(`^[0-9]{12}$`).MatchString(str(runtime["task_rule"])) || values["task_rule"] != runtime["task_rule"] || !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(str(runtime["extra_rule"])) {
		t.Fatal("task rule not resolved once")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(str(runtime[apiEnvironmentProperty(str(first["id"]), "local")])) || runtime[apiEnvironmentProperty(str(first["id"]), "local")] != runtime[apiEnvironmentProperty("http-two", "local")] {
		t.Fatal("same collection generated twice within one snapshot")
	}
	if runtime[apiEnvironmentProperty(str(first["id"]), "extra_rule")] != runtime["extra_rule"] || runtime[apiEnvironmentProperty("http-two", "extra_rule")] != runtime["extra_rule"] {
		t.Fatal("task-only rule regenerated for collection binding")
	}
	if strings.Contains(string(encoded(runtime)), "$random") {
		t.Fatal("raw rule reached dispatch properties")
	}
	if strings.Contains(str(obj(arr(snapshot["variables"])[1])["generation_rule"]), "$random") {
		t.Fatal("discarded environment rule retained as active provenance")
	}
	for _, raw := range arr(snapshot["api_overrides"]) {
		row := obj(raw)
		if row["value"] != runtime[str(row["property"])] {
			t.Fatal("collection trace and dispatch differ")
		}
	}
	dispatched := frozenDispatchTask(t, p, str(run["id"]))
	for _, key := range []string{"identity", "task_rule", "extra_rule", apiEnvironmentProperty(str(first["id"]), "identity"), apiEnvironmentProperty(str(first["id"]), "extra_rule")} {
		if dispatched.Properties[key] != str(runtime[key]) {
			t.Fatal("signed dispatch differs from frozen properties")
		}
	}
	retried, created, err := p.createRun(payload, "random-snapshot")
	if err != nil || created || retried["id"] != run["id"] {
		t.Fatal("idempotent retry regenerated snapshot", err)
	}
	if _, err = p.stopRun(str(run["id"]), "now"); err != nil {
		t.Fatal(err)
	}
	fresh, _, err := p.createRun(payload, "random-snapshot-new")
	if err != nil {
		t.Fatal(err)
	}
	freshInternal, err := p.get("runs", str(fresh["id"]))
	if err != nil || obj(freshInternal["properties"])["identity"] == runtime["identity"] {
		t.Fatal("new task reused frozen random identity", err)
	}
	copyDraft := runCopyDraft(internal)
	if _, err = p.stopRun(str(fresh["id"]), "now"); err != nil {
		t.Fatal(err)
	}
	copied, _, err := p.createRun(copyDraft, "random-snapshot-copy")
	if err != nil {
		t.Fatal("create task copy", err)
	}
	copiedInternal, err := p.get("runs", str(copied["id"]))
	if err != nil || obj(copiedInternal["properties"])["task_rule"] == runtime["task_rule"] || obj(copiedInternal["properties"])["overridden"] != "task-fixed" {
		t.Fatal("new task copy did not regenerate authored rules", err)
	}
	stored, err := p.workspaceGet("environments", str(environment["id"]))
	if err != nil || obj(arr(stored["variables"])[0])["value"] != "{{$random.uuid}}" || obj(payload["properties"])["task_rule"] != "{{$random.digits(12)}}" {
		t.Fatal("runtime generation changed definitions", err)
	}
}

// Copying a task must restore authored rules, while explicit fixed overrides and
// inherited values retain their separate provenance.
func TestEnvironmentRandomTaskCopyRestoresOnlyAuthoredRules(t *testing.T) {
	source := Obj{"id": "run-source", "name": "random", "properties": Obj{"device": "abc123", "fixed": "chosen", "inherited": "654321"}, "task_property_keys": []any{"device", "fixed"}, "environment_snapshot": Obj{"variables": []any{Obj{"name": "fixed", "value": "chosen", "generation_rule": "{{$random.uuid}}"}}, "task_property_rules": Obj{"device": "{{$random.hex(6)}}", "inherited": "{{$random.digits(6)}}"}}}
	before := string(encoded(source))
	draft := runCopyDraft(source)
	properties := obj(draft["properties"])
	if properties["device"] != "{{$random.hex(6)}}" || properties["fixed"] != "chosen" || len(properties) != 2 {
		t.Fatal("copy lost authored rules or changed fixed/inherited values")
	}
	if before != string(encoded(source)) {
		t.Fatal("copy changed original frozen task")
	}
}

// Project-save metadata must not impersonate a frozen platform-generated value.
func TestEnvironmentRandomSaveDoesNotTrustCallerProvenance(t *testing.T) {
	input := Obj{"name": "rules", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "device", "value": "caller-fixed", "generation_rule": "{{$random.uuid}}"}}}
	validated, err := validateEnvironment(input)
	if err != nil {
		t.Fatal(err)
	}
	validated["id"] = "environment-test"
	snapshot, _, err := environmentSnapshot(validated)
	if err != nil {
		t.Fatal(err)
	}
	collection := Obj{"client_state": Obj{collectionOverridesKey: Obj{"environment-test": Obj{"variables": []any{Obj{"name": "device", "value": "{{$random.uuid}}"}}}}}}
	values, err := mergedCollectionVariables(collection, snapshot, nil, newEnvironmentRuleResolver(snapshot))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(values["device"]) {
		t.Fatal("caller provenance suppressed actual random generation", err)
	}
	if obj(arr(input["variables"])[0])["generation_rule"] != "{{$random.uuid}}" {
		t.Fatal("save validation mutated request")
	}
}

func TestEnvironmentRandomCharacterLengthBoundaries(t *testing.T) {
	for _, tc := range []struct {
		rule, pattern string
		length        int
	}{
		{"{{$random.hex(1)}}", `^[0-9a-f]$`, 1},
		{"{{$random.hex(256)}}", `^[0-9a-f]{256}$`, 256},
		{"{{$random.alnum(1)}}", `^[A-Za-z0-9]$`, 1},
		{"{{$random.digits(1)}}", `^[0-9]$`, 1},
	} {
		vars, err := mergedCollectionVariables(nil, Obj{"base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "identity", "value": tc.rule}}}, nil)
		if err != nil || len(vars["identity"]) != tc.length || !regexp.MustCompile(tc.pattern).MatchString(vars["identity"]) {
			t.Fatalf("invalid generated boundary for %s: %v", tc.rule, err)
		}
	}
}

// The project route must honor collection/explicit precedence before resolving.
func TestMySQLEnvironmentRandomProjectHTTPMergeAndValidation(t *testing.T) {
	p, _ := mysqlFixture(t)
	seen := make(chan string, 3)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		id := r.Header.Get("X-Identity")
		if r.URL.Query().Get("id") != id || string(body) != id+"|"+id {
			t.Error("project HTTP merge did not share one effective generated identity")
		}
		seen <- id
		w.WriteHeader(204)
	}))
	defer target.Close()
	p.Config.proxyTestResolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.123.0.1")}}, nil
	}
	p.Config.proxyTestDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}
	env, err := p.workspaceCreate("environments", Obj{"name": "project-random-http", "base_host": "http://selected.internal", "variables": []any{Obj{"name": "identity", "value": "{{$random.uuid}}"}}})
	if err != nil {
		t.Fatal(err)
	}
	operation := Obj{"parameters": []any{Obj{"in": "query", "name": "id", "example": "{{identity}}"}, Obj{"in": "header", "name": "X-Identity", "example": "{{identity}}"}}}
	rawImportBody(operation, "{{identity}}|{{identity}}", "text/plain")
	collection, err := p.workspaceCreate("api_collections", Obj{"name": "project-random-http", "document": Obj{"openapi": "3.1.0", "paths": Obj{"/ping": Obj{"post": operation}}}, "client_state": Obj{collectionOverridesKey: Obj{str(env["id"]): Obj{"variables": []any{Obj{"name": "identity", "value": "{{$random.hex(32)}}"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	request := Obj{"target_source": "project_environment", "path": "/ping", "method": "POST", "environment_id": env["id"]}
	for i := 0; i < 2; i++ {
		result, err := p.collectionDebug(context.Background(), str(collection["id"]), request)
		if err != nil || number(result["status"]) != 204 {
			t.Fatal("project HTTP debug", err)
		}
	}
	if len(seen) != 2 {
		t.Fatal("project HTTP operation count changed")
	}
	first, second := <-seen, <-seen
	if first == second || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(first) || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(second) {
		t.Fatal("project HTTP identity invalid or reused across operations")
	}
	request["variables"] = Obj{"identity": "{{$random.digits(12)}}"}
	if _, err := p.collectionDebug(context.Background(), str(collection["id"]), request); err != nil {
		t.Fatal(err)
	}
	if id := <-seen; !regexp.MustCompile(`^[0-9]{12}$`).MatchString(id) {
		t.Fatal("explicit HTTP identity did not override collection rule")
	}
	request["variables"] = Obj{"identity": "prefix{{$random.uuid}}"}
	if _, err := p.collectionDebug(context.Background(), str(collection["id"]), request); err == nil || len(seen) != 0 {
		t.Fatal("invalid explicit rule reached HTTP target")
	}
}

// Historical snapshot reuse followed by a fixed property override must remove
// old rule provenance, or a later copy would resurrect the discarded generator.
func TestEnvironmentRandomHistoricalFixedOverrideClearsAuthoredRule(t *testing.T) {
	historical := Obj{"name": "historical", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "device", "value": "old-generated", "generation_rule": "{{$random.hex(16)}}"}}, "task_property_rules": Obj{"device": "{{$random.hex(16)}}"}}
	fixed := Obj{"device": "chosen-fixed"}
	snapshot, _, err := environmentSnapshot(historical, fixed)
	if err != nil {
		t.Fatal(err)
	}
	props, err := frozenTaskEnvironmentProperties(fixed, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	draft := runCopyDraft(Obj{"name": "copied", "properties": props, "task_property_keys": []any{"device"}, "environment_snapshot": snapshot})
	if obj(draft["properties"])["device"] != "chosen-fixed" || len(obj(snapshot["task_property_rules"])) != 0 {
		t.Fatal("fixed override retained historical authored generator")
	}
}

func TestUnknownVariableDeclarationsSaveWithoutExecution(t *testing.T) {
	for _, value := range []string{"{{$auto.future}}", "{{$random.future(16)}}"} {
		body := Obj{"name": "opaque", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "custom", "value": value}}}
		validated, err := validateEnvironment(body)
		if err != nil {
			t.Fatal("unknown declaration blocked save", err)
		}
		if obj(arr(validated["variables"])[0])["value"] != value {
			t.Fatal("saved declaration rewritten")
		}
		client := Obj{collectionOverridesKey: Obj{"environment-test": Obj{"variables": body["variables"]}}}
		if err := validateCollectionOverrides(client); err != nil {
			t.Fatal("unknown collection declaration blocked save", err)
		}
		if _, _, err := environmentSnapshot(body); err == nil {
			t.Fatal("unknown declaration executed")
		}
		collection := Obj{"client_state": client}
		if _, err := mergedCollectionVariables(collection, Obj{"id": "environment-test", "base_host": "https://fixture.invalid"}, nil); err == nil {
			t.Fatal("unknown collection declaration executed")
		}
		if _, err := frozenTaskEnvironmentProperties(Obj{"custom": value}, Obj{}); err == nil {
			t.Fatal("unknown task declaration executed")
		}
		if obj(arr(body["variables"])[0])["value"] != value {
			t.Fatal("declaration rewritten")
		}
	}
}
