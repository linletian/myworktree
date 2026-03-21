package gitx

import "testing"

func TestCurrentBranch(t *testing.T) {
	// Must run inside a git repo. The test binary is invoked from the repo root
	// via "go test ./...", so "." resolves to the actual git repo.
	root, err := GitRoot(".")
	if err != nil {
		t.Skipf("not a git repo: %v", err)
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

	branch := DefaultBranch(root)
	if branch == "" {
		t.Fatalf("DefaultBranch returned empty string")
	}
}
