package gitx

import (
	"regexp"
	"strings"
	"time"
)

// GitHubURL inspects the git remotes of gitRoot and returns the canonical
// GitHub.com HTTPS URL for the repository, or an empty string when no GitHub
// remote can be resolved.
//
// Resolution order:
//  1. `git remote get-url origin` (preferred; honors the conventional name).
//  2. If origin is missing or unparseable, iterate `git remote` and try each
//     remote in declared order.
//
// Supported URL formats (after leading/trailing whitespace is trimmed):
//   - <user>@github.com:owner/repo.git     (SCP-style; user is arbitrary —
//     `git` is the conventional default, but `~/.ssh/config` aliases and CI
//     bots commonly use other usernames such as `mywork` or `ci-bot`)
//   - https://github.com/owner/repo.git
//   - http://github.com/owner/repo.git
//   - ssh://[user@]github.com/owner/repo.git (no explicit port)
//
// `.git` suffixes and trailing slashes are stripped. Only host `github.com`
// (case-insensitive) is recognized; GitHub Enterprise and any other host
// (GitLab, Bitbucket, local paths, file://, etc.) yield an empty string.
//
// The function never errors out: timeouts, missing remotes, and unparseable
// URLs all degrade to an empty string so the caller can treat the result as
// a pure "should we render a link?" boolean.
func GitHubURL(gitRoot string) string {
	// Origin is preferred; resolve it first and short-circuit on a positive
	// match. This avoids spawning `git remote` (and one extra `get-url` per
	// non-origin remote) on the common single-origin case.
	if u := getRemoteURL(gitRoot, "origin"); u != "" {
		if url := normalizeGitHubURL(u); url != "" {
			return url
		}
	}
	for _, name := range listRemotes(gitRoot) {
		if name == "origin" {
			continue
		}
		if u := getRemoteURL(gitRoot, name); u != "" {
			if url := normalizeGitHubURL(u); url != "" {
				return url
			}
		}
	}
	return ""
}

func getRemoteURL(gitRoot, name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	cmd := GitCommand(2*time.Second, gitRoot, "remote", "get-url", name)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func listRemotes(gitRoot string) []string {
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

// githubHTTPRE matches the path portion of an HTTPS / http GitHub URL.
// Group 1 = owner, group 2 = repo (without `.git`).
var githubHTTPRE = regexp.MustCompile(`(?i)^https?://github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`)

// githubSCPRE matches <user>@github.com:owner/repo[.git] (SCP-style).
// The user segment is intentionally arbitrary: the SSH `git@` prefix is
// just a convention; `~/.ssh/config` aliases and CI bots commonly use
// other usernames (e.g. `mywork@`, `ci-bot@`). The `[^@/]+` shape mirrors
// `githubSSHRE` below so the two ssh-shaped forms are treated symmetrically.
// Group 1 = owner, group 2 = repo (without `.git`).
var githubSCPRE = regexp.MustCompile(`(?i)^[^@/]+@github\.com:([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+?)(?:\.git)?$`)

// githubSSHRE matches ssh://[user@]github.com/owner/repo[.git].
// Group 1 = owner, group 2 = repo (without `.git`).
var githubSSHRE = regexp.MustCompile(`(?i)^ssh://(?:[^@/]+@)?github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`)

func normalizeGitHubURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, re := range []*regexp.Regexp{githubHTTPRE, githubSCPRE, githubSSHRE} {
		m := re.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		owner := strings.TrimSpace(m[1])
		repo := strings.TrimSpace(m[2])
		if owner == "" || repo == "" {
			return ""
		}
		return "https://github.com/" + owner + "/" + repo
	}
	return ""
}
