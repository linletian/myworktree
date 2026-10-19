# Issue #114 数据采集手册（tty subscriber overflow → 1013 重连循环）

诊断埋点随 `fix/fix-issue114` 分支落地，统一日志前缀 `[tty-metrics]`，
**按构建渠道门控**：

- **beta 构建**（`release-beta.yml`，`-tags ttymetrics`）：埋点编译进
  二进制，长期保留；服务端输出 `[tty-metrics]` 日志，并通过响应下发
  `mw_tty_metrics=1` cookie 点亮浏览器侧的客户端埋点。
- **正式版构建**（`release.yml`，无 tag）：Go 侧编译为 passthrough 空实现
  （`tty_metrics_off.go`，计时/日志全部内联消除，二进制内不含任何
  埋点格式串，EndOffset 探针也被 const 门控消除），且**主动清除**
  浏览器里可能残留的 `mw_tty_metrics` cookie（同一浏览器会话先跑过
  beta 守护进程的场景），页面 JS 埋点整体惰性（无日志、无
  `term.write` 回调开销）。本地等价构建：`go build -tags ttymetrics ./cmd/myworktree`。
- 两种变体都由 `go-ci.yml` 编译 + 测试，防止任一变体腐化。

目的：在修复方案选型（带内 resync / 退避重连 / write deadline / 回放限速 /
后台标签页暂停推流）之前，用真实复现场景回答一个核心问题——
**消费者为什么慢**（后台标签页节流 / 网络带宽 / 浏览器 CPU 饥饿，后者与 #111 耦合）。

埋点只计时、计数、打日志，不改变任何线上行为；待修复落地且数据
回答上述问题后，再评估移除或继续保留。

## 一、埋点清单

### 服务端（守护进程 stderr 日志，`internal/app/tty_metrics.go`）

| 日志行 | 触发时机 | 关键字段 |
| --- | --- | --- |
| `[tty-metrics] handshake` | 每次 WS 握手完成（订阅建立时） | `mode`（tail/catchup/follow-live-end）、`since`（客户端 cursor）、`headAtStart`（握手开始时 ring head）、`replayBytes`、`replayMs`、`budgetExhausted`（是否撞 8MB 预算）。**规划中（FIX-PLAN v6，尚未产出）**：`windowed=true`/`droppedFromWindow`（窗口化回放截断）；compress 连接的 slow-write/conn-closed 将增 `wireBytes`（压缩后）与 `logicalBytes`（解压后）双字段，消费速率口径随连接而变，届时以 logicalBytes 对齐历史数据 |
| `[tty-metrics] overflow` | subscriber 队列溢出（drop 策略连接：发 overflow notice，连接保留；close 策略：以 1013 断开前） | `subAgeMs`（订阅存活时长）、`liveBytes`/`liveFrames`（本次连接已交付的 live 输出）、`headNow`（溢出时 ring head）、`maxWriteBlockMs`、`slowWrites`。**注意（2026-10-17 实测）**：drop 策略连接的该日志行受 `ttyDropLogInterval=500ms` 节流（首增量即打、后续每连接 ≤2 行/s），**日志是采样、wire notice 才是完整记录**；按日志行数推算 drop 总数会偏低 |
| `[tty-metrics] conn-closed` | 每条 TTY WS 连接消亡时（任何原因） | `aliveMs`、`binBytes`/`binFrames`、`slowWrites`、`maxWriteBlockMs`、`totalWriteBlockMs` |
| `[tty-metrics] slow-write` | 单次 socket 写阻塞 ≥1s | `op`（binary/text/ping）、`bytes`、`blockMs` |

字段解读：

- `overflow` 行的 `liveBytes / subAgeMs` ≈ **消费者的有效消费速率**，
  与 64KB 队列容量对照即可量化「落后多少触发断开」。
- `handshake` 行的 `headAtStart - since` = 本次重连的回放需求量；
  `replayBytes` 撞顶 8MB 且 `budgetExhausted=true` 说明需求超过预算。
- `slow-write` / `conn-closed` 的写阻塞数据区分「网络/内核背压」与
  「浏览器不收」；`op=ping` 的慢写直接证实心跳被输出写锁拖累。

### 客户端（浏览器控制台，`internal/ui/static/index.html`）

| 日志行 | 触发时机 | 关键字段 |
| --- | --- | --- |
| `[tty-metrics] ready` | 收到服务端 ready | `connectMs`、`openMs`、`since` |
| `[tty-metrics] close` | 每次 WS 关闭（onclose） | `code`、`reason`、`aliveMs`、`rxBytes`/`rxFrames`、`replayBytes`（#94 start/end sync 括号内的回放量，-1 = 无括号回放）、`replayIncomplete`（死于回放途中）、`visibility`（标签页可见性）、`writeCalls`/`writeWaitTotalMs`/`writeWaitMaxMs`（xterm.js 解析积压的**会话累计值**，重连不清零）、`connWriteCalls`/`connWriteWaitMs`（**本连接**的解析积压增量——跨重连的 drain-rate 分析用这组，无需手工减基线） |
| `[tty-metrics] visibility=...` | 标签页切换前后台 | `visibilitychange` 事件直通 |

控制台手动快照（DevTools 执行）：

```js
copy(JSON.stringify(__ttyMetrics(), null, 2))
```

返回每个终端会话的 `ttyState` / `ttyOffset` / 距最后一帧的毫秒数 /
xterm 解析积压累计值。

### 实例输出速率（无需改代码，外部轮询）

`GET /api/instances/log` 响应头 `X-Log-Offset` 即 ring head，
两次采样之差 ÷ 间隔 = 实例输出速率：

```bash
while true; do
  printf '%s ' "$(date +%T)"
  curl -s -o /dev/null -D - "http://HOST:PORT/api/instances/log?id=INSTANCE_ID&token=TOKEN" \
    | grep -i '^x-log-offset' | tr -d '\r'
  sleep 5
done | tee /tmp/headrate.log
```

## 二、部署与复现步骤

1. 构建并重启守护进程（用你日常运行的二进制，如 mwbeta）：
   **必须带 tag** —— `go build -tags ttymetrics ./cmd/myworktree`
   （beta 发布工作流已自动带此 tag，beta 包开箱即有埋点）。
2. 浏览器**硬刷新**（Ctrl/Cmd+Shift+R）拿到新 `index.html`；
   服务端响应会种下 `mw_tty_metrics=1` cookie（DevTools → Application →
   Cookies 可见），确认控制台里能看到 `[tty-metrics] ready ...`。
   若 cookie 在旧守护进程时代已缓存为不存在，刷新一次页面即可。
3. 打开那个跑长任务的大缓存实例的终端，保持复现条件（切后台标签页等）。
4. 让复现持续足够久（覆盖若干次 `ws closed, retrying...` 周期）。

确定性对照复现（可选，不依赖长任务）：在实例里跑定速输出源，例如
`while true; do head -c 4096 /dev/urandom | base64; sleep 0.05; done`，
用浏览器 DevTools 的 CPU throttle / Network throttle 或后台标签页制造慢消费者。

## 三、采集

```bash
# 服务端日志（守护进程 stderr 重定向到的文件）
grep '\[tty-metrics\]' daemon.log > tty-metrics-server.log

# 浏览器：DevTools Console 过滤 [tty-metrics]，右键 Save as… 导出；
# 结束时再执行一次 __ttyMetrics() 快照（见上）。
```

## 四、数据 → 决策矩阵

> **2026-10-17 修订注记（评审 R9；v5 二轮补注 LOW-E；v6 三轮复核订数）**：
> 第二行「subAge 小 + replay 大 → 回放洪流压垮 → 限速/降预算」**已作废**；
> 第五行「回放分帧背压 / 预算重估」同批作废（「分帧背压」即被否决的
> pacing 方向）。2026-10-17 生产日志+代码走查（EVIDENCE-2026-10-17.md
> §三）证实：回放走 `ReadSince` 直读 ring、不经订阅者队列，压不垮队列；
> subAge 小的溢出 = live 生产超标（引擎 A，缺口为 **burst 性**：持续生产
> 端点法 ~17KB/s、burst 150–358KB/s、drain 中位 47KB/s——v6 口径订正），
> 回放的真实代价是**时长**且跟随活 head（引擎 B，最差 replayMs 84.7s，
> 时长只受 8MB 预算约束）。「限速/pacing」无对象可限，已否决。现行方案
> 见 FIX-PLAN.md（v6）：引擎 A → drop-oldest+notice+4MB 队列（决策 1/1b，
> 缺口算术见其仲裁记录）；引擎 B → 窗口化回放起点+总量双封顶（决策 4）；
> 止血 → 指数退避（closing sync 后 60s 稳定锚点，决策 2）。

| 观测结果 | 结论 | 主治方案 |
| --- | --- | --- |
| `close` 行 `visibility=hidden` 占比高 | 后台标签页节流是主因 | 优先级提前：hidden 暂停推流 / 回前台再重连（**待 Phase B-8 客户端埋点回填验证**，MED-4） |
| ~~`overflow` 行 `subAgeMs` 小（秒级内）且 `handshake.replayBytes` 大~~ → **回放洪流本身压垮消费者** | ~~带内 resync（skip-to-live）+ 回放限速/降预算~~ | **已作废**（见上注）；实际机理：live 生产超标（队列仅订阅后存在）→ drop+notice |
| `slow-write` 多、`maxWriteBlockMs` 大，但标签页前台、网络正常 | 浏览器渲染/CPU 瓶颈（疑与 #111 耦合） | 带内 resync 为主；同时查 mwbeta CPU/内存 |
| `slow-write op=ping` 出现 | 心跳被输出写锁饿死 | write deadline（#83 遗留）必须补 |
| ~~`replayMs` 远大于 live 阶段时长~~ → 回放是循环的放大器 | ~~回放分帧背压 / 预算重估~~ | **已作废（v5 补注 LOW-E）**：「分帧背压」即被否决的 pacing 方向（回放不经订阅队列、每 chunk 同步阻塞写，无对象可限）；现行对策为决策 4 窗口化回放（起点+总量双封顶）；已证实回放跟随活 head、时长只受预算约束（HIGH-2） |

无论哪种成因，客户端指数退避 + jitter 都是无条件的止血项。

## 五、埋点验证记录（2026-10-14，本地冒烟）

`yes` 洪水实例 + 客户端停读 10s 的确定性复现下：

- `handshake mode=tail replayBytes=65536 headAtStart=424136824`（首连 tail 回放）✓
- `overflow subAgeMs=56 liveBytes=214981 headNow=424369285`（停读后 56ms 溢出）✓
- `conn-closed aliveMs=58 binBytes=280517` 与客户端实收 280517 字节**逐字节一致** ✓
- 既有 `subscriber overflow ... 1013` 日志与行为不变；
  `internal/app` / `internal/instance/pty` / `internal/ws` / `internal/ui` 及全量测试通过。
