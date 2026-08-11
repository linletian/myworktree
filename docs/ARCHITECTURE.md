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
  - **Exception (reasonix)**: `ReconcileRunningOnStartup` probes reasonix instances via `Reasonix.Health()` (pid liveness + TCP port). A live serve subprocess survives the restart and is kept `running` so Stop/Delete still manage it; a dead one is marked `stopped`. This prevents the restart-then-delete sequence from orphaning a serve process that holds the symlinked provider credentials.
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

- **Lifecycle**: `Manager.Start` with `kind=reasonix` delegates to `internal/instance/reasonix` (`Driver.Start`): it launches `reasonix serve --addr 127.0.0.1:0 --auth token --token-file … --port-file … --pid-file … --no-open --resume <fixed session file>` with `cwd` = worktree path. The actual port is read back from the port file (port 0 = kernel-assigned). `Stop` signals the process group (TERM → KILL). `Restart` allocates a fresh instance id and cleans the old state dir (Restart = brand-new conversation by design). `Delete` (after stop) removes the whole per-instance state dir **after** releasing the state lock, so the 5s worst-case `Stop` never blocks concurrent Start/Stop/Reorder/Rename (issue #47).
- **Version gate & readiness**: before spawning, `Driver.Start` runs `reasonix --version` and rejects CLIs older than `1.22.0` (`Driver.MinVersion` overrides) with a readable error instead of a 15s timeout (issue #45); readiness/early-exit errors include the `serve.log` tail. Tag `env` is injected into the serve process and tag `preStart` runs before serve (aborting on failure); the tag `command` is deliberately not executed (issue DEFERRED §3).
- **Isolation**: each instance gets `REASONIX_HOME=<data>/reasonix/<id>/home` so sessions never contend on reasonix session leases; `config.toml` and `.env` inside that home are **symlinks** to `~/.reasonix/config.toml` / `~/.reasonix/.env`, so provider credentials stay live without copying. `reasonix serve` must be on `PATH` (or configured via `Driver.ReasonixBin`).
- **Proxy**: `internal/app/reasonix_proxy.go` serves `/rx/<id>/…`. When the main listener is **loopback-only** (and no TLS) it is mounted on a dedicated loopback listener (issue #44) and the frontend iframe uses the `web_url` reported by `/api/instances` — the embedded page is **cross-origin** with the myworktree API, so content inside the chat iframe cannot silently call `/api/*` with the user's session. In **TLS mode or with a network-open main listener** (default `0.0.0.0`, or an explicit LAN IP — an absolute `http://127.0.0.1:<port>` web_url would be unreachable from a remote browser) the independent listener is skipped and the iframe falls back to the **relative** same-origin `/rx/<id>/` route, which follows the browser's current origin and keeps LAN/remote access working. The proxy injects the `reasonix_token` cookie (`reasonix.CookieName`, single source), streams SSE, and injects at one HTML point the URL-prefix shim plus the issue #48 layout: desktop sidebar **collapsed by default** with a toggle (`--mw-sidebar-w` width), native mobile behavior untouched. Backend port/token come from a per-instance in-memory cache (issue #46), so the proxy path does not read files per request.
- **Liveness**: `Driver.Health` = pid alive (`kill(pid,0)`) **and** TCP connect to the recorded port succeeds — cross-platform (no `/proc`), so it also works on macOS.
- **Restart reconciliation**: see the exception note in §4 — a live serve subprocess is re-attached as `running` on startup, keeping it manageable; dead ones are marked `stopped`.

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
