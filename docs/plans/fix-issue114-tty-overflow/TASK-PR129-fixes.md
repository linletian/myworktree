# TASK-PR129-FIXES — PR #129 评审修复工单(P1 守卫移除 / P2 失败路径 / P3 指标口径 / R2-A env 上界)

> 仲裁者签发 2026-10-21。背景:PR #129 两位评审的发现经仲裁者逐条取证**全部成立**,裁定记录见
> `docs/plans/fix-issue114-tty-overflow/PR129-ADJUDICATION.md`,计划修订见 FIX-PLAN v8。
> 基线 = 合并了 origin/develop 的分支工作树(仲裁者已完成合并与全量测试)。
>
> **纪律(硬约束,违者返工)**:
> 1. **禁止任何 git 写操作**(add/commit/push/restore/stash 一律不许)——只改工作区文件,
>    由仲裁者复核后提交。
> 2. 旧路径逐字不变:无 compress cap 的明文连接行为 byte-for-byte 不变;`{"type":"ready"}`
    legacy 回声逐字;#82/#87/#94/#98 契约测试逐字保留。
> 3. UI 可见文案英文-only(#73 `TestUICopyIsEnglishOnly` 禁 CJK)。
> 4. 每完成一项立即报告(写明改了哪些文件、跑了什么测试、结果),不要一口气闷头到底。
> 5. 如对裁定有异议,**停止该子项并回报论据**,不得自行改计划文档。

## 任务 1(P1):移除滑窗膨胀率守卫(方案性重裁定,FIX-PLAN v8 pin5)

裁定依据(勿再辩论,除非有新实证):合法 BestSpeed 流实测 118–209x(64KB 帧),
超过任何可放置的固定阈值;分母在单帧膨胀>窗口时塌缩至 ≤0 误杀健康流。

**移除范围(internal/ui/static/index.html)**:
- 常量 `TTY_INFLATION_WINDOW_BYTES` / `TTY_INFLATION_RATIO_LIMIT` /
  `TTY_INFLATION_SAMPLE_BYTES` 及其注释块(~2670–2715)。
- `startTTYCompressedPipe` 里 pipe 对象的守卫字段:`inflatedTotal`、`wireFedTotal`、
  `wireConsumedTotal`、`inflSamples`、`inflNextMark`、`inflEvals` 及其注释(~6071–6093)。
  **保留** `syncBaseKnown`、`frameLedger`、`framesFed` 及 cursor 机制。
- `pumpTTYDecompressor` 里的守卫块:采样 push、deque 修剪、eval、`wireSpan` 判定与
  "degenerate stream" 分支(~6174–6223 区段,行号以当前工作树为准)。`inflatedTotal`/
  backlog 的**其余**用途仔细甄别——`session.ttyBacklogBytes` 的 2MB backlog 守卫
  (6164–6173 区段)**必须保留逐字**。
- `feedTTYCompressedFrame` 里 `pipe.wireFedTotal += …`(~6288)与
  `pipe.wireConsumedTotal += …`(~6311)两行及其专属注释段(~6262–6277 的 wire 计数
  部分,按留存内容重写注释使其只描述留存机制);**`token.inflating = false` 与
  `session.ttyBacklogBytes -= u8.length`(6312–6313)必须保留**。

**测试同步(internal/ui/testdata/tty_overflow_reconnect.test.mjs 及其它)**:
- 移除/改写比率守卫用例:`makeRatioLegitChunks` 配方与 67–70 行 CAVEAT、
  32→8 mutant 判别用例、"high-compression bomb"/"single-frame bomb" 跳闸用例、
  任何断言 `inflEvals` / `degenerate` / "stream reset: degenerate stream" 的用例。
- 全仓 grep 清扫:`TTY_INFLATION`、`inflEvals`、`degenerate`、
  `wireFedTotal`、`wireConsumedTotal`(含 internal/ui 的 Go 侧 anchor 测试,
  如 terminal_status_test.go / ui_test.go 里对 index.html 的文本锚)。
- 保留并不得削弱:backlog 双跳闸点、generation fencing、mid-replay cursor resume、
  teardown-under-load 无未捕获 rejection 等既有用例。

**文档同步**:
- CHANGELOG.md 的 #114 条目(当前 Unreleased 首条):把 "two consumption guards…
  a sliding-window inflation-ratio guard (trailing 2 MB window, >32:1 trips, fail-closed
  on a zero denominator whose window grew, \"stream reset: degenerate stream, data
  dropped\")" 改写为现存真实机制(2MB backlog 守卫 + 解压/读写失败路径的可见状态+
  reset+reconnect);条目尾部的 client suite 描述("the ratio guard tripping on a
  high-compression bomb and a single-frame bomb…healthy burst…~13.2:1")同步改写为
  套件新内容。条目其余部分逐字不动。注意该文件有计数守卫测试,改完必跑。
- docs/API.md:如有比率守卫/"degenerate stream" 记述,同步移除/改写。
- FIX-PLAN.md / PR129-ADJUDICATION.md **不许动**(仲裁者领地)。

## 任务 2(P2):压缩管道失败路径不再静默/不再回退明文

1. `startTTYCompressedPipe` 构造 catch 分支(~6098–6103):与 6050–6053 的
   无-DecompressionStream 分支**同构**——console.error + 可见状态行 +
   `resetTTYStreamAndReconnect(session, …)`;**绝不**回到明文消费。6043–6047 的
   "Returns false (staying on the plaintext consumption path)…unreachable in practice"
   注释按新契约重写。ready 调用点(~7078–7080)维持简单调用即可(失败已自杀式重连)。
2. `pumpTTYDecompressor` 的 `reader.read()` catch(~6155–6158):先判
   `session.ttySocket === ws && session.ttyGeneration === pipe.gen`——属当前连接:
   可见状态行 + `resetTTYStreamAndReconnect`(理由含 inflate 错误语义);否则(teardown
   abort)静默 return。
3. `feedTTYCompressedFrame` 的 `writer.write` catch(~6301–6303):同上 generation/socket
   判定分流。注意:裸 return 前 `ttyBacklogBytes` 未减是现状,重连路径下 session 即毁,
   不要求补账,但注释里把这一点说清。
4. 可见状态行文案:与既有 "stream reset: …" 家族同构,英文-only,建议
   `"stream reset: stream error, data dropped"`(如你另选措辞,说明理由并保持家族一致)。
5. node 用例新增:构造抛错→已 reset 且未走明文;pump read reject(当前 gen)→ reset;
   feed write reject(当前 gen)→ reset;teardown 代 reject → 静默无 reset、无未捕获
   rejection。用例驱动出货代码切片,遵循套件既有 harness 模式。

## 任务 3(P3):overflow 行 liveFrames 扣除 replay 帧

- `internal/app/tty_metrics.go`:`ttyConnMetrics` 增 `replayFrames int64` 字段
  (挨 `replayBytes`,注释同族:"binFrames snapshot at handshake end")。
- `internal/app/app.go:3975–3976`:快照点加 `conn.m.replayFrames = conn.m.binFrames`。
- `internal/app/tty_metrics_on.go:120`:overflow 行 `liveFrames` 改
  `m.binFrames - m.replayFrames`;97–110 注释按需同步(liveBytes/liveFrames 同族口径)。
  **列布局冻结契约不变**:不新增列、不换序,只改既有列的取值口径。
- 测试:`TestTTYMetricsOverflowLine`(ttymetrics tag)扩/新增用例——握手 replay N 帧 +
  live M 帧后溢出,liveFrames==M;明文与压缩两臂口径一致。
- `docs/plans/fix-issue114-tty-overflow/DATA-COLLECTION.md` 定义本来就是 live-only,
  核对 :31 行措辞与实现一致即可,不一致则改实现不改定义。

## 任务 4(R2-A):MYWORKTREE_TTY_DROP_QUEUE_CHUNKS 上界

- `internal/instance/pty/driver.go` `subscriberQueueChunksDrop()`(471–481):
  增 `maxSubscriberQueueChunksDrop = 1 << 20` 常量;`n > maxSubscriberQueueChunksDrop`
  → 回落 `defaultSubscriberQueueChunksDrop`。464–470 注释改写:大但可分配的值仍由
  operator 自负 RSS;**越上界的值回落默认**(原"A huge value is NOT clamped"句废止——
  实测 maxInt 级值在 `makechan` 处 panic: size out of range)。
- 测试(driver_test.go):env=9223372036854775807 → 默认(不 panic);env=1<<20 → 采纳;
  env=(1<<20)+1 → 默认;既有 0/负数/垃圾串 → 默认用例保持绿。

## 验收(全部自己跑绿再报告)

```bash
go build ./... && go vet ./...
go test ./...                                    # 含 internal/ui 的 node 套件
go test -tags ttymetrics ./internal/app ./internal/ws
go test -race -count=1 ./internal/app ./internal/instance/pty ./internal/ws ./internal/framework
gofmt -l .                                       # 无输出
```

报告格式:每任务一节——改动文件清单、关键设计决定、测试输出尾部、遗留风险。
