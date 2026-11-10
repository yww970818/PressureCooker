package reporting

// Targets uses pointers to distinguish omitted/null targets from explicit zero.
type Targets struct {
	MinRPS             *float64 `json:"min_rps"`
	MinSuccessRate     *float64 `json:"min_success_rate"`
	MaxP95MS           *float64 `json:"max_p95_ms"`
	MaxP99MS           *float64 `json:"max_p99_ms"`
	MinDurationSeconds *float64 `json:"min_duration_seconds"`
}
type Metrics struct {
	SampleScope      string   `json:"sample_scope"`
	SampleCount      int64    `json:"sample_count"`
	SuccessCount     int64    `json:"success_count"`
	FailureCount     int64    `json:"failure_count"`
	SuccessRate      *float64 `json:"success_rate"`
	DurationSeconds  *float64 `json:"duration_seconds"`
	RPS              *float64 `json:"rps"`
	MeanMS           *float64 `json:"mean_ms"`
	MinMS            *float64 `json:"min_ms"`
	MaxMS            *float64 `json:"max_ms"`
	P50MS            *float64 `json:"p50_ms"`
	P90MS            *float64 `json:"p90_ms"`
	P95MS            *float64 `json:"p95_ms"`
	P99MS            *float64 `json:"p99_ms"`
	BytesReceived    int64    `json:"bytes_received"`
	BytesSent        int64    `json:"bytes_sent"`
	TransactionCount int64    `json:"transaction_count"`
	PercentileMethod string   `json:"percentile_method"`
}
type Quality struct {
	MetricStatus   string   `json:"metric_status"`
	LogStatus      string   `json:"log_status"`
	InvalidRows    int64    `json:"invalid_rows"`
	Warnings       []string `json:"warnings"`
	ExpectedNodes  []string `json:"expected_nodes"`
	ObservedNodes  []string `json:"observed_nodes"`
	NodeCoverage   string   `json:"node_coverage"`
	ScopeConfirmed bool     `json:"scope_confirmed"`
	exactP95       *thresholdCount
	exactP99       *thresholdCount
}
type thresholdCount struct {
	target           float64
	atOrBelow, total int64
}
type Step struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id,omitempty"`
	StepKey    string `json:"step_key"`
	Label      string `json:"label"`
	SampleKind string `json:"sample_kind"`
	Metrics
}
type Node struct {
	NodeID    string  `json:"node_id"`
	Label     string  `json:"label"`
	Expected  bool    `json:"expected"`
	Status    string  `json:"status"`
	Metrics   Metrics `json:"metrics"`
	FirstAt   *int64  `json:"first_at"`
	LastAt    *int64  `json:"last_at"`
	StepCount int     `json:"step_count"`
}
type Point struct {
	TimestampMS  int64    `json:"timestamp_ms"`
	WidthMS      int64    `json:"width_ms"`
	SampleCount  int64    `json:"sample_count"`
	FailureCount int64    `json:"failure_count"`
	RPS          *float64 `json:"rps"`
	MeanMS       *float64 `json:"mean_ms"`
	P95MS        *float64 `json:"p95_ms"`
}
type Issue struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id,omitempty"`
	StepKey    string `json:"step_key,omitempty"`
	SourceKind string `json:"source_kind"`
	SourceName string `json:"source_name"`
	Category   string `json:"category"`
	Level      string `json:"level"`
	Label      string `json:"label"`
	Message    string `json:"message"`
	Count      int64  `json:"count"`
	FirstAt    *int64 `json:"first_at"`
	LastAt     *int64 `json:"last_at"`
	LineFrom   int64  `json:"line_from"`
	LineTo     int64  `json:"line_to"`
	StepLabel  string `json:"step_label"`
}
type Finding struct {
	Origin         string   `json:"origin"`
	Level          string   `json:"level"`
	Title          string   `json:"title"`
	Detail         string   `json:"detail"`
	Recommendation string   `json:"recommendation"`
	EvidenceRefs   []string `json:"evidence_refs"`
}
type Check struct {
	Key    string   `json:"key"`
	Label  string   `json:"label"`
	Target float64  `json:"target"`
	Actual *float64 `json:"actual"`
	Unit   string   `json:"unit"`
	Status string   `json:"status"`
	Detail string   `json:"detail"`
}
type Verdict struct {
	Status      string  `json:"status"`
	Label       string  `json:"label"`
	Explanation string  `json:"explanation"`
	Checks      []Check `json:"checks"`
}
type Analysis struct {
	Metrics                 Metrics         `json:"metrics"`
	Quality                 Quality         `json:"quality"`
	Timeline                []Point         `json:"timeline"`
	Steps                   []Step          `json:"steps"`
	Nodes                   []Node          `json:"nodes"`
	NodeSteps               []Step          `json:"node_steps"`
	Issues                  []Issue         `json:"issues"`
	Findings                []Finding       `json:"findings"`
	Verdict                 Verdict         `json:"verdict"`
	Dashboard               Dashboard       `json:"dashboard"`
	RequestSamples          []RequestSample `json:"request_samples"`
	RequestSamplesTruncated bool            `json:"request_samples_truncated"`
}
type ParseOptions struct {
	AdaptiveDashboard bool
	Targets           Targets
	ExpectedNodes     []string
	RunStatus         string
	TimestampIsEnd    bool
	Redactor          func(string) string
	AnalyticsOptions  DashboardOptions
	// ScopeConfirmed explicitly attests this source contains ordinary samples only.
	ScopeConfirmed            bool
	MaxFields, MaxRecordBytes int
	MaxLabels                 int
	MaxNodes                  int
	MaxNodeSteps              int
	MaxBuckets                int
	MaxIssues                 int
	MaxRows                   int64
}

const ParserVersion = "1.5.0"
const SchemaVersion = "1"
