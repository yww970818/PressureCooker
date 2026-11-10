package main

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"pressurecooker/execution/protocol"
)

type machineCollector struct {
	mu            sync.Mutex
	root, work    string
	now           func() time.Time
	previousCPU   *float64
	previousQuota *float64
	previousAt    time.Time
}

func newMachineCollector(work string) *machineCollector {
	return &machineCollector{root: "/", work: work, now: time.Now}
}
func (c *machineCollector) read(name string) (string, bool) {
	f, err := os.Open(filepath.Join(c.root, name))
	if err != nil {
		return "", false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}
func machineInteger(text string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	return v, err == nil && v >= 0
}
func (c *machineCollector) number(name string) *int64 {
	text, ok := c.read(name)
	if !ok {
		return nil
	}
	v, ok := machineInteger(text)
	if !ok {
		return nil
	}
	return &v
}
func machineCgroupPath(text string) (string, bool) {
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\\") || filepath.Clean(text) != text || strings.Contains(text, "..") {
		return "", false
	}
	return strings.TrimPrefix(text, "/"), true
}
func (c *machineCollector) cgroups() (string, map[string]string, bool) {
	raw, ok := c.read("proc/self/cgroup")
	controllers := map[string]string{}
	if !ok {
		return "", controllers, false
	}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		path, ok := machineCgroupPath(parts[2])
		if !ok {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			return filepath.Join("sys/fs/cgroup", path), controllers, true
		}
		for _, name := range strings.Split(parts[1], ",") {
			controllers[name] = path
		}
	}
	return "", controllers, false
}
func machineCPUSet(raw string) *float64 {
	seen := map[int]bool{}
	for _, part := range strings.Split(strings.TrimSpace(raw), ",") {
		ends := strings.Split(part, "-")
		if len(ends) < 1 || len(ends) > 2 {
			return nil
		}
		from, err := strconv.Atoi(ends[0])
		if err != nil || from < 0 || from > 65535 {
			return nil
		}
		to := from
		if len(ends) == 2 {
			to, err = strconv.Atoi(ends[1])
			if err != nil || to < from || to > 65535 {
				return nil
			}
		}
		for i := from; i <= to; i++ {
			seen[i] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	n := float64(len(seen))
	return &n
}
func (c *machineCollector) sample() protocol.NodeMachine {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := protocol.NodeMachine{OS: runtime.GOOS, Arch: runtime.GOARCH, ResourceScope: "unknown"}
	if host, err := os.Hostname(); err == nil && host != "" && len(host) <= 253 && !strings.ContainsAny(host, "\x00\r\n") {
		m.Hostname = &host
	}
	logical := runtime.NumCPU()
	if logical > 0 {
		m.LogicalCPUs = &logical
	}
	total, available := (*int64)(nil), (*int64)(nil)
	if raw, ok := c.read("proc/meminfo"); ok {
		for _, line := range strings.Split(raw, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[2] != "kB" {
				continue
			}
			value, ok := machineInteger(fields[1])
			if !ok || value > 9007199254740991/1024 {
				continue
			}
			value *= 1024
			switch fields[0] {
			case "MemTotal:":
				total = &value
			case "MemAvailable:":
				available = &value
			}
		}
	}
	if total != nil && *total > 0 {
		m.HostMemoryTotalBytes = total
		m.ResourceScope = "host"
	}
	var cpuSeconds *float64
	base, controllers, v2 := c.cgroups()
	if v2 {
		if raw, ok := c.read(filepath.Join(base, "cpu.max")); ok {
			fields := strings.Fields(raw)
			if len(fields) == 2 {
				quota, qok := machineInteger(fields[0])
				period, pok := machineInteger(fields[1])
				if qok && pok && quota > 0 && period > 0 {
					value := float64(quota) / float64(period)
					if value <= 1e6 {
						m.CPULimitCores = &value
					}
				}
			}
		}
		if raw, ok := c.read(filepath.Join(base, "cpuset.cpus.effective")); ok {
			if count := machineCPUSet(raw); count != nil && (m.CPULimitCores == nil || *count < *m.CPULimitCores) {
				m.CPULimitCores = count
			}
		}
		if raw, ok := c.read(filepath.Join(base, "cpu.stat")); ok {
			for _, line := range strings.Split(raw, "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == "usage_usec" {
					if n, ok := machineInteger(fields[1]); ok {
						value := float64(n) / 1e6
						cpuSeconds = &value
					}
				}
			}
		}
		m.MemoryUsedBytes = c.number(filepath.Join(base, "memory.current"))
		m.MemoryLimitBytes = c.number(filepath.Join(base, "memory.max"))
	} else {
		if path, ok := controllers["cpu"]; ok {
			for _, mount := range []string{"cpu", "cpu,cpuacct"} {
				base = filepath.Join("sys/fs/cgroup", mount, path)
				quota := c.number(filepath.Join(base, "cpu.cfs_quota_us"))
				period := c.number(filepath.Join(base, "cpu.cfs_period_us"))
				if quota != nil && period != nil && *quota > 0 && *period > 0 {
					value := float64(*quota) / float64(*period)
					if value <= 1e6 {
						m.CPULimitCores = &value
					}
					break
				}
			}
		}
		if path, ok := controllers["cpuacct"]; ok {
			for _, mount := range []string{"cpu,cpuacct", "cpuacct", "cpu"} {
				if n := c.number(filepath.Join("sys/fs/cgroup", mount, path, "cpuacct.usage")); n != nil {
					value := float64(*n) / 1e9
					cpuSeconds = &value
					break
				}
			}
		}
		if path, ok := controllers["cpuset"]; ok {
			if raw, ok := c.read(filepath.Join("sys/fs/cgroup/cpuset", path, "cpuset.cpus")); ok {
				if count := machineCPUSet(raw); count != nil && (m.CPULimitCores == nil || *count < *m.CPULimitCores) {
					m.CPULimitCores = count
				}
			}
		}
		if path, ok := controllers["memory"]; ok {
			base = filepath.Join("sys/fs/cgroup/memory", path)
			m.MemoryUsedBytes = c.number(filepath.Join(base, "memory.usage_in_bytes"))
			m.MemoryLimitBytes = c.number(filepath.Join(base, "memory.limit_in_bytes"))
		}
	}
	if m.MemoryLimitBytes != nil && (*m.MemoryLimitBytes == 0 || *m.MemoryLimitBytes >= 1<<60) {
		m.MemoryLimitBytes = nil
	}
	for _, value := range []**int64{&m.MemoryUsedBytes, &m.MemoryLimitBytes} {
		if *value != nil && **value > 9007199254740991 {
			*value = nil
		}
	}
	if m.CPULimitCores != nil || m.MemoryUsedBytes != nil || m.MemoryLimitBytes != nil {
		m.ResourceScope = "cgroup"
	}
	if m.ResourceScope != "cgroup" && total != nil && available != nil && *available <= *total {
		used := *total - *available
		m.MemoryUsedBytes = &used
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if cpuSeconds != nil && m.CPULimitCores != nil && c.previousCPU != nil && c.previousQuota != nil && *c.previousQuota == *m.CPULimitCores && !c.previousAt.IsZero() && now.After(c.previousAt) && *cpuSeconds >= *c.previousCPU {
		percent := (*cpuSeconds - *c.previousCPU) / now.Sub(c.previousAt).Seconds() / (*m.CPULimitCores) * 100
		if !math.IsNaN(percent) && !math.IsInf(percent, 0) && percent >= 0 && percent <= 1e9 {
			m.CPUUsagePercent = &percent
		}
	}
	c.previousAt = now
	c.previousCPU = cpuSeconds
	c.previousQuota = m.CPULimitCores
	var disk unix.Statfs_t
	if c.work != "" && unix.Statfs(c.work, &disk) == nil && disk.Bsize > 0 {
		unit := uint64(disk.Bsize)
		if disk.Blocks > 0 && disk.Blocks <= 9007199254740991/unit {
			total := int64(disk.Blocks * unit)
			m.DiskTotalBytes = &total
		}
		if disk.Bavail <= 9007199254740991/unit {
			available := int64(disk.Bavail * unit)
			m.DiskAvailableBytes = &available
		}
	}
	return m
}
