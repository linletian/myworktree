# Changelog

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
