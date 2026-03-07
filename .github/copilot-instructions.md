# Copilot instructions for myworktree

## Build, test, lint
- Build (primary binary):
  - `go build -o myworktree ./cmd/myworktree`
- Build (alias command, equivalent):
  - `go build -o mw ./cmd/mw`
- Run server (during development):
  - `go run ./cmd/myworktree -listen 127.0.0.1:0`
- Compile/test (there are currently no unit tests; this mainly validates builds):
  - `go test ./...`
- Lint:
  - No linter is configured in this repo.

## High-level architecture
myworktree is a lightweight single-user manager for **git worktrees** and **long-running CLI instances**, with a minimal Web UI and HTTP API.

Key flows:
- **Target repo detection**: the server and CLI commands use the current working directory to find the target git repo root via `gitx.GitRoot(".")`.
- **Per-project state**: state is stored outside the repo under the user config dir, partitioned by a hash of the git root.
  - Data dir (conceptually): `$(os.UserConfigDir())/myworktree/<repoHash>/`
  - Contains: `state.json`, `tags.json`, `logs/<instanceId>.log`, and `worktrees/`.

Core packages:
- `internal/app/`: HTTP server + routing + auth/security checks; wires managers and serves the embedded UI.
- `internal/worktree/`: worktree lifecycle (create/list/import/delete) by calling the `git` CLI.
- `internal/instance/`: starts/stops managed processes and captures stdout/stderr into rolling log files for replay.
- `internal/store/`: `state.json` persistence with file locking + atomic write.
- `internal/tag/`: Tag config loader (MVP uses **JSON** to stay dependency-free).
- `internal/redact/`: redaction for stored logs and sanitized env.
- `internal/ui/`: embedded static UI (`embed static/*`).
- `internal/mcp/`: MCP adapter stub (tool listing; keep boundaries clean for future MCP server work).

Important behavior notes:
- Instances are started as `zsh -lc <command>` (see `internal/instance/manager.go`) and are **not** a fully interactive PTY/WebTTY yet; the UI currently focuses on management + output replay.

## Key conventions
- **Run location matters**: the server is started by the default command (e.g. `myworktree -listen ...`) and should be executed *inside the target git repo* you want to manage, because repo detection and the per-project data dir derive from CWD.
- **Branch naming**: managed worktrees are created on branches like `wt/<slug>`.
  - `slugify()` is best-effort ASCII, max length ~48; if empty it falls back to `worktree`.
  - If `wt/<slug>` already exists, suffix `-2`, `-3`, ... is appended.
- **Strict deletion**: `worktree delete` refuses to delete if `git status --porcelain` is non-empty (includes untracked files).
- **State storage discipline**:
  - Persisted state lives in `state.json` via `store.FileStore` and is protected with `flock` + atomic rename.
  - Avoid writing state directly; go through the store to preserve locking/atomicity.
- **Tag configuration**:
  - Tag schema (JSON): `id`, `command`, `env`, `preStart`, `cwd`.
  - Tags are merged from:
    - Global: `$(os.UserConfigDir())/myworktree/tags.json`
    - Project: `.../<repoHash>/tags.json`
- **Security defaults**:
  - Server defaults to loopback listen; non-loopback listen requires `--auth`.
  - Optional TLS is supported via `--tls-cert/--tls-key`.
- **Redaction**:
  - Stored output is redacted for common secret patterns (e.g. `sk-...`).
  - Env values are sanitized based on key names (TOKEN/SECRET/KEY/PASSWORD, etc.).
