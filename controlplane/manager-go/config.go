package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	RedisAddr, RedisPassword, RedisPrefix, RunnerCallbackURL                          string
	RedisDB                                                                           int
	DatasetMaxBytes, TaskInputMaxBytes                                                int64
	proxyTestResolver                                                                 func(context.Context, string) ([]net.IPAddr, error)
	proxyTestDial                                                                     func(context.Context, string, string) (net.Conn, error)
	MCPPublicURL                                                                      string
	MCPLocalPort                                                                      int
	mcpCatalogTestURL                                                                 string // Unit tests only; no environment or request can set an arbitrary internal URL.
	MySQLTablePrefix                                                                  string
	StateDir, PlansDir, ResultsDir, FrontendDir                                       string
	AdminToken, SessionKey, AgentToken                                                string
	ControllerURLs, WorkerURLs                                                        map[string]string
	DefaultClusterWorkers                                                             map[string][]string
	ManagerID, Host                                                                   string
	Port                                                                              int
	Tick, AgentTimeout, StopTimeout, WatchdogTimeout                                  time.Duration
	SessionSeconds                                                                    int
	SecureCookie                                                                      bool
	ArchiveEndpoint, ArchiveBucket, ArchiveAccessKey, ArchiveSecretKey, ArchiveRegion string
	ArchiveTimeout                                                                    time.Duration
	UploadIdleTimeout, RunnerStaleAfter                                               time.Duration
	MySQLHost, MySQLDatabase, MySQLUser, MySQLPassword                                string
	MySQLPort                                                                         int
	MySQLTimeout                                                                      time.Duration
}

func env(name, value string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return value
}
func secret(name, path string) (string, error) {
	b, e := os.ReadFile(env(name, path))
	if e != nil {
		return "", fmt.Errorf("%s must name a provisioned secret file", name)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%s cannot be empty", name)
	}
	return v, nil
}
func envDuration(name string, seconds float64) (time.Duration, error) {
	f, e := strconv.ParseFloat(env(name, strconv.FormatFloat(seconds, 'f', -1, 64)), 64)
	nanoseconds := f * float64(time.Second)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || nanoseconds < 1 || nanoseconds >= math.Exp2(63) {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return time.Duration(nanoseconds), nil
}
func loadConfig() (Config, error) {
	c := Config{StateDir: env("JMETER_STATE_DIR", "/state"), PlansDir: env("JMETER_PLANS_DIR", "/plans"), ResultsDir: env("JMETER_RESULTS_DIR", "/results"), FrontendDir: env("JMETER_FRONTEND_DIR", "/app/frontend/dist"), ManagerID: env("JMETER_MANAGER_ID", "manager"), Host: env("JMETER_MANAGER_HOST", "0.0.0.0"), SessionSeconds: 43200, SecureCookie: env("JMETER_COOKIE_SECURE", "0") == "1", ArchiveEndpoint: env("JMETER_ARCHIVE_ENDPOINT", ""), ArchiveBucket: env("JMETER_ARCHIVE_BUCKET", "jmeter-reports"), ArchiveRegion: env("JMETER_ARCHIVE_REGION", "us-east-1")}
	c.RedisAddr = env("JMETER_REDIS_ADDR", "redis:6379")
	c.RedisPrefix = env("JMETER_REDIS_PREFIX", "jmeter:v2")
	c.RunnerCallbackURL = env("JMETER_RUNNER_CALLBACK_URL", "http://manager:8080/api/runner/callback")
	c.MySQLHost = env("JMETER_MYSQL_HOST", "mysql")
	c.MySQLDatabase = env("JMETER_MYSQL_DATABASE", "jmeter")
	c.MySQLUser = env("JMETER_MYSQL_USER", "jmeter")
	var e error
	if _, e = workflowCapacity(); e != nil {
		return c, e
	}
	if c.RedisPassword, e = secret("JMETER_REDIS_PASSWORD_FILE", "/run/platform/redis-password"); e != nil {
		return c, e
	}
	if c.RedisDB, e = strconv.Atoi(env("JMETER_REDIS_DB", "0")); e != nil || c.RedisDB < 0 {
		return c, fmt.Errorf("invalid Redis DB")
	}
	if c.DatasetMaxBytes, e = strconv.ParseInt(env("JMETER_DATASET_MAX_BYTES", "268435456"), 10, 64); e != nil || c.DatasetMaxBytes < 1 || c.DatasetMaxBytes > 1024*1024*1024 {
		return c, fmt.Errorf("invalid dataset byte limit")
	}
	if c.TaskInputMaxBytes, e = strconv.ParseInt(env("JMETER_TASK_INPUT_MAX_BYTES", "1073741824"), 10, 64); e != nil || c.TaskInputMaxBytes < 1 || c.TaskInputMaxBytes > 8*1024*1024*1024 {
		return c, fmt.Errorf("invalid task input byte limit")
	}
	c.MCPPublicURL = env("JMETER_MCP_PUBLIC_URL", "")
	if c.MCPLocalPort, e = strconv.Atoi(env("JMETER_MCP_PORT", "8081")); e != nil || c.MCPLocalPort < 1 || c.MCPLocalPort > 65535 {
		return c, fmt.Errorf("invalid MCP port")
	}
	if c.MySQLPort, e = strconv.Atoi(env("JMETER_MYSQL_PORT", "3306")); e != nil || c.MySQLPort < 1 || c.MySQLPort > 65535 {
		return c, fmt.Errorf("invalid MySQL port")
	}
	if c.MySQLPassword, e = secret("JMETER_MYSQL_PASSWORD_FILE", "/run/platform/mysql-password"); e != nil {
		return c, e
	}
	if c.MySQLTimeout, e = envDuration("JMETER_MYSQL_TIMEOUT", 15); e != nil {
		return c, e
	}
	for _, p := range []*string{&c.StateDir, &c.PlansDir, &c.ResultsDir, &c.FrontendDir} {
		*p, e = filepath.Abs(*p)
		if e != nil {
			return c, e
		}
	}
	if e = validID(c.ManagerID); e != nil {
		return c, fmt.Errorf("invalid manager ID")
	}
	if c.AdminToken, e = secret("JMETER_ADMIN_TOKEN_FILE", "/run/platform/admin-token"); e != nil {
		return c, e
	}
	if c.SessionKey, e = secret("JMETER_SESSION_KEY_FILE", "/run/platform/session-key"); e != nil {
		return c, e
	}
	if c.AgentToken, e = secret("JMETER_AGENT_TOKEN_FILE", "/run/platform/agent-token"); e != nil {
		return c, e
	}
	if c.ArchiveEndpoint != "" {
		if c.ArchiveAccessKey, e = secret("JMETER_ARCHIVE_ACCESS_KEY_FILE", "/run/platform/minio-access-key"); e != nil {
			return c, e
		}
		if c.ArchiveSecretKey, e = secret("JMETER_ARCHIVE_SECRET_KEY_FILE", "/run/platform/minio-secret-key"); e != nil {
			return c, e
		}
	}
	if c.Port, e = strconv.Atoi(env("JMETER_MANAGER_PORT", "8080")); e != nil || c.Port < 1 || c.Port > 65535 {
		return c, fmt.Errorf("invalid manager port")
	}
	if e = json.Unmarshal([]byte(env("JMETER_CONTROLLER_URLS", `{}`)), &c.ControllerURLs); e != nil {
		return c, e
	}
	if e = json.Unmarshal([]byte(env("JMETER_WORKER_URLS", `{}`)), &c.WorkerURLs); e != nil {
		return c, e
	}
	if e = validateNodeInventories(c.ControllerURLs, c.WorkerURLs); e != nil {
		return c, e
	}
	rawGroups, explicitGroups := os.LookupEnv("PRESSURECOOKER_CLUSTER_WORKERS")
	if c.DefaultClusterWorkers, e = parseDefaultClusterWorkers(rawGroups, explicitGroups, c.ControllerURLs, c.WorkerURLs); e != nil {
		return c, e
	}
	for _, d := range []struct {
		name  string
		value float64
		dest  *time.Duration
	}{{"JMETER_UPLOAD_IDLE_TIMEOUT", 30, &c.UploadIdleTimeout}, {"JMETER_RUNNER_STALE_SECONDS", 90, &c.RunnerStaleAfter}, {"JMETER_TICK_SECONDS", 2, &c.Tick}, {"JMETER_AGENT_TIMEOUT", 3, &c.AgentTimeout}, {"JMETER_STOP_TIMEOUT", 20, &c.StopTimeout}, {"JMETER_WATCHDOG_TIMEOUT", 90, &c.WatchdogTimeout}, {"JMETER_ARCHIVE_TIMEOUT", 10, &c.ArchiveTimeout}} {
		*d.dest, e = envDuration(d.name, d.value)
		if e != nil {
			return c, e
		}
	}
	return c, nil
}
