// Package monitor provides real-time resource stats collection (CPU%, memory RSS)
// for running instance processes.
package monitor

import "time"

// Stats is the top-level response returned by the stats collector.
type Stats struct {
	Instances []InstanceStat `json:"instances"`
	Worktrees []WorktreeStat `json:"worktrees"`
	Global    GlobalStat     `json:"global"`
}

// InstanceStat describes the resource usage and connection status of a single instance.
type InstanceStat struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	WorktreeID     string  `json:"worktree_id"`
	WorktreeName   string  `json:"worktree_name"`
	PID            int     `json:"pid"`
	Status         string  `json:"status"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryRSSBytes uint64  `json:"memory_rss_bytes"`
	ConnectionType string  `json:"connection_type"`
}

// WorktreeStat aggregates resource usage for instances within a single worktree.
type WorktreeStat struct {
	WorktreeID    string  `json:"worktree_id"`
	Name          string  `json:"name"`
	TotalCPU      float64 `json:"total_cpu"`
	TotalMemory   uint64  `json:"total_memory"`
	InstanceCount int     `json:"instance_count"`
}

// GlobalStat aggregates resource usage across all worktrees.
type GlobalStat struct {
	TotalCPU      float64 `json:"total_cpu"`
	TotalMemory   uint64  `json:"total_memory"`
	InstanceCount int     `json:"instance_count"`
}

// cpuSnapshot stores per-PID CPU timing for delta-based CPU% calculation.
type cpuSnapshot struct {
	Times  time.Time
	User   float64
	System float64
}

// InputInstance is the minimal instance info needed for resource collection.
type InputInstance struct {
	ID           string
	Name         string
	WorktreeID   string
	WorktreeName string
	PID          int
	Status       string
}
