# Changelog

## v0.1.3

UI polish and usability improvements.

Highlights:
- Moved "Show archived" checkbox out of the scrollable tab area into the static header row, so action buttons and the toggle remain visible without horizontal scrolling.
- Added "Purge" button to bulk-delete all archived instances in one confirmation dialog, backed by a new atomic `POST /api/instances/purge` endpoint with optimistic locking.

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
