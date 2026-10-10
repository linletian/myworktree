# PRD 变更文档：v0.6.0 — Web UI 侧栏折叠与响应式 / 触屏适配

> **状态**：已实施（PR1–PR4 全部落地并经双外部评审多轮复核；PR #109 评审意见逐条并入，PR #110 评审修复同步合入；本文档不构成实施指令，按 §4 分期开工）
> **日期**：2026-10-09（同日按评审修订）
> **目标版本**：v0.6.0（当前 `develop` = v0.5.2 release 合入 + 未发布变更）
> **来源 issue**：#99（`ui(sidebar): 工作树侧栏支持折叠 / 展开（桌面端）`）、#100（`ui(responsive): Web UI 响应式布局 + 触屏交互（iOS / iPadOS）`）
> **拟定分支名**：`feature/v0.6.0-webui-sidebar-responsive`
> **本文档位置**：`docs/plans/feature-v0.6.0-webui-sidebar-responsive/PRD-CHANGE.md`
> **主文档边界**：`docs/PRD.md`、`docs/API.md`、`docs/ARCHITECTURE.md` **本次不改**；本文档登记拟变更点，落地时由各实施 PR 按 §9 清单同步。

---

## 1. 背景与问题陈述

issue #99 与 #100 是同一产品问题的两个切面：**myworktree 的 Web UI 完全按「桌面鼠标 + 大屏」设计，既不能在桌面端临时把终端拉满，也不能在 iPad / iPhone 上真正使用**。

### 1.1 #99：桌面端侧栏折叠（优先级 低-中）

主界面为单文件 vanilla JS + CSS（`internal/ui/static/index.html`，HTML/CSS/JS 全部内联），布局是一层水平 flex。侧栏同时承载 worktree 列表 + git staged / unstaged 两个面板，240px 常驻宽度在全屏终端场景（agent 正在跑 TUI）下挤压终端可视列数。用户需要的是**临时折叠**，而非永久改布局。

### 1.2 #100：响应式布局 + 触屏交互（优先级 中）

零宽度断点、viewport 声明不完整、固定 px 布局在窄屏塌陷（iPhone 竖屏扣掉 240px 侧栏后 `#main` 只剩 ~150pt）、零 Pointer/Touch 事件、触控目标远小于 44pt、hover-only 样式在触屏上粘滞、iPadOS Split View 320pt~1366pt 全宽度域出现、iOS 软键盘遮挡。**若产品定位含「在 iPad 上真正能用」，第 1 层（视口与高度）成本极低、收益立竿见影。**

### 1.3 两个 issue 的合并点

#100 明确：窄屏 drawer **复用 #99 的展开/收起语义**，但形态是 drawer 而非宽度归零；并建议 #99 先落地宽屏行为、#100 只补窄屏。因此二者**不是两个独立需求，而是一个「侧栏可见性状态机」的两种渲染形态**，必须合并设计、分期实施。

### 1.4 代码现状（已对当前 `develop` 核实）

| 事实 | 锚点（当前 develop） |
|---|---|
| 单文件 vanilla JS + CSS，HTML/CSS/JS 全内联 | `internal/ui/static/index.html`（5562 行） |
| `#app` 一层水平 flex，`100vh` / `100vw`；**无 `position`**（drawer 定位包含块需补 `position: relative`，见 R7 / §5.3） | `index.html:158-162` |
| `#sidebar` 固定宽 `var(--sidebar-width)` = 240px，纯 CSS 常量无 JS 改写 | `index.html:9`、`165-171` |
| `#main` `flex:1` + `min-width:0` | `index.html:539-545` |
| `#header` / `#tabs-container`（已有 `overflow-x:auto`、隐藏滚动条规则族） | `index.html:1370`、`548-600`（滚动条规则族为 568-600） |
| 新建 / 导入按钮 `.sidebar-actions`（chevron 按钮天然宿主） | `index.html:1335-1338` |
| 侧栏垂直分栏拖拽 `#sidebar-resize-handle` → `startSidebarResize()`（mousedown/mousemove/mouseup，仅调 top/bottom 高度，与侧栏总宽无关） | `index.html:1343`、`2355-2392` |
| git staged/unstaged 互锁折叠 `toggleGitSection()` | `index.html:2200`、`1344-1365` |
| 全文件唯一 keydown：tab 重命名输入框 `handleRenameKeydown` | `index.html:2623`、`2565` |
| **localStorage：0 处**；sessionStorage 仅 `SERVER_UPGRADED_FLAG` | `index.html:1742`、`1797`、`2113-2114` |
| **宽度断点：0 个**（全文件仅 2 个 `@media`，均为 `prefers-color-scheme`） | `index.html:79`、`1208` |
| viewport meta 缺 `viewport-fit=cover`；`interactive-widget` 缺口经决策**不引入**（仅 Chromium/Android 支持，软键盘由 R6 的 `visualViewport` 方案覆盖，见 §3.5） | `index.html:5` |
| Pointer/Touch 事件、`dvh`、`visualViewport`、`env(safe-area-inset-*)`：均 0 处 | 全文件检索确认 |
| `body, html { overflow: hidden }`（页面本身不滚，软键盘遮挡无法靠滚动救回） | `index.html:144-152` |
| `--term-ctrl-btn-size: 32px`；`.icon-btn { padding: 2px }` | `index.html:23`、`187-194` |
| 终端键盘：pty 终端是同文档 xterm（非 iframe），产生数据的按键（如 Ctrl+B → `\x02`）由 xterm 在 `xterm-helper-textarea` 上自行截断（`_keyDown` 发送数据后 `cancel(e, true)` = preventDefault + stopPropagation），这些按键到不了 document 监听器；不产生数据的组合（Ctrl+Shift+B / Ctrl+Alt+B / macOS Cmd+B）不被取消，document 守卫兜底（PR #110 评审对 vendored 源码再核实） | vendored `internal/ui/static/vendor/xterm/xterm.js`（`_keyDown` / `evaluateKeyboardEvent` / `cancel`） |
| UA 显隐提示 `[hidden]{display:none}` 被 `@namespace` 限定在 HTML 命名空间，**对 `<svg>` 元素无效**（SVG 的显隐必须显式补 CSS 消费规则） | PR1 评审实测（headless Chrome：`svg[hidden]` computed display 仍为 inline） |

> 注意：issue 中的行号是 2026-10-08 提交时快照，与当前 develop 有少量漂移（如 `SERVER_UPGRADED_FLAG` 1742≠1766、`startSidebarResize` 2355≠2324、唯一 keydown 已从 modal 内 Enter/Escape 变为 tab 重命名输入框）。**实施时以符号锚点为准，不追行号。**

---

## 2. 需求综合（#99 + #100 合并视图）

**统一产品需求：交互式终端在桌面端可临时全宽；Web UI 在 iPad / iPhone 上真正可用。** 共用一个侧栏可见性状态，两种渲染形态，按视口断点分派。

### 2.1 需求条目（合并编号，供验收 / PR 引用）

| # | 需求 | 来源 |
|---|---|---|
| R1 | 桌面折叠态：`#app` 挂 collapsed CSS 类，`#sidebar` 宽度归零 + `overflow: hidden`，`#main` 因 `flex:1` 自动吃满；**JS 不做任何宽度计算** | #99 |
| R2 | 开关按钮：chevron 图标按钮，**放在 `#header` 左侧**（折叠态常驻可见，不给侧栏留宽度） | #99 |
| R3 | 持久化：`localStorage`，命名空间化 key `mw.ui.sidebarCollapsed`；文件首个 localStorage 用例；**宽屏默认展开**（窄屏 drawer 的默认值见 R14，同一 key 两端共享）；旧浏览器 / 隐私模式失败时降级为不持久化 | #99 |
| R4 | 快捷键 `Ctrl/Cmd+B` 切换：首个全局 keydown；不在 input / textarea / contenteditable 内触发，modal（`<dialog>`）前台不盲切后方侧栏（PR #110 B3 修复）。**机制事实（PR1 评审三轮订正、PR #110 评审再核 vendored 源码）**：pty 终端是同文档 xterm（非 iframe），终端侧产生数据的按键（如 Ctrl+B → `\x02`）由 xterm 自己在 helper textarea 的 keydown 处理器截断（`_keyDown` 在发送数据后 `cancel(e, true)` = preventDefault + stopPropagation——`cancel` 仅作用于产生数据或带 cancel 标志的按键如 Tab/Enter/Esc，非全量无条件），这些按键到不了 document 监听器；不产生数据的组合（Ctrl+Shift+B / Ctrl+Alt+B / macOS Cmd+B）不被取消、照常冒泡，父文档守卫是其兜底；input/textarea/dialog 守卫的真实职责是保护**不**自行截断的宿主与 modal 前台（modal 表单字段及其 `<select>` / 按钮宿主）；跨 frame 隔离只适用于 opencode / reasonix / dsh 的 web UI iframe（其内按键确实到不了父文档，不试图绕过） | #99 |
| R5 | 视口与高度：`#app` 高度 `100dvh`（`@supports` 保留 `100vh` fallback）、宽度 `100%` 取代 `100vw`；viewport meta 补 `viewport-fit=cover`；容器用 `env(safe-area-inset-*)` 处理刘海与 Home Indicator | #100 L1 |
| R6 | iOS 软键盘：监听 `visualViewport` 的 `resize` / `scroll`，键盘弹出时收窄终端可视高度，保证输入区可见 | #100 L1 |
| R7 | 断点与形态（**768px 归窄屏**：窄屏 `max-width: 768px`，中间档 769–1024px，依据见 §3.6）：窄屏侧栏改覆盖式 drawer（`position: absolute` + `transform: translateX`，`#app` 补 `position: relative` 作定位包含块），打开带遮罩、点遮罩关闭；遮罩盖住 `#main` 与 tab 区，但 drawer 打开态下 `#header` 左侧开关按钮仍可点击关闭（z-index 分层）；宽屏维持并排；中间宽度侧栏收窄或限上限（如 `max-width: 200px`），并排仍可用 | #100 L2 |
| R8 | 窄屏 `#tabs-container` 补 `scroll-snap-type` 让 tab 吸附（滚动条继续隐藏，复用 `#tabs-container::-webkit-scrollbar` 规则族既有实现——按选择器锚定，行号随 PR 漂移） | #100 L2 |
| R9 | 侧栏分栏拖拽从 mouse 事件迁到 **Pointer Events**（`pointerdown`/`pointermove`/`pointerup` + `setPointerCapture`）；handle `touch-action: none`（**PR4 仲裁**，发起于编码实施期、经评审确认：原文「拖拽时」解释为**常驻**——`touch-action` 在手势起始时求值，拖到 `pointerdown` 才设置对当前手势已晚，W3C Pointer Events §7.2 原文背书；4px 条带下无可滚内容，常驻无损失）；`user-select: none` / `-webkit-touch-callout: none` 按原文「拖拽时」字面走**拖拽态**作用域（`.dragging` class——选区在 `pointerdown` 同 tick 被拦，无需常驻；与 `touch-action` 规则不同，勿混用） | #100 L3 |
| R10 | 触控目标：`.icon-btn` 与 `--term-ctrl-btn-size` 在 `@media (pointer: coarse)` 下放大到 ≥ 44×44 **CSS px**（Apple HIG 的 44pt 在 iOS Safari 即 44 CSS px；CSS 写 `px`，照写 `44pt` 会得到 ~58.7px），用 `min-width`/`min-height` 而非固定 width/height，避免撑破现有布局 | #100 L3 |
| R11 | hover 样式用 `@media (hover: hover)` 包裹，纯触屏设备无粘滞高亮 | #100 L3 |
| R12 | 重排：折叠 / 展开 / 断点切换后，同文档 xterm 终端（xterm + xterm-addon-fit，非 iframe）与 reasonix / opencode / dsh 内嵌 iframe 不出现滚动条、错位、尺寸错误 | #99+#100 |
| R13 | 与 `toggleGitSection()` 的侧栏内折叠**状态互不覆盖**（两个正交状态） | #99 |
| R14 | 窄屏让位：窄屏（≤768px）使用 drawer 形态，不要求桌面折叠态在小屏生效；**窄屏首次加载（无持久化值）drawer 默认关闭**，避免首访即盖住 `#main`；持久化 key 两端共享——桌面收起过的用户窄屏 drawer 亦默认关闭，这是单一状态机默认值按视口分派的必然结果，**不是第二个状态** | #99+#100 |
| R15 | 范围边界：只覆盖 myworktree 自己的 Web UI（`internal/ui/static/index.html`，含同文档的 vendored xterm）；reasonix / opencode / dsh 的 **iframe 内页面**（`internal/instance/*` 各内嵌 web UI）不在范围，但本 UI 必须保证 iframe 容器拿到正确的高 / 宽 | #100 |
| R16 | 键盘语义隔离：`Ctrl/Cmd+B` 不会与 TUI 的 Ctrl+B 冲突——承重机制是 xterm 在 helper textarea 上自行 `stopPropagation()`（R4，vendored `xterm.js` 的 `_keyDown`/`cancel`），父文档监听器在终端场景不被调用 | #99 |

---

## 3. 关键设计决策

### 3.1 单一状态机，两种渲染形态（合并设计核心）

- 唯一真源：布尔态 `sidebarCollapsed`（R3 持久化）。
- 宽屏渲染：`width: 0` + `overflow: hidden`（R1）。
- 窄屏渲染：drawer off-canvas（`translateX(-100%)`）+ 遮罩（R7）；`#app` 补 `position: relative` 作 drawer 定位包含块，遮罩盖住 `#main` 与 tab 区但不盖住 `#header` 左侧开关按钮（打开态点它关闭）。
- 分派完全由 CSS 断点 + 同一个 class 完成，**JS 只有「读状态 → 挂/摘 class」一条路径**，没有第二处宽度计算。这是 issue #99「不与响应式宽度规则互相干扰」要求的落地方式。
- **PR3 注意（PR1 落地后的规则叠加）**：PR1 的折叠规则 `#app.collapsed #sidebar` 含 `width: 0` + `overflow: hidden` + `visibility: hidden`。PR3 做窄屏 drawer 时必须按断点限定/覆盖这条规则（如把折叠规则限定在宽屏断点，或 drawer 打开态显式恢复 `visibility` / `width`）——否则窄屏 drawer 打开时是一条**不可见**的抽屉（`visibility: hidden` 比 `width: 0` 更隐蔽）。

### 3.2 按钮宿主：`#header` 左侧

采纳 #99 推荐方案（非备选的 24px 竖条把手）：折叠态常驻可见、不给侧栏留任何宽度；窄屏 drawer 形态下同一个按钮即 drawer 开关，无需第二套控件。备选方案（侧栏保留竖条）拒绝理由：与 drawer 形态重复，且仍占宽度。

**层级前提（不可变）**：`#sidebar` 与 `#main` 是 `#app` 下的同级划分——侧栏全高，`#header`（实例 tab 栏 + 开关按钮）位于 `#main` 之内、随 worktree 切换。开关按钮在「`#header` 左侧」即 `#main` 顶栏的左侧，**不得**为了实现按钮而把 tab 栏提升为压住侧栏的全宽顶栏（那会把 worktree 列表视觉上降级为实例 tab 的子内容，违反产品层级）。

### 3.3 持久化：首次引入 localStorage

`mw.ui.sidebarCollapsed`，命名空间化与 issue 给的 key 一致。与既有 `sessionStorage` 的 `SERVER_UPGRADED_FLAG` 语义分工：后者是一次性升级标记（session 级），前者是用户偏好（持久级）。**默认值按视口分派**：key 缺失时宽屏视为展开、窄屏（drawer）视为关闭（R14）；解析失败 / 抛异常降级为不持久化，默认值同样按视口分派。

### 3.4 快捷键基础设施（首个全局 keydown）

- `document` 级 keydown，`metaKey || ctrlKey` + `key.toLowerCase() === 'b'` 判定（Caps Lock 或 Ctrl/Cmd+Shift+B 时 `event.key` 为 `'B'`，`=== 'b'` 会静默不触发）；忽略 `event.repeat`（长按 B 会反复切换）；排除 `event.altKey`（Ctrl+Alt+B 在部分布局是 AltGr 组合）。
- 输入守卫：`event.target` 落在 `<dialog>` 内（modal 前台不盲切后方侧栏，PR #110 B3 修复）或 input / textarea / `[contenteditable]` 内直接 return（含 tab 重命名输入框）。**职责边界（PR1 评审三轮订正、PR #110 评审再核 vendored 源码）**：终端侧产生数据的按键（如 Ctrl+B → `\x02`）由 xterm 在 `xterm-helper-textarea` 上自行截断（`_keyDown` 发送数据后 `cancel(e, true)`；不产生数据的组合不被取消，本监听器是其兜底）；守卫保护的是**不**自行截断的宿主（modal 表单字段：worktree 名 / branch / base ref / LLM 设置，及其 `<select>` 与按钮宿主）。
- web UI iframe（opencode / reasonix / dsh）内焦点时父页面收不到按键——**写注释说明，不做 postMessage 之类的绕过**（跨 frame 限制是浏览器安全模型，绕过反而引入新攻击面）。

### 3.5 视口基线与软键盘方案（R5 / R6）

- R5 动的是 `#app` 的**高度与宽度基准**（100dvh、100% 宽），R1 动的是 `#sidebar` 在 flex 行里的**宽度占比**，两者互不依赖，但必须在同一轮验证（折叠 + 键盘弹出同时发生）。
- **不引入 `interactive-widget=resizes-content`**：该 viewport meta 指令仅 Chromium/Android 支持，iOS Safari 忽略；本期目标平台为 iOS / iPadOS，软键盘遮挡由 R6 的 `visualViewport` 方案覆盖。§1.4 将 `interactive-widget` 列为现状缺口，由本条显式承接——对照 issue #100 阅读时不应视为 R5 静默缩窄了范围。

### 3.6 断点选取依据

三档均按视口宽度划分，**768px 归窄屏**（两档不得在 768px 同时命中）：

- **窄屏 `max-width: 768px`（drawer）**：覆盖全部 iPhone、iPad mini 竖屏（744pt）、9.7″/10.2″ iPad 竖屏（768pt），以及 iPad Split View 的 1/3 与 1/2 档（约 320–683pt，全部 ≤768）。
- **中间档 769–1024px（收窄侧栏并排）**：覆盖 10.9″/11″ iPad 竖屏（820/834pt）、12.9″ 竖屏与 9.7″ 全屏横屏（1024pt），以及 11″/12.9″ 横屏的 2/3 Split View 档（约 796/911pt）。
- **宽屏 > 1024px**：全宽侧栏并排。

注意 Split View 任何档位都到不了 1024pt（12.9″ 的 2/3 档约 911pt 已是上限），而 ≤768 的 Split View 档位全部走 drawer——**不要误以为「所有 Split View 都走中间档」**。中间档收窄侧栏而非直接 drawer，因为这些宽度下并排仍可用。

**分数宽度（PR3 评审裁定）**：三档按整数定义，但视口宽度可为分数（桌面浏览器缩放，如 200% 缩放下 1537px 物理宽 = 768.5 CSS px）。若「宽屏侧」媒体查询写作 `min-width: 769px`，(768, 769) 区间两档双双落空——drawer 不存在，且被包进宽档媒体查询的折叠规则一并失效，toggle 惰性（PR3 评审实证：这不是「等价于 PR3 之前」，PR1 的折叠规则原是全局的）。因此**承担功能分派的媒体作用域必须无缝互补**：窄屏 `max-width: 768px`，其互补侧写 `@media not all and (max-width: 768px)`（`all` 匹配全部媒体类型，取反后等价于 width > 768px；`not` 为 MQ L3，全目标浏览器支持），不写 `min-width: 769px`。中间档 `(min-width: 769px) and (max-width: 1024px)` 有意保留整数缝：缺席时落宽屏样式（240px 并排），纯外观差异、功能无损。

**drawer 宽度上限（已评估，本期暂缓）**：窄屏档含 iPad Split View 1/3 档（约 320pt），240px drawer 打开时覆盖约 75% 视口；overlay drawer 语义仍成立（遮罩余约 80px 可点关）。是否引入 `min(240px, 85vw)` 之类上限属产品决策，本期不设；beta 反馈若需要再立项（PR3 评审意见，仲裁记录）。

### 3.7 分期边界（每个 issue 的显式范围声明）

- #99：纯本地偏好，无后端改动、无 WS/HTTP 协议变更，可独立 PR。
- #100：纯前端，不涉及后端协议；第 3 层（触屏）可拆出单独排期；边缘滑出 drawer 是加分项，1、2 落地后再评估（**本期不做**）。

---

## 4. 分期实施计划（每期可独立合并、独立回滚）

| PR | 内容 | 覆盖需求 | 来源 | 依赖 |
|---|---|---|---|---|
| PR1 | 桌面侧栏折叠 / 展开 | R1–R4、R12（桌面部分）、R13、R16 | #99 全量 | 无 |
| PR2 | 视口与高度（最小改动，建议紧随 PR1） | R5、R6、R12 | #100 L1 | 无 |
| PR3 | 断点与 drawer 形态 | R7、R8、R14、R2（窄屏复用）、R12 | #100 L2 | PR1（语义复用） |
| PR4 | 触屏交互 | R9、R10、R11 | #100 L3 | PR2 |

- 顺序依据：#99 独立且风险集中在终端重排，先落地宽屏行为；#100 至少先做 L1。
- PR3 的 drawer 是 collapsed 语义的窄屏渲染，**不是新状态**——review 时重点检查有没有偷偷引入第二个状态变量。
- 范围红线 R15 适用于全部 PR。

---

## 5. 验收标准

### 5.1 PR1（#99）

1. 【人工过检】折叠 / 展开过程中无内容溢出、`#main` 无抖动（视觉 / 布局断言，自动化测试线覆盖不了，由 §6 人工清单兜底）。
2. 【人工过检】折叠态终端（同文档 xterm + xterm-addon-fit）重排后不出现滚动条或错位（同上，人工过检；自动化侧只锁定「先切 class、下一帧再 fit」的时序）。
3. 状态刷新后保持（localStorage 往返）；清掉 localStorage 后回到默认展开——自动化：`sidebar_collapse.test.mjs`（localStorage 往返 / 缺 key 默认）。
4. `Ctrl/Cmd+B` 可切换，且在 input 内输入不触发——自动化：`sidebar_collapse.test.mjs`（keydown 守卫全组：Ctrl/Cmd/大小写 / repeat / AltGr / input-host）。
5. `toggleGitSection()` 的 staged/unstaged 折叠状态不受影响，两个状态互不覆盖——自动化：`sidebar_collapse.test.mjs`（accordion 正交 interleave）。
6. `internal/ui/ui_test.go` 增加 DOM 锚点**文本断言**：collapsed 类名、`mw.ui.sidebarCollapsed` key 名、挂/摘 class 的相对顺序（httptest + `strings.Contains`，不执行 JS，对齐现有 UI 测试写法，见 §6）。
7. localStorage 往返、无 key / 解析失败时默认展开等**行为用例**走 node 线：`internal/ui/testdata/*.test.mjs` 从真实 served 源码切片 + 打桩 localStorage（见 §6）。

### 5.2 PR2（#100 L1）

1. `100dvh` + `@supports` fallback；`100%` 宽度取代 `100vw`，无竖向滚动条导致的横向溢出——自动化：`TestViewportAndHeightAnchors`（fallback 在前、`@supports` 块在后、`100%` 宽度声明）。
2. viewport meta 含 `viewport-fit=cover`；safe-area inset 已处理——自动化：`TestViewportAndHeightAnchors`（meta 切片、`#app` safe-area padding 块内断言）。
3. 【人工过检】地址栏展开 / 收起、软键盘弹出 / 收起时布局不跳动，终端输入区始终可见（对应 §6 人工清单软键盘项；行为侧 `viewport_height.test.mjs`：钉高 / 清除 / 缩放短路 / scroll 门控 / 折叠同场）。

### 5.3 PR3（#100 L2）

1. 【人工过检】iPhone Safari 竖屏（375×667 / 390×844）与 iPad Safari（含 Split View 窄档）下，侧栏、tab 栏、终端区域均可达，无横向滚动条、无内容被裁掉（布局行为，UA 度量，对应 §6 人工清单窄屏项；结构侧由 `TestNarrowDrawerAnchors` 锚定媒体块 / drawer / 遮罩规则，含 PR #110 B2 修复增补的 drawer `env(safe-area-inset-*)` padding 块内断言）。
2. 窄屏 drawer 开合正常，点遮罩关闭；tab 滚动吸附生效——自动化：`TestNarrowDrawerAnchors`（scroll-snap 声明、遮罩窄屏显示规则、遮罩 DOM 位置）+ `narrow_drawer.test.mjs`（遮罩点击关闭、遮罩/按钮/Ctrl+B 三路径同态）；视觉开合【人工过检】（§6 人工清单窄屏项）。
3. 中间宽度（769–1024）侧栏收窄后并排仍可用；恰好 768px 时走窄屏 drawer（边界归属唯一，见 §3.6）——自动化：`TestNarrowDrawerAnchors`（中间档块、两档媒体字面量）；【人工过检】§6 人工清单中间档项（PR3 评审后新增）。
4. drawer 以 `#app` 为定位包含块（`#app` 补 `position: relative`）；遮罩盖住 `#main` 与 tab 区，且 drawer 打开态下 `#header` 左侧开关按钮仍可点击关闭（z-index 分层）——自动化：`TestNarrowDrawerAnchors`（`position: relative` 块内断言、40/30/25 z-index 阶梯、按钮提升仅窄屏块内）；真机点按【人工过检】（§6 人工清单窄屏项）。
5. 窄屏首次加载（无持久化值）drawer 默认关闭，不遮挡终端；与桌面共享同一持久化 key——桌面收起过的用户窄屏 drawer 亦默认关闭（单一状态机的视口分派默认值，非新状态，R14）——自动化：`narrow_drawer.test.mjs`（窄 / 宽首访默认、显式值双方向优先、解析失败 / 抛异常分派、无 matchMedia / 抛异常 matchMedia 降级）+ `sidebar_collapse.test.mjs`（共享 key 往返）。

### 5.4 PR4（#100 L3）

1. 侧栏上下分栏拖拽在 iOS / iPadOS 上可用，且拖拽过程中不触发页面滚动——自动化：`touch_interaction.test.mjs`（capture 绑定 / 钳制边界 / 清理与 cancel-as-stop / 重入守卫 9 例）+ `TestTouchInteractionAnchors`（pointer 绑定、`touch-action: none` 常驻及手势起始求值时序注释）；真机拖拽手感【人工过检】（§6 人工清单 PR4 触屏项）。
2. 触控目标有效命中区（**范围经 PR4 仲裁按 R10 收窄**，证据：R10 仅列 `.icon-btn` 与 `--term-ctrl-btn-size` 两个目标；`--header-height: 40px` 放不下 44px 可见控件，`.tab` / 通用 `button` / modal 族的 44px 化会产生可见溢出，属 header 重新设计，记录为产品决策暂缓项）：`.icon-btn` 与 `.term-ctrl-btn` 在 `pointer: coarse` 下有效命中区 ≥ 44×44 **CSS px**（透明 `.icon-btn` 在 40px header 内的溢出不可见；无安全区设备上顶部溢出会被 `body { overflow: hidden }` 裁掉、裁剪区不参与命中测试，故 coarse 块内对 `#header .icon-btn` 下移 3px（PR4 r2 评审实证修正：border-box 下 `#header` 内容盒 = 40−1（border-bottom）= 39px，44px 盒居中后顶端 −2.5px，下移 2px 仍留 0.5px 在视口外 = 43.5px，3px 才落 [0.5,44.5]），使 44px 命中区完整落入视口；`.icon-btn` 在 `.sidebar-actions` 的第二处使用点会使侧栏标题行高从约 43px 撑到约 68px，触屏可见位移，R10 本意的接受后果；主使用点 header chevron 宽 20→44px，tab 条横向让位（375px 机型约 347→323px），`#tabs-container` 的 `overflow-x: auto` 兜底不裁切，同为 R10 接受后果；`.wt-item` 等全宽行单边远超 44 属既有形态）；对应 Apple HIG 44pt，CSS 写 `px` 不写 `pt`——自动化：`TestTouchInteractionAnchors`（`pointer: coarse` 块内 min-* 断言，禁词 `44pt`）；`pointer: coarse` 模拟验证（§5.5-1，自 PR4 起适用）+ 真机命中区【人工过检】（§6 人工清单 PR4 触屏项）。`.wt-action-btn`（hover 门控显现的次级动作）在纯触屏下不可达，为 R11 移除粘滞 hover 的接受后果，非本期回归。
3. hover 样式不再粘滞——自动化：`TestTouchInteractionAnchors`（`:hover` 选择器全部位于 `@media (hover: hover)` 块内的包含性扫描）；真机无粘滞高亮【人工过检】（§6 人工清单 PR4 触屏项）。

### 5.5 全期通用（最大风险项）

1. **桌面行为零回归**：`index.html` 是 5562 行单文件、没有真机触屏回归手段，至少桌面 400px 窄窗口人工过检一轮；`pointer: coarse` 模拟验证样式分支**自 PR4 起适用**（PR3 及以前不存在任何 pointer 媒体规则可验，PR3 评审裁定）。
2. 现有测试全绿：`internal/ui/ui_test.go`、`internal/ui/terminal_status_test.go`；CI 门（`gofmt`、`go test ./...`、双二进制 build）通过。

---

## 6. 测试策略（对齐仓库现有约定）

- **Go 资源文本断言**（`internal/ui/ui_test.go` 既有写法）：通过 `httptest` 取到 served 的 `index.html` / `static/kinds/*.js`，用 `strings.Contains` 断言 DOM 锚点、class 名、函数名与**相对顺序**（参考 `TestWebRenderersStoppedSwitchHidesAllFrames`、`TestEmptyWorktreeSwitchHidesAllWebPanels` 的 slicing + position 断言风格）。
- **node 行为测试**（`internal/ui/testdata/*.test.mjs`，`node --test`，由 `terminal_status_test.go` 的 `TestTerminalStatusHandling` 包进 `go test`）：需要真正执行逻辑的用例（localStorage 往返的状态读写、快捷键守卫、断点分派函数）走这条线——**从真实 served 源码切片、打桩浏览器状态**，不断言副本。
- **扫描型断言辅助的输入假设**（PR4 r3 评审补立，PR #110 评审加固落地）：`stripCSSComments` 这类纯文本扫描 helper 对输入有隐含假设（只识别 `/*…*/`、假设注释配平、无 `//` 行注释内嵌 `/*`）——整文档输入下 JS 行注释里的 `kinds/*.js` 会把 `/*` 误判为块注释开启符、静默吞掉后文（双侧评审独立复算实证：全文输入静默过删 50.3% / 截断于 66.7% 处）。现行用法已加固为只传 `<style>` 切片（44pt 禁词与 hover 包含性扫描同域）；helper 复用到新输入域前必须先验证其假设或给 helper 自身补例行断言——一次静默截断会让整条守卫失去意义。
- **CHANGELOG 房规**（issue #95）：若用 "Pinned by N `node --test` cases" 声明覆盖数，每一处出现的数字都必须等于**该条目所声明测试文件（集合）**的 `grep -c '^test('` 总数——现行守卫 `TestTerminalStatusChangelogCount` 按单文件 `terminal_status.test.mjs` 推导（该短语语义 = "the file's total case count"，见 #95 条目原文）；子集计数必须换措辞（如 "四个 #99 场景"）。新增 testdata 文件时不得用该短语引用全量总数，除非先把守卫改成全量求和并同步修订历史条目。
- **人工清单**（每期结束过一遍）：桌面宽屏 / 400px 窄窗；刷新持久化；清 localStorage 恢复默认（宽屏展开 / 窄屏 drawer 关闭，R14）；终端内焦点按 Ctrl+B（不应切换——xterm 在 helper textarea 上自行 `stopPropagation()`，R4/R16）；git section 折叠互不覆盖；折叠 / 展开两态下 chevron 图标各自正确显示（`<<` / `>>` 切换，UA 显隐断言原则）；真机捏合 / 双击缩放（布局不塌缩——`visualViewport.scale ≠ 1` 时 R6 不钉高）；iPhone 横屏刘海条带 / Home Indicator 条带的底色缝（已知外观 nit：双面板分色，padding 条带露画布底色，不影响内容可达性，如需消除再单独评估铺底方案）；iPhone Safari 真机软键盘：聚焦终端 / 输入框后键盘弹出时终端输入行仍在键盘上方、收起后回到 `100dvh` 全高不跳动（§5.2-3）；折叠态 + 键盘同时发生时终端无滚动条 / 错位（§3.5 同场要求）；中间档（约 800px 与 1024px 窗口）：侧栏收窄至 200px、并排仍可用，768px 整窗恰为窄屏 drawer（§5.3-3）；共享 key 跨端副作用（R14 预期行为，双方向过检）：窄屏点遮罩 / 开关关闭 drawer 会把 `true` 写入共享 key，同一浏览器 profile 的桌面端下次加载侧栏默认折叠，反向（桌面收起后窄屏首访 drawer 默认关）亦同；真机窄屏形态（§5.3-1/2/4 的 UA 度量落点，beta 渠道收敛）：iPhone Safari 竖屏（375×667 与 390×844）与 iPad Split View 1/3、1/2 档目视——侧栏 / tab 栏 / 终端均可达、无横向滚动条、无内容被裁掉，drawer 开合与点遮罩关闭、tab 吸附、打开态下开关按钮可点；**drawer 顶/底安全区**（PR #110 B2 修复回归点）：刘海机竖屏 drawer 首行（`.sidebar-title` 的 New/Import 按钮）不被状态栏/刘海压盖、底部内容不被 Home Indicator 压盖、横屏左右 inset 同检——修复形态为 drawer 盒子全出血 + `env(safe-area-inset-*)` padding 内容内缩（绝对定位 drawer 的包含块是 `#app` 的 padding box，`#app` 的 padding 不会自动内缩它）；PR4 触屏项（§5.4 落点）：`pointer: coarse` 模拟（DevTools 设备模拟）验证 `.icon-btn` / `.term-ctrl-btn` 放大分支与 hover 不粘滞；真机拖拽侧栏分栏（全程不触发页面滚动）、44px 有效命中区抽查（含终端左上角 4.5px 命中压盖带的点击归属——`#header .icon-btn` `top: 3px` 偏移的接受后果，折叠态下该带内点击归 chevron 按钮而非终端，代码注释已如实记录，此处为唯一用户可感知落点）；已知豁免项过检——`.wt-action-btn` 纯触屏不可达（R11 接受后果）、4px 拖拽条带触控抓取困难（`pointer: coarse` 下单独 `min-height` 加宽为低风险候选，beta 真机评估后定，§8 暂缓项②）。
- **验收-清单对应规则**（PR2 评审补立）：每条 §5.x 验收项必须在上方人工清单有对应项，或在该验收条上明确标注自动化覆盖断言（测试名）——不允许「验收写了、清单忘了」的结构性缺口。PR3 评审裁定后已回溯标注 §5.1–§5.3 全部条目；§5.4 随 PR4 落地时标注。
- **评审与合并过程房规**（PR4 评审补立）：① 评审输入必须由工作树现场 `git diff`（含 `git add -N` 新文件）生成，不用缓存 diff 文件；② 评审结束后核对工作树与评审前逐字节一致——评审 agent 的变异实验必须还原，限额 / 崩溃中途死亡的 agent 可能留下未还原变异（PR4 实证一次：限额死亡的 mcode 把 `44px→44pt` 变异留在树内，后续评审把它误报为交付缺陷）；③ 合并前以工作树实跑 `go test ./...` 全绿（必须在最后一次改动之后跑，实现期改动与测试运行之间不得有时序缝）。
- **UA 显隐断言原则**（PR1 评审教训，blocker 实证）：任何依赖 UA 表现性提示（如 `[hidden]`）或 UA 样式的「显隐」实现，**属性层正确 ≠ 渲染层正确**——必须有真实浏览器度量断言或人工过检项兜底，不能只靠「字符串锚点 + 打桩行为测试」一条线。
- **beta 渠道**：v0.6.0 发布前走 beta（v0.5.1 beta 曾抓出 3 个真实 bug 的先例），触屏相关问题主要靠该渠道收敛。

---

## 7. 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 5562 行单文件，回归面大 | 桌面样式 / 终端重排被无意破坏 | 小步 PR + 资源文本断言锚定关键结构；每 PR 独立可回滚 |
| 无真机触屏回归手段 | iOS/iPadOS 问题漏到发布 | 桌面窄窗 + `pointer: coarse` 模拟 + beta 渠道真机验证 |
| collapsed flex 逻辑与响应式宽度规则干扰 | 抖动 / 双重状态 | 单一 class 真源、JS 零宽度计算（§3.1） |
| 终端 / iframe 重排 | xterm 终端与各内嵌 web UI 滚动条、错位 | R12 列为每轮必验收项；折叠态 fit 触发顺序显式固定 |
| 首次引入 localStorage | 隐私模式 / 旧浏览器抛异常 | try/catch 降级为不持久化，默认展开 |
| `100dvh` 兼容 | 旧 Safari 高度塌陷 | `@supports (height: 100dvh)` 渐进增强 |
| Ctrl/Cmd+B 与浏览器扩展 / 系统快捷键冲突 | 偶发不触发 | 属用户环境问题，不绕过；在注释与文档中说明 |

---

## 8. 非目标（明确不做）

- iframe 内页面（vendor xterm、reasonix / opencode / dsh 内嵌 web UI）的内部自适应——各自的问题，本 UI 只保证容器尺寸正确（R15）。
- viewport meta 引入 `interactive-widget=resizes-content`（仅 Chromium/Android 支持，软键盘由 R6 的 `visualViewport` 覆盖；决策见 §3.5）。
- 边缘滑出 drawer（#100 标注的加分项，后续评估）。
- 任何后端 / WS / HTTP 协议变更。
- PWA、安装、离线能力。
- **本期暂缓、待 beta / 产品决策的四项**（PR4 评审仲裁立档，避免制度性遗忘）：① `.tab` / 通用 `button` / modal 族的 44px 触控放大——与 `--header-height: 40px` 存在可见溢出冲突，属 header 重新设计级决策（§5.4-2）；② 4px 拖拽条带的触控加宽——`@media (pointer: coarse)` 下单独 `min-height` 加宽是低风险候选方案，beta 真机评估后定（§5.4-1 人工清单项）；③ drawer 宽度上限 `min(240px, 85vw)`——320pt Split View 档覆盖约 75% 视口，遮罩语义仍成立，beta 反馈再立项（§3.6）；④ `.wt-action-btn` 纯触屏不可达——hover 门控显现的次级动作在桌面是既有形态，但在触屏上该显形原先经点按粘滞 hover（sticky hover）实现，R11 的包裹移除了这条路径，故不可达是 **PR4 新增**的功能性后果（此前「既有行为非 PR4 新增」的定性经 PR #110 评审订正）；其残留的「隐形但可点」命中区已用 `@media (hover: none) { .wt-actions { pointer-events: none } }` 关闭（opacity:0 不拦命中测试），恢复显形方案（如长按或常驻显示）仍属产品决策，beta 评估（§5.4-2）

---

## 9. 落地时对主文档的同步清单（**本 PR 之外，实施期执行**）

- `docs/PRD.md` §7「当前实现状态」：新增 v0.6.0 条目（侧栏折叠 + 响应式 / 触屏适配，注明来源 #99/#100 与 `docs/plans/feature-v0.6.0-webui-sidebar-responsive/`）。
- `CHANGELOG.md` `## Unreleased`：每 PR 一条，遵守 §6 房规。
- `README.md` / `README.zh-CN.md` Features：可选，折叠 + iPad 可用性值得一句。
- `docs/API.md`、`docs/ARCHITECTURE.md`：**预计不动**（无协议 / 架构变更）；如 PR 发现需要记录，单独评估。

---

## 10. 分支与版本

- **分支名**：`feature/v0.6.0-webui-sidebar-responsive`（承载 #99 + #100 的完整 v0.6.0 版本开发，非文档变更分支）。
- **版本**：v0.6.0，minor（新功能、无破坏性变更、无协议变更）。
- **基线**：`develop`（v0.5.2 已合入）。
- **发布**：beta 渠道先行（`develop` 出 beta tag），稳定后按既有 release 流程切 `main`。
