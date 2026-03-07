# myworktree

A lightweight single-user manager for **git worktrees** and **long-running coding CLI instances**, with a minimal Web UI and output replay.

- 中文说明: [README.zh-CN.md](./README.zh-CN.md)
- Docs: [PRD](./docs/PRD.md) · [Architecture](./docs/ARCHITECTURE.md) · [API](./docs/API.md)

## Background & pain points
When you’re juggling multiple coding tasks in the same repo (often with multiple AI coding CLIs collaborating/reviewing each other), it’s easy to end up with:
- One working directory polluted by half-finished changes, dependency installs, and temporary scripts
- Too many terminals to track (build/test/search/review), with no single place to see what’s still running
- Long-running CLI processes that die when you close a window, or that you can’t easily reattach to later

A common workflow looks like: GPT/GLM drafts docs, Claude/MiniMax implements changes, and Qwen does review — which works best when each “role” has an isolated workspace and a persistent, re-attachable terminal.

## What myworktree does
myworktree is a thin management layer that:
- Uses **git worktrees** to give each task an isolated directory (and typically a dedicated branch)
- Runs multiple managed **instances** per worktree and keeps them alive on the backend
- Provides a minimal Web UI to list worktrees/instances and **replay recent output**
- Supports **Tag** templates (`command/env/preStart/cwd`) to start instances with the right setup, without baking project-specific logic into the manager

## Features (MVP)
- Create/list/import/delete managed worktrees (strict delete: refuses if dirty)
- Start/list/stop managed instances per worktree via **Tag** templates
- Web UI can be closed/reopened; instances keep running; log replay supported
- Optional built-in HTTPS (`--tls-cert/--tls-key`) and token auth for non-loopback
- Stored backlog redaction for common secrets (e.g. `sk-...`)
- MCP extensibility stub (`/api/mcp/tools`)

## Requirements
- macOS 12+
- `git`
- Go toolchain to build (no external Go dependencies)

## Quick start
```bash
# 1) build (in the myworktree source repo)
cd /path/to/myworktree
go build -o myworktree ./cmd/myworktree

# optional: build an alias command `mw` (equivalent to `myworktree`)
go build -o mw ./cmd/mw

# optional: move it into your PATH
# mv ./myworktree ~/bin/myworktree

# 2) run (inside the target git repo you want to manage)
cd /path/to/target/git/repo
/path/to/myworktree/myworktree -listen 127.0.0.1:0
# open the printed URL in a browser

# create a tag config (project-scoped)
# location: ~/Library/Application Support/myworktree/<repoHash>/tags.json
# (see docs/ARCHITECTURE.md for how repoHash is derived)
```

myworktree uses the **current working directory** to detect the target repo (git root) and derives an isolated per-project data dir from it, so you can manage other projects by running the same binary in a different repo directory.

## CLI examples
```bash
# worktrees
myworktree worktree new "fix login 401 and add tests"
myworktree worktree list
myworktree worktree delete <worktreeId>

# tags
myworktree tag list

# instances
myworktree instance start --worktree <worktreeId> --tag <tagId>
myworktree instance list
myworktree instance stop <instanceId>
```

## Remote access
- Default: binds to loopback only.
- If you listen on a non-loopback address, you must set `--auth`.
- For HTTPS, provide `--tls-cert` and `--tls-key`.

## License
TBD.
