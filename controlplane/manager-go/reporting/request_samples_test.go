package reporting

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

type requestSampleTestAssertion struct {
	Name           string
	Failure, Error bool
	Message        string
}
type requestSampleTestView struct {
	ID                 string `json:"id"`
	NodeID             string `json:"node_id"`
	StepKey            string `json:"step_key"`
	Label              string
	SampleKind         string `json:"sample_kind"`
	TimestampMS        int64  `json:"timestamp_ms"`
	ElapsedMS          int64  `json:"elapsed_ms"`
	Success            bool
	Method             *string
	URL                *string
	RequestParams      *string `json:"request_params"`
	ResponseCode       *string `json:"response_code"`
	ResponseBody       *string `json:"response_body"`
	AssertionsCaptured bool    `json:"assertions_captured"`
	Assertions         []requestSampleTestAssertion
	Truncated          bool
	SourceKind         string `json:"source_kind"`
	SourceName         string `json:"source_name"`
}

func requestSampleViews(t *testing.T, a Analysis) ([]requestSampleTestView, bool) {
	t.Helper()
	b, e := json.Marshal(a)
	if e != nil {
		t.Fatal(e)
	}
	var out struct {
		RequestSamples []requestSampleTestView `json:"request_samples"`
		Truncated      bool                    `json:"request_samples_truncated"`
	}
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out.RequestSamples, out.Truncated
}
func requestCSV(t *testing.T, rows [][]string) string {
	t.Helper()
	var b strings.Builder
	w := csv.NewWriter(&b)
	if e := w.WriteAll(rows); e != nil {
		t.Fatal(e)
	}
	return b.String()
}
func TestRequestSamplesCSVFirstSuccessFailureAndCapturedEmpty(t *testing.T) {
	raw := requestCSV(t, [][]string{
		{"timeStamp", "elapsed", "label", "success", "engine_id", "method", "URL", "request_params", "response_body", "responseCode", "assertionResults", "failureMessage"},
		{"1000", "10", "login", "true", "w", "POST", "https://svc/login?token=private-query", "{\"user\":\"visible\",\"password\":\"private-password\"}", "", "200", "[]", ""},
		{"1100", "10", "login", "true", "w", "POST", "https://svc/second", "later", "later", "200", "[]", ""},
		{"1200", "20", "login", "false", "w", "POST", "https://svc/login", "", "{\"nested\":{\"access_token\":\"private-access\",\"visible\":true}}", "500", "", "expected condition failed"},
	})
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"w"}})
	if e != nil {
		t.Fatal(e)
	}
	samples, truncated := requestSampleViews(t, a)
	if len(samples) != 2 || truncated {
		t.Fatalf("first success/failure samples missing: count=%d truncated=%t", len(samples), truncated)
	}
	first, failed := samples[0], samples[1]
	if !first.Success || first.TimestampMS != 1000 || first.ResponseBody == nil || *first.ResponseBody != "" || !first.AssertionsCaptured || first.Assertions == nil || len(first.Assertions) != 0 {
		t.Fatalf("captured-empty evidence lost: %+v", first)
	}
	if failed.Success || failed.TimestampMS != 1200 || !failed.AssertionsCaptured || len(failed.Assertions) != 1 || !failed.Assertions[0].Failure || failed.Assertions[0].Message != "expected condition failed" {
		t.Fatalf("explicit failed assertion lost: %+v", failed)
	}
	if first.NodeID != "w" || first.StepKey != a.Steps[0].StepKey || first.SourceKind != "jtl" || first.SourceName != "results.jtl" {
		t.Fatalf("CSV evidence unscoped: %+v", first)
	}
	encoded, _ := json.Marshal(samples)
	for _, secret := range []string{"private-query", "private-password", "private-access"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sample leaked %q", secret)
		}
	}
}
func TestRequestSamplesCSVAbsentFieldsDoNotInventAssertionsOrBodies(t *testing.T) {
	a, e := ParseJTL(context.Background(), strings.NewReader("timeStamp,elapsed,label,success,engine_id\n1000,0,a,true,w\n"), ParseOptions{})
	if e != nil {
		t.Fatal(e)
	}
	samples, _ := requestSampleViews(t, a)
	if len(samples) != 1 || samples[0].URL != nil || samples[0].Method != nil || samples[0].RequestParams != nil || samples[0].ResponseBody != nil || samples[0].ResponseCode != nil || samples[0].AssertionsCaptured || len(samples[0].Assertions) != 0 {
		t.Fatalf("historical evidence fabricated: %+v", samples)
	}
}

// fmt and utf8 are also used by boundary regressions added alongside the API.
var _ = fmt.Sprintf
var _ = utf8.ValidString

func requestText(s string) *string { return &s }
func requestAnalysisFixture() Analysis {
	key := StableStepKey("unknown", "login")
	return Analysis{Nodes: []Node{{NodeID: "w1"}, {NodeID: "w2"}}, Steps: []Step{{StepKey: key, Label: "login", SampleKind: "unknown"}}, NodeSteps: []Step{{NodeID: "w1", StepKey: key, Label: "login", SampleKind: "unknown"}}}
}
func TestRequestSampleSanitizeRecursiveSecretsBeforeClippingAndIdentity(t *testing.T) {
	secret := "private-credential-that-crosses-the-clipping-boundary"
	original := RequestSample{NodeID: "w1", Label: "/login?token=private-label", SampleKind: "unknown", URL: requestText("https://user:private-userinfo@svc/login?ordinary=visible&access_token=private-query"), RequestParams: requestText(`{"nested":[{"access_token":"private-access","safe":"visible","secrets":{"a":"private-map"}}],"literal":"credential<hidden>"}`), ResponseBody: requestText(strings.Repeat("x", 8180) + secret), AssertionsCaptured: true, Assertions: []RequestAssertion{{Name: "check", Message: "Authorization: Bearer private-header\n folded-private-header"}}}
	s := SanitizeRequestSample(original, NewRedactor([]string{secret, "credential<hidden>"}))
	if s.NodeID != "w1" || s.StepKey != StableStepKey("unknown", original.Label) || s.ResponseBody == nil || len(*s.ResponseBody) > 8192 {
		t.Fatalf("identity or body cap wrong: %+v", s)
	}
	encoded, _ := json.Marshal(s)
	for _, v := range []string{"private-label", "private-userinfo", "private-query", "private-access", "private-map", "credential", "private-credential", "private-header", "folded-private-header"} {
		if strings.Contains(string(encoded), v) {
			t.Fatalf("payload secret leaked: %q", v)
		}
	}
	if s.RequestParams == nil || !strings.Contains(*s.RequestParams, "visible") || !strings.Contains(*original.ResponseBody, secret) || original.Assertions[0].Message != "Authorization: Bearer private-header\n folded-private-header" {
		t.Fatal("safe payload fields lost or caller input mutated")
	}
	trusted := SanitizeRequestSample(RequestSample{NodeID: "trusted-node", StepKey: "server-computed-key", Label: "redacted label", SampleKind: "unknown"}, func(s string) string { return "masked" })
	if trusted.NodeID != "trusted-node" || trusted.StepKey != "server-computed-key" {
		t.Fatal("sanitization changed server identities")
	}
}
func TestRequestSampleSanitizeQueryFallbackUTF8AndAssertionsBounds(t *testing.T) {
	s := SanitizeRequestSample(RequestSample{NodeID: "w1", Label: "login", SampleKind: "unknown", URL: requestText("https://svc/p?ordinary=visible&access_token=private-form"), ResponseBody: requestText(strings.Repeat("界", 4000)), AssertionsCaptured: true, Assertions: make([]RequestAssertion, 17)}, nil)
	if s.RequestParams == nil || !strings.Contains(*s.RequestParams, "ordinary=visible") || strings.Contains(*s.RequestParams, "private-form") || s.ResponseBody == nil || len(*s.ResponseBody) > 8192 || !utf8.ValidString(*s.ResponseBody) || !s.Truncated || len(s.Assertions) != 16 {
		t.Fatalf("query or byte/assertion limits wrong: %+v", s)
	}
	for i := range s.Assertions {
		s.Assertions[i] = RequestAssertion{Name: strings.Repeat("界", 60), Message: strings.Repeat("界", 200)}
	}
	s = SanitizeRequestSample(s, nil)
	for _, v := range s.Assertions {
		if len(v.Name) > 128 || len(v.Message) > 512 || !utf8.ValidString(v.Name) || !utf8.ValidString(v.Message) {
			t.Fatal("assertion UTF8 byte bound exceeded")
		}
	}
}
func TestMergeRequestSamplesMatchesNodeStepAndPrefersSameSampleExecutor(t *testing.T) {
	a := requestAnalysisFixture()
	key := a.Steps[0].StepKey
	old := RequestSample{NodeID: "w1", StepKey: key, Label: "login", SampleKind: "unknown", TimestampMS: 1000, ElapsedMS: 10, Success: true, URL: requestText("https://svc/p"), SourceKind: "jtl", SourceName: "results.jtl"}
	a.RequestSamples = []RequestSample{old}
	richer := old
	richer.SourceKind = "worker_sample"
	richer.SourceName = "request-samples.json"
	richer.ResponseBody = requestText("")
	richer.AssertionsCaptured = true
	richer.Assertions = []RequestAssertion{}
	wrongNode := richer
	wrongNode.NodeID = "w2" // w2 exists, but this node+step pair was never sampled.
	wrongStep := richer
	wrongStep.StepKey = StableStepKey("unknown", "other")
	later := richer
	later.TimestampMS = 1100
	later.ResponseBody = requestText("later response")
	MergeRequestSamples(&a, []RequestSample{richer, wrongNode, wrongStep, later}, nil)
	if len(a.RequestSamples) != 1 {
		t.Fatalf("foreign or repeated status evidence attached: %+v", a.RequestSamples)
	}
	got := a.RequestSamples[0]
	if got.SourceKind != "worker_sample" || got.ResponseBody == nil || *got.ResponseBody != "" || got.URL == nil || *got.URL != "https://svc/p" || !got.AssertionsCaptured || got.Assertions == nil || got.StepKey != key {
		t.Fatalf("richer/captured-empty same sample discarded: %+v", got)
	}
	failure := richer
	failure.TimestampMS = 1200
	failure.Success = false
	failure.Assertions = []RequestAssertion{{Name: "failed", Failure: true, Message: "actual assertion"}}
	MergeRequestSamples(&a, []RequestSample{failure}, nil)
	if len(a.RequestSamples) != 2 || a.RequestSamples[1].Success {
		t.Fatalf("first failure lost: %+v", a.RequestSamples)
	}
}
func TestMergeRequestSamplesEncodedBudgetsAndFlags(t *testing.T) {
	a := Analysis{Nodes: []Node{{NodeID: "w"}}}
	incoming := []RequestSample{}
	for i := 0; i < 65; i++ {
		label := fmt.Sprintf("step-%d", i)
		key := StableStepKey("unknown", label)
		a.NodeSteps = append(a.NodeSteps, Step{NodeID: "w", StepKey: key})
		incoming = append(incoming, RequestSample{NodeID: "w", StepKey: key, Label: label, SampleKind: "unknown", TimestampMS: int64(i), Success: true})
	}
	MergeRequestSamples(&a, incoming, nil)
	if len(a.RequestSamples) != 64 || !a.RequestSamplesTruncated {
		t.Fatalf("64/node cap ignored: count=%d truncated=%t", len(a.RequestSamples), a.RequestSamplesTruncated)
	}
	large := Analysis{Nodes: []Node{{NodeID: "w"}}}
	for _, v := range incoming {
		v.ResponseBody = requestText(strings.Repeat("<", 8192))
		large.NodeSteps = append(large.NodeSteps, Step{NodeID: "w", StepKey: v.StepKey})
		large.RequestSamples = append(large.RequestSamples, v)
	}
	MergeRequestSamples(&large, nil, nil)
	b, e := json.Marshal(large.RequestSamples)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) > 128<<10 || !large.RequestSamplesTruncated {
		t.Fatalf("encoded HTML escaping exceeded node budget: bytes=%d", len(b))
	}
	all := Analysis{}
	batch := []RequestSample{}
	for n := 0; n < 20; n++ {
		node := fmt.Sprintf("n%d", n)
		all.Nodes = append(all.Nodes, Node{NodeID: node})
		for j := 0; j < 10; j++ {
			label := fmt.Sprintf("step%d", j)
			key := StableStepKey("unknown", label)
			all.NodeSteps = append(all.NodeSteps, Step{NodeID: node, StepKey: key})
			batch = append(batch, RequestSample{NodeID: node, StepKey: key, Label: label, SampleKind: "unknown", ResponseBody: requestText(strings.Repeat("x", 8192)), RequestParams: requestText(strings.Repeat("x", 4096)), TimestampMS: int64(j), Success: true})
		}
	}
	MergeRequestSamples(&all, batch, nil)
	b, e = json.Marshal(all.RequestSamples)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) > 2<<20 || !all.RequestSamplesTruncated {
		t.Fatalf("total encoded budget not enforced: bytes=%d", len(b))
	}
}

func TestRequestSamplesCSVAssertionStatesAndAliases(t *testing.T) {
	for _, tc := range []struct {
		name, raw                          string
		captured                           bool
		count                              int
		failure, assertionError, truncated bool
	}{
		{name: "unknown", raw: ""}, {name: "null", raw: "null"}, {name: "none", raw: "[]", captured: true},
		{name: "pass", raw: `[{"name":"ok","failure":false,"error":false,"message":""}]`, captured: true, count: 1},
		{name: "fail", raw: `[{"name":"bad","failure":true,"error":false,"message":"bad assertion"}]`, captured: true, count: 1, failure: true},
		{name: "error", raw: `[{"name":"error","failure":false,"error":true,"message":"runtime error"}]`, captured: true, count: 1, assertionError: true},
		{name: "invalid", raw: `not-json`, truncated: true}, {name: "missing flags", raw: `[{"name":"unknown"}]`, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := requestCSV(t, [][]string{{"timeStamp", "elapsed", "label", "success", "engine_id", "url", "requestParams", "responseData", "assertionResults"}, {"1000", "0", "a", "true", "w", "https://svc/p", "", "", tc.raw}})
			a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if len(a.RequestSamples) != 1 {
				t.Fatal("sample missing")
			}
			s := a.RequestSamples[0]
			if s.AssertionsCaptured != tc.captured || len(s.Assertions) != tc.count || s.Truncated != tc.truncated || s.RequestParams == nil || *s.RequestParams != "" || s.ResponseBody == nil || *s.ResponseBody != "" || s.URL == nil || *s.URL != "https://svc/p" || a.Metrics.SampleCount != 1 || a.Metrics.SuccessCount != 1 {
				t.Fatalf("assertion state/alias/captured-empty changed: %+v", s)
			}
			if tc.count > 0 && (s.Assertions[0].Failure != tc.failure || s.Assertions[0].Error != tc.assertionError) {
				t.Fatalf("assertion truth fabricated: %+v", s.Assertions)
			}
		})
	}
}
func TestRequestSampleMalformedStructuredPayloadDoesNotLeakTailSecrets(t *testing.T) {
	original := "{\"password\":\"private-start\nprivate-tail-without-closing-quote"
	s := SanitizeRequestSample(RequestSample{SampleKind: "unknown", ResponseBody: requestText(original)}, nil)
	if s.ResponseBody == nil || strings.Contains(*s.ResponseBody, "private-start") || strings.Contains(*s.ResponseBody, "private-tail") || !s.Truncated {
		t.Fatalf("malformed structured secret survived masking: %v truncated=%t", s.ResponseBody, s.Truncated)
	}
}
func TestRequestSampleBodyFormXMLAndNestedKnownSecrets(t *testing.T) {
	for _, body := range []string{
		`access%5Ftoken=private-encoded&ordinary=visible`,
		`<root><access_token>private-xml\ncontinued-xml</access_token><ordinary>visible</ordinary></root>`,
		`{"ordinary":"visible","payload":"{\"password\":\"private-embedded\"}"}`,
	} {
		s := SanitizeRequestSample(RequestSample{ResponseBody: requestText(body)}, nil)
		if s.ResponseBody == nil || strings.Contains(*s.ResponseBody, "private-") || strings.Contains(*s.ResponseBody, "continued-xml") || !strings.Contains(*s.ResponseBody, "visible") {
			t.Fatalf("payload redaction failed for %q => %v", body, s.ResponseBody)
		}
	}
}

func TestRequestSampleQueryAndFormOrdinaryFieldsSurviveRepeatedSanitization(t *testing.T) {
	for _, key := range []string{"password", "cookie", "authorization"} {
		s := RequestSample{URL: requestText("https://svc/p?" + key + "=private-query&q=visible"), RequestParams: requestText(`{"ordinary":"body-visible"}`)}
		for i := 0; i < 2; i++ {
			s = SanitizeRequestSample(s, nil)
			if s.RequestParams == nil || !strings.Contains(*s.RequestParams, "q=visible") || !strings.Contains(*s.RequestParams, "body-visible") || strings.Contains(*s.RequestParams, "private-query") {
				t.Fatalf("query/body fields lost after sanitization %d for %s: %v", i, key, s.RequestParams)
			}
		}
		s = SanitizeRequestSample(RequestSample{RequestParams: requestText(key + "=[REDACTED]&q=visible")}, nil)
		if s.RequestParams == nil || !strings.Contains(*s.RequestParams, "q=visible") {
			t.Fatalf("ordinary form field hidden by remasking: %s", key)
		}
	}
}

func TestRequestSampleKnownNumericCredentialIsMaskedInJSONValues(t *testing.T) {
	s := SanitizeRequestSample(RequestSample{ResponseBody: requestText(`{"ordinary":321654987,"nested":["321654987"]}`)}, NewRedactor([]string{"321654987"}))
	if s.ResponseBody == nil || strings.Contains(*s.ResponseBody, "321654987") {
		t.Fatal("known credential survived JSON scalar decoding")
	}
}

func TestRequestSamplePrefixedSamplerJSONUsesRecursiveMasking(t *testing.T) {
	for _, body := range []string{
		"POST https://svc/p\nPOST data:\n{\"access‒token\":\"private-unicode\",\"ordinary\":\"visible\"}\n[no cookies]",
		"POST data:\n{\"password\":\"private-start\nprivate-tail-without-closing-quote",
	} {
		s := SanitizeRequestSample(RequestSample{RequestParams: requestText(body)}, nil)
		if s.RequestParams == nil || strings.Contains(*s.RequestParams, "private-") {
			t.Fatalf("prefixed JSON bypassed recursive masking: %v", s.RequestParams)
		}
		if strings.Contains(body, "visible") && (!strings.Contains(*s.RequestParams, "visible") || !strings.Contains(*s.RequestParams, "[no cookies]")) {
			t.Fatalf("ordinary sampler payload content lost: %s", *s.RequestParams)
		}
	}
}

func TestRequestSampleUnstructuredDottedAPIKeyIsMasked(t *testing.T) {
	for _, name := range []string{"api.key", "x.api.key", "private.key", "session.id"} {
		s := SanitizeRequestSample(RequestSample{RequestParams: requestText("Request parameters: " + name + "=private-dot-value;q=visible")}, nil)
		if s.RequestParams == nil || strings.Contains(*s.RequestParams, "private-dot-value") || !strings.Contains(*s.RequestParams, "q=visible") {
			t.Fatalf("dotted sensitive key leaked or hid ordinary field: %s", name)
		}
	}
}
func TestMergeRequestSamplesRicherAssertionDoesNotDuplicateCSVFailureProof(t *testing.T) {
	a := requestAnalysisFixture()
	key := a.NodeSteps[0].StepKey
	old := RequestSample{NodeID: "w1", StepKey: key, SampleKind: "unknown", Label: "login", TimestampMS: 1000, ElapsedMS: 10, Success: false, AssertionsCaptured: true, Assertions: []RequestAssertion{{Name: "failureMessage", Failure: true, Message: "actual failure"}}, SourceKind: "jtl"}
	next := old
	next.SourceKind = "worker_sample"
	next.Assertions = []RequestAssertion{{Name: "实际断言", Failure: true, Message: "actual failure"}}
	a.RequestSamples = []RequestSample{old}
	MergeRequestSamples(&a, []RequestSample{next}, nil)
	if len(a.RequestSamples) != 1 || len(a.RequestSamples[0].Assertions) != 1 || a.RequestSamples[0].Assertions[0].Name != "实际断言" {
		t.Fatalf("same assertion counted twice across CSV and worker: %+v", a.RequestSamples)
	}
}

func TestMergeRequestSamplesWorkerObservationReplacesDifferentCSVObservationWhole(t *testing.T) {
	a := requestAnalysisFixture()
	key := a.NodeSteps[0].StepKey
	csv := RequestSample{NodeID: "w1", StepKey: key, Label: "login", SampleKind: "unknown", TimestampMS: 1000, ElapsedMS: 10, Success: true, Method: requestText("POST"), URL: requestText("https://svc/csv-observation"), RequestParams: requestText("csv-only=old"), ResponseCode: requestText("200"), SourceKind: "jtl", SourceName: "results.jtl"}
	worker := RequestSample{NodeID: "w1", StepKey: key, Label: "login", SampleKind: "unknown", TimestampMS: 1100, ElapsedMS: 20, Success: true, ResponseBody: requestText("actual worker response"), AssertionsCaptured: true, Assertions: []RequestAssertion{}, SourceKind: "worker_sample", SourceName: "request-samples.json"}
	a.RequestSamples = []RequestSample{csv}
	MergeRequestSamples(&a, []RequestSample{worker}, nil)
	if len(a.RequestSamples) != 1 {
		t.Fatalf("unexpected retained slots: %+v", a.RequestSamples)
	}
	got := a.RequestSamples[0]
	if got.TimestampMS != 1100 || got.ElapsedMS != 20 || got.ResponseBody == nil || *got.ResponseBody != "actual worker response" || got.SourceKind != "worker_sample" || got.URL != nil || got.Method != nil || got.RequestParams != nil || got.ResponseCode != nil || !got.AssertionsCaptured {
		t.Fatalf("CSV won or two different observations were mixed: %+v", got)
	}
	later := worker
	later.TimestampMS = 1200
	later.ResponseBody = requestText("later worker response")
	MergeRequestSamples(&a, []RequestSample{later, csv}, nil)
	if len(a.RequestSamples) != 1 || a.RequestSamples[0].TimestampMS != 1100 || *a.RequestSamples[0].ResponseBody != "actual worker response" {
		t.Fatalf("first worker observation was overwritten: %+v", a.RequestSamples)
	}
}

func TestMergeRequestSamplesDifferentWorkerObservationReplacementRetainsEncodedBudgets(t *testing.T) {
	a := Analysis{Nodes: []Node{{NodeID: "w"}}}
	samples := []RequestSample{}
	for i := 0; i < 3; i++ {
		label := fmt.Sprintf("step%d", i)
		key := StableStepKey("unknown", label)
		a.NodeSteps = append(a.NodeSteps, Step{NodeID: "w", StepKey: key})
		sample := RequestSample{NodeID: "w", StepKey: key, Label: label, SampleKind: "unknown", TimestampMS: 1000, Success: true, SourceKind: "jtl"}
		if i > 0 {
			sample.ResponseBody = requestText(strings.Repeat("<", 8192))
		}
		samples = append(samples, sample)
	}
	a.RequestSamples = samples
	worker := samples[0]
	worker.SourceKind = "worker_sample"
	worker.TimestampMS = 1100
	worker.ResponseBody = requestText(strings.Repeat("<", 8192))
	MergeRequestSamples(&a, []RequestSample{worker}, nil)
	encoded, e := json.Marshal(a.RequestSamples)
	if e != nil {
		t.Fatal(e)
	}
	if len(encoded) > 128<<10 || !a.RequestSamplesTruncated {
		t.Fatalf("replacement ignored encoded node budget: bytes=%d truncated=%t", len(encoded), a.RequestSamplesTruncated)
	}
	for _, s := range a.RequestSamples {
		if s.StepKey == worker.StepKey && (s.TimestampMS != 1000 || s.ResponseBody != nil) {
			t.Fatalf("oversize replacement partially attached worker fields: %+v", s)
		}
	}
}

func TestRequestSamplePrefixedEncodedFormAndNamespacedXMLMaskSecrets(t *testing.T) {
	for _, text := range []string{
		"POST data:\n%74%6f%6b%65%6e=private-form&ordinary=visible\n[no cookies]",
		"POST data:\ncookie=[REDACTED]&ordinary=visible\n[no cookies]",
		`<root><auth:token>private-xml</auth:token><ordinary>visible</ordinary></root>`,
		`<root><auth:access_token>private-xml</auth:access_token><ordinary>visible</ordinary></root>`,
	} {
		s := SanitizeRequestSample(RequestSample{RequestParams: requestText(text)}, nil)
		if s.RequestParams == nil || strings.Contains(*s.RequestParams, "private-") || !strings.Contains(*s.RequestParams, "visible") {
			t.Fatalf("prefixed form/namespaced XML escaped masking: %q => %v", text, s.RequestParams)
		}
	}
}

func TestRequestSampleURLAuthenticationIsMaskedForWebsocketAndOtherSchemes(t *testing.T) {
	for _, scheme := range []string{"ws", "wss", "ftp", "custom+scheme"} {
		s := SanitizeRequestSample(RequestSample{URL: requestText(scheme + "://user:private-url@host/path?ordinary=visible"), RequestParams: requestText("Socket connection:\n" + scheme + "://user:private-sampler@host/path"), ResponseBody: requestText(`{"next":"` + scheme + `://user:private-body@host/path"}`)}, nil)
		encoded, e := json.Marshal(s)
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{"private-url", "private-sampler", "private-body"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("non-HTTP URL userinfo leaked for %s: %s", scheme, secret)
			}
		}
		if s.URL == nil || !strings.Contains(*s.URL, "host/path") {
			t.Fatalf("safe URL address missing for %s", scheme)
		}
	}
}
