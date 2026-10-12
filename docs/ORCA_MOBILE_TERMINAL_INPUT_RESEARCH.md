# Orca 移动端终端输入调研报告（issue #100 补充调研）

**日期**：2026-10-12
**调研对象**：[stablyai/orca](https://github.com/stablyai/orca)（浅克隆于 `/tmp/orca-research`，React Native 移动端 + WebView 内嵌 xterm.js）
**起因**：issue #100 评审后用户提出两个移动端缺口——① iPadOS/iOS 软键盘无 Esc 键、无 Ctrl+C/V 组合键，终端实例如何输入；② 终端无真实可见输入框与光标（光标为渲染回显），长按无法弹出系统粘贴气泡。

---

## 一、myworktree 现状核查（问题确认，含证据）

### 终端输入模型（v0.5.x 起一直如此）

- 终端为 xterm.js（vendored v6.0.0）同文档渲染。真实输入宿主是 helper textarea（`xterm-helper-textarea`），但被刻意隐藏：`internal/ui/static/vendor/xterm/xterm.css:61` — `position: absolute; opacity: 0; left: -9999em`（原注释："so that the cursor is not visible"）。
- 屏幕上的块状光标是渲染器绘制的**回显**，非原生 caret；选区亦由 xterm 自绘。
- `docs/CHINESE_IME_ANALYSIS.md`（2026-03-24，v0.5.x 时代）分析的就是同一 textarea 机制，证明该模型自 v0.5.x 未变。
- 输入路径：`internal/ui/static/kinds/pty.js:324` — `session.term.onData(data => …)` → WebSocket → 宿主 PTY。

### 缺口 ① 软键盘无 Esc/Ctrl —— 无任何补偿机制（确认）

- xterm 的 Ctrl/Esc 依赖真实 keydown 的修饰键标志位（`evaluateKeyboardEvent`），软键盘不产生这类事件。
- 终端控制区仅 Refresh / Scroll-to-Bottom 两个按钮（`index.html` `.term-ctrl-btn`），无 Esc/Ctrl/粘贴设施。
- 纯触屏下 Esc、Ctrl+C/V/B 均无法发送；iPad 接实体键盘则完全正常（PR #110 评审实证 Ctrl+B → `\x02` 入 PTY）。

### 缺口 ② 长按无粘贴气泡 —— 结构性（确认）

- 隐藏 textarea 的系统编辑 callout 锚定在屏幕外（`left: -9999em`）；终端画面不是原生可编辑元素，长按不触发系统选择/粘贴 UI。
- xterm 有粘贴处理器（`handlePasteEvent`，同时绑在 textarea 与终端容器上）——**只要 paste 事件到达即可入 PTY**，但触屏上没有 summon 该事件的手段。
- 实体键盘 Cmd/Ctrl+V 可用；iOS 三指轻拍编辑工具栏理论上可投递 paste 到聚焦的 textarea（离屏，行为待真机验证）。
- `navigator.clipboard.readText()`：iOS Safari 13.4+ 支持，但**要求安全上下文（HTTPS/localhost）**——myworktree 典型的局域网裸 HTTP 场景下 `navigator.clipboard` 不可用（[MDN](https://developer.mozilla.org/en-US/docs/Web/API/Clipboard/readText)），这是本项目特有的硬约束。

---

## 二、Orca 的做法

### 2.1 架构决策（不可移植，但解释了其设计动机）

Orca 把 WebView 内 xterm 的 helper textarea **显式废掉**（`mobile/src/terminal/document/query-reply.ts:48-58`：`readOnly` + `inputmode=none` + `attachCustomKeyEventHandler(() => false)`），输入由 WebView 外的原生 RN `TextInput` 接管、经 RPC 直写宿主 PTY；xterm 只负责渲染与度量上报。其 WebView `onData` 仅放行查询应答白名单（CPR/DSR/DA）。

### 2.2 附件键条（对应缺口 ①）

- **键定义表**（`terminal-key-definitions.ts:97-142`）：21 个内置键，`{id, label, bytes, repeatable?}`——`Esc='\x1b'`、Shift+Tab=`\x1b[Z`、`⌫='\x7f'`、Del=`\x1b[3~`、四方向=`\x1b[A/B/C/D`、Ctrl 预烘焙 9 组合（C/D/L/Z/R/A/E/W/U）。
- **无通用 Ctrl/Alt 锁存修饰态**：Ctrl 只有预烘焙组合；用户自定义键（`CustomKeyModal.tsx`）可编码任意 Ctrl/Alt/Shift 组合，但仍是单次宏。**通用锁存是 Orca 未覆盖点**（iSH/Termius 有，可自设计）。
- **布局**：单行横向 ScrollView（`MobileSessionCommandDock.tsx:110-116`）：收起键盘键（键盘弹起时）→ 显示模式切换 → live/buffered 切换 → Paste → 内置键（可排序/隐藏）→ 自定义键 → "+"添加。
- **长按连发**：仅 `repeatable` 键，400ms 延迟 / 45ms 间隔，generation 计数防泄漏（`terminal-accessory-repeat.ts:24-76`）。
- **发送路径**：不经 xterm，键字节经 `terminalInputSend` RPC 直写 PTY；控制字节与 IME 滞留文本排序（`\r` 结尾序列先 flush 再发）。
- **可定制**：`TerminalShortcutSettings.tsx`——内置键可见性开关 + 拖拽排序（AsyncStorage 持久化，schema v2 带 v1 迁移与新键邻居插入合并）；自定义键三类：修饰组合 / 特殊键网格（F1-F12、Home/End/PgUp/PgDn）/ 文本宏（任意文本 + 可选 Enter）。

### 2.3 文本输入与 IME（浏览器侧无需照搬，纪律可借鉴）

原生 TextInput + 镜像 diff（`'\x7f'.repeat(n) + append` 同步 PTY）；preedit 期间不发字节；控制字节排在 flush 之后；Enter 走 `onSubmitEditing` 单独路径防双 CR；iOS 智能破折号还原（`—`→`--`）。myworktree 应继续留在 xterm 的 composition/onData 路径，仅吸收其**时序纪律**（对应浏览器 `e.isComposing` / `compositionend`）。

### 2.4 粘贴（对应缺口 ② —— 证明按钮方案是正解）

附件条 **Paste 按钮**（`use-mobile-terminal-paste.ts`）：`clipboard.readText()` 读系统剪贴板；**bracketed paste** 包装（PTY 开 `bracketedPasteMode` 且非 altScreen 时包 `\x1b[200~…\x1b[201~`），并剥离内容内嵌的 200~/201~ 防注入；粘贴前先 flush IME 滞留文本；256KiB 上限。**结论：移动端粘贴本就不该依赖系统长按气泡，按钮 + 剪贴板 API 是正解。**

### 2.5 键盘避让（比 PR2 的钉高更精细，可作增量）

终端 pane 整体上移 `translateY(-lift)` 而非 resize：`computeActiveTerminalKeyboardLift`（`terminal-keyboard-avoidance-lift.ts:14-53`）按**锚点行**（max(光标行, 内容最底行)+1 行边距）计算抬起量——altScreen 全抬，主缓冲区只抬到光标/内容底部露出为止（防过度平移把网格顶进 header）。键盘高度分平台（iOS 减 home-indicator inset）。

### 2.6 手势

`terminal-gesture-input.ts` 是 SGR 鼠标字节**白名单校验器**（触摸滚动转成鼠标上报时防任意文本伪装输入，长度 ≤2048、≤32 条序列）；WebView 文档内另有单指滚动 scrollback（惯性 fling）、双指捏合缩放字号。

---

## 三、对 myworktree 的可移植性评估

**可直接搬进浏览器**：

1. 键→字节映射表（Esc/Shift+Tab/Del/方向/Ctrl 组合）——浏览器里按键直接 `term.input(bytes)` 或 WS 发送，链路比 Orca 短两层（无 RN bridge/RPC）。
2. 长按连发参数 400ms/45ms + generation 取消模式（pointerdown/up + setTimeout 实现）。
3. Paste 按钮 + bracketed-paste 包装 + 200~/201~ 剥离。
4. 键盘避让的锚点行算法（从 xterm buffer 读 cursorY/内容底行，配合我们已有的 `visualViewport` 钉高）。
5. 手势字节白名单思想（若做触摸滚动转滚轮序列）。
6. 布局持久化 schema（排序 + 可见性 + 版本迁移）。
7. 时序纪律：组合态期间不发控制字节、Enter 单路径防双 CR。

**不可移植 / 需替换实现**：

1. 镜像 diff 输入模型整体（为绕过 WebView 输入所有权而生；我们留在 xterm composition/onData 路径）。
2. 键盘高度事件（RN `keyboardDidShow`）——浏览器用 `visualViewport`（PR2 已落地）。
3. 剪贴板图片粘贴（expo-clipboard + 宿主 RPC）——浏览器只能文本粘贴，且受安全上下文约束。
4. 触觉反馈、`Keyboard.dismiss()` 等价物（浏览器只有 `blur()`，且 iOS 无法程序化重开键盘——附件条须保留一个真实可 focus 元素）。

---

## 四、建议立项方向（编号注记：本草案原称「R17」；PRD-CHANGE 的 R17 已被 #100 beta 追加的终端触摸滚动 / 页面锁止占用，本方向立项时取届时下一可用编号）

**附件键条 + 粘贴按钮 + 键盘避让增强**，形态建议：

- 触发条件：`pointer: coarse` 且终端聚焦（软键盘弹起）时显示于键盘上方 / 终端底缘。
- 内置键最小集：Esc、Tab、Shift+Tab、四方向、⌫、Ctrl 预烘焙组合（C/D/L/Z/R/A/E/W/U）+ **通用 Ctrl 锁存**（Orca 未覆盖，iSH/Termius 式两态锁存，自行设计）。
- 发送：`term.input(bytes)`（与键盘输入同路径，经 `pty.js:324` 的 onData → WS）。
- Paste 按钮：`navigator.clipboard.readText()` + bracketed-paste 包装；**安全上下文检测**，裸 HTTP 下降级为提示（或引导 https/localhost 访问）。
- 键盘避让增量：在现有 visualViewport 钉高之上评估锚点行 translateY 算法。
- 风险与边界：方向键在 application cursor keys 模式下的序列差异（SS3 vs CSI）需要按 xterm 当前模式取值（Orca 自定义键构建器有处理，`terminal-accessory-keys.ts:84-112`）；Ctrl 锁存与既有 `handleSidebarToggleKeydown`（Ctrl+B 折叠）的交互需明确优先级（终端聚焦时锁存 Ctrl 优先）。

## 五、证据索引

- myworktree 现状：`internal/ui/static/vendor/xterm/xterm.css:61`（textarea 隐藏）；vendored `xterm.js` 的 `handlePasteEvent` / `_keyDown` / `evaluateKeyboardEvent`；`internal/ui/static/kinds/pty.js:324`（onData→WS）；`docs/CHINESE_IME_ANALYSIS.md`（v0.5.x 同模型）。
- Orca：`mobile/src/terminal/`（terminal-key-definitions.ts、terminal-accessory-{keys,layout,repeat}.ts、terminal-live-*.ts、use-mobile-terminal-paste.ts、terminal-keyboard-avoidance-lift.ts、terminal-gesture-input.ts、document/query-reply.ts:48-58）；`mobile/src/session/MobileSessionCommandDock.tsx`；`mobile/src/components/TerminalShortcutSettings.tsx`。
