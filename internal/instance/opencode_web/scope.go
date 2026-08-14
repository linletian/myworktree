package opencode_web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scope classifies a request's directory relative to the instance
// worktree. Values match the WORKTREE-ISOLATION.md §4.2 table.
type Scope string

const (
	// ScopeInScope means the request targets the instance worktree
	// itself. A request with no directory also resolves here (fallback
	// = server cwd = worktree), but such requests are never recorded so
	// they do not clobber a previous out-of-scope observation.
	ScopeInScope Scope = "in-scope"
	// ScopeOutOfScope means the request targets a different directory:
	// a sibling worktree, an arbitrary path, or a subdirectory of the
	// worktree (sandbox/workspace).
	ScopeOutOfScope Scope = "out-of-scope"
	// ScopeCrossProject marks a scope=project query — the one form that
	// makes opencode ignore directory filtering entirely (defensive).
	ScopeCrossProject Scope = "cross-project"
)

// ScopeState is the last observed scope for one instance. Kept in
// memory only (not persisted): the tracker records the most recent
// directory-bearing request, so the warning stays visible until the
// user navigates back to the worktree.
type ScopeState struct {
	Scope        Scope  `json:"scope"`
	Directory    string `json:"directory,omitempty"`
	CrossProject bool   `json:"cross_project,omitempty"`
	At           int64  `json:"at,omitempty"` // unix seconds, for observability
	// CSPAnchorMissing marks structural drift: the homepage CSP no longer
	// carries the 'wasm-unsafe-eval' anchor after which the injected script's
	// hash is appended, so the injected script may be blocked by CSP. Surfaced
	// to the frontend so hiding/hide-effectiveness never fails silently.
	CSPAnchorMissing bool `json:"csp_anchor_missing,omitempty"`
}

// parseDirectory extracts the directory a request targets. The v2 SDK
// sends it in the x-opencode-directory header (encodeURIComponent
// encoded); GET/HEAD additionally set it as the ?directory= query
// parameter. Header wins; query is the fallback. ok is false when the
// request carries no directory at all.
func parseDirectory(r *http.Request) (dir string, ok bool) {
	if h := r.Header.Get("x-opencode-directory"); h != "" {
		if d, err := url.PathUnescape(h); err == nil {
			return d, true
		}
		return h, true
	}
	if q := r.URL.Query().Get("directory"); q != "" {
		return q, true // url.Values.Get already percent-decodes
	}
	return "", false
}

// normalizeDir canonicalizes a directory path for comparison: clean,
// resolve symlinks (best-effort), strip the trailing separator. Both
// sides (instance worktree vs request directory) must use this so
// symlink / .. / case / trailing-slash differences do not false-positive.
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

// classify compares a request directory against the instance worktree.
// crossProject reports a scope=project query (the one form that makes
// opencode ignore directory filtering). An empty directory on either
// side resolves to in-scope (fallback = server cwd = worktree).
func classify(worktree, dir string, crossProject bool) Scope {
	if crossProject {
		return ScopeCrossProject
	}
	nw := normalizeDir(worktree)
	nd := normalizeDir(dir)
	if nw == "" || nd == "" {
		return ScopeInScope
	}
	if nd == nw {
		return ScopeInScope
	}
	return ScopeOutOfScope
}

// ScopeTracker is the in-memory, per-instance scope state store. The
// proxy records every directory-bearing request; the frontend polls the
// query endpoint to render the persistent warning bar.
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

// SetCSPAnchorMissing updates only the CSP-drift flag for an instance,
// preserving the last observed scope/directory.
func (t *ScopeTracker) SetCSPAnchorMissing(id string, missing bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.states == nil {
		t.states = map[string]ScopeState{}
	}
	st := t.states[id]
	st.CSPAnchorMissing = missing
	if st.Scope == "" {
		st.Scope = ScopeInScope
	}
	t.states[id] = st
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
