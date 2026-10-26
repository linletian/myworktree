# TASK-A2-R1:评审 B 第一轮返工单(仲裁者工单 v1,2026-10-19)

依据:FIX-PLAN.md v6 + TASK-A2-app-server.md(含仲裁修订记录)。执行者:编码 Agent。
发现均经仲裁者实证核实。行号基线=当前工作树(2026-10-19 22:30+)。

## R1(原 F1,HIGH)W=0 构造性纯 tail
现状:app.go:3200 守卫 `if replayWindow > 0 && streamed >= replayWindow` 在
W_eff==0 时整体关闭总量封顶;起点 clamp(3109-3121)把 cursor 夹到 head 后,
读循环(3255 ReadSince 读当时活 head)在活生产者下照常出字节直至 8MB 预算
(评审实测 262144B 出网)。违反 G3.5「catchup 不写任何回放字节」。
要求:
1. W_eff==0 且 clamp 的 EndOffset 成功 → **不进回放读循环**:`endOffset = head`
   (clamp 已读到的那个 head),跳过整个 catchup 循环,直达既有 closing sync
   (发布 head)与订阅。语义比照 sinceFollowLiveEnd 分支(app.go:3019-3058)
   ——零回放帧、发布 head、交 live 订阅;不新增机制、不改该哨兵路径。
2. clamp 的 EndOffset 失败时保持今日降级(不设 windowed,进循环)——与现
   「failing to apply an upper bound is a degradation」注释一致。
3. windowed 置位维持现状(clamp 半区已置 true)。
4. 测试(新增):脚本化 ring 使「clamp 之后仍有新字节可读」(比照评审探针:
   脚本化连续多次 64KB 读),`caps=sync,replay-window=0` → 断言**零回放
   二进制帧、closing sync=head、帧数=0**;既有 ReplayWindowZeroIsPureTail
   (静止 ring)保持不改并继续通过;补一条对照断言 replay-window=65536 时
   行为不变。

## R2(原 F2,HIGH)心跳分支冲刷 pending notice
现状:采样+写 notice 全在 `case chunk, ok := <-outputChan:`(3632→3787-3820);
输出停止后 pending 永久搁浅(评审实测 22-24 个 drop 从未告知),客户端 cursor
落后 ring head → 重连重复绘制,违反 #87 与 G2.3「Σ 不丢」的可见层面。
要求:
1. 在 `case <-heartbeat.C:`(3597-3622)末尾追加冲刷:dropStats!=nil 且
   (pendingNoticeChunks>0 || pendingNoticeBytes>0) 且 250ms 闸门开
   (心跳 10s/15s 恒开)→ 组装 notice TEXT 帧写出,**先清 pending 再写**
   (纪律与 chunk 分支 3805-3813 逐字同构),写失败走既有 return 路径。
2. 同 goroutine(主 select 循环),不新增锁、不新增定时器。
3. 注释写明:搁浅上界=一个心跳周期;这是「notices are the complete record」
   的闭环保证。
4. docs/API.md:把「the one case a pending count is never written is the case
   where output has stopped … keep its screen honest」句改写为冲刷契约
   (最迟下一心跳;输出完全停止且连接存活期间无 chunk 时,notice 仍将在
   ≤1 心跳周期内送达)。
5. 测试(新增):造成 drop 后**不再发任何 chunk**,等待 ≤ 心跳间隔+余量,
   断言聚合 notice 到达且 Σ 与 DropStats 终值一致。心跳 10s 真实等待可接受
   (既有 heartbeat 测试同款);测试注释说明为何必须真等。
6. 既有测试 tty_ws_test.go:1880-1896 的 keep-alive 注释更新为新契约引用
   (测试本体不改)。

## R3(原 F3,MED)裁剪读长使回放量精确 ≤W_eff
现状:封顶守卫在读前检查,单次读 64KB → 线上可到 W+63KB(实测 W=40000→65536)。
AC2「回放量 ≤W」为权威。
要求:
1. catchup 循环读长处:replayWindow>0 且 remainder=W_eff−streamed<65536 →
   `ReadSince(id, cursor, int(remainder))`;否则逐字不变(64KB)。
2. 构造性注释:W_eff==ttyHandshakeReplayBudget(8MB)时,streamed 恒为 64KB
   整数倍且 budget 为 64KB 整数倍 → 裁剪永不改变读长,预算测试零影响
   (3178-3185 注释同步引用此论证)。
3. 测试(新增):`replay-window=40000` + 大 backlog → 恰好 40000 字节出网、
   closing sync=截断点、下次 since=截断点可续(truncated-not-skipped)。
4. 既有 W=65536 截断测试(3229)与预算测试必须零改动通过。

## R4(原 F6,LOW)首帧即发测试断言修复
tty_ws_test.go:3068-3080:dropAt 零值时整个时间断言被静默跳过,注释却称
零为「STRONGEST」。修复:零值分支改为**补一次轮询取样**或直接 fail-with-hint
(择稳妥者,不得 flake);注释改正为「零=未采到,需补样/失败」。

## R5(原 F4/F8,LOW)注释精确化(不改行为)
app.go:3178-3196 与 tty_metrics.go:73-79:写明——W_eff≤budget 构造成立;
W_eff==budget 且 backlog≥budget 时起点 clamp 必已触发(windowed=true),
预算守卫先命中属设计;「预算尾部截断」在 W≤budget 世界不可达,截断语义
由窗口测试经共享 closing-sync 路径钉住。

## R6(原 F7,NIT)API.md 补埋点口径句
droppedFromWindow 字段说明补:「clamp 项是窗口算术,与 #94 start-sync 对
陈旧 since 的重基区可重叠,非两次独立的丢失」。

## 硬约束
- 允许:internal/app/app.go、internal/app/tty_ws_test.go、internal/app/tty_metrics.go
  (仅注释)、docs/API.md。
- 禁止:driver.go、ws.go、index.html、FIX-PLAN.md、各任务书;不得改 writeFrame
  deadline/锁语义;不得夹带 flate(5a 属 TASK-A3)。
- legacy 零变化:1013 测试与全部既有测试原样通过。
- 完成判据:go build ./... && go vet ./internal/app/ && go test ./internal/app/
  -count=1 && go test -race ./internal/app/ -count=1 && go test ./... -count=1
  全绿;gofmt -l 无输出。
- 交付:改动摘要、git diff --stat、R1~R6「要求→实现行号→测试名」自查表、
  质疑(如有)。参考探针(只读):评审者在 /tmp/rev114/repo 留有复现探针,
  可借用其脚本化手法,但正式测试必须落在仓库内。
