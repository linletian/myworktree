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
{ "worktree_id": "<worktreeId>", "tag_id": "optional", "command": "optional", "name": "optional" }
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

### Delete
`POST /api/instances/delete`

Body:
```json
{ "id": "<instanceId>" }
```

Deletes a stopped (non-running) instance record (best-effort deletes the log file).

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
- `instance_list`, `instance_start`, `instance_stop`, `instance_input`, `instance_delete`, `instance_log_tail`

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
