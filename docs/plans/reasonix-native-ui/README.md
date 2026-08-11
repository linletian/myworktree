# reasonix-native-ui — 文档索引

本目录承载「在 myworktree 中集成 reasonix(https://github.com/esengine/DeepSeek-Reasonix)官方 web UI」这一特性的调研与设计文档。

> **修订记录（2026-08-10 整合）**：初版调研由外部整理；随后在 myworktree 仓库内对 reasonix 源码做了二次核实（`internal/serve/` 路由与 auth、`internal/cli/serve_frontend.go`、`index.html`），修正了初版 **2 处事实错误**（desktop 壳是 **Wails** 非 Tauri；token 鉴权走 **Cookie/query** 而非 Bearer header）和 **2 处表述偏差**（端口应以 `--port-file` 为准而非 stdout 正则；vanilla HTML 的根相对 fetch 在反代子路径下仍需**轻量 URL 改写**），并补充了初版未覆盖的问题：**布局（侧栏 220px 固定不可折叠）与注入方案**、session lease 并发、bwrap sandbox 依赖、`/provider-setup` 页 CSP 障碍、托管 flag（`--port-file/--token-file/--pid-file/--behind-proxy`）、reasonix 无 `?directory=` 概念（会话按启动目录隔离）。修订点均在 `FEASIBILITY.md` 中以「整合」标记。

## 阅读顺序

| 文档 | 回答什么问题 | 何时读 |
| --- | --- | --- |
| **`FEASIBILITY.md`** | reasonix 是什么、能不能嵌入、为什么选方案 A 不选 B/C、与 opencode-native-ui 复盘的关系、布局问题的注入方案（§9）、推荐实施路线 | 想理解背景与决策 |

## 摘要(30 秒读完)

- **目标**:把 reasonix AI coding agent 的官方 web dashboard 嵌入 myworktree 实例面板,与现有 PTY/xterm 实例并列,支持在 worktree 里直接与 reasonix agent 对话(取代"开 PTY 跑 reasonix TUI"的体验)
- **调研结论**:**技术路线完全可行,且比想象中简单**。reasonix CLI 提供 `reasonix serve`(带 token/password 鉴权)和 `reasonix web`(本地浏览器模式),web UI 是 Go `//go:embed` 的 vanilla HTML + SSE,**不是** opencode 那种 Solid.js SPA,也不是 reasonix 自家 desktop 的 React+Wails 壳
- **关键技术事实**:
  - reasonix web server 在 `internal/serve/`,纯 `net/http`,路由清晰(尤其 `GET /sessions/{id}` 是显式会话路径)
  - 流式协议是 **SSE**(`GET /events`),与 myworktree 现有 SSE 降级通道天然兼容
  - 鉴权有三种模式(`none/token/password`),token 校验走 **Cookie `reasonix_token` / `?token=` query**(无 Bearer header 逻辑);可用 `--token-file` 让宿主注入统一 token
  - **托管 flag**(整合补充):`--port-file`(实际端口权威来源,端口冲突自动 +1 仍准确)、`--token-file`(须 chmod 600)、`--pid-file`、`--behind-proxy`——为 myworktree 这类宿主预留
  - **布局问题**(整合补充):侧栏固定 220px 且桌面不可折叠,需反代 + 注入折叠解锁,详见 FEASIBILITY §9
- **与 opencode-native-ui 分支的关系**:该分支的 **`framework.Kind` 抽象设计可直接复用**,但其 reverse proxy + 三层 SPA shim 的具体实现**几乎不能复用**(理由:opencode 是 Solid.js SPA + base64(dir) 路由 + 双端口 + CSP,reasonix 是 vanilla HTML + 显式 /sessions/{id} + 单端口 + 无 CSP)。**注意**:reasonix 的 fetch 是根相对路径,反代挂子路径仍需**单层轻量 fetch/EventSource 前缀改写**(~15 行,远轻于 opencode 三层 shim)
- **三种方案的取舍**:
  - 方案 A(iframe + sidecar `reasonix serve`,反代同源):推荐,理由见 FEASIBILITY §5.4 决策矩阵
  - 方案 B(MCP-only):不推荐,`reasonix serve` 已可用,UI 体验更直接
  - 方案 C(深度 embed):不推荐,需给 reasonix 提 PR 或维护 fork,工程量与收益不匹配

## 当前状态

- [x] 调研(本目录,含 2026-08-10 整合修订)
- [x] 关键项实测(2026-08-10,`feature/reasonix-native-ui-verify` 分支:session lease 并发 / 端口行为 / SSE keepalive / token-cookie 注入,详见 FEASIBILITY §8)
- [x] 决策(**已批准 2026-08-10**):采纳方案 A(反代 + iframe);凭据继承 = REASONIX_HOME 隔离 + symlink 继承;MVP 不含侧栏折叠
- [x] 实施(**已完成 2026-08-11**,`feature/reasonix-native-ui` 分支):MVP(创建 reasonix 实例 → `/rx/<id>/` 反代 iframe → 端到端跑通)。代码评审(多轮 R/N/T + 运行问题修复)已逐条核实并按 MVP 边界修复;未决项已全部转 issue **#43~#49** 并记录于本目录 `DEFERRED.md` 追溯索引(评审底稿为一次性工作文档,已按要求删除,不进入版本库)。实施内容:
  - `internal/instance/reasonix/`(driver:隔离 REASONIX_HOME + symlink 继承 + 跨平台探活)
  - `internal/app/reasonix_proxy.go`(`/rx/<id>/` 反代:token Cookie 注入 + URL 前缀改写 + SSE 透传)
  - `Manager` kind 分支(Start/Stop/Restart/Delete + Reconcile 探活保 running)
  - 前端 iframe 渲染 + 创建入口 checkbox + tab 徽标
  - 文档:docs/API.md §5.10、ARCHITECTURE.md §3.2/§4.2、PRD §7 已同步
- [x] 未决项全部处理(**2026-08-11**):#44 独立源(安全)、#48 侧栏默认折叠布局注入、#46 driver 缓存、#47 Delete 锁范围、#45 版本门 + serve.log 报错 + Cookie 单点、#43 测试隔离、DEFERRED §3 env/preStart 注入;`#49` 经用户确认保持「Restart = 新 session」现状语义后关闭。全部 7 个 issue 已关闭,详见 `DEFERRED.md` §6/§7。
