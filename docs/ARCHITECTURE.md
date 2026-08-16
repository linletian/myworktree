# myworktree — Architecture

## 1. Overview
myworktree is a lightweight single-user manager for:
- **git worktrees** (isolated working directories)
- **instances** (long-running shell/CLI processes started by myworktree)
- **web UI + HTTP API** to manage them and replay recent output
- **Portal dashboard** (shared entry port with auto-discovery of running instances across repos, reverse proxy, and tailscale serve integration)

It does **not** analyze project code or prevent concurrent write conflicts inside a worktree.

## 2. High-level components
- `cmd/myworktree/` — CLI entry.
- `internal/app/` — HTTP server, auth middleware, API routing.
- `internal/worktree/` — worktree lifecycle via `git` CLI.
- `internal/instance/` — instance lifecycle (spawn/stop/list) + in-memory PTY log ring buffer (see §4). `kind` distinguishes PTY terminal instances (`tty`) from Reasonix web-chat instances (`reasonix`, see §4.2): reasonix instances run a `reasonix serve` subprocess per worktree, managed by `internal/instance/reasonix/` and exposed through the `/rx/<id>/` reverse proxy (`internal/app/reasonix_proxy.go`).
- `internal/tag/` — Tag config loader (MVP: JSON).
- `internal/store/` — persistent state store (`state.json`) with file locking + atomic writes.
- `internal/redact/` — secret redaction applied to PTY chunks before they enter the ring buffer / live broadcast (e.g. `sk-...`).
- `internal/mcp/` — MCP adapter surface (tool names + app-level tool dispatch), keeping core decoupled.
- `internal/monitor/` — resource stats collector (CPU delta via gopsutil/process.Times, memory via RSS)
- `internal/llm/` — LLM API client（OpenAI / Anthropic / OpenAI Compatible），可选，LLM Settings 通过 Web UI 对话框配置
- `internal/config/` — global auth configuration (read/write `auth.json`)
- `internal/portal/` — Portal dashboard (port claiming, instance registry, CSRF state management, HTTP endpoints; **reverse proxy `/s/<repo-hash>/` planned but not yet implemented** — current dashboard links point to instance ports directly; tailscale serve automation code is defined but **currently unused** due to tailscale CLI bug)
- `internal/gitx/` — git CLI wrappers (branch listing, default branch detection, branch divergence detection)
- `internal/ui/` — embedded static UI.

## 3. Data & persistence
### 3.1 Project-scoped user data directory
- Stored under user-level config dir (no repo pollution), partitioned by `repoHash`.
- Contains:

### 3.1.1 Worktree directory layout
- Default: worktrees are created next to the repo under `<repo-name>-myworktree/<worktree-name>/`.
- Override: `-worktrees-dir=data` uses the legacy location under the per-project data dir; you can also set a custom path.
  - `state.json` — managed worktrees + managed instances + tab order + version
  - `tags.json` — project-level tags
  - `logs/` — **no longer used** for live instance output. The directory may still exist on upgraded installs containing dead `.log` artifacts from older versions; the daemon purges them once on startup (see §4). Live PTY output is captured into a per-instance in-memory ring buffer instead.

### 3.1.2 全局配置
- 存储于用户级配置目录：`~/.config/myworktree/config.json`（按 OpenCode 方式，0o600 权限）
- 包含 LLM 配置（protocol、api_key、api_address、model 等）
- 不存于项目目录下，避免污染 git 仓库

### 3.1.3 Global auth & Portal registry
- `~/.config/myworktree/auth.json` — global auth token (0600 permissions, plaintext storage, `internal/config/` package)
- `~/.config/myworktree/<repo-hash>/server.json` — per-instance config (`listen_port`, `instance_id`; `instance_id` is a `pid-timestamp-rand` format unique identifier used for cross-referencing with Portal registry)
- `~/.config/myworktree/portal/` — shared Portal registry directory:
  - `<instance-id>.json` — per-instance registration (instance_id, pid, port, host, repo_name, repo_hash, started_at; **no auth_token**)
  - `portal.json` — current Portal holder (instance_id, port, updated_at)

### 3.2 State model
- Worktree: id, name, path, branch, baseRef, createdAt
- Instance: id, worktreeId, tagId, command, cwd, env (sanitized), kind (`tty`/`reasonix`; empty means `tty`), pid, status, timestamps
  - **Schema note (v0.4.0)**: the legacy `log_path` field was removed when PTY logs moved to memory. `state.json` files written by older versions still load cleanly — the field is silently ignored by JSON decoding. External tools reading `state.json` should drop their dependency on `log_path`.
  - **Schema note (reasonix)**: the `kind` field is `omitempty`, so pre-existing instances serialize without it; missing `kind` decodes as `tty`, so no migration is needed.
- TabOrder (at State level): map of worktree_id to ordered list of instance IDs
- **Version** (at State level): monotonically increasing int64, incremented on every write via `SaveWithVersion`. Used for optimistic locking on concurrent modification detection.
- Main Repo: not persisted; served via `GET /api/main` with live git branch

### 3.3 Main workspace (sidebar)

The sidebar shows a pinned **Main Workspace** item at the top (purple accent), followed by a "Worktrees" divider and the managed worktree list.

- **Main repo**: `GET /api/main` returns `{name, branch, github_url}`. The branch is live — queried via `git rev-parse --abbrev-ref HEAD` (via `gitx.CurrentBranch`). Returns empty string for `branch` on detached HEAD (e.g., CI shallow clones), otherwise returns the current branch name.
- **GitHub link on the main repo row**: When `state.mainRepo.github_url` is non-empty, the sidebar main-workspace row renders a small GitHub Mark icon to the right of the project name. The icon is an `<a target="_blank" rel="noopener noreferrer">` (so middle-click / right-click "Open in new tab" still work), with `event.stopPropagation()` so clicking the icon does not trigger the row's `selectWorktree` handler. The URL is resolved on every `/api/main` request via `gitx.GitHubURL(root)`: it inspects `origin` first, then falls back to iterating `git remote`, normalizes SCP / HTTPS / `ssh://` forms, and returns `""` for any host other than `github.com` (case-insensitive) — GitHub Enterprise and non-Git remotes intentionally do not surface a link. When the field is empty, the icon is omitted entirely (the project name occupies the full row width).
- **Worktrees**: `GET /api/worktrees` also returns live branches — `worktree.Manager.List()` queries `git rev-parse --abbrev-ref HEAD` per worktree path on each call. The `branch` field reflects the currently checked-out branch, not the creation-time branch.
- **Quick actions**: The main repo item and each managed worktree row render two SVG shortcut buttons when the UI is accessed via `localhost` or `127.0.0.1`: one opens Terminal at the target path, the other opens Finder. These buttons are intentionally hidden for remote/browser sessions because the action targets the host machine running `myworktree`, not the remote client device.
- **Server-side boundary**: The backend does not trust the frontend visibility check. `POST /api/worktrees/open-terminal` and `POST /api/worktrees/open-finder` reject non-loopback clients based on the request's remote address, so remote callers cannot trigger host GUI actions by directly invoking the API.
- **Host-side execution**: Clicking a quick action calls backend endpoints that resolve the target path (`"__main__"` maps to the repo root; managed IDs map to the persisted worktree path) and execute macOS host commands. Terminal uses `open -a Terminal <path>`. Finder uses `osascript` to ask Finder to open the POSIX path and activate the app, which is more reliable for visibly bringing a Finder window forward.
- **Instance routing**: Use `worktree_id: "__main__"` (constant: `instance.MainWorktreeID`) in `POST /api/instances` to start an instance in the main repo root. The instance's `worktree_id` will be `"__main__"` and `worktree_name` will be the directory basename.
- **Auto-select**: On first load, the UI auto-selects the first worktree; if no worktrees exist, it selects the main repo.
- **Refresh**: All branch info (main repo + worktrees) updates via the existing 2-second polling.
- **Divergence labels**: Each non-main worktree item in the sidebar displays compact red labels (e.g. `m↑3`, `d↑1`) next to its branch name, indicating how many commits the upstream branch (main or develop) is ahead. Labels are refreshed every 60 seconds and immediately when the user selects a different worktree. This helps users verify whether their worktree base is up-to-date before starting new work. See `docs/plans/git-commit-history-graph/DESIGN.md` for details.
- **Git Changes panel**: Below the worktree list, a read-only panel shows changed files for the currently selected worktree, split into two mutually exclusive accordion sections: **Staged** (changes in the index via `git diff --cached --numstat`) and **Unstaged** (working tree changes via `git diff --numstat`). The panel auto-refreshes every 10 seconds and on worktree selection change. The main repo's changes also refresh when its branch changes. Both git commands run concurrently on the server with a 2-second timeout each. The accordion defaults to showing Unstaged; clicking either header expands that section and collapses the other. Empty sections still show their header with a "No staged changes" / "No unstaged changes" message.

## 4. Instance lifecycle & reconnect semantics
- An instance is a server-managed process; UI windows are merely views.
- Default interactive path is WebSocket TTY: `GET /api/instances/tty/ws?id=...` (bi-directional terminal stream).
  - **Handshake Protocol**: Server sends `{"type":"ready"}` on connect; client must wait for this before sending resize to start data flow.
  - **Timeout Handling**: Client implements 5s handshake timeout with SSE fallback.
  - **Resize Support**: Client sends `{"type":"resize","cols":80,"rows":24}` to update PTY size, triggering TUI programs to redraw.
  - **Dual Resize**: First resize starts data flow, second resize (50ms after first data) ensures complete TUI redraw.
  - **Client-side Config**: Frontend uses xterm.js for terminal rendering. Terminal buffer size (`scrollback`) is configurable on the client side to control how much history is retained in memory for scrolling. This is a client-side setting and does not affect server-side log persistence.
- The frontend keeps a **per-instance terminal session** for running instances. Each running instance owns its own xterm.js instance, transport state, timers, and reconnect logic.
- Switching between running instances hides inactive terminal containers instead of tearing down their PTY attachment. This avoids detaching long-lived TUI programs such as Copilot CLI while they remain running.
- Fallback path remains available: HTTP input `POST /api/instances/input` + replay/SSE logs (`GET /api/instances/log`, `GET /api/instances/log/stream`).
- UI shows transport state (`websocket/sse/polling`) and supports manual WS reconnect.
- PTY output is captured into a per-instance **in-memory ring buffer**; no disk I/O is involved on the steady state. The buffer feeds the HTTP/SSE/WS replay endpoints and the MCP `instance_log_tail` tool. See §4.1 for sizing, eviction, and budget rules.
- On server startup, stale persisted `running` records are reconciled to `stopped` because in-memory stdin/stdout bindings cannot be resumed after process restart. The same startup pass also calls `Manager.PurgeOrphanLogFiles()` to remove dead `.log` files left behind by pre-buffer versions; missing or empty `logs/` directories are not an error.
  - **Exception (reasonix, `RestartSurvivor`)**: `ReconcileRunningOnStartup` asks reasonix-kind instances to re-attach (`reasonix.Kind.Reattach` → `Driver.Health()`: pid liveness + TCP port). A live serve subprocess survives the restart and is kept `running` (its handle is registered back into the framework so Stop/Delete still manage it); a dead one is marked `stopped`. This prevents the restart-then-delete sequence from orphaning a serve process that holds the symlinked provider credentials.
- **Tag semantics (restored for all kinds)**: starting an instance with a `tag_id` resolves the tag exactly like the pre-kind-refactor Manager — `tag.Env` is merged into the spawned process environment, `tag.preStart` runs right before spawn with the same environment the process will get (aborting Start on failure; for reasonix it runs with `REASONIX_HOME`/`REASONIX_STATE_HOME` stripped exactly like serve), `tag.Command` is sent as the PTY initial input (an ad-hoc `command` without a tag likewise), and `tag.Cwd` is joined onto the worktree path. `tag.Command` is ignored by `opencode-web` and `reasonix`. The resolved values are persisted on the instance record (`command` / `cwd` / `env`), and the record's `tag_id` stores the effective id (`<tag>` / `adhoc` / `idle`).
- **Rename**: `PATCH /api/instances` updates an instance's display name (`name` field). The rename takes effect immediately in the UI and persists to `state.json`.
- **Tab ordering**: `PATCH /api/instances/reorder` persists per-worktree tab order to `state.json` (`tab_order` map + array order in `State.Instances`). Uses **optimistic locking** — the client sends the `version` observed from `GET /api/instances`. If the state has been modified since (e.g., another user started an instance), the server returns HTTP 409 Conflict and the client refreshes and retries.
- **Resource monitoring**: A clickable transport status bar in the bottom-right of the workspace opens a resource monitor modal. The modal shows per-instance CPU%, memory RSS, ring buffer usage (actual / capacity), and connection type (WebSocket/SSE) grouped by worktree, with subtotals and a global summary. The global totals include the mw daemon process itself (`daemon_cpu_percent`, `daemon_memory_bytes`). Data is fetched via `GET /api/instances/stats` (1-second polling when open, stops when closed). CPU% uses delta calculation from `process.Times()` with a per-PID baseline stored in the `Collector` struct. The UI includes a disclaimer that grandchild processes spawned inside instances are not individually tracked.
- **Browser close protection**: The frontend registers a `beforeunload` event handler that unconditionally triggers a browser-native confirmation dialog on any page close/refresh/navigation attempt. This is purely a client-side UX safeguard — backend instances are unaffected and continue running.

### 4.1 Instance log buffer (in-memory)

Each running instance owns a bounded, **in-memory** ring buffer (`internal/instance/logbuf.go`, `RingBuffer`) that captures the PTY output stream emitted by `pumpLogs`. This buffer is the **only** backing store for the log replay endpoints, the SSE live-stream endpoint, and the MCP `instance_log_tail` tool. There is no persistent on-disk log file.

**Why in-memory.** The previous design wrote every 1024-byte PTY chunk to a per-instance `<id>.log` file and called `enforceMaxLogSize` after each write; once a file reached the 10 MB cap, every subsequent chunk triggered a full `read 10 MB + truncate + write 10 MB` pass — write amplification on the order of 100,000× under TUI redraw workloads. Removing disk persistence eliminates the bug at the source and is consistent with the existing reconcile-on-startup semantics, which already declare that logs cannot be replayed across daemon restarts (running instances are marked `stopped`, their PTY channels are not resumed).

**Sizing (per-instance cap).**
- Floor: 16 MB (`MinBufferCap`)
- Default: 32 MB (`DefaultBufferCap`)
- Ceiling: 256 MB (`MaxBufferCap`) — never exceeded
- Adaptive: when no user override is set, the cap is `clamp(available_memory / 16, 16 MB, 256 MB)`, sampled via `gopsutil/v4/mem.VirtualMemory()` at instance start.
- User override: `log_buffer_bytes` in `~/.config/myworktree/auth.json` (`GlobalConfig.LogBufferBytes`). When set, the value is clamped to `[16 MB, 256 MB]`.
- The backing slice is allocated eagerly inside `NewRingBuffer` so the first `Write` does not stall the producer on a 32 MB malloc.

**Global budget.** The sum of all live buffer caps is bounded by `MaxTotalFraction × system_RAM` (default 25%). When `Manager.Start` would push the sum over this limit, it returns `*instance.LogBufferBudgetError` (which wraps the sentinel `ErrLogBufferBudgetExceeded`). The HTTP layer translates this into `503 Service Unavailable` with the structured `log_buffer_budget_exceeded` body documented in `docs/API.md` (Start endpoint). The MCP path returns the same shape. The budget check runs **before** `exec.Command` / `pty.Start`, so a rejected request leaves no orphan processes or PTYs to clean up.

**Cursor semantics (`since` / `next`).** `head` is a monotonic total-bytes-written counter for the instance. Clients pass it as `since` to read incrementally; the server returns the bytes plus an advanced cursor. When no new data is available, the cursor is returned unchanged (preserves the SSE 1 s poll-loop contract). When `since` points to data already evicted from the ring (oldest live byte > since), the read silently clamps to the oldest live byte.

**Lifecycle.**
- `Start` resolves the cap with budget enforcement, creates the buffer, adds it to `Manager.buffers[id]` (an `*atomic.Pointer[RingBuffer]`), and atomically increments `Manager.totalBufBytes`.
- `pumpLogs` reads PTY chunks, redacts them, and writes to the buffer via the pre-fetched pointer using a single `atomic.Pointer.Load` per chunk — no `stateMu` acquisition on the hot path. The redacted chunk is also broadcast to live subscribers (WS/SSE).
- `Stop` / `wait` / `Restart` / `Delete` all funnel into `dropBufferLocked`, which `Swap(nil)` on the pointer, closes the buffer, decrements `totalBufBytes`, and removes the map entry. In-flight `pumpLogs` chunks dropped after the swap are intentional: the same lifecycle event closes the PTY, so `Read` returns EOF and the goroutine exits naturally.

**Startup purge.** `Manager.PurgeOrphanLogFiles()` is invoked once during `Server.Start` (after `ReconcileRunningOnStartup`). It removes every `*.log` file under `DataDir/logs/` — these are now dead artifacts left by the pre-buffer code path. A missing `logs/` directory is not an error; the count of removed files is logged.

**Failure modes & user-facing knobs.**
- Budget exceeded → 503 with `log_buffer_budget_exceeded`. The dashboard surfaces a modal showing `used_bytes` / `limit_bytes` and a hint to raise `LogBufferBytes` or close other tabs.
- Memory sampler fails (e.g. unusual cgroup) → cap falls back to `DefaultBufferCap`; the budget check is skipped for that call.
- Daemon restart → all buffers are gone. The next `GET /api/instances/log` for a stopped/never-started instance returns empty (documented behaviour).

### 4.2 Reasonix web-chat instances

`kind: "reasonix"` instances run a `reasonix serve` subprocess in the worktree instead of a PTY shell, and the UI embeds the Reasonix web chat page in an iframe.

- **Lifecycle**: `framework.Manager.Start` with `kind=reasonix` dispatches to the reasonix kind (`internal/instance/reasonix/kind.go`, wrapping `internal/instance/reasonix.Driver.Start`): it launches `reasonix serve --addr 127.0.0.1:0 --auth token --token-file … --port-file … --pid-file … --no-open` with `cwd` = worktree path (no `--resume`, no `REASONIX_HOME`). The actual port is read back from the port file (port 0 = kernel-assigned). `Stop` signals the process group (TERM → KILL) and tears down the per-instance serve-management state dir. `Restart` allocates a fresh instance id and cleans the old state dir (Restart = a brand-new session, matching the terminal "new session" model). `Delete` (after stop) removes the per-instance serve-management state dir via the kind's `Cleanup` (best-effort, includes a Stop for stale-stopped records whose process outlived the marking); it never touches the shared `~/.reasonix` session pool. On server shutdown `framework.Manager.StopAllKind("reasonix")` stops every running serve (parity with tty instances, which die when their PTY hangs up).
- **Version gate & readiness**: before spawning, `Driver.Start` runs `reasonix --version` and rejects CLIs older than `1.22.0` (`Driver.MinVersion` overrides) with a readable error instead of a 15s timeout (issue #45); readiness/early-exit errors include the `serve.log` tail. Tag `env` is injected into the serve process and tag `preStart` runs before serve (aborting on failure); the tag `command` is deliberately not executed (issue DEFERRED §3).
- **No session isolation**: the serve runs **without** a `REASONIX_HOME` override — it uses the user's real `~/.reasonix`, exactly like a terminal-run reasonix. Sessions/history/config/credentials are shared per project: reasonix organizes them under `~/.reasonix/projects/<cwd-slug>/sessions` and isolates projects by cwd itself, so the same project's full history (including sessions created by terminal CLI/TUI) is visible and switchable in the embedded sidebar. Each Start opens a fresh session (no `--resume`); concurrency is handled by reasonix's own per-session-file lease (refuse-style, no silent double-write). `reasonix serve` must be on `PATH` (or configured via `Driver.ReasonixBin`).
- **Proxy**: `internal/app/reasonix_proxy.go` serves `/rx/<id>/…`. When the main listener is **loopback-only** (and no TLS) it is mounted on a dedicated loopback listener (issue #44) and the frontend iframe uses the `web_url` reported by `/api/instances` — the embedded page is **cross-origin** with the myworktree API, so content inside the chat iframe cannot silently call `/api/*` with the user's session. In **TLS mode or with a network-open main listener** (default `0.0.0.0`, or an explicit LAN IP — an absolute `http://127.0.0.1:<port>` web_url would be unreachable from a remote browser) the independent listener is skipped and the iframe falls back to the **relative** same-origin `/rx/<id>/` route, which follows the browser's current origin and keeps LAN/remote access working. The proxy injects the `reasonix_token` cookie (`reasonix.CookieName`, single source), streams SSE, and injects at one HTML point the URL-prefix shim plus the issue #48 layout: desktop sidebar **collapsed by default** with a toggle (`--mw-sidebar-w` width), native mobile behavior untouched. Backend port/token come from a per-instance in-memory cache (issue #46), so the proxy path does not read files per request.
- **Liveness**: `Driver.Health` = pid alive (`kill(pid,0)`) **and** TCP connect to the recorded port succeeds — cross-platform (no `/proc`), so it also works on macOS.
- **Restart reconciliation**: see the exception note in §4 — via the `RestartSurvivor` optional interface, a live serve subprocess is re-attached as `running` on startup (its handle is registered back into the framework so Stop/Delete keep managing it); dead ones are marked `stopped`.

> **Requirement revision (2026-08-12, see docs/PRD.md §7) — implemented**: the per-instance `REASONIX_HOME` isolation this section previously described is **removed**. myworktree does not intervene in the agent's product logic: sessions live in the agent's own state root (`~/.reasonix`), organized per project by reasonix itself, and instance lifecycle never touches the shared session pool. Issue #49's "Restart = fresh session" semantics stay; issue #56 (sidebar history/branches empty) is resolved.
>
> **Upstream contract (verified on reasonix v1.22.0, 2026-08-12)**: the shared-pool model depends on reasonix's own behavior — sessions organized under `~/.reasonix/projects/<cwd-slug>/sessions`, per-cwd project isolation, and per-session-file lease mutual exclusion (refuse-style: a second `--resume` of a held session is rejected, never silently double-written). The driver only gates the CLI version (≥1.22.0) and does not enforce these internals; **owner: the reasonix driver maintainer (this repo's maintainer). Trigger: any `reasonix` upgrade must re-verify this contract** — run `scripts/verify-reasonix-contract.sh` (automated contract probes: version gate, per-cwd session layout, fresh-session uniqueness, lease refusal). It is also reachable from `go test` as an **opt-in** (`TestReasonixUpstreamContract`, enabled with `REASONIX_CONTRACT=1`, see its doc comment — it spawns real `reasonix serve` subprocesses, so it is off by default). CI: `.github/workflows/reasonix-contract.yml` is currently a **placeholder** — GitHub-hosted runners carry no reasonix binary and the job has no install step yet, so it SKIPs (green) and verifies nothing until a binary is installed there; the go-ci job runs the same opt-in check, which likewise skips on runners without reasonix. Then re-verify with a real-browser sidebar check that the same project's history/branches still load. Legacy pre-2026-08-12 instances may keep an inert `home/` + `session.jsonl` (driver logs a hint on Start; not auto-migrated — see `docs/plans/reasonix-native-ui/FEASIBILITY.md` revision note).

## 5. Terminal Protocol Timing Specification

This section defines the strict timing protocol for terminal I/O to prevent escape sequence leakage and ensure reliable data flow.

### 5.1 Terminal Connection State Machine

The terminal connection goes through the following states:

```
IDLE → CONNECTING → HANDSHAKING → READY → DISCONNECTING → IDLE
```

| State | Description | Allowed Actions |
|-------|-------------|-----------------|
| IDLE | No active connection | May initiate connection |
| CONNECTING | WebSocket opening in progress | None (wait) |
| HANDSHAKING | WebSocket open, waiting for server `ready` message | None (wait) |
| READY | Connection fully established | Send input, receive output, resize |
| DISCONNECTING | Connection closing in progress | None (wait) |

### 5.2 WebSocket Handshake Protocol

**Server → Client (on WebSocket open):**
```json
{"type": "ready"}
```

The client MUST wait for this message before:
- Sending any input data
- Sending resize commands
- Focusing the terminal (to prevent query sequence leakage)

**Client → Server (after receiving `ready`):**
```json
{"type": "resize", "cols": 80, "rows": 24}
```

**Timeout:** Client MUST implement a 5-second handshake timeout. If `ready` is not received, fall back to SSE mode.

### 5.3 Instance Switch Protocol

When switching between instances (worktree or instance tabs), the frontend distinguishes between stopped and running instances:

```
Stopped instance
├── 1. Ensure terminal session exists for the instance
├── 2. Disconnect live transport for that stopped session
├── 3. Replay persisted log into that session
└── 4. Show stopped styling/banner

Running instance with healthy session
├── 1. Keep the existing per-instance xterm/WS session alive
├── 2. Hide previously active terminal containers
├── 3. Show the selected instance container
├── 4. Re-fit and resize the active terminal
└── 5. Focus only after the session is READY

Running instance without healthy session
├── 1. Ensure terminal session exists for the instance
├── 2. Reset that instance's local terminal state
├── 3. Replay recent log once into that same instance session
├── 4. Establish WebSocket/SSE transport for that session
├── 5. Wait for server {"type":"ready"} message
├── 6. Send resize: {"type":"resize","cols":N,"rows":M}
└── 7. Focus only after the session is READY
```

**Critical Timing Rules:**
1. Running instances MUST NOT be detached purely because another instance becomes active.
2. Each running instance owns its own frontend terminal session; TUI modes are isolated by session rather than cleared out of a shared xterm.
3. The terminal MUST NOT receive focus until that instance session is READY (after `ready` is received and resize is sent).

### 5.4 Focus Management Rules

The terminal focus is managed by `focusTerminalIfPossible()` which checks:

```javascript
function focusTerminalIfPossible() {
    if (!term) return;                    // No terminal instance
    if (!windowHasFocus) return;          // Browser window not focused
    if (openDialogs.size > 0) return;     // Modal dialog is open
    if (!state.activeInst) return;        // No active instance
    if (ttyState !== 'READY') return;     // ⚠️ Connection not ready
    const inst = state.instances.find(i => i.id === state.activeInst);
    if (inst && inst.status !== 'running') return; // Instance not running
    
    term.focus();
}
```

**The `ttyState` check is CRITICAL** - it prevents focus before the WebSocket is ready, which would cause xterm.js to send terminal query sequences (OSC 11, DA, DEC Private Mode queries) before the data path is established.

### 5.5 Terminal Query Sequence Prevention

When xterm.js gains focus, it sends query sequences to detect terminal capabilities:
- `OSC 11` - Background color query → Response: `\x1b]11;rgb:...`
- `OSC 4` - Palette color query → Response: `\x1b]4;...;rgb:...`
- `DA (Device Attributes)` → Response: `\x1b[?1;2c`
- `DEC Private Mode Report` → Response: `\x1b[?2027;0$y...`

If these responses arrive before the WebSocket is in READY state, they may be incorrectly routed or displayed as garbage text.

**Prevention Strategy:**
1. Never focus terminal before WebSocket is READY
2. Always complete handshake before sending resize
3. Send resize before focusing (this triggers any needed redraw)

### 5.6 Error Handling & Recovery

| Scenario | Detection | Recovery Action |
|----------|-----------|-----------------|
| Handshake timeout | No `ready` in 5s | Close WS, fallback to SSE |
| WebSocket close | onclose event | Retry after 1s delay |
| Instance stopped | status !== 'running' | Disconnect WS, show log with banner |
| Browser blur | blur event | Track windowHasFocus = false |
| Dialog open | showModal intercept | Track openDialogs.add() |

### 5.7 Protocol Message Summary

**Server → Client Messages:**

| Message | Format | When |
|---------|--------|------|
| Ready | `{"type":"ready"}` | Immediately after WebSocket opens |
| Output (binary) | `ArrayBuffer` | PTY output available |
| Output (text) | `string` | PTY output available |

**Client → Server Messages:**

| Message | Format | When |
|---------|--------|------|
| Resize | `{"type":"resize","cols":N,"rows":M}` | After `ready`, on terminal resize |
| Input | raw string/bytes | User keystroke (anytime after `ready`) |

### 5.8 Implementation Checklist

When implementing or modifying terminal connection code, verify:

- [ ] `focusTerminalIfPossible()` checks connection state before focusing
- [ ] WebSocket handshake timeout (5s) is implemented
- [ ] `ready` message triggers resize BEFORE focus
- [ ] Running instance switch preserves inactive session attachments
- [ ] Dialog close delays focus until connection is ready
- [ ] Window focus event respects connection state
- [ ] `beforeunload` handler triggers browser confirmation on any page close/refresh/navigation

### 5.9 Terminal Query Response Filtering

#### Background & Problem Statement

When xterm.js receives terminal query sequences (e.g., `ESC[c` for Device Attributes), it generates responses (e.g., `ESC[?1;2c`) and sends them via the `term.onData()` callback. These responses are intended to be forwarded to the PTY, which then interprets them.

However, in certain scenarios, these responses may:
1. Be displayed as garbage text in the terminal
2. Confuse shell programs (zsh, bash) that receive unexpected input
3. Appear as split sequences due to data fragmentation

#### Observed Behavior

After a TUI program (like `opencode` CLI) exits and returns to the shell prompt:
- The shell may send terminal capability queries
- xterm.js responds to these queries
- The responses are sent to PTY via `term.onData()`
- The shell displays these responses as text: `;1R`, `rgb:0b0b/1010/2020`, `?2027;0$y`

#### Root Cause Analysis

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Data Flow Diagram                               │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  [Shell in PTY]                                                     │
│       │                                                             │
│       │ sends query: ESC[c (Device Attributes)                     │
│       ▼                                                             │
│  [PTY stdout] ──► [WebSocket] ──► [xterm.js term.write()]           │
│                                           │                         │
│                                           │ xterm.js generates      │
│                                           │ response: ESC[?1;2c    │
│                                           ▼                         │
│                                    [term.onData()]                  │
│                                           │                         │
│                                           │ forwarded back          │
│                                           ▼                         │
│  [Shell in PTY] ◄── [WebSocket] ◄── [SendInput]                    │
│       │                                                             │
│       │ shell receives unexpected input                             │
│       ▼                                                             │
│  [Displayed as garbage text]                                        │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

The issue occurs because:
1. Shell sends terminal query to check capabilities (normal behavior)
2. xterm.js receives query and generates response
3. Response is forwarded back to PTY via `term.onData()` → `SendInput()`
4. Shell receives response as keyboard input and displays it

#### Current Mitigation

The frontend implements a response filter in `term.onData()`:

```javascript
term.onData(data => {
    // Filter out terminal query responses
    if (isTerminalQueryResponse(data)) {
        console.debug('Filtered terminal response:', data.length, 'bytes');
        return;
    }
    // ... forward to WebSocket
});
```

The `isTerminalQueryResponse()` function detects:
- Complete sequences with ESC prefix: `ESC]11;rgb:...`, `ESC[?1;2c`, `ESC[?2027;0$y`
- Partial sequences (split or fragmented): `;1R`, `rgb:...`, `?2027;0$y`

Additionally, output sanitization strips `ESC[?1007h` (DECSET alternate scroll mode) so wheel events are not translated into Up/Down key input when returning to shell contexts.

#### Known Limitations & Risks

**⚠️ This implementation has limitations documented in `docs/TERMINAL_FILTER_REVIEW.md`:**

1. **Cannot distinguish user intent**: If a user intentionally sends `ESC[c` to query terminal attributes, it will be filtered as well.

2. **Regex limitations**: CSI patterns may have edge cases not covered.

3. **False positives on small coordinates**: Cursor position reports with small coordinates may be incorrectly filtered.

4. **Incomplete coverage**: Some terminal response types (DSR, etc.) are not explicitly handled.

5. **Architecture consideration**: The filter is placed in the input path (`term.onData`), which handles user keystrokes. See the review document for detailed analysis of input vs output path considerations.

#### Future Considerations

If issues arise with the current filtering approach:
1. Refer to `docs/TERMINAL_FILTER_REVIEW.md` for detailed analysis
2. Consider alternative approaches:
   - Move filtering to the output path (log filtering)
   - Configure xterm.js to suppress automatic query responses
   - Add user intent detection (keyed sequences vs automatic responses)
3. Update `docs/TERMINAL_IO_ANALYSIS.md` with any new findings

#### Related Documents

- `docs/TERMINAL_FILTER_REVIEW.md` - Detailed review of current implementation
- `docs/TERMINAL_IO_ANALYSIS.md` - Terminal I/O architecture and filtering
- `docs/TERMINAL_TEST_CASES.md` - Test cases for terminal behavior

## 6. Security model (single-user, dual-layer)

myworktree implements a **dual-layer authentication architecture**:

**Layer 1 — Portal (public-facing)**:
- `mw_token` HttpOnly Cookie-based authentication (JS cannot read token, prevents XSS theft)
- Double-submit cookie CSRF protection on `/api/auth` and `/api/logout` endpoints
- CSRF token: single-use, 5-minute TTL, IP rate-limited (1 req/s)
- Cookie: 24-hour sliding expiration, `SameSite=Lax`, `Secure` flag on HTTPS
- `POST /api/auth` rate-limited per IP (20 attempts/min)
- `GET /` dashboard page served with strict CSP headers (hash-based inline script/style whitelist)

**Layer 2 — Instance (loopback-bypassed via proxy)**:
- Default listen: loopback only, **IPv6 explicitly disabled**
- Non-loopback requires `--auth`
- Loopback requests skip all token/origin validation (enables Portal reverse proxy)
- Origin/Host check + basic rate limit on unauthorized non-loopback attempts
- Optional built-in HTTPS via `--tls-cert/--tls-key`
- Redaction is applied on each PTY chunk *before* it lands in the in-memory ring buffer or is broadcast to live subscribers (e.g. `sk-...` masked). The replayed/streamed bytes never contain the raw secret.

**Proxy authentication bypass (planned)**: The planned Portal reverse proxy (`/s/<repo-hash>/`) will forward requests to instances via `127.0.0.1` (loopback), so instances automatically skip auth. **Currently not yet implemented** — dashboard links connect to instance ports directly.

**Tailscale**: WireGuard tunnel provides network-layer encryption. ~~Portal holder automatically manages `tailscale serve` for HTTPS domain access (`https://<machine>.ts.net`) with Let's Encrypt certificates.~~ **Currently disabled** — tailscale CLI `serve` command on macOS returns success but does not actually configure the proxy. The `tailscaleServeLoop` goroutine and related `cleanupStaleTailscaleServe()` call are removed from the production code path. Users can still securely access Portal via Tailscale IP (`http://100.x.x.x:12345`) over the WireGuard tunnel.

### CLI Flags & Configuration

| Flag / Command | Description |
|---------------|-------------|
| `--portal-port <int>` | Portal claim target port (default: 12345; 0 = disable Portal) |
| `--auth <token>` | Per-instance auth token (overrides global token) |
| `mw config` | Interactive guided setup for global auth token |
| `mw config set-auth` | Set global token (hidden echo + confirmation) |
| `mw config get-auth` | View token (masked: first 4 + `****` + last 4 chars) |
| `mw config clear-auth` | Clear global token (no confirmation) |

**Auth token auto-fill**: When `--auth` is empty, `startCmd` automatically loads from `~/.config/myworktree/auth.json`. If the file is corrupted, a warning is logged but startup continues (non-loopback listen will then be rejected by `validateSecurity()`).

## 7. MCP extensibility
- Core managers (worktree/instance) are transport-agnostic.
- `internal/mcp` exposes tool names; server dispatch maps tool calls to existing core managers without rewriting core.

## 8. opencode-web integration

myworktree can host `opencode serve` processes as managed instances, embedding opencode's official web UI via reverse proxy instead of the PTY + xterm.js path.

```
Browser (iframe src = /__opencode/<id>/ — full-page SPA root)
  │ GET /__opencode/<id>/
  ▼
myworktree mux (withAuth + Token/Cookie)
  │ Strip /__opencode → ReverseProxy → inject Basic auth + ?directory=<worktree>
  │   + classify request directory vs instance worktree → in/out-of-scope
  │   + rewrite HTML (assets re-route, <base> + injected script, CSP hash)
  ▼
opencode serve (127.0.0.1:<port>)
  │ serve opencode SPA → router matches HomeRoute (full page)
  ▼
opencode web app (SolidJS, rendered in iframe — single-worktree view; cross-worktree switch entries hidden)
```

### Design constraints

- **One process per instance**: each `opencode-web` instance = one independent `opencode serve` process. Multiple instances per worktree supported; each has its own session history, provider state, and plugin context.
- **Command locked**: the command, `--hostname 127.0.0.1`, and `--port 0` are hardcoded in Go (user cannot override via `tags.json`). LAN exposure requires running opencode outside myworktree or as a PTY-backed tag.
- **统一认证 token（unified auth token）**: every opencode-web instance's `OPENCODE_SERVER_PASSWORD = cfg.AuthToken`. Users cannot turn it off or override it (`buildEnv` always forces this value). The reverse proxy injects Basic auth with the same `cfg.AuthToken` when forwarding to upstream — upstream and myworktree mux share one credential.
- **Non-security env from tag**: `tag.Env` (e.g., `OPENCODE_EXPERIMENTAL`) is merged into the process environment via `buildEnv`.
- **Coexists with PTY**: existing PTY instances are unchanged. The frontend branches on `instance.kind`: `"pty"` → xterm.js, `"opencode-web"` → iframe.
- **No new dependencies**: proxy implemented with `net/http/httputil.ReverseProxy` (stdlib). No third-party Go packages.
- **单 worktree 视角（single-worktree view）**: the reverse proxy classifies every directory-bearing request against the instance worktree and records out-of-scope drift in an in-memory `ScopeTracker`; an injected script hides cross-worktree switch entries (project switch / add-project / open-project) and normalizes the localStorage server list to a single server. This is防误操作 (accident-prevention), not a hard boundary — the user can still reach other directories, and any such navigation surfaces a persistent warning bar. Full design and decision record in `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md`.
- **版本门 + 隐藏有效性兜底（version gate + hide-effectiveness）**: the injected hide script targets `1.18.x` DOM anchors. A spawn-time `opencode --version` probe (advisory — never blocks startup), plus in-page DOM-anchor / visibility checks reported via `postMessage` and a CSP-anchor drift flag, surface a persistent "hiding not effective" warning when opencode upgrades break the hiding.

### Threat model & trust boundary (unified auth token)

The current security model relies on these assumptions:

- **Loopback isolation**: every `opencode serve` binds `127.0.0.1:<port>` (the invocation is hardcoded in `internal/instance/opencode_web/driver.go` `Spawn`). The upstream HTTP server is not directly reachable from the network.
- **Single credential**: `OPENCODE_SERVER_PASSWORD = cfg.AuthToken`, and the reverse proxy injects Basic auth with the same token. Anyone holding the token has full upstream access to every opencode-web instance; the only remaining gate is the bearer/cookie check at the myworktree mux.
- **Unprivileged remote attackers** (LAN/Wi-Fi sniffer, cloud-sync adversary, dotfiles-repo leak, issue tracker / CI log exposure, sibling-vhost XSS, etc.) gain no new external attack surface from the credential merge — they still only reach the proxy through `/__opencode/<id>/*` and still must pass the mux. Upstream `127.0.0.1` stays unreachable from off-machine.
- **The real amplification is "in-trust-zone but crossing the loopback boundary"**: any future code path that exposes `127.0.0.1:<opencode-port>` outside the loopback namespace — container with `--net=host` or shared netns, reverse-proxy port forward, debug handler returning host/port for direct connection, MCP tool / worker that talks to upstream without going through the mux, SSH / local-tunnel documentation — turns a single token leak into full compromise of every instance's upstream **and** the entire API. Such paths must be reviewed against this threat model before introduction.
- **In-memory footprint**: `cfg.AuthToken` now lives in the myworktree daemon, in every spawned `opencode serve` process's `cmd.Env` (as `OPENCODE_SERVER_PASSWORD`), and in any goroutine / struct that captures it. Same-user reads of `/proc/<pid>/environ` get it. This does not weaken the model **as long as the trust zone does not change**; if it does, all of those copies leak together rather than per-instance.

### Review checklist (token leak paths)

Every review that touches authentication, proxy, token handling, opencode-web, or portal paths must verify:

1. **Disk**: `~/.config/myworktree/auth.json` (and any future token file) is `0o600` and only read by the owner.
2. **Logs**: portal / daemon startup logs, access logs, reverse-proxy logs do not print `cfg.AuthToken` in plaintext. Note existing leak at `internal/portal/portal.go:211` (`Remote access token: %s`).
3. **Process memory**: `cfg.AuthToken` is not written to `os/exec.Cmd.Env` of any subprocess, captured in goroutine closures, or held in struct fields longer than needed; new subprocesses do not inherit it unintentionally.
4. **Network**: no endpoint outside the `withAuth` mux forwards to `127.0.0.1:<opencode-port>`; `mw_token` cookie's `Secure` / `Domain` / `Path` are tightened for the deployment (TLS termination, reverse proxy).
5. **Error responses**: API errors, panic messages, and log lines do not echo the token or `Authorization` header.
6. **Test data**: tests, mocks, fixtures do not embed real-form tokens; CI logs do not surface them.
7. **Cross-trust-zone candidates**: any new debug handler, MCP tool, worker, container network config, or SSH / local-tunnel documentation that touches `127.0.0.1:<opencode-port>` or `cfg.AuthToken` — must be re-evaluated against the threat model above before merge.
8. **iframe URL credential path (added 2026-08-14)**: embedded iframe documents must never receive `cfg.AuthToken` in their own URL (`location.search`) — a script inside the embed could read it. Remote access works because `withAuth` syncs the address-bar `?token=` into the HttpOnly `mw_token` cookie on the response, and the panels navigate with plain relative URLs (`/rx/<id>/`, `/__opencode/<id>/`). The portal mirrors this: `portal.withAuth` accepts `?token=` on `/api/list` and writes the same cookie. Any new embed or panel must use the cookie, not a token-bearing URL; the two `withAuth` implementations (daemon + portal) must stay aligned — tighten or loosen one, update the other in the same change.
9. **Reverse-proxy token scrubbing (added 2026-08-14)**: every reverse proxy that forwards a browser query upstream must run the query through `authq.StripToken` — a forgotten path leaks the credential to the upstream subprocess. When adding a proxied route, grep the proxy code for raw `RawQuery` assignments; do not re-implement stripping locally.

### Related files

- `internal/instance/opencode_web/driver.go` — Kind implementation (`opencode serve` spawn, listening-address scan, health probe, `--version` probe)
- `internal/instance/opencode_web/proxy.go` — reverse proxy + HTML injection (`/__opencode/<id>/*`, `rewriteRootAttrs`, `buildInjectScript`, CSP hash)
- `internal/instance/opencode_web/scope.go` — directory classification + `ScopeTracker`
- `internal/instance/opencode_web/version.go` — version-gate helpers (`parseVersion`, `isSupportedVersion`)
- `internal/app/app.go` — API endpoints `GET /api/instances/<id>/opencode` and `GET /api/instances/opencode/scope`
- `internal/ui/static/index.html` + `internal/ui/static/kinds/opencode_web.js` — iframe panel, warning bar, scope polling + hidden-report listener
- `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` — full design + decision record

## 9. dsh-web integration

myworktree can host `dsh web` (DeepSeek Harness's headless HTTP server + embedded SPA) as managed instances, embedding the official browser UI via a **per-instance dedicated loopback origin** — unlike opencode-web (`/__opencode/<id>/` same-origin mount), dsh's SPA hardcodes its API base to `location.origin + '/api'` with no override, so a same-origin subpath mount would collide with myworktree's own `/api`. Decision record: `docs/plans/dsh-native-ui/FEASIBILITY.md`; implementation plan: `docs/plans/dsh-native-ui/PLAN.md`.

```
Browser (myworktree UI, main origin)
  └─ iframe src = http://127.0.0.1:<proxyPort>/          ← dedicated loopback origin (local mode)
       │   (remote mode: the SERVER appends ?token= to iframe_src — the proxy
       │    validates, sets the HttpOnly cookie on its origin, and 302-redirects
       │    token-free; loopback clients bypass the gate like the main UI)
       ▼
myworktree dsh_web.LoopbackProxy (per-instance net.Listen 127.0.0.1:<free>)
  │ Director: Host = upstream loopback, delete Origin, authq.StripToken
  │ WS Upgrade passthrough (/api/events.mux, /api/events.host), FlushInterval=-1
  │ POST /api body → parse RPC envelope → scope Record + session own-attribution
  │   (record-only, never blocks/rewrites); mid-body failure → abort connection
  ▼
dsh web --patch <restrict.yml> --host 127.0.0.1 --port 0   (launcher flags first — see PLAN.md §踩坑 11; cmd.Dir = worktree, env inherited)
  │ restrict.yml: storage-json.root → <dataDir>/dsh/<worktreeHash>/storages
  │               directory-picker disabled + insert directory-picker-browse
  │               (host backend only: keeps the directoryPicker service the
  │               api-gateway depends on; no client surface → no "Add workspace…")
  │               client-hmr disabled
  ▼
dsh SPA (rendered in iframe — workspace registry isolated per worktree;
  cross-worktree sessions appear as 未分组: visible, not clickable)
```

### Design constraints

- **One process per instance**: each `dsh-web` instance = one independent `dsh web` subprocess (`--port 0` — dsh fails loud on port conflict, no fallback). Multiple instances per worktree supported; each has its own upstream port and its own proxy listener.
- **Command locked**: the command, `--host 127.0.0.1`, and `--port 0` are hardcoded in Go; `--host 0.0.0.0` is rejected by dsh itself. Users cannot override via `tags.json`.
- **Shared DSH_HOME, worktree-scoped registry**: env is inherited (`DSH_HOME` shared — credentials/settings/profiles need no re-configuration); the restrict overlay re-states only `storage-json.root` to `<dataDir>/dsh/<gitx.HashPath(worktreePath)>/storages`, so the **workspace registry is isolated per worktree**. Sessions stay shared (`~/.dsh/sessions`) — a terminal-run `dsh` in the same directory sees the same sessions as the web instance; cross-worktree sessions show up in the sidebar's 未分组 group (visible, not clickable). **Cross-process session boundary (upstream dsh issue, see `docs/plans/dsh-native-ui/CROSS-PROCESS-SESSION.md`)**: live session updates are broadcast only inside the WRITER process (no fs.watch / polling), so another process sees a snapshot at open time and never refreshes; and opening an actively-written session from a second process appends a `session/end-seed` marker with `seq = log length` (no lock, no write-before check), which collides with the writer's next append and permanently corrupts the log (`corrupt session log: seq gap in committed region`). This holds with or without myworktree — the embed only inherits the boundary.
- **Workspace bootstrap**: after ready, myworktree directly POSTs the RPC envelope `{type:'client-request', rpcId, method:'workspace.create', payload:{path:<worktree>}}` to the upstream loopback (idempotent adopt; optional `session.create {cwd}` preseed). Failure is warn-only.
- **防误操作, not a hard boundary**: the `directory-picker` auto-composer row is disabled AND a bare host-backend row is inserted (`@deepseek-ai/dsh-host-directory-picker-browse`) — simply disabling the row would leave the api-gateway pending on the `directoryPicker` service and the whole plugin tree fails to load (verified on dsh 0.1.0-rc.6; see `docs/plans/dsh-native-ui/PLAN.md` §实施踩坑补充 12). The inserted backend provides the service with NO client surface, so the "Add workspace…" entry never renders; the proxy observes `session.create {cwd|workspaceId}` / `workspace.create {path}` bodies and records out-of-scope drift in an in-memory `ScopeTracker` (request forwarded unchanged — the user can still create out-of-scope sessions; their sandbox root is the out-of-scope directory and OS-level write limits still apply). A persistent warning bar (myworktree's own DOM, outside the iframe) shows until the user navigates back.
- **Foreign-session activity advisory (single-writer boundary)**: dsh is a single-writer-per-process system — opening an actively-written session from a second process appends an unguarded `session/end-seed` that collides with the writer's next seq and permanently corrupts the log (see `docs/plans/dsh-native-ui/CROSS-PROCESS-SESSION.md`). A daemon-level `SessionWatch` scans `$DSH_HOME/sessions` every 3s; a `session.jsonl.zstd` modified within 90s counts as active, and sessions this daemon itself drives (attributed from write-driving `session.*` RPC bodies through the proxy, `session.create` response tees, and the workspace-bootstrap preseed; read-only methods like `session.history` deliberately excluded) are subtracted. While foreign-active sessions exist, `/api/instances/dsh/scope` returns `foreign_active_sessions` and the frontend shows a warning bar telling the user to wait before opening them — record-only, nothing is blocked.
- **No auth on upstream, gate at the proxy**: dsh has no authentication layer; after proxying, loopback-gated endpoints (`settings`/`credentials`) are reachable through the proxy. Locally this matches the main UI's loopback trust model; **remotely (non-loopback bind or TLS) the proxy enforces a mandatory token gate** — every **non-loopback client** request (including WS upgrades) must carry `?token=` or the `mw_token` cookie, while loopback clients bypass the gate exactly like the main UI's loopback auth bypass (a local browser keeps working even when the proxy binds `0.0.0.0`, the default main listener). The embed is a dedicated origin, so the main-origin HttpOnly `mw_token` cookie cannot travel to it — **the server appends `?token=` to `iframe_src` itself** (`handleInstanceDshInfo`, which sits behind `withAuth`; page JS can never read the HttpOnly cookie and portal/login flows carry no address-bar token). On a valid query token the proxy sets the HttpOnly `mw_token` cookie on its own origin and 302-redirects to the token-free URL, so the embedded document never retains the token in its own `location.search`; the token is always stripped before forwarding upstream. The gate is hardcoded and cannot be disabled. The proxy mirrors the main listener's TLS scheme with the same certificate (avoids mixed content).
- **Version gate**: hard gate at spawn (`dsh --version` core < `0.1.0` → fail-fast, reasonix precedent); advisory supported range `[0.1.0, 0.2.0)` covers restrict-overlay row-id / WS-path / ready-line drift, surfaced as a persistent UI warning (`version_supported: false`).
- **Restriction-effectiveness check (L2)**: at spawn the driver runs `dsh web --dump-config --patch <restrict.yml>` (the launcher prints the composed tree including `--patch` overlays, then exits) and verifies the four overlay rows are present with the expected values → `overlay_verified` in the blob. `false` (or a failed dump) is surfaced as the "裁剪失效" (restriction not effective) warning — the config-level equivalent of opencode's DOM-anchor checks; the DOM-anchor L3 stays deferred with the placeholder client plugin.
- **Missing dependency flow**: spawn preflight `exec.LookPath("dsh")`; when missing, the instance fails with a structured error and the frontend offers a three-way dialog: npx launch (`npx --yes @deepseek-ai/dsh@<pin> web …`, `Setpgid` + process-group kill — npx is the dsh parent process), install now (`npm install -g`, then spawn the absolute bin resolved via `npm prefix -g`; two-step confirm in the dialog + a server-side mutex), or cancel (the poll keeps watching and the dialog stays dismissed until the next outcome). Launch mode persists **per worktree** in `<DataDir>/dsh/<worktreeHash>/launch.json` (not per instance — every Start/Restart allocates a fresh instance id).
- **Coexists with PTY / opencode-web / reasonix**: the frontend branches on `instance.kind`; existing kinds unchanged.
- **No new dependencies**: proxy implemented with `net/http/httputil.ReverseProxy` (stdlib); restrict overlay and version parsing are hand-written (no yaml/semver packages).

### Threat model & trust boundary

- **Loopback isolation (local)**: every `dsh web` binds `127.0.0.1:<port>`; the per-instance proxy binds `127.0.0.1:<free>`. Neither is reachable from the network. The proxy is unauthenticated **only because** it is loopback-only — the same trust model as the main UI's loopback bypass.
- **Token gate (remote)**: when the main listener is non-loopback or TLS, the dsh proxy binds the main listener's host and **requires** the myworktree token from every non-loopback client. A missing/invalid token from a LAN client → `401` before any byte is forwarded; loopback clients bypass the gate (main-UI trust model). This is the single boundary that keeps unauthenticated dsh (incl. `settings`/`credentials`, which become reachable once Host is rewritten to loopback) off the LAN.
- **Credential hygiene**: `?token=` is stripped (`authq.StripToken`) before forwarding upstream; the dsh subprocess never sees the myworktree token (dsh needs no password at all — nothing is injected into its env).
- **Cross-worktree drift is user-visible, not exploitable**: out-of-scope sessions are recorded and warned about, never blocked — matching the reasonix/opencode philosophy. The dsh sandbox (OS-level) is the real boundary for writes.
- **Unprivileged remote attackers** gain no new surface locally (loopback only) and face the token gate remotely. The main residual risk is a future code path that exposes a dsh upstream port or disables the token gate — see checklist items below.

### Review checklist (dsh-specific)

Every review that touches dsh-web, the loopback proxy, or remote access must verify:

1. **Token gate cannot be disabled**: the non-loopback branch *requires* token validation (no config, no env escape hatch); a test pins "non-loopback client + no token → 401" and "loopback client bypasses the gate".
2. **Token scrubbing**: every proxied query passes `authq.StripToken`; the token never reaches the dsh subprocess (no env injection, no query forwarding, no header forwarding).
3. **Origin deletion**: the Director must delete the browser `Origin` header before forwarding (dsh's `/api` fence requires `Origin.host === Host.host`; an absent Origin is fine). Regression risk if the Director is refactored.
4. **WS upgrade passthrough**: `/api/events.mux` / `/api/events.host` must pass through with the `Upgrade` handshake intact, both locally and through the remote token gate.
5. **Process-tree cleanup**: npx-mode spawns must `Setpgid` and Stop must kill the process group (`kill(-pid)`) — killing only the npx PID orphans the dsh server holding the port.
6. **Row-id drift**: restrict overlay row ids (`storage-json` / `directory-picker` / `directory-picker-browse` / `client-hmr`) are version-sensitive, and the disabled/insert combo rides on the api-gateway ↔ directoryPicker service contract; the advisory version range + `overlay_verified` (L2 `--dump-config` check) are the safety net. Verify the dsh patch loader's behavior for unknown row ids at implementation time.
7. **Bootstrap scope leak**: the workspace-bootstrap RPC must target the instance's own worktree path only; never forward a client-supplied path.
8. **Body parsing must never block or corrupt**: RPC-envelope inspection failures fall back to plain forwarding (record-only semantics).
9. **Global checklist §8 items 1–9 apply**: auth file `0o600`, no token in logs/errors/tests, cookie `Secure`/`Domain`/`Path` tightened for the deployment, and iframe documents never receive the token in their own `location.search` — the remote iframe's first navigation carries `?token=` **appended by the server** (`handleInstanceDshInfo`: the main-origin HttpOnly cookie cannot travel to the proxy origin and page JS cannot read it), the proxy validates it, sets the HttpOnly `mw_token` cookie on the proxy origin, and 302-redirects to the token-free URL. Also: proxy-listener death must fail loud (`MarkFailed` + cleared `iframe_url`), not leave a "running" instance against a dead port.

### Related files

- `internal/instance/dsh_web/driver.go` — Kind implementation (`dsh web` spawn, ready-line scan, health probe `GET /`, version gate, launch modes, npx process-group handling)
- `internal/instance/dsh_web/overlay.go` — restrict overlay generation (`storage-json` root redirect, `directory-picker` disabled + `directory-picker-browse` insert, `client-hmr` disabled)
- `internal/instance/dsh_web/proxy.go` — per-instance loopback reverse proxy (Origin deletion, Host rewrite, WS passthrough, token gate + token-free redirect, mid-body failure connection abort, RPC-body scope recording + session own-attribution, `ScopeTracker`)
- `internal/instance/dsh_web/sessionwatch.go` — shared-pool foreign-session activity watch (mtime-based, own-traffic attribution)
- `internal/instance/dsh_web/bootstrap.go` — workspace bootstrap RPC client
- `internal/instance/dsh_web/version.go` + `launch.go` — version gate + launch-mode persistence
- `internal/app/app.go` — API endpoints `GET /api/instances/dsh`, `GET /api/instances/dsh/scope`, `POST /api/instances/dsh/launch`, `POST /api/instances/dsh/install`; shutdown `StopAllKind("dsh-web")`
- `internal/ui/static/index.html` + `internal/ui/static/kinds/dsh_web.js` — iframe panel, missing-dependency dialog, warning bar, scope polling
- `docs/plans/dsh-native-ui/FEASIBILITY.md` — decision record; `docs/plans/dsh-native-ui/PLAN.md` / `TASK.md` — implementation plan
