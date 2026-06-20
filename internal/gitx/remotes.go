package gitx

import (
	"strings"
	"time"
)

// ListRemotes returns the names of all git remotes configured for the
// repository at gitRoot, with surrounding whitespace stripped and empty
// entries dropped. The returned slice is in the order `git remote` reports
// them — typically origin first, but callers should not rely on this for
// correctness; treat it as "best-effort priority" and explicitly prefer
// "origin" when needed.
//
// On any error (git not installed, timeout, non-git directory, etc.) the
// function returns nil so callers can use a plain `len(result) == 0` check
// to fall back to local-only logic. The 2-second timeout matches the rest
// of the gitx package's helper conventions.
//
// This is the single source of truth for "what remotes does this repo
// have?". Use it instead of inlining `git remote` parsing — the original
// duplicate copies of this logic in remote.go and diverged.go were
// consolidated here so future features only need to call ListRemotes.
func ListRemotes(gitRoot string) []string {
	cmd := GitCommand(2*time.Second, gitRoot, "remote")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		n := strings.TrimSpace(line)
		if n != "" {
			names = append(names, n)
		}
	}
	return names
}