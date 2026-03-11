package mcp

import "testing"

func TestAdapterToolNames(t *testing.T) {
	a := Adapter{}
	got := a.ToolNames()
	want := []string{
		"worktree_list",
		"worktree_create",
		"worktree_delete",
		"branch_list",
		"tag_list",
		"instance_list",
		"instance_start",
		"instance_stop",
		"instance_input",
		"instance_archive",
		"instance_delete",
		"instance_log_tail",
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected tool count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected tool at index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
