# TASK-A3：服务端 app-layer 压缩(5a)（仲裁者任务书 v1,2026-10-19)

依据 FIX-PLAN.md v6(本目录,只读)§二决策 5/5a、§三协议契约、§四 Phase A-3。
前置:A0 压缩门禁 **PASS 5.719x**(A0-GATE.md,同目录)→ 编码放行。
执行者:编码 Agent。发现任务书与代码现实冲突时**停止该点并在回报中质疑**(附证据),
不得自行修改任务书或 FIX-PLAN.md。

## 前置基线(已核实)
- TASK-A2(G2/G3/G4/G5)已在工作树实现,go build/vet/test/-race 全绿(2026-10-19 22:00 实测)。
- caps 全 token 匹配走 `ttyClientHasCap`,k=v 走 `ttyClientCapValue`(已落地)。
- wire notice 250ms 聚合已落地;`ws.Conn` 已有 `SetWriteTimeout`;metered wrapper
  (`conn.m`)已存在并统计 wire 字节。

## 子任务 C1:compress 协商与持久 flate 流
1. 握手:客户端声明 `compress`(全 token,`ttyClientHasCap`)→ 该连接启用 app-layer
   压缩;未声明/legacy 路径**逐字不变**(零 flate 对象、零行为差异,单测 pin)。
2. 流建立:首帧二进制写出**之前**创建每连接持久 `compress/flate` writer
   (`flate.NewWriter`,level=**BestSpeed**,raw deflate 无 zlib 头)。
   回放与 live **共用同一条流**;**中途不得 Close()**(Close 终止共享上下文,
   客户端 `DecompressionStream('deflate-raw')` 会脱同步;连接结束靠关 socket)。
3. 每帧语义:`WriteBinary` 路径 → 原文写入 flate writer → **`Flush()`(sync flush)**
   → 压缩字节作为一个 WS binary 帧写出。TEXT 帧(notice/ping/sync)不压缩、
   原样、顺序不变(同 goroutine 顺序写,notice 先于所涉 chunk 的性质保持)。
4. 缓冲区:每连接复用 scratch buffer,不得每帧 make(热路径)。
5. 错误处理:flate Write/Flush 错误 → 记日志 + 关连接(错误码自定但不得用 1013
   语义混淆 overflow 契约);socket 写错误沿用现有路径。
6. **服务端无解压方向逻辑**:服务端只压不解压(解压后字节服务端不可见);
   不实现任何 maxDecompressedBytes 服务端守卫(v6 R3-MED-1,该守卫属客户端 B-9)。
7. **ready 帧回声(仲裁补充,协议缺口修复)**:现状 ready 帧为静态
   `{"type":"ready"}`(app.go:2825)。启用 compress 的连接,ready 帧改为
   `{"type":"ready","compress":true}`;未声明/legacy **逐字**保持
   `{"type":"ready"}`。依据:客户端靠该字段分流压缩/明文消费(TASK-B B1.4);
   R16 回滚面自洽——旧客户端白名单按 type 派发,多余字段无害;新客户端收到
   无该字段(旧服务端)→ 明文路径。单测 pin 两路 ready 帧字节。

## 子任务 C2:cursor/offset 语义不变量(兼容性,最高优先)
1. 服务端所有 offset/sync/closing-sync/`X-Log-Offset` 语义保持**逻辑字节**(ring
   offset)口径**逐字不变**;压缩只发生在交付层,不进 ring、不进 cursor 算术。
2. 被 drop 的 chunk 从未进入 flate 流(压缩在写时、每 chunk 恰一次、按写出序)
   ——drop→notice→重连=新流。单测 pin:compress+drop 序列后,对 wire 全流做
   一次 inflate,结果 = 逻辑流 − 被 drop 段(notice 为 TEXT 不在 inflate 流内,
   按帧类型分离)。
3. 回放截断(决策 4 总量封顶)与压缩正交:streamed 计数按**逻辑字节**,W_eff
   同为逻辑口径(API.md 写明)。

## 子任务 C3:双字段埋点(R14)
1. compress 连接的 `[tty-metrics]` slow-write / conn-closed 行新增
   `wireBytes=<压缩后交付字节>` 与 `logicalBytes=<压缩前逻辑字节>`;
   非 compress 连接 `wireBytes==logicalBytes`(口径统一,历史可比)。
2. handshake 行在 compress 连接加 `compress=true`(未声明不发,与 windowed 同风格)。
3. 埋点仍走 ttyMetricsBuild const 门(stable 构建零开销)。

## 子任务 C4:测试(新增于 internal/app/tty_ws_test.go 或新文件)
1. 四组合矩阵(§三 R12):
   - 无 caps → 明文 + 1013(=今日逐字,pin);
   - `overflow-notice` → 明文 + notice;
   - `compress` → deflate-raw wire + 1013;
   - 两者 → deflate-raw wire + notice。
   每格断言:二进制帧 inflate 可还原(测试端用 `flate.NewReader` 还原整条流)、
   TEXT 帧明文、cursor 口径逻辑字节。
2. compress 中途 drop:inflate 不错位(同 C2.2 口径)。
3. legacy 零变化 pin:未声明 compress 时 wire 字节与压缩代码合入前逐字一致
   (可断言帧内容与明文预期全等、无 flate 头字节 0x78 误入)。
4. 持久性 pin:同连接多帧压缩后,**单条**流 inflate 全量还原(证明帧间共享
   上下文,非每帧独立流)。
5. BestSpeed pin:压缩帧可解码即可,不锁 ratio 数字(单测不依赖内容)。
6. 双字段埋点单测:ttyMetricsBuild tag 下 wireBytes≤logicalBytes、非 compress
   连接两值相等。

## 硬约束
- 允许改动:internal/app/app.go、internal/app/tty_metrics*.go、
  internal/app/tty_ws_test.go(或新增 compress 专测文件)、internal/ws/ws.go
  (若 wrapper 需要;**不得改 writeFrame 既有 deadline/锁语义**)、docs/API.md。
- 禁止:driver.go、index.html(Phase B)、FIX-PLAN.md、任务书本身。
- legacy 全链路零变化:`TestHandleInstanceTTYWS_SubscriberOverflowClosesConnection
  With1013` 与全部既有测试原样通过。
- 完成判据:`go build ./... && go vet ./internal/app/ ./internal/ws/ &&
  go test ./internal/app/ ./internal/ws/ -count=1 && go test -race
  ./internal/app/ -count=1` 全绿;`gofmt -l` 无输出。
- 交付:改动摘要、git diff --stat、每条子任务「要求→实现行号→测试名」自查表、
  任何质疑(如有)。
