package reporting

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type RequestAssertion struct {
	Name    string `json:"name"`
	Failure bool   `json:"failure"`
	Error   bool   `json:"error"`
	Message string `json:"message"`
}

type RequestSample struct {
	ID                 string             `json:"id"`
	NodeID             string             `json:"node_id"`
	StepKey            string             `json:"step_key"`
	Label              string             `json:"label"`
	SampleKind         string             `json:"sample_kind"`
	TimestampMS        int64              `json:"timestamp_ms"`
	ElapsedMS          int64              `json:"elapsed_ms"`
	Success            bool               `json:"success"`
	Method             *string            `json:"method"`
	URL                *string            `json:"url"`
	RequestParams      *string            `json:"request_params"`
	ResponseCode       *string            `json:"response_code"`
	ResponseBody       *string            `json:"response_body"`
	AssertionsCaptured bool               `json:"assertions_captured"`
	Assertions         []RequestAssertion `json:"assertions"`
	Truncated          bool               `json:"truncated"`
	SourceKind         string             `json:"source_kind"`
	SourceName         string             `json:"source_name"`
}

const requestSamplesPerNode = 64
const requestSamplesNodeBytes = 128 << 10
const requestSamplesTotalBytes = 2 << 20

// NodeID and an existing StepKey are server-bound identities. Derive a missing
// key from the original label before masking it, never from a masked label.
func SanitizeRequestSample(sample RequestSample, redact func(string) string) RequestSample {
	if redact == nil {
		redact = NewRedactor(nil)
	}
	provided := redact
	baseline := NewRedactor(nil)
	redact = func(value string) string {
		value = requestURLUserinfo.ReplaceAllString(value, "${1}[REDACTED]@")
		return baseline(provided(value))
	}
	switch strings.ToLower(strings.TrimSpace(sample.SampleKind)) {
	case "transaction", "transaction_sample":
		sample.SampleKind = "transaction"
	case "ordinary", "ordinary_samples", "sample", "http":
		sample.SampleKind = "ordinary"
	default:
		sample.SampleKind = "unknown"
	}
	if sample.StepKey == "" {
		sample.StepKey = StableStepKey(sample.SampleKind, sample.Label)
	}
	if sample.ID == "" {
		sample.ID = requestSampleID(sample)
	}
	bounded := func(value string, limit int) string {
		out, cut := requestBoundText(redact(value), limit)
		sample.Truncated = sample.Truncated || cut
		return out
	}
	sample.Label = bounded(sample.Label, 2048)
	sample.SourceKind = bounded(sample.SourceKind, 64)
	sample.SourceName = bounded(sample.SourceName, 128)
	// Keep a safe representation of query parameters when the displayed URL is
	// reduced to its address. A body, when present, remains explicitly identified.
	if sample.URL != nil {
		if parsed, e := url.Parse(*sample.URL); e == nil {
			// URL.User is scheme-independent; ws/wss and other valid URL
			// authorities carry credentials just as HTTP URLs do.
			if parsed.User != nil {
				parsed.User = nil
				safeURL := parsed.String()
				sample.URL = &safeURL
			}
			if parsed.RawQuery != "" {
				query, cut := requestPayload(parsed.RawQuery, redact, 0)
				sample.Truncated = sample.Truncated || cut
				if sample.RequestParams == nil {
					sample.RequestParams = &query
				} else if query != "" {
					combined := "Query parameters:\n" + query + "\n\nSampler data:\n" + *sample.RequestParams
					sample.RequestParams = &combined
				}
			}
		}
	}
	optional := func(value *string, limit int, payload bool) *string {
		if value == nil {
			return nil
		}
		out := *value
		if payload {
			var cut bool
			out, cut = requestPayload(out, redact, 0)
			sample.Truncated = sample.Truncated || cut
		} else {
			out = redact(out)
		}
		out, cut := requestBoundText(out, limit)
		sample.Truncated = sample.Truncated || cut
		return &out
	}
	sample.Method = optional(sample.Method, 32, false)
	sample.URL = optional(sample.URL, 4096, false)
	sample.RequestParams = optional(sample.RequestParams, 4096, true)
	sample.ResponseCode = optional(sample.ResponseCode, 256, false)
	sample.ResponseBody = optional(sample.ResponseBody, 8192, true)
	assertions := []RequestAssertion{}
	if sample.AssertionsCaptured {
		if len(sample.Assertions) > 16 {
			sample.Truncated = true
		}
		for _, a := range sample.Assertions[:min(16, len(sample.Assertions))] {
			a.Name = bounded(a.Name, 128)
			message, cut := requestPayload(a.Message, redact, 0)
			sample.Truncated = sample.Truncated || cut
			a.Message, cut = requestBoundText(message, 512)
			sample.Truncated = sample.Truncated || cut
			assertions = append(assertions, a)
		}
	} else if len(sample.Assertions) > 0 {
		sample.Truncated = true
	}
	sample.Assertions = assertions
	return sample
}
func requestSampleID(sample RequestSample) string {
	data, _ := json.Marshal([]any{sample.NodeID, sample.StepKey, sample.TimestampMS, sample.ElapsedMS, sample.Success})
	return fmt.Sprintf("request-%x", sha256.Sum256(data))
}

type requestSampleSlot struct {
	node, step string
	success    bool
}
type requestSampleCollector struct {
	samples               []RequestSample
	seen                  map[requestSampleSlot]bool
	indices               map[requestSampleSlot]int
	sizes                 []int
	nodeCounts, nodeBytes map[string]int
	totalBytes            int
	truncated             bool
}

func newRequestSampleCollector() *requestSampleCollector {
	return &requestSampleCollector{samples: []RequestSample{}, seen: map[requestSampleSlot]bool{}, indices: map[requestSampleSlot]int{}, nodeCounts: map[string]int{}, nodeBytes: map[string]int{}, totalBytes: 2}
}
func sampleSlot(s RequestSample) requestSampleSlot {
	return requestSampleSlot{s.NodeID, s.StepKey, s.Success}
}
func (c *requestSampleCollector) wants(slot requestSampleSlot) bool {
	if c.seen[slot] {
		return false
	}
	if c.nodeCounts[slot.node] >= requestSamplesPerNode {
		c.seen[slot] = true
		c.truncated = true
		return false
	}
	return true
}
func sameRequestSample(a, b RequestSample) bool {
	return a.NodeID == b.NodeID && a.StepKey == b.StepKey && a.TimestampMS == b.TimestampMS && a.ElapsedMS == b.ElapsedMS && a.Success == b.Success
}
func requestSampleRichness(s RequestSample) int {
	n := 0
	for _, v := range []*string{s.Method, s.URL, s.RequestParams, s.ResponseCode, s.ResponseBody} {
		if v != nil {
			n++
		}
	}
	if s.AssertionsCaptured {
		n++
	}
	return n
}
func mergeRequestSample(old, next RequestSample) RequestSample {
	preferNext := next.SourceKind == "worker_sample" && old.SourceKind != "worker_sample" || next.SourceKind == old.SourceKind && requestSampleRichness(next) > requestSampleRichness(old)
	out := old
	choose := func(a, b *string) *string {
		if b != nil && (preferNext || a == nil) {
			return b
		}
		return a
	}
	out.Method = choose(old.Method, next.Method)
	out.URL = choose(old.URL, next.URL)
	out.RequestParams = choose(old.RequestParams, next.RequestParams)
	out.ResponseCode = choose(old.ResponseCode, next.ResponseCode)
	out.ResponseBody = choose(old.ResponseBody, next.ResponseBody)
	if next.AssertionsCaptured && (preferNext || !old.AssertionsCaptured) {
		out.AssertionsCaptured = true
		out.Assertions = append([]RequestAssertion{}, next.Assertions...)
	}
	if preferNext {
		out.SourceKind = next.SourceKind
		out.SourceName = next.SourceName
	}
	// A failed assertion is positive evidence even if a richer receipt omitted it.
	if out.AssertionsCaptured {
		for _, a := range old.Assertions {
			if a.Failure || a.Error {
				found := false
				for _, b := range out.Assertions {
					genericCSVName := a.Name == "failureMessage" || a.Name == "assertionFailureMessage"
					if a == b || genericCSVName && a.Message == b.Message && (b.Failure || b.Error) {
						found = true
						break
					}
				}
				if !found {
					out.Assertions = append(out.Assertions, a)
					out.Truncated = true
				}
			}
		}
	}
	out.Truncated = out.Truncated || next.Truncated
	return out
}
func (c *requestSampleCollector) add(sample RequestSample, replace bool) {
	slot := sampleSlot(sample)
	c.truncated = c.truncated || sample.Truncated
	if at, ok := c.indices[slot]; ok {
		if !replace {
			return
		}
		var merged RequestSample
		if sameRequestSample(c.samples[at], sample) {
			merged = mergeRequestSample(c.samples[at], sample)
		} else if sample.SourceKind == "worker_sample" && c.samples[at].SourceKind != "worker_sample" {
			// Distributed CSV delivery and listener sampling can choose different
			// first observations. Prefer the actual worker observation as a whole;
			// fields from another timestamp must never be mixed into it.
			merged = sample
		} else {
			return
		}
		// Merging assertion proof can add records; the same field limits still apply.
		if len(merged.Assertions) > 16 {
			merged.Assertions = merged.Assertions[:16]
			merged.Truncated = true
			c.truncated = true
		}
		encoded, _ := json.Marshal(merged)
		delta := len(encoded) - c.sizes[at]
		if c.nodeBytes[slot.node]+delta > requestSamplesNodeBytes || c.totalBytes+delta > requestSamplesTotalBytes {
			c.truncated = true
			c.samples[at].Truncated = true
			return
		}
		c.samples[at] = merged
		c.sizes[at] = len(encoded)
		c.nodeBytes[slot.node] += delta
		c.totalBytes += delta
		return
	}
	if c.seen[slot] {
		return
	}
	c.seen[slot] = true
	encoded, _ := json.Marshal(sample)
	size := len(encoded)
	nodeBytes := c.nodeBytes[slot.node]
	if c.nodeCounts[slot.node] == 0 {
		nodeBytes = 2
	} else {
		nodeBytes++
	}
	totalBytes := c.totalBytes
	if len(c.samples) > 0 {
		totalBytes++
	}
	if c.nodeCounts[slot.node] >= requestSamplesPerNode || nodeBytes+size > requestSamplesNodeBytes || totalBytes+size > requestSamplesTotalBytes {
		c.truncated = true
		return
	}
	c.indices[slot] = len(c.samples)
	c.samples = append(c.samples, sample)
	c.sizes = append(c.sizes, size)
	c.nodeCounts[slot.node]++
	c.nodeBytes[slot.node] = nodeBytes + size
	c.totalBytes = totalBytes + size
}
func sortRequestSamples(samples []RequestSample) {
	sort.SliceStable(samples, func(i, j int) bool {
		a, b := samples[i], samples[j]
		if a.TimestampMS != b.TimestampMS {
			return a.TimestampMS < b.TimestampMS
		}
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		if a.StepKey != b.StepKey {
			return a.StepKey < b.StepKey
		}
		if a.Success != b.Success {
			return !a.Success
		}
		return a.ID < b.ID
	})
}

// Only pairs observed in the saved JTL can acquire payload evidence. Node and
// step catalogs alone are insufficient: that would invent a node-step relation.
func MergeRequestSamples(a *Analysis, incoming []RequestSample, redact func(string) string) {
	if a == nil {
		return
	}
	pairs := map[nodeStepIdentity]bool{}
	nodes := map[string]bool{}
	for _, n := range a.Nodes {
		nodes[n.NodeID] = true
	}
	for _, s := range a.NodeSteps {
		if nodes[s.NodeID] {
			pairs[nodeStepIdentity{s.NodeID, s.StepKey}] = true
		}
	}
	c := newRequestSampleCollector()
	c.truncated = a.RequestSamplesTruncated
	for _, list := range [][]RequestSample{a.RequestSamples, incoming} {
		for _, sample := range list {
			sample = SanitizeRequestSample(sample, redact)
			if !pairs[nodeStepIdentity{sample.NodeID, sample.StepKey}] || sample.TimestampMS < 0 || sample.ElapsedMS < 0 {
				c.truncated = true
				continue
			}
			c.add(sample, true)
		}
	}
	sortRequestSamples(c.samples)
	a.RequestSamples = c.samples
	a.RequestSamplesTruncated = c.truncated
}
