package worktree

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"myworktree/internal/store"
)

func TestParseBranchSpec(t *testing.T) {
	group, name, ok := parseBranchSpec("feature/auth-login")
	if !ok || group != "feature" || name != "auth-login" {
		t.Fatalf("valid branch spec parse failed: ok=%v group=%q name=%q", ok, group, name)
	}

	cases := []string{
		"",
		"feature",
		"feature/auth/login",
		"feature /auth",
		"feature/ auth",
		"-feature/auth",
	}
	for _, c := range cases {
		if _, _, ok := parseBranchSpec(c); ok {
			t.Fatalf("invalid branch spec %q should be rejected", c)
		}
	}
}

func TestSlugify(t *testing.T) {
	got := slugify("Fix login 401 & add tests!")
	want := "fix-login-401-add-tests"
	if got != want {
		t.Fatalf("unexpected slugify result: got %q, want %q", got, want)
	}

	if got := slugify("你好，世界"); got != "" {
		t.Fatalf("non-ascii-only input should result empty slug, got %q", got)
	}

	long := strings.Repeat("a", 60)
	got = slugify(long)
	if len(got) != 48 {
		t.Fatalf("slug should be truncated to 48 chars, got len=%d", len(got))
	}
}

// TestDirtyWorktreeErrorSummarizePorcelain pins the classification of the
// issue #102 refusal summary: every porcelain line lands in EXACTLY ONE
// bucket (priority delete > rename > add > modify), so the buckets sum to
// Entries; "!!" lines never appear here in production (the blocking check
// runs without --ignored) and are skipped defensively.
func TestDirtyWorktreeErrorSummarizePorcelain(t *testing.T) {
	out := strings.Join([]string{
		" D firmware/CMakeLists.txt", // worktree delete
		"D  tools/gone.txt",          // staged delete
		" M docs/changed.md",         // worktree modify
		"M  staged.txt",              // staged modify
		"MM both.txt",                // both columns — still ONE bucket
		"T  typechange.txt",          // typechange counts as modify
		"A  added.txt",               // staged add
		"R  old.txt -> new.txt",      // rename (path field kept raw)
		"C  src.txt -> copy.txt",     // copy counts as rename
		"?? scratch.log",             // untracked
		"!! debug.log",               // defensive: ignored lines are skipped
	}, "\n")

	d := &DirtyWorktreeError{}
	d.summarizePorcelain(out)

	if d.Entries != 10 {
		t.Fatalf("Entries = %d, want 10 (the !! line is skipped)", d.Entries)
	}
	if d.Deleted != 2 || d.Modified != 4 || d.Added != 1 || d.Renamed != 2 || d.Untracked != 1 {
		t.Fatalf("buckets = %dD/%dM/%dA/%dR/%dU, want 2D/4M/1A/2R/1U — and they must sum to Entries",
			d.Deleted, d.Modified, d.Added, d.Renamed, d.Untracked)
	}
	if got := d.Deleted + d.Modified + d.Added + d.Renamed + d.Untracked; got != d.Entries {
		t.Fatalf("buckets sum to %d, want Entries=%d", got, d.Entries)
	}
	if len(d.FirstPaths) != maxDirtyFirstPaths {
		t.Fatalf("FirstPaths = %d entries, want the %d-entry cap", len(d.FirstPaths), maxDirtyFirstPaths)
	}
	if d.FirstPaths[0] != "firmware/CMakeLists.txt" {
		t.Fatalf("FirstPaths[0] = %q, want the first path field without its status prefix", d.FirstPaths[0])
	}
}

// TestCountIgnoredPorcelain pins the "!!" count used for the gitignored
// risk warning in the refusal message (issue #102 P3).
func TestCountIgnoredPorcelain(t *testing.T) {
	out := " M tracked.txt\n?? scratch.txt\n!! build/\n!! debug.log\n!! nv_backup.bin\n"
	if n := countIgnoredPorcelain(out); n != 3 {
		t.Fatalf("countIgnoredPorcelain = %d, want 3", n)
	}
	if n := countIgnoredPorcelain(""); n != 0 {
		t.Fatalf("countIgnoredPorcelain(\"\") = %d, want 0", n)
	}
}

// TestDirtyWorktreeErrorMessage pins the refusal wording shape (issue #102
// P1/P3): counts by category, first paths, the gitignored-file warning,
// and the force exit — the flat "delete is refused" gave none of these.
func TestDirtyWorktreeErrorMessage(t *testing.T) {
	d := &DirtyWorktreeError{
		Entries: 133, Deleted: 133,
		FirstPaths: []string{"firmware/CMakeLists.txt", "firmware/main.c"},
		Ignored:    89,
	}
	msg := d.Error()
	for _, want := range []string{
		"133 entries", "(133 deleted, 0 modified, 0 untracked)",
		"first paths: firmware/CMakeLists.txt, firmware/main.c",
		"89 gitignored entries", "force delete would destroy unrecoverably",
		"retry with force",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal message missing %q:\n%s", want, msg)
		}
	}

	// Singular forms and the optional buckets.
	d = &DirtyWorktreeError{Entries: 1, Modified: 1, Added: 2, Renamed: 1, Ignored: 1}
	msg = d.Error()
	for _, want := range []string{"1 entry", ", 2 added", ", 1 renamed", "1 gitignored entry present"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal message missing %q:\n%s", want, msg)
		}
	}
}

func TestCapDirtyPorcelain(t *testing.T) {
	make := func(n int) string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = "?? f"
		}
		return strings.Join(lines, "\n")
	}

	// At or under the cap the output is carried verbatim.
	if got := capDirtyPorcelain(make(maxDirtyPorcelainLines)); got != make(maxDirtyPorcelainLines) {
		t.Fatalf("output at the cap must pass through unchanged, got %d lines", strings.Count(got, "\n")+1)
	}
	// One line over: singular "line", and the carried body stays capped.
	got := capDirtyPorcelain(make(maxDirtyPorcelainLines + 1))
	if !strings.HasSuffix(got, "… (1 more line truncated)") {
		t.Fatalf("1-over truncation marker should be singular, got suffix %q", got[len(got)-40:])
	}
	if strings.Count(got, "?? f") != maxDirtyPorcelainLines {
		t.Fatalf("carried output must be capped at %d lines", maxDirtyPorcelainLines)
	}
	// Two over: plural.
	if got := capDirtyPorcelain(make(maxDirtyPorcelainLines + 2)); !strings.HasSuffix(got, "… (2 more lines truncated)") {
		t.Fatalf("2-over truncation marker should be plural, got suffix %q", got[len(got)-40:])
	}
}

// newRenameTestStore seeds a temp state file with one managed worktree so
// UpdateName's store behaviour can be pinned without a git repo (issue #122).
func newRenameTestStore(t *testing.T) (Manager, store.FileStore) {
	t.Helper()
	fs := store.FileStore{Path: filepath.Join(t.TempDir(), "state.json")}
	seed := store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "old", Path: "/tmp/x", Branch: "b", BaseRef: "main", CreatedAt: "2020-01-01T00:00:00Z"},
		},
	}
	if err := fs.Save(seed); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	return Manager{Store: fs}, fs
}

// TestUpdateNameTrimsAndPersists pins the happy path (issue #122): the
// incoming name is trimmed, only Name changes, and the update survives a
// Load round-trip.
func TestUpdateNameTrimsAndPersists(t *testing.T) {
	m, fs := newRenameTestStore(t)

	updated, err := m.UpdateName("wt1", "  new name  ")
	if err != nil {
		t.Fatalf("UpdateName failed: %v", err)
	}
	if updated.Name != "new name" {
		t.Fatalf("returned Name = %q, want the trimmed %q", updated.Name, "new name")
	}
	// Only the display name moves; every git-facing field is untouched.
	if updated.ID != "wt1" || updated.Path != "/tmp/x" || updated.Branch != "b" || updated.BaseRef != "main" || updated.CreatedAt != "2020-01-01T00:00:00Z" {
		t.Fatalf("non-name fields changed: %#v", updated)
	}

	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Worktrees) != 1 {
		t.Fatalf("worktree count = %d, want 1", len(st.Worktrees))
	}
	persisted := st.Worktrees[0]
	if persisted.Name != "new name" || persisted.ID != "wt1" || persisted.Path != "/tmp/x" || persisted.Branch != "b" || persisted.BaseRef != "main" || persisted.CreatedAt != "2020-01-01T00:00:00Z" {
		t.Fatalf("persisted record drifted from the rename: %#v", persisted)
	}
}

// TestUpdateNameBumpsVersion pins the persistence path (issue #122 review
// A-F1): a rename must go through SaveWithVersion, which increments Version.
// A plain Save (what this package's Delete does) leaves Version flat, so a
// flat version would mean the cross-record lost-write window was re-opened.
// Two renames in a row also prove the loop reloads the bumped version.
func TestUpdateNameBumpsVersion(t *testing.T) {
	m, fs := newRenameTestStore(t)

	before, err := fs.Load()
	if err != nil {
		t.Fatalf("load before: %v", err)
	}
	if _, err := m.UpdateName("wt1", "first"); err != nil {
		t.Fatalf("first UpdateName failed: %v", err)
	}
	after, err := fs.Load()
	if err != nil {
		t.Fatalf("load after: %v", err)
	}
	if after.Version != before.Version+1 {
		t.Fatalf("Version = %d, want %d — a plain Save would leave it flat", after.Version, before.Version+1)
	}

	// A second rename must succeed off the reloaded (bumped) version.
	if _, err := m.UpdateName("wt1", "second"); err != nil {
		t.Fatalf("second UpdateName failed: %v", err)
	}
	after2, err := fs.Load()
	if err != nil {
		t.Fatalf("load after second: %v", err)
	}
	if after2.Version != after.Version+1 || after2.Worktrees[0].Name != "second" {
		t.Fatalf("second rename state = version %d name %q, want version %d name %q",
			after2.Version, after2.Worktrees[0].Name, after.Version+1, "second")
	}
}

// TestUpdateNameRejectsEmptyAndUnknown pins the input validation and the
// not-found sentinel (issue #122): blank id/name are rejected before any
// write — with the EXACT wording decision B copied verbatim from
// framework/methods.go, so deleting a validation branch cannot silently
// regress the API contract (a dropped id check would fall through to the
// not-found path — still an error, but 404 instead of the contractual 400).
// An unknown id wraps ErrWorktreeNotFound so errors.Is can see it.
func TestUpdateNameRejectsEmptyAndUnknown(t *testing.T) {
	m, fs := newRenameTestStore(t)

	if _, err := m.UpdateName("wt1", "   "); err == nil || err.Error() != "name cannot be empty" {
		t.Fatalf("whitespace-only name: err = %v, want exactly %q", err, "name cannot be empty")
	}
	if _, err := m.UpdateName("   ", "valid"); err == nil || err.Error() != "id is required" {
		t.Fatalf("whitespace-only id: err = %v, want exactly %q", err, "id is required")
	}

	_, err := m.UpdateName("does-not-exist", "x")
	if !errors.Is(err, ErrWorktreeNotFound) {
		t.Fatalf("unknown id err = %v, want it to wrap ErrWorktreeNotFound", err)
	}

	// None of the rejected calls touched the store.
	st, loadErr := fs.Load()
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if len(st.Worktrees) != 1 || st.Worktrees[0].Name != "old" {
		t.Fatalf("rejected renames must not mutate the store, got %#v", st.Worktrees)
	}
}
