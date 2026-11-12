package reporting

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

const UnassignedNodeID = "__unassigned__"

// A key identifies a sampling label and normalized kind, not a workflow component.
// Hash before redaction so two different sensitive labels cannot be merged.
func StableStepKey(kind, rawLabel string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(kind+"\x00"+rawLabel)))
}

func stableStepKey(kind, rawLabel string) string { return StableStepKey(kind, rawLabel) }

// PublicNodeID separates a masked display label from its physical identity.
// Use the same opaque identifier for metrics, logs, samples and thread evidence.
func PublicNodeID(raw string, redact func(string) string) string {
	if raw == "" || raw == UnassignedNodeID || redact == nil || redact(raw) == raw {
		return raw
	}
	return fmt.Sprintf("node-%x", sha256.Sum256([]byte(raw)))
}

type nodeAggregate struct {
	ordinary     agg
	transactions int64
	observed     bool
	first, last  int64
	stepCount    int
}

type nodeStepIdentity struct{ node, step string }

type nodeDetails struct {
	nodes        map[string]*nodeAggregate
	steps        map[nodeStepIdentity]*agg
	expected     map[string]bool
	maxNodes     int
	maxNodeSteps int
}

func newNodeDetails(expected []string, maxNodes, maxNodeSteps int) (*nodeDetails, error) {
	d := &nodeDetails{nodes: map[string]*nodeAggregate{}, steps: map[nodeStepIdentity]*agg{}, expected: map[string]bool{}, maxNodes: maxNodes, maxNodeSteps: maxNodeSteps}
	for _, id := range expected {
		if strings.TrimSpace(id) == "" || id == UnassignedNodeID {
			return nil, fmt.Errorf("JTL invalid expected node identifier")
		}
		if d.nodes[id] == nil {
			if len(d.nodes) >= maxNodes {
				return nil, fmt.Errorf("JTL node limit exceeded")
			}
			d.nodes[id] = &nodeAggregate{}
		}
		d.expected[id] = true
	}
	return d, nil
}

func (d *nodeDetails) add(node, key, kind string, ts, elapsed int64, ok bool, br, bs int64, scopeUnconfirmed bool) error {
	n := d.nodes[node]
	if n == nil {
		if len(d.nodes) >= d.maxNodes {
			return fmt.Errorf("JTL node limit exceeded")
		}
		n = &nodeAggregate{}
		d.nodes[node] = n
	}
	id := nodeStepIdentity{node, key}
	s := d.steps[id]
	if s == nil {
		if len(d.steps) >= d.maxNodeSteps {
			return fmt.Errorf("JTL node-step limit exceeded")
		}
		s = &agg{}
		d.steps[id] = s
		n.stepCount++
	}
	s.add(ts, elapsed, ok, br, bs)
	s.scopeUnconfirmed = s.scopeUnconfirmed || scopeUnconfirmed
	if !n.observed || ts < n.first {
		n.first = ts
	}
	if !n.observed || ts+elapsed > n.last {
		n.last = ts + elapsed
	}
	n.observed = true
	if kind == "transaction" {
		n.transactions++
	} else {
		n.ordinary.add(ts, elapsed, ok, br, bs)
		n.ordinary.scopeUnconfirmed = n.ordinary.scopeUnconfirmed || scopeUnconfirmed
	}
	return nil
}

func (d *nodeDetails) finish(a *Analysis, labels, kinds map[string]string, red func(string) string) error {
	ids := make([]string, 0, len(d.nodes))
	for id := range d.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	publicIDs := map[string]string{}
	for _, id := range ids {
		public := PublicNodeID(id, red)
		if prior, ok := publicIDs[public]; ok && prior != id {
			return fmt.Errorf("JTL node identity collision after redaction")
		}
		publicIDs[public] = id
		n := d.nodes[id]
		status := "OBSERVED"
		if id == UnassignedNodeID {
			status = "UNASSIGNED"
		} else if !n.observed {
			status = "MISSING"
		} else if len(d.expected) > 0 && !d.expected[id] {
			status = "UNEXPECTED"
		}
		metrics := n.ordinary.metrics()
		metrics.TransactionCount = n.transactions
		node := Node{NodeID: public, Label: red(id), Expected: d.expected[id], Status: status, Metrics: metrics, StepCount: n.stepCount}
		if n.observed {
			first, last := n.first, n.last
			node.FirstAt, node.LastAt = &first, &last
		}
		a.Nodes = append(a.Nodes, node)
	}
	keys := make([]nodeStepIdentity, 0, len(d.steps))
	for key := range d.steps {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].node != keys[j].node {
			return keys[i].node < keys[j].node
		}
		return keys[i].step < keys[j].step
	})
	for i, key := range keys {
		kind, label := kinds[key.step], labels[key.step]
		metrics := d.steps[key].metrics()
		if kind == "transaction" {
			metrics.SampleScope = "transaction_samples"
			metrics.TransactionCount = metrics.SampleCount
		}
		a.NodeSteps = append(a.NodeSteps, Step{ID: fmt.Sprintf("node-step-%d", i+1), NodeID: PublicNodeID(key.node, red), StepKey: stableStepKey(kind, strings.SplitN(key.step, "\x00", 2)[1]), Label: label, SampleKind: kind, Metrics: metrics})
	}
	return nil
}
