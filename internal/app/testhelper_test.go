package app

import "myworktree/internal/store"

// managedTestInstance is the shared per-kind test-instance helper
// (REVIEW-2026-08-15.md #12): a ManagedInstance stub with the fixed
// wt1 worktree and Name=ID, parameterized by kind. The dsh tests were
// the first consumer; future web-ui kinds reuse it instead of
// copy-pasting a kind-specific helper.
func managedTestInstance(id, kind, cwd, status string, blob []byte) store.ManagedInstance {
	return store.ManagedInstance{
		ID:         id,
		WorktreeID: "wt1",
		Name:       id,
		Kind:       kind,
		Cwd:        cwd,
		Status:     status,
		KindBlob:   blob,
	}
}
