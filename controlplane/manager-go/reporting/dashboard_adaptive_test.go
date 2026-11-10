package reporting

import (
	"context"
	"fmt"
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
