# OpenCode 原生 Web UI 实例 — 实施计划

> **范围**：在 myworktree 中新增一种 instance 类型 `opencode-web`，与现有 PTY 实例并存；把 `opencode serve` 进程作为 instance 托管，复用现有 tag / 实例生命周期 / tab 切换 UI，把 opencode 官方 web UI 嵌入主面板。
>
> **本目录三份文档的关系**：
> - `FEASIBILITY.md` §0 = 需求背景；§1–§2 = 调研结果（opencode 架构 + UI/agent 问题根因）；§3 = 方案 A/B/C 对比；§4 = 方案 A 设计详图；§5 = agent 故障排查；§6 = 待决策项
> - 本文档 = 把方案 A 落到代码的具体计划：实施步骤、改动文件清单、测试 / 手测 checklist
> - （实施开始后）`TASK.md` = 拆分到 PR-ready 的子任务清单

---

## Context

myworktree 当前对 opencode 的支持方式是「PTY + xterm.js」：每个 instance = 一个 zsh 进程跑 `opencode` TUI，浏览器通过 WebSocket 桥接 PTY。这条链路有结构性问题（详见 `docs/TERMINAL_FILTER_REVIEW.md`、`docs/GHOSTTY_WEB_RESEARCH.md`、`memory/project_mw_disk_write_issue.md`）：OSC/DA 查询回声污染、TUI 高频重绘造成 CPU 高、键盘鼠标跨 PTY 往返脆弱。

调研（见 `FEASIBILITY.md` §1）发现 opencode 本身已经把 server 与 TUI 拆分（`packages/opencode/src/server/server.ts`），server 是**多 directory / 多 session 的纯 HTTP 服务**（`middleware/workspace-routing.ts:86-88` 通过 `?directory=` 或 `x-opencode-directory` 决定 instance），web UI 是独立 SolidJS 应用（`packages/app/`）。可以**完全跳过 PTY 与 xterm.js**，直接起 `opencode serve` 并把它的官方 web UI 嵌入 myworktree。

经用户澄清（2026-06-20，见会话记录）后的目标行为：

- 每个 **opencode-web** instance = 1 个独立的 `opencode serve` 进程（自带端口 + 自带 `OPENCODE_SERVER_PASSWORD`），与现有 PTY 实例并存而非替换
- **同一 worktree 可启 N 个** opencode-web instance（每个一份独立 session 历史 / plugin 状态），与现有「同一 worktree 多 PTY instance」的语义对齐
- 多个 opencode UI 的切换 = **复用 myworktree 现有 instance tab 切换**，不引入 opencode 自带的「workspace 管理 / 多 server」首页
- 继承 myworktree 现有设计语言：tag 系统、instance 生命周期、ring buffer、sidebar/tab 列表

## 方案选型

**采用方案 A**（每个 opencode-web instance = 1 个 opencode 进程，多实例天然支持）。理由与方案 B/C 的对比详见 `FEASIBILITY.md` §3，核心考量：

- 改动量适中（不 fork opencode 源码）
- 隔离性最强（每个 instance 独立 provider / plugin / session / db）
- 生命周期与现有 instance 模型对齐（一个 instance 死了不影响其他）
- 资源占用虽高（每个进程一份 bun + node），但单 worktree 启 2–4 个 opencode 在 macOS 上内存压力可控

---

## Approach

新增 instance `Kind = "opencode-web"`（与现有 PTY instance 并存）；后端用 `os/exec` 直接 spawn（不走 PTY），抓 stdout 解析端口、注入随机密码、把元信息存到 `instance.extra`；新增 Go 端 reverse proxy `/__opencode/<id>/*` 把请求代理到 opencode server（注入 Basic auth header 和 `?directory=<worktree>`）；前端在 instance tab 渲染时按 Kind 分支：`pty` 显示 xterm.js（现状），`opencode-web` 显示 iframe。

**约束**：

- **不修改 opencode 源码**
- **不动现有 PTY opencode TUI 实例**（`docs/PRD.md` §7 的 PTY + Web TTY 描述保持原样）
- **不引入新 Go 第三方依赖**（`httputil.ReverseProxy` 已够用）

### 安全姿态

**威胁模型**：用户对自己的机器有完全控制权（已经有 PTY 实例跑任意命令的能力），需要防御的**唯一**外部威胁是「同 LAN/WAN 其他主机扫描端口触达 myworktree 托管的 opencode server」。**不做用户层对抗**——和 PTY 实例的语义一致。

**默认安全 + 参数锁定**：`opencode-web` 这个 **kind** 是 myworktree 托管的固定形态：

- 命令 = `opencode serve --hostname 127.0.0.1 --port 0`（**Go 代码里硬编码**）
- 用户通过 `tags.json` / 前端**无法改写** `Command` 和 `--hostname` / `--port` 这两个 flag——改也无效

理由：

1. **零实用价值**：myworktree 内的 `opencode-web` 是「在 iframe 里给你看官方 web UI」的快捷方式，绑 127.0.0.1 是该场景唯一有意义的目标地址
2. **避免脏数据**：开放 `--hostname 0.0.0.0` 等「看似无害」的定制会让用户在没意识到 LAN 暴露的情况下把 opencode 暴露给同网段主机
3. **真要外网/LAN 访问的用户请走其他路径**：
   - 在 myworktree 外自己 `opencode serve --hostname 0.0.0.0 --port 14096`（不归本特性管）
   - 或者用 myworktree 的 PTY 实例跑同一个命令（自负责任）
   - 这两条路径都不会触发 myworktree 的 `opencode-web` kind lifecycle

`OPENCODE_SERVER_PASSWORD` 和 `OPENCODE_CLIENT` 同样由 myworktree 强制注入/覆盖，不允许关闭——密码为空 = opencode 裸奔。

**允许用户通过 tags.json 设置**的：

- `tag.Env` 中的其他 env（如 `OPENCODE_EXPERIMENTAL`、`OPENCODE_DISABLE_DEFAULT_PLUGINS` 等非安全相关的配置）—— 通过 `BuildEnv` helper 合并（**`OPENCODE_SERVER_PASSWORD` 和 `OPENCODE_CLIENT` 仍被强制覆盖**）
- 新增完全独立的 tag（如 `id: "my-opencode"`，command 任意）—— 走普通 PTY 路径，不被 `opencode-web` kind 捕获

**不允许用户改的**：

| 项 | 行为 | 理由 |
| --- | --- | --- |
| 命令（`opencode serve`） | 硬编码 | 改它就脱离「托管实例」语义 |
| `--hostname` | 硬编码 `127.0.0.1` | 改它就能 LAN 暴露；用户真要 LAN 暴露请走 PTY 实例自起 |
| `--port` | 硬编码 `0`（opencode 自选空闲端口） | 改固定端口无价值且可能冲突；myworktree 需要 stdout 解析出真实端口 |
| `OPENCODE_SERVER_PASSWORD` | 等于 myworktree `cfg.AuthToken`（**统一认证 token**）；`BuildEnv` 强制覆盖 tag 里用户写的值 | 由 myworktree bearer token 在 `/__opencode/` proxy 层把关；详见 ARCH §8 威胁模型与评审检查表 |
| `OPENCODE_CLIENT` | 强制设为 `myworktree` | 仅用于 opencode 内部标识 |

**凭证统一**：所有 opencode-web 实例共享 `cfg.AuthToken` 作为上游密码（持久化于 `~/.config/myworktree/auth.json`，0o600）。`state.json` 不再写 per-instance `extra["password"]` 字段——所有实例共用一份凭证，proxy 注入 Basic auth 时也用同一 token。详见 ARCH §8 威胁模型。

**前端透明度**：当用户在 add-instance 选 `opencode-web` tag 时，UI 旁边显示一段说明：

> **托管实例**：仅监听 `127.0.0.1`（loopback），端口由 myworktree 协调（opencode 自选空闲端口）。命令、hostname、port 由 myworktree 固定——如需 LAN 访问请另起 PTY 实例或 myworktree 外自己跑 opencode。
>
> 密码由 myworktree 用全局 `cfg.AuthToken` 强注入（所有实例共享同一 token，无法关闭）。可通过 `tag.Env` 设置其他 opencode 配置项（如 `OPENCODE_EXPERIMENTAL`）。详见 ARCH §8 威胁模型。

**残余风险（依赖用户自律或上游修复）**：

- opencode 进程 env 仍可被同机 root 通过 `ps auxe` 读到（PTY 实例下用户已具备等同能力，threat model 一致）
- opencode 自身协议/解析层漏洞——out of scope，由 opencode 上游负责
- `.opencode/config.json` 等 worktree 内配置文件的篡改——out of scope

**未做的加固**（用户明确否决或与本特性范围不符）：

- ❌ 启动后用 `lsof` / `/proc` 验证 opencode 实际绑 127.0.0.1（用户否决；命令已硬编码，信任命令一定生效）
- ❌ macOS `pf` 防火墙规则锁端口（用户否决；依赖 127.0.0.1 绑定的 OS 默认行为）
- ❌ 允许用户改 `--hostname` / `--port`（用户否决；理由：零实用价值，会让用户在没意识到的情况下把 opencode LAN 暴露）

---

## 关键修改文件

### 后端（Go）

#### `internal/store/state.go`

`ManagedInstance` struct（`internal/store/state.go:30-45`）增加：

```go
Kind  string            `json:"kind,omitempty"`  // 缺省 "pty"
Extra map[string]string `json:"extra,omitempty"` // 仅 opencode-web 使用
```

- JSON unmarshal 走 `omitempty` 即可天然向后兼容：旧 `state.json` 没这俩字段 → unmarshal 后 `Kind == ""`、`Extra == nil`；`Manager.Start()` 入口处把 `Kind == ""` 归一化为 `"pty"`
- 不需要自定义 `UnmarshalJSON`

#### `internal/instance/manager.go`

`StartInput`（`internal/instance/manager.go:243` 附近）增加字段：

```go
Kind string // "pty" (default) | "opencode-web"
```

`Manager.Start()`（`internal/instance/manager.go:193`）按 Kind 分叉：

- **`Kind == "pty"`（默认）**：走现有 `pty.Start()` 路径（`internal/instance/manager.go:332`），零改动
- **`Kind == "opencode-web"`**：新分支
  1. `cmd := exec.CommandContext(ctx, "opencode", "serve", "--hostname", "127.0.0.1", "--port", "0")`（**完全硬编码**，不读 `tag.Command`，不解析用户输入；详见安全姿态 §0.5）
  2. **env 拼接**：以 `os.Environ()` 为基础，叠 `tag.Env`（用户可设的非安全 env），**强制覆盖** `OPENCODE_SERVER_PASSWORD=<myworktree 生成>` 和 `OPENCODE_CLIENT=myworktree`（详见下方 helper `BuildEnv`）
  3. `cmd.Dir` 设为 worktree 路径
  4. `cmd.Stdout` / `cmd.Stderr` 通过 pipe 接到 `pumpLogs()` 同一逻辑（接入 ring buffer `internal/instance/logbuf.go`）
  5. 起 `go opencode.parseAndWatch(stdout, inst, m)`：扫描 `opencode server listening on http://<host>:<port>` 行 → 正则提取 host + port → 写 `inst.Extra["host"]`/`["port"]`/`["password"]`/`["worktree_abs"]`/`["url_path"]` → 状态置 `running`（host 理论上恒为 `127.0.0.1`，但解析出来备用，便于将来扩展）
  6. 起 `go opencode.healthLoop(inst, m)`：每 5s `GET http://<host>:<port>/global/health`（host/port 来自 inst.Extra，Basic auth 用 inst.Extra["password"]），连续 3 次失败 → 状态置 `failed`
  7. `cmd.Wait()` 由现有 `m.wait()`（`internal/instance/manager.go:398`）监听，退出时清理 `inst.Extra` 并状态置 `exited`

#### `internal/instance/opencode.go`（新建）

opencode-web 专用 helper：

- `Command() (exe string, args []string)` — 返回 `("opencode", []string{"serve", "--hostname", "127.0.0.1", "--port", "0"})`，**唯一**被 `Manager.Start()` 使用的命令源
- `BuildEnv(tagEnv map[string]string, authToken string) []string` — 合并 env：先 `os.Environ()`，再覆盖 `tagEnv`，最后强制覆盖 `OPENCODE_SERVER_PASSWORD=authToken`、`OPENCODE_CLIENT=myworktree`。返回 `[]string` 给 `cmd.Env`
- `extractListeningAddress(line string) (host string, port int, ok bool)` — 正则 `opencode server listening on http://([^:\s]+):(\d+)`，允许行尾空白 / ANSI（剥 ANSI 后再匹配）
- `IsAPIPath(path string) bool` — 判定 `/api/...`、`/doc`、`/global/...`、`/session/...` 等，proxy 用于决定是否注入 `?directory=`

#### `internal/tag/tag.go`

`internal/tag/tag.go:28-32` 的 default tags 列表追加：

```go
{
    ID:      "opencode-web",
    Command: "opencode serve --hostname 127.0.0.1 --port 0", // 占位展示用，Manager 不读
    Env:     map[string]string{},                              // 用户可在此设非安全 env
}
```

文档字符串：「managed opencode web UI instance. Command, hostname and port are fixed by myworktree for security; cannot be customized via `tags.json`. Use a separate PTY-backed tag if you need custom opencode launch flags (e.g., LAN exposure). myworktree always overrides `OPENCODE_SERVER_PASSWORD` and `OPENCODE_CLIENT`; other env vars in `tag.Env` are merged.」

**注**：此 tag 的 `Command` 字段是**纯展示占位**——`Manager.Start()` 在 `opencode-web` 分支**不读** `tag.Command`，直接用 `opencode.Command()` 返回的硬编码值。即便用户在 `tags.json` 把这条 command 改成 `rm -rf /`，也不会被使用。`tag.Env` **会被读**，但仅作为非安全 env 的来源；`OPENCODE_SERVER_PASSWORD` / `OPENCODE_CLIENT` 永远由 myworktree 强制覆盖（详见安全姿态 §0.5）。

#### `internal/app/app.go`

`internal/app/app.go:537-546` 的 instance 路由块：

- `POST /api/instances` 的 request payload 接受新字段 `kind`（缺省 `"pty"`），写入 `ManagedInstance.Kind`
- `GET /api/instances` 返回的每条实例带 `kind` + `extra` 字段
- 新增 `GET /api/instances/<id>/opencode`：
  ```json
  {
    "iframe_src": "/__opencode/<id>/<base64(worktree)>/session/",
    "api_base":   "/__opencode/<id>",
    "worktree_path": "/abs/path/to/worktree",
    "host": "127.0.0.1",
    "port": 51234
  }
  ```
  **不返回 password 本身**（前端不需要，Go 侧代理注入）。`iframe_src` 中的 worktree 路径经 `base64.RawURLEncoding` 编码（与 opencode 的前端 `base64Encode` 一致），拼接 `/session/` 使 iframe 直接打开聊天 session 页面（绕过首页 `HomeRoute`）。

#### `internal/ui/proxy.go`（新建）

opencode reverse proxy：

```go
package ui

import (
    "context"
    "encoding/base64"
    "net/http"
    "net/http/httputil"
    "net/url"
    "path"
    "strings"
    "myworktree/internal/instance"
)

func OpencodeProxy(m *instance.Manager) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 1. /__opencode/<id>/<rest...>  提取 id 和 rest
        // 2. m.Get(id) → inst；inst.Kind 必须 == "opencode-web"；否则 404
        // 3. inst.Extra 必须有 host + port + password + worktree_abs；否则 503（还没 ready）
        // 4. 构造 target：scheme=http, host=<inst.Extra["host"]>:<inst.Extra["port"]>, path=<rest>
        //    （当前 host 固定为 127.0.0.1，但 host 字段保留在 inst.Extra 中以备将来扩展；proxy Director 不硬编码）
        // 5. director(target):
        //      - target.Req = r（带 X-Forwarded-* 由 ReverseProxy 自己处理）
        //      - target.Req.Header.Set("Authorization", "Basic "+base64("opencode:"+pw))
        //      - if r.Method in {GET,HEAD} && isAPIPath(target.URL.Path) && !target.URL.Query().Has("directory"):
        //          target.URL.Query().Set("directory", worktreeAbs)
        // 6. proxy := httputil.NewSingleHostReverseProxy(target.URL)
        // 7. proxy.Director = 上面的自定义 director
        // 8. proxy.FlushInterval = -1  // SSE 立即 flush
        // 9. proxy.ErrorHandler = func(rw, req, err) { 502 + JSON body }
        // 10. proxy.ServeHTTP(w, r)
    })
}
```

#### `internal/ui/ui.go`

`internal/ui/ui.go:57-58` 现有静态资源路由块中，在那两条路由前面追加：

```go
mux.Handle("/__opencode/", OpencodeProxy(manager))
```

注意：该路由挂在主 mux 下 → 自动受现有 auth 中间件保护（loopback bypass / Token Cookie）

### 前端（静态 HTML/JS）

#### `internal/ui/static/index.html`

- `renderTabs()`（约 `internal/ui/static/index.html:2146`）—— tab label 渲染时，如果 `inst.kind === "opencode-web"`，在文字前面拼 `<span class="badge-oc" title="opencode web UI">OC</span>`
- 主面板渲染处（xterm.js panel 的同一父容器下）—— 新增 `<div id="opencode-panel" hidden>`：
  ```html
  <div id="opencode-panel" hidden>
    <iframe id="opencode-iframe" sandbox="allow-scripts allow-same-origin allow-forms allow-popups"></iframe>
  </div>
  ```
- `selectInstance(id)`（约 `internal/ui/static/index.html:2406`）—— 切换后判断 active 实例 kind：
  - `"pty"`：保持现有行为（连接 WS 到 `/api/instances/tty/ws?id=...`，显示 xterm panel）
  - `"opencode-web"`：
    1. `fetch('/api/instances/<id>/opencode')` 拿 `iframe_src`
    2. 把 `iframe.src` 设为 `iframe_src`
    3. 隐藏 xterm container（`display:none`），显示 `#opencode-panel`
    4. 若有已开 WS，关闭（`ws.close()`），detach xterm 但不 dispose（切回 PTY 实例时复用）

#### `internal/ui/static/preview.html`

侧栏 instance 列表渲染处：同样在 `inst.kind === "opencode-web"` 时显示 `OC` 徽章。

#### 样式

`#opencode-panel { width:100%; height:100%; }` + `#opencode-iframe { width:100%; height:100%; border:0; }`，与现有 `#terminal` panel 的 CSS 并存。

---

## 复用的现有组件（不要重新实现）

| 组件 | 路径 | 复用方式 |
| --- | --- | --- |
| Tag 解析与持久化 | `internal/tag/tag.go:11-17` + `internal/instance/manager.go:252` | 新 tag 走完全相同的路径 |
| Instance store + TabOrder | `internal/store/state.go:17` + `:30-45` | `TabOrder map[string][]string` 已天然支持同 worktree 多 instance 排序 |
| Ring buffer | `internal/instance/logbuf.go` | opencode 的 stdout/stderr 直接接到同一 buffer，复用 `/api/instances/log` 与 `/log/stream` |
| 进程退出检测 | `internal/instance/manager.go:398 m.wait()` | opencode-web 进程同样由 `cmd.Wait()` 触发状态置 `exited` |
| Stop / 删除 / 重启 / 重命名 / reorder API | `internal/app/app.go:537-546` | 现有端点不变，新 instance 通过 Kind 差异化处理 |
| 前端 tab 列表 / drag-and-drop | `internal/ui/static/index.html:2196-2322` | 扩展渲染逻辑，不重写 tab 系统 |
| myworktree 全局 token 认证 | `internal/config/global.go` + `internal/app/app.go` auth middleware | reverse proxy 路由受现有 Token/Cookie 保护 |

---

## 实施步骤（按依赖顺序）

1. **状态字段扩展**（`internal/store/state.go`）：加 `Kind` + `Extra`。`go test ./internal/store/...` 跑过。
2. **Helper 包**（`internal/instance/opencode.go` + `opencode_test.go`）：`BuildEnv`、`extractListeningPort`、`IsAPIPath`。单测覆盖。（`GeneratePassword` 已移除——凭证统一为 `cfg.AuthToken`，由 `Manager` 直接注入，详见 ARCH §8。）
3. **Default tag**（`internal/tag/tag.go`）：加 `opencode-web`。
4. **Manager 分支**（`internal/instance/manager.go`）：`Start()` 里按 Kind 分叉；`StartInput` 加 `Kind` 字段。
5. **API 端点**（`internal/app/app.go`）：`POST /api/instances` 加 `kind`；`GET /api/instances/<id>/opencode` 新增。
6. **Reverse proxy**（`internal/ui/proxy.go` + `proxy_test.go`）：Director 逻辑、auth 注入、`?directory=` 注入、SSE flush。单测用 `httptest` mock opencode server。
7. **Mount proxy 路由**（`internal/ui/ui.go`）：挂到主 mux。
8. **前端 tab 渲染**（`internal/ui/static/index.html`）：kind badge、iframe 容器、`selectInstance()` 分支。
9. **文档更新**：`docs/PRD.md` §7、`docs/API.md`、`docs/ARCHITECTURE.md`、`docs/CHANGELOG.md`。
10. **集成测试 + 手测**（见下）。

---

## Verification

### 单元测试

#### `internal/instance/opencode_test.go`

- `extractListeningAddress` 多个 input：
  - 正常行 `opencode server listening on http://127.0.0.1:4096` → host=`127.0.0.1`, port=4096
  - 行尾带 `\r\n` / 空格
  - 含 ANSI 颜色码（如 `web.ts:78` 的 `UI.println` 输出，剥 ANSI 后再匹配）
  - 错误输入（空行、纯文本）→ `ok=false`
- `BuildEnv`：
  - 输入 `tagEnv = {"FOO":"bar", "OPENCODE_SERVER_PASSWORD":"weak"}` + `password = "abc123..."` → 输出 env 含 `FOO=bar`、**`OPENCODE_SERVER_PASSWORD=abc123...`**（被覆盖）、`OPENCODE_CLIENT=myworktree`
  - tagEnv 为 nil 也不崩
  - 已存在的 `os.Environ()` 项不被丢失（除非被 tag/password 显式覆盖）
- `Command()` 始终返回固定元组 `("opencode", ["serve", "--hostname", "127.0.0.1", "--port", "0"])`（无参数）
- `IsAPIPath`：
  - `/api/foo`、`/doc`、`/global/event` → true
  - `/`、`/index.html`、`/assets/x.js` → false

#### `internal/store/state_test.go`

- 旧 `state.json`（无 `Kind`/`Extra`）→ unmarshal 成功，`Kind == ""`，`Extra == nil`
- 新 `state.json` 含 `Kind: "opencode-web"` + `Extra: {"port":"4096"}` → round-trip 一致
- Manager 入口处归一化 `Kind == ""` → `"pty"`（与现有逻辑兼容）

#### `internal/ui/proxy_test.go`

- Director 移除 `/__opencode/<id>` 前缀，路径正确
- 注入 Authorization header（值 = `Basic base64("opencode:"+pw)`）
- target host 用 `inst.Extra["host"]` + `inst.Extra["port"]`（host 来自 stdout 解析；当前固定 `127.0.0.1` 但代码不硬编码）
- GET `/api/foo` → target URL 含 `?directory=<wt>`；POST `/api/foo` → 不含
- GET `/index.html` → 不注入 `?directory=`
- SSE：mock upstream 每 100ms 写一行 → client 在 1s 内收到 ≥ 8 行（验证 `FlushInterval = -1`）
- inst.Kind != "opencode-web" → 404
- inst.Extra 缺 host/port → 503（instance 还没 ready）

#### `internal/instance/manager_test.go`（新增 case）

- 用户在 `tags.json` 把 `opencode-web` tag 的 `Command` 改成 `rm -rf /tmp/xxx` → `Manager.Start()` 仍跑 `opencode.Command()` 返回的硬编码命令（验证 Manager 不读 `tag.Command`）
- 用户在 `tag.Env` 设 `OPENCODE_SERVER_PASSWORD=weak` → 实际进程 env 中是 myworktree 生成的值（验证强制覆盖）

### 集成测试

#### `internal/instance/manager_integration_test.go`

新增 `_TestStartOpencodeWeb`：

1. mock 脚本 `opencode_serve_mock.sh`（写一个返回 `listening on http://127.0.0.1:<port>` 的小 HTTP server）：
   ```sh
   #!/bin/sh
   PORT_FILE=$1
   sleep 0.5
   PORT=$(( (RANDOM % 10000) + 30000 ))
   echo $PORT > $PORT_FILE
   echo "opencode server listening on http://127.0.0.1:$PORT"
   while true; do sleep 1; done
   ```
2. 通过 `Manager.Start({Kind: "opencode-web", Tag: {Command: "<mock path> <port-file>"}})` 启动（**故意用危险 command 验证 Manager 不读 tag.Command**——如果读了，会执行 mock 脚本逻辑但忽略 `--hostname/--port`；如果硬编码会尝试执行 `opencode` 这个不存在的 binary，所以需要把 mock 二进制符号链接到 PATH 的 `opencode` 上；或者直接接受测试只覆盖 `Manager.Start` 解析阶段）
3. 5s 内 `inst.Extra["host"]` 和 `inst.Extra["port"]` 被填充
4. 用 `net/http` GET `http://127.0.0.1:<port>/global/health`（mock 也需要响应 200；如果做不到，至少验证端口在监听）
5. 调用 `Stop(id)` → 进程退出，inst 状态 `exited`，端口释放

### 手测 checklist

```bash
# 0. 构建
go build -o myworktree ./cmd/myworktree

# 1. 启动
mkdir -p /tmp/opencode-test && cd /tmp/opencode-test && git init
./myworktree -listen 127.0.0.1:50099 -open=false
# 浏览器打开 http://localhost:50099/
```

| # | 操作 | 期望 |
| --- | --- | --- |
| 1 | 创建 worktree `feat-a` | 侧栏出现 |
| 2 | 在 `feat-a` 下 add instance，选 tag `opencode-web` | ~5s 后侧栏出现新 tab，label 带 `OC` 徽章 |
| 3 | 点击该 tab | 主面板从 xterm 切到 iframe；iframe 内显示 opencode 官方 UI（带 session 列表） |
| 4 | 在 iframe 内点击「New session」、输入 prompt | opencode 正常工作（编辑文件、跑命令） |
| 5 | 同一 worktree 再加一个 `opencode-web` instance | 侧栏出现第二个 `OC` tab |
| 6 | 切回第一个 tab | iframe 内容是第一个 opencode 进程的 session 列表（独立历史） |
| 7 | 切到第二个 tab | iframe 内容是第二个 opencode 进程的 session 列表（与第一个完全不同） |
| 8 | 在 worktree `feat-b` 下加一个 `opencode-web` instance | 三个 `OC` tab 同时存在 |
| 9 | 任意 tab 上点 Stop | 该 tab 对应 opencode 进程被 kill，端口释放（`lsof -nP -iTCP \| grep opencode`），tab 状态变 `stopped` |
| 10 | kill myworktree 进程，再重启 | 所有 `OC` tab 状态变 `stopped`（`ReconcileRunningOnStartup` 现有逻辑） |
| 11 | 添加一个普通 PTY instance（选 `docs` tag） | 仍是 xterm.js 渲染，**未回归** |
| 12 | 在 worktree 目录的 `.opencode/config.json` 里配 `{"plugin":["oh-my-opencode"]}`，重启 instance | opencode UI 内的 agent 下拉可能卡（已知问题，详见 `FEASIBILITY.md` §2.2）；myworktree 侧不崩，iframe 仍能打开 |
| 13 | 远程 portal 访问 `http://<lan-ip>:50099/`，通过 portal 进入 instance tab | iframe 仍工作（proxy 走同源；Basic auth 由 Go 注入，不暴露给前端） |

### 验收标准

- [ ] 同一 worktree 可同时启 ≥ 2 个 `opencode-web` instance，互不干扰
- [ ] 切换 tab 时 iframe 正确切换到对应 opencode 进程的 session 视图
- [ ] PTY 模式 instance 完全无回归
- [ ] 旧 `state.json`（无 `Kind`/`Extra`）加载成功
- [ ] 用户在 `tags.json` 改 `opencode-web` 的 `Command` **无效**——Manager 始终跑硬编码的 `opencode serve --hostname 127.0.0.1 --port 0`
- [ ] 用户在 `tag.Env` 设 `OPENCODE_SERVER_PASSWORD=weak` **无效**——实际进程 env 强制覆盖为 myworktree 生成值
- [ ] 用户在 `tag.Env` 设 `OPENCODE_EXPERIMENTAL=xxx` **生效**——非安全 env 经 `BuildEnv` 合并进进程 env
- [ ] `go.mod` 不引入新第三方依赖（`ParseCommand` 已删除，无需 `shlex`）
- [ ] `gofmt -l .` 无输出
- [ ] `go test ./...` 全过
- [ ] `docs/PRD.md` §7、`docs/API.md`、`docs/ARCHITECTURE.md`、`docs/CHANGELOG.md` 同步更新