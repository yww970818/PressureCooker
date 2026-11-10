// Package protocol defines the signed immutable dispatch and completion envelopes.
package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ObjectRef struct {
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Name        string `json:"name,omitempty"`
	URL         string `json:"url,omitempty"`
}
type Worker struct {
	ID       string `json:"id"`
	AgentURL string `json:"agent_url"`
	RMIHost  string `json:"rmi_host"`
}
type DatasetInput struct {
	NodeID        string      `json:"node_id"`
	NodeName      string      `json:"node_name"`
	Columns       []string    `json:"columns"`
	VariableNames []string    `json:"variable_names"`
	Delimiter     string      `json:"delimiter"`
	Files         []ObjectRef `json:"files"`
	Distribution  string      `json:"distribution"`
	Recycle       bool        `json:"recycle"`
	StopThread    bool        `json:"stop_thread"`
}
type Task struct {
	Version        int               `json:"version"`
	TaskID         string            `json:"task_id"`
	AttemptID      string            `json:"attempt_id"`
	ProjectID      string            `json:"project_id"`
	ClusterID      string            `json:"cluster_id"`
	ControllerID   string            `json:"controller_id"`
	Workers        []Worker          `json:"workers"`
	Plan           ObjectRef         `json:"plan"`
	Datasets       []DatasetInput    `json:"datasets"`
	Properties     map[string]string `json:"properties"`
	CallbackURL    string            `json:"callback_url"`
	CreatedAt      string            `json:"created_at"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}
type SignedTask struct {
	Task      Task   `json:"task"`
	Signature string `json:"signature"`
}

const MaxNodeLogBytes = 32 << 10
const MaxNodeEvidenceBytes = 2 << 20
const MaxCallbackBytes = 4 << 20
const MaxRequestSamples = 64
const MaxRequestSamplesBytes = 128 << 10

const ControllerRuntimePrefix = "pressurecooker.runtime."
const ControllerWorkersProperty = ControllerRuntimePrefix + "workers"
const ControllerTaskProperty = ControllerRuntimePrefix + "task_id"

type RequestAssertion struct {
	Name    string `json:"name"`
	Failure bool   `json:"failure"`
	Error   bool   `json:"error"`
	Message string `json:"message"`
}

type RequestSample struct {
	StepKey            string             `json:"step_key,omitempty"`
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
}

// NodeEvidence is a private task/attempt/generation-scoped worker log receipt.
// A reset receipt describes log collection, never business execution success.
type NodeEvidence struct {
	NodeID                   string          `json:"node_id"`
	TaskID                   string          `json:"task_id"`
	AttemptID                string          `json:"attempt_id"`
	Generation               int64           `json:"generation"`
	PreparedAt               string          `json:"prepared_at"`
	FinishedAt               string          `json:"finished_at"`
	Status                   string          `json:"status"`
	Logs                     []NodeLog       `json:"logs"`
	RequestSamples           []RequestSample `json:"request_samples,omitempty"`
	RequestSamplesTruncated  bool            `json:"request_samples_truncated,omitempty"`
	RequestSamplesCaptured   bool            `json:"request_samples_captured,omitempty"`
	ThreadObservationsStatus string          `json:"thread_observations_status,omitempty"`
	ThreadObservations       *ObjectRef      `json:"thread_observations,omitempty"`
}

type NodeLog struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	Truncated bool   `json:"truncated"`
	LineFrom  int64  `json:"line_from"`
	LineTo    int64  `json:"line_to"`
	Size      int64  `json:"size"`
}
type Callback struct {
	EventID        string         `json:"event_id"`
	TaskID         string         `json:"task_id"`
	AttemptID      string         `json:"attempt_id"`
	RunnerID       string         `json:"runner_id"`
	Status         string         `json:"status"`
	ExitCode       int            `json:"exit_code"`
	StartedAt      string         `json:"started_at"`
	FinishedAt     string         `json:"finished_at"`
	Artifacts      []ObjectRef    `json:"artifacts"`
	MissingFiles   []string       `json:"missing_files"`
	InputArtifacts []ObjectRef    `json:"input_artifacts,omitempty"`
	NodeEvidence   []NodeEvidence `json:"node_evidence,omitempty"`
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)

func ValidID(s string) bool { return validID.MatchString(s) }
func mac(body []byte, token string) string {
	h := hmac.New(sha256.New, []byte(token))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
func SignTask(t Task, token string) (SignedTask, error) {
	b, e := json.Marshal(t)
	if token == "" {
		return SignedTask{}, errors.New("empty key")
	}
	return SignedTask{t, mac(b, token)}, e
}
func VerifyTask(s SignedTask, token string) error {
	want, e := SignTask(s.Task, token)
	if e != nil || !hmac.Equal([]byte(s.Signature), []byte(want.Signature)) {
		return errors.New("invalid task signature")
	}
	return nil
}

const CallbackPath = "/api/runner/callback"

func NormalizeCallbackURL(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawPath != "" {
		return "", errors.New("invalid callback URL")
	}
	switch u.Path {
	case "", "/":
		u.Path = CallbackPath
	case CallbackPath:
	default:
		return "", errors.New("invalid callback path")
	}
	return u.String(), nil
}
func signature(req *http.Request, body []byte, runner, stamp, token string) string {
	return mac(append([]byte(strings.Join([]string{req.Method, req.URL.EscapedPath(), stamp, runner, ""}, "\n")), body...), token)
}
func SignRequest(req *http.Request, body []byte, runnerID, token string, now time.Time) {
	s := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set("X-JMeter-Runner", runnerID)
	req.Header.Set("X-JMeter-Timestamp", s)
	req.Header.Set("X-JMeter-Signature", signature(req, body, runnerID, s, token))
}
func VerifyRequest(req *http.Request, body []byte, token string, now time.Time) (string, error) {
	id := req.Header.Get("X-JMeter-Runner")
	s := req.Header.Get("X-JMeter-Timestamp")
	stamp, e := strconv.ParseInt(s, 10, 64)
	if token == "" || !ValidID(id) || e != nil || stamp < now.Unix()-300 || stamp > now.Unix()+300 || !hmac.Equal([]byte(req.Header.Get("X-JMeter-Signature")), []byte(signature(req, body, id, s, token))) {
		return "", errors.New("invalid request signature")
	}
	return id, nil
}

// RuntimePropertyReserved protects the official engine's process and RMI
// configuration from task and environment overrides. Compiler-owned environment
// bindings (jmeter.env.*) are checked against their provenance by the publisher.
func RuntimePropertyReserved(key string) bool {
	key = strings.ToLower(key)
	switch key {
	case "mode", "server_port", "remote_hosts", "user.properties", "system.properties", "search_paths", "plugin_dependency_paths", "user.classpath", "engine_id":
		return true
	}
	for _, prefix := range []string{"server.", "client.rmi.", "remote.", "jmeter.save.", "javax.", "jmeterengine.", "beanshell.", ControllerRuntimePrefix} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
