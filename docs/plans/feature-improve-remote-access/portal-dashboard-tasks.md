# Portal Dashboard - 任务分解

> 需求来源：[portal-dashboard-plan.md](./portal-dashboard-plan.md)
> 每个任务引用对应计划章节，格式 `§章节号 章节名`，便于工程师追溯需求上下文。

## 任务总览

| 任务 | 依赖 | 预估工时 | 文件 |
|------|------|------|------|
| [Task 0](#task-0-项目主文档更新) | — | 0.5d | `README.md`、`README.zh-CN.md` |
| [Task 1](#task-1-全局-auth-配置) | — | 0.5d | `internal/config/global.go` + `_test.go` |
| [Task 2](#task-2-cli-变更) | Task 1 | 1d | `internal/cli/cli.go` |
| [Task 3](#task-3-app-配置扩展--serverjson-重构) | — | 0.5d | `internal/app/app.go` |
| [Task 4](#task-4-withauth-中间件改造) | Task 3 | 0.5d | `internal/app/app.go` |
| [Task 5](#task-5-portal-核心---抢占注册csrf-清理) | Task 1 | 2d | `internal/portal/portal.go` + `_test.go` |
| [Task 6](#task-6-portal-http-端点--仪表板-html) | Task 5, Task 9 | 1.5d | `internal/portal/portal.go` + `dashboard.html` |
| [Task 7](#task-7-portal-反向代理) | Task 5, Task 6 | 1d | `internal/portal/portal.go` |
| [Task 8](#task-8-tailscale-serve-自动管理) | Task 5 | 1d | `internal/portal/portal.go` |
| [Task 9](#task-9-go-generate-csp-hash-自动计算) | Task 6 | 0.5d | `internal/portal/gen.go` + `csp_gen.go` |
| [Task 10](#task-10-portal-生命周期集成) | Task 4, Task 5 | 0.5d | `internal/app/app.go` |
| [Task 11](#task-11-端到端测试验证) | Task 1–10 | 2d | 各 `_test.go` |

### 依赖图

```
Task 0 (文档优先，最先执行)

Task 1 ──→ Task 2 ──→ (无后续依赖)
       │
       ├──→ Task 5 ──→ Task 6 ──→ Task 9
       │         │         │
       │         ├──→ Task 7 ──┤
       │         │              │
       │         └──→ Task 8 ──┤
       │                       │
Task 3 ──→ Task 4 ─────────────┤
                               │
                               └──→ Task 10 ──→ Task 11
```

---

## Task 0: 项目主文档更新 ⚡ 文档优先，最先执行

**文件**: `README.md`（修改）、`README.zh-CN.md`（修改）、`docs/PRD.md`（修改）、`docs/ARCHITECTURE.md`（修改）、`docs/API.md`（修改）

**需求**: §设计目标 (行 19–27)、§用户使用流程 (行 514–552)、§架构 §目录结构 (行 35–45)、§仪表板端点 (行 212–222)、§网络安全说明 (行 473–490)

> **执行原则**：本文档应在任何代码实施前完成。文档先行确保团队对功能边界、用户接口和架构设计达成共识，后续实施可严格按文档验收。

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 0.1 | **README.md §Remote access 重写**：扩展当前「Remote access」章节（行 213–217），增加以下内容：<br>- **全局 Token 配置**：`mw config` 交互式引导，token 存储在 `~/.config/myworktree/auth.json`<br>- **Portal 仪表板**：`--portal-port 12345` 共享入口，列出所有运行中实例并可点击跳转<br>- **Tailscale HTTPS**：自动配置 `tailscale serve`，提供 `https://<machine>.ts.net` 域名访问<br>- **网络安全说明表**：复制计划文档 §网络安全说明 (行 477–490) 中的访问路径/协议/加密层级表 | §设计目标 行 21–27、§用户流程 行 514–552 |
| 0.2 | **README.md §Features 更新**：在「Features (MVP)」章节末尾增加 Portal Dashboard 相关条目：<br>- `Portal Dashboard with shared entry port, auto-discovery of running instances across repos`<br>- `Global auth token (HttpOnly Cookie, CSRF protection, tailscale serve integration)` | §设计目标 行 24–27 |
| 0.3 | **README.md §CLI examples 更新**：增加 `mw config` 子命令示例：<br>```bash<br>mw config              # 交互式引导（设置/查看/清除 Token）<br>mw config set-auth     # 直接设置 Token<br>mw config get-auth     # 查看 Token（掩码）<br>mw config clear-auth   # 清除 Token<br>mw start --listen 0.0.0.0:0            # LAN 访问，自动继承全局 Token<br>mw start --listen 0.0.0.0:0 --portal-port 12346  # 自定义 Portal 端口<br>mw start --listen 0.0.0.0:0 --portal-port 0       # 禁用 Portal<br>``` | §5 CLI 变更 行 393–411、§用户流程 行 514–552 |
| 0.4 | **README.md §Run 示例更新**：在 `mw` 启动示例下方增加 Portal 输出示例：<br>```<br>Portal dashboard at:<br>  http://0.0.0.0:12345/<br>Tailscale: https://my-machine.tail-scale.ts.net/<br>``` | §可观测性 行 600、§用户流程 行 526–530 |
| 0.5 | **README.zh-CN.md 同步更新**：对应中文 README 的「远程访问」章节（行 211–213）、「功能」章节、CLI 示例进行与 0.1–0.4 等效的中文更新 | 同上述子任务 |
| 0.6 | **PRD.md 更新**：<br>(a) 标题/描述行（行 1–4）：增加「Portal 仪表板」作为关键特性<br>(b) §6 安全（行 40–48）：扩展安全章节，增加 Portal 端口绑定、全局 Token（HttpOnly Cookie + CSRF）、Tailscale HTTPS 相关安全约束<br>(c) §7 当前实现状态（行 50–64）：将「规划增强：无」改为列出 Portal Dashboard 各项增强（全局 Token、Portal 仪表板、反向代理、Tailscale serve 自动管理）<br>(d) §8 验收标准（行 66–70）：新增 Portal Dashboard MVP 验收标准项 | §设计目标 行 19–27、§安全总结 行 607–619 |
| 0.7 | **ARCHITECTURE.md 更新**：<br>(a) §1 Overview（行 3–9）：加入 Portal/仪表板作为新能力层<br>(b) §2 High-level components（行 11–22）：新增 `internal/config/`（全局 auth 配置）和 `internal/portal/`（抢占/注册/反向代理/Tailscale）两个包<br>(c) §3 Data & persistence（行 24–46）：增加 `auth.json`（全局 token）、`portal/` 注册目录、`server.json` 扩展（新增 `instance_id` 字段）的描述<br>(d) §6 Security model（行 332–341）：扩展安全模型为双层认证架构（Portal 层 Cookie + CSRF，实例层 loopback 绕过）<br>(e) **新增** CLI Flags / 配置章节：列出 `--portal-port`、`--auth` 自动填充、`mw config` 子命令 | §架构 §目录结构 行 35–45、§架构 §文件变更清单 行 49–57、§7 双层认证架构 行 469–470 |
| 0.8 | **API.md 更新**：<br>(a) Header/Auth 块（行 1–21）：增加 `mw_token` Cookie 作为第三个 token 来源说明<br>(b) **新增** Portal 端点章节：列出所有 Portal 端点的 URL、方法、认证要求、请求/响应格式（`GET /`、`GET /api/csrf-token`、`POST /api/auth`、`GET /api/list`、`GET /api/portal-status`、`POST /api/logout`、`ANY /s/<repo-hash>/*`），标注 CSRF 防护（`/api/auth` 和 `/api/logout` 需 `csrf_token`）<br>(c) 反向代理说明：`/s/<repo-hash>/*` 的代理行为、repo-hash 格式校验、WebSocket 支持<br>(d) 响应格式文档化：`/api/list` 的 JSON schema（`is_portal`、`portal_port`、`processes[]`），`/api/portal-status` 的响应 | §3 仪表板端点 行 212–222、§3 反向代理 行 231–244、§3 `/api/list` 响应 行 224–229 |

**验收标准**:
- `README.md` 和 `README.zh-CN.md` 的「Remote access / 远程访问」章节包含 Portal Dashboard、全局 Token、Tailscale HTTPS 三类功能说明
- CLI 示例包含 `mw config` 和 `--portal-port` 的用法
- 网络安全说明表（访问路径/协议/加密层级）出现在远程访问章节
- 中英文 README 内容一致
- `PRD.md` §7 列出 Portal Dashboard 增强项，§8 含 Portal Dashboard MVP 验收标准
- `ARCHITECTURE.md` 包含 `internal/config/` 和 `internal/portal/` 两个新包、`auth.json` / `portal/` 目录、双层认证架构描述、新增 CLI Flags 章节
- `API.md` 包含完整的 Portal 端点文档（7 个端点，含请求/响应 schema、CSRF 标注、反向代理说明）

---

## Task 1: 全局 Auth 配置

**文件**: `internal/config/global.go`（新增）、`internal/config/global_test.go`（新增）

**需求**: §1 全局配置 (行 63–115)、安全总结 §auth.json 文件泄露 (行 600)

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 1.1 | 定义 `GlobalConfig` 结构体，包含 `AuthToken string` 字段（JSON tag `auth_token`） | §1 行 82 |
| 1.2 | 实现 `Load()` — 从 `~/.config/myworktree/auth.json` 读取并解析 JSON | §1 行 82 |
| 1.3 | `Load()` 错误处理：(a) 文件不存在 → 零值 + `nil` error；(b) JSON 解析失败 → 零值 + 非 nil error（`fmt.Errorf("config: failed to parse auth.json: %w", err)`）；任何情况不 panic | §1 行 84–87 |
| 1.4 | 实现 `Save(cfg)` — 原子写入：写临时文件 → `os.Rename`，文件权限 `0o600` | §1 行 94、§安全总结 行 610 |
| 1.5 | 参考 `internal/llm/config.go` 中的原子写入实现，保持风格一致 | §1 行 94 |
| 1.6 | **单测** `global_test.go`：覆盖 `Save()` 原子写入完整性、`0o600` 权限、重复写入一致性；`Load()` 文件不存在零值 + nil、JSON 损坏零值 + error、不 panic | §测试策略 行 578 |

**验收标准**:
- `Load()` 对不存在的文件返回 `(*GlobalConfig{}, nil)`
- `Load()` 对损坏的 JSON 返回 `(*GlobalConfig{}, error)`，error 包含路径和原始错误
- `Save()` 写入过程崩溃不会产生损坏文件（残留临时文件可接受）

---

## Task 2: CLI 变更

**文件**: `internal/cli/cli.go`（修改）

**需求**: §5 CLI 变更 (行 393–411)、§1 全局配置 §自动填充 (行 111–114)、§6 §迁移注意事项 (行 427–431)

**前置**: Task 1（需要 `config.Load` / `config.Save`）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 2.1 | 在 `startCmd` 的 `flag.FlagSet` 中新增 `--portal-port` flag（int，默认 12345；0 = 禁用 Portal） | §5 行 397–398 |
| 2.2 | `--auth` 自动填充：解析 flag 后若 `--auth` 为空，调用 `config.Load()`；nil error + AuthToken 非空则赋值；nil error + 空则保留空值；非 nil error 则 Warn 日志 `[config] auth.json is corrupted: <error>` + 保留空值 | §1 行 111–114 |
| 2.3 | 在 `Run()` 中新增 `case "config":` 分支，根据 `args[2]` 路由（`set-auth` / `get-auth` / `clear-auth` / 无参） | §1 行 109 |
| 2.4 | 实现 `mw config set-auth`：隐藏回显读取 Token → 二次确认 → 两次一致后 `config.Save()` | §1 行 99–100、行 104–105 |
| 2.5 | 实现 `mw config get-auth`：掩码输出（前 4 位 + `****` + 后 4 位） | §1 行 100、行 106 |
| 2.6 | 实现 `mw config clear-auth`：`AuthToken` 置空 → `config.Save()`，无需确认 | §1 行 101、行 107 |
| 2.7 | 实现 `mw config`（无参交互引导）：菜单 [1] 设置 [2] 查看 [3] 清除 [q] 退出 | §1 行 96–102 |

**验收标准**:
- `mw start --portal-port 12346` 正常启动，Portal 端口设置为 12346
- `mw start --portal-port 0` 不启动 Portal
- `mw start --listen 0.0.0.0:0`（auth.json 已配置 token）自动继承 token
- `mw config set-auth` 两次输入不一致时提示重试
- `mw config get-auth` 输出如 `abcd****wxyz`（token 长度 >= 8 时）

---

## Task 3: App 配置扩展 + server.json 重构

**文件**: `internal/app/app.go`（修改）

**需求**: §6 App 变更 (行 415–430)、§架构 §目录结构 (行 35–45)

**前置**: 无（纯 app.go 内部修改，不依赖其他 Task）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 3.1 | 在 `Config` 结构体中新增 `PortalPort int` 字段（默认 0） | §6 行 419 |
| 3.2 | 扩展 `serverConfig` 结构体，新增 `InstanceID string` 字段（JSON tag `instance_id,omitempty`） | §6 行 423 |
| 3.3 | 重构 `writeRepoListenPort()` 为「读取-合并-写入」模式：<br>(a) 读现有 JSON（宽容解析：未知字段忽略，缺失字段零值）<br>(b) 仅更新 `listen_port`，保留 `instance_id` 等已有字段<br>(c) 原子写入（写临时文件 → rename），文件权限 `0o600` | §6 行 425 |
| 3.4 | `allocateRepoListenAddr()` / `resolveRepoListenAddr()` 中写入 `server.json` 时同时写入 `instance_id`（格式 `pid-timestamp-rand`） | §3 行 163、§6 行 432–438 |

**验收标准**:
- `server.json` 写操作不会清除已有的 `instance_id` 字段
- 旧版（仅含 `listen_port`）和新版（含 `listen_port` + `instance_id`）均可正确读取
- 并发写不产生损坏文件

---

## Task 4: withAuth 中间件改造

**文件**: `internal/app/app.go`（修改）

**需求**: §2 Auth 中间件改造 (行 118–145)、§7 Cookie 认证 (行 446–470)

**前置**: Task 3（需要 `Config` 已扩展，但 `withAuth` 逻辑本身可先行修改后再集成）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 4.1 | 修改 `withAuth` 执行流程，按以下顺序重排：<br>1. `AuthToken` 为空 → 直接放行（保留现有行为）<br>2. `isLoopbackRequest(r)` → loopback 则放行（跳过 origin 校验和 token 校验）<br>3. 非 loopback → `sameOriginHost(r)` 校验<br>4. Token 提取（Authorization → `?token=` → `mw_token` Cookie）<br>5. Token 比对 + 速率限制 | §2 行 124–130 |
| 4.2 | Token 提取新增 `mw_token` Cookie 来源（优先级第三，位于 `Authorization` 和 `?token=` 之后） | §2 行 129、行 134–136 |
| 4.3 | 确保速率限制（`allowAuthAttempt` / `resetAuthAttempts`）对非 loopback 请求照常生效 | §2 行 130、§7 行 453 |
| 4.4 | **单测** `app_test.go`：验证 loopback 请求跳过所有校验（无 token、无 origin 匹配）、非 loopback 请求仍需 token、Cookie token 可正常认证、速率限制保留 | §测试策略 行 580 |

**验收标准**:
- `curl http://127.0.0.1:PORT/api/list` 无 token 返回 200（loopback 放行）
- `curl -H "Origin: http://evil.com" http://192.168.1.18:PORT/api/list` 无 token 返回 403
- `curl -H "Cookie: mw_token=correct-token" http://192.168.1.18:PORT/api/list` 返回 200
- 连续 21 次错误 token → 返回 429

---

## Task 5: Portal 核心 — 抢占、注册、CSRF、清理

**文件**: `internal/portal/portal.go`（新增）

**需求**: §3 Portal 包 (行 148–210)、§3 CSRF Token 服务端设计 (行 272–294)、§边界情况处理 (行 494–510)

**前置**: Task 1（需要 `config.Load` 辅助）、Task 3（需要 `Config` 已含 `PortalPort`）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| **Portal 结构体 & 配置** | | |
| 5.1 | 定义 `Config` 结构体：`PortalPort`、`Host`、`AuthToken`、`RegistryDir`、`DataDir`、`RepoName`、`RepoHash` | §3 行 152–159 |
| 5.2 | 定义 `Portal` 结构体：`cfg`、`instanceID`、`mu`、`srv`、`ln`、`done`、`wg`、`closeOnce` | §3 行 161–169 |
| **生命周期** | | |
| 5.3 | 实现 `New(cfg)` 构造函数：生成 `instanceID`（`pid-timestamp-rand`），初始化 channel 和 WaitGroup | §3 行 163、行 173–174 |
| 5.4 | 实现 `Start()`：写入注册文件 `portal/<instanceID>.json` → 启动 `claimerLoop` goroutine → 启动 `tailscaleServeLoop` goroutine（tailscale 逻辑见 Task 8） | §3 行 173–179 |
| 5.5 | 实现 `Stop()`（`sync.Once`）：关闭 `done` → `srv.Shutdown(ctx)`（5s 超时）→ `stopTailscaleServe()` → 删除注册文件和 portal.json → `wg.Wait()` | §3 行 181–186 |
| **抢占者 (claimerLoop)** | | |
| 5.6 | 初始随机延迟 0~5 秒（`rand.Intn(5000)ms`）避免惊群效应 | §3 行 189 |
| 5.7 | 死循环：`net.Listen` 抢占端口 → 成功则设置 `ln`/`srv` → 原子写入 `portal.json` → 阻塞 `srv.Serve(ln)` | §3 行 190 |
| 5.8 | `net.Listen` 失败 → 等待 10~15 秒随机间隔（`10s + rand.Intn(5000)ms`）后重试 | §3 行 191 |
| 5.9 | `srv.Serve()` 非 `ErrServerClosed` 退出 → 输出 Warn 日志 `[portal] HTTP serve exited unexpectedly: %v` → 回到死循环顶部重试 | §3 行 193 |
| 5.10 | 每次循环开始检查 `done` channel | §3 行 192 |
| 5.11 | 连续抢占失败 ≥ 10 次 → Warn 日志提示用户考虑 `--portal-port` 或 `--portal-port 0` | §5 行 399–400 |
| **注册文件** | | |
| 5.12 | 实现注册文件写入/读取：`<instance-id>.json`（instance_id, pid, port, host, repo_name, repo_hash, started_at），**不含 auth_token** | §3 行 200–206 |
| 5.13 | 实现 `portal.json` 原子写入（instance_id, port, updated_at） | §3 行 208–210 |
| 5.14 | 实现无效注册文件定期清理（每 5 分钟扫描，PID + TCP + instance_id 三重校验存活，删除无法验证的条目） | §3 行 195–198、§边界情况 行 507 |
| **CSRF Token 状态管理** | | |
| 5.15 | 定义 `csrfState` 结构体：`mu`（`sync.Mutex`）、`used`（`map[string]time.Time`）、`rateLimits`（`map[string]time.Time`） | §3 行 274–277 |
| 5.16 | 实现 `generate()`：`crypto/rand` 生成 32 字节 hex 编码，存入内存 | §3 行 279 |
| 5.17 | 实现 `verifyAndConsume(cookieValue, bodyValue)`：三重校验 (1) cookie 匹配 body (2) 未在 used 中且 ≤ 5 分钟 TTL (3) 标记 used | §3 行 281–284 |
| 5.18 | 实现 `/api/csrf-token` 速率限制：每 IP 每秒最多 1 次；`used` / `rateLimits` 容量上限各 10000 条 | §3 行 286–287、行 293 |
| 5.19 | 实现 `cleanup()`：后台 goroutine 每 30 秒清理 `used` 和 `rateLimits` 中超过 5 分钟的条目 | §3 行 288、行 294 |
| 5.20 | **单测** `portal_test.go`：`generateInstanceID()` 唯一性、注册文件读写、`isPortalHolder()`、活性校验四种场景、CSRF 生成→使用→过期→重用拒绝、`rateLimits` 过期清理、`-race` 并发无 data race | §测试策略 行 579 |

**验收标准**:
- 两个进程启动后只有一个持有 portal 端口，另一个持续重试
- 持有者崩溃后 15 秒内另一个接管（`ln` 非 nil → `GET /api/portal-status` 返回 `true`）
- 注册目录中死进程文件在 5 分钟内被清理
- CSRF token 使用后立即失效，重复使用返回 403
- CSRF token 超过 5 分钟未使用返回 403
- `/api/csrf-token` 每秒超过 1 次请求返回 429

---

## Task 6: Portal HTTP 端点 & 仪表板 HTML

**文件**: `internal/portal/portal.go`（修改）、`internal/portal/dashboard.html`（新增）

**需求**: §3 仪表板端点 (行 212–222)、§3 仪表板页面 (行 246–303)、§3 Token 流程 (行 305–332)

**前置**: Task 5（Portal 结构体、CSRF state、注册文件已实现）、Task 9（CSP hash 常量已生成——可以先写临时常量后替换）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| **HTTP 路由 & 中间件** | | |
| 6.1 | 在 `claimerLoop` 中 `net.Listen` 成功后创建 `http.Server`，注册所有路由（`/`、`/api/*`、`/s/`） | §3 行 190 |
| 6.2 | 实现认证中间件（Portal 级别，与实例 `withAuth` 独立）：检查 `mw_token` Cookie 值是否等于 `cfg.AuthToken`，不匹配返回 401 | §3 行 218–219、行 222 |
| 6.3 | 认证成功时在响应中重新设置 `Set-Cookie: mw_token=...; Max-Age=86400`（滑动过期） | §3 行 328 |
| **仪表板首页** | | |
| 6.4 | 实现 `GET /` 处理器：`go:embed` 内嵌的 `dashboard.html`；设置 CSP 响应头（hash 来自 `csp_gen.go`）；设置 `X-Content-Type-Options: nosniff` | §3 行 216、行 254–259 |
| **API 端点** | | |
| 6.5 | 实现 `GET /api/csrf-token`：返回 `{"csrf_token":"<random>"}` + `Set-Cookie: mw_csrf=<token>; Path=/; SameSite=Strict`（非 HttpOnly）；Secure 判断：`r.TLS != nil \|\| X-Forwarded-Proto == "https"` | §3 行 217 |
| 6.6 | 实现 `POST /api/auth`：读取 JSON body `{token, csrf_token}` → `verifyAndConsume` 校验 CSRF → 明文比较 auth_token → 含速率限制（每 IP 每分钟 20 次）→ 成功则 `Set-Cookie: mw_token`（HttpOnly, Max-Age=86400, SameSite=Lax） | §3 行 218 |
| 6.7 | `POST /api/auth` 边界：`AuthToken` 为空时返回 `400 {"error":"auth token not configured on server"}` | §3 行 218 |
| 6.8 | 实现 `GET /api/list`：扫描 `portal/` 目录，读取所有 `<instance-id>.json`，综合 PID + TCP 判断 `alive` 字段，排序后返回 JSON | §3 行 219、行 224–229 |
| 6.9 | `GET /api/list` 认证成功时设置 `Set-Cookie: mw_token=...`（滑动过期） | §3 行 328 |
| 6.10 | 实现 `GET /api/portal-status`：检查 `ln != nil` 返回 `{"is_portal": bool}` | §3 行 220 |
| 6.11 | 实现 `POST /api/logout`：校验 body 中 `csrf_token` 匹配 `mw_csrf` Cookie（防 logout CSRF）→ 清除 `mw_token`（`Max-Age=0`）→ 标记 CSRF token 已使用 → 返回 200 | §3 行 221 |
| **仪表板 HTML** | | |
| 6.12 | 创建 `dashboard.html`：暗色主题、等宽字体，符合主界面风格 | §3 行 248–249 |
| 6.13 | 顶部 Token 输入框（`401` 时显示） + 表格（repo_name, port, 状态, alive 指示灯） | §3 行 250–251 |
| 6.14 | 每 5 秒 `fetch /api/list` 局部刷新表格（`textContent` 渲染防 XSS） | §3 行 253、行 260 |
| 6.15 | CSRF 流程 JS：(1) fetch `/api/csrf-token` (2) 用户输入 → `POST /api/auth {token, csrf_token}` (3) 成功后刷新表格 | §3 行 265–270 |
| 6.16 | 实例链接使用相对路径 `/s/<repo_hash>/` | §3 行 298 |
| 6.17 | 所有 `<style>` 写在 `<style>` 块内（**禁止** `style="..."` 属性），供 CSP hash 白名单覆盖 | §3 行 258 |
| 6.18 | Embed：`//go:embed dashboard.html` → `var DashboardHTML []byte` | §3 行 248 |

**验收标准**:
- `GET /` 返回 dashboard.html，CSP 头包含 script-src 和 style-src 的 sha256 hash
- 无 Cookie 访问 `/api/list` 返回 401
- 正确的 CSRF + token 通过 `/api/auth` → 返回 200 + Set-Cookie
- `/api/list` 响应包含当前存活实例列表，字段齐全
- Dashboard 可点击实例链接跳转到 `/s/<repo_hash>/`
- `/api/logout` 无 csrf_token 返回 403，正确 csrf_token 返回 200 + Cookie 清除
- `/api/auth` 在 AuthToken 为空时返回 400

---

## Task 7: Portal 反向代理

**文件**: `internal/portal/portal.go`（修改）

**需求**: §3 反向代理 (行 231–244)、§3 为什么需要反向代理 (行 240–244)

**前置**: Task 5（注册文件已可读）、Task 6（Portal HTTP Server 已就绪）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 7.1 | 实现 `ANY /s/<repo-hash>/` 处理器：解析 URL 路径提取 `repo-hash` | §3 行 231–233 |
| 7.2 | repo-hash 格式校验：仅允许 `[a-f0-9]+`（小写 hex），拒绝 `..`、`/`、`\` 等字符，返回 400 | §3 行 238 |
| 7.3 | 根据 repo-hash 查找注册表 → 获取实例端口 → 构造 `http://127.0.0.1:<port>` 目标 URL | §3 行 233 |
| 7.4 | 使用 `net/http/httputil.ReverseProxy` 创建反向代理，转发请求至目标 URL | §3 行 233 |
| 7.5 | 转发前校验 `mw_token` Cookie（与 `/api/list` 一致逻辑），未认证返回 401 | §3 行 236–237 |
| 7.6 | 认证成功后设置 `Set-Cookie` 头滑动过期 | §3 行 328 |
| 7.7 | 目标实例离线（端口不可达）→ 返回 502，Warn 日志含目标实例信息 | §3 行 235、§可观测性 行 599 |
| 7.8 | WebSocket 代理：`httputil.ReverseProxy` 内置 WebSocket 支持，无需额外配置 | §3 行 235 |
| 7.9 | **集成测试**：正常代理 200、目标离线 502、WebSocket 双向消息、repo-hash 非法字符拒绝 | §测试策略 行 585–586 |

**验收标准**:
- `curl -H "Cookie: mw_token=correct" http://host:12345/s/<valid-hash>/` → 代理到对应实例
- `curl http://host:12345/s/../etc/passwd` → 400
- `curl -H "Cookie: mw_token=wrong" http://host:12345/s/<valid-hash>/` → 401
- 目标实例离线时 → 502 + Warn 日志

---

## Task 8: Tailscale Serve 自动管理

**文件**: `internal/portal/portal.go`（修改）

**需求**: §4 Tailscale Serve 自动管理 (行 336–389)

**前置**: Task 5（Portal 结构体、`ln` 字段、`done` channel 已就绪）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 8.1 | 实现 `tailscaleServeLoop()` goroutine：每 30 秒检查一次 | §4 行 346 |
| 8.2 | 循环内：(a) 检查 `done` channel (b) 检查 `ln != nil`（是否为持有者）(c) 非持有者跳过 | §4 行 347–348、行 356 |
| 8.3 | 调用 `tailscale serve status --json` 获取状态，解析 JSON 中 `"TCP"` map 的 `:443` key | §4 行 350–355 |
| 8.4 | 三种状态处理：(a) `:443 → 127.0.0.1:<portal-port>` → 已正确，跳过 (b) `:443 → 其他` → Warn 日志 + `stop` + 重新 `--bg` 启动 (c) 无 `:443` → 启动新的 | §4 行 352–355 |
| 8.5 | 启动命令：`tailscale serve --bg http://127.0.0.1:<portal-port>`，10 秒超时 | §4 行 375 |
| 8.6 | 启动时孤儿清理：`claimerLoop` 抢占端口成功后立即调用 `tailscale serve status --json`，检测到 `:443` 指向错误端口 → Warn 日志 + `stop` + 重新启动 | §4 行 358–361 |
| 8.7 | 实现 `stopTailscaleServe()`：执行 `tailscale serve stop` | §4 行 379 |
| 8.8 | 边界处理：(a) Tailscale 未安装 → Warn 跳过 (c) 用户手动配置 → 检测到已配置同端口则跳过 (d) `status` 退出码非零 → 视为未配置 | §4 行 371、行 383–389 |
| 8.9 | **集成测试**：mock 外部命令输出，验证四种分支：已正确配置/指向错误端口/无配置/json 解析失败；验证启动时孤儿清理场景 | §测试策略 行 586 |

**验收标准**:
- Portal 持有者启动后 30 秒内 `tailscale serve --json` 显示 `:443 → 127.0.0.1:12345`
- 非持有者不执行 `tailscale serve` 命令
- `tailscale serve` 残留指向其他端口 → 接管时清理 + 重新配置
- Tailscale 未安装时 `mw start` 正常启动（仪表板可用，仅无 HTTPS 域名）
- 【已移除】仅支持 tailscale ≥ 1.56.0（`--json` 标志引入版本），不再兼容旧版

---

## Task 9: Go Generate CSP Hash 自动计算

**文件**: `internal/portal/gen.go`（新增）、`internal/portal/csp_gen.go`（生成，不手动编辑）

**需求**: §3 仪表板页面 CSP 部分 (行 255–263)

**前置**: Task 6（`dashboard.html` 已创建，`go:embed` 已就位）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 9.1 | 创建 `gen.go`（`//go:build ignore`）：`//go generate` 指令，读取 `dashboard.html` 原始字节（`os.ReadFile`，无 BOM UTF-8，不做预处理/trim/编码转换） | §3 行 255–256、行 263 |
| 9.2 | 提取所有 `<script>` 和 `<style>` 块内容（不含标签本身），对每个块计算 SHA256 → Base64 编码 | §3 行 256–257 |
| 9.3 | 生成 `csp_gen.go`：导出 `CSPHashes [][2]string`（每项 `{type, hash}`，type 为 `"script"` 或 `"style"`），附带 `// Code generated by go generate; DO NOT EDIT.` 注释 | §3 行 256 |
| 9.4 | 在 `dashboard.html` 所在 package 添加 `//go:generate go run gen.go` 注释 | §3 行 255 |
| 9.5 | 在 `GET /` 处理器中读取 `CSPHashes`，构造 CSP 头：`script-src` 拼入所有 type=`"script"` 的 hash，`style-src` 拼入所有 type=`"style"` 的 hash + `'self'` | §3 行 255–257 |
| 9.6 | **CSP hash CI 验证测试**：从 `//go:embed` 内嵌字节（`portal.DashboardHTML`）提取 `<script>`/`<style>` 块，计算 hash，与 `csp_gen.go` 常量断言对比——失败则 CI 不通过 | §测试策略 行 581 |

**验收标准**:
- 修改 `dashboard.html` 中内联 `<script>` 或 `<style>` 内容后，运行 `go generate ./internal/portal/` → `csp_gen.go` 自动更新 hash
- CSP 头包含所有内联块的 sha256 hash（空格分隔）
- CI 中 CSP hash 验证测试强制通过

---

## Task 10: Portal 生命周期集成

**文件**: `internal/app/app.go`（修改）

**需求**: §6 Portal 生命周期集成 (行 432–443)、§7 双层认证架构 (行 469–470)

**前置**: Task 4（withAuth 改造完成）、Task 5（Portal 包可用）

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| 10.1 | 在 `Server.Start()` 中 `net.Listen` 成功后、`resolveListenAddr` 之后，插入 Portal 启动逻辑 | §6 行 434 |
| 10.2 | 若 `PortalPort > 0`：构造 `portal.Config`（PortalPort, Host, AuthToken, RegistryDir=`~/.config/myworktree/portal/`, DataDir, RepoName, RepoHash）→ `portal.New(cfg)` → `portal.Start()` | §6 行 435–437 |
| 10.3 | 若 `portal.Start()` 返回错误 → 记录日志但主服务继续运行（Portal 失败不阻断核心功能） | §6 行 438 |
| 10.4 | 在 `Start()` 的 defer 块中检查 `s.portal != nil` → `s.portal.Stop()`（优雅关闭 + 清理） | §6 行 441–442 |
| 10.5 | 启动输出中增加 Portal 地址（`http://<host>:<portal-port>/`）；若 Tailscale 可用则额外输出 `https://<machine>.ts.net/`；Portal 禁用时不输出 | §可观测性 行 600、§用户流程 行 526–530 |

**验收标准**:
- `mw start --listen 0.0.0.0:0` 启动后 Portal 地址出现在控制台输出
- `mw start --portal-port 0` 不启动 Portal，无 Portal 地址输出
- 主服务退出时 Portal 优雅关闭（无 goroutine 泄露）、注册文件被清理
- Portal 启动失败（如端口被占）主服务仍正常运行，日志记录错误

---

## Task 11: 端到端测试验证

**文件**: 各 `_test.go` 文件

**需求**: §测试策略 (行 574–591)、§可观测性 (行 595–603)

**前置**: Task 1–10 全部完成

| 子任务 | 描述 | 需求来源 |
|--------|------|----------|
| **单元测试补齐** | | |
| 11.1 | `global_test.go`：Task 1 中已覆盖 | §测试策略 行 578 |
| 11.2 | `portal_test.go`：Task 5 中已覆盖抢占/注册/CSRF | §测试策略 行 579 |
| 11.3 | `app_test.go`：Task 4 中已覆盖 withAuth 改造 | §测试策略 行 580 |
| **并发测试** | | |
| 11.4 | 多 goroutine 抢占同一 portal 端口（验证先 listen 者胜 + 失败者重试 + 随机抖动有效） | §测试策略 行 582 |
| 11.5 | Portal 注册目录并发读写与清理（清理不误删活跃实例，PID 回收时 instance_id 校验有效） | §测试策略 行 583 |
| 11.6 | `Portal.Stop()` 与 `claimerLoop` 并发交互（`closeOnce` + `sync.Mutex` 无 panic） | §测试策略 行 584 |
| **集成测试** | | |
| 11.7 | 反向代理正常/离线/非法 repo-hash 测试 | §测试策略 行 585 |
| 11.8 | WebSocket 升级转发双向消息互通测试 | §测试策略 行 585 |
| 11.9 | tailscale serve mock 四种分支 + 启动孤儿清理测试 | §测试策略 行 586 |
| 11.10 | 全局 token 自动填充三种场景测试（auth.json 正常/不存在/损坏） | §测试策略 行 587 |
| 11.11 | 注册目录被删除后 Portal 恢复测试 | §测试策略 行 588 |
| 11.12 | `/api/auth` AuthToken 为空时返回 400 测试 | §测试策略 行 589 |
| 11.13 | `/api/logout` CSRF token 校验测试（有效/缺失/不匹配） | §测试策略 行 590 |
| **CSP Hash 验证** | | |
| 11.14 | CSP hash CI 验证测试（见 Task 9.6）：从 `go:embed` 字节计算 hash 与 `csp_gen.go` 对比 | §测试策略 行 581 |
| **端到端认证流程** | | |
| 11.15 | 使用 `httptest.Server` 模拟完整浏览器序列：获取 CSRF → 提交 auth → 验证 Set-Cookie → `/api/list` → `/s/<repo-hash>/` 代理 → 验证 `Set-Cookie` 滑动续期 → `/api/logout`（带 CSRF）→ 再次请求 401 | §测试策略 行 591 |
| 11.16 | 运行 `go test -race ./...` 确保无 data race | §测试策略 行 579 |
| **日志验证** | | |
| 11.17 | 验证抢占成功/失败/连续10次失败、Serve 意外退出、tailscale serve 孤儿清理等关键事件有对应前缀日志输出 | §可观测性 行 599 |

**验收标准**:
- `go test -race ./...` 全部通过（所有新增和已有测试）
- CSP hash 验证测试通过（即 `gen.go` 输出与 `dashboard.html` 一致）
- 端到端认证流程测试覆盖 CSP → CSRF → Cookie → 反向代理 → 滑动过期 → logout CSRF 六层协同
- CI 中所有测试通过

---

## 附录 A: 跨任务关注点

以下关注点贯穿多个任务，各工程师在实现时需保持一致性：

| 关注点 | 涉及任务 | 说明 |
|--------|----------|------|
| **原子写入** | Task 1, 3, 5 | `auth.json`、`server.json`、`portal.json` 均使用「写临时文件 → rename」模式 |
| **日志风格** | Task 2, 5, 8, 10 | `log.Printf("[prefix] message: %v", err)` 格式，与现有 `s.logger.Printf` 一致 |
| **错误降级** | Task 2, 8, 10 | 非核心组件失败不阻断主服务，仅 Warn 日志 |
| **并发安全** | Task 5, 10 | `sync.Mutex` 保护 srv/ln，`sync.Once` 保护 Stop，`sync.WaitGroup` 等待退出 |
| **向后兼容** | Task 3, 4 | `server.json` 宽容解析，`withAuth` token 为空时保留现有行为 |

## 附录 B: 文件变更总览

```
README.md               (修改, Task 0)
README.zh-CN.md          (修改, Task 0)
docs/
├── PRD.md               (修改, Task 0)
├── ARCHITECTURE.md      (修改, Task 0)
└── API.md               (修改, Task 0)

internal/
├── config/
│   ├── global.go           (新增, Task 1)
│   └── global_test.go      (新增, Task 1)
├── portal/
│   ├── portal.go           (新增, Task 5 + 6 + 7 + 8)
│   ├── dashboard.html      (新增, Task 6)
│   ├── gen.go              (新增, Task 9)
│   ├── csp_gen.go          (生成, Task 9)
│   └── portal_test.go      (新增, Task 5 + 11)
├── app/
│   └── app.go              (修改, Task 3 + 4 + 10)
└── cli/
    └── cli.go              (修改, Task 2)
```
