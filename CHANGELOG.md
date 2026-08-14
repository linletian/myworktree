# Changelog

## Unreleased

## v0.4.0 (2026-08-12)

Release focused on the native Reasonix web UI integration, eliminating PTY log disk write amplification, and workspace visibility improvements.

**PR #58 评审修复（评审后整理）**：`PATCH /api/instances` 与 `POST /api/instances/restart` 的响应改为与 `GET`/`POST` 一致，reasonix 实例补回 `web_url` 字段；`instanceView` 改为直接构造响应 map（去掉每次序列化后的 Marshal→Unmarshal 往返）；reasonix 实例的 `preStart` 环境与 `serve` 一致地剥离继承的 `REASONIX_HOME`/`REASONIX_STATE_HOME`（宿主导出这两个变量时，preStart 与 serve 不再解析到不同的 `~/.reasonix`，覆盖仍通过实例 tag env 生效）；实例停止/删除/重启时清理 per-instance 的 Start 锁 map 条目与管理目录（不再随启停循环无限累积）；侧栏删除实例的确认文案改为 "Delete instance?"。

**Reasonix instances run without `REASONIX_HOME` isolation (requirement revision, issue #56)**: `serve` now uses the user's real `~/.reasonix`, so sessions/history/config/credentials are shared per project exactly like a terminal-run `reasonix` — the same project's history (including terminal CLI/TUI sessions) is visible and switchable in the embedded sidebar, and cross-project isolation is done by reasonix itself (per-cwd). The per-instance `home` dir, `session.jsonl`, and config/`.env` symlinks are removed; the instance state dir now only carries `token`/`port`/`pid`/`serve.log`, and deleting an instance never touches the shared session pool. Each Start opens a fresh session (no `--resume`), matching terminal behaviour; issue #49's "Restart = fresh session" semantics stay. The reverse proxy at `/rx/<id>/` (token cookie injection + HTML URL-prefix rewrite for fetch/EventSource/XHR) is unchanged.

**Legacy data (pre-2026-08-12 instances)**: instances created under the old per-instance `REASONIX_HOME` isolation keep an inert `home/` dir and `session.jsonl` that the driver no longer reads (it logs a migration hint on Start). Those sessions are NOT auto-merged into the shared pool — delete the instance to clean the leftover, or export the session manually. No automatic migration is performed.

Disk write amplification fix for long-running PTY-heavy sessions.

**Merged main → feature/opencode-native-ui (reasonix + opencode-web unification)** — the reasonix web-UI feature released in v0.4.0 is now implemented as a third `framework.Kind` (`internal/instance/reasonix/kind.go`) inside the opencode-native-ui kind architecture, replacing the pre-refactor `internal/instance/manager.go` integration. All v0.4.0 reasonix behaviour is preserved: `/rx/<id>/` reverse proxy (token cookie + HTML URL-prefix rewrite), independent loopback listener with `web_url` (cross-origin, issue #44) and same-origin fallback for TLS/network listeners, sidebar-toggle injection, SSE streaming, CLI version gate (1.22.0), per-instance Start lock, state-dir cleanup on stop/restart/delete, shutdown `StopAllKind("reasonix")`, and re-attach of live serve processes on daemon restart (`RestartSurvivor`). Frontend: a dedicated Reasonix modal tab and `kinds/reasonix.js` renderer (per-instance iframe keep-alive across tab switches, issues #53/#54/#55). Tag semantics (`tag.Env` / `preStart` / `Command` / `Cwd`) restored for all kinds — a regression of the framework refactor that had left tags inert at instance start. `state.json` `kind` values are `pty` / `opencode-web` / `reasonix` (empty means `pty`).
- **fix(reasonix): load `kinds/reasonix.js`** — the Reasonix renderer script tag was missing from `index.html`, so `window.KindRenderers['reasonix']` never registered; `selectInstance` fell back to the PTY path and the TTY WebSocket looped on `kind "reasonix" does not support output subscription` with no web UI. The renderer now registers and the iframe panel renders (regression pinned by `TestReasonixTabIsFixedCommandNoTemplate`).
- **fix(remote-access): reasonix/opencode embeds no longer blank behind `?token=` auth** — remote browsers (Tailscale/LAN) authenticate the parent UI via the address-bar token; the iframes navigate with relative URLs that drop it, so `withAuth` 302'd them to `/login` and the panel showed the login page (diagnosed live: daemon had zero connections to the serve while the renderer had navigated). `framework.js` now ships `window.authURL` and both renderers append the token to same-origin iframe URLs; the reverse proxies strip `token` before forwarding upstream (`reasonix_proxy` already did; `opencode_web.ProxyHandler` gained `stripAuthQuery` so the credential never reaches the opencode subprocess). The reasonix proxy also defuses the SPA's render-blocking Google-Fonts stylesheet (`media="print" onload="this.media='all'"`): a pending third-party stylesheet blocks rendering and defers the entire inline SPA, which blanked the page on networks that cannot reach fonts.googleapis.com. Pinned by `TestAuthURLHelperServed`, `TestStripAuthQuery`, `TestDefuseBlockingFontLinks`.
- **fix(ui): web panels no longer stack, reasonix iframe keep-alive extended across instance switches** — switching between opencode-web and reasonix tabs left BOTH panels visible (`deactivate` hid only the iframe), stacking the two embeds into top/bottom halves; `selectInstance` now hides both panels before dispatching and each renderer hides its panel on deactivate. The Reasonix renderer keeps a per-instance iframe cache (switching between two reasonix instances only hides/shows their frames, so a draft survives even A→B→A — beyond main's single shared frame, which reloaded on cross-instance switches), with a 2s self-healing poll (re-navigates on `web_url` port changes after a daemon restart, invalidates on stop/failure — main's per-render-tick check restored), navigation only while the record is `running`/`starting` (the serve is already listening by then — `Driver.Start` waits for the port; the proxy accepts `starting`), hide-not-destroy keep-alive across non-reasonix tabs (issue #53), invalidate-on-stop, the issue #54/#55 xterm cleanups, and `[reasonix-renderer]` console diagnostics for white-screen reports.
- **fix(tags): built-in defaults survive pre-existing tags.json** — `tag.Manager.LoadMerged` now merges the default tags (incl. the command-less `opencode-web` reference tag) as the base layer at load time instead of only when the file is first created, so an existing config without the entry no longer fails Opencode-Web startup with `unknown tag id: opencode-web`; user entries still override defaults (`TestLoadMergedDefaultsSurviveExistingFile`).
- **refactor(ui): Reasonix start tab has no template picker (decision B)** — name-only form, fixed `tag_id: ''`; tag env/preStart remain available via API/CLI `tag_id`.

### Bug fixes

- **PTY multi-tab cross-talk** — opening two PTY tabs interleaves output across tabs. The per-instance subscriber set was previously global inside the kind package; now keyed by `instance id` (`internal/instance/pty/driver.go`, `TestSubscribeOutput_ScopedPerInstance`).
- **PTY ring-buffer budget bypass** — PTY instances no longer allocate their own buffers; the framework now allocates the per-instance ring buffer via `Manager.AllocateBuffer` and passes it through `SpawnParams.Buffer`. Budget enforcement (`BufferCapBytesFor` / `dropBuffer` / 25%-of-RAM cap) now covers PTY.
- **opencode-web long-run pipe deadlock** — `pumpAndWatch` now drains the child's stdout to EOF after parsing the listening line, so the upstream process never blocks on a full 64 KiB OS pipe (`internal/instance/opencode_web/driver.go`, `TestPumpAndWatch_DrainsAfterListening`).
- **opencode-web publisher data race** — `Handle.publisher` is now `atomic.Pointer[framework.Publisher]`; readers in `pumpAndWatch` / `healthLoop` / `wait` go through `loadPublisher()`. `-race` clean.
- **framework ring-buffer map leak** — `Manager.runLifecycle`'s defer now calls `dropBuffer` instead of only decrementing `totalBufBytes`, so `m.buffers[id]` no longer holds a stale `*RingBuffer` for instances that exited via Stop / kind-reported terminal status / ready-timeout. Aligns with the "Stop / wait / Restart / Delete all funnel into dropBuffer" contract in `docs/ARCHITECTURE.md` §"Buffer lifecycle". `TestRunLifecycle_DropsBufferOnTerminalStatus` / `TestRunLifecycle_DropsBufferOnReadyTimeout` / `TestStop_DropsBuffer`.

- **feat(instance): opencode-web kind embedding opencode official web UI** — implemented: `internal/instance/opencode.go`, `internal/ui/proxy.go`, `/__opencode/<id>/*` reverse proxy, iframe panel + OC badge in `internal/ui/static/index.html`. Design, threat model, and review checklist in `docs/ARCHITECTURE.md` §8.
- **feat(opencode-web): single-worktree isolation** — full-page embed (`/__opencode/<id>/`, replacing the deep session link that blanked the SPA), reverse-proxy directory monitoring that records out-of-scope requests in an in-memory `ScopeTracker` (`/api/instances/opencode/scope?id=`), an injected script that hides cross-worktree switch entries (project switch / add-project / open-project) and normalizes the localStorage server list to a single server, an `opencode --version` gate (advisory, 1.18.x) plus DOM-anchor / visibility checks reported via `postMessage`, and a persistent in-panel warning bar (out-of-scope + hiding-not-effective states). Design and decision record in `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md`.
- **fix(opencode-web): hide project list via CSS to avoid SSE-time render jank** — replace the `MutationObserver` + `querySelectorAll` walk that hid `[data-slot="home-projects-scroll"]` (home project list) and `[data-action="project-switch"]` (project switch button) with a one-shot `<style>` injection (`aside:has([data-slot="home-projects-scroll"]){display:none!important}` etc.), so the SSE-driven session stream no longer re-traverses the whole DOM on every mutation. Same JS continues to hide per-element foreign-worktree projects and the "Open project" button. Post-mortem and seven follow-up debugging notes (URL/Worker rewrite, projects preseed, sandbox normalization, orphan process) added to `docs/plans/opencode-native-ui/DEBUG.md`.
- docs: add `REVIEW-e4158fa.md` code review for `feature/opencode-native-ui` HEAD (`e4158fa`).

**Reasonix sidebar toggle resized to a vertical pill that fits the chat gutter**: the injected hide/expand sidebar button in the embedded Reasonix web chat was 34×34px at (8,8), so it overlapped the conversation. It is now a vertical 24×64px pill at (2,8) with a CSS arrow glyph (▶ when collapsed — click to expand, ◀ when expanded — click to collapse; direction points at where the sidebar moves, so no text or i18n needed): measured against the upstream `.transcript` padding (`24px 28px` on desktop), the button's right edge (26px) stays inside the chat's 28px left gutter, so it never covers message text in either the collapsed or expanded state, and the taller target is easier to see and click.

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
- **Text file preview** — click a file in the Changes panel to preview it with line numbers, a formatted view, and a diff view (including a synthetic diff for untracked files, with `quotePath` handled).
- **Branch divergence badge** — the sidebar now shows a diverge badge when a branch is ahead of / behind its upstream, backed by new API handlers and scheduled refresh.
- **`mw config regen`** — new CLI command to regenerate the config, plus per-request auth token reload so config/token changes take effect without a daemon restart.
- **Tags config directory** — open the tags config directory from the UI and use default tags out of the box.

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
