package reporting

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// These checks catch accidental request-metric mutation and forward-filled node totals.
func TestIndependentThreadObservationsSeparateScopesAndGaps(t *testing.T) {
	d := Dashboard{Scopes: []DashboardScope{
		{NodeID: "", StepKey: "", Points: []DashboardPoint{{TimestampMS: 9000, SampleCount: 1}}, Metrics: Metrics{SampleCount: 1}},
		{NodeID: "worker-1", StepKey: ""},
		{NodeID: "", StepKey: "step", Points: []DashboardPoint{{TimestampMS: 9000, SampleCount: 1}}},
	}}
	original, _ := json.Marshal(d.Scopes[0])
	b := NewThreadObservationBuilder([]string{"worker-1", "worker-2"})
	b.AddNode("worker-1", []ThreadObservation{{1000, 2}, {2000, 3}, {5000, 0}}, true)
	b.AddNode("worker-2", []ThreadObservation{{1000, 7}, {2000, 8}}, false)
	b.Apply(&d)
	if d.Scopes[2].ThreadObservations != nil {
		t.Fatal("periodic node totals attributed to step")
	}
	requestScope := d.Scopes[0]
	requestScope.ThreadObservations = nil
	after, _ := json.Marshal(requestScope)
	if string(original) != string(after) || d.Scopes[0].Metrics.SampleCount != 1 {
		t.Fatal("request statistics changed")
	}
	got := d.Scopes[0].ThreadObservations
	if got == nil || got.Source != "PERIODIC" || got.IntervalMS != 1000 || got.ResolutionMS != 1000 || got.Status != "PARTIAL" {
		t.Fatalf("unexpected periodic metadata: %+v", got)
	}
	if len(got.Points) != 4 || *got.Points[0].ActiveThreads != 9 || *got.Points[1].ActiveThreads != 11 || got.Points[2].TimestampMS != 3000 || got.Points[2].ActiveThreads != nil || got.Points[2].ThreadsObservedNodes != 0 || *got.Points[3].ActiveThreads != 0 || got.Points[3].ThreadsObservedNodes != 1 || got.Points[3].ThreadsExpectedNodes != 2 {
		t.Fatalf("missing/gap/zero semantics lost: %+v", got.Points)
	}
	if len(got.Warnings) == 0 {
		t.Fatal("partial evidence not disclosed")
	}
	if len(d.Scopes) != 4 || d.Scopes[3].NodeID != "worker-2" || d.Scopes[3].ThreadObservations.Status != "PARTIAL" {
		t.Fatal("node without JTL scope omitted")
	}
}

func TestIndependentThreadObservationsMissingAndSingleActualReading(t *testing.T) {
	d := Dashboard{Scopes: []DashboardScope{{}}}
	b := NewThreadObservationBuilder([]string{"a", "b"})
	b.AddNode("a", []ThreadObservation{{1793926800123, 0}}, true)
	b.Apply(&d)
	if g := d.Scopes[0].ThreadObservations; g.Status != "PARTIAL" || len(g.Points) != 1 || g.Points[0].ActiveThreads == nil || *g.Points[0].ActiveThreads != 0 {
		t.Fatalf("single actual zero missing: %+v", g)
	}
	if g := d.Scopes[2].ThreadObservations; g.Status != "MISSING" || len(g.Points) != 0 {
		t.Fatalf("missing node fabricated readings: %+v", g)
	}
	empty := Dashboard{}
	NewThreadObservationBuilder([]string{"a"}).Apply(&empty)
	if len(empty.Scopes) != 2 || empty.Scopes[0].ThreadObservations.Status != "MISSING" {
		t.Fatalf("missing report scope absent: %+v", empty)
	}
}

func TestIndependentThreadObservationsAdaptiveBoundAndLatestValue(t *testing.T) {
	nodes := []string{}
	for i := 0; i < 64; i++ {
		nodes = append(nodes, fmt.Sprintf("node-%02d", i))
	}
	b := NewThreadObservationBuilder(nodes)
	for _, node := range nodes {
		p := make([]ThreadObservation, 86400)
		for i := range p {
			p[i] = ThreadObservation{TimestampMS: 1793926800000 + int64(i)*1000, ActiveThreads: int64(i)}
		}
		b.AddNode(node, p, true)
	}
	d := Dashboard{}
	b.Apply(&d)
	total := 0
	for _, s := range d.Scopes {
		g := s.ThreadObservations
		total += len(g.Points)
		if len(g.Points) > 3600 || g.ResolutionMS <= 1000 || g.IntervalMS != 1000 {
			t.Fatalf("invalid adaptive scope: %+v", g)
		}
	}
	if total > 12000 {
		t.Fatalf("display contains %d points", total)
	}
	g := d.Scopes[0].ThreadObservations
	last := g.Points[len(g.Points)-1]
	if last.ActiveThreads == nil || *last.ActiveThreads != 64*86399 || last.ThreadsObservedNodes != 64 || g.Status != "AVAILABLE" {
		t.Fatalf("latest observed bucket not summed: %+v / %s", last, g.Status)
	}
	clone := Dashboard{}
	b.Apply(&clone)
	if !reflect.DeepEqual(d, clone) {
		t.Fatal("nondeterministic periodic scopes")
	}
}

func TestIndependentThreadObservationsBucketGapIsExplicit(t *testing.T) {
	b := NewThreadObservationBuilder([]string{"a"})
	b.AddNode("a", []ThreadObservation{{TimestampMS: 999, ActiveThreads: 4}, {TimestampMS: 2000, ActiveThreads: 6}}, true)
	d := Dashboard{}
	b.Apply(&d)
	g := d.Scopes[0].ThreadObservations
	if g.Status != "PARTIAL" || len(g.Points) != 3 || g.Points[1].ActiveThreads != nil || len(g.Warnings) == 0 {
		t.Fatalf("absent display bucket not disclosed: %+v", g)
	}
}

func TestIndependentThreadObservationsRawGapSurvivesDownsample(t *testing.T) {
	for _, first := range []int64{1000, 1001} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			b := NewThreadObservationBuilder([]string{"a"})
			points := []ThreadObservation{{TimestampMS: first, ActiveThreads: 3}}
			for stamp := int64(3000); stamp <= 4001000; stamp += 1000 {
				points = append(points, ThreadObservation{TimestampMS: stamp, ActiveThreads: 3})
			}
			b.AddNode("a", points, true)
			d := Dashboard{}
			b.Apply(&d)
			g := d.Scopes[0].ThreadObservations
			if g.ResolutionMS <= 1000 || g.Status != "PARTIAL" || len(g.Warnings) < 2 {
				t.Fatalf("raw missing interval hidden by adaptive display: status=%s, resolution=%d, warnings=%v", g.Status, g.ResolutionMS, g.Warnings)
			}
		})
	}
}
