# 分支落后检测 — 实施计划

> 基于需求文档：`DESIGN.md`  
> 日期：2026-05-26

---

## 1. 计划概述

本计划描述如何实现"分支落后检测"功能：在侧栏每个 worktree 分支名旁显示红色标签（如 `m↑3` / `d↑1`），标识当前分支是否落后于主分支或 develop 分支持。

### 1.1 设计决策回顾

| # | 决策点 | 结论 |
|---|--------|------|
| 1 | 当前是主分支 | 不显示标记 |
| 2 | 当前是 develop | 仍检查 main 是否领先 |
| 3 | 当前是普通分支 | 检查 main 和 develop 是否领先 |
| 4 | effective head | 取本地和远端中 ahead 更多的 |
| 5 | 远端发现 | 优先 origin → 其次最领先 remote → 无则略过 |
| 6 | develop 不存在 | 跳过，不渲染该区块 |
| 7 | 标记格式 | `m↑3` / `d↑1`，红色，常驻，10px |
| 8 | 刷新策略 | 60s 定时 + 切换 worktree 立即刷新 |
| 9 | 批量 vs 单条 | 初始加载用批量接口，切换用单条接口 |

---

## 2. 文档优先原则

在代码实现之前，必须先更新 `docs/` 下的主文档：

| 优先级 | 文档 | 更新内容 |
|--------|------|---------|
| 1 | `docs/PRD.md` | §7 新增"分支落后检测"功能描述 |
| 2 | `docs/ARCHITECTURE.md` | §2 新增 `internal/gitx/diverged.go` 组件；§3.3 侧栏新增标签说明 |
| 3 | `docs/API.md` | §2 Worktrees 节新增两个 API 端点的文档 |

---

## 3. 实施阶段

### Phase A：后端核心逻辑

**目标**：新增 `internal/gitx/diverged.go`，封装 git 查询函数。

涉及 git 命令：
- `git rev-list --count <HEAD>..<upstream_eff_head>` — 核心判定（结果 > 0 即落后）
- `git rev-list --left-right --count <local>...<remote>` — 计算 ahead/behind
- `git remote` — 发现可用 remote 列表
- `git show-ref --verify refs/heads/develop` — 检测 develop 是否存在

对外暴露的函数：
- 计算指定分支相对上游的 ahead 数量
- 计算分支 effective head（本地与远端取更领先者）
- 发现远端 tracking branch（优先 origin，其次其他 remote）

### Phase B：后端 API

**目标**：在 `internal/app/app.go` 中注册两个新路由并实现 handler。

- `GET /api/worktrees/diverged` — 批量接口，遍历所有 worktree 返回聚合结果
- `GET /api/worktree/diverged?id=<worktree_id>` — 单条接口，用于切换 worktree 时即时刷新

复用现有 `handleWorktreeStatus` 的路径解析逻辑。

### Phase C：前端渲染

**目标**：修改 `internal/ui/static/index.html`。

需新增：
- CSS 样式：`.wt-diverge-badges` / `.wt-diverge-badge`（红色标签，10px）
- `state.diverged` 全局状态字段
- `renderSidebar()` 中追加标签渲染逻辑
- `fetchDiverge()` 批量刷新函数（调用批量接口）
- `fetchDivergeSingle(id)` 单条刷新函数（调用单条接口）

### Phase D：刷新调度

**目标**：将刷新逻辑接入现有事件循环。

- `refresh()` 初始加载中追加批量接口调用
- `setInterval(fetchDiverge, 60000)` 60s 定时刷新
- `selectWorktree()` 中追加单条接口即时刷新

---

## 4. 文件变更清单

| 文件 | 操作 | 内容 |
|------|------|------|
| `docs/PRD.md` | 修改 | §7 新增功能描述 |
| `docs/ARCHITECTURE.md` | 修改 | 新增组件 + 侧栏说明 |
| `docs/API.md` | 修改 | 新增两个 API 文档 |
| `internal/gitx/diverged.go` | **新增** | git 查询工具函数 |
| `internal/app/app.go` | 修改 | 注册 2 路由 + 2 handler |
| `internal/ui/static/index.html` | 修改 | CSS + JS 渲染 + 刷新调度 |

---

## 5. 不包含的内容

- 不实现 commit 历史图形树（原始需求已调整）
- 不实现 mouse hover tooltip
- 不实现闪烁动画
- 不需要前端缓存或离线支持
