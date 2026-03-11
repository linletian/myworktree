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
- Default interactive path is WebSocket TTY: `GET /api/instances/tty/ws?id=...` (bi-directional terminal stream).
  - **Handshake Protocol**: Server sends `{"type":"ready"}` on connect; client must wait for this before sending resize to start data flow.
  - **Timeout Handling**: Client should implement 5s handshake timeout with SSE fallback.
  - **Resize Support**: Client sends `{"type":"resize","cols":80,"rows":24}` to update PTY size, triggering TUI programs to redraw.
  - **Client-side Config**: Terminal buffer size (scrollback) is configurable on the client side to control how much history is retained in memory for scrolling. This is a client-side setting and does not affect server-side log persistence.
- Fallback path remains available: HTTP input `POST /api/instances/input` + replay/SSE logs (`GET /api/instances/log`, `GET /api/instances/log/stream`).
- UI shows transport state (`websocket/sse/polling`) and supports manual WS reconnect.
- Backlog is stored on disk with a size cap (rolling truncate).
- On server startup, stale persisted `running` records are reconciled to `stopped` because in-memory stdin/stdout bindings cannot be resumed after process restart.

## 5. Security model (single-user)
- Default listen: loopback only.
- Non-loopback requires `--auth`.
- Optional built-in HTTPS via `--tls-cert/--tls-key`.
- Origin/Host check + basic rate limit on unauthorized attempts.
- Redaction on stored backlog (e.g. `sk-...`).

## 6. MCP extensibility
- Core managers (worktree/instance) are transport-agnostic.
- `internal/mcp` exposes tool names; server dispatch maps tool calls to existing core managers without rewriting core.
