# myworktree — PRD (v0.1)

> 定位：单人使用的 **git worktree + coding CLI instance** 管理框架；提供 Web UI 做管理与输出回放；Portal 仪表板全局入口，支持跨仓库运行实例自动发现；默认本机安全运行，可选远程访问（全局 Token、Portal 反向代理、Tailscale HTTPS）。

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
7. **agent 实例与终端语义一致**：`kind=reasonix` 等 agent 实例的会话/历史按项目（worktree）组织，同一项目内与终端直接运行的 agent CLI/TUI **互通**（共享同一会话池，可互相切换）；myworktree 仅做工作区与实例管理，**不介入 agent 的会话/项目/分支产品逻辑**。

## 3. 非目标（Non-Goals）
- **不解决**同一 worktree 内多 instance 并发写文件导致的冲突/竞态。
- 不处理任何项目具体内容：不解析代码、不做索引、不做质量分析。
- 不管理非 myworktree 启动的进程/终端实例。
- 不做多人协作权限体系。
- **不实现 agent 的会话/项目/分支管理**：项目隔离、历史会话切换、会话并发（lease 争用）等语义全部由 agent 自身负责（如 reasonix 按 cwd 组织 `~/.reasonix/projects/<slug>/sessions`）；myworktree **不复制、不隔离、不代管** agent 会话状态。

## 4. 术语
- **Worktree**：`git worktree` 创建的独立工作区目录。
- **Instance**：myworktree 托管启动的一个进程（通常运行 zsh + 某个命令）。
- **Window**：前端对 instance 的渲染视图；Window 关闭不影响 instance。
- **Tag**：启动模板（command/env/preStart/cwd）。

## 5. 关键规则
- 后端必须保持：worktree 与 instance 的生命周期独立于前端。
- 删除 worktree：若 `git status --porcelain` 非空（含 untracked），**拒绝删除**。
- 分支命名：
  - 分支名直接使用用户在表单中输入的值（手动输入或 LLM 生成）。
  - LLM 模式：调用 LLM 将任务描述转换为分支名（如 `fix/auth-bug`），保留 `/` 分组前缀（如 `feature/auth` → `feature/auth`）。
  - 自定义输入：用户在 Branch Name 输入框直接输入时，直接使用该值作为分支名。
  - 命名冲突：如目标分支已存在，自动给 `<name>` 加 `-2/-3` 后缀避免冲突；并支持将既有 worktree **纳入管理（import）**。
- **agent 实例语义边界**：agent 实例（如 `kind=reasonix`）的会话数据归属 agent 自身状态根（默认 `~/.reasonix`），myworktree **不设独立 `REASONIX_HOME`、不绑定固定会话文件**来改变 agent 语义；实例生命周期（启停/重启/删除）只作用于 serve 子进程，**不改动共享会话池**。并行冲突由 agent 自身的 session lease 机制处理（拒绝式，不静默双写），myworktree 不额外加锁。**opt-out**：driver 会从 serve 环境剥离继承的 `REASONIX_HOME`/`REASONIX_STATE_HOME`（确保走真实 `~/.reasonix`），如需自定义 home，用实例 tag 的 `env` 显式设置即可（剥离后追加，后值生效）。

## 6. 安全
- 默认监听 `0.0.0.0:0`，自动选择端口并持久化。
- 启动时若未通过 `--auth` 显式提供 Token，则自动生成 32 位随机 Token 并持久化到 `auth.json`（0o600 权限），确保认证始终启用。
- 可选内置 HTTPS：`--tls-cert/--tls-key`。
- **Portal 端口绑定**：Portal 仪表板可绑定到 `0.0.0.0`，用户通过 LAN IP 或 Tailscale 域名访问。非 loopback 访问 Portal 时须通过 `mw_token` Cookie 认证。
- **全局 Token（HttpOnly Cookie + CSRF）**：全局 Token 存储在 `~/.config/myworktree/auth.json`（0600 权限），通过 `mw config` 交互式配置。Portal 仪表板使用 HttpOnly Cookie（`mw_token`）传输 Token——JS 不可读取，防止 XSS 窃取。登录/登出端点采用 double-submit cookie 模式做 CSRF 防护。
- **速率限制**：实例端认证失败限流 20 次/分钟/IP；Portal 端 CSRF token 生成限流 1 req/s/IP，认证尝试限流 20 次/分钟/IP。
- **Tailscale HTTPS**：~~当 Tailscale 可用时，Portal 持有者自动配置 `tailscale serve` 提供 `https://<machine>.ts.net` 域名访问（Let's Encrypt 证书）。~~ **（暂未启用）** 在 macOS 下测试发现 tailscale 1.98 CLI 的 `serve` 命令存在 bug：`tailscale serve --bg <port>` 返回成功但实际未配置代理、`tailscale serve status` 始终报告 No serve config。相关代码（`tailscaleServeLoop`、`ensureTailscaleServe` 等）已封存不调用，待 tailscale 修复后恢复。Tailscale WireGuard 隧道本身提供网络层加密，仍可通过 `http://100.x.x.x:PORT` 安全访问。
- 涉及宿主机图形界面的快捷动作（例如从侧栏直接打开 Terminal / Finder）只在浏览器通过 `127.0.0.1` / `localhost` 访问时展示；远程访问时隐藏，避免误导用户在远端会话里触发本机 GUI 行为。
- 对应后端接口也强制仅接受 loopback 客户端请求，不能只依赖前端隐藏来形成安全边界。
- Loopback 判断仅支持 IPv4（`127.x.x.x` / `localhost`）；IPv6 地址（含 `::1`）不被识别为 loopback，需走完整的 Token 认证流程。
- 日志/回放脱敏：
  - env 键名包含 `TOKEN/SECRET/KEY/PASSWORD` 的值写入状态时替换为 `***`。
  - 输出回放中按模式脱敏主流 AI key（如 `sk-***`）。

## 7. 当前实现状态（与愿景差异）
- **Reasonix web chat 实例（MVP 完结，含需求修订 2026-08-12）**：`kind=reasonix` 实例在 worktree 内运行 `reasonix serve` 子进程，其 web 聊天界面经反代 `/rx/<id>/` 以 iframe 嵌入实例标签页（创建实例时选择 *Reasonix* 标签页，仅需名称——启动命令固定为 reasonix serve；tag 的 env/preStart 仍可经 API/CLI 的 tag_id 注入，command 始终忽略）。
  - **语义基线（需求修订 2026-08-12，见 §2 Goal 7 / §5 关键规则）**：实例 = 在该 worktree 项目里打开 reasonix 的 web UI。项目间隔离由 reasonix 自身按 cwd 组织（`~/.reasonix/projects/<slug>/sessions`）；同一项目的全部历史会话（含终端直接跑 reasonix CLI/TUI 产生的）在实例侧边栏**可见、可切换**，与终端行为一致。myworktree 只负责 worktree/实例生命周期与 `/rx/<id>/` 反代，不介入 agent 的会话/项目/分支语义。
  - **实现状态（2026-08-12 已按新基线实现）**：已取消 `REASONIX_HOME` 隔离与固定 `--resume` 会话文件，serve 直接使用 `~/.reasonix`（与终端运行一致），会话按项目由 reasonix 自身组织、同项目历史会话（含终端产生的）在实例侧边栏可见可切换，`#56` 期望满足。`#49`「Restart = 全新会话」语义保留（重启开新会话，历史仍在共享会话池中可切换）。
  - 已实现且不变：安全加固（#44 独立源跨源隔离）、侧栏默认折叠布局注入（#48）、生命周期/性能（#46 缓存、#47 锁范围）、driver 健壮性（#45 版本门 + serve.log 报错）、测试隔离（#43）；`env`/`preStart` 注入已支持（DEFERRED §3）。详见 `docs/plans/reasonix-native-ui/`。
- 已实现：worktree/instance 管理、Web UI、API、输出回放、脱敏、认证与可选 HTTPS、MCP tools 列表接口。
- 已实现：侧栏主工作区/各 worktree 项提供两个快捷入口，可一键在宿主机打开对应目录的 Terminal（zsh）与 Finder 窗口，便于在 Web UI 与本机 GUI/CLI 间快速切换。
- **已实现 Git Changes 面板暂存/未暂存分离**：侧栏底部 CHANGES 区域拆分为 Staged 和 Unstaged 两个互锁折叠 section。默认展开 Unstaged，点击任一标题栏展开当前 section 并自动折叠另一个。每个 section 独立显示暂存/未暂存的文件列表和行数汇总。
  - 后端 `/api/worktree/status` 并发执行 `git diff --cached --numstat`（暂存）和 `git diff --numstat`（未暂存），复用同一解析器，响应中分别返回 `staged` 和 `unstaged` 两个字段。
- **已实现 PTY + Web TTY**：instance 通过 PTY 启动，支持真正的交互式终端（vim/htop/less 等 TUI 程序）。
  - WebSocket 握手协议：服务端发送 `{"type":"ready"}`，客户端等待后发送 resize 开始数据流。
  - 窗口尺寸传递：前端监听窗口 resize 并通知后端 PTY，确保 TUI 程序正确重绘。
  - 智能重绘：前端在收到第一条数据后延迟 50ms 再次发送 resize，确保 TUI 完整刷新。
   - 超时降级：客户端若在 5 秒内未收到 WebSocket `ready` 握手消息，则自动关闭 WebSocket 并回退到 SSE 方案。
  - 运行中的实例在前端按实例维护各自的终端会话；切换标签时隐藏非活动终端，而不是强制断开其 PTY 连接。
  - 终端配置：Web TTY 的缓冲区（scrollback）、主题、字体等参数由前端灵活配置，以适应不同的调试和使用场景。
- **opencode-web 实例类型**（已实现；设计详见 `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md`；威胁模型与评审检查表见 `docs/ARCHITECTURE.md` §8）—— 跳过 PTY/xterm.js，直接内嵌 opencode 官方 web UI。每个 opencode-web instance = 1 个独立的 `opencode serve` 进程（命令、`--hostname 127.0.0.1`、`--port 0` 由 myworktree 硬编码；`OPENCODE_SERVER_PASSWORD = cfg.AuthToken`——统一认证 token，全部实例共享，**不允许关闭**）。非安全 env 通过 `tag.Env` 合并。浏览器通过 `/__opencode/<id>/` iframe 加载 opencode **完整页**（HomeRoute；反向代理路径 `/__opencode/<id>/*`，myworktree bearer token 校验 → Go 端注入 Basic auth + `?directory=<worktree>`）。为防跨 worktree 误操作：proxy 层监测请求 directory 并记录越界状态（越界时 iframe 上方常驻警告），注入脚本隐藏跨 worktree 切换入口（项目/目录切换 + server 切换/添加，仅保留当前 worktree 入口），并以 `opencode --version` 版本门 + DOM 锚点检测兜底隐藏失效。前端按 instance kind 分支：PTY 仍显示 xterm.js，opencode-web 显示 iframe。多 session 切换复用 myworktree instance tab。
- **dsh-web 实例类型**（已实现；设计详见 `docs/plans/dsh-native-ui/FEASIBILITY.md`，实施计划见同目录 `PLAN.md`/`TASK.md`；威胁模型见 `docs/ARCHITECTURE.md` §9）—— 内嵌 DeepSeek Harness 官方 web UI（`dsh web` = 无头 HTTP server + 内嵌 SPA，形态与 `opencode serve` 同构）。每个 dsh-web instance = 1 个独立的 `dsh web --patch <restrict.yml> --host 127.0.0.1 --port 0` 子进程（launcher 选项必须前置，见 PLAN.md §踩坑 11）（命令与 flags 由 myworktree 硬编码，**恒带 `--port 0`**——dsh 端口冲突无回退、fail-loud）。**数据面**：共享 `DSH_HOME`（凭据/设置/profiles 免重配），restrict overlay 仅重述 `storage-json.root` → `<dataDir>/dsh/<worktree-hash>/storages`（工作区注册表按 worktree 隔离），sessions 保持共享（终端同 cwd 裸跑 dsh 与 web 实例会话**可见、可打开、可读快照**——实时刷新仅限写入者进程，跨进程打开活跃会话会损坏日志，属 dsh 上游问题，见 `docs/plans/dsh-native-ui/CROSS-PROCESS-SESSION.md`；跨 worktree 会话以「未分组」组可见不可点）。**嵌入形态**：每实例一个独立 loopback origin（SPA API base 硬编码 `location.origin + '/api'`，同源子路径挂载不可行），iframe src = `http://127.0.0.1:<proxyPort>/`；代理删 Origin、Host 设上游、透传 WS Upgrade（事件通道 `/api/events.mux`、`/api/events.host`）。**工作区自举**：就绪后直连上游打 `workspace.create {path:<worktree>}`（幂等 adopt，失败仅告警）。**防误操作**：restrict overlay 停用 `directory-picker` 自动组合器并裸挂 `-browse` host 后端行（保留 api-gateway 依赖的 `directoryPicker` 服务、不挂 client 表面 → Add workspace 入口不渲染；实施修正见 PLAN.md §踩坑 12）；代理观察 `session.create {cwd|workspaceId}` / `workspace.create {path}` body，越界只 Record 不拦截，iframe 外常驻警告条三态；`dsh --version` 版本硬门（< 0.1.0 fail-fast）+ advisory 区间 `[0.1.0, 0.2.0)` + Spawn 时 `--dump-config` 校验 overlay 生效（`overlay_verified`）兜底行 id 漂移。**缺失依赖**：未装 dsh 时前端对话框三选（npx 启动 `@deepseek-ai/dsh@<pin>`（Setpgid 杀进程组）/ 立即安装 `npm install -g` 后绝对路径 Spawn / 取消）。**远程访问**：非 loopback 场景代理绑主监听同 host 并强制 myworktree token 门（dsh 无认证面）。
- 规划增强：**Portal Dashboard MVP**（已实现）：
  - 全局 Token 配置（`mw config` 交互式引导，`~/.config/myworktree/auth.json`，`0o600` 权限）
  - Portal 仪表板（共享入口端口，自动发现所有仓库的运行实例，HttpOnly Cookie 认证，CSRF 防护）
  - ~~反向代理（通过 Portal 统一入口访问各实例，解决跨域 Cookie 问题，支持 WebSocket）~~ **（暂未实现）**：计划通过 `/s/<repo-hash>/` 路径代理到对应实例端口，当前仪表板链接直接指向实例端口
  - ~~Tailscale Serve 自动管理（自动配置 `tailscale serve` 提供 `https://<machine>.ts.net` 域名访问）~~ **（暂未启用，见 §6 安全说明）**
  - 双层认证架构（Portal 层 Cookie + CSRF，实例层 loopback 绕过）
- 浏览器关闭保护：前端在 `beforeunload` 事件时，无论是否存在运行中实例，均触发浏览器原生确认对话框，防止误操作关闭页面。
- **Main workspace 分支查询**：`GET /api/main` 返回 `{name, branch, github_url}`。branch 字段实时查询（`git rev-parse --abbrev-ref HEAD`），在 detached HEAD 场景（如 CI 浅克隆）下返回空字符串而非错误。github_url 字段基于 `git remote` 推算（优先 origin，回退到 `git remote` 列表），规范化 SCP / HTTPS / ssh:// 三种形式，仅识别 `github.com`，返回 `https://github.com/<owner>/<repo>`，其它情况返回空串。
- **已实现侧栏主工作区 GitHub 链接**：侧栏主工作区项目名右侧条件渲染 GitHub 图标，链接到 `state.mainRepo.github_url`（来源：`/api/main` 的 github_url 字段，由 `gitx.GitHubURL` 计算）。点击在新窗口打开，使用 `<a target="_blank" rel="noopener noreferrer">` 并通过 `event.stopPropagation()` 避免触发 `selectWorktree`。非 GitHub remote、无 remote 或解析失败时不渲染图标。仅识别 `github.com`，不含 GitHub Enterprise。
- **已实现：instance PTY 日志内存化（彻底取消磁盘日志）**：
  - 旧实现：每条 PTY chunk（1024 字节）写入 `logs/<instanceId>.log`，并在文件达到 10MB 后做"读 10MB + 截断 + 写 10MB"，造成约 10 万倍磁盘写放大；OpenCode 等 TUI 高频重绘场景下数据目录写入量可达 280MB+。
  - 新实现：每个 running instance 拥有一个进程内的 **有界 ring buffer**（`internal/instance/logbuf.go`）。HTTP/SSE/WS 日志回放端点与 MCP `instance_log_tail` 全部从该 buffer 读取，磁盘 I/O 完全消除。
  - 容量策略（与 bounded-but-adaptive 原则一致：硬上限防 OOM、富裕时适度放大）：
    - 单实例下限 16 MB、默认 32 MB、上限 256 MB（硬天花板，永不超过）。
    - 自适应：未配置时 `clamp(available_memory / 16, 16 MB, 256 MB)`；用户可通过 `~/.config/myworktree/auth.json` 的 `log_buffer_bytes` 字段覆盖。
    - 全局预算：所有 live buffer 容量合计上限为系统 RAM 的 25%。
  - 当新实例会突破全局预算时，`POST /api/instances` 返回 `503 Service Unavailable` + 结构化 `log_buffer_budget_exceeded` body，UI 弹出专用对话框提示已用/上限字节并给出处置建议。
  - 守护进程启动时，`Manager.PurgeOrphanLogFiles()` 一次性清理旧版本遗留的 `.log` 文件（幂等）。
  - **破坏性 schema 变更**：`state.json` 的 `log_path` 字段已移除；旧文件继续可加载（被 JSON 解码静默忽略），外部读取 `state.json` 的工具应去掉对该字段的依赖。
  - 守护进程重启会清空所有 buffer，与既有的 `ReconcileRunningOnStartup` 语义一致（重启后日志原本就不能回放，运行中实例会被标记为 stopped）。
  - 详情见 `docs/ARCHITECTURE.md` §4.1、`docs/API.md` *Start* 端点错误段。
- **规划新增：分支落后检测**：
  - 侧栏每个 worktree 分支名旁显示红色标签（如 `m↑3` / `d↑1`），标识当前分支是否落后于主分支或集成分支 develop。
  - 如果当前 worktree 就是主分支自身，则不显示标记。
  - 判断逻辑：计算当前 HEAD 到上游 effective head（本地和远端中更领先的一方）的 ahead 数量，结果 `> 0` 即落后。
  - 远端发现优先 `origin`，其次取其他 remote 中领先最多的；无远端则仅用本地判断。
  - 标签常驻显示，60 秒定时刷新；切换 worktree 时立即刷新。
  - 详情见 `docs/plans/git-commit-history-graph/DESIGN.md`。

## 8. 验收标准（MVP）
- 可创建/列出/删除 worktree（dirty 删除被拒绝）。
- 可启动/列出/停止 instance，且前端关闭后 instance 仍继续运行。
- UI 重连可看到所有已管理对象，并能回放 instance 近期输出。
- 本机访问 Web UI 时，可从侧栏一键打开所选主工作区/worktree 的 Terminal 与 Finder；远程访问时不展示这两个快捷入口。
- **Portal Dashboard MVP**：
  - `mw config` 交互式引导可完成全局 Token 的配置、查看（掩码）、清除、重新生成
  - `mw start --listen 0.0.0.0:0` 自动启动 Portal 仪表板，多实例中仅一个持有 Portal 端口
  - Portal 仪表板可通过 LAN IP 和 Tailscale 域名访问，显示所有运行实例并可点击跳转
  - Cookie 认证流程：获取 CSRF token → 提交 auth → 获得 HttpOnly Cookie → 访问实例列表
  - ~~实例通过 Portal 反向代理访问时，loopback 请求自动绕过实例端 auth 中间件~~ **（暂未实现）**
  - ~~反向代理支持 WebSocket 升级转发~~ **（暂未实现）**
  - ~~Tailscale 可用时自动配置 `tailscale serve`，提供 HTTPS 域名访问~~ **（暂未启用）**
  - Portal 持有者崩溃后，其他实例在 10~15 秒内完成故障转移接管
## 9. LLM 智能分支命名

在 Create Worktree 时，可选使用 LLM 将任务描述转换为简洁、规范的分支名。

### 9.1 模式选择
- **正则模式**（默认）：使用 `slugify()` 正则转换，不调用任何 LLM API
- **LLM 模式**：调用 LLM API（支持 OpenAI / Anthropic 两种协议，由配置决定）

### 9.2 配置方式
配置文件：`~/.config/myworktree/config.json`（0o600 权限），示例：
```json
{
  "protocol": "openai",
  "api_address": "<provider_api_address>",
  "api_key": "<api_key>",
  "model": "<model_name>"
}
```

支持的 protocol：
- `openai`：OpenAI API 格式（适用于 OpenAI 及 DeepSeek 等兼容 OpenAI 格式的服务）
- `anthropic`：Anthropic API 格式（适用于 Anthropic 及 DeepSeek 的 Anthropic 格式端点）

同时支持环境变量（优先级更高）：`OPENAI_API_KEY` / `ANTHROPIC_API_KEY`。

### 9.3 分支名规范（由 LLM 遵守）
1. 长度不超过 100 个字符
2. 只包含小写字母、数字、连字符和斜杠（用于 git 分支分组，如 `feature/auth`）
3. 符合 git 规范（以字母开头）
4. 使用英文，可包含数字

### 9.4 前端交互
- **AI Generate 按钮**：在创建 worktree 弹窗中，输入任务描述后，点击 "AI Generate" 按钮调用 LLM 生成分支名，填入 Branch Name 输入框。
- **Branch Name 输入框**：始终可见，用户可直接编辑 LLM 生成的结果，或手动输入自定义分支名。
- **LLM Settings 按钮**：位于创建表单左下角，点击打开 LLM 配置对话框（仅在 localhost 或 HTTPS 环境下可见）。
  - 支持配置：协议类型、API 地址、API Key、模型名称。
  - 提供"测试连接"功能验证配置是否正确。
- LLM 调用失败时显示错误提示，用户仍可手动输入或修改 Branch Name 输入框。

### 9.5 资源占用
- 仅在调用 LLM 时产生网络请求，无本地资源占用
- 不开启 LLM 时行为与现有版本完全一致
