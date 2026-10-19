# TASK-C:Phase C 收尾(仲裁者任务书 v1,2026-10-19)

依据 FIX-PLAN.md v6 §四 Phase C。执行者:编码 Agent(文档撰写同走编码通道)。
**FIX-PLAN.md 的 §四.0 状态表回填由仲裁者本人完成,执行者不得触碰 FIX-PLAN.md。**

## 前置
- Phase A(A1 driver 1b、A2 窗口/notice/env/slow-link、A3 压缩)与 Phase B(客户端)
  已分别双评审通过并提交;行号以提交后 HEAD 为准。
- docs/API.md 已在 A2/A3 增量同步,本任务查漏定稿。

## 子任务 D1:CHANGELOG.md
1. `## Unreleased` 段**顶部**新增一条 issue #114 fix 条目,遵本文件既有风格
   (一段到底、机制→决策→边界→测试名清单),覆盖:
   - 问题一句话(overflow→1013→回放→再 overflow 风暴,引 2026-10-17 证据数字:
     52 次 overflow / replayMs 84,656 / drain p25 6.4KB/s,口径照 FIX-PLAN §一);
   - 服务端:drop-oldest+notice(250ms 聚合)、队列 4096、窗口化回放
     (起点 clamp+总量封顶)、write deadline 15s env、slow-link 心跳 15s、
     app-layer deflate-raw 压缩(A0 门 5.719x);
   - 客户端:overflow 白名单+reset+tail+可见提示、generation+fencing、
     指数退避(前 8s/后 30s、60s closing-sync 锚点)、B-5 backlog 2MB、
     B-9 单帧 4MB 界、compress 消费与降级;
   - 兼容性:legacy/未声明 caps 零变化;#82/#87/#94/#98 全绿;
   - env 名三个 + 测试名清单(全部真实存在的测试,逐个核对后写入)。
2. **数字纪律**:条目内所有数字必须能在 FIX-PLAN v6/A0-GATE/测试中找到出处,
   不得新造;引用测试名必须 `grep` 验证存在。
3. 注意 #95 计数守卫:不得使用「Pinned by N `node --test` cases」句式
   (该句式绑定 terminal_status.test.mjs 总数),用独立措辞。

## 子任务 D2:docs/API.md 查漏定稿
对照 FIX-PLAN §四 Phase C 清单逐项核对,缺哪补哪(多数已在 A2/A3 落地):
回放窗口语义(起点+总量封顶、截断点 closing sync、「仍在 ring 内」限定、
线损预算口径)、overflow 帧+250ms 聚合、caps 四组合矩阵、notice 清空
scrollback UX 语义、env 名三个、slow-link 档、compress wire 语义
(deflate-raw、ready 回声、双字段口径、cursor=逻辑字节)。

## 子任务 D3:DATA-COLLECTION.md 补记
1. 客户端埋点清单段补「Phase B 已落地」状态与实际字段名(以 index.html 实现为准)。
2. 补一行:压缩上线后用 wireBytes/logicalBytes 双字段回验 A0 的 5.719x
   与帧尺寸分布(A0-GATE §六局限 3 的闭环)。

## 硬约束
- 允许:CHANGELOG.md、docs/API.md、docs/plans/fix-issue114-tty-overflow/DATA-COLLECTION.md。
- 禁止:一切代码、FIX-PLAN.md、各任务书。
- 完成判据:`go test ./... -count=1` 全绿(文档改动不应破坏任何守卫测试,
  特别是 #95 计数守卫与 API.md 锚点类测试);`git diff --stat` 只含上述三文件。
- 交付:改动摘要 + 数字出处对照表(条目内每个数字→出处文件:行)。
