# ACCEPTANCE — issue #114 §五 实测验收 (2026-10-20, round5 返工订正版)

- 工单：`TASK-D-acceptance.md`；返工依据：round1 D-R1/D-R2 + round2 E1~E5 + round3 F1~F5 + round4 G1~G3 + **round5 最终批 H1~H7**（全报告/计划/脚本层，无产品缺陷）
- 构建：beta `-tags ttymetrics`（`/tmp/mw-acc`），隔离 `HOME=/tmp/mw-acc-home`
- 端口：自选 loopback `-listen=127.0.0.1:0`（round4 单 daemon 实得 **`38803`**，pid **1295453**）；**生产实例 127.0.0.1:40751 全程未触碰**（复原后实测 `http=200`）
- harness：`tools/acceptance/`（嵌套 module `myworktree/tools/acceptance`，唯一依赖 `internal/ws`）；本轮新增 `e2eCatchupMs`、逐帧 cursor、修正 `continuityOK`、`drainInflate` 硬上限
- 回归：`go test ./... -count=1` **全绿 EXIT=0**（round5 复跑 app 34.1s / ws 1.8s / pty / ui … 全 ok）；曾间歇 flake `TestHandleInstanceTTYWS_MatrixCompressOverflowDeflate1013` **已由 `4b0525b` 修复（确定性同步点，零弱化）**，修复后复跑 EXIT=0（见 §缺陷清单）
- **仲裁者 round1→round2 三项裁定（2026-10-20，已落到下文对应判据）**：① AC5 接受综合判定 PASS（同时刻 5 路 overflow 活体行未在 netem 下稳定采集，扇出独立性另有 driver 单测与单连接 definitive 证据互补）；② AC1a 维持 PARTIAL（诚实分级）；③ 明文 45KB/s 无需去 loss 复测（loss+RTT 为跨境链路真实组分，判据口径即实测带宽）。
- **round2 合并返工批 E1~E5 落点**：**E1** harness read deadline 放宽至 30s（`-read-deadline-ms`，desync 真因）→ AC5 重采得 **5/5 存活 frames>0 + 5 路独立 conn-closed**（见 AC5 节）；**E2** AC3c 改「风暴速率档 ≤358KB/s 误杀=0」（此速率档表述**已被 F1 取代**）、§限制新增「持续超容量+socket 楔死→15s 写 deadline 回收=设计行为（Q1；评审 B 复现 4MB/s+慢读者 blockMs=15000）」、AC1a「连接保留」加「写阻塞 <deadline 时成立」限定；**E3** run-netem.sh AC7 重启后重解析 PORT + 补 AC5 步 + 明文 catchup 步（run-ms 边界注明）；**E4** 附录逐字化（qdisc 无 distribution、AC5 5 id+conn-closed×5+RSS /proc 行、AC3a 压缩 JSON、AC2/AC1a overflow 全文含 headNow/liveLogicalBytes、RSS BASE→PEAK 口径）；**E5** drainInflate「追平等待(400ms 封顶)」、continuityOK null 边界注、AC3a wire 速率除数口径注。
- **round3 合并返工批 F1~F5 落点**：**F1(R3-B1 MED)** AC3c 订正为「**所引运行中（健康慢链路，消费者跟得上）误杀=0**」，§限制订正决定因素=**消费者排空 vs 生产速率**（无论是否 netem；评审 B 反例 240/340KB/s+300ms 慢读无 netem 即 blockMs=15001；4MB/s 案例客户端**在读但读得慢**，"读不进"表述订正）；**F2(R3-B2)** 附录 AC3a 换 harness **真实输出原文**（`notices:[]` 数组、无 ratio 字段，ratio 现算），handshake×5/conn-closed×5/AC7 行**补全字段逐字**；**F3(R3-B3)** run-netem.sh AC5 步补 `/proc/$DPID/status` VmRSS 采样→`$OUT/ac5-rss.txt`，DPID 全脚本统一定义；**F4(R3-B4/B6)** 工件按 `$OUT/<step>.log` 留存（round4 起改为**轮次戳目录** `$OUT-<ts>/`），速览 AC5 补「RSS 上界口径=连接/队列分量；ring 为每实例既有内存(16–256MB 级,config 决定),不在本判据」，静实例 RSS 随 F3 重采（`$OUT/rss-quiet.txt`）；**F5(A-N1/N2/N3)** AC1a 步显式传 `-read-deadline-ms`，AC3c 附录补风暴档逐字行（`maxWriteBlockMs=0`），harness `main.go` 注释 `closeCode=0`=未收 close 帧/跑满语义。（round3 曾把 AC7 复跑失败归因 env=5000——**该归因已被 round4 R4-B3 取代**：真因是 daemon CWD/git 根解析，env 无关。）
- **round4 合并返工批 G1~G3 落点**：**G1** 单 daemon（CWD=throwaway repo 修 git 根、env=5000 全程、stdin 保持不 EOF）顺序完成 quiet RSS + AC5 加载 RSS + AC3a 压缩对比 + AC7 env=5000，两组 RSS 同 pid（**1295453**）可相减；工件入轮次戳目录 `/tmp/acc-netem-20261020-114614/`（Q-C）。**G2** 缺陷清单「误杀」限定（R4-B1）、附录全换 round4 留存件原文+标题「工件摘录」逐块标节选/轮次（R4-B2/B6）、AC7 归因改 git 根/CWD（R4-B3）、历史记述加取代标记（R4-B7）、AC5 handshake×5 入 `$OUT`（A-T2）、§其他观察另立 gitx 工单（R4-B7 附）。**G3** AC3a/RSS 数字统一 round4 同源。
- **round5 最终返工批 H1~H7 落点**：**H1** AC2 正文证据块换 `$OUT/ac2.log` round4 原文（windowed=true droppedFromWindow=85151601 replayBytes=5024 replayMs=4）+ span 复算 86200177−85151601=1048576；AC1a/AC3a 明文块换 `/tmp/acc-netem/ac3a.log` round3 原文（d9a628ebc0a0 droppedBytes=21874752）标〔round3〕；速览 AC1a 格补轮次。**H2** run-netem.sh 加固：AC5 步后 **DELETE 5 实例**（否则后续步在 5×1.2MB/s 残留负载下 dial 超时）、`wait … || true`、单步失败重试一次+STEP-FAILED 标记、DPID `pgrep` 唯一性断言、加固后端到端复跑 **exit 0** @ `/tmp/acc-netem-20261020-123744/`。**H3** AC5 披露客户端实收 29–36 帧/≈93–100kB vs server 写出 2541–2576（"交付"→"服务端写出"）；附录补回客户端 5 行。**H4** 附录标前缀剥除、host 行尾真实空格照实。**H5** DPID 唯一性（见 H2）+ netns list `tee $OUT/netns-list.txt`。**H6** 速览 AC3a 格标两臂轮次/env 档不同。**H7** 产品仓 flake `TestHandleInstanceTTYWS_MatrixCompressOverflowDeflate1013` 披露入 §其他观察（另一 agent 修复中）。
- **终审 B 收尾批落点（双 APPROVE 后）**：**B-1** flake 已由 `4b0525b` 修复（确定性同步点、零弱化），缺陷清单+回归行去「待复跑」；**B-2** 附录补 round5 加固复跑自有数字（quiet≈194kB/conn、e2eCatchupMs=4554、ratio≈42.17x、blockMs=5000）与 round4 并列；**B-4** :61「工单 31–42x」订正为「实测 41.8–42.2x（三轮）」；**B-5** 评审 B 反例 + FIX-PLAN §五.5 ~171kB/~122kB 标「探针工件未入库（评审 /tmp）」；**item5** AC5 补工单判据=每连接增量/基线扣除、本判据由 quiet 档测得；**Q2** AC3c 补「未构成强检验、有意收窄、§五.3.c 同口径限定」；**Q3** 勘误清单补 AC5 RSS per-conn 授权解释（FIX-PLAN §五.5 v7 同口径）。

## 结论速览

| 判据 | 结论 | 关键实测（本轮新证据） |
|---|---|---|
| AC1a | **PARTIAL** | 服务端溢出确证 `drop-oldest, connection kept`（〔轮次 round3 default-deadline〕`droppedBytes=21874752`、**无 1013**）；但 **wire-notice "到达客户端"未活体实测**——由 pin 覆盖 + `250ms` 常量推导，故诚实记 PARTIAL |
| AC2 | **PASS** | `windowed=true`、`replayLogicalBytes=1048576`(=W)、`syncStart=85151601`(exclusive)→ `86200177−85151601=1048576` 精确=W（round4 `$OUT/ac2_ac3b.json`） |
| AC3a | **PASS** | 判别性对照（**两臂轮次/env 档不同**）：**压缩臂〔round4 env=5000〕** `binaryWireBytes=236100/inflatedBytes=9890880`→ ratio≈**41.89x**、`notices=[]`→ **0 溢出**；**明文臂〔round3 default-deadline〕** `droppedBytes=21874752` drop-oldest。**round4 明文臂在 env=5000 下未现 drop-oldest**（慢消费经 write-deadline 回收先于 app-queue drop，见 AC7/§限制），故明文溢出取 default 档 |
| AC3b | **PASS** | `e2eCatchupMs=2438ms`（connect→closing sync 真实墙钟，round4 `$OUT/ac2_ac3b.json`）；`continuityOK=true`；明文链路实测≈42–47KB/s |
| AC3c | **PASS** | **所引运行中（健康慢链路，消费者跟得上）误杀=0**（`maxWriteBlockMs=0`、正常主动关闭）。决定因素=**消费者排空速率 vs 生产速率**，非 netem 有无（见正文/§限制） |
| AC3d | **PASS** | 逐帧 cursor（round4）：`base(85151601)+replayInflated(1048576)=cursorAtClosing(86200177)=closing sync offset`，`continuityOK=true`（带 sync 基点的运行真实求值） |
| AC5 | **PASS**（真 5 路·round4 单 daemon 同源） | netem 下 5 独立实例各 1 慢消费者 **5/5 存活 frames>0**（server `binFrames` 2541–2576）+ handshake **5 distinct id** + `conn-closed=5`；各 conn 独立 `maxWriteBlockMs=5000/5001`（env=5000 写 deadline 回收，Q1）。RSS **同 pid 1295453 可相减**：quiet BASE 14096→PEAK 14972（Δ876kB/5≈**175kB/conn** 连接分量）；AC5 BASE 68608→PEAK 621832（Δ553224kB ring 主导）。**RSS 上界口径=连接/队列分量**；ring 为每实例既有内存（16–256MB 级，config），**不在本判据** |
| AC7 | **PASS**（round4 单 daemon 同源） | `MYWORKTREE_TTY_WRITE_TIMEOUT_MS=5000`（AC5/AC7 同进程同一 env）→ `slow-write blockMs=5002`、`maxWriteBlockMs=5002` **有界回收 ~5s**（`$OUT/ac7.log`） |

## 逐条证据

### AC1a — overflow-notice 不下发 1013 / 连接保留 / notice≤4/s —— **PARTIAL**
netem 内明文 flood（acc-flood ~2MB/s）+ overflow-notice 客户端（harness 步显式传 `-read-deadline-ms 30000`，见 run-netem.sh AC1a 步）。**drop-oldest 证据取 default-deadline 档 round3**（env=5000 档下慢消费经 write-deadline 回收先于 app-queue drop，见 AC7/§限制），服务端日志：
```
# 〔轮次 round3 原文〕/tmp/acc-netem/ac3a.log（保留行首时间戳与 [tty-metrics] 前缀）
2026/10/20 10:43:39 tty output subscriber overflow for d9a628ebc0a0: consumer fell behind, overflow notice pending (wire rate 250ms): droppedChunks=22034 droppedBytes=21874752 (drop-oldest, connection kept)
2026/10/20 10:43:39 [tty-metrics] overflow id=d9a628ebc0a0 subAgeMs=14495 liveBytes=2532416 liveFrames=2548 headNow=123227825 maxWriteBlockMs=13139 slowWrites=1 liveLogicalBytes=2532416
```
- **无 1013、连接保留（写阻塞 < deadline 时成立）**：本例连接存活至窗口结束，app 订阅队列填满 4MB 后持续 drop-oldest（累计 `droppedBytes=21874752`≈21.8MB），server 写出阻塞 `maxWriteBlockMs=13139` < 15s deadline → 连接保留、不下发 1013。若写阻塞持续至 ≥deadline，则由 write deadline 回收（见 §限制 / Q1，属设计行为）。
- **诚实降级 PARTIAL 的理由**：`notice 帧"到达 raw-WS 客户端"这一环未活体实测（本轮各 overflow 运行客户端侧 `notices=0`）**。单 goroutine handler 卡在 `WriteBinary`（backlog 满）时无法推进 select 的 notice 分支，notice 滞留 backlog；回环 kernel 发送缓冲吸收写出使多数轻/中载运行 app 队列不溢出。故"到达率≤4/s"仅由 `ttyOverflowNoticeInterval=250ms` 常量**构造推导**，非活体计数。
- **pin 覆盖（互补，非活体替代）**：服务端 `internal/app/tty_ws_test.go:1760` `TestHandleInstanceTTYWS_OverflowNoticeCapNoticesInsteadOfClosing` 在 in-process 真实 socket 断言溢出时下发 notice 帧而非 close；客户端 `handleTTYOverflow`（`internal/ui/static/index.html:6484`）由 `internal/ui/testdata/tty_overflow_reconnect.test.mjs`、`terminal_status.test.mjs` 钉住。
- 综合：服务端生成 + 常量节流 + pin 帧到达三重覆盖，但**缺 raw harness 活体到达计数** → 判 **PARTIAL**（依仲裁者 D-R2a）。
- **仲裁裁定（质疑②）**：AC1a **维持 PARTIAL**（诚实分级）——服务端生成 + pin 覆盖虽充分，但 raw harness 未活体计数 wire-notice 到达率，故不升 PASS。

### AC2 — 重放窗口 clamp + 总量封顶 + sync 发布截断
```
# $OUT/ac2.log（round4 原文，行首时间戳与 [tty-metrics] 前缀已剥；id=11e9f0e7eeda）
handshake id=11e9f0e7eeda mode=catchup since=0 headAtStart=86200177 published=86200177
    replayBytes=5024 replayMs=4 budgetExhausted=false windowed=true droppedFromWindow=85151601 replayLogicalBytes=1048576 compress=true
```
- **exclusive 起点**：opening sync `syncStart=85151601`（=headAtStart−W，**非** 85151602）；closing sync `offset=86200177`（`$OUT/ac2_ac3b.json`）。
- 复算 span：`86200177 − 85151601 = 1048576 = W` **精确封顶**；`droppedFromWindow=85151601`（窗口前丢弃字节）与 `replayLogicalBytes=1048576`（窗口内重放）自洽。

### AC3（netns + netem，宿主零改动）
netem（netns `mwacc` 内 `lo`，`tc qdisc show` **本机实跑原文**，注：本机 tc 不回显 `distribution normal`，虽 `add` 时指定）：
```
qdisc netem 800f: root refcnt 2 limit 1000 delay 300ms  50ms loss 2% rate 800Kbit seed 18005498265520688065
```
（`add` 命令含 `distribution normal`；`show` 原文如上，无该字样，照实记录。）
> **勘误**：工单原文 `buffer 3200` 被 netem 拒绝（`buffer` 属 tbf/sfq）；netem 以 `limit`（包）计 backlog，已去 buffer 保 limit。

- **AC3a 判别性对照**（同一 ≥2MB/s flood 实例）：
  - 明文：`binaryWireBytes` 溢出触发 server `droppedBytes=17191808 drop-oldest`（wire=logical≥netem 排空）。
  - 压缩（round4 `$OUT/ac3a-compress.json` 原文，单 daemon pid 1295453）：`readyCompress=true binaryWireBytes=236100 inflatedBytes=9890880 notices=[] closeCode=0` → ratio=9890880/236100≈**41.89x**（现算，非 harness 字段）；wire=236100B / elapsed 12.0s≈**19.7KB/s**（除数口径=完整 run-ms=12s 墙钟）≪ netem 100KB/s → **0 溢出**（`notices=[]`）。**实测 41.8–42.2x（三轮：round3 41.98x / round4 41.89x / round5 42.17x）**（工单原文无该区间，此为三轮实测值）。
  - 明文 side（AC1a/溢出场景，〔轮次 round3 default-deadline〕 `/tmp/acc-netem/ac3a.log` 原文 id=d9a628ebc0a0）：`droppedBytes=21874752 drop-oldest`。**round4 单 daemon 为 env=5000 档**，慢消费经 write-deadline（~5s）回收先于 app-queue drop-oldest，故本轮明文未现 drop-oldest 行；两档 backpressure 机制不同，均为设计路径（见 AC7/§限制）。
- **AC3b e2e catchup**（catchup since=0, compress+sync，round4 `$OUT/ac2_ac3b.json`）：`e2eCatchupMs=2438`（connect→closing sync 真实墙钟，含 netem RTT）。server `replayBytes=5024`（compress 后 wire）→ 快于明文。
  - 明文 catchup 链路带宽实测：单 1MiB 重放 22s 仅达 917504B、256KB 窗 18s 达 851968B → **有效≈42–47KB/s**（loss+RTT 拖累，低于 800kbit 理论 100KB/s）。**诚实说明**：server 侧 `replayLogicalBytes/replayMs`（≈209MB/s）仅测"写入 kernel 发送缓冲"，对 netem 排空**无意义**；`e2eCatchupMs` 才是 netem 真实 catchup 数。
  - 判据 `catchup ≤ W_eff/带宽 + 余量`：明文 1MiB @≈45KB/s ≈ 23s（需 run≥25s 方见 closing sync）；compress 同负载 e2e=2.4s（wire 仅 5KB）。两者均在"窗口完成 + 余量"内，无 budget 截断（`budgetExhausted=false`）。
  - **仲裁裁定（质疑③）**：明文 ≈45KB/s **无需去 loss 复测**——loss+RTT 为跨境链路真实组分，判据口径即**实测带宽**（非理论 800kbit）；实测值即有效口径。
- **AC3c**：**所引运行中（健康慢链路、消费者排空速率 ≥ 生产速率）实测误杀=0**——这些运行里 `maxWriteBlockMs=0`、`conn-closed` 均为客户端主动或跑满（见附录 `ac3c.log` 逐字行）。**决定因素是"消费者排空速率 vs 生产速率"，与 netem 有无无关**：消费者跟得上则写不阻塞、无误杀；消费者排空持续低于生产速率（无论是否 netem）则写可阻塞至 deadline（见 §限制）。**仲裁裁定（Q2）**：该运行**未构成对判据的强检验**（口径收窄至"消费者跟得上"档）——**有意收窄**，计划 §五.3.c 已加同口径限定；消费者跟不上档的回收行为另由 §限制/Q1 覆盖。
- **AC3d 逐帧 cursor（D-R1.2）**：closing sync 处 `drainInflate` 追平等待（400ms 封顶）后（round4 `$OUT/ac2_ac3b.json`）：`cursorAtClosing = base(85151601) + replayInflated(1048576) + dropped(0) = 86200177 == closing sync offset(86200177)` → **`continuityOK=true`**。前置条件已修正：凡有 opening-sync 基点的运行（catchup+sync）均真实求值（不再被 `ReadyCompress` 门与全量 inflated 误判）。**边界注（评审 A Q2）**：opening sync 已到而 closing sync 未到时，`continuityOK` 保持 `null`（未求值），指针语义正确——非 false，仅表"尚无 closing 基准可比"。

### AC5 — N=5 独立 drop/notice + RSS（round4 单 daemon 同源，pid 1295453，RSS 可相减）
G1 单 daemon（env=5000 全程）下 5 个独立实例（各 ~1.2MB/s 明文生产者）各挂 1 个慢消费者（`-read-delay-ms 400 -read-deadline-ms 30000 -run-ms 20000`）。**5/5 连接全存活至回收、frames>0**——server 端 `conn-closed` 逐字（`$OUT/ac5.log`，全字段）：
```
conn-closed id=b8c85f54e42b aliveMs=9222 binBytes=2627520 binFrames=2576 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5027 wireBytes=2627520 logicalBytes=2627520
conn-closed id=f749ab852816 aliveMs=9213 binBytes=2603968 binFrames=2554 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5027 wireBytes=2603968 logicalBytes=2603968
conn-closed id=61803e14f242 aliveMs=9194 binBytes=2604992 binFrames=2549 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5026 wireBytes=2604992 logicalBytes=2604992
conn-closed id=afb10e07eb24 aliveMs=9256 binBytes=2599872 binFrames=2541 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5001 totalWriteBlockMs=5025 wireBytes=2599872 logicalBytes=2599872
conn-closed id=502b5ce1aa93 aliveMs=9237 binBytes=2629568 binFrames=2571 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5025 wireBytes=2629568 logicalBytes=2629568
```
- **5/5 存活、服务端写出 frames>0（措辞订正 H3）**：每条 conn 回收前**服务端写出** `binFrames` 2541–2576（server 端 definitive；这是"服务端写出"计数，非"客户端实收"）；全部存活 ~9.2s 后回收（env=5000 → `maxWriteBlockMs=5000/5001`，与 AC7 同档）。**客户端实收远少于此**（慢读 + netem 限速 + 5s 回收窗口内）：`$OUT/ac5-{1..5}.json` 客户端 `binaryFrames` **29–36**、`binaryWireBytes` **93184–100352**（≈93–100kB）——server 写出 2541–2576 帧 / ~2.6MB，客户端在阻塞/回收前仅消费 29–36 帧 / ~93–100kB，差额滞留 kernel 发送缓冲 + netem backlog（正是超容量 backpressure 的体现）。
- **5 连接独立成组**：`handshake` 行 **5 个 distinct id**（`61803e14f242/afb10e07eb24/f749ab852816/b8c85f54e42b/502b5ce1aa93`，源自 `$OUT/ac5-handshake.log`），`conn-closed=5`，各 conn **独立** `maxWriteBlockMs`（5000/5001）。
- **溢出/backpressure 实际形态（诚实）**：env=5000 档下 backpressure 表现为**每条连接独立命中 5s 写 deadline 回收**（慢读者 + 超容量生产者 → server 写阻塞 → deadline，即 §限制 Q1 设计路径的 5 路独立版）；本档写阻塞命中 deadline 先于 app-queue drop，未见 `subscriber overflow drop-oldest` 行。drop-oldest 证据见 default-deadline 档（AC1a/AC3a，〔轮次 round3〕 `/tmp/acc-netem/ac3a.log` definitive `droppedBytes=21874752`）。依仲裁裁定①，扇出独立性由 5 路独立 handshake/conn-closed + 单连接 definitive drop-oldest 互补。
- **RSS 同 pid 相减（daemon pid 1295453，`$OUT/ac5-rss.txt` / `$OUT/rss-quiet.txt`）**：
  - quiet（先采，ring 冻结基线）：`quiet BASE VmRSS: 14096 kB → PEAK 14972 kB`，5 并发 compress 连接增量 Δ876kB → **per-conn≈175kB**（连接/队列分量）。
  - AC5（后采，加载）：`ac5 BASE VmRSS: 68608 kB → PEAK 621832 kB`，Δ553224kB **由 5 路 ring 增长主导**。
  - 两组同 pid（1295453）→ 可相减：AC5 基线较 quiet 基线高 Δ54512kB（其间实例 ring 累积），加载增量归 ring。
  - **工单判据口径**：每连接增量 = 有连接态 − 基线（**基线扣除**）；本判据由 **quiet 档**测得 **≈175kB/conn**（连接/队列分量）；AC5 加载档的 Δ（ring 主导）**不进入本判据**（ring 为每实例既有内存，非 per-conn 增量）。
- **仲裁裁定（质疑①）**：AC5 **接受综合判定 PASS**；round4 进一步取得 **5/5 存活 frames>0（binFrames 2541–2576）+ 5 路独立 conn-closed（各 maxWriteBlockMs=5000/5001）+ 同 pid RSS 可相减**。
- **H2 加固（round5）**：AC5 的 5 实例各持续 ~1.2MB/s 生产，若不回收则残留 ~6MB/s 负载使后续步 dial 超时（评审 B 复跑 1/2 因此整轮 abort）。run-netem.sh 现于 AC5 步后 `POST /api/instances/delete` **DELETE 这 5 个实例**；配套 `wait … || true`、单步失败重试一次+`STEP-FAILED` 标记、`pgrep -x mw-acc` **DPID 唯一性断言**。加固后端到端复跑 **exit 0** @ `/tmp/acc-netem-20261020-123744/`（后续 AC3a/AC1a/AC2/AC3c/AC7 全部无超时完成，AC7 `blockMs=5000` 与 round4 `5002` 同档互证）。

### AC7 — 阻塞读有界回收（env 档，D-R1.5；round4 单 daemon 同源）
`MYWORKTREE_TTY_WRITE_TIMEOUT_MS=5000`（与 AC5 同进程同 env）+ flood 生产者 + 阻塞读（stop 模式），`$OUT/ac7.log` 逐字：
```
[tty-metrics] slow-write id=ea9251b5836c op=binary bytes=1024 blockMs=5002 wireBytes=2627327 logicalBytes=2627327
[tty-metrics] conn-closed id=ea9251b5836c aliveMs=7051 binBytes=2627327 binFrames=2581 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5002 totalWriteBlockMs=5030 wireBytes=2627327 logicalBytes=2627327
```
- 阻塞写**精确 ~5002ms** 触发 write deadline → 回收；`maxWriteBlockMs=5002` 对齐 env=5000 → **env 生效、有界回收**。socket/goroutine 无泄漏。
- **归因订正（R4-B3 取代 round3）**：round3 曾把 AC7 复跑失败归因 env=5000——**误判**。真因是 daemon 启动期 **git 根解析（CWD）** 失败：`gitx.GitRoot` 对非仓库 CWD（或 2s 超时）统一报 `not a git repository`（`internal/gitx/gitx.go:14-20`）。评审 B 实证：带/不带该 env **3/3 正常**（env 仅连接期读取，与启动无关）。round4 将 daemon CWD 置于 throwaway git repo（`/tmp/mw-acc-home/repo`）后，env=5000 单 daemon 正常 `Server running`，AC7 直接 definitive 复跑（`blockMs=5002`）。

## Q1 裁定（D-R2d）
**handler 阻塞写被 write deadline 有界回收 = 设计行为，非缺陷。** AC7 实测：客户端停读 → server `WriteBinary` 阻塞 → per-write deadline（默认 15s / env 5s）到期 → `slow-write blockMs` 记录 → `conn-closed` 回收，`maxWriteBlockMs` 与 deadline 精确对齐。此为 §五 decision-3「write deadline #83」刻意引入的**保护路径**，非缺陷，裁定结案。

## 工件摘录（cited=round4 单 daemon pid 1295453 `/tmp/acc-netem-20261020-114614/`；round5 加固复跑绿另证；逐块标〔节选/轮次〕）
> 前缀/空格说明（H4）：conn-closed/handshake/overflow 行中，凡标注"已剥"者其行首 `2026/10/20 HH:MM:SS ` 时间戳与 `[tty-metrics] ` 前缀已从留存日志剥除以便对齐阅读；**AC1a/AC3a 明文 overflow 块保留原前缀**（见该块注）。`host-before/after.txt` 的 `qdisc noqueue 0: root refcnt 2` 行**行尾含一个真实空格**（内核 tc 原样输出），照实保留、未 trim。
```
# HOST qdisc BEFORE / AFTER（〔轮次 round4〕逐字一致，宿主零改动；行尾含真实空格 ␠ 未 trim；$OUT/host-before.txt / host-after.txt）
qdisc noqueue 0: root refcnt 2␠
qdisc noqueue 0: root refcnt 2␠

# netns qdisc（〔轮次 round4〕mwacc 内 lo；本机 tc qdisc show 实跑原文，不回显 distribution normal；$OUT/netns-qdisc.txt）
qdisc netem 800f: root refcnt 2 limit 1000 delay 300ms  50ms loss 2% rate 800Kbit seed 18005498265520688065

# ip netns list（〔轮次 round4〕复原后）
（空）

# 〔节选/轮次 round4〕$OUT/ac2_ac3b.json — AC2/AC3b/AC3d 同一运行（continuityOK=true）
{"scenario":"normal","daemon":"127.0.0.1:38803","instanceId":"11e9f0e7eeda","caps":"compress,sync","since":0,"replayWindow":0,"readyCompress":true,"syncStart":85151601,"lastSyncEnd":86200177,"binaryFrames":23151,"binaryWireBytes":556737,"inflatedBytes":24020352,"textFrames":4,"pings":0,"notices":[],"noticeMaxPerSec":0,"closeCode":0,"elapsedMs":26066,"inflateErr":"unexpected EOF","e2eCatchupMs":2438,"closingSyncSeen":true,"continuityBase":85151601,"replayInflated":1048576,"cursorAtClosing":86200177,"continuityOK":true,"note":""}

# 〔节选/轮次 round4〕$OUT/ac3a-compress.json — AC3a 压缩侧（0 溢出；notices 为数组、无 ratio 字段）
{"scenario":"normal","daemon":"127.0.0.1:38803","instanceId":"11e9f0e7eeda","caps":"compress,overflow-notice","since":-1,"replayWindow":0,"readyCompress":true,"syncStart":0,"lastSyncEnd":0,"binaryFrames":9886,"binaryWireBytes":236100,"inflatedBytes":9890880,"textFrames":2,"pings":0,"notices":[],"noticeMaxPerSec":0,"closeCode":0,"elapsedMs":12017,"inflateErr":"unexpected EOF","e2eCatchupMs":0,"closingSyncSeen":false,"continuityBase":0,"replayInflated":0,"cursorAtClosing":0,"continuityOK":null,"note":""}
# ratio 现算=9890880/236100≈41.89x（非 harness 字段）；wire 236100B/elapsed 12.0s≈19.7KB/s≪netem→0 溢出

# 〔节选/轮次 round3 default-deadline〕AC3a/AC1a 明文溢出 drop-oldest（/tmp/acc-netem/ac3a.log 原文，保留行首时间戳与 [tty-metrics] 前缀；env=5000 档下慢消费经 write-deadline 回收先于 app-queue drop，故取 default 档）
2026/10/20 10:43:39 tty output subscriber overflow for d9a628ebc0a0: consumer fell behind, overflow notice pending (wire rate 250ms): droppedChunks=22034 droppedBytes=21874752 (drop-oldest, connection kept)
2026/10/20 10:43:39 [tty-metrics] overflow id=d9a628ebc0a0 subAgeMs=14495 liveBytes=2532416 liveFrames=2548 headNow=123227825 maxWriteBlockMs=13139 slowWrites=1 liveLogicalBytes=2532416

# 〔节选/轮次 round4〕AC3c 风暴档（消费者跟得上）conn-closed 逐字（$OUT/ac3c.log）
conn-closed id=00947a6b23f8 aliveMs=11417 binBytes=19447 binFrames=789 txtFrames=2 pingFrames=1 slowWrites=0 maxWriteBlockMs=0 totalWriteBlockMs=12 wireBytes=19447 logicalBytes=849536

# 〔节选/轮次 round4〕AC5 handshake ×5（$OUT/ac5-handshake.log；tail 模式 replayBytes=65536 each）
handshake id=61803e14f242 mode=tail since=-1 headAtStart=3420175 published=3420175 replayBytes=65536 replayMs=0 budgetExhausted=false windowed=false droppedFromWindow=0 replayLogicalBytes=65536
handshake id=afb10e07eb24 mode=tail since=-1 headAtStart=2460175 published=2460175 replayBytes=65536 replayMs=0 budgetExhausted=false windowed=false droppedFromWindow=0 replayLogicalBytes=65536
handshake id=f749ab852816 mode=tail since=-1 headAtStart=4560175 published=4560175 replayBytes=65536 replayMs=0 budgetExhausted=false windowed=false droppedFromWindow=0 replayLogicalBytes=65536
handshake id=b8c85f54e42b mode=tail since=-1 headAtStart=5700175 published=5700175 replayBytes=65536 replayMs=0 budgetExhausted=false windowed=false droppedFromWindow=0 replayLogicalBytes=65536
handshake id=502b5ce1aa93 mode=tail since=-1 headAtStart=1500175 published=1500175 replayBytes=65536 replayMs=0 budgetExhausted=false windowed=false droppedFromWindow=0 replayLogicalBytes=65536

# 〔节选/轮次 round4〕AC5 conn-closed ×5（$OUT/ac5.log；env=5000 → 各 maxWriteBlockMs=5000/5001；binFrames 2541–2576 证 5/5 frames>0）
conn-closed id=b8c85f54e42b aliveMs=9222 binBytes=2627520 binFrames=2576 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5027 wireBytes=2627520 logicalBytes=2627520
conn-closed id=f749ab852816 aliveMs=9213 binBytes=2603968 binFrames=2554 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5027 wireBytes=2603968 logicalBytes=2603968
conn-closed id=61803e14f242 aliveMs=9194 binBytes=2604992 binFrames=2549 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5026 wireBytes=2604992 logicalBytes=2604992
conn-closed id=afb10e07eb24 aliveMs=9256 binBytes=2599872 binFrames=2541 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5001 totalWriteBlockMs=5025 wireBytes=2599872 logicalBytes=2599872
conn-closed id=502b5ce1aa93 aliveMs=9237 binBytes=2629568 binFrames=2571 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5000 totalWriteBlockMs=5025 wireBytes=2629568 logicalBytes=2629568

# 〔节选/轮次 round4〕AC5 客户端实收 ×5（$OUT/ac5-{1..5}.json；对比 server binFrames 2541–2576——客户端在 5s 回收+netem 限速下仅实收 29–36 帧/≈93–100kB，差额滞留发送缓冲+netem backlog）
client c1 binaryFrames=31 binaryWireBytes=95232 closeCode=0
client c2 binaryFrames=33 binaryWireBytes=98048 closeCode=0
client c3 binaryFrames=36 binaryWireBytes=100352 closeCode=0
client c4 binaryFrames=34 binaryWireBytes=99328 closeCode=0
client c5 binaryFrames=29 binaryWireBytes=93184 closeCode=0

# 〔轮次 round4〕RSS 同 pid 1295453（可相减）
#   quiet（先采）$OUT/rss-quiet.txt：BASE 14096 → PEAK 14972（Δ876kB/5≈175kB/conn 连接分量）
quiet BASE VmRSS:    14096 kB
t=8s  VmRSS:     14972 kB
#   AC5（后采）$OUT/ac5-rss.txt：BASE 68608 → PEAK 621832（Δ553224kB ring 主导）
ac5 BASE VmRSS:    68608 kB
t=2s  VmRSS:    116412 kB
t=12s VmRSS:    352672 kB
t=24s VmRSS:    621832 kB   # PEAK；BASE=68608 → Δ553224kB

# 〔节选/轮次 round4〕AC7 env=5000 回收（$OUT/ac7.log，同 daemon，全字段）
[tty-metrics] slow-write id=ea9251b5836c op=binary bytes=1024 blockMs=5002 wireBytes=2627327 logicalBytes=2627327
[tty-metrics] conn-closed id=ea9251b5836c aliveMs=7051 binBytes=2627327 binFrames=2581 txtFrames=2 pingFrames=0 slowWrites=1 maxWriteBlockMs=5002 totalWriteBlockMs=5030 wireBytes=2627327 logicalBytes=2627327

# 〔轮次 round5 加固复跑自有数字〕/tmp/acc-netem-20261020-123744/（pid 1355357，DELETE+重试加固后 exit 0；与 round4 并列，非替换）
#   quiet RSS $OUT/rss-quiet.txt：BASE 14212 → PEAK 15180（Δ968kB/5≈194kB/conn 连接分量）
quiet BASE VmRSS:    14212 kB
t=8s  VmRSS:     15180 kB
#   AC2/AC3b $OUT/ac2_ac3b.json：syncStart=90951601 lastSyncEnd=92000177（span=1048576=W）e2eCatchupMs=4554 continuityOK=true
#   AC3a 压缩 $OUT/ac3a-compress.json：inflatedBytes=4981248/binaryWireBytes=118109 → ratio≈42.17x notices=[]（0 溢出）
#   AC7 $OUT/ac7.log：slow-write id=a71f150f33d2 blockMs=5000；conn-closed id=a71f150f33d2 maxWriteBlockMs=5000（同 daemon env=5000，与 round4 blockMs=5002 互证）
```

## netns 隔离与复原证明（宿主零改动）
- 建立：`ip netns add mwacc` → `ip netns exec mwacc ip link set lo up` → netem on netns `lo`。daemon + harness 均 `sudo ip netns exec mwacc …` 空间内运行；daemon RSS 经宿主 `/proc/<pid>/status` 采样（ip netns 不隔离 PID ns）。
- 复原：kill daemons（按 exe，避自匹配）→ `ip netns del mwacc` → `ip netns list` 空；宿主 qdisc **前==后** `qdisc noqueue 0: root refcnt 2`（diff 空）。
- 说明：`net.core.wmem_max` 非 netns 作用域（全局，收窄被拒），故 kernel 发送缓冲维持 4MB——正是 app 队列需持续 flood 才溢出的根因（非缺陷）。

## harness 侧修正（非产品代码；供复现）
1. **须发 resize 触发握手**：`completeHandshake()` 仅首条 `{"type":"resize","cols":>0,"rows":>0}` 或 5s timer 调用。
2. **read deadline 1s→30s（E1）**：netem 下帧（含 64KB 握手帧@800kbit≈1s、多实例拥塞时更长）须整帧到达；短 deadline 会在帧中途 abort → 下次 `ReadMessage` 从 payload 中段误判 `data frame before the final continuation`。**此 abort/desync 即早先"5 路活体"失败的真因**；放宽至 30s（`-read-deadline-ms` 可调）后评审 B 复现 5/5 全绿。
3. **`drainInflate` 硬上限 400ms**：closing sync 处对 cursor 做**追平等待（400ms 封顶）**后再快照 inflate；持续 flood 下 counter 永不"稳"，原无限轮询会死循环，加墙钟上限封顶（返工期实测修复）。
4. **`continuityOK` 求值前置修正**：以 opening-sync 为基点、closing-sync 处 `drainInflate` 后快照 `replayInflated`，判 `base+replayInflated+dropped==closing offset`；去掉 `ReadyCompress` 门与全量 inflated 误判。
5. **module 命名** `myworktree/tools/acceptance`；**replace 深度** `../../../../..`（五级到仓库根）；**netem 语法** 去 `buffer`。
6. **JSON 体经文件传**（`--data-binary @file`，python 字面 heredoc 生成）：规避嵌套引号致 curl body 损坏。

## 卫生（本轮）
- 删除遗留二进制 `tools/acceptance/acceptance`（5.9MB ELF，`go build .` 未加 `-o` 产物）；构建改显式 `-o /tmp/acc-harness`。
- 统一目录权限：源 `main.go`/`go.mod` `0644`、脚本 `run-netem.sh` `0755`。

## 限制与仲裁结论（非缺陷）
- **消费者排空速率持续低于生产速率（无论是否 netem）时，写阻塞可触 15s 写 deadline 回收——设计行为**（Q1 裁定；`MYWORKTREE_TTY_WRITE_TIMEOUT_MS` 可调）：决定因素是**消费者排空 vs 生产速率**，不是 netem 是否存在。只要消费者排空持续跟不上生产，server 的 `WriteBinary` 即长时间阻塞，per-write deadline 到期后回收该连接属**刻意保护路径**，非缺陷。**评审 B 档内反例**〔探针工件未入库（评审 /tmp），与本文「工件未留存」口径一致〕：240/340KB/s 生产 + 300ms 慢读、**无 netem** 即 `blockMs=15001`（证明无需 netem，慢读即可触发）；其 4MB/s 案例中客户端**在读但读得慢**（原文"读不进/socket 楔死"表述订正为"读得慢"——慢读即可，非完全不读）。此与 AC3c「所引运行（消费者跟得上）误杀=0」为不同档：前者消费者排空<生产→设计回收，后者消费者排空≥生产→无误杀。
- **raw-WS harness 无法活体逼出 wire-notice 到达计数**（AC1a→PARTIAL）：回环 kernel 发送缓冲（auto-tune，MB 级）吸收写出（`maxWriteBlockMs=0`、写 713KB 不阻塞），app 队列不填满→不触发 drop；停读使 server 真阻塞时单 goroutine handler 卡 `WriteBinary`→notice 卡 backlog。服务端"生成"已确证（`drop-oldest, connection kept` + `overflow notice pending (wire rate 250ms)`）；≤4/s 由常量推导；帧到达由 pin 覆盖。**限制与 pin 覆盖互补，无缺口**。
- `inflateErr=unexpected EOF`：server deflate 逐帧 sync-flush、**从不 finalize** 的预期产物，**非损坏**。

## 其他观察（**另立工单，不属本修复 #114**；评审 B 发现）
- **`gitx.GitRoot` 把 2s 超时误报为「非仓库」**：`internal/gitx/gitx.go:14-20` 中 `GitCommand(2*time.Second, dir, "rev-parse", "--show-toplevel")` 若因**超时**（而非真非仓库）返回 err，一律 `errors.New("not a git repository (or git not available)")`，诊断性弱、误导归因（本轮 round3 即被其误导）。建议区分"超时"与"确非仓库"两种 err。
- **`GitCommand` 的 `CommandContext` 在孙进程持管道时不及时返回**：`internal/gitx/branches.go:54-62` 已注释"WaitDelay 只限制取消后的 shutdown，不能单独终止 hung git"；但当 git 的子/孙进程持有 stdout/stderr 管道时，`ctx` 到期后 `cmd.Output()` 仍可能阻塞至孙进程释放管道 → 2s 超时形同虚设。建议显式设置 `Cmd.WaitDelay` 或强制 kill 进程组。
- 二者均为**独立可诊断性/健壮性议题**，与本 issue #114 TTY 溢出修复无关，标记另立工单，**不在本次验收范围**。

## 缺陷清单
- **产品缺陷：无。** 全程未见 1013 误发、cursor 错位（continuityOK=true）、goroutine/socket 泄漏、数据损坏。**误杀口径**：AC3c 所引运行内误杀=0；AC5 档 5 路写 deadline 回收按 Q1=**设计保护路径**（若按工单"误杀"字面定义则计 5 次，此处**口径已声明**——消费者排空持续低于生产速率，回收为设计行为，非误杀）。
- **测试 flake（既有，非本修复引入；已由 `4b0525b` 修复）**：`internal/app/tty_ws_compress_test.go:388` `TestHandleInstanceTTYWS_MatrixCompressOverflowDeflate1013` 曾间歇失败（评审 B 观测 ~7.5–15%，自提交 `d2fc415` 起既有）。**已由 `4b0525b` 修复——在 overflow flood 前 pin 一个确定性同步点，断言零弱化**；修复后全仓复跑 `go test ./... -count=1` **EXIT=0（internal/app 34.1s——评审 B 实测；本 subagent 亦复跑 EXIT=0）**。修复由并行 agent 落地，本 subagent 未触碰 `internal/`。
- 工单勘误 2 处（`replace` 级数、netem `buffer`→`limit`）；批准偏离 1 处（module 命名）；AC1a 依仲裁降级 PARTIAL。
- **判据解释（Q3 仲裁授权）**：AC5 RSS 判据按仲裁授权解释为 **per-conn 连接/队列分量**（每连接增量、基线扣除；per-instance ring 为既有内存不计入）——与 FIX-PLAN §五.5 v7 注记同口径。
