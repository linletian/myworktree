# Portal Dashboard - 实施方案

## 状态

**阶段**: 规划中（等待实施）

---

## 背景与动机

当前 myworktree 服务默认只绑定 `127.0.0.1:0`，只能从本机访问。用户希望：

1. 通过局域网 IP（如 `192.168.1.18`）访问服务
2. 通过 Tailscale 安全地将服务暴露到外网
3. 无需手动记录端口，即可管理多个不同仓库的运行实例

---

## 设计目标

1. **局域网访问**：绑定 `0.0.0.0`，所有网口均可访问
2. **安全性**：非 loopback 地址必须携带 token（非 loopback 无 token 则拒绝启动）
3. **全局 Token**：配置一次，所有实例自动继承，实例级别可按需覆盖
4. **Portal 仪表板**：共享的入口端口，列出所有运行中的实例并可点击跳转
5. **Token 安全**：Token 存储在 HttpOnly Cookie 中，JS 和 URL 均不可见
6. **零配置**：仪表板自动发现运行中的实例，无需人工追踪
7. **Tailscale HTTPS**：自动为 Portal 配置 `tailscale serve`，零手动管理

---

## 架构

### 目录结构

```
~/.config/myworktree/
├── auth.json                      ← 全局 auth token（独立于 LLM 的 config.json）
├── <repo-hash-A>/
│   └── server.json               ← { listen_port, instance_id }
├── <repo-hash-B>/
│   └── server.json
└── portal/                       ← 共享注册目录
    ├── <instance-id>.json        ← 各实例写入自己的注册信息
    └── portal.json              ← 当前 portal 持有者（instance_id + port）
```

### 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/config/global.go` | **新增** | 读写 `~/.config/myworktree/auth.json`（auth token 独立于 LLM 的 `config.json`） |
| `internal/config/global_test.go` | **新增** | 测试 |
| `internal/portal/portal.go` | **新增** | 核心：抢占者、注册、Portal HTTP 服务、反向代理、Tailscale serve 管理 |
| `internal/portal/dashboard.html` | **新增** | 仪表板 HTML（go:embed 内嵌） |
| `internal/portal/portal_test.go` | **新增** | 测试 |
| `internal/app/app.go` | **修改** | Config 扩展、server.json 字段、portal 生命周期集成 |
| `internal/cli/cli.go` | **修改** | `--portal-port` flag、全局 token 自动填充、交互式 `config` 子命令 |

---

## 详细设计

### 1. 全局配置

**文件**: `~/.config/myworktree/auth.json`（独立于 LLM 的 `config.json`，避免字段冲突）

```json
{
  "auth_token": "your-global-secret-token"
}
```

**存储策略**：

- `auth_token` 字段存储明文（供反向代理转发、Cookie 设置、`/api/auth` 登录校验，以及实例 `withAuth` 中间件做字符串比较）
- **仅明文存储**——不引入 bcrypt 哈希。由于 `auth_token` 明文同时用于 `withAuth` 中间件字符串比较、反向代理转发和 Cookie 设置，引入哈希并不能消除明文存储需求；`auth_token_hash` 的安全价值微乎其微（攻击者若能读取文件即可同时获得明文）。接受此风险，缓解措施为严格的 `0o600` 文件权限

**Token 强度建议**：推荐使用 `openssl rand -hex 32` 生成 64 字符（256-bit）随机 token。用户也可使用 `pwgen -s 32 1` 或密码管理器生成。避免使用字典单词或短密码。

**文件**: `internal/config/global.go`

定义 `GlobalConfig` 结构体，包含 `auth_token` 字符串字段（JSON tag）。提供两个导出函数：`Load()` 从 `~/.config/myworktree/auth.json` 读取并解析 JSON 返回配置指针和 error；`Save(cfg)` 将配置序列化为 JSON 写入 `auth.json`。

**`Load()` 错误处理策略**：
- 若文件不存在（`os.IsNotExist`），返回零值空配置 + `nil` error（不报错，这是正常情况：用户从未配置过 Token）
- 若 JSON 解析失败，返回零值空配置 + 非 nil error（包装原始解析错误，如 `fmt.Errorf("config: failed to parse auth.json: %w", err)`），让调用方决定如何处理错误
- 在任何情况下都不 panic

**调用方处理**（`startCmd` 中的自动填充逻辑）：调用 `config.Load()` 时根据返回值区分：
- error 为 nil + `AuthToken` 非空 → 使用加载的 Token ✓
- error 为 nil + `AuthToken` 为空 → Token 未配置，保留空值 ✓
- error 非 nil → **auth.json 存在但已损坏**，打印 Warn 日志（`[config] auth.json is corrupted: %v`）后保留空值。注意：若 `--listen` 绑定非 loopback 且 auth 为空，后续 `validateSecurity()` 会拒绝启动并提示 `--auth is required`，此时用户可结合 Warn 日志定位到 auth.json 损坏的问题。若 `--listen` 绑定 loopback，则不做特殊处理（loopback 访问本身无需 token）

**原子写入**：`Save()` 必须使用「写临时文件 → rename」模式（与 `internal/llm/config.go` 一致），避免进程崩溃时文件损坏。

**交互式 Config 命令**（`mw config` 无参数时进入引导流程）:

引导流程展示一个菜单，用户选择：
- 选项 1：设置全局 Token —— 提示用户输入 Token（输入时字符隐藏），再提示确认输入，两次一致后保存
- 选项 2：查看当前 Token —— 以掩码形式显示（仅显示前 4 位和后 4 位，中间用星号替代）
- 选项 3：清除全局 Token —— 将 `auth_token` 设为空字符串并保存
- 选项 q：退出

子命令：
- `mw config set-auth`：进入交互式设置流程（输入 + 确认）
- `mw config get-auth`：直接输出当前 Token（掩码形式）
- `mw config clear-auth`：直接清除（无需二次确认）

**子命令路由设计**：`mw config`（无参数）进入上述交互式引导。`mw config set-auth`、`mw config get-auth`、`mw config clear-auth` 在 `Run()` 中新增 `case "config":` 分支处理，该分支再根据 `args[2]` 分发到对应的处理函数。

**自动填充逻辑**（在 `startCmd` 中）：解析完 `--auth` flag 后，若其值为空字符串，则从全局配置 `config.Load()` 读取。处理逻辑：
- `Load()` 返回 error 为 nil 且 `AuthToken` 非空 → 赋值给 auth 变量
- `Load()` 返回 error 为 nil 且 `AuthToken` 为空 → 保留 auth 为空（Token 未配置，正常情况）
- `Load()` 返回 error 非 nil → 打印 Warn 日志 `[config] auth.json is corrupted: <error>`，保留 auth 为空。此时若 `--listen` 绑定非 loopback 地址，`validateSecurity()` 会拒绝启动并提示 `--auth is required`，用户可结合 Warn 日志定位到 auth.json 损坏问题

---

### 2. Auth 中间件改造（Loopback 始终放行）

**文件**: `internal/app/app.go`

修改 `withAuth`，对 loopback 请求跳过 token 校验，无论 token 来源是全局配置还是 `--auth` 参数。

`withAuth` 中间件改造后的执行流程（按顺序）：

1. **AuthToken 为空**：若配置中未设置 token，直接放行所有请求（保留现有行为）。
2. **Loopback 检查**：调用 `isLoopbackRequest(r)` 判断请求来源是否为回环地址（`127.0.0.1`、`localhost`、`::1`）。若为 loopback，直接放行——跳过 Origin 同源校验和 token 校验。这是实现反向代理认证绕过的基础。
3. **Origin 同源校验**（仅非 loopback）：调用 `sameOriginHost(r)` 比较请求头 `Origin` 与 `Host`。若 Origin 为空（浏览器未发送）则放行；若 Origin 的 Host 部分与请求 Host 不匹配则返回 403。注意：此校验依赖浏览器诚实地发送 Origin 头，非浏览器客户端（curl 等）可伪造，因此仅作为纵深防御层而非独立安全边界。
4. **Token 提取**：按以下优先级从请求中提取 token：(a) `Authorization: Bearer <token>` 请求头；(b) URL 查询参数 `?token=<token>`；(c) `mw_token` Cookie（新增的 Cookie 来源）。取第一个非空值。
5. **Token 比对 + 速率限制**：将提取的 token 与配置中的 `AuthToken` 做字符串明文比较。若不匹配，记录该 IP 的失败次数（每 IP 每分钟最多 20 次），超出则返回 429，未超出则返回 401。若匹配成功，清除该 IP 的失败计数并放行请求。

**Token 来源**（调整后的 `withAuth` 校验顺序）:

1. `Authorization: Bearer <token>` 请求头
2. `?token=<token>` URL 查询参数
3. `mw_token` Cookie（新增）

**行为矩阵**:

| 来源 IP | AuthToken 来源 | 结果 |
|---------|---------------|------|
| 127.0.0.1 / localhost | 任意来源 | **放行**（无需 token） |
| 非 loopback | 全局配置 | **需要 token** |
| 非 loopback | `--auth` 参数 | **需要 token** |

---

### 3. Portal 包（`internal/portal/portal.go`）

#### Portal 配置

`portal.Config` 结构体包含以下字段：
- `PortalPort`：抢占的目标端口（整数，默认 12345；设为 0 表示禁用 Portal，不启动抢占、不注册）
- `Host`：监听地址（来自主服务的 listen host）
- `AuthToken`：全局或进程级的认证 token
- `RegistryDir`：注册目录路径（`~/.config/myworktree/portal/`）
- `DataDir`：当前实例数据目录路径（`~/.config/myworktree/<repo-hash>/`）
- `RepoName`：仓库显示名称
- `RepoHash`：仓库 hash，用于构造反向代理路径

`portal.Portal` 结构体包含以下关键字段：
- `cfg`：上述 `Config` 配置
- `instanceID`：格式为 `pid-timestamp-rand` 的唯一实例标识符，在 `Start()` 时生成，生命周期内不变
- `mu`：`sync.Mutex`，保护以下 `srv` 和 `ln` 字段的并发读写（claimerLoop 写入，Stop 读取，避免 data race）
- `srv`：`*http.Server`，Portal HTTP 服务实例（仅当前持有者非 nil）
- `ln`：`net.Listener`，portal 端口监听器（仅当前持有者非 nil）
- `done`：`chan struct{}`，关闭信号，通知所有协程退出
- `wg`：`sync.WaitGroup`，等待所有协程完全退出
- `closeOnce`：`sync.Once`，确保 `Stop()` 只执行一次

#### 生命周期

**`Start()` 方法**：
1. 生成 `instanceID`（格式 `pid-timestamp-rand`），全生命周期不变
2. 增加 `WaitGroup` 计数（用于 `claimerLoop` 和 `tailscaleServeLoop` 两个 goroutine）
3. 将实例注册信息写入 `portal/<instanceID>.json`
4. 启动 `claimerLoop` goroutine
5. 启动 `tailscaleServeLoop` goroutine
6. 返回

**`Stop()` 方法**（通过 `sync.Once` 保证只执行一次）：
1. 关闭 `done` channel，通知所有 goroutine 退出
2. 加 `mu` 锁检查 `srv` 是否为 nil，若非 nil 则调用 `Shutdown(ctx)` 优雅关闭（5 秒超时排空活跃连接），完成后将 `srv` 和 `ln` 置 nil
3. 调用 `stopTailscaleServe()` 清理 tailscale serve
4. 删除注册文件（`portal/<instanceID>.json`，以及若本进程是持有者则删除 `portal/portal.json`）
5. 调用 `wg.Wait()` 等待所有 goroutine 完全退出

**`claimerLoop` goroutine**：
1. 初始随机延迟 0~5 秒（`rand.Intn(5000)` 毫秒），避免多实例同时启动时的惊群效应
2. 进入死循环：尝试 `net.Listen` 抢占 portal 端口。成功则获取 `mu` 锁设置 `ln` 和 `srv`（创建 HTTP Server 并配置路由），写入 `portal.json` 声明自己为持有者，然后阻塞在 `srv.Serve(ln)` 直到 `Shutdown()` 被调用或意外错误。`Shutdown` 完成后获取 `mu` 锁将 `srv` 和 `ln` 置 nil。
3. 若 `net.Listen` 失败（端口已被占用），等待 10~15 秒随机间隔（`10s + rand.Intn(5000)ms`）后重试。随机抖动避免多个失败者同步唤醒同时重试。
4. 每次循环开始时检查 `done` channel，若已关闭则立即退出。
5. **Serve 意外退出的影响**：若 `srv.Serve()` 因 `http.ErrServerClosed` 以外的错误返回（如监听器异常关闭、系统资源耗尽），`ln` 将被关闭，下一个循环迭代中将重新绑定端口。在本次迭代的 `Serve()` 退出到下次迭代 `net.Listen` 成功之间的窗口期内（含 10~15 秒退避），Portal 不可用——反向代理返回 502、仪表板不可达、所有通过 Portal 的请求中断。`tailscaleServeLoop` 不受影响（30 秒定时检查感知到 Portal 离线后不会错误清理 tailscale serve）。**缓解措施**：`Serve()` 返回意外错误时，立即输出 Warn 日志（`[portal] HTTP serve exited unexpectedly: %v`），让用户感知到 Portal 中断，同时依靠 10~15 秒自动恢复窗口

> **已知边界情况——instance_id 与 server.json 的非原子性**：`portal.Start()` 生成 `instanceID` 并立即写入 `portal/<instanceID>.json` 注册文件，但 `server.json` 中的 `instance_id` 由 `app.go` 独立、异步写入（写入时机为 `net.Listen` 成功后）。若进程在两者之间崩溃，注册文件存在但 `server.json` 中无对应 `instance_id`。此不一致在以下场景中被自动修复：
> - Portal 活性校验（PID + TCP + instance_id 匹配）可检测并过滤该条注册文件
> - 下一轮清理周期（5 分钟）会删除无法验证存活的无效注册文件
> - 进程下次启动时会重新生成 instanceID 并写入 `server.json`

#### 注册文件结构

**`~/.config/myworktree/portal/<instance-id>.json`**（各实例写入，instance-id 格式为 `pid-timestamp-rand`，稳定唯一）：

包含字段：`instance_id`（实例唯一标识）、`pid`（进程 ID）、`port`（实例监听端口）、`host`（监听地址）、`repo_name`（仓库名）、`repo_hash`（仓库 hash）、`started_at`（启动时间 ISO 8601 格式）。注册文件中**不包含 auth_token**，token 仅保存在进程内存和 `server.json` 中。

当 `--portal-port` 设置为 0（禁用 Portal）时，实例不写入此注册文件，也不参与抢占。

**`~/.config/myworktree/portal/portal.json`**（当前 portal 持有者写入，**原子写入**：写临时文件 → rename，避免并发写损坏）：

包含字段：`instance_id`（当前持有者的实例标识）、`port`（Portal 端口）、`updated_at`（更新时间 ISO 8601 格式）。

#### 仪表板端点

| 端点 | 方法 | 认证 | 说明 |
|------|------|------|------|
| `GET /` | | 无 | 仪表板 HTML 页面。响应头包含 `Content-Security-Policy: default-src 'self'; script-src 'sha256-<hash1>' 'sha256-<hash2>' ...; style-src 'self' 'sha256-<hash1>' 'sha256-<hash2>' ...`。所有 `<hashN>` 均为对应内联 `<script>` 块的 SHA256 哈希（Base64 编码），通过 `go generate` 在编译前自动计算并写入 `csp_gen.go`，开发时修改 `dashboard.html` 后运行 `go generate ./internal/portal/` 即可更新 |
| `GET /api/csrf-token` | | 无 | 返回 `{"csrf_token":"<random>"}`，同时设置 `mw_csrf` Cookie（非 HttpOnly——JS 需读取来构造 `/api/auth` 请求体以完成 double-submit cookie 校验，这是该 CSRF 模式的必要条件；`Path=/`，`SameSite=Strict`；`Secure` 标记判断逻辑：`r.TLS != nil` 或 `r.Header.Get("X-Forwarded-Proto") == "https"` 时附加 `Secure`）。前端每次 `/api/auth` 请求前必须获取新的 CSRF token |
| `POST /api/auth` | | Body: `{ "token": "xxx", "csrf_token": "yyy" }` | 校验 token，写入 `mw_token` HttpOnly Cookie（`Max-Age=86400`，每次认证成功刷新过期时间实现滑动过期，`SameSite=Lax`，HTTPS 或 X-Forwarded-Proto 为 https 时附加 `Secure`）。**双重验证**：(1) `csrf_token` 必须匹配 `mw_csrf` Cookie 值，且 CSRF token 为一次性使用（见下方 CSRF token 服务端设计）；(2) 明文比较 `auth_token`。**含速率限制**：每 IP 每分钟最多 20 次尝试。**无 Token 配置时的行为**：若 Portal 的 `AuthToken` 为空（全局 token 未设置且未通过 `--auth` 覆盖），直接返回 `400 Bad Request`，响应 body 为 `{"error":"auth token not configured on server"}`——不允许空 token 登录（prevent accepting arbitrary input） |
| `GET /api/list` | | Cookie 或 Bearer | 返回所有运行中实例的 JSON 列表 |
| `GET /api/portal-status` | | 无 | 返回当前实例是否持有 portal 端口 |
| `POST /api/logout` | | Cookie + CSRF | 清除 `mw_token` Cookie（设置 `Max-Age=0`，`SameSite=Lax`）。Body: `{ "csrf_token": "yyy" }`，服务端校验 csrf_token 匹配 `mw_csrf` Cookie 值并标记为已使用（防 logout CSRF）。始终返回 200——即使未登录也成功（幂等） |
| `ANY /s/<repo-hash>/*` | | Cookie 或 Bearer | **反向代理**：将请求转发到对应实例的 `http://127.0.0.1:<port>`，解决跨域 Cookie 问题 |

**`GET /api/list` 响应**:

返回 JSON 对象，包含以下字段：
- `is_portal`：当前持有 Portal 端口的实例为 true
- `portal_port`：Portal 端口号
- `processes`：数组，每项包含 `instance_id`、`pid`、`port`、`repo_name`、`repo_hash`、`started_at`、`alive` 字段。`alive` 字段根据 PID 存活性和 TCP 端口可达性综合判断

**反向代理 `ANY /s/<repo-hash>/*`**:

使用 `net/http/httputil.ReverseProxy`（Go 标准库），根据 `repo-hash` 查找注册表中对应实例的端口，将请求转发至 `http://127.0.0.1:<port>`。由于代理请求通过 loopback 发送，实例的 `withAuth` 将自动放行（loopback 绕过）。

- 支持 HTTP/HTTPS。WebSocket 升级通过 `httputil.ReverseProxy` 的 `Hijacker` 接口自动处理——`httputil.ReverseProxy` 内置 WebSocket 代理支持，无需额外配置。验证方式：在集成测试中覆盖 WebSocket 双向消息互通
- 转发前校验 `mw_token` Cookie（与 `/api/list` 一致）
- 未认证时返回 401，而非代理请求
- **repo-hash 安全校验**：解析 URL 路径提取 `repo-hash` 后，必须校验其格式仅为 `[a-f0-9]+`（小写 hex），拒绝包含 `/`、`..`、`\` 等路径遍历字符的请求，返回 400 Bad Request。虽然 hash 由系统生成（`sha1` 或类似），但防御性校验防止路径注入风险

**为什么需要反向代理？**

用户可能通过不同方式访问 Portal（`http://localhost:12345`、`http://192.168.1.18:12345`、`https://machine.ts.net`）。`mw_token` Cookie 的 `SameSite=Lax` 和浏览器同源策略决定了 Cookie 只在设置它的域名下发送。如果仪表板链接直接指向实例的 `http://host:PORT`，当 Portal 和实例使用了不同的域名/主机名时，Cookie 不会送达实例。

反向代理统一了所有流量的入口：用户始终通过 Portal 端口访问，仪表板链接使用相对路径（`/s/<repo-hash>/`），Cookie 始终在同源下发送。

#### 仪表板页面（`internal/portal/dashboard.html`）

- 通过 `//go:embed dashboard.html` 内嵌
- 暗色主题，等宽字体（与主界面风格一致）
- 顶部为 Token 输入框（未认证时显示）
- 表格展示所有实例：仓库名、端口、状态、存活指示灯
- 点击行通过 Portal 反向代理跳转到对应实例
- 每 5 秒 JS fetch 局部刷新表格（无整页重载）
- **安全措施**：
   - 响应头 `Content-Security-Policy: default-src 'self'; script-src 'sha256-<hash>'; style-src 'self' 'sha256-<hash>'`（防 XSS，`<hash>` 为内联 `<script>` 和 `<style>` 块的 SHA256 哈希）。CSP 使用 hash 而非 nonce，因为 dashboard.html 是编译时嵌入的静态文件，hash 可在编译期预计算
   - CSP hash 的维护采用 `go generate` 方案：在 `internal/portal/` 目录下创建 `gen.go` 文件，使用 `//go:build ignore` 标记使其独立于正常编译，文件中包含 `//go generate` 指令和一个读取 HTML、提取 `<script>` 和 `<style>` 块、计算 SHA256 哈希（Base64 编码）、生成 Go 常量文件的实现。生成的输出文件为 `csp_gen.go`，其中包含所有内联脚本块和样式块的 SHA256 hash 常量（分别以 `[][2]string` 切片存储，每个元素包含类型 `"script"`/`"style"` 和对应 hash，在构造 CSP 响应头时分别拼入 `script-src` 和 `style-src`）。开发者在修改 `dashboard.html` 后运行 `go generate` 即可自动更新 hash，避免手动计算
   - 若存在多个内联 `<script>` 或 `<style>` 块，每个块各生成一个独立的 hash，CSP 头中 `script-src` 和 `style-src` 分别包含所有对应 hash（空格分隔）
   - **禁止在 HTML 元素上使用 `style="..."` 内联属性**——这类样式无法被 hash 白名单覆盖，会导致浏览器拒绝应用样式。所有样式必须写在 `<style>` 块中，确保 hash 可计算
   - CSP hash 过期的 CI 验证测试（见[测试策略](#测试策略)）确保 hash 与当前 HTML 内容一致——未运行 `go generate` 时 CI 失败，阻止合并
   - `repo_name` 等用户控制字段渲染前进行 HTML 文本转义（使用 `textContent` 而非 `innerHTML`）
   - 每次 `/api/auth` 请求前先获取新 CSRF token
   - **注意**：CSP 的 `script-src` 和 `style-src` 不能使用 `'self'`（会阻止内联脚本/样式），也不能使用 `'unsafe-inline'`（完全削弱 XSS 防护）。必须使用 `'sha256-<hash>'` 精确指定允许的内联块
   - **Hash 计算的数据源一致性**：`gen.go` 中 `go generate` 脚本从文件系统读取 `dashboard.html` 原始字节并计算 hash。CI 验证测试则从编译时 `//go:embed` 内嵌的字节计算 hash（`portal.DashboardHTML`）并与 `csp_gen.go` 中的常量对比。两个步骤读取的是同一个源文件，但 CI 测试确保 `go generate` 输出的 hash 与编译时实际嵌入的内容匹配——防止因文件编码转换、换行符差异、`.gitattributes` 配置等导致 hash 不匹配。**关键约束**：`gen.go` 读取文件的字节必须与 `go:embed` 结果逐字节一致（统一使用 `os.ReadFile` + 无 BOM 标记的 UTF-8 编码；`gen.go` 不应对文件内容做任何预处理、trim 或编码转换）

**CSRF Token 流程**（前端 JS）:

每次提交 auth token 前，前端执行以下步骤：
1. 调用 `GET /api/csrf-token` 获取一次性 CSRF token（响应 JSON 中的 `csrf_token` 字段），同时浏览器自动保存服务端返回的 `mw_csrf` Cookie
2. 向 `POST /api/auth` 发送 JSON body，包含 `token`（用户输入的全局 token）和 `csrf_token`（上一步获取的值）
3. 根据响应状态码判断登录是否成功

**CSRF Token 服务端设计**（`portal.go`）:

定义 `csrfState` 结构体，内部维护：
- `mu`：`sync.Mutex`，保护以下两个数据结构的并发访问
- `used`：`map[string]time.Time`，记录已使用过的 CSRF token 及其使用时间，用作一次性校验
- `rateLimits`：`map[string]time.Time`，记录每个 IP 最后一次请求 `/api/csrf-token` 的时间，用于速率限制

`generate()` 方法：生成随机 CSRF token（使用 `crypto/rand` 生成 32 字节 hex 编码），将 token 存入内存（不立即放入 `used`，而是等待校验时再标记）。

`verifyAndConsume(cookieValue, bodyValue string)` 方法，执行三重校验：
1. body 中的 `csrf_token` 必须匹配 Cookie 中的 `mw_csrf` 值（double-submit cookie 模式）
2. 该 token 未在 `used` 集合中出现过（一次性使用），且必须在生成后 5 分钟内使用（检查本地生成时间戳）
3. 校验通过后将 token 加入 `used` 集合，记录使用时间

速率限制的实现：`/api/csrf-token` 端点被调用时，检查 `rateLimits` map 中该 IP 的上次请求时间。若距上次请求不足 1 秒，返回 429。无论是否被限流，更新该 IP 的最后请求时间。

`cleanup()` 方法：后台 goroutine，每 30 秒执行一次。清理 `used` 中使用时间超过 5 分钟的条目，同时清理 `rateLimits` 中最后请求时间超过 5 分钟的 IP 记录。防止两个 map 的内存无限增长。

**关键约束**：
- CSRF token 的 TTL 为 **5 分钟**——超时后 token 失效，`/api/auth` 返回 403
- 每个 IP 每秒最多 1 次 `/api/csrf-token` 请求（防止攻击者耗尽 csrfState 内存）
- `used` map 容量上限为 10000 条（如突破则拒绝新 token 的生成），避免内存耗尽攻击。`rateLimits` map 同样设上限 10000 条
- `cleanup()` 同时清理 `used` 和 `rateLimits` 两个 map，确保过期 IP 记录不会无限累积

**URL 构造**（前端 JS）:

仪表板中使用相对路径格式 `/s/<repo_hash>/` 构造实例链接。无论用户通过 localhost、LAN IP 还是 Tailscale 域名访问 Portal，相对路径自动匹配当前域名，Cookie 始终同源发送。

效果：
- 所有请求都走 Portal 端口 12345 → `mw_token` Cookie 始终同源发送
- Portal 反向代理将请求转发至 `http://127.0.0.1:<instance_port>`
- 实例收到来自 loopback 的请求 → 自动跳过 token 校验（loopback 放行）

#### Token 流程

```
1. 用户打开 http://host:12345/
2. JS fetch /api/list → 401（无 Cookie）
3. 页面显示 Token 输入框
4. JS fetch /api/csrf-token → 获取 csrf_token + mw_csrf Cookie
5. 用户输入全局 token → POST /api/auth { token: "xxx", csrf_token: "yyy" }
   （服务端校验 csrf_token 匹配 mw_csrf Cookie 且未使用过，然后明文比较 token）
   （服务端速率限制：每 IP 每分钟最多 20 次）
6. 服务端校验通过
7. 服务端设置: Set-Cookie: mw_token=xxx; Path=/; HttpOnly; Max-Age=86400; SameSite=Lax（24 小时有效期，每次认证成功刷新 Max-Age 实现滑动过期）
   - `Secure` 标记的判断逻辑：`r.TLS != nil` **或** `r.Header.Get("X-Forwarded-Proto") == "https"` 时附加 `Secure`
   - 双重判断的原因：Tailscale Serve 在 Gate 层终止 TLS 并转发 HTTP 到本地端口，此时 `r.TLS` 为 `nil`，但 `X-Forwarded-Proto: https` 可正确反映 HTTPS 访问。HTTP 直接访问（LAN IP 直连）时不附加 `Secure`，确保 Cookie 在两种访问方式下均可用
8. 返回 200 → JS fetch /api/list 成功（Cookie 自动携带）
9. 仪表板渲染表格
10. 用户点击实例链接 → 浏览器跳转至 /s/<repo-hash>/ → Portal 代理转发 → 实例收到 loopback 请求 → 自动放行
```

**为什么用 HttpOnly + 会话级 Cookie？**

- `HttpOnly`：JS 无法读取 Token（防 XSS 窃取）
- **有效期策略**：`mw_token` Cookie 设置 `Max-Age=86400`（24 小时）。不使用纯会话级 Cookie（`Max-Age` 省略），因为 Chrome 的「Continue where you left off」设置会使会话 Cookie 在浏览器完全重启后仍然存活，用户可能无感知地保持登录状态。24 小时有效期在便利性和安全性之间取得平衡——用户一天内无需反复输入 Token，过期后自然退出
  - **滑动过期机制**：每次携带有效 `mw_token` Cookie 的请求被成功认证后（包括 `/api/list`、`/s/<repo-hash>/` 反向代理等受保护端点），服务端在处理请求的响应中重新设置 `Set-Cookie: mw_token=...; Max-Age=86400` 头，刷新过期时间。这意味着只要用户持续活跃使用仪表板（每 5 秒的 `/api/list` 轮询即自动续期），Cookie 将持续有效；若 24 小时内无任何请求，Cookie 自然过期，用户下次访问需重新输入 Token。`/api/auth` 登录成功时的 Set-Cookie 属于此机制的首次触发
- `SameSite=Lax`：仅允许同站顶级导航携带 Cookie（防 CSRF）
- `Secure`：判断条件为 `r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"`。双重判断的原因：Tailscale Serve 在 Gate 层终止 TLS 并转发 HTTP 到本地，此时 `r.TLS` 为 `nil`，需通过 `X-Forwarded-Proto` 头识别 HTTPS 访问。HTTPS 访问时附加 `Secure`，HTTP LAN 直连时不附加，确保 Cookie 在 HTTPS 下不会被明文传输，同时在 HTTP LAN 场景下仍可用

**HTTPS 与 HTTP 访问的 Cookie 隔离**：由于 `Secure` 标记根据不同访问方式动态设置，用户在同一个浏览器中通过不同方式访问 Portal 时，认证状态不互通。具体来说，通过 `https://machine.ts.net`（Tailscale HTTPS）认证获得的 Cookie 带有 `Secure` 标记，浏览器不会在 `http://192.168.1.18:12345`（LAN HTTP）请求中携带该 Cookie，用户需要重新输入 Token。反之，通过 LAN HTTP 获得的 Cookie 无 `Secure` 标记，可在 HTTPS 下发送——但此 Cookie 在 LAN 上以明文传输过，安全性低于首次即在 HTTPS 下认证。建议用户优先使用 Tailscale HTTPS 方式进行首次认证。

---

### 4. Tailscale Serve 自动管理

#### 设计原则

- **Tailscale 的 WireGuard 隧道已对网络层加密**，应用层全程 HTTP 无需额外 HTTPS 层
- `tailscale serve` 的作用是提供 `*.ts.net` 域名 + Let's Encrypt 证书，让用户无需记忆 IP
- Portal 持有者自动配置 `tailscale serve`，非持有者不管理

#### 实现逻辑

`tailscaleServeLoop` goroutine 每 30 秒检查一次：
1. 检查 `done` channel 是否关闭，若关闭则退出
2. 判断当前实例是否为 Portal 持有者（通过检查 `ln` 是否非 nil）
3. 若为持有者：
   - 调用 `tailscale serve status --json` 获取当前 tailscale serve 状态
   - 解析 JSON 中的 `"TCP"` map，查找 key 为 `:443` 的配置
    - 若 `:443` 指向本机 `127.0.0.1:<portal-port>`：已正确配置，跳过
    - 若 `:443` 指向其他端口：认为配置过期（旧 Portal 崩溃残留或端口变更），**先输出 Warn 日志**（`[portal] detaching stale tailscale serve at :443 → 127.0.0.1:<old-port>`），然后调用 `tailscale serve stop` 清理
    - 若无 `:443` 配置或 `tailscale serve status` 返回非零退出码：视为未配置
    - 需要启动时，执行 `tailscale serve --bg http://127.0.0.1:<portal-port>`（带 10 秒超时）
4. 若非持有者，跳过此轮检查

**启动时孤儿清理**：在 `Start()` 方法中，`claimerLoop` 成功抢占端口后（即 `net.Listen` 成功、`portal.json` 写入后），增加一步孤儿检测：调用 `tailscale serve status --json` 检查当前 `tailscale serve` 状态。若 `:443` 已指向其他端口（可能是上一个持有者崩溃后残留）：
 - 输出 Warn 日志记录即将清理的内容
 - 调用 `tailscale serve stop` 清理残留配置
 - 重新执行 `tailscale serve --bg` 启动新配置

**检测是否已运行**:

执行 `tailscale serve status --json` 获取结构化输出，解析 JSON 中的 `"TCP"` map，检查是否有 key 为 `:443` 的配置：

- 若指向本机 `127.0.0.1:<portal-port>` → 认为已正确配置，跳过启动
- 若指向其他端口 → 认为配置已过期（上轮 Portal 崩溃残留或端口变更），调用 `tailscale serve stop` 清理后重新启动
- 若无 `:443` 配置或 `tailscale serve status` 以非零退出码退出 → 视为未运行，启动新的 tailscale serve

**检测失败时的安全默认**：若 `--json` 不支持（旧版 tailscale）或解析失败，不自动启动 tailscale serve（避免与现有手动配置冲突），仅记录 Warn 日志。

**启动命令**:

执行 `tailscale serve --bg http://127.0.0.1:<portal-port>`，带 10 秒超时。`--bg` 标志使 tailscale serve 在后台运行。

**停止**（仅当本进程启动了 tailscale serve 时）:

执行 `tailscale serve stop` 清理配置。

#### 边界情况

| 场景 | 处理 |
|------|------|
| Tailscale 未安装/未登录 | 静默跳过，日志 Warn，不影响主服务 |
| 多个 portal 竞争 | 只有当前持有者会启动 tailscale serve |
| tailscale serve 已被用户手动配置 | 检测到已配置则跳过启动，避免冲突 |
| `tailscale serve` 已存在但指向不同端口 | 调用 `tailscale serve stop` 清理旧配置，再重新启动（处理 Portal 端口变更或旧 Portal 崩溃后残留的场景） |
| 用户退出 Tailscale | tailscale serve 自动失效，loop 下次检测到会重试 |

---

### 5. CLI 变更（`internal/cli/cli.go`）

**新增 Flag**:

在 `startCmd` 的 `flag.FlagSet` 中新增 `--portal-port` flag，整型，默认值为 12345。设为此值表示 Portal 抢占目标端口。设为 0 表示禁用 Portal（不抢占、不注册、不启动 tailscale serve）。注意：`--portal-port 0` 的语义是「禁用 Portal」，与 `--listen 0.0.0.0:0` 中 `:0` 表示「随机端口」不同。

**多用户默认端口冲突处理**：当同一台机器上多个用户同时运行 `mw start` 使用默认 portal 端口时，`claimerLoop` 会持续重试抢占失败。连续重试超过一定次数（如 10 次，即约 2 分钟后），应打印 Warn 日志提示用户当前端口被占用、建议使用 `--portal-port` 指定不同端口或 `--portal-port 0` 禁用仪表板。

**交互式 Config 子命令**:

在 `Run()` 函数中新增 `case "config":` 分支，根据 `args[2]`（若存在）路由：
- `set-auth`：交互式提示输入 Token 并二次确认后保存
- `get-auth`：直接输出当前 Token（掩码形式，仅显示前 4 位和后 4 位）
- `clear-auth`：直接清除 Token（无需确认）
- 无参数或未知参数：进入交互式引导菜单（选项：[1] 设置 Token、[2] 查看 Token、[3] 清除 Token、[q] 退出）

**Auth 自动填充**（在 `startCmd` 中）:

解析完所有 flag 后，若 `--auth` flag 的值为空字符串，调用 `config.Load()` 读取全局配置。若读取成功且 `AuthToken` 非空，将其赋值给 auth 变量。若 `Load()` 返回错误（如文件不存在或 JSON 解析失败），忽略错误并保留 auth 为空——不阻断启动流程。

---

### 6. App 变更（`internal/app/app.go`）

**Config 结构体扩展**:

在现有 `Config` 结构体中新增 `PortalPort int` 字段。默认值为 0（由调用方 `cli.go` 通过 flag 默认值 12345 传入）。当 PortalPort > 0 时启用 Portal 功能。

**server.json 扩展**（向后兼容）:

`serverConfig` 结构体增加 `InstanceID string` 字段（JSON tag `instance_id,omitempty`）。当 InstanceID 为空时 JSON 序列化省略该字段，确保旧版本文件不受影响。

**写入策略改造**：`writeRepoListenPort()` 当前以全新结构体覆盖写入文件。实施时改为「读取-合并-写入」模式：先 `readFile` 读取并解析现有 JSON（使用宽容模式：未知字段忽略，缺失字段默认为零值），仅更新 `listen_port` 字段，保留 `instance_id` 等已有字段，再写回。同时采用原子写入（写临时文件 → rename），与 `auth.json` 保持一致，避免进程崩溃时 `server.json` 损坏。

**迁移注意事项**：
- **必须停止所有旧版本实例后再启动新版本**：旧版本 `writeRepoListenPort()` 使用覆盖写入（无 read-merge-write），与新版本并发运行时会导致 `instance_id` 字段在旧版本写操作后被清除。不接受此数据丢失——部署升级时由用户责任确保旧版本进程已全部退出
- 原子写入的 `os.Rename` 在同一文件系统上为原子操作（Unix 保证），跨文件系统可能失败——但 `server.json` 与其临时文件位于同一目录，此场景不会发生
- JSON 解析使用 `json.NewDecoder` + 宽容模式（忽略未知字段，缺失字段默认零值），确保向前兼容

**Portal 生命周期集成**（在 `Server.Start()` 中）:

在现有 `resolveListenAddr` 和 `net.Listen` 成功后，增加 Portal 启动逻辑：
1. 若 `PortalPort > 0`，构造 `portal.Config`，填入 portal 端口、监听主机、auth token、注册目录路径、数据目录、仓库名和 hash
2. 调用 `portal.New(cfg)` 创建 Portal 实例
3. 调用 `portal.Start()` 启动（内部生成 instanceID、写入注册文件、启动 claimerLoop 和 tailscaleServeLoop）
4. 若 `portal.Start()` 返回错误，记录日志但主服务继续运行（Portal 失败不阻断核心功能）

**Defer 清理**:

在 `Start()` 的 defer 块中检查 `s.portal` 是否非 nil，若非 nil 则调用 `s.portal.Stop()` 执行优雅关闭（关闭 done channel、Shutdown HTTP 服务、清理 tailscale serve、删除注册文件、等待 goroutine 退出）。

---

### 7. Cookie 认证（withAuth 改造）

详见 [第 2 节「Auth 中间件改造」](#2-auth-中间件改造loopback-始终放行)，主要变更：

- **loopback 放行**：`isLoopbackRequest(r)` 检查置于最前，loopback 请求跳过 origin 校验和 token 校验
- **sameOriginHost 保留**：非 loopback 请求仍需 Origin 匹配 Host（防 CSRF）
- **Cookie 来源**：token 读取优先级为 `Authorization` → `?token=` → `mw_token` Cookie
- **速率限制保留**：`allowAuthAttempt` / `resetAuthAttempts` 对非 loopback 请求照常生效

**关于 `sameOriginHost` 的防护边界**：

`sameOriginHost` 通过比较请求头 `Origin` 与 `Host` 来防止跨域请求。此检查依赖浏览器诚实发送 `Origin` 头——非浏览器 HTTP 客户端（curl、wget、Python requests 等）可自由伪造 `Origin` 和 `Host` 头使其匹配，绕过此检查。因此 `sameOriginHost` 仅作为**纵深防御层**，不能作为独立安全边界。核心安全依赖仍然是 token 校验（256-bit 随机值不可暴力破解），`sameOriginHost` 配合 `SameSite=Lax` 和 CSRF double-submit cookie 共同防护浏览器级别的 CSRF 攻击。

**关于直接访问实例**：

用户仍可直接通过 IP 访问实例端口（跳过 Portal 代理），这是现有行为并保持兼容：
- `http://localhost:<port>` → loopback 放行，无需 token
- `http://192.168.1.18:<port>` → Origin 与 Host 一致，`sameOriginHost` 通过
- `http://100.x.x.x:<port>`（Tailscale IP）→ Origin 与 Host 一致，`sameOriginHost` 通过
- `mw start --open` 自动打开浏览器时，默认跳转到实例直连地址

`tailscale serve` 域名（`https://machine.ts.net`）仅用于 Portal 端口，不用于直接访问实例端口。实例端口的 `sameOriginHost` 不处理 `tailscale serve` 域名场景，因为该场景不在计划范围内。

**双层认证架构说明**：Portal 和实例各自维护独立的认证逻辑（Portal 通过 `mw_token` Cookie + CSRF，实例通过 `withAuth` 中间件），反向代理利用 loopback 绕过实例 auth。两者的认证状态独立，存在潜在不一致风险：若 Portal 的 Cookie 校验实现与实例的 `withAuth` token 比较逻辑出现偏差，可能导致认证绕过。**缓解措施**：Portal 反向代理在转发前严格校验 Cookie（与 `/api/list` 一致逻辑），且对 repo-hash 做格式校验防路径注入。

---

## 网络安全说明

**全程 HTTP + WireGuard 加密**:

| 访问路径 | 协议 | 加密层级 |
|----------|------|----------|
| 实例直连（本地/LAN IP） | `http://192.168.1.18:PORT` → 实例 | 无（仅 LAN 可及） |
| 实例直连（Tailscale IP） | `http://100.x.x.x:PORT` → 实例 | WireGuard 隧道加密 |
| Dashboard + 实例（Portal 代理，本地/LAN） | `http://host:12345` → proxy `http://127.0.0.1:PORT` | 无（仅 LAN 可及） |
| Dashboard + 实例（Portal 代理，Tailscale IP） | `http://100.x.x.x:12345` → proxy `http://127.0.0.1:PORT` | WireGuard 隧道加密 |
| `tailscale serve` 域名访问 | `https://machine.ts.net` → proxy `http://127.0.0.1:PORT` | Let's Encrypt TLS + WireGuard |

**说明**：
- Tailscale 的 WireGuard 隧道已对网络层加密，应用层无需重复 TLS
- 实例直连（不经过 Portal 代理）是现有行为并保持兼容；用户启动 `mw start --open` 时自动打开直连地址
- 走 Portal 代理时，所有用户流量统一经过 Portal 端口的反向代理转发至实例，实例仅接收来自 `127.0.0.1` 的代理请求
- 走 `tailscale serve` 的 HTTPS 域名访问时，TLS 证书由 Let's Encrypt 提供，浏览器显示安全
- 裸 IP 的 HTTP 访问仅限 LAN 可及，外网用户推荐用 Tailscale IP 或域名

---

## 边界情况处理

| 场景 | 处理方式 |
|------|----------|
| Portal 进程崩溃 | goroutine 退出 → 端口释放 → 其他实例 10~15 秒内感知并接管（初始 0~5 秒随机延迟 + 10~15 秒重试间隔） |
| 无进程持有 Portal | 仪表板不可达，最多 15 秒内必有进程接管。**此故障转移窗口为已知设计取舍**：10~15 秒的中断对开发工具场景可接受；用户可通过实例直连 IP:PORT 绕过此窗口 |
| 多进程指定相同 Portal 端口 | 正常 TCP 竞争，先 listen 者胜（含 0~5 秒初始随机延迟避免惊群效应），失败者每 10~15 秒重试（含随机抖动避免同步唤醒） |
| Tailscale 未安装 | 回退到 listen host，仪表板正常可用 |
| Tailscale 未运行 | 同上，优雅降级 |
| Tailscale serve 已被手动配置 | 检测到已配置则跳过自动启动，避免冲突 |
| tailscale serve 启动失败 | 日志 Warn，Portal 主功能不受影响 |
| `tailscale serve` 孤儿残留（Portal 持有者崩溃） | **已知窗口**：Portal 持有者崩溃后，`tailscale serve` 继续运行并指向已释放的端口。下一个持有者接管时立即检测（在 `Start()` 中做启动时孤儿清理，不等 30 秒定时检查），调用 `tailscale serve stop` 清理后再启动新配置。若所有 myworktree 实例均退出，残留的 `tailscale serve` 进程不会被自动清理——用户下次启动任意 `mw start` 时触发接管清理 |
| portal.json 并发写入 | 使用「写临时文件 → rename」原子写入模式，避免文件交错损坏 |
| 注册目录 (portal/) 被删除 | Portal 持有者在下次清理周期（5 分钟）自动重新创建目录并恢复注册文件 |
| 注册目录权限异常 / 磁盘满 | 注册写入或清理失败时记录 Warn 日志，Portal 核心功能降级但主服务不受影响 |
| `server.json` 回退逻辑导致实例监听 loopback | 若用户指定 `--listen 0.0.0.0:0` 但因极端情况回退到 `127.0.0.1:随机端口`，实例直连 IP 访问失效，但 Portal 反向代理（通过 loopback）仍可用 |
| Portal 持有者崩溃导致代理中断 | Portal 持有者崩溃后，所有通过 Portal 反向代理的流量中断（`/s/<repo-hash>/` 返回 502 或连接拒绝），正在进行的 WebSocket 连接立即断开。用户需等待最多 15 秒（其他实例接管）后手动刷新页面恢复。在此期间可通过实例直连 IP:PORT 绕过。此**单点故障窗口**约 10~15 秒，对开发工具场景可接受。若所有 myworktree 实例均已退出，无进程接管 Portal，代理永久不可用 |

---

## 用户使用流程

```bash
# 1. 一次性配置全局 Token（交互式引导）
mw config
# → 选择 [1] 设置全局 Token → 输入并确认
# （Token 保存在 ~/.config/myworktree/auth.json，独立于 LLM 配置）

# 2. 启动各仓库实例（自动继承全局 Token）
mw start --listen 0.0.0.0:0              # 在 repo-a 目录
cd ~/projects/repo-b && mw start --listen 0.0.0.0:0  # 在 repo-b 目录

# 输出：
# Portal dashboard at:
#   http://0.0.0.0:12345/
# Tailscale: https://my-machine.tail-scale.ts.net/
# （若 Portal 禁用则不输出以上两行）

# 3. 打开仪表板（两种方式，体验一致）
# 方式 A: LAN IP
open http://192.168.1.18:12345/
# 方式 B: Tailscale 域名（自动 HTTPS）
open https://my-machine.tail-scale.ts.net/

# 4. 首次访问需输入 Token → 存入 Cookie（24小时有效期，滑动过期）

# 5. 查看所有实例，点击直达（通过 Portal 反向代理，Cookie 自动携带）
```

```bash
# 单个实例覆盖 Token
mw start --listen 0.0.0.0:0 --auth "override-token"

# 自定义 Portal 端口
mw start --listen 0.0.0.0:0 --portal-port 12346

# 禁用 Portal（同时不启动 tailscale serve）
mw start --listen 0.0.0.0:0 --portal-port 0
```

---

## 实施顺序

| 步骤 | 内容 | 复杂度 |
|------|------|--------|
| 1 | `internal/config/global.go` — 全局 auth 配置读写（`auth.json`，原子写入，仅明文存储，`Load()` 容错处理：文件不存在返回零值 + nil error，JSON 解析失败返回零值 + 非 nil error） | 低 |
| 2 | `cli.go` — 交互式 `config` 子命令（`Run()` 中新增 `case "config":` 分支，二级子命令路由） + 全局 Token 自动填充 + `--portal-port` flag | 中 |
| 3 | `app.go` — Config 结构体扩展 + `server.json` 读写改为读取-合并-写入 + 原子写入 | 中 |
| 4 | `app.go` — `withAuth` loopback 跳过 + sameOriginHost 保留 + Cookie 读取 + resetAuthAttempts | 中 |
| 5 | `internal/portal/portal.go` — 抢占者（含初始随机延迟 + 随机抖动，`sync.Mutex` 保护 srv/ln 字段）+ 注册、Portal HTTP 服务（`http.Server` 优雅关闭）+ CSRF token 端点 + CSRF token 内存状态管理（含 `rateLimits` map 及其 cleanup）+ 无效注册文件定期清理（含 instance_id 校验）+ 多用户端口冲突日志提示 | 高 |
| 6 | `internal/portal/portal.go` — 反向代理（`/s/<repo-hash>/*` → 实例，含路径格式校验、WebSocket 升级代理验证） | 中 |
| 7 | `internal/portal/portal.go` — `tailscaleServeLoop()` 自动管理（`--json` 检测 + 端口不匹配清理，超时控制，portal.json 原子写入）+ **启动时孤儿清理**（在 `claimerLoop` 抢占成功后立即检测并清理残留的 tailscale serve 配置） | 中 |
| 8 | `internal/portal/dashboard.html` — 仪表板页面 + JS 局部刷新 + CSRF token 流程 + HTML 转义 | 中 |
| 9 | `internal/portal/gen.go` — `go generate` 脚本：读取 dashboard.html，计算所有内联 `<script>` 和 `<style>` 块的 SHA256 hash（Base64），生成 `csp_gen.go` 包含 CSP hash 常量（区分 script/style 类型） | 低 |
| 10 | `app.go` — Portal 生命周期集成（WaitGroup 优雅关闭） | 中 |
| 11 | 端到端测试验证（含 CSP hash CI 验证测试） | 高 |

---

## 测试策略

| 测试类别 | 覆盖内容 |
|----------|----------|
| **单元测试** | `global_test.go`: `Save()` 原子写入完整性、文件权限 `0o600`、重复写入一致性。`Load()` 容错：文件不存在时返回零值空配置 + nil error、JSON 解析失败时返回零值空配置 + 非 nil error（不 panic） |
| | `portal_test.go`: `generateInstanceID()` 唯一性、注册文件读写、`isPortalHolder()` 逻辑、活性校验（PID 不存在、TCP 端口不可达、PID+TCP 都无效、instance_id 不匹配）、CSRF token 生命周期（生成 → 使用 → 过期/重复使用被拒绝）、CSRF `rateLimits` 过期清理正确性。`Portal.srv/ln` 并发读写无 data race（通过 `go test -race` 验证） |
| | `app_test.go`: `withAuth` loopback 放行、Cookie token 读取优先级、`sameOriginHost` 保留行为、速率限制保留 |
| | **CSP hash 验证测试**：读取编译时内嵌的 `dashboard.html` 和 `go generate` 生成的 `csp_gen.go` 中的 hash 常量，提取所有内联 `<script>` 和 `<style>` 块并计算 SHA256 hash（Base64），与常量进行断言对比。此测试**在 CI 中强制通过**，防止修改 HTML 后忘记运行 `go generate` 导致仪表板 JS/CSS 全部静默失效 |
| **并发测试** | 多 goroutine 抢占同一 portal 端口（验证先 listen 者胜 + 失败者重试） |
| | Portal 注册目录的并发读写与清理（验证清理不误删活跃实例，含 PID 回收场景下 instance_id 匹配校验） |
| | `Portal.Stop()` 与 `claimerLoop` 的并发交互（验证 `closeOnce` + `sync.Mutex` 确保优雅关闭无 panic） |
| **集成测试** | 反向代理转发（正常请求 → 200，目标离线 → 502）、WebSocket 升级转发（双向消息互通）、repo-hash 格式校验拒绝非法字符 |
| | `tailscale serve` 命令模拟（mock 外部命令输出，验证检测逻辑的三种分支：已配置同端口/已配置不同端口/未配置）。验证启动时孤儿清理：残留 tailscale serve 指向错误端口 → 新持有者接管后 `Start()` 立即检测并清理 |
| | 全局 token 自动填充（`--auth` 为空时从 `auth.json` 加载；`auth.json` 不存在时无 token 继续启动；`auth.json` 损坏时 Warn 日志 + 保留空值不阻断启动） |
| | 注册目录被删除后 Portal 恢复能力（下一个清理周期自动重建） |
| | `/api/auth` 在 `AuthToken` 为空时返回 400（含 `{"error":"auth token not configured on server"}`） |
| | `/api/logout` 需携带有效 CSRF token（body 中 `csrf_token` 匹配 `mw_csrf` Cookie）；无 CSRF token 或 CSRF token 不匹配时返回 403 |
| | **端到端认证流程测试**：使用 `httptest.Server` 模拟完整的浏览器认证序列——获取 CSRF token → 提交 auth → 验证 Cookie 设置 → 访问 `/api/list` 获取实例列表 → 通过 `/s/<repo-hash>/` 代理访问实例页面 → 验证 loopback 绕过 → 验证 `/api/list` 响应包含 `Set-Cookie` 头刷新滑动过期 → 调用 `/api/logout`（携带 CSRF token）清除 Cookie → 再次请求返回 401。此测试验证 CSP、CSRF、Cookie、反向代理、滑动过期、logout CSRF 防护的协同正确性 |

---

## 可观测性

| 维度 | 方案 |
|------|------|
| **结构化日志** | Portal 关键操作使用 `log.Printf("[portal] <action>: <detail>")` 前缀格式，覆盖以下事件：抢占成功（含端口号和 instanceID）、抢占失败（端口被占用时记录连续失败次数，超过 10 次后额外提示考虑 `--portal-port`）、放弃持有者（Shutdown 完成）、HTTP Serve 意外退出（`srv.Serve` 返回非 `ErrServerClosed` 错误时输出 Warn，含错误原因）、注册文件写入/删除成功与失败、tailscale serve 启动成功/启动失败（含错误原因）/停止/跳过（含原因：已配置/检测失败/Tailscale 未安装）、tailscale serve 孤儿清理（检测到残留配置 → 清理 → 重新启动）、反向代理 502 错误（含目标实例信息）、清理周期执行（含删除的无效注册文件数量）。避免纯 `fmt.Println`，保持与现有 `s.logger.Printf` 风格一致 |
| **启动输出** | `mw start` 启动时在控制台输出 Portal 相关地址：`http://<host>:<portal-port>/`（仪表板）和若 Tailscale 可用则输出 `https://<machine>.ts.net/`。当 Portal 被禁用（`--portal-port 0`）时不输出 Portal 地址行 |
| **Health Check** | 无需额外端点，`GET /api/portal-status` 返回当前实例是否持有 portal 端口，可用作简单健康探测 |
| **活性诊断** | `GET /api/list` 返回的 `processes[].alive` 字段基于 PID + TCP 可达性，帮助排查注册文件残留和死进程问题 |
| **错误降级** | Portal 组件启动失败（端口被占、目录无权限等）记录日志后主服务继续运行，不影响核心功能 |

---

## 安全总结

| 风险点 | 缓解措施 |
|--------|----------|
| Token 被 JavaScript 读取 | HttpOnly Cookie，JS 不可访问 |
| Token 出现在 URL 中 | 不使用 `?token=`；Cookie + Portal 代理自动发送 |
| XSS 窃取 Token | HttpOnly 防止 JS 读取；仪表板 `Content-Security-Policy: default-src 'self'; script-src 'sha256-<hash>'; style-src 'self' 'unsafe-inline'`（hash 通过 `go generate` 自动维护） |
| XSS 注入恶意数据 | `repo_name` 渲染使用 `textContent` 而非 `innerHTML` 做 HTML 转义 |
| 关闭浏览器后 Token 长期有效 | Cookie 设置 `Max-Age=86400`（24 小时），每次认证成功刷新过期时间（滑动过期） |
| CSRF 攻击 | 三重防护：(1) `/api/auth` 和 `/api/logout` 需一次性 CSRF token（double-submit cookie 模式）；(2) `SameSite=Lax`；(3) `sameOriginHost` 检查 Origin 头（注意：此检查依赖浏览器诚实发送 Origin，非浏览器客户端可绕过，仅作为纵深防御层） |
| Portal 登录暴力破解 | `POST /api/auth` 每 IP 每分钟最多 20 次尝试 |
| CSRF token 接口被滥用耗尽内存 | `/api/csrf-token` 每 IP 每秒最多 1 次；`rateLimits` map 定期清理过期条目（30 秒周期）；`used` 和 `rateLimits` map 各有 10000 条容量上限 |
| auth.json 文件泄露导致 token 暴露 | `auth_token` 仅明文存储（不引入 bcrypt 哈希）。风险接受理由：`auth_token` 明文已同时用于 `withAuth` 中间件字符串比较、反向代理转发和 Cookie 设置，引入哈希无法消除明文存储需求，安全收益微乎其微。缓解措施：(1) 严格 `0o600` 文件权限；(2) 推荐使用 `openssl rand -hex 32` 生成高熵 token（256-bit 随机） |
| 实例被外部直接访问 | 实例绑定 `0.0.0.0`，但 token 校验对所有非 loopback 请求强制生效 |
| 代理绕过认证 | 反向代理仅监听 Portal 端口内的路径（`/s/<repo-hash>/`），转发前先校验 Cookie |
| Loopback 始终放行 | 对 `127.0.0.1`/`localhost`/`::1` 不校验 Token（仅代理内部转发使用） |
| portal.json / server.json 进程崩溃时文件损坏 | 所有持久化文件均采用原子写入（写临时文件 → rename） |
| 日志输出泄露敏感信息 | Token 仅以掩码形式（前 4 位 + 后 4 位）出现在日志和终端输出中 |

---

## 注意事项

- **URL 统一代理**：前端使用相对路径 `/s/<repo-hash>/` 构造链接，所有实例流量通过 Portal 反向代理，Cookie 始终同源发送
- **实例直连仍可用**：用户可通过 IP 直接访问实例端口（`http://<ip>:PORT`），`mw start --open` 默认使用直连地址；`sameOriginHost` 对 IP 访问天然兼容
- **Token 不写入注册表**：实例的 token 仅保存在进程内存和 `server.json` 中，Portal 注册文件（`<instance-id>.json`）不含 token
- **Token 仅明文存储**：`auth_token` 明文存储在 `auth.json` 中（0o600 权限），供反向代理转发、Cookie 设置、`/api/auth` 和 `withAuth` 校验使用。不引入 bcrypt 哈希（安全价值分析见[安全总结](#安全总结)）
- **CSRF 三重防护**：`/api/auth` 和 `/api/logout` 要求一次性 CSRF token（double-submit cookie），加上 `SameSite=Lax` + `sameOriginHost`（`sameOriginHost` 依赖浏览器诚实发送 Origin 头，仅作为纵深防御）
- **原子写入**：`auth.json`、`portal.json`、`server.json` 全部使用「写临时文件 → rename」原子写入模式，防止进程崩溃或并发写导致文件损坏
- **注册文件定期清理**：Portal 持有者每 5 分钟扫描注册目录，删除无法验证存活的死进程注册文件
- **启动时孤儿清理**：`claimerLoop` 抢占门户端口成功后，立即检测并清理残留的 `tailscale serve` 配置（无需等待 30 秒定时检查），确保上一个持有者崩溃后残留的 serve 进程被立即清除
- **向后兼容**：`server.json` 使用宽容的 JSON 解析，缺失字段默认为零值；`writeRepoListenPort()` 改为读取-合并-写入 + 原子写入模式
- **Tailscale serve 独占性**：同一时刻只有 portal 持有者启动 tailscale serve，避免端口冲突
- **多用户隔离**：若同一台机器多个用户运行 `mw`，各自 `~/.config/myworktree/` 天然隔离；Portal 路径也各自独立。但默认 Portal 端口（12345）全局唯一，多个用户同时使用时只有一个能成功抢占，其他用户需通过 `--portal-port` 指定不同端口
- **CSP hash 维护**：`dashboard.html` 中内联 `<script>` 变更后运行 `go generate ./internal/portal/` 自动更新 `csp_gen.go` 中的 hash 常量。CI 验证测试确保 hash 与 HTML 内容一致
- **Cookie 有效期**：`mw_token` Cookie 设置 24 小时 `Max-Age`，每次认证成功刷新过期时间（滑动过期）。用户一天内无需反复输入 Token
- **`config.Load()` 容错**：文件不存在时返回零值空配置 + nil error（正常情况）。JSON 解析失败时返回零值空配置 + 非 nil error，调用方打印 Warn 日志提示用户 auth.json 已损坏。在任何情况下不 panic、不阻断启动，确保新增功能不影响未配置用户的正常使用