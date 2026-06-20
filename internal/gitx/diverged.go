package gitx

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const MainBranchKey = "mainBranch"

type DivergedStatus struct {
	Diverged bool   `json:"diverged"`
	Ahead    int    `json:"ahead,omitempty"`
	Error    string `json:"error,omitempty"`
}

type DivergedResult map[string]DivergedStatus

func CheckDiverged(gitRoot, worktreeBranch, mainBranch string) DivergedResult {
	result := make(DivergedResult)

	isMain := worktreeBranch == mainBranch
	isDevelop := worktreeBranch == "develop"

	if isMain {
		return result
	}

	if isDevelop {
		count, err := aheadCount(gitRoot, worktreeBranch, mainBranch)
		if err != nil {
			result[MainBranchKey] = DivergedStatus{Error: fmt.Sprintf("failed to check main: %v", err)}
		} else if count > 0 {
			result[MainBranchKey] = DivergedStatus{Diverged: true, Ahead: count}
		} else {
			result[MainBranchKey] = DivergedStatus{Diverged: false}
		}
		return result
	}

	if count, err := aheadCount(gitRoot, worktreeBranch, mainBranch); err != nil {
		result[MainBranchKey] = DivergedStatus{Error: fmt.Sprintf("failed to check main: %v", err)}
	} else if count > 0 {
		result[MainBranchKey] = DivergedStatus{Diverged: true, Ahead: count}
	} else {
		result[MainBranchKey] = DivergedStatus{Diverged: false}
	}

	if branchExists(gitRoot, "develop") || remoteHead(gitRoot, "origin", "develop") != "" {
		if count, err := aheadCount(gitRoot, worktreeBranch, "develop"); err != nil {
			result["develop"] = DivergedStatus{Error: fmt.Sprintf("failed to check develop: %v", err)}
		} else if count > 0 {
			result["develop"] = DivergedStatus{Diverged: true, Ahead: count}
		} else {
			result["develop"] = DivergedStatus{Diverged: false}
		}
	}

	return result
}

func aheadCount(gitRoot, branch, upstreamBranch string) (int, error) {
	effective, effErr := effectiveHead(gitRoot, upstreamBranch)
	if effErr != nil {
		return 0, effErr
	}
	if effective == "" {
		return 0, nil
	}

	localHead := branchHead(gitRoot, branch)
	if localHead == "" {
		return 0, nil
	}

	cmd := GitCommand(2*time.Second, gitRoot, "rev-list", "--count", localHead+".."+effective)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count failed: %w", err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse rev-list count: %w", err)
	}

	return count, nil
}

func effectiveHead(gitRoot, branch string) (string, error) {
	localHead := branchHead(gitRoot, branch)

	if localHead != "" {
		if remoteHeadVal := remoteHead(gitRoot, "origin", branch); remoteHeadVal != "" {
			localAhead, remoteAhead, lrErr := leftRightCount(gitRoot, localHead, remoteHeadVal)
			if lrErr != nil {
				return localHead, fmt.Errorf("comparing origin/%s: %w", branch, lrErr)
			}
			if remoteAhead > localAhead {
				return remoteHeadVal, nil
			}
			return localHead, nil
		}

		var maxRemoteAhead int
		var maxRemoteHead string
		for _, remote := range listRemotes(gitRoot) {
			if remote == "origin" {
				continue
			}
			if remoteHeadVal := remoteHead(gitRoot, remote, branch); remoteHeadVal != "" {
				localAhead, remoteAhead, lrErr := leftRightCount(gitRoot, localHead, remoteHeadVal)
				if lrErr != nil {
					log.Printf("diverged: effectiveHead(%q): leftRightCount(%s): %v", branch, remote, lrErr)
					continue
				}
				if remoteAhead > localAhead && remoteAhead >= maxRemoteAhead {
					maxRemoteAhead = remoteAhead
					maxRemoteHead = remoteHeadVal
				}
			}
		}
		if maxRemoteHead != "" {
			return maxRemoteHead, nil
		}

		return localHead, nil
	}

	if remoteHeadVal := remoteHead(gitRoot, "origin", branch); remoteHeadVal != "" {
		return remoteHeadVal, nil
	}
	for _, remote := range listRemotes(gitRoot) {
		if remote == "origin" {
			continue
		}
		if remoteHeadVal := remoteHead(gitRoot, remote, branch); remoteHeadVal != "" {
			return remoteHeadVal, nil
		}
	}
	return "", nil
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

func leftRightCount(gitRoot, left, right string) (leftAhead, rightAhead int, err error) {
	if left == "" || right == "" {
		return 0, 0, nil
	}
	cmd := GitCommand(2*time.Second, gitRoot, "rev-list", "--left-right", "--count", left+"..."+right)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("git rev-list --left-right: %w", err)
	}
	parts := strings.Fields(string(out))
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list --left-right output: %q", strings.TrimSpace(string(out)))
	}
	l, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse left count %q: %w", parts[0], err)
	}
	r, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse right count %q: %w", parts[1], err)
	}
	return l, r, nil
}
