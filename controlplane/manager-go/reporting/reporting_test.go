package reporting

import (
	"context"
	"math"
	"strings"
	"testing"
)

func fp(v float64) *float64 { return &v }
func TestJTL(t *testing.T) {
	a, e := ParseJTL(context.Background(), strings.NewReader("\ufefftimeStamp,elapsed,label,success,hostname,sample_kind\n1000,100,\"hello, world\",true,n1,ordinary\n1100,200,assertion,false,n1,ordinary\n1200,300,txn,true,n1,transaction\n"), ParseOptions{ExpectedNodes: []string{"n1"}, RunStatus: "COMPLETED", Targets: Targets{MaxP95MS: fp(200)}})
	if e != nil {
		t.Fatal(e)
	}
	if a.Metrics.SampleCount != 2 || a.Metrics.TransactionCount != 1 || a.Verdict.Status != "PASS" {
		t.Fatalf("%+v", a)
	}
}
func TestConservative(t *testing.T) {
	a, e := ParseJTL(context.Background(), strings.NewReader("timeStamp,elapsed,label,success\n1,1,a,true\n"), ParseOptions{Targets: Targets{MaxP95MS: fp(2)}, RunStatus: "COMPLETED"})
	if e != nil || a.Verdict.Status != "INCOMPLETE" {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestExact(t *testing.T) {
	s := "timeStamp,elapsed,label,success,hostname,sample_kind\n"
	for i := 0; i < 100; i++ {
		v := "10"
		if i >= 95 {
			v = "100"
		}
		s += "1000," + v + ",a,true,n,ordinary\n"
	}
	a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{ExpectedNodes: []string{"n"}, RunStatus: "COMPLETED", Targets: Targets{MaxP95MS: fp(10), MaxP99MS: fp(10)}})
	if e != nil || a.Verdict.Status != "FAIL" || a.Verdict.Checks[0].Status != "PASS" {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestTargets(t *testing.T) {
	if ValidateTargets(Targets{MinRPS: fp(math.Inf(1))}) == nil || ValidateTargets(Targets{MinSuccessRate: fp(101)}) == nil {
		t.Fatal("accepted bad target")
	}
	if Evaluate(Metrics{}, Quality{}, Targets{}, "FAILED").Status != "NOT_SET" {
		t.Fatal("unset")
	}
}
func TestRedact(t *testing.T) {
	s := NewRedactor([]string{"mysecret"})("mysecret Authorization: Bearer abc password=xyz https://u:pw@host/?token=abc")
	for _, v := range []string{"mysecret", "abc", "xyz", "pw@"} {
		if strings.Contains(s, v) {
			t.Fatal(s)
		}
	}
}
func TestLogs(t *testing.T) {
	v, e := ParseLogs(context.Background(), strings.NewReader("2026 ERROR first password=secret\n java.lang.Exception\n\tat x.y\n2026 WARN second\n more\n"), "test", NewRedactor(nil))
	if e != nil || len(v) != 2 || v[0].LineTo != 3 || strings.Contains(v[0].Message, "secret") {
		t.Fatalf("%+v %v", v, e)
	}
}
func TestMalformedMultilineAndBoundaries(t *testing.T) {
	s := "timeStamp,elapsed,label,success,hostname,sample_kind\n2000,100,\"multi\nline password=secret\",true,n,ordinary\n2100,no,a,true,n,ordinary\n2200,0,a,true,n,ordinary\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{ExpectedNodes: []string{"n"}, TimestampIsEnd: true, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(0)}})
	if e != nil || a.Metrics.SampleCount != 2 || a.Quality.InvalidRows != 1 || a.Verdict.Status != "INCOMPLETE" || *a.Metrics.DurationSeconds != .3 || strings.Contains(a.Steps[1].Label, "secret") {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestCoverageAndStatus(t *testing.T) {
	s := "timeStamp,elapsed,label,success,hostname,sample_kind\n1000,10,a,true,n,ordinary\n"
	for _, status := range []string{"RUNNING", "FAILED", "CANCELLED"} {
		a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{ExpectedNodes: []string{"n", "missing"}, RunStatus: status, Targets: Targets{MinSuccessRate: fp(0)}})
		if e != nil || a.Quality.NodeCoverage != "PARTIAL" || a.Verdict.Status != "INCOMPLETE" {
			t.Fatalf("%+v %v", a, e)
		}
	}
}
func TestLimitsAndCancellation(t *testing.T) {
	s := "timeStamp,elapsed,label,success\n1000,10,a,true\n1001,10,b,true\n"
	_, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{MaxLabels: 1})
	if e == nil {
		t.Fatal("unbounded labels")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = ParseJTL(ctx, strings.NewReader(s), ParseOptions{})
	if e != context.Canceled {
		t.Fatal(e)
	}
}
func TestExactDisplayDisagrees(t *testing.T) {
	s := "timeStamp,elapsed,label,success,hostname,sample_kind\n"
	for i := 0; i < 100; i++ {
		v := "10"
		if i >= 95 {
			v = "100"
		}
		s += "1000," + v + ",a,true,n,ordinary\n"
	}
	a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{ExpectedNodes: []string{"n"}, RunStatus: "COMPLETED", Targets: Targets{MaxP95MS: fp(10)}})
	if e != nil || a.Verdict.Status != "PASS" || *a.Metrics.P95MS <= 10 {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestRequiredDuplicateAndBadCSV(t *testing.T) {
	for _, s := range []string{"label,elapsed\na,10\n", "timeStamp,elapsed,label,success,success\n"} {
		if _, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{}); e == nil {
			t.Fatal("accepted malformed header")
		}
	}
	s := "timeStamp,elapsed,label,success\n1000,10,a,true,extra\n1000,10,\"open,true\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{})
	if e != nil || a.Quality.InvalidRows != 2 || a.Quality.MetricStatus != "PARTIAL" {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestAssertionAndSensitiveFields(t *testing.T) {
	s := "timeStamp,elapsed,label,success,hostname,sample_kind,responseMessage,failureMessage\n1000,1,password=hidden,true,n,ordinary,token=private,password=hidden\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{})
	if e != nil || a.Metrics.FailureCount != 1 || len(a.Issues) != 1 || strings.Contains(a.Issues[0].Message, "hidden") || strings.Contains(a.Issues[0].Message, "private") || strings.Contains(a.Steps[0].Label, "hidden") {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestQuotedSensitive(t *testing.T) {
	s := NewRedactor(nil)(`{"password":"secret value","token":"private"}`)
	if strings.Contains(s, "secret") || strings.Contains(s, "private") {
		t.Fatal(s)
	}
}
func TestReviewWholeHeadersAndPropertyMap(t *testing.T) {
	r := NewRedactor(nil)
	for _, s := range []string{"Cookie: a=x; sessionid=live", "Set-Cookie: session=live; secret=hidden; Path=/", "Authorization: Bearer live trailing-secret", "ClientJMeterEngine ERROR global properties = {Cookie=a=x; sessionid=live, token=hidden}", "ERROR request https://host/?token=live&next=ok"} {
		got := r(s)
		for _, secret := range []string{"sessionid=live", "live", "hidden", "trailing-secret"} {
			if strings.Contains(got, secret) {
				t.Fatalf("leak %q => %q", s, got)
			}
		}
	}
}
func TestReviewTransactionBytes(t *testing.T) {
	s := "timeStamp,elapsed,label,success,sample_kind,bytes,sentBytes\n1000,10,tx,true,transaction,123,45\n1001,10,bad,true,transaction,broken,1\n1002,10,http,true,ordinary,2,3\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if a.Metrics.TransactionCount != 2 || a.Quality.InvalidRows != 0 || a.Quality.MetricStatus != "COMPLETE" || a.Dashboard.Capabilities.Bytes != "PARTIAL" {
		t.Fatalf("optional bytes must not discard a valid transaction: metrics=%+v quality=%+v", a.Metrics, a.Quality)
	}
	for _, step := range a.Steps {
		if step.Label == "tx" && (step.BytesReceived != 123 || step.BytesSent != 45) {
			t.Fatalf("%+v", step)
		}
		if step.Label == "bad" && (step.SampleCount != 1 || step.BytesReceived != 0 || step.BytesSent != 1) {
			t.Fatalf("valid fields from an incomplete record were lost: %+v", step)
		}
	}
}
func TestReviewPhysicalCSVLines(t *testing.T) {
	s := "timeStamp,elapsed,label,success,responseMessage\n1000,10,\"multi\nlabel\",false,\"multi\nmessage\"\n1001,10,next,false,plain\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(s), ParseOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(a.Issues) != 2 || a.Issues[0].LineFrom != 2 || a.Issues[0].LineTo != 4 || a.Issues[1].LineFrom != 5 || a.Issues[1].LineTo != 5 {
		t.Fatalf("%+v", a.Issues)
	}
}

func TestIntegrationEngineIdentityAndAggregateRows(t *testing.T) {
	options := ParseOptions{ExpectedNodes: []string{"worker-1"}, RunStatus: "COMPLETED", ScopeConfirmed: true, Targets: Targets{MinSuccessRate: ptr(99)}}
	raw := "timeStamp,elapsed,label,success,Hostname,engine_id,SampleCount\n1000,100,a,true,docker-host,worker-1,1\n"
	a, e := ParseJTL(context.Background(), strings.NewReader(raw), options)
	if e != nil || a.Quality.NodeCoverage != "COMPLETE" || a.Verdict.Status != "PASS" {
		t.Fatalf("engine_id ignored: %#v %v", a.Quality, e)
	}
	raw = strings.Replace(raw, ",1\n", ",2\n", 1)
	a, e = ParseJTL(context.Background(), strings.NewReader(raw), options)
	if e != nil || a.Verdict.Status != "INCOMPLETE" || a.Metrics.SampleScope != "sample_records" {
		t.Fatalf("aggregated rows treated as original samples: %#v %v", a, e)
	}
}

func TestReviewURLAndAttribution(t *testing.T) {
	h := "timeStamp,elapsed,label,success,engine_id\n"
	for _, n := range []string{"other", ""} {
		a, e := ParseJTL(context.Background(), strings.NewReader(h+"1,1,https://h/p?customer=opaque#auth,true,n\n2,1,ok,true,"+n+"\n"), ParseOptions{ExpectedNodes: []string{"n"}, ScopeConfirmed: true, RunStatus: "COMPLETED", Targets: Targets{MinSuccessRate: fp(95)}})
		if e != nil || a.Verdict.Status != "INCOMPLETE" || strings.Contains(a.Steps[0].Label, "opaque") {
			t.Fatalf("coverage/redaction: %+v %v", a, e)
		}
	}
}
func TestReviewCSVLimits(t *testing.T) {
	h := "timeStamp,elapsed,label,success\n"
	o := ParseOptions{MaxFields: 4, MaxRecordBytes: 60}
	for _, v := range []string{strings.Repeat(",", 80) + "\n", h + "1,1,a,true,extra\n", h + "1,1,\"" + strings.Repeat("a\n", 40) + "\",true\n"} {
		if _, err := ParseJTL(context.Background(), strings.NewReader(v), o); err == nil {
			t.Fatal("unbounded CSV record accepted")
		}
	}
	a, err := ParseJTL(context.Background(), strings.NewReader(h+"1,1,\"a,\"\"b\nc\",true\r\n"), o)
	if err != nil || a.Metrics.SampleCount != 1 || a.Steps[0].Label != "a,\"b\nc" {
		t.Fatal(a, err)
	}
}

func TestReviewRedactURLBeforePersistence(t *testing.T) {
	red := NewRedactor([]string{"opaque?fragment"})
	for _, test := range []struct{ value, path string }{
		{"WARN GET https://svc/t?email=opaque%40example.org&code=once#private", "https://svc/t"},
		{"POST /relative?email=opaque%40example.org#private", "/relative"},
		{"GET /fragment#opaque", "/fragment"},
		{"GET /nested%3Fcode%3Dopaque%23private", "/nested"},
		{"script opaque%3Ffragment", "script"},
		{`{"password":"opaque?fragment","id":"public"}`, "password"},
	} {
		v := red(test.value)
		if strings.Contains(v, "opaque") || strings.Contains(v, "private") || strings.Contains(v, "once") || !strings.Contains(v, test.path) {
			t.Fatal(v)
		}
	}
}

func TestReviewURLAuthorityAndSecretOverlap(t *testing.T) {
	redact := NewRedactor([]string{"?email=user@example.org", "opaque?who=me@example.org"})
	for _, tc := range []struct{ input, want string }{
		{"https://svc?email=a@example.org&code=one-time", "https://svc[REDACTED]"},
		{"https://svc?email=user@example.org&code=one-time", "https://svc[REDACTED]"},
		{"./relative?email=user@example.org&code=one-time", "./relative[REDACTED]"},
		{"?email=user@example.org&code=one-time", "[REDACTED]"},
		{"https://user:pass@svc#contact=a@example.org&code=one-time", "https://[REDACTED]@svc[REDACTED]"},
		{"/relative/opaque?who=me@example.org&code=one-time", "/relative/[REDACTED]"},
		{`{"password":"arbitrary?who=me@example.org","id":"public"}`, `{"password":"[REDACTED]","id":"public"}`},
	} {
		if got := redact(tc.input); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
