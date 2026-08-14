# Code Review: feature/opencode-native-ui @ e4158fa

> 评审时间：2026-08-13 19:01–19:08
> 评审 commit：`e4158fa44cefa8338647beedd320539be14c208b`（分支 tip，`feature/opencode-native-ui`）
> 对比 base：`bc7a9ae62effc1d4d384ce2306661a2256a225e4`（main / develop 共同 merge-base）
> 评审范围：`bc7a9ae6..e4158fa`，10 commit，52 files，+7162 / −3081
> 评审者：Mavis (root session `mvs_54f3e5e493764620900a369733276cbc`)
> 关联文档：`docs/plans/opencode-native-ui/{README,PLAN,FEASIBILITY,WORKTREE-ISOLATION,DEBUG}.md`

## TL;DR

- 对比 base：`bc7a9ae6`（main 与 develop 在此处 fork 相同）
- 范围：当前分支领先 fork 点 10 个 commit（其中最近 2 个尚未 push）；最新 1 个 commit `e4158fa` 是 CSS 隐藏优化（无功能问题）
- 本地状态：工作区干净；`go build ./...` 与 `go vet ./internal/{framework,instance,app,cli,store,tag,mcp}/...` 均通过
- **结论**：整体架构（`framework.Manager` + `Kind` 接口 + 子包注册）思路清晰，向后兼容做得不错（`Extra`/`KindBlob` 双写、Status 字符串不变）。但 **PTY 路径有两处相对老 `instance.Manager` 的真实回归**，并伴随一个会让 opencode-web 子进程最终卡死的管道处理 bug。其余多为可改进项。

---

## 🔴 严重：PTY 输出订阅是全局共享的（多 tab 串台）

**位置**：`internal/instance/pty/driver.go:254-289`（全局 `subs`）+ `internal/instance/pty/driver.go:214-216`（`Driver.SubscribeOutput` 直接返回全局订阅）

**现象**：所有 PTY 实例共用一份 `subs map[chan string]struct{}`；`pumpLogs` 调 `broadcast(chunk)` 走的是 `subsMu` 下的全局 map。`framework.Manager.SubscribeOutput(id)`（`internal/framework/methods.go:329-347`）虽然按 id 查了 `m.running[id]`，但最终把 `Driver.SubscribeOutput()` 的返回值原样返回——所以**订阅 instance A 的客户端会同时收到 instance B/C/D 的 PTY 输出**。

**对照老实现**（`bc7a9ae6` 时的 `internal/instance/manager.go:921-947`）：

```go
func (m *Manager) broadcastOutput(id string, chunk string) {
    ...
    subs := m.subscribers[id]   // per-instance
    for ch := range subs { ... }
}
```

**触发路径**：开 ≥2 个 PTY tab，`xterm` 终端会混入另一 tab 的输出。

**修复方向**：把 `subs` 下沉为 `Driver` 的实例字段（或在 `framework.Manager` 里维护 `m.subscribers[id] map[chan string]struct{}`，PTY 走 `outputSub` 扩展时把 id 透传给 `Driver`），让 broadcast 带 id 过滤。

---

## 🔴 严重：PTY 的 ring buffer 没有注册到 Manager（buffer 统计回归）

**位置**：`internal/instance/pty/driver.go:78` — `buf := framework.NewRingBuffer(framework.DefaultBufferCap)` 创建后直接挂到 `Handle`，**从未调 `m.AllocateBuffer(id, cap)`**。

**影响**：
- `framework.Manager.BufferCapBytesFor(id)` / `BufferUsedBytesFor(id)`（`internal/framework/methods.go:297-324`）对 PTY 实例永远返回 0。
- `m.totalBufBytes` 不包含 PTY buffer，`memsampler.ResolveCap` 的预算判断等于失效——理论上可无限制开 PTY 实例。
- `m.buffers[id]` 为空导致 `dropBuffer`（`methods.go:400-414`）是空操作，PID/FD 清理路径可能漏。

**修复方向**：在 `Driver.Spawn` 收尾前 `mgr.AllocateBuffer(id, framework.DefaultBufferCap)`（需要把 `m *framework.Manager` 注入 Driver，或在 `Spawn` 后由 framework 回灌——前者更简单）。

---

## 🟠 重要：`opencode_web` 子进程 stdout 管道不会 drain（长跑后会写阻塞）

**位置**：`internal/instance/opencode_web/driver.go:331-370` `pumpAndWatch`

```go
for sc.Scan() {
    ...
    host, port, ok := extractListeningAddress(line)
    if ok {
        ...
        return   // 找到 listening 行就退出，scanner 不再读
    }
}
```

`pumpAndWatch` 一旦匹配到 `opencode server listening on ...` 就 `return`，scanner 停读。`stdoutR` 是 `io.PipeReader`，`pumpAndWatch` 退出后没人消费。子进程后续任何 stdout 写入都会进 OS pipe 缓冲（Linux 默认 64 KB），缓冲一满子进程在 `write(2)` 上阻塞，UI 端会看到「卡死」。

**对照老 `instance.Manager.pumpLogs`**：始终循环 `ptmx.Read` 到 EOF，不管有没有 buffer（见 `bc7a9ae6` 的 `manager.go:888` 注释：「drain ptmx so the underlying process is not blocked on a full pty buffer」）。

**修复方向**：找到 listening 行后改成 `io.Copy(io.Discard, r)` 把剩余字节消费光再退出，或单独开一个常驻 drain goroutine。

---

## 🟠 重要：`opencode_web` `SetPublishers` 无锁写，与 `pumpAndWatch` / `healthLoop` 读竞态

**位置**：`internal/instance/opencode_web/driver.go:164-168`

```go
func (Driver) SetPublishers(handle framework.Handle, p framework.Publisher) {
    h := mustHandle(handle)
    h.publisher = p         // 无锁写
    h.AttachInstanceID(p.InstanceID())  // 内部也只对 h.instanceID 加锁，但调用顺序不严谨
}
```

`pumpAndWatch`（`driver.go:363-365`）和 `healthLoop`（`driver.go:394-395`）读 `h.publisher` 不在 `h.mu` 下：

```go
if h.publisher != nil {
    _ = h.publisher.MarkRunning()
}
```

`-race` 跑会报 `WARNING: DATA RACE`。`AttachInstanceID` 内部确实拿了 `h.mu`，但 `h.publisher = p` 这行裸写，且 `h.instanceID` 在 `pumpAndWatch` 的 `h.mu` 块内读（line 348），外面写——时序上 `SetPublishers` 在 `Spawn` 返回后立即发生，但顺序仍非 happen-before。

**修复方向**：`SetPublishers` 内对 `h.mu` 加锁；或把 `h.publisher` 改成 `atomic.Pointer[framework.Publisher]`。

---

## 🟠 重要：`opencode_web` `Stop` 忽略 `graceSeconds` 参数

**位置**：`internal/instance/opencode_web/driver.go:170-183`

`graceSeconds int` 参数收到但完全没用——SIGKILL 倒计时始终是包内常量 `stopGrace = 5 * time.Second`。framework 传 `m.stopGraceSeconds`（默认 5s）过来，调用方配置无效。

**修复方向**：把硬编码常量换成 `time.Duration(graceSeconds) * time.Second`，并对 0/负值兜底（用 `stopGrace` 兜底）。

---

## 🟡 次要：proxy.go 每次请求无条件写 stderr

**位置**：`internal/instance/opencode_web/proxy.go:86-87`

```go
fmt.Fprintf(os.Stderr, "[opencode-proxy] %s %s → http://%s:%s%s ...\n", ...)
```

`xterm.js` SSE 流是高频请求，这条日志会对每个请求写一次 stderr——既扰民又有竞争。`dir`/`scope` 来自请求方，可被用来伪造日志行。

**修复方向**：注入一个 `*log.Logger`（与 `framework.Manager.Logger` 对齐），用 `Debug` 级别 + 默认关闭；或者干脆只在 4xx/5xx 时记。

---

## 🟡 次要：未使用 import 的「凑数声明」

- `internal/framework/methods.go:434-436`：`var (_ sync.Mutex)` 凑 `sync` import（其实文件里没直接用 `sync`）。
- `internal/instance/opencode_web/proxy.go:351-352`：`var _ = log.Printf` 凑 `log` import（真的没用到）；`var _ = io.Discard` 是多余的（`io.ReadAll`/`io.NopCloser` 已用到 `io`）。

**修复方向**：删掉凑数声明，把真正没用的 import 一并清理（`goimports` 一遍就解决）。

---

## 🟡 次要：`framework.Manager` 内部状态字段未清理

**位置**：`internal/framework/manager.go:60-73`

- `stateMu sync.Mutex` 声明了但 `manager.go` 自己从不用（只在 `methods.go` 用）。
- `buffers`/`subscribers`/`conns` 三个 map 在 `manager.go` 没出现，但声明放在这里。

读起来割裂，且 `stateMu` 名字暗示是「state 互斥」但和 `mu` 的分工没文档化，`Delete`（`methods.go:56-61`）里的 mu → connsMu → stateMu 嵌套顺序需要靠注释维护。

**修复方向**：把 `stateMu`/`buffers`/`subscribers`/`conns` 整体迁到 `methods.go`（或拆出 `manager_state.go`），并在 `Manager` 文档注释里写清楚锁序：始终 `mu` → `connsMu` → `stateMu`。

---

## 🟡 次要：硬编码的 ready 超时

**位置**：`internal/framework/manager.go:228-233`

`runLifecycle` 里 `<-time.After(60 * time.Second)` 是硬编码 60s，与 opencode_web 包内 `readyTimeout = 60 * time.Second`（`driver.go:51`）耦合——但 PTY 启动 < 1s、opencode 1-3s 启动、某些慢机器 30s 都不够。framework 应当从配置拿，或者把这个超时下放到 `Kind.ReadySignal` 上下文（让每个 kind 自己定）。

---

## 🟢 已做对的点（不需要改）

- `ManagedInstance` 的 `Kind/Extra/KindBlob/LastError/ExitCode` 向后兼容（`state_test.go:303-362` 已覆盖）。
- 注入脚本的 CSP 锚点检测 + SHA256 hash（`proxy.go:189-204`）+ drift 上报（`ScopeTracker.SetCSPAnchorMissing`）——结构性 CSP 漂移不会静默丢单 worktree 隐藏。
- `scope.go` 的 `normalizeDir` 用 `filepath.EvalSymlinks` 做软链对齐，symlink/大小写/尾分隔不会误判。
- `version.go` 用 min..max 区间（`1.18.0 ≤ v < 1.19.0`）+ 未知版本 `true`——dev 构建不会被误伤。
- `isolation_check` 顶层 smoke + `proxy_test.go` / `driver_test.go` / `scope_test.go` 覆盖到了主要路径，proxy 的 HTML 重写、CSP 锚点、压缩跳过都有断言。
- `instance.Manager` → `framework.Manager` 的迁移在 `app.go` / `cli.go` / `mcp/stub.go` 都把引用方同步改完了，无悬挂 import。
- `tag.go` 给 `opencode-web` tag 留空 `Command`，并加单测（`tag_test.go:13-37`）固化「不能通过 tag 改 opencode 启动参数」这个产品决策，避免误用。

---

## 优先级建议

1. **先修 P0**：PTY per-instance subscriber 回归（多 tab 串台是用户立刻能看到的）。PTY buffer 未注册（影响预算）。
2. **P1**：`opencode_web` stdout 管道 drain（长跑才暴露，但一旦遇到就是子进程卡死）+ `SetPublishers` data race。
3. **P2**：`Stop` 忽略 `graceSeconds`、proxy 每请求 stderr 日志。
4. **P3**：清理 unused import 凑数、迁移 manager 字段、补 `runLifecycle` ready 超时配置化。

---

## 修复记录（2026-08-13）

> 修复 commit：基于 `e4158fa` 的后续工作树，未 push；`git diff --stat` 9 files / +370 / −54
> 修复时间：2026-08-13
> 关联需求文档：`PLAN.md`、`WORKTREE-ISOLATION.md`、`DEBUG.md`

### 评审核验结论

9 项评审点中 7 项确认为真实问题（6 项真实代码 bug + 1 项真实回归），按优先级全部修复；2 项（stateMu 位置、ready 超时 60s）确认为美化项，未触动。修复前已对照 `PLAN.md` 验收清单与 `WORKTREE-ISOLATION.md` 的设计基线，确认未发生需求漂移。

### 修复明细

| # | 评审项 | 验证证据 | 修复方案 | 落地位置 |
|---|---|---|---|---|
| 1 | 🔴 PTY 输出订阅全局共享（多 tab 串台） | `internal/instance/pty/driver.go:254-289` `subs map[chan string]struct{}` 包级变量；`pumpLogs` 无 id 过滤；`Driver.SubscribeOutput()` 直接返回全局订阅 | `subs` 改为 `map[string]map[chan string]struct{}` 按 instance id 分桶；`broadcast(id, chunk)` 过滤；`SubscribeOutput(id)` 必传 id（空 id 返回错误防止误用） | `internal/instance/pty/driver.go:266-319` |
| 2 | 🔴 PTY ring buffer 未注册到 Manager | `internal/instance/pty/driver.go:78` `buf := framework.NewRingBuffer(...)` 创建后仅挂到 Handle；`m.AllocateBuffer(id, cap)` 从未被调用；`m.totalBufBytes` 不含 PTY；`dropBuffer` 是空操作 | `framework.SpawnParams` 新增 `Buffer *RingBuffer` 字段；`framework.Manager.Start` 在 Spawn 前调 `AllocateBuffer(id, capBytes)` 并通过 SpawnParams 下传；PTY 使用 `params.Buffer`；删除冗余的 `m.totalBufBytes.Add(capBytes)`（AllocateBuffer 已包含） | `internal/framework/{kind,manager,methods}.go`、`internal/instance/pty/driver.go:79-86` |
| 3 | 🟠 `opencode_web` stdout 不 drain | `internal/instance/opencode_web/driver.go:331-370` `pumpAndWatch` 找到 listening 行直接 `return`，未消费 stdoutR；子进程继续写满 OS pipe 后阻塞；对照老 `Manager.pumpLogs` 注释「drain ptmx so the underlying process is not blocked on a full pty buffer」 | ready 后追加 `_, _ = io.Copy(io.Discard, r)` 直至 EOF；如 `ctx` cancel 则立即退出 | `internal/instance/opencode_web/driver.go:384-389` |
| 4 | 🟠 SetPublishers data race | `internal/instance/opencode_web/driver.go:164-168` `h.publisher = p` 裸写；`pumpAndWatch`/`healthLoop`/`wait` 裸读；`AttachInstanceID` 内部加锁但裸写 line 166 不加锁 | `h.publisher` 改为 `atomic.Pointer[framework.Publisher]`；统一走 `h.loadPublisher()` 读侧；`-race` 通过 | `internal/instance/opencode_web/driver.go:106-184` |
| 5 | 🟠 Stop 忽略 graceSeconds | `internal/instance/opencode_web/driver.go:170-183` `graceSeconds int` 收到但使用包级常量 `stopGrace = 5 * time.Second` | `time.Duration(graceSeconds) * time.Second`，0/负值兜底到 `stopGrace` | `internal/instance/opencode_web/driver.go:170-193` |
| 6 | 🟡 proxy.go 每请求 stderr | `internal/instance/opencode_web/proxy.go:86-87` 每个请求 `fmt.Fprintf(os.Stderr, ...)`；SSE 高频请求会写穿 stderr | 删除 stderr 直写，改读 `framework.Manager.Logger`（`NewManager` 默认 `io.Discard`）；nil logger 静默跳过 | `internal/instance/opencode_web/proxy.go:86-94, 351-358` |
| 7 | 🟡 unused import 凑数 | `proxy.go:351-352` `var _ = log.Printf` / `var _ = io.Discard`（`log` 真未用，`io` 真用了 `ReadAll`/`NopCloser`）；`methods.go:434-436` `var (_ sync.Mutex)` 凑 `sync` import | 删除三个占位声明 + `os` import；同步删除 `methods.go` 中的 `sync` import（已无任何引用） | `internal/instance/opencode_web/proxy.go`、`internal/framework/methods.go` |
| 8 | 🟡 stateMu 位置 | `internal/framework/manager.go:60-73` `stateMu`/`buffers`/`subscribers`/`conns` 声明在 manager.go 但实现散在 methods.go | 跳过 — 美化项，非回归；锁序 `mu → connsMu → stateMu` 现有注释已说明 |
| 9 | 🟡 60s ready 超时硬编码 | `internal/framework/manager.go:228-233` `<-time.After(60 * time.Second)` 与 opencode_web 包内 `readyTimeout = 60s` 耦合 | 跳过 — 美化项；60s 覆盖 PTY(<1s) + opencode(1-3s) + 慢机器(30s)，且超出时已 `markFailed` 显式提示 |

### 改动统计

```
CHANGELOG.md                                  |   1 +
internal/framework/kind.go                    |  21 ++++
internal/framework/manager.go                 |  26 +++--
internal/framework/methods.go                 |  17 +--
internal/instance/opencode_web/driver.go      |  65 +++++++++---
internal/instance/opencode_web/driver_test.go |  61 +++++++++++
internal/instance/opencode_web/proxy.go       |  25 ++++-
internal/instance/pty/driver.go               |  66 ++++++++----
internal/instance/pty/driver_test.go          | 142 ++++++++++++++++++++++++++
9 files changed, 370 insertions(+), 54 deletions(-)
```

### 验证

- **构建**：`go build ./...` 无输出
- **静态检查**：`go vet ./...` 无输出
- **测试（含 race）**：`go test ./... -race` 全过（`framework`、`pty`、`opencode_web`、`app`、`cli`、`portal`、`store`、`tag` 等共 18 个包）
- **新增回归测试**：
  - `pty/driver_test.go::TestSubscribeOutput_ScopedPerInstance` — A/B 实例互不串台（直接对应评审 P0 #1）
  - `pty/driver_test.go::TestBroadcast_Parallel` — 并发 subscribe/cancel/broadcast race 干净
  - `opencode_web/driver_test.go::TestPumpAndWatch_DrainsAfterListening` — 256 KiB 尾随输出（远超 64 KiB pipe 缓冲）必须在 2s 内 EOF 退出（直接对应评审 P1 #3）

### 与需求文档一致性核查

- `PLAN.md §复用的现有组件` Ring buffer 预算语义：修复前 PTY 不计入 `totalBufBytes`，预算失效；修复后 `BufferCapBytesFor(pty_id)` 返回正确值，`dropBuffer` 真正释放
- `PLAN.md §验收标准` 「PTY 模式 instance 完全无回归」：修复后多 tab 输出隔离、buffer 注册到 Manager 均回归正确行为
- `WORKTREE-ISOLATION.md §3 D4`（完整页嵌入 + 入口隐藏）未受影响；proxy.go 改动仅替换日志通道，未触及 HTML 注入、CSP hash、scope 监测、`rewriteRootAttrs` 等核心逻辑
- `DEBUG.md`「待解决问题」清单（孤儿进程、sandbox 归一化、Safari ReadableStream）仍标注未根治，与本次修复范围无关
- `outputSub` 接口签名升级为 `SubscribeOutput(id string)`；`SpawnParams` 新增字段均为可选（`Buffer == nil` 时 PTY 兜底自建）→ 向后兼容；老 `instance.Manager` 路径已不存在（`feature/opencode-native-ui` 分支纯 kind 注册表架构），无其他 caller
