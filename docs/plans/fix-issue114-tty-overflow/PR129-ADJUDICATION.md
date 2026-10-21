# PR #129 评审意见仲裁裁定书（2026-10-21)

仲裁者对 PR #129 两条评审评论（R1 = opencode / deepseek-v4.1-flash-free,2026-10-21T10:45Z;R2 = Mavis / MiniMax-M3.1-Flash,2026-10-21T11:29Z)逐条取证后的裁定记录。取证全部在本仓库工作区 + /tmp 临时区完成，可复现。

## 裁定总览

| # | 议题 | 裁定 | 性质 |
|---|---|---|---|
| P0 | 与 develop 冲突（6 文件）+ 无 CI check + 前两提交与 #119 重复 | **接受** | 流程修复(合并 develop) |
| P1 | 32:1 膨胀率阈值与分母口径 | **接受，且比评审所述更重**;修复方向=**移除比率守卫**(重设计，v8 修订) | 代码重设计 |
| P2 | 压缩管道构造/读/写失败静默失败或退回明文 | **接受**(R2-B 与 R1-P2-1 同一缺陷，合并) | 代码修复 |
| P3 | overflow 指标 `liveFrames` 混入 replay 帧 | **接受**(仅诊断面) | 代码修复 |
| R2-A | `MYWORKTREE_TTY_DROP_QUEUE_CHUNKS` 无上界 → 订阅 panic | **接受**(已复现 panic) | 代码修复 |

---

## P0 — 接受

取证:

- `gh pr view 129`:`mergeable=CONFLICTING`、`mergeStateStatus=DIRTY`、`statusCheckRollup` 0 项 → **属实**。
- `git merge-tree --write-tree origin/develop HEAD` 复现:6 个冲突文件，与 R1 清单逐字一致(CHANGELOG.md / DATA-COLLECTION.md / app.go / tty_metrics.go / tty_metrics_on.go / index.html)→ **属实**。
- 分支前两提交 `55f5518`/`ca875d1` 经 `gh pr view 119` 证实已折入 develop(merge commit `781f30f`,标题即含 "#114 tty-metrics diagnostics"),且非 develop 祖先 → **重复叠加风险属实**。
- CI:`go-ci.yml` 触发器覆盖 `pull_request`→develop;但 `gh run list --branch fix/fix-issue114` 与 2026-10-21 当日全仓 run 均为 0。分支对 `.github/` 的双点 diff 为空(#119 已带入同内容),排除工作流文件损坏。未触发原因未定(不排除 GitHub 侧事件丢失);**可执行解 = 推送 synchronize 后观察 CI 是否开跑并转绿**。PR body「CI 全绿」复选框本就未勾选，无虚报。
- R2 的解冲突配方(`merge -X ours` + 仅 CHANGELOG 手工插入;tty_metrics*/DATA-COLLECTION 取 PR 侧;`checkout --ours` 会碎 5 个 develop 测试)将在执行时由仲裁者独立复核探针(iosIme / startWTRename / fetchMonitorAlerts 出现次数、`TestResponsiveLayerChangelogCounts` 等),不直接背书。

## P1 — 接受，且裁定重设计(移除守卫)

两条机制均证实:

**机制一(阈值被合法流击穿)**:

- 守卫常量 `internal/ui/static/index.html:2705-2706`(窗口 2,000,000 B、阈值 32)、触发式 `:6213`(`wireSpan <= 0 || 2000000 > 32*wireSpan`)→ 窗口 wire 成本 < 62,500 B 即触发。
- 本 PR 自家验收 `ACCEPTANCE-2026-10-20.md:21,62` 三轮实测 ratio 41.89/41.98/42.17x > 32 → 2 MB 输出对应 wire ≈ 47.7 KB < 62.5 KB → **必触发**。AC3a 的 PASS 与客户端守卫无关(Go harness 只做 inflate+计数;node 用例把比率固定在 ~13.2:1,`tty_overflow_reconnect.test.mjs:936-943`)。
- 测试文件自己的 CAVEAT(`:67-70`)承认:同一份内容换 DefaultCompression 即 58.3:1、会踩 32。

**机制二(分母口径在帧粒度下塌缩)**:

- 代码实证:`wireFedTotal` 在帧进入时累加(`:6288`),`wireConsumedTotal` 在 `writer.write` **resolve** 时累加(`:6311`,即 transform 接收即算"已消费"),采样在窗口起点(`:6195`),判定 `wireSpan = wireFedTotal(now) − ref.wireConsumed`(`:6209`)。
- 推演:64 KB wire 帧在 >31.25x 比率下单帧膨胀即超过 2 MB 窗口;回放帧恰为 64 KB(AC3a 实测 41.9x → 单帧 ≈ 2.7 MB)。paint 期间无新喂帧时 `wireFed` 不动、采样点 `wireConsumed` 已含该帧 → `wireSpan ≤ 0` → 健康流被判 `degenerate deflate stream` 误杀。代码注释自述的"低估至 ~0.75x"以"帧 ≪ 窗口"为前提,该前提在 >31.25x 时结构性地不成立。

**仲裁者补充测量(决定性)**:用库内 a0compress 工具(level=BestSpeed、持久流、逐帧 Flush,与出货口径一致)测三种**合法**负载:

| 负载 | 1 KB 帧(scenario A) | 64 KB 帧(scenario B) |
|---|---|---|
| `yes` 风格("y\n" × 4M) | 42.7x | **209.4x** |
| 空格填充(79+LF 行) | 31.6x | **146.2x** |
| AC3a 式短语重复 | 25.4x | **118.5x** |

合法终端输出在生产 BestSpeed 下实测可达 **~209x**,且全同字节长行程逼近 deflate 理论上限(~1032:1)。**不存在任何能把合法重复输出与"退化流"分开的固定比率阈值**:定在 42x 以上误杀 `yes`,定在理论上限以上形同虚设。

**裁定:移除滑窗比率守卫整体**(常量、采样队列、wireFed/wireConsumed 计数、判定分支;frameLedger/backlog 等无关机制保留)。真实防护由三件既有/本次补强的事物承担:① inflate/读写错误 → 可见状态 + reset+reconnect(P2 修复后真正生效,损坏/失同步的 deflate 必抛错);② 2 MB backlog 上限(内存界);③ awaited `term.write`(解析背压)。合法高膨胀输出被画到终端是正确行为(`yes` 天经地义)。此裁定为 FIX-PLAN v8 修订(见该文件 v8 节),实现与测试更新走编码子代理 + 双评审。

## P2 — 接受(R1-P2 与 R2-B 合并)

代码实证:

- 构造失败分支 `:6098-6103` 仅 `console.error` + 置空 + `return false`;ready 调用点 `:7079` 丢弃返回值 → 服务端按 `compress:true` 发 deflate 帧,客户端 `:7273` 走明文分支把 deflate 字节当终端文本画出。对照 `:6050-6053` 无 DecompressionStream 分支是 reset+reconnect —— 两个"能力不可用"分支行为不一致,**属实**。
- pump `reader.read()` 异常 `:6155-6158`、feed `writer.write` 异常 `:6301-6303` 均裸 `return`:真实 deflate 损坏/流失同步时终端静默冻结而 socket 存活(backlog 2 MB 兜底要以错误理由迟发,空闲终端则永远冻结),**属实**。
- 探针 `:6914-6924` 确实在 connect 时构造 `new DecompressionStream("deflate-raw")`,故 ready 时构造失败窗口窄(资源耗尽等),但存在;而 read/write 失败是损坏流的**常规**路径,不窄。

修复方向(与评审建议一致):构造失败 → 与 `:6052` 同构 reset+reconnect,**绝不回退明文**;read/write 异常 → 先按 generation/socket 判定,属当前连接 → 可见状态 + reset+reconnect,属 teardown 中止 → 静默放行。

## P3 — 接受(仅诊断面)

- `tty_metrics_on.go:120`:`liveBytes = binBytes − replayBytes`(扣了 replay),`liveFrames = m.binFrames`(**未扣**);`binFrames` 在 `tty_metrics.go:170` 对 replay 与 live 写出同路自增;无 `replayFrames` 快照字段。
- 注释(`:99-100`)与 `DATA-COLLECTION.md:31` 均把 `liveFrames` 定义为"订阅后交付的 live 输出" → 实现与文档口径矛盾,**属实**;`liveBytes/liveFrames` 帧长估计被 replay 拉偏。仅影响诊断(ttymetrics tag 门禁),不影响协议。
- 修复方向:handshake 结束时快照 `replayFrames`,overflow 行用 `binFrames − replayFrames`;同步注释/DATA-COLLECTION。列布局冻结契约(追加不插位)不变。

## R2-A — 接受

- `driver.go:471-481` 无 upper clamp(注释 467-470 明确"不钳制,大值由 operator 自负 RSS");`:943` `make(chan string, capacity)`。
- 仲裁者本机复现:`make(chan string, 9223372036854775807)` → `panic: makechan: size out of range`(Go 运行时常数检查)。panic 于 WS handler goroutine,net/http 每连接 recover 兜住 → daemon 存活、该连接被杀、日志留栈。**属实**。
- 修复方向:加上界(≤ 1<<20),越界回落 `defaultSubscriberQueueChunksDrop`,与 `ttyWriteTimeout()`/`ttyReplayWindowDefaultBytes()` 的"无效 → 默认"一族行为一致;注释同步(467-470 段改写:大但可分配的值自负 RSS,越界值回落默认)。

## 评审意见中未被采纳的表述(记录在案)

- R2 称两个兄弟 env(`ttyWriteTimeout`/`ttyReplayWindowDefaultBytes`)"都是越界回落默认值" —— 措辞不精确:二者仅有"无效(≤0/不可解析)→ 默认",无上界逻辑(`ttyReplayWindowDefaultBytes` 的超预算值是在消费点 clamp 的)。不影响 R2-A 成立与修复方向。
- R2 对机制二的语句「窗口起点的 wireConsumed 可能已经追平 wireFedTotal」方向表述含混(准确机制是 consumed 领先 paint + feed 停摆),但其结论(wireSpan→0 误杀健康流)经仲裁者独立推演成立。

## 执行序

1. P0:仲裁者执行合并(git 操作,非编码),探针复核 + 全量测试,提交后双评审验证合并保真。
2. P1(移除守卫)/P2/P3/R2-A:编码子代理按工单实施 → 双评审 APPROVE → 提交。
3. 推送 → 盯 CI → PR 评论逐条回复(引用本裁定书与修复提交)。
