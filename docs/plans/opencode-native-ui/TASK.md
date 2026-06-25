# opencode-native-ui — Implementation Tasks

> **Status**: 待实施 · [FEASIBILITY.md](./FEASIBILITY.md) ([中文](../../../README.zh-CN.md)) · [PLAN.md](./PLAN.md)
>
> 5 PRs in dependency order, **docs-first** (PR 1 solidifies spec before code).

---

## PR 1: 文档先行（规格落定）

- [ ] `docs/plans/opencode-native-ui/TASK.md` — this file
- [ ] `docs/PRD.md` §7 — add opencode-web entry after PTY+Web TTY paragraph
- [ ] `docs/API.md` — add `GET /api/instances/<id>/opencode` + `/__opencode/<id>/*` proxy path
- [ ] `docs/ARCHITECTURE.md` — new §8 opencode-web integration with ASCII diagram
- [ ] `docs/CHANGELOG.md` — Unreleased entry: `feat(instance): add opencode-web kind (spec)`

**Verify**: PR review only (no build/test needed). Spec consistency check against FEASIBILITY.md + PLAN.md.

---

## PR 2: 基础设施（schema + helpers + default tag）

files:

- [ ] `internal/store/state.go` — add `Kind` + `Extra` to `ManagedInstance` (l.30-45)
- [ ] `internal/store/state_test.go` — backward compat + round-trip tests
- [x] `internal/instance/opencode.go` (new) — `Command()`, `BuildEnv()`, `ExtractListeningAddress()`, `IsAPIPath()`（`GeneratePassword` 已移除；凭证统一为 `cfg.AuthToken`，详见 ARCH §8）
- [ ] `internal/instance/opencode_test.go` (new) — unit tests for all helpers
- [ ] `internal/tag/tag.go` — add `opencode-web` default tag (l.28-32)

**Verify**: `gofmt -l .` clean; `go test ./internal/store/... ./internal/instance/... ./internal/tag/...` passes; PTY instance regression-free.

---

## PR 3: Manager opencode-web branch

files:

- [ ] `internal/instance/manager.go`
  - `StartInput` + `Kind string` (l.74-80)
  - `Start()` normalize Kind, dispatch cmd/env/pty per Kind (l.193)
  - env: `opencode.BuildEnv()` for opencode-web (l.318-321)
  - pty: `cmd.Start()` for opencode-web, keep `pty.Start()` for pty (l.332)
  - `pumpLogs()` shared (l.891-919), stdout pipe for opencode-web
  - `go opencodeWatch()` goroutine: parse listening addr → write Extra + set running
  - `go opencodeHealth()` goroutine: poll `/global/health` every 5s, 3 fails → failed
  - `wait()` shared (l.845-877), cleanup Extra on exit
- [ ] `internal/instance/manager_test.go` — Kind routing, env override, command locked
- [ ] `internal/instance/manager_integration_test.go` — `_TestStartOpencodeWeb` with mock binary

**Verify**: `go test ./internal/instance/... -v -run TestStart`; Manual: start opencode-web, `lsof -nP -iTCP` shows 127.0.0.1 port occupied.

---

## PR 4: API + 反向代理

files:

- [ ] `internal/app/app.go`
  - POST `/api/instances` request struct + `Kind` field (l.1284-1316)
  - GET `/api/instances` response includes `kind` + `extra`
  - `GET /api/instances/<id>/opencode` new handler
- [ ] `internal/ui/proxy.go` (new) — `OpencodeProxy()`: StripPrefix, Director with auth injection, `?directory=` for GET/HEAD APIs, SSE flush, 502 error handler
- [ ] `internal/ui/proxy_test.go` (new) — mock upstream, test auth/directory injection/flush/error paths
- [ ] `internal/ui/ui.go` — mount `OpencodeProxy(manager)` at `/__opencode/` (after static routes, l.57-58)

**Verify**: `go test ./internal/ui/... ./internal/app/... -v`; Manual: `curl /__opencode/<id>/global/health` returns opencode health response with injected auth.

---

## PR 5: 前端 iframe 面板 + tab badge

files:

- [ ] `internal/ui/static/index.html`
  - `#opencode-panel` div + `#opencode-iframe` with sandbox (next to `#terminal-container` l.1199)
  - CSS (near l.706): `#opencode-panel`/`#opencode-iframe` sizing; `.badge-oc` style
  - `renderTabs()` (l.2111-2194): OC badge when `inst.kind === "opencode-web"`
  - `selectInstance()` (l.2427-2461): fetch `/api/instances/<id>/opencode`, set iframe.src, toggle panel visibility per Kind

**Verify**: `go build ./...`; Full e2e manual checklist (see PLAN.md §Verification).

---

## Global acceptance

after all 5 PRs merged:

- [ ] ≥2 opencode-web instances in same worktree run independently
- [ ] Tab switching swaps iframe src correctly
- [ ] PTY instances zero regression
- [ ] Old `state.json` (no Kind/Extra) loads clean
- [ ] User-modified tag `Command` ignored; `OPENCODE_SERVER_PASSWORD` forced override
- [ ] `go.mod` zero new deps
- [ ] `gofmt -l .` clean; `go test ./...` passes
- [ ] PRD/API/ARCH/CHANGELOG synced
- [ ] PLAN.md 13-step manual checklist all pass

---

## Reuse reference

| component | path | usage |
| --- | --- | --- |
| ManagedInstance | `internal/store/state.go:30-45` | +Kind, +Extra |
| State.TabOrder | `internal/store/state.go:17` | auto-multi-instance |
| StartInput | `internal/instance/manager.go:74-80` | +Kind |
| Manager.Start | `internal/instance/manager.go:193` | Kind dispatch |
| pty.Start branch | `internal/instance/manager.go:332` | opencode-web uses cmd.Start() |
| pumpLogs | `internal/instance/manager.go:891-919` | shared PTY/pipe src |
| wait | `internal/instance/manager.go:845-877` | shared, cleanup Extra |
| ReconcileRunningOnStartup | `internal/instance/manager.go:84-110` | auto-mark stopped |
| RingBuffer.WriteString | `internal/instance/logbuf.go` | stdout to buffer |
| Tag default list | `internal/tag/tag.go:28-32` | +opencode-web |
| withAuth | `internal/app/app.go:393` | covers new routes |
| httputil.ReverseProxy | stdlib | proxy impl |
| renderTabs/selectInstance | `internal/ui/static/index.html:2111,2427` | extend render+branch |
| status badge pattern | `internal/ui/static/index.html:651-665` | reuse for OC badge |
