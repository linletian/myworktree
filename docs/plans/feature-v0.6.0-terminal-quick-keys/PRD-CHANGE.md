# PRD 变更文档：v0.6.0 — 终端快捷按键输入面板（padOS / iOS 触屏输入补偿）

> **状态**：需求评审三轮闭环（2026-10-13）；Phase 0 设计评审中；本文档不构成实施指令，按 §4 分期开工
> **日期**：2026-10-13（同日按用户补充修订：remote 访问的剪贴板系统归属契约——读写对象恒为**浏览器所在客户端系统**，见 §1.3 / R21 / R22 / §3.4）
> **设计评审修订（2026-10-13，Phase 0 设计稿用户评审）**：① 面板由「落在按钮列下方」改为**贴按钮列左侧展开、顶缘对齐入口按钮**（iPhone 窄屏可见性由宽度上限纪律承接）；② 明示**收起路径**（再点入口按钮 toggle 为唯一手动收起，不引入面板内 × 或点面板外关闭）——见 R19 / §3.1 / §3.2 / §5.1
> **目标版本**：v0.6.0（并入**未发布**的 v0.6.0 批次——侧栏响应式 PR1–PR4 与本面板同属一个版本；需求编号 R18–R22 接续该批次 PRD-CHANGE 的 R1–R17，编号不变）
> **来源**：issue #100 首个评论的共性输入缺口（padOS/iOS 软键盘无 Esc / Ctrl 组合键；终端无真实输入框与光标，长按无系统粘贴气泡）+ 用户立项指令（2026-10-13，PR #110 合并后）+ `docs/ORCA_MOBILE_TERMINAL_INPUT_RESEARCH.md`（2026-10-12）「四、建议立项方向」的正式承接——**编号注记**：该草案原称「R17」，已被 PR #110 beta 追加的终端触摸滚动占用；本文档自 **R18** 起接续编号（v0.6.0 PRD-CHANGE 末号为 R17，编号全局连续、不跨文档复用）
> **拟定分支名**：`feature/v0.6.0-terminal-quick-keys`
> **本文档位置**：`docs/plans/feature-v0.6.0-terminal-quick-keys/PRD-CHANGE.md`
> **主文档边界**：`docs/PRD.md`、`docs/API.md`、`docs/ARCHITECTURE.md` **本次不改**；本文档登记拟变更点，落地时由各实施 PR 按 §9 清单同步。

---

## 1. 背景与问题陈述

### 1.1 缺口回顾（issue #100 的输入半边，至今未愈）

issue #100 首评指出两类触屏缺口。v0.6.0（PR1–PR4 + PR #110 beta 追加）已修复**布局与滚动**半边（视口高度、软键盘钉高、drawer、触控目标、R17 触摸滚动与页面锁止），但**输入**半边原样存在：

- **缺口① 软键盘无 Esc / Ctrl**：iPadOS/iOS 软键盘不产生 Esc 键与 Ctrl 组合键事件；xterm 的 Ctrl/Esc 依赖真实 keydown 的修饰键标志位（vendored `evaluateKeyboardEvent`），软键盘不产生这类事件。纯触屏下 Esc、Ctrl+C/D 均无法发送——无法退出 TUI 模式、无法中断进程。
- **缺口② 无系统粘贴气泡**：终端的真实输入宿主是隐藏的 helper textarea（`xterm.css:61` 规则，`left: -9999em` 于第 68 行，离屏），屏幕上的光标是渲染回显而非原生 caret；终端画面不是原生可编辑元素，长按不触发系统选择/粘贴 callout。触屏上没有 summon paste 事件的手段。

证据与机理详见 `docs/ORCA_MOBILE_TERMINAL_INPUT_RESEARCH.md` 第一节（本节为其结论的引用，不重复论证）。

### 1.2 用户立项指令（2026-10-13，产品决策原文要点）

1. 在终端实例窗口增加常用按键快捷输入，重点包含 ESC 等 padOS/iOS 软键盘缺失能力；
2. 入口沿用实例窗口右上角「刷新」「快速到底」按钮风格，**新入口按钮放在刷新上面**，点击展开一个**浮在上层**的快捷按键输入窗；
3. 输入窗展开时切换实例，输入窗应被同时切换掉——**输入窗状态与实例窗口绑定**；
4. 快捷键应有独立 **ESC、复制、粘贴**；
5. 粘贴必须从系统剪贴板取内容**实际粘贴**，而不是输入一个 `^V` 字符到终端内；
6. 复制尽量做到 xterm 内选区后复制；做不到要给理由；**分别照顾 pty 终端状态与 TUI 状态**。

### 1.3 代码现状（已对 `develop` @ PR #110 合并后核实）

| 事实 | 锚点 |
|---|---|
| 终端控制浮层 `#terminal-controls`：`position:absolute; top:20px; right:20px; flex-direction:column; z-index:20`，现有两个 `.term-ctrl-btn`（Refresh & Scroll to Bottom / Scroll to Bottom） | `internal/ui/static/index.html:1808-1816`、`1951-1958` |
| 控制层显隐单源：`renderWorkspace()` 对 web kinds（opencode-web/reasonix/dsh-web）与无实例态挂 `oc-hidden`；pty kind 恒显示 | `index.html:3812-3869`（`oc-hidden` 切换在 3833/3868） |
| `.term-ctrl-btn` 32px（`--term-ctrl-btn-size`），`@media (pointer: coarse)` 下 `min-width/min-height: 44px`（R10 约定：CSS px 即 Apple HIG 的 44pt 落点） | `index.html:23`、`1822-1834`、`506-509` |
| 活动终端会话访问器 `getActiveTerminalSession()`（经 `__ptyRenderer.getSession` / 遗留 `terminalSessions` 双通道） | `index.html:2405-2415` |
| 键盘输入唯一闸门：`term.onData` 处理器——终态实例拒发（`isInstanceTerminalStatus`）→ 终端查询应答过滤（`isTerminalQueryResponse`）→ WS 直发 / HTTP 缓冲兜底；pty.js 工厂与 index.html 遗留工厂两套镜像实现 | `internal/ui/static/kinds/pty.js:324-394`、`index.html:4753` 附近 |
| 查询应答过滤器全貌：`MIN_RESPONSE_LENGTH=2`；非 ESC 开头只匹配裸 `;n;nR` / `?...$y` / `rgb:`；ESC 开头匹配 `ESC]`、`ESCP`、`ESC[?...$y/c/R`、`ESC[n;nR` 收尾 | `index.html:5183-5201` |
| vendored xterm v6 公开 API 实测在位：`getSelection()` / `hasSelection()` / `clearSelection()` / `onSelectionChange`；`modes` getter 暴露 `applicationCursorKeysMode` / `applicationKeypadMode` / `bracketedPasteMode` / `mouseTrackingMode`（`none`/`x10`/`vt200`/`drag`/`any`）等；`buffer.active`（BufferApiView，与 `buffer.normal` / `buffer.alternate` 可做恒等比较判定 alt screen）、`viewportY`、`getLine(i).translateToString(true)`；`input(e,t=!0){ this.coreService.triggerDataEvent(e,t) }`——程序化输入触发 `onData`，与键盘同路径 | vendored `internal/ui/static/vendor/xterm/xterm.js`（逐处 grep 实证） |
| vendored xterm 自带粘贴处理器（桌面 Cmd/Ctrl+V 路径）的语义：换行规范化 `e.replace(/\r?\n/g,"\r")`；bracketed 包装条件 = `decPrivateModes.bracketedPasteMode && !ignoreBracketedPasteMode`，**不含 alt buffer 判定** | vendored `xterm.js`（`i()` / `s()` 函数体，minified） |
| 触屏选区三证据（R17 评审终版机理 + 本次复核）：① vendored v6 的 Gesture 单例整体惰性（`addTarget` 与其姊妹入口 `ignoreTarget`（经 `Widget.ignoreGesture` 包装）均零调用方，单例从不构造——与 v0.6.0 PRD-CHANGE §3.8 逐字同口径），xterm 自身在触屏上无任何手势路径；② R17 在 `#terminal-container` 的非 passive `touchmove preventDefault` 阻断触摸→兼容鼠标事件合成，拖动手势不会变成 mousedown/mousemove；③ 自绘选区由 SelectionService 驱动，其监听为 mouse 事件族 | v0.6.0 PRD-CHANGE §3.8；vendored `xterm.js` |
| 剪贴板 API 硬约束：`navigator.clipboard.readText()` / `writeText()` 要求**安全上下文**（HTTPS 或 localhost）；iPad 经局域网裸 HTTP（`http://<mac-ip>:<port>`）访问时 `navigator.clipboard` 整体不存在——**这是本项目触屏主场景，兜底路径是一等公民而非边缘** | ORCA 调研报告 §一（MDN 背书）；myworktree 无 TLS 能力 |
| Remote 访问判定 `isRemoteAccess()`：hostname 非 loopback 即 remote（issue #72 单源），此时**浏览器所在系统 ≠ myworktree 进程所在系统**。剪贴板 API 是浏览器侧能力，读写恒为浏览器所在客户端系统的剪贴板——本设计不经后端取剪贴板（§8 锁死无协议变更），remote 场景下该区分**自动正确**；且 remote + 裸 HTTP 恰是不安全上下文，路径 B 即 remote 主路径 | `internal/ui/static/framework.js:43-52` |
| R17 触摸滚动映射委托挂载在 `#terminal-container`；`#terminal-controls` 是 `#workspace` 下的平级兄弟，不在其作用域内 | `index.html`（R17 实现，PR #110） |
| R6 软键盘钉高把 `#app` 高度钉在可视底缘；顶锚定的浮层在键盘弹起时保持可见 | v0.6.0 PRD-CHANGE §3.5 |
| 实例切换主路径：`selectInstance()`（tab 切换）、`deactivateInstanceRenderer()`（worktree 切换先行调用）、`PtyRenderer.deactivate()`（隐藏全部宿主、保留会话）、`cleanup()`/`destroySession()`（实例删除时销毁会话对象） | `index.html:3982`、`3918-3926`；`pty.js:205-229` |

---

## 2. 需求综合

**统一产品需求：纯触屏（无实体键盘）下，终端实例可输入 Esc / Tab / 方向 / Ctrl 组合等软键盘缺失按键，可复制终端文本、可从系统剪贴板真实粘贴——全部收进一个与实例绑定的浮层快捷按键面板。**

### 2.1 需求条目（编号接续 v0.6.0，供验收 / PR 引用）

| # | 需求 | 来源 |
|---|---|---|
| R18 | **入口按钮**：`#terminal-controls` 新增首子元素按钮（DOM 序在 Refresh 之上 = 视觉最上方），复用 `.term-ctrl-btn` 全部样式约定（32px / coarse 44px / `rgba(0,0,0,0.5)` 底 / hover 包 `@media (hover: hover)`），键盘类 SVG 图标，title 与既有按钮同为英文；显隐零新逻辑——随 `#terminal-controls` 的 `oc-hidden` 对 web kinds 与无实例态自动隐藏。**两个明示取向**（评审 N-3/N-5 补立）：① 桌面端同样显示、功能等价（鼠标可点）——`oc-hidden` 分支只对 web kinds / 无实例态隐藏（`index.html:3833/3868`），桌面 pty 恒显示为预期行为，不引入 coarse-only 显隐（那与「显隐零新逻辑」直接冲突）；② 无障碍延续 `.term-ctrl-btn` 既有约定仅 `title`（刻意与同列按钮一致——aria 三件套是 `#sidebar-toggle-btn` 一类全局控件的约定，不适用于此）；动作绑 `pointerdown` 意味着桌面纯键盘（Enter/Space → click）无法触发入口——刻意接受：面板仅服务触屏补偿，桌面用户有实体键盘与鼠标，面板对其冗余 | 用户指令② |
| R19 | **浮层面板壳 + 实例绑定状态机**：面板为浮于终端上层的浮层（DOM 作为 `#terminal-controls` 的子节点——免费获得 web-kind 隐藏同步与顶右锚定），承载按键按钮网格；**展开形态**（设计评审修订①）：面板 `absolute` 出 flex 流，**贴按钮列左侧展开、顶缘与入口按钮对齐**（`top:0; right:calc(100% + 8px)`），不再落按钮列下方；iPhone 窄屏可见性由 §3.1 宽度上限纪律承接；**收起路径明示**（设计评审修订②，清单见 §3.2）：手动收起唯一入口 = 再点一次入口按钮（toggle，左展形态下入口恒在面板右侧外可见），不引入面板内 × 或点面板外关闭；**可见性 = 活动会话对象的 `quickKeysOpen` 布尔旗标**（单一面板 DOM、每会话一旗标），同步函数在 `renderWorkspace`（每次 render 含 2s 轮询，幂等 classList.toggle）与开关处理器两处调用：切实例 A→B 时 A 的面板随会话隐藏（旗标留存、回切 A 时恢复展开——「绑定」的完整语义：面板既不会停在错误实例上，也不丢用户的展开状态）；删实例（会话销毁 → 旗标随之不可达 → 隐藏）、切 worktree（`activeInst` 置 null → 隐藏）、web kind（无 pty 会话 → 隐藏）三个边界由同一条「无活动会话即隐藏」规则覆盖；面板在 `#terminal-container` 之外，与 R17 触摸滚动映射零交互；顶锚定 + R6 钉高 → 软键盘弹起时面板顶部保持可见——**「保持可用」以 §3.1 尺寸纪律（高度上限 + 宽度上限 + 面板内部滚动）为前提**（评审 M-1 补立，设计评审修订①改写） | 用户指令③ + 设计评审①② |
| R20 | **字节键集与发送契约**：内置键 v1 = `Esc` `\x1b`、`Tab` `\t`、`Shift+Tab` `\x1b[Z`（同 Orca 内置键表；备注：若干 agent TUI 以 Shift+Tab 作模式切换——经验性备注，非本文档锚定事实，评审 N-2 修订）、方向四键（发送时读 `term.modes.applicationCursorKeysMode`：on 发 SS3 `\x1bOA-D`，off 发 CSI `\x1b[A-D`）、`^C` `\x03`、`^D` `\x04`；**发送一律 `term.input(bytes)`**——触发 `onData` 走与键盘完全相同的闸门（终态拒发 / 查询应答过滤 / WS-HTTP 传输），两套会话工厂无关、零新传输代码；载荷族对 `isTerminalQueryResponse` 的安全性已逐类核验（§3.3）；按钮动作绑定 **`pointerdown`（非 click）并 `preventDefault()`**——压制兼容鼠标事件族与 mousedown 默认聚焦行为，终端焦点不转移、软键盘不收起（§3.3 契约）；IME 组合态期间的字节排队至 `compositionend` 后发送（§3.3）；不做长按连发（v1 简化，Orca 的 400ms/45ms 连发留 §8） | 用户指令①④ + ORCA §2.2/§三 |
| R21 | **粘贴（真剪贴板，双路径）**：主路径 `navigator.clipboard.readText()`（瞬态用户激活内调用；安全上下文）；**兜底路径（不安全上下文 = iPad 局域网裸 HTTP 主场景）**：**复用同一面板 DOM 切换为「粘贴兜底态」**——在屏可见的真实 `<textarea>` + 提示 + 取消（评审 M-2 裁定选型 (a)，不新增第二浮层 DOM、不引入第二条显隐路径，详见 §3.4），提示长按→系统「粘贴」callout，监听 `paste` 事件取文本——iOS 上不存在第二个程序化读剪贴板路径（无 `execCommand('paste')`），两拍式是唯一全域可用方案；取到文本后：换行规范化镜像 vendored xterm 自带粘贴（`/\r?\n/g`→`\r`，孤立 `\r` 不动）→ 剥离载荷内嵌的 `\x1b[200~` / `\x1b[201~`（注入防护，Orca 纪律，xterm 自身未做）→ `term.modes.bracketedPasteMode` 为真时包 `\x1b[200~…\x1b[201~`（与 vendored xterm 自带粘贴同规则，**不按 Orca 多加 alt buffer 排除**，理由 §3.4）→ `term.input()` 同闸门发送；256Ki 码元上限（Orca 实证值的同语义承接，§3.4 ③④）；**系统归属契约**（用户补充 2026-10-13）：读取对象恒为浏览器所在**客户端系统**的剪贴板——remote 访问（`isRemoteAccess()`，浏览器与守护进程异机）时粘贴的正是客户端机器上的内容，而非 myworktree 进程所在机器；不引入任何后端剪贴板通道；空剪贴板 / 超限为带反馈的无操作（`updateStatus` 行）；**API 拒绝（同步 throw / promise reject）= 反馈 + 自动落入路径 B**（§7 风险表缓解条款落地，PR2 评审修订——本条早前写法「API 拒绝均为带反馈的无操作」与 §7 矛盾，以此为准） | 用户指令⑤ + ORCA §2.4 |
| R22 | **复制（选区优先 / 视口兜底，pty 与 TUI 分态正确）**：`term.hasSelection()` 为真 → 复制 `term.getSelection()`（桌面鼠标选区路径；TUI 鼠标上报态下桌面选区惯例为 Shift+drag，xterm 惯例不变）；否则 → 复制**当前视口文本**：`term.buffer.active` 的 `[viewportY, viewportY+rows)` 行逐行 `translateToString(true)`（行尾trim）、`\n` 连接、丢弃尾部空行——`buffer.active` 自动指向活跃缓冲，**pty 普通缓冲（shell）与 TUI alt screen 同一套代码、行为分态自动正确**（TUI 态复制到的是当前帧文本，对拷贝 TUI 报错 / 路径有用）；写入主路径 `navigator.clipboard.writeText()`（写入对象同为浏览器所在**客户端系统**剪贴板——复制的内容可直接粘进客户端本地应用，remote 访问下与粘贴语义对称），不安全上下文兜底 `execCommand('copy')`（临时 textarea + select，iOS Safari 支持，deprecated 但可用【beta 实测项】）；**触屏拖选不做**——三条结构性证据 + Orca 旁证，结论与理由记录于 §3.5，非目标登记于 §8 | 用户指令⑥ |

---

## 3. 关键设计决策

### 3.1 入口与面板形态（用户指令②的落地 + 被否方案）

- **入口**：`#terminal-controls` 首子元素。该容器已是顶右绝对定位浮层（`top:20px; right:20px`），「放在刷新上面」= DOM 序首位，零定位新增。
- **面板 DOM 作为 `#terminal-controls` 的子节点，`absolute` 贴按钮列左侧展开**（设计评审修订①）：`position: absolute; top: 0; right: calc(100% + 8px)`——面板出 flex 流，顶缘与入口按钮（容器首子元素）对齐、右缘贴按钮列左缘留 8px 间隙，向左覆盖终端区（浮层语义）；`renderWorkspace` 对 web kinds 挂的 `oc-hidden` 把入口与面板一起隐藏——显隐正确性免费获得，不新增第二条显隐路径（§3.2 的会话绑定只管 pty 场景内的开关）。**实现警戒**：① 出流后面板不受容器 flex 约束——此前「容器补 `align-items: flex-end` 防面板被 stretch 钳宽」的警戒**作废**（stretch 只对 in-flow 子元素生效）；② 左展出流后，容器高度若维持 auto（= 按钮栈高），面板的百分比 `max-height` 不解析（CSS 2.1 §10.5/§10.7：包含块高度依赖内容时，百分比按 `none`/`auto` 处理）——高度上限必须改由「容器双向偏移获得确定高」承接，见下条尺寸纪律；③ 键网格列数不得用 `repeat(auto-fit/auto-fill, minmax(...))` 依赖面板 shrink-to-fit 宽度（列数 ↔ 宽度循环依赖，宽度会按 max-content 摊成一行直抵 `max-width` 上限）——列数用媒体查询显式分档（默认 3 列 / 窄屏 2 列，断点 PR1 实施时随按钮实测宽度定）。
- **被否方案：键盘上方附件条（Orca `MobileSessionCommandDock` 形态）**。Orca 的附件条依赖 RN 原生层拿到键盘几何与 `Keyboard.dismiss` 等价物；浏览器侧无法程序化升降 iOS 软键盘（报告 §三「不可移植」第 4 条），附件条只能钉在终端底缘并常驻吃纵向空间，与 R6 钉高争夺可视区。用户已明确指定「右上角入口 + 浮层」形态，附件条形态不采用；其键表 / 连发参数 / 粘贴纪律等**机制层**成果照常吸收（R20/R21）。
- 面板自身样式复用控制层视觉语言（深色半透明底、圆角、coarse 下按键 ≥44px，R10/R11 约定同样适用于面板内按钮）。
- **面板尺寸纪律（评审 M-1 补立，R2-1/R2-2 修订，设计评审修订①改写）**：`#workspace` 为 `overflow: hidden`（`index.html:1281-1288`），顶锚定只保证面板**顶部**可见——R6 钉高在软键盘弹起时压缩 `#workspace` 可视高度，键集 v1 = 9 字节键 + Copy/Paste 共 11 个按钮（coarse 每个 ≥44px）总高可超可视区，溢出的下方按键会被静默裁切、且无任何补救路径；左展形态下超宽同理（iPhone 窄屏主战场）。契约（替换原「容器 `max-height` + 面板作 flex 子元素 `min-height:0`」方案——面板出流后原方案不再约束面板，警戒②）：**容器** `#terminal-controls` 补 `bottom: 20px` 与 `pointer-events: none`（三个按钮与面板各自 `pointer-events: auto`）——`top:20px`/`bottom:20px` 双向偏移使容器高度确定（= `#workspace` 高 − 顶部偏移 20 − 底部**状态行高度带 20px 保守常数**：`#status-bar` 为 `position:absolute; bottom:0; right:0`、11px 字号（无自身 line-height，经 UA normal ≈ 13px 行盒）+ 2×2px padding 实带 ≈ 17px、`z-index:10` 且 `pointer-events: auto`（`index.html:1324-1337`）；扣除量 20px ≥ 实带，面板底缘恒高于状态行顶缘，评审 R2-2/R3-1 复算余量约 3px，保守方向正确），且整列空档不吞终端右侧的点击/滚动/选区（原 auto 高容器只覆盖按钮带，`pointer-events: none/auto` 组合保持该语义等价）；**面板** `max-height: 100%; overflow-y: auto`——对确定高容器解析，R6 钉高经「JS 钉 `#workspace` 高 → 容器双向偏移收缩 → 面板上限」链传导，超高时**面板内部滚动**而非被裁，全部按键恒可达；**面板宽度** shrink-to-fit + `max-width: calc(100vw - 88px)`（保守常数推导：容器 `right:20px` + coarse 按钮列 44px + 面板-按钮间隙 8px + 视口左安全边距 16px；iPhone SE 320px 下 ≤232px，网格退 2 列全键可达——iPhone 窄屏可见性的硬承接）。键网格列数媒体查询分档（警戒③）压低常态总高。面板滚动在 `#terminal-container` 之外，与 R17 触摸映射无交互。

### 3.2 面板-实例绑定状态机（R19）

- **单一面板 DOM + 每会话布尔旗标**：旗标挂在会话对象上（`session.quickKeysOpen`）——会话对象是两个工厂各自创建的普通对象，挂属性与工厂无关；不引入第二个面板 DOM，也不引入全局「面板开着」变量（那正是用户报障过的「浮层停在错误实例上」类缺陷的结构来源）。
- **派生可见性**：`可见 = 活动pty会话存在 && session.quickKeysOpen`。同步函数 `syncQuickKeysPanel()` 的职责 = 读旗标 + `classList.toggle` + **隐藏时联动复位内容态**（粘贴兜底态 → 键网格，§3.4 瞬态性——隐藏路径不止开关处理器一条，切实例 / 删实例 / 切 worktree / web kind 全经 `renderWorkspace → sync`，复位必须落在 sync 路径上才普遍成立；评审 R2-3 补立），幂等，可从 `renderWorkspace`（每次 render，含 2s 轮询）反复调用而无副作用——沿用 v0.6.0 §3.1「JS 只有读状态→挂/摘 class 一条路径」的纪律。
- **恢复语义**：切实例只隐藏、不改旗标；回切时 `renderWorkspace → sync` 自然恢复展开。删除实例销毁会话对象，旗标随对象不可达，等价于关闭。
- **内容态归属**（PR2 评审修订，opencode minor）：粘贴兜底态（paste-mode）是**会话级**状态——进入时记录归属会话；`syncQuickKeysPanel` 除隐藏复位外，另在「面板仍可见但活动会话 ≠ 兜底态归属会话」时复位内容态（覆盖 A 开兜底态 → 切到同样开着面板的 B 的路径：B 应见键网格而非 A 的粘贴态）；复位与隐藏复位同函数、幂等。
- **收起路径清单**（设计评审修订②，2026-10-13）：① **手动收起唯一入口 = 再点一次入口按钮（toggle）**——左展形态下面板不遮按钮列，入口恒在面板右侧外可见可点；**不引入**面板内 × 控件或「点击面板外区域关闭」——后者需全局监听 = 第二条显隐路径（违 §3.1 纪律），且终端区点击/选区是高频操作，误触收起是更大的可用性伤害；② **派生隐藏** = 切实例 / 删实例 / 切 worktree / web kind（下条边界清单，旗标语义不变）；③ 明示**不**触发收起的动作：字节键发送后 / 粘贴发送后保持展开（连击 Esc、连续粘贴是主场景）；面板内 Esc 键 = 发送 `\x1b`，永不关面板；粘贴兜底态「取消」只切回键网格（§3.4）。
- **边界清单**（全部由同一条派生规则覆盖，无特判）：web kind 激活（无 pty 会话）；无实例 / 空 worktree；实例删除；实例停止（面板仍可开——复制可用，字节键与粘贴由 R20 闸门的终态拒发兜底，面板不额外置灰）。

### 3.3 发送路径契约（R20 的机制核心）

- **`term.input(bytes)` 是唯一发送口**。vendored v6 实测 `input(e,t=!0){ this.coreService.triggerDataEvent(e,t) }`——直接触发 `onData`，于是终态拒发、查询应答过滤、WS/HTTP 传输选择全部复用键盘路径；对 pty.js 工厂与 index.html 遗留工厂同样成立（`onData` 绑在各自 term 上，`input` 与工厂无关）。绕过 xterm 直发 WS 的被否理由：复制一份闸门逻辑，两套工厂漂移风险（§1.3 已记录两工厂「drifted before」的前科）。
- **查询应答过滤器安全性（逐类核验，§1.3 正则全貌）**：`Esc` 长 1 < `MIN_RESPONSE_LENGTH=2`；`Tab`/`^C`/`^D` 非 ESC 开头且不匹配裸应答式；方向键 / Shift+Tab 为 `ESC[` 开头但第二字符非 `?`、不以 `n;nR` 收尾；bracketed 包装后的粘贴载荷以 `ESC[200~` 起、`ESC[201~` 收，两端均不命中任何应答式。**唯一已知边缘**：未包装粘贴载荷若以 `ESC]` 或 `ESCP` 开头会被过滤器整个吞掉（过滤器对 OSC/DCS 应答的一刀切）——发生条件 = 剪贴板首字符为 ESC 且次字符 `]`/`P`，概率近零，立档不处理（§7 风险表）。
- **保焦点契约**：按键动作绑定 `pointerdown` 而非 `click`，handler 内 `preventDefault()`。依据：W3C Pointer Events——取消 `pointerdown` 压制兼容鼠标事件族（含 mousedown，其默认行为是焦点转移），终端的 helper textarea 保住焦点、iOS 软键盘不收起；`click` 是否仍触发属引擎差异，实现**不监听 click** 而与其绝缘（动作已在 pointerdown 完成）。反面方案（click + 事后 `term.focus()` 回补）在 iOS 上造成键盘可见的收起-弹起抖动，否决。
- **IME 组合态纪律**（ORCA §2.3 时序纪律的浏览器对应物）：监听 helper textarea 的 `compositionstart`/`compositionend` 维护组合态旗标；组合态期间到达的快捷键字节与粘贴载荷**排队**，`compositionend` 后按序 flush——避免注入字节与滞留 preedit 文本交错（保焦点契约使组合态在点键时仍存活，该窗口真实存在）。组合是秒级暂态，排队无感。**队列归属**（评审 §4-1 确认项补立）：队列按**会话**挂载，flush 时向产生组合的那个会话发送（`term.input` 与可见性无关，后台会话的 WS 仍活）——不重取活动会话（排队窗口内切实例会把字节送进错误的终端）；会话已销毁则队列随对象丢弃。**第三保险**（PR #112 评审 M-3 修复）：helper textarea 增绑 `blur`——焦点迁移即组合中止（UI Events 规范语义，组合随编辑宿主焦点消亡而终止），清位 + flush 到本会话；覆盖引擎未按规范补发 `compositionend` 的漏火情形（漏火则旗标永久卡真、后续点键静默入队且无任何反馈），与 R20 保焦点链不交叉（点键 pointerdown+preventDefault 不触发 helper blur），幂等。

### 3.4 粘贴双路径（R21）

- **分派条件**：`navigator.clipboard && navigator.clipboard.readText` 存在（=安全上下文）走路径 A；否则路径 B。不做 UA 嗅探，按能力分派。
- **Remote 访问矩阵（用户补充 2026-10-13）**：`isRemoteAccess()` 为真 = 浏览器与守护进程异机。剪贴板读写是浏览器侧 API，归属恒为**客户端系统**，与守护进程所在机器无关——契约上**不需要也不允许**后端剪贴板通道（§8 非目标同锁）。路径分派与访问形态的组合：本机 loopback + 裸 HTTP = 安全上下文（localhost 可信源）→ 路径 A；remote + 裸 HTTP（局域网 IP 直连）= 不安全上下文 → 路径 B（两拍式浮层，remote 触屏主路径）；remote + HTTPS 反代 / 隧道 = 安全上下文 → 路径 A。判定无需调用 `isRemoteAccess()`——能力分派（`navigator.clipboard` 存在性）已天然覆盖三种组合。
- **路径 A**：`readText()` 在 `pointerdown` 的瞬态用户激活内调用。平台差异如实立档为【beta 实测项】：iOS Safari 对程序化读剪贴板可能弹系统「粘贴」确认（iOS 16+ 粘贴隐私），Chrome 桌面首次调用触发 `clipboard-read` 权限提示——均为平台正当行为，不试图绕过。
- **路径 B（不安全上下文 = 主场景）**：**复用同一面板 DOM 做内容切换**（评审 M-2 裁定选型 (a)；否决新增第二浮层——那会违反 R19 单一面板与 §3.1 单一显隐路径纪律，其锚定/层级/边界规则也将整套重写）：面板进入「粘贴兜底态」，内容换成在屏可见的真实 `<textarea>` + 提示文案 + 取消按钮；显隐继续走同一会话旗标派生规则，web kind / 无实例 / 删实例 / 切 worktree 边界与键网格态完全一致。兜底态为**瞬态**：`paste` 事件捕获文本 → 发送 → 自动切回键网格；取消按钮 = 切回键网格（不关面板）；面板每次隐藏即复位键网格态（兜底态不持久化）。**焦点预期明示**（评审 §4-2 确认项）：兜底态 textarea 必须可聚焦才能长按弹出系统 callout——终端保焦点契约（§3.3）在兜底态期间主动暂停，终端失焦为预期行为，完成或取消后由用户点回终端恢复。这是 iOS 裸 HTTP 下唯一可行路径（`execCommand('paste')` 在 iOS 不存在；离屏 textarea 的 callout 锚在屏幕外不可用——缺口②的同一结构原因，所以 textarea 必须**在屏可见**）。
- **取文本之后的处理管线**（两路径共用）：① 换行规范化 `replace(/\r?\n/g, "\r")`——与 vendored xterm 自带粘贴处理器 `i()` 逐字符一致（孤立 `\r` 不翻倍）；② 剥离载荷内嵌的 `\x1b[200~`/`\x1b[201~`（Orca 注入防护纪律；vendored xterm 未剥离，属我们相对桌面路径的加固，记录差异）；③ 256Ki 上限截断并反馈（Orca 实证值；**按 JS 字符串码元计**，与 Orca 的字符串长度上限同语义——CJK 线上字节约 3 倍，上限目的是防呆非精确字节配额，PR2 双评审修订）；④ bracketed 包装条件 = `term.modes.bracketedPasteMode` 为真——**与 vendored xterm 自带粘贴同规则**；Orca 额外排除 altScreen 是其 RN 侧的保守选择，xterm 自身无此判定（`s()` 函数体实证），且 TUI（如开了 mouse 的 vim）开 bracketedPasteMode 正是期待包装，跟随 xterm 语义——**截断必须先于包装**（③④ 顺序为 PR2 双评审修订：包装后截断会切断收尾 `\x1b[201~`，bracketed 态应用吞掉后续按键；截断作用于包装前文本，包装标记字节不入上限）；⑤ `term.input()` 发送（§3.3 契约，含 IME 排队与过滤器核验——**投递会话在 Paste 键 pointerdown 入口捕获，异步兑现不重取活动会话**，窗口内切实例不串终端，已销毁会话由 onData 闸门终态拒发兜底，PR2 评审 major 修复）。
- **pty/TUI 分态**：粘贴发送本身不分态（字节进 PTY，应用自理）；唯一分态点是 ③ 的 bracketed 判定，由 `modes` 实时读取，自动正确。

### 3.5 复制分态与触屏拖选结论（R22）

**分态矩阵**（`buffer.active` 自动指向活跃缓冲，同一套实现两态自动正确）：

| | pty 普通缓冲（shell） | TUI（alt screen，常伴 mouse tracking） |
|---|---|---|
| 桌面选区 | 鼠标拖选 → `hasSelection()` → `getSelection()` | 鼠标上报被应用接管，需 Shift+drag 强制选区（xterm 惯例）；选区在 alt buffer 上同样有效 |
| 触屏 | 无选区机制（见下）→ 视口兜底 | 同左 → 视口兜底（复制到 TUI 当前帧文本） |
| 视口兜底语义 | 当前可见屏文本；用户可用 R17 触摸滚动定位后再复制 | 当前帧文本 |

**触屏拖选不做的理由（用户指令⑥要求的论证，三条结构性证据 + 一条旁证）**：

1. vendored xterm v6 的触摸手势层**整体惰性**（Gesture 单例从不构造，R17 评审终版机理）——xterm 自身在触屏上不存在任何选区路径，不是被谁挡住，是根本没有；
2. R17 的非 passive `touchmove preventDefault`（页面锁止的必要件）按规范阻断触摸→兼容鼠标事件合成，触屏拖动不会变成 SelectionService 监听的 mousedown/mousemove——**滚动与拖选在同一手势上互斥**，单指拖曳已分配给滚动（R17 手势分工，用户显式诉求）；
3. 可用触屏拖选 = 自建命中测试 + 选择手柄 + 边缘自动滚动 + 与 R17 滚动手势的仲裁，是独立大特性，远超本期范围；
4. 旁证：Orca（以 RN 原生 TextInput 接管输入的重度移动端实现）附件条同样只有 Paste 没有 Copy——移动端终端复制是行业级难点，视口复制是务实的 80% 方案。

**写入路径**：`writeText()`（安全上下文）→ 兜底 `execCommand('copy')`（临时在屏外但 `display` 可见的 textarea + `select()` + 同步执行，用户手势链内）；复制结果为空的视口（全空行）视为无操作并反馈。所有反馈走既有 `updateStatus` 状态行，不引入 toast 体系。

### 3.6 键集与字节表（R20 的表）

| 键 | 字节 | 备注 |
|---|---|---|
| Esc | `\x1b` | 独立发送；与键盘 Esc 完全同路径 |
| Tab | `\t` | |
| Shift+Tab | `\x1b[Z` | 同 Orca 内置键表 |
| ↑ ↓ ← → | `applicationCursorKeysMode` on：`\x1bOA/B/C/D`；off：`\x1b[A/B/C/D` | 发送时实时读 `term.modes` |
| ^C | `\x03` | 中断——触屏下此前完全无法发送 |
| ^D | `\x04` | EOF / 退出 shell |
| Copy / Paste | 非字节键，走 R22 / R21 管线 | |

不收通用 Ctrl/Alt **锁存修饰态**（iSH/Termius 式两态锁存）：Orca 同样未覆盖（预烘焙组合之外靠自定义宏），锁存态与 R4/R16 的 `Ctrl/Cmd+B` 侧栏快捷键存在优先级交叉，需单独设计——登记 §8 非目标 / 未来方向。

---

## 4. 分期实施计划（每期可独立合并、独立回滚）

| PR | 内容 | 覆盖需求 | 依赖 |
|---|---|---|---|
| PR1 | 入口按钮 + 浮层面板壳 + 实例绑定状态机 + 字节键（Esc/Tab/Shift+Tab/方向/^C/^D） | R18、R19、R20 | 无 |
| PR2 | 复制（选区优先 / 视口兜底）+ 粘贴（双路径） | R21、R22 | PR1（面板壳） |

- 顺序依据：PR1 纯字节发送、无浏览器 API 方差，风险最低先落地；PR2 的剪贴板行为跨平台方差大（安全上下文分派、iOS 粘贴确认、execCommand 兜底），独立成 PR 独立评审。
- 编码 / 评审流程沿用 v0.6.0 既定约定（编码 sub-agent + mcode / opencode 双外部评审 + 仲裁 + 原子提交），评审输入为工作树现场 `git diff`。

---

## 5. 验收标准

### 5.1 PR1

1. 实例窗口右上角出现键盘图标入口按钮，位于 Refresh 之上，视觉风格与 Refresh / Scroll to Bottom 一致；coarse 指针下 ≥44px——自动化：Go 资源锚点断言（按钮 DOM 序、`term-ctrl-btn` class、面板容器锚点与相对顺序）。
2. 点击入口展开、再点收起（toggle——左展形态下入口不被面板遮挡，§3.2 收起路径清单）；面板展开时切实例 A→B 面板随 A 隐藏，回切 A 恢复展开；删实例 / 切 worktree / 切 web kind 后面板不可见——自动化：node 行为测试（从真实源码切片的同步函数 + 打桩会话/state）。
3. 面板各键发送字节正确（含方向键两模式分支），经 `term.input` 到达 onData 闸门；终态实例拒发——自动化：node 行为测试（字节表纯函数切片 + 模式桩）。
4. 面板点按不转移终端焦点（pointerdown + preventDefault 契约锚点断言）；IME 组合态排队逻辑——自动化锚点 + node 切片。
5. 软键盘弹起时面板顶部保持可见（全部按键的可达性由第 6 条尺寸纪律承接）【人工过检 / beta】；iPhone 窄屏与 iPad 上入口不被 drawer tab / tab 条遮挡、面板左展后完整可见不被 `#workspace` 左缘裁切（§3.1 宽度上限纪律）【人工过检】。
6. 软键盘弹起 + 矮屏（iPhone 横屏）组合下面板超高时**内部滚动**、全部按键可达，不被 `#workspace` 裁切（§3.1 尺寸纪律）【人工过检 / beta】——自动化：Go 锚点断言（`#terminal-controls` 的 `bottom: 20px` 与 `pointer-events: none`、按钮与面板 `pointer-events: auto`、面板 `position: absolute` / `top: 0` / `right: calc(100% + 8px)` / `max-height: 100%` / `overflow-y: auto` / `max-width: calc(100vw - 88px)` 锚点）。

### 5.2 PR2

1. 安全上下文（localhost / HTTPS）下 Paste 读系统剪贴板真实内容入终端；bracketed 模式开时包装、关时裸发；载荷内嵌 200~/201~ 被剥离；`\n`/`\r\n` 统一为 `\r`；超限截断反馈——自动化：node 行为测试（管线纯函数切片 + modes 桩）。
2. 不安全上下文（局域网 IP 访问）下 Paste 将面板切换为粘贴兜底态（同一面板 DOM，两拍式），textarea 长按系统粘贴后自动发送并切回键网格；取消按钮切回键网格而不关面板【人工过检 / beta：iPad→Mac 裸 HTTP 主场景】。
3. 桌面鼠标选区后 Copy 复制选区文本；无选区时 Copy 复制当前视口文本；pty 与 TUI（alt screen）两态结果分别正确【自动化切片 + 人工过检：opencode TUI 内复制当前帧】。
4. 不安全上下文下 Copy 走 execCommand 兜底成功【beta 实测】；iOS 路径 A 的系统粘贴确认行为如实记录【beta 实测】。
5. Remote 访问（浏览器与守护进程异机）下：粘贴取到的是**客户端系统**剪贴板内容而非守护进程所在机器；复制的内容落入客户端系统剪贴板、可粘进客户端本地应用【人工过检：双机实测，记入 §6 人工清单】。

### 5.3 全期通用

桌面端（fine 指针）既有行为零回归：键盘输入、鼠标选区复制（浏览器既有路径）、Refresh / Scroll to Bottom、R17 触摸滚动、R6 钉高。面板不监听 click、不改任何既有监听器。

---

## 6. 测试策略（对齐仓库现有约定）

- **Go 资源文本断言**（`internal/ui/ui_test.go` 既有写法）：DOM 锚点、class 名、函数名与相对顺序（入口按钮在 Refresh 之前的 DOM 序、面板容器在 `#terminal-controls` 内、pointerdown 绑定锚点）。
- **node 行为测试**（`internal/ui/testdata/*.test.mjs`，`node --test`，包进 `go test`）：纯函数边界点名（评审 §4-3 补立）：`quickKeyBytes`（键→字节，含 `applicationCursorKeysMode` 分支）、`normalizePasteNewlines` / `stripBracketedPasteMarkers` / `maybeWrapBracketedPaste`（粘贴管线三段 + 256KiB 上限截断）、`extractViewportText`（视口提取，打桩 buffer）、`deriveQuickKeysVisibility`（派生可见性同步）、IME 排队 flush——**从真实 served 源码切片、打桩浏览器状态**，不断言副本。
- **CHANGELOG 房规**（issue #95）：计数短语语义同前；新增 testdata 文件不得用 "Pinned by N `node --test` cases" 短语引用全量总数（守卫按 `terminal_status.test.mjs` 单文件推导）。
- **评审与合并过程房规**（沿用 v0.6.0 §6）：评审输入现场 `git diff`；评审后工作树逐字节核对；合并前最后一次改动之后实跑 `go test ./...` 全绿。
- **人工清单（beta 渠道收敛，触屏主战场）**：iPad 局域网裸 HTTP 的两拍式粘贴全流程；remote 访问双机实测——粘贴取自客户端（浏览器所在）系统剪贴板、复制落入客户端剪贴板可粘进本地应用（§5.2-5）；iOS 路径 A 粘贴确认弹窗行为记录；路径 A 拒绝后自动落兜底态的行为记录（PR2 评审修订④）；不安全上下文下 `execCommand('copy')` 兜底复制真机实测（§5.2-4 对应项，mcode 评审补登记）；IME 组合中切粘贴兜底态的行为记录（兜底态 textarea 抢焦点、若组合态未消粘贴载荷入 IME 队列排队——mcode 评审补登记）；面板点键时软键盘不收起、终端焦点不丢；拼音 IME 组合中点 Esc/^C 的排队 flush；opencode TUI 内 Shift+Tab / 方向键 / 视口复制 / bracketed 粘贴；软键盘弹起 + iPhone 横屏矮屏下面板内部滚动、全部按键可达（§5.1-6）；窄屏下入口与面板不被 drawer tab、tab 条、「+」按钮遮挡；面板展开切实例 / 删实例 / 切 worktree 三路径。
- **验收-清单对应规则**（沿用）：每条 §5.x 验收项在本节人工清单有对应项，或标注自动化断言名。

---

## 7. 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| iOS 剪贴板行为跨版本方差（粘贴确认弹窗、瞬态激活窗口） | 路径 A 在部分系统多一次确认或失败 | 双路径分派 + beta 真机实测立档；失败降级路径 B |
| 未包装粘贴载荷以 `ESC]`/`ESCP` 开头被查询应答过滤器吞没 | 极端边缘下粘贴丢失（需剪贴板首两字符为 ESC+`]`/`P`） | 立档不处理；若真实出现，评估在粘贴管线前置转义 |
| 7000+ 行单文件回归面（撰写时 6553 基线 + 本特性增量） | 既有控制层 / 终端行为被无意破坏 | 面板为纯增量 DOM + 新监听器，不改既有行；Go 锚点断言钉住相对顺序 |
| `pointerdown` 与 `click` 的引擎差异 | 个别浏览器动作双发或不发 | 动作只绑 pointerdown、不监听 click，与差异绝缘；beta 抽查 |
| IME 排队窗口内终端已断开 | 排队字节落到死会话 | flush 时仍过 onData 闸门（终态拒发 / 传输兜底），无新风险；队列随会话销毁丢弃（§3.3 归属条款） |
| 面板与 R17 手势 / R6 钉高交互 | 滚动锁止被旁路，或面板在键盘弹起 + 矮屏下被 `#workspace` 裁切 | 面板在 `#terminal-container` 作用域外 + 顶锚定 + §3.1 尺寸纪律（容器双向偏移确定高 + 面板 `max-height:100%` 内部滚动 + `max-width` 上限）；容器整列 `pointer-events: none` 防吞终端右侧交互；beta 同场验证 |
| IME 绑定残差窗口（opencode 评审 minor，仲裁登记不修） | 新实例 `activate()` 创建会话对象在首个 render 之后，IME 监听由下一次 render（≤2s 轮询）挂上；该窗口内开始的组合无监听，组合中点键直发（与 preedit 交错风险） | 无 JS API 可查询进行中的组合，彻底修复需工厂侧绑定（§8 锁死不动两工厂）——接受为残差；toggle 开面板时会补绑（已开始的组合无法补听）；窗口需「切实例后 2s 内聚焦并开始拼音组合且组合中点键」才命中，概率低 |
| `updateStatus` 反馈可见性（mcode PR2 评审 minor，仲裁登记不修） | 反馈文案渲染为 `Transport: <msg>` 且 renderWorkspace 的 2s 轮询会以传输状态覆写同行——「Paste failed: NotAllowedError」类反馈存活 ≤2s，iOS 上基本必然错过 | 实现符合 §3.5 字面（走既有 updateStatus 行、不引 toast）；不动共享状态机（回归面纪律）；beta 人工清单确认反馈可见性，若实证不足再立项评估持久提示（如面板内提示行） |
| R22 复制路径 writeText 异步拒绝后 execCommand 兜底的激活窗口归属（PR #112 评审 M-1，仲裁登记为接受残差） | 严格 task 级激活模型的引擎上，writeText 的 rejection 回调可能已落在用户激活窗口之外，`execCommand('copy')` 返回 false——用户看到 "Copy failed"（非静默、可重试）；time-windowed 激活引擎（Chrome 秒级瞬态激活）不受影响，兜底照常生效 | 失败非静默（updateStatus 反馈）+ 用户可重试；移除兜底只会收窄挽救面故保留代码（注释已注明引擎差异）；beta 真机确认各引擎实际行为 |
| `execCommand('copy')` 兜底带走终端焦点（mcode PR2 评审 nit，仲裁登记不修） | 兜底复制的临时 textarea removeChild 后焦点落 body，终端失焦、软键盘收起（仅不安全上下文路径） | 复制是一次性动作、焦点代价可接受；代码注释已声明预期；beta 实测若体感糟糕再评估复制后 `term.focus()` 回补 |
| 已销毁会话的投递窄窗口（mcode PR2 评审 minor，修复落地） | pty.js `_destroySession` 只 dispose 不置 null（`pty.js:410`），pointerdown 捕获的会话在 readText 兑现窗口内被销毁时 `term.input()` 会在已 dispose 的 Emitter 上抛 TypeError（走不到 onData 闸门）；IME flush（`flushQuickKeysQueue`）同一根因 | 粘贴投递处 try/catch + `updateStatus` 反馈、IME flush 处同守卫静默丢弃（字节本就投给死会话）（PR2 修复轮落地）；legacy 工厂 cleanup 置 null 的路径本就安全 |

---

## 8. 非目标（明确不做）

- **触屏拖选复制**——结构性不可行（三条证据见 §3.5），自建触摸选区是独立大特性，且与 R17 单指滚动在同一手势上互斥；如未来立项需先解决手势仲裁。
- 通用 Ctrl / Alt 锁存修饰态（iSH/Termius 式；与 R4/R16 快捷键优先级交叉，需单独设计）。
- 键集自定义（排序 / 显隐 / 宏）与持久化（Orca `TerminalShortcutSettings` 形态）——本期键表内置固定。
- 长按连发（Orca 400ms/45ms）——v1 单击单发，手感反馈后再评估。
- Orca 的键盘避让锚点行 translateY 增强（§2.5 调研项）——R6 钉高之上增量，单独评估。
- 剪贴板图片 / RTF 等非文本粘贴（浏览器侧本就只能文本）。
- web kinds（opencode-web / reasonix / dsh-web）iframe 内的等价能力——各自页面的事，入口随 `oc-hidden` 对它们隐藏。
- 任何后端 / WS / HTTP 协议变更。

---

## 9. 落地时对主文档的同步清单（**本 PR 之外，实施期执行**）

- `docs/PRD.md` §7「当前实现状态」：并入 v0.6.0 条目（终端快捷按键输入面板，注明来源 issue #100 输入缺口与 `docs/plans/feature-v0.6.0-terminal-quick-keys/`）。
- `CHANGELOG.md` `## Unreleased`：每 PR 一条，遵守 §6 房规。
- `README.md` / `README.zh-CN.md` Features：可选，触屏输入补偿值得一句。
- `docs/ORCA_MOBILE_TERMINAL_INPUT_RESEARCH.md`：其「建议立项方向」的立项落点指针注记**已随本 PR 一并补入**（实施期无需再动）。
- `docs/API.md`、`docs/ARCHITECTURE.md`：**预计不动**（无协议 / 架构变更）。

---

## 10. 分支与版本

- **分支名**：`feature/v0.6.0-terminal-quick-keys`（承载 R18–R22 的完整开发，非仅文档分支）。
- **版本**：并入未发布的 v0.6.0 批次（与该版本一并走 beta → release；纯增量 UI 功能、无协议变更）。
- **基线**：`develop`（PR #110 已合入）。
- **发布**：随 v0.6.x beta 渠道真机收敛（触屏问题主要靠 beta，v0.5.1/v0.6.0 先例）。
