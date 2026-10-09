# v0.6.0 Web UI 侧栏折叠与响应式 / 触屏 — 开发计划

> **依据文档**：`PRD-CHANGE.md`（已经 PR #109 评审修订；需求编号 R1–R16、验收 §5、测试策略 §6 均以该文档为准）
> **日期**：2026-10-09
> **分支**：`feature/v0.6.0-webui-sidebar-responsive`（基线 `develop`，v0.5.2 已合入）
> **目标版本**：v0.6.0（minor；无后端 / 协议变更）
> **来源 issue**：#99（桌面侧栏折叠）、#100（响应式 + 触屏，iOS / iPadOS）

---

## 1. 计划概述

单一 `sidebarCollapsed` 状态机、宽屏 / 窄屏两种渲染形态（PRD-CHANGE §3.1），分 4 个可独立合并、独立回滚的实施 PR 落地。实施前置一道**设计稿确认门禁（Phase 0）**，主文档更新按「文档优先、随 PR 同步」原则执行，不做最后一次补齐。

| 阶段 | 内容 | 覆盖需求 | 依赖 |
|---|---|---|---|
| Phase 0 | Web UI 设计稿确认（门禁） | R1、R2、R7、R10、R14 的形态可视化 | 无 |
| PR1 | 桌面侧栏折叠 / 展开 | R1–R4、R12（桌面）、R13、R16 | Phase 0 |
| PR2 | 视口与高度（最小改动，紧随 PR1） | R5、R6、R12 | 无（可与 PR1 并行，但建议紧随） |
| PR3 | 断点与 drawer 形态 | R7、R8、R14、R2（窄屏复用）、R12 | PR1（语义复用）+ Phase 0 |
| PR4 | 触屏交互 | R9、R10、R11 | PR2 + Phase 0 |

范围红线（PRD-CHANGE R15 / §8）适用于全部阶段：只改 `internal/ui/static/index.html`；iframe 内页面不自适应；无后端 / WS / HTTP 协议变更；不引入 `interactive-widget`；不做边缘滑出 drawer。

---

## 2. Phase 0：Web UI 设计稿确认（实施门禁）

**目的**：PR1 / PR3 动的是 5562 行单文件的布局骨架，开工前用设计稿把各形态钉死，避免实施期临场发挥。

### 2.1 产出物

- 设计稿文件置于 `docs/plans/feature-v0.6.0-webui-sidebar-responsive/design/`（源文件 + 导出 PNG，文件名自取，如 `sidebar-states.*`）。
- 必须覆盖的画面：
  1. 桌面宽屏：**展开态**与**折叠态**（`#sidebar` 宽度归零、`#main` 吃满、chevron 按钮在 `#header` 左侧常驻，R1 / R2）。
  2. 窄屏（≤768px）：drawer **关闭态**（首访默认，R14）与**打开态**（off-canvas drawer + 遮罩；遮罩盖住 `#main` 与 tab 区，`#header` 左侧开关按钮保持可点，R7）。
  3. 中间档（769–1024px）：收窄侧栏并排（R7）。
  4. 触屏目标尺寸标注：`.icon-btn` / 终端控制按钮在 `pointer: coarse` 下 ≥ 44×44 CSS px（R10，注意是 CSS px 不是 pt）。

### 2.2 确认流程

1. 设计稿随本目录提交，在 PR 中评审（或单独评审）。
2. 评审重点：与 PRD-CHANGE 的决策一致性——按钮宿主（§3.2）、单一状态机两形态（§3.1）、断点三档归属（§3.6）、窄屏默认关闭（R14）、z-index 分层（§5.3 第 4 条）。
3. **确认通过前，PR1 / PR3 不开工**；PR2（视口与高度，无布局形态变化）可并行准备。
4. 设计稿与 PRD 冲突时：先回改 PRD-CHANGE 或设计稿之一并记录原因，再实施。

### 2.3 完成判据

设计稿覆盖 §2.1 全部画面且评审通过，即 Phase 0 关闭。

---

## 3. 主文档更新（文档优先，随 PR 同步）

依据 PRD-CHANGE §9 清单。**每个实施 PR 自带其文档更新**，评审时一并检查；不允许集中到 release 前补。

| # | 文档 | 更新内容 | 时机 |
|---|---|---|---|
| D1 | `docs/PRD.md` | §7「当前实现状态」新增 v0.6.0 条目（侧栏折叠 + 响应式 / 触屏，注明来源 #99 / #100 与本目录） | PR1 落地时建立条目，后续 PR 更新状态 |
| D2 | `CHANGELOG.md` | `## Unreleased` 每 PR 一条；遵守 issue #95 房规：声明覆盖数时，每处数字 = `grep -c '^test('` 导出总数，子集换措辞 | 每个 PR |
| D3 | `README.md` / `README.zh-CN.md` | Features 可选加一句（折叠 + iPad 可用性），中英双语同步 | PR3 合并后评估 |
| D4 | `docs/API.md` / `docs/ARCHITECTURE.md` | **预计不动**（无协议 / 架构变更）；如实施中发现需要记录，单独评估后再改 | 按需 |

---

## 4. 实施阶段

> 所有改动的代码锚点以 PRD-CHANGE §1.4 为准（符号锚点优先，不追行号）。

### PR1：桌面侧栏折叠 / 展开（#99 全量）

- **改动点**（`internal/ui/static/index.html`）：
  - collapsed CSS 类挂 `#app`，`#sidebar` `width: 0` + `overflow: hidden`；JS 零宽度计算（R1）。
  - chevron 开关按钮放 `#header` 左侧，折叠态常驻（R2）。
  - 首个 localStorage 用例 `mw.ui.sidebarCollapsed`：try/catch 降级不持久化、宽屏默认展开（R3 / §3.3）。
  - 首个全局 keydown `Ctrl/Cmd+B`：`key.toLowerCase() === 'b'`、忽略 `event.repeat`、input / textarea / contenteditable 守卫、iframe 焦点限制写注释不绕过（R4 / §3.4）。
  - 折叠 / 展开后触发 xterm fit，重排顺序显式固定（R12）。
- **验收**：PRD-CHANGE §5.1 全部 7 条。
- **测试**：
  - `internal/ui/ui_test.go`：collapsed 类名、key 名、挂 / 摘 class 相对顺序的文本断言（§5.1 第 6 条）。
  - `internal/ui/testdata/*.test.mjs`（node `--test`，由 `terminal_status_test.go` 风格包装进 `go test`）：localStorage 往返、无 key / 解析失败默认展开、快捷键守卫（§5.1 第 7 条）。
  - 文档：D1 建立条目 + D2 一条。
- **可独立回滚**：纯前端、无依赖。

### PR2：视口与高度（#100 L1）

- **改动点**：viewport meta 补 `viewport-fit=cover`；`#app` 高度 `100dvh` + `@supports` 保留 `100vh` fallback、宽度 `100%` 取代 `100vw`；`env(safe-area-inset-*)` 处理刘海与 Home Indicator（R5）；`visualViewport` resize / scroll 监听收窄终端可视高度（R6）。**不引入 `interactive-widget`**（§3.5 决策）。
- **验收**：§5.2 全部 3 条；与折叠态同场验证（§3.5：折叠 + 键盘弹出同时发生不跳动）。
- **测试**：ui_test.go 文本断言（meta 内容、`dvh` / `@supports` 块、safe-area 引用）；node 行为用例打桩 `visualViewport`；D2 一条。
- **依赖**：无，可与 PR1 并行；建议紧随 PR1 合并。

### PR3：断点与 drawer 形态（#100 L2）

- **改动点**：
  - `@media (max-width: 768px)`：侧栏改覆盖式 drawer（`position: absolute` + `transform: translateX`），`#app` 补 `position: relative` 作定位包含块；遮罩盖住 `#main` 与 tab 区、点遮罩关闭；drawer 打开态 `#header` 左侧按钮仍可点击关闭（z-index 分层）（R7）。
  - 中间档 769–1024px 侧栏收窄（如 `max-width: 200px`），768px 归窄屏（§3.6）。
  - `#tabs-container` 补 `scroll-snap-type`，滚动条继续隐藏（复用 `index.html:568-600` 规则族，R8）。
  - 窄屏首次加载（无持久化值）drawer 默认关闭；与桌面共享同一 key，不引入第二个状态变量（R14）——**review 重点：有没有偷偷引入第二个状态变量**。
- **验收**：§5.3 全部 5 条。
- **测试**：ui_test.go 文本断言（断点块、drawer class、遮罩元素、`scroll-snap-type`）；node 行为用例（断点分派函数、窄屏默认值按视口分派、遮罩关闭）；人工清单过窄屏形态；D2 一条。
- **依赖**：PR1（复用 collapsed 语义与按钮）。

### PR4：触屏交互（#100 L3）

- **改动点**：侧栏分栏拖拽迁 Pointer Events（`pointerdown` / `pointermove` / `pointerup` + `setPointerCapture`），拖拽时 handle `touch-action: none`、拖拽元素 `user-select: none` / `-webkit-touch-callout: none`（R9）；`@media (pointer: coarse)` 下 `.icon-btn` 与 `--term-ctrl-btn-size` 用 `min-width` / `min-height` 放大到 ≥ 44×44 CSS px（R10）；hover 样式用 `@media (hover: hover)` 包裹（R11）。
- **验收**：§5.4 全部 3 条。
- **测试**：ui_test.go 文本断言（pointer 事件绑定、`pointer: coarse` / `hover: hover` 媒体查询块）；桌面鼠标回归（拖拽、hover 不变）；`pointer: coarse` 模拟验证样式分支；D2 一条。
- **依赖**：PR2（视口基线）。

---

## 5. 测试规划（对齐 PRD-CHANGE §6 与仓库约定）

| 线 | 工具 | 覆盖 | 接入 |
|---|---|---|---|
| Go 资源文本断言 | `internal/ui/ui_test.go`，httptest + `strings.Contains` | DOM 锚点、class 名、key 名、相对顺序（不执行 JS） | `go test ./...`，CI 门 |
| node 行为测试 | `internal/ui/testdata/*.test.mjs`，`node --test` | localStorage 往返、默认值分派、快捷键守卫、断点分派、`visualViewport` 打桩 | 由 `terminal_status_test.go` 风格包装进 `go test`（node 缺席 skip）；CI 另跑 `node --test` |
| CHANGELOG 房规 | issue #95 守卫测试 | 覆盖数声明 = `grep -c '^test('` 导出总数 | 每个 PR 自查 + 守卫测试 |
| 人工清单 | 每期结束过一遍 | 桌面宽屏 / 400px 窄窗；刷新持久化；清 localStorage 恢复默认（宽屏展开 / 窄屏 drawer 关闭）；iframe 内焦点 Ctrl+B 不切换；git section 折叠互不覆盖 | 每个 PR 合并前 |
| 真机验证 | beta 渠道 | iOS / iPadOS 触屏、软键盘、Split View | 发布前 beta tag（v0.5.1 beta 曾抓出 3 个真实 bug） |

**全期通用门禁（§5.5）**：桌面行为零回归（400px 窄窗人工过检 + `pointer: coarse` 模拟）；`gofmt`、`go test ./...`、双二进制 build 全绿。

---

## 6. 里程碑

| 里程碑 | 内容 | 门禁 |
|---|---|---|
| M0 | Phase 0 设计稿确认 | §2.3 完成判据 |
| M1 | PR1 合并（桌面折叠可用） | §5.1 全过 + D1 / D2 落位 |
| M2 | PR2 合并（视口与高度） | §5.2 全过 |
| M3 | PR3 合并（窄屏 drawer + 断点） | §5.3 全过 + 人工清单窄屏项 |
| M4 | PR4 合并（触屏交互） | §5.4 全过 |
| M5 | `develop` 出 beta tag，真机收敛 | beta 反馈清零或评估延期 |
| M6 | 切 `main` 发布 v0.6.0 | 既有 release 流程 |

---

## 7. 风险与回滚

继承 PRD-CHANGE §7 风险表，计划层面的缓解：

- **5562 行单文件回归面大** → 小步 PR + 文本断言锚定 + 每 PR 独立可回滚。
- **无真机触屏回归手段** → 模拟验证前置到每个 PR，真机靠 M5 beta 收敛，不在合并门禁内。
- **设计稿与实施漂移** → Phase 0 门禁 + PR review 对照设计稿。
- **状态机污染**（drawer 偷偷引入第二状态） → PR3 review 显式检查项（PRD-CHANGE §4）。
