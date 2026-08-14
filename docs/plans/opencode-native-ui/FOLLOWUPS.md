# opencode-native-ui — 后续优化 / 待办清单

> 本文件汇总评审与调试过程中识别的**后续优化项、已知限制、待验证项**，方便后期统一查看。
> 详细背景与决策过程见 `OPENCODE-WORKDIR-DEBUG-2026-08-13.md`（附录 E 及各轮评审回应）。
> 状态约定：⬜ 待办 · 🕐 暂缓/观察期 · 📌 已知限制（不计划做，仅记录）。

## 一、注入脚本重构（来源：评审第二轮 #1/#5）

- ⬜ **把注入脚本提取为独立 `.js` 资源**（`go:embed` + CSP hash 自动重算），替代 `internal/instance/opencode_web/proxy.go` 里 `buildInjectScript` 的单字符串模板。
  - 动机：当前脚本是一个 Go 字符串直接塞给浏览器，无 IDE/lint 帮助，**逐字段重建漏字段就是静默丢**（`duplex:'half'` 就是实例——曾导致 "ReadableStream uploading is not supported"）；同一字符串里还有 WebSocket/EventSource/Worker/sendBeacon/XHR 劫持，任一项随新平台/新 headers 演化都容易再漏。
  - 收益：可读、可单测（直接 import）、可 lint（如 `node --check` / eslint）。
  - 注意：CSP `script-src` 的 hash 需随内容重算——现有 `TestFixProxyHTMLCSPAnchor` 覆盖，重构后该测试会自动兜底。
  - **提取时一并审**（评审第三轮 #3）：`ri()` 的嵌套 try/catch 保护较"刻薄"，而 preseed、`r()` 等历史代码块的回退均无同等保护——重构时统一审视这些薄弱路径，按同标准补保护。
- ⬜ 顺带解决命名/归类问题（评审第二轮 #5）：
  - `internal/instance/opencode_web/proxy.go` 实际是「注入脚本生成 + ScopeTracker」，与规划中的反向代理文件命名易混；
  - 脚本内 `p`（`/__opencode/<id>` 前缀）、`ri()`/`r()`（URL 重写）为压缩风格缩写，提取成独立文件后可换有意义的名字。
- 📌 **URL 重写的主路径是「`Request` 构造时加前缀」，不是 fetch 层重建**（评审第四轮单列追踪）：
  - 取舍：单 server 模式（bare origin）后，SDK 每个请求都经 `new Request(url, init)` 构造——劫持构造器在源头加前缀，`fetch()` 拿到已带前缀的 Request 原样放行，**零重建、零 body/duplex 往返**，与单 server 前（各浏览器正常）路径完全一致；
  - 曾走过的弯路：在 fetch 层用 `ri()` 重建 Request（显式复制字段 + `duplex:'half'`）——Chromium 接受但 **Safari 仍报 `ReadableStream uploading is not supported`**，故重建降级为兜底；
  - 构造包装覆盖三种形态：**string**、**URL 对象（`href`）**、**Request 复制构造（`url`，init 缺省时以原 Request 为 init——满足 Web 规范的 Request 复制构造语义，保留 method/body——否则会静默退化为 GET 空 body）**；其余形态静默不加前缀；
  - **未来约束**：若 SDK 改变请求构造形态，先扩展此处的形态覆盖，**不要**把前缀重写移回 fetch 层重建（Safari 流式上传回归）；行为测试 `TestInjectURLRewriteBehavior` 覆盖三种形态与复制构造。

## 二、真浏览器测试（来源：评审第二轮 #2）

- ⬜ **Playwright headless 冒烟测试**（作为 CI 项或手动脚本）：打开实例 → 发送会话消息 → 断言无 `ReadableStream uploading is not supported`、消息回显成功。
  - 动机：现测试在 Node VM（undici）里跑，Node 与 Chromium 的 `Request` 实现不一致；Node 通过 ≠ Chromium 接受。`duplex` 回归只能靠真浏览器兜底。
  - 现状已做的补偿：`TestInjectURLRewriteBehavior` 读回重建后 Request 的 body，断言流可读且内容完整（`fetch-body:hello`）。

## 三、旧数据清理策略（来源：评审第一轮问题 3）

- 🕐 **`projects[origin+p]` 旧 key 与旧裸键 `opencode.global.dat:server` 的 localStorage 残留**：实例键隔离后旧数据成孤儿（无害但累积）。
  - 目前**不自动清理**：裸键可能含用户原生 opencode web（非 myworktree 实例）的本地数据，误删有损。
  - 可选未来方案：提供手动清理入口（如 debug 命令），或仅当确认裸键为旧注入脚本格式时迁移/删除。

## 四、已知限制（记录，不计划做）

- 📌 **markdown 根相对图片**（`![](/path)`）：解析到裸 origin → 404。属用户内容，与 opencode 原生子路径部署行为一致；前端本身不动态创建根相对资源（`getProjectAvatarSource` 只返回完整 URL）。
- 📌 **多实例极端竞态**：实例键隔离后，两实例同时刷新（并行加载）的极端场景下 localStorage 最终值 = 最后完成加载的实例；各实例内存 store 不受影响，任一实例下次刷新自愈。
- 📌 **`sendBeacon`/`WebSocket` 劫持为防御性覆盖**：SDK 目前用 SSE（EventSource），未来切换协议时需验证（`r()` 已支持 `ws://`/`wss://` 前缀重写）。

## 五、观察期决策项（来源：OPENCODE-WORKDIR-DEBUG-2026-08-13.md §3，disable 模式试验期后评估）

- 🕐 **disable 模式效果评估**：跨 worktree 项目入口「可见但不可交互」（pointer-events + opacity + aria-disabled）试验期结束后，决定保留/调整。L3 检测报告 `disable-failed` 会给出数据。
- 🕐 **越界提示可选优化**（暂缓）：
  - 接受现状（推荐）：仅新建会话页短暂提示一次；
  - 恢复时把 worktree 设为当前 worktree（worktree 字段是项目级单值，未来在其他 worktree 起实例会换路径提示）；
  - proxy 宽容「同 git 仓库」目录（需比较 git common dir，复杂且削弱监测，不推荐）。

## 六、端到端验证清单（重启 myworktree 后逐项确认）

- [ ] 单 server 模式：home 页左侧无多余 server 行（无双 `HomeServerRow`），仅项目列表
- [ ] 多实例并存：各实例 home 页显示**各自** worktree 的项目（localStorage 键隔离生效）
- [ ] 发消息：无 `ReadableStream uploading is not supported`，消息流式回显正常（`duplex:'half'` 保留）
- [ ] 搜索栏 placeholder 显示项目名而非 URL（displayName 生效）
- [ ] 新会话 directory = 实例 worktree（无越界警告；ScopeTracker 记录 in-scope）
- [ ] opencode-web 实例：无底部多行状态栏、无右上角刷新/到底按钮；终端实例保留两者
