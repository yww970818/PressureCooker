package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"pressurecooker/execution/protocol"
)

func (c *config) nodeSettings() error {
	c.managerURL = env("PRESSURECOOKER_MANAGER_URL", "http://manager:8080")
	u, err := url.Parse(c.managerURL)
	if !nodeAgentURL(c.managerURL) || err != nil || u.Path != "" && strings.TrimRight(u.Path, "/") != strings.TrimRight(path.Clean(u.Path), "/") {
		return errors.New("invalid manager URL")
	}
	c.managerURL = strings.TrimRight(c.managerURL, "/")
	name := c.id
	if c.role == "controller" {
		switch c.id {
		case "controller-a":
			name = "PressureCooker-Core-1"
		case "controller-b":
			name = "PressureCooker-Core-2"
		default:
			name = "PressureCooker-Core-" + c.id
		}
	}
	c.nodeName = env("PRESSURECOOKER_NODE_NAME", name)
	if !utf8.ValidString(c.nodeName) || len(c.nodeName) > 800 || utf8.RuneCountInString(c.nodeName) > 200 || strings.TrimSpace(c.nodeName) == "" || strings.ContainsAny(c.nodeName, "\x00\r\n") {
		return errors.New("invalid node display name")
	}
	return nil
}

func (s *service) engineStatus() map[string]any {
	if s.worker != nil {
		return s.worker.status()
	}
	if s.runner != nil {
		return s.runner.status()
	}
	return map[string]any{"healthy": false, "status": "recovering", "generation": 0, "job": nil, "reset_operation": nil}
}

func (s *service) nodeSnapshot(now time.Time, sequence int64, queueConnected *bool) protocol.NodeReport {
	// Existing engine status remains authoritative for healthy/status/job/reset.
	// Queue connectivity is independent transport evidence and does not change it.
	raw, _ := json.Marshal(s.engineStatus())
	var report protocol.NodeReport
	_ = json.Unmarshal(raw, &report)
	report.Version, report.ID, report.Role = 1, s.c.id, s.c.role
	report.DisplayName = s.c.nodeName
	if report.DisplayName == "" {
		report.DisplayName = s.c.id
	}
	report.BootID, report.BootStartedAtMS = s.boot, s.bootStartedAtMS
	report.Sequence, report.ObservedAt = sequence, now.UTC().Format(time.RFC3339Nano)
	report.QueueConnected = queueConnected
	report.Machine = protocol.NodeMachine{ResourceScope: "unknown"}
	if s.machine != nil {
		report.Machine = s.machine.sample()
	}
	return report
}

func (s *service) statusSnapshot(ctx context.Context) protocol.NodeReport {
	var connected *bool
	if s.q != nil {
		check, end := context.WithTimeout(ctx, 500*time.Millisecond)
		ok := s.q.Ping(check) == nil
		end()
		connected = &ok
	}
	return s.nodeSnapshot(time.Now(), s.statusSequence.Add(1), connected)
}
