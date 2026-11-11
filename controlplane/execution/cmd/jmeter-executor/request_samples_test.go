package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func TestRequestCaptureInjectionPreservesRuntimePlanAndIsFirst(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?><jmeterTestPlan><hashTree><TestPlan testname="original"><stringProp name="extra"><![CDATA[keep & bytes]]></stringProp></TestPlan><hashTree><ThreadGroup testname="existing"/><hashTree/></hashTree></hashTree></jmeterTestPlan>`)
	task := protocol.Task{TaskID: "task", AttemptID: "attempt"}
	got, err := injectRequestCapture(raw, task, "owner-key")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`<![CDATA[keep & bytes]]>`)) || !bytes.Contains(got, []byte(`<ThreadGroup testname="existing"/>`)) {
		t.Fatal("injection rewrote original plan bytes")
	}
	if bytes.Index(got, []byte("<JSR223Listener")) > bytes.Index(got, []byte("<ThreadGroup")) {
		t.Fatal("collector follows result-stripping listeners")
	}
	dec := xml.NewDecoder(bytes.NewReader(got))
	count := 0
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "JSR223Listener" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("collector was not injected exactly once")
	}
	if _, err = injectRequestCapture(got, task, "owner-key"); err == nil {
		t.Fatal("duplicate collector accepted")
	}
	if _, err = injectRequestCapture(raw, protocol.Task{TaskID: "../unsafe", AttemptID: "attempt"}, "owner-key"); err == nil {
		t.Fatal("unsafe capture identity accepted")
	}
}

func TestRequestCaptureOwnerGenerationAndEmptyEvidence(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	w := &worker{s: &service{c: config{id: "worker", work: filepath.Join(base, "work"), state: filepath.Join(base, "state")}}}
	a := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 3}
	if err := w.initializeRequestCapture(a); err != nil {
		t.Fatal(err)
	}
	samples, captured, truncated := w.collectRequestSamples(a)
	if !captured || truncated || len(samples) != 0 {
		t.Fatal("prepared empty sample file was treated as unknown")
	}
	foreign := a
	foreign.Generation = 4
	if _, captured, _ = w.collectRequestSamples(foreign); captured {
		t.Fatal("different generation inherited sample file")
	}
	foreign = a
	foreign.AttemptID = "other"
	if _, captured, _ = w.collectRequestSamples(foreign); captured {
		t.Fatal("different attempt inherited samples")
	}
	w.captureEvidence(a, "2026-11-07T00:00:00Z", nil)
	saved := w.savedEvidence(a)
	if saved == nil || !saved.RequestSamplesCaptured || saved.Status != "COLLECTED" {
		t.Fatal("empty captured receipt did not survive durable journal")
	}
}

func TestRequestSamplesBudgetsPreserveNullAndGlobalEvidenceBound(t *testing.T) {
	text := strings.Repeat("<日志>\x00", 2000)
	empty := ""
	sample := protocol.RequestSample{Label: "label", SampleKind: "unknown", URL: &text, RequestParams: &empty, ResponseBody: &text, AssertionsCaptured: true, Assertions: []protocol.RequestAssertion{{Name: "assertion", Message: text}}}
	many := make([]protocol.RequestSample, 100)
	for i := range many {
		many[i] = sample
		many[i].Label = fmt.Sprintf("label-%d", i)
	}
	bounded, truncated := boundRequestSamples(many)
	raw, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(bounded) > 64 || len(raw) > 128<<10 {
		t.Fatal("per-worker sample capture is unbounded")
	}
	if bounded[0].Method != nil || bounded[0].RequestParams == nil || *bounded[0].RequestParams != "" {
		t.Fatal("null and captured-empty collapsed")
	}
	evidence := make([]protocol.NodeEvidence, 64)
	for i := range evidence {
		evidence[i] = protocol.NodeEvidence{RequestSamples: bounded, RequestSamplesCaptured: true, Logs: []protocol.NodeLog{{Name: "jmeter.log", Content: text, Status: "COLLECTED", Size: int64(len(text))}}}
	}
	got := boundedNodeEvidence(evidence)
	raw, err = json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 2<<20 {
		t.Fatalf("node evidence exceeded global encoded bound: %d", len(raw))
	}
	anyTruncated := false
	for _, e := range got {
		anyTruncated = anyTruncated || e.RequestSamplesTruncated
	}
	if !anyTruncated {
		t.Fatal("aggregate sample loss not explicit")
	}
}

func TestRequestSamplesDiscardUnnamedResultsAndKeepFollowingSamples(t *testing.T) {
	input := []protocol.RequestSample{
		{Label: "", TimestampMS: 1000},
		{Label: "named", TimestampMS: 2000},
		{Label: "", TimestampMS: 3000},
		{Label: " ", TimestampMS: 4000},
	}
	got, truncated := boundRequestSamples(input)
	if !truncated || len(got) != 2 || got[0].Label != "named" || got[1].Label != " " || got[1].TimestampMS != 4000 {
		t.Fatal("unnamed results must be disclosed and skipped without losing later valid samples")
	}
	if input[0].Label != "" || input[1].TimestampMS != 2000 {
		t.Fatal("bounding rewrote the original capture")
	}
}

func TestRequestSampleValidationRejectsUnnamedResults(t *testing.T) {
	if validateRequestSamples(protocol.NodeEvidence{Status: "COLLECTED", RequestSamplesCaptured: true, RequestSamples: []protocol.RequestSample{{Label: ""}}}) {
		t.Fatal("executor accepted a sample that the manager rejects")
	}
}

func TestOfficialJMeterRequestCaptureSkipsUnnamedResults(t *testing.T) {
	const end = "</hashTree></hashTree></hashTree></jmeterTestPlan>"
	unnamed := `<JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">SampleResult.setSampleLabel(''); SampleResult.setResponseData('synthetic unnamed result', 'UTF-8'); SampleResult.setResponseCode('200')</stringProp></JSR223Sampler><hashTree/>`
	plan := strings.TrimSuffix(officialCapturePlan, end) + unnamed + end
	runOfficialRequestCapture(t, plan)
}

func TestOfficialJMeterRequestCaptureAfterAssertionsBeforeStripping(t *testing.T) {
	runOfficialRequestCapture(t, officialCapturePlan)
}

func TestOfficialJMeterRequestCaptureMasksManagedScalarValues(t *testing.T) {
	// A generated variable may have a neutral name; its opaque property must
	// still mask matching payload text even when normal name matching cannot.
	plan := strings.ReplaceAll(officialCapturePlan, "jmeter.env.api.111.222", "jmeter.env.scalar.0123456789abcdef.fedcba9876543210")
	runOfficialRequestCapture(t, plan)
}

func runOfficialRequestCapture(t *testing.T, source string) {
	t.Helper()
	if os.Getenv("JMETER_REQUEST_CAPTURE_IT") != "1" {
		t.Skip("enable official-runtime synthetic capture test")
	}
	home, err := filepath.Abs("../../../../runtime/toolcache/apache-jmeter-5.6.3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(home, "bin", "ApacheJMeter.jar")); err != nil {
		t.Fatal("official JMeter unavailable")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	work := filepath.Join(base, "work")
	engine := filepath.Join(work, "engine")
	if err = directory(engine); err != nil {
		t.Fatal(err)
	}
	w := &worker{s: &service{c: config{id: "worker", work: work, state: filepath.Join(base, "state")}}}
	a := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 3}
	if err = w.initializeRequestCapture(a); err != nil {
		t.Fatal(err)
	}
	plan, err := injectRequestCapture([]byte(source), protocol.Task{TaskID: a.TaskID, AttemptID: a.AttemptID}, evidenceOwnerKey(a))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "plan.jmx")
	if err = os.WriteFile(path, plan, 0600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(base, "unrelated", "hsperfdata_jmeter")
	if err = directory(foreign); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "java", "-Djava.awt.headless=true", "-Duser.dir="+foreign, "-Dpressurecooker.capture.tasks_root="+filepath.Join(work, "tasks"), "-jar", filepath.Join(home, "bin", "ApacheJMeter.jar"), "-n", "-Jsampleresult.timestamp.start=false", "-Jjmeter.save.saveservice.timestamp_format=ms", "-t", path, "-j", filepath.Join(base, "jmeter.log"), "-l", filepath.Join(base, "results.jtl"))
	cmd.Dir = foreign
	if _, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("official synthetic runtime failed: %v", err)
	}
	var binding requestCaptureBinding
	if err = load(filepath.Join(w.requestCaptureDir(a), "binding.json"), &binding); err != nil {
		t.Fatal(err)
	}
	var rawCapture requestCaptureFile
	if err = load(filepath.Join(w.requestCaptureDir(a), binding.File), &rawCapture); err != nil {
		t.Fatal(err)
	}
	for _, row := range rawCapture.Samples {
		if row.Label == "" {
			t.Fatal("listener persisted an unnamed sample that the manager rejects")
		}
	}
	samples, captured, truncated := w.collectRequestSamples(a)
	if !captured || !truncated || len(samples) != 11 {
		t.Fatalf("official collector counts: captured=%v truncated=%v samples=%d", captured, truncated, len(samples))
	}
	csvFile, err := os.Open(filepath.Join(base, "results.jtl"))
	if err != nil {
		t.Fatal(err)
	}
	csvRows, err := csv.NewReader(csvFile).ReadAll()
	csvFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	indexes := map[string]int{}
	for i, name := range csvRows[0] {
		indexes[name] = i
	}
	firstEnds := map[string]int64{}
	for _, row := range csvRows[1:] {
		key := row[indexes["label"]] + row[indexes["success"]]
		if _, ok := firstEnds[key]; !ok {
			end, _ := strconv.ParseInt(row[indexes["timeStamp"]], 10, 64)
			firstEnds[key] = end
		}
	}
	successes, failures, emptyCount, queryCount, clippedCount, opaqueCount, dottedCount, formXMLCount := 0, 0, 0, 0, 0, 0, 0, 0
	for _, s := range samples {
		if end, ok := firstEnds[s.Label+strconv.FormatBool(s.Success)]; ok && s.SampleKind != "transaction" && s.TimestampMS+s.ElapsedMS != end {
			t.Fatalf("sample timestamp did not use canonical start under end-stamped CSV: label=%s elapsed=%d difference=%d", s.Label, s.ElapsedMS, s.TimestampMS+s.ElapsedMS-end)
		}
		if s.Label == "Payload" {
			if s.Success {
				successes++
			} else {
				failures++
			}
			if s.ResponseBody == nil || !strings.Contains(*s.ResponseBody, "visible") || strings.Contains(*s.ResponseBody, "runtime-known-secret") || strings.Contains(*s.ResponseBody, "password-value") {
				t.Fatal("response stripped too early or secret remained")
			}
			if !json.Valid([]byte(*s.ResponseBody)) {
				t.Fatal("redaction destroyed captured JSON structure")
			}
			if !s.AssertionsCaptured || len(s.Assertions) != 1 || s.Assertions[0].Failure == s.Success {
				t.Fatal("collector ran before actual assertion results")
			}
			if s.URL == nil || strings.Contains(*s.URL, "userinfo-secret") || strings.Contains(*s.URL, "query-secret") {
				t.Fatal("URL credentials leaked")
			}
		} else if s.Label == "Empty" {
			emptyCount++
			if s.ResponseBody == nil || *s.ResponseBody != "" || s.RequestParams != nil || !s.AssertionsCaptured || len(s.Assertions) != 0 {
				t.Fatalf("empty/missing/assertion-none distinction failed: body_nil=%v params_nil=%v assertions_captured=%v assertion_count=%d", s.ResponseBody == nil, s.RequestParams == nil, s.AssertionsCaptured, len(s.Assertions))
			}
		}
		if s.Label == "Query" {
			queryCount++
			if s.RequestParams == nil || !strings.Contains(*s.RequestParams, "q=visible") || strings.Contains(*s.RequestParams, "query-secret") {
				t.Fatal("actual GET query parameters missing or unsafe")
			}
		}
		if s.Label == "Clip" {
			clippedCount++
			if s.ResponseBody == nil || !s.Truncated || strings.Contains(*s.ResponseBody, "runtime-") {
				t.Fatal("known secret prefix survived clipping")
			}
		}
		if strings.HasPrefix(s.Label, "Opaque") {
			opaqueCount++
			if s.ResponseBody == nil || !s.Truncated || strings.Contains(*s.ResponseBody, "api-o") || strings.Contains(*s.ResponseBody, "var-o") {
				t.Fatal("opaque environment secret prefix survived clip")
			}
		}
		if s.Label == "DottedApiKey" {
			dottedCount++
			if s.ResponseBody == nil || !s.Truncated || strings.Contains(*s.ResponseBody, "dot-a") {
				t.Fatal("api.key credential prefix survived clipping")
			}
		}
		if s.Label == "FormXML" {
			formXMLCount++
			if s.RequestParams == nil || strings.Contains(*s.RequestParams, "form-private") || !strings.Contains(*s.RequestParams, "ordinary=visible") || s.ResponseBody == nil || strings.Contains(*s.ResponseBody, "xml-private") || strings.Contains(*s.ResponseBody, "ws-private") {
				t.Fatal("encodedform/XML/websocket credential mask failed")
			}
		}
	}
	if successes != 1 || failures != 1 || emptyCount != 1 || queryCount != 1 || clippedCount != 1 || opaqueCount != 2 || dottedCount != 1 || formXMLCount != 1 {
		t.Fatal("first success/failure sampling policy failed")
	}
}

const officialCapturePlan = `<?xml version="1.0"?><jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree><TestPlan guiclass="TestPlanGui" testclass="TestPlan" testname="Capture fixture" enabled="true"><elementProp name="TestPlan.user_defined_variables" elementType="Arguments"><collectionProp name="Arguments.arguments"/></elementProp></TestPlan><hashTree><ThreadGroup guiclass="ThreadGroupGui" testclass="ThreadGroup" testname="Synthetic only" enabled="true"><stringProp name="ThreadGroup.on_sample_error">continue</stringProp><elementProp name="ThreadGroup.main_controller" elementType="LoopController"><boolProp name="LoopController.continue_forever">false</boolProp><stringProp name="LoopController.loops">3</stringProp></elementProp><stringProp name="ThreadGroup.num_threads">1</stringProp><stringProp name="ThreadGroup.ramp_time">0</stringProp></ThreadGroup><hashTree><TransactionController guiclass="TransactionControllerGui" testclass="TransactionController" testname="Parent transaction" enabled="true"><boolProp name="TransactionController.parent">true</boolProp><boolProp name="TransactionController.includeTimers">false</boolProp></TransactionController><hashTree><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="Payload" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="cacheKey">true</stringProp><stringProp name="script"><![CDATA[vars.put('access_token','runtime-known-secret'); Thread.sleep(20); SampleResult.setURL(new URL('https://user:userinfo-secret@fixture.invalid/path?token=query-secret&q=visible')); SampleResult.setSamplerData('{"password":"password-value","q":"visible"}'); SampleResult.setResponseData('{"nested":{"password":"password-value"},"echo":"runtime-known-secret","ok":"visible"}', 'UTF-8'); SampleResult.setResponseCode('200')]]></stringProp></JSR223Sampler><hashTree><JSR223Assertion guiclass="TestBeanGUI" testclass="JSR223Assertion" testname="Actual assertion" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="cacheKey">true</stringProp><stringProp name="script"><![CDATA[if (vars.getIteration() > 1) { AssertionResult.setFailure(true); AssertionResult.setFailureMessage('assertion token=runtime-known-secret') }]]></stringProp></JSR223Assertion><hashTree/><JSR223Listener guiclass="TestBeanGUI" testclass="JSR223Listener" testname="Subsequent stripping sentinel" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">sampleResult.setResponseData(new byte[0])</stringProp></JSR223Listener><hashTree/></hashTree></hashTree><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="Empty" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">SampleResult.setSamplerData(null); SampleResult.setResponseData('', 'UTF-8'); SampleResult.setResponseCode('200')</stringProp></JSR223Sampler><hashTree/><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="Query" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">SampleResult.setSamplerData(null); SampleResult.setURL(new URL('https://fixture.invalid/path?token=query-secret&amp;q=visible')); SampleResult.setResponseData('', 'UTF-8'); SampleResult.setResponseCode('200')</stringProp></JSR223Sampler><hashTree/><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="Clip" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">SampleResult.setSamplerData(null); SampleResult.setResponseData(('x'*8188)+vars.get('access_token')+'_visible', 'UTF-8'); SampleResult.setResponseCode('200')</stringProp></JSR223Sampler><hashTree/><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="OpaqueApi" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">props.setProperty('jmeter.env.api.111.222','api-opaque-hidden-value'); SampleResult.setSamplerData(null); SampleResult.setResponseData(('x'*8187)+props.getProperty('jmeter.env.api.111.222')+'_visible', 'UTF-8'); SampleResult.setResponseCode('200')</stringProp></JSR223Sampler><hashTree/><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="OpaqueVar" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">props.setProperty('jmeter.env.var.333','var-opaque-hidden-value'); SampleResult.setSamplerData(null); SampleResult.setResponseData(('x'*8187)+props.getProperty('jmeter.env.var.333')+'_visible', 'UTF-8'); SampleResult.setResponseCode('200')</stringProp></JSR223Sampler><hashTree/><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="DottedApiKey" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script">props.setProperty('api.key','dot-api-hidden-value'); SampleResult.setSamplerData(null); SampleResult.setResponseData(('x'*8187)+props.getProperty('api.key')+'_visible', 'UTF-8'); SampleResult.setResponseCode('200')</stringProp></JSR223Sampler><hashTree/><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="FormXML" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script"><![CDATA[SampleResult.setSamplerData('POST data:\n%74%6f%6b%65%6e=form-private&ordinary=visible\n[no cookies]'); SampleResult.setResponseData('<root><auth:token>xml-private</auth:token><ordinary>wss://user:ws-private@fixture.invalid/visible</ordinary></root>', 'UTF-8'); SampleResult.setResponseCode('200')]]></stringProp></JSR223Sampler><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`

func TestJVMProvidesSupervisorConfiguredCaptureRoot(t *testing.T) {
	args := (config{work: "/supervisor/custom-worker"}).baseArgs()
	want := "-Dpressurecooker.capture.tasks_root=/supervisor/custom-worker/tasks"
	found := false
	for _, arg := range args {
		if arg == want {
			found = true
		}
	}
	if !found {
		t.Fatal("JVM did not receive the supervisor-configured absolute capture root")
	}
}
