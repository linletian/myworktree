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
| 11 | 完整页嵌入白屏 + "版本过新"警告 | opencode promise client 用 `new URL(path, baseUrl)` 构造 fetch 参数，shim 漏 URL 对象(`.href`)和根相对路径(`/api/...`) → `/api/event` 等 404 → 白屏 → L2 超时误报 | `ri` 加 URL 对象分支、`r` 加根相对路径分支 |
| 12 | 白屏修复后仍报"版本错误" | L2 锚点 `sidebar-rail`/`project-switch` 只在会话页，完整页嵌入停在首页(NewHome)时不存在 | `anchorsPresent` 加首页锚点 `home-session-search`/`home-add-project` |
| 13 | 首页"新建会话"无反应 | projects 持久化列表为空(无 localStorage)，`newSessionProject` undefined → `openNewSession` 直接 return | 预置 `projects[pu]=[{worktree,expanded:true}]` |
| 14 | 进会话报 Permission server not found | server list 被清空，defaultServer(proxy URL) 不在 servers 列表 | `sd.list=[pu]` 钉为 proxy URL |
| 15 | Markdown highlighting worker failed | Worker 脚本 URL 是根相对路径 `/assets/xxx.js`，shim 漏 Worker 构造器重写 | 拦截 Worker，`r()` 重写 |
| 16 | out-of-scope: `/home/linletian/projects/myworktree` | opencode 按 git remote 归 project，worktree 是历史项目的 sandbox，`rootFor` 归一化 | 未根治(模型冲突) |
| 17 | 旧实例 stop 但页面能加载 | mw 退出/删实例没回收 opencode serve 子进程，孤儿进程占 4096 | 未根治(实例生命周期) |
| 18 | 隐藏项目列表引入渲染卡顿 | JS `querySelectorAll` 在 MutationObserver 里频繁遍历大子树(SSE 流式渲染) | 改用 CSS 一次性注入 |

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

## 完整页嵌入 harden 排查记录（2026-08-13）

> 前 10 个问题解决后，完整页嵌入（`/__opencode/<id>/`，从首页开始）能渲染首页，但暴露出更深层的问题：白屏、"版本过新"误报、新建会话无反应、Permission server not found、Markdown worker failed、sandbox 归一化越界、孤儿进程、隐藏项目列表引入渲染卡顿。本轮逐一定位，记录根因、修复与踩坑。

### 0. 关键结论：URL 重写必须覆盖所有 fetch 参数形态

opencode 1.18.x 的 `@opencode-ai/client/promise`（`OpenCode.make`，源码 `packages/client/src/generated/client.ts`）用 `new URL(path, baseUrl)` 构造 URL 对象并 `fetch(url对象, init)`。

注入脚本的 `ri`/`r` 必须处理全部形态：
- 字符串：绝对 URL（`http://origin/...`）、根相对路径（`/api/...`，`charAt(0)==='/'`）
- `Request`：有 `.url`
- **`URL` 对象：有 `.href`，没有 `.url`**（漏掉就 404）
- **`Worker`：脚本 URL 是根相对路径 `/assets/xxx.js`**
- `EventSource`、`XMLHttpRequest`

漏任何一类，对应请求打回 myworktree origin 返回 404。本轮补上的是 **URL 对象 + 根相对路径**（问题 11）和 **Worker**（问题 15）。这是"白屏"的真正根因，不是"隐藏没生效"。

### 1. 问题 11/12：白屏 + "版本过新"是两层叠加

- **白屏**：`OpenCode.make` 的 `fetch(new URL(...), init)` 参数是 `URL` 对象，`ri` 的 `typeof i==='string'` 和 `i.url` 两个分支都匹配不到，原样返回 → URL 没加 proxy 前缀 → 打到 myworktree 的 `/api/event`、`/api/health`、`/api/provider`、`/api/model` 等 → 404 → SPA 拿不到数据 → 白屏。
- **"版本过新或结构变化"警告**：白屏导致 DOM 里没有 L2 锚点，10s 超时误报 `structure-changed`。这条警告是 **L2 DOM 锚点超时的误报，不是真版本问题**——版本 1.18.18 在范围内、CSP 锚点 `'wasm-unsafe-eval'` 也在。
- 修复后又暴露出第二层：L2 锚点 `sidebar-rail`/`project-switch` 只在**会话页**存在，完整页嵌入停在**首页**（NewHome）时根本没有这两个元素，仍会超时。于是 `anchorsPresent` 补上首页必现锚点 `home-session-search`/`home-add-project`。

教训：**别被警告文本误导去查版本**，先确认 shim 的 URL 重写覆盖了所有参数形态，以及 L2 锚点是否在当前页面必现。

### 2. 问题 13/14：新建会话链路（projects 预置 + server list 钉死）

完整页嵌入（全新浏览器 context，无 localStorage）下，opencode 前端的 projects 持久化列表为空：

- `newSessionProject()` 从 `projects.list()`（= `store.projects[scope]`）取第一个项目，空 → undefined → `openNewSession` 里 `if (!project) return` 直接**静默无反应**。修复：注入脚本预置 `projects[pu]=[{worktree:wtp,expanded:true}]`（`pu` = proxy URL，即 defaultServer 的 scope key）。
- 进会话后报 `Permission server not found: <proxy URL>`：注入脚本把 server list 清空（`sd.list=[]`），`servers` 列表只剩 entry.tsx 塞进来的 `location.origin`，而 `defaultServer` 是 proxy URL，permission 按 proxy URL 去 servers 列表找不到。修复：`sd.list=[pu]`，把 server 列表钉为 proxy URL（与 defaultServer 一致）。

### 3. 问题 15：Worker URL

markdown 高亮跑在 Web Worker 里，脚本 URL 是根相对路径 `/assets/markdown.worker-*.js`，`new Worker(ode, {type:"module"})` 时 shim 只拦了 fetch/EventSource/XHR，漏了 Worker 构造器 → 请求打到 myworktree 的 `/assets/...` 404 → 报 `Markdown highlighting worker failed`。修复：`var OW=Worker;window.Worker=function(u,opts){return new OW(r(u),opts)}`。

### 4. 问题 16：sandbox 归一化越界（未根治）

新建会话后 directory 变成 `/home/linletian/projects/myworktree`（不是 worktree），触发 out-of-scope 报警。

根因是 **opencode 的 project 模型**：project id 由 git remote URL 的 hash 决定（`remote(repo)` → `Hash.fast('git-remote:<host>/<path>')`），所以**同一个 git remote 的所有目录/worktree 被并成一个 project**，最早被记录的目录是 worktree，其余全是 sandbox。`packages/app/src/context/layout.tsx` 的 `rootFor` 会把 sandbox 目录归一到 project.worktree。

用户的 worktree 恰好是历史项目 `/home/linletian/projects/myworktree` 的 sandbox（该路径已不是 git 仓库），所以被归一到这个失效路径。**清理 sandbox 数据会反复回来**：`fromDirectory` 的逻辑是"worktree 字段写死、sandbox 自动追加"，只要 worktree 和主仓库 remote 相同，下次处理就会被重新追加成 sandbox。

这是 opencode 的 project/sandbox 模型和 myworktree「每个 worktree 独立」假设的**结构性冲突**。两条路：A（清数据）治标不治本；B（myworktree 理解 sandbox 关系）耦合 opencode 内部模型。均未实施。

### 5. 问题 17：孤儿进程（未根治）

mw 退出/删实例时没有回收 opencode serve 子进程，serve 变成孤儿（被 systemd 收养）还占着 4096 端口，导致"实例 stop 但页面还能加载"。根子在实例生命周期：`driver` 需要在 mw 退出时正确 terminate 子进程。

### 6. 问题 18：隐藏项目列表 + SSE 流式不实时

需求：项目/目录切换 UI 全隐藏（最多留只读状态）。

- 第一版用 JS 在 MutationObserver 里逐个 `querySelectorAll` 隐藏 aside + project-switch → **在会话流式渲染时频繁遍历大子树，引入渲染卡顿**（SSE 事件驱动的"思考中/完成/title" UI 卡住不动）。
- 优化尝试：MutationObserver 加 debounce → 没改善（且 `fr` 只保留最后一个 root，导致项目列表又出现，已回滚）。
- 最终：**改用 CSS 一次性注入**（`aside:has([data-slot="home-projects-scroll"]){display:none!important}[data-action="project-switch"]{display:none!important}`），不再遍历。项目列表隐藏已验证生效。

**SSE 流式不实时本身未根治**：会话"思考中"不消失、切 tab/切会话才显示内容、title 总结延迟。已排除的因素：proxy SSE 转发 chunked 实时（第一个 `server.connected` 事件）、`FlushInterval=-1`、http.Server 无超时、MutationObserver 渲染卡顿（debounce 没改善）、filter 遍历（CSS 隐藏没改善）。用户反馈 **macOS Safari 下正常，ubuntu Chrome 有差异**，暂时归为浏览器差异，不追究。

### 7. 踩过的坑与经验

1. **mw 的 dataDir 按 `git rev-parse --show-toplevel` 的 sha256 前 16 位分目录**。从主仓库（`~/SoftwareWorkspace/myworktree`）启动 vs 从 worktree（`~/orca/workspaces/myworktree/opencode-native-ui`）启动，dataDir/端口/项目全对不上。**重启 mw 必须在原启动位置**。
2. **"版本过新或结构变化"是 L2 DOM 锚点超时的误报**，不是真版本问题（见问题 11/12）。
3. **隐藏 DOM 优先用 CSS 一次性注入**，而非 MutationObserver + `querySelectorAll` 遍历（流式渲染时遍历大子树会卡渲染）。
4. **debounce 陷阱**：回调里若只保留最后一个 root，前面的 root 会被漏掉；要么统一 `filter(document)`，要么收集所有 root。
5. **opencode 按 git remote 归 project**，同 remote 的 worktree 是 sandbox；清数据会反复回来（见问题 16）。
6. **测试环境有系统代理**（`192.168.1.254:6268`，gsettings），chromium 默认走代理会 502/变慢；用 `--proxy-server=direct://` 绕过。排查时优先确认网络链路，别误判成代码问题。
7. **opencode serve 双端口**：4096（HTTP API）+ `--port 0` 随机端口；driver 从 stdout 解析随机端口，blob 里 port 可能是 4096 或随机端口，要理清 proxy 转发目标。

## 待解决问题

| 问题 | 优先级 |
|------|--------|
| SSE event stream 流式不实时（ubuntu Chrome 会话"思考中"不消失、title 总结延迟；macOS Safari 正常） | 高 |
| out-of-scope 越界误报（worktree 是历史项目的 sandbox，`rootFor` 归一化） | 中 |
| mw 退出未回收 opencode serve 子进程（孤儿进程占 4096） | 中 |
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
