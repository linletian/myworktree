# TASK-D:§五 实测验收(netem 慢链路+N=5 多消费者)(仲裁者任务书 v1,2026-10-20)

依据 FIX-PLAN.md v7 §五。执行者:编码 Agent。前置:Phase A/B/C 已提交
(1b6c658/eeb3851/d2fc415/05b4677/8da7064),工作树干净。
**本任务不改任何产品代码;发现产品缺陷即停该点并回报,不得自行修复。**
**禁止任何 git 写操作。**

## 目标与判据(FIX-PLAN §五 逐条实测)
在真实守护进程(beta 构建,-tags ttymetrics)+ netem 慢链路上验收:
- **AC1a**:overflow-notice cap 连接不下发 1013、连接保留、wire notice ≤4 帧/s。
- **AC2**:回放窗口 clamp+总量封顶生效(handshake 行 windowed=true),
  回放量 ≤W 且 closing sync 发布截断点。
- **AC3(netem)**:`sudo tc qdisc add dev lo root netem delay 300ms 50ms
  distribution normal loss 2% rate 800kbit buffer 3200 limit 1000`
  (计划原文参数,buffer/limit 必带):
  - a. compress 连接稳态不溢出,或 reset cadence ≥ 4MB ÷ netem 实测缺口;
  - b. catchup 完成时间 ≤ W_eff ÷ 实测带宽 + 余量;实测带宽 = handshake 行
    `replayLogicalBytes/replayMs`(compress 连接)或 `replayBytes/replayMs`
    (明文);
  - c. **deadline 误杀数 = 0**(handshake 成功 + conn-closed 行 binFrames
    持续增长=有进展 但被回收者才算误杀;client-liveness 收割不计);
  - d. 全程无 cursor 错位(客户端侧 inflate 全流与 ring 逻辑流一致性校验;
    缺口只能来自 notice 明示的 drop 或窗口截断)。
- **AC5**:N=5 慢消费者并发,各自独立 drop/notice(5 连接各自 handshake/
  overflow 行独立成组);notice 率 ≤4/s;服务端 RSS ≤ 4MB×N + 1.3MB×N
  (读 /proc/<pid>/status VmRSS 采样,含基线扣除)。
- **AC7**:人为阻塞 socket 读 ≥15s 的客户端,其连接被有界回收(出现
  conn-closed,服务端 goroutine 不泄漏——/debug/pprof 或日志佐证)。
- 回归:`go test ./...` 在验收后复跑仍全绿。

## 架构约束(仲裁定案)
1. 守护进程:`go build -tags ttymetrics -o /tmp/mw-acc ./cmd/myworktree`;
   以**隔离 HOME**(/tmp/mw-acc-home)运行,端口自选(避开 40751 生产实例);
   stderr 落盘 /tmp/mw-acc.log([tty-metrics] 行来源)。实例创建:
   POST /api/instances {worktree_id:"main", kind:"pty", command:<producer>}。
   生产者为速率可控的 shell(如 `while :; do head -c 60000 /dev/urandom |
   base64; sleep 0.2; done` 变体——burst 形态对照证据 150-358KB/s)。
2. harness 位置:`docs/plans/fix-issue114-tty-overflow/tools/acceptance/`
   (嵌套 module:`module acceptance`、`go 1.25.0`、`require myworktree v0.0.0`
   + `replace myworktree => ../../../..`;复用 internal/ws 客户端
   `ws.Dial(ctx, addr, path, hdr)`——目录树内 internal 可见)。**只引入
   internal/ws 一个依赖面**;不引 internal/app。
3. WS 客户端行为矩阵:caps 四组合(无 / overflow-notice / compress / 两者)
   + 慢读(每 N ms 读一帧)+ 完全停读(AC7)。compress 客户端用 Go
   `compress/flate` 做 inflate(与 DecompressionStream('deflate-raw') 同格式),
   cursor 按解压后字节推进,校验连续性。
4. netem 生命周期:apply 前 `tc qdisc show dev lo` 留底;**完成后必须
   `sudo tc qdisc del dev lo root` 复原**并再 show 验证;脚本异常退出也要
   复原(defer/清理段)。
5. 认证:守护进程以无 auth 或自设 token 运行(隔离 HOME 下自行 set-auth);
   token 不得写入任何入库文件。

## 交付
1. harness 代码(tools/acceptance/,gofmt+go vet+go build 绿)。
2. `docs/plans/fix-issue114-tty-overflow/ACCEPTANCE-2026-10-20.md`:
   每条判据 PASS/FAIL + 实测数字(handshake/overflow/conn-closed 行原文摘录)
   + netem 配置与复原证据 + RSS 采样表 + 缺陷清单(如有,单独编号)。
3. 回报:判据逐项自查表 + 关键日志摘录 + 质疑(如有)。

## 碎步纪律
构建运行链路→AC1a/AC2(无 netem 基线)→netem AC3→AC5→AC7→报告。
每步 send_message 回报一行。单步失败重试一次,再失败回报并继续下一项。
