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
- `branch`: currently checked-out branch (via `git rev-parse --abbrev-ref HEAD`). Returns HTTP 500 if the git repository is inaccessible or HEAD is malformed.

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
{ "instances": [ {"id":"...","worktree_id":"...","worktree_name":"...","tag_id":"...","name":"build-server","labels":{"purpose":"refactor"},"pid":123,"status":"running"} ] }
```

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
- `instance_list`, `instance_start`, `instance_stop`, `instance_input`, `instance_archive`, `instance_delete`, `instance_log_tail`
