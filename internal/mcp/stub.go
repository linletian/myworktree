package mcp

import (
	"myworktree/internal/instance"
	"myworktree/internal/worktree"
)

// Adapter is a thin compatibility layer for future MCP tool exposure.
// It keeps the core managers decoupled from transport concerns.
type Adapter struct {
	Worktrees worktree.Manager
	Instances *instance.Manager
}

func (a Adapter) ToolNames() []string {
	return []string{
		"worktree_list",
		"worktree_create",
		"worktree_delete",
		"instance_list",
		"instance_start",
		"instance_stop",
		"instance_log_tail",
	}
}
