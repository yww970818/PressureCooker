package reporting

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

type agg struct {
	n, ok, br, bs    int64
	sum, min, max    float64
	start, end       int64
	hist             map[int]int64
	scopeUnconfirmed bool
}

func ptr(v float64) *float64 { return &v }
func (a *agg) add(ts, elapsed int64, ok bool, br, bs int64) {
	a.addValue(ts, float64(elapsed), ok, br, bs)
}
func (a *agg) addValue(ts int64, elapsed float64, ok bool, br, bs int64) {
	if a.n == 0 {
		a.min = float64(elapsed)
		a.start = ts
	}
	a.n++
	if ok {
		a.ok++
	}
	a.br += br
	a.bs += bs
	a.sum += float64(elapsed)
	a.min = math.Min(a.min, float64(elapsed))
	a.max = math.Max(a.max, float64(elapsed))
	if ts < a.start {
		a.start = ts
	}
	if ts+int64(math.Ceil(elapsed)) > a.end {
		a.end = ts + int64(math.Ceil(elapsed))
	}
	if a.hist == nil {
		a.hist = map[int]int64{}
	}
	bin := histogramBin(elapsed)
	a.hist[bin]++
}
func (a *agg) quant(p float64) *float64 {
	if a.n == 0 {
		return nil
	}
	keys := make([]int, 0, len(a.hist))
	for k := range a.hist {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	want := int64(math.Ceil(float64(a.n) * p))
	var n int64
	for _, k := range keys {
		n += a.hist[k]
		if n >= want {
			return ptr(math.Min(a.max, math.Expm1(float64(k)/100)))
		}
	}
	return nil
}
func (a *agg) metrics() Metrics {
	m := Metrics{SampleScope: "ordinary_samples", SampleCount: a.n, SuccessCount: a.ok, FailureCount: a.n - a.ok, BytesReceived: a.br, BytesSent: a.bs, PercentileMethod: "log_histogram_1pct_upper_bound"}
	if a.scopeUnconfirmed {
		m.SampleScope = "sample_records"
	}
	if a.n > 0 {
		m.SuccessRate = ptr(float64(a.ok) * 100 / float64(a.n))
		m.MeanMS = ptr(a.sum / float64(a.n))
		m.MinMS = ptr(a.min)
		m.MaxMS = ptr(a.max)
		m.P50MS = a.quant(.5)
		m.P90MS = a.quant(.9)
		m.P95MS = a.quant(.95)
		m.P99MS = a.quant(.99)
		d := float64(a.end-a.start) / 1000
		m.DurationSeconds = ptr(d)
		if d > 0 {
			m.RPS = ptr(float64(a.n) / d)
		}
	}
	return m
}
func ParseJTL(ctx context.Context, r io.Reader, o ParseOptions) (Analysis, error) {
	a := Analysis{Quality: Quality{MetricStatus: "COMPLETE", LogStatus: "UNKNOWN", Warnings: []string{}, ExpectedNodes: []string{}, ObservedNodes: []string{}, NodeCoverage: "UNKNOWN", ScopeConfirmed: true}, Timeline: []Point{}, Steps: []Step{}, Nodes: []Node{}, NodeSteps: []Step{}, Issues: []Issue{}, Findings: []Finding{}, RequestSamples: []RequestSample{}}
	if err := ValidateTargets(o.Targets); err != nil {
		return a, err
	}
	options, err := NormalizeDashboardOptions(o.AnalyticsOptions)
	if err != nil {
		return a, err
	}
	if o.Redactor == nil {
		o.Redactor = NewRedactor(nil)
	}
	red := func(s string) string {
		s = o.Redactor(s)
		if len(s) > 2048 {
			s = s[:2048] + "…"
		}
		return s
	}
	for _, id := range o.ExpectedNodes {
		a.Quality.ExpectedNodes = append(a.Quality.ExpectedNodes, red(id))
	}
	if o.MaxLabels <= 0 {
		o.MaxLabels = 500
	}
	if o.MaxNodes <= 0 {
		o.MaxNodes = 1024
	}
	if o.MaxNodeSteps <= 0 {
		o.MaxNodeSteps = 4096
	}
	details, err := newNodeDetails(o.ExpectedNodes, o.MaxNodes, o.MaxNodeSteps)
	if err != nil {
		return a, err
	}
	if o.MaxBuckets <= 0 {
		o.MaxBuckets = 3600
	}
	if o.MaxIssues <= 0 {
		o.MaxIssues = 500
	}
	if o.MaxRows <= 0 {
		o.MaxRows = 10000000
	}
	if o.Targets.MaxP95MS != nil {
		a.Quality.exactP95 = &thresholdCount{target: *o.Targets.MaxP95MS}
	}
	if o.Targets.MaxP99MS != nil {
		a.Quality.exactP99 = &thresholdCount{target: *o.Targets.MaxP99MS}
	}
	lr := &io.LimitedReader{R: r, N: 128 << 20}
	if o.MaxFields <= 0 {
		o.MaxFields = 256
	}
	if o.MaxRecordBytes <= 0 {
		o.MaxRecordBytes = 1 << 20
	}
	guard := &csvGuard{r: lr, maxFields: o.MaxFields, maxBytes: o.MaxRecordBytes}
	cr := csv.NewReader(guard)
	cr.FieldsPerRecord = -1
	h, err := cr.Read()
	if err != nil {
		return a, fmt.Errorf("JTL header invalid")
	}
	idx := map[string]int{}
	for i, k := range h {
		k = strings.TrimPrefix(k, "\ufeff")
		if _, ok := idx[k]; ok {
			return a, fmt.Errorf("JTL duplicate header")
		}
		idx[k] = i
	}
	for _, k := range []string{"timeStamp", "elapsed", "label", "success"} {
		if _, ok := idx[k]; !ok {
			return a, fmt.Errorf("JTL missing required column %s", k)
		}
	}
	get := func(row []string, k string) string {
		if i, ok := idx[k]; ok && i < len(row) {
			return row[i]
		}
		return ""
	}
	all := &agg{}
	dashboard := newDashboardBuilder(options, details, all, red)
	dashboard.adaptive = o.AdaptiveDashboard
	requestSamples := newRequestSampleCollector()
	steps := map[string]*agg{}
	kinds := map[string]string{}
	labels := map[string]string{}
	buckets := map[int64]*agg{}
	nodes := map[string]bool{}
	expected := details.expected
	unassigned := false
	var tx int64
	var rows int64
	for {
		if err := ctx.Err(); err != nil {
			return a, err
		}
		row, e := cr.Read()
		if e == io.EOF {
			break
		}
		rows++
		if rows > o.MaxRows {
			return a, fmt.Errorf("JTL row limit exceeded")
		}
		if e != nil {
			a.Quality.InvalidRows++
			if _, ok := e.(*csv.ParseError); ok {
				continue
			}
			return a, fmt.Errorf("JTL read failed")
		}
		if len(row) != len(h) {
			a.Quality.InvalidRows++
			continue
		}
		firstLine, _ := cr.FieldPos(0)
		lastLine, _ := cr.FieldPos(len(row) - 1)
		lastLine += strings.Count(row[len(row)-1], "\n")
		ts, e1 := strconv.ParseInt(get(row, "timeStamp"), 10, 64)
		el, e2 := strconv.ParseInt(get(row, "elapsed"), 10, 64)
		ok, e3 := strconv.ParseBool(get(row, "success"))
		if e1 != nil || e2 != nil || e3 != nil || ts < 0 || ts > 1e16 || el < 0 || el > 1e12 {
			a.Quality.InvalidRows++
			continue
		}
		if o.TimestampIsEnd {
			ts -= el
			if ts < 0 {
				a.Quality.InvalidRows++
				continue
			}
		}
		if get(row, "failureMessage") != "" || get(row, "assertionFailureMessage") != "" {
			ok = false
		}

		scopeUnconfirmed := false
		for _, field := range []string{"SampleCount", "sampleCount"} {
			if value := get(row, field); value != "" {
				count, err := strconv.ParseInt(value, 10, 64)
				if err != nil || count < 1 {
					a.Quality.InvalidRows++
					e1 = fmt.Errorf("invalid aggregate count")
					break
				}
				if count > 1 {
					scopeUnconfirmed = true
					a.Quality.ScopeConfirmed = false
					a.Quality.MetricStatus = "PARTIAL"
					a.Quality.Warnings = appendUnique(a.Quality.Warnings, "包含合并采样记录，无法还原每个原始请求的耗时分布")
				}
			}
		}
		if e1 != nil {
			continue
		}
		field := func(name string) string { return get(row, name) }
		received, badReceived := optionalInteger(field, "bytes", 1e11)
		sent, badSent := optionalInteger(field, "sentBytes", 1e11)
		latency, badLatency := optionalNumber(field, "Latency", "latency")
		connect, badConnect := optionalNumber(field, "Connect", "connect", "connectTime")
		threads, badThreads := optionalInteger(field, "allThreads", math.MaxInt64)
		dashboard.optionalInvalid = dashboard.optionalInvalid || badReceived || badSent || badLatency || badConnect || badThreads
		br, bs := int64(0), int64(0)
		if received != nil {
			br = *received
		}
		if sent != nil {
			bs = *sent
		}
		kind := strings.ToLower(get(row, "sample_kind"))
		if kind == "" {
			kind = strings.ToLower(get(row, "sampleKind"))
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(get(row, "responseMessage"))), "number of samples in transaction :") {
			kind = "transaction"
		}
		switch kind {
		case "transaction", "transaction_sample":
			kind = "transaction"
		case "ordinary", "ordinary_samples", "sample", "http":
			kind = "ordinary"
		default:
			kind = "unknown"
			if !o.ScopeConfirmed {
				scopeUnconfirmed = true
				a.Quality.ScopeConfirmed = false
			}
		}
		node := UnassignedNodeID
		for _, field := range []string{"engine_id", "hostname", "Hostname"} {
			candidate := strings.TrimSpace(get(row, field))
			// JMeter writes a literal null for unset sample variables. It is
			// missing identity evidence, so try the next explicit identity field.
			if candidate == "" || strings.EqualFold(candidate, "null") || strings.EqualFold(candidate, "<null>") {
				continue
			}
			node = candidate
			break
		}
		if node == UnassignedNodeID || !expected[node] {
			unassigned = true
		}
		if node != UnassignedNodeID && kind != "transaction" {
			nodes[node] = true
		}
		rawLabel := get(row, "label")
		label := red(rawLabel)
		key := kind + "\x00" + rawLabel
		if steps[key] == nil {
			if len(steps) >= o.MaxLabels {
				return a, fmt.Errorf("JTL label limit exceeded")
			}
			steps[key] = &agg{}
			kinds[key] = kind
			labels[key] = label
		}
		if err := details.add(node, key, kind, ts, el, ok, br, bs, scopeUnconfirmed); err != nil {
			return a, err
		}
		steps[key].add(ts, el, ok, br, bs)
		steps[key].scopeUnconfirmed = steps[key].scopeUnconfirmed || scopeUnconfirmed
		if kind == "transaction" {
			tx++
		} else {
			bucket := ts / options.GranularityMS * options.GranularityMS
			if buckets[bucket] == nil {
				if len(buckets) >= o.MaxBuckets {
					return a, fmt.Errorf("JTL timeline bucket limit exceeded")
				}
				buckets[bucket] = &agg{}
			}
			all.add(ts, el, ok, br, bs)
			buckets[bucket].add(ts, el, ok, br, bs)
			for _, c := range []*thresholdCount{a.Quality.exactP95, a.Quality.exactP99} {
				if c != nil {
					c.total++
					if float64(el) <= c.target {
						c.atOrBelow++
					}
				}
			}
		}
		stepKey := stableStepKey(kind, rawLabel)
		category, message := requestFailure(field)
		if err := dashboard.add(dashboardRecord{ts: ts, elapsed: el, node: node, key: stepKey, label: label, kind: kind, ok: ok, scopeUnconfirmed: scopeUnconfirmed, kindKnown: kind != "unknown" || o.ScopeConfirmed, latency: latency, connect: connect, threads: threads, received: received, sent: sent, code: red(optionalCode(get(row, "responseCode"))), category: category, message: red(message)}, all, steps[key], details, key); err != nil {
			return a, err
		}
		publicNode := red(node)
		if requestSamples.wants(requestSampleSlot{publicNode, stepKey, ok}) {
			sample := csvRequestSample(row, idx, publicNode, stepKey, rawLabel, kind, ts, el, ok)
			requestSamples.add(SanitizeRequestSample(sample, o.Redactor), false)
		}
		if !ok && len(a.Issues) < o.MaxIssues {
			message := strings.TrimSpace(get(row, "responseMessage") + " " + get(row, "failureMessage") + " " + get(row, "assertionFailureMessage"))
			if message == "" && strings.TrimSpace(get(row, "responseCode")) != "" {
				message = "responseCode=" + strings.TrimSpace(get(row, "responseCode"))
			}
			a.Issues = append(a.Issues, Issue{ID: fmt.Sprintf("sample-%d", rows), NodeID: red(node), StepKey: stableStepKey(kind, rawLabel), SourceKind: "jtl", SourceName: "results.jtl", Category: "sample_failure", Level: "ERROR", Label: label, StepLabel: label, Message: red(message), Count: 1, FirstAt: &ts, LastAt: &ts, LineFrom: int64(firstLine), LineTo: int64(lastLine)})
		} else if !ok && len(a.Issues) == o.MaxIssues {
			a.Quality.Warnings = appendUnique(a.Quality.Warnings, "失败样本明细达到上限；整体失败计数保留")
		}
	}
	if guard.err != nil {
		return a, guard.err
	}
	if lr.N == 0 {
		return a, fmt.Errorf("JTL size limit exceeded")
	}
	a.Metrics = all.metrics()
	a.Metrics.TransactionCount = tx
	if !a.Quality.ScopeConfirmed {
		a.Metrics.SampleScope = "sample_records"
	}
	all.scopeUnconfirmed = !a.Quality.ScopeConfirmed
	if a.Quality.InvalidRows > 0 || all.n == 0 {
		a.Quality.MetricStatus = "PARTIAL"
	}
	for n := range nodes {
		a.Quality.ObservedNodes = append(a.Quality.ObservedNodes, red(n))
	}
	sort.Strings(a.Quality.ObservedNodes)
	if len(o.ExpectedNodes) > 0 {
		a.Quality.NodeCoverage = "COMPLETE"
		if unassigned {
			a.Quality.NodeCoverage = "PARTIAL"
			a.Quality.Warnings = append(a.Quality.Warnings, "部分采样记录缺少预期节点标识或来自其他节点")
		}
		for _, n := range o.ExpectedNodes {
			if !nodes[n] {
				a.Quality.NodeCoverage = "PARTIAL"
			}
		}
	}
	keys := make([]string, 0, len(steps))
	for k := range steps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if kinds[keys[i]] != kinds[keys[j]] {
			return kinds[keys[i]] < kinds[keys[j]]
		}
		if labels[keys[i]] != labels[keys[j]] {
			return labels[keys[i]] < labels[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for i, k := range keys {
		metrics := steps[k].metrics()
		if kinds[k] == "transaction" {
			metrics.SampleScope = "transaction_samples"
			metrics.TransactionCount = metrics.SampleCount
		} else if kinds[k] == "unknown" && !o.ScopeConfirmed {
			metrics.SampleScope = "sample_records"
		}
		a.Steps = append(a.Steps, Step{ID: fmt.Sprintf("step-%d", i+1), StepKey: stableStepKey(kinds[k], strings.SplitN(k, "\x00", 2)[1]), Label: labels[k], SampleKind: kinds[k], Metrics: metrics})
	}
	if err := details.finish(&a, labels, kinds, red); err != nil {
		return a, err
	}
	a.Quality.Warnings = appendUnique(a.Quality.Warnings, "节点与步骤统计按采样标签和类型聚合；同名同类型流程组件无法区分")
	times := make([]int64, 0, len(buckets))
	for k := range buckets {
		times = append(times, k)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	for _, ts := range times {
		b := buckets[ts]
		a.Timeline = append(a.Timeline, Point{TimestampMS: ts, WidthMS: options.GranularityMS, SampleCount: b.n, FailureCount: b.n - b.ok, RPS: ptr(float64(b.n) * 1000 / float64(options.GranularityMS)), MeanMS: ptr(b.sum / float64(b.n)), P95MS: b.quant(.95)})
	}
	a.Dashboard = dashboard.finish()
	sortRequestSamples(requestSamples.samples)
	a.RequestSamples = requestSamples.samples
	a.RequestSamplesTruncated = requestSamples.truncated
	if a.RequestSamplesTruncated {
		a.Quality.Warnings = appendUnique(a.Quality.Warnings, "接口留样内容或采集范围不完整；仅展示已保存的有界证据")
	}
	for _, warning := range a.Dashboard.Warnings {
		a.Quality.Warnings = appendUnique(a.Quality.Warnings, warning)
	}
	a.Verdict = Evaluate(a.Metrics, a.Quality, o.Targets, o.RunStatus)
	return a, nil
}
func appendUnique(v []string, s string) []string {
	for _, x := range v {
		if x == s {
			return v
		}
	}
	return append(v, s)
}
