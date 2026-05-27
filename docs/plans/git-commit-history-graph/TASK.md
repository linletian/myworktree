# 分支落后检测 — 任务清单

> 基于需求文档：`DESIGN.md` 和实施计划：`PLAN.md`  
> 日期：2026-05-26

---

## 文档更新任务

### T-01 更新 PRD.md
**文件**：`docs/PRD.md`  
**内容**：在 §7 当前实现状态中，新增"分支落后检测"功能描述条目  
**依赖**：无  
**验证**：确认 PRD.md 中新增条目描述了此功能及其文档引用

### T-02 更新 ARCHITECTURE.md
**文件**：`docs/ARCHITECTURE.md`  
**内容**：
- §2 High-level components 中新增 `internal/gitx/diverged.go` 组件说明
- §3.3 Main workspace 侧栏中新增分支落后标签的渲染说明
**依赖**：无  
**验证**：确认 ARCHITECTURE.md 中组件列表和侧栏说明已更新

### T-03 更新 API.md
**文件**：`docs/API.md`  
**内容**：在 §2 Worktrees 节末尾，新增以下两个端点的完整 API 文档：
- `GET /api/worktrees/diverged` — 批量接口
- `GET /api/worktree/diverged?id=<worktree_id>` — 单条接口
**依赖**：无  
**验证**：确认 API.md 中已包含上述两个端点的请求/响应格式说明

---

## 代码实现任务

### T-04 新增 gitx/diverged.go
**文件**：`internal/gitx/diverged.go`（新增）  
**内容**：封装以下 git 查询工具函数：
- 计算当前分支相对上游的 ahead 数量（核心判定：`> 0` 即落后）
- 计算分支 effective head（本地与远端取 ahead 更多的一方）
- 发现远端 tracking branch（优先 `origin`，其次最领先的 remote，无则略过）
- 检测 develop 分支是否存在  
**依赖**：无，可独立实现  
**验证**：可通过 `go test` 或集成测试验证 git 命令输出解析正确

### T-05 注册 API 路由
**文件**：`internal/app/app.go`  
**内容**：
- 在 `registerAPIs()` 中注册两个新路由
- 实现 `handleWorktreesDiverged`（批量 handler）
- 实现 `handleWorktreeDiverged`（单条 handler）
- 复用 `handleWorktreeStatus` 中已有的 worktree 路径解析逻辑  
**依赖**：T-04（需要调用 diverged.go 中的函数）  
**验证**：启动服务后用 curl 测试两个端点返回正确 JSON

### T-06 前端渲染逻辑
**文件**：`internal/ui/static/index.html`  
**内容**：
- 新增 CSS 样式：`.wt-diverge-badges` 容器和 `.wt-diverge-badge` 红色标签
- `state` 中新增 `diverged` 字段
- `renderSidebar()` 中读取 `state.diverged`，为每个 worktree 追加标签
- 标签带 `title` 属性提供 tooltip 说明文字  
**依赖**：T-05（需要 API 已就绪才能联调）  
**验证**：页面加载后，落后分支旁出现红色标签 `m↑N` / `d↑N`

### T-07 刷新调度
**文件**：`internal/ui/static/index.html`  
**内容**：
- 新增 `fetchDiverge()` 函数，调用 `GET /api/worktrees/diverged`
- 新增 `fetchDivergeSingle(id)` 函数，调用 `GET /api/worktree/diverged?id=xxx`
- `refresh()` 初始加载中追加 `fetchDiverge()` 调用
- `setInterval(fetchDiverge, 60000)` 60s 定时刷新
- `selectWorktree()` 中追加 `fetchDivergeSingle(id)` 即时刷新  
**依赖**：T-06（渲染逻辑已就绪）  
**验证**：切换 worktree 后标签立即更新；等待 60s 后标签自动更新

---

## 执行顺序

```
T-01 ─┬─ T-04 ── T-05 ── T-06 ── T-07
T-02 ─┤
T-03 ─┘
```

文档更新任务（T-01 到 T-03）之间无依赖，可并行执行，且须在代码实现任务之前完成。代码实现任务（T-04 到 T-07）须严格按序执行。
