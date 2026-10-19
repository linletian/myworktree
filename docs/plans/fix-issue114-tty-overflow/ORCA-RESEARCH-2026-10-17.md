# ORCA 调研：终端流式传输/慢链路适配的可借鉴优化（2026-10-17）

调研对象：github.com/stablyai/orca（Stably AI，agent IDE；终端为 xterm.js；
移动伴侣经 Orca Relay 云中继连接桌面）。与 #114 的相关面：**远程/跨境访问下的
终端流传输、断连重连、背压、保序**。方法：仓库 README + cloud/relay-contract
契约源码（admission-budgets / splice-state-machine / control-continuity /
resume-confirmation / protocol-limits）+ docs/terminal、docs/mobile 官方文档。

## 一、splice 水印背压（relay-contract/src/admission-budgets.ts）— 最相关

```
spliceLowWaterBytes:  64KB    # 恢复向该 peer 转发的水位
spliceHighWaterBytes: 256KB   # 暂停向该 peer 转发的水位
spliceHardQueuedBytes: 8MB+256KB  # 硬上限，超限关 splice (LIMIT_EXCEEDED)
spliceWedgedTimeoutMs: 10s     # splice 卡死（无进展）超时即拆
```

注释自证两条纪律：(a) **必须为背压 peer 至少放行一帧 maxFrameBytes**，
否则大响应会把 splice 关掉；(b) 高/低水印之间是**回滞**——不是"满即杀"。

**对照我方**：64-chunk 满即 1013 是同一问题的粗糙版；v3 决策 1 的
drop+notice 已对齐"不杀连接"方向，但可再借两点：
- **卡死超时作为兜底语义**：ORCA 用独立的 wedged timeout（10s）区分
  "背压"与"卡死"；我方的 write deadline 15s 是同一角色（评审 R2 要求它
  区分"慢"与"死"，落 AC3 误杀指标）。
- **"至少放行一帧"纪律**：我方 notice 必须先于 chunk 发出、且背压时
  notice 仍要出得去——与 (a) 同构（已实现，评审认可）。

## 二、resume 确认契约（resume-confirmation-contract.ts）— 采纳

恢复不是隐式的：`ConfirmableResumeTuple = { basisConnId, owningControlGeneration,
confirmDeadline, acceptedAs: current|grace }`，提交结果五态
（renew-current / return-unchanged-grace / reject-retired / reject-expired /
revoked）。

**借鉴 → 决策 2 稳定锚点（MED-1 修复）**：退避重置锚点从"closing sync 到达"
升级为"closing sync 到达 **且连接稳定存活 ≥60s**"——恢复需被确认（存活
一段时间）才算数，与 ORCA 的 confirmDeadline 同构。ORCA 的 grace/retired
拒绝态则提示：重连携带的 cursor 也应可被服务端判废（我方 ring 淘汰 +
W 截断语义已覆盖"判废"侧，客户端按 since 重取即可）。

## 三、control generation + fencing（control-continuity.ts）— 采纳

控制连接丢失时的处置：`controlLossDisposition(匹配 resume secret?, 竞争
generation?) → rebind | fence-old | orphan-grace`；`orphanGraceMs: 30s`。
两条纪律：(a) **generation 单调递增，竞争 generation 出现时旧代被 fence**
（迟到的旧连接帧不得生效）；(b) 孤儿控制有 30s 宽限再拆。

**借鉴 → 评审 R6 的 epoch 方案**：Phase B 的 `ttyEpoch` 不止"异步回调先对
epoch"，应按 ORCA 升级为 **generation + fencing 语义**：每连接一个单调
generation；notice/reset 后 bump，旧 generation 的迟到帧（WS 缓冲残余、
解压管道在途输出、xterm 回调）**一律 fence 丢弃**（不是"尽力检查"）；
旧 generation 迟到的 notice/控制帧同样 fence。30s 宽限不需要（我方无
跨进程控制面），但 fencing 的严格性要写进实现纪律。

## 四、splice 状态机"无假就绪"（splice-state-machine.ts）— 采纳为纪律

状态机 PRE_AUTH→…→HOST_ATTACHED→CLIENT_ACKNOWLEDGED→SPLICED；
`mayAcknowledgeClient` 注释自证：**两个转发 handler 都装好之前不得 ack
客户端，否则把客户端留在假 splice 上**。

**借鉴**：与我方"ready 之后才武装 write deadline / notice 必须先于 chunk"
同族。写进 Phase B 纪律：任何"就绪"信号（ready 帧、closing sync）只能在
管线真正就绪后发出；解压管道未挂好前不得声明 compress 生效。

## 五、移动端 read-mostly 快照水合（docs/mobile）— 方向互证 + UX 借鉴

- "Hydrate **recent** terminal scrollback… After a terminal stream ends,
  the view **accepts a fresh snapshot instead of staying stale**"——
  慢客户端是**有界近期视图的只读消费者**，不是全量历史流消费者。
- 移动端有 **Live 模式开关**（逐字符直送 vs 快照）——慢链路的保真度取舍
  **交给用户显式选**，而非自动启发式。
- "a machine that dropped is visible at a glance"——**连接降级状态在 UI
  可见**，不让用户把降级流误当完整流。

**借鉴**：
1. 我方 W 窗口 + tail 重绘的方向与 ORCA 快照水合同构（决策 4/5b 的
  外部互证，可写入 API.md 语义）。
2. **MED-3 取舍落地为用户可见项**：notice 触发 reset 时，终端 UI 应显示
   一次"流已重置（链路慢，数据有丢弃）"的可见提示（一行状态 + console），
   对应 ORCA 的 host-health 可见性。
3. Live/快照模式开关 = MED-3 备选（cursor+droppedBytes 保 scrollback vs
   reset+tail 跳 live）的未来用户可选形态；本期维持 reset+tail 默认，
   取舍理由记录在案（见 FIX-PLAN 决策 1 仲裁记录）。

## 六、长 haul 心跳比（protocol-limits.ts）— 调参参考

relay 控制通道：`controlPingIntervalMs: 15s / controlSilenceTimeoutMs: 75s`
（5:1），且 `firstFrameDeadlineMs: 2s`、`maxFrameBytes: 8MB`（注释：宁可
大框架也不让重连洪流杀死会话，"pagination is the long-term fix"）。

**借鉴**：
- 我方 10s/30s（3:1）对 300ms RTT + 丢包的跨境链路偏紧：停顿接近但未
  达 30s 的可恢复链路会被 liveness 误收割。**决策 5c 增补**：慢链路
  （caps 声明或 env）可放宽至 15s/60s；默认维持 10s/30s 不变
  （LAN 语义不动）。firstFrameDeadline 2s 与我方握手定时器同角色，
  无需新增。
- maxFrameBytes 8MB 的注释与我方 8MB 预算争论同构：ORCA 选择"大框架
  兜底 + 长期分页"，我方选择"W 窗口封顶 + 预算退化为兜底"——方向一致，
  W 即我方的"分页"前身。

## 七、版本协议 + 能力门控（docs/mobile 排障）— 对照确认

"phone and desktop speak a versioned mobile protocol… if either side is
too old, mobile blocks that host"；旧桌面不 advertise quick-commands 时
启动器禁用——**按能力粒度协商，旧端优雅降级**。

**对照**：我方 caps（#98）同族；评审 R16（新客户端→旧服务端仍可连、
走 legacy）正是该模式的我方变体，已列入 v4 协议契约回归。

## 八、幂等日志化动作（docs/mobile）— 输入侧未来注意

"the phone journals the attempt so a retry after a flaky connection does
not double-spend"——**弱网下的输入动作必须幂等**。本期 #114 只治输出侧；
若后续做跨境命令提交/快捷键重发，需引入 attempt journal（去重键），
列为未来项，不进本期范围。

## 九、借鉴落点汇总

| ORCA 模式 | 落点（FIX-PLAN v4） |
| --- | --- |
| 水印回滞 + wedged timeout + 至少放行一帧 | 决策 5c（write deadline=wedged 角色，AC3 误杀指标）；notice 先于 chunk 纪律（已实现） |
| resume 确认（confirmDeadline/grace） | 决策 2 稳定锚点（closing sync + ≥60s 存活） |
| generation + fencing | Phase B epoch → generation+fencing 实现纪律（R6） |
| 无假就绪（handler 装好才 ack） | Phase B 纪律：就绪信号晚于管线就绪 |
| read-mostly 快照 / 可见降级 / Live 开关 | 决策 4/5b 方向互证；notice 时 UI 可见提示（MED-3）；用户可选保真度为未来形态 |
| 15s/75s 心跳比 | 决策 5c 慢链路放宽档（caps/env，默认不变） |
| 版本/能力门控 | 对照 caps（#98）；R16 回归 |
| 幂等 journal | 未来项（输入侧），不进本期 |
