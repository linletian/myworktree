# TASK-B:客户端 Phase B(index.html)(仲裁者任务书 v1,2026-10-19)

依据 FIX-PLAN.md v6(本目录,只读)§二决策 1/2/5、§三协议契约、§四 Phase B。
执行者:编码 Agent。发现任务书与代码现实冲突时**停止该点并在回报中质疑**(附证据),
不得自行修改任务书或 FIX-PLAN.md。

## 前置基线(已核实,2026-10-19)
- 服务端 Phase A 已落地:overflow-notice(250ms 聚合)、replay-window(W_eff 双封顶)、
  slow-link、write deadline;双评审进行中。
- 客户端锚点(实际行号,已 grep 核实):scrollback `internal/ui/static/index.html:5453`;
  caps 串 `:6383`;控制帧白名单 `:6038`;固定重连延迟两处 `:6269`(reconnectStaleTTY)
  与 `:6787`(ws.onclose),常量 `TTY_RECONNECT_DELAY_MS=5000` @:2654;
  liveness tick `TTY_LIVENESS_CHECK_MS=5000` @:2646。
- 服务端 5a(compress)尚未实现:本任务**声明 compress cap 必须走优雅降级**
  (服务端忽略未知 caps → 明文字节照旧,§三 R16);解压消费管道按「服务端可能
  压/可能不压」两态设计,由 handshake ready 帧的 `compress=true` 字段分流
  (若服务端未回声该字段则按明文消费——见 B1.4)。

## 子任务 B1:caps 声明与压缩消费管道
1. `:6383` caps 串追加 `overflow-notice,compress`;`DecompressionStream` 构造
   `'deflate-raw'` 不可用(旧浏览器)时**不声明 compress**(try/catch 特性探测,
   console 注记),其余 caps 照常。
2. **仲裁定值**:`replay-window` 与 `slow-link` 本期**不声明**(保持默认 1MB/10s;
   声明它们是运维旋钮,客户端无场景数据支撑,少一个面)。
3. 二进制帧消费分两态:
   - 明文态(无 compress):今日路径逐字不变;
   - 压缩态:每 WS binary 帧喂入**每连接持久** `DecompressionStream('deflate-raw')`
     (单 reader 顺序读),输出按序 `term.write(data, cb)` 且 **await cb**(pin 3,
     保序+解析背压);cursor 按**解压后**字节推进。
4. 态判定:ready 帧 `compress===true` → 压缩态;缺省/false → 明文态。服务端
   (TASK-A3)保证 ready 回声该字段;**A3 未合入前声明 compress 的客户端收到
   明文且 ready 无该字段 → 走明文路径,连接照常(R16 降级)**——此为本任务
   可先行合入的安全依据,测试需 pin 该降级。
5. **B-9 解压炸弹守卫(v7 重裁定,评审 B round2 R1)**:~~单帧解压后字节
   maxDecompressedBytes~~ → **滑窗膨胀率守卫**:trailing 输出窗口 ≥2MB 且
   窗口输出/窗口 wire > 32:1 → 关连接 + **可见提示**(与 notice 同构,不
   得静默清屏) + tail 重连。只用单调累计量(泵滞后免疫;分母为 feed 领先
   与 write()-先-resolve 两个反向领先的净值,方向随形态翻转:高估 ≤1.61x
   /低估至 0.75x,安全相关侧为高估侧;分母 ==0 且输出有增长=触发)。依据:共享 deflate 上下文下帧界不可可靠识别
   (评审探针实测);A0 合法最大 13.056x,炸弹域 ≥64:1,32:1 居中。
   **不做连接生命周期累计**(v5 N3 不变)。与 B-5 分工:本界防炸弹,
   B-5 界防积压。
6. **B-5 客户端设界**:待解压+待写字节总量上界 `ttyClientBacklogMax = 2MB`
   (仲裁定值,区间 1–2MB 取上沿);超限 → `term.reset()` + 弃连 + tail 重连
   (计入退避);计数器随交付递减、随入帧递增,单连接作用域。

## 子任务 B2:overflow notice 处理(generation+fencing)
1. 白名单 `:6038` 增 `"overflow"`;新增 `handleTTYOverflow(session, msg)`:
   console 日志(含 droppedBytes/droppedChunks)→ 可见降级提示(状态行
   「流已重置:链路慢,有数据丢弃」,复用现有 updateStatus 通道)→
   `term.reset()` → **主动关闭 socket** → 无 `since` 的 tail 重连(计入退避序列)。
2. **generation+fencing**:`session.ttyGeneration` 单调递增;每次新建连接、
   notice、reset 均 bump;解压管道每一输出、`term.write` 回调、迟到控制帧、
   onmessage 派发**先比对 generation,不符一律丢弃**(三层残余:WS 缓冲、
   解压管道在途、xterm 回调)。现有 socket-identity guard 保留,generation
   叠加其上。
3. notice 处理必须同步置 fence(先 bump 后关),保证 bump 之后任何在途
   输出不得上屏。

## 子任务 B3:指数退避(决策 2)
1. `session.ttyReconnectAttempts`(session 级);延迟 =
   `Math.min(1000 * 2**attempts, cap) * (0.75 + Math.random() * 0.5)`;
   cap:后台(document.hidden)**30000**,前台可见 **8000**(决策 2 R10);
   attempts 自增后再排程,首次重连 attempts=0(≈1s×jitter)。
2. **重置锚点(唯一)**:closing sync 到达时记录 `closingSyncAtTs`;
   **自 closing sync 起存活 ≥60s**(60_000ms 后连接仍为该 session 活动连接)
   → `ttyReconnectAttempts=0`。实现自选(setTimeout 复查或 close 时回看),
   但必须满足:**回放中段断连、notice→tail 重连(未到 60s)均不清零**。
3. 替换 `:6269` 与 `:6787` 两处固定 `TTY_RECONNECT_DELAY_MS`;全仓复核
   重连调度点仅此两处消费该常量(liveness tick 不直接排连——复核后在回报
   中列证据);`TTY_RECONNECT_DELAY_MS` 常量删除或改造为退避基数 1000。
4. jitter 的 `Math.random()` 写法必须过 `internal/ui/testdata/*.test.mjs`
   切片规则;新增/更新 node test 断言退避序列、cap、60s 锚点(可注入时钟)。

## 子任务 B4:测试与守卫
1. 新增 `internal/ui/testdata/tty_overflow_reconnect.test.mjs`(遵现有切片+断言
   风格:`inlineScript`/`sliceBlock`/`sliceBlockQuoted` 从真实 index.html 切函数,
   stub 浏览器态跑行为,不复制实现):退避增长、前后台 cap、60s 锚点、
   notice→reset+tail、fencing 迟到帧丢弃、compress 降级(ready 无 compress
   字段→明文)、backlog 超限弃连、maxDecompressedBytes 超限关连。
2. **Go 接线(必做,照抄 terminal_status_test.go 模式)**:在 internal/ui/ 新增
   (或并入现有)`*_test.go` wrapper,`exec.LookPath("node")` 缺失时 Skip,
   否则 `node --test testdata/tty_overflow_reconnect.test.mjs`;CI
   (.github/workflows/go-ci.yml:61 `node --test 'internal/ui/testdata/*.test.mjs'`)
   自动覆盖新文件。
3. `node --test internal/ui/testdata/*.test.mjs` 全绿;既有 UI 测试零回归。

## 硬约束
- 允许改动:`internal/ui/static/index.html`、`internal/ui/testdata/*.test.mjs`
  (新增/更新)、`docs/API.md`(客户端语义段)。
- 禁止:一切 Go 产品代码、FIX-PLAN.md、任务书本身。
- 明文 legacy 路径零变化:未收到 ready.compress 时,字节消费、cursor 推进、
  重连语义与今日逐字一致(仅重连延迟改退避——这是决策 2 要求,非回归)。
- 完成判据:`node --test internal/ui/testdata/*.test.mjs` 全绿 + `go build ./...`
  (index.html 经 embed 编译) + 既有 Go 测试不受影响 + `bash -n` 不适用则跳过。
- 交付:改动摘要、git diff --stat、每条子任务「要求→实现行号→测试名」自查表、
  质疑(如有)。

## 仲裁预裁定记录(编码前生效)
- 任务书 vs 计划冲突时以 FIX-PLAN v6 为准(评审 A Q1 仲裁,2026-10-19)。
- 「解析一次」=每连接 handshake 一次(评审 A Q3 仲裁)。
- backlog 界 2MB、maxDecompressedBytes 4MB、不声明 replay-window/slow-link:
  均为仲裁定值,变更须经仲裁。
