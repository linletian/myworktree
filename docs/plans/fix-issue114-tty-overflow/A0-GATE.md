# A0-GATE — Phase A-0 压缩率测量门（issue #114，决策 5a pin 1）

- **日期**：2026-10-19（CST；探测 19:37，采集 19:38–19:41:38，测量 19:41–19:50，
  尾部复探 20:08）
- **执行角色**：数据采集与测量 Agent（**只测量，不改任何产品代码**）
- **门禁定义**（FIX-PLAN 5a pin 1 / §四 Phase A-0 / §四.0）：docs 实例真实输出
  数 MB，离线 `compress/flate` **BestSpeed** + **每 1KB sync flush**，实测
  ratio = Σ原始字节 / Σ压缩字节。**≥2.0x → PASS，进 5a 编码；<2.0x → FAIL，
  压缩降为 Phase C 候选，W/队列参数按无压缩重算。**
- **结论**：**PASS，场景 A ratio = 5.719x**（阈值 2.0x，余量 2.86x）。
  详见 §五。

---

## 一、数据源与只读约束

| 项 | 值 |
| --- | --- |
| 守护进程 | `http://127.0.0.1:40751`（mwbeta，自 2026-10-17 运行，`GET /` → 200） |
| 实例 | `1d13eeebd40b`（docs 实例） |
| 端点 | `GET /api/instances/log?id=1d13eeebd40b[&since=<offset>]` |
| 鉴权 | `Authorization: Bearer <token>`，token 取自 `~/.config/myworktree/auth.json` 的 `auth_token` 字段（32 字符；该文件无 `token` 键，实际键名为 `auth_token`，服务端 `extractAuthToken`（app.go:3860）同时接受 header/query/cookie） |
| **token 明文** | **未出现在任何命令输出、日志、文件、本文档中**（采集脚本从文件读取，仅入内存；未使用 query 形式，避免 URL/访问日志泄漏） |
| 方法约束 | **全程仅 GET**；未发出任何 POST/PUT/PATCH/DELETE，未重启/停止任何实例 |
| ring 语义 | `X-Log-Offset` = 本段返回字节之后的绝对 offset；不带 `since` 时走 `Tail(64KB)`，其 `X-Log-Offset` = ring head（ringbuffer.go `Tail`/`ReadSince`） |
| 单次响应上限 | 65,536 B（`ReadSince(id, since, 64*1024)`，app.go:3427）→ 8MB 必须分 128 段拉取 |
| PTY 读粒度 | 1024 B（driver.go:302 `readBuf := make([]byte, 1024)`）→ 场景 A 帧尺寸的代码依据 |

## 二、采样过程

### 2.1 head 探测与钳位判定

| 探测 | 时间 | 结果 |
| --- | --- | --- |
| `GET /`（可达性） | 19:37 | 200 |
| `GET` 无 token（对照，确认服务端未强制鉴权） | 19:37 | 200 |
| 首探测 `GET`（无 `since`，走 `Tail(64KB)`） | 19:37:03 | `X-Log-Offset: 94746464` → head = 94,746,464 |
| 钳位探测 `since=86357856`（= 首探 head − 8MiB） | 19:37 | 65,536 B，`next=86423392` → implied_start = 86,357,856 = 请求值 → **未钳位** |
| `since=0` 对照 | 19:37 | implied_start = 0（ring 自 0 起未淘汰，保留跨度覆盖实例全生命周期） |
| **采集起始 head 探测** | 19:38 | **head = 94,746,501** → 窗口 `since = 86,357,893` |
| 采集 128 段完成 | 19:41:38 | 8,388,608 B，末段 `next = 94,746,501` |
| 复探（10s 间隔） | 19:50 | head 94,746,575 → 94,746,575，**delta = 0 B** |
| 尾部复探（同一秒三连） | 20:08:30 | 94,746,612 / 94,746,612 / 94,746,612 |

**输出速率实测**：19:37:03（94,746,464）→ 20:08:30（94,746,612）= **+148 B /
1,887 s ≈ 0.08 B/s**。计划文档记录的 2026-10-19 早前探测值 head = 94,746,427，
本次首探 +37 B → **该实例当前处于近空闲态**（≈282 B/h），见 §六 局限 2。

### 2.2 采集窗口与分段

- **窗口**：`since = head − 8MiB = 86,357,893` → 结束 `94,746,501`
- **累计**：**8,388,608 B（8 MiB，达目标值；下限 4 MiB 的 2.0×）**
- **分段**：**128 段 × 65,536 B**（服务端单次上限），逐段 `since=next` 前推
- **HTTP 状态**：128/128 全 200；**重试次数 = 0**（无 oMLX/HTTP 报错，无超时）
- **落盘**：`/tmp/a0-sample.bin`，8,388,608 B，md5 `ff566edd0bf1644dd4d40f89139cd77f`

分段明细（`seg since +bytes -> next status`，`X-Log-Offset` 即 next；
implied_start 与 since 逐段相等，无钳位、无空洞、无重复）：**完整 128 段
清单见 §2.3**（同一份采集 manifest，逐段核对于 §7.1 的采集脚本输出）。

### 2.3 完整分段清单（128/128，全 200，全 65,536 B）

```
  1 since=86357893  + 65536 -> 86423429  200    2 since=86423429  + 65536 -> 86488965  200
  3 since=86488965  + 65536 -> 86554501  200    4 since=86554501  + 65536 -> 86620037  200
  5 since=86620037  + 65536 -> 86685573  200    6 since=86685573  + 65536 -> 86751109  200
  7 since=86751109  + 65536 -> 86816645  200    8 since=86816645  + 65536 -> 86882181  200
  9 since=86882181  + 65536 -> 86947717  200   10 since=86947717  + 65536 -> 87013253  200
 11 since=87013253  + 65536 -> 87078789  200   12 since=87078789  + 65536 -> 87144325  200
 13 since=87144325  + 65536 -> 87209861  200   14 since=87209861  + 65536 -> 87275397  200
 15 since=87275397  + 65536 -> 87340933  200   16 since=87340933  + 65536 -> 87406469  200
 17 since=87406469  + 65536 -> 87472005  200   18 since=87472005  + 65536 -> 87537541  200
 19 since=87537541  + 65536 -> 87603077  200   20 since=87603077  + 65536 -> 87668613  200
 21 since=87668613  + 65536 -> 87734149  200   22 since=87734149  + 65536 -> 87799685  200
 23 since=87799685  + 65536 -> 87865221  200   24 since=87865221  + 65536 -> 87930757  200
 25 since=87930757  + 65536 -> 87996293  200   26 since=87996293  + 65536 -> 88061829  200
 27 since=88061829  + 65536 -> 88127365  200   28 since=88127365  + 65536 -> 88192901  200
 29 since=88192901  + 65536 -> 88258437  200   30 since=88258437  + 65536 -> 88323973  200
 31 since=88323973  + 65536 -> 88389509  200   32 since=88389509  + 65536 -> 88455045  200
 33 since=88455045  + 65536 -> 88520581  200   34 since=88520581  + 65536 -> 88586117  200
 35 since=88586117  + 65536 -> 88651653  200   36 since=88651653  + 65536 -> 88717189  200
 37 since=88717189  + 65536 -> 88782725  200   38 since=88782725  + 65536 -> 88848261  200
 39 since=88848261  + 65536 -> 88913797  200   40 since=88913797  + 65536 -> 88979333  200
 41 since=88979333  + 65536 -> 89044869  200   42 since=89044869  + 65536 -> 89110405  200
 43 since=89110405  + 65536 -> 89175941  200   44 since=89175941  + 65536 -> 89241477  200
 45 since=89241477  + 65536 -> 89307013  200   46 since=89307013  + 65536 -> 89372549  200
 47 since=89372549  + 65536 -> 89438085  200   48 since=89438085  + 65536 -> 89503621  200
 49 since=89503621  + 65536 -> 89569157  200   50 since=89569157  + 65536 -> 89634693  200
 51 since=89634693  + 65536 -> 89700229  200   52 since=89700229  + 65536 -> 89765765  200
 53 since=89765765  + 65536 -> 89831301  200   54 since=89831301  + 65536 -> 89896837  200
 55 since=89896837  + 65536 -> 89962373  200   56 since=89962373  + 65536 -> 90027909  200
 57 since=90027909  + 65536 -> 90093445  200   58 since=90093445  + 65536 -> 90158981  200
 59 since=90158981  + 65536 -> 90224517  200   60 since=90224517  + 65536 -> 90290053  200
 61 since=90290053  + 65536 -> 90355589  200   62 since=90355589  + 65536 -> 90421125  200
 63 since=90421125  + 65536 -> 90486661  200   64 since=90486661  + 65536 -> 90552197  200
 65 since=90552197  + 65536 -> 90617733  200   66 since=90617733  + 65536 -> 90683269  200
 67 since=90683269  + 65536 -> 90748805  200   68 since=90748805  + 65536 -> 90814341  200
 69 since=90814341  + 65536 -> 90879877  200   70 since=90879877  + 65536 -> 90945413  200
 71 since=90945413  + 65536 -> 91010949  200   72 since=91010949  + 65536 -> 91076485  200
 73 since=91076485  + 65536 -> 91142021  200   74 since=91142021  + 65536 -> 91207557  200
 75 since=91207557  + 65536 -> 91273093  200   76 since=91273093  + 65536 -> 91338629  200
 77 since=91338629  + 65536 -> 91404165  200   78 since=91404165  + 65536 -> 91469701  200
 79 since=91469701  + 65536 -> 91535237  200   80 since=91535237  + 65536 -> 91600773  200
 81 since=91600773  + 65536 -> 91666309  200   82 since=91666309  + 65536 -> 91731845  200
 83 since=91731845  + 65536 -> 91797381  200   84 since=91797381  + 65536 -> 91862917  200
 85 since=91862917  + 65536 -> 91928453  200   86 since=91928453  + 65536 -> 91993989  200
 87 since=91993989  + 65536 -> 92059525  200   88 since=92059525  + 65536 -> 92125061  200
 89 since=92125061  + 65536 -> 92190597  200   90 since=92190597  + 65536 -> 92256133  200
 91 since=92256133  + 65536 -> 92321669  200   92 since=92321669  + 65536 -> 92387205  200
 93 since=92387205  + 65536 -> 92452741  200   94 since=92452741  + 65536 -> 92518277  200
 95 since=92518277  + 65536 -> 92583813  200   96 since=92583813  + 65536 -> 92649349  200
 97 since=92649349  + 65536 -> 92714885  200   98 since=92714885  + 65536 -> 92780421  200
 99 since=92780421  + 65536 -> 92845957  200  100 since=92845957  + 65536 -> 92911493  200
101 since=92911493  + 65536 -> 92977029  200  102 since=92977029  + 65536 -> 93042565  200
103 since=93042565  + 65536 -> 93108101  200  104 since=93108101  + 65536 -> 93173637  200
105 since=93173637  + 65536 -> 93239173  200  106 since=93239173  + 65536 -> 93304709  200
107 since=93304709  + 65536 -> 93370245  200  108 since=93370245  + 65536 -> 93435781  200
109 since=93435781  + 65536 -> 93501317  200  110 since=93501317  + 65536 -> 93566853  200
111 since=93566853  + 65536 -> 93632389  200  112 since=93632389  + 65536 -> 93697925  200
113 since=93697925  + 65536 -> 93763461  200  114 since=93763461  + 65536 -> 93828997  200
115 since=93828997  + 65536 -> 93894533  200  116 since=93894533  + 65536 -> 93960069  200
117 since=93960069  + 65536 -> 94025605  200  118 since=94025605  + 65536 -> 94091141  200
119 since=94091141  + 65536 -> 94156677  200  120 since=94156677  + 65536 -> 94222213  200
121 since=94222213  + 65536 -> 94287749  200  122 since=94287749  + 65536 -> 94353285  200
123 since=94353285  + 65536 -> 94418821  200  124 since=94418821  + 65536 -> 94484357  200
125 since=94484357  + 65536 -> 94549893  200  126 since=94549893  + 65536 -> 94615429  200
127 since=94615429  + 65536 -> 94680965  200  128 since=94680965  + 65536 -> 94746501  200
```

**校验**：Σbytes = 128 × 65,536 = 8,388,608 = 落盘字节数 = head − 窗口起点；
since 链严格连续（每段 since = 上一段 next），无空洞、无重复、无钳位。

### 2.4 样本内容画像（可见文本节选，已去 ANSI）

```
要成对的 keydown.t/keyup.t，丢了配对就没法算。想稳妥的话，在
控制台跑这段（fail-soft，跑挂了也不影响页面）：
window.__im=[];const _o=console.log;console.log=(...a)=>{try{if(String(a[0]).indexOf("[ios-ime]")===0)window._
● 直接回答：copy() 之后整份日志就在 Mac 的剪贴板里，直接粘到这个对话发我就行。
```

→ 构成 = 中文自然语言 + 内联 JS 代码块 + TUI 结构化装饰（`›`/`●`），
真彩色 SGR + OSC 8 超链接。这正是「docs 实例输出构成」的实测形态。

## 三、ANSI 占比（gate 判定的关键构成指标）

| 指标 | 值 | 占比 |
| --- | --- | --- |
| **ESC 字节（`\x1b` 出现次数）** | 353,037 | **4.209%** |
| 转义序列条数 | 353,013 | — |
| ├─ CSI（`ESC [`） | 323,459 | — |
| └─ OSC（`ESC ]`，其中 OSC 8 超链接 29,553） | 29,554 | — |
| **转义序列字节跨度（ANSI 占比）** | 3,146,568 | **37.510%** |
| ├─ 序列长度 mean / p99 / max | 8.91 / 19 / 90 B | — |
| 文本字节（非转义） | 5,242,040 | 62.490% |
| 高位字节（UTF-8 载荷） | 841,389 | 10.030% |
| SGR reset churn（`[0m` + `[39m`） | 112,064 | — |
| CR / LF | 54,216 / 24,625 | 0.646% / 0.294% |
| alnum / base64-ish 字符占比 | — | 36.93% / 37.28% |
| **最长 base64-ish 连续段** | **41 B** | — |
| 字节级香农熵 | 4.621 bit/B | （8.0 = 不可压） |
| 1KB 完全重复块占比 | 7.6% | 非「同一块反复灌满」型样本 |

**要点**：计划 §5a pin 1 担心的「ANSI 占比、base64 块未测」两项——
ANSI 占比 **37.5%（ESC 字节 4.209%）**，确实极高；但 **base64 块不存在**
（最长 base64-ish 连续段仅 41 B，熵 4.621 bit/B）→ 转义序列高度模板化
（平均 8.91 B，SGR reset 11 万次），正是 flate 能吃掉的模式冗余。

**交叉校验**（独立实现，防工具自证）：
- Python 独立 ANSI 解析器：ESC 353,037 / 序列 353,013 / 序列字节 3,146,568
  → 与 Go 工具**逐字节一致**（ESC 总数比序列数多 24，即 OSC 内的 ST `ESC \`
  终止符，非解析分歧）。
- 独立压缩实现 `zlib`（raw deflate, level=1, 每帧 `Z_SYNC_FLUSH`）同一
  样本同帧粒度：1KB → 9.922x、64KB → 12.232x、512B → 9.024x、128B → 6.133x。
  **权威口径仍是 Go `compress/flate`**（生产服务端就是它，且它的 BestSpeed
  弱于 zlib level 1）；zlib 数字只作「量级不异常」的外部印证。
- 阴性对照：同模型压缩 8 MiB 随机数据 → **0.990x**（≈1.0，证明测量链路
  没有凭空放大）。

## 四、测量语义（逐字对齐 5a）

`tools/a0compress/main.go`（独立 mini-module）实现：

- **一条持久 `compress/flate` 流**（`flate.NewWriter`，`level=BestSpeed`）
  喂完整个样本；**每帧写后 `Flush()`（sync flush）**；
  **中途不 `Close()`**（Close 会终止共享上下文并使客户端
  `DecompressionStream('deflate-raw')` 脱同步）→ ratio 的分子/分母都只统计
  到「最后一次 Flush」为止，不被收尾块稀释。
- 压缩字节在 **`Flush()` 之后的下游 writer** 上计数（= 服务端交给 socket 的
  位置）；deflate-raw 2 字节流头（CMF+FLG）计入首帧。
- **场景 A（主判指标）**：1024 B/帧 = PTY 读粒度（driver.go:302）。
- **场景 B（次指标）**：65,536 B/帧 = `ReadSince` 粒度（app.go:3427）。
- `ratio = Σ原始字节 / Σ压缩字节`，两场景分别算，同一份字节流、同一 flate
  上下文按文件序（回放与 live 共用同一流）。

## 五、结果与判定

```
Phase A-0 compression gate (issue #114, FIX-PLAN 5a pin 1)
sample: /tmp/a0-sample.bin  bytes=8388608  md5=ff566edd0bf1644dd4d40f89139cd77f  level=bestspeed

scenario                         frames   raw bytes  comp bytes    ratio  savings   mean/fr
A live 1KB sync flush              8192     8388608     1466688   5.719x   82.52%     179.0
B replay 64KB sync flush            128     8388608      744277  11.271x   91.13%    5814.6
sweep 256B frame                  32768     8388608     2820295   2.974x   66.38%      86.1
sweep 512B frame                  16384     8388608     1936420   4.332x   76.92%     118.2
sweep 2048B frame                  4096     8388608     1169782   7.171x   86.06%     285.6
sweep 4096B frame                  2048     8388608      952270   8.809x   88.65%     465.0
sweep 8192B frame                  1024     8388608      813492  10.312x   90.30%     794.4
sweep 16384B frame                  512     8388608      730944  11.476x   91.29%    1427.6
ref: no per-frame flush               1     8388608      695523  12.061x   91.71%  695521.0
ref: DefaultCompression 1KB        8192     8388608      642522  13.056x   92.34%      78.4

scenario A per-frame compressed size: min 28 p50 163 p99 401 max 518 (deflate-raw header 2 B, sync-flush floor ~40960 B)
scenario A fresh-stream windows (n=8): min 5.048x (offset 5242880)  max 7.735x (offset 1048576)
roundtrip, one persistent deflate-raw stream flushed per frame: ok=true

GATE (scenario A >= 2.0x): PASS
scenario A ratio 5.719x >= 2.0x: proceed to 5a encoding; worst of 8 windows 5.048x at offset 5242880
```

### 主判数字

| 场景 | 帧 | 原始 | 压缩后 | **ratio** | 省 |
| --- | --- | --- | --- | --- | --- |
| **A（主判）** | 1024 B sync flush | 8,388,608 | 1,466,688 | **5.719x** | 82.52% |
| B（次指标） | 65,536 B sync flush | 8,388,608 | 744,277 | **11.271x** | 91.13% |

**判定：PASS**（场景 A 5.719x ≥ 2.0x，**余量 2.86×**）→ 按计划 §四.0/§四
Phase A-0：**放行进入 5a 压缩编码**；无需触发「压缩降为 Phase C 候选、
W/队列参数按无压缩重算」的降级处置。计划预估「文本日志 3–5x」被实测
超出（5.719x）。

### 稳健性（不是单点数字）

- **帧尺寸敏感性（同一持久流，同 level）**（小帧段来自
  `-sweep 64,96,112,128,144,160,192,224`，大帧段来自
  `-sweep 32768,65535,131072`，中段为默认 sweep；命令见 §7.2）：
  64B→1.092x、96B→1.213x、
  112B→1.260x、128B→2.034x、144B→2.186x、160B→2.321x、192B→2.557x、
  224B→2.763x、256B→2.974x、512B→4.332x、**1024B→5.719x**、
  2048B→7.171x、4096B→8.809x、8192B→10.312x、16384B→11.476x、
  32768B→11.958x、65535B→12.050x、65536B→11.271x、131072B→11.653x。
  → **ratio = 2.0x 的盈亏平衡点在 ≈128 B/帧**；生产 PTY 读上限 1024 B，
  距平衡点有 8× 帧尺寸余量。这是本门唯一的真实失效模式（见 §六 局限 3）。
  （注：≥32 KB 后曲线进入平台区并出现 ±0.8x 的**非单调**抖动
  ——16384B→11.476x、32768B→11.958x、65535B→12.050x、65536B→11.271x、
  131072B→11.653x。这是 deflate 内部 block/lookahead 边界与 sync flush
  落点如何对齐的实现产物（Go `compress/flate` 的 `deflate()` 仅在
  lookahead 满足条件时才吐出 block，帧边界不同 → 切出的 block 数不同），
  不是测量噪声（同命令重跑逐次一致，见下「确定性」），量级 ±0.8x，
  不影响门禁结论。）
- **内容分段稳定性**：把样本切成 8 段、每段**新开一条 flate 流**（模拟
  重连/新流），ratio 区间 **5.048x – 7.735x**，最差段仍 ≥2.0x 的 2.5 倍。
- **sync flush 税**：1KB×8192 帧的 flush 下限 40,960 B，占压缩后
  1,466,688 B 的 **2.79%**；单帧压缩后 min 28 / p50 163 / p99 401 / max 518 B。
  → flush 粒度不是本样本的瓶颈；帧再小才咬人（见上 sweep）。
- **上界参照**：不分帧（整块一次 flush）12.061x；`DefaultCompression` 下
  1KB 分帧 13.056x。→ 5a 选 BestSpeed 的代价 = ratio 下降 **56.2%**
  （13.056x → 5.719x，等价 wire 字节 ×2.28），但仍远高于 2.0x 门禁；
  若 Phase C 想再要带宽，换 level 是现成杠杆。
- **可解码性**：场景 A 的字节流（每 1KB 一次 sync flush 的单条持久
  deflate-raw）经 `flate.NewReader` **还原为逐字节相同的原文**（ok=true）。
- **确定性**：同一命令连跑两次，输出 **完全一致**（`diff` 无差异）。
- **wire 换算**：按计划 §决策 5 的 150–300 KB/s 逻辑输出，5.719x →
  **26–52 KB/s wire**；跨境 50–100 KB/s 链路下 drain 能力提升同比例。

## 六、局限（如实记录，不粉饰）

1. **单实例、单窗口**：全部数字来自 `1d13eeebd40b` 的一个 8 MiB 窗口
   （offsets 86,357,893–94,746,501）。门禁本身定义如此（计划指定 docs 实例），
   但换实例（如 base64 编译日志刷屏型）ratio 可能显著更低——**5a 编码时
   必须保留 caps `compress` 协商 + 明文降级路径**（pin 2 已覆盖），
   不要把本数字当全局常数。
2. **采集时实例近空闲**：head 19:37:03 = 94,746,464 → 19:41:38 采集完成
   = 94,746,501（+37 B / 约 4 分钟）→ 20:08:30 = 94,746,612
   （**+148 B / 1,887 s ≈ 0.08 B/s**，≈282 B/h）→ 本窗口是 **ring 里的
   历史字节**，不是采集瞬间的 live 突发。场景 A 是在这些真实字节上
   **按 1024 B 帧粒度建模**（与生产 PTY 读出粒度 driver.go:302 一致），
   不是抓到的真实帧序列。
3. **唯一可见失效模式**：若 live 实际帧远小于 1024 B（零散 trickle），
   ratio 按 sweep 曲线下滑，**≈128 B/帧时触到 2.0x 平衡点**。
   生产 overflow 场景（#114 要治的）恰是高输出率 → 读满 1024 B 的区间，
   与场景 A 建模吻合；但建议在 Phase B 埋点里实测真实帧尺寸分布回验
   （`wireBytes`/`logicalBytes` 双字段，R14，已具备条件）。
4. **未含 WS 成帧开销**：本 ratio 是 flate 流口径；每帧 WS 头（2–8 B）
   与既有 TEXT 控制帧不在分子分母内。
5. **ring 容量未直接读取**：以 `since=0` 返回 implied_start=0 反推
   「保留 ≥ 本窗口」，未从守护进程取 capBytes 配置值（无该只读端点，
   且本次不做任何写操作）。

## 七、复现命令

### 7.1 采样（只读，仅 GET；token 不落命令行、不落文件）

采集器不入库（避免污染主模块），但**全文内嵌于此以便复现**——存为
`/tmp/a0collect.py` 后 `python3 /tmp/a0collect.py` 执行：

```python
#!/usr/bin/env python3
"""Phase A-0 sampler (issue #114). READ-ONLY: issues only HTTP GET."""
import json, os, sys, time, urllib.request

DAEMON, INST = "http://127.0.0.1:40751", "1d13eeebd40b"
OUT, MANI = "/tmp/a0-sample.bin", "/tmp/a0-segments.tsv"
CHUNK, TARGET, MINIMUM, MAX_TRIES = 64 * 1024, 8 * 1024 * 1024, 4 * 1024 * 1024, 5

def tok():  # token 只进内存，永不打印
    d = json.load(open(os.path.expanduser("~/.config/myworktree/auth.json")))
    return d.get("auth_token") or d.get("token")

def get(since=None):
    url = "%s/api/instances/log?id=%s" % (DAEMON, INST)
    if since is not None:
        url += "&since=%d" % since
    req = urllib.request.Request(url, headers={"Authorization": "Bearer %s" % tok()})
    for attempt in range(1, MAX_TRIES + 1):
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                return r.status, int(r.headers.get("X-Log-Offset", "-1")), r.read(), attempt
        except Exception as e:
            sys.stderr.write("retry %d/%d since=%s err=%s\n" % (attempt, MAX_TRIES, since, type(e).__name__))
            time.sleep(1.5 * attempt)
    raise RuntimeError("GET failed after %d tries" % MAX_TRIES)

retries = 0
st, head, body, att = get(None)          # 无 since => Tail(64KB)，X-Log-Offset = ring head
retries += att - 1
print("head_probe status=%d head=%d bytes=%d attempts=%d" % (st, head, len(body), att))
start = head - TARGET
st, nxt, body, att = get(start)          # 钳位探测：服务端是否把 since 前移
retries += att - 1
print("clamp_probe status=%d requested_since=%d implied_start=%d next=%d bytes=%d attempts=%d"
      % (st, start, nxt - len(body), nxt, len(body), att))
with open(OUT, "wb") as f, open(MANI, "w") as m:
    m.write("seg\tsince\timplied_start\tnext\tbytes\tcum\tstatus\tattempts\n")
    since, seg, cum = start, 0, 0
    while cum < TARGET:
        st, nxt, body, att = get(since)
        retries += att - 1
        if not body:
            print("seg=%d empty at live head=%d" % (seg + 1, nxt)); break
        f.write(body); cum += len(body); seg += 1
        m.write("%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n" % (seg, since, nxt - len(body), nxt, len(body), cum, st, att))
        if nxt <= since:
            print("seg=%d no offset progress" % seg); break
        since = nxt
print("done segments=%d bytes=%d retries=%d min_ok=%s" % (seg, cum, retries, cum >= MINIMUM))
```

期望输出（本次实测）：`head_probe … head=94746501` /
`clamp_probe … implied_start=86357893 = requested_since`（clamp_delta=0）/
`done segments=128 bytes=8388608 retries=0 min_ok=True`。

纯 curl 等价形式（`<T>` 由操作者从 `~/.config/myworktree/auth.json` 的
`auth_token` 代入；**不要把 token 写进任何脚本、文档或 shell history**——
下面用变量注入。注意 curl 会把 header 放进 argv（本机 `ps` 可见），
跨机场景请改用 `--config -` 从 stdin 传）：

```bash
AUTH=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/.config/myworktree/auth.json')))['auth_token'])")
URL='http://127.0.0.1:40751/api/instances/log?id=1d13eeebd40b'
OFF=$(curl -s -o /dev/null -D - -H "Authorization: Bearer $AUTH" "$URL" \
      | tr -d '\r' | awk 'tolower($1)=="x-log-offset:"{print $2}')
S=$((OFF - 8388608)); : > /tmp/a0-sample.bin
while [ "$S" -lt "$OFF" ]; do
  curl -s -o /tmp/a0-part.bin -D /tmp/a0-h.txt -H "Authorization: Bearer $AUTH" "$URL&since=$S"
  N=$(wc -c < /tmp/a0-part.bin); [ "$N" -gt 0 ] || break
  cat /tmp/a0-part.bin >> /tmp/a0-sample.bin
  S=$(tr -d '\r' < /tmp/a0-h.txt | awk 'tolower($1)=="x-log-offset:"{print $2}')
done
wc -c /tmp/a0-sample.bin   # 8388608
```

### 7.2 测量（本次门禁判定口径）

```bash
cd docs/plans/fix-issue114-tty-overflow/tools/a0compress
go run . -sample /tmp/a0-sample.bin
```

CLI（全部可选，默认即门禁口径）：

| flag | 默认 | 含义 |
| --- | --- | --- |
| `-sample` | `/tmp/a0-sample.bin` | 原始样本文件（只读） |
| `-frameA` | `1024` | 场景 A 帧尺寸（live，PTY 读粒度）——**主判指标** |
| `-frameB` | `65536` | 场景 B 帧尺寸（回放，`ReadSince` 粒度） |
| `-level` | `bestspeed` | flate level（5a pin 死 BestSpeed；可 `default`/`9`/`0-9` 做参照） |
| `-sweep` | `256,512,1024,2048,4096,8192,16384,65536` | 帧尺寸扫描；`""` 关闭 |
| `-windows` | `8` | 切成 N 段各自**新开流**测 ratio（0 关闭）；给「最差段」下界 |
| `-roundtrip` | `true` | 场景 A 字节流 inflate 还原原文校验 |
| `-noRefs` | `false` | 跳过 no-flush / DefaultCompression 参照 |
| `-json` | `false` | 输出 JSON（机读，喂评审脚本） |

退出码：`0` = PASS，`1` = FAIL，`2` = 用法/IO 错误 → 门禁可直接接 CI。

常用变体：

```bash
# 小帧盈亏平衡定位（§五 稳健性）
go run . -sample /tmp/a0-sample.bin -sweep 64,96,112,128,144,160,192,224 -noRefs
# 大帧平台区
go run . -sample /tmp/a0-sample.bin -sweep 32768,65535,131072 -noRefs
# 机读
go run . -sample /tmp/a0-sample.bin -json
```

## 八、工具落盘与主仓隔离验证

- **本阶段（本 Agent）新建文件，仅三处**：
  - `docs/plans/fix-issue114-tty-overflow/tools/a0compress/go.mod`
    （`module a0compress` / `go 1.25.0`，照抄主仓 go.mod 的 go 指令；**仅 stdlib**）
  - `docs/plans/fix-issue114-tty-overflow/tools/a0compress/main.go`
  - `docs/plans/fix-issue114-tty-overflow/A0-GATE.md`（本文档）
- **本 Agent 未修改任何产品代码**；`FIX-PLAN.md` 未改动（见 §九）。
- 嵌套 `go.mod` 使主仓 `./...` 自动排除本工具。**主仓 `go build ./...`
  时间线（如实记录，本 worktree 有兄弟 Agent 并行改产品代码）**：

| 时刻 | `go build ./...` | 说明 |
| --- | --- | --- |
| 19:52 | **exit 0（全绿）** | 本阶段首次验证 |
| 20:14:26–20:14:27 | **exit 0（全绿）** | 二次确认（此时兄弟 Agent 已改过 driver.go 19:53、driver_test.go 19:56，仍绿） |
| 20:41:53 / 20:43:32 / 20:44:29 / 20:46:43 / **20:49:17（本阶段最后一次）** | **exit 1** | 报错文件**只有** `internal/app/app.go`：`slowLinkCap`/`replayWindow`（20:41–43）→ `pendingNoticeChunks`/`pendingNoticeBytes`/`lastOverflowNoticeAt`（20:44–46）→ `windowed`（20:49:17）。未使用变量集合逐次收缩、文件 mtime 一路推进（20:39:50 → 20:48:53），是兄弟 Agent **正在实时写入**决策 4/5b/1 的 caps+notice+窗口逻辑并趋于收敛的编译中间态，**与本阶段无任何关系**，本 Agent 未写入该文件 |

```console
$ go list ./... | grep -c a0compress
0
$ go vet ./docs/...
go: warning: "./docs/..." matched no packages
no packages to vet
$ go list -m ./docs/plans/fix-issue114-tty-overflow/tools/a0compress
go: cannot use relative path ./docs/plans/fix-issue114-tty-overflow/tools/a0compress to specify module
```

→ **本工具的隔离性成立**：`go list ./...` 不含 `a0compress`（计数 0）、
`go vet ./docs/...` 「matched no packages」证明整个 docs 树不在主仓包图内、
末行为 Go 对嵌套模块的正常拒绝。20:41 起的红灯是兄弟 Agent 在途编辑
`internal/app/app.go` 造成的**编译中间态**（未使用变量），不在本阶段范围，
本 Agent 无权限也无须修复产品代码；该红理由与 A-0 测量无关，
门禁判定不受影响（测量全程离线于 `/tmp/a0-sample.bin`，不编译产品代码）。

mini-module 自身（20:41:53 复核，与主仓状态无关）：`gofmt -l .` 无输出、
`go vet ./...` 通过、`go build -o /dev/null ./...` 通过
（**用 `-o /dev/null`，不要在工具目录留 `a0compress` 可执行文件**——
`go build ./...` 会在包目录落二进制，本次曾误落一个 3.4 MB 的
`a0compress`，已删除；工具目录最终只有 `go.mod` + `main.go` 两个文件）。

**并发说明（非本 Agent 所为，如实记录）**：本 worktree 有兄弟 Agent 并行工作，
`git status` 在我采集期间出现下列改动——`internal/instance/pty/driver.go`
（mtime 19:53:47）、`internal/instance/pty/driver_test.go`（19:56:37）、
`docs/plans/fix-issue114-tty-overflow/TASK-A2-app-server.md`（20:05:29），
另有 Oct 14 起即存在的未提交改动（`docs/API.md`、`internal/app/app.go`、
`internal/ws/*.go` 等）。**我未写入其中任何一个**；上面的 `go build ./...`
绿灯是在包含这些并发改动的树测得的（20:14:26–20:14:27），因此该绿灯
不代表这些文件的最终状态——评审时以各自阶段的验证为准。

## 九、变更清单（本阶段）

| 文件 | 动作 |
| --- | --- |
| `docs/plans/fix-issue114-tty-overflow/tools/a0compress/go.mod` | 新建 |
| `docs/plans/fix-issue114-tty-overflow/tools/a0compress/main.go` | 新建 |
| `docs/plans/fix-issue114-tty-overflow/A0-GATE.md` | 新建（本文档） |
| `docs/plans/fix-issue114-tty-overflow/FIX-PLAN.md` | **未改**（禁止项） |
| `internal/**`、`cmd/**`、`go.mod`（主仓） | **本 Agent 未改**（工作树另有兄弟 Agent 的并行改动，见 §八 并发说明） |

**门禁结论：PASS（场景 A 5.719x ≥ 2.0x）→ 5a 压缩编码可以开工。**
