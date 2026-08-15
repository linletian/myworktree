package dsh_web

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scope classification for dsh-web instances (PLAN.md §工作区限制).
// Unlike opencode-web — which observes the request directory carried in
// headers / query params — dsh carries the target directory only in RPC
// request BODIES (workspace.create {path} / session.create {cwd |
// workspaceId}), so the per-instance reverse proxy parses those bodies
// and records the result here. Observation is record-only: requests are
// forwarded unchanged, out-of-scope sessions still succeed (their
// sandbox root is the out-of-scope directory; OS-level write limits
// still apply), and the warning stays until the user navigates back.

type Scope string

const (
	// ScopeInScope means the request targets the instance worktree
	// itself.
	ScopeInScope Scope = "in-scope"
	// ScopeOutOfScope means the request targets a different directory
	// or workspace: a sibling worktree or an arbitrary path.
	ScopeOutOfScope Scope = "out-of-scope"
)

// ScopeState is the last observed scope for one instance. Kept in
// memory only (not persisted): the tracker records the most recent
// directory-bearing RPC, so the warning stays visible until the user
// navigates back to the worktree.
type ScopeState struct {
	Scope     Scope  `json:"scope"`
	Directory string `json:"directory,omitempty"` // cwd path or workspaceId
	At        int64  `json:"at,omitempty"`        // unix seconds, for observability
}

// ScopeTracker is the in-memory, per-instance scope state store. The
// proxy records every workspace/session RPC body; the frontend polls
// the query endpoint to render the persistent warning bar.
type ScopeTracker struct {
	mu     sync.RWMutex
	states map[string]ScopeState
}

// NewScopeTracker returns an empty tracker.
func NewScopeTracker() *ScopeTracker {
	return &ScopeTracker{states: map[string]ScopeState{}}
}

// Record stores the latest observed scope for an instance.
func (t *ScopeTracker) Record(id string, st ScopeState) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.states == nil {
		t.states = map[string]ScopeState{}
	}
	st.At = time.Now().Unix()
	t.states[id] = st
}

// Get returns the recorded state and whether it exists.
func (t *ScopeTracker) Get(id string) (ScopeState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.states[id]
	return st, ok
}

// Reset clears the state for an instance (used on instance stop).
func (t *ScopeTracker) Reset(id string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, id)
}

// normalizeDir canonicalizes a directory path for comparison: clean,
// resolve symlinks (best-effort), strip the trailing separator. Both
// sides (instance worktree vs request directory) must use this so
// symlink / .. / case / trailing-slash differences do not
// false-positive.
func normalizeDir(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	dir = strings.TrimRight(dir, string(filepath.Separator))
	if dir == "" {
		return string(filepath.Separator)
	}
	return dir
}

// classifyRPC compares one RPC target against the instance worktree.
// dir is a cwd path (workspace.create path / session.create cwd);
// workspaceID is the session.create workspaceId form. Exactly one is
// non-empty. ok=false when nothing comparable was supplied (no
// observation recorded). A workspaceId observation with no known
// worktree workspace id (bootstrap not done / failed) is skipped — it
// would otherwise false-positive on the worktree's own workspace.
func classifyRPC(worktree, worktreeWorkspaceID, dir, workspaceID string) (ScopeState, bool) {
	switch {
	case dir != "":
		if normalizeDir(dir) != normalizeDir(worktree) {
			return ScopeState{Scope: ScopeOutOfScope, Directory: dir}, true
		}
		return ScopeState{Scope: ScopeInScope, Directory: dir}, true
	case workspaceID != "":
		if worktreeWorkspaceID == "" {
			return ScopeState{}, false
		}
		if workspaceID != worktreeWorkspaceID {
			return ScopeState{Scope: ScopeOutOfScope, Directory: workspaceID}, true
		}
		return ScopeState{Scope: ScopeInScope, Directory: workspaceID}, true
	}
	return ScopeState{}, false
}
