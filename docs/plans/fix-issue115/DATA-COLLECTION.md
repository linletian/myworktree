# Issue #115 数据采集手册（iOS 软键盘直接提交输入丢失 → flush 算法校准）

诊断埋点随 T0 落地（`internal/ui/static/index.html`），**URL 参数门控、纯前端、零后端**：
地址带 `?iosime-debug=1` 打开，日志统一前缀 `[ios-ime]`，输出到 Safari Web Inspector 控制台。

**状态**：结论以 T0 数据为准（v6，2026-10-16 按 PR #123 第一轮评审仲裁修订；plan.md 已放行 T0 与 T1）。真机验证矩阵按维护者决定于**合并后（POST-MERGE）在 beta 渠道执行**，不是本 PR 的合并门禁（follow-up issue 跟踪）。

**代码落点与可移除性**（plan.md §八）：埋点代码位于 `index.html` 中 issue #115 的
`ios-ime bridge` 单一可移除区块内（`// ==== ios-ime bridge … BEGIN/END` 纯 ASCII 横幅包裹、
`iosIme` 前缀标识符），上游 xterm 修复发布后随区块整体删除，不向 pty.js / factory 外溢任何接口。

**埋点归属分层与绑定（评审 G2）**：T0 诊断与 T1 桥接**共用唯一绑定函数
`ensureIosImeDirectInputBridge` 与唯一幂等标志 `iosImeBridgeTerm`**——T0 阶段该函数只登记
诊断监听子集并发出 `bound`（否则本手册 §二 的采集前置不可达），T1 落地即在其上叠加桥接监听，
textarea 上不会出现两套并行监听；`?iosime-debug=1` 关闭时诊断监听**完全不存在**（零开销）。

flag 的两个独立决定（评审 LOW-3 拆分，勿再混为一谈）：

- **门控机制 = URL 参数**。与 #114 的差异：#114 的诊断兼有服务端与客户端两侧且需长期保留，故走 Go build tag（`-tags ttymetrics`）+ cookie；本问题纯浏览器侧输入事件流，URL 门控是更小实现——默认关闭、零监听零开销。
- **是否长期保留 = T0 数据回答后单独决定**（与 #114 的保留先例对齐与否另行评审），不在本手册预先捆绑。

目的：在修复方案（T1 应用层桥接，plan.md §三）已完成算法设计的前提下，用真机事件流回答三个
决定**校准项**的问题：

1. **英文软键盘直接输入字母是否也失败？**
   - 若失败 ⇒ baseline 需从"keydown 229"扩展到 `beforeinput`（预案已预留扩展点）；
   - 若成功（代码分析预期：无 keydown 时走 `composed=false` 的 input 路径，`_inputEvent` 正常投递）⇒ 维持 keyCode 229 单一入口。
2. **第 3 环根因假设**："IME 对 textarea 的插入**晚于** keydown 时调度的 setTimeout(0) 回调"
   （plan.md §一 已按评审 M3 降级为假设）。DOM 事件顺序回答不了它——必须靠与 xterm 同构的
   时序探针（见 §一 `[ios-ime] probe` 行）。
3. **中文输入法直提交的完整事件序列**是否与上游 #5835 日志一致？其中：
   - 空格→。 的"删后插（deleteContentBackward + insertText）"是否复现，决定 delta 算法等长分支
     （LCP 差分，`\x7f。`）的真实校准；
   - keyup 的 keyCode 实测分布，校准 failsafe 750ms 的取值区间。

埋点只打日志，不改变任何线上行为；门控关闭时页面 JS 无监听、无开销。

## 一、埋点清单（浏览器控制台，`?iosime-debug=1` 打开）

"落地"列标注该日志行由哪个任务产生：**T0** = 诊断埋点（T0 阶段即可见）、**T1** = 桥接内部状态
（需 T1 落地后才产生；评审 G2 修正——v3 曾把两类混列，导致采集前置依赖 T0 产不出的行）。

| 日志行 | 落地 | 触发时机 | 关键字段 |
| --- | --- | --- | --- |
| `[ios-ime] bound` | T0+T1 | 绑定到某会话的 textarea（幂等，每会话一次） | `sessionId` |
| `[ios-ime] keydown` | T0+T1 | textarea 上每次 keydown | `t`（keydown 绝对时刻）、`keyCode`、`key`、`value`、`len` |
| `[ios-ime] probe` | T0+T1 | **同构探针**：keydown 内自排一个 `setTimeout(0)`，回调时打时间戳与当时 `value` | `t0`（keydown 时刻）、`t1`（探针回调时刻）、`valueAtProbe` |
| `[ios-ime] beforeinput` | T0+T1 | textarea 上每次 beforeinput | `inputType`、`data`、`composed`、`isComposing`、`value` |
| `[ios-ime] input` | T0+T1 | textarea 上每次 input | `inputType`、`data`、`composed`、`value`、`dt`（相对 `probe.t1` 的毫秒数） |
| `[ios-ime] compositionstart` | T0+T1 | 合成开始 | `value` |
| `[ios-ime] compositionend` | T0+T1 | 合成结束 | `data`、`value` |
| `[ios-ime] keyup` | T0+T1 | textarea 上每次 keyup | `t`（keyup 绝对时刻）、`keyCode`、`key`、`value`、`len` |
| `[ios-ime] pending` | T1 | keydown 229 记下/保留最早 baseline | `baseline`、`len`（baseline 长度）、`keptEarliest`、`rearmed`（timer 是否重臂；等于 keptEarliest——首击为新装，后续 229 才是重臂） |
| `[ios-ime] deliver` | T1 | 延后 flush（keyup 后一个宏任务）、failsafe 或 blur flush 的判定结果 | `baseline`、`current`、`delta`、`emissions`、`branch`（A/B/C/D；空 delta 判定行为 null）、`payload`、`via`（keyup-defer/failsafe/blur/external-rewrite（外部改写终结前的末次投递））、`armed`（进入 flush 时 `iosImeFailsafeArmed` 的值，关窗判定依据） |
| `[ios-ime] clear` | T1 | flush 关窗 / compositionstart / blur 后清除 pending | `reason` |

字段解读：

- `probe` 与 `input` 的时序就是第 3 环假设的判据：FIFO 下 xterm 的回调不晚于我们的探针，
  **`probe.t1` 早于 `input` 且 `valueAtProbe` 不含新字符 ⇒ 插入晚于 0ms 回调，假设成立**；
  若 `valueAtProbe` 已含新字符 ⇒ diff 本应命中，需回头查 `_keyPressHandled` 等其他丢弃路径。
  `input.dt` 相对**本次敲击**探针的 `t1`（每次 keydown 重置）：本次敲击的探针触发前 `dt` 为
  `null`，`null` 即"插入早于探针"的证伪分支；`dt` 仅当 input 跟随本次敲击的 keydown 时才有意义，无前置 keydown 的 input（粘贴、composition 驱动）该数字无意义，转写时必须忽略。
- failsafe 校准用 `keyup.t − keydown.t`（同一次敲击的 keydown→keyup 间隔，对照 §三 实测基线
  12–34ms）：keydown/keyup 行各自带 `t`，间隔可直接由日志字段算出。
- **无 `probe` 行 = 该次敲击发生在绑定之前**（≤2s 残留窗口，plan.md §六）——数据无效，
  不得读作"假设不成立"（评审 N7）。采集时必须先看到 `[ios-ime] bound` 再开始记录（该行
  T0 阶段由 T0 自带绑定发出，评审 G2）。
- `keydown(keyCode=229) → input(insertText, composed=true, isComposing=false) → keyup` 是**失败路径**的
  标准形态：此刻终端无回显即为复现。
- `deliver` 行的 `branch`：A=水印空全发、B=前缀消费发剩余、C=水印已含（xterm 自投递，静默）、
   D=无关投递间隙全发。真机期望失败场景落在 A。日志顺序为 `deliver` 先于 `clear(close-window)`。
- `[ios-ime] clear` 的 `reason` 取值 `close-window / compositionstart / blur / external-rewrite`：分别对应 flush 关窗、
  composition 取消、blur flush-then-clear、外部改写终结四条清除路径各自留痕。blur 清除路径同时复位
  `iosImeFailsafeArmed` 与 `iosImeComposing`（compositionend 丢失时 blur 自愈，防 composing 锁死）；
  重新绑定（term 重建）会整体重置窗口状态。
- 选字栏路径应只见 `compositionstart/end` 行与至多一条 `clear(reason=compositionstart)` 行：合成期间 229 守卫（`event.isComposing` / `session.iosImeComposing`）不开 pending 窗口、`compositionstart` 还会关掉已开的窗口，故**永不出现 `deliver` 行与 A 分支补发**。

**切片纪律（评审 MEDIUM-3，T0 验收条件）**：埋点代码不得在 `index.html` 的 inline script 里引入
新的模块级自由标识符；`?iosime-debug` 读 `location.search` 必须包在 `typeof location !== "undefined"`
守卫内（或整体收进一个函数、由 deps 注入）；测试侧字符串锚点边界包裹。

## 二、采集步骤

0. **前置条件**（评审 N7）：进入页面后**先在控制台确认出现 `[ios-ime] bound` 行**再开始记录——
   桥接有 ≤2s 绑定残留窗口，窗口内的敲击不产生 `probe`/`keydown` 等桥接日志，其数据无效。
1. `go run ./cmd/myworktree`（或既有 beta 二进制）起守护进程，iPad/iPhone Safari 打开
   `http://<lan>:<port>/?iosime-debug=1`，进入任一终端实例。
2. Mac Safari → 开发菜单 → 连接设备 → 选中终端页面 Web Inspector。
3. 按矩阵逐项操作并保存控制台记录：
   - ① 中文输入法直输数字 `123`；
   - ② 中文输入法直输标点 `，。！？`（含 Shift 符号键）；
   - ③ 中文输入法连按两次空格（触发 。转换——重点看 beforeinput/input 的
     deleteContentBackward + insertText 组合与 keyup 时的 textarea 值）；
   - ④ 英文（系统键盘）直输字母 `abc`；
   - ⑤ 选字栏候选输入"你好"；
   - ⑥ 长按连删（backspace 重复）；
   - ⑦ 实体键盘接入后重复 ①②（对照组）。
4. 每项同时在终端里 `cat -v` 观察字节是否到达（另一实例或先跑 `cat -v` 再切回）。

## 三、预期结论 → 方案校准映射

| 采集结论 | 对 T1 的校准 |
| --- | --- |
| `probe.t1` 早于 `input` 且 `valueAtProbe` 无新字符 | 第 3 环假设成立，flush 挂 keyup（延后一宏任务）的时序设计确认 |
| `valueAtProbe` 已含新字符（假设被证伪） | 回头排查其他丢弃路径（`_keyPressHandled` 等），必要时改挂 `input` 事件即时补发 |
| 英文字母直输成功（有 keydown 且 `composed=false`） | 维持 229 单一入口，范围不变 |
| 英文字母直输失败（同样无投递） | baseline 扩展到 beforeinput（预案扩展点），flush 条件不变 |
| 空格→。 呈现删后插等长替换 | delta 等长分支按 LCP 差分输出 `\x7f。`（残留已投递时语义正确）；观察是否出现 over-delete（未投递残留 + 转换的已知边界，plan.md §六） |
| keyup keyCode：**空格类 32、标点类 0**（评审 M4 修正——此前文档误写"恒为 0"；上游 #5835 日志实测即此分布） | flush 触发不按 keyCode 过滤（现设计如此），确认即可；`[ios-ime] keyup` 行的 `keyCode` 仅作观测记录，不设"异常"阈值 |
| 敲击间隔落在某区间 | failsafe 取值校准：**实测基线**（#5835 三次敲击 keydown→keyup = **12–34ms**）对照计划现值 750ms = **22–62 倍余量**（22–62× 对 keydown→keyup 12–34ms；对 keydown→插入 10–18ms 为 41–75×——与 plan §3.2 分歧表的数字配对一致）——现值是极端保守的兜底，非贴近交互的取值。若 T0 实测本机 keydown→keyup 同样在 10–40ms 量级，可议降到 200–300ms（仍有 6–25 倍余量，漏 keyup 时"字符卡住"从 0.75s 缩到 0.2–0.3s）——**UX 取舍由 T0 数据 + 评审定，本手册不预设结论** |
| 长按出现连续 229 keydown | 确认"每个 keydown 重臂 timer、baseline 取最早"的重臂语义（plan.md §3.2） |

数据回答上述问题后，评估诊断 flag 的保留/移除（与 #114 `[tty-metrics]` 保留先例的对照，
按两个独立决定分别评审，见文首），再进入 T1–T5 的正式实现与验证。
