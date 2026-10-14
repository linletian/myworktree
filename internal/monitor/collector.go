package monitor

import (
	"os"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
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

	// ONE process enumeration for the whole collect (review G5): every
	// instance's subtree walk reads the same ppid index instead of paying
	// a gopsutil Children() full /proc scan per node per instance. A
	// failed enumeration is not fatal — it marks every tree unavailable
	// (review G3), which is the truth the UI renders as "unavailable".
	//
	// Skip it entirely when no running instance carries a live PID
	// (ARB-F10): the walk below runs only for those, so building the index
	// no one reads is a wasted /proc glob-scan on a fully-stopped set.
	var procIx *procIndex
	if anyRunningInstance(instances) {
		procIx = buildProcIndex()
	}

	// System-level signals for the alerts block and the System section.
	// Sampled once per collect; failures leave the zero value, which the
	// alert evaluator skips (never fabricates).
	var system SystemStat
	if content, err := readPSIMemory(); err == nil {
		if v, ok := parsePSIsomeAvg10(content); ok {
			system.PSIsSomeAvg10 = v
			system.HasPSI = true
		}
	}
	if vm, err := readVirtualMemory(); err == nil {
		system.AvailableBytes = vm.Available
		system.TotalBytes = vm.Total
	}

	for _, inst := range instances {
		connType := connTypes[inst.ID]
		if connType == "" {
			connType = "none"
		}

		cpuPercent := 0.0
		memRSS := uint64(0)
		childCount := 0
		subtreeRSS := uint64(0)
		var children []ChildProc
		childrenTrunc := false
		treeUnavailable := false
		if inst.Status == "running" && inst.PID > 0 {
			if sample, ok := instanceSamples[inst.PID]; ok {
				memRSS = sample.rss
				if cp, ok := instanceCPUs[inst.PID]; ok {
					cpuPercent = cp
				}
			}
			// Subtree walk (read-only, best-effort — see tree.go) over the
			// collect's single procIndex. The main PID's own RSS enters the
			// subtree total here; the walk itself collects descendants only.
			// When the walk cannot read the tree at all, treeUnavailable
			// rides the row (review G3) so the zero below reads as
			// "unknown", never as a measured all-clear.
			tree := walkSubtree(inst.PID, procIx)
			childCount = tree.ChildCount
			subtreeRSS = memRSS + tree.SubtreeRSSBytes
			children = tree.Children
			childrenTrunc = tree.Truncated
			treeUnavailable = tree.Unavailable
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
			ChildCount:           childCount,
			SubtreeRSSBytes:      subtreeRSS,
			Children:             children,
			ChildrenTruncated:    childrenTrunc,
			TreeUnavailable:      treeUnavailable,
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

	// Fresh full alert evaluation: system signals AND per-instance subtree
	// signals (the tree walk above is what makes the subtree signals
	// possible — this is the ONLY place they are computed; the background
	// sampler deliberately does not walk trees, see sampler.go).
	instTrees := make([]InstanceTree, 0, len(instancesOut))
	for _, is := range instancesOut {
		if is.Status != "running" {
			continue
		}
		instTrees = append(instTrees, InstanceTree{
			ID:         is.ID,
			Name:       is.Name,
			ChildCount: is.ChildCount,
			SubtreeRSS: is.SubtreeRSSBytes,
		})
	}
	alerts := EvaluateAlerts(AlertInput{
		PSIsSomeAvg10:  system.PSIsSomeAvg10,
		HasPSI:         system.HasPSI,
		AvailableBytes: system.AvailableBytes,
		TotalBytes:     system.TotalBytes,
		DaemonRSSBytes: daemonSample.rss,
		Instances:      instTrees,
	})
	if alerts == nil {
		alerts = []Alert{} // JSON contract: alerts is [] , never null
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
		Alerts: alerts,
		System: system,
	}
}

// anyRunningInstance reports whether at least one instance is running with a
// live PID (>0) — i.e. whether any subtree walk will actually run. The
// collect skips the /proc ppid index build when this is false (ARB-F10), so
// this predicate MUST mirror the walk's own guard exactly
// (`inst.Status == "running" && inst.PID > 0`).
func anyRunningInstance(instances []InputInstance) bool {
	for _, inst := range instances {
		if inst.Status == "running" && inst.PID > 0 {
			return true
		}
	}
	return false
}

// readVirtualMemory is the system-RAM access seam (gopsutil VirtualMemory).
// Injectable so tests pin the alert ratios without touching real memory.
var readVirtualMemory = func() (*mem.VirtualMemoryStat, error) {
	return mem.VirtualMemory()
}
