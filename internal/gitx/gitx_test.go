package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCurrentBranch(t *testing.T) {
	// Must run inside a git repo. The test binary is invoked from the repo root
	// via "go test ./...", so "." resolves to the actual git repo.
	root, err := GitRoot(".")
	if err != nil {
		t.Skipf("not a git repo: %v", err)
	}

	// Detect detached HEAD (e.g., in CI shallow clones).
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) == "HEAD" {
		t.Skip("detached HEAD (common in CI shallow clones)")
	}

	branch, err := CurrentBranch(root)
	if err != nil {
		t.Fatalf("CurrentBranch returned error: %v", err)
	}
	if branch == "" {
		t.Fatalf("CurrentBranch returned empty string (detached HEAD?)")
	}
}

func TestDefaultBranch(t *testing.T) {
	root, err := GitRoot(".")
	if err != nil {
		t.Skipf("not a git repo: %v", err)
	}

	// Detect detached HEAD or shallow clone without remote tracking refs.
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) == "HEAD" {
		t.Skip("detached HEAD or shallow clone (common in CI)")
	}

	branch := DefaultBranch(root)
	if branch == "" {
		t.Fatalf("DefaultBranch returned empty string")
	}
}

func TestGitHubURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"scp style", "git@github.com:owner/repo.git", "https://github.com/owner/repo"},
		{"scp style no .git", "git@github.com:owner/repo", "https://github.com/owner/repo"},
		{"https", "https://github.com/owner/repo.git", "https://github.com/owner/repo"},
		{"https no .git", "https://github.com/owner/repo", "https://github.com/owner/repo"},
		{"https trailing slash", "https://github.com/owner/repo.git/", "https://github.com/owner/repo"},
		{"http", "http://github.com/owner/repo.git", "https://github.com/owner/repo"},
		{"ssh url", "ssh://git@github.com/owner/repo.git", "https://github.com/owner/repo"},
		{"ssh url no user", "ssh://github.com/owner/repo.git", "https://github.com/owner/repo"},
		{"uppercase host", "git@GITHUB.COM:owner/repo.git", "https://github.com/owner/repo"},
		{"dotted owner", "git@github.com:my.org/awesome.repo.git", "https://github.com/my.org/awesome.repo"},
		{"gitlab rejected", "git@gitlab.com:owner/repo.git", ""},
		{"bitbucket rejected", "https://bitbucket.org/owner/repo.git", ""},
		{"ghe rejected", "git@github.acme.com:owner/repo.git", ""},
		{"local path rejected", "/path/to/repo", ""},
		{"file scheme rejected", "file:///path/to/repo", ""},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"missing repo", "git@github.com:owner/", ""},
	}

	for _, c := range cases {
		t.Run("parse/"+c.name, func(t *testing.T) {
			got := normalizeGitHubURL(c.url)
			if got != c.want {
				t.Fatalf("normalizeGitHubURL(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}

	// End-to-end: GitHubURL against the real repo's origin. We compute the
	// expected value from the actual `git remote get-url origin` output so
	// the assertion survives owner / repo renames and forks.
	t.Run("e2e/real repo", func(t *testing.T) {
		root, err := GitRoot(".")
		if err != nil {
			t.Skipf("not a git repo: %v", err)
		}
		originCmd := exec.Command("git", "remote", "get-url", "origin")
		originCmd.Dir = root
		originOut, originErr := originCmd.Output()
		if originErr != nil {
			t.Skipf("origin not configured: %v", originErr)
		}
		origin := strings.TrimSpace(string(originOut))
		want := normalizeGitHubURL(origin)
		if want == "" {
			t.Skipf("origin %q does not point at github.com; skipping end-to-end check", origin)
		}
		if got := GitHubURL(root); got != want {
			t.Fatalf("GitHubURL(root) = %q, want %q", got, want)
		}
	})

	// End-to-end: a freshly-initialized repo with no remotes yields "".
	t.Run("e2e/no remotes", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q")
		if got := GitHubURL(dir); got != "" {
			t.Fatalf("GitHubURL(no-remote repo) = %q, want empty", got)
		}
	})

	// End-to-end: a repo whose only remote is GitLab yields "".
	t.Run("e2e/gitlab only", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q")
		runGit(t, dir, "remote", "add", "origin", "git@gitlab.com:foo/bar.git")
		if got := GitHubURL(dir); got != "" {
			t.Fatalf("GitHubURL(gitlab origin) = %q, want empty", got)
		}
	})

	// End-to-end: origin is non-GitHub but a non-origin remote is GitHub —
	// the fallback iterates `git remote` and finds the GitHub URL.
	t.Run("e2e/origin rejected, upstream accepted", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q")
		runGit(t, dir, "remote", "add", "origin", "https://gitlab.com/foo/bar.git")
		runGit(t, dir, "remote", "add", "upstream", "git@github.com:foo/bar.git")
		if got := GitHubURL(dir); got != "https://github.com/foo/bar" {
			t.Fatalf("GitHubURL(fallback) = %q, want %q", got, "https://github.com/foo/bar")
		}
	})

	// End-to-end: HTTPS GitHub remote with no .git suffix.
	t.Run("e2e/https no dotgit", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q")
		runGit(t, dir, "remote", "add", "origin", "https://github.com/hello/world")
		if got := GitHubURL(dir); got != "https://github.com/hello/world" {
			t.Fatalf("GitHubURL = %q, want %q", got, "https://github.com/hello/world")
		}
	})
}

// runGit is a test-only helper that runs a git command inside dir with a
// short context-derived timeout. Used by TestGitHubURL e2e subtests to set
// up throwaway remotes without depending on gitx internals.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func TestGitCommandTimeoutStopsHungGit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	err := GitCommand(100*time.Millisecond, ".", "status").Run()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected git command timeout")
	}
	if elapsed > time.Second {
		t.Fatalf("git command exceeded timeout window: %v", elapsed)
	}
}
