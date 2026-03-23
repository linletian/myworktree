package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Branch struct {
	Name       string `json:"name"`
	CommitUnix int64  `json:"commit_unix"`
}

type Cmd struct {
	*exec.Cmd
	cancel context.CancelFunc
}

func (c *Cmd) release() {
	if c != nil && c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

func (c *Cmd) Run() error {
	defer c.release()
	return c.Cmd.Run()
}

func (c *Cmd) Output() ([]byte, error) {
	defer c.release()
	return c.Cmd.Output()
}

func (c *Cmd) CombinedOutput() ([]byte, error) {
	defer c.release()
	return c.Cmd.CombinedOutput()
}

func (c *Cmd) Wait() error {
	defer c.release()
	return c.Cmd.Wait()
}

// GitCommand creates a git command with a real execution timeout.
// WaitDelay only limits shutdown after cancellation; it does not stop a hung git
// process on its own, so we use CommandContext here.
func GitCommand(timeout time.Duration, gitRoot string, args ...string) *Cmd {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = gitRoot
	return &Cmd{Cmd: cmd, cancel: cancel}
}

func DefaultBranch(gitRoot string) string {
	// 1) origin/HEAD symbolic ref (fastest, local metadata)
	cmd := GitCommand(2*time.Second, gitRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if out, err := cmd.Output(); err == nil {
		// "origin/main" -> "main"
		ref := strings.TrimSpace(string(out))
		ref = strings.TrimPrefix(ref, "origin/")
		if ref != "" {
			return ref
		}
	}

	// 2) git rev-parse --abbrev-ref origin/HEAD (fallback for local symbolic ref)
	cmd = GitCommand(2*time.Second, gitRoot, "rev-parse", "--abbrev-ref", "origin/HEAD")
	if out, err := cmd.Output(); err == nil {
		ref := strings.TrimSpace(string(out))
		ref = strings.TrimPrefix(ref, "origin/")
		if ref != "" && ref != "HEAD" {
			return ref
		}
	}

	// 3) Slow network check removed (git remote show origin) - causes 3-5s blocking delay!

	// 4) Common names
	if branchExists(gitRoot, "main") {
		return "main"
	}
	if branchExists(gitRoot, "master") {
		return "master"
	}

	// 5) current branch
	cmd = GitCommand(2*time.Second, gitRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if out, err := cmd.Output(); err == nil {
		b := strings.TrimSpace(string(out))
		if b != "" && b != "HEAD" {
			return b
		}
	}
	return ""
}

// CurrentBranch returns the currently checked-out branch (e.g., "feature/ui-update").
// Unlike DefaultBranch, it never falls back to origin/HEAD or common names.
// Returns an error if the directory is not a git repo or HEAD is malformed.
func CurrentBranch(gitRoot string) (string, error) {
	cmd := GitCommand(2*time.Second, gitRoot, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w", err)
	}
	b := strings.TrimSpace(string(out))
	if b != "" && b != "HEAD" {
		return b, nil
	}
	return "", errors.New("git HEAD is detached or malformed")
}

func ListLocalBranchesByCommitTime(gitRoot string, limit int) ([]Branch, error) {
	cmd := GitCommand(5*time.Second, gitRoot, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)\t%(committerdate:unix)", "refs/heads/")
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
	cmd := GitCommand(2*time.Second, gitRoot, "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", name))
	return cmd.Run() == nil
}
