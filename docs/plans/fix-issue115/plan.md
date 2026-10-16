# Issue #115 修复计划：iPadOS/iOS 中文输入法直接键入无法输入终端

**日期**：2026-10-16（v4：按第三轮两份评审意见修订）
**状态**：**放行 T0 与 T1——结论以 T0 数据为准**（T0 校准 failsafe 取值；第三轮 G1/G2 已在 v4 补齐）
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
| `keydown` 且 `keyCode===229` | 无 pending → 记 `baseline = textarea.value`；已有 pending → **保留最早 baseline，不重取**；**每个 229 keydown 都重臂** failsafe timer（750ms，iOS 长按连续发 229，重置语义见评审 M17）；更新 `iosImeSeen` |
| `input` | `iosImeSeen = textarea.value`（每次改动后刷新最后观测值） |
| `compositionstart` | 清 pending + 清 timer（composition 路径自有投递，不干涉） |
| `blur` | **flush-then-clear**：先按 §3.3 flush（评审 M7：只清不 flush 会丢字），再清 pending + timer |
| `keyup` | **不同步 flush**——排一个 `setTimeout(0)` 宏任务再 flush（评审 HIGH-2：不得与同轮尾部 `input` 赛跑；且 xterm 的 keydown timer 先于我们的 timer 入队，FIFO 保证它先跑完，水印得以裁决） |

**lastSeen 守卫**（评审 HIGH-3/M7 引出）：bundle 实证 xterm 自身有 3 处 `textarea.value=` 赋值——`_handleTextAreaBlur` 清空、`^C`/CR 的 `ETX` 分支清空、Linux 中键选区驻留。这些外部清空会晚于我们的观测发生，flush 时若直接读 live value，会算出 shrink → 发出**幽灵 `\x7f`**。因此 flush 的 current 取值规则：**live value === `iosImeSeen` 时用 live；不一致时（外部清空/改写）用 `iosImeSeen`**——桥接永不对外部改写后的值计 delta。

**failsafe timer**（750ms）：漏 keyup 的兜底。关窗规则（评审 N2，实现前必须定死；三支齐全由评审 G1 补齐）：

- **keyup 延后 flush 产出空 delta** 且 `iosImeFailsafeArmed` 为 false（第一次）：**不关窗**，置 `iosImeFailsafeArmed = true`，把"第二次机会"交给 failsafe；emissions 不动；
- **keyup 延后 flush 产出空 delta** 且 `iosImeFailsafeArmed` 已为 true（第二次，评审 G1 补的 else 支——同窗口内连按两次 229、IME 两次都没提交即可达）：**无条件关窗**（第二次机会已用过，不能再延期；否则 N2 的 pending 泄漏原样复现）；
- **failsafe 触发的 flush 无条件关窗**（无论 delta 是否为空）——它是最后一次机会，不是第三次机会；
- 非空 delta 的 flush：交付后关窗并清 timer。

关窗时 `iosImeFailsafeArmed` 一并复位（新窗口重新允许一次留窗机会）。

不关窗的理由（评审 MEDIUM-1）：flush 早于插入时若立即关窗，字符永失（成为下一次 baseline 后无人再补发）；有界地保留一次机会兼顾两者。无界保留的故障链（N2 实测推演）：死键 tap（229 无插入）→ 空 delta 不关窗 → failsafe 仍空 → 再不关 → pending 泄漏，后续按键沿用陈旧 baseline，叠加 lastSeen 守卫算出空/错 delta——与 issue #115 同类的静默吞字。有界化后该链断裂。

**flush 首行守卫**（评审 F6，明写为契约）：`if (!session.iosImePending) return;` 必须排在 term 守卫（`!session.term`）**之前**——keyup 延后 flush 与 blur flush 可交错（keyup 排下 D、blur 先跑并关窗交付、D 后到），第二次进入必须因 pending 已空而空转，正确性不能依赖未写下的约定。

**与上游 PR #5836 的五处有意分歧**（评审 N5/G4：披露，避免 T5 真机验证时把差异误判为回归；表供 T5 执行者一次看全，不要求其判断"算算法层还是实现层"）：

| # | 上游 PR #5836 | 本计划 | 理由 |
| --- | --- | --- | --- |
| timer 重臂 | `_ensurePending229Timer()` 首行 `if (this._textareaChangeTimer !== undefined) return;`——首个 229 装 timer、后续忽略 | 每个 229 keydown 重臂 | 改进：iOS 长按连续发 229，重臂使 failsafe 从最后一次 keydown 起算，避免长按中段误触发 |
| timer 时长 | `setTimeout(…, 0)`（单次宏任务） | 750ms failsafe | 改进：上游窗口只有一次宏任务，漏 keyup 即无兜底；750ms 覆盖迟到的 input（实测 keydown→插入 10–18ms，余量 22–62×，见 R3-A） |
| 关窗条件 | `if (dataSent || (timerFired && keyupFired)) _clearPending229()` | 空 delta 有界保留一次机会（见上三支） | 上游"两次空即关窗"在某些次序下等于立即关窗；我们有界不关窗补漏，但有界化杜绝其副作用 |
| current 取值 | 读 live `textarea.value` | lastSeen 守卫（读 `iosImeSeen`，§3.2） | 结构性需要：我们在 xterm 外部，读不到它内部的 diff 快照；外部清空（blur/^C）会污染 live |
| 发送粒度 | 单串 `triggerDataEvent(payload)` | 逐码点 `term.input()`（§3.3，评审 M6） | 结构性需要：`isTerminalQueryResponse` 按整串匹配（`^rgb:` 等），整块投递会被吞；逐码点与 xterm 原生逐键语义对齐 |

（"算法层复刻"仅指 LCP 差分与 baseline/补发/composition 取消的语义；上表五处为实现层的有意分歧，各有理由。）

**timer 与 session 销毁的边界**：failsafe timer 不随销毁路径清除（销毁路径在工厂内、不碰），但 flush 首行两道守卫（`!iosImePending` / `!session.term`，term 已 dispose 时 `term.input` 抛错亦被捕获）即静默返回——补丁不留悬挂行为，也不需要改任何销毁代码。

### 3.3 flush 算法（两个纯函数 + 复位语义）

**`iosImeDeltaBytes(baseline, current)`**（评审 M1：删除数必须显式定义——最长公共前缀 LCP 差分）：

```text
if current === baseline → ""
p = LCP(baseline, current) 长度
removed = baseline.length - p
appended = current.slice(p)
current.length <  baseline.length → "\x7f"                       （纯缩短：单 DEL，同 xterm 语义）
removed > 0                     → "\x7f" × removed + appended     （替换：DEL×删除数 + 插入）
否则                            → appended                        （纯追加）
```

以上游 #5835 实测序列钉死：baseline `" ，！ "` → current `" ，！。"` → LCP=3、removed=1、appended=`"。"` → payload **`"\x7f。"`**（残留已投递时语义正确：删掉那个空格再插入。）。**未投递残留 + 转换**的 over-delete 是已知边界（上游同款，§六）。

**`iosImeResidualPayload(delta, emissions)`**（emissions = 本 pending 窗口内 xterm 自身经 onData 投递的串数组）：

| 分支 | 条件 | 行为 |
| --- | --- | --- |
| A | emissions 为空（xterm 没投递 = iOS 失败模式） | 返回 delta |
| B | delta 以 emissions 拼接串为前缀（多敲击部分投递） | 返回剩余部分 |
| C | emissions 拼接串包含 delta、或包含 current（xterm 自身路径已处理：桌面 sogou / composition 结束投递） | 返回空（静默） |
| D | 其余（如窗口内无关的焦点回报 `ESC[I`） | 返回 delta |

**emissions 复位语义**（评审 HIGH-1/M5）：① 建 baseline 时重置为 `[]`；② **关窗的 flush 完成后清空**（不关窗的保留——emissions 与 pending 同作用域，跨那一次 failsafe 机会持续累积）；③ **`iosImeSending` 重入标志**：水印监听在 `iosImeSending` 为真时不记录——桥接自己的 `term.input()` 会经 `triggerDataEvent` → `onData`（bundle 实证：`input(e,t=!0){this.coreService.triggerDataEvent(e,t)}`），不排除会自我污染；④ 条数/长度上限（防呆）。

**分块发送**（评审 M6）：payload 按**码点**逐个 `term.input()`，不整块发送。原因：`isTerminalQueryResponse`（`index.html:6042`）对整串做 `/^rgb:/i`、`/^;?\d+(;\d+)?R$/i` 等匹配，xterm 原生逐键投递永远单字符不命中，而桥接的一次性 delta（多敲击累积、转换替换）可能整串命中被吞——例如整串 `rgb:ff0000`。逐码点后每串仍是单字符，语义与 xterm 一致、且 `\x7f` 与插入段分路投递不影响终端。（注：quick-keys 粘贴同样经过该过滤器，属既有行为，本 issue 不扩大范围。）

**关闭顺序**（重入安全）：凡关窗的 flush（按 §3.2 关窗规则判定），先关窗（`pending=null`、清 timer、`emissions=[]`、`iosImeFailsafeArmed=false`）再发送；不关窗的 flush（keyup 空 delta 的第一次）只置 `iosImeFailsafeArmed=true`，不动 emissions；发送期间 `iosImeSending=true`；`term.input` 抛错（term 已销毁）时丢弃其余字节并复位标志（同 `flushQuickKeysQueue` 先例）。

**flush 全流程真值表**（评审 G1：把 §3.2 关窗规则 + 本节全部语义一次写全，实现者不需猜。`via ∈ {"keyup-defer","failsafe","blur"}`）：

```js
flushIosIme(session, via):
    if (!session.iosImePending) return;           // F6：必须第一行，早于 term 守卫
    if (!session.term) return;                    // §3.2 死会话（timer 存活于销毁路径）
    current = (textarea.value === session.iosImeSeen)   // lastSeen 守卫（§3.2）
            ? textarea.value : session.iosImeSeen;
    delta = iosImeDeltaBytes(session.iosImePending.baseline, current);

    if (delta === "") {
        if (via === "failsafe") { closeWindow(); return; }      // 无条件关窗（N2）
        if (session.iosImeFailsafeArmed) { closeWindow(); return; }  // else 支（G1）
        session.iosImeFailsafeArmed = true;                     // 只留这一次机会
        return;                                                 // 不动 emissions
    }

    payload = iosImeResidualPayload(delta, session.iosImeEmissions);
    closeWindow();                                              // 先关窗再发送
    if (!payload) return;                                      // 分支 C 静默
    session.iosImeSending = true;                              // 重入标志：排除自身投递
    try { for (const cp of codePointsOf(payload)) session.term.input(cp); }  // 逐码点（M6）
    catch (e) { /* term 已销毁：丢弃其余字节 */ }
    finally { session.iosImeSending = false; }
```

**分支 B / C 的真实职责**（评审 r3 §三：用 #5835 实测时间戳把五种时序逐事件代入后确认，非理论摆设——T5 逐条对照用）：

- **分支 B（delta 以 emissions 为前缀）** 只在**空格→。 转换且 xterm 的 0ms timer 恰好抽在 delete 与 insert 之间**时触发：xterm 先投 `DEL`（emissions=`["\x7f"]`），桥接 delta=`\x7f。` 以其为前缀 → 只补 `"。"`，净效果正确（xterm 的 DEL + 桥接的。= 各一次）；
- **分支 C 的第二条件（emissions 包含 current）** 只在**同一转换但 timer 抽在 insert 之后**时触发：xterm 走等长分支投出整个 current（水印=`[" ，！。"]`），delta=`\x7f。` 不被包含、但 current 被包含 → 桥接静默，不叠加（over-sent 归 §3.4 上游既有缺陷）；
- 两条子路径同一转换场景的两支，恰好被 timer 抽水点分隔；正常单字符路径由分支 A（xterm 未投递）与 C（xterm 已投递）各覆盖。

### 3.4 桌面不回归论证（含上游既有缺陷披露）

- 桌面中文 IME（sogou/系统）直提交时 xterm setTimeout 路径已投递 → 分支 C 静默；
- 若未投递（旧版首字丢失场景）→ 分支 A 桥接补发，行为改善而非回归；
- `composed=false` 的普通输入根本不进入桥接逻辑；
- composition 全程（候选选字）由 xterm 自有路径投递，水印命中分支 C 或 delta 为空，桥接零行为；
- **上游既有缺陷披露**（评审 m16）：`_handleAnyTextareaChanges` 的等长分支 `t.length===e.length&&t!==e&&triggerDataEvent(t,!0)` 发送的是**整个 textarea 内容**（bundle 实证），上游 #5835 称其 "duplicated or over-sent data"。本桥接的分支 C 会正确静默——既不制造也不修复它；T5 验证时记为**既存问题**，避免误判为本次回归。

## 四、开发任务清单

| # | 任务 | 落点 | 产出 |
| --- | --- | --- | --- |
| T0 | 诊断埋点：`?iosime-debug=1` 门控的 textarea 事件流日志（keydown keyCode/key、input、composition*、keyup、textarea value）**+ 与 xterm 同构的 setTimeout(0) 时序探针**（评审 M3：现有 DOM 事件埋点回答不了"插入 vs 回调谁先"，探针在 keydown 内自排一个 0ms timer 打时间戳、与 input 时间戳对比——FIFO 下 xterm 的回调不晚于我们的探针，故探针早于 input 即证假设）；**+ 自带绑定实现**（评审 G2：唯一绑定函数 `ensureIosImeDirectInputBridge`，T0 阶段只登记诊断监听子集并发 `[ios-ime] bound`，否则 DATA-COLLECTION 的采集前置不可达——同一函数名、同一幂等标志 `iosImeBridgeTerm`，T1 落地即在其上叠加桥接监听，不留两套并行监听）；真机确认中/英键盘事件序列 | `index.html` | 事件流证据；校准 flush 边界（空格→。 转换序列、failsafe 取值——实测基线见 DATA-COLLECTION §三） |
| T1 | 桥接实现：两个纯函数 + ensure-binding + keydown/input/keyup/composition/blur/timer + onData 水印 + lastSeen 守卫 + 绑定点接线 | `index.html`（quick-keys IME 段邻位新区块；`renderTerminalSessions`；`focusTerminalIfPossible`） | 修复本体 |
| T2 | 单测：新建 `internal/ui/testdata/ios_ime_direct_input.test.mjs`，切片驱动**恰好 20 个 `^test(` 场景**（硬性验收，评审 F4：计数与 T4 CHANGELOG 数字词联动；≤20 才能用英文数字词拼写，`words` 表上限 twenty）：① 纯追加补发 ② 水印分支 C 抑制双发 ③ composition 不拦截 ④ 多敲击最早 baseline ⑤ 连续两窗互不吞字（HIGH-1）⑥ failsafe 补发 **+ 死键无插入→空 delta→有界二次机会→failsafe 无条件关窗→下一个键正常送达（N2，同一 scenario 的第二阶段）** ⑦ 重复 229 重臂 timer **+ 同窗口内两次"空 delta keyup flush"后窗口必定已关、下一个字符正常送达；`iosImeFailsafeArmed` 在关窗时复位（G1，同 scenario 的第二阶段）** ⑧ blur flush-then-clear **+ keyup 延后 flush 与 blur flush 交错只交付一次（F6，第二阶段）** ⑨ 外部清空后无幽灵 DEL ⑩ 上游等长序列 → `\x7f。` ⑪ 纯缩短单 DEL ⑫ emissions 作用域复位 ⑬ 重入标志排除自发 ⑭ 逐码点分块 ⑮ `rgb:` 整串不被过滤器吞 ⑯ 绑定幂等 ⑰ term 重建后重绑 ⑱ 死 term 容错 **+ timer 触发时 `session.term` 已销毁则 flush 静默（同 scenario 内断言，不新开 `^test(`）** ⑲ keyup 早于 input 次序单次投递 ⑳ 非 229 keydown 不拦截。验收附加：切片锚点须命中 §八 的区块横幅（区块被挪走/横幅被删则 slice 阶段失败） | `internal/ui/testdata/` | 文件恰好 20 个 `^test(` |
| T3 | Go 侧：`terminal_status_test.go` 加 node wrapper（`TestIosImeDirectInputHandling`）+ `TestResponsiveLayerChangelogCounts` 表新增一行（短语 **`new issue #115 ios-ime scenarios`**、与现有七条互异、搭配数字词 **twenty**）+ 契约断言**全部按符号名**（评审 m14）：`static/kinds/pty.js` 全文 `iosIme` 计数为 0、legacy factory 的 onData 门锚点原样（`isInstanceTerminalStatus` / `isTerminalQueryResponse` 调用仍在原函数体内）、桥接发送口只有 `term.input(`、`iosImeSending` 重入标志存在 + **bundle 指纹守卫** `TestIosImeBridgeMatchesVendoredBundle`（§8.3：读 **CWD 相对路径** `static/vendor/xterm/xterm.js`——Go 测试的 CWD 是 `internal/ui/`，评审 R3-C；指纹 `_pending229Baseline` 在 vendored bundle 中 0 命中 ⇒ 补丁必须在位；≥1 命中 ⇒ 补丁痕迹必须清零；该 bundle 是 git 追踪资产（R3-C 已核），CI 可稳定读取） | `internal/ui/terminal_status_test.go` | `go test ./internal/ui/` 全绿 |
| T4 | 文档：CHANGELOG 条目落 **`## Unreleased`**（评审 LOW-6/M8），计数措辞必须是 **`… twenty new issue #115 ios-ime scenarios …`**——数字词与短语之间**只有空格**（不加反引号/括号，否则 `(\w+) ` + QuoteMeta(phrase) 零命中，评审 F1），且**不得**复用 "Pinned by N `node --test` cases" 句形（digits 形式会被 `TestTerminalStatusChangelogCount` 误捕；该句形是 terminal_status.test.mjs 专属）；本文档；DATA-COLLECTION.md | 仓库根、`docs/plans/fix-issue115/` | 文档闭环 |
| T5 | 真机验证矩阵（beta 渠道 + 手动清单，见下节）签字 | — | 可合并 |

**T0/T2 验收附加（切片纪律，评审 MEDIUM-3）**：被切片区块内**不得引入新的自由标识符**（新增函数须整体可切片、依赖以 deps 注入）；`?iosime-debug` 读 `location.search` 必须在 `typeof location` 守卫内（或经 deps 注入）；测试字符串锚点边界包裹；切片块移动/重命名时 node 用例必须在 slice 阶段响亮失败，不允许以镜像副本通过。

## 五、真机验证矩阵（手动 / beta）

- iPad/iPhone × Safari / Chrome(iOS)：中文半角数字、中文直输标点、英文软键盘字母（T0 确认项）；
- `cat -v` 观察字节流确实到达 shell；
- vim INSERT 模式下全角/中文输入；
- **选字栏候选全角符号（今日正例，必须不回归）**；
- 半角输入回归；
- 桌面中文 IME（sogou / 系统）全角符号回归（CHANGELOG v0.3.0 桌面修复不回归）；
- **既存问题观察项**（评审 m16）：等长替换时 xterm 自发整个 textarea 内容的 duplicated/over-sent 行为，记为上游缺陷、不算本次回归（§3.3 分支 B/C 真实职责已给出该场景的两条子路径）；
- **死键路径**（评审 G7：v3 定的核心关窗规则必须有对应手测项）：连按两次 229 而 IME 未提交任何文本（死键/语言切换键），随后输入一个正常字符，确认该字符送达、无重复、且无残留窗口影响；
- 快速连续输入、粘贴按钮、quick-keys 面板按键、Esc/Ctrl 组合与桥接共存无异常；
- T0 探针结论回填：插入 vs setTimeout(0) 回调时序、failsafe 750ms 校准值。

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

- **零外溢**：区块内不新增 `window.*` 导出、不改任何既有函数体；所有 session 状态字段用 `iosIme` 前缀（`iosImePending` / `iosImeSeen` / `iosImeEmissions` / `iosImeSending` / `iosImeFailsafeArmed` / `iosImeFailsafeTimer` / `iosImeBridgeTerm` / `iosImeDataDisposable`——评审 R3-B：`failsafeArmed` 无前缀会脱离下述 grep pattern，"零外溢"论证与 §8.4 的零命中收尾断言都建立在前缀一致性上），既有代码零引用这些字段，删除后无残留引用。
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

*计划维护：fix/fix-issue115 ｜ v4 2026-10-16 ｜ 待 linletian 复审*
