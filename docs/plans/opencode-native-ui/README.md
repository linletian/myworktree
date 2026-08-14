# opencode-native-ui — 文档索引

本目录承载「在 myworktree 中集成 opencode 官方 web UI」这一特性的完整设计文档。

## 阅读顺序

| 文档 | 回答什么问题 | 何时读 |
| --- | --- | --- |
| **`FEASIBILITY.md`** | 为什么要做、做了什么调研、为什么选方案 A 不选 B/C、opencode agent 列表加载失败怎么排查 | 想理解背景与决策 |
| **`PLAN.md`** | 怎么落地：改哪些文件、改什么、怎么测 | 准备实施时 |
| **`TASK.md`** *(实施启动后)* | 拆到 PR-ready 的子任务清单，每条都有人能独立 review | 实施 / 拆分任务时 |
| **`FOLLOWUPS.md`** | 评审/调试识别的后续优化项、已知限制、待验证清单 | 想找「还有什么没做/待观察」时 |

## 排障记录（按时间）

| 文档 | 内容 |
| --- | --- |
| **`DEBUG.md`** | 早期集成期 post-mortem（10 个问题，前 8 个已修） |
| **`OPENCODE-WORKDIR-DEBUG-2026-08-13.md`** | 工作目录/会话显示问题：DB 陈旧 `worktree` 字段 → `x-opencode-directory` header 传播链；数据修复 + 注入脚本最终形态（含附录速查表） |
| **`OPENCODE-WORKDIR-DEBUG-2026-08-14.md`** | worktree 删除后 UI 仍显示旧路径、远程/本地/隐私窗口三态不一致：per-origin localStorage 污染 + preseed 仅空时写入；headless Chromium 四场景复现；preseed 自愈修复 |

## 摘要（30 秒读完）

- **现状**：myworktree 对 opencode 的支持是 PTY + xterm.js 跑 opencode TUI，存在 OSC/DA 回声、TUI 重绘 CPU 高、键鼠跨 PTY 往返脆弱等结构性问题（详见 `docs/TERMINAL_FILTER_REVIEW.md`、`docs/GHOSTTY_WEB_RESEARCH.md`、`memory/project_mw_disk_write_issue.md`）
- **方案**：新增 instance 类型 `opencode-web`，每个 opencode-web instance = 1 个独立的 `opencode serve` 进程；Go 端 reverse proxy 把 opencode 官方 web UI 嵌入主面板；前端按 Kind 分支：PTY 仍显示 xterm，opencode-web 显示 iframe；复用现有 tag / 实例生命周期 / tab 切换
- **关键事实**：opencode server 本身是多 directory / 多 session 的（`middleware/workspace-routing.ts:86-88`），server 启动时不绑死 cwd，所以同一 server 可服务多个 worktree；web UI 的「workspace 管理 / 多 server」首页来自 `packages/app/src/pages/home.tsx`，通过深链 `/{base64(dir)}/session` 绕开
- **安全姿态 / 参数锁定**：`opencode-web` 是 myworktree 托管的固定形态——命令、 `--hostname 127.0.0.1`、 `--port 0` 均在 Go 代码里硬编码，用户通过 `tags.json` 改 `Command` 也无效（不实用且会引入 LAN 暴露隐患）。`OPENCODE_SERVER_PASSWORD = cfg.AuthToken`（**统一认证 token**——所有实例共享同一上游密码）和 `OPENCODE_CLIENT=myworktree` 由 myworktree 强制注入/覆盖，不允许关闭。`tag.Env` 中的非安全 env（如 `OPENCODE_EXPERIMENTAL`）经 `BuildEnv` helper 合并。用户真要 LAN/外网访问 opencode web，自己用 PTY 实例或 myworktree 外另起一个 `opencode serve --hostname 0.0.0.0`。reverse proxy + myworktree 全局 token 认证覆盖 `/__opencode/<id>/*` 路径作为深度防御；威胁模型与评审检查表见 `docs/ARCHITECTURE.md` §8
- **不修改 opencode 源码**，**不替换现有 PTY 实例**，**不引入新 Go 第三方依赖**

## 当前状态

- [x] 需求梳理（`FEASIBILITY.md` §0）
- [x] 调研（`FEASIBILITY.md` §1–§2）
- [x] 方案选型（`FEASIBILITY.md` §3，PLAN.md §方案选型）
- [x] 实施计划（`PLAN.md`）
- [ ] TASK 拆分（实施启动时）
- [ ] 实施（待批准）