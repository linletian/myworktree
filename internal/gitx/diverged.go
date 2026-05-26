package gitx

import (
	"strconv"
	"strings"
	"time"
)

type DivergedStatus struct {
	Diverged bool `json:"diverged"`
	Ahead    int  `json:"ahead,omitempty"`
}

type DivergedResult map[string]DivergedStatus

func CheckDiverged(gitRoot, worktreeBranch, mainBranch string) DivergedResult {
	result := make(DivergedResult)

	isMain := worktreeBranch == mainBranch
	isDevelop := worktreeBranch == "develop"
	developExists := branchExists(gitRoot, "develop")

	if isMain {
		return result
	}

	if isDevelop {
		if count := aheadCount(gitRoot, worktreeBranch, mainBranch); count > 0 {
			result[mainBranch] = DivergedStatus{Diverged: true, Ahead: count}
		} else {
			result[mainBranch] = DivergedStatus{Diverged: false}
		}
		return result
	}

	if count := aheadCount(gitRoot, worktreeBranch, mainBranch); count > 0 {
		result[mainBranch] = DivergedStatus{Diverged: true, Ahead: count}
	} else {
		result[mainBranch] = DivergedStatus{Diverged: false}
	}

	if developExists {
		if count := aheadCount(gitRoot, worktreeBranch, "develop"); count > 0 {
			result["develop"] = DivergedStatus{Diverged: true, Ahead: count}
		} else {
			result["develop"] = DivergedStatus{Diverged: false}
		}
	}

	return result
}

func aheadCount(gitRoot, branch, upstreamBranch string) int {
	effectiveHead := effectiveHead(gitRoot, upstreamBranch)
	if effectiveHead == "" {
		return 0
	}

	localHead := branchHead(gitRoot, branch)
	if localHead == "" {
		return 0
	}

	cmd := GitCommand(2*time.Second, gitRoot, "rev-list", "--count", localHead+".."+effectiveHead)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}

	return count
}

func effectiveHead(gitRoot, branch string) string {
	localHead := branchHead(gitRoot, branch)
	if localHead == "" {
		return ""
	}

	if remoteHead := remoteHead(gitRoot, "origin", branch); remoteHead != "" {
		localAhead, remoteAhead := leftRightCount(gitRoot, localHead, remoteHead)
		if remoteAhead > localAhead {
			return remoteHead
		}
	}

	var maxRemoteAhead int
	var maxRemoteHead string
	for _, remote := range listRemotes(gitRoot) {
		if remote == "origin" {
			continue
		}
		if remoteHead := remoteHead(gitRoot, remote, branch); remoteHead != "" {
			_, remoteAhead := leftRightCount(gitRoot, localHead, remoteHead)
			if remoteAhead >= maxRemoteAhead {
				maxRemoteAhead = remoteAhead
				maxRemoteHead = remoteHead
			}
		}
	}
	if maxRemoteHead != "" && maxRemoteAhead > 0 {
		return maxRemoteHead
	}

	return localHead
}

func branchHead(gitRoot, branch string) string {
	if branch == "" {
		return ""
	}
	cmd := GitCommand(2*time.Second, gitRoot, "rev-parse", branch)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func remoteHead(gitRoot, remote, branch string) string {
	ref := remote + "/" + branch
	cmd := GitCommand(2*time.Second, gitRoot, "rev-parse", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func leftRightCount(gitRoot, left, right string) (leftAhead, rightAhead int) {
	if left == "" || right == "" {
		return 0, 0
	}
	cmd := GitCommand(2*time.Second, gitRoot, "rev-list", "--left-right", "--count", left+"..."+right)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0
	}
	parts := strings.Fields(string(out))
	if len(parts) != 2 {
		return 0, 0
	}
	l, _ := strconv.Atoi(parts[0])
	r, _ := strconv.Atoi(parts[1])
	return l, r
}

func listRemotes(gitRoot string) []string {
	cmd := GitCommand(2*time.Second, gitRoot, "remote")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var remotes []string
	for _, r := range strings.Fields(string(out)) {
		if r != "" {
			remotes = append(remotes, r)
		}
	}
	return remotes
}