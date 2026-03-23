package worktree

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"myworktree/internal/gitx"
)

type gitWT struct {
	Path   string
	Branch string // refs/heads/...
}

func listGitWorktrees(gitRoot string) ([]gitWT, error) {
	cmd := gitx.GitCommand(2*time.Second, gitRoot, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list failed: %w", err)
	}

	var res []gitWT
	var cur gitWT
	s := bufio.NewScanner(strings.NewReader(string(out)))
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			if cur.Path != "" {
				res = append(res, cur)
				cur = gitWT{}
			}
			cur.Path = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		}
	}
	if cur.Path != "" {
		res = append(res, cur)
	}
	return res, nil
}

func branchExists(gitRoot, branch string) bool {
	cmd := gitx.GitCommand(2*time.Second, gitRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}
