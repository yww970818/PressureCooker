package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func machineFixture(t *testing.T, files map[string]string) *machineCollector {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &machineCollector{root: root, work: root, now: time.Now}
}

func TestNodeMachineUsesContainerLimitsInsteadOfHostTotals(t *testing.T) {
	c := machineFixture(t, map[string]string{"proc/meminfo": "MemTotal: 16777216 kB\nMemAvailable: 8388608 kB\n", "proc/self/cgroup": "0::/\n", "sys/fs/cgroup/cpu.max": "50000 100000\n", "sys/fs/cgroup/cpu.stat": "usage_usec 100000\n", "sys/fs/cgroup/memory.max": "268435456\n", "sys/fs/cgroup/memory.current": "123456\n"})
	m := c.sample()
	if m.ResourceScope != "cgroup" || m.CPULimitCores == nil || *m.CPULimitCores != 0.5 {
		t.Fatal("container CPU quota missing", m)
	}
	if m.MemoryLimitBytes == nil || *m.MemoryLimitBytes != 268435456 || m.MemoryUsedBytes == nil || *m.MemoryUsedBytes != 123456 {
		t.Fatal("host RAM replaced container observations", m)
	}
	if m.HostMemoryTotalBytes == nil || *m.HostMemoryTotalBytes != 17179869184 {
		t.Fatal("host total lost its separate identity", m)
	}
	if m.CPUUsagePercent != nil {
		t.Fatal("first CPU observation invented usage", m)
	}
}

func TestNodeMachineMissingAndUnlimitedMetricsStayNull(t *testing.T) {
	c := machineFixture(t, nil)
	m := c.sample()
	if m.ResourceScope != "unknown" || m.CPULimitCores != nil || m.MemoryLimitBytes != nil || m.MemoryUsedBytes != nil || m.CPUUsagePercent != nil {
		t.Fatal("missing machine data became zero or host quota", m)
	}
	c = machineFixture(t, map[string]string{"proc/meminfo": "MemTotal: 16777216 kB\nMemAvailable: 8388608 kB\n", "proc/self/cgroup": "0::/\n", "sys/fs/cgroup/cpu.max": "max 100000\n", "sys/fs/cgroup/memory.max": "max\n", "sys/fs/cgroup/memory.current": "42\n"})
	m = c.sample()
	if m.CPULimitCores != nil || m.MemoryLimitBytes != nil || m.MemoryUsedBytes == nil || *m.MemoryUsedBytes != 42 {
		t.Fatal("unlimited container resource became a host limit", m)
	}
}

func TestNodeMachineCPUUsesElapsedContainerCountersAndOwnCgroup(t *testing.T) {
	c := machineFixture(t, map[string]string{"proc/self/cgroup": "0::/own.slice\n", "sys/fs/cgroup/cpu.max": "max 100000\n", "sys/fs/cgroup/own.slice/cpu.max": "50000 100000\n", "sys/fs/cgroup/own.slice/cpu.stat": "usage_usec 100000\n"})
	now := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	if m := c.sample(); m.CPUUsagePercent != nil || m.CPULimitCores == nil || *m.CPULimitCores != 0.5 {
		t.Fatal("wrong cgroup or invented initial CPU", m)
	}
	now = now.Add(time.Second)
	if err := os.WriteFile(filepath.Join(c.root, "sys/fs/cgroup/own.slice/cpu.stat"), []byte("usage_usec 350000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := c.sample()
	if m.CPUUsagePercent == nil || math.Abs(*m.CPUUsagePercent-50) > 1e-9 {
		t.Fatal("container CPU delta was not normalized by quota", m)
	}
}

func TestNodeMachineSupportsV1AndRejectsMalformedMetrics(t *testing.T) {
	c := machineFixture(t, map[string]string{"proc/self/cgroup": "2:cpu,cpuacct:/box\n3:memory:/box\n", "sys/fs/cgroup/cpu/box/cpu.cfs_quota_us": "100000\n", "sys/fs/cgroup/cpu/box/cpu.cfs_period_us": "100000\n", "sys/fs/cgroup/memory/box/memory.limit_in_bytes": "9223372036854771712\n", "sys/fs/cgroup/memory/box/memory.usage_in_bytes": "512\n"})
	m := c.sample()
	if m.CPULimitCores == nil || *m.CPULimitCores != 1 || m.MemoryLimitBytes != nil || m.MemoryUsedBytes == nil || *m.MemoryUsedBytes != 512 {
		t.Fatal("v1 limits or unlimited sentinel misread", m)
	}
	c = machineFixture(t, map[string]string{"proc/self/cgroup": "0::/../../outside\n", "sys/fs/cgroup/cpu.max": "garbage\n", "sys/fs/cgroup/memory.max": "-1\n", "sys/fs/cgroup/memory.current": "not-a-number\n"})
	m = c.sample()
	if m.CPULimitCores != nil || m.MemoryLimitBytes != nil || m.MemoryUsedBytes != nil {
		t.Fatal("malformed input became usable metrics", m)
	}
}
