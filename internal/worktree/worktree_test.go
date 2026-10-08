package worktree

import (
	"strings"
	"testing"
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
