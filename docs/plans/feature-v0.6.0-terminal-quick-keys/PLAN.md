# v0.6.0 终端快捷按键输入面板 — 开发计划与任务清单

> **依据文档**：`PRD-CHANGE.md`（三轮评审全部闭环：M-1/M-2/N-1~N-5、R2-1~R2-4、R3-1/R3-2；需求编号 R18–R22、验收 §5、测试策略 §6 均以该文档为准）
> **日期**：2026-10-13
> **分支**：`feature/v0.6.0-terminal-quick-keys`（基线 `develop`，PR #110 已合入）
> **目标版本**：v0.6.0（并入未发布批次；无后端 / 协议变更）
> **来源**：issue #100 输入缺口（软键盘无 Esc/Ctrl、无系统粘贴气泡）+ 用户立项指令（2026-10-13）+ `docs/ORCA_MOBILE_TERMINAL_INPUT_RESEARCH.md` 立项落点

---

## 1. 计划概述

单一浮层面板 DOM + 每会话 `quickKeysOpen` 旗标（PRD-CHANGE §3.2），分 Phase 0（设计稿确认门禁）+ 2 个可独立合并、独立回滚的实施 PR 落地。主文档更新按「文档优先、随 PR 同步」原则执行。

| 阶段 | 内容 | 覆盖需求 | 依赖 |
|---|---|---|---|
| Phase 0 | pencil 设计稿确认（门禁） | R18–R22 的形态可视化 | 无（设计稿已随本计划提交，见 §2） |
| PR1 | 入口按钮 + 面板壳 + 实例绑定 + 字节键 | R18、R19、R20 | Phase 0 |
| PR2 | 复制 + 粘贴（双路径） | R21、R22 | PR1（面板壳） |

范围红线（PRD-CHANGE §8）适用于全部阶段：只改 `internal/ui/static/index.html`（测试文件除外）；无后端 / WS / HTTP 协议变更；不引入第二面板 DOM 与第二显隐路径；不做触屏拖选、通用 Ctrl/Alt 锁存、键集自定义与长按连发；web kinds iframe 内不涉及。

**流程约定**（沿用 v0.6.0 侧栏批次既定）：编码由指定 sub-agent 执行（仲裁者不写生产代码）；每个编码任务产出经 mcode + opencode（`opencode-go/glm-5.3-flash`）双外部评审 APPROVE 方可提交，评审问题先由仲裁者取证裁定再交修复；双 APPROVE 且仅剩 nit 时可自修后独立确认直接提交；git 原子提交；评审输入为工作树现场 `git diff`，评审后逐字节核对工作树无污染；合并前最后一次改动之后实跑 `go test ./...` 全绿。

---

## 2. Phase 0：pencil 设计稿确认（实施门禁）

**目的**：PR1 动的是 6553 行单文件的终端控制浮层与新增面板，开工前用设计稿把入口位置、面板两态、尺寸纪律与实例绑定行为钉死，避免实施期临场发挥。

### 2.1 产出物（已随本计划同目录提交）

- 源文件：`design/quick-keys-panel.pen`（已经 `pen` CLI headless 模式 schema 校验通过；PNG 均由 `pen interactive` headless `Export()` 导出，修改后用同一途径重出，桌面 app 可用时亦可）。
- 导出画面：
  1. `notes-decisions.png` — 设计稿说明（R18–R22 决策摘要，对照 PRD 阅读）。
  2. `component-control-column.png` — 终端控制列收起态：新入口（键盘图标）在 Refresh 之上（R18）；fine 32px / coarse 44px。
  3. `component-panel-keys.png` — 面板键网格态：9 字节键 + Copy/Paste = 11 键、列数媒体查询分档（默认 3 列 / 窄屏 2 列）、键 ≥44px（coarse）；左展锚定与 `max-height:100% + overflow-y:auto`、`max-width: calc(100vw - 88px)` 标注。
  4. `component-panel-paste-fallback.png` — 面板粘贴兜底态：同一 DOM 内容切换（M-2 选型 a）、在屏可见 textarea、取消切回不关面板、瞬态复位。
  5. `scene-desktop-panel-open.png` — 桌面宽屏展开：顶右锚定 + **面板贴按钮列左侧展开、顶缘对齐入口**（设计评审修订①）、容器 `bottom:20px` 状态行带扣除（R2-2/R3-1）。
  6. `scene-iphone-keyboard-panel.png` — iPhone 窄屏软键盘弹起：R6 钉高 + 面板超高内部滚动（M-1）+ 左展 `max-width` 上限约束全键可见、入口不被 drawer tab 遮挡且不被面板遮挡（再点 = 收起）。
  7. `scene-instance-binding.png` — 实例绑定行为 A→B→A 三态（R19）+ 删实例/切 worktree/web kind 边界。

### 2.2 确认流程

1. 设计稿随本目录提交，由用户评审（可对照 PRD-CHANGE 与 PNG 逐画面过）。
2. 评审重点：与 PRD-CHANGE 决策一致性——入口 DOM 序（R18）、面板左展锚定与收起路径（设计评审修订①②，R19/§3.1/§3.2）、单一面板 DOM 与实例绑定（R19/§3.2）、尺寸纪律与状态行避让（§3.1 M-1/R2-2 + 宽度上限）、粘贴兜底态同一 DOM 切换（M-2/§3.4）、键集与字节表（§3.6）。
3. **确认通过前，PR1 不开工**；PR2 的纯函数管线（与 DOM 形态无关的部分）可并行准备测试用例骨架。
4. 设计稿与 PRD 冲突时：先回改 PRD-CHANGE 或设计稿之一并记录原因，再实施。

### 2.3 完成判据

设计稿覆盖 §2.1 全部画面且用户确认通过，即 Phase 0 关闭。**已关闭**（2026-10-13，含修订①②回改后复审通过）。

---

## 3. 主文档更新（文档优先，随 PR 同步）

依据 PRD-CHANGE §9 清单。**每个实施 PR 自带其文档更新**，评审时一并检查；不允许集中到 release 前补。

| # | 文档 | 更新内容 | 时机 |
|---|---|---|---|
| D1 | `docs/PRD.md` | §7「当前实现状态」并入 v0.6.0 条目（终端快捷按键输入面板，注明来源 issue #100 输入缺口与本目录） | PR1 落地时建立，PR2 更新 |
| D2 | `CHANGELOG.md` | `## Unreleased` 每 PR 一条；遵守 issue #95 房规：声明覆盖数时，每处数字 = 该条目所声明测试文件集合的 `grep -c '^test('` 导出总数，子集换措辞（新增 testdata 文件不得用 "Pinned by N `node --test` cases" 短语引用全量总数） | 每个 PR |
| D3 | `README.md` / `README.zh-CN.md` | Features 可选加一句（触屏输入补偿），中英双语同步 | PR2 合并后评估 |
| D4 | `docs/API.md` / `docs/ARCHITECTURE.md` | **预计不动**（无协议 / 架构变更）；如实施中发现需要记录，单独评估后再改 | 按需 |
| D5 | `docs/ORCA_MOBILE_TERMINAL_INPUT_RESEARCH.md` | 立项落点指针**已随 PRD 提交补入**，无需再动 | 已完成 |

---

## 4. 实施阶段

> 所有改动的代码锚点以 PRD-CHANGE §1.3 为准（符号锚点优先，不追行号）。

### PR1：入口按钮 + 面板壳 + 实例绑定 + 字节键（R18–R20）

- **改动点**（`internal/ui/static/index.html`，纯增量 DOM + 新监听器，不改既有行）：
  - 入口按钮：`#terminal-controls` 首子元素（DOM 序在 Refresh 之上），复用 `.term-ctrl-btn`，键盘 SVG 图标，英文 title（R18；仅 title 无 aria，N-3 取向；桌面同显，N-5 取向）。
  - 容器契约（设计评审修订①改写）：`#terminal-controls` 补 `bottom: 20px`（top/bottom 双向偏移 → 高度确定，底部扣除状态行带 20px 保守常数）+ `pointer-events: none`（三个按钮与面板各自 `pointer-events: auto`，整列空档不吞终端右侧点击/滚动/选区）。
  - 面板 DOM（`#terminal-controls` 子节点，单一 DOM 两内容态）：`position: absolute; top: 0; right: calc(100% + 8px)` 贴按钮列左展、顶缘对齐入口；`max-height: 100%; overflow-y: auto`；`max-width: calc(100vw - 88px)`；键网格态 = 11 键（Esc/Tab/⇧Tab/←↑↓→/^C/^D/Copy/Paste，列数媒体查询分档：默认 3 列 / 窄屏 2 列——禁 `auto-fit` + shrink-to-fit 循环依赖，§3.1 警戒③；coarse 键 ≥44px）；粘贴兜底态 DOM（textarea + 提示 + 取消）本期先建壳、PR2 接管逻辑。
  - 绑定状态机：每会话 `quickKeysOpen` 旗标 + `syncQuickKeysPanel()`（读旗标 + `classList.toggle` + **隐藏时联动复位内容态**，挂 `renderWorkspace` 与开关处理器两处；幂等）（R19/§3.2、R2-3）。
  - 收起路径（§3.2 清单）：手动收起唯一入口 = 再点入口按钮 toggle；不引入面板内 × 或点面板外关闭；字节键/粘贴发送后保持展开；面板内 Esc 只发 `\x1b` 不关面板。
  - 发送契约：`quickKeyBytes()` 纯函数（键→字节，方向键读 `term.modes.applicationCursorKeysMode` 选 SS3/CSI）；发送一律 `term.input(bytes)` 走既有 onData 闸门；按钮绑 `pointerdown`（非 click）+ `preventDefault()` 保焦点（§3.3）。
  - IME 纪律：helper textarea `compositionstart`/`compositionend` 旗标 + 按会话挂载的字节队列，`compositionend` 后向**产生组合的那个会话** flush（不重取活动会话；会话销毁随弃）（§3.3）。
- **验收**：PRD-CHANGE §5.1 全部 6 条。
- **测试**：
  - `internal/ui/ui_test.go`：入口按钮 DOM 序（Refresh 之前）、面板容器在 `#terminal-controls` 内、容器 `bottom: 20px` / `pointer-events: none`、按钮与面板 `pointer-events: auto`、面板 `position: absolute` / `top: 0` / `right: calc(100% + 8px)` / `max-height: 100%` / `overflow-y: auto` / `max-width: calc(100vw - 88px)`、`pointerdown` 绑定锚点与相对顺序。
  - 新 `internal/ui/testdata/quick_keys.test.mjs`（node `--test`，由 `terminal_status_test.go` 风格包装进 `go test`）：`quickKeyBytes`（两模式分支）、`deriveQuickKeysVisibility`（派生可见性 + 隐藏复位内容态）、IME 队列 flush 归属——**从真实 served 源码切片、打桩**，不断言副本。
  - 文档：D1 建立条目 + D2 一条。
- **可独立回滚**：纯增量；Copy/Paste 两键本期为占位（点击无操作或暂隐藏，实施时定，评审确认）。

### PR2：复制 + 粘贴（R21、R22）

- **改动点**（`internal/ui/static/index.html`，承接 PR1 面板壳）：
  - 粘贴：能力分派（`navigator.clipboard.readText` 存在 → 路径 A，瞬态用户激活内调用；**API 拒绝（throw/reject）→ 反馈 + 自动落路径 B**——PR2 评审修订；否则路径 B = 面板切换粘贴兜底态，textarea `paste` 事件捕获）；管线纯函数 `normalizePasteNewlines`（`/\r?\n/g`→`\r`）→ `stripBracketedPasteMarkers`（剥离内嵌 200~/201~）→ 256Ki 码元上限截断（**先于包装**，PR2 评审修订）→ `maybeWrapBracketedPaste`（iff `bracketedPasteMode`）→ `term.input()`；**投递会话在 Paste 键入口捕获，异步兑现不重取活动会话**（PR2 评审 major 修复）；兜底态记归属会话、sync 检测活动会话变化即复位内容态（PR2 评审修订）；空 / 超限均 `updateStatus` 反馈（R21/§3.4）。
  - 复制：`term.hasSelection()` → `getSelection()`；否则 `extractViewportText`（`buffer.active` 的 `[viewportY, viewportY+rows)`，逐行 `translateToString(true)`，`\n` 连接，丢弃尾部空行）；写入 `navigator.clipboard.writeText()`，不安全上下文兜底 `execCommand('copy')`（临时 textarea + select）；空结果无操作并反馈（R22/§3.5）。
  - 粘贴兜底态交互：进入 / 取消切回键网格（不关面板）、发送后自动切回；面板每次隐藏经 sync 复位（PR1 已落）；兜底态期间终端失焦为预期。
- **验收**：PRD-CHANGE §5.2 全部 5 条。
- **测试**：
  - `quick_keys.test.mjs` 扩充：管线三段 + 上限、路径分派（打桩 `navigator.clipboard` 有无）、`extractViewportText`（打桩 buffer 普通/alt 两态）、复制写入分派。
  - `ui_test.go`：兜底态 DOM 锚点、管线函数名锚点。
  - 人工清单 beta 项（§6 清单全项，重点：iPad 裸 HTTP 两拍式、remote 双机剪贴板归属、iOS 路径 A 确认弹窗、execCommand 兜底）。
  - 文档：D1 更新 + D2 一条。
- **依赖**：PR1（面板壳与 sync）。

---

## 5. 测试规划（对齐 PRD-CHANGE §6 与仓库约定）

| 线 | 工具 | 覆盖 | 接入 |
|---|---|---|---|
| Go 资源文本断言 | `internal/ui/ui_test.go`，httptest + `strings.Contains` | DOM 锚点、class 名、函数名、相对顺序（不执行 JS） | `go test ./...`，CI 门 |
| node 行为测试 | `internal/ui/testdata/quick_keys.test.mjs`，`node --test` | 字节表 / 模式分支、派生可见性 + 内容态复位、IME 队列、粘贴管线、视口提取、路径分派 | 由 `terminal_status_test.go` 风格包装进 `go test`（node 缺席 skip）；CI 另跑 `node --test` |
| CHANGELOG 房规 | issue #95 守卫测试 | 覆盖数声明 = `grep -c '^test('` 导出总数 | 每个 PR 自查 + 守卫测试 |
| 人工清单 | 每期结束过一遍 | PRD-CHANGE §6 清单全项（裸 HTTP 两拍式、remote 双机归属、保焦点、IME 排队、TUI 内键行为、矮屏滚动、遮挡检查、绑定三路径） | 每个 PR 合并前 |
| 真机验证 | beta 渠道 | iOS / iPadOS 触屏全链路 | 并入 v0.6.0 批次 beta |

**全期通用门禁（PRD-CHANGE §5.3）**：桌面端既有行为零回归（键盘输入、鼠标选区复制、Refresh / Scroll to Bottom、R17 触摸滚动、R6 钉高）；面板不监听 click、不改任何既有监听器；`gofmt`、`go test ./...`、双二进制 build 全绿。

---

## 6. 里程碑

| 里程碑 | 内容 | 门禁 |
|---|---|---|
| M0 | Phase 0 设计稿确认 | §2.3 完成判据（用户确认） |
| M1 | PR1 合并（面板 + 字节键可用） | §5.1 全过 + 双评审 APPROVE + D1 / D2 落位 |
| M2 | PR2 合并（复制 + 粘贴） | §5.2 全过 + 双评审 APPROVE + D2 落位 |
| M3 | 并入 v0.6.0 批次 beta，真机收敛 | beta 反馈清零或评估延期 |

---

## 7. 风险与回滚

继承 PRD-CHANGE §7 风险表，计划层面的缓解：

- **7000+ 行单文件回归面大** → 纯增量 DOM + 新监听器 + 文本断言锚定；每 PR 独立可回滚。
- **iOS 剪贴板行为方差** → 双路径分派 + beta 真机实测立档；路径 A 失败降级路径 B。
- **设计稿与实施漂移** → Phase 0 门禁 + PR review 对照设计稿。
- **无真机触屏回归手段** → 模拟验证前置到每个 PR，真机靠 M3 beta 收敛，不在合并门禁内。

---

## 8. 任务清单

### Phase 0（设计稿）

- [x] pencil 设计稿（`design/quick-keys-panel.pen`，schema 校验通过）+ 7 张导出 PNG
- [x] 用户评审设计稿并确认（**2026-10-13 确认通过**；评审中提出修订①面板贴按钮列左展、②收起路径明示，已回改 PRD-CHANGE / 本计划 / .pen 并重出 PNG 复审）——Phase 0 关闭，PR1 可开工

### PR1（R18–R20）— 已完成（2026-10-13，双评审 APPROVE 后提交）

- [x] 入口按钮（DOM 序首位、键盘图标、英文 title）
- [x] `#terminal-controls` 容器契约（`bottom: 20px`、`pointer-events: none` / 子元素 `auto`）
- [x] 面板 DOM 两态壳（键网格 11 键，列数媒体查询分档 3/2 列；粘贴兜底态壳）+ `position:absolute; top:0; right:calc(100% + 8px)` 左展锚定 + `max-height: 100%` / `overflow-y: auto` / `max-width: calc(100vw - 88px)`
- [x] `quickKeysOpen` 每会话旗标 + `syncQuickKeysPanel()`（含隐藏复位内容态，挂 `renderWorkspace` + 开关处理器）
- [x] `quickKeyBytes()` + `term.input()` 发送 + `pointerdown` 保焦点绑定
- [x] IME 组合态旗标 + 按会话队列 flush（评审修复：绑定改无条件；残差窗口登记 PRD-CHANGE §7）
- [x] `ui_test.go` 锚点断言 + `quick_keys.test.mjs`（字节表 / 派生可见性 / IME 队列 / toggle / sendQuickKey）
- [x] D1 建立 + D2 一条
- [x] 双外部评审 APPROVE（mcode 两轮 + opencode 一轮；major 1 / minor 3 修复复审通过，nit 级仲裁自修）→ 原子提交

### PR2（R21–R22）— 已完成（2026-10-13，四轮双评审后双 APPROVE 提交）

- [x] 粘贴路径 A（`readText()`）+ 路径分派（含拒绝自动降级路径 B——评审修订④）
- [x] 粘贴兜底态逻辑（textarea paste 捕获 / 取消切回 / 发送自动切回 / 归属会话复位）
- [x] 管线四函数 + 256Ki 码元上限（cap 先于 wrap——评审修订②）+ `updateStatus` 反馈 + 投递会话入口捕获（评审 major ①）
- [x] 复制（选区优先 / `extractViewportText` 视口兜底）+ `writeText()` / `execCommand` 兜底
- [x] `quick_keys.test.mjs` 扩充至 24 例（管线 / 分派 / 视口两态 / 兜底交互 / 归属与守卫）+ `ui_test.go` 锚点
- [ ] 人工清单 beta 项全过（含 remote 双机实测）——**真机人工项，随 v0.6.0 beta 渠道收敛**
- [x] D1 更新 + D2 一条
- [x] 双外部评审 APPROVE（mcode×4 轮 + opencode×4 轮；major 2 / minor 6 修复复审通过，nit 级仲裁自修）→ 原子提交
- [x] PR #112 评审两轮闭环：第一轮 mcode minor 1（writeText 拒绝兜底激活窗口——仲裁登记 PRD-CHANGE §7）+ Sisyphus minor 2（粘贴捕获 preventDefault 先行、composing 无兜底清除）/ nit 2（toggle tabindex、测试 seam）→ 修复 `5dc6057`（quick_keys 24→27 例）；第二轮 Sisyphus 复核 0 新问题、一审意见全部闭环 APPROVE

### 全期

- [x] 桌面既有行为零回归核对（§5 门禁）——双评审逐轮核验 + `git diff` 删除行穷举均为计划内
- [x] 评审后工作树逐字节核对（每轮）——四轮评审后均 `git status` 核对无评审外污染
- [x] 合并前最后一次改动后实跑 `go test ./...` 全绿——21 包 ok；`node --test` 139/139
- [ ] D3（README 双语一句）评估——按 PLAN §3 时机为「PR2 合并后评估」，分支合并时执行
- [ ] 并入 v0.6.0 beta 真机收敛——人工项（§6 人工清单）
