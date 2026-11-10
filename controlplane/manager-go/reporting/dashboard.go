package reporting

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const dashboardMaxPoints = 12000
const dashboardMaxErrors = 512
const dashboardMaxCodes = 128

// Bound new histogram allocations as well as point cardinality for a 1GiB manager.
const dashboardMaxHistogramCells = 2000000

var distributionBounds = [...]float64{0, 1, 10, 50, 100, 250, 500, 1000, 1500, 2000, 5000, 10000, 30000, 60000, 1e12 + 1}

type dashboardScopeID struct{ node, step string }
type dashboardErrorID struct{ category, code, message string }
type threadObservation struct{ timestamp, value int64 }
type dashboardRecord struct {
	ts, elapsed                     int64
	node, key, label, kind          string
	ok, scopeUnconfirmed, kindKnown bool
	latency, connect                *float64
	threads, received, sent         *int64
	code, category, message         string
}
type dashboardAggregate struct {
	rt                                                     *agg
	successful, latency, connect                           agg
	transactions, knownKinds, receivedRecords, sentRecords int64
	codes                                                  map[string]int64
	errors                                                 map[dashboardErrorID]int64
	apdex                                                  Apdex
	distribution                                           [len(distributionBounds) - 1]int64
}
type dashboardBucket struct {
	data    dashboardAggregate
	threads map[string]threadObservation
}
type dashboardAccumulator struct {
	id          dashboardScopeID
	label, kind string
	data        dashboardAggregate
	points      map[int64]*dashboardBucket
}
type dashboardBuilder struct {
	adaptive, compacted                                                                                bool
	options                                                                                            DashboardOptions
	scopes                                                                                             map[dashboardScopeID]*dashboardAccumulator
	expected                                                                                           map[string]bool
	red                                                                                                func(string) string
	records, latencyRecords, connectRecords, threadRecords, codeRecords, byteRecords, knownKindRecords int64
	pointCount, histogramCells, maxHistogramCells                                                      int
	optionalInvalid                                                                                    bool
	partialThreads                                                                                     bool
}

func optionalNumber(get func(string) string, names ...string) (*float64, bool) {
	invalid := false
	for _, name := range names {
		raw := strings.TrimSpace(get(name))
		if raw == "" || strings.EqualFold(raw, "null") || strings.EqualFold(raw, "<null>") {
			continue
		}
		v, e := strconv.ParseFloat(raw, 64)
		if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e12 {
			invalid = true
			continue
		}
		return &v, invalid
	}
	return nil, invalid
}
func optionalInteger(get func(string) string, name string, max int64) (*int64, bool) {
	raw := strings.TrimSpace(get(name))
	if raw == "" || strings.EqualFold(raw, "null") || strings.EqualFold(raw, "<null>") {
		return nil, false
	}
	v, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || v < 0 || v > max {
		return nil, true
	}
	return &v, false
}
func optionalCode(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "null") || strings.EqualFold(raw, "<null>") {
		return ""
	}
	return raw
}
func requestFailure(get func(string) string) (string, string) {
	message := strings.TrimSpace(get("failureMessage") + " " + get("assertionFailureMessage"))
	if message != "" {
		return "assertion_failure", message
	}
	message = strings.TrimSpace(get("responseMessage"))
	if message == "" {
		if code := optionalCode(get("responseCode")); code != "" {
			message = "responseCode=" + code
		}
	}
	if message == "" {
		message = "JTL未记录失败原因"
	}
	return "sample_failure", message
}
func newDashboardBuilder(options DashboardOptions, details *nodeDetails, all *agg, red func(string) string) *dashboardBuilder {
	b := &dashboardBuilder{options: options, scopes: map[dashboardScopeID]*dashboardAccumulator{}, expected: details.expected, red: red, maxHistogramCells: dashboardMaxHistogramCells}
	b.scope(dashboardScopeID{}, "全部采样", "ordinary", all)
	for id, n := range details.nodes {
		b.scope(dashboardScopeID{node: id}, red(id), "ordinary", &n.ordinary)
	}
	return b
}
func (b *dashboardBuilder) scope(id dashboardScopeID, label, kind string, rt *agg) *dashboardAccumulator {
	s := b.scopes[id]
	if s == nil {
		s = &dashboardAccumulator{id: id, label: label, kind: kind, data: dashboardAggregate{rt: rt}, points: map[int64]*dashboardBucket{}}
		b.scopes[id] = s
	}
	return s
}
func histogramBin(value float64) int {
	if value == 0 {
		return 0
	}
	return int(math.Ceil(math.Log1p(value) * 100))
}
func (b *dashboardBuilder) histogramAdd(a *agg, ts int64, value float64, ok bool, received, sent int64) error {
	bin := histogramBin(value)
	if a.hist[bin] == 0 {
		if b.histogramCells >= b.maxHistogramCells {
			return fmt.Errorf("JTL dashboard histogram cell limit exceeded")
		}
		b.histogramCells++
	}
	a.addValue(ts, value, ok, received, sent)
	return nil
}
func (b *dashboardBuilder) add(r dashboardRecord, all, step *agg, details *nodeDetails, rawStepKey string) error {
	if b.adaptive {
		for (b.pointCount >= dashboardMaxPoints-4 || b.histogramCells >= b.maxHistogramCells*4/5) && b.options.GranularityMS <= 1800000 {
			b.compactPoints()
		}
	}
	b.records++
	if r.latency != nil {
		b.latencyRecords++
	}
	if r.connect != nil {
		b.connectRecords++
	}
	if r.threads != nil && r.node != UnassignedNodeID {
		b.threadRecords++
	}
	if r.code != "" {
		b.codeRecords++
	}
	if r.received != nil {
		b.byteRecords++
	}
	if r.sent != nil {
		b.byteRecords++
	}
	if r.kindKnown {
		b.knownKindRecords++
	}
	specs := []struct {
		id          dashboardScopeID
		label, kind string
		rt          *agg
	}{
		{dashboardScopeID{}, "全部采样", "ordinary", all},
		{dashboardScopeID{node: r.node}, b.red(r.node), "ordinary", &details.nodes[r.node].ordinary},
		{dashboardScopeID{step: r.key}, r.label, r.kind, step},
		{dashboardScopeID{node: r.node, step: r.key}, r.label, r.kind, details.steps[nodeStepIdentity{r.node, rawStepKey}]},
	}
	for _, spec := range specs {
		s := b.scope(spec.id, spec.label, spec.kind, spec.rt)
		eligible := r.kind != "transaction" || s.kind == "transaction"
		if err := b.addData(&s.data, r, eligible, false); err != nil {
			return err
		}
		timestamp := r.ts / b.options.GranularityMS * b.options.GranularityMS
		p := s.points[timestamp]
		if p == nil {
			if b.pointCount >= dashboardMaxPoints {
				return fmt.Errorf("JTL dashboard point limit exceeded")
			}
			b.pointCount++
			p = &dashboardBucket{data: dashboardAggregate{rt: &agg{}}, threads: map[string]threadObservation{}}
			s.points[timestamp] = p
		}
		if err := b.addData(&p.data, r, eligible, true); err != nil {
			return err
		}
		if r.threads != nil && r.node != UnassignedNodeID {
			prior, seen := p.threads[r.node]
			// Same-timestamp ties use a deterministic maximum at that one instant.
			if !seen || r.ts > prior.timestamp || r.ts == prior.timestamp && *r.threads > prior.value {
				p.threads[r.node] = threadObservation{r.ts, *r.threads}
			}
		}
	}
	return nil
}
func (b *dashboardBuilder) addData(d *dashboardAggregate, r dashboardRecord, eligible, point bool) error {
	if r.kind == "transaction" {
		d.transactions++
	}
	if r.kindKnown {
		d.knownKinds++
	}
	if !eligible {
		return nil
	}
	received, sent := int64(0), int64(0)
	if r.received != nil {
		received = *r.received
		d.receivedRecords++
	}
	if r.sent != nil {
		sent = *r.sent
		d.sentRecords++
	}
	if point {
		if err := b.histogramAdd(d.rt, r.ts, float64(r.elapsed), r.ok, received, sent); err != nil {
			return err
		}
		d.rt.scopeUnconfirmed = d.rt.scopeUnconfirmed || r.scopeUnconfirmed
		if r.ok {
			if err := b.histogramAdd(&d.successful, r.ts, float64(r.elapsed), true, 0, 0); err != nil {
				return err
			}
		}
	}
	if r.latency != nil {
		if err := b.histogramAdd(&d.latency, r.ts, *r.latency, true, 0, 0); err != nil {
			return err
		}
	}
	if r.connect != nil {
		if err := b.histogramAdd(&d.connect, r.ts, *r.connect, true, 0, 0); err != nil {
			return err
		}
	}
	if r.code != "" {
		if d.codes == nil {
			d.codes = map[string]int64{}
		}
		if _, seen := d.codes[r.code]; !seen && len(d.codes) >= dashboardMaxCodes {
			return fmt.Errorf("JTL dashboard response code limit exceeded")
		}
		d.codes[r.code]++
	}
	if !point {
		d.apdex.Count++
		switch {
		case !r.ok || float64(r.elapsed) > b.options.ApdexToleratedMS:
			d.apdex.Frustrated++
		case float64(r.elapsed) <= b.options.ApdexSatisfiedMS:
			d.apdex.Satisfied++
		default:
			d.apdex.Tolerated++
		}
		for i := range d.distribution {
			if float64(r.elapsed) >= distributionBounds[i] && float64(r.elapsed) < distributionBounds[i+1] {
				d.distribution[i]++
				break
			}
		}
		if !r.ok {
			if d.errors == nil {
				d.errors = map[dashboardErrorID]int64{}
			}
			key := dashboardErrorID{r.category, r.code, r.message}
			if _, seen := d.errors[key]; !seen && len(d.errors) >= dashboardMaxErrors {
				return fmt.Errorf("JTL dashboard distinct error limit exceeded")
			}
			d.errors[key]++
		}
	}
	return nil
}
func timingStats(a *agg) TimingStats {
	if a.n == 0 {
		return TimingStats{}
	}
	return TimingStats{SampleCount: a.n, MeanMS: ptr(a.sum / float64(a.n)), MinMS: ptr(a.min), MaxMS: ptr(a.max), P50MS: a.quant(.5), P90MS: a.quant(.9), P95MS: a.quant(.95), P99MS: a.quant(.99)}
}
func responseCodes(codes map[string]int64, total int64, seconds *float64) []ResponseCode {
	out := make([]ResponseCode, 0, len(codes))
	for code, count := range codes {
		p := float64(0)
		if total > 0 {
			p = float64(count) * 100 / float64(total)
		}
		row := ResponseCode{Code: code, Count: count, Percent: p}
		if seconds != nil && *seconds > 0 {
			row.RPS = ptr(float64(count) / *seconds)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}
func fullPercentiles(a *agg) []Percentile {
	out := []Percentile{}
	if a.n == 0 {
		return out
	}
	keys := make([]int, 0, len(a.hist))
	for k := range a.hist {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	index := 0
	var cumulative int64
	for p := 0; p <= 100; p++ {
		value := a.min
		if p == 100 {
			value = a.max
		} else if p > 0 {
			want := int64(math.Ceil(float64(a.n) * float64(p) / 100))
			for cumulative < want && index < len(keys) {
				cumulative += a.hist[keys[index]]
				index++
			}
			value = math.Min(a.max, math.Expm1(float64(keys[index-1])/100))
		}
		out = append(out, Percentile{Percentile: float64(p), ValueMS: ptr(value)})
	}
	return out
}
func capability(have, total int64) string {
	if have == 0 {
		return "MISSING"
	}
	if have < total {
		return "PARTIAL"
	}
	return "AVAILABLE"
}
func (b *dashboardBuilder) finish() Dashboard {
	d := Dashboard{Version: 1, Options: b.options, Warnings: []string{}, Scopes: []DashboardScope{}, Capabilities: DashboardCapabilities{ActiveThreads: capability(b.threadRecords, b.records), Latency: capability(b.latencyRecords, b.records), Connect: capability(b.connectRecords, b.records), ResponseCodes: capability(b.codeRecords, b.records), Bytes: capability(b.byteRecords, b.records*2), Transactions: capability(b.knownKindRecords, b.records)}}
	if b.compacted {
		d.Warnings = append(d.Warnings, fmt.Sprintf("趋势数据量较大，时间桶已自动合并为%d毫秒；所有采样均参与统计，范围计数和分位数口径不变", b.options.GranularityMS))
	}
	ids := make([]dashboardScopeID, 0, len(b.scopes))
	for id := range b.scopes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].node != ids[j].node {
			return ids[i].node < ids[j].node
		}
		return ids[i].step < ids[j].step
	})
	for _, id := range ids {
		s := b.scopes[id]
		metrics := s.data.rt.metrics()
		metrics.TransactionCount = s.data.transactions
		if s.kind == "transaction" {
			metrics.SampleScope = "transaction_samples"
		}
		apdex := s.data.apdex
		apdex.SatisfiedMS = b.options.ApdexSatisfiedMS
		apdex.ToleratedMS = b.options.ApdexToleratedMS
		if apdex.Count > 0 {
			apdex.Score = ptr((float64(apdex.Satisfied) + float64(apdex.Tolerated)/2) / float64(apdex.Count))
		}
		out := DashboardScope{NodeID: b.red(id.node), StepKey: id.step, Label: s.label, SampleKind: s.kind, Metrics: metrics, Latency: timingStats(&s.data.latency), Connect: timingStats(&s.data.connect), Apdex: apdex, Points: []DashboardPoint{}, Distribution: []DistributionBin{}, Percentiles: fullPercentiles(s.data.rt), ResponseCodes: responseCodes(s.data.codes, metrics.SampleCount, metrics.DurationSeconds), Errors: []DashboardError{}, TopErrors: []DashboardError{}}
		if metrics.SampleCount > 0 {
			for i, count := range s.data.distribution {
				if distributionBounds[i] > s.data.rt.max {
					break
				}
				out.Distribution = append(out.Distribution, DistributionBin{LowerMS: distributionBounds[i], UpperMS: distributionBounds[i+1], Count: count})
			}
		}
		for key, count := range s.data.errors {
			out.Errors = append(out.Errors, DashboardError{Category: key.category, Code: key.code, Message: key.message, Count: count, PercentTotal: float64(count) * 100 / float64(metrics.SampleCount), PercentErrors: float64(count) * 100 / float64(metrics.FailureCount)})
		}
		sort.Slice(out.Errors, func(i, j int) bool {
			x, y := out.Errors[i], out.Errors[j]
			if x.Count != y.Count {
				return x.Count > y.Count
			}
			if x.Category != y.Category {
				return x.Category < y.Category
			}
			if x.Code != y.Code {
				return x.Code < y.Code
			}
			return x.Message < y.Message
		})
		out.TopErrors = append(out.TopErrors, out.Errors[:min(5, len(out.Errors))]...)
		timestamps := make([]int64, 0, len(s.points))
		for ts := range s.points {
			timestamps = append(timestamps, ts)
		}
		sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
		for _, ts := range timestamps {
			out.Points = append(out.Points, b.point(s, s.points[ts], ts))
		}
		d.Scopes = append(d.Scopes, out)
	}
	for _, c := range []struct{ name, status string }{{"活跃线程", d.Capabilities.ActiveThreads}, {"Latency", d.Capabilities.Latency}, {"Connect", d.Capabilities.Connect}, {"响应代码", d.Capabilities.ResponseCodes}, {"字节", d.Capabilities.Bytes}, {"事务分类", d.Capabilities.Transactions}} {
		if c.status != "AVAILABLE" {
			d.Warnings = append(d.Warnings, c.name+"字段证据"+map[string]string{"MISSING": "缺失", "PARTIAL": "不完整"}[c.status]+"；仅展示有效采样观测，不填充缺失值")
		}
	}
	if b.optionalInvalid {
		d.Warnings = append(d.Warnings, "无效的可选字段已忽略；有效的请求采样仍保留")
	}
	if b.partialThreads {
		d.Warnings = append(d.Warnings, "部分时间桶的线程观测未覆盖所有预期节点；活跃线程为采样观测之和，不能视为整个集群的精确并发")
	}
	return d
}
func (b *dashboardBuilder) point(s *dashboardAccumulator, p *dashboardBucket, ts int64) DashboardPoint {
	a := p.data.rt
	seconds := float64(b.options.GranularityMS) / 1000
	out := DashboardPoint{TimestampMS: ts, WidthMS: b.options.GranularityMS, SampleCount: a.n, SuccessCount: a.ok, FailureCount: a.n - a.ok, TransactionCount: p.data.transactions, MeanMS: nil, P50MS: a.quant(.5), P90MS: a.quant(.9), P95MS: a.quant(.95), P99MS: a.quant(.99), SuccessfulP90MS: p.data.successful.quant(.9), SuccessfulP95MS: p.data.successful.quant(.95), SuccessfulP99MS: p.data.successful.quant(.99), LatencyP95MS: p.data.latency.quant(.95), ConnectP95MS: p.data.connect.quant(.95), ResponseCodes: responseCodes(p.data.codes, a.n, &seconds)}
	if s.kind != "transaction" {
		out.RPS = ptr(float64(a.n) / seconds)
		out.SuccessRPS = ptr(float64(a.ok) / seconds)
		out.FailureRPS = ptr(float64(a.n-a.ok) / seconds)
	}
	if p.data.knownKinds > 0 {
		out.TPS = ptr(float64(p.data.transactions) / seconds)
	}
	if a.n > 0 {
		out.MeanMS = ptr(a.sum / float64(a.n))
	}
	if p.data.latency.n > 0 {
		out.LatencyMeanMS = ptr(p.data.latency.sum / float64(p.data.latency.n))
	}
	if p.data.connect.n > 0 {
		out.ConnectMeanMS = ptr(p.data.connect.sum / float64(p.data.connect.n))
	}
	if p.data.receivedRecords > 0 {
		out.BytesReceivedPerSecond = ptr(float64(a.br) / seconds)
	}
	if p.data.sentRecords > 0 {
		out.BytesSentPerSecond = ptr(float64(a.bs) / seconds)
	}
	out.ThreadsObservedNodes = len(p.threads)
	out.ThreadsExpectedNodes = len(b.expected)
	if s.id.node != "" {
		out.ThreadsExpectedNodes = 0
		if b.expected[s.id.node] {
			out.ThreadsExpectedNodes = 1
		}
	}
	if out.ThreadsObservedNodes > 0 {
		var n int64
		overflow := false
		for _, v := range p.threads {
			if v.value > math.MaxInt64-n {
				overflow = true
				break
			}
			n += v.value
		}
		if !overflow {
			out.ActiveThreads = &n
		} else {
			b.optionalInvalid = true
		}
	}
	if s.id.node == "" {
		// Equal cardinalities do not prove coverage when a foreign worker
		// replaces a missing expected one. Compare identities in this bucket.
		for node := range b.expected {
			if _, seen := p.threads[node]; !seen {
				b.partialThreads = true
			}
		}
		for node := range p.threads {
			if !b.expected[node] {
				b.partialThreads = true
			}
		}
	} else if out.ThreadsObservedNodes != out.ThreadsExpectedNodes {
		b.partialThreads = true
	}
	return out
}
