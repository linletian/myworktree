# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

<!-- gitnexus:start -->
## GitNexus MCP

This project is indexed by GitNexus as **myworktree** (221 symbols, 611 relationships, 20 execution flows).

### Skills

| Task | Read this skill file |
|------|---------------------|
| Understand architecture | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius analysis | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Debug/tracing | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Refactoring | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |

> If the index is stale, run `npx gitnexus analyze` first.
<!-- gitnexus:end -->

## Build, test, run

```bash
# Build binaries
go build -o myworktree ./cmd/myworktree
go build -o mw ./cmd/mw

# Run tests
go test ./...

# Run a single test
go test -v ./internal/app -run TestParseInt64Default

# Run dev server
go run ./cmd/myworktree -listen 127.0.0.1:0
```

## Architecture overview

myworktree is a lightweight single-user manager for **git worktrees** and **long-running CLI instances**, with a Web UI and HTTP API.

### Core packages

- `internal/app/` — HTTP server, auth, API routing
- `internal/worktree/` — worktree lifecycle via `git` CLI
- `internal/instance/` — instance lifecycle + output log files
- `internal/store/` — `state.json` persistence with file locking
- `internal/tag/` — Tag config loader (JSON)
- `internal/redact/` — secret redaction for logs
- `internal/mcp/` — MCP adapter surface

### Key conventions

- **Run location matters**: execute inside the target git repo. The server detects the repo from CWD.
- **Branch naming**:
  - Default: `mwt/<slug>` (e.g., "fix login" → branch `mwt/fix-login`)
  - Custom grouping: `<group>/<name>` (e.g., "feature/auth" → branch `feature/auth`)
  - Conflicts: appends `-2`, `-3`, etc.
- **Strict deletion**: refuses to delete worktree if `git status --porcelain` is non-empty
- **State storage**: `$(os.UserConfigDir())/myworktree/<repoHash>/state.json`
- **Security**:
  - Default: loopback only
  - Non-loopback requires `--auth`
  - Optional TLS via `--tls-cert/--tls-key`
  - Env values with `TOKEN/SECRET/KEY/PASSWORD` are masked to `***`
  - Output logs redact patterns like `sk-...`

### Data model

- **Worktree**: id, name, path, branch, baseRef, createdAt
- **Instance**: id, worktreeId, tagId, command, cwd, env (sanitized), pid, status, logPath

### Instance lifecycle

- Instances are server-managed processes; UI is just a view
- Default transport: WebSocket TTY (`GET /api/instances/tty/ws?id=...`)
- Fallback: HTTP input + SSE log stream
- On restart, persisted `running` instances are reconciled to `stopped`

### Tags

Merged from global + project-level:
- Global: `$(os.UserConfigDir())/myworktree/tags.json`
- Project: `.../<repoHash>/tags.json`

Schema: `{ id, command, env, preStart, cwd }`
