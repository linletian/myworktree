# TASK-A2：app.go 服务端缺口补齐（仲裁者任务书 v1，2026-10-19）

依据 FIX-PLAN.md v6（本目录，只读）。执行者：编码 Agent。本任务书由仲裁者起草；
执行者发现任务书与代码现实冲突时**停止该点并在回报中提出质疑**（附证据），
不得自行修改任务书或 FIX-PLAN.md。

## 前置状态（已核实的基线）
- 工作树已实现：overflow-notice caps、drop 订阅、notice 先于 chunk（每增量）、
  SetWriteTimeout(15s) ready 后武装（app.go:2562）、legacy 1013 逐字保留。
- 基线测试全绿（go test ./internal/app/ ok）。

## 子任务 G2：wire notice 250ms 区间聚合（决策 1 补 1 / R5）
现状（冲突证据）：app.go:3334-3370 wire notice 每增量即发，注释明写
"the notice is not throttled"——与 FIX-PLAN v4 R5「wire notice 按 250ms
区间聚合」直接冲突，这正是风暴期 ~150 帧/s 争 `writeFrame(c.mu)` 的自激源。

要求：
1. 新常量 `ttyOverflowNoticeInterval = 250 * time.Millisecond`（与
   ttyDropLogInterval=500ms 并存，后者管日志、前者管 wire）。
2. wire notice 语义：**首次 drop 增量立即发**；此后每 ≥250ms 最多一帧，
   帧内计数 = 自上一 notice 以来的 Σ 增量（droppedBytes/droppedChunks
   都是区间和，仍增量语义、仍先于当轮二进制 chunk 写出）。
3. 实现纪律：增量仍每次循环都从 `dropStats.Snapshot()` 采样并推进
   seen 基线；「发不发」只影响 wire，不影响计数推进（Σ 不丢）。
4. 安全性依据（写进注释）：被 drop 恒为队首未交付；reset+tail 响应下
   ≤250ms 决策延迟安全（瞬态洞由 reset+重绘自愈）；输出停止时未发的
   pending 无洞可盖（屏 contiguous）。
5. 测试：
   - 首增量即发（延迟 <100ms 级断言用注入时钟或宽松墙钟）；
   - 风暴期帧率 ≤4/s（灌 >250ms 的多增量，断言 wire notice 帧数
     ≤ ceil(elapsed/250ms)+1，且 Σ 计数与 DropStats 终值一致）；
   - 更新现有 `TestHandleInstanceTTYWS_OverflowNoticeCapNoticesInsteadOfClosing`
     至聚合语义（不得删测试，改断言并在注释留痕）。
6. API.md 同步：把「written immediately ahead of the binary chunk」段改为
   250ms 聚合口径（首帧即发、区间 Σ、≤4 帧/s、先于所涉 chunk）。

## 子任务 G3：窗口化回放——起点 clamp + 总量封顶（决策 4，HIGH-2 修复）
现状（证据）：app.go:2771 `cursor := since` 无 clamp；循环唯一终止兜底
:2814 `streamed >= ttyHandshakeReplayBudget`（8MB）。回放跟随活 head
（ringbuffer.go:149 ReadSince 以当时 head 为界），时长与 backlog 成正比。

要求：
1. `W_eff = min(W_default, caps 声明值)`；W_default 来自 env
   `MYWORKTREE_TTY_REPLAY_WINDOW_BYTES`（默认 1048576；缺失/非法/≤0→默认）；
   caps 声明值来自 G4 的 `replay-window=<bytes>` 前缀解析——**只许缩小**
   （>W_default 的声明被夹到 W_default，防 DoS）。
2. 起点 clamp：catchup 分支 `cursor := since` 之后，若
   `headAtStart − cursor > W_eff` → `cursor = headAtStart − W_eff`，
   **复用 #94 start-sync 既有发布路径**（emitStartSync 公布真实夹取后
   起点，客户端零感知）。
3. 总量封顶：循环预算 guard 处增 `streamed >= W_eff` 终止条件，与
   `streamed >= ttyHandshakeReplayBudget` 并列取先到者；**语义逐字复用
   预算截断**：break 时 endOffset 保持最后已写 chunk 末尾、closing sync
   照发（发布截断点非 head）、「truncated, not skipped」。W ≤ 预算恒成立
   则 budgetExhausted 不再触发（保留其为 8MB 兜底唯一权威）。
4. 埋点：handshake 行增 `windowed=true`（起点夹取或总量截断任一发生）与
   `droppedFromWindow=<bytes>` =（夹取量 clampedStart−since）+（总量
   截断时循环退出瞬间读一次 head 减 endOffset）；现有字段全保留。
5. 测试（新增，且 AC2 对应）：
   - since 在窗口内 → 行为与今日逐字一致（无 clamp，windowed 不发）；
   - since 在窗口外 → start-sync 公布 headAtStart−W_eff；
   - W=0 → 退化纯 tail（catchup 不写任何回放字节，closing sync 照发）；
   - 总量截断：backlog > W_eff → 恰好 W_eff 字节出网、closing sync=截断点、
     下次 since=截断点可续（truncated-not-skipped 的往返测试）；
   - caps replay-window 大于默认 → 夹到默认（只缩不涨）；
   - env 覆盖 W_default 生效。
6. API.md：回放窗口语义（起点+总量双封顶、截断点 closing sync、
   「仍在 ring 内时」限定、线损预算口径、env 名）。

## 子任务 G4：`ttyClientCapValue` 前缀解析（R15）
现状（证据）：全仓 grep `ttyClientCapValue` 无结果；`ttyClientHasCap`
（app.go:3981 区）是全 token 相等匹配，`replay-window=1024` 不匹配。
要求：
1. 新增 `ttyClientCapValue(caps []string, name string) (string, bool)`：
   匹配 `name=v` 前缀形态；单测 pin 四情形：`name=v` 命中；`name=`
   空值忽略（返回 false）；`nameX`（无 = 号）不匹配；重复 caps 取首个
   合法值并容忍非法值继续扫描（裁定口径：首个**可解析**值）。
2. `replay-window` 接入 G3 的 W_eff；非法值（非数字/负）→ 忽略该声明
   （用默认），不拒连。

## 子任务 G5：env 可配（决策 3/5c，R13 冻结名）
1. `MYWORKTREE_TTY_WRITE_TIMEOUT_MS`：ttyWriteTimeout 由 const→启动解析
   （默认 15000；非法/缺失→默认；解析一次，注释声明）。
2. `MYWORKTREE_TTY_REPLAY_WINDOW_BYTES`：见 G3。
3. 心跳放宽档（决策 3(c)/5c，**本任务实现服务端侧**——仲裁修订：
   FIX-PLAN Phase A-3 明列 caps 解析含 `slow-link`（全 token），若只解析
   不应用即成死 cap；客户端侧 stale 窗口 60s 属 Phase B）：
   - 解析 `slow-link` 全 token（复用 ttyClientHasCap）；
   - 声明的连接：服务端 TEXT ping 间隔 10s→**15s**（RFC ping 同步放宽）；
     未声明/legacy 连接 10s/45s **逐字不变**；
   - 常量：ttyHeartbeatIntervalSlowLink=15s（参照 ORCA 15s/75s，计划
     决策 3(c)）；env `MYWORKTREE_TTY_PING_INTERVAL_MS` 暂不做
     （caps 已覆盖需求，少一个面）。
4. 测试：env 覆盖生效 + 非法回退默认 + 未设 env 行为与今日逐字一致 +
   slow-link 连接 ping 放宽/未声明连接逐字不变（pin 两路）。

## 硬约束
- 允许改动：internal/app/app.go、internal/app/tty_ws_test.go、docs/API.md。
  禁止：driver.go（A1 正在改）、ws.go、index.html、FIX-PLAN.md。
- **仲裁修订（评审 A Q1，2026-10-19）**：允许清单扩至 FIX-PLAN v6 §四 Phase A
  点名文件全集——`internal/ws/ws.go`（A-2 SetWriteTimeout 本体）、
  `internal/ws/ws_test.go`（A-4 测试）、`internal/framework/methods.go` +
  `manager_subscribeoutputwith_test.go`（A-3「同门同错」）、
  `internal/app/tty_metrics*.go`（A-3 埋点 windowed/双字段）。边界：上述文件仅可
  做计划点名范围内的改动；**仍禁止** driver.go、index.html、FIX-PLAN.md，
  及 ws.go writeFrame 既有 deadline/锁语义变更。计划与任务书冲突时以 FIX-PLAN v6 为准。
- `TestHandleInstanceTTYWS_SubscriberOverflowClosesConnectionWith1013`
  必须原样通过（legacy 零变化）。
- 完成判据：go build ./... && go vet ./internal/app/ &&
  go test ./internal/app/ -count=1 && go test -race ./internal/app/
  -count=1 全绿。
- 交付：改动摘要、git diff --stat、每条子任务「要求→实现行号→测试名」
  自查表、任何质疑（如有）。

## 仲裁修订记录（评审 B 第一轮发现处置，2026-10-19）
返工单见 TASK-A2-R1-fixes.md。摘要：
- **F1 成立（HIGH）**：G3.5「W=0 纯 tail」须为**构造性保证**——W_eff==0 不进
  回放读循环（比照 sinceFollowLiveEnd 分支语义：发布 head、直接交订阅），
  不得依赖生产者静止。原 `replayWindow > 0 &&` 守卫在 W=0 时关闭封顶判缺陷。
- **F2 成立（HIGH）**：G2.4「输出停止时未发 pending 无洞可盖」前提**驳回**——
  cursor 按已收字节推进，drop 使其落后 ring head，重连必重复绘制（违 #87）。
  新契约：pending 冲刷最迟 = 下一心跳 tick（心跳分支冲刷；10s/15s > 250ms
  闸门恒开）。API.md「screen honest / complete record」两句同步改写。
- **F3 成立（MED）**：AC2「回放量 ≤W」为计划权威，不改措辞；实现裁剪读长
  `min(64KB, W_eff−streamed)` 达成精确（W_eff==budget 时 8MB 对齐，裁剪
  永不改变读长，预算测试零影响）。
- **F5 裁定接受（LOW）**：G5.1「解析一次」正名为「每连接 handshake 一次、
  逐帧不重读」（app.go:2463-2470 注释的偏离接受，运维语义等价且更优）。
- **F4/F8 降级 LOW**：W_eff≤budget 构造下「预算尾部截断」不可达（backlog≥
  budget ⇒ 起点 clamp 必触发 ⇒ windowed 已由 clamp 置位；截断语义由窗口
  测试经共享 closing-sync 路径钉住）。处置 = app.go 与 tty_metrics.go
  注释精确化，不新增测试。
- **F6 成立（测试）**：首帧时间断言零值静默跳过 + 注释语义颠倒，修复。
- **F7 成立（NIT）**：API.md 补埋点口径句（clamp 项与 #94 重基区可重叠）。
