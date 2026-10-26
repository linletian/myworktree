# FIX-PLAN — issue #130：无人值守 82.7h 长跑以 Go runtime `fatal error: fault` 自杀退出的可执行修复

状态：v2（调度者起草 v1；评审 1/2 双 REJECT 后的 P1–P6 计划分歧已由**仲裁 Sub-agent**（kimi-coding/k3.1-pro）裁定并给出修订文本，本文件为其转录结果。编码 / 评审 Agent 可以质疑挑战本计划，但**不得直接修改本文件**；修改须交仲裁 Sub-agent 仲裁。）

## 0. 边界与依据（证据 → 结论）

- 崩溃点不在业务代码，在 Go `net/http` per-conn 收尾 defer（生产 Go 1.26 dump：`server.go:1911` → `inFlightResponse.cancelCtx()`；Go 1.25 GOROOT 同形状代码在 `server.go:1939-1958`：`recover()`≈1940、`cancelCtx()`=1947）的野指针 SIGSEGV（`addr=0x28066ec1be0`，`SEGV_MAPERR`，`addr≥0x1000` 且非 freed arena → `signal_unix.go` 走 `throw("fault")`）。无第二份 dump、无 core。
- **结论①**（P1 仲裁修订版）：
  - (a) D1 **不承诺**下一次 #130 形态的 teardown fault 后进程存活。其可验证承诺是：在**已武装的 HTTP 连接 goroutine** 上，handler 体内的非 nil 地址 fault 转为 panic 后可由 net/http 的 per-conn recover 接住，关闭该连接并让 daemon 继续服务；若 fault 发生在该 defer 的 `recover()` 返回之后（包括 #130 的 `cancelCtx` 形态），同一 defer 不能再次 recover，**进程仍以未恢复 panic 退出**（带 faulting goroutine 栈）。
  - (b) D1 的证据目标是 handler 路径的 `http: panic serving` + 当前 goroutine 栈，以及 teardown 路径的未恢复 panic + faulting goroutine 栈，不是"所有路径都存活"。dump 宽度由 GOTRACEBACK 决定：默认 `single` 只有当前 goroutine；无人值守部署应设置 `GOTRACEBACK=all` 以纳入所有用户创建的 goroutine，需要 runtime/internal 细节时用 `system`，需要 core 时用 `crash`（`all` ≠ `system`/`crash`）。D2 的聚合 goroutine profile 不冒充 crash traceback。
  - (c) 交叉归因仪器（开放问题 2）与 ring 稳态可见性按 D2/D3 交付，不受 P1 修订影响。
- **结论②**：§4 的回放饥饿已由 #114 修复（已合并 `3ead70d`：起点 clamp 窗口 + `sync start` 帧 + write deadline），本计划不重复实现，只补 ring 稳态可见性字段（D3）。
- **结论③**：无人值守通用自愈与孤儿 `node dsh web` 启动清理维持 out of scope（P1 仲裁同时裁决：**#130 teardown 点的同进程存活或服务级自动重启也不在本期交付内**——若重新成为硬验收项，须另立 stdlib 修改/替换或进程监督重启设计，不得用 D1 冒充达标）。

## 1. 交付物

### D1 — rescue mode：`debug.SetPanicOnFault`（默认开启，env 可一键关闭）

- env：`MYWORKTREE_PANIC_ON_FAULT`（缺省=开启；`0`/`false` → 显式关闭；解析为纯函数，非法值回退缺省并警告）。
- **接线与覆盖**（P1 修订）：`Start()` 只负责解析 env、记录配置并武装调用它的 goroutine；**主 listener 与独立 rx listener（`rxSrv`，`app.go:671-674`）的请求 middleware 必须在各自请求路径顶部重新武装实际连接 goroutine**。不得在 handler 返回时提前 disarm，否则连 teardown 的 panic 转换也得不到。handler 自行派生的 goroutine **不在本期覆盖范围内**，不得宣称"全进程所有 fault 路径均已转换"。
- **完成标准**（P1+P4）：handler-body fault 必须由真实 http.Server 集成测试证明"当前连接失败、daemon 存活、下一请求成功"；recover-after teardown 形状必须由子进程测试证明"仍以带 faulting goroutine 栈的 panic exit 2"。不得宣称后一路径产生 `http: panic serving`、只死一个连接或 daemon keeps serving。
- **dump 宽度契约**（P1）：默认 `GOTRACEBACK=single` 下，armed teardown panic 不再具有原 runtime throw 的全 goroutine 视角。本 PR 不在代码里暗改操作员的 GOTRACEBACK；D4 必须在部署/架构文档中把 `GOTRACEBACK=all` 写成无人值守取证配置建议，并用 single/all 子进程对照测试钉住差异（`all` ≠ `system`/`crash`）。
- **既有代价继续保留**：fault 可能意味着 runtime/heap 已损坏，handler 路径继续运行可能暴露更多损坏；`MYWORKTREE_PANIC_ON_FAULT=0|false` 是回滚开关。
- 启动日志：武装生效时打 INFO `[runtime-health] panic-on-fault enabled (issue #130)`；显式关闭时也打一行。
- 注释必须写清机制：`recover()` 只能接住当时已在飞的 panic（Go `panic.go:1003-1016` 语义）；`net/http` 收尾 defer 的 `recover()` 先于 `cancelCtx()` 执行，因此 defer 内后发生的 fault 无人接（`SetPanicOnFault` 是 per-goroutine 开关：`runtime/rdebug.go`、新 goroutine 自 `proc.go` 复位）。
- **测试**（P4 修订，必做，普通 go test 与 -race 均须运行）：真实 fault 集成与边界测试——
  1. **handler 存活用例**：子进程 helper 使用真实非 nil SIGSEGV（例如匿名 mmap 后 munmap 再读取，读取结果写入全局 sink 防止编译器删 load），把测试 `/fault`、`/ok` 路由装入测试 Server，走生产 `Server.Start()` 和生产 middleware，不得用手写 recover 或假 setter 替代。请求 `/fault` 后允许得到 EOF；必须观察到 `http: panic serving` 和 faulting 测试函数栈，随后新请求 `/ok` 必须返回 200 和预期 body，最后正常 Shutdown。
  2. **teardown 边界用例**：另一子进程 helper 执行与 net/http 相同顺序的 defer——先调用并结束 `recover()`，再制造真实 fault。父测试必须断言 helper exit 2、输出含未恢复 `panic:` 与 faulting 函数栈；不得断言 daemon 存活或 `http: panic serving`。
  3. **dump 宽度对照**：预置并同步一个 `parkedStackMarker` goroutine；`GOTRACEBACK=single` 的 helper 输出不得包含其独立栈，`GOTRACEBACK=all` 必须包含。两种情况下 exit 2 都是预期结果。
  4. 父测试统一用 `exec.Command(os.Args[0], ...)` + 独立 helper env 启动并 `CombinedOutput()` 捕获；致命形状只允许存在于 helper。helper 是否崩溃由父测试判别，不能让 expected exit 2 直接成为 CI 失败。
  5. 保留 env 纯函数与接线单测；普通 panic 不能代替真实非 nil SIGSEGV。

### D2 — `[runtime-health]` 周期采样器 + goroutine 告警 dump + per-endpoint 归因

- 新文件 `internal/app/runtime_health.go` + `runtime_health_test.go`；goroutine 由 `Start()` 启动、`Shutdown()` 停止并 join（参照 `alertSampler` 生命周期）。
- 周期打点（默认 60s；env `MYWORKTREE_RUNTIME_HEALTH_INTERVAL_MS`，≤0 = 关闭）：
  `[runtime-health] tick uptMs=... goroutines=N heapMB=... sysMB=... gcCount=... inflight=... reqTotal=... top=(pattern inflight/total, …)`
  （`runtime.NumGoroutine` + `runtime.ReadMemStats`；注释量化 60s 频率下的 STW 成本。）uptime 以 `Start()` 时刻计（v1 以构造时刻计的偏移须修正）。
- goroutine 告警：`goroutines > 阈值`（env `MYWORKTREE_RUNTIME_HEALTH_GOROUTINE_ALERT`，默认 1000）→ `[runtime-health] alert goroutines=N threshold=...` + goroutine profile 完整 dump 到 logger（先写 buffer 再一次落 logger，防与并发 tick 行交错）；**同一进程两次 dump 冷却 ≥30 分钟**（常量钉在单独测试）。
- **per-endpoint 归因**（P2 修订）：middleware 挂在 `http.Server.Handler`、保持路由注册不变。由于该挂载点位于 ServeMux 外，进入 wrapper 时 `r.Pattern` 通常尚未填充，不得直接把它作为唯一键。键解析顺序固定为：
  1. `r.Pattern` 已非空则沿用；
  2. 否则调用**当前 listener 所对应 mux** 的 `mux.Handler(r)`，以其第二返回值（注册 pattern）为键；该查询不修改 request，但会多走一次路由查找；
  3. 仍取不到 pattern（如 404/405 或 httpmuxgo121 模式）则落 `(!pattern:METHOD:/path)`，不带 query。
  键表继续受既有 64 键上限约束，溢出折入 `(!capped)`；tick 与 alert 均输出按累计量排序的 top-5 `pattern inflight/total`。
  （键必须无空格/换行/控制字符——URL.Path 是解码后的值，含空格/换行的真实路径会破坏空格分隔契约并造成日志注入；用 `r.URL.EscapedPath()` 或做确定性清洗，并用 `%20`/`%0A` 用例钉住。）
- **listener 覆盖范围**（P3 修订）：覆盖主 listener 与独立 loopback reasonix listener（`rxSrv`）。两者都必须在各自 `http.Server.Handler` 最外层安装 `withPanicOnFault(withRuntimeHealth(...))`；D2 的 pattern 预查找必须接收并使用**当前 listener 的 mux**（主=`s.mux`，rx=`rxMux`），禁止 rx 请求仍固定拿 `s.mux` 查键。两个 listener 共享同一 runtimeHealth、同一 64 键上限和同一采样循环，不新增 ticker/goroutine。rxMux 仅注册 `/rx/`，故独立反代流量归入 `/rx/` 聚合桶，不承诺 reasonix instance 级归因；现有鉴权、跨域、cookie 和反代行为不变。每个 rx 请求的边际成本与主链相同。**验收必须向真实 rx listener 发一个无需上游即可完成的请求（404/503 亦可），断言共享 reqTotal 增加且 `/rx/` 桶可见**。

### D3 — TTY 指标补 ring 稳态可见性（P5 修订）

- `ttyConnMetrics.logSummary` 与握手行追加 `ringCapBytes=`、`ringLagBytes=`，保持既有字段顺序及 ttymetrics 构建门控：
  - `ringCapBytes > 0`：握手探针测得的 ring 容量；`=0`：探针已执行但当时无 ring（stopped/never-started 或无 buffer）；`=-1`：探针尚未执行，例如连接在 completeHandshake 前关闭。0 与 -1 不得合并解释。
  - `ringLagBytes >= 0`：已知 lag，普通情况为 `max(headAtStart-since, 0)`；`=0` 表示已追平，也包含 `since>head` 的向下 clamp。`=-1` 表示未知，包括 head 读取失败、`since=-1` 的无游标首连、`since=-2` 的 follow-live-end；未知不得打印成 0。
  - 两个数值都冻结于握手开始；conn-closed 行重复该快照，不宣称是关闭时的实时 lag。
- ARCHITECTURE 必须逐项写出上述语义，不能用一句笼统的"两者都是 -1=未知、0=追平"覆盖 cap 与 lag。
- stable（未打 ttymetrics tag）可以继续沿用现有"少量 dead field stores"的共享结构约定；本项不得让 stable 新增 manager read、真实计时或日志输出，也无需仅为 ring 字段再拆一套类型。文档应准确写"日志仅在 tagged 构建输出"，不得写"stable 不含任何死字段/完全零写入"。

### D4 — 文档与提交材料

- `CHANGELOG.md` 顶部新 entry，沿用现仓库"证据→改动→回滚"逐字风格；**必须显式记录既有冻结布局断言的契约变更**（`tty_ws_test.go` 的 suffix→contains + 新尾部正则）。
- `docs/ARCHITECTURE.md` §10 增补 runtime-health 契约（D1 真实承诺 + P3 覆盖范围 + D3 哨兵语义 + 告警行节奏的数量级说明）。
- commit message（P1 仲裁定稿）：
  `fix(runtime): handler 路径 fault 可恢复、teardown fault 转带栈 panic 退出，并补 runtime-health 归因 (issue #130)`
- 三处文案统一承诺（文件头 / CHANGELOG 正文 / ARCHITECTURE）：
  D1 默认在已挂载 middleware 的 HTTP 连接 goroutine 上武装 `SetPanicOnFault`：handler 体内的非 nil 地址 fault 可由 `net/http` recover、关闭该连接并让 daemon 继续服务，而发生在 per-conn 收尾 defer 的 `recover()` 之后的 fault（#130 的 `cancelCtx` 形态）会逃出该 defer、仍以带 faulting goroutine 栈的 panic 终止进程，且默认 `GOTRACEBACK=single` 会把原 runtime throw 的全 goroutine 诊断视角收窄，因此无人值守取证部署应同时设置 `GOTRACEBACK=all`，不得宣称 teardown fault 后进程仍然存活。

## 2. 验收（P6 修订）

- `go build ./...`；`go test ./...`；`go vet ./internal/app/`；`go test -race -count=1 ./internal/app/ ./internal/ws/`；`go test -tags ttymetrics ./internal/app/` 全绿；`gofmt -l .` 必须无输出。真实 fault 集成/边界用例不得从普通或 -race 口径中跳过。
- 新增后台 goroutine 必须 join（Shutdown 返回后无泄漏）。
- 不改 wire 协议、不改 #114 回放语义、不动 ring cap/内存策略。

## 3. 明确不做（OUT OF SCOPE）

- 硬碰野指针 root cause（当前无证据支持任何一种解释，见 issue §8.1）。
- **#130 teardown 点的同进程存活或服务级自动重启**（P1 仲裁条款；须另立 stdlib 修改/替换或进程监督重启设计）。
- ring cap / 全局内存预算 / 稳态回收改版；孤儿 `node dsh web` 启动清理；Web UI 侧改动。
