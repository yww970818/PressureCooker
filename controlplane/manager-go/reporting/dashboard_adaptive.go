package reporting

import "math"

func mergeTiming(dst, src *agg) {
	if src.n == 0 {
		return
	}
	if dst.n == 0 {
		dst.min = src.min
		dst.start = src.start
	}
	dst.n += src.n
	dst.ok += src.ok
	dst.br += src.br
	dst.bs += src.bs
	dst.sum += src.sum
	dst.min = math.Min(dst.min, src.min)
	dst.max = math.Max(dst.max, src.max)
	if src.start < dst.start {
		dst.start = src.start
	}
	if src.end > dst.end {
		dst.end = src.end
	}
	dst.scopeUnconfirmed = dst.scopeUnconfirmed || src.scopeUnconfirmed
	if dst.hist == nil {
		dst.hist = map[int]int64{}
	}
	for bin, count := range src.hist {
		dst.hist[bin] += count
	}
}
func bucketCells(p *dashboardBucket) int {
	return len(p.data.rt.hist) + len(p.data.successful.hist) + len(p.data.latency.hist) + len(p.data.connect.hist)
}
func mergeDashboardBucket(dst, src *dashboardBucket) {
	mergeTiming(dst.data.rt, src.data.rt)
	mergeTiming(&dst.data.successful, &src.data.successful)
	mergeTiming(&dst.data.latency, &src.data.latency)
	mergeTiming(&dst.data.connect, &src.data.connect)
	dst.data.transactions += src.data.transactions
	dst.data.knownKinds += src.data.knownKinds
	dst.data.receivedRecords += src.data.receivedRecords
	dst.data.sentRecords += src.data.sentRecords
	if dst.data.codes == nil {
		dst.data.codes = map[string]int64{}
	}
	for code, count := range src.data.codes {
		dst.data.codes[code] += count
	}
	for node, observation := range src.threads {
		prior, ok := dst.threads[node]
		if !ok || observation.timestamp > prior.timestamp || observation.timestamp == prior.timestamp && observation.value > prior.value {
			dst.threads[node] = observation
		}
	}
}

// Only trend buckets are coarsened. Exact counters, APDEX, per-step metrics and
// histogram estimators for the full range are untouched. Actual width is saved.
func (b *dashboardBuilder) compactPoints() {
	nextWidth := b.options.GranularityMS * 2
	if nextWidth > 3600000 {
		return
	}
	if nextWidth == b.options.GranularityMS {
		return
	}
	b.options.GranularityMS = nextWidth
	b.compacted = true
	b.pointCount = 0
	for _, scope := range b.scopes {
		next := map[int64]*dashboardBucket{}
		for timestamp, point := range scope.points {
			key := timestamp / nextWidth * nextWidth
			if existing := next[key]; existing != nil {
				before := bucketCells(existing) + bucketCells(point)
				mergeDashboardBucket(existing, point)
				b.histogramCells -= before - bucketCells(existing)
			} else {
				next[key] = point
			}
		}
		scope.points = next
		b.pointCount += len(next)
	}
}
