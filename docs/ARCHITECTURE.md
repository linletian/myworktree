# myworktree — Architecture

## 1. Overview
myworktree is a lightweight single-user manager for:
- **git worktrees** (isolated working directories)
- **instances** (long-running shell/CLI processes started by myworktree)
- **web UI + HTTP API** to manage them and replay recent output

It does **not** analyze project code or prevent concurrent write conflicts inside a worktree.

## 2. High-level components
- `cmd/myworktree/` — CLI entry.
- `internal/app/` — HTTP server, auth middleware, API routing.
- `internal/worktree/` — worktree lifecycle via `git` CLI.
- `internal/instance/` — instance lifecycle (spawn/stop/list) + output log file.
- `internal/tag/` — Tag config loader (MVP: JSON).
- `internal/store/` — persistent state store (`state.json`) with file locking + atomic writes.
- `internal/redact/` — secret redaction for stored logs/backlog.
- `internal/mcp/` — MCP adapter surface (tool names + app-level tool dispatch), keeping core decoupled.
- `internal/ui/` — embedded static UI.

## 3. Data & persistence
### 3.1 Project-scoped user data directory
- Stored under user-level config dir (no repo pollution), partitioned by `repoHash`.
- Contains:

### 3.1.1 Worktree directory layout
- Default: worktrees are created next to the repo under `<repo-name>-myworktree/<worktree-name>/`.
- Override: `-worktrees-dir=data` uses the legacy location under the per-project data dir; you can also set a custom path.
  - `state.json` — managed worktrees + managed instances
  - `tags.json` — project-level tags
  - `logs/<instanceId>.log` — rolling instance backlog

### 3.2 State model
- Worktree: id, name, path, branch, baseRef, createdAt
- Instance: id, worktreeId, tagId, command, cwd, env (sanitized), pid, status, logPath, timestamps

## 4. Instance lifecycle & reconnect semantics
- An instance is a server-managed process; UI windows are merely views.
- UI reconnect enumerates instances via `GET /api/instances`, fetches backlog via `GET /api/instances/log?id=...`, and can follow live output via SSE `GET /api/instances/log/stream?id=...`.
- Backlog is stored on disk with a size cap (rolling truncate).

> Note: current implementation supports Web TTY streaming via `/api/instances/tty/ws` plus HTTP input fallback (`/api/instances/input`).

## 5. Security model (single-user)
- Default listen: loopback only.
- Non-loopback requires `--auth`.
- Optional built-in HTTPS via `--tls-cert/--tls-key`.
- Origin/Host check + basic rate limit on unauthorized attempts.
- Redaction on stored backlog (e.g. `sk-...`).

## 6. MCP extensibility
- Core managers (worktree/instance) are transport-agnostic.
- `internal/mcp` exposes tool names; server dispatch maps tool calls to existing core managers without rewriting core.
