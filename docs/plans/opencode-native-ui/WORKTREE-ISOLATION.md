# opencode-web 工作目录隔离监测方案 — 调研分析报告

> 调研对象:myworktree `feature/opencode-native-ui` 分支(当前)+ `feature/reasonix-native-ui` 分支(参考)+ opencode 1.18.16 源码(`/home/linletian/GithubRepo/opencode/`,与本地二进制版本一致,`packages/app/package.json` version = `1.18.16`)。
> 调研时间:2026-08(基于 `feature/opencode-native-ui` 当前实现与 dev 分支 `d92d1e654b` 对应 1.18.16 源码)。
> 状态:调研完成,方案已确认,**已实施**(见 §8 落地记录)。
> 结论一句话:**iframe 嵌入 opencode 完整页(非直跳 session 页——该路径此前反复失败,见 §0.4),不做数据目录隔离(会丢历史,reproduce reasonix issue #56),隐藏全部跨 worktree 切换入口(项目/目录切换 + server 切换/添加,仅保留当前 worktree 入口),并以"proxy 层目录监测 + 常驻警告框"作行为兜底、"版本门 + DOM 锚点检测"作隐藏有效性兜底,实现"防止跨 worktree 误操作、可读/切当前项目全部历史 session"。定位是防误操作而非禁止:与 PTY/xterm TUI 一致,用户主动访问其他目录不被阻止,web UI 只是"鼠标点击即切换"太容易误触发,需要即时提醒(详见 §0.3)。**

---

## 0. 背景与目标

### 0.1 问题

`opencode serve` 是**多 directory 共享**的 HTTP server(每次请求经 `?directory=` / `x-opencode-directory` header / `process.cwd()` 三级决定 instance),所有项目的 session 集中在单一 SQLite(`opencode.db`)按 directory 组织,官方**没有** `OPENCODE_DATA` 之类的数据目录环境变量(portable mode 为公开未实现 issue #4526,仅测试用 `OPENCODE_TEST_HOME`)。

叠加 opencode web UI 自带 **HomeRoute(多 workspace 管理)+ ServerProvider(多 server 列表持久化在 localStorage)**,导致 myworktree 嵌入的 opencode web UI **切换成本趋近于零**:一次鼠标点击即可切到其他 worktree 的 project / session / server,极易**误操作**。因此**所有跨 worktree 切换入口(项目/目录 + server)一律注入隐藏**,仅保留当前 worktree 入口;越界监测 + 常驻提示作兜底(§3 D4)。

### 0.2 目标(经用户确认)

1. **防止跨项目和 worktree 的误操作**:隐藏全部跨 worktree 切换入口(项目/目录切换 + server 切换/添加,§3 D4),仅保留当前 worktree 项目与 session 入口;经任何遗漏途径离开范围时即时、持久地提醒;
2. **可以读取/切换当前项目目录的所有历史 session**:包括终端 CLI/TUI 在同一个 directory 下创建的 session;
3. 尽量**不与 opencode 内容深度耦合**(不 fork 源码、不深度依赖其内部结构、升级尽量免疫);
4. 先分析,后实施(本报告为分析阶段产物)。

### 0.3 方案定位:防误操作,而非禁止

myworktree 现有的 PTY/xterm 路径下,`opencode` TUI 同样可以切换并访问其他目录——myworktree 一直允许,威胁模型是"用户对自己机器有完全控制权"(与 PLAN.md 安全姿态一致)。web UI 的问题**不是能力**,而是**切换成本**:TUI 需要键盘操作,web UI 一次鼠标点击即可切换,误触发概率显著更高。

因此本方案定位为**防误操作 + 即时提醒**,不做硬隔离、不阻止用户主动访问其他目录:

- 判定与警告用于**降低误操作概率**、**即时暴露已发生的越界**,不干预用户有意、主动的跨目录操作;
- 这也与 reasonix issue #56 后的语义一致:myworktree 不干预 agent 的产品逻辑(会话/目录归属归 opencode 自身),只在外围做观察与提醒。

### 0.4 嵌入形态:完整页(替代直跳 session 页)

当前分支 `iframe_src` 直跳 session 页(`/__opencode/<id>/<base64>/session/`,`internal/app/app.go:1410`、`internal/instance/opencode_web/driver.go:324`),该路径**自实现起反复失败,从未真正解决**(详见 `DEBUG.md`):

- SPA Router 看到 4 段路径无法匹配 `/:dir/session` → **整页空白**(DEBUG.md 问题 7);
- `replaceState` 剥前缀的副作用、localStorage `server.v3` 清洗破坏 SolidJS persisted store(问题 8)、Safari `ReadableStream`(问题 9)、左侧项目不正确(问题 10)。

早期版本 iframe src 为完整页 `/__opencode/<id>/`(问题 1 前的形态)**能正常显示**,只是含 workspace 管理不符合"单 worktree 视角"。

**本方案采用完整页嵌入**:iframe src = `/__opencode/<id>/`,从首页开始。完整页剥前缀后 `pathname=/`,Router 天然匹配 HomeRoute,**绕开"路由无法匹配 → 空白"这个 session 页直跳的死因**(实证见 §2.5)。完整页的代价是首页/侧栏暴露全部切换入口——**全部跨 worktree 切换入口(项目/目录 + server)注入隐藏,仅保留当前 worktree 项目与 session 入口**;越界监测 + 常驻提示兜底(§3 D4)。

---

## 1. 参考:feature/reasonix-native-ui 分支的经验

### 1.1 分支做法回顾

reasonix 分支在旧 `internal/instance/manager.go` 上加 `kind` 分支,新增 `internal/instance/reasonix/driver.go`(每 worktree 一个 `reasonix serve` 子进程)+ `internal/app/reasonix_proxy.go`(同源挂载 `/rx/<id>/` 反代 + HTML 注入)。

关键 commit:

| commit | 内容 |
|---|---|
| `8dd10e9` MVP | 引入 **REASONIX_HOME 隔离**:每实例独立 `home/`,`config.toml`/`.env` 从真实 `~/.reasonix` symlink,`session.jsonl` + `--resume` 固定会话 |
| `c21dda6` | **移除 REASONIX_HOME 隔离**(issue #56):隔离导致 UI 看不到项目已有历史(sidebar empty, 'branches: none');需求修订为"myworktree 不干预 agent 产品逻辑",共享真实 `~/.reasonix`,跨项目隔离靠 reasonix 自身 per-cwd |

### 1.2 核心教训(对 opencode 分支的直接警示)

- **用 HOME/数据目录隔离 = 丢历史**:reasonix 先隔离再回退,白付一轮成本。opencode 若照搬(靠 `XDG_DATA_HOME` 按实例隔离),必然重现 issue #56 的两大后果:**看不到既有 session 历史 + provider 凭据(`auth.json`)也要 symlink 处理**。
- **reasonix 能放弃隔离,是因为它自己 per-cwd 隔离**(session 按 serve cwd 组织,侧栏天然只有当前项目);**opencode 不具备这个能力**(多 directory 共享 + UI 有跨项目入口),所以 opencode 需要自己补"范围监测与提醒"(防误操作,而非禁止)。

### 1.3 可参考的工程点(注入侧)

- `rewriteRootAttrs`:正则白名单 `src|href|action|poster`、幂等(跳过 `//` 与已有前缀)、不误伤 `data-src`/`xlink:href`;优于 opencode 分支的 `bytes.ReplaceAll` 全量替换。
- 注入点单一(全部插在 `</head>` 前)。
- MutationObserver 兜底(动态插入的 img/a 二次重写)——opencode 分支当前没有。
- Accept-Encoding 协商:HTML 导航强制 `identity`,其余 `gzip` 透传,`ModifyResponse` 拒绝注入压缩 HTML——opencode 分支 `fixProxyHTML` 未处理 `Content-Encoding`。
- issue #44 独立源(loopback 模式 `/rx/` 挂独立 listener,iframe 与 myworktree API 跨源;TLS/远程回退同源)——opencode 分支 `/__opencode/` 挂在主 mux 下,同源,未做此加固。
- issue #53/54/55:iframe 只隐藏不销毁(保聊天状态)、清理 xterm 残留样式、`dataset.src` 守卫按需重新导航。

---

## 2. opencode 1.18.16 关键事实(源码核实)

### 2.1 数据模型与 directory 传输

- session 按 directory 组织,存共享 db;`session.list` handler 默认**严格按 directory 过滤**(`packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts:64-75`):

```ts
const list = Effect.fn("SessionHttpApi.list")(function* (ctx) {
  const directory = ctx.query.directory ? yield* InstanceState.directory : undefined
  return yield* session.list({
    directory: ctx.query.scope === "project" ? undefined : directory,  // scope=project 才忽略 directory
    ...
  })
})
```

- v2 SDK 把 directory 写进 **`x-opencode-directory` header**(`encodeURIComponent` 编码),GET/HEAD 额外改写为 `?directory=` query(`packages/sdk/js/src/v2/client.ts:25-66`);`packages/sdk/js/src/client.ts`(v1)同机制。
- **含义**:只要不隔离数据目录,`?directory=<worktree>` 指向的 instance 读到的是该 directory 下**全部**历史 session(含终端 CLI/TUI 建的)→ **"读/切当前项目全部历史"天然成立,无需数据隔离**。

### 2.2 布局验证(1.18.16 = 新布局)

| 事实 | 位置 |
|---|---|
| `newLayoutDesignsDefault = true` | `context/settings.tsx:61` |
| 升级强制切换 cutoff = `"1.17.19"` | `context/settings.tsx:64` |
| 旧布局 sunset = 2026-09-14,之后无切换入口 | `context/settings.tsx:63` |
| 非 prod channel legacy 默认亦为 true | `context/settings.tsx:60` |

**结论:1.18.16 默认新布局,只需处理新布局。** 旧布局(<1.17.19 升级用户)声明 out of scope。

### 2.3 新布局路由与 directory 可观测性

路由表(`app.tsx:216-239`,新布局):

```
/                                  → NewHome
/:dir/session/:id                  → NewLayoutLegacySessionRedirect → 重定向 /server/<key>/session/:id
/server/:serverKey/session/:id     → TargetSessionRoute(最终会话 URL)
/new-session                       → DraftRoute
```

- **最终会话 URL 不含 directory**(`/server/<base64(serverKey)>/session/<id>`,serverKey = server URL);项目切换 `navigateToProject` 短暂跳 `/:base64(dir)/session`(`pages/layout.tsx:1185`)后即被重定向。
- **directory 的可观测点在请求层,不在 URL**:SDK header/query 带 directory;session 列表请求明确带 directory(`context/server-sync.tsx:423` `loadRootSessions({ api, directory })`)。
- 实时通道:**SSE 仅**(`text/event-stream`,无 WebSocket);全局接口(如 `provider.list`)也带 directory(即 UI 上下文目录)。

### 2.4 UI 入口清单(完整页下:全部隐藏,仅保留当前 worktree 入口)

完整页下**所有跨 worktree 切换入口全部隐藏**(项目/目录切换 + server 切换/添加),仅保留当前 worktree 的项目与 session 入口;越界监测 + 常驻提示作兜底(隐藏覆盖不到的路径——历史 session 中的范围外条目、URL、注入遗漏——一旦越界即提醒)。

| # | 入口 | 位置 | 处理 |
|---|---|---|---|
| 1 | Home 页 `/`(首页,项目列表 + server 管理) | `pages/home.tsx` | 渲染保留;项目列表**只显示当前 worktree 项**;server 管理部分隐藏 |
| 2 | 侧栏项目项(点击切换项目) | `pages/layout/sidebar-project.tsx`(`data-action="project-switch"`) | **只保留当前 worktree 项,其余隐藏/禁用** |
| 3 | 侧栏"打开项目"入口 | `sidebar-shell.tsx`(openProjectLabel) | **隐藏/禁用** |
| 4 | 命令面板:`project.open`(mod+o)、`project.previous` 等 | `pages/layout.tsx:908-913` | **隐藏/禁用** |
| 5 | server 管理对话框(添加/切换/删除 server) | `components/dialog-select-server.tsx`、`settings-v2/servers.tsx` | **隐藏/禁用** |
| 6 | new-session 页 `PromptProjectSelector`(项目下拉)+ `PromptWorkspaceSelector` | `pages/new-session/new-session-view.tsx:50-60` | 项目下拉**只显示当前 worktree 项**;workspace(sandbox)选择器隐藏/禁用 |
| 7 | ConnectionError 界面"其他 server"列表(故障页) | `app.tsx:97-130` | **隐藏/禁用** |
| 8 | (不存在)回首页导航 | —— | 完整页下首页即起点,无此入口 |

**实现手段**:注入脚本在 DOMContentLoaded 后按 DOM 的 `data-project`/`slug`(= `base64(worktree)`,`sidebar-project.tsx`/`sidebar-workspace.tsx`)与实例 worktree 对比,非当前项隐藏/禁用;`MutationObserver` 兜底动态插入节点;当前 worktree 路径由 proxy 注入时传入脚本。**结构性难点**:①localStorage `server.v3.list` 残留会被 `resolveServerList`(`context/server.tsx:148-186`)全部合进 `allServers()`,喂给入口 5/7 与入口 6 的分组判定——实施时需配合"注入重置 localStorage 为单 server"(见 §5 第 7 条);②`data-project` 对比需与判定同一套路径归一化(见 §4.2)。

### 2.5 1.18.16 真机实证(2026-08,隔离数据目录起 `opencode serve`)

在 `XDG_DATA_HOME=/tmp/oc-test-data` 下起 1.18.16 `opencode serve`,curl 验证:

| 验证项 | 结果 | 结论 |
|---|---|---|
| `GET /`(完整页 HTML) | 200,SPA 入口 | 完整页可渲染 |
| HTML 资源路径 | 全部绝对路径(`/assets/index-*.js`、`/favicon*`、`/site.webmanifest`) | 当前分支 `fixProxyHTML` 的 `src="/`/`href="/` 替换恰好覆盖 |
| CSP header | 含 `'wasm-unsafe-eval'` | 与当前分支 CSP hash 注入代码精确匹配(`strings.Replace(csp, "'wasm-unsafe-eval'", …)`) |
| `X-Frame-Options` | 无 | iframe 嵌入无阻碍 |
| `GET /session?directory=X` | 200 | directory 按 query 过滤,监测可判定 |
| `POST /session` + `x-opencode-directory` header | 200 | **header 对 POST 有效 → proxy 层监测判定源成立(全方法覆盖)** |
| `/global/health`、`/project?directory=` | 200 | 首页 health 检查与项目列表正常 |
| `GET /assets/*.js` | 200 text/javascript | 静态资源可代理 |
| v2 SDK 请求 URL | `baseUrl + path` 拼绝对 URL(`packages/sdk/js/src/v2/gen/core/utils.gen.ts:83-108`),baseUrl = shim 设的当前实例 proxy URL | 请求含 proxy 前缀 → shim 放行 → 必过 proxy |
| `XDG_DATA_HOME` 重定向 | 生效 | 数据目录 env 重定向可行(副作用:影响其他 XDG 应用;本方案不用它) |

**实证结论**:完整页渲染、shim 放行链路、proxy 监测判定源、静态资源代理全部成立;监测是"隐藏入口"之外的独立兜底层(§3 D4:隐藏全部跨 worktree 切换入口)。

---

## 3. 方案确认(用户决策记录)

| # | 决策 | 内容 |
|---|---|---|
| D1 | 判定粒度 | **以当前 worktree 根路径为准,严格相等**;子目录(sandbox/workspace)算越界;其他 worktree/任意其他目录算越界 |
| D2 | 监测 | 按建议走 **proxy 层监测,覆盖尽量完整**(所有请求必经 myworktree proxy) |
| D3 | 干预程度 | **只警告不干预**;警告持久且醒目,**但不能遮盖/影响 web UI 操作** |
| D4 | 嵌入形态与入口 | iframe 嵌入**完整页**(`/__opencode/<id>/`,§0.4);**隐藏全部跨 worktree 切换入口**:项目/目录切换(§2.4 入口 1-4/6)与 server 切换/添加(入口 5/7),仅保留当前 worktree 项目与 session 入口;越界监测 + 常驻提示兜底 |
| D5 | 版本与隐藏有效性 | **版本门 + DOM 锚点检测 + 隐藏有效性验证**(§4.7):opencode 版本超范围、或注入找不到待隐藏元素、或隐藏未生效 → **持久警告**"opencode web UI 版本过新或结构变化,切换入口隐藏未生效" |

> ⚠️ D1 的必然推论:新布局 workspace 切换(directory 变为 worktree 下子目录)会报警——即使用户是有意切换,警告也会出现(防误操作设计下的可接受噪音;只提醒不阻断)。若后续希望"worktree 内子工作区不报",需改判定为"子目录也算 in-scope",其余设计不变。

---

## 4. 方案设计

### 4.1 总体架构

```
opencode UI (iframe,完整页 /__opencode/<id>/)
   │  注入:URL 重写 + CSP hash + defaultServerUrl + 隐藏全部跨 worktree 切换入口(仅留当前 worktree)
   │  每个请求带 x-opencode-directory=<dir> (encodeURIComponent)
   ▼
myworktree proxy ── ① 解析 directory(header 优先 / query 兜底)
   │                ② 归一化(realpath + 去尾部斜杠 + decodeURIComponent)
   │                ③ 与实例 worktree 比较 → in-scope / out-of-scope(dir)
   │                ④ 越界请求:记录事件(不干预,照常转发)
   ├─► 转发 upstream
   ▼
myworktree 前端 ── ⑤ 轮询/SSE 收越界状态 → iframe 外常驻警告条
                  (警告条是 myworktree 自己的 DOM,不进入 opencode DOM)
                  ╰── ⑥ iframe 内脚本 postMessage 上报隐藏有效性(hidden-report)
                      → 警告条第三状态:版本过新/结构变化/隐藏未生效(§4.7)
```

### 4.2 判定规则

| 请求 directory(归一后) | 判定 |
|---|---|
| `== worktree` | in-scope |
| worktree 的**子目录**(含 sandbox/workspace) | **out-of-scope** |
| 其他任何路径(其他 worktree、任意目录) | **out-of-scope** |
| 无 directory(header/query 均无;静态资源、部分全局接口) | in-scope(fallback = server cwd = worktree),打日志观察 |
| `scope=project` 查询(唯一让 server 忽略 directory 的形态) | **out-of-scope** + 标记"跨项目查询"(防御盲区;已确认 UI 正常路径未使用) |

归一化注意:两侧(实例记录的 worktree 路径 vs SDK 传入的 directory)统一 `realpath + 去尾部斜杠`,否则 symlink/`..`/大小写差异会误报——这是最大误报源。

### 4.3 监测覆盖矩阵(proxy 层,唯一入口)

| 通道 | 判定方式 | 覆盖性 |
|---|---|---|
| 普通 HTTP GET/HEAD | query + header 双解析 | ✅ |
| POST/PUT/DELETE 等 | header | ✅ |
| SSE(`/event` 及 sync 流) | 长连接,握手请求 header 判定一次 | ✅ |
| WebSocket | UI 未使用;proxy 遇 `Upgrade` 照常转发并尝试解析(防御) | ⚠️ 无实据 |
| 静态资源 `/assets/*` | 无 directory → in-scope | ✅ 不误报 |
| 全局接口 | 带 directory(UI 上下文目录)→ 正常时 = worktree | ✅ 不误报 |

**覆盖完整性的关键论据**:切换项目/目录必然触发 session 列表加载(带新 directory)或 sync 流重建——任何"出范围"操作都会产生带越界 directory 的请求,proxy 无盲区。

### 4.4 警告状态机(满足"持久")

```
in-scope ──(收到越界请求)──▶ out-of-scope(dir=X)
out-of-scope ──(收到 directory==worktree 的请求)──▶ in-scope
out-of-scope ──(持续越界请求)──▶ 保持,更新 dir 显示
```

- 状态 = 最后观测值:无新请求时保持 → 切出去后停手,警告持续显示,切回才消失。
- 同目录连续请求防抖;状态按实例存内存,不落盘(重启清零,可接受)。

### 4.5 警告 UI(持久、醒目、不遮盖)

**核心原则:警告条在 iframe 外**(myworktree 实例面板顶部/底部固定条),不进入 opencode DOM——既保证"不遮挡 web UI 操作",又延续"不深度耦合"。

| 属性 | 设计 |
|---|---|
| 位置 | iframe 上方常驻条(占位压缩 iframe 高度,而非覆盖) |
| 高度 | ~32–36px 固定 |
| 内容 | `⚠ opencode 已离开 worktree 范围: <目录路径(截断显示)>` |
| 样式 | 红/橙警示底 + 深色文字,与 myworktree 主题一致;常驻无自动消失 |
| 交互 | 不拦截点击、不抢焦点、无动画延迟 |
| 辅助(可选) | iframe 边框 2px 红色描边(边框不遮挡内容,增强醒目度) |
| 状态获取 | 前端轮询 myworktree 侧端点(~1–2s)或复用现有 SSE;状态不变不刷新 |

### 4.6 切换入口隐藏(D4)

完整页下**全部跨 worktree 切换入口**都隐藏(§2.4):

- **项目/目录切换**:首页与侧栏项目列表**只保留当前 worktree 项**(按 DOM `data-project`/`slug` = `base64(worktree)` 与实例 worktree 对比,非当前项隐藏/禁用),"打开项目"入口与命令面板项目命令(`project.open` 等)隐藏/禁用,new-session 项目下拉只留当前项、workspace(sandbox)选择器隐藏;
- **server 切换/添加**:首页/settings 的 `DialogSelectServer` 与服务管理列表(入口 5)、ConnectionError 的"其他 server"列表(入口 7)隐藏/禁用。

实现手段:注入脚本在 DOMContentLoaded 后按 `data-project`/`slug` 过滤,`MutationObserver` 兜底动态插入节点;当前 worktree 路径由 proxy 注入时传入脚本。**越界监测 + 常驻提示是独立兜底**:隐藏覆盖不到的路径(历史 session 中的范围外条目、URL、注入遗漏)一旦越界即提醒,不依赖隐藏"完整覆盖"。

### 4.7 版本与隐藏有效性检测(D5)

注入隐藏依赖 opencode 前端 DOM 结构,升级即可能**静默失效**。三层检测(先例:reasonix 分支 `Driver.checkVersion` 版本门 ≥1.22.0 + `TestReasonixUpstreamContract` 契约探针):

| 层 | 检测方式 | 失效信号 | 警告语义 |
|---|---|---|---|
| L1 进程版本门 | 实例 Start 时 `opencode --version`,与受支持版本范围(如 1.18.x)比对 | 版本超出范围 | "opencode 版本过新/旧,切换入口隐藏未验证" |
| L2 DOM 锚点检测 | 注入脚本携带 worktree 与预期版本,页面加载后轮询(如 500ms × 10s)检测**结构必现锚点**(侧栏项目项 `data-action="project-switch"`、server 管理入口等) | 超时仍无任何锚点 → 结构变化 | "版本过新或结构变化,隐藏未生效" |
| L3 隐藏有效性验证 | 检测"本应被隐藏的非当前项"是否仍可见、当前项是否被误隐藏 | 非当前项可见 / 当前项被藏 | 同上 + 细节(隐藏失效/误隐藏) |

**上报通道**:iframe 内注入脚本 `parent.postMessage({type: "mw-oc/hidden-report", status, detail})`(同源 iframe,可靠),myworktree 前端监听后把持久警告条扩展出第三状态:

| 警告条状态 | 触发 | 展示 |
|---|---|---|
| 正常 | — | 无 |
| 越界 | proxy 监测到 `directory != worktree` 请求(§4.4) | `⚠ opencode 已离开 worktree 范围: <dir>` |
| **隐藏失效** | L1/L2/L3 任一触发 | `⚠ opencode web UI 版本过新或结构变化,切换入口隐藏未生效,请升级 myworktree 或使用受支持版本(1.18.x)` |

**避免误报**:L2 锚点选"结构必现"元素(侧栏容器/设置入口),不选依赖数据的元素(项目项为空可能是"没打开过项目"而非版本问题);轮询窗口覆盖 SPA 异步首屏;L1 与 L2 互相印证(版本在范围内但锚点缺失 → 结构被改;版本超范围 → 直接警告)。

---

## 5. 边界与待真机验证项

1. **路径归一化是最大误报源**:symlink、`..`、大小写、尾部斜杠,两侧必须对称归一化。
2. **越界兜底(隐藏之外的路径)**:隐藏入口后,越界仍可能经 ①历史 session 列表中的范围外条目(正常历史数据)、②URL 直改/注入遗漏 发生;点开即触发越界请求 → 常驻警告。语义为"任何触及范围外目录的操作",防误操作定位下只提醒不阻止。
3. **警告条压缩 iframe 高度**:首次出现有布局位移,建议固定占位或高度变化控制在一次。
4. **SSE 长连接**:判定在握手时,之后目录不会中途变(SSE 按 directory 建立),无需处理。
5. **scope=project 防御**:UI 正常路径未使用,但作为盲区防御保留标记。
6. **D1 子目录推论**:workspace 切换会报警——即使用户有意切换也会提醒(防误操作设计下的可接受噪音;只提醒不阻断)。若后续希望"worktree 内子工作区不报",需改判定为"子目录也算 in-scope"。
7. **localStorage 单 server 归一(配合 D4)**:即便隐藏入口 5/7,`server.v3.list` 残留仍会:①喂给入口 6 的分组判定(`server.list.length > 1` 时跨 server 合并项目)、②影响 server key 路由。实施时注入重置 localStorage 为"仅当前实例"(保留 `projects`/`lastProject` schema,只清 `list`),避免 DEBUG.md #8 全量清洗破坏 persisted store 的坑。
8. **版本检测的边界**:①L2 锚点缺失优先归因"加载慢",超时后才报(检测窗口需覆盖 SPA 异步首屏);②`opencode --version` 输出格式需在实施时确认并锁定(实测 1.18.16 输出 `1.18.16`,格式稳定);③L2 只报"锚点全缺",单锚点缺失不报(可能是数据空态);④受支持版本范围需随 myworktree 发布记录,升级 opencode 后由 L1 门禁提示。

---

## 6. 实现量估算(未实施)

| 模块 | 内容 | 量级 |
|---|---|---|
| proxy 监测 | directory 解析(header/query)+ 归一化 + 判定 + 越界状态记录 + `scope=project` 防御 | ~50–70 行 |
| 版本与隐藏有效性检测(D5) | L1 `opencode --version` 版本门(可复用 reasonix `versionLess` 思路)+ L2 注入脚本锚点轮询 + L3 可见性验证 + postMessage 上报 | ~60–90 行 |
| 前端警告条 | 常驻条元素 + 轮询/SSE 状态 + 进出场 | ~40–60 行 |
| 切换入口隐藏(D4) | 首页/侧栏项目列表过滤(仅当前 worktree)+ 打开项目/命令面板/server 管理/other servers 入口隐藏 | ~50–80 行 |
| localStorage 单 server 归一(可选) | 注入重置 `server.v3.list` 为仅当前实例(保留 schema) | ~15–25 行 |

---

## 7. 结论

- **数据面**:不隔离数据目录,"读/切当前项目全部历史 session"天然成立(共享 `opencode.db` + directory 过滤)。
- **嵌入形态**:iframe 嵌入**完整页**(§0.4/§2.5 实证可渲染、可代理),替代反复失败的直跳 session 页。
- **防止跨 worktree 误操作**:隐藏全部跨 worktree 切换入口(项目/目录切换 + server 切换/添加),仅保留当前 worktree 项目与 session 入口;越界监测 + iframe 外常驻警告兜底(隐藏覆盖不到的路径一旦越界即提醒)。不阻止用户主动访问其他目录,只降低误操作概率并即时提醒;均不深度耦合 opencode 内部结构,升级影响面收敛在 header 契约与路由约定(相对稳定)。
- **隐藏有效性兜底(D5)**:版本门(L1)+ DOM 锚点检测(L2)+ 可见性验证(L3),隐藏因版本过新/结构变化失效时**持久警告**,避免"静默失去保护"。
- **避免的坑**:不做 `XDG_DATA_HOME` 按实例隔离(丢历史 + 凭据要 symlink,reproduce reasonix issue #56);隐藏范围收敛为"所有跨 worktree 切换入口",不依赖 UI 内部逻辑(按 `data-project` 对比,升级只影响选择器);监测警告是独立兜底,不依赖隐藏"完整覆盖"。

---

## 8. 落地记录(已实施)

方案已按 §4 实施于 `feature/opencode-native-ui` 分支。各模块落地位置与实现要点:

| 模块 | 落地文件 | 实现要点 |
|---|---|---|
| proxy 目录监测(D1/D2) | `internal/instance/opencode_web/scope.go`、`proxy.go` | `parseDirectory`(header 优先/query 兜底,全方法)、`normalizeDir`(Clean + EvalSymlinks + 去尾斜杠)、`classify` 判定矩阵、`ScopeTracker` 内存状态;`ProxyHandler` 转发前判定并 `Record`,无 directory 请求不覆盖越界状态 |
| scope 查询端点 | `internal/app/app.go` | `/api/instances/opencode/scope?id=` 返回 `{scope,directory,cross_project,at}` |
| 完整页嵌入(§0.4) | `driver.go`、`app.go` | `IframeURL`/`iframe_src` 由 `/<base64>/session/` 改为 `/`(完整页);清理 `RegisterHTTP`/`makeProxyHandler` 死代码 |
| HTML 注入重构 | `proxy.go` | `rewriteRootAttrs`(正则白名单 `src|href|action|poster`)替换 `bytes.ReplaceAll`;Accept-Encoding 协商 + 压缩 HTML 拒绝注入 |
| 切换入口隐藏(D4) | `proxy.go` `buildInjectScript` | 按 `data-project`(base64url worktree)过滤侧栏非当前项,隐藏 `home-add-project` 与 "Open project" 入口;`MutationObserver` 兜底 |
| localStorage 单 server 归一(§5.7) | `proxy.go` `buildInjectScript` | 清空 `opencode.global.dat:server` 的 `list`(保留 projects/lastProject/recentlyClosed) |
| 版本门(D5 L1) | `version.go`、`driver.go` | `parseVersion`/`versionLess`/`isSupportedVersion` 锁定 1.18.x(≥1.18.0 且 <1.19.0);`probeVersion` goroutine 探测 `opencode --version` 写入 blob;告警不拒绝启动 |
| 隐藏有效性检测(D5 L2/L3) | `proxy.go` `buildInjectScript` | L2 轮询检测 `[data-component="sidebar-rail"]`/`[data-action="project-switch"]` 锚点(500ms×20);L3 可见性验证;`parent.postMessage({type:"mw-oc/hidden-report"})` 上报 |
| 前端警告条 | `internal/ui/static/index.html`、`kinds/opencode_web.js` | `#opencode-scope-warning` 常驻条(iframe 上方占位);轮询 scope 端点(~1.5s)+ 监听 hidden-report;三态(正常/越界/隐藏失效) |

**与文档的偏差(实施时发现)**:
1. opencode 1.18.16 源码实际位于 `packages/app/src/`(非调研时的 `packages/opencode/src/`),锚点路径以 `packages/app/src/` 为准。
2. home 页项目行 `home-project-row` **无 `data-project` 属性**(项目身份仅存在于 JS),无法按 worktree 过滤;该项目列表由越界监测兜底,未强制隐藏。
3. 命令面板 `project.open`、new-session `PromptProjectSelector`/`PromptWorkspaceSelector`、ConnectionError "other servers" 等入口因 DOM 结构复杂且动态,未逐一隐藏,由越界监测 + L2/L3 检测兜底。

**验证**:
- 单测:`scope_test.go`(归一/判定/状态)、`proxy_test.go`(rewrite/注入脚本)、`proxy_handler_test.go`(proxy 端到端:目录监测 + 注入 + 状态保持)、`version_test.go`(版本门)。
- 契约测试:`contract_test.go`(opt-in `OPENCODE_CONTRACT=1`),验证 opencode 1.18.16 `--version` 可解析、首页 CSP 含 `'wasm-unsafe-eval'` 锚点 + `<head>`。
