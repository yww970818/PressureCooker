package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MaxThreadObservationBytes   int64 = 4 << 20
	MaxThreadObservationPoints        = 90000
	ThreadObservationIntervalMS       = 1000
)

var objectSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ThreadObservation struct {
	TimestampMS   int64 `json:"timestamp_ms"`
	ActiveThreads int64 `json:"active_threads"`
}

func ThreadObservationsKey(e NodeEvidence, sha string) string {
	t, err := time.Parse(time.RFC3339Nano, e.PreparedAt)
	if err != nil || !ValidID(e.TaskID) || !ValidID(e.AttemptID) || !ValidID(e.NodeID) || e.Generation < 1 || !objectSHA.MatchString(sha) {
		return ""
	}
	return fmt.Sprintf("%s/%s/raw/threads/%s/%s/%d/%s.csv", t.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02"), e.TaskID, e.AttemptID, e.NodeID, e.Generation, sha)
}

func ValidThreadObservations(e NodeEvidence) bool {
	if e.ThreadObservations == nil {
		return e.ThreadObservationsStatus == "" || e.ThreadObservationsStatus == "MISSING" || e.ThreadObservationsStatus == "PENDING" || e.ThreadObservationsStatus == "INVALID"
	}
	r := e.ThreadObservations
	return (e.ThreadObservationsStatus == "ARCHIVED" || e.ThreadObservationsStatus == "INCOMPLETE") && r.Size > 0 && r.Size <= MaxThreadObservationBytes && r.Bucket != "" && r.Key != "" && r.Key == ThreadObservationsKey(e, r.SHA256) && r.Name == "thread-observations.csv" && r.URL == "s3://"+r.Bucket+"/"+r.Key
}

// ReadThreadObservations validates the complete bounded source before emitting
// any points. An invalid tail can never leave a partially accepted series.
// EOF without END is a valid interrupted capture; it does not imply zero threads.
func ReadThreadObservations(r io.Reader, e NodeEvidence, emit func(ThreadObservation) error) (bool, error) {
	if !ValidID(e.TaskID) || !ValidID(e.AttemptID) || !ValidID(e.NodeID) || e.Generation < 1 {
		return false, errors.New("invalid thread observation owner")
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxThreadObservationBytes+1))
	if err != nil {
		return false, err
	}
	if int64(len(raw)) > MaxThreadObservationBytes {
		return false, errors.New("thread observation byte limit exceeded")
	}
	// Bound row allocation before splitting: a newline-only 4 MiB file must
	// not allocate millions of slice headers before it can be rejected.
	if bytes.Count(raw, []byte{'\n'}) > MaxThreadObservationPoints+3 {
		return false, errors.New("thread observation record limit exceeded")
	}
	lines := bytes.Split(raw, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	line := func(i int) string { return strings.TrimSuffix(string(lines[i]), "\r") }
	header := fmt.Sprintf("pressurecooker_thread_observations_v1,%s,%s,%s,%d,%d", e.TaskID, e.AttemptID, e.NodeID, e.Generation, ThreadObservationIntervalMS)
	if len(lines) < 2 || line(0) != header || line(1) != "timestamp_ms,active_threads" {
		return false, errors.New("thread observation metadata mismatch")
	}
	points := make([]ThreadObservation, 0, min(len(lines)-2, MaxThreadObservationPoints))
	complete := false
	last := int64(-1)
	for i := 2; i < len(lines); i++ {
		left, right, found := strings.Cut(line(i), ",")
		if complete || !found || strings.Contains(right, ",") {
			return false, errors.New("invalid thread observation record")
		}
		if left == "END" {
			stamp, err := threadObservationInteger(right)
			if err != nil || stamp < last || stamp > 8640000000000000 {
				return false, errors.New("invalid thread observation end")
			}
			complete = true
			continue
		}
		stamp, err := threadObservationInteger(left)
		active, countErr := threadObservationInteger(right)
		if err != nil || countErr != nil || stamp <= last || stamp > 8640000000000000 || active > 1_000_000_000 {
			return false, errors.New("invalid thread observation value")
		}
		if len(points) >= MaxThreadObservationPoints {
			return false, errors.New("thread observation point limit exceeded")
		}
		points = append(points, ThreadObservation{stamp, active})
		last = stamp
	}
	if emit != nil {
		for _, point := range points {
			if err = emit(point); err != nil {
				return complete, err
			}
		}
	}
	return complete, nil
}

func threadObservationInteger(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("missing integer")
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid integer")
		}
	}
	return strconv.ParseInt(s, 10, 64)
}
