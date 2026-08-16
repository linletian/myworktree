# dsh-native-ui — Implementation Tasks

> **Status**: PR1–PR4 已完成 · PR5 远程实测待完成 · [FEASIBILITY.md](./FEASIBILITY.md)（决策文档）· [PLAN.md](./PLAN.md)（实施计划）
>
> 5 PRs in dependency order, **docs-first** (PR 1 solidifies spec before code).

---

## PR 1: 文档先行（规格落定）

- [x] `docs/plans/dsh-native-ui/PLAN.md` — 实施计划（本文档的上游）
- [x] `docs/plans/dsh-native-ui/TASK.md` — this file
- [x] `docs/PRD.md` §7 — add dsh-web entry after opencode-web paragraph
- [x] `docs/API.md` §5 — add `GET /api/instances/dsh`、`GET /api/instances/dsh/scope`、`POST /api/instances/dsh/launch`、`POST /api/instances/dsh/install`
- [x] `docs/ARCHITECTURE.md` — new §9 dsh-web integration with ASCII diagram + threat model + review checklist
- [x] `docs/CHANGELOG.md` — Unreleased entry: `feat(instance): add dsh-web kind (spec)`
- [x] `README.md` / `README.zh-CN.md` — Features mention dsh-web

**Verify**: PR review only (no build/test needed). Spec consistency check against FEASIBILITY.md + PLAN.md.

---

## PR 2: dsh_web 包核心（driver + overlay + 版本门 + launch 模式）

files:

- [x] `internal/instance/dsh_web/driver.go` (new) — `Driver`（framework.Kind，`dsh-web`，`Interactive:false`）：Spawn（launch 解析 → 预检 LookPath → 版本门 → 写 restrict.yml → **L2 校验 `dsh web --dump-config --patch` → blob `overlay_verified`** → exec（npx 模式 `Setpgid`）→ 抓就绪行 `dsh web: http://127.0.0.1:(\d+)` → blob → 起代理监听 → ready）、tag preStart（redact）、Stop（SIGTERM→grace→SIGKILL；npx 杀进程组 `kill(-pid)`；关代理监听；等 exited）、Status、healthLoop（`GET /` 5s/3 连败）、probeVersion、KindBlob、`DshBin` 测试覆写
- [x] `internal/instance/dsh_web/overlay.go` (new) — restrict.yml：`storage-json.config.root` → `<dataDir>/dsh/<HashPath(worktree)>/storages`；`directory-picker.disabled`；`client-hmr.disabled`（fmt 手写 yml，零依赖）
- [x] `internal/instance/dsh_web/version.go` (new) — `parseVersion`（x.y.z 核心 + rc 容忍）、`versionLess`、硬门 `checkVersion`（min core `0.1.0`；advisory `[0.1.0, 0.2.0)`；npx pin `0.1.0-rc.6`）
- [x] `internal/instance/dsh_web/launch.go` (new) — launch.json 读写（`mode: path|npx|install` + `resolvedBin`）、`npm prefix -g` 解析、npx 命令组装、resolveLaunch
- [x] `internal/instance/dsh_web/driver_test.go` (new) — 就绪行/版本门/launch 解析/Setpgid 断言
- [x] `internal/instance/dsh_web/overlay_test.go` (new)
- [x] `internal/instance/dsh_web/version_test.go` (new)
- [x] `internal/instance/dsh_web/integration_test.go` (new) + `testdata/dsh-mock.go` — Manager 全生命周期、双端口释放、npx 进程组清理

**Verify**: `gofmt -l .` clean; `go test ./internal/instance/dsh_web/... ./internal/instance/...` passes; PTY/opencode-web/reasonix zero regression.

---

## PR 3: loopback 代理 + scope 监测 + 自举 + app 接线

files:

- [x] `internal/instance/dsh_web/proxy.go` (new) — `LoopbackProxy`：Director（Host=上游、删 Origin、`authq.StripToken`、Accept-Encoding 透传）、WS Upgrade 透传、`FlushInterval=-1`、502；非 loopback 绑定 + token 门（硬编码不可关）+ TLS 镜像；POST body RPC 信封 scope 记录；`ScopeTracker`（平移 opencode `scope.go`）
- [x] `internal/instance/dsh_web/bootstrap.go` (new) — 自举 RPC 客户端（`workspace.create {path}` + 可选 `session.create {cwd}`；3×2s 重试；失败仅告警；记录 workspaceId 到 blob）
- [x] `internal/instance/dsh_web/proxy_test.go` (new) — Director 矩阵 / WS 升级 / token 门（非 loopback 无 token → 401）/ scope 分类矩阵
- [x] `internal/instance/dsh_web/bootstrap_test.go` (new) — 信封格式 + 幂等 + 失败容忍
- [x] `internal/store/state.go` — add `KindDsh = "dsh-web"` constant
- [x] `internal/app/app.go` — `reg.Register(dsh_web.Driver{DataDir, Logger})`; routes `GET /api/instances/dsh`、`GET /api/instances/dsh/scope`、`POST /api/instances/dsh/launch`、`POST /api/instances/dsh/install`; Shutdown `StopAllKind(store.KindDsh)`
- [x] `internal/tag/tag.go` — add `dsh-web` default tag (no Command, like opencode-web)
- [x] `internal/app/app_test.go` — route existence + kind guard tests

**Verify**: `go test ./...`; smoke: mock dsh, direct upstream `/api` trust fence (no Origin passes), via proxy (Host rewrite) passes.

---

## PR 4: 前端 renderer + 缺失依赖对话框 + 警告条

files:

- [x] `internal/ui/static/kinds/dsh_web.js` (new) — `DshWebRenderer`：iframe 面板、就绪轮询（复用 `#opencode-loading` 模式）、per-instance iframe keep-alive、scope 轮询三态警告条（正常/越界/版本或裁剪失效）、版本 advisory 警告、远程 `?token=` 附加（token 读取复用 `framework.js` `window.api` 模式：`mw_token` cookie → `?token=` query 回退）
- [x] `internal/ui/static/index.html` — `#dsh-panel`/`#dsh-iframe`/警告条 DOM + CSS；tab badge；script 标签；缺失依赖 `<dialog>`（三选：npx 启动 / 立即安装（进度+失败提示）/ 取消；触发：kind=dsh-web 且 failed 且 info 端点 `missing_dsh`）；`selectInstance` 面板互斥扩展

**Verify**: `go build ./...`; local manual checklist (PLAN.md §Verification) all pass.

---

## PR 5: 远程访问验证 + 收尾

- [ ] Remote e2e (LAN + TLS): non-loopback bind + token gate + WS passthrough + cross-origin iframe auth — **半自动实测已通过**（`docs/plans/dsh-native-ui/pr5-remote-e2e.sh`，2026-08-15：真实 myworktree daemon `0.0.0.0` + 自签 TLS + token 门 + 真实 dsh 0.1.0-rc.6 + 隔离 DSH_HOME/仓库，LAN IP 直连模拟远端客户端：无 token → 401 / 首次导航 `?token=` → 302 Location=/ + Set-Cookie / 跟随 → 200 / 仅 cookie → 200 / loopback 直通 → 200 / WS `Upgrade` → **101 Switching Protocols** / 会话 watch 归属（自举 preseed 排除、伪造外来会话上报警告、删除后清除）/ Shutdown 无孤儿进程）。剩余人工步骤：真实浏览器点击（SPA 渲染 + 交互式 WS 帧）与 portal 面板链接
- [x] Docs final sync (CHANGELOG full entry, PLAN/TASK status markers)

**Verify**: remote browser e2e passes; `go test ./...`, `gofmt -l .` clean.

---

## Global acceptance

after all 5 PRs merged:

- [ ] ≥2 dsh-web instances in same worktree run independently
- [ ] Tab switching swaps iframe src correctly
- [ ] "启动就是对应工作区" holds (bootstrap workspace.create; cross-worktree sessions visible-but-not-clickable)
- [ ] Terminal-run dsh (same cwd) sessions interoperate with web instances
- [ ] Out-of-scope = warn only; warning bar three states correct
- [ ] Missing-dsh dialog: all 3 options work; npx mode leaves no orphan processes
- [ ] Version hard gate fail-fast; advisory out-of-range → persistent UI warning
- [ ] Remote mode without token → 401 (token gate cannot be disabled)
- [ ] PTY / opencode-web / reasonix zero regression
- [ ] Old `state.json` loads clean (new kind is purely additive)
- [ ] `go.mod` zero new deps; `gofmt -l .` clean; `go test ./...` passes
- [ ] PRD §7 / API §5 / ARCH §9 / CHANGELOG synced

---

## Reuse reference

| component | path | usage |
| --- | --- | --- |
| Kind interface | `internal/framework/kind.go` | Driver implements Spawn/Stop/Status/ReadLogs/KindBlob; `Publisher`/`ReadySignal` for state push |
| opencode-web skeleton | `internal/instance/opencode_web/driver.go` | spawn/ready-line/health/stop pattern (copy + adapt) |
| Scope machinery | `internal/instance/opencode_web/scope.go` | `normalizeDir`/`classify`/`ScopeTracker` (port) |
| Version gate | `internal/instance/reasonix/driver.go` `checkVersion` | hard-gate precedent (issue #45) |
| Token stripping | `internal/authq` | `StripToken` for every proxied query (review checklist §8.9) |
| Per-kind state dir | `internal/instance/reasonix/driver.go` (DataDir) | `<DataDir>/dsh/<id>/` for restrict.yml + launch.json |
| Kind registry | `internal/app/app.go:152-161` | register dsh_web.Driver with DataDir/Logger |
| StopAllKind | `internal/app/app.go:536-539` | add `store.KindDsh` to Shutdown |
| Default tags | `internal/tag/tag.go` | +`{ID: "dsh-web"}` |
| Renderer registry | `internal/ui/static/kinds/renderer.js` | register `dsh-web` renderer |
| Loading overlay | `internal/ui/static/kinds/opencode_web.js` `_ensureLoading` | reuse `#opencode-loading` pattern |
| Remote token for iframe | `internal/ui/static/framework.js` `window.api` | token read pattern (`mw_token` cookie → `?token=` query) for cross-origin iframe URL in remote mode |
| RPC wire envelope | upstream `packages/host/apiproxy/src/api/rpc.schema.ts` | `{type:'client-request', rpcId, method, payload}` |
| Mock upstream pattern | `internal/instance/opencode_web/testdata/opencode-mock.go` | `testdata/dsh-mock.go` |
