# Plan: Replace Per-Instance Log File with In-Memory Ring Buffer

## Context

The `mw` daemon (PID 93493 at last observation) exhibits massive sustained disk writes during long-running PTY-heavy sessions. Root cause: `instance.Manager.pumpLogs` calls `enforceMaxLogSize` after every 1024-byte PTY chunk. Once a per-instance log file reaches `maxLogBytes` (10MB), each subsequent chunk pushes the file over the limit and triggers `enforceMaxLogSize` to do `open + read 10MB + os.WriteFile(path, 10MB)` — a full 10MB read + truncate + write. Net effect: every ~100 bytes of PTY output produces ~20MB of disk I/O (amplification ~100,000×). Symptom: 280MB+ accumulated log directory; file sizes oscillate between 10485760 and 0 (the `0` comes from `os.WriteFile`'s `O_TRUNC` window between truncate and write).

User has confirmed: **logs should live entirely in memory, no disk persistence**. This aligns with the existing `ReconcileRunningOnStartup` semantics (manager.go:56-84) which already declares logs cannot be read after restart — running instances are marked stopped, in-memory PTY channels are not resumed. Removing the on-disk log file preserves existing behavior while eliminating the write-amplification bug entirely.

Outcome: zero disk I/O from PTY logging; bounded memory per instance; HTTP/WS/SSE/MCP log endpoints continue to work unchanged for the running instance's lifetime.

## Design Decisions (locked with user)

- **In-memory only.** `LogPath` field on `ManagedInstance` is removed entirely (not kept as empty string). All 5 current references (`manager.go:184/231/570/661/942`) are deleted along with the `logPathByID` helper and the `Restart`/`Delete` log-file removal paths.
- **Bounded but adaptive sizing** per `feedback-memory-buffer-sizing`:
  - Default cap per instance: **32 MB**
  - Hard ceiling per instance: **256 MB** (never exceeded)
  - Min floor per instance: **16 MB**
  - Total budget across all live instances: **≤ 25% of system RAM**
  - Adaptive default: `clamp(available_mem / 16, 16MB, 256MB)` computed via `gopsutil/mem.VirtualMemory()` at instance start (cache result for 60s)
  - User override: new `config.GlobalConfig.LogBufferBytes int64` (default `0` = use adaptive). When set, clamped to [16MB, 256MB].
  - When new instance would exceed total budget, `Start` returns a new exported error `ErrLogBufferBudgetExceeded`.

## Architecture

### New: `internal/instance/logbuf.go`

Chunked ring buffer. Internals:
- `cap int64` — hard ceiling
- `chunks [][]byte` — fixed-size 64KB chunks, head/tail indices
- `head int64` — monotonic byte offset for `ReadSince` semantics
- `mu sync.Mutex` — guards state; pumpLogs writes, Tail/ReadSince read

Public surface:
- `NewRingBuffer(capBytes int64) *RingBuffer`
- `Write(p []byte) (n int)` — drops oldest when full, never blocks
- `Tail(n int64) (string, int64)` — last `n` bytes; returns end offset
- `ReadSince(since, maxBytes int64) (string, int64, error)` — bytes from offset, returns next offset; returns empty+`since` when no new data (preserves SSE poll loop semantics)
- `Offset() int64` — current head offset
- `BytesUsed() int64`
- `Close()` — idempotent

### New: `internal/instance/sizing.go`

- Constants: `DefaultBufferCap=32MB`, `MinBufferCap=16MB`, `MaxBufferCap=256MB`, `MaxTotalFraction=0.25`
- `MemSampler` interface (one method `VirtualMemory() (*mem.VirtualMemoryStat, error)`) — inject for tests; default impl wraps `gopsutil/v4/mem`
- `resolveCap(cfgBytes int64, sampler MemSampler, totalBufBytes int64) (int64, error)` — returns the cap to use or `ErrLogBufferBudgetExceeded`
- New exported sentinel: `ErrLogBufferBudgetExceeded` (in `manager.go`) wrapping detailed context via `fmt.Errorf("...: %w", ErrLogBufferBudgetExceeded)`. The wrapped error must carry a structured payload so HTTP handlers can render it without re-querying the manager.
  - Implementation note: define a struct error type `LogBufferBudgetError struct { UsedBytes int64; LimitBytes int64; SystemBytes int64 }` implementing `error`. Handler does `errors.As(err, &budgetErr)` to extract bytes for the response body.

### Modify: `internal/app/app.go` — error contract

Currently `writeErr(w, http.StatusBadRequest, err)` returns `{"error": "msg"}` JSON. Add a sibling helper `writeLogBufferBudgetErr(w, err)` for the budget-exceeded case so the UI can detect it without parsing free-form text.

Update both `instance.Start` call sites (HTTP `handleInstance`/`handleInstanceMCP` at app.go:1140 and MCP `handleMCPTool` at app.go:1686):

```go
if err != nil {
    var budgetErr *instance.LogBufferBudgetError
    if errors.As(err, &budgetErr) {
        writeLogBufferBudgetErr(w, budgetErr)  // HTTP 503 + structured body
        return
    }
    writeErr(w, http.StatusBadRequest, err)
    return
}
```

`writeLogBufferBudgetErr` writes:
- HTTP status: `503 Service Unavailable`
- Headers: `Content-Type: application/json`, `Retry-After: 0` (won't auto-resolve; user must close instances)
- Body: `{"error":"log_buffer_budget_exceeded","message":"...","used_bytes":N,"limit_bytes":M,"system_bytes":K,"hint":"Close other instances, raise LogBufferBytes in config, or reduce concurrent tabs."}`

### Modify: `internal/ui/static/index.html` — popup

When `POST /api/instances` returns 503 with body containing `"error":"log_buffer_budget_exceeded"`, the existing fetch handler must:
1. NOT mark the new tab as created (already handled since fetch threw)
2. Show a modal/toast with the message, with a "Close" button. Use the existing modal/dialog pattern in `index.html` (or a simple inline alert if none). Include the hint string and used/limit numbers in the dialog body so the user understands the constraint.
3. Optionally surface the limit/usage in the tab bar header so users can see at a glance how close they are to the budget. (Stretch goal — only if a small change.)

> **Note**: the original plan called for the popup to live in `internal/portal/dashboard.html`, but that file is a read-only cross-repo list view that never POSTs to `/api/instances`. The popup therefore went into `internal/ui/static/index.html`, which is the actual instance-creation UI. This section reflects that.

The MCP tool path (app.go:1686) should also return this error with the same shape so CLI/agent consumers can handle it programmatically.

### Modify: `internal/instance/manager.go`

Add to `Manager` struct:
- `buffers map[string]*RingBuffer` (guarded by `stateMu`)
- `totalBufBytes int64` (atomic, no separate mutex)
- `memSampler MemSampler` (nullable; defaults to gopsutil)
- `cfgLogBufferBytes int64` (from `config.GlobalConfig.LogBufferBytes`)
- `lastMemSampleAt time.Time` + `lastMemTotal int64` for 60s cache

Modify:
- `Start` — drop `logPath`/`logFile` creation; compute `cap` via `resolveCap`; create `RingBuffer`; store in `m.buffers[id]`; pass to `pumpLogs`; bump `totalBufBytes`; clean up on error
- `pumpLogs(id, ptmx)` — drop the `out *os.File` and `logPath string` parameters; new shape: read PTY → `redact.Text` → `buf.Write` + `broadcastOutput`
- `Tail(id, n)` — read from `m.buffers[id].Tail(n)`; if buffer missing (instance stopped/never had one) return `("", nil)`
- `ReadSince(id, since, maxBytes)` — read from buffer; same nil-buffer behavior
- `closeSubscribersLocked(id)` — also remove from `m.buffers`, decrement `totalBufBytes`
- `Stop`/`wait` — already call `closeSubscribersLocked`, so buffer cleanup is automatic
- `Restart`/`Delete` — drop the `os.Remove(oldLogPath)` lines and the `oldLogPath` lookups entirely

Delete:
- `enforceMaxLogSize` function (manager.go:867-892)
- `logPathByID` helper (manager.go:939-946)
- `TestEnforceMaxLogSize` (manager_test.go:45-62)
- All references to `LogPath` in `Start`/`Restart`/`Delete`

Add to `Manager`:
- `PurgeOrphanLogFiles() (int, error)` — scans `m.DataDir/logs/`, removes every `.log` file (no in-memory or on-disk state references them after this refactor; all are dead artifacts). Missing directory is not an error. Returns count of files removed. Uses `m.Logger` to report each remove + summary.

### Modify: `internal/store/state.go`

Remove `LogPath string \`json:"log_path"\`` from `ManagedInstance` struct (line 43). Old `state.json` files with this field will have it silently ignored by JSON decoding — no migration needed.

### Modify: `internal/config/global.go`

Add field: `LogBufferBytes int64 \`json:"log_buffer_bytes,omitempty"\`` (default 0 = adaptive).

### Modify: Manager construction sites (only 2)

- `internal/app/app.go:131` — pass buffer config into `instance.Manager`
- `internal/cli/cli.go:269` — same

Pass via new optional field on `Manager` (e.g., `LogBufferBytes int64` set after literal-init) or a small `NewManager` constructor. Recommend: keep `&Manager{...}` literal but add a single field; both sites already use literals, no churn beyond one extra line each.

## Files to Modify (order)

1. **`internal/instance/logbuf.go`** (new) — `RingBuffer` implementation
2. **`internal/instance/logbuf_test.go`** (new) — unit tests
3. **`internal/instance/sizing.go`** (new) — `resolveCap`, `MemSampler`, constants, `ErrLogBufferBudgetExceeded`
4. **`internal/instance/sizing_test.go`** (new) — `resolveCap` tests with `fakeMemSampler`
5. **`internal/instance/manager.go`** — remove `enforceMaxLogSize`, `logPathByID`; refactor `pumpLogs`/`Start`/`Tail`/`ReadSince`/`closeSubscribersLocked`/`Stop`/`wait`/`Restart`/`Delete`; add `buffers`, `totalBufBytes`, `memSampler`, `cfgLogBufferBytes` fields; add `ResolveCap` invocation
6. **`internal/instance/manager_test.go`** — delete `TestEnforceMaxLogSize`; update `TestLogPathByID` (drop or refactor — see below); add `TestPumpLogsWritesToBuffer` and `TestCloseSubscribersDropsBuffer`
7. **`internal/store/state.go`** — remove `LogPath` field
8. **`internal/config/global.go`** — add `LogBufferBytes` field
9. **`internal/app/app.go:131`** — set `LogBufferBytes` on Manager literal (read from `config.Load()`)
10. **`internal/cli/cli.go:269`** — same
11. **`internal/app/app.go` (startup)** — after `instanceMgr.ReconcileRunningOnStartup()`, call `n, err := instanceMgr.PurgeOrphanLogFiles()` and log the count. Idempotent. Runs once per daemon boot. This reclaims the 280MB+ of dead log files left behind by the bug, and keeps the `logs/` directory empty for future runs.
12. **`internal/app/app.go`** (HTTP/MCP handlers at lines 1140 and 1686) — detect `*instance.LogBufferBudgetError` via `errors.As`, route to new `writeLogBufferBudgetErr` helper returning HTTP 503 + structured JSON body.
13. **`internal/ui/static/index.html`** — fetch error handler for `POST /api/instances` detects 503 + `error:"log_buffer_budget_exceeded"` and shows a modal/popup with the message and limit numbers. New instance is rejected (no tab created) since the request itself failed.

Test updates:
- `TestLogPathByID` (manager_test.go:31) — drop the test (helper being deleted). If you want a regression guard, replace with `TestRingBuffer_LookupByID` covering the new `m.buffers[id]` map access pattern.

## Verification

1. **Build**: `go build ./...`
2. **Unit tests**: `go test -race ./internal/instance/... ./internal/store/... ./internal/config/...`
3. **Integration**: `go test ./internal/instance/... -run TestStartStop` should pass (already exists, just confirm we didn't break Start/Stop lifecycle)
4. **Smoke test (manual)**:
   - Start `mw` on this machine: process should boot normally; `ls ~/Library/Application\ Support/myworktree/*/logs/` should NOT get any new `.log` files for new instances (confirm no per-instance log files are created)
   - Open a worktree tab, run an OpenCode TUI session that produces heavy PTY output
   - Watch `iostat -d 1` or Activity Monitor disk writes — write rate on the data dir should be **zero** after startup (only `state.json` writes from save paths)
   - Watch RSS of the `mw` process — should grow up to ~32MB per active instance, stabilize, not grow unboundedly
   - Open WS endpoint (`/api/instances/log?since=N`): SSE/WS should still stream and tail-back correctly
   - Call `instance_log_tail` MCP tool: should return last N bytes from memory
5. **Sizing test (manual)**: start 8+ concurrent instances on this machine; verify total RSS stays within 25% of system RAM (a 16GB machine → cap at ~4GB total)
6. **Backward state compat**: drop the existing `state.json` from a previous session (or copy) and restart — JSON decoder should ignore the missing `log_path` field cleanly
7. **Startup cleanup (manual)**: with the current 280MB of `.log` files still on disk, start the new `mw` binary; verify daemon log line `purge: removed N orphan log files` and that `du -sh ~/Library/Application\ Support/myworktree/*/logs/` reports near-zero (only files written during the current session's test, which there should be none of).
8. **Budget exceeded UI popup (manual)**: set `LogBufferBytes` in `~/Library/Application Support/myworktree/auth.json` (or wherever config lives — verify path) to a tiny value like `1048576` (1MB). Open 3+ worktree tabs so total demand exceeds 1MB × N. Attempt to open a 4th tab via UI; confirm a modal/popup appears with text like "Insufficient memory to start new instance... used: X bytes, limit: Y bytes..." and that no new tab is created. The popup should have a Close button.

## Risks & Open Items

- **OpenCode TUI redraw storms** can chew through 32MB fast. 256MB ceiling is the safety net; if a single TUI instance regularly needs more than 32MB, the user can set `LogBufferBytes` in config. Document in CHANGELOG.
- **SSE 1-second polling** relies on `ReadSince(id, cursor, ...)` returning `("", cursor, nil)` when no new data — verified by reading `app.go:1521-1541`. The ring buffer must preserve this: when `since >= head`, return empty and `since` unchanged. Implementation note in `logbuf.go`.
- **Old log files on disk** are dead artifacts left behind. Cleanup is handled by `Manager.PurgeOrphanLogFiles()` called once at daemon startup (see Files to Modify #11). No need for a follow-up.
- **`PurgeOrphanLogFiles` test**: `TestPurgeOrphanLogFiles` in `manager_test.go` — pre-create a `logs/` dir with several `.log` files and one non-`.log` file; call method; assert only `.log` files removed and count correct; assert idempotent on second call (count=0).
- **Budget error path test**: simulate memory pressure by setting `cfgLogBufferBytes` very low + multiple running instances; assert `Start` returns `*LogBufferBudgetError` with correct `UsedBytes` / `LimitBytes`; assert HTTP handler returns 503 + correct JSON body.
- **UI popup test**: `internal/portal/portal_test.go` already covers portal embedding; add a smoke test for `index.html` (the instance-creation UI) if the existing test infra supports it. Otherwise manual verification: open dev tools, hit `/api/instances` with low budget, confirm popup appears with correct numbers.
- **Memory accounting** uses `atomic.Int64` for `totalBufBytes` so it doesn't contend with `stateMu`. Lock ordering: `stateMu` is acquired in `Start` after computing the cap but before mutating `m.buffers`; `totalBufBytes` updates are lock-free.
- **Tests for `pumpLogs` integration with PTY**: existing `TestStartStopIntegration` (manager_integration_test.go:14) only checks Start/Stop. Optional add: a `TestPumpLogsBroadcastAndBuffer` that spawns `echo hello && sleep 0.1` and asserts both the WS subscriber channel and a direct `Tail` read see `"hello"`.
- **`gopsutil/v4/mem.VirtualMemory()` in containerized envs**: may report cgroup-limited memory rather than host. Acceptable — caps are bounded, `MaxTotalFraction=0.25` is conservative.
- **CHANGELOG note** required: removing `log_path` field from `state.json` is a breaking schema change for any external tooling reading state files.
