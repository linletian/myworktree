package opencode_web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNormalizeDir(t *testing.T) {
	dir := t.TempDir()
	// Canonicalize the base the same way normalizeDir does: on macOS
	// os.TempDir() lives under /var, which is a symlink to /private/var,
	// so EvalSymlinks rewrites the prefix and a raw comparison fails.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"trailing slash", dir + "/", dir},
		{"dot segments", filepath.Join(dir, "a", ".."), dir},
		{"plain", sub, sub},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDir(tt.in); got != tt.want {
				t.Fatalf("normalizeDir(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeDirSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if got := normalizeDir(link); got != normalizeDir(real) {
		t.Fatalf("normalizeDir(symlink) = %q, want %q", got, normalizeDir(real))
	}
}

func TestParseDirectory(t *testing.T) {
	// Header wins, percent-decoded.
	r := httptest.NewRequest(http.MethodPost, "/session", nil)
	r.Header.Set("x-opencode-directory", "/home%2Fuser%2Frepo")
	dir, ok := parseDirectory(r)
	if !ok || dir != "/home/user/repo" {
		t.Fatalf("header parse = (%q, %v), want (/home/user/repo, true)", dir, ok)
	}

	// Query fallback (already decoded by net/url).
	r = httptest.NewRequest(http.MethodGet, "/session?directory=%2Ftmp%2Fother", nil)
	dir, ok = parseDirectory(r)
	if !ok || dir != "/tmp/other" {
		t.Fatalf("query parse = (%q, %v), want (/tmp/other, true)", dir, ok)
	}

	// Header takes precedence over query.
	r = httptest.NewRequest(http.MethodGet, "/session?directory=%2Ftmp%2Fquery", nil)
	r.Header.Set("x-opencode-directory", "/tmp/header")
	dir, _ = parseDirectory(r)
	if dir != "/tmp/header" {
		t.Fatalf("precedence = %q, want /tmp/header", dir)
	}

	// No directory.
	r = httptest.NewRequest(http.MethodGet, "/session", nil)
	if _, ok = parseDirectory(r); ok {
		t.Fatal("expected ok=false for request without directory")
	}
}

func TestClassify(t *testing.T) {
	wt := t.TempDir()
	sub := filepath.Join(wt, "sandbox")
	other := t.TempDir()

	tests := []struct {
		name         string
		worktree     string
		dir          string
		crossProject bool
		want         Scope
	}{
		{"equal worktree", wt, wt, false, ScopeInScope},
		{"worktree trailing slash", wt, wt + "/", false, ScopeInScope},
		{"subdirectory out of scope", wt, sub, false, ScopeOutOfScope},
		{"other directory out of scope", wt, other, false, ScopeOutOfScope},
		{"empty directory in scope", wt, "", false, ScopeInScope},
		{"empty worktree in scope", "", wt, false, ScopeInScope},
		{"cross project", wt, other, true, ScopeCrossProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.worktree, tt.dir, tt.crossProject); got != tt.want {
				t.Fatalf("classify(%q, %q, %v) = %q, want %q",
					tt.worktree, tt.dir, tt.crossProject, got, tt.want)
			}
		})
	}
}

func TestScopeTracker(t *testing.T) {
	tr := NewScopeTracker()

	if _, ok := tr.Get("nope"); ok {
		t.Fatal("Get on empty tracker should return ok=false")
	}

	tr.Record("id1", ScopeState{Scope: ScopeOutOfScope, Directory: "/tmp/x"})
	st, ok := tr.Get("id1")
	if !ok || st.Scope != ScopeOutOfScope || st.Directory != "/tmp/x" {
		t.Fatalf("Get(id1) = (%+v, %v)", st, ok)
	}
	if st.At == 0 {
		t.Fatal("expected At to be populated")
	}

	// Concurrent writes/reads must not race.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tr.Record("id2", ScopeState{Scope: ScopeInScope})
			tr.Get("id2")
		}(i)
	}
	wg.Wait()

	tr.Reset("id1")
	if _, ok := tr.Get("id1"); ok {
		t.Fatal("Get after Reset should return ok=false")
	}

	// nil tracker is safe.
	var nilT *ScopeTracker
	nilT.Record("x", ScopeState{})
	nilT.Reset("x")
}
