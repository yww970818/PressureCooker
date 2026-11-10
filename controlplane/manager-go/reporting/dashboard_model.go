package reporting

import (
	"fmt"
	"math"
)

type DashboardOptions struct {
	ApdexSatisfiedMS float64 `json:"apdex_satisfied_ms"`
	ApdexToleratedMS float64 `json:"apdex_tolerated_ms"`
	GranularityMS    int64   `json:"granularity_ms"`
}

func DefaultDashboardOptions() DashboardOptions {
	return DashboardOptions{ApdexSatisfiedMS: 500, ApdexToleratedMS: 1500, GranularityMS: 1000}
}

// Zero fields mean omitted to Go callers; HTTP callers reject explicit zero.
func NormalizeDashboardOptions(o DashboardOptions) (DashboardOptions, error) {
	d := DefaultDashboardOptions()
	if o.ApdexSatisfiedMS == 0 {
		o.ApdexSatisfiedMS = d.ApdexSatisfiedMS
	}
	if o.ApdexToleratedMS == 0 {
		o.ApdexToleratedMS = d.ApdexToleratedMS
	}
	if o.GranularityMS == 0 {
		o.GranularityMS = d.GranularityMS
	}
	for _, v := range []float64{o.ApdexSatisfiedMS, o.ApdexToleratedMS} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 1e9 {
			return o, fmt.Errorf("invalid APDEX thresholds")
		}
	}
	if o.ApdexSatisfiedMS > o.ApdexToleratedMS || o.GranularityMS < 1000 || o.GranularityMS > 3600000 {
		return o, fmt.Errorf("invalid dashboard analysis options")
	}
	return o, nil
}

type DashboardCapabilities struct {
	ActiveThreads string `json:"active_threads"`
	Latency       string `json:"latency"`
	Connect       string `json:"connect"`
	ResponseCodes string `json:"response_codes"`
	Bytes         string `json:"bytes"`
	Transactions  string `json:"transactions"`
}
type Dashboard struct {
	Version      int                   `json:"version"`
	Options      DashboardOptions      `json:"options"`
	Capabilities DashboardCapabilities `json:"capabilities"`
	Warnings     []string              `json:"warnings"`
	Scopes       []DashboardScope      `json:"scopes"`
}
type TimingStats struct {
	SampleCount int64    `json:"sample_count"`
	MeanMS      *float64 `json:"mean_ms"`
	MinMS       *float64 `json:"min_ms"`
	MaxMS       *float64 `json:"max_ms"`
	P50MS       *float64 `json:"p50_ms"`
	P90MS       *float64 `json:"p90_ms"`
	P95MS       *float64 `json:"p95_ms"`
	P99MS       *float64 `json:"p99_ms"`
}
type Apdex struct {
	Score       *float64 `json:"score"`
	Satisfied   int64    `json:"satisfied"`
	Tolerated   int64    `json:"tolerated"`
	Frustrated  int64    `json:"frustrated"`
	Count       int64    `json:"count"`
	SatisfiedMS float64  `json:"satisfied_ms"`
	ToleratedMS float64  `json:"tolerated_ms"`
}
type ResponseCode struct {
	Code    string   `json:"code"`
	Count   int64    `json:"count"`
	Percent float64  `json:"percent"`
	RPS     *float64 `json:"rps"`
}
type DashboardError struct {
	Category      string  `json:"category"`
	Code          string  `json:"code"`
	Message       string  `json:"message"`
	Count         int64   `json:"count"`
	PercentTotal  float64 `json:"percent_total"`
	PercentErrors float64 `json:"percent_errors"`
}
type DistributionBin struct {
	LowerMS float64 `json:"lower_ms"`
	UpperMS float64 `json:"upper_ms"`
	Count   int64   `json:"count"`
}
type Percentile struct {
	Percentile float64  `json:"percentile"`
	ValueMS    *float64 `json:"value_ms"`
}
type DashboardScope struct {
	ThreadObservations *ThreadObservations `json:"thread_observations,omitempty"`
	NodeID             string              `json:"node_id"`
	StepKey            string              `json:"step_key"`
	Label              string              `json:"label"`
	SampleKind         string              `json:"sample_kind"`
	Metrics            Metrics             `json:"metrics"`
	Latency            TimingStats         `json:"latency"`
	Connect            TimingStats         `json:"connect"`
	Apdex              Apdex               `json:"apdex"`
	Points             []DashboardPoint    `json:"points"`
	Distribution       []DistributionBin   `json:"distribution"`
	Percentiles        []Percentile        `json:"percentiles"`
	ResponseCodes      []ResponseCode      `json:"response_codes"`
	Errors             []DashboardError    `json:"errors"`
	TopErrors          []DashboardError    `json:"top_errors"`
}
type DashboardPoint struct {
	TimestampMS            int64          `json:"timestamp_ms"`
	WidthMS                int64          `json:"width_ms"`
	SampleCount            int64          `json:"sample_count"`
	SuccessCount           int64          `json:"success_count"`
	FailureCount           int64          `json:"failure_count"`
	TransactionCount       int64          `json:"transaction_count"`
	RPS                    *float64       `json:"rps"`
	SuccessRPS             *float64       `json:"success_rps"`
	FailureRPS             *float64       `json:"failure_rps"`
	TPS                    *float64       `json:"tps"`
	MeanMS                 *float64       `json:"mean_ms"`
	P50MS                  *float64       `json:"p50_ms"`
	P90MS                  *float64       `json:"p90_ms"`
	P95MS                  *float64       `json:"p95_ms"`
	P99MS                  *float64       `json:"p99_ms"`
	SuccessfulP90MS        *float64       `json:"successful_p90_ms"`
	SuccessfulP95MS        *float64       `json:"successful_p95_ms"`
	SuccessfulP99MS        *float64       `json:"successful_p99_ms"`
	LatencyMeanMS          *float64       `json:"latency_mean_ms"`
	LatencyP95MS           *float64       `json:"latency_p95_ms"`
	ConnectMeanMS          *float64       `json:"connect_mean_ms"`
	ConnectP95MS           *float64       `json:"connect_p95_ms"`
	ActiveThreads          *int64         `json:"active_threads"`
	ThreadsObservedNodes   int            `json:"threads_observed_nodes"`
	ThreadsExpectedNodes   int            `json:"threads_expected_nodes"`
	BytesReceivedPerSecond *float64       `json:"bytes_received_per_second"`
	BytesSentPerSecond     *float64       `json:"bytes_sent_per_second"`
	ResponseCodes          []ResponseCode `json:"response_codes"`
}
