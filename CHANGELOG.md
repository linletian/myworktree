# Changelog

## Unreleased

Disk write amplification fix for long-running PTY-heavy sessions.

- **feat(instance): opencode-web kind embedding opencode official web UI** — implemented: `internal/instance/opencode.go`, `internal/ui/proxy.go`, `/__opencode/<id>/*` reverse proxy, iframe panel + OC badge in `internal/ui/static/index.html`. Design, threat model, and review checklist in `docs/ARCHITECTURE.md` §8.

### Breaking changes

- **`state.json` schema: `log_path` field removed** — external tooling that reads instance state files must drop the `log_path` field. Old state files continue to load cleanly (the field is silently ignored by JSON decoding). See `docs/ARCHITECTURE.md` §3.2.

### Highlights

- **In-memory ring buffer for PTY logs** — replaced per-instance on-disk log files with a bounded ring buffer (default 32 MB per instance, hard ceiling 256 MB, total budget capped at 25% of system RAM). Eliminates the 100,000× write amplification caused by the old `enforceMaxLogSize` loop. No more disk I/O from PTY logging on the steady state.
- **Bounded adaptive sizing** — new `LogBufferBytes` config knob overrides the per-instance cap (clamped to 16–256 MB). When unset, the cap is `clamp(available / 16, 16 MB, 256 MB)` sampled via gopsutil with a 60 s cache to amortize syscall cost across batch starts.
- **Startup cleanup** — `Manager.PurgeOrphanLogFiles` removes dead `.log` files left behind by the old code path on the first start of the new binary.
- **Budget-exceeded UX** — when a new instance would push the global buffer budget past 25% of system RAM, the HTTP/MCP API returns `503 Service Unavailable` with a structured `log_buffer_budget_exceeded` body. The dashboard surfaces this as a modal with used/limit numbers and a hint to close other tabs.
- **Hot-path redesign for heavy TUI workloads** — `pumpLogs` no longer acquires `stateMu` per 1024-byte PTY chunk; the ring buffer pointer is pre-fetched once and the per-chunk lookup is a single `atomic.Pointer.Load`. The ring buffer's backing slice is allocated eagerly so the first Write never blocks on a 32 MB malloc. A new `WriteString` path avoids the `[]byte(chunk)` conversion that would otherwise happen on every chunk.
- **Daemon resource monitoring** — the resource stats API (`GET /api/instances/stats`) now includes the mw daemon process itself in global totals (`daemon_cpu_percent`, `daemon_memory_bytes`). The UI displays a dedicated "mw daemon" row so users can distinguish daemon overhead from instance resource usage.
- **Ring buffer usage reporting** — per-instance stats now expose `memory_buffer_bytes` (actual usage) and `memory_buffer_cap_bytes` (pre-allocated capacity). The UI memory column shows the combined `RSS + buffer_used` with a `buf used/cap` annotation for active buffers, giving users visibility into per-instance buffer memory cost.
- **Sidebar GitHub link** — main workspace row now shows a small GitHub Mark icon to the right of the project name when `git remote` resolves to `github.com`; clicking opens the canonical `https://github.com/<owner>/<repo>` URL in a new tab. Source of truth is a new `github_url` field on `GET /api/main`, computed via the new `gitx.GitHubURL` helper (prefers `origin`, then falls back to iterating `git remote`; normalizes SCP / HTTPS / `ssh://` forms; strips `.git`). GitHub Enterprise and non-GitHub remotes are intentionally not surfaced.
- **Unified auth token for opencode-web** — `OPENCODE_SERVER_PASSWORD` now equals `cfg.AuthToken` for every opencode-web instance (replacing per-instance random passwords). The reverse proxy at `/__opencode/<id>/*` injects Basic auth with the same token. `state.json` no longer carries a per-instance `password` field. Threat model and review checklist in `docs/ARCHITECTURE.md` §8.
- **Graceful goroutine lifecycle** — `Manager` gains `instCtx` + `instWG`. `Restart` / `Delete` cancel the instance context and `Wait()` for `pumpAndWatch` / `opencodeHealth` / `wait` (opencode-web) or `pumpLogs` / `wait` (PTY) to drain before returning, so callers never observe stale goroutines touching `m.buffers[id]` / `m.ocRT[id]` after a lifecycle transition. `Stop` keeps its existing fire-and-forget semantics.
- **In-memory health-fail counter** — `opencodeHealth` no longer writes `_health_fail_count` to `Extra`; the counter lives in `Manager.ocRT[id]` (atomic Int32, cleared on restart). Reduces store writes from "every probe" to "every successful listen-address parse".
- **`UpdateExtra` is now merge + retry** — preserves pre-existing keys (e.g., `worktree_abs`), retries on `ErrVersionConflict` so concurrent writers don't silently drop updates, and transitions `starting → running`.
- **`password_set` removed from `/api/instances/<id>/opencode` response** — was always `true` and no longer meaningful; no frontend consumer. See `docs/API.md` §5.10.

## v0.3.0

Release focused on remote collaboration, build robustness, and Apple Silicon reliability.

Highlights:
- Added **Portal Dashboard** — a shared entry port with auto-discovery of running instances across repos, global auth token (HttpOnly Cookie, CSRF protection), and Tailscale serve readiness.
- Improved **remote access** — instances and portal now bind to `0.0.0.0` by default, auth token auto-generates on first run, and the login flow supports CSRF-protected forms for non-loopback clients.
- Integrated **LLM-powered branch naming** — configurable protocol (OpenAI / Anthropic), with reasoning split support and a manual override option.
- Upgraded terminal shell with **xterm.js v6.0.0** and fixed Chinese IME shift-symbol fullwidth issues.
- Enhanced the **Changes panel** with separate Staged / Unstaged accordion sections, untracked file tracking, and per-file diff stats.
- Streamlined instance lifecycle — instances can be deleted directly on stop (no archive step), with per-worktree tab reordering via optimistic locking.

Build hardening for Apple Silicon:
- Release builds now set `CGO_ENABLED=0` to guarantee pure-Go cross-compilation from Linux to Darwin.
- Removed `-w` linker flag to preserve macOS code-signing compatibility.
- Added optional macOS codesign + notarization job (enabled via repository variables/secrets) to resolve Gatekeeper blocking on Apple Silicon Macs.
- Users who still encounter "no response" on Apple Silicon can run `xattr -d com.apple.quarantine ./mw` to clear the download quarantine attribute.

Documentation and validation:
- Expanded API, architecture, and PRD docs to cover the Portal Dashboard, remote access flow, CSRF protection, and auto-auth generation.
- Release packaging continues to publish Darwin `amd64` / `arm64` archives plus SHA256 checksums via the tag-triggered GitHub Actions workflow, with an optional codesign job.

## v0.2.0

Feature release focused on workspace visibility, terminal continuity, and day-to-day usability improvements.

Highlights:
- Added a pinned **Main Workspace** alongside managed worktrees, with live branch tracking and support for launching managed instances directly in the main repository.
- Improved instance management with rename support, per-worktree tab reordering with optimistic locking, bulk purge for archived instances, and preserved per-instance terminal sessions when switching between running instances.
- Expanded workspace tooling with localhost-only quick actions to open Terminal/Finder, a Git Changes panel with per-file diff stats, and a resource monitor modal showing CPU, memory RSS, and transport state per instance.
- Hardened terminal and git integration with multi-client TTY resize sync, browser close protection, detached-HEAD-safe branch queries, timeout-protected git commands, and more reliable diff parsing/error handling.

Documentation and validation:
- Expanded API, architecture, and PRD docs to cover the main workspace, quick actions, git changes, resource stats, optimistic-locking instance operations, and terminal session behavior.
- Release packaging continues to publish Darwin `amd64` / `arm64` archives plus SHA256 checksums via the tag-triggered GitHub Actions workflow.

## v0.1.2

Focused follow-up release that tightens terminal switching and reconnect behavior after `v0.1.1`.

Highlights:
- Improved shared-terminal reset behavior when switching instances so stale private modes, alternate-buffer state, and scrollback are cleared more reliably before replay or reattach.
- Reduced unnecessary WebSocket reconnect churn for healthy running instances to avoid long-output TUI flicker and duplicate replay during transport recovery.
- Sanitized frontend terminal output more aggressively by dropping DECSET `?1007h`, preventing mouse-wheel scroll from being remapped into shell Up/Down input after certain TUI sessions.

Documentation and validation:
- Expanded architecture notes with a stricter instance-switch timing protocol and focus rules for WebSocket TTY handshakes.
- Added terminal test-case coverage documenting instance-switch reset expectations and reconnect behavior for interactive CLIs such as Copilot CLI.

## v0.1.1

Recommended stable release after the `v0.1.0` GitHub Release assets were withdrawn during post-release validation. The `v0.1.0` tag remains the comparison baseline, but `v0.1.1` is the supported public release.

Highlights:
- Fixed multiple terminal/TUI regressions that could leak terminal query responses into the shell, break UTF-8 output across transport boundaries, or leave the terminal in a bad state after switching instances.
- Added a WebSocket TTY handshake plus browser-to-PTY resize propagation so interactive programs start with a valid terminal size and reconnect more reliably.
- Improved terminal UX with larger client-side scrollback, safer reconnect/state transitions, and instance-switch behavior that avoids stale mouse-tracking side effects.
- Fixed managed worktree edge cases when deleting entries whose directories were already removed, and kept compatibility when importing older `wt/<name>` worktrees.

Documentation and validation:
- Added deeper terminal I/O analysis, filter review notes, and terminal-focused test cases to document the root causes behind the `v0.1.1` fixes.
- Expanded automated coverage for PTY resize behavior, redaction behavior, and worktree integration scenarios touched by the release.

## v0.1.0

Initial public release of `myworktree`.

Highlights:
- Manage isolated `git worktree` task directories from a small local-first UI.
- Start and reconnect long-running CLI instances per worktree.
- Use tag templates to standardize instance startup commands and environment.
- Replay redacted logs over WebSocket/SSE/HTTP fallback transports.
- Optional token auth and built-in TLS for non-loopback access.

Release engineering:
- Added `myworktree version` / `myworktree --version` and matching `mw` version output.
- Added macOS coverage to CI and a tag-triggered GitHub release workflow for darwin artifacts.
- Updated installation and runtime docs for release packaging.
