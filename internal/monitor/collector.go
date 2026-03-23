package monitor

import (
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
func (c *Collector) Collect(instances []InputInstance, connTypes map[string]string) Stats {
	now := time.Now()

	c.mu.Lock()
	if c.snapshots == nil {
		c.snapshots = make(map[int]cpuSnapshot)
	}
	defer c.mu.Unlock()

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
			p, err := process.NewProcess(int32(inst.PID))
			if err == nil {
				times, err := p.Times()
				if err == nil {
					prev, ok := c.snapshots[inst.PID]
					if ok {
						elapsed := now.Sub(prev.Times).Seconds()
						if elapsed > 0 {
							deltaUser := times.User - prev.User
							deltaSystem := times.System - prev.System
							cpuPercent = (deltaUser + deltaSystem) / elapsed * 100
							if cpuPercent < 0 {
								cpuPercent = 0
							}
						}
					}
					c.snapshots[inst.PID] = cpuSnapshot{
						Times:  now,
						User:   times.User,
						System: times.System,
					}
				}

				mem, err := p.MemoryInfo()
				if err == nil {
					memRSS = mem.RSS
				}
			}
		}

		if inst.Status == "running" {
			globalTotalCPU += cpuPercent
			globalTotalMem += memRSS
			globalCount++
		}

		instancesOut = append(instancesOut, InstanceStat{
			ID:             inst.ID,
			Name:           inst.Name,
			WorktreeID:     inst.WorktreeID,
			WorktreeName:   inst.WorktreeName,
			PID:            inst.PID,
			Status:         inst.Status,
			CPUPercent:     cpuPercent,
			MemoryRSSBytes: memRSS,
			ConnectionType: connType,
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

	// Prune stale snapshots: keep only PIDs that are currently running.
	livePIDs := make(map[int]struct{}, len(instances))
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

	return Stats{
		Instances: instancesOut,
		Worktrees: worktreesOut,
		Global: GlobalStat{
			TotalCPU:      globalTotalCPU,
			TotalMemory:   globalTotalMem,
			InstanceCount: globalCount,
		},
	}
}
