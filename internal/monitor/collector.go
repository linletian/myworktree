package monitor

import (
	"os"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Collector holds the per-PID CPU baseline and is safe for concurrent use.
type Collector struct {
	mu        sync.Mutex
	snapshots map[int]cpuSnapshot
}

// Collect gathers resource stats for all provided instances and returns
// a fully aggregated Stats response. instances is the list of all instances
// (both running and stopped). connTypes maps instance ID to its current
// transport ("websocket", "sse", or "").
type procSample struct {
	user, system float64
	hasTimes     bool
	rss          uint64
}

func sampleProcess(pid int32) procSample {
	var s procSample
	p, err := process.NewProcess(pid)
	if err != nil {
		return s
	}
	if times, err := p.Times(); err == nil {
		s.user = times.User
		s.system = times.System
		s.hasTimes = true
	}
	if mem, err := p.MemoryInfo(); err == nil {
		s.rss = mem.RSS
	}
	return s
}

func cpuDelta(prev cpuSnapshot, sample procSample, now time.Time) float64 {
	if !sample.hasTimes {
		return 0
	}
	elapsed := now.Sub(prev.Times).Seconds()
	if elapsed <= 0 {
		return 0
	}
	deltaUser := sample.user - prev.User
	deltaSystem := sample.system - prev.System
	cpu := (deltaUser + deltaSystem) / elapsed * 100
	if cpu < 0 {
		return 0
	}
	return cpu
}

func (c *Collector) Collect(instances []InputInstance, connTypes map[string]string) Stats {
	now := time.Now()

	// Phase 1: collect all process data outside the lock (system calls).
	daemonPID := os.Getpid()
	daemonSample := sampleProcess(int32(daemonPID))

	instanceSamples := make(map[int]procSample, len(instances))
	for _, inst := range instances {
		if inst.Status != "running" || inst.PID <= 0 {
			continue
		}
		instanceSamples[inst.PID] = sampleProcess(int32(inst.PID))
	}

	// Phase 2: compute CPU% and update snapshots under the lock.
	c.mu.Lock()
	if c.snapshots == nil {
		c.snapshots = make(map[int]cpuSnapshot)
	}

	daemonCPU := 0.0
	if daemonSample.hasTimes {
		if prev, ok := c.snapshots[daemonPID]; ok {
			daemonCPU = cpuDelta(prev, daemonSample, now)
		}
		c.snapshots[daemonPID] = cpuSnapshot{
			Times:  now,
			User:   daemonSample.user,
			System: daemonSample.system,
		}
	}

	instanceCPUs := make(map[int]float64, len(instanceSamples))
	for pid, sample := range instanceSamples {
		cpuPct := 0.0
		if sample.hasTimes {
			if prev, ok := c.snapshots[pid]; ok {
				cpuPct = cpuDelta(prev, sample, now)
			}
			c.snapshots[pid] = cpuSnapshot{
				Times:  now,
				User:   sample.user,
				System: sample.system,
			}
		}
		instanceCPUs[pid] = cpuPct
	}

	livePIDs := make(map[int]struct{}, len(instances)+1)
	livePIDs[daemonPID] = struct{}{}
	for _, inst := range instances {
		if inst.Status == "running" && inst.PID > 0 {
			livePIDs[inst.PID] = struct{}{}
		}
	}
	for pid := range c.snapshots {
		if _, ok := livePIDs[pid]; !ok {
			delete(c.snapshots, pid)
		}
	}
	c.mu.Unlock()

	// Phase 3: build result outside the lock (no more snapshot access).
	var instancesOut []InstanceStat
	worktreeMap := make(map[string]*WorktreeStat)
	var globalTotalCPU float64
	var globalTotalMem uint64
	var globalCount int

	for _, inst := range instances {
		connType := connTypes[inst.ID]
		if connType == "" {
			connType = "none"
		}

		cpuPercent := 0.0
		memRSS := uint64(0)
		if inst.Status == "running" && inst.PID > 0 {
			if sample, ok := instanceSamples[inst.PID]; ok {
				memRSS = sample.rss
				if cp, ok := instanceCPUs[inst.PID]; ok {
					cpuPercent = cp
				}
			}
		}

		if inst.Status == "running" {
			globalTotalCPU += cpuPercent
			globalTotalMem += memRSS
			globalCount++
		}

		instancesOut = append(instancesOut, InstanceStat{
			ID:                   inst.ID,
			Name:                 inst.Name,
			WorktreeID:           inst.WorktreeID,
			WorktreeName:         inst.WorktreeName,
			PID:                  inst.PID,
			Status:               inst.Status,
			CPUPercent:           cpuPercent,
			MemoryRSSBytes:       memRSS,
			MemoryBufferBytes:    inst.BufferUsedBytes,
			MemoryBufferCapBytes: inst.BufferCapBytes,
			ConnectionType:       connType,
		})

		if inst.Status == "running" {
			ws := worktreeMap[inst.WorktreeID]
			if ws == nil {
				ws = &WorktreeStat{
					WorktreeID:    inst.WorktreeID,
					Name:          inst.WorktreeName,
					InstanceCount: 0,
				}
				worktreeMap[inst.WorktreeID] = ws
			}
			ws.TotalCPU += cpuPercent
			ws.TotalMemory += memRSS
			ws.InstanceCount++
		}
	}

	var worktreesOut []WorktreeStat
	for _, ws := range worktreeMap {
		worktreesOut = append(worktreesOut, *ws)
	}

	return Stats{
		Instances: instancesOut,
		Worktrees: worktreesOut,
		Global: GlobalStat{
			TotalCPU:          globalTotalCPU + daemonCPU,
			TotalMemory:       globalTotalMem + daemonSample.rss,
			InstanceCount:     globalCount,
			DaemonCPUPercent:  daemonCPU,
			DaemonMemoryBytes: daemonSample.rss,
		},
	}
}
