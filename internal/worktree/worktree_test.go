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
