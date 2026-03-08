# myworktree — HTTP API

Base URL: printed by `myworktree start`, e.g. `http://127.0.0.1:50053/`.

Auth:
- If `--auth <token>` is set, send `Authorization: Bearer <token>`.
- Alternatively, pass `?token=<token>` for simple clients.

## 1) Worktrees
### List
`GET /api/worktrees`

Response:
```json
{ "worktrees": [ {"id":"...","name":"...","path":"...","branch":"..."} ] }
```

### Create
`POST /api/worktrees`

Body:
```json
{ "task_description": "fix login", "base_ref": "" }
```

Response (201):
```json
{ "id":"...","name":"fix-login","path":"...","branch":"wt/fix-login","created_at":"..." }
```

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

## 2) Branches
### List (default + top 10)
`GET /api/branches`

Returns local branches with default branch always first, then branches sorted by last commit time (newest → oldest), max 10.

Response:
```json
{ "default": "main", "branches": [ {"name":"main","commit_unix":1700000000} ] }
```

## 3) Tags
### List (merged: global + project)
`GET /api/tags`

Response:
```json
{ "tags": [ {"id":"...","command":"..."} ] }
```

## 4) Instances
### List
`GET /api/instances`

Response:
```json
{ "instances": [ {"id":"...","worktree_id":"...","tag_id":"...","pid":123,"status":"running"} ] }
```

### Start
`POST /api/instances`

Body:
```json
{ "worktree_id": "<worktreeId>", "tag_id": "optional", "command": "optional", "name": "optional" }
```

If both `tag_id` and `command` are empty, the server starts an **idle** instance (non-interactive MVP placeholder).

Example (ad-hoc command without tags):
```json
{ "worktree_id": "<worktreeId>", "command": "echo hello && ls" }
```

Response (201):
```json
{ "id":"...","pid":123,"status":"running","log_path":"..." }
```

### Stop
`POST /api/instances/stop`

Body:
```json
{ "id": "<instanceId>" }
```

### Log replay (tail)
`GET /api/instances/log?id=<instanceId>`

Response: `text/plain`

## 3) MCP stub
### Tool names
`GET /api/mcp/tools`

Response:
```json
{ "tools": ["worktree_list", "worktree_create", "..."] }
```
