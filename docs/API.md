# myworktree — HTTP API

> **Note**: This document covers the HTTP API. Terminal-related client-side settings (e.g., scrollback buffer size, font size, theme) are handled by the Web UI and are not part of the backend API.

Base URL: printed when starting `myworktree` or `mw`, e.g. `http://127.0.0.1:50053/`.
`mw` opens the browser automatically by default; `myworktree` prints the URL unless you pass `-open=true`.

Auth:
- If `--auth <token>` is set, send `Authorization: Bearer <token>`.
- Alternatively, pass `?token=<token>` for simple clients.
- Prefer the `Authorization` header when possible so tokens do not end up in browser history or shell history.

Common response header:
- `X-Myworktree-Server-Rev: <rev>` is returned by API and UI responses.
- Clients can treat this value as a backend revision fingerprint; if it changes after reconnect, reload the page to align frontend assets/runtime with the upgraded backend.

Version commands:
- `myworktree --version`
- `myworktree version`
- `mw --version`
- `mw version`

## 1) Main Workspace
### Get main repo info
`GET /api/main`

Returns the main (host) git repository name and its currently checked-out branch. Useful for quickly identifying which project a myworktree tab belongs to.

Response:
```json
{ "name": "myproject", "branch": "feature/ui-update" }
```

- `name`: basename of the git root directory
- `branch`: currently checked-out branch (via `git rev-parse --abbrev-ref HEAD`). Returns empty string on detached HEAD (e.g., CI shallow clones).

## 2) Worktrees
### List
`GET /api/worktrees`

Response:
```json
{ "worktrees": [ {"id":"...","name":"...","path":"...","branch":"..."} ] }
```

- `branch`: **live** — queried on every call via `git rev-parse --abbrev-ref HEAD` from the worktree path. Reflects the currently checked-out branch, not the branch name used at creation time.

### Create
`POST /api/worktrees`

Body:
```json
{ "task_description": "fix login", "base_ref": "", "adopt_if_exists": false }
```

- If `adopt_if_exists` is true and the target branch already exists, the server will attempt to **import/adopt** an existing git worktree for that branch; if no existing worktree is found, it falls back to creating a new worktree with a numeric suffix.

Response (201):
```json
{ "id":"...","name":"fix-login","path":"...","branch":"mwt/fix-login","created_at":"..." }
```

### Import (adopt existing git worktree)
`POST /api/worktrees/import`

Body:
```json
{ "name": "foo" }
```

- `name: "foo"` maps to branch `mwt/foo`.
- You can also pass a full spec like `"feature/foo"`.

Response (201): same as create.

### Delete (strict: refuses if dirty)
`POST /api/worktrees/delete`

Body:
```json
{ "id": "<worktreeId>" }
```

Response:
```json
{ "status": "ok" }
```

### Open Terminal (host macOS)
`POST /api/worktrees/open-terminal`

Opens the selected worktree path in the host machine's Terminal app. The UI should only expose this action for local browser sessions (`localhost` / `127.0.0.1`), because it affects the machine running `myworktree`, not the client device.

Body:
```json
{ "id": "<worktreeId>" }
```

- `id` can be a managed worktree ID, or `"__main__"` to target the main repo root.
- Uses `open -a Terminal <path>` on macOS.
- The backend enforces a loopback-only boundary using the request remote address; non-loopback callers receive HTTP 403 even if they know the endpoint.

Response:
```json
{ "status": "ok" }
```

- Returns HTTP 404 if the worktree ID is unknown or the resolved path no longer exists.
- Returns HTTP 403 if the request does not originate from a loopback client.
- Returns HTTP 500 if launching Terminal fails.

### Open Finder (host macOS)
`POST /api/worktrees/open-finder`

Opens the selected worktree path in the host machine's Finder. As with `open-terminal`, this is a host-local side effect and should only be presented in the UI for local browser sessions.

Body:
```json
{ "id": "<worktreeId>" }
```

- `id` can be a managed worktree ID, or `"__main__"` to target the main repo root.
- Uses AppleScript (`osascript`) to tell Finder to open the path and activate the app, so the Finder window is brought to the foreground more reliably than plain `open <path>`.
- The backend enforces a loopback-only boundary using the request remote address; non-loopback callers receive HTTP 403 even if they know the endpoint.

Response:
```json
{ "status": "ok" }
```

- Returns HTTP 404 if the worktree ID is unknown or the resolved path no longer exists.
- Returns HTTP 403 if the request does not originate from a loopback client.
- Returns HTTP 500 if launching Finder fails.

### Get worktree git status
`GET /api/worktree/status?id=<worktreeId>`

Returns the list of changed files (staged + unstaged relative to HEAD) for the specified worktree.

- `id`: worktree ID (`"__main__"` for the main repo) or a managed worktree ID from `GET /api/worktrees`.
- Uses `git diff --numstat HEAD` with a 2-second timeout.
- Returns an empty list if there are no changes or if the command fails.

Response:
```json
{
  "changes": [
    { "path": "foo.go", "additions": 10, "deletions": 3 }
  ],
  "total": { "additions": 10, "deletions": 10 }
}
```
- Returns HTTP 400 if `id` is missing or unknown.

## 3) Branches
### List (default + top 10)
`GET /api/branches`

Returns local branches with default branch always first, then branches sorted by last commit time (newest → oldest), max 10.

Response:
```json
{ "default": "main", "branches": [ {"name":"main","commit_unix":1700000000} ] }
```

## 4) Tags
### List (merged: global + project)
`GET /api/tags`

Response:
```json
{ "tags": [ {"id":"...","command":"..."} ] }
```

## 5) Instances
### List
`GET /api/instances`

Response:
```json
{ "instances": [ {"id":"...","worktree_id":"...","worktree_name":"...","tag_id":"...","name":"build-server","labels":{"purpose":"refactor"},"pid":123,"status":"running"} ], "version": 7 }
```

- `version`: monotonically increasing state version. Incrementing `SaveWithVersion` calls cause this to grow. Clients should track it and send it back on operations that modify state (e.g., reorder) to detect concurrent modifications.

### Start
`POST /api/instances`

Body:
```json
{ "worktree_id": "<worktreeId>", "tag_id": "optional", "command": "optional", "name": "optional", "labels": {"purpose":"refactor","priority":"P1"} }
```

- `worktree_id` can be a regular worktree ID, or `"__main__"` to run an instance in the main (host) git repository. For `"__main__"`, the instance starts in the main repo root directory.

If both `tag_id` and `command` are empty, the server starts an **interactive shell** instance in the worktree.
If `command` is provided, it is sent to the shell as the initial command and the shell remains available for further input.

Example (ad-hoc command without tags):
```json
{ "worktree_id": "<worktreeId>", "command": "echo hello && ls" }
```

Response (201):
```json
{ "id":"...","pid":123,"status":"running","log_path":"..." }
```

### Rename
`PATCH /api/instances`

Updates mutable metadata of an existing instance. Currently only `name` is supported.

Body:
```json
{ "id": "<instanceId>", "name": "build-server" }
```

- `name`: new display name (required). Empty/whitespace-only names are rejected.
- Returns the full updated instance as JSON.
- Returns HTTP 404 if instance not found, HTTP 400 if name is empty.

Response (200):
```json
{ "id":"...","worktree_id":"...","worktree_name":"...","tag_id":"...","name":"build-server","labels":{},"pid":123,"status":"running","created_at":"..." }
```

### Reorder tabs
`PATCH /api/instances/reorder`

Sets the tab display order for a specific worktree. The order persists across page reloads and server restarts.

Body:
```json
{ "worktree_id": "<worktreeId>", "order": ["id1", "id2", "id3"], "version": 7 }
```

- `worktree_id`: the worktree whose tab order is being set (can be `"__main__"` for the main repo)
- `order`: ordered list of ALL instance IDs belonging to that worktree (both active and archived). All instances must be included.
- `version`: the state version observed by the client (from `GET /api/instances`). Used for optimistic locking — if the state has changed since the client fetched it, the server returns HTTP 409 Conflict.

Response (200):
```json
{ "status": "ok" }
```

- Returns HTTP 400 if the order list is missing an instance or contains an ID that does not belong to the worktree.
- Returns HTTP 409 Conflict if the state version has changed (concurrent modification). The response body includes the current version so the client can refresh and retry:

```json
{ "error": "state changed, please refresh", "version": 8 }
```

- The instance order is also stored as the array order in `state.json`, so `GET /api/instances` reflects the new order immediately.

### Stop
`POST /api/instances/stop`

Body:
```json
{ "id": "<instanceId>" }
```

### Restart
`POST /api/instances/restart`

Body:
```json
{ "id": "<instanceId>" }
```

- Creates a new instance with the same worktree + tag/command + labels.
- If the old instance is not archived yet (and is not running), it will be archived automatically.
- The old instance record is linked to the new one via `restarted_to` / `restarted_from`.

### Send input
`POST /api/instances/input`

Body:
```json
{ "id": "<instanceId>", "input": "ls -la\n" }
```

### Web TTY stream (WebSocket)
`GET /api/instances/tty/ws?id=<instanceId>`

Bi-directional stream for terminal output/input with PTY support.

**Handshake Protocol:**
1. Server sends `{"type":"ready"}` immediately after connection
2. Client should wait for this message before sending resize
3. Client sends `{"type":"resize","cols":80,"rows":24}` to start data flow
4. Server sends initial log + real-time output as binary frames
5. Client receives first data and triggers second resize (50ms delay) for TUI redraw

**Frontend session model:**
- The current UI keeps transport state per running instance rather than sharing a single terminal across tabs.
- Switching tabs may leave other running instances connected in the background; hidden instances are not rendered, but their PTY attachment can remain alive.
- Stopped instances still use the log replay endpoints as their primary display source.

**Message Types:**

*Client → Server:*
- Input: text/binary frames (raw bytes)
- Resize: `{"type":"resize","cols":<number>,"rows":<number>}`

*Server → Client:*
- Ready: `{"type":"ready"}` (text frame)
- Output: binary frames (terminal output chunks)

**Timeout & Fallback:**
- Client should implement handshake timeout (recommended: 5s)
- On timeout, close WebSocket and fallback to SSE: `GET /api/instances/log/stream?id=<instanceId>`

**Example Flow:**
```
Client                    Server
   |                         |
   |--- Connect ------------>|
   |<-- {"type":"ready"} ----|  Handshake
   |                         |
   |-- {"type":"resize", --->|  Notify terminal size
   |    "cols":80,"rows":24} |
   |                         |
   |<-- binary output -------|  Initial log + realtime
   |                         |
   |--- (50ms delay) -------|
   |                         |
   |-- {"type":"resize", --->|  Trigger TUI redraw
   |    "cols":80,"rows":24} |
   |                         |
   |--- input bytes -------->|  User input
   |<-- binary output -------|  Process output
```

### Archive
`POST /api/instances/archive`

Body:
```json
{ "id": "<instanceId>" }
```

Archives a non-running instance (hides it from the main list).

### Delete
`POST /api/instances/delete`

Body:
```json
{ "id": "<instanceId>" }
```

Deletes a non-running instance record (best-effort deletes the log file).

### Purge archived
`POST /api/instances/purge`

Deletes archived instances for a given worktree in a single atomic write, protected by optimistic locking. Use an empty `worktree_id` to target all worktrees.

Body:
```json
{ "worktree_id": "wt1", "version": 7 }
```

- `worktree_id`: the worktree whose archived instances to delete (omit or use empty string to target all worktrees).
- `version`: the state version observed by the client (from `GET /api/instances`). Used for optimistic locking — if the state has changed since the client fetched it, the server returns HTTP 409 Conflict.

Response (200):
```json
{ "status": "ok" }
```

- Returns HTTP 409 Conflict if the state version has changed. The response body includes the current version so the client can refresh and retry:
```json
{ "error": "state changed, please refresh", "version": 8 }
```

### Log replay (tail / incremental)
`GET /api/instances/log?id=<instanceId>[&since=<byteOffset>]`

- Without `since`: returns recent tail as `text/plain`.
- With `since`: returns incremental content from byte offset and includes response header `X-Log-Offset: <nextByteOffset>`.

Response: `text/plain`

### Live log stream (SSE)
`GET /api/instances/log/stream?id=<instanceId>[&since=<byteOffset>]`

- Server-Sent Events stream.
- Emits `event: log` with JSON payload:
```json
{"chunk":"...","next":12345}
```

### Instance resource stats
`GET /api/instances/stats`

Returns per-instance resource consumption and connection status, grouped by worktree.

**Note**: This endpoint performs real-time process stat collection (via `gopsutil`). It only yields meaningful CPU% values after at least 1-2 seconds of server runtime, as CPU% requires a delta calculation from the previous measurement.

Response:
```json
{
  "instances": [
    {
      "id": "inst-abc123",
      "name": "build-server",
      "worktree_id": "wt-xyz",
      "worktree_name": "feature-ui",
      "pid": 12345,
      "status": "running",
      "cpu_percent": 3.5,
      "memory_rss_bytes": 52428800,
      "connection_type": "websocket"
    }
  ],
  "worktrees": [
    {
      "worktree_id": "wt-xyz",
      "name": "feature-ui",
      "total_cpu": 5.2,
      "total_memory": 104857600,
      "instance_count": 2
    }
  ],
  "global": {
    "total_cpu": 8.7,
    "total_memory": 209715200,
    "instance_count": 3
  }
}
```

Fields:
- `cpu_percent`: CPU utilization as a percentage of a single core. 0% on the first measurement (no prior baseline).
- `memory_rss_bytes`: Resident Set Size — actual physical memory used by the process.
- `connection_type`: `"websocket"` if the instance has an active WebSocket TTY connection, `"sse"` if using the SSE fallback, `"none"` otherwise.
- Worktree subtotals and global totals aggregate only `running` instances.

### 5.9 Instance lifecycle (frontend)

All page close/refresh/navigation events trigger a browser-native confirmation dialog. This is a purely client-side UX feature:

- **Trigger**: `beforeunload` event on `window`
- **Behavior**: Calls `event.preventDefault()` and sets `event.returnValue = ''` to force the browser to show its native confirmation dialog
- **No backend involvement**: Instances continue running regardless of the user's choice
- **Condition**: Always triggered on any close action — no dependency on instance state

## 6) MCP
### Tool names
`GET /api/mcp/tools`

Response:
```json
{ "tools": ["worktree_list", "worktree_create", "..."] }
```

### Tool call
`POST /api/mcp/call`

Body:
```json
{ "tool": "instance_list", "args": {} }
```

Response:
```json
{ "result": { "instances": [] } }
```

Supported tool names:
- `worktree_list`, `worktree_create`, `worktree_delete`
- `branch_list`, `tag_list`
- `instance_list`, `instance_start`, `instance_stop`, `instance_input`, `instance_archive`, `instance_delete`, `instance_purge`, `instance_log_tail`
