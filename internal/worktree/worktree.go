package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"myworktree/internal/gitx"
	"myworktree/internal/llm"
	"myworktree/internal/store"
)

type Manager struct {
	GitRoot      string
	DataDir      string
	WorktreesDir string // optional; "data" uses legacy DataDir/worktrees
	Store        store.FileStore
}

func (m Manager) List() ([]store.ManagedWorktree, error) {
	st, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	out := make([]store.ManagedWorktree, len(st.Worktrees))
	for i, wt := range st.Worktrees {
		out[i] = wt
		if branch, err := currentBranch(wt.Path); err == nil {
			out[i].Branch = branch
		}
		// On error (path gone, detached HEAD), keep the stored Branch value.
	}
	return out, nil
}

// currentBranch returns the currently checked-out branch for the given git worktree path.
func currentBranch(worktreePath string) (string, error) {
	cmd := gitx.GitCommand(2*time.Second, worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w", err)
	}
	b := strings.TrimSpace(string(out))
	if b == "HEAD" || b == "" {
		return "", errors.New("git HEAD is detached or malformed")
	}
	return b, nil
}

type UnmanagedWorktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

func (m Manager) ListUnmanaged() ([]UnmanagedWorktree, error) {
	st, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	managed := make(map[string]bool)
	for _, w := range st.Worktrees {
		managed[filepath.Clean(w.Path)] = true
	}

	raw, err := listGitWorktrees(m.GitRoot)
	if err != nil {
		return nil, err
	}

	var out []UnmanagedWorktree
	for _, r := range raw {
		clean := filepath.Clean(r.Path)
		if clean == filepath.Clean(m.GitRoot) {
			continue
		}
		if managed[clean] {
			continue
		}
		// Branch is like "refs/heads/foo", simplify
		br := strings.TrimPrefix(r.Branch, "refs/heads/")
		if br == "" || br == "HEAD" {
			continue
		}
		out = append(out, UnmanagedWorktree{Path: r.Path, Branch: br})
	}
	return out, nil
}

type CreateOptions struct {
	BaseRef       string
	AdoptIfExists bool
	BranchName    string // if non-empty, use this branch name directly (skip LLM), parsed as group/name if contains "/"
}

func (m Manager) Create(taskDesc string, baseRef string) (store.ManagedWorktree, error) {
	return m.CreateWithOptions(taskDesc, CreateOptions{BaseRef: baseRef})
}

func (m Manager) CreateWithOptions(taskDesc string, opts CreateOptions) (store.ManagedWorktree, error) {
	return m.CreateWithOptionsCtx(context.Background(), taskDesc, opts)
}

func (m Manager) CreateWithOptionsCtx(ctx context.Context, taskDesc string, opts CreateOptions) (store.ManagedWorktree, error) {
	if strings.TrimSpace(taskDesc) == "" {
		return store.ManagedWorktree{}, errors.New("task description is required")
	}
	slug := slugify(taskDesc)
	if slug == "" {
		slug = "worktree"
	}

	group, baseName, custom := parseBranchSpec(taskDesc)

	// If BranchName is provided, use it directly (skip LLM).
	// Parse to extract group/baseName for worktree path.
	if opts.BranchName != "" {
		group, baseName, _ = parseBranchSpec(opts.BranchName)
		custom = true
	} else if !custom && llm.IsAvailable() {
		// Use LLM to generate branch name if protocol is set and not a custom spec.
		// Custom specs (e.g., "feature/foo") are used as-is and not passed to LLM.
		generated, err := llm.GenerateBranchName(ctx, taskDesc)
		if err != nil {
			return store.ManagedWorktree{}, fmt.Errorf("LLM branch naming failed: %w", err)
		}
		// Parse the generated branch name to extract group/baseName.
		// e.g. "fix/instance-ime" -> group="fix", baseName="instance-ime"
		// Worktree path will use "fix-instance-ime" (hyphen separator).
		group, baseName, _ = parseBranchSpec(generated)
		custom = true // Treat LLM output like a custom spec
	}

	if !custom {
		group = "worktree"
		baseName = slug
	}

	if opts.AdoptIfExists {
		importName := baseName
		if custom {
			importName = group + "/" + baseName
		}
		if branchExists(m.GitRoot, group+"/"+baseName) {
			if wt, err := m.Import(importName); err == nil {
				return wt, nil
			}
		}
	}

	branchName := baseName
	for i := 2; branchExists(m.GitRoot, group+"/"+branchName); i++ {
		branchName = fmt.Sprintf("%s-%d", baseName, i)
	}

	name := branchName
	if custom {
		name = group + "-" + branchName
	}

	id := shortID()
	branch := group + "/" + branchName

	root, legacy, err := m.worktreesRoot()
	if err != nil {
		return store.ManagedWorktree{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return store.ManagedWorktree{}, err
	}
	pathName := name
	if legacy {
		pathName = fmt.Sprintf("%s-%s", id, name)
	}
	path := filepath.Join(root, pathName)

	ref := strings.TrimSpace(opts.BaseRef)
	if ref == "" {
		ref = "HEAD"
	}
	verify := gitx.GitCommand(10*time.Second, m.GitRoot, "rev-parse", "--verify", ref)
	if out, err := verify.CombinedOutput(); err != nil {
		if ref == "HEAD" {
			return store.ManagedWorktree{}, fmt.Errorf("repository has no commits (HEAD not found); create an initial commit or pass -base: %s", strings.TrimSpace(string(out)))
		}
		return store.ManagedWorktree{}, fmt.Errorf("base ref not found: %s", ref)
	}

	// git worktree add -b <branch> <path> <ref>
	args := []string{"worktree", "add", "-b", branch, path, ref}
	cmd := gitx.GitCommand(10*time.Second, m.GitRoot, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return store.ManagedWorktree{}, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	wt := store.ManagedWorktree{
		ID:        id,
		Name:      name,
		Path:      path,
		Branch:    branch,
		BaseRef:   opts.BaseRef,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedWorktree{}, err
	}
	st.Worktrees = append(st.Worktrees, wt)
	if err := m.Store.Save(st); err != nil {
		return store.ManagedWorktree{}, err
	}
	return wt, nil
}

func (m Manager) Import(nameOrPath string) (store.ManagedWorktree, error) {
	nameOrPath = strings.TrimSpace(nameOrPath)
	if nameOrPath == "" {
		return store.ManagedWorktree{}, errors.New("worktree name or path is required")
	}

	items, err := listGitWorktrees(m.GitRoot)
	if err != nil {
		return store.ManagedWorktree{}, err
	}

	var path, branch, displayName string

	// Check if input is an absolute path match
	if filepath.IsAbs(nameOrPath) {
		cleanInput := filepath.Clean(nameOrPath)
		for _, it := range items {
			if filepath.Clean(it.Path) == cleanInput {
				path = it.Path
				branch = strings.TrimPrefix(it.Branch, "refs/heads/")
				displayName = filepath.Base(path)
				break
			}
		}
		if path == "" {
			return store.ManagedWorktree{}, fmt.Errorf("no git worktree found at path %s", nameOrPath)
		}
	} else {
		// Name-based lookup
		displayName = nameOrPath
		branch = "mwt/" + nameOrPath
		if g, n, ok := parseBranchSpec(nameOrPath); ok {
			branch = g + "/" + n
			displayName = g + "-" + n
		}
		for _, it := range items {
			if it.Branch == "refs/heads/"+branch {
				path = it.Path
				break
			}
		}
		if path == "" {
			// Backward-compat: older versions used wt/<name>.
			legacy := "wt/" + nameOrPath
			for _, it := range items {
				if it.Branch == "refs/heads/"+legacy {
					path = it.Path
					branch = legacy
					break
				}
			}
		}
		if path == "" {
			return store.ManagedWorktree{}, fmt.Errorf("no existing git worktree found for branch %s", branch)
		}
	}

	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedWorktree{}, err
	}
	for _, existing := range st.Worktrees {
		if existing.Path == path {
			return existing, nil
		}
	}

	wt := store.ManagedWorktree{
		ID:        shortID(),
		Name:      displayName,
		Path:      path,
		Branch:    branch,
		BaseRef:   "",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	st.Worktrees = append(st.Worktrees, wt)
	if err := m.Store.Save(st); err != nil {
		return store.ManagedWorktree{}, err
	}
	return wt, nil
}

func (m Manager) worktreesRoot() (root string, legacy bool, err error) {
	v := strings.TrimSpace(m.WorktreesDir)
	if v == "" {
		repo := filepath.Base(filepath.Clean(m.GitRoot))
		parent := filepath.Dir(filepath.Clean(m.GitRoot))
		return filepath.Join(parent, repo+"-myworktree"), false, nil
	}
	if v == "data" || v == "datadir" {
		return filepath.Join(m.DataDir, "worktrees"), true, nil
	}
	if filepath.IsAbs(v) {
		return v, false, nil
	}
	return filepath.Join(m.GitRoot, v), false, nil
}

// DirtyWorktreeError is returned by Delete when the worktree carries
// uncommitted or untracked changes and force was not set (issue #102). It
// carries the full breakdown so API clients can render the details: the
// previous flat message — "worktree has uncommitted or untracked changes;
// delete is refused" — gave no way to tell "I left a scratch file" (low
// risk) from "133 tracked source files vanished BEFORE the delete attempt"
// (the workspace is damaged; restore it instead of deleting), two
// situations that demand OPPOSITE reactions.
type DirtyWorktreeError struct {
	Entries    int      // total git status --porcelain lines
	Deleted    int      // lines with D in either status column
	Modified   int      // remaining modification lines (M/T/U in either column)
	Added      int      // lines with A in either status column
	Renamed    int      // lines with R/C in either status column
	Untracked  int      // "??" lines
	Ignored    int      // gitignored entries present — invisible to the blocking check (which runs without --ignored) but destroyed unrecoverably by a force delete
	FirstPaths []string // up to maxDirtyFirstPaths raw path fields, in status order
	Porcelain  string   // the git status --porcelain -uall output for UI expansion, capped at maxDirtyPorcelainLines lines
}

// maxDirtyFirstPaths caps how many paths the refusal message names inline;
// the remaining paths are in Porcelain (itself capped at
// maxDirtyPorcelainLines lines).
const maxDirtyFirstPaths = 5

// maxDirtyPorcelainLines bounds the status output carried in the refusal.
// The probe runs with -uall so untracked directories count file-by-file —
// an untracked dependency dir (node_modules) would otherwise expand the
// API/UI payload to tens of thousands of lines.
const maxDirtyPorcelainLines = 200

// capDirtyPorcelain truncates the carried status output; bucket counts and
// FirstPaths are always computed from the FULL output before capping.
func capDirtyPorcelain(out string) string {
	lines := strings.Split(out, "\n")
	if len(lines) <= maxDirtyPorcelainLines {
		return out
	}
	rest := len(lines) - maxDirtyPorcelainLines
	word := "lines"
	if rest == 1 {
		word = "line"
	}
	return fmt.Sprintf("%s\n… (%d more %s truncated)", strings.Join(lines[:maxDirtyPorcelainLines], "\n"), rest, word)
}

func (e *DirtyWorktreeError) Error() string {
	entryWord := "entries"
	if e.Entries == 1 {
		entryWord = "entry"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "worktree has uncommitted or untracked changes; delete is refused: %d %s (%d deleted, %d modified, %d untracked",
		e.Entries, entryWord, e.Deleted, e.Modified, e.Untracked)
	if e.Added > 0 {
		fmt.Fprintf(&b, ", %d added", e.Added)
	}
	if e.Renamed > 0 {
		fmt.Fprintf(&b, ", %d renamed", e.Renamed)
	}
	b.WriteString(")")
	if len(e.FirstPaths) > 0 {
		fmt.Fprintf(&b, "; first paths: %s", strings.Join(e.FirstPaths, ", "))
	}
	if e.Ignored > 0 {
		ignoredWord := "entries"
		if e.Ignored == 1 {
			ignoredWord = "entry"
		}
		fmt.Fprintf(&b, "; additionally %d gitignored %s present, which a force delete would destroy unrecoverably",
			e.Ignored, ignoredWord)
	}
	b.WriteString("; retry with force to delete anyway")
	return b.String()
}

// summarizePorcelain classifies `git status --porcelain` (v1) lines into the
// DirtyWorktreeError buckets. Every classified line counts in EXACTLY ONE
// bucket — the two status columns collapse by priority
// (delete > rename > add > modify) — so the buckets always sum to Entries.
// "??" lines are untracked; "!!" ignored lines are skipped outright (they
// never occur here in production — the blocking check runs without
// --ignored). FirstPaths is capped at maxDirtyFirstPaths.
func (e *DirtyWorktreeError) summarizePorcelain(out string) {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "!!") {
			// Ignored entries never reach this summary in production
			// (the blocking check runs WITHOUT --ignored); skip them
			// defensively so a caller passing the --ignored output
			// cannot misclassify them as modifications.
			continue
		}
		e.Entries++
		xy, path := line, ""
		if len(line) >= 3 {
			xy = line[:2]
			path = strings.TrimSpace(line[3:])
		}
		switch {
		case xy == "??":
			e.Untracked++
		case strings.ContainsAny(xy, "D"):
			e.Deleted++
		case strings.ContainsAny(xy, "RC"):
			e.Renamed++
		case strings.ContainsAny(xy, "A"):
			e.Added++
		default:
			e.Modified++
		}
		if path != "" && len(e.FirstPaths) < maxDirtyFirstPaths {
			e.FirstPaths = append(e.FirstPaths, path)
		}
	}
}

// countIgnoredPorcelain counts the "!!" entries of a
// `git status --porcelain --ignored` run — the gitignored files/dirs the
// plain dirty check never sees (issue #102: 89 gitignored files, 64 of them
// single-copy, survived the dirty check unnoticed in the report).
func countIgnoredPorcelain(out string) (n int) {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "!!") {
			n++
		}
	}
	return n
}

// Delete removes the worktree and its state record. It refuses a dirty
// worktree with a *DirtyWorktreeError unless force is set. The returned
// count reports how many gitignored entries the delete destroyed with the
// directory — the blocking dirty check never sees them, so surfacing the
// number keeps a clean-looking delete from silently erasing build caches,
// logs, or backups that exist only in this worktree (issue #102 review).
// The count is 0 on the force path, where the status probe is skipped
// entirely (see below).
func (m Manager) Delete(id string, force bool) (int, error) {
	st, err := m.Store.Load()
	if err != nil {
		return 0, err
	}
	idx := -1
	var wt store.ManagedWorktree
	for i := range st.Worktrees {
		if st.Worktrees[i].ID == id {
			idx = i
			wt = st.Worktrees[i]
			break
		}
	}
	if idx == -1 {
		return 0, fmt.Errorf("unknown worktree id: %s", id)
	}

	ignoredDestroyed := 0
	if _, err := os.Stat(wt.Path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("stat worktree failed: %w", err)
		}

		cmd := gitx.GitCommand(10*time.Second, m.GitRoot, "worktree", "prune", "--expire", "now")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("git worktree prune failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		// Strict: refuse to delete if dirty (unless force). The refusal
		// carries the full breakdown — counts by category, first paths,
		// and the gitignored-file risk a force delete would destroy
		// (issue #102): the flat "delete is refused" message made a
		// damaged workspace (tracked files missing) indistinguishable
		// from a leftover scratch file.
		//
		// The probe runs ONLY without force: its output builds the
		// refusal, and gating the escape hatch on it would strand exactly
		// the workspaces force exists for — on a damaged worktree (e.g.
		// a corrupt index) git status fails while git worktree remove
		// --force still succeeds (issue #102 review).
		if !force {
			// -uall: untracked/ignored DIRECTORIES count file-by-file —
			// the default collapse reports "?? node_modules/" as one
			// entry while a delete would destroy thousands of files.
			// -c core.quotePath=false: keep non-ASCII paths literal
			// instead of C-style octal escapes in the refusal.
			cmdStatus := gitx.GitCommand(10*time.Second, wt.Path, "-c", "core.quotePath=false", "status", "--porcelain", "-uall")
			statusOut, err := cmdStatus.Output()
			if err != nil {
				return 0, fmt.Errorf("git status failed: %w", err)
			}
			porcelain := strings.TrimSpace(string(statusOut))
			if porcelain != "" {
				dirty := &DirtyWorktreeError{Porcelain: capDirtyPorcelain(porcelain)}
				dirty.summarizePorcelain(porcelain)
				// Ignored files do NOT block the delete (the check above
				// runs without --ignored), so name the risk explicitly: a
				// force delete destroys them unrecoverably (issue #102 P3).
				// A failed count is not worth failing the refusal over —
				// the dirty entries alone justify it.
				cmdIgnored := gitx.GitCommand(10*time.Second, wt.Path, "-c", "core.quotePath=false", "status", "--porcelain", "--ignored", "-uall")
				if ignoredOut, ignoredErr := cmdIgnored.Output(); ignoredErr == nil {
					dirty.Ignored = countIgnoredPorcelain(string(ignoredOut))
				}
				return 0, dirty
			}
			// Clean per the blocking check — but the delete still
			// destroys every gitignored file with the directory. Count
			// them so callers can surface the loss; ignoring the count's
			// error keeps a broken probe from blocking a clean delete.
			// Edge case (second review round): on a pathologically large
			// ignored tree the probe can hit the 10 s GitCommand timeout,
			// which leaves the count at 0 — accepted, because blocking a
			// legitimate clean delete on a counting failure is worse than
			// occasionally missing the note.
			cmdIgnored := gitx.GitCommand(10*time.Second, wt.Path, "-c", "core.quotePath=false", "status", "--porcelain", "--ignored", "-uall")
			if ignoredOut, ignoredErr := cmdIgnored.Output(); ignoredErr == nil {
				ignoredDestroyed = countIgnoredPorcelain(string(ignoredOut))
			}
		}

		args := []string{"worktree", "remove"}
		if force {
			// `git worktree remove` refuses a dirty worktree on its own;
			// --force is the explicit override the caller opted into.
			args = append(args, "--force")
		}
		args = append(args, wt.Path)
		cmd := gitx.GitCommand(10*time.Second, m.GitRoot, args...)
		removeOut, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("git worktree remove failed: %w: %s", err, strings.TrimSpace(string(removeOut)))
		}
	}

	st.Worktrees = append(st.Worktrees[:idx], st.Worktrees[idx+1:]...)
	if err := m.Store.Save(st); err != nil {
		return 0, err
	}
	return ignoredDestroyed, nil
}

var (
	nonSlug     = regexp.MustCompile(`[^a-z0-9]+`)
	branchToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func parseBranchSpec(s string) (group string, name string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return "", "", false
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	g := strings.TrimSpace(parts[0])
	n := strings.TrimSpace(parts[1])
	if !branchToken.MatchString(g) || !branchToken.MatchString(n) {
		return "", "", false
	}
	return g, n, true
}

func slugify(s string) string {
	// Best-effort without external deps; if no ascii, caller will fallback.
	s = strings.ToLower(s)
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = s[:48]
		s = strings.Trim(s, "-")
	}
	return s
}

func shortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
