# AGENTS.md - Guide for Coding Agents

<!-- gitnexus:start -->
# GitNexus MCP

This project is indexed by GitNexus as **feature-ui-update** (312 symbols, 871 relationships, 25 execution flows).

## Always Start Here

1. **Read `gitnexus://repo/{name}/context`** — codebase overview + check index freshness
2. **Match your task to a skill below** and **read that skill file**
3. **Follow the skill's workflow and checklist**

> If step 1 warns the index is stale, run `npx gitnexus analyze` in the terminal first.

## Skills

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->

## Project Overview

myworktree is a lightweight single-user manager for **git worktrees** and **long-running CLI instances**, with a minimal Web UI and HTTP API. It provides isolated workspaces for multiple coding tasks with persistent, re-attachable terminals.

**Key Requirements:**
- Go 1.25+ (see go.mod)
- No external Go dependencies
- Target: macOS 12+ (other platforms not validated)
- Requires: `git`, `zsh`, `script`

## Build, Test, and Lint Commands

### Building
```bash
# Build primary binary
go build -o myworktree ./cmd/myworktree

# Build alias command (auto-opens browser by default)
go build -o mw ./cmd/mw

# Run server (MUST be inside target git repo)
go run ./cmd/myworktree -listen 127.0.0.1:0
```

### Testing
```bash
# Run full test suite
go test ./...

# Run tests for a specific package with verbose output
go test ./internal/worktree -v

# Run a single test function
go test ./internal/app -run TestNormalizeLabels -v

# Run integration tests (may require git setup)
go test ./internal/worktree -run TestIntegration -v
```

### Linting and Formatting
```bash
# Format check used by CI (must pass before commit)
test -z "$(gofmt -l .)"

# Auto-format all Go files
gofmt -w .

# Verify formatting
gofmt -d .
```

### Pre-commit Checklist
```bash
test -z "$(gofmt -l .)" && go test ./... && go build -o myworktree ./cmd/myworktree && go build -o mw ./cmd/mw
```

## Code Style Guidelines

### Formatting
- **Use gofmt**: All code must be formatted with `gofmt`. This is enforced by CI.
- **No external formatters**: Do not introduce additional formatting tools.

### Imports
Group imports in this order with blank lines between:
1. Standard library packages
2. Third-party packages (none in this project)
3. Internal packages (prefixed with `myworktree/internal/`)

```go
import (
	"errors"
	"fmt"
	"os"

	"myworktree/internal/store"
	"myworktree/internal/worktree"
)
```

### Naming Conventions
- **Exported names**: PascalCase (e.g., `ManagedWorktree`, `FileStore`)
- **Unexported names**: camelCase (e.g., `dataDir`, `worktreeMgr`)
- **Acronyms**: Keep uppercase (e.g., `ID`, `PID`, `HTTP`, `JSON`)
- **Interfaces**: Use `-er` suffix for single-method interfaces (e.g., `Manager`, `Store`)
- **Constants**: MixedCaps or ALL_CAPS for exported constants
- **Error variables**: Prefix with `err` (e.g., `errInvalidRepoListenPort`)

### Type Definitions
- **Structs**: Define with JSON tags for serializable types
- **Prefer composition**: Embed structs rather than inheritance
- **Constructor pattern**: Use `New()` functions that return (`*Type, error`)

```go
type Config struct {
	ListenAddr   string
	AuthToken    string
	WorktreesDir string
}

type Manager struct {
	GitRoot      string
	DataDir      string
	WorktreesDir string
	Store        store.FileStore
}

func New(cfg Config, logger *log.Logger) (*Server, error) {
	// Validate inputs
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	// ... initialization
	return &Server{...}, nil
}
```

### Error Handling
- **Return errors explicitly**: Do not panic in library code
- **Wrap errors**: Provide context when appropriate
- **Use errors.New()** for simple static error messages
- **Define package-level error variables** for errors that need comparison

```go
var errInvalidRepoListenPort = errors.New("invalid persisted listen_port")

func (m Manager) Create(taskDesc string) (store.ManagedWorktree, error) {
	if strings.TrimSpace(taskDesc) == "" {
		return store.ManagedWorktree{}, errors.New("task description is required")
	}
	// ... implementation
}
```

### Resource Management
- **Use defer for cleanup**: Always defer Close(), Unlock(), etc.
- **Check errors in defer**: Use blank identifier for cleanup errors when appropriate

```go
f, err := os.OpenFile(path, os.O_RDONLY, 0o600)
if err != nil {
	return err
}
defer f.Close()

if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
	return err
}
defer func() { _ = syscall.Flock(int(f.Fd(), syscall.LOCK_UN) }()
```

### Concurrency
- **Protect shared state**: Use `sync.Mutex` or `sync.RWMutex` for thread safety
- **Lock at the right granularity**: Hold locks for minimal time
- **Document thread safety**: Comment on whether functions are thread-safe

```go
type Manager struct {
	stateMu sync.Mutex  // protects Store access
	mu      sync.Mutex  // protects running/inputs maps
	// ...
}

func (m *Manager) Start(in StartInput) (store.ManagedInstance, error) {
	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*exec.Cmd{}
	}
	m.mu.Unlock()
	// ... rest of implementation
}
```

### Testing Conventions
- **Test file naming**: `<name>_test.go` in the same package
- **Test function naming**: `Test<FunctionName>` or `Test<Scenario>`
- **Table-driven tests**: Prefer for multiple test cases
- **Integration tests**: Suffix with `_integration_test.go` or use build tags

```go
func TestSlugify(t *testing.T) {
	got := slugify("Fix login 401 & add tests!")
	want := "fix-login-401-add-tests"
	if got != want {
		t.Fatalf("unexpected slugify result: got %q, want %q", got, want)
	}
}
```

### Comments and Documentation
- **Package comments**: Start with "Package <name>" for package docs
- **Exported types/functions**: Add doc comments explaining purpose
- **Implementation comments**: Explain "why", not "what"
- **No TODO/FIXME comments** in production code without tracking issue

### File Organization
- **One primary type per file**: Named after the type (e.g., `manager.go` for `Manager`)
- **Test files**: Same directory as source with `_test.go` suffix
- **Package structure**: Follow internal package layout:
  - `app/` - HTTP server, routing, auth
  - `worktree/` - Worktree lifecycle management
  - `instance/` - Process management
  - `store/` - State persistence
  - `tag/` - Tag configuration
  - `redact/` - Log redaction
  - `ui/` - Embedded static UI
  - `mcp/` - MCP adapter
  - `gitx/` - Git operations
  - `ws/` - WebSocket handling
  - `cli/` - CLI command handling
  - `version/` - Version information

### JSON and Serialization
- **Use struct tags**: Always include `json` tags for serializable fields
- **Omit empty fields**: Use `omitempty` for optional fields
- **Use consistent field names**: Prefer snake_case in JSON for API compatibility

```go
type ManagedWorktree struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	CreatedAt string `json:"created_at"`
}
```

## Important Architectural Notes

1. **Run Location Matters**: The server MUST be executed inside the target git repository because repo detection and per-project data directories derive from CWD.

2. **State Storage**: All persisted state uses `store.FileStore` with file locking and atomic writes. Never write state directly; always go through the store.

3. **Branch Naming**:
   - Default: `mwt/<slug>`
   - Custom: If task description is `<group>/<name>`, branch is `<group>/<name>` (no prefix)
   - Slug is ASCII-only, max 48 chars, falls back to "worktree" if empty

4. **Strict Deletion**: Worktree deletion refuses if `git status --porcelain` is non-empty (includes untracked files).

5. **Security Defaults**:
   - Server defaults to loopback listen
   - Non-loopback requires `--auth` token
   - Optional TLS via `--tls-cert/--tls-key`
   - Log redaction for secrets (e.g., `sk-...`)
   - Env sanitization for TOKEN/SECRET/KEY/PASSWORD keys

## CI/CD

GitHub Actions runs on:
- Pushes to `develop` and `main` branches
- Pull requests targeting `develop` and `main`

CI Pipeline (`.github/workflows/go-ci.yml`):
1. Verify `gofmt` formatting
2. Run `go test ./...`
3. Build both binaries (`myworktree` and `mw`)

Release Process (`.github/workflows/release.yml`):
- Triggered by `v*` tags
- Builds darwin `amd64`/`arm64` archives with checksums

## Additional Resources

- **Documentation**: See `docs/` directory for PRD, Architecture, and API docs
- **Changelog**: See `CHANGELOG.md` for version history
- **License**: MIT License (see `LICENSE` file)
