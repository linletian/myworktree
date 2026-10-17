# Issue #115 修复计划：iPadOS/iOS 中文输入法直接键入无法输入终端

**日期**：2026-10-17（v8：**T0 真机数据已采集并回填——failsafe 定值维持 750ms 不下调**（实测 keydown→keyup 63–246ms，远高于 DATA-COLLECTION §三 假定的 10–40ms 量级，issue #124 的"降至 200–300ms"前提被证伪）；第 3 环假设在 47/50 例成立，3 例 `dt:null` 全部由分支 B 正确抑制、无丢字；§五 矩阵首轮已过，直输/标点/合成/死键四路全绿。v7：PR #123 第二轮评审仲裁落地——外部改写终结改 flush-then-terminate（via 增补 "external-rewrite"）、真值表坍缩对齐、终结者 clear 日志；v6 一轮仲裁（外部改写检测、current 坍缩、码点 LCP、T5 政策调整）见 A-5；三轮评审 53 条 + 实现轮 + PR 一/二轮全部落账）
**状态**：**T0 真机数据已落地，T1/T2/T3/T4 代码与文档已完成；failsafe 取值已定（维持 750ms，合并 n=73 敲击独立验证，`via` 直接证据为零触发）**；**T5 真机验证矩阵改为合并后（beta 渠道）执行——按维护者决定，不作为 PR 的合并门槛**（PR #123 评审更正评论）。**矩阵全部项均已有结论（2026-10-17 四轮，iPad Safari + Tailscale 远端 + Safari Web Inspector）**：6 项通过、3 项定性为**不适用或不可构造**（④ 英文软键盘不经过 textarea、⑥ 长按发 keyCode 32、astral 收缩方向三条理由不可达）——详见 §5.1 / §5.2 / §5.3。**failsafe 定案：维持 750ms 不下调。**
**Issue**：#115（bug，v0.6.0 develop / mwbeta）
**评审记录**：三轮评审共六份临时文档（两位评审各三轮），按其自身约定不提交、不忽略，已在
linletian 确认后删除；**逐条判定与处置完整落账于本文档附录**：附录 A（一轮 27 条）、
附录 A-2（二轮 16 条）、附录 A-3（三轮 10 条），合计 53 条全部关闭。
**任务边界**：本文档只含分析、证据、方案与任务清单；代码实现待本文档复审通过后进行。

---

## 一、问题与分析结论

**现象**：iPadOS/iOS 设备，输入法切到中文/日文后，终端实例中**直接键入**的数字（经设备端复测为半角）与标点无任何字节进入 PTY；通过**选字栏候选**选中的全角符号可正常输入。失败与全角/半角属性无关——失败面是**所有不走 composition 的"直接提交"输入**。

**根因链**（第 2 环为源码实证；第 3 环为**待 T0 证实的假设**，依据评审 M3 降级）：

1. 终端的唯一输入通道是 xterm 的 `onData`（`internal/ui/static/kinds/pty.js:324`；index.html legacy factory `:5585`），应用层无自建 IME 管线。`isTerminalQueryResponse`（`index.html:6042`）与服务端转发链均不含字符白名单（见 §七），**应用层过滤器不可能是元凶**。
2. **（实证）vendored `@xterm/xterm@6.0.0` 的 keydown 路径对 229 与延迟 diff 是同一条代码**。`_keyDown` 的结构（bundle 实证）：

   ```js
   _keyDown(e){
     if(this._keyDownHandled=!1, this._keyDownSeen=!0, …) return !1;
     if(!t && !this._compositionHelper.keydown(e)) return … , !1;   // ← 229 在此短路
     const i=(0,D.evaluateKeyboardEvent)(e, …);                     // ← 229 永不到达
     …
   }
   ```

   `CompositionHelper.keydown` 的尾部为 `return 229!==e.keyCode||(this._handleAnyTextareaChanges(),!1)`：keyCode 229 时先调度 `_handleAnyTextareaChanges()` 的 setTimeout(0) diff，再返回 false 令 `_keyDown` 短路——**`evaluateKeyboardEvent` 对 229 根本不会执行**（评审 M2 更正：此前把它归因为"不识别 229 的 keydown 路径"是错误的，229 与延迟 diff 是同一条路径）。
   同轮抵达的 `input` 事件则被第二条路径结构性丢弃：`_inputEvent` 守卫 `(e.data && "insertText"===e.inputType && (!e.composed || !this._keyDownSeen) && …)`——iOS 中文 IME 直提交的事件是 `composed=true` 且 keydown 已置 `_keyDownSeen=true` → 守卫判 false；vendored bundle 无任何 `beforeinput` 监听（grep 计数 0）。
3. **（假设，待 T0 证实）iOS 上 IME 对 textarea 的插入晚于第 2 环那个 setTimeout(0) 回调**，因此 diff 捕不到它、且不会复查；v6.0.0 的 `_keyUp` 仅重置 `_keyDownSeen=!1`，无任何补偿投递。支撑（评审 R3-A）：
   - 桌面 sogou 的同款时序（`docs/CHINESE_IME_ANALYSIS.md`，桌面场景、跨 IME，属推断）；
   - 上游 #5835 的行为描述 "often not sent"（其事件日志只记录 DOM 事件顺序，未记录 setTimeout 回调的实际执行时刻）；
   - **实测时间戳的间接定量支撑**（第三轮新补）：#5835 原始日志的三次敲击，keydown→首个 input 为 **10–18ms**，而 `setTimeout(0)` 回调的实际延迟为 **0–4ms** 量级——"插入晚于回调"有一个数量级的余量，不是勉强成立；插入均落在 keyup **之前 2–6ms**。
   - 明确边界：回调时刻仍未被直接记录，以上均为间接证据，**T0 同构探针（probe.t1 vs input 时间戳）为准**。自洽性论证：若插入早于该回调，diff 就会命中、也就没有这个 bug。
4. 同一 vendored 版本上 `compositionstart/end → triggerDataEvent` 路径工作正常——**选字栏候选可输入即为正证**。

**结论**：229 keydown 的延迟 diff 被插入时序打败（第 3 环，竞态），composed input 守卫结构性不可能命中（第 2 环）。应用层桥接本质上是 `_handleAnyTextareaChanges` 的重实现，**唯一区别是触发时机（keyup 而非 setTimeout(0)）**——评审 M2 的判断，本计划采纳。

## 二、上游对照与路线决策

| 事实 | 证据 |
| --- | --- |
| xterm.js **issue #5835 "iOS Chinese IMEs cannot input punctuation (including space)"** 即本问题（created 2026-04-24T01:28:24Z，state open），附 Safari Web Inspector 完整事件日志：keydown(keyCode 229) → beforeinput/input(`insertText`、`composed=true`) → keyup（空格类 keyCode **32**、标点类 keyCode **0**） | 上游 xtermjs/xterm.js |
| 修复 **PR #5836**（keydown 229 记 baseline → keyup 补发 textarea diff + timer 兜底 + compositionstart/end 取消）**至今 OPEN 未合并**（merged_at null）；变更 6 文件、+407/−24，其中 CompositionHelper.test.ts +218/−0、Terminal.test.ts +84/−2、TestUtils.test.ts +3/−0、CoreBrowserTerminal.ts +2/−0、Types.ts +1/−0、CompositionHelper.ts +99/−22（实现合计 +102/−22、测试合计 +305/−2，相加恰为 +407/−24；此前文档"218 行测试"及"实现 +218"均不精确，已按构成改正，评审 m12/N6/G3） | 上游 API |
| **升级 vendored xterm.js 无效**：npm `dist-tags` 实测 `latest=6.0.0`、`beta=6.1.0-beta.315`；PR #5836 未合入 master，**任何发布版（含 beta 预发布线）都不含该修复**。本仓库维持 **stable-only** 记档决策：不跟踪 beta 预发布线 | registry.npmjs.org |
| 升级即使可用，仍须重新审计 PR #110 对 v6 bundle 具体行为的依赖（Gesture 惰性、无 `scrollPixels`、cellHeight 近似、自定义滚动条样式），并保桌面中文 IME 修复不回归 | `scripts/download-xterm.sh`、CHANGELOG **`## v0.3.0` 条目**（CHANGELOG.md:194-202，"Upgraded terminal shell with **xterm.js v0.6.0**…"——注意该条目在 v0.3.0 节下，非 v0.6.0，评审 LOW-1 更正） |

**决策：应用层桥接**（issue 建议方向③，语义复刻上游 PR #5836 的**算法层**），在 `index.html` 内对 `session.term.textarea` 做 per-session 事件桥接，补发的字节走既有 `term.input()` 单一发送口。

**被否定的备选**：

- **vendored bundle 打补丁**：破坏 `download-xterm.sh` 的完整性校验与原子安装纪律，改的是压缩生成物，不可维护、不可回归测试。
- **`beforeinput` + `preventDefault` 拦截**：与 IME 对抗（部分 IME 依赖插入完成其内部状态机），且必须绕开选字栏的 composition 路径，误伤面大；上游维护者亦未采用此形态。
- **等待上游合并**：PR 零维护者互动（comments 0、review_comments 0、mergeable_state unstable），无时间表，不可作为依赖。
- **全量软键盘输入自建（Orca 式镜像 diff）**：`docs/ORCA_MOBILE_TERMINAL_INPUT_RESEARCH.md` 已论证其不可移植性；本问题只需补 composition 之外的最后一段投递。

## 三、桥接设计（`internal/ui/static/index.html`，应用层单文件）

> v4 按第三轮评审（Mavis G1–G7、opencode R3-A/B/C）修订：
> §3.2/§3.3 补齐空 delta 关窗真值表（G1，字段更名 `iosImeFailsafeArmed`）、§3.1/T0 埋点归属分层与
> 共享绑定（G2）、§二 PR 合计数更正（G3）、§3.2 分歧表补全（G4）、§8.1 grep 路径清理与横幅 ASCII 化
> （G5/G6）、§五 死键验证项（G7）、§一 第 3 环补实测时间戳支撑与 §六/DATA-COLLECTION 校准区间
> （R3-A）、§8.3 守卫读 bundle 路径改 CWD 相对（R3-C）。
> 前两轮变更（延后 flush、删除 textarea 清空、LCP 差分、emissions 语义、分块发送、指纹守卫等）保留。

**边界纪律（评审重点）**：

- 不触碰 vendored bundle；
- 不触碰两个 session factory——"不触碰"的可检查定义（评审问题 4）：`static/kinds/pty.js` 全文与 index.html 中 legacy factory 的 onData 块，`iosIme` 标识符出现数必须为 **0**，桥接符号只出现在 index.html；T3 按此断言；
- 不改 quick-keys 既有代码（仅共享 `session.term.textarea` 与 `term.input` 发送口）；
- 不 `preventDefault` 任何事件；不拦截 composition 路径（选字栏正例不回归）；
- **单一区块 + 可撤离（用户要求，详见 §八）**：全部桥接代码放在 index.html 一段 BEGIN/END 横幅包裹的连续区块内，无 `window.*` 导出，触点（绑定点调用、测试、signpost）全部带 `ios-ime` 标记可 grep；
- 逻辑以可切片纯函数为主，node 单测矩阵驱动。

### 3.1 绑定

`ensureIosImeDirectInputBridge(session)`：幂等，**绑定标志按 term 作用域**（`session.iosImeBridgeTerm === session.term` 才跳过——评审 M10：标志挂 session 而监听挂 term，一旦 session 对象上重建 Terminal 会静默把桥接关掉；按 term 记录后重建自动重绑）。仿 quick-keys `ensureQuickKeysImeBinding`（`index.html:3462`）模式。监听 `session.term.textarea` 的 `keydown` / `input` / `keyup` / `compositionstart` / `blur`；水印 `term.onData` 监听的 disposable 按仓库惯例挂 session（`session.iosImeDataDisposable`，随 term dispose 回收，评审 LOW-4/M10）。

**T0 诊断与 T1 桥接共用同一条绑定路径**（评审 G2，v3 修 N7 时引入的新矛盾的解）：打 textarea 事件日志本身就需要绑定，绑定动作天然存在，故

- **唯一绑定函数** `ensureIosImeDirectInputBridge(session)`：注册桥接监听，**并在 `?iosime-debug=1` 打开时额外登记 T0 诊断监听**（并在登记完成时发 `[ios-ime] bound`），发完关闭时这些监听**完全不存在**（不注册、零开销）；
- 幂等标志只有一个（`iosImeBridgeTerm`），两套监听不并列——T1 落地后 textarea 上不会出现两套并行监听；
- T0 阶段（T1 尚未写）时由 T0 自带绑定实现该函数：监听子集只有诊断行（`bound` / `keydown` / `probe` / `beforeinput` / `input` / `composition*` / `keyup`），**同一函数名、同一标志**——T1 落地即在其上叠加桥接监听，flag 语义不变；
- 文档 DATA-COLLECTION §一 的采集前置（"先见 `[ios-ime] bound` 再记录"，评审 N7）在 T0/T1 两阶段均可达。

绑定点（覆盖两个工厂的会话）：

- `renderTerminalSessions()`（`index.html:5908`）：遍历全 session，每次渲染（含 2s 轮询，经 `renderWorkspace` ← `refresh`）均绑定；
- `focusTerminalIfPossible()`（`index.html:6024`）：聚焦即绑定。注意该函数有多重早退（`ttyState!=='READY'`、无焦点、有 dialog、实例非 live）且只处理当前活动会话（评审 LOW-5），一般路径仍依赖 ≤2s 轮询，残留窗口与 quick-keys 同款（§六）。

### 3.2 状态机（状态挂 session，随 session 销毁）

| 事件 | 动作 |
| --- | --- |
| `keydown` 且 `keyCode===229` **且非合成中** | **外部改写检测与终结（PR 一轮 M1 + 二轮 R2-M1，仲裁裁定终版）**：先判 `textarea.value !== session.iosImeSeen`——xterm 的程序化改写（`^C`/ETX 清空、中键驻留）不入 input 事件，一旦发生，旧窗 baseline 与 seen 都已失真。处置为 **flush-then-terminate**（二轮裁定：一轮的 terminate-and-drop 复放证实丢字——未投递增量在 `keyup-defer` 尚未运行时被静默丢弃）：先 `flushIosIme(session, "external-rewrite")`（此刻 current=seen 仍可信，属于窗未投递增量的最后一次投递，物理键盘语义与 M7 blur 先例一致——结束/打断不吞已敲字节），再终结残余（清 pending/timer/armed；emissions 不动交由下次建窗复位；终结本身仅当 flush 后 pending 仍非空时打 `clear{reason:"external-rewrite"}`——与 blur 同构防双记）。其余：无 pending → 记 `baseline = textarea.value`；已有 pending → **保留最早 baseline，不重取**；**每个 229 keydown 都重臂** failsafe timer（750ms，iOS 长按连续发 229，重置语义见评审 M17）；更新 `iosImeSeen`。**composing 守卫（T1 实现轮评审 BLOCKER，仲裁裁定）**：状态机仅在 `event.isComposing !== true && session.iosImeComposing !== true` 时运行——bundle 实证 `compositionupdate` 只绘制、合成文本仅在 `_finalizeComposition`（compositionend）投递，合成期间水印必为空，无守卫的 229 窗口会走分支 A 把拼音中间态发进终端（评审 harness 实测 `"ihao你好"`）；iOS 直提交失败路径无 composition（#5835 日志无合成事件），守卫不会哑掉修复 |
| `input` | `iosImeSeen = textarea.value`（每次改动后刷新最后观测值） |
| `compositionstart` | 置 `session.iosImeComposing = true`；清 pending + 清 timer + **复位 `iosImeFailsafeArmed`**（窗口终结者统一：compositionstart 与关窗/blur 同类）；emissions 不动（composition 路径自有投递，不干涉） |
| `compositionend` | **常驻监听**（非 debug 门控）：仅清 `session.iosImeComposing`（日志行仍 debug 门控）——合成结束后下一个直提交 tap 必须能重新进入状态机 |
| `blur` | **flush-then-clear**：先按 §3.3 flush（评审 M7：只清不 flush 会丢字），再清 pending + timer + **复位 `iosImeFailsafeArmed`**（其自身 clear 路径终结窗口，同关窗语义）；并**无条件清 `iosImeComposing`**（compositionend 丢失时 blur 自愈，防 composing 锁死哑掉桥接；`ensureQuickKeysImeBinding` 的 blur 清 `session.composing` 为同款先例） |
| `keyup` | **不同步 flush**——排一个 `setTimeout(0)` 宏任务再 flush（评审 HIGH-2：不得与同轮尾部 `input` 赛跑；且 xterm 的 keydown timer 先于我们的 timer 入队，FIFO 保证它先跑完，水印得以裁决） |

**lastSeen 守卫**（评审 HIGH-3/M7 引出；**PR 轮仲裁修订两处**）：bundle 实证 xterm 自身有 3 处 `textarea.value=` 赋值——`_handleTextAreaBlur` 清空、`^C`/CR 的 `ETX` 分支清空、Linux 中键选区驻留。这些外部清空会晚于我们的观测发生，flush 时若直接读 live value，会算出 shrink → 发出**幽灵 `\x7f`**。规则：**flush 的 current 恒取 `session.iosImeSeen`**——live===seen 时二者本为同一字符串，分歧时（外部清空/改写）seen 是唯一可信源（PR 轮 Mavis 指出原三元 `live===seen ? live : seen` 是恒取 seen 的死代码、且注释会诱导未来维护者反向"修正"重新引入幽灵 DEL，故坍缩形状并明写语义；与 §3.3 真值表伪代码的形状差异记录在案）。同窗存续期内的外部改写由 keydown-229 的**外部改写检测**（上表 M1 行）在下一个 tap 探到并终结陈旧窗。

**failsafe timer**（750ms，**v8 定值维持不变**）：漏 keyup 的兜底。关窗规则（评审 N2，实现前必须定死；三支齐全由评审 G1 补齐）：

- **keyup 延后 flush 产出空 delta** 且 `iosImeFailsafeArmed` 为 false（第一次）：**不关窗**，置 `iosImeFailsafeArmed = true`，把"第二次机会"交给 failsafe；emissions 不动；
- **keyup 延后 flush 产出空 delta** 且 `iosImeFailsafeArmed` 已为 true（第二次，评审 G1 补的 else 支——同窗口内连按两次 229、IME 两次都没提交即可达）：**无条件关窗**（第二次机会已用过，不能再延期；否则 N2 的 pending 泄漏原样复现）；
- **failsafe 触发的 flush 无条件关窗**（无论 delta 是否为空）——它是最后一次机会，不是第三次机会；
- 非空 delta 的 flush：交付后关窗并清 timer。

关窗时 `iosImeFailsafeArmed` 一并复位（新窗口重新允许一次留窗机会）。

不关窗的理由（评审 MEDIUM-1）：flush 早于插入时若立即关窗，字符永失（成为下一次 baseline 后无人再补发）；有界地保留一次机会兼顾两者。无界保留的故障链（N2 实测推演）：死键 tap（229 无插入）→ 空 delta 不关窗 → failsafe 仍空 → 再不关 → pending 泄漏，后续按键沿用陈旧 baseline，叠加 lastSeen 守卫算出空/错 delta——与 issue #115 同类的静默吞字。有界化后该链断裂。

**failsafe 取值校准（v8，T0 真机数据定案——维持 750ms，不下调）**：

issue #124 曾计划"若真机 keydown→keyup 亦在 10–40ms 量级，可议降到 200–300ms"。**T0 实测该前提不成立**：iPad Safari 中文软键盘直提交路径，keydown(229) → keyup 实测 **n=53，min 63 / p50 146 / p90 222 / max 246 ms**（对照 DATA-COLLECTION §三 依上游 #5835 假定的 12–34ms，为其 **4–7 倍**）。分布规律：快速连打时（`123` 连击）达 190–246ms，慢速单敲仅 63–160ms——**峰值由快速输入贡献，而快速输入恰是最易丢 keyup 的场景**。

按余量换算（对实测 max 246ms）：750ms = **3.0×**；500ms = 2.0×；300ms = 1.2×；250ms = **1.0×（压线）**；200ms = **0.8×（抢在 keyup 之前）**。

**定案 750ms 的三条依据**：

1. **降到 250ms 会抢在 keyup 之前触发 failsafe。** failsafe 是"无条件关窗"，它一旦先跑，有界第二次机会（空 delta → armed → 等插入）即被饿死——正是上一段 N2 故障链要防的失效。
2. **首轮 53 次敲击 failsafe 一次都没触发**：所有 `deliver` 均紧随同拍 keyup 出现（delta 非空 → 交付并关窗），无一次延后至 keydown+750ms。750ms 是**纯安全网，未为 UX 付出代价**，下调换来的 UX 收益是虚的。（`via` 字段在本轮采集的控制台显示中被截断，此判断由时序推得；坐实需 `via` 值，见 §五 待补项。）
3. **探针→插入实测仅 5–37ms（p50 10ms），插入恒早于 keyup**。有界第二次机会保护的是"keyup 正常但插入迟到"的窗口，该窗口在慢敲下约 50–200ms 长；下调 failsafe 等于压缩这个窗口。

**备选（未采纳）**：降至 500ms。2.0× 余量仍安全，最坏"字符卡住"由 0.75s 缩至 0.5s，收益有限。

**flush 首行守卫**（评审 F6，明写为契约）：`if (!session.iosImePending) return;` 必须排在 term 守卫（`!session.term`）**之前**——keyup 延后 flush 与 blur flush 可交错（keyup 排下 D、blur 先跑并关窗交付、D 后到），第二次进入必须因 pending 已空而空转，正确性不能依赖未写下的约定。

**与上游 PR #5836 的五处有意分歧**（评审 N5/G4：披露，避免 T5 真机验证时把差异误判为回归；表供 T5 执行者一次看全，不要求其判断"算算法层还是实现层"）：

| # | 上游 PR #5836 | 本计划 | 理由 |
| --- | --- | --- | --- |
| timer 重臂 | `_ensurePending229Timer()` 首行 `if (this._textareaChangeTimer !== undefined) return;`——首个 229 装 timer、后续忽略 | 每个 229 keydown 重臂 | 改进：iOS 长按连续发 229，重臂使 failsafe 从最后一次 keydown 起算，避免长按中段误触发 |
| timer 时长 | `setTimeout(…, 0)`（单次宏任务） | 750ms failsafe | 改进：上游窗口只有一次宏任务，漏 keyup 即无兜底；750ms 覆盖迟到的 input（实测 keydown→插入 10–18ms，余量 **41–75×**；对照 keydown→keyup 12–34ms 则为 22–62×——PR 轮评审指出的数字配对勘误，原句把 22–62× 挂在插入区间下，见 R3-A 与 DATA-COLLECTION §三） |
| 关窗条件 | `if (dataSent || (timerFired && keyupFired)) _clearPending229()` | 空 delta 有界保留一次机会（见上三支） | 上游"两次空即关窗"在某些次序下等于立即关窗；我们有界不关窗补漏，但有界化杜绝其副作用 |
| current 取值 | 读 live `textarea.value` | lastSeen 守卫（读 `iosImeSeen`，§3.2） | 结构性需要：我们在 xterm 外部，读不到它内部的 diff 快照；外部清空（blur/^C）会污染 live |
| 发送粒度 | 单串 `triggerDataEvent(payload)` | 逐码点 `term.input()`（§3.3，评审 M6） | 结构性需要：`isTerminalQueryResponse` 按整串匹配（`^rgb:` 等），整块投递会被吞；逐码点与 xterm 原生逐键语义对齐 |

（"算法层复刻"仅指 LCP 差分与 baseline/补发/composition 取消的语义；上表五处为实现层的有意分歧，各有理由。）

**timer 与 session 销毁的边界**：failsafe timer 不随销毁路径清除（销毁路径在工厂内、不碰），但 flush 首行两道守卫（`!iosImePending` / `!session.term`，term 已 dispose 时 `term.input` 抛错亦被捕获）即静默返回——补丁不留悬挂行为，也不需要改任何销毁代码。（实现轮评审 NIT，仲裁记录：term 存在但 `textarea` 为 falsy 时 failsafe 不关窗——有界保留的窗口由 term 重建时的重绑复位兜底，见下。）

**绑定即重置窗口状态（T1 实现轮评审 MINOR，仲裁裁定）**：绑定按 term 作用域，窗口状态却挂 session——term 重建后陈旧状态会泄入新 term（旧 baseline 对新 `textarea.value`（""）算 pure-shrink → 幽灵 `\x7f` 进新终端；`iosImeComposing` 锁死 / `iosImeFailsafeArmed` 陈旧同理）。故 `ensureIosImeDirectInputBridge` 在置 `iosImeBridgeTerm` 后先初始化 `iosImeSeen = textarea.value`，随后**整体重置** `iosImePending` / `iosImeFailsafeTimer` / `iosImeEmissions` / `iosImeFailsafeArmed` / `iosImeComposing`（与实现行序一致）。

### 3.3 flush 算法（两个纯函数 + 复位语义）

**`iosImeDeltaBytes(baseline, current)`**（评审 M1：删除数必须显式定义——最长公共前缀 LCP 差分；**PR 轮仲裁：LCP 粒度官定为码点（`Array.from`），非 UTF-16 码元**——码元粒度在 astral 替换场景（`a😀`→`a😃`）会劈开代理对、发出孤立低代理到 PTY，且多码元字符被按码元计 DEL 数（多删）；码点粒度对全部 ASCII/BMP 序列逐位等价，仅修 astral；折叠测试进 T2 ⑭）：

```text
cpsB = Array.from(baseline); cpsC = Array.from(current)
current === baseline                             → ""
p = cpsB 与 cpsC 的最长公共前缀长度（码点）
removed = cpsB.length - p
appended = cpsC.slice(p).join("")
cpsC.length < cpsB.length                        → "\x7f"                       （纯缩短：单 DEL）
removed > 0（码点数相等或增长）                   → "\x7f".repeat(removed) + appended
否则                                             → appended
```

以上游 #5835 实测序列钉死：baseline `" ，！ "` → current `" ，！。"` → LCP=3、removed=1、appended=`"。"` → payload **`"\x7f。"`**（残留已投递时语义正确：删掉那个空格再插入。）。**未投递残留 + 转换**的 over-delete 是已知边界（上游同款，§六）。**纯缩短单 DEL 的上游 parity 实证（PR 轮 MINOR 挑战的裁定依据，REJECT 改为按 removed 计数）**：bundle `_handleAnyTextareaChanges` 缩短分支为 `t.length<e.length?this._coreService.triggerDataEvent(\`${a.C0.DEL}\`,!0)`——对任意缩短只发**一个** DEL（整个缩短串仅此一投），与 xterm 语义逐字一致；窗内多字符净删少删是上游同款行为，不属本补丁的回归面。

**`iosImeResidualPayload(delta, emissions, current)`**（emissions = 本 pending 窗口内 xterm 自身经 onData 投递的串数组；**仲裁修订**：签名增补 `current`——分支 C 的第二条件需要它，v4 伪代码漏写）：

| 分支 | 条件 | 行为 |
| --- | --- | --- |
| A | emissions 为空（xterm 没投递 = iOS 失败模式） | 返回 delta |
| B | delta 以 emissions 拼接串为前缀（拼接串非空；多敲击部分投递） | 返回剩余部分 |
| C | emissions 拼接串包含 delta、或包含 current **且 `current !== ""`**（xterm 自身路径已处理：桌面 sogou / composition 结束投递） | 返回空（静默） |
| D | 其余（如窗口内无关的焦点回报 `ESC[I`） | 返回 delta |

**仲裁修订两条（T1 实现轮）**：① C 的"包含 current"必须带 `current !== ""` 守卫——`includes("")` 对任意 emissions 恒真，会把真实的删除 payload（current 为空时）吞成静默（评审 B 变异实证）；② B 的条件带 `joined.length > 0`（与 A 去重）——对 payload 无影响，仅 `deliver.branch` 标签在 emissions 含空串元素时取 D 而非 B。**已知局限（记录不改码，评审 B challenge + 评审 A 复核支持）**：C 的第一子句按子串包含判定，窗口内无关投递若恰好包含单码点 delta（如 `ESC[I` ⊃ `I`、查询应答 ⊃ 数字）会被误判为 C 静默——上游等长/增长分支的真实投递形态（= appended 串 / 整个 current / DEL 串）均为精确相等或前缀场景，故该子句的真实正例未被观测到；列 T5 手测观察项，若真机复现再按投递边界收紧。

**emissions 复位语义**（评审 HIGH-1/M5）：① 建 baseline 时重置为 `[]`；② **关窗的 flush 完成后清空**（不关窗的保留——emissions 与 pending 同作用域，跨那一次 failsafe 机会持续累积）；③ **`iosImeSending` 重入标志**：水印监听在 `iosImeSending` 为真时不记录——桥接自己的 `term.input()` 会经 `triggerDataEvent` → `onData`（bundle 实证：`input(e,t=!0){this.coreService.triggerDataEvent(e,t)}`），不排除会自我污染；④ 条数/长度上限（防呆）。

**分块发送**（评审 M6）：payload 按**码点**逐个 `term.input()`，不整块发送。原因：`isTerminalQueryResponse`（`index.html:6042`）对整串做 `/^rgb:/i`、`/^;?\d+(;\d+)?R$/i` 等匹配，xterm 原生逐键投递永远单字符不命中，而桥接的一次性 delta（多敲击累积、转换替换）可能整串命中被吞——例如整串 `rgb:ff0000`。逐码点后每串仍是单字符，语义与 xterm 一致、且 `\x7f` 与插入段分路投递不影响终端。（注：quick-keys 粘贴同样经过该过滤器，属既有行为，本 issue 不扩大范围。）

**关闭顺序**（重入安全）：凡关窗的 flush（按 §3.2 关窗规则判定），先关窗（`pending=null`、清 timer、`emissions=[]`、`iosImeFailsafeArmed=false`）再发送；不关窗的 flush（keyup 空 delta 的第一次）只置 `iosImeFailsafeArmed=true`，不动 emissions；发送期间 `iosImeSending=true`；`term.input` 抛错（term 已销毁）时丢弃其余字节并复位标志（同 `flushQuickKeysQueue` 先例）。

**flush 全流程真值表**（评审 G1：把 §3.2 关窗规则 + 本节全部语义一次写全，实现者不需猜。`via ∈ {"keyup-defer","failsafe","blur","external-rewrite"}`——第四值系 PR 二轮 R2-M1 增补，外部改写终结前的最后一次投递）：

```js
flushIosIme(session, via):                        // via ∈ {"keyup-defer","failsafe","blur","external-rewrite"}（四轮评审后增补）
    if (!session.iosImePending) return;           // F6：必须第一行，早于 term 守卫
    if (!session.term) return;                    // §3.2 死会话（timer 存活于销毁路径）
    textarea = session.term.textarea; if (!textarea) return;
    current = session.iosImeSeen;                 // lastSeen 守卫（§3.2 二轮坍缩定稿：恒取 seen；历史三元
                                                  // live===seen?live:seen 是恒取 seen 的死代码，勿回退——
                                                  // 回退即重新引入外部清空的幽灵 DEL）
    delta = iosImeDeltaBytes(session.iosImePending.baseline, current);

    if (delta === "") {
        if (via === "failsafe") { closeWindow(); return; }      // 无条件关窗（N2）
        if (session.iosImeFailsafeArmed) { closeWindow(); return; }  // else 支（G1）
        session.iosImeFailsafeArmed = true;                     // 只留这一次机会
        return;                                                 // 不动 emissions
    }

    payload = iosImeResidualPayload(delta, session.iosImeEmissions, current);   // 三参（PR 一轮 A-5 修订）
    closeWindow();                                              // 先关窗再发送
    if (!payload) return;                                      // 分支 C 静默
    session.iosImeSending = true;                              // 重入标志：排除自身投递
    try { for (const cp of codePointsOf(payload)) session.term.input(cp); }  // 逐码点（M6）
    catch (e) { /* term 已销毁：丢弃其余字节 */ }
    finally { session.iosImeSending = false; }
```

**码点 DEL 计数的终端侧依据（PR 二轮 R2-MINOR-2 补证）**：PTY/shell 侧 DEL 的擦除单位是**字符**而非字节或码元——readline 对多字节（含 astral 4 字节）字符一次 DEL 擦除整个字符，故按码点计 DEL 与终端行为对齐；上游意志只覆盖 BMP 场景（bundle 缩短分支单 DEL），astral 属本补丁在码点粒度下的自然延伸。T5 矩阵增 `cat -v` 观察 astral 替换项（`a😀`→`a😃` 实测擦除/插入对数）。

**emissions 边界对齐不变式（PR 二轮 R2-MINOR-3 补注）**：分支 B 的 `delta.slice(joined.length)` 按 UTF-16 码元切——安全前提是 emissions 以**整串**入队（水印 push 的是 term.onData 的完整 data 参数、cap 拒收也是整元），`delta.startsWith(joined)` 成立时 joined 的边界落在 delta 的码点边界上（xterm 不投递断代理串）；该前提若破坏（未来有人按码元截断 emissions），分支 B 可能产出断代理——锁定为注释级不变式。

**分支 B / C 的真实职责**（评审 r3 §三：用 #5835 实测时间戳把五种时序逐事件代入后确认，非理论摆设——T5 逐条对照用）：

- **分支 B（delta 以 emissions 为前缀）** 只在**空格→。 转换且 xterm 的 0ms timer 恰好抽在 delete 与 insert 之间**时触发：xterm 先投 `DEL`（emissions=`["\x7f"]`），桥接 delta=`\x7f。` 以其为前缀 → 只补 `"。"`，净效果正确（xterm 的 DEL + 桥接的。= 各一次）；
- **分支 C 的第二条件（emissions 包含 current）** 只在**同一转换但 timer 抽在 insert 之后**时触发：xterm 走等长分支投出整个 current（水印=`[" ，！。"]`），delta=`\x7f。` 不被包含、但 current 被包含 → 桥接静默，不叠加（over-sent 归 §3.4 上游既有缺陷）；
- 两条子路径同一转换场景的两支，恰好被 timer 抽水点分隔；正常单字符路径由分支 A（xterm 未投递）与 C（xterm 已投递）各覆盖。

### 3.4 桌面不回归论证（含上游既有缺陷披露）

- 桌面中文 IME（sogou/系统）直提交时 xterm setTimeout 路径已投递 → 分支 C 静默；
- 若未投递（旧版首字丢失场景）→ 分支 A 桥接补发，行为改善而非回归；
- `composed=false` 的普通输入根本不进入桥接逻辑；
- composition 全程（候选选字）由 xterm 自有路径投递，桥接零行为；**仲裁修订（T1 实现轮评审 BLOCKER）**：v4 原表述"水印命中分支 C 或 delta 为空"为假——合成期间 xterm 不投递（仅 `_finalizeComposition` 投），水印为空，且合成文本逐键增长使 delta 非空，无守卫的桥接会走分支 A 泄漏拼音中间态（harness 实测终端流 `"ihao你好"`）。实际保证零行为的机制是 **composing 守卫**（§3.2）：合成中的 229 tap 根本不建窗、不臂 timer、不调度 flush，合成文本完全留给 xterm 自有路径；
- **上游既有缺陷披露**（评审 m16）：`_handleAnyTextareaChanges` 的等长分支 `t.length===e.length&&t!==e&&triggerDataEvent(t,!0)` 发送的是**整个 textarea 内容**（bundle 实证），上游 #5835 称其 "duplicated or over-sent data"。本桥接的分支 C 会正确静默——既不制造也不修复它；T5 验证时记为**既存问题**，避免误判为本次回归。

## 四、开发任务清单

| # | 任务 | 落点 | 产出 |
| --- | --- | --- | --- |
| T0 | 诊断埋点：`?iosime-debug=1` 门控的 textarea 事件流日志（keydown keyCode/key、input、composition*、keyup、textarea value）**+ 与 xterm 同构的 setTimeout(0) 时序探针**（评审 M3：现有 DOM 事件埋点回答不了"插入 vs 回调谁先"，探针在 keydown 内自排一个 0ms timer 打时间戳、与 input 时间戳对比——FIFO 下 xterm 的回调不晚于我们的探针，故探针早于 input 即证假设）；**+ 自带绑定实现**（评审 G2：唯一绑定函数 `ensureIosImeDirectInputBridge`，T0 阶段只登记诊断监听子集并发 `[ios-ime] bound`，否则 DATA-COLLECTION 的采集前置不可达——同一函数名、同一幂等标志 `iosImeBridgeTerm`，T1 落地即在其上叠加桥接监听，不留两套并行监听）；真机确认中/英键盘事件序列 | `index.html` | 事件流证据；校准 flush 边界（空格→。 转换序列、failsafe 取值——实测基线见 DATA-COLLECTION §三） |
| T1 | 桥接实现：两个纯函数 + ensure-binding + keydown/input/keyup/composition/blur/timer + onData 水印 + lastSeen 守卫 + 绑定点接线 | `index.html`（quick-keys IME 段邻位新区块；`renderTerminalSessions`；`focusTerminalIfPossible`） | 修复本体 |
| T2 | 单测：新建 `internal/ui/testdata/ios_ime_direct_input.test.mjs`，切片驱动**恰好 20 个 `^test(` 场景**（硬性验收，评审 F4：计数与 T4 CHANGELOG 数字词联动；≤20 才能用英文数字词拼写，`words` 表上限 twenty）：① 纯追加补发 ② 水印分支 C 抑制双发 ③ composition 不拦截 ④ 多敲击最早 baseline ⑤ 连续两窗互不吞字（HIGH-1）⑥ failsafe 补发 **+ 死键无插入→空 delta→有界二次机会→failsafe 无条件关窗→下一个键正常送达（N2，同一 scenario 的第二阶段）** ⑦ 重复 229 重臂 timer **+ 同窗口内两次"空 delta keyup flush"后窗口必定已关、下一个字符正常送达；`iosImeFailsafeArmed` 在关窗时复位（G1，同 scenario 的第二阶段）** ⑧ blur flush-then-clear **+ keyup 延后 flush 与 blur flush 交错只交付一次（F6，第二阶段）** ⑨ 外部清空后无幽灵 DEL ⑩ 上游等长序列 → `\x7f。` ⑪ 纯缩短单 DEL ⑫ emissions 作用域复位 ⑬ 重入标志排除自发 ⑭ 逐码点分块 ⑮ `rgb:` 整串不被过滤器吞 ⑯ 绑定幂等 ⑰ term 重建后重绑 ⑱ 死 term 容错 **+ timer 触发时 `session.term` 已销毁则 flush 静默（同 scenario 内断言，不新开 `^test(`）** ⑲ keyup 早于 input 次序单次投递 ⑳ 非 229 keydown 不拦截。**T2 实现轮补充钉死（评审第二轮，全部折叠进既有场景的第二阶段，不新增 `^test(`；每条均有变异证明——删除对应实现行恰使该场景失败）**：② 钉 `deliver` 先于 `clear(close-window)` 的日志顺序；③ 钉 composing 旗标置位/旗标单独守卫（无 `isComposing` 的 tap）与 compositionstart 前先臂窗的非空 armed 复位；⑧ 钉 blur 自愈清 `iosImeComposing`（compositionend 丢失）后直提交仍达；⑬ 钉真投递回声排除（`term.input` 内重放水印）；⑦ 钉 `deliver.armed` 的进入时值（首 flush false / 次 flush true）与空 delta 行 `branch:null`；⑰ 钉旧水印 disposable dispose 恰一次；⑱ 的 F6 断言用 **throwing term getter** 使守卫次序可判别。**PR 轮补充折叠（A-5，同不改 `^test(` 计数）**：① 钉 debug-OFF 常驻监听数恰为 6；⑨ 第二阶段钉 M1 外部改写→同窗次 tap 终结陈旧窗（幽灵 DEL 消失、新字符照常送达）；⑬ 第二阶段钉 emissions 双上限（条数 64 / 长度 4096 停止追加不抛错）；⑭ 第二阶段钉 astral 替换（码点 LCP：`a😀`→`a😃` ⇒ `["\x7f","😃"]`，无孤立代理）；⑲ 第二阶段钉 xterm-timer 先入队 FIFO 次序（注入模拟 xterm timer，断言先于 keyup-defer flush 运行）；harness 的 750 不再是魔数（切片求值 `iosImeFailsafeMs`，T0 校准改值不误红）。验收附加：切片锚点须命中 §八 的区块横幅（区块被挪走/横幅被删则 slice 阶段失败） | `internal/ui/testdata/` | 文件恰好 20 个 `^test(` |
| T3 | Go 侧：`terminal_status_test.go` 加 node wrapper（`TestIosImeDirectInputHandling`）+ `TestResponsiveLayerChangelogCounts` 表新增一行（短语 **`new issue #115 ios-ime scenarios`**、与现有七条互异、搭配数字词 **twenty**）+ 契约断言**全部按符号名**（评审 m14）：`static/kinds/pty.js` 全文 `iosIme` 计数为 0、legacy factory（`createTerminalSession`）的 onData 门锚点原样（`isInstanceTerminalStatus` / `isTerminalQueryResponse` 调用仍在原函数体内）、桥接发送口只有 `term.input(`、`iosImeSending` 重入标志存在 + **bundle 指纹守卫** `TestIosImeBridgeMatchesVendoredBundle`（§8.3：读 **CWD 相对路径** `static/vendor/xterm/xterm.js`——Go 测试的 CWD 是 `internal/ui/`，评审 R3-C；指纹 `_pending229Baseline` 在 vendored bundle 中 0 命中 ⇒ 补丁必须在位；≥1 命中 ⇒ 补丁痕迹必须清零；该 bundle 是 git 追踪资产（R3-C 已核），CI 可稳定读取）+ **signpost 归属（评审 A challenge，仲裁裁定归 T3/T4 同批）**：`scripts/download-xterm.sh` 的 `XTERM_VERSION` 行尾注释（§8.3 原文）随本批落地 | `internal/ui/terminal_status_test.go`、`scripts/download-xterm.sh` | `go test ./internal/ui/` 全绿 |
| T4 | 文档：CHANGELOG 条目落 **`## Unreleased`**（评审 LOW-6/M8），计数措辞必须是 **`… twenty new issue #115 ios-ime scenarios …`**——数字词与短语之间**只有空格**（不加反引号/括号，否则 `(\w+) ` + QuoteMeta(phrase) 零命中，评审 F1），且**不得**复用 "Pinned by N `node --test` cases" 句形（digits 形式会被 `TestTerminalStatusChangelogCount` 误捕；该句形是 terminal_status.test.mjs 专属）；本文档；DATA-COLLECTION.md | 仓库根、`docs/plans/fix-issue115/` | 文档闭环 |
| T5 | 真机验证矩阵（beta 渠道 + 手动清单，见下节）签字 | — | **合并后执行（beta 渠道）——按维护者决定不作为本 PR 的合并门槛**（PR #123 评审更正；follow-up issue 跟踪：probe 回填 + failsafe 校准 + §五矩阵签字） |

**T0/T2 验收附加（切片纪律，评审 MEDIUM-3）**：被切片区块内**不得引入新的自由标识符**（新增函数须整体可切片、依赖以 deps 注入）；`?iosime-debug` 读 `location.search` 必须在 `typeof location` 守卫内（或经 deps 注入）；测试字符串锚点边界包裹；切片块移动/重命名时 node 用例必须在 slice 阶段响亮失败，不允许以镜像副本通过。

## 五、真机验证矩阵（手动 / beta）

> **执行时机（PR 轮政策调整）**：合并后 beta 渠道执行，不阻塞 PR #123 合并；自动层已证逻辑，本矩阵证时序与真机行为，结论回填本文档与 DATA-COLLECTION。

- iPad/iPhone × Safari / Chrome(iOS)：中文半角数字、中文直输标点、英文软键盘字母（T0 确认项）；
- `cat -v` 观察字节流确实到达 shell；
- vim INSERT 模式下全角/中文输入；
- **选字栏候选全角符号（今日正例，必须不回归）**；
- 半角输入回归；
- 桌面中文 IME（sogou / 系统）全角符号回归（CHANGELOG v0.3.0 桌面修复不回归）；
- **既存问题观察项**（评审 m16）：等长替换时 xterm 自发整个 textarea 内容的 duplicated/over-sent 行为，记为上游缺陷、不算本次回归（§3.3 分支 B/C 真实职责已给出该场景的两条子路径）；
- **死键路径**（评审 G7：v3 定的核心关窗规则必须有对应手测项）：连按两次 229 而 IME 未提交任何文本（死键/语言切换键），随后输入一个正常字符，确认该字符送达、无重复、且无残留窗口影响；
- 快速连续输入、粘贴按钮、quick-keys 面板按键、Esc/Ctrl 组合与桥接共存无异常；
- **分支 C 首子句观察项（§3.3 已知局限的手测对照）**：启用 focus-events 的终端（vim）里，若窗口内同时出现焦点回报/查询应答等无关投递，且其文本恰包含用户直提交的单字符，确认该字符不被静默吞（理论面窄、未被观测到；真机复现则按 §3.3 仲裁修订的收紧方案处理）；
- **迟到 input 误判观察项（A-6，R2-MINOR-1）**：长按连击场景（ua 异序使前一 tap 的 input 晚于下一 keydown 时，外部改写检测会终结并前移 baseline——极窄）开 `?iosime-debug=1` 观察 `pending/clear/deliver` 序列是否出现首字丢失；
- **astral 替换 `cat -v` 观察项（A-6，R2-MINOR-2）**：`a😀`→`a😃` 经 IME 直提交，验证 DEL/插入对数与多字节擦除语义一致（码点 LCP 的终端侧依据实测）；（v7 更正：A-6 表格曾称该两条已入本矩阵，实为遗漏，本轮补入）
- T0 探针结论回填：插入 vs setTimeout(0) 回调时序、failsafe 750ms 校准值。**（v8 已回填，见下）**

### 5.1 首轮矩阵执行结果（2026-10-17）

**环境**：iPad + Safari，远端访问 `100.86.87.23:40751/?iosime-debug=1`（Tailscale），Mac Safari Web Inspector 观察；采集落在 session `89b67bf9642a`（`dev` 实例）；daemon `mwbeta` 构建于当日 14:16，晚于 PR #123 合并。

| 矩阵项 | 结果 | 证据要点 |
| --- | --- | --- |
| ① 中文直输数字 `123` | ✅ | 逐字 `keydown 229 → probe → beforeinput → input(dt 8–10ms) → keyup → deliver(branch A) → clear(close-window)` |
| ② 中文直输标点 `！@#¥%…（），。《》？/"\`：；` | ✅ | 同上，50+ 字符全部 branch A 补发成功 |
| ③ 连按两次空格触发 `。` 转换 | ✅ | baseline `…； `(len 44) → current `…；。`(len 45)，delta = **`\x7f。`**（`\x7f` 在控制台不可见，渲染为 `delta: 。"`）——**正是 §3.3 LCP 差分预测的"删后插"输出**，branch A 补发 |
| ④ 英文软键盘直输 `abc` | ⚠️ 部分 | keyup 到达（keyCode 65/66/67）但**无 229、无 bridge 事件**。**第二轮已定性**：该路径不经过 textarea（无 keydown、无 `input`、`value` 全程不变），字符由 xterm 另路送出——本项不能用 bridge 日志判定，只能用终端回显，且不命中本次修复面。见 §5.2 |
| ⑤ 选字栏候选输入「你好」（`nihao`） | ✅ | `compositionstart → insertCompositionText(ni/ni h/ni ha/ni hao/你好) → deleteCompositionText → insertFromComposition → compositionend`；**零 deliver 行、零 branch A 补发**，与 DATA-COLLECTION §一 预测一致；229 窗口在 `compositionstart` 即被 `clear{compositionstart}` 关闭 |
| 死键路径 | ✅ | `CapsLock`（keyCode 20）/`Shift`（keyCode 16）keydown 有 probe 但 keyCode ≠ 229，**状态机完全未介入**（无 pending 行、无 timer）——与 T2 scenario 20 一致 |
| ⑥ 长按连删 | ⚠️ 数据不足 | 两批 Backspace（keyCode 8）**只记录到 keyup、无 keydown 229**，与正常敲击行模式不一致；无法区分是控制台截断还是真实行为 |
| ⑦ 实体键盘对照组 | ✅ 已于 §5.3.1 补做 | 首轮未执行。补做结果**证伪了原预判**：IME 开启时实体键盘同样产生 keyCode 229、桥接照常介入，对照组以"无回归"通过 |

**第 3 环假设判定**：**在 47/50 例成立**（`input.dt` 为 5–37ms，即插入晚于 0ms 探针回调，与 plan §一 假设一致）。**3 例例外 `dt: null`**（插入早于探针）：`@` / `#` / `》`。

**例外行的正面结论**（回答 A-6 / R2-MINOR-1 观察项）：这 3 例 `deliver` **全部走 branch B** 且 `emissions` 携带该字符——即 **xterm 自身已投递成功**，桥接水印正确抑制了重复发送，**无首字丢失**。两种事件序均被正确处理。故 `valueAtProbe` 已含新字符这一分支**不需要回头排查 `_keyPressHandled` 等丢弃路径**（该排查仅在"假设被证伪 **且** 字符丢失"时才触发），但"第 3 环假设是强假设而非不变式"这一事实应记入已知边界。

### 5.2 第二轮矩阵执行结果（2026-10-17，session `1d13eeebd40b` / `docs`）

**环境**：同首轮；改采 running 实例 `docs`（`1d13eeebd40b`），并预置 `window.__im` 缓冲以绕开 Safari 控制台保留上限的截断。

**时序复现 —— failsafe 定案得到独立验证**：本轮 229 直提交敲击 n=10（min 124 / p50 144 / p90 210 / max 227ms）。两轮合并 **n=63，min 63 / p50 146 / p90 222 / max 246ms，>750ms 的敲击 0 次**。750ms 对实测 max 仍为 3.0×，250ms 仍为 1.0×。**维持 750ms 的定案不再是单轮观察。**

**`via` 字段直接坐实（关闭首轮待补第 4 项）**：本轮 10 次 `deliver` 的 `via` 分布为 **`{"keyup-defer": 10}`，`failsafe` 计数为 0**。首轮"failsafe 从未触发"由时序推断升级为直接证据——两轮 63 次敲击中 750ms 定时器从未触发，确认其为纯安全网。

**长按连击走 keyCode 32 而非 229（关闭首轮待补第 2 项，结论与预期相反）**：长按空格 1939ms 内产生 **17 次 keydown，keyCode 全为 32、间隔稳定 102ms、末尾仅 1 次 keyup**；全程 **229 状态机窗口数 = 0**（无 `pending` / `deliver` / `clear`）。对照首轮两次空格**点按**有 keydown 229 ⇒ **点按发 229、自动重复发 32**。

⇒ plan §3.2 中 M17 的"iOS 长按连发 229、故每次 keydown 重臂"前提，**在本设备不成立**；「每次 229 keydown 重臂 + 保留最早 baseline」是**防御性代码路径，真机不可达**，而非活路径。T2 scenario 4 / 7 仍以单测钉住该语义。收口条件：再试长按数字键与符号区标点键，若同为 32 即按此结论结案。

**`cat -v` 端到端字节验证通过（关闭首轮待补第 1 项）**：在 `docs` 实例 `cat -v` 下采集，输出行解码为 `!@123[17空格]abc!@1nihao你好！@123！@123!2@123`：

| 转义 | 解码 | 码点 | UTF-8 |
| --- | --- | --- | --- |
| `M-oM-<M-^A`（2 处） | `！` | U+FF01 | `ef bc 81` ✅ |
| `M-dM-=M- ` | `你` | U+4F60 | `e4 bd a0` ✅ |
| `M-eM-%M-=` | `好` | U+597D | `e5 a5 bd` ✅ |

17 个空格与 17 次长按 keydown **精确对应**；全角多字节流完整到达 PTY，未截断、未错位。**bridge 层证据链至此贯通到 PTY 层。**

**英文软键盘不经过 textarea（改写矩阵第 ④ 项判据）**：`abc`、`!@123` 及首尾的 `nihao` **只有 keyup，无 keydown、无 `input`、textarea `value` 全程不变**，但字符确实到达 PTY（`cat -v` 输出可见）。⇒ iOS 软键盘非 IME 路径**不经 textarea diff**；矩阵第 ④ 项**不能用 bridge 日志判定，只能用终端回显**，且本就不走 229 路径、不会命中本次修复面。首轮该项的"待确认是否走了分支 D"悬案就此消解——它根本没进桥接状态机。

**合成路径无泄漏（澄清项）**：`cat -v` 输出中 `nihao` 字面量经确认为**切换中文前用英文键盘的一次独立输入**，其后 `你好` 为 IME 合成结果，与实际输入相符。合成期间 textarea 暂存的 `nihao` 被 `deleteCompositionText` 清除、仅 `insertFromComposition "你好"` 出货，**中间态拼音未泄漏到 PTY**。

**分支 B 是罕见路径**：两轮 63 次中仅首轮的 3 例走 branch B（`dt:null`），本轮 10 次全为 branch A、`dt` 无一为 null。已在两轮中各被自然观测到一次即足够钉死正确性，不再专门造场景。

**附带发现（非本 issue 范围，登记备查）**：`tty ws closed: 1005` 重连间隙内键入的字符**仅有 keyup、无 keydown / 无 `input`、textarea 全程为空，整段丢失**（紧邻 `！@123` 正常段）。桥接完全未被触发，故非 ios-ime 问题；但**重连后未重新 focus 终端会导致静默丢输入**，属终端连接层缺陷，建议单独立 issue。

**§5.2 收尾时的剩余待补**（均已在 §5.3 解决或定性）：

1. **实体键盘对照组**（矩阵第 ⑦ 项）——接入实体键盘后重复 ①②。
2. **astral `cat -v` 观察项**（A-6 / R2-MINOR-2）——`a😀`→`a😃` 经 IME 直提交，验证 DEL/插入对数与多字节擦除语义（码点 LCP 的终端侧依据）。

### 5.3 收尾两轮：对照组重定性 + astral 判据不可构造（2026-10-17）

#### 5.3.1 硬件键盘对照组 —— 预测被证伪，结论重定性

原计划预判"实体键盘 ⇒ keyCode 为真实键码、不产生 229 ⇒ `pending`/`deliver` 应为 0、桥接完全惰性"。**实测证伪**：接入实体键盘后重复 ①②，8 个字符（`123` + `！@#¥%`）**每一个都是 `keydown 229` → `pending` → `deliver(branch A)` → `clear`**，桥接 8 次全部介入。

⇒ **keyCode 229 是输入法（IME）的属性，不是输入硬件的属性。** 中文 IME 开启时，实体键盘按键同样被 IME 截获并派发 229，桥接行为与软键盘**不可区分**。真正使桥接惰性的条件是 **IME 关闭（输入源切英文 ABC）**，而非硬件差异。

对照组本意（"接入实体键盘后不回归"）**已达成**：8/8 字符经 `cat -v` 字节精确匹配（`M-oM-<M-^A`=`！`、`M-BM-%`=`¥`），8/8 branch A 补发且 `emissions` 全空（xterm 未自投递、无重复），0 分支 B、0 个 `dt:null`、0 次重臂，时序 min 156 / max 226ms。

#### 5.3.2 astral 观察项 —— shrink 方向在软键盘上不可构造

**已证实（追加方向）**：emoji 直提交经 `keydown 229` 进入状态机，`deliver` 的 `delta` 为 `"😀"` 与 `"😃"`——**各是一个完整码点**，`term.input` 按码点单发、**未拆代理对**（T2 scenario 14 的不变量在真机成立）。astral 内容下的 branch B 抑制亦自然观测到第 3 次（`emissions: ["😃"]` → 不重复发送），`dt:null` 异序路径同为第 3 次，均无丢字。

**未证实（收缩/替换方向），且判定为设备不可构造**，三条独立理由：

1. **软键盘退格不进 textarea** —— 两处尝试均为 `keyup{keyCode:8}` 而无 `beforeinput` / `input`，`textarea.value` 全程不变（两次都停在 `"😀"` / len 2）。因此 `deliver` 的 delta 恒为纯追加，**永远走不到产生 DEL 的分支**。
2. **PTY 行规程先吃掉 DEL** —— canonical 模式下 line discipline 将 0x7f 当作"擦除行缓冲末字符"处理，应用层收不到该字节，故 **`cat -v` 结构上不可能显示 `^?`**，DEL 计数判据无法从终端侧观测。
3. **终端转录不可作证** —— 本轮 `cat -v` 输出为人工转录，其中一行含截断的 UTF-8 序列（`F0 9F 98` 后接完整序列），与控制台日志矛盾（delta 均为完整码点），判定为转录误差、**该行作废不作证据**。

⇒ **结论**：A-6 / R2-MINOR-2 的"DEL/插入对数"判据在本设备的软键盘路径上**不可达**。该方向由单测承担覆盖——T2 scenario 11（`"abc"→"a"` 恰好 **1 个** DEL、纯收缩绝不两次）与 scenario 14（astral 按码点单发、绝不吐孤立代理）。**记录为"未构造出该场景"，不记作"已验证"。**

#### 5.3.3 附带发现（不在 #115 范围，登记备查）

- **emoji 键盘插入走 `keydown` keyCode 0**，非 229 ⇒ 桥接不开窗；配合终端表现看，该字符**疑似未抵达 PTY**。属 vendored xterm 自身的既有缺口（#115 之前即存在，非本次修复引入的回归），建议单独立 issue。
- **`tty ws closed: 1005` 重连间隙内键入的字符整段丢失**（见 §5.2）：终端未重新 focus，键盘输入静默丢弃。属终端连接层缺陷。

#### 5.3.4 矩阵终态

| 项 | 结论类型 |
| --- | --- |
| ① 中文直输数字 | ✅ 通过 |
| ② 中文直输标点 | ✅ 通过 |
| ③ 空格→。转换 | ✅ 通过（delta = `\x7f。`，LCP 差分） |
| ④ 英文软键盘直输 | ⚪ **不适用** —— 该路径不经过 textarea、不命中修复面，判据须为终端回显 |
| ⑤ 选字栏合成 | ✅ 通过（零 deliver，中间态无泄漏） |
| ⑥ 长按连击 | ⚪ **不适用** —— 发 keyCode 32 而非 229，229 状态机零介入；重臂为防御性代码 |
| ⑦ 硬件键盘对照组 | ✅ 通过（无回归；桥接行为与软键盘不可区分，见 5.3.1） |
| `cat -v` 端到端字节 | ✅ 通过（三轮，合并 n=73 敲击 / max 246ms / 750ms 余量恒 3.0×） |
| `via` 坐实 | ✅ 通过（`{"keyup-defer": 10}`，failsafe 零触发） |
| astral 追加方向 | ✅ 通过（按码点单发，代理对未拆） |
| astral 收缩方向 | ⚪ **不可构造** —— 见 5.3.2，由 scenario 11/14 承担覆盖 |

**T5 矩阵全部项均有结论**（6 项通过、3 项定性为不适用/不可构造）。failsafe 定值维持 750ms，两轮以上独立验证。


## 六、风险与边界（显式记录）

- **残留窗口**：新建会话在首次渲染前键入的字符不被桥接（≤2s 轮询 + 聚焦绑定；quick-keys 同款先例）。
- **flush 早于插入**（评审 MEDIUM-1、R3-A 三）：延后 flush 已把窗口压到"keyup 早于 input 且浏览器把 input 派发到我们 timer 之后"的次序；该次序下若 xterm 也丢弃（`_keyDownSeen` 已被 keyup 重置、`_inputEvent` 实际会放行自愈），字符才真的丢失——空 delta 有界不关窗（§3.2）+ failsafe 提供第二次机会，且 failsafe 自身无条件关窗（评审 N2），杜绝 pending 泄漏吞后续按键。**实测（#5835 三次敲击）插入均早于 keyup 2–6ms 抵达，该风险在现有证据下是假设性的、未在实测中复现**；T0 探针可证伪（probe.t1 晚于 input 即说明插入更早）。
- **未投递残留 + 转换的 over-delete**（评审 M1 边界）：残留从未进入 PTY 时，等长替换的 `\x7f` 会删掉 shell 里上一个真实字符。触发条件：残留未投递（桥接未绑定的 ≤2s 窗口内键入）**且**随后发生 IME 转换；上游 PR #5836 同款行为。per-tap 投递使残留窗口极小。
- **"keyup 早于 input"是未经实测的假设次序**（评审 F5/N8 改写）：上游 #5835 的实测事件序是 keydown → input → keyup（input 先于 keyup 派发），该次序下 xterm 的 keydown timer 于 keyup 后才跑、桥接延后 flush 更晚，整轮恰好一次投递，无异常。为完整性保留假设次序的两个互补分支——timer 抽水晚于插入时 **xterm 内部双投**（`_inputEvent` 放行一次 + timer 再投一次，与桥接无关，水印分支 C 保持桥接静默）；二者取决于 timer 与插入的先后，是互补两支而非矛盾。均记为上游观察项，T0 真机日志可证伪/证实。
- **水印观察点在网关之前**（评审 MEDIUM-4）：`isInstanceTerminalStatus` 拒收门与 `isTerminalQueryResponse` 过滤都在 onData 处理器内部，被拒收/被过滤的数据也会进水印。判定为**已知接受**：状态门只拒收已消亡实例（`starting` 是放行的，`pty.js:328-334` 注释），拒收时会话正在销毁、输入无意义；查询应答属分支 D 已预见范围。
- **lastSeen 守卫的失效方向是安全的**（评审 F7）：其失效模式（`iosImeSeen` 因未预期的外部改写失真）最坏结果是**漏发**（用旧值算出空 delta），而非多发 DEL——安全的一侧。这是有意设计而非凑合；注意其安全性以 N2 的有界关窗为前提（pending 泄漏时陈旧 baseline 会把它推向 spurious DEL 那一侧）。
- **诊断 flag 的两个独立决定**（评审 LOW-3）：(a) 门控机制 = URL 参数（纯前端，比 #114 的 cookie + Go build tag 更小）；(b) 是否长期保留 = T0 数据出来后单独决定（与 #114 的保留先例对齐与否另行评审），不在本计划预先捆绑。
- **英文软键盘直输**：代码分析预期其走 `composed=false` 的 input 路径本就可输入；若 T0 证伪，按预案把 baseline 扩展到 `beforeinput`（设计预留扩展点，不推翻架构）。
- **漏 keyup**：750ms failsafe 兜底；多 229 keydown 取最早 baseline（上游语义）、每个 keydown 重臂 timer。

## 七、证据索引

> 行号均为 **T1 前基线 `781f30f`**（评审 m14：T1 改动后行号必然漂移，T3 断言一律按符号名）。

- Issue #115 正文与设备端复测评论（失败与全角/半角无关；选字栏路径无障碍）
- vendored bundle 实测（`internal/ui/static/vendor/xterm/xterm.js`）：`_keyDown` 中 `!t && !this._compositionHelper.keydown(e)` 先于 `evaluateKeyboardEvent` 短路；`CompositionHelper.keydown` 尾部 `return 229!==e.keyCode||(this._handleAnyTextareaChanges(),!1)`；`_inputEvent` 的 `composed/keyDownSeen` 守卫；`_handleAnyTextareaChanges` 的 setTimeout diff（grow/DEL/等长发整个值三分支）；`_keyUp` 仅置 `_keyDownSeen=!1`；`input(e,t=!0){this.coreService.triggerDataEvent(e,t)}`；`beforeinput` 0 处；`textarea.value=` 3 处（blur 清空、ETX/CR 清空、Linux 中键选区）
- `docs/CHINESE_IME_ANALYSIS.md`（v0.5.x、桌面 sogou：IME 插入晚于 setTimeout 回调——桌面场景，属推断）
- 上游 xterm.js issue #5835（created 2026-04-24T01:28:24Z，open）、PR #5836（open，merged_at null，+407/−24、6 文件）
- `docs/ORCA_MOBILE_TERMINAL_INPUT_RESEARCH.md`（移动端输入调研）
- 输入链路现状：`internal/ui/static/kinds/pty.js:324`、`internal/ui/static/index.html:5585`（legacy onData）/`:5908`（renderTerminalSessions）/`:6024`（focusTerminalIfPossible）/`:6042`（isTerminalQueryResponse）/`:3462`（ensureQuickKeysImeBinding）/`:4705`（renderWorkspace 调 syncQuickKeysPanel）
- 服务端转发链（评审 m13/LOW-2 更正）：`internal/app/app.go:2424`（HTTP 输入）/`:3110`（WS 输入）/`:3647`（工具输入）→ `internal/framework/manager.go:528`（`Manager.SendInput`）→ `internal/instance/pty/driver.go:159`（`Handle.SendInput`：仅把 `0x03/0x1A/0x1C` 转换为 SIGINT/SIGTSTP/SIGQUIT 信号并写入，其余逐字符原样写 PTY——**无字符白名单**，原 issue 所引 `app.go:2943` 实为 tty 回放游标发布块，已弃用）
- npm `dist-tags`：`latest=6.0.0`、`beta=6.1.0-beta.315`

---

## 八、可移除性设计（模块化边界）

> 用户要求：补丁必须控制好模块化边界，上游 xterm 官方版本解决本问题后，我们的补丁应能**快速撤掉**。
> 本节把该要求落成可执行的结构约束、守卫测试与移除清单。

### 8.1 物理边界：单一区块 + 可 grep 触点

- **单一区块**：全部桥接代码（两个纯函数、ensure-binding、事件处理、水印、T0 诊断埋点）在 `index.html` 中位于**一段连续区块**，首尾用 **`//` 行注释横幅**（评审 N4：HTML 注释 `<!--` 在 inline script 的 Annex B sloppy 语义下会把该行剩余部分整段吃掉，与代码同行的横幅会让 `new Function` 切片 harness 以难定位的方式失败，且未来搬进 module/打包器直接语法错误——禁用），且横幅**纯 ASCII**（评审 G6：切片 harness 的 `sliceBlock()` 用 `src.indexOf(header)` 逐字符相等，em dash/智能标点会被一次编辑器替换变成 `-`，令 T2 在 slice 阶段以"锚点找不到"失败），删除时整段选中即可：

  ```js
  // ==== ios-ime bridge (issue #115) BEGIN - removable block ====
  …（本 issue 的全部应用层代码；标识符一律 iosIme 前缀）…
  // ==== ios-ime bridge (issue #115) END - removable block ====
  ```

- **零外溢**：区块内不新增 `window.*` 导出、不改任何既有函数体；所有 session 状态字段用 `iosIme` 前缀（`iosImePending` / `iosImeSeen` / `iosImeEmissions` / `iosImeSending` / `iosImeFailsafeArmed` / `iosImeFailsafeTimer` / `iosImeBridgeTerm` / `iosImeDataDisposable` / `iosImeComposing` / `iosImeProbeT1`——评审 R3-B：`failsafeArmed` 无前缀会脱离下述 grep pattern，"零外溢"论证与 §8.4 的零命中收尾断言都建立在前缀一致性上；后两者为 T1 实现轮新增，评审 A NIT 指出 v4 清单漏记，仲裁补入），既有代码零引用这些字段，删除后无残留引用。
- **触点全集可枚举**（评审 N3/F3/G5：pattern 必须覆盖驼峰与下划线变体、include 必须含 `.mjs`——实测 `--include='*.js'` 不匹配 `*.mjs`；搜索路径**不含 `docs/plans/fix-issue115`**——include 集合不含 `.md` 时该目录是死路径，文档归档由 §8.4 第 6 步单独处理）：每处触点行内带标记，一条命令即得完整清单：

  ```
  grep -rn 'iosIme\|ios_ime\|IosIme\|ios-ime' internal/ui cmd scripts \
    --include='*.html' --include='*.js' --include='*.mjs' --include='*.go' --include='*.sh'
  ```

  | 触点 | 位置 | 内容 |
  | --- | --- | --- |
  | 绑定点 ×2 | `index.html` `renderTerminalSessions()`、`focusTerminalIfPossible()` 内各一行 `ensureIosImeDirectInputBridge(session);` | 标记注释包裹 |
  | 测试文件 ×1 | `internal/ui/testdata/ios_ime_direct_input.test.mjs` | 整体 |
  | Go 测试 ×3 处 | `terminal_status_test.go`：`TestIosImeDirectInputHandling` wrapper、`TestResponsiveLayerChangelogCounts` guard 表一行、`TestIosImeBridgeMatchesVendoredBundle` 指纹守卫 | 整函数/整行 |
  | signpost ×1 | `scripts/download-xterm.sh` `XTERM_VERSION` 行尾注释 | 一行 |

### 8.2 自中和：上游修复落地后补丁自动静默

上游 PR #5836 的修复形态是 `_keyUp` → `CompositionHelper.keyup(ev)` **同步补发**。若它随 xterm 升级进入 vendored bundle：

- xterm 的 keyup 监听在 `term.open()` 时注册、早于桥接绑定 → 同一 textarea 上**它的 handler 先跑**，同步 emit → onData → 桥接的水印监听先记录；
- 桥接的 flush 在 keyup 后延一个宏任务，此刻水印非空 → 分支 C 判定"xterm 已投递" → **桥接静默，不会双发**。

即：升级 xterm 后即使忘记撤补丁，行为上也退化为 no-op；撤补丁从"正确性紧急事项"降级为"卫生事项"，可从容排期。

**契约性告警**（评审 N9）：上述"注册顺序"（xterm 于 `open()` 绑 keyup、桥接在后）是**当前 bundle 实现的事实，不是 xterm 的公开契约**——上游若改为事件委托或重排注册，自中和会静默失效而无人察觉。因此 §8.3 的指纹守卫是真正的兜底，两者不互相替代：注册顺序负责"双发不发生"，指纹守卫负责"补丁终将撤掉"。

### 8.3 触发器与 bundle 指纹守卫

- **触发器**：`scripts/download-xterm.sh` 的 `XTERM_VERSION` 升级到含上游修复的版本。**判定用 bundle 指纹，不用版本号**（评审 N1/F2：`!= "6.0.0"` 谓词过宽——一次与 #115 无关的例行 bump（6.0.1/5.x 回退/7.x 大版本）都会逼人撤掉仍然必需的补丁，把例行升级变成无人察觉的功能回归；而 PR #5836 至今 open，"下一个发布版"与"含修复"不等价）。
- **指纹**：`_pending229Baseline`（PR #5836 新增的 CompositionHelper 字段，语义即"229 pending 基线"，不会与无关代码撞名）。当前 vendored bundle 命中 **0**；对照组 `_dataAlreadySent` 命中 **4**——证明私有字段名在本项目压缩产物中**不做 mangle**，指纹方法可用（实测计数：`_flushPending229`/`_pending229TimerFired`/`_textareaChangeTimer` 同为 0，可作备用指纹）。
- **守卫测试** `TestIosImeBridgeMatchesVendoredBundle`（T3，读 **`static/vendor/xterm/xterm.js`**——评审 R3-C：Go 测试 CWD 是 `internal/ui/`，须用 CWD 相对路径，照仓库根相对字面路径写会首次运行即 `no such file or directory`；该 bundle 是 git 追踪资产，CI 稳定可读），计指纹命中数：
  - 指纹 **0 命中**（bundle 不含上游修复）→ 断言**补丁必须在位**：index.html 含区块 `// ==== ios-ime bridge` 横幅、`iosImeDeltaBytes` 与 `ensureIosImeDirectInputBridge` 符号、`testdata/ios_ime_direct_input.test.mjs` 与 Go wrapper 存在；
  - 指纹 **≥1 命中**（bundle 已含上游修复）→ 断言**补丁已撤**：上述横幅/符号/文件/wrapper/guard 表行全部不存在；
  - **不做版本号断言**——"版本未知与已确认含修复"的状态区分由指纹承担；上游若以不同于 #5836 的形态修复，指纹不命中、守卫要求补丁在位（**安全侧**：宁可留着会自中和的桥接，不可误删），届时按 §8.4 人工评估。
  - 效果：升级 xterm 而忘撤补丁，CI fail 并指向 §8.4 清单；反向防止补丁被误删后 bundle 仍未含修复。
- **signpost**：`scripts/download-xterm.sh` 的版本行加注释：

  ```sh
  XTERM_VERSION="6.0.0"   # ios-ime-115: 早于上游 #5835/#5836（iOS IME 直提交修复）；index.html 的 ios-ime bridge 是补偿层，升级版本后按 docs/plans/fix-issue115/plan.md §八清单移除
  ```

  （升级者改版本号时必然路过这行。）

### 8.4 移除清单（上游修复发布后执行）

1. 删除 `index.html` 中 `// ==== ios-ime bridge` BEGIN/END 横幅之间的整段；
2. 执行 §8.1 的 grep，删除全部触点：2 处绑定点调用（含标记注释）、`download-xterm.sh` signpost；
3. 删除 `internal/ui/testdata/ios_ime_direct_input.test.mjs`；
4. 删除 `terminal_status_test.go` 中 `TestIosImeDirectInputHandling`、`TestResponsiveLayerChangelogCounts` 表中对应行、`TestIosImeBridgeMatchesVendoredBundle` 本身；
5. **收尾断言**：复跑 §8.1 扩容后的 grep（路径与 pattern 均按 §8.1 现状），必须**零命中**（评审 N3：漏一个驼峰/下划线触点就是残留引用；评审 G5 提醒：不要把 `docs/plans/fix-issue115` 加回搜索路径——`.md` 不在 include 集合内，该路径本就零命中，文档归档见第 6 步）；
6. `docs/plans/fix-issue115/` 整体归档（本目录的 plan.md 与 DATA-COLLECTION.md 是本补丁的设计文档，上游修复落地后一并归档；DATA-COLLECTION.md 的 T0 部分可先行删除）；
7. CHANGELOG：历史条目保留，另起一条 "feat(ui): drop the ios-ime bridge — xterm.js vX.Y.Z carries the upstream iOS IME direct-commit fix"；
8. 回归：`go test ./internal/ui/` 全绿 + 真机矩阵中"中文直输数字/标点/英文软键盘"改由上游修复保证（§五）。

---

## 附录 A：评审逐条判定与处置

**判定图例**：✅ 成立 · 🟡 部分成立（机制/事实有误，要求成立）· ❌ 不成立。

### 第一轮 · Mavis（2026-10-16）

| # | 判定 | 依据与处置 |
| --- | --- | --- |
| M1 | 🟡 | "删除数未定义"成立 → §3.3 改为 LCP 差分并显式定义删除数。但其期望 payload `。`（零 `\x7f`）与其自述算法（删除数=baseline.length−LCP=1）及"不清空"设计矛盾（残留已投递时 `\x7f` 是正确语义）→ 上游原样序列钉 `\x7f。`；over-delete 边界入 §六。**（第二轮 Mavis 已自行撤回该期望值：拉了 PR #5836 全量 diff 后确认上游 LCP 实现逐位给出 `\x7f。`，本判定成立）** |
| M2 | ✅ | bundle 实证 `_keyDown` 结构，`evaluateKeyboardEvent` 对 229 永不到达；§一 第 2 环已改写（同一条路径 + 一条结构上不可能命中的路径） |
| M3 | ✅ | §一 第 3 环降级为"假设，待 T0 证实"；T0 增加 setTimeout(0) 同构探针（DATA-COLLECTION.md §一） |
| M4 | ✅ | 上游日志实测空格 keyup=32、标点=0；DATA-COLLECTION §三预期值修正；§3.2"不按 keyCode 过滤"设计不变 |
| M5 | ✅ | §3.3 emissions 双复位点 + `iosImeSending` 重入标志 + 上限；T2 场景⑤钉连续两窗 |
| M6 | ✅ | §3.3 改逐码点分开发送；T2 场景⑭⑮；quick-keys 同款既有行为不入范围 |
| M7 | ✅ | §3.2 blur 改 flush-then-clear；**且必须配 lastSeen 守卫**（xterm 的 blur 处理器先于我们清空 textarea，直接 flush 会计出 shrink → 幽灵 DEL）；T2 场景⑧⑨ |
| M8 | ✅ | §四 T2 钉 20 场景、短语 `new issue #115 ios-ime scenarios`、Unreleased |
| M9 | ✅ | registry 实测 beta=6.1.0-beta.315；§二 明确 stable-only 记档决策 |
| M10 | ✅ | §3.1：`session.iosImeDataDisposable` 挂 session；绑定标志改 term 作用域（`iosImeBridgeTerm`）；T2 场景⑰ |
| m11 | ✅ | API created_at=2026-04-24T01:28:24Z；§二已改 |
| m12 | 🟡 | "218 行测试"不精确（CompositionHelper.test.ts +218、Terminal.test.ts +86、TestUtils +3；PR 汇总 +407/−24）→ §二改为构成列举 |
| m13 | ✅ | `app.go:2943` 实为回放游标发布块；§七锚点链改为 2424/3110/3647→manager.go:528→driver.go:159，措辞精确化（信号转换特判） |
| m14 | ✅ | §七标注"T1 前基线、行号必然漂移"；T3 契约断言全按符号名 |
| m15 | ✅ | 两份文档状态统一为"有条件通过——结论以 T0 数据为准" |
| m16 | ✅ | §3.4 末尾披露上游等长分支 duplicated/over-sent；§五列为既存观察项 |
| m17 | ✅ | §3.2 定义：每个 229 keydown 重臂 failsafe、baseline 取最早、窗内不重取；T2 场景⑦ |

放行条件（M1–M4 修复、M5–M10 于 T1 前处理）：本版全部落地。

### 第一轮 · opencode（2026-10-16）

| # | 判定 | 依据与处置 |
| --- | --- | --- |
| HIGH-1 | ✅ | 同 M5：双复位点 + 重入标志；其"failsafe 与 flush 都要清"的疑问由 §3.3 关闭顺序（先关窗后发送、发送期置重入标志）回答 |
| HIGH-2 | 🟡 | 其描述的"failsafe 先发、input 后落"双发窗因 750ms 间隔现实中几乎不可达（IME 提交以毫秒计）；**真实**双发窗是"xterm 的 keydown timer 晚于桥接投递"（FIFO 下也为罕见）。但"flush 不得与同轮 input 赛跑"的要求成立 → §3.2 改 keyup 后延一个宏任务再 flush（评审问题 1 的取舍：延后 flush，天然排在 xterm timer 之后、由水印裁决，比宽限窗简单可测）；T2 场景⑲ |
| HIGH-3 | 🟡 | 机制成立（timer 对 live vs keydown 快照、缩短即发 DEL），但事实前提有误：bundle 实测**有** 3 处 `textarea.value=` 赋值（`_handleTextAreaBlur` 清空、ETX/CR 清空、Linux 中键选区），"xterm 从不清空"为假。处置（评审问题 2）：**删掉桥接自己的清空**——延后 flush 已使其不必要；并以 lastSeen 守卫使我方永不对外部清空后的值计 delta，从根上杜绝幽灵 DEL；T2 场景⑨同时模拟 xterm timer 回调 |
| MEDIUM-1 | ✅ | §3.2：空 delta 的 flush 不关窗、failsafe 第二次机会；§六补风险行并绑 750ms 取值理由（T0 唯一待校准数） |
| MEDIUM-2 | ✅ | 同 M8 |
| MEDIUM-3 | ✅ | §四 T0/T2 验收附加：切片块无新自由标识符、`location` 守卫、锚点边界包裹 |
| MEDIUM-4 | 🟡 | 现象属实（水印在网关前、被拒收数据混入），但触发面仅限已消亡实例的状态门（`starting` 放行）→ 判定**已知接受**，§六记录理由；查询应答属分支 D 已预见 |
| LOW-1 | ✅ | CHANGELOG 实测该条目在 `## v0.3.0`（:194）；§二已改 |
| LOW-2 | ✅ | 同 m13；driver.go:159-190 信号转换措辞已精确化 |
| LOW-3 | ✅ | §六拆为两个独立决定（门控机制 vs 是否保留） |
| LOW-4 | ✅ | §3.1 显式说明水印 disposable 挂 session、随 term dispose 回收 |
| LOW-5 | 🟡 | focusTerminalIfPossible 多重早退、仅活动会话属实 → §3.1 软化为"不声称已压到最小"，一般路径仍是 ≤2s 轮询 |
| LOW-6 | ✅ | T4 明确 Unreleased |
| 问题 3 | — | §三题记 + §一第 4 点结论：复刻上游**算法层**（baseline/keyup diff/composition 取消/最早 baseline），不复刻其实现层（xterm 内部 `_dataAlreadySent`、内部 timer 槽位）；比上游多的两层（水印去重、延后 flush）与上游未处理的竞态均在 §3.2/§3.3 写明 |
| 问题 4 | — | §三边界纪律：`iosIme` 在 pty.js 与 legacy factory 零匹配，T3 按符号名断言 |

**两份评审共同确认无误的部分**（§一根因链除第 3 环降级外、§二路线决策、§3.4 四个分支前提、DATA-COLLECTION 两个校准问题、真机矩阵完整性）按原样保留，不重复论证。

---

## 附录 A-2：第二轮评审逐条判定与处置（2026-10-16）

两份第二轮评审（opencode N1–N9、Mavis F1–F7）共 16 条，
**全部属实**（其中含两位评审各自认错/撤回的旧条目——opencode 认了 HIGH-3 事实前提错误与
229 归因错误，Mavis 撤回 M1 的 `。` 期望值并确认 `\x7f。` 与上游 LCP 实现逐位一致，
另主动撤回一条"多字符缩短发少了"的拟提意见）。所有事实性论断均已独立复核：指纹计数
（`_dataAlreadySent` 4、四个 PR 新增字段 0）、`Terminal.test.ts +84/−2`、grep `--include`
glob 不匹配 `.mjs`——逐条实测属实。

### opencode 第二轮（r2）

| # | 判定 | 依据与处置 |
| --- | --- | --- |
| N1 | ✅ | 指纹计数实测复核属实（4/0/0/0/0）。§8.3 改为 bundle 指纹守卫（`_pending229Baseline`），版本号不再承担移除触发；T3 同步改名 `TestIosImeBridgeMatchesVendoredBundle` |
| N2 | ✅ | §3.2 关窗规则定死：keyup 空 delta 有界不关窗（`failsafeArmed` 只许一次）+ failsafe 无条件关窗；§六补 pending 泄漏故障链记录。其推演链末步（"下一个键永不发送"）有小偏差——IME 插入会触发 `input` 刷新 `iosImeSeen`，实际结局是 spurious DEL 而非静默——但泄漏缺陷与修法成立 |
| N3 | ✅ | §8.1 pattern 扩为 `iosIme\|ios_ime\|IosIme\|ios-ime`；§8.4 新增"复跑 grep 零命中"收尾断言 |
| N4 | ✅ | §8.1 横幅改 `//` 行注释（HTML 注释 Annex B 陷阱证实）；T3/§8.3 守卫断言同步改 `// ==== ios-ime bridge` |
| N5 | ✅ | §3.2 新增"与上游三处有意分歧"表（timer 重臂 / timer 时长 / 关窗条件）+ 复刻边界声明 |
| N6 | ✅ | §二 改为 `Terminal.test.ts +84/−2`（API 实测），构成全量列举 |
| N7 | ✅ | DATA-COLLECTION §二 采集步骤加"先见 `[ios-ime] bound` 再记录"前置；§一 字段解读加"无 probe 行 = 绑定前敲击 = 数据无效" |
| N8 | ✅ | §六 改写"keyup 早于 input"条目：与 F5 合并为假设次序说明 + 互补两支 |
| N9 | ✅ | §8.2 补契约性告警：注册顺序是当前实现事实、非上游契约；指纹守卫为真正的兜底，两者不互相替代 |

### Mavis 第二轮

| # | 判定 | 依据与处置 |
| --- | --- | --- |
| F1 | ✅ | 实测两个守卫的句形要求互斥属实。T4 改为 CHANGELOG 必须写 `twenty new issue #115 ios-ime scenarios`（数字词与短语间仅空格、无反引号），禁用 "Pinned by N `node --test` cases" 句形 |
| F2 | ✅ | 同 N1，§8.3 改指纹判定（比 allowlist 更强：不依赖人维护版本清单） |
| F3 | ✅ | 同 N3，include 集合补 `--include='*.mjs'`（glob 语义实测证实） |
| F4 | ✅ | T2 明写"恰好 20 个 `^test(`"为硬性验收；三条追加断言（N2 死键关窗链、F6 交错只交付一次、死会话 flush 静默）合并进既有场景 ⑥⑧⑱ 的第二阶段，不开新 `^test(` |
| F5 | ✅ | 同 N8，§六 按实测事件序（#5835 时间戳：input 先于 keyup）改写机制描述 |
| F6 | ✅ | §3.2 明写 flush 首行守卫 `if (!session.iosImePending) return;` 排 term 守卫之前；T2 场景⑧ 含交错断言 |
| F7 | ✅ | §六 补 lastSeen 守卫失效方向记录（最坏漏发、非多发 DEL；安全性以 N2 有界关窗为前提） |

### 第二轮同时确认通过、可作为实施前提的论断

§3.3 LCP 差分与上游逐位一致（含上游 `applyPending229Change` 三组测试对照）、缩短分支"同 xterm 语义"
标注准确、延后 flush 的 FIFO 论证（上游 0ms timer 先于桥接 timer 入队）、§8.2 自中和四条路径推演、
§3.2/§3.3 与上游差异已写进正文而非只在附录、`textarea.value=` 3 处、DATA-COLLECTION 同构探针
判据充分、"不触碰"可检查化——均属实保留。

**放行条件**：两轮共 43 条已全部关闭；本版（v3）满足 r2 的 N1–N6 与 Mavis 的 F1–F2/F4 动工前条件，
N7–N9 与 F3/F5–F7 已并入 T0/T1 任务。T0 可立即开工。

---

## 附录 A-3：第三轮评审逐条判定与处置（2026-10-16）

第三轮两份评审（Mavis G1–G7、opencode R3-A/B/C）共 10 条，
**全部属实**。两者独立复核对方结论（opencode 复核 Mavis G1–G7 全部成立、Mavis 复核 opencode
的指纹计数逐符号吻合），无分歧。本版（v4）全部落地。事实性论断独立复核：指纹计数（`_dataAlreadySent` 4、
四个 PR 新增字段 0）、`grep --include='*.js'` 不匹配 `.mjs`、§8.1 grep 当前树零命中、
`static/kinds/pty.js` 的 CWD 相对读取约定、vendored bundle 为 git 追踪资产、PR 六文件
实现 +102/−22 与测试 +305/−2 相加 = +407/−24——逐条实测。

| # | 判定 | 依据与处置 |
| --- | --- | --- |
| G1 | ✅ | §3.2 三支补齐（新增"空 delta 且 `iosImeFailsafeArmed` 已置位 → 无条件关窗"else 支）；§3.3 增**完整 flush 真值表**（评审 r3 §四 原样采纳，并入字段更名）；关窗时复位 armed 标志；T2 场景⑦ 增两条断言 |
| G2 | ✅ | §3.1 新增"T0 诊断与 T1 桥接共用同一条绑定路径"段（唯一绑定函数 + 唯一幂等标志 `iosImeBridgeTerm` + flag 关闭时诊断监听不存在）；T0 任务范围含"自带绑定并发 `bound`"；DATA-COLLECTION §一 增"落地任务"列 |
| G3 | ✅ | §二 合计数更正为"实现 +102/−22、测试 +305/−2"（原写 +218 是误抄测试文件数值，218+305=523≠407 自相矛盾） |
| G4 | ✅ | §3.2 表题改"五处有意分歧"并补 lastSeen 守卫、逐码点分块两行——表的用途是给 T5 执行者看，不外包"算法层 vs 实现层"的判断 |
| G5 | ✅ | §8.1 搜索路径去掉 `docs/plans/fix-issue115`（include 不含 `.md`，该路径为死路径）；§8.4 第 5 步同步注明 |
| G6 | ✅ | 横幅纯 ASCII 化（`BEGIN - removable block` / `END - removable block`），em dash 仅留文档正文——切片 harness 的 `sliceBlock()` 是 `indexOf` 逐字符相等 |
| G7 | ✅ | §五 增"死键路径"手测项（连按两次 229 而 IME 未提交，随后正常字符送达、无重复、无残留窗口影响） |
| R3-A | ✅ | §一 第 3 环补入实测时间戳支撑（keydown→插入 10–18ms vs 0ms 回调 0–4ms，一个数量级余量；插入早于 keyup 2–6ms）并标明"间接证据、T0 探针为准"；§六 flush-early 行注明该风险未在实测复现；DATA-COLLECTION §三 增实测基线与校准方向（750ms 为 22–62× 保守值，200–300ms 的取舍留 T0/UX 决定） |
| R3-B | ✅ | `failsafeArmed` 更名 `iosImeFailsafeArmed`（§3.2/§3.3/§8.1 全部同步；违反 §8.1 前缀不变式会脱离 grep pattern） |
| R3-C | ✅ | §8.3 与 T3 的守卫读路径改为 CWD 相对 `static/vendor/xterm/xterm.js`（Go 测试 CWD 是 `internal/ui/`）；补充 vendored bundle 是 git 追踪资产的确认 |

**第三轮同时确认通过的论断**（可作实施前提）：§8.3 指纹计数与失败方向安全性、N4 Annex B 横幅结论、
F4 场景计数闭合、F1 双守卫句形互斥、N2 有界化切断故障链（含对 r2 推演末步的更正复核成立）、
F5/N8 假设次序改写、F6 首行守卫、F7 失效方向、N9 注册顺序告警、§二 升级路线 stable-only 决策——
全部属实保留。

**r3 §三 五种时序推演**（用 #5835 实测时间戳逐事件代入）经复核全部正确，已把其中两个非显然结论
写入 §3.3"分支 B / C 的真实职责"（分支 B 只在转换场景 timerA 抽于 delete 与 insert 之间触发；
分支 C 第二条件只在同一转换 timerA 抽于 insert 之后触发）。

**放行条件**：三轮共 53 条全部关闭；本版满足第三轮 T1 动工前条件（G1/G2）与同批项（G3/G4/G5/R3-B/R3-C）、
T0 收尾项（G6/G7/R3-A）。**T0 与 T1 均可开工**，T0 数据回来后按 §三 校准 failsafe 取值。

---

## 附录 A-4：实现轮仲裁判定与处置（2026-10-16，v5）

T0/T1/T2/T3+T4 各轮实现产出经双评审（第一轮：opencode/space-bunny-free + opencode/glm-5.3-flash-free；
其后者因限额改配 opencode-go/glm-5.3-flash + opencode-go/deepseek-v4.1-flash）与仲裁者逐条判定。
全部事实性论断经仲裁者独立复核（bundle 指纹/守卫引文逐字比对、评审 harness 场景复跑、变异复现）。

| 轮 | 判定 | 依据与处置 |
| --- | --- | --- |
| T0-r1 A | MAJOR keyup 无时间戳 → §三 的 keydown→keyup 校准不可算 | ✅ 成立（§三 实测基线与 §四 T0 产出明确要求；Safari 控制台时间戳非契约）→ keydown/keyup 行加 `t`，§一/§三 文档同步 |
| T0-r1 A/B | `dt` 引用上次敲击探针 | ✅ 成立 → keydown 首句重置 `iosImeProbeT1`，null 即"插入早于探针"证伪分支 |
| T0-r1 A | 探针注释 FIFO 前提过宽 | ✅ 成立（bundle 实证 0ms diff 仅 229 且非合成中）→ 注释写明前置条件，日志不收窄 |
| T0-r1 A | 无 unbind / sliceBlock 地雷 / console 抛错 | 前两者 ❌ 不改码（quick-keys 同款先例；属 T2 harness 须知）；console 抛错 ✅ iosImeLog fail-soft |
| T0-r1 A challenge | signpost 无归属任务 | ✅ 成立 → 归 T3/T4 批次（§四 T3 行已记） |
| T1-r1 B | **BLOCKER 合成中 229 建窗走分支 A 泄漏拼音** | ✅ 成立（bundle：`compositionupdate` 仅绘制、`_finalizeComposition` 唯一投递点、composing 中 229 到达实证）→ §3.2 composing 守卫 + 常驻 compositionend + compositionstart 终结窗口（含 armed 复位）；§3.4 前提更正 |
| T1-r1 A/B | `rearmed` 恒真；`deliver.armed` 语义与日志顺序 | ✅ 成立 → `rearmed=keptEarliest`；deliver 先于 clear(close-window) 落日志；armed=进入 flush 时值 |
| T1-r1 B challenge | 分支 C 首子句过宽（`ESC[I` ⊃ `I`） | ✅ 成立但不改码（上游真实投递形态均为精确相等/前缀，正例未观测；真机复现再按边界收紧）→ §3.3 已知局限 + §五 观察项 |
| T1-r2 B | blur 不复位 armed；composing 锁死（丢 compositionend） | ✅ 成立（harness 实证）→ blur clear 复位 armed + 无条件清 composing（quick-keys blur 先例）；仲裁加第三项：**绑定整体重置窗口状态**（陈旧 baseline 对新 term 是幽灵 DEL 面） |
| T2-r1 B | ⑬ 重入仅测旗标未测回声；composing 旗标支路/blur 自愈/日志顺序/armed 字段未钉；③ armed 断言空转；F6 断言不可判别；dispose 无计数 | ✅ 全部成立（评审逐条变异实证：删实现行套件仍 20/20 绿）→ 全部折叠进既有场景第二阶段（附录见 §四 T2 行补钉清单），8 项变异现逐一命中 |
| T3/T4-r1 | A: MINOR 移除分支断言 CHANGELOG 短语消失与 §8.4 步骤 7 "历史条目保留"自相矛盾（超 §8.3 清单的自加断言）→ ✅ 改为 terminal_status_test.go 内 guard 表行消失；NIT 移除分支缺绑定点清除断言 → ✅ 已补（bindingPoint==0）；B: DATA-COLLECTION 选字栏 bullet 陈旧 + §3.2 重置次序措辞 → ✅ 均修 |

**流程记录**：评审 Sub-agent 两次沙箱权限异常（/tmp 写入被拒、`cd ../..` 越出仓库根）——以"内联 node -e/heredoc、禁止越根 cd、go test 用 `go test ./internal/ui/`"约束重跑解决；评审模型一轮超时——重跑解决。

---

## 附录 A-5：PR #123 第一轮评审仲裁判定与处置（2026-10-16，v6）

两位评审（Muse Spark：Request Changes + 政策更正评论；Mavis：可合并 + 一条可执行意见）共 1 条政策调整、5 条 MAJOR 级、13 条 MINOR 级、8 条 NIT。逐条仲裁如下；事实性论断经仲裁者独立复验（M1 以评审复现脚本原样重放证实；纯收缩单 DEL 以 bundle 分支逐字引文证实；数字配对勘误复算证实）。

**政策调整（评审更正评论，维护者决定）**：T5 真机验证移至合并后 beta 执行，不作为本 PR 合并门槛——撤回其 B1"拦合并"；四处文档（本文档 §首/§四 T5/§五、DATA-COLLECTION、CHANGELOG 尾段）与 PR body 同步改写，follow-up issue **#124** 跟踪（https://github.com/linletian/myworktree/issues/124；PR body 已链接）——二轮 R2-MINOR-6 的可验性补账。

| # | 判定 | 依据与处置 |
| --- | --- | --- |
| Mavis-1 | ✅ 成立 | `live===seen ? live : seen` 恒取 seen（相等时二者同串），三元为死代码且注释会诱导反向"修正"重新引入幽灵 DEL → flush 的 current 坍缩为 `const current = session.iosImeSeen;` + 注释明写语义（§3.2 已改） |
| M1 | ✅ 成立（复放证实） | 外部改写（xterm 程序化 `textarea.value=`，不入 input 事件）+ 同窗后续 input → seen 被无条件覆盖、keep-earliest 保留失真 baseline → 幽灵 DEL 且新字符丢失（replay：sent=`["\x7f"]`）→ keydown-229 增**外部改写检测**：`live !== seen` 即终结陈旧窗再建新窗（§3.2 已改；折叠进 T2 ⑨ 第二阶段） |
| M2 | ✅ 成立 | CHANGELOG 把第 3 环假设写成定论（plan §一/§一 DATA 均标"待 T0 证实"）→ 加 presumed/pending-T0 hedge |
| M3a | ✅ 成立 | 指纹守卫缺对照指纹——私有名 mangle 场景 `_pending229Baseline` 恒 0 → 补丁判"必须在位"恒绿假阴 → 加 `_dataAlreadySent >= 1` 对照断言（0 即 Fatal"指纹不可信"） |
| M3b | ✅ 成立 | `createTerminalSession` 体按 `"\n        }"` 8 空格截断，重缩进即早截/延截 → 截断后断言切片含 `termDataDisposable` 锚点（证明到达 onData 区域）+ `end >= 0` |
| M4 | ✅ 成立 | 测试魔数 750 镜像生产 const，T0 校准改值即全红且报错误导 → harness 切片求值 `iosImeFailsafeMs` |
| M5 | ✅ 成立（按折叠处理） | HIGH-2 FIFO 未被钉 → ⑲ 第二阶段注入模拟 xterm timer 并断言先于 keyup-defer flush |
| MINOR LCP 码元劈代理对 | ✅ 成立 | `a😀`→`a😃` 码元 LCP 劈开代理对 → 孤立低代理进 PTY、DEL 数按码元误计 → §3.3 LCP 粒度官定码点（ASCII/BMP 逐位等价）；折叠进 ⑭ |
| MINOR 纯收缩单 DEL 证据链 | ❌ REJECT（parity 实证） | bundle 缩短分支 `t.length<e.length?triggerDataEvent(DEL)` 对任意缩短只发一个 DEL——单 DEL 即上游语义，多字符净删少删是上游同款行为；§3.3 已附引文 |
| MINOR destroy 双路径不清 timer/水印 | ❌ REJECT（成本超收益） | 清理代码只能落在工厂内，而工厂触碰为 §8.1 零外溢契约禁止——其代价（破契约）高于收益（fail-soft 守卫 F6 + input 抛错捕获已兜底，且 term falsy 时 failsafe 不关窗的窗口由重绑重置收敛）；§3.2 timer/销毁边界已记 |
| MINOR 旧 keyup-defer 跨 rebind 误 flush 新窗 | ❌ REJECT（二轮复核：极窄 + 兜底） | defer 是 keyup 后**首个排队回调**——本评审证据下（真实事件 FIFO 序）新窗不可能先于它建立；即便发生，rebind 重置 pending 兜底空窗、新窗内 delta 多以空/arm 收敛；T5 快速连输项顺带观察 |
| MINOR 水印在 app 门之前（查询应答双丢） | ❌ REJECT（结构性不可改 + 已记） | 门在工厂自身 onData 消费者内部，零外溢契约禁止触碰；§3.3 已知局限 + §五观察项已覆盖（MEDIUM-4 已知接受） |
| MINOR emissions cap 截断后分支 B 复发 | ✅ 成立（注释级） | cap 注释已记 C 侧退化，补 B 侧重发风险一句 |
| MINOR debug-OFF 监听数未钉 | ✅ 成立 | 实测 ① 无监听数断言 → 钉 6 |
| MINOR caps 零覆盖 | ✅ 成立 | 折叠进 ⑬ 第二阶段（64/4096 停止追加不抛错） |
| MINOR pty 零溢出仅查 `iosIme` 子串 | ✅ 成立 | Go 守卫改查四模式全零（iosIme/IosIme/ios_ime/ios-ime；今日实测全零） |
| MINOR bindingPoint==2 过刚 | ❌ REJECT | §8.1 触点全集契约就是恰两处，守卫钉契约 |
| MINOR 发送口守卫弱 | ❌ REJECT | `term.input(` ≥1 + `.triggerDataEvent(` ==0 已是合理绊网 |
| MINOR signpost 无守卫 | ✅ 成立 | 指纹守卫 present 分支加 signpost 在位断言、removed 分支加消失断言（CWD 相对 `../../scripts/download-xterm.sh`） |
| MINOR harness 默认不回声 | ❌ REJECT | 按场景控制回声是设计（⑬ 专测回声），全局默认回声会污染分支断言 |
| MINOR failsafe 倍数配对错位 | ✅ 成立 | 750/10–18=41–75×，22–62× 对应 keydown→keyup 12–34ms → §3.2 分歧表句已改 |
| NIT ×8 | — 逐条不拦（二轮 R2-MINOR-8 要求各自可判，逐条补判如下） | ① B/C 标签二义：❌ 不改（两条路径行为同为静默，仅 deliver.branch 字面 differ，无转写歧义）；② 顶层 function 实为 window 属性：❌ 不改（§8.1 所指是"无显式 `window.*=` 赋值"，注释措辞已足）；③ `iosImeProbeT1=null` 移入 debug 门：❌ 不改（生产每键一次 null 写，仅写已存在字段，churn 不值）；④ ≤2s 残窗：❌ 已记 §六 + §五（quick-keys 同款先例）；⑤ `twenty` 数字词空格脆弱：❌ 守卫已钉（F1 双锚）；⑥ `globalThis.location` 串行：❌ node --test 同文件按序执行，pytest 式并行不存在；⑦ 长度门未单独钉：✅ 已并入 ⑬ caps 阶段；⑧ checklist 硬编码 `§8.4` 路径：❌ 该目录随补丁共存亡（§8.4 步骤 6 一并归档），守卫存在期内路径稳定 |

**合并前 checklist（本 PR 内）**：修 Mavis-1/M1/M2/M3a/M3b/M4/M5 + MINOR 五项（LCP/注释/监听数/caps/pty 四模式/signpost）+ 文档政策改写。T5 改合并后执行。


---

## 附录 A-6：PR #123 第二轮评审仲裁判定与处置（2026-10-16，v7）

两位评审（Muse Spark：不拦合并，1 MAJOR + 1 P1 + 8 MINOR/NIT；Mavis：无新功能缺陷，1 MINOR + 1 NIT）共 13 条。关键事实经由仲裁者独立复放/复核。

| # | 判定 | 依据与处置 |
| --- | --- | --- |
| R2-M1 | ✅ 成立（复放证实） | 外部改写终结是 terminate-and-drop：`^C` 后未投递增量在 keyup-defer 未运行时被静默丢弃（replay：sent=`[]`，`d` 字恒失）。裁定 **flush-then-terminate**：终结前先 `flushIosIme(session, "external-rewrite")`（current=seen 此刻仍可信，属末次投递机会；物理键盘语义——中断前敲入的字符应送达，与 M7 blur 先例同构），再终结残余；via 枚举增补第四值（§3.2/§3.3 已改）。**注**：常规事件序下 keyup-defer 先于后续真实 keydown 运行，损失窗口主要在 iOS 长按连发 229 无 keyup 的流中（M17 场景） |
| R2-P1 | ✅ 成立 | §3.3 真值表伪代码残留三元与 §3.2"恒取 seen"矛盾（单看真值表复制即复活 Mavis-1 陷阱）→ 真值表坍缩 + "勿回退"注记（本文档已改） |
| Mavis-MINOR 终结者无 clear 日志 | ✅ 成立 | 三个同族终结者（close/compositionstart/blur）都留 `clear` 行，外部改写终结静默 → T5 转写者无法区分"窗被终结"与"日志截断"；且该路径真机可预期（两次直提交之间按一次回车即触发）→ 补 `clear{reason:"external-rewrite"}`（仅当 flush 后 pending 仍非空——与 blur 同构防双记）+ DATA §一 reason 枚举补值 |
| Mavis-NIT 死 helper | ✅ 成立 | `setFlag`/`fireOne` 定义并导出但零调用（预留管线被 `setValueRaw`/直接赋值替代）→ 删除 |
| R2-MINOR-1 迟到 input 误判外部改写 | ✅ 记录不改码 | `live!==seen` 无法区分"外部改写"与"前一 tap 的 input 事件晚于下一 keydown"（正常 FIFO 下 input 先到，UA 异序才触发）；误判后果=旧窗被 arm-处理后终结 + baseline 前移，末字可能随新 baseline 消失——概率极低且无低成本判别机制 → T5 长按连击日志观察项（开 debug 看 `pending/clear/deliver` 序列） |
| R2-MINOR-2 astral DEL 缺终端侧依据 | ✅ 记录 + T5 项 | 码点 DEL 与 shell 每字符擦除对齐（readline 多字节）——§3.3 已补依据句；T5 增 `cat -v` astral 替换观察项 |
| R2-MINOR-3 分支 B 码元切片混粒度 | ✅ 成立（注释级） | emissions 整串入队/cap 整元拒收 ⇒ `delta.slice(joined.length)` 边界对齐安全——不变式补注 §3.3 + 码内一句 |
| R2-MINOR-4 纯收缩码内无 bundle 锚点 | ✅ 成立（注释级） | 引文搬一句进码注，防后人按 removed 计数"修复" |
| R2-MINOR-5 (P2-1) DATA §三 缺 41–75× | ✅ 成立 | 750/10–18=41–75×（插入配对）与 22–62×（keyup 配对）双注已进 plan §3.2 分歧表；DATA §三 补同款双注 |
| R2-MINOR-6 (P2-2) follow-up 无编号 | ✅ 成立 | issue **#124** 已建并写入 A-5/A-6 + PR body |
| R2-MINOR-7 REJECT 措辞过强 | ✅ 成立（措辞级） | A-4 两行已改"极窄+兜底"/"成本超收益"——结论不变，论证边界收敛到证据范围内 |
| R2-MINOR-8 NIT 合行不可独立复判 | ✅ 成立（透明度） | A-4 NIT 行扩为逐条判定（①–⑧，二改一：长度门已并入 ⑬） |
| （T5 政策） | — 已按一轮更正执行 | 维持"合并后 beta、非合并门槛"；本轮 Mavis 复核通过不再提 |

**合前 checklist（本 PR 内）**：R2-M1 flush-then-terminate + "external-rewrite" via/clear 行/文档枚举、R2-P1 真值表坍缩、Mavis 两项（clear 行 + 死 helper）、注释级三项（B 边界不变式、码内引文、DATA §三 双注）。T5 观察项两项（迟到 input 误判、astral 替换 `cat -v`）随 #124 执行。

---

*计划维护：fix/fix-issue115 ｜ v7 2026-10-16（PR 二轮评审仲裁：外部改写终结改 flush-then-terminate、真值表坍缩、终结者日志补行）｜ T5 改合并后执行（#124）*
