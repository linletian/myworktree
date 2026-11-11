# 终端状态、Scrollback 与 Reattach 路线解析

> 状态：分析记录，未决策，不含实现承诺。
>
> 基线：`develop` = `5f4be0e`（2026-11-11）。文中仓库行号均以该提交为准；
> 后续代码变动需要重新核对。
>
> 本文记录一次围绕 issue #117 后续观察展开的路线分析：late attach 到
> 长时间运行的 fullscreen TUI 时，滚动为什么只滚 xterm 缓存、为什么不与
> 正常浏览器同步，以及“客户端 xterm 缓存 / 服务端终端模拟器 / 远端复用器”
> 几条路线的代价。只做事实归因和路线解析，不选择方案、不排期、不承诺。
>
> 相关文档：
> [TERMINAL_DETACH_REATTACH_DISCUSSION.md](./TERMINAL_DETACH_REATTACH_DISCUSSION.md)
> 已记录 PTY 生命周期与“关闭 myworktree 后能否拉回”的讨论；本文不重复其
> 进程拓扑细节，只引用结论。

---

## 0. 结论边界

本文能支持的结论只有三类：

1. **代码事实**：某个行为在 `develop 5f4be0e` 的哪段代码里成立；
2. **机制归因**：在给定代码事实下，late attach 的滚动失效可以怎样解释；
3. **路线代价**：如果引入服务端终端状态/快照/模拟器，会牵动哪些层，
   各有什么代价和未解问题。

本文**不**包含：

- “应该采用哪条路线”；
- “先做哪个阶段”的排期承诺；
- 对 API 兼容性、性能、安全、产品语义的最终取舍；
- 运行期实测结论。除特别标注外，本次没有启动 daemon/browser 做端到端验证。

---

## 1. 概念澄清：四种“历史/回滚”

讨论终端滚动时，至少有四种不同的东西会被混称为“历史”：

| 名称 | 谁拥有 | 触发方式 | 内容 | 是否发给 PTY |
|---|---|---|---|---|
| **终端 scrollback** | 终端模拟器（xterm/tmux） | 滚轮、触摸、PageUp | 已经渲染过的屏幕行 | 否（默认） |
| **服务器字节回放** | myworktree 的 ring buffer | attach / reconnect / overflow | 原始 PTY 字节 | 否（只是填充客户端） |
| **shell 命令历史** | zsh ZLE / bash readline / history 文件 | `↑`/`↓`/`history`/`Ctrl-R` | 命令文本 | 是（输入给 shell） |
| **TUI 应用内滚动** | TUI 程序自己 | 鼠标上报 / 方向键 | TUI 自己的模型：会话、列表、任务 | 是（输入给 TUI） |

关键区分：

- **zsh/bash 没有“输出 scrollback”。** shell 只保存命令历史，不保存屏幕输出。
  输出一旦写进 PTY，shell 就不知道终端把它显示成什么样了。
- **xterm 的 scrollback 不是“替代 zsh 的 scrollback”**，而是普通终端本来就
  存在的那一层。没有它，滚轮往上没有内容可显示。
- **服务器字节回放不是滚动动作**。它只在客户端还没有这些字节时，把历史灌进
  客户端；之后滚动是本地行为。
- **TUI 滚出来什么由 TUI 决定**，不是由终端的历史回放决定。终端只负责把
  wheel 编码成输入、把 TUI 回来的帧渲染出来。

因此“滚动是历史回放还是滚动交互”没有单一答案，取决于当前终端模式：

| 场景 | 滚动归属 | 滚出来什么 |
|---|---|---|
| 普通 shell / inline CLI | 终端模拟器 | xterm scrollback 中的历史行 |
| fullscreen TUI（alt screen + mouse） | TUI 程序 | TUI 自己的会话/列表/任务模型 |
| regular 模式 TUI（normal buffer、无 mouse） | 终端模拟器 | host scrollback |
| 程序主动开启鼠标上报的 shell/插件 | 程序 | 程序自己的选择 |

---

## 2. 本次观察到的现象

场景：

1. 在终端实例中启动 pi agent（`tuiMode=fullscreen`，默认）；
2. 当前浏览器内，TUI 滚动正常：只滚会话区域，输入框固定在底部，
   能滚出会话前后内容；
3. 在另一个浏览器中重新加载页面后：
   - 滚轮滚的是整块终端画布（包括 TUI 输入框），输入框被滚走；
   - 滚出来的旧内容不是连续的会话内容，更像 xterm 缓存的旧帧；
   - 两个浏览器同时打开时，正常浏览器的 TUI 滚动会同步到异常浏览器；
     异常浏览器的滚动不会同步回正常浏览器。

这些现象可以用“异常浏览器的 xterm 本地状态与 TUI 不一致”解释，而不是
“#149 的滚动路由判断写错了”。

---

## 3. 代码事实与归因

### 3.1 pi 的终端模式序列只在启动时发一次

已安装的 `@earendil-works/pi-coding-agent@1.5.0` 中：

- `TuiAltScreen.beforeTerminalStart()` 写入：
  - `ENTER_ALT_SCREEN = "\x1B[?1049h"`
  - `DISABLE_AUTOWRAP = "\x1B[?7l"`
  - `ENABLE_ALL_MOTION_MOUSE = "\x1B[?1000h\x1B[?1002h\x1B[?1003h\x1B[?1004h\x1B[?1006h"`
- `beforeTerminalStart()` 由 `start()` 调用一次：
  `this.beforeTerminalStart(); this.terminal.start(...)`；
- resize/重绘走 `requestRender()`，不会重新发送上述模式序列；
- `1003h` 在 xterm 中对应 `mouseTrackingMode = "any"`；
- pi 的 `TuiAltScreen` 使用 `previousScreen` 做差分渲染，主要发送变化行，
  在尺寸变化等条件下才全量重绘。

也就是说，只有“页面在 pi 启动前/启动瞬间已经连着”的浏览器能看到这些
模式序列。

### 3.2 首次 attach 只回放最新 64KB

服务端：

- `GET /api/instances/log`：`Tail(id, 64*1024)` 或 `ReadSince(id, since, 64*1024)`
  （`internal/app/app.go:4536`、`:4558`、`:4568`）；
- SSE `GET /api/instances/log/stream`：tail/ReadSince 同样 `64*1024`
  （`:4578`、`:4615`、`:4617`）；
- WS 首次连接、无 `since`：`Tail(id, 64*1024)`（`:3624`）；
- WS 的 catch-up replay 上限是 8MB（`:231`），默认 replay window 1MB
  （`:2845`）；这些只对“带 `since` 的重连”生效，首次 attach 不走大窗口。

客户端：

- `loadLog()`（`internal/ui/static/index.html:9021`）GET 该 64KB；
- 成功后把 `logCursor` / `ttyOffset` 都钉到 `next`（`:9105-9106`）；
- `connectTTY()`（`:9305`）随后用 `since=<该 head>` 连 WS（`:9406`），
  所以 WS 不会再补任何旧字节。

结论：新 xterm 从默认状态开始，只看到最近 64KB 的原始 PTY 输出。只要 pi
的启动序列已经滚出这 64KB，就不会被解析。

`docs/ARCHITECTURE.md:233` 把首次连接的 tail 描述为“replays the screen state
itself”；但从“终端状态”的角度，tail 只是字节后缀，不是状态快照。

### 3.3 xterm 的滚动路径由本地模式决定

vendored xterm（v6，`internal/ui/static/vendor/xterm/xterm.js`；源码可从
`xterm.js.map` 读取）：

- `Viewport` 构造时 `handleMouseWheel` 初值为 true；
- 只有 `coreMouseService.onProtocolChange` 报告当前协议包含 WHEEL 事件时，
  才把 `handleMouseWheel` 设为 false；
- 没有鼠标协议时，`ScrollableElement` 自己消费 wheel，滚动虚拟 `scrollTop`
  → `Viewport._handleScroll` → 修改 buffer `ydisp`；
- 滚动发生后它会 `preventDefault()` + `stopPropagation()`；
- 在 normal buffer 且无鼠标协议时，wheel 走 host scrollback；
- 在 alternate buffer 且无鼠标协议时，xterm 有“把 wheel 转成 Up/Down”的
  fallback。

`#149` 的触摸路由（`internal/ui/static/index.html:4652-4669`）读取同一组本地
状态：

```js
const mouseWheelTracking = (term) => {
  const mode = term.modes.mouseTrackingMode;
  return mode === "vt200" || mode === "drag" || mode === "any";
};
const touchScrollRoute = (term) => {
  if (mouseWheelTracking(term)) return "wheel";
  const bufferType = term.buffer.active.type;
  return bufferType === "alternate" ? "none" : "host";
};
```

### 3.4 对本次现象的最小归因

异常浏览器（后加载页面）：

- 没有收到 pi 启动时的 `1049h` → `buffer.active.type = "normal"`；
- 没有收到 `1003h` → `modes.mouseTrackingMode = "none"`；
- 因此触摸路由落到 `"host"`，桌面滚轮走 xterm 的 host scrollback；
- 滚的是回放进去的原始字节（旧帧/控制序列），不是 pi 的 ScrollView；
- 滚轮/触摸没有变成 PTY 输入，所以不会改变 pi 的状态，也不会同步给其他浏览器。

正常浏览器（pi 启动时已连接）：

- 解析过 `1049h` → `alternate`；
- 解析过 `1003h` → `any`；
- wheel 通过 xterm 的 SGR 鼠标上报进入 `term.onData` → PTY → pi；
- pi 修改自己的 ScrollView 并重绘，重绘字节广播给所有客户端；
- 所以正常浏览器的滚动会同步到异常浏览器，反向不同步。

这不是 `#149` 引入的回归：#149 的路由逻辑在“本地模式可信”的前提下是
正确的；late attach 让这个前提失效。修复前两个浏览器都会滚 host scrollback；
#149 让“知道模式”的浏览器变对了，但异常浏览器的信息本来就是错的。

同一根因还影响其他“清屏 + tail 重连”的路径：

- 页面 reload；
- issue #114 的 `resetTTYStreamAndReconnect()`
  （`internal/ui/static/index.html:8419`）先 `term.reset()`，再以
  `CURSOR_UNKNOWN` tail 重连；
- 任何 `resetTerminalForSwitch()`（`:8692`）之后重新回放的场景。

### 3.5 “不保证完整屏幕”指什么

P0（只补模式 preamble）恢复的是**模式位**，不是**屏幕内容和历史**。

“完整屏幕”至少包含：

- 当前每个 cell 的字符和属性；
- 光标位置、保存的光标；
- 当前 buffer（normal/alternate）；
- 滚动区域、origin mode、字符集、当前 SGR pen；
- scrollback 历史；
- 与这些内容对应的屏幕几何（cols/rows）。

tail 不能保证这些，因为：

1. **tail 不是从帧边界开始**：64KB 是字节数，起点可能落在 ANSI 转义序列、
   UTF-8 多字节字符或一帧全屏重绘的中间；
2. **差分渲染需要基线**：pi 的 `previousScreen` 只在 pi 进程里；新客户端只有
   tail，没有基线，diff 无法正确落位；
3. **屏幕几何可能不一致**：tail 是在 PTY 当时的 cols/rows 下产生的，新
   xterm 的尺寸可能不同，换行/CUP 会错位；
4. **备用屏的底是空的**：恢复 `1049h` 后 alt screen 是空白，只有 tail 触碰
   过的行才有内容；
5. **scrollback 不是屏幕**：tail 之外的历史没有；普通 shell 的 scrollback
   仍然只有 64KB；
6. **其他状态仍是默认值**：保存光标、滚动区域、origin mode、字符集、
   SGR pen、同步输出中间态等，P0 通常不追踪。

因此 P0 的定位是“交互路由修复”，不是“显示保真修复”。它让滚轮重新属于
TUI，但不保证 TUI 回来的下一帧能完整落屏。

---

## 4. 是否所有 TUI 都有类似问题

不是所有 TUI 都必然出现“滚轮失效”，但所有**依赖启动时一次性终端状态、
且初始化序列已经滚出回放窗口**的 TUI 都会遇到同类状态缺失。

| 类型 | 典型例子 | late attach 后的表现 |
|---|---|---|
| A. 备用屏 + 鼠标上报 + 应用自己滚 | pi `fullscreen`、opencode、vim/htop/less 开 mouse | 丢失 `1049h` → normal buffer；丢失鼠标模式 → 滚轮滚 host scrollback |
| B. 备用屏，但不用鼠标 | 某些全屏 TUI、未开 mouse 的 vim | 丢失 `1049h` → normal buffer；wheel 走 host scrollback；触摸路由可能落到 `"host"` 而不是 `"none"` |
| C. normal buffer + 不用鼠标 + 依赖终端 scrollback | 普通 shell、`mcode/kimi` regular 模式、简单行式 CLI | 滚动本身基本正常（host scrollback 是设计路径）；但 bracketed paste、autowrap、cursor、keypad 等状态可能丢；历史只有 64KB |
| D. 每帧/resize/focus 重发模式 | 少数实现 | 可能不受影响或在下一次重绘时自愈；需要逐个验证 |
| E. 运行在 tmux/screen 里 | — | 外层 xterm 仍只看到 tail；内层复用器能重绘自己的屏幕，但是否替外层重发模式取决于实现 |

所以更准确的说法是：

> 这不是“所有 TUI 都坏”，而是“字节 tail 回放模型对所有有状态 TUI 都不完整”；
> 是否表现为滚动失效，取决于该 TUI 是否把滚动交给应用自己、是否重发模式。

---

## 5. 与其他系统的对照

### 5.1 裸 SSH

- SSH 每次连接新建 PTY；连接断开时远端 `sshd` 关闭 PTY，前台进程组通常收到
  `SIGHUP`。
- 即使进程靠 `nohup` / `disown` / `setsid` 活下来，它的 PTY 也已经没了，
  新 SSH 连接无法“接回”那个 TUI。
- `ControlMaster` / `ControlPersist` 复用 SSH 传输连接，但每次 `ssh host`
  仍是新的 session/PTY，不能恢复旧 TUI。
- 结论：裸 SSH 本身不能接续 TUI。

### 5.2 tmux / screen / zellij

- 复用器持有 PTY 和屏幕模型；
- TUI 进程挂在复用器的 PTY 上，不直接挂 SSH 的 PTY；
- 新客户端 attach 时，复用器重发当前屏幕、光标、颜色、备用屏、鼠标模式、
  bracketed paste 等；
- scrollback 也由复用器拥有（copy-mode）。

这是 SSH 场景最通用的方案，也是 Termius 官方 FAQ 给出的方式。

### 5.3 Mosh

- `mosh-server` 在远端维护终端模拟器；
- 客户端断开、换 IP、休眠后恢复，重新同步的是**当前可见屏幕状态**；
- 支持漫游和间歇连接；
- 官方 FAQ 明确说明：Mosh 只同步可见状态，scrollback 不完整，workaround 是
  screen/tmux。

### 5.4 Orca（`stablyai/orca`）

Orca 是 MIT 开源（TypeScript/Electron），其终端模型：

- 后台 daemon 持有 PTY；关闭 Orca 窗口、Orca 崩溃、自动更新重启，
  agent 进程继续跑（`docs/site/content/docs/model/session-restore.mdx`）；
- daemon 内为每个 PTY 维护 headless 终端模拟器
  （`@xterm/headless` + `@xterm/addon-serialize`，见 `package.json`）；
- reattach 时发送 `TerminalSnapshot`
  （`src/main/daemon/protocol/terminal-snapshot.ts`），包含：
  - `snapshotAnsi`（当前屏幕）；
  - `scrollbackAnsi`（normal buffer 历史，和 alt frame 分开）；
  - `pendingEscapeTailAnsi`（被切断的转义尾巴）；
  - `rehydrateSequences`、`frameRestoreAnsi`；
  - `modes`、`cols/rows`、`cwd`、`lastTitle`、`outputSequence`、
    `terminalOwner`；
- 客户端 replay 编排
  （`src/renderer/src/components/terminal-pane/pty-connection/reattach/apply-reattach-payload.ts`）：
  prologue → 快照 body → epilogue → pending escape tail → 等解析 → live；
- 模式恢复/清理按场景区分
  （`src/shared/terminal/protocol/input-modes/terminal-mode-reset-profiles.ts`）：
  - live alt-screen TUI：保留鼠标模式，否则拖拽变选中（#8291）；
  - dead TUI / 普通 shell：清理残留鼠标模式（#7329/#7893）；
  - cold restore 新 shell：重置 mouse/focus/bracketed paste/kitty keyboard/
    modifyOtherKeys 等；
- 鼠标编码单独跟踪
  （`src/renderer/src/lib/pane-manager/terminal-protocol/terminal-mouse-encoding-tracker.ts`），
  因为 SerializeAddon 不保存 `1006/1016` 编码；
- 有对应 e2e：
  - `tests/e2e/terminal-reattach-tui-mouse-mode.spec.ts`；
  - `tests/e2e/terminal-reattach-mouse-mode-leak.spec.ts`。

Orca 的核心不是“更大的字节回放窗口”，而是“服务端持有状态 + attach 时发
状态快照”。它的 snapshot 是“当前状态”，不是“重播全部历史帧”；scrollback
只对 normal buffer 的 shell 历史有意义。

### 5.5 Termius

- Termius 客户端本身**闭源**（商业 SSH 客户端）；终端引擎、同步、连接管理
  不公开。
- GitHub 上的 `termius/termius-cli` 是一个独立的历史 CLI 工具
  （API 报 `NOASSERTION`，源码带 BSD 风格许可），不是 Termius 终端源码。
- 从公开文档可确认：
  - 官方 FAQ 给出的做法是使用 **screen 或 tmux** 保留 SSH 断线后的终端会话；
  - 移动端后台断开受 OS 限制，Termius 无法保持 SSH 连接；
  - Termius 支持 **Mosh**（自家兼容库，要求 Mosh 1.3.0+），把漫游/断线恢复
    交给 Mosh；Mosh 只同步可见屏幕，不保留完整 scrollback；
  - Session logs 是“录制/回放日志”，不是可接续的 live PTY；Mosh 会话不被
    记录。
- 因此 Termius 自身不做 PTY 持久化/状态快照，依赖远端 tmux/screen 或 Mosh。

### 5.6 对照表

| 方案 | 能否接回 TUI | 历史 scrollback | 原理 |
|---|---|---|---|
| 裸 SSH / `nohup` | 否 | 否 | PTY 随连接销毁 |
| tmux / screen / zellij | 是 | 是 | 复用器持有 PTY + 屏幕模型，attach 时重绘 |
| Mosh | 是（可见屏幕） | 不完整 | 远端终端模拟器同步当前屏幕 |
| Orca daemon + snapshot | 是 | 是 | daemon 持有 PTY + headless xterm 快照 + 模式重放 |
| 单纯字节 tail 回放 | 对全屏 TUI 不可靠 | 只有 tail | 不是状态快照 |

---

## 6. 参考 Orca 模式的整体影响评估

### 6.1 先把 Orca 模式拆开

| 编号 | 能力 | 解决什么 |
|---|---|---|
| O1 | 独立 daemon 持有 PTY，UI 退出/崩溃/重启后 agent 继续跑 | 服务端重启后还能接回 |
| O2 | 每个 PTY 一个 headless 终端模拟器 | 持续维护屏幕、光标、模式、scrollback |
| O3 | attach 时发终端快照（屏幕 ANSI + scrollback + modes + seq + pending escape），再从 seq 接 live | 替代“64KB 字节 tail” |
| O4 | 重放编排 + owner 判定：live TUI 保留 mouse mode；死 TUI / shell 清理残留模式 | 避免拖拽变选中，或 mode 泄漏给 shell |
| O5 | scrollback/session 持久化，daemon 死后也能恢复布局和历史 | App/机器重启后的恢复 |

当前 issue 的直接现象只需要 O2+O3+O4 的一部分；O1/O5 是产品级能力。
本文只解析代价，不判断是否需要。

### 6.2 进程与生命周期（O1/O5）

当前项目：

- `mw` 进程自己就是 daemon；PTY 是它的子进程；
- server 重启时 PTY 随进程组结束；
- `RestartSurvivor` 只有 reasonix 实现；PTY 明确不实现
  （`internal/framework/kind.go:356-361`）；
- `ReconcileRunningOnStartup`（`internal/framework/methods.go:241`）在启动时
  把没有 `RestartSurvivor` 的 running 记录标记为 stopped；
- `docs/ARCHITECTURE.md:91-104` 明确：PTY 输出只在内存 ring，不在磁盘；
  daemon 重启后不能恢复。

O1/O5 需要：

- 一个和 UI/控制进程分离的 PTY supervisor/daemon；
- PTY、进程组、scrollback、session 元数据跨 app 重启存活；
- 崩溃恢复、PID 所有权、孤儿清理、升级、跨平台、权限、持久化格式。

这是进程模型级改动，不是终端模块改动；工作量与风险都远大于 O2/O3。

### 6.3 输出热路径（O2）

当前 `pumpLogs`（`internal/instance/pty/driver.go`）：

```text
PTY Read → redact.Text → RingBuffer.WriteString → broadcast
```

增加 O2 后：

```text
PTY Read → redact.Text → RingBuffer.WriteString → HeadlessEmulator.Write → broadcast
```

影响：

- **CPU**：每个字节多一次 VT 解析；TUI 全屏重绘时输出量大；
- **内存**：每个 PTY 一个屏幕 + scrollback（例如 1 万行）；相比 16-256MB
  ring 不一定更大，但实例多时线性增长；
- **顺序**：snapshot 的 `seq` 必须和 ring offset 对齐，否则 snapshot 与
  live replay 会重叠或缺口；
- **redaction 一致性**：emulator 必须消费 redact 后的字节，否则 snapshot
  可能泄漏 ring 已脱敏的内容；同时要验证 redact 不会改坏转义序列；
- **替代思路**：不做热路径 emulator，而是在 attach 时把 ring 从 oldest
  开始重放给客户端 xterm（见 §7.2），让客户端自己重建状态。

### 6.4 状态层（O2/O3）

候选结构（仅结构分析，不是决定）：

- 新增独立包，例如 `internal/terminalstate`；
- 包装 Go VT 模拟器；
- 维护 screen/scrollback/cursor/modes/alt-screen；
- 提供 `Snapshot()`（ANSI + modes + seq + pending tail）；
- 框架增加可选接口，例如：

```go
type SnapshotProvider interface {
    Snapshot(id string, opts SnapshotOptions) (Snapshot, error)
}
```

`framework.Manager` 增加 `Snapshot(id)`；`kind.go` 不改 `ReadLogs` 契约。
PTY kind 实现 snapshot；opencode-web/reasonix/dsh-web 不受影响；旧路径
（Tail/ReadSince/SSE）保留。

### 6.5 客户端重放（O3/O4）

当前 `loadLog()` 是“GET 64KB → 写入 → 把 cursor 钉到 head”。改成 snapshot
后需要：

1. 清屏/重置（prologue）；
2. 写 snapshot（可能需要按 capture geometry resize）；
3. 写 epilogue（恢复/清理模式）；
4. 等 xterm 解析完（write callback / parse barrier）；
5. 再接 live。

涉及 `internal/ui/static/index.html` 的 `loadLog`、`connectTTY`、
`parseTTYControlMessage`、`resetTerminalForSwitch`、
`resetTTYStreamAndReconnect`，以及 `kinds/pty.js` 的 session factory /
activate。

还要处理：

- snapshot 写入期间 live 字节不能插队；
- `term.write` 是异步队列，必须等解析完再显示 live；
- replay 期间的 resize 不能触发 SIGWINCH 循环。

### 6.6 多客户端与 resize

当前服务端 resize 是“多 viewer 取 min”，客户端有 `queueTTYResize` /
`applyTTYSize` / `sendResize` 的 ack 状态机（issue #116 刚修过）。

snapshot 引入的新问题：

- snapshot 是在 capture 时的 cols/rows 下生成的；
- 新浏览器可能尺寸不同；
- 直接按新尺寸解析会导致换行/光标/全屏帧错位；
- 需要“按 snapshot geometry replay，再 fit 到当前窗口”，并抑制 replay
  期间的 resize 转发。

如果改成“服务端拥有 scrollback”，还会多一个产品问题：

- **共享 viewport**：所有客户端滚同一个位置（tmux 式）；
- **每客户端 viewport**：各自滚各自的位置，服务端要为每个客户端保存 offset
  并分别渲染。

TUI 场景天然共享（app 的 ScrollView 只有一份）；普通 shell 场景两种都
可能，取决于期望的产品行为，本文不选择。

### 6.7 其他 kinds

- PTY（`tty`）是唯一需要终端 snapshot 的 kind；
- `opencode-web` / `reasonix` / `dsh-web` 是 iframe/Web UI，不走 xterm；
- 如果未来加 remote PTY/SSH host，snapshot 协议可以复用，但当前不需要。

### 6.8 持久化与安全（O5）

当前产品明确宣传“Output replay without disk writes”，
`docs/ARCHITECTURE.md:102-104` 把“无磁盘日志”作为设计原则。O5 会改变：

- secrets 可能落盘（即使 redact，也有残留风险）；
- 磁盘配额、保留策略、清理、迁移；
- 启动时恢复/损坏处理；
- 备份/隐私合规。

这是产品级取舍，不是终端模块能单独决定的。

---

## 7. 现有 API/协议的影响面

### 7.1 现有契约

当前 TTY API 已经有一整套语义：

- `GET /api/instances/log?id=&since=`
  - `since` 省略 → 64KB tail
  - `since=-2` → 400（sentinel 只属于 stream）
  - `since>=0` → `ReadSince`
- `GET /api/instances/log/stream`（SSE）
- `WS /api/instances/tty/ws?id=&since=&caps=`
  - `since` 省略 / `-1` → 64KB tail
  - `since=-2` → follow live end，不重放
  - `since=0` → 从 oldest live 开始，受 W_eff 前端 clamp 和 8MB budget 限制
  - `since>0` → catch-up，最多 8MB，start/closing `sync` 帧
  - `caps`: `ping,sync,overflow-notice,compress,replay-window,slow-link`
  - 压缩：deflate-raw，按帧
  - 溢出：`overflow` notice + reset + tail reconnect

这些被以下测试/文档钉住：

- `docs/API.md:534+`；
- `internal/app/tty_ws_test.go`（4300+ 行）；
- `internal/ui/testdata/terminal_status.test.mjs`（2700+ 行）；
- `internal/ui/testdata/tty_overflow_reconnect.test.mjs`（1200+ 行）；
- `docs/ARCHITECTURE.md:116-118`（cursor/WS handshake）。

因此 snapshot 只能 additive，不能替换 `since`/`sync`/overflow 模型。

### 7.2 可选的 additive 形态（只列结构，不选择）

#### WS 首次 attach

新增 cap，例如 `caps=snapshot`。服务端在 handshake 时原子捕获：

```json
{"type":"snapshot","seq":123456,"cols":120,"rows":40,
 "alternateScreen":true,"scrollbackRows":10000,
 "modes":{"mouseTrackingMode":"any","bracketedPaste":true,
          "applicationCursorKeys":false},
 "compressed":true}
```

然后：

1. 发送 snapshot body（二进制，可压缩）；
2. 从 `seq` 开始 replay live backlog；
3. 发 `sync`（复用现有帧）；
4. 进入 live。

客户端收到 snapshot 后清屏/重建，设置 `ttyOffset = seq`；后续 reconnect
仍然用 `since`，不变。旧客户端不声明 `snapshot`，继续走 64KB tail。

#### GET / SSE

候选：

- A：新增 `?snapshot=1`，返回 ANSI + metadata；SSE 首帧带 snapshot；
- B：live 实例首次 attach 只走 WS snapshot；WS 失败退回旧 tail/SSE；
- C：GET 保留给已停止实例的 tail，live 实例不再用 GET 首绘。

#### Cursor / seq

- snapshot 的 `seq` 是 ring head；
- 客户端不再需要 `X-Log-Offset` + “钉到 head”的旧逻辑；
- `-1` / `-2` sentinel 保留给旧客户端；
- 用 `caps`/query 表达“我要 snapshot”，避免再增加负数状态。

#### 压缩 / overflow / resize

- snapshot 可能几十 KB 到几 MB；应复用 `compress` 或单独声明压缩；
- overflow 当前是 reset + tail reconnect；有 snapshot 后可以改为“重连请求
  新 snapshot”，或保留旧逻辑作为 fallback；
- snapshot 的 cols/rows 与 `resize`、`appliedTTYSize`、min-across-viewers
  的关系需要明确，并避免 replay resize 触发 SIGWINCH 循环。

#### MCP / 外部 API

- `instance_log_tail` 继续返回 tail；
- 可以新增 `instance_snapshot` MCP 工具，但不是必需；
- `docs/API.md` 需要新增 snapshot cap/帧结构/seq 语义/降级行为。

### 7.3 兼容性

- 同一 binary 服务 UI，版本偏差主要来自缓存页面/远程旧页面；
- 项目已有 `caps` 协商传统，additive cap 是最稳的；
- 旧客户端、旧 MCP、旧文档行为需要保持；
- snapshot 默认关闭、通过 cap 开启，是候选的灰度方式（不是决定）。

---

## 8. 路线候选项（只解析，不选择）

### 8.1 P0：模式 preamble

服务端维护少量 DEC 模式状态（例如 `1049/1000/1002/1003/1006/7/25/2004/2026`），
在 attach 时于 tail 前补一段合成 `ESC[?...h/l`。

- **能保证**：鼠标模式、备用屏、autowrap、cursor、bracketed paste 等
  关键模式位；滚轮/触摸重新归属 TUI。
- **不能保证**：完整屏幕、scrollback、光标/滚动区域/charset/SGR pen、
  pending escape、屏幕几何。
- **代价**：需要能跨 chunk 边界的小型增量解析器；`1049` 的进入/退出语义；
  所有“清屏 + tail 重放”路径都要覆盖。
- **风险**：恢复 alt screen 后，tail 只有 diff 时画面可能比现在更空；
  是否需要“强制全量重绘”的辅助手段，需要单独评估。

### 8.2 P1：全量 ring 重放

首次 attach 不用 64KB tail，而是从 ring 的 oldest live 开始按顺序重放，
让客户端 xterm 自己重建屏幕、模式、光标、scrollback。

- **能保证**：只要 ring 仍保留从启动到现在的完整流，屏幕/模式/历史是完整的；
- **不能保证**：ring 回绕后启动序列被淘汰；超过 replay 预算的会话；attach
  延迟/带宽；xterm 自身 scrollback 上限（当前 10000 行）之外的更早行仍会被
  客户端丢弃；
- **注意**：它重建的是终端状态，不是 TUI 的内部 model；TUI 的会话/列表内容
  仍由 TUI 进程决定；
- **实现面**：可利用现有 `ReadSince`/GET cursor 分页，或扩大 WS replay
  window；不需要 Go VT emulator；
- **代价**：大 ring（16-256MB）全量重放可能慢；TUI 输出是字节流，快照可能
  比全量回放小得多。

### 8.3 P2：headless emulator + snapshot

服务端维护终端模拟器，attach 时发当前屏幕 + scrollback + modes + seq +
pending escape；客户端按 replay 编排重建，再从 seq 接 live。

- **能保证**：屏幕/模式/光标/scrollback 的状态一致；ring 回绕也不影响
  emulator 的当前状态；
- **不能保证**：TUI 的内部 model（那始终在 TUI 进程里）；emulator 与 xterm
  的渲染差异；
- **代价**：Go VT 依赖/自研；热路径 CPU；snapshot 序列化；协议/UI/测试；
- **依赖问题**：Orca 用 `@xterm/headless` 是 JS，不符合单 Go binary；
  需要评估 Go VT 库的 alt-screen/mouse/scrollback/serializer 成熟度。

### 8.4 P3：owner-aware 模式恢复/清理

判断当前前台是 live TUI、死 TUI 还是 shell，再决定 snapshot/replay 后保留
还是清理 mouse/alt/focus/bracketed paste 等模式。

- **为什么需要**：TUI 崩溃但 shell 还在时，残留模式可能被 snapshot 恢复；
- **现状**：当前仓库没有 OSC 133 shell integration（grep 未发现）；
- **候选**：OSC 133、foreground process group 探测、shell 退出时 reset；
- **代价**：需要 shell 配合或平台相关探测；测试矩阵增加。

### 8.5 P4：独立 daemon + 持久化

- PTY 脱离 server；
- scrollback/session 落盘；
- 崩溃恢复、升级、清理、安全。

这是产品级架构变更，工作量远大于 P0-P3；本文只记录，不判断。

### 8.6 服务端模拟器的三种产品形态

| 形态 | 状态权威 | scrollback 归属 | 客户端角色 | 改动量 |
|---|---|---|---|---|
| H1：Hybrid snapshot | 服务端 emulator（attach/模式） | xterm（普通 shell） | 终端渲染 + 本地滚 | 中 |
| H2：Server-owned scrollback | 服务端 emulator | 服务端 | 终端渲染 + 输入 | 中高 |
| H3：Full thin client | 服务端 | 服务端 | 纯 frame 渲染 | 很高 |

- **H1**：服务端模拟器只用于 attach 快照/模式；客户端 xterm 继续渲染和本地
  scrollback；不改变滚动延迟、选择、IME。
- **H2**：客户端把 wheel/scroll 发给服务端；服务端移动 viewport 并发回
  ANSI viewport；客户端 `scrollback: 0`；解决 64KB 历史和一致性，但引入
  RTT、per-client viewport、选择/搜索 API。
- **H3**：服务端发 cell/frame；客户端只画；选择/搜索/IME/链接全部服务端化；
  接近重写 UI 层。

### 8.7 各路线“保证/不保证”对照

| 路线 | 滚动归属正确 | 当前屏幕完整 | scrollback 完整 | 需要 Go VT emulator | 需要协议变更 | 需要进程模型变更 |
|---|---|---|---|---|---|---|
| P0 模式 preamble | 是 | 否（best-effort） | 否 | 否 | 小 | 否 |
| P1 全量 ring 重放 | 是 | 是（ring 未回绕时） | 是（同左，受 xterm scrollback 上限） | 否 | 小-中 | 否 |
| P2 emulator + snapshot | 是 | 是 | 是（受 cap） | 是 | 中 | 否 |
| P3 owner-aware | 是 | 取决于 P2/P1 | 取决于 P2/P1 | 视情况 | 小 | 否 |
| H2 服务端 scrollback | 是 | 是 | 是 | 是 | 中-大 | 否 |
| H3 thin client | 是 | 是 | 是 | 是 | 大 | 否 |
| P4 daemon + 持久化 | 是 | 是 | 是 | 是 | 中-大 | 是 |

---

## 9. 关键难点与风险（汇总）

1. **snapshot 与 live 的一致性**：捕获 `seq`、序列化快照、同时输出继续；
   需要 “snapshot at seq + live backlog from seq”，不能简单锁住 `pumpLogs`。
2. **pending escape / UTF-8 tail**：PTY chunk 可能在序列中间结束；snapshot
   必须带 pending tail，并在 reset 后最后写。
3. **normal/alt screen 分离**：全屏 TUI 的 scrollback 在 normal buffer；
   snapshot 需要把 normal scrollback 和 alt frame 分开。
4. **owner 判定**：live TUI vs 死 TUI vs shell；没有 OSC 133 时需要额外探测。
5. **多客户端 resize/reflow**：snapshot geometry vs viewer geometry；
   min-across-viewers；抑制 replay resize。
6. **性能与 DoS**：snapshot 生成/序列化成本；重复 attach；大 scrollback；
   需要预算/缓存/限流。
7. **Go 依赖**：Orca 的 `@xterm/headless` 是 JS，不符合单 Go binary；
   Go VT 库的兼容性/维护性是主要不确定性。
8. **测试面**：`tty_ws_test.go`（4300+ 行）、`terminal_status.test.mjs`
   （2700+ 行）、`tty_overflow_reconnect.test.mjs`（1200+ 行）；任何
   snapshot 改动都要重新验证 #81/#86/#87/#94/#114 的 cursor/duplicate/
   overflow 语义。
9. **选择/复制/搜索/链接/IME**：如果 scrollback 只在后端，这些能力需要
   服务端 API 或客户端只读 buffer；会改变交互模型。
10. **滚动延迟**：本地 xterm 滚动是 60fps 无网络；服务端滚动有 RTT；
    需要合并/限帧，或乐观渲染（又引入状态）。

---

## 10. 待决问题（本文不决策）

1. 验收标准是“滚动归属正确”还是“首帧显示与 TUI 完全一致”？
2. 普通 shell 的历史深度目标：64KB / 1MB / 8MB / 全 ring / snapshot？
3. 是否需要跨 server 重启接回 PTY（O1/O5），还是只解决浏览器 late attach？
4. 服务端 scrollback 是共享 viewport 还是每客户端独立？
5. 走 P1（全量重放）还是 P2（emulator + snapshot）？
6. 如果 P2，Go VT 库选型/自研范围？是否接受新增依赖？
7. 是否引入 OSC 133 或 foreground process group 探测做 owner 判定？
8. snapshot 协议用 WS control frame、GET 参数还是新端点？
9. 旧客户端的降级路径和 caps 协商策略？
10. 是否接受磁盘持久化（O5）及其安全/隐私影响？
11. 性能预算：每实例 CPU/内存、attach 延迟、snapshot 大小上限？
12. 测试策略：snapshot/xterm parity 用 golden、fuzz 还是 e2e？

---

## 11. 参考资料

### 本仓库

- `internal/app/app.go:231` — 8MB handshake replay budget；
- `internal/app/app.go:2845` — 1MB default replay window；
- `internal/app/app.go:3168` — TTY WS handler；
- `internal/app/app.go:3624` — first-connect tail 64KB；
- `internal/app/app.go:3938` — zero-progress tail 64KB；
- `internal/app/app.go:4536` — `handleInstanceLog`；
- `internal/app/app.go:4558`、`:4568`、`:4615`、`:4617` — `ReadSince`/`Tail`
  64KB；
- `internal/ui/static/index.html:4652-4669` — #149 touch route；
- `internal/ui/static/index.html:7892` — client `scrollback: 10000`；
- `internal/ui/static/index.html:8419` — overflow reset + tail reconnect；
- `internal/ui/static/index.html:8692` — `resetTerminalForSwitch`；
- `internal/ui/static/index.html:8959` — `parseTTYControlMessage`；
- `internal/ui/static/index.html:9021` — `loadLog`；
- `internal/ui/static/index.html:9105-9106` — cursor pin；
- `internal/ui/static/index.html:9305` — `connectTTY`；
- `internal/ui/static/index.html:9406` — WS `since`；
- `internal/ui/static/kinds/pty.js:181`、`:209`、`:315`、`:321` —
  activate/reset/loadLog/new Terminal/scrollback；
- `internal/framework/kind.go:356-361` — `RestartSurvivor` 与 PTY 例外；
- `internal/framework/methods.go:241` — `ReconcileRunningOnStartup`；
- `internal/framework/memsampler.go:14-17` — ring cap 16/32/256MB；
- `docs/ARCHITECTURE.md:91-104` — in-memory ring / no disk / restart 语义；
- `docs/ARCHITECTURE.md:116-118` — cursor/WS handshake；
- `docs/ARCHITECTURE.md:233` — first-connect tail 被描述为 screen state；
- `docs/ARCHITECTURE.md:463` — redaction；
- `docs/API.md:534+` — TTY WS `since`/`caps`/`sync`；
- `internal/ui/static/vendor/xterm/xterm.js` + `xterm.js.map` — vendored
  xterm v6 的 `CoreBrowserTerminal.bindMouse` / `Viewport`；
- `CHANGELOG.md` issue #117 条目。

### 外部

- Orca: <https://github.com/stablyai/orca>（MIT）
  - `docs/site/content/docs/model/session-restore.mdx`
  - `src/main/daemon/protocol/terminal-snapshot.ts`
  - `src/main/daemon/emulation/terminal-snapshot-ansi-buffers.ts`
  - `src/renderer/src/components/terminal-pane/pty-connection/reattach/apply-reattach-payload.ts`
  - `src/shared/terminal/protocol/input-modes/terminal-mode-reset-profiles.ts`
  - `src/renderer/src/lib/pane-manager/terminal-protocol/terminal-mouse-encoding-tracker.ts`
  - `src/shared/terminal/replay/terminal-serialize-absolute-cursor.ts`
  - `tests/e2e/terminal-reattach-tui-mouse-mode.spec.ts`
  - `tests/e2e/terminal-reattach-mouse-mode-leak.spec.ts`
- Termius docs:
  - <https://docs.termius.com/organize-and-connect-to-hosts/connecting-to-a-server>
  - <https://docs.termius.com/help-center/faq/how-can-i-keep-termius-sessions-alive-in-the-background-on-ios-ipados>
  - <https://docs.termius.com/organize-and-connect-to-hosts/session-logs>
- `termius/termius-cli`（独立 CLI，不是 Termius 客户端源码）：
  <https://github.com/termius/termius-cli>
- Mosh FAQ（scrollback 只同步可见状态）：
  <https://mosh.org/#faq>
- tmux / screen / zellij：远端复用器，持有 PTY 与屏幕状态；
- pi TUI：`@earendil-works/pi-coding-agent@1.5.0` 的
  `TuiAltScreen.beforeTerminalStart()` / `start()` 行为（minified bundle，
  引用函数/常量名而非打包文件名）。

---

## 12. 复现/验证提示（非执行记录）

以下检查可用于将来确认本文的机制归因；本次未执行：

1. 在异常浏览器 console 读取：
   `window.__ptyRenderer.allSessions()` 中每个 session 的
   `term.buffer.active.type`、`term.modes.mouseTrackingMode`、
   `baseY`/`viewportY`；
   预期异常侧为 `normal` / `none`，正常侧为 `alternate` / `any`。
2. 读取 `GET /api/instances/log?id=<id>` 的 body，检查是否包含
   `\x1b[?1049h` / `\x1b[?1000h` / `\x1b[?1006h`；预期 64KB tail 中通常
   不包含启动序列。
3. 在 pi 启动后立刻开第二个浏览器（累计输出 <64KB）与输出超过 64KB 后
   再开，比较行为差异；
4. 在异常浏览器临时写入鼠标模式（仅诊断）：
   `term.write('\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h')`，
   观察滚轮是否重新到达 pi；刷新页面可撤销。

这些检查只用于验证“模式状态缺失”的归因，不构成修复方案。
