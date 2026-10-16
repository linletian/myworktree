# Issue #122 侧栏工作树行双击改名 — 开发计划

> **依据 issue**：#122「ui(sidebar): 工作树行支持双击改描述 —— 复用实例 tab 的 rename 交互」（OPEN，enhancement）
> **分支**：`feature/issue122-rename-worktree`（基线 `origin/develop` @ 7e124a7）
> **目标**：激活的工作树行（`.wt-item.active`）双击就地编辑展示名，交互与实例 tab 改名完全一致；后端新增 `PATCH /api/worktrees`
> **计划日期**：2026-10-16
> **计划状态**：双评审 APPROVE 定稿（§7 评审记录）

---

## 1. 需求摘要

侧栏 worktree 行的 `w.name` 是创建时任务描述的 slug（`worktree.go` `slugify(taskDesc)` 落库 `wt.Name`），原始描述从未持久化，侧栏只有静态文本不可编辑。本 issue 把实例 tab 成熟的 rename 交互（`index.html` `startRename` / `commitRename` / `cancelRename` / `handleRenameKeydown` → `PATCH /api/instances` → `instanceMgr.UpdateName`）复用到侧栏工作树行：

- **前端**：`renderSidebar()` 行上加双击进入编辑态；Enter 提交 / Esc 取消 / 失焦提交（非空）或回滚（空值）——与实例 tab 逐字一致（决策 H 仲裁）；空值不写回。
- **后端**：新增 `PATCH /api/worktrees`（`{"id","name"}`）；`worktree.Manager.UpdateName(id, name)`：Load（版本保护的重试循环内）→ 改 `Name` → `SaveWithVersion`；未找到返回 `ErrWorktreeNotFound` → 404；trim 后为空 → 400。
- **文档**：`docs/API.md` 第 2 节补 PATCH 说明。
- **测试**：`internal/worktree` 与 `internal/app` 补单测（issue 验收标准明文要求）；UI 行为按仓库既有惯例补 `internal/ui/testdata/*.test.mjs` + Go 包装。

范围红线（issue「作用域决策」）：主仓库行（`__main__` / `.wt-item-main`）v1 不开放编辑；只改展示标签，不动 `Branch` / `Path`；无协议层 / WS 改动。

范围红线补充（仲裁裁定，证据：`internal/mcp/stub.go` 的动词表只有 `worktree_list` / `worktree_create` / `worktree_delete`——连既有的 import 都没有 MCP 动词；CLI 侧同理无 rename 子命令）：**MCP `worktree_rename` 动词与 CLI 子命令明确不做**，v1 交付面 = `PATCH /api/worktrees` + 仪表盘侧栏 + 文档。与 issue「无协议层变更、可独立 PR」的自述一致。

---

## 2. 关键设计决策与仲裁记录

> 原则：每条决策附证据（代码锚点用符号/文本锚，不追行号）。编码 / 评审 Agent 可对以下决策提出质疑，由仲裁者裁定，任何一方不得直接修改本计划。

### A. 采纳 issue「方案 A」：直接改 `Name`，不新增 `Description` 字段

- 证据：`internal/store/state.go` `ManagedWorktree.Name` 注释即 "human-facing slug"；实例 tab 改名改的也是 `Name`（`framework.Manager.UpdateName` → `inst.Name = name`），用户心智统一。
- 收益：零 schema 变更、零迁移成本；后端只加一个 `UpdateName`。
- 代价（issue 自述）：「名字」与「描述」合成一个字段。接受——issue 需求只说「能改」，未要求保留原 slug；真出现「既留 slug 又写描述」的需求再单开 issue 走方案 B。

### B. 后端错误映射：400 / 404，新增 sentinel `ErrWorktreeNotFound`

- 证据：`handleInstances` PATCH 分支的既有形状——`errors.Is(err, framework.ErrInstanceNotFound)` → 404，其余 → 400（app.go `case http.MethodPatch:`）；issue 明文「未找到返回 `ErrWorktreeNotFound` → 404」。
- 结论：`internal/worktree` 新增 `var ErrWorktreeNotFound = errors.New("worktree not found")`；`UpdateName` 未命中时返回 `fmt.Errorf("%w: %s", ErrWorktreeNotFound, id)`（`%w` 保证 `errors.Is` 可判定，消息带 id 便于排障）；handler 用 `errors.Is` 映射 404。
- 空 id / trim 后空 name：照 `framework.Manager.UpdateName` 语义（methods.go）：`"id is required"` / `"name cannot be empty"`，handler 映射 400。

### C. trim 与长度语义：与实例改名逐字对齐，不引入长度上限

- 证据：`framework.Manager.UpdateName`（methods.go）对 id / name 均 `strings.TrimSpace`，空值报错，**无长度上限**；前端 `commitRename` 同样 `input.value.trim()` + 空值跳过。
- 结论：`worktree.Manager.UpdateName` 与 `commitWTRename` 逐字镜像该语义。验收标准的「超长内容渲染后原样显示为文本」由前端 `escapeHtml` 与文本渲染天然满足（`escapeHtml` 转义五个元字符，见 index.html `escapeHtml` 定义处注释），不需要后端截断——保持与实例改名 parity，不偷偷加限制。

### D. 前端状态机镜像 + 两套状态机互斥（至多一个 input）

- 新增 `state.renamingWT` / `state.renamingWTValue`，与 `state.renamingInst` 同构；`window.state` 定义在 `internal/ui/static/framework.js`（`renamingInst: null` 的初始化处），同步初始化两个新字段。
- 验收标准要求「两套状态机互不干扰，任一时刻至多一个 input 存在」。结论：**互相取消**——`startWTRename` 先清 `state.renamingInst` 并 `renderTabs()` 拆掉 tab input；`startRename` 先清 `state.renamingWT` 并 `renderSidebar()`。这是一行级的对称改动，保证不变量成立。

### E. 双击行为实测证据（Chrome，CDP 探针，2026-10-16）

issue「方案」称非激活行双击「第一次 click 激活、第二次进编辑，不需要额外分支」。该结论依赖「第一次 click 触发 `selectWorktree` → `render()` → `renderSidebar()` 重建 `#wt-list` innerHTML 后，第二次 click 的 dblclick 仍能命中行内联 `ondblclick`」这一浏览器事件语义，**不能凭经验断言，已实测**：

- 探针：headless Chrome（google-chrome 158 环境可用）+ 最小 CDP 客户端复刻真实应用结构——`selectWorktree` 对已激活行早退（不重建 DOM）、对非激活行重建 innerHTML；每行带内联 `ondblclick`。
- 结果（三场景全部符合预期）：
  - 非激活行双击：`activated@b` → `RENAME-STARTED@b`（第一次 click 激活并重建 DOM，dblclick 依然触发了新行的内联 handler）；
  - 已激活行双击：直接 `RENAME-STARTED@b`；
  - 切换激活行后双击：`activated@a` → `RENAME-STARTED@a`。
- 补充实测（早期探针）：即使第二次 click 再次重建 DOM 导致 dblclick 派发到已分离节点，**该节点上的内联 handler 仍会执行**（document 级监听收不到，证明目标节点已分离，但 attribute handler 照常触发）。
- **不依赖探针的代码级事实**（评审 A F4 要求补入，二者使结论可复现、跨浏览器稳健）：
  (a) 首次 click 的 DOM 重建是**同步**的：`selectWorktree()` 在切换分支里先同步 `render()`（index.html `state.activeWT = id;` 之后的 `render();`，锚点 `selectWorktree` 函数体）重建 `#wt-list`，`refresh()` / `fetchDivergeSingle(id)` 只是其后启动的异步后续；
  (b) **所有非主行都带同一个内联 `ondblclick`**（`renderSidebar()` 的 worktree 行模板不按 active 状态区分挂载），因此第二次 click 的 dblclick 无论命中重建前还是重建后的行节点，该节点上都带 handler——不依赖「事件派发到哪个节点」的浏览器差异；
  (c) 硬验收路径（**已激活**行双击）上 `selectWorktree` 第一行即早退（锚点 `if (state.activeWT === id) return;`），根本不重建 DOM，dblclick 必然命中同一连接节点——确定性成立，与浏览器无关。
- 探针存档：本目录 `evidence/`（`test.html` + `cdp.mjs` + `RESULTS.md`，headless Chrome CDP 实测，复现命令见 RESULTS.md）。
- 裁定：issue 的「不需要额外分支」成立，按 `ondblclick` 内联属性实现即可，不引入容器级委托或 click-count 特殊分支。已知局限：本环境 Firefox 无法启动可调试实例，探针仅覆盖 Chrome；但由上述 (a)(b)(c) 代码事实兜底，非激活路径不依赖浏览器事件派发差异，激活路径（硬验收）更是确定性的。

### F. 渲染落点：改名提交后只 `renderSidebar()`

- 证据：全仓检索工作树名的渲染点，侧栏 `renderSidebar()` 的 `.wt-name` 是 `w.name` 的唯一界面渲染处；其余引用（`state.worktrees` 查找、`wt_name` 仅作为 `/preview` 的 URL 参数、实例 `worktree_name`）不渲染名字本身。
- 结论：`commitWTRename` 提交前后与成功后均调用 `renderSidebar()`（对齐 `commitRename` 提交前 `renderTabs()` 的形状），不触发整页 `render()`，避免触碰 workspace / iframe。
- 已知去规范化残留（记录在案，不在本 issue 范围）：Resource Monitor 模态按实例的 `worktree_name`（实例记录上的去规范化字段，monitor collector 取 `inst.WorktreeName`）分组显示，工作树改名后该模态在实例重启前仍显示旧名。这是既有数据结构行为，issue 范围仅限侧栏行；后续如需要可在 monitor 数据装配处改读 worktree 记录。

### G. pitfall #4（issue 未列名，实测代码发现）：轮询期 `renderSidebar()` 直调点

issue 的三个坑只提到给 `refresh()` 的 `render()` 加 `state.renamingWT` guard。但侧栏输入框**住在侧栏里**，而轮询还会经由两条**不经 `render()`** 的路径直调 `renderSidebar()`，同样会重建 `#wt-list` innerHTML、撕掉输入框和 IME 组合态：

- `refresh()` 内 `/api/main` 的 `.then` continuation：`renderSidebar(); // Only re-render sidebar, not the full UI.`（index.html，锚点：`const prevBranch = state.mainRepo` 之后）——随 refresh 每次轮询（2s 间隔，`pollTimer = setInterval(refresh, 2000)`）。
- `fetchDiverge()`（`/api/worktrees/diverged` 返回后）：`renderSidebar()`——由 `refresh()` 每次轮询调用，**且另有 60s 独立定时器**（`divergeTimer = setInterval(fetchDiverge, 60000)`，index.html `Polling for diverge status` 处）。因此守卫必须落在 `fetchDiverge()` 函数体内（callee 侧）才能同时覆盖两条触发路径；`/api/main` 续段的守卫落在该调用点。
- （对照证据：tab rename 从未被此坑咬到，因为 tab input 在 `#tabs-container`，`renderSidebar()` 不碰它；侧栏 rename 是新暴露面。）

结论：上述两处（`/api/main` 续段调用点 + `fetchDiverge()` 函数体内）加 `if (!state.renamingWT)` 守卫；`fetchDivergeSingle` / `openTerminal` / `openFinder` / `render()` 内的调用均为用户动作同步路径（动作前 blur 已提交或状态已清），不加 guard。这与 issue pitfall #1 的注释口径一致：「preserve input DOM and IME composition」。

附注（防误改）：`framework.js` 里的 `window.refresh` 是死代码——`framework.js`（`<script src="/static/framework.js">`，在 2561 行附近）先于内联脚本（2567 行起）加载，内联脚本的 `async function refresh()` 函数声明后覆盖 `window.refresh`；**轮询实际走的是 index.html 内的 `refresh()`**（guard 所在处），改守卫不要改错文件。

### H. blur 语义仲裁：parity 获胜——非空失焦 = 提交（issue 原文自相矛盾的裁定）

issue 原文两处口径冲突：**需求**（加粗主诉求）「交互与实例 tab 改名完全一致」 vs 验收标准「Esc 取消、点外部回滚」。而 tab 的真实行为是**失焦提交**（`index.html` tab 行 `onblur="commitRename(...)"`；`commitRename` 非空即发 PATCH，空值才 `return` 不写回）。

- 裁定：**parity 获胜**。`onblur` 接 `commitWTRename`，与 tab 逐字一致：
  - Enter / 失焦（输入非空）→ 提交（PATCH）；
  - Esc / 失焦（输入为空或纯空白）→ 回滚，不写回、无 PATCH。
- 依据：(1) issue 的加粗主诉求就是「完全一致」，且痛点本身就是「同一个界面里行为不一致」——为侧栏另造一套失焦回滚语义等于把不一致搬进新代码；(2) issue「方案」小节对该输入框的指令是「空值提交按现状语义不写回（对齐 `commitRename` 的 `if (newName === '') return`）」——「空值**提交**」这个说法本身 presupposes 失焦/回车都会走提交通路；(3) 验收标准「三种退出都不写脏数据」在 parity 语义下成立：空值与 Esc 不写，Enter/非空失焦写的是用户明确键入的合法新名，不是「脏数据」。原文「点外部回滚」是对 tab 行为的误述，以代码事实与加粗主诉求为准。
- 影响面：本计划 §4 映射表 row 1 与 §5 人工清单第 5 条按此语义改写（评审 B F1）。

### I. XSS 与转义

- 证据：`escapeHtml`（index.html）转义 `& < > " '` 五个元字符，属性上下文安全；tab 侧回填先过 `escapeHtml`（`renameValueEsc`）。
- 结论：输入框 `value="${escapeHtml(renameValue)}"`；展示文本 `escapeHtml(w.name)`（现有）。含 `<script>` / 引号的名字渲染后原样显示为文本。

---

## 3. 任务清单

> 每个任务由编码 Sub-agent 执行；产出必须双评审 APPROVE（nit 级剩余时按 §6 流程处理）。任务按依赖排序，T1→T2→T3 为代码主线，T4（浏览器层测试）与 T5（CHANGELOG）随对应任务落地（仓库惯例：文档随 PR 同步，不集中补；API 文档在 T2 内）。

### T1 — 后端 Manager：`worktree.Manager.UpdateName`

**文件**：`internal/worktree/worktree.go`（+ 测试）

**改动点**：
1. 新增 sentinel：`var ErrWorktreeNotFound = errors.New("worktree not found")`。
2. 新增方法（input 语义照 `framework.Manager.UpdateName` / methods.go；**落盘必须走版本保护的 `SaveWithVersion` + `ErrVersionConflict` 短重试**，照 `framework.Manager.updateStore`（manager.go:705-737：`maxRetries = 3`、10ms backoff）——**不要**照本包 `Delete` 的 plain `Save`，理由见下）：
   ```go
   func (m Manager) UpdateName(id, name string) (store.ManagedWorktree, error)
   ```
   - `strings.TrimSpace` id/name；空 id → `errors.New("id is required")`；空 name → `errors.New("name cannot be empty")`。
   - Load → 按 ID 找下标 → 未命中返回 `fmt.Errorf("%w: %s", ErrWorktreeNotFound, id)`。
   - 只改 `Name`：`st.Worktrees[idx].Name = name`（ID/Path/Branch/BaseRef/CreatedAt 一字不动）。
   - 落盘：`m.Store.SaveWithVersion(st, st.Version)`；`errors.Is(err, store.ErrVersionConflict)` 时重试（整条 Load→改→Save 重来，3 次上限、10ms backoff，与 `updateStore` 逐字对齐）；重试耗尽返回包装错误。
   - **为什么不用 plain `Save`**（评审 A F1，证据：`store.Save` 无条件整文件写回且写回的是加载时的旧 `Version`（state.go:114-141）；`state.json` 里 Worktrees/Instances/TabOrder 同文件，net/http 每请求一 goroutine、实例 lifecycle goroutine 常驻——rename 的 Load 与 Save 之间若发生一次实例写入（`SaveWithVersion` 会 bump Version），plain Save 会整卷回滚该次实例变更并把 Version 打回旧值，破坏乐观锁语义。本包 `Create`/`Import`/`Delete` 的 plain Save 是既有现状，不在本 issue 范围内改动；`UpdateName` 作为 issue 点名「照 instanceMgr.UpdateName」的新入口，按被镜像方的真实落盘形状实现）。
   - 注：`SaveWithVersion` 成功会 bump `Version`（本包其他写路径不 bump）——无副作用：version 只服务于实例更新的乐观锁，前端每次轮询重取。
3. 测试（`internal/worktree`，对齐 `worktree_test.go` / `worktree_integration_test.go` 既有写法）：
   - 纯 store 用例（`t.TempDir()` + `store.FileStore`，无需 git）：改名成功且 `Load()` 读回；仅 `Name` 变化；空 name 报错；空 id 报错；未知 id `errors.Is(err, ErrWorktreeNotFound)`。
   - 版本保护用例：`SaveWithVersion` 语义 mock 不易注入，改为断言 `UpdateName` 成功后 `Version` 被 bump（证明走的是 SaveWithVersion 而非 plain Save——plain Save 不 bump；`SaveWithVersion` 做 `st.Version = current.Version + 1`，state.go），以及连续两次 `UpdateName` 均成功（Version bump 已区分两条落盘路径）。
   - 集成用例（真实 git repo，照 `worktree_integration_test.go` 的 `initGitRepo` 助手）：`Create` → `UpdateName` → 断言 `Branch` / `Path` 不变、`git worktree list` 仍列该路径（验收标准「`git worktree list` 不受影响」）。

**验收**：`go test ./internal/worktree/` 全绿；`gofmt` 干净。

### T2 — 后端 Handler：`PATCH /api/worktrees`

**文件**：`internal/app/app.go`（`handleWorktrees` 加 `case http.MethodPatch:`）、`docs/API.md`、测试新文件 `internal/app/app_worktree_rename_test.go`

**改动点**：
1. `handleWorktrees` 新增 PATCH 分支（形状照 `handleInstances` PATCH）：
   - 入参 `{"id": "...", "name": "..."}`；`readJSON` 失败 → 400（注意 `readJSON` 带 `DisallowUnknownFields`）。
   - `s.worktreeMgr.UpdateName(req.ID, req.Name)`；`errors.Is(err, worktree.ErrWorktreeNotFound)` → 404；其余 → 400。
   - 200 返回更新后的 `store.ManagedWorktree`（与 GET 列表元素同形，前端可直接替换 `state.worktrees` 对应项）。
2. `docs/API.md` 第 2 节「Worktrees」新增 `### Rename` 小节：方法 / body / 200 响应样例 / 400（空 name）/ 404（未知 id）说明；行文风格对齐该节既有小节。
3. 测试（新文件；handler 用例照 `app_test.go` 的 `TestHandleInstanceUpdate` 形状——httptest 直调 `srv.handleWorktrees`、断言 200/400/404 与响应体；`newIsolatedTestServer` **不**设置 `worktreeMgr`，故按 `app_worktree_delete_test.go` 的方式自建最小 `&Server{worktreeMgr: worktree.Manager{Store: fs}}`（`UpdateName` 是纯 store 操作，`GitRoot`/`DataDir` 用不到，无需真实 git repo；git 级不变量由 T1 集成用例钉住））：
   - PATCH happy path：200，响应 `name` 为新值，store 落盘；
   - 随后 GET `/api/worktrees`（经 `handleWorktrees` GET 或 `worktreeMgr.List()`）读回新名（验收「`List()` 能读回 / 刷新页面重启 daemon 后仍是新值」的机制证明）；
   - 空 name → 400 且 store 不变；
   - 未知 id → 404；
   - 非法 JSON / 未知字段 → 400。

**验收**：`go test ./internal/app/` 全绿。

### T3 — 前端状态与交互（index.html + framework.js）

**文件**：`internal/ui/static/framework.js`、`internal/ui/static/index.html`（含 CSS）

**改动点**：
1. `framework.js` 的 `window.state`：`renamingInst: null` 旁补 `renamingWT: null, renamingWTValue: ""`（issue 明文要求两字段与 `renamingInst` 同构）。**诚实注记（评审 A F5）**：`renamingInstValue` 是既有的 write-only 残留态（只被赋 `""`，`renderTabs` 的 `isRenaming && state.renamingInstValue` 永假）——`renamingWTValue` 是 issue 点名的同构镜像，保留但注释标注「存量死变量模式，等待 tab 侧清理；若 tab 侧未来接入实时值绑定，WT 侧跟随」。
2. `renderSidebar()` 普通 worktree 行（非 `.wt-item-main`）：
   - 行加 `ondblclick="startWTRename('${wIdEsc}', event)"`；`wIdEsc` 即现有 `escapeHtml(w.id)`。**所有**非主行统一挂载（不按 active 状态区分）——决策 E(b)：第二次 click 的 dblclick 命中重建前/后的行节点都带 handler。
   - 编辑态分支（`state.renamingWT === w.id`）：`.wt-name` 位置渲染 `<input id="wt-rename-input-${wIdEsc}" class="wt-rename-input" value="${escapeHtml(renameValue)}" onkeydown="handleWTRenameKeydown('${wIdEsc}', event)" onblur="commitWTRename('${wIdEsc}', event)">`；`renameValue` 逐字镜像 tab 侧（`state.renamingWTValue` 非空则用它，否则 `w.name`——因 F5 的死态现状，实际渲染值即 `w.name`）。
   - 主仓库行（`wt-item-main`）**不加** `ondblclick`（v1 明确排除，验收标准明文）。
   - 行补 `title="Double-click to rename"` 提示（对齐 tab 的 `title="Drag to reorder; double-click to rename"` 惯例）。
3. 新 handler 组（置于 tab rename handlers 旁，命名 / 形状逐字镜像）：
   - `startWTRename(id, event)`：`event.stopPropagation()`；互斥清理 `state.renamingInst = null` + `renderTabs()`；置 `state.renamingWT = id; state.renamingWTValue = ""`；`renderSidebar()`；`requestAnimationFrame` 内 `input.focus(); input.select()`（用 `wt-rename-input-<id>` 取节点，focus 前判空）。
   - `commitWTRename(id, event)`：`stopPropagation`；读 `wt-rename-input-<id>`（无节点直接 return）；`newName = input.value.trim()`；清 `state.renamingWT/renamingWTValue`；`renderSidebar()`（先恢复文本形态，与 `commitRename` 同序）；`newName === ''` 直接 return（不写回）；否则 `api('/api/worktrees', {method:'PATCH', body: JSON.stringify({id, name:newName})})` → 成功用响应替换 `state.worktrees` 中对应项 → `renderSidebar()` → **再调一次 `refresh()`**（评审 A F6：PATCH 响应携带的是落库 `Branch`，而侧栏 branch 徽标的数据源是 `List()` 每次 `git rev-parse` 的 live 值；不 resync 的话徽标会显示落库 branch 直到下一次 ≤2s 轮询。一次性的 refresh round-trip 与错误路径已有的 `refresh()` 对称，代价可忽略）；失败 `console.error` + `refresh()`。
   - `cancelWTRename()`：清状态 + `renderSidebar()`。
   - `handleWTRenameKeydown(id, event)`：Enter → commit；Escape → cancel；末尾 `event.stopPropagation()`（照抄 tab 侧，不再重新设计）。
4. `startRename`（tab 侧）加对称互斥：进入前清 `state.renamingWT = null` + `renderSidebar()`。
5. 两个后台守卫（决策 G、issue pitfall #1/#2）：
   - `refresh()` 的既有 guard `if (state.renamingInst)` 改判 `if (state.renamingInst || state.renamingWT)`；
   - `/api/main` `.then` 与 `fetchDiverge()` 内的 `renderSidebar()` 调用加 `if (!state.renamingWT)` 条件；
   - `focusTerminalIfPossible()` 的 `if (state.renamingInst) return;` 改判 `if (state.renamingInst || state.renamingWT) return;`。
6. CSS：新增 `.wt-rename-input`（样式照 `.tab-rename-input` 的变量用法：`var(--main-bg)` / `var(--border-color)` / `var(--active-color)`、`font: inherit`、`outline: none`；宽度适配 `.wt-name` 容器，如 `width: 100%` + `box-sizing: border-box`，不写死像素宽）。

**验收**（独立可执行，不依赖 T4）：`gofmt -l .` 无输出、`go build ./...` 通过、`go test ./...` 既有用例全绿、§5 人工清单可逐项执行。行为机与守卫的自动化证明在 T4（T4 自身验收）。

### T4 — 浏览器层测试（仓库既有惯例）

**文件**：新 `internal/ui/testdata/sidebar_wt_rename.test.mjs`；Go 包装加进 `internal/ui/terminal_status_test.go`（照该文件既有 `node --test` 包装函数的形状：`exec.LookPath("node")` 缺失则 `t.Skip`，失败带输出 `t.Fatalf`）。

**行为用例**（从真实源码切片、打桩浏览器全局，照 `sidebar_collapse.test.mjs` 的 `new Function("deps", ...)` 模式；切不动的依赖用桩，禁止把被测逻辑复制成第二份）：
1. `startWTRename`：置 `state.renamingWT`、调用 `renderSidebar()`、rAF 后 focus + select（桩 rAF 同步执行、桩 input 记录 focus/select）。
2. Enter 提交：PATCH body 为 `{id, name: trim 后值}`；value 前后空白被 trim。
3. 空值提交（仅空白）：**不发** PATCH，状态复位，侧栏恢复文本。
4. Esc 取消：不发 PATCH，状态复位。
5. blur 提交（决策 H，parity）：非空输入失焦 → 与 Enter 同路径发 PATCH；空/纯空白输入失焦 → 同用例 3 不写回、不发 PATCH。
6. XSS：名字含 `<script>"'&` 时到达 `api()` 的 body 保持原样（服务端存原文），且渲染路径中输入框 `value` 属性与 `.wt-name` 文本均经 `escapeHtml`——后者以 Go 文本断言钉死。
7. 轮询守卫：`state.renamingWT` 非空时，切出来的 `refresh()` 续段不调用 `render()`，且 `/api/main` 续段与 `fetchDiverge()` 不调用 `renderSidebar()`（守卫在 `fetchDiverge` 函数体内，一并覆盖 60s `divergeTimer` 触发路径）。
8. 焦点守卫：`focusTerminalIfPossible()` 在 `state.renamingWT` 非空时早退，不调用 `session.term.focus()`。
9. 互斥（决策 D）：`startWTRename` 后 `state.renamingInst === null`、DOM 中至多一个 rename input；反向 `startRename` 后 `state.renamingWT === null` 同样成立——两个方向都要驱动。

**Go 文本断言**（`internal/ui/ui_test.go`，仓库既有惯例，钉死接线）：
- 普通 worktree 行含 `ondblclick="startWTRename(`；主仓库行（`wt-item-main`）不含；
- 编辑态分支存在 `wt-rename-input-`、`value="` 前有 `escapeHtml(`、`onkeydown="handleWTRenameKeydown(`、`onblur="commitWTRename(`；
- `framework.js` `window.state` 含 `renamingWT` / `renamingWTValue` 初始化；
- `refresh()` guard、两个后台 `renderSidebar()` 调用点（`/api/main` 续段调用点 + `fetchDiverge` 函数体内）、`focusTerminalIfPossible` 均含 `state.renamingWT` 判定；
- `startRename` / `startWTRename` 各自开头的互斥清理（清对方状态 + 调对方渲染函数）存在。

**验收**：`node --test internal/ui/testdata/sidebar_wt_rename.test.mjs` 与 `go test ./internal/ui/` 全绿。

### T5 — CHANGELOG

**文件**：`CHANGELOG.md` — `## Unreleased` 顶部按既有体例加一条 feat(ui) 条目（说明交互、`PATCH /api/worktrees`、三个坑 + 决策 G 第四坑的守卫、互斥语义）。随 T3/T4 落地，不集中补。

**措辞红线（评审 B F3，两处 guard 实测存在）**：
- **禁止**用 ``Pinned by N `node --test` cases`` 句式描述新文件——`internal/ui/terminal_status_test.go` 的 `TestTerminalStatusChangelogCount` 要求 CHANGELOG 中该句式的**每一处**都等于 `terminal_status.test.mjs` 的用例总数，新文件复用必挂。
- 新文件的用例计数用 **per-layer 英文数词短语**（`TestResponsiveLayerChangelogCounts` 认可的体例，如 "eight new #122 scenarios"），且短语不得与该 guard 表现有条目的短语雷同（`#99 scenarios` / `new issue #100 L1 scenarios` / `new issue #100 L2 scenarios` 等已被占用）；若要让新短语受 guard 保护，按该函数表格形式追加 `{file, phrase}` 条目（ Derive-from-`grep -c '^test('` 机制）。

---

## 4. 验收标准映射（issue §验收 → 计划 / 测试）

| # | issue 验收标准 | 落在哪 |
|---|---|---|
| 1 | 激活行双击进编辑态，自动 focus + 全选；Enter/Esc/点外部三种退出不写脏数据（点外部语义按决策 H 仲裁：非空失焦=提交，与 tab 一致） | T3 handler 组 + T4 行为用例 1–5 |
| 2 | 提交后 PATCH 200、侧栏更新；刷新页面 / 重启 daemon 后仍是新值（`List()` 读回） | T2 handler + T2 测试 GET 读回 + T1 store 测试 |
| 3 | 编辑期间跨过一次后台 `refresh()` 轮询，输入框不消失、IME 组合态不被清 | 决策 G 两处后台守卫（含 60s divergeTimer 路径）+ T4 行为用例 7 |
| 4 | 编辑期间终端不抢焦点 | T3 `focusTerminalIfPossible` guard + T4 行为用例 8 |
| 5 | 空串提交不写回；超长 / `<script>` / 引号渲染后原样显示为文本，无 HTML 注入 | T1 空值测试 + T3 `escapeHtml` + T4 用例 3/5/6 + Go 文本断言 |
| 6 | `Branch` 与 `Path` 改名后不变，`git worktree list` 不受影响 | T1 集成用例（真实 repo + `git worktree list`） |
| 7 | 主仓库行双击不进编辑态 | T3 不加 handler + Go 文本断言 |
| 8 | 不影响实例 tab 改名；任一时刻至多一个 input | 决策 D 互斥 + T4 行为用例 9（双向）+ Go 文本断言（互斥清理行） |
| 9 | `internal/worktree` 与 `internal/app` 补单测；`docs/API.md` 补 PATCH 说明 | T1/T2/T2-docs |

---

## 5. 验证与人工清单

每个编码任务完成后、提交前，仲裁者执行：

```bash
gofmt -l .                 # 必须无输出
go build ./...
go test ./...              # 全绿（含 node 包装的 JS 层；node 缺失时 wrapper t.Skip，需另行 node --test 直跑）
node --test internal/ui/testdata/sidebar_wt_rename.test.mjs
```

人工验证清单（浏览器，Chrome；issue 验收口径）：
1. 创建工作树 → 侧栏双击该行 → 输入框出现且文字全选；
2. 输入新名（含中文 IME 输入）→ Enter → 侧栏更新；刷新页面 / 重启 daemon 后仍是新名；
3. 输入态静置 >5s（跨过一次轮询）→ 输入框与 IME 组合态仍在；
4. 输入态下点击终端区 → 焦点不被终端抢走；
5. 失焦语义（决策 H，parity）：键入新名后点击行外 → **提交**（PATCH 发出、侧栏更新，与 tab 一致）；输入框为空 / 纯空白时失焦、以及 Esc → 回滚，无 PATCH（DevTools Network 确认）；
6. 空串 Enter → 不写回；
7. 名字输入 `<script>alert(1)</script>"'&` → 提交后原样显示为文本；
8. 双击主仓库行 → 无编辑态；
9. tab 改名与侧栏改名互不干扰：tab 输入态中双击侧栏行 → tab input 消失、只剩侧栏 input（反之亦然）；任一时刻至多一个输入框；
10. `git worktree list` 与 `Branch` 列不变。

---

## 6. 流程规则（双评审）

1. 本计划先经双评审 APPROVE 才进入编码。
2. 每个编码任务产出由两个评审 Sub-agent 独立评审（模型按调度配置）；评审结论必须给出证据（代码锚点 / 测试输出）。
3. 评审问题先由仲裁者判定是否成立（不成立的驳回并附证据），成立的交编码 Sub-agent 修复后重新走双评审。
4. 双 APPROVE 且仅剩 nit 级问题时，由仲裁者独立确认后提交，不再回调评审 Agent。
5. 编码 / 评审 Agent 对计划的质疑由仲裁者裁定；计划修订记录进 §7。

## 7. 计划评审记录

| 轮次 | 评审 A（glm-5.3-flash） | 评审 B（muse-spark-1.3-contributor） | 仲裁结论 |
|---|---|---|---|
| R1 | REQUEST_CHANGES（F1 major + F2/F3/F4 minor + F5/F6 nit） | REQUEST_CHANGES（F1 major + F2/F3 minor + F4 nit） | 见下方仲裁明细 |
| R2 | **APPROVE**（余 N1/N2 两条 nit，按 §6.4 由仲裁者修复） | **APPROVE**（FINDINGS: NONE） | 计划定稿，进入编码 |

### R1 仲裁明细（全部接受——两评审的问题均经代码证据复核成立）

| Finding | 级别 | 仲裁 | 处置 |
|---|---|---|---|
| A-F1 UpdateName 落盘用 plain Save 会跨记录丢写 / Version 回退 | major | **接受**。已核实 `framework.Manager.updateStore`（manager.go:705-737）真实形状为 `SaveWithVersion(st, st.Version)` + 3 次重试 + 10ms backoff；`store.Save`（state.go:114-141）确实无条件整文件写回旧 Version；state.json 三记录同文件，net/http 每请求一 goroutine，丢写窗口真实。issue 原文要求「照 instanceMgr.UpdateName」，被镜像方的真实落盘即版本保护 | T1 改为 `SaveWithVersion` + `ErrVersionConflict` 短重试（与 `updateStore` 逐字对齐），新增「Version bump 证明」用例；明文记录本包 Create/Import/Delete 的 plain Save 为既有现状、不在本 issue 改 |
| A-F2 / B-同项 决策 G 漏 60s divergeTimer | minor | **接受**。已核实 `divergeTimer = setInterval(fetchDiverge, 60000)`（index.html）独立于 refresh 触发 | 决策 G 补全该触发点，明确守卫必须落 `fetchDiverge` 函数体内（callee 侧）才能双覆盖 |
| A-F3 / B-F2 互斥缺正测 | minor | **接受**。原 T4 行为清单确无双向互斥用例 | T4 增用例 9（双向）+ Go 文本断言补互斥清理行锚点；§4 row 8 映射改正 |
| A-F4 决策 E 探针不可复现 + 代码事实缺失 | minor | **接受**。已核实 `selectWorktree` 的 `render()` 为同步重建、refresh/fetchDivergeSingle 为异步后续；非主行统一挂 `ondblclick`；激活行早退不重建 | 决策 E 补 (a)(b)(c) 三条代码锚点；探针存档至 `evidence/`（test.html + cdp.mjs + RESULTS.md） |
| A-F5 renamingWTValue 死态 | nit | **接受事实、保留字段**。issue 明文要求两字段同构；`renamingInstValue` 确为 write-only 残留 | 保留 `renamingWTValue`，代码注释标注「存量死变量模式，等待 tab 侧清理」 |
| A-F6 / 提交后 branch 徽标短暂回落库值 | nit | **接受**。已核实 `List()` 的 branch 是 live `git rev-parse` 值而 PATCH 响应是落库值 | `commitWTRename` 成功路径在 renderSidebar 后追加一次 `refresh()` resync |
| B-F1 blur 语义矛盾（parity vs 失焦回滚） | major | **接受**。已核实 tab 真行为是失焦提交（`onblur="commitRename(...)"`、非空即 PATCH）；issue 加粗主诉求为「与实例 tab 改名完全一致」 | 新增决策 H：parity 获胜——非空失焦=提交，空值失焦/Esc=回滚无 PATCH；§4 row 1 与 §5-5 按此改写 |
| B-F3 CHANGELOG 措辞陷阱 + "T5/T6" 幽灵引用 | minor | **接受**。已核实 `TestTerminalStatusChangelogCount` 的全局句式约束与 `TestResponsiveLayerChangelogCounts` 的 per-layer 体例 | T5 明令禁用 `Pinned by N` 句式并给出安全措辞规则；§3 引言 "T5/T6" → "T4/T5" |
| B-F4 T3 验收耦合 T4 | nit | **接受** | T3 验收改为独立可执行（build + 既有测试 + 人工清单），T4 绿作为 T4 自身验收 |

### R2 确认（双 APPROVE，计划定稿）

两评审对修订后计划均 APPROVE。评审 A 余两条 nit，由仲裁者直接修复（§6.4）：
- N1（nit）：T3「文件」行重复列 index.html → 已合并为一行加「(含 CSS)」。
- N2（nit）：T1 版本保护用例括号说明过强（「重试路径不被误触发」不可证伪）→ 已改为「Version bump 已区分两条落盘路径」。
