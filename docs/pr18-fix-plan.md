# PR18 terminal fix plan

## Problem
PR #18 fixed terminal corruption and response-garbage issues, but the merged frontend implementation still has two material defects in `internal/ui/static/index.html`:

1. The WebSocket binary path decodes each frame independently with `TextDecoder().decode(u8)`, which can corrupt multibyte UTF-8 characters when a character is split across frames.
2. The SSE fallback path writes `msg.chunk` directly to the terminal and bypasses the new response-filtering logic, so the same garbage strings can still leak through when WebSocket is unavailable or times out.

## Current state analysis
- Output handling is concentrated in `internal/ui/static/index.html`.
- Query-response filtering is currently duplicated across `loadLog()`, `loadLogSince()`, WebSocket text handling, and WebSocket binary handling.
- `term.onData()` already has a separate input-side guard via `isTerminalQueryResponse()`.
- WebSocket handshake/fallback logic already explicitly falls back to `startSSE()` after timeout, so SSE is a live path that must stay behaviorally aligned with WebSocket.
- Baseline backend tests currently pass with `go test ./...`.

## Proposed approach
1. Extract one shared helper for terminal output sanitization so all output paths use the same filtering rules.
2. Split "sanitize text" from "decode bytes" so the WebSocket binary path preserves byte-stream semantics instead of per-frame UTF-8 decoding.
3. Reuse the shared sanitization helper in the SSE path and existing log replay paths to keep live, replay, and fallback behavior consistent.
4. Validate with existing Go tests plus targeted manual browser verification around UTF-8 output, instance switching, and WebSocket→SSE fallback.

## Todos
1. Audit and refactor terminal output handling in `internal/ui/static/index.html`.
2. Introduce a persistent decoder or equivalent byte-safe handling for WebSocket binary frames.
3. Route SSE chunks through the same sanitization helper used by log replay / WebSocket text output.
4. Run repository validation (`go test ./...`) and confirm formatting impact is nil for the touched files.
5. Manually verify Chinese/multibyte output, TUI redraw, instance switching, and handshake-timeout fallback behavior.

## Notes
- The fix should stay frontend-only unless investigation reveals backend framing assumptions need adjustment.
- Prefer a minimal, behavior-safe refactor: centralize the regex filter once, then wire each path through it.
- Be careful not to change the existing handshake/state-machine behavior while fixing output handling.
