package reporting

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAdaptiveDashboardPreservesMeasurementsAcrossCompaction(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("timeStamp,elapsed,label,success,engine_id,sample_kind,responseCode,Latency,Connect,allThreads\n")
	for i := 0; i < 24000; i++ {
		fmt.Fprintf(&raw, "%d,%d,step-%d,%t,worker-%d,ordinary,200,3,1,10\n", 1794000000000+int64(i)*20, 10+i%30, i%20, i%10 != 0, i%6)
	}
	opts := ParseOptions{AdaptiveDashboard: true}
	a, e := ParseJTL(context.Background(), strings.NewReader(raw.String()), opts)
	if e != nil {
		t.Fatal(e)
	}
	coarse, e := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{AnalyticsOptions: DashboardOptions{GranularityMS: a.Dashboard.Options.GranularityMS}})
	if e != nil {
		t.Fatal(e)
	}
	if a.Dashboard.Options.GranularityMS <= 1000 || len(a.Dashboard.Warnings) == 0 {
		t.Fatal("missing explicit compaction/granularity evidence")
	}
	if !reflect.DeepEqual(a.Timeline, coarse.Timeline) {
		t.Fatal("overview and dashboard use different buckets")
	}
	if a.Metrics.SampleCount != 24000 || a.Metrics.FailureCount != 2400 {
		t.Fatal("sample counts changed")
	}
	if *a.Metrics.P95MS != *coarse.Metrics.P95MS {
		t.Fatal("compaction changed overall percentile")
	}
	points := 0
	for i, s := range a.Dashboard.Scopes {
		points += len(s.Points)
		if len(s.Points) != len(coarse.Dashboard.Scopes[i].Points) {
			t.Fatal("bucket alignment changed")
		}
		for j, p := range s.Points {
			other := coarse.Dashboard.Scopes[i].Points[j]
			if p.SampleCount != other.SampleCount || p.FailureCount != other.FailureCount || *p.P95MS != *other.P95MS {
				t.Fatal("merged bucket differs from direct aggregation")
			}
		}
	}
	if points > dashboardMaxPoints {
		t.Fatal("unbounded dashboard")
	}
}

func TestAdaptiveTimelineCoarsensWithoutDroppingSparseOrOutOfOrderSamples(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("timeStamp,elapsed,label,success,engine_id,sample_kind\n")
	for i := 4999; i >= 0; i-- {
		fmt.Fprintf(&raw, "%d,%d,a,%t,w,ordinary\n", 1794000000123+int64(i)*1000, 1+i%30, i%7 != 0)
	}
	for _, limit := range []int{3600, 8} {
		a, err := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{AdaptiveDashboard: true, MaxBuckets: limit})
		if err != nil {
			t.Fatal(err)
		}
		width := a.Dashboard.Options.GranularityMS
		if width <= 1000 || len(a.Timeline) > limit || a.Metrics.SampleCount != 5000 {
			t.Fatal("overview did not adapt")
		}
		coarse, err := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{MaxBuckets: limit, AnalyticsOptions: DashboardOptions{GranularityMS: width}})
		if err != nil {
			t.Fatal(err)
		}
		coarse.Dashboard.Warnings = a.Dashboard.Warnings
		if !reflect.DeepEqual(a.Timeline, coarse.Timeline) || !reflect.DeepEqual(a.Metrics, coarse.Metrics) || !reflect.DeepEqual(a.Dashboard, coarse.Dashboard) {
			t.Fatal("adaptive statistics differ from direct aggregation")
		}
	}
}
func TestAdaptiveGranularityStopsAtExactMergeBoundary(t *testing.T) {
	b := &dashboardBuilder{options: DashboardOptions{GranularityMS: 2000000}}
	if b.compactPoints() || b.options.GranularityMS != 2000000 {
		t.Fatal("merged buckets into an incompatible time grid")
	}
	var raw strings.Builder
	raw.WriteString("timeStamp,elapsed,label,success,engine_id,sample_kind\n")
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&raw, "%d,1,a,true,w,ordinary\n", i*3600000)
	}
	_, err := ParseJTL(context.Background(), strings.NewReader(raw.String()), ParseOptions{AdaptiveDashboard: true, MaxBuckets: 1, AnalyticsOptions: DashboardOptions{GranularityMS: 3600000}})
	if err == nil || !strings.Contains(err.Error(), "timeline bucket limit") {
		t.Fatal("hard limit lost or loop failed to terminate", err)
	}
}
