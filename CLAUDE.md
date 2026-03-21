# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

## Build, test, lint

```bash
# Build binaries
go build -o myworktree ./cmd/myworktree
go build -o mw ./cmd/mw

# Run tests
go test ./...

# Run a single test
go test -v ./internal/app -run TestParseInt64Default

# Lint/format check (required to pass before commits)
test -z "$(gofmt -l .)"

# Pre-commit checklist
test -z "$(gofmt -l .)" && go test ./... && go build -o myworktree ./cmd/myworktree && go build -o mw ./cmd/mw

# Run dev server (MUST be inside the target git repo)
go run ./cmd/myworktree -listen 127.0.0.1:0
```

## Architecture

myworktree is a lightweight single-user manager for **git worktrees** and **long-running CLI instances**, with a Web UI and HTTP API.

### Core packages

- `internal/app/` — HTTP server, auth, API routing; wires all managers together
- `internal/worktree/` — worktree lifecycle via `git` CLI
- `internal/instance/` — instance lifecycle (spawn/stop/list) + rolling log files
- `internal/store/` — `state.json` persistence with `flock` + atomic rename
- `internal/tag/` — Tag config loader (JSON), merged from global + project-level
- `internal/redact/` — secret redaction for stored logs and sanitized env
- `internal/mcp/` — MCP adapter surface (tool listing + dispatch to core managers)
- `internal/ui/` — embedded static UI (`//go:embed static/*`)
- `internal/ws/` — custom WebSocket server (no external dependency)
- `internal/gitx/` — thin wrapper around git commands

### Data model

- **Worktree**: id, name, path, branch, baseRef, createdAt
- **Instance**: id, worktreeId, tagId, command, cwd, env (sanitized), pid, status, logPath, timestamps
- **Main Repo**: not persisted; served via `GET /api/main` with live git branch (`git rev-parse --abbrev-ref HEAD`)

### Main workspace (sidebar)

The sidebar shows a pinned **Main Workspace** item at the top (purple accent), followed by a "Worktrees" divider and the managed worktree list.

- **Main repo**: `GET /api/main` returns `{name, branch}`. The branch is live — queried every refresh via `git rev-parse --abbrev-ref HEAD` (via `gitx.CurrentBranch`). Falls back to HTTP 500 on error (e.g., git repo inaccessible, detached HEAD).
- **Worktrees**: `GET /api/worktrees` also returns live branches — `worktree.Manager.List()` queries `git rev-parse --abbrev-ref HEAD` per worktree path on each call.
- **Auto-select**: On first load, the UI auto-selects the first worktree; if no worktrees exist, it selects the main repo.
- **Instance routing**: Use `worktree_id: "__main__"` (constant: `instance.MainWorktreeID`) in `POST /api/instances` to start an instance in the main repo root. The instance's `worktree_id` field will be `"__main__"` and `worktree_name` will be the directory basename.
- **Refresh**: All branch info updates via the existing 2-second polling (`refresh()` → `Promise.all`).

### Instance lifecycle

Instances are server-managed PTY processes (`script -q /dev/null zsh -f -i`). The UI is just a view.

- **Default transport**: WebSocket TTY (`GET /api/instances/tty/ws?id=...`) — bidirectional terminal stream
- **Fallback**: HTTP input + SSE log stream (`POST /api/instances/input`, `GET /api/instances/log/stream`)
- **Handshake**: Server sends `{"type":"ready"}` on connect; client must wait for this before sending `{"type":"resize",...}` or any input. This prevents xterm.js query sequences (DA, DEC Private Mode, OSC color queries) from leaking into the PTY as garbage text
- **Instance switching**: Running instances keep their PTY attachment; inactive sessions are hidden, not torn down
- **Startup reconcile**: Stale persisted `running` instances → `stopped` (in-memory stdin/stdout bindings cannot resume after restart)

### Terminal query filtering (frontend)

The UI filters terminal response sequences in the input path (`term.onData`) to prevent TUI programs from receiving xterm.js-generated responses as keyboard input. Known patterns: `ESC[?1;2c` (DA), `ESC]11;rgb:...` (OSC 11), `ESC[?2027;0$y` (DEC Private Mode), and their partial/fragmented forms. See `docs/TERMINAL_FILTER_REVIEW.md` for limitations.

### State storage

`$(os.UserConfigDir())/myworktree/<repoHash>/`:
- `state.json` — worktrees + instances
- `tags.json` — project-level tags
- `logs/<instanceId>.log` — rolling instance backlog (10MB cap)
- `server.json` — persisted listen port

State always goes through `store.FileStore` (never write directly) to preserve locking and atomicity.

### Key conventions

- **Run location matters**: server detects the repo from CWD. Each repo gets its own data dir.
- **Branch naming**:
  - Default: `mwt/<slug>` (e.g., "fix login" → `mwt/fix-login`)
  - Custom grouping: `<group>/<name>` if task starts with "group/name"
  - Conflicts: append `-2`, `-3`, etc.
- **Strict deletion**: refuses to delete worktree if `git status --porcelain` is non-empty
- **Worktree placement**: default is `<repo-parent>/<repo-name>-myworktree/<worktree-name>/`; `-worktrees-dir=data` uses the data dir
- **Security**:
  - Default: loopback only; non-loopback requires `--auth`
  - Optional TLS via `--tls-cert/--tls-key`
  - Env values with `TOKEN/SECRET/KEY/PASSWORD` → `***`
  - Logs redact patterns like `sk-...`

### Tags

Merged from global + project-level:
- Global: `$(os.UserConfigDir())/myworktree/tags.json`
- Project: `.../<repoHash>/tags.json`

Schema: `{ id, command, env, preStart, cwd }`
