package gitx

import (
	"os/exec"
	"strings"
	"testing"
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
