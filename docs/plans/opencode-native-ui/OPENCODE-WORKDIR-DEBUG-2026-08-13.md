# opencode-web 工作目录与会话显示问题 — 现象关联分析

> 状态:分析阶段,**未改动代码**。仅基于当前 commit 的实现 + 用户观察 + opencode 1.18.16 源码阅读(`/home/linletian/GithubRepo/opencode/`,`packages/app/package.json` version = `1.18.16`)做出的推断,需在 devtools / 进程级验证后再决定动哪里。
> 报告产物:本文件 + 此前对话中的口头分析。
> 关联文档:`WORKTREE-ISOLATION.md`(方案设计,§4.6 注入脚本、`§0.1` 数据目录设计、`§2.3` serverKey 路由)、`DEBUG.md`(完整页 vs session 页直跳反复失败记录)。

---

## 0. 基础信息

- **当前 commit**:`4a72fb81c58d083cac3d563cfac3f86969f871cb`(`4a72fb8`)
- **commit 信息**:`fix(opencode-web): preserve current worktree's project-switch button`
- **commit 时间**:2026-08-13 23:04:19 +0800
- **分支**:`feature/opencode-native-ui`,领先 `origin/feature/opencode-native-ui` 2 个 commit
- **本次分析的 commit 范围**:`4a72fb8` 及其父链(回看至 `598621b`、`49b18a1`、`28ec484`、`9448517` 等涉及 review/缓冲/css/iframe 保留的提交)
- **报告日期**:2026-08-13
- **Worktree 路径**:`/home/linletian/orca/workspaces/myworktree/opencode-native-ui`(git root,`app.go:120` `gitx.GitRoot(".")`)
- **触发问题路径**(用户上报):`/home/linletian/projects/myworktree`(`$HOME/projects/myworktree`,**非 git 仓库**,存在 `.codegraph` 子目录,目录创建时间 2026-08-13 15:24)

---

## 1. 前提钉死

### 1.1 myworktree 端的真相

- `opencode serve` 启动命令(`driver.go:114`):
  ```
  opencode serve --hostname 127.0.0.1 --port 0
  ```
  `cmd.Dir = params.WorktreePath`(`driver.go:115`),所以 **进程 cwd = worktree 根**(`/home/linletian/orca/workspaces/myworktree/opencode-native-ui`)。
- `s.root` 同源:`app.go:120` `gitx.GitRoot(".")` → `gitx.gitx.go:12` `git rev-parse --show-toplevel`。
- `params.WorktreePath` 来源:`manager.go:152` `m.resolveWorktree(in)` → `manager.go:486`:
  - 优先 `in.Root`(`StartParams.Root`,由 `app.go:388` 设为 `s.root`);
  - 否则查 `st.Worktrees` 找到 `wt.Path`。
- proxy 转发时给所有 `(GET|HEAD) && isAPIPath(rest) && !req.URL.Query().Has("directory") && worktree != ""` 注入 `?directory=<worktree>`(`proxy.go:109-114`)。
- `ScopeTracker` 是 in-memory 状态机:最后一次带 directory 的请求决定当前条状态;`scope=project` 标记 `cross-project`,其他按 `classify(worktree, dir, crossProject)` 判 in/out(`scope.go:88`)。
- 启动时 `cmd.Dir` 没问题 → **"opencode 进程 cwd 始终是 worktree,这是真;问题在浏览器侧 SPA 把 worktree 重新解释成另一条路径、并以这个新路径作为 serverKey 的项目身份"**。

### 1.2 opencode 1.18.16 端的已知事实

来源:`WORKTREE-ISOLATION.md §2.1/§2.3/§2.5` + 源码阅读 `packages/app/src/`。

- **数据存储**:单 SQLite 共存(`~/.local/share/opencode/opencode.db`),按 `directory` 组织(`WORKTREE-ISOLATION.md §0.1`)。
- **directory 传输**:v2 SDK 在每个请求上送 `x-opencode-directory` header(`encodeURIComponent` 编码),GET/HEAD 额外改写为 `?directory=` query(parameter 兜底,header 优先)。`packages/sdk/js/src/v2/client.ts:25-66`。
- **新布局路由**(`app.tsx:216-239`):
  ```
  /                                  → NewHome
  /:dir/session/:id                  → NewLayoutLegacySessionRedirect → /server/<key>/session/:id
  /server/<base64(serverKey)>/session/:id → TargetSessionRoute
  /new-session                       → DraftRoute
  ```
  **serverKey = server URL**(base64 后嵌入路径)— `WORKTREE-ISOLATION.md §2.3`。
- **layout 详情**:`pages/layout.tsx:1185` 中 `navigateToProject` 短暂跳 `/:base64(dir)/session` 后即被重定向;`directory` 的可观测点在请求层(header/query),不在 URL。

---

## 2. 四个现象的统一根因

### 2.1 现象 1+2:警告条 + 新会话找仓库

**用户观察**:

- 全新启动实例,**不**出现"opencode 已离开 worktree 范围"警告。
- 点击"开始新会话"后,警告条亮起:`⚠ opencode 已离开 worktree 范围: /home/linletian/projects/myworktree`。
- 新会话第一次 bash 工具调用输出:
  ```
  当前目录不是 git 仓库。让我找到实际的仓库
  $ ls -la /home/linletian/projects/myworktree 2>/dev/null; echo "===";
    ls /home/linletian/orca/workspaces/myworktree/ 2>/dev/null; echo "===";
    ls -d /home/linletian/orca/workspaces/myworktree/*/.git 2>/dev/null
  ...
  ```

**判断**:`/home/linletian/projects/myworktree` **不是 myworktree 造的**(myworktree 只会写 `s.root` 衍生路径,不会写 `$HOME/projects/...`);它落在 `$HOME/projects/myworktree` 下,基本可以**确认是 opencode 内部把 worktree 路径"项目化"后的产物**。

证据链:

1. 注入脚本会写 `localStorage.opencode.global.dat:server.projects[pu] = [{worktree: wtp, expanded:true}]`(`proxy.go:238`)。
2. opencode 1.18.16 把这个 `worktree` 字段当作"项目目录"展示 + 用于 server-side 的 session 创建/文件操作上下文。**它把 base64url 的 `wtp` 解码后,作为 `directory` 字段走到我的请求里**。
3. 但是 — `proxy.go:226` 用 `fmt.Sprintf ... wtp=%q` 插入,`wtp` 是 **base64url 编码**之后的字符串;opencode 客户端在写入 `projects[pu]` 之前,需要的是 **解码后的 raw path**(否则 `x-opencode-directory` 头会变成 base64 串)。这就是**第一处可能错位**:opencode 解码 base64url `wtp` 后得到 `wt = /home/linletian/orca/workspaces/myworktree/opencode-native-ui`,这是真实的 worktree 路径。
4. 那 `/home/linletian/projects/myworktree` 怎么来的?**猜测:opencode 1.18.16 把 `wt` 当作"项目标识"展示时,做了一层 HOME 视图的"相对化"** — 当路径不在 `$HOME` 下时,opencode 把它显示为 `$HOME/projects/<basename>` 形式;然后这个被 HOME 视图化的字符串又被 client 端回写进 `x-opencode-directory` header。**这是 1.18 的 UI 行为,不是 myworktree 行为**。
5. 真实请求里 `x-opencode-directory` 头很可能即为 `/home/linletian/projects/myworktree`(HOME 视图化后的路径),proxy 解析到 `dir`、与 `worktree` 字符串化比较 → `normalizeDir` 不等 → `ScopeOutOfScope` → 警告条亮起。
6. 警告条触发后,新会话的 agent 已经在 `/home/linletian/projects/myworktree` 这个非 git 目录里执行 bash → 因此出现"当前目录不是 git 仓库。让我找到实际的仓库"的查找输出。

**对应代码**:
- `proxy.go:109-114` 注入 `?directory=<worktree>`(只在客户端没自带 `directory` 参数时);
- `scope.go:52-63` `parseDirectory` 优先 `x-opencode-directory` header;
- `scope.go:69-82` `normalizeDir` 双侧 `Clean + EvalSymlinks + TrimRight(sep)`;
- `scope.go:88-101` `classify`:`nd == nw` 为 `InScope`,否则 `OutOfScope`。

**关键裂点**:即便 `cmd.Dir = worktree` 且 proxy 额外注入 `?directory=<worktree>`,**只要请求头里 `x-opencode-directory` 不是 worktree 自身**,`parseDirectory` 的 header 优先规则就以 header 值为准、走 `OutOfScope` 判定。也就是说我注入的 `?directory=` 在 header 存在时被绕过了。

### 2.2 现象 3:session 历史不完整

**用户观察**:
- 在 myworktree 启动的实例里打开,新会话**没有完整历史会话**;刚关闭的活动会话也不显示。
- 重启实例进程后,出现的是**更早时候的个别会话**,会丢失最近一段。
- 在**同目录**(`/home/linletian/orca/workspaces/myworktree/opencode-native-ui`)用原生 `opencode web` 启动 →能看到所有丢失的会话。

**判断**:session **物理上不丢**,丢的是 serverKey 视图。

证据链:

1. `WORKTREE-ISOLATION.md §0.1` 明确:opencode 单 SQLite 共存,**myworktree 故意没动 `XDG_DATA_HOME`**(吸取 reasonix issue #56 教训,见 `WORKTREE-ISOLATION.md §1.2`)。
2. 1.18 新布局下,session 展示 URL 是 `/server/<base64(serverKey)>/session/<id>`,**serverKey = server URL**。
3. 不同 `serverKey` 的 session 在 UI 侧栏默认按当前 `defaultServerUrl` 过滤显示(`WORKTREE-ISOLATION.md §2.5` v2 SDK baseUrl = 当前 server URL)。
4. 原生 `opencode web` 启动时,server URL = `http://127.0.0.1:<native port>` 直接绑定的某个端口;myworktree 注入时,server URL = `http://127.0.0.1:<portal>/__opencode/<instance_id>`。
5. 即便 `directory` 相同,serverKey 不同 → 侧栏看到的 session 子集就不同。
6. **"刚关闭的活动会话都不显示"**:那条最近 session 的 `directory` 字段如果是经过 HOME 视图化的路径(`/home/linletian/projects/myworktree`),而**注入的 `?directory=<worktree>` 是原始 raw path** → 即使 serverKey 匹配,`session.list` 也按 `directory` 过滤,二者对不上 → 看不到。
7. **"重新启动实例进程后出现的是更早时候的个别会话"**:可能是 instance id 变化导致 serverKey 变化,命中了别的 serverKey 的历史;或者 user 曾经在 raw path 下创建过 session,最近一条切到 HOME 视图化路径后,切换前后 serverKey/directory 都不一致。
8. **"同目录起的 opencode web 原生 UI 能看到所有丢失的会话"**:原生 UI 的 serverKey 是 `http://localhost:<port>`,独立存储;不同的 serverKey 看到的就是各自历史。

**第三种可能(待验证)**:1.18.16 还可能按 `server.v3.list` 全集匹配,而不仅仅是 `defaultServerUrl`。这个需要 devtools 验证。

**对应代码**:
- `proxy.go:229-240` 注入脚本:
  ```js
  localStorage['opencode.settings.dat:defaultServerUrl'] = pu
  localStorage['opencode.global.dat:server'] = {
    list: [pu],
    projects: { [pu]: [{worktree: wtp, expanded: true}] }
  }
  ```
  注意 `projects[pu]` 里只有 `worktree` 和 `expanded`,**没有 `name`/`label`/`id` 字段**。

### 2.3 现象 4:搜索栏显示 URL 而不是项目名

**用户观察**:
- 原生 `opencode web`:搜索栏显示 `在 <项目名> 中搜索会话`。
- myworktree 启动的 OC web 实例:显示 `在 <IP:Port>/__opencode/5635febc6bdd 中搜索会话`(后半段截断,推测是同一 URL 完整显示)。

**判断**:注入脚本 `server.v3.list[0]` 缺 `name` 字段,UI 兜底展示 URL。

原生 `opencode web` 启动时,`server.v3.list` 元素结构可能是 `{name: "local", url: "http://127.0.0.1:1234"}` 或类似由 opencode 客户端默认填的;myworktree 注入只设 `url`,没设 `name`,UI 兜底用 URL。

**对应代码**:`proxy.go:230-239`(完整片段见 §2.2 第 8 点)。`list[0]` 被写成 `{url: pu}`,无 `name` 字段。

---

## 3. 四个现象的对应关系表

| # | 现象 | 触发点 | 直接原因 | 关键代码 |
|---|---|---|---|---|
| 1+2 | 警告条 + 新会话找仓库 | proxy 收到 `directory != worktree` 的请求 | opencode 1.18 把 `projects[pu][0].worktree` 解析后,经 HOME 视图化(`$HOME/projects/...`)写回 `x-opencode-directory` 头;header 优先于 `?directory=` | `proxy.go:109-114`、`scope.go:52-63`、`scope.go:88-101` |
| 3 | session 视图不完整 | serverKey 维度按 URL 划分 | 注入脚本只设 `url` 不设稳定 `name`,且 instance id 变化会改 serverKey;同时 `directory` 视图化导致部分最近 session 落在另一条 directory 键下 | `proxy.go:236-238`(list / projects 结构) |
| 4 | 搜索栏显示 URL | UI 渲染 `server.v3.list[*].name` 兜底 | 注入脚本没塞 `name` | `proxy.go:236` `sd.list=[pu]` |

**共同根因一句话**:myworktree 注入脚本只补 `defaultServerUrl` + `projects.worktree`,**没补 serverKey 的稳定标识和 directory 视图化的纠偏**。opencode 进程 cwd 始终是 worktree(正确);问题在浏览器侧 SPA 把 worktree 重新解释成另一条路径并以这个新路径作为 serverKey 的项目身份。

---

## 4. 验证手段(改代码前必做)

不动代码,先用浏览器 devtools / 进程级验证以下三件事:

1. **确认现象 1 的实际 header 值**:
   - 打开 myworktree 启动的 opencode-web iframe。
   - devtools Network → 找到触发警告的那一次请求(`/session` 或 `/project` 等带 `x-opencode-directory` 的请求)。
   - 记录 `x-opencode-directory` header 的实际值,以及 myworktree 的 `worktree_abs`(从 `localStorage` 或 instance 记录的 `kindBlob` 查)。
   - 这能确认是 HOME 视图化路径,还是别的形态。

2. **确认现象 3 的 serverKey 假设**:
   - 原生 `opencode web`(同 worktree 目录)打开 devtools → Application → Local Storage → `opencode.global.dat:server` → `list[0].url`、`list[0].name`。
   - myworktree iframe 同样 Local Storage → 对比。
   - 同时检查 `opencode.db`(可以 `sqlite3 ~/.local/share/opencode/opencode.db "SELECT id, directory, ... FROM session LIMIT 20"`),看 session 实际存的 `directory` 字段是什么。

3. **确认现象 4 的字段缺失**:
   - myworktree iframe → `localStorage.opencode.global.dat:server.list[0]` 展开;对照原生 `opencode web` 的 `list[0]` 字段。

如果验证结论一致,则进入修复设计阶段(下一份报告);验证不一致则回头补充本页分析。

---

## 5. 备选修复方向(仅作"如果验证后这是根因"的备选)

> **标注:都还没做,等 §4 验证后才会动。**

### 5.1 现象 1+2(目录错位)

- **方案 A**:proxy 注入脚本里,在 `projects[pu]` 写入时,**额外存一个 `rawPath` 或 `directory` 字段为 raw worktree 路径**,让 opencode 客户端在发请求时优先用 raw 路径做 `x-opencode-directory` 头。
- **方案 B**:proxy 在最终转发时,如果检测到 `x-opencode-directory` 是 HOME 视图化路径(以 `$HOME/projects/...` 开头且能映射回 `worktree_basename`),**改写为 `worktree` 后转发**,同时记录 scope 状态。需要在 proxy 解析里多一层。
- **方案 C**:放弃 `cmd.Dir = worktree`,改为 `cmd.Dir = $HOME/projects/<worktree_basename>` 让 opencode 进程 cwd 与它自己认为的"项目目录"对齐。**副作用**:opencode 内部对相对路径解析会指向 `$HOME/projects/...`,与 git root / 文件系统绝对路径依赖产生新冲突;**不推荐**。

### 5.2 现象 3(serverKey 错位)

- 注入脚本在 `list[0]` 写 `{url: pu, name: <worktree_basename>}` — **稳定 name** 解决一部分。
- 同时把 `projects[pu][0].worktree` 字段保持为 raw path,确保后续 directory 视图化前先拿到 raw。
- 注意:不能 hot-fix SQLite 里的历史 session 的 `serverKey` 字段;那部分历史只能通过让原生 opencode web 的 `serverKey` 也被显示出来解决。需要 UI 侧做"显示全部 serverKey 历史 session"或在注入脚本里把其他 serverKey 的 session 列表"merge"。

### 5.3 现象 4(显示 URL)

- 注入脚本写 `list[0] = {url: pu, name: <worktree_basename>}`。一句话修复,与 5.2 共享。

---

## 6. 风险与边界

- **方案 A/B 都依赖 opencode 1.18.16 客户端对 `projects[pu]` 字段的现行语义**;如果未来版本改了字段语义(尤其是把 `worktree` 当作 ID 而非路径),注入脚本需要适配。
- **方案 C** 会改变 opencode 进程 cwd,这与 `WORKTREE-ISOLATION.md §1.2` 的"reasonix 教训"反向:**与 opencode 自身的产品语义(disk 上的 absolute path = project identity)脱钩**,风险大。
- **serverKey 的 merge** 必须谨慎,因为这是 UI 视图层逻辑 — 改 localStorage 字段可能影响 opencode 升级后的 schema 校验。
- **warning 行为**(`§4.5` 设计)目前为"防误操作而非禁止";如果 5.1 方案 B 实施,会有"proxy 改写 directory → 警告条不触发"的路径,**需要确认产品语义是否可接受**(可接受 = "只要最终落到 worktree 内,即便 SPA 误投递 HOME 视图也没事")。

---

## 7. 结论

不改动代码,等待 §4 三项验证结论再决定动哪一处。当前分析的最大不确定性:

1. `x-opencode-directory` header 里到底写的是 raw 还是 HOME 视图化路径(`WORKTREE-ISOLATION.md §2.5` 1.18.16 实证只验证了 header 字段存在,没验证内容);
2. 1.18 session 是否真按 `serverKey + directory` 二维 keyspace 区分;
3. 原生 `opencode web` 在 Local Storage 里 `list[0]` 的完整字段定义。

---

## 附录 A:相关代码位置速查

| 文件 | 行 | 关键内容 |
|---|---|---|
| `internal/instance/opencode_web/driver.go` | 114-115 | `cmd.Dir = params.WorktreePath`(opencode 进程 cwd) |
| `internal/instance/opencode_web/driver.go` | 141 | `blob: Blob{WorktreeAbs: params.WorktreePath}` |
| `internal/instance/opencode_web/proxy.go` | 60 | `host, port, worktree := readBlob(inst.KindBlob)` |
| `internal/instance/opencode_web/proxy.go` | 74-84 | `parseDirectory` + `ScopeTracker.Record` |
| `internal/instance/opencode_web/proxy.go` | 109-114 | `?directory=<worktree>` 注入(仅当客户端没自带) |
| `internal/instance/opencode_web/proxy.go` | 191-192 | `worktreeB64 = base64.RawURLEncoding(worktree)` |
| `internal/instance/opencode_web/proxy.go` | 224-294 | `buildInjectScript` 完整注入脚本 |
| `internal/instance/opencode_web/proxy.go` | 230 | `localStorage['opencode.settings.dat:defaultServerUrl'] = pu` |
| `internal/instance/opencode_web/proxy.go` | 236-238 | `sd.list=[pu]` + `sd.projects[pu]=[{worktree:wtp,expanded:true}]` |
| `internal/instance/opencode_web/scope.go` | 52-63 | `parseDirectory` header 优先 / query 兜底 |
| `internal/instance/opencode_web/scope.go` | 69-82 | `normalizeDir` Clean + EvalSymlinks + TrimRight |
| `internal/instance/opencode_web/scope.go` | 88-101 | `classify` 判定矩阵 |
| `internal/gitx/gitx.go` | 12-24 | `GitRoot` = `git rev-parse --show-toplevel` |
| `internal/app/app.go` | 120 | `root, err := gitx.GitRoot(".")` |
| `internal/app/app.go` | 388 | `portal.Config{WorktreePath: s.root}` |
| `internal/framework/manager.go` | 152 | `wtPath, wtName, err := m.resolveWorktree(in)` |
| `internal/framework/manager.go` | 486-500 | `resolveWorktree` 优先 `in.Root` |
| `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` | §0.1 | 数据目录设计原则(不隔离) |
| `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` | §1.2 | reasonix issue #56 教训(隔离丢历史) |
| `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` | §2.3 | 1.18 新布局 serverKey 路由 |
| `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` | §2.5 | 1.18.16 实证(隔离数据目录起服务) |
| `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` | §4.5 | 警告条设计(persistent) |
| `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md` | §4.6 | 注入脚本设计(D4 隐藏入口) |

---

## 附录 B:回答用户提问的明确答复

> **2026-08-13 更新**:以下答复基于附录 D 的最终结论修订(原"HOME 视图化"假设已证伪)。

**Q:`/home/linletian/projects/myworktree` 是 opencode 自己搞的沙箱吗?**

A:不是"沙箱",也不是"HOME 视图化"产物。它是 **opencode.db 中 project `a6e112...`(myworktree 仓库项目)的 `worktree` 字段陈旧值**——2026-06-19 该项目首次在该目录初始化时写入(当时该目录是仓库的真实 checkout,证据:该 project 下有 5 个当日会话 `directory=/home/linletian/projects/myworktree`),此后 `fromDirectory` 对已存在项目永不更新该字段;8-13 该目录被清空只剩 `.codegraph`,但 DB 字段原样保留(详见附录 D)。

**Q:为什么新会话总会找很久原始仓库?**

A:前端新建会话时 `sync().project?.worktree`(server 原始 project 的陈旧值)优先于正确的 `sdk().directory`,POST /session 携带 `x-opencode-directory: /home/linletian/projects/myworktree`;server 依 header 把 agent 的 cwd 切到该目录(非 git 仓库),于是 agent 需要自己找到真实 worktree。

**Q:为什么 session 历史不完整?**

A:不是物理丢失,是**目录分桶错位**:会话按 `project_id + directory` 分桶(schema 实证)。8-13 新会话落在 `global + 幽灵目录` 桶,主页列表按请求目录解析出的 project(`a6e112`)过滤,二者互不可见。原 SQLite(`opencode.db`)数据完整。

**Q:为什么搜索栏显示 URL?**

A:`selectedProject=null`(layout 选择态为空)+ 注入使 server 列表变为 2 个(entry 的 `location.origin` 与注入的 `pu`)→ 命中"多 server 显示 `serverName`"分支;`displayName` 缺失是次要因素。修复:注入脚本给 `sd.list` 补 `displayName`(见 D.5)。

---

## 附录 C:报告与对话的差异

- 标题:本文档与对话中口头分析的核心结论一致,**未修改任何结论**。
- 增量结构:对话外的文档化补充包括:
  - §0 基础信息(commit hash、commit message、时间、分支状态);
  - §1.1 myworktree 端代码引用(`driver.go`、`proxy.go`、`scope.go` 行号);
  - §3 现象对应表(替换对话中的口语化表述);
  - §4 验证手段(三条 devtools 检查清单);
  - §5 备选修复方向(每个现象单独列出);
  - §6 风险与边界(防止改坏的清单);
  - 附录 A 代码位置速查表、附录 B 用户提问的明确答复、附录 C 文档与对话差异。

---

**报告结束。**

---

# 附录 D:两版分析综合判断(2026-08-13 定稿)

> 本节是本文档(基于 opencode 1.18.16 源码 + "HOME 视图化"假设)与第二版分析(基于 opencode 1.18.18 源码 clone + 运行时数据库/日志实证)的综合结论。
> **结论先行:本文档 §2.1 的"HOME 视图化"核心假设被证伪;真实根因是 opencode.db 中 project 表 `worktree` 字段的陈旧值,经前端 `sync().project?.worktree` 传播进 `x-opencode-directory` header。** 修复决策:仅 myworktree 代码防御(proxy 强制覆盖 directory),不动 opencode 数据,幽灵会话保留。

## D.1 分歧点与判定

`/home/linletian/projects/myworktree` 的来源:

| 假设 | 证据 | 判定 |
|---|---|---|
| 本文档:"HOME 视图化"(opencode UI 把 worktree 显示为 `$HOME/projects/<basename>` 后回写 header) | ① `displayName = project.name \|\| getFilename(project.worktree)`(1.18.16/1.18.18 一致),前端无任何 `$HOME/projects/` 路径变换逻辑;② 若视图化应得 `/home/linletian/projects/opencode-native-ui`(worktree basename),实际是 `/home/linletian/projects/myworktree`(basename 为 `myworktree`);③ 该路径在 opencode.db 中自 2026-06-19(project `a6e112` 创建日)即存在 | **证伪** |
| 第二版:数据库陈旧 `worktree` 字段 | ① project `a6e112c1ae...` 的 `worktree` 字段 = `/home/linletian/projects/myworktree`,6-19 首次初始化写入,`fromDirectory` 对已存在 project 永不更新该字段;② 该 project 下有 5 个 6-19 的会话 `directory=/home/linletian/projects/myworktree`(如"创建 SoftwareWorkspace 文件夹")——**该目录是历史真实 checkout**,后被清空只剩 `.codegraph`;③ 8-13 的 20 个新会话 `project_id=global`(目录已非 git 仓库 → 解析为 global) | **证实** |

## D.2 传播链(前端代码实证)

1. `GET /project` 返回全部 project,含 `a6e112.worktree = /home/linletian/projects/myworktree`(陈旧值)。
2. 前端多处直接消费 server 原始 project 对象(无 `enrich` 覆盖;enrich 只作用于项目列表展示层):
   - `new-session-workspace-controller.ts`:`projectRoot = sync().project?.worktree ?? sdk().directory`;`sync().project` 即 server 原始对象(`directory-sync.ts` 的 `Binary.search(serverSync.data.project, ...)`)。
   - `session-new-view.tsx`、`projectForSession` 的 `byID.get(session.projectID)` 同源。
3. 新建会话时 `sync().project?.worktree`(幽灵目录)优先 → POST /session 带 `x-opencode-directory: /home/linletian/projects/myworktree`。
4. server `defaultDirectory` 解析:`?directory=` → `x-opencode-directory` header → `process.cwd()`(header 优先于 proxy 注入的 query,本文档 §2.1 的判断正确)。日志实证:`creating instance directory=/home/linletian/projects/myworktree` 当天出现 **34 次**,每次都在 worktree instance 建立后数秒。

## D.3 四现象最终归因

| 现象 | 最终根因 |
|---|---|
| 1+2 警告条/找仓库 | 请求 header 携带陈旧幽灵目录 > proxy 注入的 query → `classify` out-of-scope;agent cwd=幽灵目录(非 git) |
| 3 会话不完整 | 会话按 `project_id + directory` 分桶(schema 实证);新会话落 `global + 幽灵目录` 桶,主页列表按请求目录解析出的 project(a6e112)过滤 → 互不可见。serverKey 是次要缓存维度(本文档 §2.2 部分成立) |
| 4 搜索栏显示 URL | `selectedProject=null`(layout 选择态为空)+ 注入使 server 列表变为 2 个(entry 的 `location.origin` + 注入的 `pu`)→ 命中"多 server 显示 `serverName`"分支;`displayName` 缺失是次要因素(本文档 §2.3 部分成立) |

## D.4 本文档 §5 备选方案的再评估

> **2026-08-13 修订**:§5.1 方案 B(proxy 改写 header 为 worktree)曾被采纳为"全方法强制覆盖",实施后经评估**回退**(见 D.5)——理由:锁定过度干涉 opencode 自身跨目录业务逻辑;最终改为"proxy 仅监测 + UI 入口 disable"。

- §5.1 方案 B(proxy 改写 header 为 worktree):**曾采纳后回退**。目录锁定的监测价值(越界提醒)被"强制改写"替代后,opencode 的跨目录能力(agent `cd`、跨 worktree 会话)被请求层禁止,与"给用户提醒而非禁止"的产品语义不符。
- §5.1 方案 A/C:不采纳。A 依赖 opencode 客户端字段语义,C 改变进程 cwd,与 §1.2 原则相悖。
- §5.2/§5.3(serverKey merge / name 字段):仅 §5.3 的 `displayName` 注入被采纳为现象 4 的改善项(保留);serverKey merge 不做(非物理丢失,且改 localStorage 有 schema 风险)。

## D.5 已确认修复决策(2026-08-13 用户确认;2026-08-13 修订)

> **修订记录**:初版曾计划"proxy 全方法目录锁定",实施后经评估**回退**——锁定过度干涉 opencode 自身业务逻辑,opencode 应有跨目录行为的能力(与 xterm/pty 终端实例一致),只因键鼠操作过于方便需给用户提醒而非禁止。最终形态:**proxy 仅监测 + UI 入口 disable(可见但禁用)**。

1. **不做目录锁定(回退)**:proxy 保持监测模式——记录请求携带的 directory,`classify` 判定越界后由前端常驻警告条提醒;`Director` 仅对 GET/HEAD 且无 directory 的 API 请求注入 worktree 作为默认目录提示(原始契约)。opencode 的跨目录能力(agent `cd`、跨 worktree 会话)不受影响。
2. **注入脚本隐藏 → disable**:跨 worktree 项目入口(主页项目列表 `home-projects-scroll`、`Open project` 按钮、session 页 `project-switch` 非当前项)从 `display:none` 隐藏改为 **disable**——`pointer-events:none` + `opacity:0.45` + `aria-disabled` 灰显,列表与按钮保持可见作为提醒;当前 worktree 项保持可交互(展开/折叠)。L3 报告从检测"隐藏是否生效"改为检测"禁用是否生效"(`disable-failed`),前端警告文案同步为"切换入口禁用未生效"。**该形态为试验性,测试一段时间后评估**。
3. **不动 opencode 数据**:project `a6e112` 的 `worktree` 字段保持现状(前端展示名可能仍取陈旧值,属已知可接受项);幽灵目录 20 个会话保留原样。
4. 现象 4:注入脚本给 `sd.list` 补 `displayName`(保留,与目录策略无关)。

## D.6 本文档 §4 验证清单的回应

1. **header 实际值**:已实证 = `/home/linletian/projects/myworktree`(raw 路径,非视图化)。依据:34 次 `creating instance` 日志 + 20 个 `session.directory` 记录。
2. **serverKey 假设**:不成立。`session` 按 `project_id + directory` 分桶(schema 实证),与 server URL 无关。
3. **list[0] 字段**:`displayName` 缺失存在,但现象 4 的充要条件是 `selectedProject=null && server.list().length>1`(注入造成双 server)。

# 附录 E:TUI 与 opencode web 的目录差异 — 用户实测 + 源码实证(2026-08-13)

## E.1 用户实测(触发本次分析)

相同 Provider + 模型 + Agent + 提示词,让 AI 返回"当前项目路径":

| 启动方式 | AI 返回 |
|---|---|
| TUI(仓库主工作区 / worktree 工作区) | 实际启动 TUI 时的正确路径 |
| opencode web 启动的 web UI | `/home/<username>/projects/<project_name>/`(即 `/home/linletian/projects/myworktree`),并提示"这是一个包含 `.codegraph/` 目录的项目,看起来是一个代码图(codegraph)项目,使用了 SQLite 数据库来存储代码图数据" |

结论:**会话目录确实落在幽灵目录**,AI 的描述(看到 `.codegraph/`)反证了这一点——该目录正是由 opencode codegraph daemon 按 instance 目录初始化的。

## E.2 机制:不是"沙盒策略",是"客户端目录来源"差异

服务端两模式共用同一逻辑(v1.18.18 实证):

- `packages/opencode/src/server/routes/instance/httpapi/middleware/workspace-routing.ts:86-88`
  ```ts
  function defaultDirectory(request, url): string {
    return url.searchParams.get("directory") || request.headers["x-opencode-directory"] || process.cwd()
  }
  ```
- `packages/opencode/src/session/session.ts:669-682`:`Session.create` 的 **body 无 directory 字段**,`directory: ctx.directory`(InstanceState = middleware 解析值),另有 `path: sessionPath(ctx.worktree, ctx.directory)`。
- `packages/opencode/src/cli/cmd/serve.ts:10-11` 注释:"Server loads instances per-request via x-opencode-directory header — no need for an ambient project InstanceContext at startup."

**目录解析优先级:query > header > process.cwd()**。server 启动时的 cwd 只是兜底。

## E.3 TUI 路径(永远正确)

`packages/opencode/src/cli/cmd/tui.ts:66,200-211`:

```ts
const next = resolveThreadDirectory(args.project)  // = process.cwd()
process.chdir(next)                                // TUI 进程切到启动目录
```

worker(server 进程)继承该 cwd。TUI 的请求要么不带 directory(server 兜底 = 启动目录),要么带 cwd 值。**不经过任何项目恢复状态 → 永远正确**。

## E.4 web UI 路径(为什么拿到幽灵目录) — 完整闭环

1. 前端创建会话显式传目录 — `packages/app/src/components/prompt-input/submit.ts:355,404-407`:
   ```ts
   const projectDirectory = sdk().directory
   ...
   .api.session.create({ ..., location: { directory: sessionDirectory } })
   ```
2. 该目录 = 新建会话页选中的项目 worktree — `packages/app/src/pages/home/home-controller.ts:30-35`:
   ```ts
   const newSessionProject = createMemo(() =>
     projects().find((p) => p.worktree === projects.last()) ?? projects()[0])
   ```
   `projects.last()` = localStorage `lastProject[scope]`(打开过幽灵会话后被写入);为空时取**列表第一个**。
3. projects 列表 = server `GET /project` = **DB project 表全量** — `packages/opencode/src/project/project.ts:336-338`(`Project.list` 无 cwd 过滤,`db.select().from(ProjectTable).all()`)。
4. DB 陈旧行(本次实测,opencode.db 只读查询):
   ```
   id = a6e112c1ae884ba2e286275480e04b64179d5839
   worktree = /home/linletian/projects/myworktree, vcs = git, 挂 14 个会话
   ```
   6/19 创建(当时为真实 checkout);`fromDirectory` 只 upsert 匹配 id 的行,陈旧行**永不清理、永不更新 worktree** → 前端列表永远有该"幽灵项目"。
5. SDK 把目录写进每个请求 — `packages/sdk/js/src/v2/client.ts:49,66`:
   ```ts
   "x-opencode-directory": encodeURIComponent(config.directory)   // POST 也带
   ```
   GET/HEAD 另由 request interceptor 改写为 `?directory=`(v2/client.ts:25-45)。
6. server header 优先于 cwd(§E.2)→ instance directory = 幽灵目录 → agent 在该目录工作。

**关键推论:这不是 myworktree 引入的问题,原生 opencode web 同样复现**(只要 DB 有陈旧 project 行且前端选中它)。此前 34 次 `creating instance directory=/home/linletian/projects/myworktree` 日志正是该机制。

## E.5 版本一致性确认

v1.18.18(用户实际版本)与本地源码 v1.18.16 逐项比对:defaultDirectory 优先级、`Session.create` 的 `ctx.directory`、`Project.list` 全表、v2 client header 编码、submit.ts 的 `location.directory`、home-controller `newSessionProject`、tui.ts `process.chdir` — **全部一致**。

## E.6 与既有结论的关系

- 确认并强化 D.2/D.3:header 携带幽灵目录的传播链成立;本次补充了"**前端为什么携带**"的完整源头(DB 陈旧行 → `GET /project` 全量 → `last() ?? projects()[0]` 默认选中)。
- 补充维度:**TUI 与 web UI 的行为差异是设计使然**(serve 按 header 加载实例,TUI 按 chdir 后的 cwd),并非缺陷;缺陷在 DB 陈旧行的残留与前端无校验的默认选中。
- 现象 3(会话丢失)与现象 4(URL 显示)在该机制下是同一根源的不同表现(会话落幽灵桶 / server 未识别项目时显示地址)。

## E.7 后续方案(2026-08-13 选定 A+E,实施中)

候选方向演进:
1. 治本:清理 DB 陈旧 project 行(a6e112)或前端选中前校验目录存在且为 git 仓库;
2. 上游防御:`newSessionProject` 过滤幽灵项目;`defaultDirectory` 校验目录有效性后回退 cwd;
3. myworktree 侧:注入脚本 preseed 项目列表 + proxy 监测(已就位;目录锁定已按 D.5 回退)。

### E.7.1 选定决策(2026-08-13,用户确认)

**选定 A + E 先行试验**(先更新文档 → 备份数据库 → 再实施),B/C/D 暂缓。

- **A:清理 DB 陈旧 project 行**。对象 = project 表 `a6e112c1ae884ba2e286275480e04b64179d5839`(worktree=`/home/linletian/projects/myworktree` 陈旧值)。
  - 影响面(实施前实测):该行挂 14 个会话,分布在 4 个目录——`SoftwareWorkspace/myworktree`×1、`orca/workspaces/myworktree/opencode-native-ui`×1(均仍存在)、`projects/myworktree`×5、`projects/myworktree-myworktree/feature-github-link`×7(后两者为废弃目录)。
  - 关键机制:**project id 基于 git remote hash,稳定**——删除后,opencode 下一次在任一真实 worktree(如 `opencode-native-ui`)解析目录时会经 `fromDirectory` **自动重建该行,worktree 字段变为真实目录**,幽灵项从 `GET /project` 列表消失,前端 `newSessionProject` 不再默认选中幽灵目录。
  - 会话数据不受影响(仅 `project_id` 悬挂至重建);`project_directory` 关联保留(重建时 upsert 复用)。
  - 操作顺序:在线备份(SQLite backup API,服务运行中一致快照)→ `DELETE FROM project WHERE id='a6e112…'` → 只读校验行已删、会话行未动。
- **E:UI disable 试验模式(已实现,继续生效)**。D.5 已落地:项目列表/`Open project`/`project-switch` 非当前项 disable(可见但不可点),L3 报告 `disable-failed`,前端文案"切换入口禁用未生效"。本阶段无新代码,与 A 配合验证"启动自动定位 + 不漂移"。
- 预期效果:启动后新会话默认目录 = 注入脚本 preseed 的 worktree(正确)→ in-scope,无越界警告;过程中 agent 跨目录能力保留;disable 防误操作。

### E.7.2 实施记录(2026-08-13)

- [x] 更新本文档(本节)
- [x] 备份 opencode.db → **`.omo/opencode-backup/opencode.db.2026-08-14-01-37-17`**(2.23GB,`VACUUM INTO` 事务性一致快照;校验 projects=6/sessions=235/messages=7298 与源一致;工作区 git 忽略目录,可随时移往主目录)
- [x] DELETE project 行 a6e112 —— **沙箱限制:Reasonix 执行环境只读挂载(`attempt to write a readonly database`),写操作需用户在真实终端执行**,命令见下方
  - 2026-08-14 补充:用户已确认停止相关实例;残留的 systemd 托管 `opencode serve`(pid 899425)已由用户授权 kill,当前无任何 `opencode serve` 进程,DB 写入者清空(删除前状态:projects=6, sessions=235, a6e112 行仍在)
  - **级联副作用(重要)**:用户执行 `run-delete.js` 后,SQLite 外键级联(ON DELETE CASCADE,node:sqlite 默认开启外键)连带删除 **14 session / 341 message / 1339 part / 21 todo / 2 project_directory**(14 会话含 2 个近期活会话)。备份完好。
- [x] **恢复(用户确认"恢复全部 14 个会话")**:`run-restore.js` 从备份重建 project 行(**worktree 修正为真实仓库根 `/home/linletian/SoftwareWorkspace/myworktree`**,幽灵值彻底清除)+ 恢复 14 会话及 message/part/todo/project_directory。恢复前先在备份副本上模拟验证通过(各表数量与备份一致),再由用户执行。
- [x] 验证:只读终验通过——project 6/session 235/message 7298/part 32535/todo 637/project_directory 11 与备份完全一致;**无任何 project 行再指向幽灵目录**(0);a6e112 行 worktree=仓库根,挂 14 会话。
- [~] 用户重启 opencode-web 实例后观察(首轮反馈见 E.7.3,测试进行中)
- 脚本与备份存档:`.omo/opencode-backup/`(git 忽略)——`opencode.db.2026-08-14-01-37-17`(删除前快照)、`run-delete.js`、`run-restore.js`(支持 `RESTORE_TARGET`/`RESTORE_BACKUP` 环境变量,可用于沙箱模拟;删除命令块已完成使命,不再内联)

### E.7.3 首轮观察(2026-08-14,测试进行中)

**用户反馈**:首次启动实例后新建新会话,仍有越界提示,但**提示路径为主工作区路径**(`/home/linletian/SoftwareWorkspace/myworktree`),实际会话从 worktree(`opencode-native-ui`)启动;新会话开始对话后提示消失。

**DB 实证(决定性)**:最新 2 个会话:
```
2026-08-14T01:56/01:57 | project=a6e112c1 | directory=/home/linletian/orca/workspaces/myworktree/opencode-native-ui
```
→ **会话创建完全正确(directory=实例 worktree,无漂移),A 修复生效**;幽灵目录值已彻底消失(警告路径从幽灵目录变为真实仓库根)。

**警告来源与消失机制(源码确认)**:
1. 新建会话页(`/new-session`)的 worktree 选择器组件 `new-session-workspace-controller.ts` 用 `sync().project?.worktree`(server 返回的 project 原始对象,**worktree=仓库根**,项目级主目录)调用 `serverSync().child(仓库根)` 查询分支信息 → 产生一个 `directory=仓库根` 的请求 → proxy 监测如实判 out-of-scope → 警告条短暂亮起(路径=仓库根)。
2. 发送第一条消息后:会话创建请求与后续请求的 directory 均为实例 worktree(in-scope),覆盖 ScopeTracker 记录 → 警告熄灭。

**判定**:**正常、无害**。`project.worktree`(仓库根)是 opencode 项目级主目录概念(跨 worktree 共享),myworktree 实例 worktree 是隔离单位,二者不同属原生语义;draft 页查询一次主目录分支是原生行为,不改变会话目录。该提示兼有防误操作价值。

**可选优化(暂缓,测试期后评估)**:
- 接受现状(推荐):仅新建会话页短暂提示一次,语义准确。
- 恢复时把 worktree 设为当前 worktree:本次不亮,但 worktree 字段是项目级单值,未来在其他 worktree 起实例时会换路径出现同样提示。
- proxy 宽容"同 git 仓库"目录:需比较 git common dir,复杂且削弱监测,不推荐。

**下一步**:用户在当前状态下继续测试(新会话定位、会话持久、disable 入口、越界监测),观察期后决定是否采纳优化或结束试验。

### E.7.4 多实例观察与串台修复(2026-08-14)

**现象一纠正(会话互通判断修正)**:用户反馈主工作区与 opencode-native-ui worktree 实例显示**两个独立项目**(项目名分别为 `myworktree` / `opencode-native-ui`)。复查源码:主页 `session.list` handler 传 `directory`(InstanceState.directory),`Session.list` 内为 **project_id AND directory 双条件过滤**(session.ts list 的 `conditions.push(eq(SessionTable.directory, input.directory))`)——同仓库不同 worktree 实例**本就按目录隔离显示**,不存在"project 级聚合互通"。此前"现象一=DB project 级聚合互通"的判断为误判,予以纠正。前端项目名差异来自各实例注入脚本 preseed 的 worktree(enrich 覆盖)。

**现象二(短暂串台)根因与修复**:
- 根因:① `deactivate()` 不隐藏当前 iframe(keep-alive 设计,但切到空 worktree/新实例时旧页面残留可见);② `activate()` 对 `status !== 'running'`(含 starting)直接 return——不创建新 iframe、不 poll、不隐藏旧 iframe,直到用户手动重切 tab 才恢复。
- 修复(代码已改,待提交):
  1. `deactivate()` 隐藏 `_currentFrame`(页面状态保留,keep-alive 语义不变);
  2. `activate()` 仅对 `status === 'stopped'` return;starting 继续 poll,ready 后自动导航(无需手动切 tab);
  3. **新增 loading 层**(`#opencode-loading`):starting 时显示"正在启动 opencode server…"+ 信息行(instance id、elapsed;ready 后由 debug bar 展示 upstream/proxy/worktree/version),超时显示错误,stopped 显示"已停止"。样式与 helper(`_ensureLoading/_showLoading/_hideLoading`)通用,未来 web-ui 类实例可复用。
- 验证:JS 语法 `node --check` 通过;`go test ./...` 全绿。
- 待办:提交(Reasonix 沙箱对 git 元数据只读,commit 需用户在真实终端执行)。

