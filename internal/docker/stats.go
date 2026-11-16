package docker

import (
	"context"
	"net/url"
)

type statSample struct {
	cpu      float64
	mem      int64
	memLimit int64
}

// stat reads a single stats sample for a container and computes CPU percent
// the same way the docker CLI does: the container's CPU-time delta over the
// system CPU-time delta, scaled by the number of CPUs.
func (m *Manager) stat(ctx context.Context, id string) (statSample, error) {
	var s apiStats
	err := m.c.getJSON(ctx, "/containers/"+esc(id)+"/stats",
		url.Values{"stream": {"false"}, "one-shot": {"true"}}, &s)
	if err != nil {
		return statSample{}, err
	}

	out := statSample{mem: s.memoryUsed(), memLimit: s.MemoryStats.Limit}

	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage - s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemCPUUsage - s.PreCPUStats.SystemCPUUsage)
	cpus := s.CPUStats.OnlineCPUs
	if cpus == 0 {
		cpus = 1
	}
	if cpuDelta > 0 && sysDelta > 0 {
		out.cpu = (cpuDelta / sysDelta) * float64(cpus) * 100
	}
	return out, nil
}

// memoryUsed mirrors docker's own calculation: total usage minus the page
// cache, which is reclaimable and misleading to show as "used".
func (s apiStats) memoryUsed() int64 {
	used := s.MemoryStats.Usage
	if cache, ok := s.MemoryStats.Stats["inactive_file"]; ok {
		used -= cache
	} else if cache, ok := s.MemoryStats.Stats["total_inactive_file"]; ok {
		used -= cache
	}
	if used < 0 {
		return 0
	}
	return used
}
