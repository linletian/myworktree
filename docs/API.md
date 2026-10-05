# myworktree — HTTP API

> **Note**: This document covers the HTTP API. Terminal-related client-side settings (e.g., scrollback buffer size, font size, theme) are handled by the Web UI and are not part of the backend API.

Base URL: printed when starting `myworktree` or `mw`, e.g. `http://127.0.0.1:50053/`.
`mw` opens the browser automatically by default; `myworktree` prints the URL unless you pass `-open=true`.

Auth:
- If `--auth <token>` is set, send `Authorization: Bearer <token>`.
- Alternatively, pass `?token=<token>` for simple clients.
- For Portal dashboard access, the `mw_token` HttpOnly Cookie is used as the third token source (automatically sent by browser after `/api/auth` login).
- Token priority: `Authorization` header → `?token=` query → `mw_token` Cookie.
- Prefer the `Authorization` header when possible so tokens do not end up in browser history or shell history.
- **Auto-generate token**: When `--auth` is not provided and no token exists in `auth.json`, the CLI automatically generates a random 32-char hex token, persists it to `~/.config/myworktree/auth.json`, and uses it as the instance auth token. This ensures instances always have auth enabled by default.

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
{
  "name": "myproject",
  "branch": "feature/ui-update",
  "github_url": "https://github.com/owner/myproject"
}
```

- `name`: basename of the git root directory.
- `branch`: currently checked-out branch (via `git rev-parse --abbrev-ref HEAD`). Returns empty string on detached HEAD (e.g., CI shallow clones).
- `github_url`: when the main repo has a git remote pointing at `github.com`, returns the canonical `https://github.com/<owner>/<repo>` URL; otherwise returns an empty string. Resolution order:
  1. `git remote get-url origin` (preferred).
  2. If `origin` is missing or not parseable, fall back to iterating `git remote` and trying each remote in declared order.
  3. Supported URL formats: `<user>@github.com:owner/repo.git` (SCP-style; the user segment is arbitrary — `git` is the conventional default, but `~/.ssh/config` aliases and CI bots commonly use other usernames), `https://github.com/owner/repo.git`, `ssh://[user@]github.com/owner/repo.git` (no explicit port — `ssh://git@github.com:22/...` is **not** recognized). The `.git` suffix and a trailing `/` are stripped.
  4. Only host `github.com` (case-insensitive) is recognized. GitHub Enterprise (`*.ghe.com`, self-hosted) and any non-GitHub host (GitLab, Bitbucket, local paths, `file://`) yield an empty string.
  5. The field is always present in the JSON response (an empty string means "no GitHub link to show").

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
{ "task_description": "fix login", "base_ref": "", "adopt_if_exists": false, "branch_name": "" }
```

- `branch_name`: optional. If provided, it is used directly as the branch name (no automatic prefix added). If omitted, LLM is used to generate a branch name if configured, otherwise slugified task description is used.
- If `adopt_if_exists` is true and the target branch already exists, the server will attempt to **import/adopt** an existing git worktree for that branch; if no existing worktree is found, it falls back to creating a new worktree with a numeric suffix.

Response (201):
```json
{ "id":"...","name":"fix-login","path":"...","branch":"fix-login","created_at":"..." }
```

### Import (adopt existing git worktree)
`POST /api/worktrees/import`

Body:
```json
{ "name": "foo" }
```

- `name: "foo"` maps to branch `foo`.
- You can also pass a full spec like `"feature/foo"`.
- For backward compatibility, `"foo"` also matches existing branches with `mwt/foo` prefix.

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

Returns the list of changed files, split into staged and unstaged changes, for the specified worktree.

- `id`: worktree ID (`"__main__"` for the main repo) or a managed worktree ID from `GET /api/worktrees`.
- Uses two concurrent git commands, each with a 2-second timeout:
  - `git diff --cached --numstat` — staged changes (index vs HEAD)
  - `git diff --numstat` — unstaged changes (working tree vs index)
- Returns empty lists if there are no changes.
- Returns HTTP 500 if both git commands fail.
- On partial failure (one command fails), the failing section includes an `error` field describing the failure, while the successful section returns its results normally. HTTP 200 is returned in this case.

Response:
```json
{
  "staged": {
    "changes": [
      { "path": "foo.go", "additions": 10, "deletions": 3 }
    ],
    "total": { "additions": 10, "deletions": 3 }
  },
  "unstaged": {
    "changes": [
      { "path": "bar.go", "additions": 5, "deletions": 2 }
    ],
    "total": { "additions": 5, "deletions": 2 }
  }
}
```

Partial failure example (staged succeeded, unstaged failed):
```json
{
  "staged": {
    "changes": [
      { "path": "foo.go", "additions": 10, "deletions": 3 }
    ],
    "total": { "additions": 10, "deletions": 3 }
  },
  "unstaged": {
    "changes": [],
    "total": { "additions": 0, "deletions": 0 },
    "error": "git diff failed: context deadline exceeded"
  }
}
```
- Returns HTTP 400 if `id` is missing or unknown.

### Get single file diff
`GET /api/worktree/file/diff?id=<worktreeId>&path=<filePath>&staged=true|false`

Returns the unified diff (`git diff [--cached] -- <path>`) for a single file in a worktree.

- `id`: worktree ID (`"__main__"` for the main repo) or a managed worktree ID.
- `path`: file path relative to the worktree root.
- `staged`: optional, defaults to `false`. Set to `true` for staged (index) diff.
- Uses a 2-second timeout.
- Returns plain text (`Content-Type: text/plain; charset=utf-8`).
- Returns HTTP 400 if `id` or `path` is missing.
- Returns HTTP 403 if the path escapes the worktree root or targets a sensitive file.
- Returns HTTP 413 if the diff output exceeds 500 KB.
- Returns HTTP 500 on git failure.

**Synthetic diff for untracked files**: when a file is untracked (not in git's index), `git diff` produces no output. In this case the endpoint synthesizes a standard unified diff header with the full file content as an all-addition hunk, mimicking `git diff /dev/null <path>`. The synthetic diff includes `\ No newline at end of file` when the source file lacks a trailing newline, matching native git behaviour.
Clients that parse this output should be prepared for both real and synthetic diffs.

### Get single file content
`GET /api/worktree/file/content?id=<worktreeId>&path=<filePath>`

Returns the raw content of a single file in a worktree.

- `id`: worktree ID (`"__main__"` for the main repo) or a managed worktree ID.
- `path`: file path relative to the worktree root.
- Returns plain text (`Content-Type: text/plain; charset=utf-8`).
- Response headers:
  - `X-File-Type`: hint — `text/markdown`, `text/x-code`, or `text/plain`.
  - `X-File-FullPath`: absolute filesystem path of the requested file.
- 1 MB file size limit.
- Returns HTTP 400 if `id` or `path` is missing.
- Returns HTTP 403 if the path escapes the worktree root or targets a sensitive file.
- Returns HTTP 404 if the file does not exist.
- Returns HTTP 413 if the file exceeds 1 MB.
- Returns HTTP 415 if the file is binary (detected via null bytes).

### Get all worktrees divergence
`GET /api/worktrees/diverged`

Returns divergence information for all worktrees: whether each worktree branch is behind the main branch or develop branch.

- Called on page load and every 60 seconds.
- For each worktree whose branch is the main branch itself, returns an empty object `{}` (no divergence check needed).
- For each worktree whose branch is develop, checks only main.
- For all other worktrees, checks both main and develop (if develop exists locally).
- Uses the local vs remote effective head that is more ahead (`git rev-list --left-right --count`).
- If the worktree's current branch cannot be determined (e.g., detached HEAD), the worktree entry contains only an `error` field.

Response:
```json
{
  "items": {
    "wt_abc123": {
       "mainBranch":    {"diverged": true,  "ahead": 3},
       "develop": {"diverged": false}
     },
     "wt_def456": {
       "mainBranch":    {"diverged": true,  "ahead": 1},
      "develop": {"diverged": true,  "ahead": 2}
    },
    "wt_detached": {
       "mainBranch": {"error": "cannot determine branch: git HEAD is detached or malformed"}
    },
    "__main__": {}
  }
}
```

- `diverged`: `true` means the upstream branch has commits not yet contained in the worktree branch HEAD.
- `ahead`: number of commits the upstream effective head is ahead of the worktree HEAD. Only present when `diverged` is `true`.
- `error`: optional string describing why the check failed (e.g., git command timeout). When present, `diverged` is `false` and `ahead` is absent.
- `mainBranch` / `develop`: each key may be absent if the check is not applicable (e.g., develop does not exist locally).

### Get single worktree divergence
`GET /api/worktree/diverged?id=<worktreeId>`

Returns divergence information for a single worktree. Same response structure as above, but only contains the requested worktree entry.

- Called immediately when the user selects a worktree in the sidebar to refresh divergence labels.
- `id` can be a managed worktree ID, or `"__main__"` for the main repo.

Response:
```json
{
  "items": {
    "wt_abc123": {
       "mainBranch":    {"diverged": true,  "ahead": 3}
    }
  }
}
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
{ "instances": [ {"id":"...","worktree_id":"...","worktree_name":"...","tag_id":"...","name":"build-server","pid":123,"status":"running"} ], "version": 7 }
```

- `version`: monotonically increasing state version. Incrementing `SaveWithVersion` calls cause this to grow. Clients should track it and send it back on operations that modify state (e.g., reorder) to detect concurrent modifications.

### Start
`POST /api/instances`

Body:
```json
{ "worktree_id": "<worktreeId>", "tag_id": "optional", "command": "optional", "name": "optional", "kind": "optional" }
```

- `worktree_id` can be a regular worktree ID, or `"__main__"` to run an instance in the main (host) git repository. For `"__main__"`, the instance starts in the main repo root directory.
- `kind`: `""`/`"pty"` (default) starts a PTY terminal instance; `"opencode-web"` starts an `opencode serve` subprocess embedded via `/__opencode/<id>/` (see section 5.11); `"reasonix"` starts a `reasonix serve` subprocess in the worktree and exposes its web chat UI under `/rx/<id>/` (see section 5.10). For `"reasonix"` the tag's `command` is ignored; tag `env` / `preStart` are applied.

If both `tag_id` and `command` are empty, the server starts an **interactive shell** instance in the worktree.
If `command` is provided, it is sent to the shell as the initial command and the shell remains available for further input.

Example (ad-hoc command without tags):
```json
{ "worktree_id": "<worktreeId>", "command": "echo hello && ls" }
```

Response (201):
```json
{ "id":"...","pid":123,"status":"starting","created_at":"...","kind":"pty" }
```

**`status` is `starting`, not `running`.** Start returns as soon as the process is spawned; the record is persisted as `starting` and flipped to `running` on the kind's ready signal by a separate goroutine, so the `201` body — and any `GET /api/instances` issued immediately afterwards — legitimately reports `starting`. The full status vocabulary is `starting` / `running` / `unhealthy` / `stopping` / `stopped` / `failed` / `exited` (`internal/framework/status.go`); see `docs/ARCHITECTURE.md` §5.3 for how the UI buckets them. Clients MUST NOT treat "not `running`" as "stopped": a `starting` instance is alive and accepts I/O.

**Error: log buffer budget exceeded (`503 Service Unavailable`)**

Returned when starting the new instance would push the per-process log-buffer total past the global budget (default: 25% of system RAM — see `docs/ARCHITECTURE.md` §4.1 *Instance log buffer*). The error code `log_buffer_budget_exceeded` is part of the stable API contract; the UI matches on it to surface a dedicated modal.

Headers:
- `Content-Type: application/json`
- `Retry-After: 0` (will not auto-resolve; user must close other instances or raise `log_buffer_bytes` in `auth.json`)

Body:
```json
{
  "error": "log_buffer_budget_exceeded",
  "message": "Insufficient memory to start new instance: used 100.00 MB, limit 64.00 MB.",
  "used_bytes": 104857600,
  "limit_bytes": 67108864,
  "system_bytes": 268435456,
  "hint": "Close other instances, raise LogBufferBytes in auth.json, or reduce concurrent tabs."
}
```

The same shape is returned by the MCP `instance_start` tool when the budget is exceeded.

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
{ "id":"...","worktree_id":"...","worktree_name":"...","tag_id":"...","name":"build-server","pid":123,"status":"running","created_at":"..." }
```

### Reorder tabs
`PATCH /api/instances/reorder`

Sets the tab display order for a specific worktree. The order persists across page reloads and server restarts.

Body:
```json
{ "worktree_id": "<worktreeId>", "order": ["id1", "id2", "id3"], "version": 7 }
```

- `worktree_id`: the worktree whose tab order is being set (can be `"__main__"` for the main repo)
- `order`: ordered list of ALL instance IDs belonging to that worktree. All instances must be included.
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

- Creates a new instance with the same worktree + tag/command.
- If the old instance is not running, it will be deleted automatically.
- The old instance record is linked to the new one via `restarted_to` / `restarted_from`.

### Send input
`POST /api/instances/input`

Body:
```json
{ "id": "<instanceId>", "input": "ls -la\n" }
```

### Web TTY stream (WebSocket)
`GET /api/instances/tty/ws?id=<instanceId>[&since=<offset>]`

Bi-directional stream for terminal output/input with PTY support.

`since` is an optional byte cursor (issue #87):
- **omitted / not a valid integer** → the server replays its newest 64KB
  tail, as before (this is the first-connect path).
- **`since=0`** → accepted, but it is NOT a "tail" mode. On
  `GET /api/instances/log` a single read starting at 0 starts at the
  OLDEST live byte (the #81 symptom); on this WS endpoint the catch-up
  loop reads forward from 0 and streams every chunk it reads straight to
  the socket, so the effect there is a replay of everything still live in
  the ring — up to the 8 MB handshake budget — with `sync` at the end of
  the last chunk actually written. The shipped UI never sends `0`;
  it omits `since` entirely when its cursor is unknown (the `-1` sentinel).
- **`since>0`** → the server replays the bytes at or after that offset, in
  full, so a reconnecting client that still holds its rendered screen does
  not receive the tail a second time.

Each replay read is capped at 64KB. When the delta since `since` exceeds
64KB, the server loops reads until it is caught up and **streams each chunk
to the socket as it reads it** instead of retaining one, so the whole delta
is delivered and peak memory stays ~64KB (one chunk) regardless of the ring
cap. The `sync` offset is the end of the **last chunk actually written** to
this socket — never an offset over bytes the client did not receive **and can
still receive**, because a client cursor only moves forward and would never ask
for them again. The two deliberate exceptions both publish the ring's real
head, and neither re-delivers anything a healthy client still had coming: a
path that writes nothing has no last chunk to name, and a ring whose process
has exited and whose buffer has been dropped publishes head precisely because
the bytes behind it are permanently undeliverable — publishing 0 there would
instead throw the client into a full tail replay. One handshake replays at
most 8 MB
(`ttyHandshakeReplayBudget` in `internal/app/app.go`), counted in bytes
already written and checked before the next read: once the budget is spent
the loop stops, and the undelivered remainder sits **ahead** of the published
cursor, so the next reconnect re-requests exactly those bytes — truncation is
**deferred to the next reconnect, never a skip**. The deferral has a cost:
ring caps are 16–256 MB, so a badly lagged client can need up to 32 reconnects
to converge, and until it reconnects the deferred bytes stay off a screen whose
socket still looks healthy (the replay lands in xterm.js `scrollback: 10000`,
so it is the older replayed content that scrolls out, never the live output
that follows it). There is deliberately no write deadline on this path: the
handshake is not yet subscribed to the output stream, so a blocked write does
not stall the pump, and a wall-clock deadline would livelock a slow-but-
progressing consumer where a byte budget simply resumes next reconnect. Output produced between the
last chunk written and the live subscription sits behind
the published cursor and is re-requested by the next reconnect (self-healing
contiguity, not absolute coverage). A `since` older than the oldest byte
still in the ring buffer is silently clamped to the oldest live byte; a
`since` at or beyond head replays nothing (at head) or falls back to the
tail (beyond head, defensive) — the client's own number is never echoed back
as authoritative.

**Handshake Protocol:**
1. Server sends `{"type":"ready"}` immediately after connection
2. Client should wait for this message before sending resize
3. Client sends `{"type":"resize","cols":80,"rows":24}` to start data flow
4. Server sends initial log (the tail, or only the bytes after `since`) as binary frames
5. Server sends `{"type":"sync","offset":<int64>}` (text frame) — the end
   offset of that replay; the client stores it and sends it back as `since`
   on its next reconnect
6. Real-time output continues as binary frames
7. Client receives first data and triggers second resize (50ms delay) for TUI redraw
8. If the client stops draining the live stream, the server closes the
   connection with **`1013` / reason `subscriber overflow: slow consumer`**
   (issue #82) instead of silently dropping output — see Close codes below;
   the client reconnects with the `since` cursor it already holds, exactly
   as after any abnormal close

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
- Sync: `{"type":"sync","offset":<int64>}` (text frame, sent after the handshake replay — even when that replay was empty. The client latches it per connection: replay frames received BEFORE the sync never touch the cursor — the sync publishes the authoritative end of the whole replay in one step; binary frames received AFTER it advance the cursor by their wire byte count — every one of them is ring-buffer output, as the server closes the connection rather than writing diagnostics as binary)

*Server → Client close codes:*
- `1013` with reason `subscriber overflow: slow consumer` (issue #82) — the live
  output subscription was torn down server-side because this client stopped draining:
  its 64-slot output queue filled (the PTY pump reads in 1024-byte chunks, so ≈64 KB
  of unread output) and the server disconnected the subscriber rather than dropping
  chunks forever. Any queued chunks still in flight are **discarded at the source** —
  every one of them was already written to the ring buffer before being broadcast, so
  they come back on the reconnect's replay from the client's `since` cursor; nothing
  is lost. The close frame itself is BEST-EFFORT (the stalled socket may never drain
  it; its write carries a bounded 5 s deadline set on this teardown path only — a
  write deadline for healthy connections is the separate liveness issue #83), so the
  client must treat **any** abnormal close as "reconnect with your stored `since`
  cursor", not only this one. The shipped UI's `ws.onclose` logs the code and reason
  and reconnects after 5 s; the server also logs the overflow.
- `1013` with a dynamic error reason — handshake/setup failures on this endpoint
  (upgrade, replay-read or subscribe errors); the connection is closed rather than
  served half-configured, and the client retries like any other abnormal close.

**Timeout & Fallback:**
- Client should implement handshake timeout (recommended: 5s)
- On timeout, close WebSocket and fallback to SSE: `GET /api/instances/log/stream?id=<instanceId>`

**Example Flow:**
```
Client                    Server
   |                         |
   |--- Connect ----------->|  (optionally ?since=<cursor> on reconnect)
   |<-- {"type":"ready"} ----|  Handshake
   |                         |
   |-- {"type":"resize", --->|  Notify terminal size
   |    "cols":80,"rows":24} |
   |                         |
   |<-- binary output -------|  Replay: tail, or bytes after `since`
   |<-- {"type":"sync", ----|  End offset of that replay
   |    "offset":4096}      |
   |                         |
   |--- (50ms delay) -------|
   |                         |
   |-- {"type":"resize", --->|  Trigger TUI redraw
   |    "cols":80,"rows":24} |
   |                         |
   |--- input bytes -------->|  User input
   |<-- binary output -------|  Process output
```

### Delete
`POST /api/instances/delete`

Body:
```json
{ "id": "<instanceId>" }
```

Deletes a stopped (non-running) instance record. The instance's in-memory log buffer is also released, decrementing the global log-buffer accounting.

### Log replay (tail / incremental)
`GET /api/instances/log?id=<instanceId>[&since=<byteOffset>]`

- Without `since`: returns the recent tail (newest bytes) as `text/plain`; the response also includes `X-Log-Offset: <endOffset>` — the cursor at the end of the tail, usable as `since` on a follow-up incremental read.
- With `since`: returns incremental content from byte offset and includes response header `X-Log-Offset: <nextByteOffset>`.
- Logs live in an in-memory ring buffer attached to the **running** instance (see `docs/ARCHITECTURE.md` §4.1 *Instance log buffer*). After the instance stops, exits, or fails — or after the daemon restarts — the buffer is released and this endpoint returns `200 OK` with an empty body (tail reads carry `X-Log-Offset: 0`; incremental reads echo the requested `since`). Unknown / never-started instance IDs behave the same.
- The `byteOffset` cursor is the running total of bytes the instance has produced (monotonic; never decreases). When `since` points to data that has already been evicted from the ring (oldest-byte > since), the response silently clamps to the oldest live byte and `X-Log-Offset` advances accordingly.

Response: `text/plain`

### Live log stream (SSE)
`GET /api/instances/log/stream?id=<instanceId>[&since=<byteOffset>]`

- Server-Sent Events stream.
- Emits `event: log` with JSON payload:
```json
{"chunk":"...","next":12345}
```
- Same in-memory backing as the tail endpoint above. Without `since` (or with a negative `since`), the stream starts from the tail (newest bytes), same as the log endpoint. The cursor `next` is the same monotonic byte counter; clients should echo it as `since` on the next request to receive only new chunks.
- Polling cadence: 1 s. When no new data is available, the server emits an SSE comment line (`: ping`) as a keep-alive — no `log` event, no cursor update. Clients should treat the absence of a `log` event as "no progress" and keep using the last `next` they saw.
- Stopped / unknown / never-started instance IDs return `200 OK` and emit one empty `log` event followed by `: ping` keep-alives — the stream stays open; clients decide when to give up. The cursor in that first event is `0` for a tail read (omitted / negative `since`), and echoes the requested `since` for an incremental read.

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
      "memory_buffer_bytes": 5242880,
      "memory_buffer_cap_bytes": 33554432,
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
    "instance_count": 3,
    "daemon_cpu_percent": 1.2,
    "daemon_memory_bytes": 67108864
  }
}
```

Fields:
- `cpu_percent`: CPU utilization as a percentage of a single core. 0% on the first measurement (no prior baseline).
- `memory_rss_bytes`: Resident Set Size — actual physical memory used by the process.
- `memory_buffer_bytes`: Actual bytes currently held in the instance's in-memory ring buffer (0 if the instance is stopped or has no buffer).
- `memory_buffer_cap_bytes`: Pre-allocated capacity of the instance's in-memory ring buffer (0 if the instance is stopped or has no buffer). When both `memory_buffer_bytes` and `memory_buffer_cap_bytes` are non-zero, the buffer is active with `used / cap` semantics.
- `connection_type`: `"websocket"` if the instance has an active WebSocket TTY connection, `"sse"` if using the SSE fallback, `"none"` otherwise.
- Worktree subtotals aggregate only `running` instances (instance RSS only, not buffer memory).
- Global totals include both all running instances and the daemon process itself (`daemon_cpu_percent`, `daemon_memory_bytes`).

### 5.9 Instance lifecycle (frontend)

All page close/refresh/navigation events trigger a browser-native confirmation dialog. This is a purely client-side UX feature:

- **Trigger**: `beforeunload` event on `window`
- **Behavior**: Calls `event.preventDefault()` and sets `event.returnValue = ''` to force the browser to show its native confirmation dialog
- **No backend involvement**: Instances continue running regardless of the user's choice
- **Condition**: Always triggered on any close action — no dependency on instance state

### 5.10 Reasonix web UI proxy

For `kind: "reasonix"` instances the web chat UI is served under:

```
GET /rx/<instanceId>/...
```

- **Independent origin (loopback only)**: when the main listener is loopback-only (`127.0.0.1` / `localhost`, and no TLS), the proxy is mounted on a dedicated loopback listener (`127.0.0.1:<random>`), reported to the frontend as `web_url` in `GET/POST /api/instances` responses (view-only field, not persisted). The iframe loads that URL, so the embedded reasonix page is **cross-origin** with the myworktree API — a script inside the chat iframe cannot silently call `/api/*` with the user's session (issue #44).
- **Same-origin fallback (network / TLS)**: when TLS is configured (`--tls-cert/--tls-key`, an `http://127.0.0.1` iframe inside an https page would be blocked as mixed content) **or the main listener is open to the network** (default `0.0.0.0`, or an explicit LAN IP — a remote browser would resolve `127.0.0.1` to itself and the iframe would fail), the independent listener is skipped; `web_url` is empty and the frontend falls back to the **relative** same-origin path `/rx/<id>/`, which follows the browser's current origin — so LAN/remote access works (same as the pre-#44 behavior).
- The proxy forwards to the instance's `reasonix serve` at `http://127.0.0.1:<port>` (port/token cached in memory by the driver — issue #46), injecting `Cookie: reasonix_token=<token>` (name from `reasonix.CookieName`, single source — issue #45) for auth.
- HTML responses get a script injected (single injection point before `</head>`) that prefixes the page's root-relative `fetch` / `EventSource` / `XMLHttpRequest` calls with `/rx/<id>/`, plus the issue #48 layout injection: the 220px sidebar is **collapsed by default** on desktop with a dedicated toggle button (`--mw-sidebar-w` CSS var makes the expanded width configurable); narrow screens keep the native mobile sidebar.
- The `Accept-Encoding` header is forced to `identity`; only the myworktree auth `?token=` parameter is stripped from the query before forwarding upstream (`authq.StripToken` — every other query parameter, e.g. `?session=`, passes through; parse-failed queries are still token-scrubbed rather than forwarded raw).
- **Remote-access authentication**: when the UI is opened with `?token=` in the address bar (portal jump / remote access), the server syncs the token into the HttpOnly `mw_token` cookie on the response (`withAuth`); the iframe then navigates with a plain relative `/rx/<id>/` URL and authenticates via the cookie — the token never appears in the embedded document's `location.search`, and a rotated token self-heals on the next cookie refresh.
- SSE (`/events`) is streamed through (`FlushInterval=-1`); the upstream sends its own 15s `: ping` keepalive.
- Returns `404` for unknown/non-reasonix instance ids, `503` when the instance is not running, `502` when the backend is unreachable.
- Driver version gate: `Start` runs `reasonix --version` and rejects CLIs older than `1.22.0` (configurable via `Driver.MinVersion`); readiness failures include the tail of the instance `serve.log` (issue #45).
- Tag semantics apply like other kinds: `tag.Env` is injected into the serve environment, `tag.preStart` runs before serve (with `REASONIX_HOME` / `REASONIX_STATE_HOME` stripped exactly like serve), and `tag.Command` is ignored (a reasonix instance runs the agent, not a shell command).

### 5.11 opencode-web info (instance proxy metadata)

`GET /api/instances/<id>/opencode`

Returns the iframe source URL and metadata for an opencode-web instance. Only valid for instances with `kind: "opencode-web"`; returns `404` for other kinds or non-existent instances. Returns `503` if the opencode server process is not yet ready (port not populated).

Response (200):
```json
{
  "iframe_src": "/__opencode/<id>/",
  "api_base": "/__opencode/<id>",
  "worktree_path": "/abs/path/to/worktree",
  "host": "127.0.0.1",
  "port": 51234,
  "version": "1.18.16",
  "version_supported": true
}
```

`host` and `port` are the opencode server's bound address. `iframe_src` is the full-page SPA root to load in the iframe (the deep `/session/` link was replaced by the full page — see `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` §0.4). `version` is the installed `opencode --version` probed at spawn; `version_supported` is `false` when it is outside the `1.18.x` range the injected hide script targets (advisory only — the instance still starts). The upstream `OPENCODE_SERVER_PASSWORD` is `cfg.AuthToken` (unified auth token — every opencode-web instance shares the same upstream password, gated by the myworktree bearer token at the proxy); see `docs/ARCHITECTURE.md` §8 for the threat model and the review checklist.

### 5.12 opencode reverse proxy

`/__opencode/<id>/*`

Reverse proxy to the opencode HTTP server backing the given instance. Protected by myworktree's global token authentication (same as all instance routes). Go-side proxy injects `Authorization: Basic base64("opencode:"+password)` and adds `?directory=<worktree>` to GET/HEAD API requests when missing from the original query. HTML navigation responses are rewritten (assets re-routed through the proxy, `<base>` + injected script for URL rewriting, localStorage server-list normalization, and cross-worktree switch-entry hiding) with a matching CSP hash.

- Returns `404` if the instance does not exist or `kind` is not `"opencode-web"`
- Returns `503` if the opencode server is not yet listening
- Returns `502` if the opencode server is unreachable during proxying
- The myworktree auth `?token=` parameter is stripped from the query before forwarding upstream (`authq.StripToken`, shared with the `/rx/` proxy) — the opencode subprocess never sees the credential. Remote-access iframe navigations authenticate via the `mw_token` cookie synced by `withAuth` (see §5.10), so the token is not needed in the iframe URL.

### 5.13 opencode-web scope (out-of-scope state)

`GET /api/instances/opencode/scope?id=<id>`

Returns the last observed out-of-scope state for an opencode-web instance, recorded in-memory by the reverse proxy from directory-bearing requests. The frontend polls it (~1.5s) to render the persistent warning bar. See `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` §4.4.

Response (200):
```json
{
  "scope": "out-of-scope",
  "directory": "/abs/path/to/other/worktree",
  "cross_project": false,
  "at": 1753500000,
  "csp_anchor_missing": false
}
```

`scope` is `in-scope` / `out-of-scope` / `cross-project`; `csp_anchor_missing` marks structural drift (the homepage CSP lost the `'wasm-unsafe-eval'` anchor the injected script's hash is appended after), which the frontend surfaces as the "hiding not effective" warning.

### 5.14 dsh-web info (instance proxy metadata)

`GET /api/instances/dsh?id=<id>`

Returns the iframe source URL and metadata for a dsh-web instance. Only valid for instances with `kind: "dsh-web"`; returns `404` for other kinds or non-existent instances.

Response (200):
```json
{
  "iframe_src": "http://127.0.0.1:35422/",
  "proxy_host": "127.0.0.1",
  "proxy_port": 35422,
  "host": "127.0.0.1",
  "port": 35421,
  "worktree_path": "/abs/path/to/worktree",
  "version": "0.2.0",
  "version_supported": true,
  "remote_capable": true,
  "min_remote_version": "0.2.0",
  "overlay_verified": true
}
```

Failed instance — `missing_dsh` is present **only** when spawn failed because the `dsh` executable could not be resolved (instance `failed`); there is then no live upstream, so `host` / `port` / `version` come back empty and never sit next to a live `version`:
```json
{
  "iframe_src": "",
  "proxy_host": "",
  "proxy_port": "",
  "host": "",
  "port": "",
  "worktree_path": "/abs/path/to/worktree",
  "version": "",
  "version_supported": false,
  "remote_capable": false,
  "min_remote_version": "0.2.0",
  "overlay_verified": false,
  "missing_dsh": {"npm_available": true, "suggested_pin": "0.2.0-rc.2"}
}
```

- `host`/`port` are the upstream `dsh web` server's bound address; `proxy_host`/`proxy_port` are the per-instance myworktree reverse-proxy listener (a **dedicated loopback origin** — the dsh SPA hardcodes its API base to `location.origin + '/api'`, so same-origin subpath mounting is not possible). `iframe_src` is what the iframe loads (plain `http://127.0.0.1:<proxyPort>/` locally).
- **Remote access**: when the main listener is non-loopback or TLS, the proxy binds the main listener's host with a **mandatory token gate** — `?token=` or the `mw_token` cookie, validated on every **non-loopback client** request including WebSocket upgrades (loopback clients bypass the gate, the same trust model as the main UI). The embed is a dedicated origin, so the main-origin HttpOnly `mw_token` cookie cannot travel to it — **the server appends `?token=` to `iframe_src` itself** (page JS can never read the HttpOnly cookie, and portal/login flows carry no address-bar token); the frontend only falls back to an address-bar token for a src that somehow lacks one. The proxy validates the first navigation, sets the HttpOnly `mw_token` cookie on the proxy origin, and 302-redirects to the token-free URL so the embedded document never retains the token in its own `location.search`. The token is stripped (`authq.StripToken`) before anything is forwarded upstream (see `docs/ARCHITECTURE.md` §9).
- `version` is the installed `dsh --version` probed at spawn — the **parsed core** (`x.y.z`; npx mode records the raw pinned version instead); `version_supported` is `false` when it is outside the `[0.2.0, 0.3.0)` range the restrict overlay and the slash-style RPC wire target (advisory — the instance still starts; the hard gate below `0.2.0` blocks startup — 0.1.x is out of support). `overlay_verified` (L2 check) is `false` when a spawn-time `dsh web --dump-config --patch <restrict.yml>` run did not confirm the four overlay rows (`storage-json` root redirect, `directory-picker` composer disabled, `directory-picker-browse` host backend inserted and not disabled, `client-hmr` disabled) — the frontend then shows the "裁剪失效" (restriction not effective) warning. `missing_dsh` belongs to the **failed instance only** (second example above): it is present only when the `dsh` executable was not found at spawn (instance `failed`, `host`/`port`/`version` empty — never alongside a live `version`); `npm_available` tells the frontend whether the install option is offered, `suggested_pin` is the pinned npx version.
- Design: `docs/plans/dsh-native-ui/FEASIBILITY.md`; threat model: `docs/ARCHITECTURE.md` §9.

### 5.15 dsh-web scope (out-of-scope state)

`GET /api/instances/dsh/scope?id=<id>`

Returns the last observed out-of-scope state for a dsh-web instance, recorded in-memory by the per-instance reverse proxy from **RPC request bodies** (dsh 0.2.x slash-style endpoints — `session/create` with `payload.args.request.{cwd|workspaceId}`, `workspace/create` with `payload.args.request.path`). The frontend polls it (~1.5s) to render the persistent warning bar.

Response (200):
```json
{
  "scope": "out-of-scope",
  "directory": "/abs/path/to/other/worktree",
  "at": 1753500000,
  "foreign_active_sessions": ["session-6f1a…"]
}
```

`scope` is `in-scope` / `out-of-scope`. Observation is record-only — the request is forwarded unchanged; out-of-scope sessions still succeed (their sandbox root is the out-of-scope directory; OS-level write limits still apply) and the warning stays until the user navigates back to the worktree.

`foreign_active_sessions` (omitempty) lists the sessions in the shared `$DSH_HOME/sessions` pool that the daemon's session watch classified as **actively written by another dsh process** (mtime within 90s, excluding sessions this daemon itself drives — own traffic is attributed from `session/*`-family RPC bodies through the proxy (`session/prompt`, `session/cancel`, `session/fork`, `session/rename`, `session/selectModel`, `session/attachment`, `session/updateQueue`, plus the plural-namespaced `subagents/prompt` — ids read from `payload.args.request`), `session/create` responses, and the workspace-bootstrap preseed). dsh is a single-writer-per-process system: opening such a session from the embed appends an unguarded `session/end-seed` and can permanently corrupt the log, so the frontend renders a warning bar telling the user to wait until the session is idle (record-only — nothing is blocked; see `docs/plans/dsh-native-ui/CROSS-PROCESS-SESSION.md`).

### 5.16 dsh-web launch mode

`POST /api/instances/dsh/launch`

Selects how a dsh-web instance resolves the `dsh` executable. Persisted **per worktree** at `<DataDir>/dsh/<worktreeHash>/launch.json` (survives myworktree restarts; keyed by worktree — not instance — because every Start/Restart allocates a fresh instance id and wipes the old per-instance state dir, which would lose the choice exactly when the user needs it: failed instance → choose npx → restart). Body:

```json
{ "id": "<instance-id>", "mode": "npx" }
```

`mode` is one of:

- `"path"` (default) — resolve `dsh` via `exec.LookPath` at every Start
- `"npx"` — spawn `npx --yes @deepseek-ai/dsh@<pin> web --port 0 ...` (pinned version, no interactive prompt; the process is started with `Setpgid` and stopped by killing the whole process group — npx is the parent of dsh, killing only the npx PID would orphan the server)
- `"install"` — run `npm install -g @deepseek-ai/dsh` (see §5.17), then resolve the global bin via `npm prefix -g` and spawn that **absolute path**

Returns `404` for unknown / non-dsh-web instances; `400` for an invalid mode. Used by the missing-dependency dialog (three options: npx launch / install now / cancel) when an instance is `failed` with `missing_dsh` reported by §5.14.

### 5.17 dsh-web install (npm install -g)

`POST /api/instances/dsh/install`

Runs `npm install -g @deepseek-ai/dsh` on behalf of the user (bounded timeout, serialized server-side — concurrent requests queue behind one install). Body: `{ "id": "<instance-id>" }`. On success the driver resolves the global bin directory via `npm prefix -g` and records the absolute executable path in the worktree's `launch.json` (`mode: "install"`), so the next Start spawns it directly — **no daemon restart needed** (kinds re-read `os.Environ()` at every Start; no PATH cache). Response includes the resolved bin path. Returns `404` for unknown / non-dsh-web instances, `502` when `npm` is not available or the install fails (failure output is redacted and truncated). The frontend requires a two-step confirm before calling it (the install rewrites the global npm prefix).

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
- `instance_list`, `instance_start`, `instance_stop`, `instance_input`, `instance_delete`, `instance_log_tail`

### `instance_log_tail`
Args: `{ "id": "<instanceId>", "n": 65536 }` — `n` is the max bytes to return (defaults to `4096` when absent or non-positive).

Returns the **newest** `n` bytes of the instance's ring buffer (tail semantics, same bytes as `GET /api/instances/log` without `since`), not the oldest. Instances that are stopped, exited, or unknown return an empty string, since the buffer is released with the running instance. Instances whose kind does not capture logs (`reasonix`, `opencode-web`, `dsh-web`) also return an empty string.

## 7) Portal Dashboard

The Portal dashboard provides a shared entry point for discovering and accessing all running instances across repos.

Base URL: `http://<host>:<portal-port>/` (default portal port: 12345).

**Auth model**: Portal uses `mw_token` HttpOnly Cookie for authentication. The token is obtained via the CSRF-protected `/api/auth` endpoint. Once authenticated, the Cookie is automatically sent by the browser on all subsequent requests. Cookie has 24-hour sliding expiration (refreshed on each successful auth request).

**CSRF protection**: `/api/auth` and `/api/logout` endpoints use double-submit cookie pattern. Client must fetch a CSRF token from `/api/csrf-token`, then include it in the request body. CSRF tokens are single-use with a 5-minute TTL.

### Dashboard page
`GET /`

Returns the embedded Portal dashboard HTML page (no authentication required).

Response headers:
- `Content-Security-Policy: default-src 'self'; script-src 'sha256-<hash>' ...; style-src 'self' 'sha256-<hash>' ...`
- `X-Content-Type-Options: nosniff`

### Get CSRF token
`GET /api/csrf-token`

Returns a new single-use CSRF token and sets `mw_csrf` Cookie.

Rate limit: 1 request per second per IP.

Response:
```json
{ "csrf_token": "<64-char-hex>" }
```

Sets Cookie: `mw_csrf=<token>; Path=/; SameSite=Strict` (non-HttpOnly — JS must read it for CSRF double-submit).

### Authenticate (login)
`POST /api/auth`

Authenticates with the global auth token. Requires valid CSRF token.

Body:
```json
{ "token": "<auth-token>", "csrf_token": "<csrf-token>" }
```

Rate limit: 20 attempts per minute per IP.

Success (200): Sets `mw_token` HttpOnly Cookie (`Max-Age=86400, SameSite=Lax`) and returns:
```json
{ "status": "ok" }
```

Errors:
- `400`: Auth token not configured on server (`{"error":"auth token not configured on server"}`)
- `401`: Invalid token
- `403`: CSRF token invalid/expired/used
- `429`: Rate limit exceeded

### List instances
`GET /api/list`

**Authentication required** (Cookie `mw_token` or Bearer token).

Returns JSON with all running instances and Portal status. Each successful request refreshes the `mw_token` Cookie's expiration (sliding).

Response:
```json
{
  "is_portal": true,
  "portal_port": 12345,
  "processes": [
    {
      "instance_id": "12345-1710000000-a1b2c3",
      "pid": 12345,
      "port": 50053,
      "host": "0.0.0.0",
      "repo_name": "myproject",
      "repo_hash": "a1b2c3d4e5f6",
      "started_at": "2024-03-10T12:00:00Z",
      "alive": true
    }
  ]
}
```

- `is_portal`: whether the current process holds the Portal port
- `portal_port`: Portal port number
- `alive`: determined by PID liveness and TCP port reachability

### Portal status
`GET /api/portal-status`

No authentication required. Returns whether the current instance holds the Portal port.

Response:
```json
{ "is_portal": true }
```

### Logout
`POST /api/logout`

**CSRF required**. Clears the `mw_token` Cookie.

Body:
```json
{ "csrf_token": "<csrf-token>" }
```

Response (200):
```json
{ "status": "ok" }
```

Always returns 200 (idempotent — successful even if not logged in).

Errors:
- `403`: CSRF token invalid/expired/used or missing

### Reverse proxy (access instance) — planned, not yet implemented
`ANY /s/<repo-hash>/*`

> **Status: Planned but not yet implemented.** Currently the Portal dashboard links directly to instance ports (`http://<host>:<port>/`) instead of using the reverse proxy path. The `/s/<repo-hash>/*` route handler is not registered in the Portal HTTP server.

**Authentication required** (Cookie `mw_token` or Bearer token). Each successful request refreshes the `mw_token` Cookie's expiration (sliding).

Proxies the request to the corresponding instance at `http://127.0.0.1:<port>`. Since the proxy connects via loopback, the instance's auth middleware automatically bypasses token validation.

Security:
- `repo-hash` format validation: only `[a-f0-9]+` (lowercase hex) accepted; path traversal characters (`..`, `/`, `\`) rejected with 400
- WebSocket upgrade is automatically handled by the reverse proxy (Go's `httputil.ReverseProxy` natively supports WebSocket hijacking)

Errors:
- `400`: Invalid `repo-hash` format
- `401`: Not authenticated
- `502`: Target instance offline

### Instance login page (instance-level, HTML)
`GET /login`, `POST /login`

**No authentication required** — this is the login page itself.

The instance server serves an HTML login form at `/login` for browser-based authentication (separate from the Portal JSON API). Non-loopback browser requests that lack valid auth are redirected to this page.

- `GET /login` — Returns an HTML login page with password input and form. If the user already has a valid `mw_token` Cookie that matches the global auth config, they are redirected to `/` immediately.
- `POST /login` — Accepts `token` and optional `next` form fields. On successful auth, sets `mw_token` HttpOnly Cookie (Max-Age=86400, SameSite=Lax, Secure on HTTPS) and redirects to the `next` path (or `/` if not provided). **Note**: `/login` is explicitly exempt from `withAuth` middleware rate limiting; no per-IP rate limits apply to this endpoint.

Errors:
- `401`: Invalid token

## 8) LLM 配置
### 获取当前配置
`GET /api/llm/config`

返回当前 LLM 配置（不包含明文 API Key）：
```json
{
  "protocol": "openai",
  "api_address": "<provider_api_address>",
  "api_key_masked": "<masked_api_key>",
  "model": "<model_name>",
  "reasoning_split": false,
  "is_secure": true,
  "available": true
}
```
- `protocol`: `"openai"` | `"anthropic"`
- `api_address`: API 地址（需要包含完整路径如 `/v1/chat/completions`）
- `api_key_masked`: API Key 脱敏显示（仅显示前 3 字符 + `***` + 后 3 字符）
- `model`: 当前使用的模型名称
- `reasoning_split`: 是否启用思考分离（部分 provider 支持）
- `is_secure`: 当前是否为 localhost 或 HTTPS 环境（影响 LLM Settings 按钮可见性）
- `available`: LLM 是否可用（protocol、API Key、API Address、Model 四项全部已配置）

### 更新配置
`PATCH /api/llm/config`

Body:
```json
{ "protocol": "openai", "api_address": "<provider_api_address>", "api_key": "<api_key>", "model": "<model_name>", "reasoning_split": false }
```

环境变量 `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` 优先级更高。

Response (200):
```json
{ "status": "ok", "protocol": "openai" }
```

### 测试 LLM 连接
`POST /api/llm/test`

测试当前 LLM 配置是否有效（发送一个简单的 test 分支名请求）。
用于用户在配置后验证 API Key 是否正确。

Response (200):
```json
{ "status": "ok", "branch_name": "<branch_name>" }
```

Response (400):
```json
{ "error": "no LLM configured" }
```

```json
{ "error": "invalid API key or network error" }
```

### 生成分支名
`POST /api/llm/generate`

根据任务描述调用 LLM 生成分支名。

Body:
```json
{ "task_description": "fix the login timeout issue" }
```

Response (200):
```json
{ "branch_name": "fix/login-timeout" }
```

Response (400):
```json
{ "error": "no LLM protocol configured" }
```

Response (500):
```json
{ "error": "generation failed: HTTP error: status 401" }
```
