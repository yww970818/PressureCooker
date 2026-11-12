package reporting

import "sort"

const (
	threadObservationIntervalMS       int64 = 1000
	maxThreadObservationScopePoints         = 3600
	maxThreadObservationDisplayPoints       = 12000
)

// Thread observations have their own timeline. They never contribute samples,
// denominators, timings or timestamps to request analysis.
type ThreadObservation struct {
	TimestampMS   int64
	ActiveThreads int64
}
type ThreadObservationPoint struct {
	TimestampMS          int64  `json:"timestamp_ms"`
	ActiveThreads        *int64 `json:"active_threads"`
	ThreadsObservedNodes int    `json:"threads_observed_nodes"`
	ThreadsExpectedNodes int    `json:"threads_expected_nodes"`
}
type ThreadObservations struct {
	Source       string                   `json:"source"`
	IntervalMS   int64                    `json:"interval_ms"`
	ResolutionMS int64                    `json:"resolution_ms"`
	Status       string                   `json:"status"`
	Points       []ThreadObservationPoint `json:"points"`
	Warnings     []string                 `json:"warnings"`
}
type threadNodeObservations struct {
	buckets  map[int64]ThreadObservation
	complete bool
	gaps     bool
}
type ThreadObservationBuilder struct {
	expected   []string
	nodes      map[string]*threadNodeObservations
	resolution int64
	count      int
}

func NewThreadObservationBuilder(expected []string) *ThreadObservationBuilder {
	b := &ThreadObservationBuilder{nodes: map[string]*threadNodeObservations{}, resolution: threadObservationIntervalMS}
	for _, node := range expected {
		if _, exists := b.nodes[node]; exists {
			continue
		}
		b.expected = append(b.expected, node)
		b.nodes[node] = &threadNodeObservations{buckets: map[int64]ThreadObservation{}}
	}
	return b
}

// AddNode receives one fully validated archive at a time. The manager discards
// an entire archive on any parse/SHA error before handing it to this builder.
// Only bounded display buckets are retained; the immutable raw archive stays 1Hz.
func (b *ThreadObservationBuilder) AddNode(node string, points []ThreadObservation, complete bool) {
	n := b.nodes[node]
	if n == nil {
		return
	}
	n.complete = complete
	for i, p := range points {
		if i > 0 && p.TimestampMS/threadObservationIntervalMS-points[i-1].TimestampMS/threadObservationIntervalMS > 1 {
			n.gaps = true
		}
		key := p.TimestampMS / b.resolution * b.resolution
		if old, ok := n.buckets[key]; !ok {
			n.buckets[key] = p
			b.count++
		} else if p.TimestampMS > old.TimestampMS {
			n.buckets[key] = p
		}
		if b.count > maxThreadObservationDisplayPoints || len(n.buckets) > maxThreadObservationScopePoints {
			b.coarsen()
		}
	}
}
func (b *ThreadObservationBuilder) coarsen() {
	b.resolution *= 2
	b.count = 0
	for _, n := range b.nodes {
		next := make(map[int64]ThreadObservation, len(n.buckets)/2+1)
		for _, p := range n.buckets {
			key := p.TimestampMS / b.resolution * b.resolution
			if old, ok := next[key]; !ok || p.TimestampMS > old.TimestampMS {
				next[key] = p
			}
		}
		n.buckets = next
		b.count += len(next)
	}
}

func (b *ThreadObservationBuilder) series(node string) *ThreadObservations {
	out := &ThreadObservations{Source: "PERIODIC", IntervalMS: threadObservationIntervalMS, ResolutionMS: b.resolution, Status: "AVAILABLE", Points: []ThreadObservationPoint{}, Warnings: []string{}}
	selected := b.expected
	if node != "" {
		selected = []string{node}
	}
	expected := len(selected)
	sums := map[int64]int64{}
	observed := map[int64]int{}
	for _, id := range selected {
		n := b.nodes[id]
		if n == nil || len(n.buckets) == 0 {
			out.Status = "PARTIAL"
			out.Warnings = append(out.Warnings, "执行节点 "+id+" 缺少独立线程观测")
			continue
		}
		if !n.complete {
			out.Status = "PARTIAL"
			out.Warnings = append(out.Warnings, "执行节点 "+id+" 采集未正常结束，尾部观测可能缺失")
		}
		if n.gaps {
			out.Status = "PARTIAL"
			out.Warnings = append(out.Warnings, "执行节点 "+id+" 的原始1秒观测存在缺口；缺口不补值")
		}
		for stamp, p := range n.buckets {
			sums[stamp] += p.ActiveThreads
			observed[stamp]++
		}
	}
	keys := make([]int64, 0, len(sums))
	for stamp := range sums {
		keys = append(keys, stamp)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	partialCoverage := false
	displayGap := false
	for i, stamp := range keys {
		if i > 0 && stamp-keys[i-1] > b.resolution {
			out.Points = append(out.Points, ThreadObservationPoint{TimestampMS: keys[i-1] + b.resolution, ThreadsExpectedNodes: expected})
			out.Status = "PARTIAL"
			displayGap = true
		}
		value := sums[stamp]
		out.Points = append(out.Points, ThreadObservationPoint{TimestampMS: stamp, ActiveThreads: &value, ThreadsObservedNodes: observed[stamp], ThreadsExpectedNodes: expected})
		if observed[stamp] < expected {
			out.Status = "PARTIAL"
			partialCoverage = true
		}
	}
	if displayGap {
		out.Warnings = append(out.Warnings, "显示时间桶存在未观测区间，缺口不补值")
	}
	if partialCoverage {
		out.Warnings = append(out.Warnings, "部分时间桶未覆盖全部执行节点；并发仅汇总该桶实际观测，不沿用前值")
	}
	if len(keys) == 0 {
		out.Status = "MISSING"
	}
	if b.resolution > threadObservationIntervalMS {
		out.Warnings = append(out.Warnings, "显示已按时间桶降采样，每个节点取桶内最后一次真实观测；原始文件仍为1秒采集")
	}
	return out
}

func (b *ThreadObservationBuilder) Apply(d *Dashboard, redactors ...func(string) string) {
	// Include null gap separators in both limits. Empty intervals are represented
	// by one separator, never expanded into an unbounded grid or synthetic zeros.
	var series map[string]*ThreadObservations
	for {
		series = map[string]*ThreadObservations{"": b.series("")}
		total := len(series[""].Points)
		bounded := total <= maxThreadObservationScopePoints
		for _, node := range b.expected {
			s := b.series(node)
			series[node] = s
			total += len(s.Points)
			bounded = bounded && len(s.Points) <= maxThreadObservationScopePoints
		}
		if bounded && total <= maxThreadObservationDisplayPoints {
			break
		}
		b.coarsen()
	}
	redact := func(value string) string { return value }
	if len(redactors) > 0 && redactors[0] != nil {
		redact = redactors[0]
	}
	labels := map[string]string{}
	for _, node := range b.expected {
		labels[node] = PublicNodeID(node, redact)
	}
	for _, value := range series {
		for i, warning := range value.Warnings {
			value.Warnings[i] = redact(warning)
		}
	}
	published := map[string]*ThreadObservations{"": series[""]}
	for _, node := range b.expected {
		published[labels[node]] = series[node]
	}
	seen := map[string]bool{}
	for i := range d.Scopes {
		s := &d.Scopes[i]
		if s.StepKey != "" {
			continue
		}
		if value, ok := published[s.NodeID]; ok {
			s.ThreadObservations = value
			seen[s.NodeID] = true
		}
	}
	if !seen[""] {
		d.Scopes = append([]DashboardScope{{ThreadObservations: published[""], Points: []DashboardPoint{}}}, d.Scopes...)
	}
	for _, node := range b.expected {
		label := labels[node]
		if !seen[label] {
			d.Scopes = append(d.Scopes, DashboardScope{NodeID: label, Label: label, ThreadObservations: published[label], Points: []DashboardPoint{}})
			seen[label] = true
		}
	}
}
