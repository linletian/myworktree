// Package monitor provides real-time resource stats collection (CPU%, memory RSS)
// for running instance processes.
package monitor

import "time"

// Stats is the top-level response returned by the stats collector.
//
// Issue #111 P2 added Alerts and System. Incremental compatibility rule for
// this endpoint: fields are only ever ADDED, never removed or re-semantics —
// existing consumers (the modal) keep working unchanged.
type Stats struct {
	Instances []InstanceStat `json:"instances"`
	Worktrees []WorktreeStat `json:"worktrees"`
	Global    GlobalStat     `json:"global"`
	// Alerts is the FRESH (computed-per-request) full alert set — system
	// signals AND per-instance subtree signals. The lightweight
	// /api/monitor/alerts endpoint serves the cached system-level subset;
	// here everything is live. Never nil (empty slice when all clear).
	Alerts []Alert `json:"alerts"`
	// System is this request's PSI / available-RAM sample.
	System SystemStat `json:"system"`
}

// InstanceStat describes the resource usage and connection status of a single instance.
type InstanceStat struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	WorktreeID           string  `json:"worktree_id"`
	WorktreeName         string  `json:"worktree_name"`
	PID                  int     `json:"pid"`
	Status               string  `json:"status"`
	CPUPercent           float64 `json:"cpu_percent"`
	MemoryRSSBytes       uint64  `json:"memory_rss_bytes"`
	MemoryBufferBytes    int64   `json:"memory_buffer_bytes"`
	MemoryBufferCapBytes int64   `json:"memory_buffer_cap_bytes"`
	ConnectionType       string  `json:"connection_type"`
	// Issue #111 P2 — subtree visibility (READ-ONLY aggregation, zero
	// intervention). ChildCount counts this instance's DESCENDANT processes
	// (main PID excluded); SubtreeRSSBytes is main PID + descendants RSS.
	// Children is the top-10 descendants by RSS (may be fewer; the
	// undisplayed tail is NOT truncation — it reaches the UI as "and M
	// more", child_count − len(children), and child_count stays the exact
	// census). ChildrenTruncated is true ONLY when the walk hit a
	// depth/count cap, where ChildCount is then a floor, not a census
	// (ARB-F1). For stopped instances these fields are all zero — "not
	// running", not "no children".
	// TreeUnavailable (review G3) marks "the tree could not be read" —
	// the /proc enumeration failed or the main PID was not in it. The
	// numbers on such a row are UNKNOWN, not measured zeros; the UI
	// renders "unavailable" instead of digits.
	ChildCount        int         `json:"child_count"`
	SubtreeRSSBytes   uint64      `json:"subtree_rss_bytes"`
	Children          []ChildProc `json:"children,omitempty"`
	ChildrenTruncated bool        `json:"children_truncated"`
	TreeUnavailable   bool        `json:"tree_unavailable"`
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
	TotalCPU          float64 `json:"total_cpu"`
	TotalMemory       uint64  `json:"total_memory"`
	InstanceCount     int     `json:"instance_count"`
	DaemonCPUPercent  float64 `json:"daemon_cpu_percent"`
	DaemonMemoryBytes uint64  `json:"daemon_memory_bytes"`
}

// cpuSnapshot stores per-PID CPU timing for delta-based CPU% calculation.
type cpuSnapshot struct {
	Times  time.Time
	User   float64
	System float64
}

// InputInstance is the minimal instance info needed for resource collection.
type InputInstance struct {
	ID              string
	Name            string
	WorktreeID      string
	WorktreeName    string
	PID             int
	Status          string
	BufferCapBytes  int64
	BufferUsedBytes int64
}
