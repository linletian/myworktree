# opencode-web 跨 origin 陈旧工作目录状态 — 分析与修复记录

> 状态：已定稿（2026-08-14）。本文档是 `OPENCODE-WORKDIR-DEBUG-2026-08-13.md` 的后续排查记录，
> 完整收录「worktree 删除后 web UI 仍显示旧路径、且远程/本地/隐私窗口三态不一致」的分析、证据与解决方法。
> 关联文档：`OPENCODE-WORKDIR-DEBUG-2026-08-13.md`（前一轮：幽灵 project 行问题）、
> `WORKTREE-ISOLATION.md`（方案设计）、`FOLLOWUPS.md` §三（旧数据清理策略）、`CHANGELOG.md` Unreleased。
> 适用版本：opencode 1.18.18（实例实测版本）；myworktree 提交 `709fd03`（运行中 daemon）。

---

## 1. 现象

1. opencode-native-ui worktree 时期，实例在 web UI 中显示**正确**的 worktree 路径（`/home/linletian/orca/workspaces/myworktree/opencode-native-ui`）。
2. worktree 删除后，从主工作区（`/home/linletian/SoftwareWorkspace/myworktree`）重新启动实例，web UI 里**仍然显示已删除的 opencode-native-ui 路径**，而不是主工作区 myworktree 路径。
3. 同一实例、不同访问来源，表现不一致：
   - 远程 `http://100.86.87.23:41143`：显示**错误**的旧 opencode-native-ui 路径；
   - 本地 `http://127.0.0.1:41143`：显示**正确**的 myworktree 路径；
   - 远程用**隐私窗口**访问：显示**正确**的 myworktree 路径。
4. 初步怀疑与 cookie 有关（隐私窗口差异），但排查后否定（见 §3.4）。

## 2. 结论（一句话）

**不是 cookie、不是服务端、也不是代理注入——是 opencode 自己持久化的、按 origin 隔离的 localStorage 状态。**
注入脚本对实例键 `opencode.global.dat:server/__opencode/<id>` 的项目 preseed 是「仅空时写入」，一旦该键里已有旧
worktree 数据就永远不再修正；而 opencode 前端会把自己打开过的目录（`projects.open` / `projects.touch`）写回这个键。
于是「哪个 origin 的历史状态被污染，哪个 origin 就显示旧路径」：隐私窗口无历史状态 → 正确；本地 origin 未被污染 → 正确。

## 3. 根因分析（证据链）

### 3.1 服务端全部正确（逐一实测，2026-08-14）

| 检查项 | 命令/途径 | 结果 |
|---|---|---|
| 实例记录 | `GET http://127.0.0.1:41143/api/instances` | `id=482d049f1301`，`cwd`/`kind_blob.worktree_abs` = `/home/linletian/SoftwareWorkspace/myworktree`，`worktree_id=__main__` ✅ |
| 代理注入 HTML | `GET http://127.0.0.1:41143/__opencode/482d049f1301/` | `wtp="/home/linletian/SoftwareWorkspace/myworktree"`、`dn="myworktree"` ✅ |
| upstream 项目表 | `GET http://127.0.0.1:4096/project`（Basic auth） | 8 行（global/opencode-config/aragora/SLGService/SLG-SSPS/myworktree/e2e×2），**无 opencode-native-ui 行** ✅ |
| 越界监测 | `GET /api/instances/opencode/scope?id=482d049f1301` | `in-scope`，directory = 主工作区 ✅ |
| 实例身份 | `state.json`（`~/.config/myworktree/6a233b3d468c3f00/`） | 实例 2026-08-14T14:09:25Z 创建于 `__main__`；旧 worktree 的 tab_order 里无此 id → 旧实例 id 已随 worktree 删除，新实例是新 id ✅ |

结论：旧路径**不可能**来自服务端（DB 无旧行、记录正确、注入正确）。

### 3.2 opencode 1.18.18 主页显示完全由 localStorage 驱动

（源码：`/home/linletian/GithubRepo/opencode@1.18.18`，`packages/app/src/`）

1. 主页项目列表 = `createServerProjects.list()` = 持久化 store 的 `projects[scope]`
   （`context/server.tsx:79-146`；`scope` 对 canonical 本地 server 恒为 `"local"`）；
   `GET /project` 的 DB 数据只做图标/会话 enrichment（`context/layout.tsx:445-516` `enrich`），**不进列表**。
2. 启动 autoselect 用 `lastProject[scope]` 决定跳转目标：
   `list.find(p => p.worktree === last) ?? list[0]` → `openProject` → `navigateToProject`
   （`pages/layout.tsx:539-555`、`1258-1261`）。
3. 主页「当前项目」选中态 = `projects().find(p => p.worktree === selection().directory)`，
   新会话默认项目 = `selectedProject ?? find(p => p.worktree === projects.last()) ?? projects()[0]`
   （`pages/home/home-controller.ts:30-36`）。
4. 上述 `projects[scope]` / `lastProject[scope]` 持久化在 `opencode.global.dat:server` 键
   （`utils/persist.ts` `Persist.global("server")` → `localStorageWithPrefix("opencode.global.dat")`），
   由注入脚本**按实例重定向**到 `opencode.global.dat:server/__opencode/<id>`
   （`internal/instance/opencode_web/proxy.go` `buildInjectScript` 的 getItem/setItem/removeItem 劫持）。

### 3.3 污染链：opencode 前端会把旧目录写回实例键

- 打开任意会话时：`ctx.projects.open(directory)`（prepend 进 `projects.local`）+ `ctx.projects.touch(directory)`
  （写 `lastProject.local`）——`context/server.tsx:98-144`、`pages/home/home-sessions-controller.tsx:181-205`；
  项目导航同理（`pages/layout.tsx:1174-1177`）。
- 旧目录的会话在共享 DB 中**仍然存在**（实例切换/重启不清 session；实测
  `GET /session?directory=<旧目录>` 可列出，其中还有 `directory=opencode-native-ui` 的会话），
  会话搜索 / 命令面板 / 标签恢复命中这类会话即可把旧路径写回实例键。
- 注入脚本的 preseed **只 fill-if-empty**（修复前的形态）：
  - `if(!Array.isArray(sd.projects['local'])||sd.projects['local'].length===0)` —— 非空即跳过；
  - `ok = cur.http.url===location.origin`（不看 displayName）—— 同源旧条目原样保留；
  - `lastProject` 从不触碰。
  → 键一旦含旧数据，**永远不会被修正**，且跨实例 id 重建也不影响（键含实例 id，但同一个浏览器 origin 上
  实例 id 复用/重开时键名相同，旧值继续生效）。

### 3.4 为什么「远程错 / 本地对 / 隐私窗口对」——与 cookie 无关

- 上述全部状态都在 iframe 同源（`100.86.87.23:41143` vs `127.0.0.1:41143` 是**两个 origin**）的 localStorage 里，
  origin 之间互不相通。
- `mw_token` cookie 只承载认证 token，**不含任何路径数据**（`internal/authq/authq.go`、
  `internal/ui/static/framework.js:49-58`）；隐私窗口同时清空 cookie 与 localStorage，因此「像 cookie 问题」
  实为 localStorage 问题。
- 三态差异 = 三个独立的 localStorage：远程 origin 历史状态被污染 → 旧路径；本地 origin 干净 → 新路径；
  隐私窗口无历史 → 新路径。

## 4. 实证：headless Chromium 四场景复现

方法：CDP（Chrome DevTools Protocol）驱动真实 headless Chromium，加载运行中实例的真实 SPA
（`http://127.0.0.1:41143/__opencode/482d049f1301/`），预埋不同 localStorage 状态后观察渲染结果。

| 场景 | 预埋状态 | 渲染结果 |
|---|---|---|
| **A** | 实例键（当前 id）含旧 `projects.local` / `lastProject.local` / 旧 displayName + 旧 `opencode.global.dat:layout`（`home.selection.directory`=旧）+ 旧 draft tab | **复现**：主页项目行 = `opencode-native-ui` 且选中（`data-selected`），URL 自动跳到 `/L2h…opencode-native-ui/session`，命令面板标题「在 opencode-native-ui 中搜索会话」 |
| **C** | 仅旧实例键（不同 id，`deadbeef0000`）+ 旧 layout + 旧 draft tab | 正确显示 `myworktree`，URL 跳 `/…myworktree/session/…`；layout 里的旧 selection 仍残留在存储中但**不再驱动显示**（休眠向量） |
| **B** | 干净存储（对照） | 正确显示 `myworktree` |
| **F** | 同 A 的脏状态 + 修复后（self-heal）preseed | 正确显示 `myworktree`，实例键被自动重写回当前 worktree |

> 复现脚本要点（供未来复现/扩展）：本地起 headless Chromium（`--remote-debugging-port`），Node 原生
> WebSocket 连 CDP：`Target.createTarget` → 先导航到 shell 页用 `Runtime.evaluate` 预埋
> localStorage → 再导航到 `/__opencode/<id>/` → 轮询 `[data-component="home-project-row"]` 渲染 →
> 采集 `location.href` / 项目行文本与 `data-selected` / 各 `opencode.*` 键。场景 A 的预埋即
> 「旧 era 浏览器状态」的完整模拟。

## 5. 解决方法（已实施）

### 5.1 修复：preseed 从「仅空时写入」改为「自愈」

`internal/instance/opencode_web/proxy.go` `buildInjectScript` 的 preseed 块：

- `list[0]`：改为要求 `url===location.origin && displayName===dn`，不一致即重写为
  `{type:"http",http:{url:location.origin},displayName:dn}`（同时修复搜索栏 placeholder 显示旧名的问题）；
- `projects.local`：改为「非单元素 / worktree 不等于当前 worktree / expanded 不为 true」即重写为
  `[{worktree:wtp,expanded:true}]`；
- `lastProject.local`：新增——不等于当前 worktree 即重写（保证 autoselect 落到当前 worktree）；
- 全部 compare-then-write：已正确时**零写入**，不产生额外 localStorage 扰动。

效果：受污染浏览器**下次刷新即自动恢复**，无需清 localStorage / 隐私模式。实例键隔离
（多实例互不覆盖 preseed）语义不变。已知取舍：用户通过「打开项目」加入的其他目录会在下次
加载时被重置为单 worktree 视图——与「单 worktree 视角、切换入口禁用」的设计意图一致
（`WORKTREE-ISOLATION.md` §1/§3 D4）。

### 5.2 回归测试

- `internal/instance/opencode_web/proxy_test.go` `TestBuildInjectScriptSingleServer` 新增 **phase 3**：
  预埋陈旧实例键（旧 worktree / 旧 displayName / 旧 lastProject）→ 重新执行注入脚本 →
  断言三项均被修复、共享裸键仍从未被写。
- `TestBuildInjectScript` 增加 self-heal 片段断言（`displayName===dn`、`prj[0].worktree!==wtp`、
  `sd.lastProject['local']!==wtp`）。
- 顺带修正该测试 probe 的一处历史缺陷：`__SCRIPT__` 占位符以裸文本替换进
  `const __scriptA = __SCRIPT_A__;`，导致 IIFE 在声明处即执行、`eval(__scriptA)` 实为 `eval(undefined)`
  （旧测试靠副作用"碰巧"通过）；现改为 `strconv.Quote` 引用为 JS 字符串字面量后再 `eval`。

验证：`go test ./...` 全绿（20 个包），`go build ./...` 通过；`go test ./internal/instance/opencode_web/ -count=1` 通过。

### 5.3 部署与用户侧恢复

- 重新编译并重启 daemon（注入 HTML 由运行中进程生成，需新二进制生效）；
- 用户无需任何手动操作：远程浏览器**直接刷新**页面即自愈（preseed 在每次 HTML 加载时执行）。

## 6. 遗留与后续（未做，记录跟踪）

| 项 | 说明 | 状态 |
|---|---|---|
| `opencode.global.dat:layout` 的 `home.selection.directory` 旧值 | 场景 C 证实是**休眠向量**（projects 列表被修复后不再驱动显示），但残留不清理；若未来 projects 列表再被旧目录会话污染，会重新激活 | 观察；可考虑在注入脚本里做同款安全清理（仅删/改 `home.selection.directory` 一个字段，try/catch 包裹） |
| 会话打开时的再污染 | 用户在会话内主动打开旧目录会话仍会 `projects.open` 写回旧路径；下次刷新被 self-heal 修正，会话内由 scope 警告条提醒 | 符合「提醒而非禁止」设计，不修 |
| `recentlyClosed` / 旧裸键 / `projects[origin+p]` 孤儿数据 | 见 `FOLLOWUPS.md` §三（不自动清理：裸键可能含用户原生 opencode 数据） | 维持原决策 |
| Playwright 真浏览器冒烟测试 | 本次 headless 复现是一次性脚本；`FOLLOWUPS.md` §二 的 CI 化建议仍有效 | 待办 |

## 附录：本次排查的关键事实速查

| 事实 | 位置 |
|---|---|
| 主页项目列表/选中态/新会话默认项目来源 | opencode `packages/app/src/pages/home/home-controller.ts:30-36`、`context/server.tsx:79-146` |
| autoselect 启动跳转 | `packages/app/src/pages/layout.tsx:539-555` |
| 会话打开回写 `projects.open`/`touch` | `packages/app/src/pages/home/home-sessions-controller.tsx:181-205`、`pages/layout.tsx:1174-1177` |
| `opencode.global.dat:server` 键与 prefix 存储 | `packages/app/src/utils/persist.ts`（`Persist.global("server")`） |
| canonical server = `location.origin`、defaultServerUrl 读取 | `packages/app/src/entry.tsx:100-103, 128-129, 169-170` |
| myworktree 注入脚本 / 实例键劫持 / preseed | `internal/instance/opencode_web/proxy.go` `buildInjectScript` |
| myworktree 实例状态 / 代理元数据 | `GET /api/instances`、`GET /api/instances/opencode?id=`、`GET /api/instances/opencode/scope?id=`（`internal/app/app.go`） |
| 认证 cookie（仅认证，无路径） | `internal/authq/authq.go`、`internal/ui/static/framework.js:49-58` |
