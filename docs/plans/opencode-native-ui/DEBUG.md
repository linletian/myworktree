# opencode-native-ui — 调试报告

> 记录了从第一版实现到 iframe 成功渲染 opencode 官方 web UI 的完整排查过程。

## 时间线总览

| 序号 | 问题 | 根因 | 修复 |
|------|------|------|------|
| 1 | iframe 加载 opencode **首页**而非聊天 session 页面 | `iframe_src` 只有 `/__opencode/<id>/`，缺 `<base64(worktree)>/session` | app.go: `iframe_src` 补全路径，`base64.RawURLEncoding` 编码 |
| 2 | 所有 API 请求 404 | SPA SDK 以 `location.origin`（myworktree 端口）为 baseUrl，请求直击 myworktree root，未走 proxy | 注入三层 shim：CSP hash 允许内联脚本、localStorage 改写 server URL、fetch/EventSource/XHR URL 重写 |
| 3 | CSP 拦截内联脚本 | opencode 返回的 `Content-Security-Policy: script-src 'self'` 不允许内联 `<script>` | `sha256.Sum256()` 计算 shim 的 hash，注入 CSP header |
| 4 | SSE 事件流失败 | `new Request(rewrittenURL, original)` 丢失 `timeout` 自定义属性，长连接超时 | `q.timeout = i.timeout` 手动复制 |
| 5 | `127.0.0.1:1689` 跨端口连接失败 | localStorage 残留旧 opencode server 记录 | shim 清洗 `server.v3` 列表（后因副作用撤销） |
| 6 | 502 Bad Gateway | proxy 从 stdout 解析到 opencode 非 HTTP API 端口（51177），回退到固定 4096 | 定位到 opencode serve HTTP API 始终在 4096，proxy 连接正确端口 |
| 7 | **iframe 完全空白** | SPA Router 看到 `/__opencode/<id>/<base64>/session/` 4 段路径，无法匹配 `/:dir/session/:id?` | `history.replaceState()` 剥离 proxy 前缀 |
| 8 | `server.v3` 清洗导致空白页 | 清洗操作丢失 `projects`/`lastProject` 字段，破坏 SolidJS persisted store | 撤销清洗，只保留 `defaultServerUrl` 设置 |
| 9 | `ReadableStream uploading is not supported` | shim 的 `new Request(n, i)` 复制 body 为 ReadableStream 的 Request 时 Safari 不支持 | 待修复 |
| 10 | 左侧未显示正确的 worktree 项目 | `?directory=` 注入条件可能未覆盖所有 API 路径，或 `cmd.Dir`/`process.cwd()` 回退到主仓库 | 待排查 |

## 关键技术细节

### 1. iframe URL 修正

**问题**：`app.go:1407` 构造 `iframe_src = "/__opencode/" + id + "/"`，加载的是 opencode 首页 `HomeRoute`。

**修复**：
```go
base64Dir := base64.RawURLEncoding.EncodeToString([]byte(worktreeAbs))
"iframe_src": "/__opencode/" + id + "/" + base64Dir + "/session/"
```

`base64.RawURLEncoding` 产出 `-/` 替代 `+/`、无 padding，与 opencode 前端的 `base64Encode` 一致。

### 2. SPA Router 路径匹配

**这是最隐蔽的 bug**。opencode SPA 路由只有两条：
```
/               → HomeRoute
/:dir/session   → SessionRoute
```

iframe 加载 `/__opencode/<id>/<base64>/session/`，Router 看到 4 段路径无法匹配 → **整个 SPA 不渲染** → 空白页。

**修复**：shim 第一行执行：
```javascript
var a = location.pathname.slice(p.length);  // "/<base64>/session/"
if (a) history.replaceState(null, '', a);
```

效果：
```
修复前: location.pathname = /__opencode/a5030a230563/L1VzZXJzL.../session/
修复后: location.pathname = /L1VzZXJzL.../session/
Router 匹配: /:dir/session  →  params.dir = L1VzZXJzL...
```

`<base>` 标签确保后续资源加载仍走 proxy，`localStorage` + URL 重写确保 API 请求也走 proxy。

### 3. CSP hash 注入

opencode 返回 `Content-Security-Policy` 不允许内联脚本。shim 作为 `<script>...</script>` 被拦截。

**修复**：计算 shim 内容（不含 `<script>` 标签，CSP spec 要求）的 SHA-256，注入到 CSP header：
```go
h := sha256.Sum256([]byte(shimContent))
hashB64 := base64.StdEncoding.EncodeToString(h[:])
csp = strings.Replace(csp, "'wasm-unsafe-eval'",
    "'wasm-unsafe-eval' 'sha256-"+hashB64+"'", 1)
```

### 4. opencode serve 双端口

`opencode serve --port 0` 同时监听两个端口：
- `127.0.0.1:4096` — HTTP API（始终存在，默认端口）
- `127.0.0.1:<random>` — `--port 0` 分配的随机端口

proxy 从 stdout 解析到随机端口 → 502。修复后 proxy 连接 4096。

### 5. localStorage 策略

opencode entry.tsx 的服务器 URL 决策链：
```typescript
const getDefaultUrl = () => {
    const lsDefault = readDefaultServerUrl()  // ← localStorage
    if (lsDefault) return lsDefault
    return getCurrentUrl()                     // ← location.origin
}
```

shim 设 `localStorage.setItem('opencode.settings.dat:defaultServerUrl', proxyUrl)`，SPA 启动时直接采用 proxy URL。

### 6. URL 重写（防御层）

即使 localStorage 策略生效，仍有极端情况需要 URL 重写兜底：
- same-origin URL：`location.origin` 开头的请求 → 插入 proxy prefix
- loopback 跨端口 URL：`http://127.0.0.1:*` 的请求 → 重写到当前 origin 的 proxy
- `Request.timeout` 保留：`new Request()` 复制时手动保留自定义属性

## 当前 shim 完整策略（三层）

```javascript
// 第 1 层：history.replaceState 剥离 proxy 前缀（让 SPA Router 匹配路由）
var a = location.pathname.slice(p.length);
if (a) history.replaceState(null, '', a);

// 第 2 层：localStorage 改写 server URL（SPA 启动时采用 proxy URL）
localStorage.setItem('opencode.settings.dat:defaultServerUrl', pu);

// 第 3 层：fetch/EventSource/XHR URL 重写（防御深度）
```

## 待解决问题

| 问题 | 优先级 |
|------|--------|
| `ReadableStream uploading is not supported` — `new Request()` 复制 ReadableStream body 时 Safari 不兼容 | 高 |
| 左侧项目未显示正确的 worktree 路径 — `?directory=` 或 `cmd.Dir` 传递问题 | 高 |
| SSE event stream 在 Safari iframe 中的兼容性 | 中 |

## 修改文件清单

| 文件 | 改动 |
|------|------|
| `internal/app/app.go` | `iframe_src` 补全 base64 worktree + `/session/` 路径 |
| `internal/instance/opencode_web/driver.go` | `IframeURL` 同步更新 |
| `internal/instance/opencode_web/proxy.go` | 三层 shim 注入 + CSP hash 修补 |
| `docs/PRD.md` §7 | URL 描述更新 |
| `docs/ARCHITECTURE.md` §8 | 代理图更新为完整路径 |
| `docs/plans/opencode-native-ui/PLAN.md` | 实施记录同步 |
