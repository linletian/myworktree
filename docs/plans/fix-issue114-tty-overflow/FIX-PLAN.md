# Issue #114 修复计划（FIX-PLAN v7）—— tty WS subscriber overflow 1013 重连循环

仲裁者定稿 2026-10-14；v2 修订 2026-10-17（生产日志证据）；v3 修订 2026-10-17
（跨境慢链路适配）；v4 修订 2026-10-17（双评审闭环第一轮）；v5 修订 2026-10-17
（双评审闭环第二轮）；v6 修订 2026-10-19（双评审闭环第三轮：REVIEW-v5-TEMP
V5-N1–N4 + REVIEW-B 第三轮 R3-MED-1/2、R3-LOW-1/2/3、R3-nit——全部实测核验后
处置；本轮为文档同步与数字可复现性修正，无方案性变更）；**v7 修订 2026-10-19
深夜（Phase A 服务端落地后 §四.0 状态表回填；另采纳评审 B round2 实证订正：
回放在活生产者下存在部分读（ReadSince 至多 maxBytes），streamed 非恒 64KB
整数倍——读长裁剪在 W_eff==budget 路线可触发并将预算路线收紧到精确 8MB；
budgetExhausted=true & windowed=false 组合在「W_eff==budget 且 clamp 未触发且
生产者跑赢回放」下可达，windowed 语义冻结为两个窗口截断的 OR。无方案性变更）**。
本计划对编码/评审 Agent 生效：可以质疑挑战（回报给仲裁者），**不得自行修改本文件**。
行号基线：2026-10-17 工作树（含未提交 Phase A 编码）。

## 修订记录

- **v1（2026-10-14）**：初版。决策 4（回放背压/限速）以「先看 tty-metrics 数据」为由推迟。
- **v2（2026-10-17）**：数据已到（52 次 overflow 风暴）。决策 4 重写为窗口化回放；
  否决 pacing（回放不经订阅队列）；新增 1b（drop 队列 1024）、退避锚点改 closing sync。
- **v3（2026-10-17）**：跨境适配评审。新增决策 5（压缩/W 调参/deadline 可配）；
  1b 上调 4096；netem 验收。
- **v4（2026-10-17）**：两份评审闭环。核验结论：**除 REVIEW-B 多设备对数 12 vs
  实测 11 的口径差（同秒 >1 计数法差异，两者均远超文档原记 6）外，全部发现成立**。
  主要变更：
  1. **证据订正（R1/R8/LOW-3，阻塞）**：replayMs 最大值 1029ms→**84656ms**
     （18:35:43，1.5MB 回放）；overflow 53 次（≤19:22 窗口 52）；亚秒再溢出
     14→**19** 个；handshake 76→**77**；slow-write 25→**26**；多设备同秒对
     6→**11** 组；补记 9ba7f2ee8964、b4308f9af270 两实例各 1 次溢出。
     新增计数口径（窗口 ≤19:22:00、实例过滤、注释头排除）。
  2. **决策 4 补总量封顶（HIGH-2，阻塞）**：W 同时约束回放起点**与已流出总量**
     （`streamed ≥ W_eff` 即 break，复用预算截断语义）。走查证实 ReadSince 追
     活 head、循环只靠 8MB 预算终止——持续缺口下回放时长与 W 无关（可达
     160s@50KB/s），AC3(b) 原表述不可达。日志佐证：17:09:39 published 超
     headAtStart ~500KB（回放跟随 live head 前移）。
  3. **决策 2 锚点加稳定期（MED-1）**：重置条件 = closing sync **且连接存活
     ≥60s**；notice→tail 重连的 closing sync 不再清零退避。前台可见连接退避
     上限 8s、后台 30s（R10）。
  4. **决策 1 补 notice 线上限速（R5）**：wire notice 按 250ms 区间聚合
     （增量=区间Σ），首次即发；客户端语义不变。
  5. **决策 5a 补五个可实现性 pin（R3）**：压缩率前置测量门（Phase A-0）、
     DecompressionStream('deflate-raw') 基线与降级、term.write 回调背压纪律、
     丢弃标志逐输出检查、解压炸弹守卫（maxDecompressedBytes）。
  6. **R2**：deadline「慢即死」二元谬误成立 → 保留 15s 默认+env，AC3 增
     「误杀数=0」指标；慢链路心跳放宽档 15s/60s（caps/env，默认不变，
     ORCA 15s/75s 参照）。
  7. **R6**：epoch 升级为 **generation + fencing** 语义（ORCA 对照）；协议
     契约显式写「notice = 客户端自杀式重连，单次代价=一次 tail+W」。
  8. **R7**：W=1MB 定性为「线损预算」非行数等价；客户端排空估计改用
     rxBytes/aliveMs 或 replayBytes/replayMs（弃 connWriteWait）；截断段
     「重连可用」加「仍在 ring 内」限定。
  9. **R12/R13/R14/R15/R16**：caps 四组合矩阵、env 命名 Phase A 冻结、
     wireBytes/logicalBytes 双字段、caps k=v 前缀解析+单测、新客户端→旧
     服务端降级回归。
  10. **HIGH-1**：新增 §四.0 实现状态表（1b/4/5a/5c 计划有、工作树未落地；
     1/3 已实现未提交；2 未开工）；Phase A 单测「1024 chunk」残留订正为 4096。
  11. **R9/LOW-1**：DATA-COLLECTION 决策矩阵第二行加修订注记；补节流与
     compress wire 字节语义（本次已直接修订该文档）。
  12. **MED-2**：Phase B 增补客户端设界（待解压/待写字节上限 1–2MB，超限
     本地 reset+弃连，与服务端 drop 同构）。
  13. **MED-3 仲裁记录**：维持 reset+tail 默认（保 skip-to-live、客户端简单），
     备选已拆为「跳过/补回」两变体分别记录（v5 订正原混述）；API.md 写明
     「notice 会清空客户端 scrollback」；notice 时 UI 显示可见
     降级提示（ORCA host-health 可见性借鉴）。
  14. **R4 仲裁记录**：4096 默认保留（v4 引用的「回放量验证」论证范畴
      错误，v5 已重写为缺口算术——见决策 1b）；env 可下调；RSS 上界
      4MB×N 写入验收。
  15. **验收**：订正编号重复（Phase B 两个「4.」、验收两个「5.」）；netem
     rate pin 内核参数（buffer/limit）+ 应用层限速双轨；AC(b) 有效带宽以
     服务端 handshake replayMs 实测（wire 口径，compress 用 logicalBytes）。
  16. 外部参照：ORCA（stablyai/orca）调研结论并入（水印回滞+wedged、
     resume 确认、generation+fencing、无假就绪、read-mostly 快照、15s/75s
     心跳）——详见 `ORCA-RESEARCH-2026-10-17.md`。
- **v5（2026-10-17 晚）**：第二轮双评审闭环（REVIEW-v4-TEMP N1–N8 +
  REVIEW-B 第二轮 MED-A/MED-B、LOW-C–F）。**判定：除「多设备对数口径」外
  全部成立，无一驳回**（两评审独立收敛于同一组修复；A 独有 N1/N3，B 独有
  MED-A 归属数据与 LOW-C 基线实测）。主要变更：
  1. **计数口径二次订正（MED-A/N4，实测确认 6 处）**：窗口统一为
     15:04–19:22:59；slow-write 归属 26=17+8+1（docs 窗口 14）；17,666ms
     串实例订正；1,981/1,818 移出 >2s 列；subAge 最大 1,919,661/窗口
     1,505,006；尾段「多实例混合含 docs 自身」订正；日志范围 14:17:29–
     19:57:02。
  2. **决策 1b 仲裁句重写（N1）**：v4「3.96MB 回放量验证 4MB live 队列」
     系范畴错误（回放不经队列）——重写为缺口算术：实测 drain 分布
     （n=52：p25 6.4 / 中位 47 / p75 364 KB/s）、storm 生产中位 ~26KB/s
     （burst 150–358KB/s）、4MB→burst 缺口下 cadence ~28–39s、64KB→~0.6s
     （与实测亚秒再溢出吻合）；并明确「无固定队列能为病态 drain 保证
     cadence——压缩是杠杆、队列只调典型消费者节奏」。
  3. **备选 resume 算术拆分两变体（N2/MED-B）**：跳过变体（since=cursor+Σ：
     无重复无错位、丢弃段确认丢失+洞永久、回放短）与补回变体（since=首个
     notice 时的 cursor 快照：无洞无重复、丢弃段从 ring 补回、回放多付
     W/带宽、要求首 notice 即快照+fence）；「Σ 滞后无害」订正为「跳过
     变体下 Σ 滞后→since 偏小→有界重复绘制（违反 #87 不重复 pin）」。
  4. **maxDecompressedBytes 定义澄清（N3）**：v4「每连接累计 W+4MB」在
     150–300KB/s 生产下 20–30s 耗尽=30s 定时杀器——改为**单帧解压后上界**
     max(W_eff, 4MB)（合法 1KB live 帧永不触及；64KB 回放帧 >64:1 膨胀率
     才触发=退化/损坏流）；明确不做生命周期累计；超限=关连接+tail 重连。
  5. **退避锚点计时基准（N7/LOW-D）**：由「连接存活 ≥60s」改为「**closing
     sync 之后存活 ≥60s**」（与 W 解耦）；声明 1b 组合下 cadence>60s 时
     退避恒 base 为**设计行为**（队列已把 reset 压到 ≥28s，无风暴风险）。
  6. **pin 2 基线订正（LOW-C，caniuse 实测）**：deflate-raw = Chrome
     **103+**/Edge 103+/Firefox 113+/Safari 16.4+（全局 96.12%）；维持
     raw deflate（zlib 包装备选记录在案）。
  7. **MED-4 回填落任务（N5/LOW-F）**：Phase B 增第 8 项（客户端埋点回填）。
  8. **AC3 操作化（N6）**：补「误杀」判定定义（handshake 成功+持续
     WriteBinary 进展+单写超 15s 被回收）；AC3(b) 公式订正为
     `W_eff(logical)/（replayBytes/replayMs 实测带宽）`。
  9. **DATA-COLLECTION 矩阵第 3/5 行**划线注记（LOW-E）。
- **v6（2026-10-19）**：第三轮双评审闭环（REVIEW-v5-TEMP V5-N1–N4 + REVIEW-B
  第三轮 R3-MED-1/2、R3-LOW-1/2/3、R3-nit）。**判定：全部成立，无一驳回**
  （三评独立复算出 v5 三处数字不可复现，属我的复算错误）。变更：
  1. **R3-MED-1/V5-N1（5a 编码前置）**：Phase A-3 的
     `maxDecompressedBytes`（W+4MB）每连接累计句**删除**——v5 pin5 已判明
     该守卫是**客户端消费侧**（解压后大小服务端不可见；压缩前逻辑帧结构性
     ≤64KB，服务端无可检查之物），任务行留旧口径=给编码者两份相反指令。
     单帧解压炸弹守卫落为 **Phase B-9**（与 B-5 待消费界的关系写明：
     单帧界防炸弹、待消费界防积压）。
  2. **R3-MED-2/V5-N3（数字可复现性）**：决策 1b 缺口算术三处订正——
     「storm 生产中位 ~26KB/s」不可复现（26 系我偶数样本取错中位），改
     多口径表述：端点法时间平均 ~**17KB/s**（17:05:57→17:37:42，
     +33.0MB/1905s）、正区间速率中位 ~**75KB/s**、burst 150–358KB/s；
     「burst 缺口 103–144」混合基准错误（103=150−drain中位、144=150−
     drain p25），统一为 burst 生产 150–358 − drain 中位 47 =
     **103–311KB/s**；cadence 4MB→**~13–39s**（原 28–39 建立在无出处的
     144 上）、64KB→**~0.2–0.6s**（与 19 个亚秒再溢出互证保持）。
     **AC3(a) 改按验收实测缺口折算**（cadence ≥ 4MB ÷ netem 实测缺口），
     不绑定日志推导值（消除 netem 下 200KB/s 缺口=20s<28s 的假失败）。
     drain 分布补口径注记（最近邻秩、B/ms=KB/s、10^3 进制）。
  3. **V5-N2/R3-LOW-3**：Phase B-6 锚点行文补「自 closing sync 起」与决策 2
     逐字对齐；决策 2 删除失义论证（「60s 覆盖已观测上界 56s」——新计时
     基准下回放时长不计入 60s，该比较属旧锚点残余），保留驻顶设计行为
     声明；容量单位口径统一注明（MB=10^6）。
  4. **R3-LOW-1/V5-N4**：标题版本滞后（v4→内容 v5）订正为与修订记录同步
     （现为 v6）；DATA-COLLECTION §一 handshake 行「FIX-PLAN v4」→v6。
     （V5-N4③ §一.5 游离粗体标记经复核在 v5 已修复，无需再动。）
  5. **R3-LOW-2**：§一.2 生产定性同步为多口径表述（原「~150–300KB/s」把
     burst 当持续速率、高估约一个量级）；subAge 1245s 陈旧引用同步为
     809s/1,505s。EVIDENCE §二/§三、头部窗口措辞（15:04–19:22:59）同步。

## 一、问题回路（证据链：代码走查 + 2026-10-17 生产日志，计数口径见末）

**引擎 A（1013 扳机）：稳态生产 > 排空。**

1. `driver.go:301` PTY 单次读 1024B（chunk ≈1KB）；`driver.go:759/819` 订阅者
   队列 64 chunk ≈ 64KB；`broadcast()` 满则 delete+close+drain（#82）。
2. 生产实测（多口径，R3-MED-2 订正——原「~150–300KB/s」系把 burst 当持续
   速率、高估约一个量级）：17:05–37 风暴段端点法时间平均 **~17KB/s**
   （17:05:57→17:37:42，+33.0MB/1905s）；正区间（相邻溢出事件 headNow
   增量/间隔，n=21）速率**中位 ~75KB/s**、均值 ~101KB/s；**burst 秒级
   150–358KB/s**（15:04:15→16 一秒 +150,130B；区间最大 358KB/s）。
   外网排空 <生产时 64KB 队列秒级填满——与回放无关：重连后订阅刚建立即
   溢出（subAge<2000 共 **19** 个：196/247/374/443/476/480/490/576/586/696/
   860/880/1004/1005/1086/1091/1143/1594/1746）；已追上 live 的老订阅者同样
   被收割（subAge 809s、334s、1,505s≈25.1min）。
3. 多设备实锤：**11 组同秒 >1 溢出**（15:17:50、17:09:32、17:09:39、17:15:19、
   17:16:03、17:20:11、17:21:47、18:58:41、18:59:21、19:14:05（三连）、
   19:14:11）。各设备独立队列各自被收割。

**引擎 B（回放放大循环）：回放跟随活 head，时长只受预算约束。**

4. 重连 → 固定 5s 后 catchup 回放。**代码走查（v4 新增，HIGH-2 依据）**：
   catchup 循环（app.go:2771 `cursor := since` 起）逐 chunk `ReadSince(id,
   cursor, 64*1024)`，而 `RingBuffer.ReadSince`（ringbuffer.go:149）以
   **当时的 head** 为界——**回放目标不是握手时的 headAtStart，而是持续前移的
   活 head**。持续生产>排空时 cursor 永远追不上，循环只靠 8MB 预算终止。
   日志佐证：17:09:39 `headAtStart=21242409 published=21749078`（published
   超 headAtStart ~500KB，回放期间 head 前移被跟随）。**结论：只 clamp 起点
   封不住时长——W 必须同时封总量（决策 4）。**
5. 回放量实测：replayBytes 最大 **3,962,845**（17:35:56）；replayMs 最大
   **84,656ms**（18:35:43，1.5MB≈17.7KB/s 有效排空）；docs 实例 >2s 的
   replayMs 全量共 4 个（84,656/6,496/4,961/2,740；6,496 在 19:55:24 窗口外；
   17,666ms 属 b4308f9af270、1,981/1,818 本 <2s——v5 订正 v4 的串实例与误列）；
   77 次 handshake 全部 `budgetExhausted=false`（预算从来不是扳机，但它是
   唯一的时长兜底）。
6. 客户端 xterm `scrollback: 10000`（index.html:5453）≈ ≤1MB 可渲染量：
   ≥70% 回放字节是客户端到手即弃的垃圾（线损预算口径，见决策 4 语义边界）。

**放大器**：`app.go:2388 ttyWriteTimeout=15s`（const，已实现但未 env 化）；
生产日志 19:11:15 `maxWriteBlockMs=544599`（**单次写阻塞 ~9.1 分钟**，
slowWrites=1）——`writeFrame` 持 `c.mu` 无 deadline（修复前）/持 deadline
但 15s 前写卡死则心跳 ping 一并拖死，客户端 30s liveness 收割半开连接。

**结果**：突发生产超标 → 64KB 队列 <1s 填满 → 1013 → 5s → MB 级回放
（外网 10–80s、屏幕近乎冻结）→ 订阅建立 → live 立刻再灌满队列 → 循环。
多设备同时在场时各自独立重复该循环。

**计数口径（v4 新增 R8，v5 修订：二评 MED-A/N4 复算确认，统一边界；v6 补单位口径 R3-LOW-3）**：
以 `mw-tty2-2026-10-17.metrics.log` 为准；排除第 1–5 行注释头；实例过滤
`1d13eeebd40b`（docs）。**窗口 = 15:04–19:22:59**（风暴段：末次溢出
19:22:42；19:23:05 起为零星单发，19:55 尾段为多实例混合**含 docs 自身**——
v5 订正 v4「尾段属其他实例」之误）。全量 vs 窗口：overflow **53/52**；
handshake **77/75**；slow-write 全库 **26 = docs 17（窗口 14）+
b4308f9af270 8 + 9ba7f2ee8964 1**（v5 订正 v4「1 条属其他实例」之误）；
docs subAge 最大 **1,919,661ms（~32min，19:55:05，窗口外）**，窗口内最大
**1,505,006ms（~25.1min，18:58:41）**（v5 订正原记 1,245,088 既非总量亦非
窗口最大值）。另两实例各 1 次溢出（19:55 后）。日志实际范围 14:17:29–
19:57:02（EVIDENCE 头部同步订正）。
**单位口径（v6，R3-LOW-3）**：KB=10^3 B、MB=10^6 B（1MiB=1.049MB）；
速率 KB/s 为 10^3 进制；drain=liveBytes/subAgeMs（B/ms，数值即 KB/s），
百分位取**最近邻秩**（线性插值会得出不同小数，复算以最近邻秩为准）；
队列 4096×1024B=4,194,304B≈4.19MB（习称 4MB）。

## 二、设计决策（仲裁结论，每条附证据）

**决策 1：drop-oldest + in-band 通知，cap 门控，legacy 维持 1013。**（v1–v4 不变，v4 补三条）
- 引擎 A 直接解药。证据：#82 + 引擎 A 机理；live chunk 无 offset，客户端按
  已收字节推进 cursor（#87），静默丢中段永久错位——drop 必须配 notice。
- legacy 客户端：1013 路径逐字保留，#82 钉住的测试不改预期。
- **v4 补 1（R5，notice 线上限速）**：wire notice 按 **250ms 区间聚合**发送
  （首次 drop 即发；后续每 250ms 最多一帧，计数=区间Σ增量）。推导：被 drop
  的 chunk 恒为最旧未交付（队首），Σ增量语义下客户端累计账目仍精确；区间
  聚合只把 reset/resume 决策延迟 ≤250ms，两种响应策略（reset+tail /
  cursor+Σdropped）均安全。150KB/s×1KB chunk 下每 chunk 一 notice（~150 帧/s，
  各一次 `writeFrame(c.mu)` 与 binary 争锁）正是风暴期自激源。
- **v4 补 2（R6，明确定性）**：「连接不死」= **从服务端踢改为客户端自杀**：
  服务端不下 1013，客户端 notice→reset→主动弃连→tail 重连，**单次代价仍是
  一次 tail+W 回放**。AC1(b)「不再出现 1013 重连循环」不得误读为「不再重连」。
- **v4 补 3（R6，fencing）**：Phase B  epoch 纪律按 ORCA（control-continuity）
  升级为 **generation + fencing**：每连接单调 generation；notice/reset/重连
  即 bump；旧 generation 的迟到帧（WS 缓冲残余、解压管道在途输出、xterm
  回调、迟到的 notice/控制帧）**一律 fence 丢弃**，非「尽力检查」。可达界：
  epoch 检查点到 xterm 内部之间至多一个在途 chunk。

**决策 1b（v2 新增，v3 上调，v4 仲裁维持）：drop 队列 64 → 4096 chunk；close 策略保持 64 逐字不变。**
- 边界：PTY 读 1024B → 4096 chunk = **4MB/订阅者精确上界**。
- **v5 仲裁记录（R4/N1，v4 论证范畴错误已订正）**：4096 默认保留。v4 引的
  「最坏 backlog 3.96MB < 4MB ⇒ 完整吸收风暴」系**范畴错误**——3.96MB 是
  catchup **回放量**（ring→socket 直写，不经订阅队列，HIGH-2 已自证），
  队列吸收的是 **live 生产−排空缺口**，两者无换算关系。正确验证（缺口
  算术，当日实测；口径注记：KB/s 为 10^3 进制，drain=liveBytes/subAgeMs
  即 B/ms，百分位取**最近邻秩**，n=52）：
  - 实测 drain（52 个 docs 溢出连接）：p25 **6.4** / 中位 **47** /
    p75 **364** KB/s，全域 0.6–2,141KB/s——消费者速率极度分散；
  - storm 生产（多口径，R3-MED-2 订正）：端点法时间平均 **~17KB/s**
    （17:05:57→17:37:42，+33.0MB/1905s）；正区间速率**中位 ~75KB/s**、
    均值 ~101KB/s（n=21）；**burst 秒级 150–358KB/s**。即持续生产低于
    典型 drain、缺口是 **burst 性**的；
  - burst 缺口（一致基准：burst 生产 − drain 中位）= 150–358 − 47 =
    **~103–311KB/s**（原「103–144」混合基准，已订正）；
  - cadence 算术（稳态口径，实际触发是瞬时不齐）：4MB 队列在 burst
    缺口 103–311KB/s 下 ≈ **~13–39s**；64KB 队列同缺口 ≈ **~0.2–0.6s**
    ——与实测 19 个亚秒再溢出互证；对 p25 drain（6.4KB/s）连接，持续
    缺口 ~11KB/s（17−6.4）→ cadence ~6min——那正是消费降级
    （reset+tail）的场景；
  - **结论**：4MB 把典型消费者的 reset cadence 从亚秒拉到 **~13–39s
    量级**（burst 缺口口径），容量选择有算术支撑；但**无固定队列能为
    病态 drain（p25 6.4KB/s 乃至 0.6KB/s）保证 cadence**——那是消费者
    根本跟不上，答案是 reset+tail（消费降级），**压缩（5a）才是把缺口
    翻转的杠杆**（wire 排空 ×压缩比 = 逻辑 drain 提升）。env 可下调；
    压缩成立后典型消费者实际占用趋近于 0。
- close 策略容量不变 → #82/#87 回归零改动。

**决策 2：指数退避 + jitter；重置锚点 = closing sync 之后存活 ≥60s；前台可见连接上限 8s。**（v2 修正，v4 再修正，v5 计时基准修订）
- base 1s ×2，±25% jitter。**隐藏/后台连接上限 30s；前台可见连接上限 8s**
  （R10：用户盯着屏幕，30s 黑屏不可接受，手动刷新可用）。
- **v4 锚点修正（MED-1，实测确认）**：closing sync 对 syncCap 客户端在
  **tail 模式也发送**（app.go:2996-3000）——v2 的「closing sync 即重置」
  会被 notice→tail 重连（1.3s 即达 closing sync）**每轮清零**，退避形同
  虚设。新锚点：closing sync 到达 **且该连接自 closing sync 起存活
  ≥60s**（v5 计时基准修订，N7/LOW-D：从连接真正可用起算、与 W 解耦；
  覆盖回放中段断连与 notice 循环两种清零路径）。ORCA resume 确认
  （confirmDeadline）同构。
- **锚点覆盖边界的诚实表述（v5 立，v6 删失义论证 R3-LOW-3）**：新计时
  基准下 60s 自 closing sync 起算、**不含回放时长**——故 v5 原「60s
  覆盖已观测回放上界（1MB@17.7KB/s≈56s）」属旧锚点语义残余，**删除**
  （56.5s 为 10^6 口径；1MiB 口径 59.2s，本文档容量单位统一
  MB=10^6、KB=10^3）。保留：极端慢链路/env 放大 W 下 notice 循环
  cadence（1b 队列 burst 缺口口径 ~13–39s）叠加退避增长后，驻顶 30s
  为**设计如此**（队列已把 reset 节奏压住，无风暴风险），**不得当故障
  排查**。
- overflow notice 触发的 tail 重连**计入**退避序列（v1 仲裁不变，现由稳定
  锚点真正生效）。
- jitter 的 `Math.random` 须过 node-test 切片规则（R10③）。
- 定位不变：退避是安全网，引擎 A 由 1+1b 解、引擎 B 时长由 4 解。

**决策 3 / 5c：write deadline 15s 默认 + env 可调；「慢即死」二元谬误已修正认知。**（v3 修正，v4 补）
- 已实现：app.go:2388（const）、ready 后武装（app.go:2562）、teardown 前卸除。
  服务端 15s < 客户端 30s 次序健康。
- **v4（R2）**：承认「>15s 即链路已死」是二元谬误——50KB/s+2%loss 下
  64KB 单写 >15s 而链路可恢复完全可能，误杀→重连→回放→再误杀形成
  **deadline 诱导的新循环**，正打在跨境场景。处置：(a) 默认 15s + env
  上调（30s）；(b) **AC3 增「deadline 误杀数=0」指标**（netem 下健康慢
  连接被 deadline 回收次数必须为 0，flapping 只允许来自 client-liveness）；
  (c) 慢链路心跳放宽档：**ping 15s / stale 60s**（caps `slow-link` 或 env，
  默认 10s/30s 不变；ORCA relay 15s/75s 参照，5:1 比对 3:1 更适合
  300ms RTT+丢包）。

**决策 4（v2，v4 补总量封顶）：窗口化回放——W 同时约束起点与已流出总量。**
- **起点 clamp**：catchup 分支 `cursor := since` 之后 `head−since > W` 时
  clamp 到 `head−W`（复用 #94 start-sync 既有 clamp 路径，客户端零感知）。
- **总量封顶（v4，HIGH-2 修复，阻塞）**：回放循环现有预算 guard
  （app.go:2814-2833 `streamed >= ttyHandshakeReplayBudget`）增加更紧的
  `streamed >= W_eff` 终止条件，**语义逐字复用预算截断**：截断、endOffset=
  最后已写字节、closing sync 照发（发布截断点非 head）、未交付段在 cursor
  之前——「truncated, not skipped」，下次重连可重取（仍在 ring 内时）。
  二者取严：起点封顶 + 总量封顶 ⇒ **回放时长 ≤ W_eff/有效带宽**，
  与 backlog 大小无关。8MB 预算退化为 W 之上的硬兜底（W ≤ 预算恒成立）。
- W 默认 1MB（env 可配，决策 5b）；跨境 50–100KB/s 推荐 256–512KB。
- **语义边界（v4 修正，R7）**：① W 是**线损预算**而非「=scrollback 行数
  等价」——ANSI/宽字符下字节↔行非线性，1MB 是工程估值（10000 行×~100B），
  不是可证等价；② 被截断段「后续重连仍可用」加限定：**仍在 ring 内时**
  （与 ring 淘汰叠加时不成立）；③ W=0（纯 tail）永远安全；W<scrollback
  容量 → 重连可见历史变少（UX 取舍非损坏）；W>scrollback 容量 → 纯线损。
  不变量：scrollback 配置上调时 W 必须同步复核。
- closing sync 发布真实终点（截断时=截断点），cursor 语义不变（#87）。

**决策 5（v3 新增，v4 补 pin）：跨境慢链路适配（RTT 200–400ms / 50–200KB/s / 丢包 1–5%）。**

**5a：app-layer 压缩（caps `compress` 门控，我方客户端默认声明）。**（v4 补五个 pin，R3）
- 现状：`ws.go:70-105 Upgrade` 手写 WS 不协商 permessage-deflate，RFC 7692
  成本高风险大，不做；走 app-layer：服务端每连接持久 `compress/flate`
  （BestSpeed）流、每帧写后 `Flush()`（sync flush）；客户端每连接持久
  `DecompressionStream('deflate-raw')`，单流 `pipeTo` 按序 `term.write()`。
  TEXT 控制帧不压缩；回放与 live 共用同一流。
- **pin 1（前置门，阻塞）**：压缩率**先测量再编码**。Phase A-0：用
  `/api/instances/log` 对 docs 实例采样数 MB 真实输出（含 ANSI），离线过
  `compress/flate` BestSpeed + 每 1KB sync flush，实测比率。**<2x 则 v3
  主收益坍塌**——压缩降为 Phase C 候选，W/队列参数按无压缩重算。文本日志
  预期 3–5x，但 docs 实例输出构成（ANSI 占比、base64 块）未测。
- **pin 2（基线与降级，v5 订正 LOW-C：caniuse 实测）**：`deflate-raw`
  **格式**基线 Chrome **103+**/Edge 103+/Firefox 113+/Safari 16.4+（全局
  96.12%；Chrome 80+ 只是 API 对 gzip/zlib 的基线）；缺失时**不声明
  compress → 明文路径**（优雅降级 + console 注记），本期不做 WASM inflate。
  备选：改 zlib 包装 `'deflate'`（`compress/zlib`，每帧 Flush 语义相同，
  +2 字节流头）可覆盖 Chrome 80+——本期不做，格式名变更会波及 5a/契约/
  测试，留档。
- **pin 3（保序与背压）**：`term.write(data, cb)` 是回调式异步，
  WritableStream 的 `write()` 必须 `await new Promise(res =>
  term.write(data, res))` 后才 resolve——否则「单流保序」不成立且无解析
  背压。node-test 切片规则适用。
- **pin 4（丢弃标志逐输出检查）**：notice（TEXT）同步置位；解压管道**每一
  输出**（含已入队未输出）写入前查标志。单测 pin。
- **pin 5（解压炸弹，v5 定义澄清 N3）**：`ws.go maxMessageBytes=10MB` 只
  限 wire 单帧。守卫 = **单帧解压后字节上界 maxDecompressedBytes =
  max(W_eff, 4MB)**：合法 1KB live 帧解压后远低于界；64KB 回放帧需
  >64:1 膨胀率（deflate 上限 ~1032:1，仅退化/损坏流可达）才触发。
  **明确不做连接生命周期累计**——150–300KB/s 生产下任何 MB 级累计上限
  都会在 20–30s 耗尽，等于给压缩长连接装定时杀器（正是评审担心的）。
  与 1b 队列的关系：队列是服务端逻辑字节缓冲（生产侧），本 guard 是
  客户端解压后上界（消费侧），两者独立。超限处置=关连接+客户端 tail
  重连（与 notice 路径同构）。回放窗口+live 在途的「待消费」上界由
  Phase B-5 客户端设界（1–2MB）覆盖，不重复计数。
- **双字段埋点（R14）**：compress 连接上 slow-write/conn-closed 增
  `wireBytes`（压缩后）与 `logicalBytes`（解压后）双字段——消费速率读数
  口径随连接而变，双字段保历史可比。
- 兼容不变量（v3 已论证，v4 保留）：被 drop 的 chunk 从未入压缩流（压缩在
  写时、每 chunk 恰一次、按写出序），两端上下文始终一致；notice→reset→
  重连=新流。cursor 按**解压后**字节推进（ring offset 同为逻辑字节，#87
  不受影响）。

**5b：W 可配置与跨境调参。**（v4 修正）
- env 覆盖（命名 Phase A 冻结，R13，与 `ttyWriteTimeout`、队列容量三者一并
  定名并写入 API.md）。跨境推荐：50–100KB/s → 256–512KB。
- caps `replay-window=<bytes>`（Phase B 可选）：**caps k=v 需前缀解析**
  （R15：`ttyClientHasCap`（app.go:3981）是全 token 相等匹配，`replay-window=1024`
  不匹配 `replay-window`）——新增 `ttyClientCapValue(caps, name)` 前缀解析器
  + 单测（`name=v`、`name=` 空值忽略、`nameX` 不匹配、重复 caps）。W_eff =
  min(默认 W, 声明值)，只许缩小（防 DoS）。
- **客户端排空估计口径修正（R7②）**：弃 `connWriteWait`（xterm 解析积压，
  度量 CPU 非网络）；改用 `rxBytes/aliveMs`（conn-closed 既有字段）或
  handshake `replayBytes/replayMs`（服务端实测，含 wire/logical 口径），
  DATA-COLLECTION 注明。
- 服务端按实例自适应 W = 未来项（本期不做）。

**5c：deadline 与心跳可配。** 见决策 3/5c（15s 默认+env；慢链路 15s/60s 档）。

**新：回滚面（R16）。** 新客户端→旧服务端：未知 caps（`overflow-notice`/
`compress`/`replay-window`/`slow-link`）被旧服务端忽略（白名单门控=
legacy 行为），连接照常、无新语义。单测 pin「未知 caps → legacy 路径」。

## 三、协议契约（v4 增补：限速 notice、四组合矩阵、fencing、备选 resume 算术）

- caps 握手新增：`overflow-notice`、`compress`、可选 `replay-window=<bytes>`、
  可选 `slow-link`（前两者独立门控可组合；k=v 参数走前缀解析，R15）。
- 服务端→客户端控制帧（TEXT）：`{"type":"overflow","droppedBytes":<int>,
  "droppedChunks":<int>}`——**增量语义，按 250ms 区间聚合**（区间Σ；首次
  即发）；仅发给声明 `overflow-notice` 的客户端；先于所涉 chunk 写出。
- 客户端白名单（index.html:6038）新增 `"overflow"`；收到后：console 日志 →
  `term.reset()` → **主动关闭 socket**（reset 后残留字节不得上屏——
  generation+fencing 收编三层残余：WS 缓冲/解压管道/xterm 回调）→ 不带
  `since` 的 tail 重连，**计入退避序列**（60s 稳定锚点下真正生效）。
- **notice 的 UX 语义（v4，MED-3 仲裁）**：API.md 写明「notice 会清空客户端
  scrollback」；notice 时终端 UI 显示一次可见降级提示（「流已重置：链路慢，
  有数据丢弃」状态行；ORCA host-health 可见性借鉴）。
- **caps 四组合矩阵（R12）**（wire 语义与 cursor 口径）：

| caps | 溢出行为 | 二进制 wire | cursor 口径 |
| --- | --- | --- | --- |
| 无（legacy） | 1013 关闭 | 明文 | wire=logical |
| `overflow-notice` | notice+drop，连接保留 | 明文 | wire=logical |
| `compress` | 1013 关闭 | deflate-raw | logical（解压后） |
| 两者 | notice+drop，连接保留 | deflate-raw | logical（解压后） |

  四种组合各有单测；legacy 行为零变化单测 pin。
- **备选 resume 两变体（v4 记录，v5 拆分订正 N2/MED-B——v4 原段落把两个
  变体的性质混于一条公式，已拆开）**：
  - **变体 A（跳过）**：`notice → 弃连 → since = cursor + ΣdroppedBytes`。
    推导（成立）：客户端 cursor 按已收字节从 sync 基点累加（#87），
    cursor + Σdropped = 下一未交付字节的 ring 位置——**无重复、无错位**；
    即使 notice 延迟/聚合，只要 Σ 累计（各 notice 增量之和）仍精确，
    resume 位置仍正确。性质：丢弃段**确认丢失**、scrollback 中段**洞
    永久**；回放量 = 队列在途+live（短）。**Σ 滞后（漏 notice）→ since
    偏小 → 落入已接收区间 → 有界重复绘制**（违反 #87「不重复绘制」
    pin——v4 原「无害」表述错误，已订正）。
  - **变体 B（补回）**：`notice → 弃连 → since = 首个 notice 到达时的
    cursor 快照`。推导（成立）：协议保证 notice 先于其后 chunk 写出，
    首 notice 处理时客户端 cursor 尚未跨过任何丢弃段、精确等于洞起点
    （handler 阻塞期间多次 drop 也不影响——首 notice 仍先于第一个跨洞
    chunk）；重放 [cursor, head) 连续补齐，**无洞、无重复**、丢弃段从
    ring 补回。R5 的「首次 drop 即发」恰好保住该变体可行性。代价：
    每轮回放 = 丢弃段+队列在途+live（多付 W/带宽 量级）；要求客户端
    在首 notice **即快照 cursor** 并 fence 其后所有帧（generation+fencing）。
  - **本期维持 reset+tail 默认**（skip-to-live 保证 + 客户端简单 +
    1.3s 廉价）；两变体待 UX 数据（跨境用户是否在意 scrollback 丢失）
    再仲裁；用户可选保真度开关（ORCA Live/快照模式）为未来形态。
- 回放窗口截断语义（决策 4）：API.md 记录——起点 clamp（复用 start-sync）+
  总量封顶（复用预算截断语义，closing sync 发布截断点）；被截断段对后续
  重连「仍在 ring 内时」可用。
- 回滚面（R16）：未知 caps 忽略 = legacy；新客户端→旧服务端单测 pin。

## 四、任务分解（按序执行，每阶段：编码 → 双评审 → 仲裁 → 修复）

### §四.0 实现状态（v4 新增，HIGH-1；2026-10-19 深夜回填：Phase A 双评审闭环后）

| 决策 | 计划要求 | 工作树现状 | 证据 |
| --- | --- | --- | --- |
| 1 drop+notice | 已列 Phase A | **已实现并提交（含 250ms 聚合+心跳冲刷，两轮双评审闭环）** | driver.go policy/DropStats；app.go notice 先于 chunk、ttyOverflowNoticeInterval=250ms、心跳分支冲刷 pending（评审 B F2 修复）；聚合帧率 ≤4/s 测试钉住 |
| 1b 队列 4096 | Phase A-1 | **已实现并提交（1b6c658）** | driver.go drop 4096 / close 64 逐字不变 |
| 2 退避 | Phase B | 未开工 | index.html 两处 TTY_RECONNECT_DELAY_MS 固定 5s（TASK-B 待发） |
| 3 write deadline | Phase A | **已实现并提交（env 可配，ready 后武装）** | app.go ttyWriteTimeout()/MYWORKTREE_TTY_WRITE_TIMEOUT_MS（每连接解析，评审 F5 仲裁接受）；ws.go SetWriteTimeout+writeFrame 装弹 |
| 4 窗口化 | Phase A-3 | **已实现并提交（起点 clamp+总量封顶+W=0 构造性纯 tail）** | app.go clamp（复用 #94 start-sync）、streamed>=W_eff 守卫、读长裁剪精确 ≤W（评审 B F1/F3 修复）；截断语义=预算语义 |
| 5a 压缩 | Phase A-3 | **未实现（A0 门 PASS 5.719x，TASK-A3 待发）** | A0-GATE.md；工作树无 flate 代码 |
| 5b W 可配 | Phase A-3 | **已实现并提交（env+replay-window 前缀解析，只缩不涨）** | app.go ttyReplayWindowBytes/ttyClientCapValue（R15） |
| 5c deadline/心跳可配 | Phase A-3 | **已实现并提交（deadline env + slow-link 心跳 15s 档）** | app.go ttyHeartbeatIntervalFor/slow-link caps；未声明连接 10s/45s 逐字不变 |

处置（仲裁）：Phase A 服务端（除 5a）已全部落地并经双评审两轮闭环（第一轮 B 提 F1/F2 HIGH 返工，第二轮双 APPROVE+nit 清零）。**5a 由 TASK-A3 接续（A0 门已放行）**；Phase B 由 TASK-B 接续；Phase C 以本表与 CHANGELOG 收尾。

### Phase A(server)· 一次编码
1. `internal/instance/pty/driver.go`:
   - 溢出策略枚举：`OverflowClose`（默认）与 `OverflowDropOldest`；新增
     `SubscribeOutputWithPolicy(id, policy) (<-chan string, cancel, *DropStats,
     error)`（新增 API，不改 `SubscribeOutput` 签名）；`DropStats` 单原子对
     发布（atomic.Pointer 不可变 pair）。
   - `broadcast` drop 臂：丢队首最旧至新 chunk 可入队、累计计数、不关不
     delete；close 路径逐字保留。
   - **容量（1b）**：drop 策略 4096 chunk（4MB 上界，env 可调）；close
     策略 64 不变。
   - 单测：drop 保留最新/计数正确/通道存活/cancel 幂等/close 不变/
     **容量边界 4096**（订正 v2「1024」残留）。
2. `internal/ws/ws.go`：`Conn` 增 `writeTimeout`（默认 0）+ `SetWriteTimeout`；
   `writeFrame` 在 `c.mu` 内写前 `SetWriteDeadline`（d>0）。零值零变化。
3. `internal/app/app.go` TTY handler:
   - caps 解析增 `overflow-notice`/`compress`/`slow-link`（全 token）与
     **`ttyClientCapValue` 前缀解析**（`replay-window=<bytes>`，R15 单测）。
   - cap 客户端 drop 策略订阅；chunk 收取后查 DropStats 增量，**250ms 区间
     聚合**发 overflow TEXT 帧（先于 chunk）；legacy 走 1013。
   - **回放窗口（4）**：`cursor := since` 后 clamp 到 `head−W_eff`
     （W_eff = min(默认 W env 可配, `replay-window` 声明值)）；**循环增
     `streamed >= W_eff` 终止条件**（复用预算截断语义：截断点入 endOffset、
     closing sync 照发、truncated-not-skipped）；埋点 handshake 增
     `windowed=true droppedFromWindow=<bytes>`（现有字段保留）。
   - **压缩编码（5a pin 2/5，v6 订正 R3-MED-1）**：`compress` 客户端在首二进制帧前建立持久
     flate 流；每帧压缩+Flush；TEXT 原样。**服务端无解压炸弹守卫可加**——
     解压后大小服务端不可见，压缩前逻辑帧结构性 ≤64KB（PTY 读 1KB/回放
     读 64KB），无可检查之物；v5 曾误列于此的「maxDecompressedBytes
     （W+4MB）每连接累计」句**已删除**（该守卫属客户端消费侧，见
     Phase B-9）。
   - **双字段埋点（R14）**：compress 连接 slow-write/conn-closed 增
     `wireBytes`/`logicalBytes`。
   - 存活判定唯一权威 = Manager `m.running`（`SubscribeOutputWith` 同门同错）。
   - 订阅建立后 `SetWriteTimeout(15s)`（env 可调）；legacy teardown 前卸除。
   - `TestHandleInstanceTTYWS_SubscriberOverflowClosesConnectionWith1013`
     原样通过。
   - **未知 caps → legacy 回归（R16）**。
4. 测试：`go test ./internal/...` 全绿；notice 帧格式+聚合语义、cap 门控、
   窗口 clamp 边界（since 窗口内/外、W=0 退化 tail、总量截断语义）、
   caps k=v 解析、四组合矩阵、未知 caps 降级。
5. **Phase A-0（压缩率测量门，先于 5a 编码）**：`/api/instances/log` 采样
   docs 实例数 MB 真实输出，离线 flate BestSpeed+1KB sync flush 实测比率；
   ≥2x 才进 5a 编码，否则按 §四.0 处置降级。

### Phase B(client)· 一次编码
`internal/ui/static/index.html`:
1. `caps=ping,sync` → `caps=ping,sync,overflow-notice,compress`（+可选
   `slow-link`/`replay-window`）；`DecompressionStream('deflate-raw')` 缺失
   时不声明 compress（pin 2 降级）。
2. 解压消费：单流 `pipeTo` 按序 `term.write()`，**`write()` 内 await
   `term.write(data, cb)`**（pin 3）；cursor 按解压后字节推进。
3. 控制帧白名单 + `handleTTYOverflow`：console 日志、`term.reset()`、
   **可见降级提示**（MED-3）、无 cursor tail 重连。
4. **generation+fencing（R6/ORCA）**：每连接 `ttyGeneration` 单调递增；
   notice/reset/重连即 bump；解压管道每输出、xterm 写回调、迟到控制帧
   一律先对 generation 再动作（三层残余：WS 缓冲/管道/xterm 回调）。
   **丢弃标志**（pin 4）：notice 同步置位、管道逐输出检查。
5. **客户端设界（MED-2）**：待解压+待写字节上限 1–2MB，超限本地
   `term.reset()`+弃连+tail 重连（与服务端 drop 同构；压缩把背压推进页面，
   浏览器 onmessage 不可暂停，机器不提供该界）。
6. 退避：`ttyReconnectAttempts`（session 级），延迟 =
   `min(1000*2^n, 30000)*(0.75+rand*0.5)`；**隐藏 30s / 前台可见 8s 上限**；
   **重置锚点 = closing sync 到达且该连接自 closing sync 起存活 ≥60s**
   （v6 订正 V5-N2：与决策 2 逐字一致；60s 从连接真正可用起算、与 W 解耦）。
   替换 :6269/:6787 两处固定 5s（全仓复核仅此两处调度重连，含 liveness
   tick 不间接调度）。
7. node-test 切片规则（含 `Math.random` 写法）；`scripts/` 计数守卫（#95）通过。
8. **客户端埋点回填（MED-4/LOW-F）**：上线后采集一次浏览器 `[tty-metrics]`
   close 行的 `visibility`/`writeWait` 数据（DATA-COLLECTION §一客户端
   清单首次落地），回填决策矩阵第一行（hidden 节流 vs 网络/CPU 归因）
   并归档到本目录。
9. **单帧解压炸弹守卫（v6 新增，R3-MED-1：5a pin5 的客户端侧落地）**：
   每连接对 `DecompressionStream` 输出的**单帧解压后字节**计上界
   `maxDecompressedBytes = max(W_eff, 4MB)`；超限=流退化/损坏 → 关连接
   + tail 重连（与 notice 路径同构）。与 B-5 的分工：**本界防炸弹
   （单帧解压膨胀），B-5 界防积压（待解压+待写总量 1–2MB）**；明确
   **不做连接生命周期累计**（任何 MB 级累计在 150–300KB/s 生产下
   20–30s 耗尽=定时杀器）。合法 1KB live 帧永不触及；64KB 回放帧需
   >64:1 膨胀率才触发。

### Phase C · 收尾
- CHANGELOG.md（fix 段）。
- **API.md**：回放窗口语义（起点+总量封顶、截断点 closing sync、「仍在
  ring 内」限定、线损预算口径）、overflow 帧+250ms 聚合语义、caps 四组合
  矩阵、**notice 清空 scrollback 的 UX 语义**、env 名（W/`ttyWriteTimeout`/
  队列容量，R13）、`slow-link` 心跳放宽档。
- DATA-COLLECTION.md：节流语义与 compress wire/logical 双字段口径
  （矩阵第二行修订注记本次已写）。
- 本计划标注落地状态（以 §四.0 表回填）；全量 `go test ./...` + UI node test。

## 五、验收标准

1. 溢出场景（持续输出+慢消费者）分期验收：
   - a.（Phase A）cap 订阅不下发 1013、通道不关闭，发 overflow 帧且计数
     递增（**250ms 聚合后 ≤4 帧/s**）；legacy 仍收 1013。
   - b.（Phase B）cap 客户端 notice→reset+可见提示→弃连→tail 重绘，不再
     出现 1013 循环；**不等于不再重连**（单次代价=一次 tail+W）。
2. 回放窗口：clamp+总量封顶生效（埋点 `windowed=true`），重连回放量 ≤W
   **且时长 ≤W/有效带宽**；cursor 单调无重复（#87）；closing sync 发布
   截断点（真实终点）。
3. 跨境慢链路（netem：`tc qdisc add dev <if> root netem delay 300ms 50ms
   distribution normal loss 2% rate 800kbit buffer 3200 limit 1000`——
   rate 需配 buffer/limit 方跨内核一致；应用层限速客户端作双轨）：
   - a. compress 客户端稳态不溢出，或 reset cadence ≥ **4MB ÷ netem 实测
     缺口**（v6 订正 R3-MED-2：按验收时实测折算、不绑定日志推导值——
     日志 burst 缺口 103–311KB/s 对应预期 ~13–39s；原「≥28s」建立在
     无出处的 144 缺口上，netem 下实测 200KB/s 缺口会假失败）；
     压缩翻转缺口时趋于不溢出）；
   - b. **catchup 完成时间 ≤ W_eff ÷ 实测有效带宽 + 余量**，实测带宽 =
     `replayBytes/replayMs`（服务端 handshake 埋点；compress 连接用
     `logicalBytes/replayMs`，W_eff 同为逻辑口径），而非与 backlog 成
     正比——W=256–512KB 时 ≈5–10s@50–100KB/s；
   - c. **deadline 误杀数=0**（操作定义：handshake 成功且 `conn-closed
     binFrames` 持续增长=有写入进展，但单写超 deadline 被服务端回收者
     计为误杀；client-liveness 收割（30s/60s 无数据）不计）（R2/N6）；
   - d. 全程无 cursor 错位（压缩×drop 一致性由 Phase A 单测覆盖）。
4. 压缩回归：中途 drop 后 inflate 不错位（Phase A 单测+Phase B node test）；
   caps 四组合矩阵全绿；legacy/未声明 compress 字节流零变化；
   `maxDecompressedBytes` 超限有关断+重连路径。
5. 多设备量化：N=5 慢消费者并发，各自独立 drop/notice（广播扇出无恶化）；
   notice 率 ≤4/s（节流生效）；服务端 CPU 无异常、**RSS 上界 =
   4MB×N + 64KB×N（flate）**；netem 下互不恶化。
6. 退避：指数增长（日志可证）；**回放中段断连与 notice 循环均不退避清零**
   （60s 稳定锚点）；前台可见上限 8s。
7. 人为阻塞 socket 写 ≥15s，handler 有界回收（env 调档后按新值）。
8. 回归全绿：#82、#83、#87、#94、#98 + 全部现有测试。
