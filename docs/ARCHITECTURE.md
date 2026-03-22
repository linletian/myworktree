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
- `internal/monitor/` — resource stats collector (CPU delta via gopsutil/process.Times, memory via RSS)
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
  - `logs/<instanceId>.log` — rolling instance backlog

### 3.2 State model
- Worktree: id, name, path, branch, baseRef, createdAt
- Instance: id, worktreeId, tagId, command, cwd, env (sanitized), pid, status, logPath, timestamps
- TabOrder (at State level): map of worktree_id to ordered list of instance IDs
- **Version** (at State level): monotonically increasing int64, incremented on every write via `SaveWithVersion`. Used for optimistic locking on concurrent modification detection.
- Main Repo: not persisted; served via `GET /api/main` with live git branch

### 3.3 Main workspace (sidebar)

The sidebar shows a pinned **Main Workspace** item at the top (purple accent), followed by a "Worktrees" divider and the managed worktree list.

- **Main repo**: `GET /api/main` returns `{name, branch}`. The branch is live — queried via `git rev-parse --abbrev-ref HEAD` (via `gitx.CurrentBranch`). Returns empty string for `branch` on detached HEAD (e.g., CI shallow clones), otherwise returns the current branch name.
- **Worktrees**: `GET /api/worktrees` also returns live branches — `worktree.Manager.List()` queries `git rev-parse --abbrev-ref HEAD` per worktree path on each call. The `branch` field reflects the currently checked-out branch, not the creation-time branch.
- **Quick actions**: The main repo item and each managed worktree row render two SVG shortcut buttons when the UI is accessed via `localhost` or `127.0.0.1`: one opens Terminal at the target path, the other opens Finder. These buttons are intentionally hidden for remote/browser sessions because the action targets the host machine running `myworktree`, not the remote client device.
- **Server-side boundary**: The backend does not trust the frontend visibility check. `POST /api/worktrees/open-terminal` and `POST /api/worktrees/open-finder` reject non-loopback clients based on the request's remote address, so remote callers cannot trigger host GUI actions by directly invoking the API.
- **Host-side execution**: Clicking a quick action calls backend endpoints that resolve the target path (`"__main__"` maps to the repo root; managed IDs map to the persisted worktree path) and execute macOS host commands. Terminal uses `open -a Terminal <path>`. Finder uses `osascript` to ask Finder to open the POSIX path and activate the app, which is more reliable for visibly bringing a Finder window forward.
- **Instance routing**: Use `worktree_id: "__main__"` (constant: `instance.MainWorktreeID`) in `POST /api/instances` to start an instance in the main repo root. The instance's `worktree_id` will be `"__main__"` and `worktree_name` will be the directory basename.
- **Auto-select**: On first load, the UI auto-selects the first worktree; if no worktrees exist, it selects the main repo.
- **Refresh**: All branch info (main repo + worktrees) updates via the existing 2-second polling.
- **Git Changes panel**: Below the worktree list, a read-only panel shows all changed files (staged + unstaged) relative to HEAD for the currently selected worktree. The panel auto-refreshes every 10 seconds and on worktree selection change. The main repo's changes also refresh when its branch changes. Per-file addition/deletion counts are obtained via `git diff --stat HEAD` (2-second timeout).

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
- Backlog is stored on disk with a size cap (rolling truncate).
- On server startup, stale persisted `running` records are reconciled to `stopped` because in-memory stdin/stdout bindings cannot be resumed after process restart.
- **Rename**: `PATCH /api/instances` updates an instance's display name (`name` field). The rename takes effect immediately in the UI and persists to `state.json`.
- **Tab ordering**: `PATCH /api/instances/reorder` persists per-worktree tab order to `state.json` (`tab_order` map + array order in `State.Instances`). Uses **optimistic locking** — the client sends the `version` observed from `GET /api/instances`. If the state has been modified since (e.g., another user started an instance), the server returns HTTP 409 Conflict and the client refreshes and retries.
- **Resource monitoring**: A clickable transport status bar in the bottom-right of the workspace opens a resource monitor modal. The modal shows per-instance CPU%, memory RSS, and connection type (WebSocket/SSE) grouped by worktree, with subtotals and a global summary. Data is fetched via `GET /api/instances/stats` (1-second polling when open, stops when closed). CPU% uses delta calculation from `process.Times()` with a per-PID baseline stored in the `Collector` struct.
- **Browser close protection**: The frontend registers a `beforeunload` event handler that unconditionally triggers a browser-native confirmation dialog on any page close/refresh/navigation attempt. This is purely a client-side UX safeguard — backend instances are unaffected and continue running.

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

## 6. Security model (single-user)
- Default listen: loopback only.
- Non-loopback requires `--auth`.
- Optional built-in HTTPS via `--tls-cert/--tls-key`.
- Origin/Host check + basic rate limit on unauthorized attempts.
- Redaction on stored backlog (e.g. `sk-...`).

## 7. MCP extensibility
- Core managers (worktree/instance) are transport-agnostic.
- `internal/mcp` exposes tool names; server dispatch maps tool calls to existing core managers without rewriting core.
