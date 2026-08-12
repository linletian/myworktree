# 分支落后检测 — 需求文档

> 版本：1.0  
> 日期：2026-05-26

---

## 1. 背景

myworktree 是一个基于 git worktree 的多工作区管理工具。用户在日常开发中会在同一个仓库下创建多个 worktree，每个 worktree 对应一个独立的功能分支。随着时间推移，主分支（main/master）或集成分支（develop）可能已经包含新的提交，而用户的 worktree 分支可能基于旧版本创建，导致分支落后。

在创建新 worktree 时，如果 base 不是最新的主分支或集成分支，后续合并可能产生不必要的冲突。用户需要一种直观的方式来随时了解每个 worktree 分支是否落后于上游。

---

## 2. 需求

1. 在 Web 前端左侧 worktree 列表中，每个 worktree 分支名旁边用**红色文字标签**简洁标识是否落后于主分支（main/master）或集成分支（develop）
2. 标签格式如 `m↑3`（主分支领先 3 个 commit）、`d↑1`（develop 领选 1 个 commit）
3. 如果当前分支就是主分支自身，不显示任何标记
4. 如果当前分支就是 develop 自身，仍检查是否落后于主分支
5. 本地和远端取**领先更多的那个**作为 effective head 进行比较
6. 远端优先使用 `origin`，没有 `origin` 则取其他 remote 中领先最多的，没有远端则仅用本地
7. 标记**常驻显示**，60 秒定时刷新；当用户切换 worktree 时立即刷新

---

## 3. 需求分析

### 3.1 核心判定逻辑

只用一个 git 命令即可完成判定：

```
git rev-list --count <当前分支HEAD>..<上游effective_head>
```

- 结果 `> 0` → 上游领先，显示标记
- 结果 `= 0` → 无落后，不显示标记

相比于使用 `git merge-base --is-ancestor` 额外判断，这种方式一步完成，更简洁。

### 3.2 分支关系

```
检查方       被检查方
─────────────────────
普通分支  →  主分支 (必须)
普通分支  →  develop (如果 develop 存在且当前分支不是 develop)
develop  →  主分支 (必须)
主分支    →  无 (不显示标记)
```

### 3.3 effective head 计算

对每个上游分支（main 或 develop）：

1. `git rev-parse <branch_name>` → 本地 head
2. 远端发现：
   - 优先 `git rev-parse origin/<branch_name>`
   - 不存在则遍历 `git remote` 列表，取 ahead 最多的 remote tracking branch
   - 没有任何远端匹配 → 仅使用本地 head
3. `git rev-list --left-right --count <branch_name>...<remote_tracking>` → 取 `ahead` 值
4. 本地和远端中，取 `ahead` 更多的一方作为 effective head

### 3.4 刷新策略

| 触发条件 | 接口 | 说明 |
|---------|------|------|
| 页面初始加载 | 批量接口 | 随 refresh() 一起调用 |
| 定时刷新 (60s) | 批量接口 | 低频轮询，避免频繁 git 操作 |
| 用户切换 worktree | 单条接口 | 立即获取最新状态 |

---

## 4. 基础规划

### 4.1 后端 API

**批量接口**：`GET /api/worktrees/diverged`

遍历所有 worktree，对每个分支执行分支关系判定，返回聚合结果。

**单条接口**：`GET /api/worktree/diverged?id=<worktree_id>`

根据指定 worktree 执行判定，用于切换 worktree 时的即时刷新。

**新增后端模块**：`internal/gitx/diverged.go`

封装三个核心工具函数：
- 计算分支的 ahead 数量
- 计算分支的 effective head（本地与远端中更领先的一方）
- 发现远端 tracking branch（优先 origin，其次其他 remote）

### 4.2 前端 UI

**渲染位置**：在 `renderSidebar()` 中，每个 worktree 行的分支名字符串后，追加红色标签。

**HTML 结构**：一个内联的 `<span>` 标签组，每个标签带 `title` 属性提供 tooltip 说明文字。

**CSS 样式**：10px 字号，红色 (`--danger-text`)，与分支名字号形成对比但不过分突出。

**状态管理**：在全局 `state` 中新增 `diverged` 字段存储每个 worktree 的检测结果，由刷新函数异步更新，`renderSidebar()` 渲染时读取。

**交互**：切换 worktree 的 `selectWorktree()` 函数中追加单条接口调用。

### 4.3 涉及文件

| 文件 | 操作 | 内容 |
|------|------|------|
| `internal/gitx/diverged.go` | 新增 | 核心 git 查询函数 |
| `internal/app/app.go` | 修改 | 注册 2 个新路由 + handler |
| `internal/ui/static/index.html` | 修改 | CSS 样式 + JS 刷新逻辑 + 渲染 |

---

*本文档确认后进入实现阶段。*
