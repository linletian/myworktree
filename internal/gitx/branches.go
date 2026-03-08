package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Branch struct {
	Name       string `json:"name"`
	CommitUnix int64  `json:"commit_unix"`
}

func DefaultBranch(gitRoot string) string {
	// 1) origin/HEAD symbolic ref
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	cmd.Dir = gitRoot
	if out, err := cmd.Output(); err == nil {
		ref := strings.TrimSpace(string(out)) // refs/remotes/origin/main
		ref = strings.TrimPrefix(ref, "refs/remotes/origin/")
		if ref != "" {
			return ref
		}
	}

	// 2) parse `git remote show origin`
	cmd = exec.Command("git", "remote", "show", "origin")
	cmd.Dir = gitRoot
	if out, err := cmd.Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "HEAD branch:") {
				b := strings.TrimSpace(strings.TrimPrefix(line, "HEAD branch:"))
				if b != "" {
					return b
				}
			}
		}
	}

	// 3) common fallbacks
	if branchExists(gitRoot, "main") {
		return "main"
	}
	if branchExists(gitRoot, "master") {
		return "master"
	}

	// 4) current branch
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = gitRoot
	if out, err := cmd.Output(); err == nil {
		b := strings.TrimSpace(string(out))
		if b != "" && b != "HEAD" {
			return b
		}
	}
	return ""
}

func ListLocalBranchesByCommitTime(gitRoot string, limit int) ([]Branch, error) {
	cmd := exec.Command("git", "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)\t%(committerdate:unix)", "refs/heads/")
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(out, []byte("\n"))
	items := make([]Branch, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		parts := strings.SplitN(string(line), "\t", 2)
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		var ts int64
		if len(parts) == 2 {
			ts, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		}
		items = append(items, Branch{Name: name, CommitUnix: ts})
		if limit > 0 && len(items) >= limit {
			// This is already sorted by git; early stop is safe.
			break
		}
	}
	return items, nil
}

func branchExists(gitRoot, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", name))
	cmd.Dir = gitRoot
	return cmd.Run() == nil
}
