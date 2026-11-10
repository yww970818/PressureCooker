package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Decode the public JSON so a missing node behavior fails without requiring a new Go type.
type nodeDetailTestNode struct {
	NodeID    string  `json:"node_id"`
	Label     string  `json:"label"`
	Expected  bool    `json:"expected"`
	Status    string  `json:"status"`
	Metrics   Metrics `json:"metrics"`
	FirstAt   *int64  `json:"first_at"`
	LastAt    *int64  `json:"last_at"`
	StepCount int     `json:"step_count"`
}
type nodeDetailTestStep struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id"`
	StepKey    string `json:"step_key"`
	Label      string `json:"label"`
	SampleKind string `json:"sample_kind"`
	Metrics
}
type nodeDetailTestIssue struct {
	NodeID     string `json:"node_id"`
	StepKey    string `json:"step_key"`
	SourceName string `json:"source_name"`
	Message    string `json:"message"`
}
type nodeDetailTestView struct {
	Nodes     []nodeDetailTestNode  `json:"nodes"`
	NodeSteps []nodeDetailTestStep  `json:"node_steps"`
	Steps     []nodeDetailTestStep  `json:"steps"`
	Issues    []nodeDetailTestIssue `json:"issues"`
}

func nodeDetailView(t *testing.T, a Analysis) nodeDetailTestView {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var v nodeDetailTestView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func nodeDetailByID(t *testing.T, v nodeDetailTestView, id string) nodeDetailTestNode {
	t.Helper()
	for _, n := range v.Nodes {
		if n.NodeID == id {
			return n
		}
	}
	t.Fatalf("missing node %q in %+v", id, v.Nodes)
	return nodeDetailTestNode{}
}
func TestNodeDetailsAggregateOrdinaryAndKeepTransactionsSeparate(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,bytes,sentBytes,responseMessage\n" +
		"1000,100,checkout,true,n1,ordinary,10,1,ok\n" +
		"1100,300,checkout,false,n2,ordinary,20,2,HTTP failure\n" +
		"1500,0,checkout,true,n1,http,30,3,ok\n" +
		"2000,900,checkout,false,n1,transaction,100,10,transaction failure\n" +
		"2200,100,checkout,true,n2,transaction,200,20,ok\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"n1", "n2"}})
	if err != nil {
		t.Fatal(err)
	}
	v := nodeDetailView(t, a)
	if len(v.Nodes) != 2 || len(v.NodeSteps) != 4 {
		t.Fatalf("node details absent or collapsed: %+v", v)
	}
	if a.Metrics.SampleCount != 3 || a.Metrics.TransactionCount != 2 || a.Metrics.FailureCount != 1 || a.Metrics.BytesReceived != 60 || *a.Metrics.DurationSeconds != .5 {
		t.Fatalf("transactions polluted ordinary totals: %+v", a.Metrics)
	}
	n1, n2 := nodeDetailByID(t, v, "n1"), nodeDetailByID(t, v, "n2")
	if n1.Status != "OBSERVED" || !n1.Expected || n1.StepCount != 2 || n1.Metrics.SampleCount != 2 || n1.Metrics.TransactionCount != 1 || *n1.Metrics.SuccessRate != 100 || *n1.Metrics.MeanMS != 50 || n1.Metrics.BytesReceived != 40 || *n1.FirstAt != 1000 || *n1.LastAt != 2900 {
		t.Fatalf("n1: %+v", n1)
	}
	if n2.Metrics.SampleCount != 1 || n2.Metrics.FailureCount != 1 || *n2.Metrics.SuccessRate != 0 || *n2.Metrics.MeanMS != 300 || n2.Metrics.TransactionCount != 1 {
		t.Fatalf("n2: %+v", n2)
	}
	keys := map[string]string{}
	ids := map[string]bool{}
	for _, s := range append(v.Steps, v.NodeSteps...) {
		if len(s.StepKey) != 64 || ids[s.ID] {
			t.Fatalf("missing key or duplicate ID: %+v", s)
		}
		ids[s.ID] = true
		if prior := keys[s.SampleKind]; prior != "" && prior != s.StepKey {
			t.Fatalf("step key varies by node: %+v", s)
		}
		keys[s.SampleKind] = s.StepKey
		if s.SampleKind == "transaction" && (s.SampleScope != "transaction_samples" || s.TransactionCount != s.SampleCount) {
			t.Fatalf("transaction scope: %+v", s)
		}
	}
	if keys["ordinary"] == keys["transaction"] || len(v.Issues) != 2 {
		t.Fatalf("kind or failed transaction association lost: %+v", v)
	}
	if v.Issues[0].NodeID != "n2" || v.Issues[0].StepKey != keys["ordinary"] || v.Issues[1].NodeID != "n1" || v.Issues[1].StepKey != keys["transaction"] {
		t.Fatalf("unscoped failure: %+v", v.Issues)
	}
	for _, issue := range v.Issues {
		if issue.SourceName != "results.jtl" {
			t.Fatalf("failure log cannot identify its archived source: %+v", issue)
		}
	}
}
func TestNodeDetailsRetainMissingUnassignedAndUnexpected(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind\n1,1,a,true,n1,ordinary\n2,1,a,false,foreign,ordinary\n3,1,a,false,,ordinary\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"n1", "n2"}, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(0)}})
	if err != nil {
		t.Fatal(err)
	}
	v := nodeDetailView(t, a)
	if len(v.Nodes) != 4 {
		t.Fatalf("missing identities merged or omitted: %+v", v.Nodes)
	}
	missing := nodeDetailByID(t, v, "n2")
	if !missing.Expected || missing.Status != "MISSING" || missing.Metrics.SampleCount != 0 || missing.Metrics.MeanMS != nil || missing.Metrics.SuccessRate != nil || missing.Metrics.DurationSeconds != nil || missing.FirstAt != nil || missing.LastAt != nil || missing.StepCount != 0 {
		t.Fatalf("invented missing-node evidence: %+v", missing)
	}
	if nodeDetailByID(t, v, "foreign").Status != "UNEXPECTED" || nodeDetailByID(t, v, "__unassigned__").Status != "UNASSIGNED" {
		t.Fatalf("untrusted attribution hidden: %+v", v.Nodes)
	}
	if a.Metrics.SampleCount != 3 || a.Quality.NodeCoverage != "PARTIAL" || a.Verdict.Status != "INCOMPLETE" || v.Issues[0].NodeID != "foreign" || v.Issues[1].NodeID != "__unassigned__" {
		t.Fatalf("unsafe coverage or lost unknown failures: %+v %+v", a, v)
	}
}
func TestNodeDetailsTransactionOnlyAndInvalidRowsDoNotEstablishCoverage(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind\n1000,1,a,true,n1,ordinary\n1001,1,a,true,n2,transaction\n1002,broken,a,true,n3,ordinary\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"n1", "n2", "n3"}, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(0)}})
	if err != nil {
		t.Fatal(err)
	}
	v := nodeDetailView(t, a)
	n := nodeDetailByID(t, v, "n2")
	if n.Status != "OBSERVED" || n.Metrics.SampleCount != 0 || n.Metrics.TransactionCount != 1 || n.Metrics.MeanMS != nil || n.Metrics.SuccessRate != nil {
		t.Fatalf("transaction created ordinary metrics: %+v", n)
	}
	if nodeDetailByID(t, v, "n3").Status != "MISSING" || a.Quality.InvalidRows != 1 || a.Quality.NodeCoverage != "PARTIAL" || a.Verdict.Status != "INCOMPLETE" {
		t.Fatalf("invalid coverage: %+v", a)
	}
}
func TestNodeDetailsRedactionDoesNotMergeDistinctLabelsOrExposeIdentity(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,responseMessage\n1,1,password=first-sensitive-value,false,token=private-sensitive-value,ordinary,password=first-sensitive-value\n2,2,password=second-sensitive-value,false,token=private-sensitive-value,ordinary,password=second-sensitive-value\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"token=private-sensitive-value"}})
	if err != nil {
		t.Fatal(err)
	}
	v := nodeDetailView(t, a)
	if len(v.NodeSteps) != 2 || v.NodeSteps[0].StepKey == v.NodeSteps[1].StepKey || v.NodeSteps[0].Label != v.NodeSteps[1].Label {
		t.Fatalf("redaction merged distinct raw steps: %+v", v.NodeSteps)
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"first-sensitive-value", "second-sensitive-value", "private-sensitive-value"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret persisted: %s", data)
		}
	}
	for i, issue := range v.Issues {
		if issue.NodeID != v.Nodes[0].NodeID || issue.StepKey == "" || (i > 0 && issue.StepKey == v.Issues[0].StepKey) {
			t.Fatalf("redacted association lost: %+v", v)
		}
	}
	more, err := ParseJTL(context.Background(), strings.NewReader(raw+"3,1,aaa,true,token=private-sensitive-value,ordinary,ok\n"), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	vv := nodeDetailView(t, more)
	for _, old := range v.NodeSteps {
		found := false
		for _, current := range vv.NodeSteps {
			if current.StepKey == old.StepKey {
				found = true
			}
		}
		if !found {
			t.Fatalf("key depends on ordinal: %+v", old)
		}
	}
}
func TestNodeDetailsAggregationLimitsFailExplicitly(t *testing.T) {
	header := "timeStamp,elapsed,label,success,engine_id,sample_kind\n"
	t.Run("observed nodes including transactions", func(t *testing.T) {
		var raw strings.Builder
		raw.WriteString(header)
		for i := 0; i < 1025; i++ {
			fmt.Fprintf(&raw, "%d,1,a,true,n%d,transaction\n", i, i)
		}
		_, err := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{})
		if err == nil || !strings.Contains(err.Error(), "node limit") {
			t.Fatalf("node aggregation silently exceeded bound: %v", err)
		}
	})
	t.Run("expected nodes", func(t *testing.T) {
		expected := make([]string, 1025)
		for i := range expected {
			expected[i] = fmt.Sprintf("n%d", i)
		}
		_, err := ParseJTL(context.Background(), strings.NewReader(header), ParseOptions{ExpectedNodes: expected})
		if err == nil || !strings.Contains(err.Error(), "node limit") {
			t.Fatalf("expected-node aggregation silently exceeded bound: %v", err)
		}
	})
	t.Run("node steps", func(t *testing.T) {
		var raw strings.Builder
		raw.WriteString(header)
		for n := 0; n < 65; n++ {
			for s := 0; s < 64; s++ {
				fmt.Fprintf(&raw, "1,1,step%d,true,n%d,ordinary\n", s, n)
			}
		}
		_, err := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{})
		if err == nil || !strings.Contains(err.Error(), "node-step limit") {
			t.Fatalf("node-step aggregation silently exceeded bound: %v", err)
		}
	})
}

func TestNodeDetailsZeroMetricsDifferFromMissingEvidence(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind\n1000,0,zero,false,n,ordinary\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"n", "missing"}, TimestampIsEnd: true})
	if err != nil {
		t.Fatal(err)
	}
	v := nodeDetailView(t, a)
	observed, missing := nodeDetailByID(t, v, "n"), nodeDetailByID(t, v, "missing")
	for _, value := range []*float64{observed.Metrics.MeanMS, observed.Metrics.P95MS, observed.Metrics.MinMS, observed.Metrics.MaxMS, observed.Metrics.SuccessRate, observed.Metrics.DurationSeconds} {
		if value == nil || *value != 0 {
			t.Fatalf("real zero discarded: %+v", observed)
		}
	}
	if observed.Metrics.RPS != nil || *observed.FirstAt != 1000 || *observed.LastAt != 1000 || missing.Metrics.MeanMS != nil || missing.Metrics.SuccessRate != nil {
		t.Fatalf("zero and missing evidence confused: %+v", v.Nodes)
	}
}

func TestNodeDetailsUnconfirmedScopeRemainsLocalAndConservative(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,SampleCount\n1000,10,a,true,confirmed,ordinary,1\n1000,10,a,true,unknown,,1\n1000,10,a,true,aggregate,ordinary,2\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"confirmed", "unknown", "aggregate"}, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(0)}})
	if err != nil {
		t.Fatal(err)
	}
	v := nodeDetailView(t, a)
	if nodeDetailByID(t, v, "confirmed").Metrics.SampleScope != "ordinary_samples" || nodeDetailByID(t, v, "unknown").Metrics.SampleScope != "sample_records" || nodeDetailByID(t, v, "aggregate").Metrics.SampleScope != "sample_records" || a.Verdict.Status != "INCOMPLETE" {
		t.Fatalf("unconfirmed records gained ordinary attestation: %+v", a)
	}
	for _, step := range v.NodeSteps {
		if step.NodeID != "confirmed" && step.SampleScope != "sample_records" {
			t.Fatalf("unconfirmed node-step scope: %+v", step)
		}
	}
}

func TestNodeDetailsConfiguredLimitsIncludeMissingNodesAndAllStepKinds(t *testing.T) {
	header := "timeStamp,elapsed,label,success,engine_id,sample_kind\n"
	for _, test := range []struct {
		name, raw, want string
		options         ParseOptions
	}{
		{"union", header + "1,1,a,true,foreign,ordinary\n", "node limit", ParseOptions{ExpectedNodes: []string{"missing"}, MaxNodes: 1}},
		{"ordinary and transaction", header + "1,1,a,true,n,ordinary\n2,1,a,true,n,transaction\n", "node-step limit", ParseOptions{MaxNodeSteps: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseJTL(context.Background(), strings.NewReader(test.raw), test.options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("limit ignored: %v", err)
			}
		})
	}
	a, err := ParseJTL(context.Background(), strings.NewReader(header+"1,1,a,true,n,ordinary\n2,1,a,true,n,ordinary\n"), ParseOptions{MaxNodes: 1, MaxNodeSteps: 1, ExpectedNodes: []string{"n", "n"}})
	if err != nil || len(a.Nodes) != 1 || len(a.NodeSteps) != 1 || a.Nodes[0].Metrics.SampleCount != 2 {
		t.Fatalf("exact bound or duplicate expectation rejected: %+v %v", a, err)
	}
}

func TestNodeDetailsRedactedIdentityCollisionsFailExplicitly(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind\n1,1,a,true,token=private-one,ordinary\n2,1,a,true,token=private-two,ordinary\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{})
	if err == nil || !strings.Contains(err.Error(), "identity collision") {
		t.Fatalf("ambiguous redacted nodes exposed: %+v %v", a, err)
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "private-one") || strings.Contains(string(b), "private-two") || strings.Contains(err.Error(), "private") {
		t.Fatalf("failed analysis exposed raw identifiers: %s %v", b, err)
	}
}

func TestNodeDetailsForeignTransactionPreventsPass(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind\n1,1,a,true,n,ordinary\n2,1,a,true,foreign,transaction\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"n"}, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(100)}})
	if err != nil || a.Quality.NodeCoverage != "PARTIAL" || a.Verdict.Status != "INCOMPLETE" {
		t.Fatalf("foreign transaction evidence ignored: %+v %v", a, err)
	}
}

func TestNodeDetailsFailureFallsBackToResponseCodeWithoutLeakingSecrets(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,responseCode,responseMessage\n1,1,a,false,n,ordinary,504,\n2,1,b,false,n,ordinary,token=private-failure-value,\n3,1,c,false,n,ordinary,500,saved failure detail\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Issues) != 3 || !strings.Contains(a.Issues[0].Message, "504") || a.Issues[1].Message == "" || strings.Contains(a.Issues[1].Message, "private-failure-value") || a.Issues[2].Message != "saved failure detail" {
		t.Fatalf("missing or unsafe fallback failure reason: %+v", a.Issues)
	}
}

func TestNodeDetailsJMeterNullEngineIdentityFallsBackToWorkerHostname(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,hostname,Hostname,sample_kind\n" +
		"1000,10,a,true,null,,worker1,ordinary\n" +
		"1100,10,a,true, NULL ,,worker2,ordinary\n" +
		"1200,10,a,true, <NuLl> ,,worker1,ordinary\n" +
		"1300,10,a,true,null,worker2,worker1,ordinary\n" +
		"1400,10,a,true,,null,worker1,ordinary\n" +
		"1500,10,a,true,worker2,worker1,worker1,ordinary\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"worker1", "worker2"}, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(100)}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Metrics.SampleCount != 6 || a.Quality.NodeCoverage != "COMPLETE" || a.Verdict.Status != "PASS" || len(a.Nodes) != 2 || len(a.NodeSteps) != 2 {
		t.Fatalf("JMeter unset engine variable hid valid worker identities: %+v", a)
	}
	for _, node := range a.Nodes {
		if node.Status != "OBSERVED" || !node.Expected || node.Metrics.SampleCount != 3 {
			t.Fatalf("wrong worker attribution: %+v", a.Nodes)
		}
	}
	if strings.Join(a.Quality.ObservedNodes, ",") != "worker1,worker2" {
		t.Fatalf("placeholder node persisted: %+v", a.Quality)
	}
}

func TestNodeDetailsAllNullNodeIdentityRemainsUnassigned(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,hostname,Hostname,sample_kind\n" +
		"1000,10,a,true, null , <NULL> ,NULL,ordinary\n" +
		"1100,10,a,false, ,null,<null>,ordinary\n"
	a, err := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"worker1", "worker2"}, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(0)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Nodes) != 3 || len(a.NodeSteps) != 1 || a.NodeSteps[0].NodeID != UnassignedNodeID || len(a.Quality.ObservedNodes) != 0 || a.Quality.NodeCoverage != "PARTIAL" || a.Verdict.Status != "INCOMPLETE" {
		t.Fatalf("null placeholders became node evidence: %+v", a)
	}
	for _, node := range a.Nodes {
		if node.NodeID == UnassignedNodeID {
			if node.Status != "UNASSIGNED" || node.Metrics.SampleCount != 2 {
				t.Fatalf("lost unassigned samples: %+v", node)
			}
		} else if node.Status != "MISSING" || node.Metrics.SampleCount != 0 || node.Metrics.SuccessRate != nil {
			t.Fatalf("invented expected-node samples: %+v", node)
		}
	}
	if len(a.Issues) != 1 || a.Issues[0].NodeID != UnassignedNodeID {
		t.Fatalf("failure associated with null placeholder: %+v", a.Issues)
	}
}
