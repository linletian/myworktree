package worktree

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	return st.Worktrees, nil
}

func (m Manager) Create(taskDesc string, baseRef string) (store.ManagedWorktree, error) {
	if strings.TrimSpace(taskDesc) == "" {
		return store.ManagedWorktree{}, errors.New("task description is required")
	}
	slug := slugify(taskDesc)
	if slug == "" {
		slug = "worktree"
	}

	group, baseName, custom := parseBranchSpec(taskDesc)
	if !custom {
		group = "mwt"
		baseName = slug
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

	ref := strings.TrimSpace(baseRef)
	if ref == "" {
		ref = "HEAD"
	}
	verify := exec.Command("git", "rev-parse", "--verify", ref)
	verify.Dir = m.GitRoot
	if out, err := verify.CombinedOutput(); err != nil {
		if ref == "HEAD" {
			return store.ManagedWorktree{}, fmt.Errorf("repository has no commits (HEAD not found); create an initial commit or pass -base: %s", strings.TrimSpace(string(out)))
		}
		return store.ManagedWorktree{}, fmt.Errorf("base ref not found: %s", ref)
	}

	// git worktree add -b <branch> <path> <ref>
	args := []string{"worktree", "add", "-b", branch, path, ref}
	cmd := exec.Command("git", args...)
	cmd.Dir = m.GitRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return store.ManagedWorktree{}, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	wt := store.ManagedWorktree{
		ID:        id,
		Name:      name,
		Path:      path,
		Branch:    branch,
		BaseRef:   baseRef,
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

func (m Manager) Import(name string) (store.ManagedWorktree, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.ManagedWorktree{}, errors.New("worktree name is required")
	}
	// Accept either a full branch spec like "feature/foo" or a short name that defaults to "mwt/<name>".
	displayName := name
	branch := "mwt/" + name
	if g, n, ok := parseBranchSpec(name); ok {
		branch = g + "/" + n
		displayName = g + "-" + n
	}
	items, err := listGitWorktrees(m.GitRoot)
	if err != nil {
		return store.ManagedWorktree{}, err
	}
	var path string
	for _, it := range items {
		if it.Branch == "refs/heads/"+branch {
			path = it.Path
			break
		}
	}
	if path == "" {
		// Backward-compat: older versions used wt/<name>.
		legacy := "wt/" + name
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

func (m Manager) Delete(id string) error {
	st, err := m.Store.Load()
	if err != nil {
		return err
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
		return fmt.Errorf("unknown worktree id: %s", id)
	}

	// Strict: refuse to delete if dirty.
	cmdStatus := exec.Command("git", "status", "--porcelain")
	cmdStatus.Dir = wt.Path
	out, err := cmdStatus.Output()
	if err != nil {
		return fmt.Errorf("git status failed: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return errors.New("worktree has uncommitted or untracked changes; delete is refused")
	}

	cmd := exec.Command("git", "worktree", "remove", wt.Path)
	cmd.Dir = m.GitRoot
	out2, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove failed: %w: %s", err, strings.TrimSpace(string(out2)))
	}

	st.Worktrees = append(st.Worktrees[:idx], st.Worktrees[idx+1:]...)
	return m.Store.Save(st)
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
