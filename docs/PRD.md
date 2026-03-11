# myworktree — PRD (v0.1)

> 定位：单人使用的 **git worktree + coding CLI instance** 管理框架；提供 Web UI 做管理与输出回放；默认本机安全运行，可选远程访问（内置 HTTPS + Token）。

## 1. 背景
在同一项目中并行多个 AI coding 任务时，常见痛点：
- 多任务共享一个工作区会相互污染（分支/依赖/临时改动）
- 同一任务往往需要多个终端/agent 并行（改代码/跑测试/查询/Review）
- 需要把“正在运行的 CLI”统一可视化、可重连、可回放

## 2. 目标（Goals）
1. 基于当前项目 git 主工作区管理多个 **worktree**（隔离目录）。
2. 每个 worktree 下可启动多个 **instance**（独立终端/CLI 进程）。
3. 前端 UI 可关闭/刷新/断网：后端 instance **持续运行**；重连可列出全部 worktree 与 instance，并支持输出回放。
4. 支持 **Tag（启动模板）**：启动命令、preStart 脚本、env、cwd。
5. 单用户远程访问：非 loopback 必须认证；可选内置 HTTPS。
6. 预留扩展为 MCP server 的接口形态。

## 3. 非目标（Non-Goals）
- **不解决**同一 worktree 内多 instance 并发写文件导致的冲突/竞态。
- 不处理任何项目具体内容：不解析代码、不做索引、不做质量分析。
- 不管理非 myworktree 启动的进程/终端实例。
- 不做多人协作权限体系。

## 4. 术语
- **Worktree**：`git worktree` 创建的独立工作区目录。
- **Instance**：myworktree 托管启动的一个进程（通常运行 zsh + 某个命令）。
- **Window**：前端对 instance 的渲染视图；Window 关闭不影响 instance。
- **Tag**：启动模板（command/env/preStart/cwd）。
- **Labels**：管理标签（键值对元数据），用于搜索/过滤/分组，不影响启动行为。

## 5. 关键规则
- 后端必须保持：worktree 与 instance 的生命周期独立于前端。
- 删除 worktree：若 `git status --porcelain` 非空（含 untracked），**拒绝删除**。
- 分支命名：
  - 默认：创建 worktree 使用分支 `mwt/<slug>`（不再是 `wt/*`）。
  - 自定义分组：当用户在 task description 里直接输入 `<group>/<name>`（例如 `feature/auth`）时，分支名就是 `<group>/<name>`，不会再额外加前缀。
  - 命名冲突：如目标分支已存在，自动给 `<name>` 加 `-2/-3` 后缀避免冲突；并支持将既有 worktree **纳入管理（import）**。

## 6. 安全
- 默认只监听 `127.0.0.1`。
- 监听非 loopback（例如 `0.0.0.0` 或局域网 IP）时：必须提供 `--auth`。
- 可选内置 HTTPS：`--tls-cert/--tls-key`。
- 日志/回放脱敏：
  - env 键名包含 `TOKEN/SECRET/KEY/PASSWORD` 的值写入状态时替换为 `***`。
  - 输出回放中按模式脱敏主流 AI key（如 `sk-***`）。

## 7. 当前实现状态（与愿景差异）
- 已实现：worktree/instance 管理、Web UI、API、输出回放、脱敏、认证与可选 HTTPS、MCP tools 列表接口。
- 规划增强：将 instance 升级为真正 PTY 并通过 WebSocket 实现交互式 Web TTY（xterm.js）。Web TTY 的终端缓冲区、主题、字体等参数由前端灵活配置，以适应不同的调试和使用场景。

## 8. 验收标准（MVP）
- 可创建/列出/删除 worktree（dirty 删除被拒绝）。
- 可启动/列出/停止 instance，且前端关闭后 instance 仍继续运行。
- UI 重连可看到所有已管理对象，并能回放 instance 近期输出。
- 非 loopback 无 `--auth` 时拒绝启动；输出回放对 `sk-...` 做脱敏。
