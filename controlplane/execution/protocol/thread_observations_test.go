package protocol

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func threadEvidence() NodeEvidence {
	return NodeEvidence{TaskID: "task", AttemptID: "attempt", NodeID: "worker", Generation: 7, PreparedAt: "2026-11-08T00:00:00Z"}
}

func TestThreadObservationsDelimiterAllocationBound(t *testing.T) {
	// An invalid single row must not allocate a slice entry for every comma.
	input := "pressurecooker_thread_observations_v1,task,attempt,worker,7,1000\ntimestamp_ms,active_threads\n" + strings.Repeat(",", 1<<20)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := ReadThreadObservations(strings.NewReader(input), threadEvidence(), nil)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("accepted malformed delimiter row")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 12<<20 {
		t.Fatalf("invalid 1 MiB row allocated %d bytes", allocated)
	}
}

func TestThreadObservationsStrictCSV(t *testing.T) {
	e := threadEvidence()
	header := "pressurecooker_thread_observations_v1,task,attempt,worker,7,1000\ntimestamp_ms,active_threads\n"
	for _, tc := range []struct {
		name, body      string
		complete, valid bool
		points          int
	}{
		{"complete", header + "1000,0\n2000,3\nEND,2000\n", true, true, 2},
		{"interrupted", header + "1000,2\n2000,2\n", false, true, 2},
		{"zero-is-real", header + "1000,0\n", false, true, 1},
		{"bad-tail-atomic", header + "1000,2\nbroken\n", false, false, 0},
		{"duplicate-time", header + "1000,2\n1000,2\n", false, false, 0},
		{"backwards", header + "2000,2\n1000,2\n", false, false, 0},
		{"negative", header + "1000,-1\n", false, false, 0},
		{"count-bound", header + "1000,1000000001\n", false, false, 0},
		{"timestamp-bound", header + "8640000000000001,1\n", false, false, 0},
		{"line-bound", header + strings.Repeat("\n", MaxThreadObservationPoints+1), false, false, 0},
		{"after-end", header + "1000,2\nEND,1000\n2000,2\n", false, false, 0},
		{"end-before-last", header + "1000,2\nEND,999\n", false, false, 0},
		{"stale-attempt", strings.Replace(header, ",attempt,", ",stale,", 1) + "1000,2\n", false, false, 0},
		{"stale-generation", strings.Replace(header, ",7,", ",6,", 1) + "1000,2\n", false, false, 0},
		{"wrong-cadence", strings.Replace(header, ",1000\n", ",2000\n", 1) + "1000,2\n", false, false, 0},
		{"bytes-bound", header + strings.Repeat("1", int(MaxThreadObservationBytes)), false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var points []ThreadObservation
			complete, err := ReadThreadObservations(strings.NewReader(tc.body), e, func(p ThreadObservation) error { points = append(points, p); return nil })
			if (err == nil) != tc.valid || complete != tc.complete || len(points) != tc.points {
				t.Fatalf("complete=%v points=%d error=%v", complete, len(points), err)
			}
		})
	}
	var many strings.Builder
	many.WriteString(header)
	for i := 0; i <= MaxThreadObservationPoints; i++ {
		fmt.Fprintf(&many, "%d,1\n", i+1)
	}
	if _, err := ReadThreadObservations(strings.NewReader(many.String()), e, nil); err == nil {
		t.Fatal("accepted over 90000 points")
	}
}

func TestThreadObservationsReferenceOwnerAndBounds(t *testing.T) {
	e := threadEvidence()
	sha := strings.Repeat("a", 64)
	key := ThreadObservationsKey(e, sha)
	e.ThreadObservationsStatus = "ARCHIVED"
	e.ThreadObservations = &ObjectRef{Bucket: "reports", Key: key, SHA256: sha, Size: 100, Name: "thread-observations.csv", URL: "s3://reports/" + key}
	if !ValidThreadObservations(e) {
		t.Fatal("valid archive rejected")
	}
	for _, change := range []func(*NodeEvidence){func(e *NodeEvidence) { e.TaskID = "foreign" }, func(e *NodeEvidence) { e.AttemptID = "foreign" }, func(e *NodeEvidence) { e.NodeID = "foreign" }, func(e *NodeEvidence) { e.Generation++ }, func(e *NodeEvidence) { e.PreparedAt = "2026-11-09T00:00:00Z" }, func(e *NodeEvidence) { e.ThreadObservationsStatus = "PENDING" }} {
		copy := e
		change(&copy)
		if ValidThreadObservations(copy) {
			t.Fatal("invalid scoped archive accepted")
		}
	}
	e.ThreadObservations.Size = MaxThreadObservationBytes + 1
	if ValidThreadObservations(e) {
		t.Fatal("oversized archive accepted")
	}
	e.ThreadObservations = nil
	for _, status := range []string{"", "MISSING", "PENDING", "INVALID"} {
		e.ThreadObservationsStatus = status
		if !ValidThreadObservations(e) {
			t.Fatal("valid absent status rejected", status)
		}
	}
}
