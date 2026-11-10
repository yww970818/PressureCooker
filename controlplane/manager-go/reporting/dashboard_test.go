package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

type dashboardTestStats struct {
	SampleCount int64    `json:"sample_count"`
	MeanMS      *float64 `json:"mean_ms"`
	MinMS       *float64 `json:"min_ms"`
	MaxMS       *float64 `json:"max_ms"`
	P95MS       *float64 `json:"p95_ms"`
}
type dashboardTestApdex struct {
	Score                                   *float64 `json:"score"`
	Satisfied, Tolerated, Frustrated, Count int64
}
type dashboardTestCode struct {
	Code    string
	Count   int64
	Percent float64
	RPS     *float64
}
type dashboardTestError struct {
	Category, Code, Message string
	Count                   int64
	PercentTotal            float64 `json:"percent_total"`
	PercentErrors           float64 `json:"percent_errors"`
}
type dashboardTestScope struct {
	NodeID           string `json:"node_id"`
	StepKey          string `json:"step_key"`
	Label            string
	SampleKind       string `json:"sample_kind"`
	Metrics          Metrics
	Latency, Connect dashboardTestStats
	Apdex            dashboardTestApdex
	Points           []map[string]any
	Distribution     []map[string]any
	Percentiles      []map[string]any
	ResponseCodes    []dashboardTestCode `json:"response_codes"`
	Errors           []dashboardTestError
	TopErrors        []dashboardTestError `json:"top_errors"`
}
type dashboardTestView struct {
	Version      int
	Options      map[string]float64
	Capabilities map[string]string
	Warnings     []string
	Scopes       []dashboardTestScope
}

func dashboardView(t *testing.T, a Analysis) dashboardTestView {
	t.Helper()
	b, e := json.Marshal(a)
	if e != nil {
		t.Fatal(e)
	}
	var out struct{ Dashboard dashboardTestView }
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out.Dashboard
}
func dashboardScope(t *testing.T, d dashboardTestView, node, key string) dashboardTestScope {
	t.Helper()
	for _, s := range d.Scopes {
		if s.NodeID == node && s.StepKey == key {
			return s
		}
	}
	t.Fatalf("missing scope node=%q step=%q in %+v", node, key, d)
	return dashboardTestScope{}
}
func dashboardNumber(t *testing.T, p map[string]any, key string, want float64) {
	t.Helper()
	got, ok := p[key].(float64)
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s=%v, want %v", key, p[key], want)
	}
}
func TestDashboardAllScopesLatestWorkerThreadsAndNativeTransactions(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,allThreads,Latency,Connect,responseCode,bytes,sentBytes,responseMessage\n" +
		"1000,100,a,true,w1,ordinary,10,20,0,200,100,10,OK\n" +
		"1100,200,a,false,w1,ordinary,8,30,5,500,200,20,HTTP failure\n" +
		"1050,300,a,true,w2,ordinary,4,40,10,200,300,30,OK\n" +
		"1800,400,b,true,w2,ordinary,3,50,15,200,400,40,OK\n" +
		"1900,500,TX,false,w1,,8,0,0,200,999,999,Number of samples in transaction : 4; number of failing samples : 1\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"w1", "w2"}})
	if e != nil {
		t.Fatal(e)
	}
	d := dashboardView(t, a)
	if d.Version != 1 || len(d.Scopes) != 10 {
		t.Fatalf("missing scoped dashboard: version=%d scope_count=%d", d.Version, len(d.Scopes))
	}
	g := dashboardScope(t, d, "", "")
	if g.Metrics.SampleCount != 4 || g.Metrics.TransactionCount != 1 || len(g.Points) != 1 {
		t.Fatalf("transaction polluted overall scope: %+v", g)
	}
	if g.Latency.SampleCount != 4 || g.Latency.MeanMS == nil || *g.Latency.MeanMS != 35 || g.Connect.MeanMS == nil || *g.Connect.MeanMS != 7.5 {
		t.Fatalf("network timings wrong: %+v", g)
	}
	for k, w := range map[string]float64{"active_threads": 11, "threads_observed_nodes": 2, "threads_expected_nodes": 2, "rps": 4, "success_rps": 3, "failure_rps": 1, "tps": 1, "bytes_received_per_second": 1000, "bytes_sent_per_second": 100} {
		dashboardNumber(t, g.Points[0], k, w)
	}
	if g.Apdex.Score == nil || *g.Apdex.Score != .75 || g.Apdex.Satisfied != 3 || g.Apdex.Frustrated != 1 || len(g.Errors) != 1 || g.Errors[0].Count != 1 || g.Errors[0].PercentTotal != 25 {
		t.Fatalf("failure/APDEX denominator double-counted tx: %+v", g)
	}
	keyA, keyTX := "", ""
	for _, s := range a.Steps {
		if s.Label == "a" {
			keyA = s.StepKey
		}
		if s.SampleKind == "transaction" {
			keyTX = s.StepKey
		}
	}
	if keyA == "" || keyTX == "" || a.Metrics.SampleCount != 4 || a.Metrics.TransactionCount != 1 {
		t.Fatalf("native transaction not classified: %+v", a.Steps)
	}
	dashboardNumber(t, dashboardScope(t, d, "", keyA).Points[0], "active_threads", 12)
	dashboardNumber(t, dashboardScope(t, d, "w1", "").Points[0], "active_threads", 8)
	tx := dashboardScope(t, d, "", keyTX)
	if tx.Metrics.SampleScope != "transaction_samples" || tx.Apdex.Frustrated != 1 || len(tx.Errors) != 1 || tx.Points[0]["rps"] != nil {
		t.Fatalf("transaction scope ambiguity: %+v", tx)
	}
	dashboardNumber(t, tx.Points[0], "tps", 1)
}
func TestDashboardOptionalMissingZeroAndInvalidDoNotDiscardSamples(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,allThreads,Latency,Connect,bytes,sentBytes,responseCode\n" +
		"1000,0,a,true,w,ordinary,0,0,0,0,0,0\n" +
		"1100,10,a,true,w,ordinary,-1,broken,NaN,broken,-2,\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"w"}})
	if e != nil {
		t.Fatal(e)
	}
	if a.Metrics.SampleCount != 2 || a.Quality.InvalidRows != 0 {
		t.Fatalf("optional invalid data discarded a valid sample: %+v", a)
	}
	d := dashboardView(t, a)
	g := dashboardScope(t, d, "", "")
	for _, k := range []string{"active_threads", "latency", "connect", "bytes", "response_codes"} {
		if d.Capabilities[k] != "PARTIAL" {
			t.Fatalf("%s capability=%s", k, d.Capabilities[k])
		}
	}
	if g.Latency.SampleCount != 1 || g.Latency.MeanMS == nil || *g.Latency.MeanMS != 0 || g.Connect.MeanMS == nil || *g.Connect.MeanMS != 0 {
		t.Fatalf("real zero lost: %+v", g)
	}
	dashboardNumber(t, g.Points[0], "active_threads", 0)
	dashboardNumber(t, g.Points[0], "bytes_received_per_second", 0)
	if len(d.Warnings) == 0 || len(a.Quality.Warnings) == 0 {
		t.Fatal("missing optional evidence was not explained")
	}
	missing, e := ParseJTL(context.Background(), strings.NewReader("timeStamp,elapsed,label,success,engine_id,sample_kind,grpThreads\n1,1,a,true,w,ordinary,50\n"), ParseOptions{})
	if e != nil {
		t.Fatal(e)
	}
	dm := dashboardView(t, missing)
	p := dashboardScope(t, dm, "", "").Points[0]
	for _, k := range []string{"active_threads", "latency", "connect", "bytes", "response_codes"} {
		if dm.Capabilities[k] != "MISSING" {
			t.Fatalf("absent %s fabricated: %+v", k, dm)
		}
	}
	for _, k := range []string{"active_threads", "latency_mean_ms", "connect_mean_ms", "bytes_received_per_second", "bytes_sent_per_second"} {
		if p[k] != nil {
			t.Fatalf("missing %s became zero: %+v", k, p)
		}
	}
}
func TestDashboardErrorsExhaustiveBeyondIssueCapAndTopFiveDeterministic(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("timeStamp,elapsed,label,success,engine_id,sample_kind,responseCode,responseMessage,failureMessage\n")
	for i := 0; i < 620; i++ {
		code, msg := "500", "server error"
		if i >= 510 {
			code, msg = "400", "bad request"
		}
		fmt.Fprintf(&raw, "1000,1,a,false,w,ordinary,%s,%s,\n", code, msg)
	}
	a, e := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{})
	if e != nil {
		t.Fatal(e)
	}
	g := dashboardScope(t, dashboardView(t, a), "", "")
	if len(a.Issues) != 500 || len(g.Errors) != 2 || len(g.TopErrors) != 2 || g.TopErrors[0].Count != 510 || g.TopErrors[1].Count != 110 || g.TopErrors[0].Code != "500" {
		t.Fatalf("error aggregate sampled/truncated: %+v", g.Errors)
	}
	var sum int64
	for _, v := range g.Errors {
		sum += v.Count
	}
	if sum != 620 || math.Abs(g.TopErrors[0].PercentErrors-51000.0/620) > 1e-9 {
		t.Fatalf("incorrect exhaustive error denominator: %+v", g.Errors)
	}
}
func TestDashboardAPDEXFailureAndFullDistribution(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind\n"
	for i, v := range []int{0, 500, 1000, 1500, 1501, 1} {
		ok := i != 5
		raw += fmt.Sprintf("1000,%d,a,%t,w,ordinary\n", v, ok)
	}
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{})
	if e != nil {
		t.Fatal(e)
	}
	d := dashboardView(t, a)
	g := dashboardScope(t, d, "", "")
	if d.Options["apdex_satisfied_ms"] != 500 || d.Options["apdex_tolerated_ms"] != 1500 || d.Options["granularity_ms"] != 1000 || g.Apdex.Score == nil || *g.Apdex.Score != .5 || g.Apdex.Satisfied != 2 || g.Apdex.Tolerated != 2 || g.Apdex.Frustrated != 2 {
		t.Fatalf("incorrect APDEX defaults/failure handling: %+v", g.Apdex)
	}
	var n float64
	for _, v := range g.Distribution {
		n += v["count"].(float64)
	}
	if n != 6 || len(g.Percentiles) != 101 || g.Percentiles[0]["value_ms"] != float64(0) || g.Percentiles[100]["value_ms"] != float64(1501) {
		t.Fatalf("incomplete histogram/percentiles: %+v", g)
	}
	dashboardNumber(t, g.Points[0], "successful_p95_ms", 1501)
}

func TestDashboardConfiguredAPDEXGranularityAndOptionValidation(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind\n1000,100,a,true,w,ordinary\n9000,200,a,true,w,ordinary\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{AnalyticsOptions: DashboardOptions{ApdexSatisfiedMS: 100, ApdexToleratedMS: 200, GranularityMS: 10000}})
	if e != nil {
		t.Fatal(e)
	}
	g := a.Dashboard.Scopes[0]
	if g.Apdex.Score == nil || *g.Apdex.Score != .75 || len(g.Points) != 1 || g.Points[0].WidthMS != 10000 || *g.Points[0].RPS != .2 || len(a.Timeline) != 1 || a.Timeline[0].WidthMS != 10000 || *a.Timeline[0].RPS != .2 {
		t.Fatalf("options did not control immutable analysis: apdex=%+v points=%+v timeline=%+v", g.Apdex, g.Points, a.Timeline)
	}
	for _, o := range []DashboardOptions{{ApdexSatisfiedMS: -1}, {ApdexToleratedMS: math.NaN()}, {ApdexSatisfiedMS: math.Inf(1)}, {ApdexSatisfiedMS: 1000, ApdexToleratedMS: 999}, {ApdexToleratedMS: 1e9 + 1}, {GranularityMS: 999}, {GranularityMS: 3600001}} {
		if _, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{AnalyticsOptions: o}); e == nil {
			t.Fatalf("invalid analysis options accepted: %+v", o)
		}
	}
}

func TestDashboardExplicitCardinalityBounds(t *testing.T) {
	header := "timeStamp,elapsed,label,success,engine_id,sample_kind,responseCode,responseMessage\n"
	t.Run("errors exact bound and overflow", func(t *testing.T) {
		var raw strings.Builder
		raw.WriteString(header)
		for i := 0; i < 512; i++ {
			fmt.Fprintf(&raw, "1000,1,a,false,w,ordinary,500,error-%03d\n", i)
		}
		a, e := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{})
		if e != nil {
			t.Fatal(e)
		}
		if len(a.Dashboard.Scopes[0].Errors) != 512 || len(a.Dashboard.Scopes[0].TopErrors) != 5 || a.Dashboard.Scopes[0].TopErrors[0].Message != "error-000" {
			t.Fatal("distinct errors or deterministic tie order lost")
		}
		raw.WriteString("1000,1,a,false,w,ordinary,500,overflow\n")
		if _, e = ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{}); e == nil || !strings.Contains(e.Error(), "distinct error limit") {
			t.Fatalf("error overflow silently truncated: %v", e)
		}
	})
	t.Run("codes", func(t *testing.T) {
		var raw strings.Builder
		raw.WriteString(header)
		for i := 0; i < 128; i++ {
			fmt.Fprintf(&raw, "1000,1,a,true,w,ordinary,%d,OK\n", i)
		}
		a, e := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{})
		if e != nil {
			t.Fatal(e)
		}
		if len(a.Dashboard.Scopes[0].ResponseCodes) != 128 {
			t.Fatal("codes truncated at exact bound")
		}
		raw.WriteString("1000,1,a,true,w,ordinary,overflow,OK\n")
		if _, e = ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{}); e == nil || !strings.Contains(e.Error(), "response code limit") {
			t.Fatalf("code overflow silently truncated: %v", e)
		}
	})
	t.Run("points shared across all scopes", func(t *testing.T) {
		var raw strings.Builder
		raw.WriteString(header)
		for i := 0; i < 3000; i++ {
			fmt.Fprintf(&raw, "%d,1,a,true,w,ordinary,200,OK\n", i*1000)
		}
		a, e := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{})
		if e != nil {
			t.Fatal(e)
		}
		n := 0
		for _, s := range a.Dashboard.Scopes {
			n += len(s.Points)
		}
		if n != 12000 {
			t.Fatalf("point bound was applied per scope: %d", n)
		}
		raw.WriteString("3000000,1,a,true,w,ordinary,200,OK\n")
		if _, e = ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{}); e == nil || !strings.Contains(e.Error(), "point limit") {
			t.Fatalf("point overflow silently truncated: %v", e)
		}
		a, e = ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{AnalyticsOptions: DashboardOptions{GranularityMS: 10000}})
		if e != nil || len(a.Timeline) != 301 {
			t.Fatalf("coarse analysis cannot accommodate longer run: %v", e)
		}
	})
}

func TestDashboardHistogramBudgetIsSharedAndAllowsExistingCells(t *testing.T) {
	b := &dashboardBuilder{maxHistogramCells: 1}
	a := &agg{}
	for i := 0; i < 2; i++ {
		if e := b.histogramAdd(a, 0, 1, true, 0, 0); e != nil {
			t.Fatalf("repeated bin consumed capacity: %v", e)
		}
	}
	if e := b.histogramAdd(a, 0, 2, true, 0, 0); e == nil || !strings.Contains(e.Error(), "histogram cell limit") || a.n != 2 {
		t.Fatalf("new histogram bin exceeded capacity: %v n=%d", e, a.n)
	}
	if e := b.histogramAdd(&agg{}, 0, 1, true, 0, 0); e == nil {
		t.Fatal("budget was per histogram instead of shared")
	}
}

func TestDashboardSuccessOnlyPercentilesAliasesAndUnorderedThreadSamples(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,allThreads,latency,connectTime\n1000,10,a,true,w,ordinary,10,0.5,0.25\n1200,10000,a,false,w,ordinary,3,1.5,1.25\n1100,10000,a,false,w,ordinary,99,,\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"w", "missing"}})
	if e != nil {
		t.Fatal(e)
	}
	g := a.Dashboard.Scopes[0]
	p := g.Points[0]
	if p.ActiveThreads == nil || *p.ActiveThreads != 3 || p.ThreadsObservedNodes != 1 || p.ThreadsExpectedNodes != 2 || p.SuccessfulP95MS == nil || *p.SuccessfulP95MS != 10 || p.P95MS == nil || *p.P95MS != 10000 || g.Latency.MeanMS == nil || *g.Latency.MeanMS != 1 || g.Connect.MeanMS == nil || *g.Connect.MeanMS != .75 {
		t.Fatalf("successful-only percentiles, aliases or last worker snapshot wrong: point=%+v latency=%+v connect=%+v", p, g.Latency, g.Connect)
	}
	for _, s := range a.Dashboard.Scopes {
		if s.NodeID == "missing" && (len(s.Points) != 0 || s.Apdex.Score != nil || s.Latency.MeanMS != nil || s.Metrics.SuccessRate != nil) {
			t.Fatal("missing worker fabricated data")
		}
	}
}

func TestDashboardRedactsErrorsCodesAndPreservesDistinctStepKeys(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,responseCode,responseMessage,failureMessage\n" +
		"1000,1,/p?token=label-private-one,false,w,ordinary,token=code-private-value,fallback-visible,password=assert-private-value\n" +
		"1001,1,/p?token=label-private-two,false,w,ordinary,500,token=response-private-value,\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{})
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(a.Dashboard)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"label-private-one", "label-private-two", "code-private-value", "assert-private-value", "response-private-value", "fallback-visible"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("dashboard leaked unredacted/preferred-away value %q", secret)
		}
	}
	keys := map[string]bool{}
	for _, s := range a.Dashboard.Scopes {
		if s.NodeID == "" && s.StepKey != "" {
			keys[s.StepKey] = true
		}
	}
	if len(keys) != 2 || a.Dashboard.Scopes[0].Errors[0].Category != "assertion_failure" {
		t.Fatalf("redaction merged sampling labels or lost assertion category")
	}
}

func TestDashboardDistributionOmitsUnobservedFarTailButKeepsActualTail(t *testing.T) {
	for _, elapsed := range []int64{100, 60001} {
		raw := fmt.Sprintf("timeStamp,elapsed,label,success,engine_id,sample_kind\n1000,%d,a,true,w,ordinary\n", elapsed)
		a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{})
		if e != nil {
			t.Fatal(e)
		}
		bins := a.Dashboard.Scopes[0].Distribution
		var count int64
		for _, bin := range bins {
			if bin.LowerMS > float64(elapsed) {
				t.Fatalf("unobserved far-tail bin distorts histogram range: elapsed=%d lower=%v", elapsed, bin.LowerMS)
			}
			count += bin.Count
		}
		if count != 1 || len(bins) == 0 || bins[len(bins)-1].Count != 1 {
			t.Fatalf("observed tail omitted: elapsed=%d bins=%+v", elapsed, bins)
		}
	}
}

func TestDashboardUnexpectedWorkersCannotCompleteThreadCoverageByCount(t *testing.T) {
	raw := "timeStamp,elapsed,label,success,engine_id,sample_kind,allThreads\n1000,1,a,true,w1,ordinary,2\n1000,1,a,true,foreign,ordinary,3\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), ParseOptions{ExpectedNodes: []string{"w1", "w2"}})
	if e != nil {
		t.Fatal(e)
	}
	p := a.Dashboard.Scopes[0].Points[0]
	if p.ThreadsObservedNodes != 2 || p.ThreadsExpectedNodes != 2 || p.ActiveThreads == nil || *p.ActiveThreads != 5 {
		t.Fatalf("observed worker measurement changed: %+v", p)
	}
	found := false
	for _, warning := range a.Dashboard.Warnings {
		if strings.Contains(warning, "未覆盖所有预期节点") {
			found = true
		}
	}
	if !found {
		t.Fatal("foreign worker falsely completed thread coverage because counts matched")
	}
}
