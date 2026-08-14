# myworktree

> **An ORCA-like lightweight agents team orchestrator** — spin up multiple AI coding CLIs side-by-side, each in its own isolated git worktree with a persistent re-attachable terminal, and steer them all from a minimal Web UI.

- 中文说明: [README.zh-CN.md](./README.zh-CN.md)
- Docs: [PRD](./docs/PRD.md) · [Architecture](./docs/ARCHITECTURE.md) · [API](./docs/API.md)

![](docs/codecliteams.png)

![](docs/webui.png)

## Features

- **One worktree per task, kept apart by git** — every agent lives in its own isolated worktree (typically its own branch), so half-finished changes, dependency installs, and experiments never collide.
- **Persistent, re-attachable terminals** — every agent runs in a managed instance that survives page reloads and browser closes; reopen anytime, scroll back the full output, and keep going.
- **Output replay without disk writes** — every keystroke lands in a bounded in-memory ring buffer, so you can scrub back through what an agent did while you were away.
- **Bring your own agents** — OpenCode and Reasonix run out of the box (Reasonix even with its native web chat UI embedded in the sidebar); for everything else, drop a Tag template (`command/env/preStart/cwd`) and bring up Claude Code, Codex, GLM, Qwen, or any other CLI.
- **One place to see what's running** — worktrees, instances, output, and PTY state in a single minimal Web UI — no IDE, no editor, no context switch.

## What makes myworktree different

- **Single Go binary, zero runtime deps** — no desktop app, no bundled editor; just `mw` and a browser tab.
- **Headless-friendly by design** — drop it on a server, a VM, or a CI box; everything is reachable over Tailscale.
- **Portal Dashboard** — a single shared entry port auto-discovers running instances across every repo on the host.
- **Global auth & CSRF out of the box** — HttpOnly Cookie + double-submit CSRF, plus an auto-generated 32-char hex token on first run.
- **Hot config reload** — `mw config regen` rotates the auth token and reapplies settings without restarting the daemon.
- **File preview & diff in the Changes panel** — click any changed or untracked file for a line-numbered, diff-aware preview.
- **Reasonix web-chat instances** — tick one box to run `reasonix serve` in a worktree, with its web chat UI embedded in the same sidebar.
- **Branch divergence badge** — see at a glance when a branch is ahead of or behind its upstream, with scheduled refresh.

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
- Provides a minimal Web UI to list worktrees/instances and **replay/follow output**
- Supports **Tag** templates (`command/env/preStart/cwd`) to start instances with the right setup, without baking project-specific logic into the manager

## Features (MVP)
- Create/list/import/delete managed worktrees (strict delete: refuses if dirty)
- Start/list/stop managed instances per worktree via **Tag** templates
- Instance restart support (keeps worktree/tag-or-command and links old/new instance records)
- Web UI can be closed/reopened; instances keep running; WebSocket Web TTY is default (with SSE/HTTP fallback)
- UI shows transport status (`websocket/sse/polling`) and provides WS reconnect action
- Startup reconcile: stale persisted `running` instances are auto-marked `stopped` after mw restart
- Optional built-in HTTPS (`--tls-cert/--tls-key`) and token auth for non-loopback
- Stored backlog redaction for common secrets (e.g. `sk-...`)
- MCP tool endpoints (`/api/mcp/tools`, `/api/mcp/call`)
- Portal Dashboard with shared entry port, auto-discovery of running instances across repos
- Global auth token (HttpOnly Cookie, CSRF protection, tailscale serve integration)
- Sidebar main workspace shows a GitHub icon next to the project name when the repo's git remote points at `github.com`; clicking opens the canonical `https://github.com/<owner>/<repo>` URL in a new tab. GitHub Enterprise and non-GitHub remotes are intentionally not surfaced.
- **Reasonix web chat instances (MVP)** — check *Reasonix (web chat UI)* when starting an instance to run a `reasonix serve` agent in that worktree and render its web chat UI in an iframe (`/rx/<id>/`, same-origin reverse proxy with token cookie injection and URL-prefix rewrite). The serve uses the user's real `~/.reasonix` (no per-instance isolation): sessions/history/config/credentials are shared per project exactly like a terminal-run `reasonix`, so the same project's history (including terminal sessions) is visible/switchable in the embedded sidebar. Each Start opens a fresh session (no `--resume`); deleting an instance never touches the shared session pool. Requires a `reasonix` binary on `PATH`; the reasonix UI sidebar (project switching) is intentionally left visible in this MVP.
- **In-memory PTY log ring buffer** — per-instance PTY logs live in a bounded in-memory ring buffer (default 32 MB per instance, hard ceiling 256 MB, total budget capped at 25% of system RAM) instead of on-disk log files, eliminating disk write amplification on long-running PTY-heavy sessions. `LogBufferBytes` overrides the per-instance cap; a start that would exceed the global budget returns `503` with a structured `log_buffer_budget_exceeded` body.
- **File preview in the Changes panel** — click any changed or untracked file to preview it with line numbers, a formatted view, and a diff view (synthetic diff for untracked files, `quotePath` handled).
- **Branch divergence badge** — the sidebar shows a diverge badge when a branch is ahead of / behind its upstream, backed by scheduled refresh.
- **`mw config regen` & hot auth reload** — regenerate the config from the CLI; auth token / config changes take effect without restarting the daemon.
- **Tags config directory** — open the tags config directory straight from the UI, with sensible default tags out of the box.
- **Daemon resource monitoring** — the resource stats API now includes the mw daemon process itself in global totals, shown as a dedicated row in the UI.

## Requirements
- macOS 12+ (other platforms are not validated yet)
- `git`
- `zsh`
- `script` (used to host managed interactive shells)
- Go toolchain to build (Go modules are statically linked at compile time; zero runtime dependencies)

## Quick start

### Release binaries

If you just want to use `myworktree`, download the latest release assets from GitHub Releases:

- Apple Silicon Macs: `myworktree_vX.Y.Z_darwin_arm64.tar.gz`
- Intel Macs: `myworktree_vX.Y.Z_darwin_amd64.tar.gz`
- Integrity file: `checksums.txt`

Example:

```bash
# Pick the archive that matches your Mac, then verify and unpack it.
curl -LO https://github.com/linletian/myworktree/releases/download/v0.4.0/myworktree_v0.4.0_darwin_arm64.tar.gz
curl -LO https://github.com/linletian/myworktree/releases/download/v0.4.0/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf myworktree_v0.4.0_darwin_arm64.tar.gz

# Optional: install into PATH
sudo install -m 755 ./mw /usr/local/bin/mw
sudo install -m 755 ./myworktree /usr/local/bin/myworktree

# Verify the downloaded binary
mw --version
```

Start from `v0.4.0` or newer for public release binaries. The earlier `v0.1.0` GitHub Release assets were withdrawn after post-release validation uncovered severe terminal interaction issues, and `v0.4.0` is the current recommended public release.

Each release archive contains `mw`, `myworktree`, `README.md`, `LICENSE`, and `CHANGELOG.md`.
If there is no prerelease/release asset yet, or you need a platform we do not publish, follow the source build steps below.

**Apple Silicon troubleshooting:** macOS may quarantine downloaded binaries and silently prevent execution (Gatekeeper). If the binary does not respond or shows "cannot be opened":
```bash
xattr -d com.apple.quarantine ./mw ./myworktree
```
Or open **System Settings → Privacy & Security** and click "Allow Anyway" for the blocked binaries.

### Build & install

```bash
# Build (in the myworktree source repo)
cd /path/to/myworktree
go build -o myworktree ./cmd/myworktree

# Optional: build alias command `mw` (equivalent to `myworktree`)
# (`mw` auto-opens browser by default; disable with `-open=false`)
go build -o mw ./cmd/mw
```

Strongly recommended: install built binary into your user/system PATH (example for macOS):

```bash
# Optional: install into PATH (pick ONE approach)
# A) user-local bin
# mkdir -p ~/bin
# mv /path/to/myworktree/myworktree ~/bin/myworktree
# mv /path/to/myworktree/mw ~/bin/mw
# B) system-wide (usually already in PATH)
# NOTE: install expects a *built binary*, not the Go source directory (so NOT cmd/mw).
# cd /path/to/myworktree && go build -o mw ./cmd/mw && go build -o myworktree ./cmd/myworktree
# sudo install -m 755 ./myworktree /usr/local/bin/myworktree
sudo install -m 755 ./mw /usr/local/bin/mw

# Alternative: go install (installs into GOBIN/GOPATH/bin)
# go install ./cmd/mw
# go install ./cmd/myworktree
```

Check the build metadata:

```bash
myworktree --version
mw version
```

### Run

```bash
# Run (inside target git repository)
cd /path/to/target/git/repo

# Run via absolute path if PATH is not configured
# /path/to/myworktree/mw -listen 127.0.0.1:0

# `-listen` is optional; using port `0` means auto-select and persist a repo-bound port.
# For the same repo, future runs will reuse that port when available.
# myworktree prints the full URL with actual port.
# /path/to/myworktree/myworktree -listen 127.0.0.1:0
# /path/to/myworktree/mw -listen 127.0.0.1:0
mw

# Optional: use a fixed port
# /path/to/myworktree/myworktree -listen 127.0.0.1:50053
# /path/to/myworktree/myworktree -open=true
```

When startup succeeds, `mw` opens the web page automatically at the serving URL by default.
`myworktree` prints the URL without opening a browser unless you pass `-open=true`.

When Portal is enabled, the startup output includes:
```
[portal] Portal dashboard at: http://0.0.0.0:12345/
[portal] Tailscale URL: https://my-machine.tail-scale.ts.net/
```

myworktree uses the **current working directory** to detect the target repo (git root) and derives an isolated per-project data dir from it, so you can manage other projects by running the same binary in a different repo directory.

By default, newly created worktrees are placed next to your repo:
`<repo-parent>/<repo-name>-myworktree/<worktree-name>/`.
Use `-worktrees-dir=data` to use the legacy location under the per-project data dir, or set `-worktrees-dir` to a custom path.

## CLI examples
```bash
# version
myworktree --version
mw version

# worktrees
myworktree worktree new "fix login 401 and add tests"
myworktree worktree list
myworktree worktree delete <worktreeId>

# tags
myworktree tag list

# instances
myworktree instance start --worktree <worktreeId> --tag <tagId>
myworktree instance start --worktree <worktreeId> --cmd "echo hello && ls"
myworktree instance start --worktree <worktreeId>  # starts an interactive shell instance
myworktree instance list
myworktree instance stop <instanceId>

# config (global auth token)
mw config              # interactive guided setup (set/view/clear/regen token)
mw config set-auth     # set token directly
mw config get-auth     # view token (masked)
mw config clear-auth   # clear token
mw config regen        # regenerate token (with confirmation)

# start with remote access & Portal (IPv6 is explicitly disabled)
mw start --listen 0.0.0.0:0                     # LAN access, auto-inherits global token
mw start --listen 0.0.0.0:0 --portal-port 12346 # custom Portal port
mw start --listen 0.0.0.0:0 --portal-port 0     # disable Portal
```

Note: command starts are executed inside the instance shell, and you can continue sending input to the same running instance from the UI.

## Tag config

Tags are loaded from:
- Global: `$(os.UserConfigDir())/myworktree/tags.json`
- Project: `$(os.UserConfigDir())/myworktree/<repoHash>/tags.json`

Example:

```json
{
  "tags": [
    {
      "id": "backend-dev",
      "command": "npm run dev",
      "preStart": "npm install",
      "cwd": "apps/backend",
      "env": {
        "NODE_ENV": "development"
      }
    }
  ]
}
```

## Local testing & CI

Run local checks before opening a PR:

```bash
test -z "$(gofmt -l .)"
go test ./...
go build -o myworktree ./cmd/myworktree
go build -o mw ./cmd/mw
```

GitHub Actions (`.github/workflows/go-ci.yml`) runs on:
- pushes to `develop` and `main`
- pull requests targeting `develop` and `main` (`opened`, `synchronize`, `reopened`, `ready_for_review`)

The workflow verifies `gofmt`, runs `go test ./...`, and builds both binaries on Ubuntu and macOS.

Tagged releases (`v*`) run `.github/workflows/release.yml`, which produces darwin `amd64` / `arm64` archives plus SHA256 checksums.

## Remote access

### Global Token

Configure a global auth token once, and all instances automatically inherit it:

```bash
mw config
# → Interactive guided setup: [1] set token [2] view token [3] clear token [4] regen token [q] quit
# Token stored in ~/.config/myworktree/auth.json (0600 permissions)
```

When `--auth` is not provided and no token exists in `auth.json`, the CLI **automatically generates a random 32-character hex token** and persists it. This ensures instances always have authentication enabled by default. Instance-level `--auth` override takes precedence over the global token.

### Portal Dashboard

`mw start --listen 0.0.0.0:0` starts a **Portal Dashboard** on port `12345` (configurable via `--portal-port`). The dashboard:

- Lists all running instances across repos with auto-discovery
- Click an instance to jump to its Web UI (directly via instance port — Portal reverse proxy `/s/<repo-hash>/` is planned but not yet implemented)
- Uses **HttpOnly Cookie** (`mw_token`) for authentication — embedded iframe documents never receive the token in their own URL; the portal's address-bar `?token=` (portal jump) is accepted by the server and synced into the cookie on the response, so panels authenticate via the cookie alone
- **CSRF protection** via double-submit cookie pattern on login/logout endpoints
- Cookie has 24-hour **sliding expiration** (refreshed on each auth-successful request)

Set `--portal-port 0` to disable the Portal.

### Tailscale HTTPS

> **Note**: Automatic `tailscale serve` configuration is **currently disabled** due to a Tailscale 1.98 CLI bug on macOS where `tailscale serve --bg` reports success but does not actually configure the proxy. The relevant code exists in `internal/portal/portal.go` but is not wired into the production code paths. Users can still securely access Portal via Tailscale IP (`http://100.x.x.x:12345`) over the WireGuard tunnel. Automatic `tailscale serve` management will be re-enabled once Tailscale fixes the upstream bug.

### Network Security

| Access Path | Protocol | Encryption Layer |
|-------------|----------|-----------------|
| Instance direct (local/LAN IP) | `http://192.168.1.18:PORT` → instance | None (LAN only) |
| Instance direct (Tailscale IP) | `http://100.x.x.x:PORT` → instance | WireGuard tunnel |
| Dashboard + proxy (local/LAN) | `http://host:12345` → proxy `http://127.0.0.1:PORT` | None (LAN only) |
| Dashboard + proxy (Tailscale IP) | `http://100.x.x.x:12345` → proxy `http://127.0.0.1:PORT` | WireGuard tunnel |
| `tailscale serve` domain | `https://machine.ts.net` → proxy `http://127.0.0.1:PORT` | Let's Encrypt TLS + WireGuard |

> **Note**: Tailscale's WireGuard tunnel provides network-layer encryption. Application-layer HTTPS is only used when accessing via `tailscale serve` domain (Let's Encrypt certificate).

## Known Limitations

### Reasonix instances and `REASONIX_HOME`

Reasonix instances run `reasonix serve` **without** a `REASONIX_HOME` override, so the embedded web UI uses your real `~/.reasonix` — exactly like a terminal-run `reasonix`. The driver strips any `REASONIX_HOME` / `REASONIX_STATE_HOME` inherited from the host environment (logging a hint), so the instance and your terminal always share the same per-project session pool; project isolation is done by reasonix itself, per cwd.

Consequence to be aware of: if your shell exports a custom `REASONIX_HOME` (e.g. `~/.custom-reasonix`), a terminal-run `reasonix` uses that custom home while myworktree instances use the default `~/.reasonix` — the two will **not** see each other's history. This is deliberate. To point an instance at a custom home, set `REASONIX_HOME` via the instance tag's `env` (applied after stripping).

Instance lifecycle never touches the shared session pool: Start / Stop / Restart / Delete only manage the `serve` subprocess and its management files (`token`/`port`/`pid`/`serve.log` in the instance state dir). Sessions live in `~/.reasonix/projects/<cwd-slug>/sessions`, so restarting an instance opens a **fresh** session (no `--resume`) while your history stays available in the sidebar.

## License
MIT. See [LICENSE](./LICENSE).
