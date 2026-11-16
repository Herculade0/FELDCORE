// Package system collects live host metrics for the FELDCORE dashboard.
//
// It is a thin, dependency-light wrapper over gopsutil. A single Collector is
// shared across requests and keeps the small amount of state (previous network
// counters) needed to turn monotonic counters into per-second rates.
package system

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// Info is static-ish host information that rarely changes.
type Info struct {
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Platform    string `json:"platform"`
	PlatformVer string `json:"platformVersion"`
	Kernel      string `json:"kernel"`
	Arch        string `json:"arch"`
	CPUModel    string `json:"cpuModel"`
	CPUCores    int    `json:"cpuCores"`
	BootTime    int64  `json:"bootTime"`
}

// DiskUsage describes one mounted filesystem.
type DiskUsage struct {
	Mountpoint  string  `json:"mountpoint"`
	Device      string  `json:"device"`
	Fstype      string  `json:"fstype"`
	TotalBytes  uint64  `json:"totalBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

// NetRate is throughput for one interface, in bytes per second.
type NetRate struct {
	Name     string  `json:"name"`
	RxPerSec float64 `json:"rxPerSec"`
	TxPerSec float64 `json:"txPerSec"`
}

// Stats is one live sample of the machine's state.
type Stats struct {
	Timestamp     int64       `json:"timestamp"`
	UptimeSeconds uint64      `json:"uptimeSeconds"`
	CPUPercent    float64     `json:"cpuPercent"`
	PerCore       []float64   `json:"perCore"`
	Load1         float64     `json:"load1"`
	Load5         float64     `json:"load5"`
	Load15        float64     `json:"load15"`
	MemTotal      uint64      `json:"memTotal"`
	MemUsed       uint64      `json:"memUsed"`
	MemPercent    float64     `json:"memPercent"`
	SwapTotal     uint64      `json:"swapTotal"`
	SwapUsed      uint64      `json:"swapUsed"`
	Disks         []DiskUsage `json:"disks"`
	Net           []NetRate   `json:"net"`
}

// Collector gathers metrics and tracks the state needed for rate calculations.
type Collector struct {
	mu       sync.Mutex
	prevNet  map[string]net.IOCountersStat
	prevTime time.Time
}

// NewCollector returns a ready-to-use Collector.
func NewCollector() *Collector {
	return &Collector{prevNet: map[string]net.IOCountersStat{}}
}

// Info returns static host information.
func (c *Collector) Info(ctx context.Context) (*Info, error) {
	h, err := host.InfoWithContext(ctx)
	if err != nil {
		return nil, err
	}
	info := &Info{
		Hostname:    h.Hostname,
		OS:          h.OS,
		Platform:    h.Platform,
		PlatformVer: h.PlatformVersion,
		Kernel:      h.KernelVersion,
		Arch:        h.KernelArch,
		BootTime:    int64(h.BootTime),
	}
	if ci, err := cpu.InfoWithContext(ctx); err == nil && len(ci) > 0 {
		info.CPUModel = ci[0].ModelName
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		info.CPUCores = n
	}
	return info, nil
}

// Sample collects one live Stats snapshot.
func (c *Collector) Sample(ctx context.Context) (*Stats, error) {
	now := time.Now()
	s := &Stats{Timestamp: now.UnixMilli()}

	if up, err := host.UptimeWithContext(ctx); err == nil {
		s.UptimeSeconds = up
	}

	// CPU: gopsutil returns usage since the previous call when interval==0.
	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		s.CPUPercent = pct[0]
	}
	if per, err := cpu.PercentWithContext(ctx, 0, true); err == nil {
		s.PerCore = per
	}

	if l, err := load.AvgWithContext(ctx); err == nil {
		s.Load1, s.Load5, s.Load15 = l.Load1, l.Load5, l.Load15
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.MemTotal, s.MemUsed, s.MemPercent = vm.Total, vm.Used, vm.UsedPercent
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		s.SwapTotal, s.SwapUsed = sw.Total, sw.Used
	}

	s.Disks = c.disks(ctx)
	s.Net = c.netRates(ctx, now)
	return s, nil
}

// disks returns usage for physical, non-virtual mounts.
func (c *Collector) disks(ctx context.Context) []DiskUsage {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}
	// The same block device can be mounted in several places (bind mounts,
	// WSL's /mnt/wslg/distro). Report each device once, at its first mount.
	seen := map[string]bool{}
	seenDev := map[string]bool{}
	var out []DiskUsage
	for _, p := range parts {
		if skipFstype(p.Fstype) {
			continue
		}
		if seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true
		if strings.HasPrefix(p.Device, "/dev/") {
			if seenDev[p.Device] {
				continue
			}
			seenDev[p.Device] = true
		}
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		out = append(out, DiskUsage{
			Mountpoint:  p.Mountpoint,
			Device:      p.Device,
			Fstype:      p.Fstype,
			TotalBytes:  u.Total,
			UsedBytes:   u.Used,
			UsedPercent: u.UsedPercent,
		})
	}
	return out
}

// netRates converts interface byte counters into per-second rates.
func (c *Collector) netRates(ctx context.Context, now time.Time) []NetRate {
	counters, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	dt := now.Sub(c.prevTime).Seconds()
	var out []NetRate
	for _, io := range counters {
		if skipIface(io.Name) {
			continue
		}
		prev, ok := c.prevNet[io.Name]
		if ok && dt > 0 {
			out = append(out, NetRate{
				Name:     io.Name,
				RxPerSec: float64(io.BytesRecv-prev.BytesRecv) / dt,
				TxPerSec: float64(io.BytesSent-prev.BytesSent) / dt,
			})
		}
		c.prevNet[io.Name] = io
	}
	c.prevTime = now
	return out
}

func skipFstype(fs string) bool {
	switch fs {
	case "tmpfs", "devtmpfs", "overlay", "squashfs", "proc", "sysfs",
		"cgroup", "cgroup2", "devpts", "mqueue", "debugfs", "tracefs",
		"ramfs", "autofs", "binfmt_misc", "fusectl", "configfs", "none":
		return true
	}
	return false
}

func skipIface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, pfx := range []string{"veth", "docker", "br-", "virbr"} {
		if len(name) >= len(pfx) && name[:len(pfx)] == pfx {
			return true
		}
	}
	return false
}
