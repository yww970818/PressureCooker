package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxNodeReportBytes = 32 << 10
const MaxCoreNodeReportBytes = 64 << 10
const MaxNodeReportWorkers = 64

type NodeMachine struct {
	Hostname             *string  `json:"hostname"`
	OS                   string   `json:"os"`
	Arch                 string   `json:"arch"`
	ResourceScope        string   `json:"resource_scope"`
	LogicalCPUs          *int     `json:"logical_cpus"`
	CPULimitCores        *float64 `json:"cpu_limit_cores"`
	CPUUsagePercent      *float64 `json:"cpu_usage_percent"`
	MemoryUsedBytes      *int64   `json:"memory_used_bytes"`
	MemoryLimitBytes     *int64   `json:"memory_limit_bytes"`
	HostMemoryTotalBytes *int64   `json:"host_memory_total_bytes"`
	DiskTotalBytes       *int64   `json:"disk_total_bytes"`
	DiskAvailableBytes   *int64   `json:"disk_available_bytes"`
}
type NodeJob struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
}
type NodeResetOperation struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	ToGeneration int64  `json:"to_generation"`
}
type NodeReport struct {
	Version         int                 `json:"version"`
	ID              string              `json:"id"`
	Role            string              `json:"role"`
	DisplayName     string              `json:"display_name"`
	BootID          string              `json:"boot_id"`
	BootStartedAtMS int64               `json:"boot_started_at_ms"`
	Sequence        int64               `json:"sequence"`
	ObservedAt      string              `json:"observed_at"`
	QueueConnected  *bool               `json:"queue_connected"`
	Status          string              `json:"status"`
	Healthy         bool                `json:"healthy"`
	Generation      int64               `json:"generation"`
	Job             *NodeJob            `json:"job"`
	ResetOperation  *NodeResetOperation `json:"reset_operation"`
	Machine         NodeMachine         `json:"machine"`
}
type CoreNodeReport struct {
	NodeReport
	Workers          []NodeReport `json:"workers"`
	ObservationEpoch string       `json:"observation_epoch"`
}
type NodeRegistrationResponse struct {
	Accepted         bool     `json:"accepted"`
	Workers          []Worker `json:"workers"`
	ClusterWorkerIDs []string `json:"cluster_worker_ids"`
	ServerTime       string   `json:"server_time"`
	ObservationEpoch string   `json:"observation_epoch"`
}

func NodeObservationEpoch(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const nodeReportSafeInteger int64 = 9007199254740991

var nodeBootID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func nodeText(s string, max int) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}
func nodeState(s string, allowed ...string) bool {
	for _, v := range allowed {
		if s == v {
			return true
		}
	}
	return false
}
func ValidateNodeReport(report NodeReport, now time.Time) error {
	invalid := func() error { return errors.New("invalid node report") }
	if report.Version != 1 || !ValidID(report.ID) || !nodeState(report.Role, "controller", "worker") || !nodeText(report.DisplayName, 800) || utf8.RuneCountInString(report.DisplayName) > 200 || report.Sequence < 1 || report.Sequence > nodeReportSafeInteger || report.Generation < 0 || report.Generation > nodeReportSafeInteger {
		return invalid()
	}
	observed, err := time.Parse(time.RFC3339Nano, report.ObservedAt)
	if err != nil || observed.Before(now.Add(-120*time.Second)) || observed.After(now.Add(120*time.Second)) {
		return invalid()
	}
	if report.BootID != "" && !nodeBootID.MatchString(report.BootID) {
		return invalid()
	}
	if report.BootStartedAtMS < 0 || report.BootStartedAtMS > nodeReportSafeInteger || report.BootStartedAtMS > now.Add(120*time.Second).UnixMilli() || report.BootStartedAtMS > observed.Add(120*time.Second).UnixMilli() {
		return invalid()
	}
	if report.Role == "controller" && (report.BootID == "" || report.BootStartedAtMS == 0 || report.QueueConnected == nil || report.DisplayName == "") {
		return invalid()
	}
	if !nodeState(report.Status, "idle", "running", "recovering", "unreachable") || report.Status == "unreachable" && (report.Role != "worker" || report.Healthy) {
		return invalid()
	}
	if job := report.Job; job != nil {
		if !ValidID(job.ID) || job.Kind != "run" || !nodeState(job.State, "CLAIMING", "QUEUED", "PREPARING", "RUNNING", "STOPPING", "RECOVERING", "RESETTING", "UPLOADING", "CALLBACK_PENDING", "COMPLETED", "STOPPED", "FAILED", "INTERRUPTED") {
			return invalid()
		}
	}
	if reset := report.ResetOperation; reset != nil {
		if !ValidID(reset.ID) || !nodeState(reset.State, "PENDING", "RUNNING", "COMPLETED", "FAILED") || reset.ToGeneration < 0 || reset.ToGeneration > nodeReportSafeInteger {
			return invalid()
		}
	}
	m := report.Machine
	if !nodeText(m.OS, 32) || !nodeText(m.Arch, 32) || m.Hostname != nil && !nodeText(*m.Hostname, 253) || !nodeState(m.ResourceScope, "", "unknown", "host", "cgroup") {
		return invalid()
	}
	if m.LogicalCPUs != nil && (*m.LogicalCPUs < 1 || *m.LogicalCPUs > 1000000) {
		return invalid()
	}
	for _, v := range []*float64{m.CPULimitCores, m.CPUUsagePercent} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 1e9) {
			return invalid()
		}
	}
	if m.CPULimitCores != nil && *m.CPULimitCores == 0 {
		return invalid()
	}
	for _, v := range []*int64{m.MemoryUsedBytes, m.MemoryLimitBytes, m.HostMemoryTotalBytes, m.DiskTotalBytes, m.DiskAvailableBytes} {
		if v != nil && (*v < 0 || *v > nodeReportSafeInteger) {
			return invalid()
		}
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > MaxNodeReportBytes {
		return invalid()
	}
	return nil
}
func ValidateCoreNodeReport(report CoreNodeReport, now time.Time) error {
	if report.Role != "controller" || len(report.Workers) > MaxNodeReportWorkers {
		return errors.New("invalid core node report")
	}
	if err := ValidateNodeReport(report.NodeReport, now); err != nil {
		return err
	}
	if report.ObservationEpoch != "" && !objectSHA.MatchString(report.ObservationEpoch) || len(report.Workers) > 0 && report.ObservationEpoch == "" {
		return errors.New("invalid node observation epoch")
	}
	seen := map[string]bool{}
	for _, worker := range report.Workers {
		if worker.Role != "worker" || seen[worker.ID] || worker.ID == report.ID {
			return errors.New("invalid worker inventory")
		}
		if err := ValidateNodeReport(worker, now); err != nil {
			return err
		}
		seen[worker.ID] = true
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > MaxCoreNodeReportBytes {
		return errors.New("node report exceeds limit")
	}
	return nil
}
