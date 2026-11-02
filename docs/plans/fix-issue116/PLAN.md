# Issue #116 修复计划：iPadOS 可视高度、Gmail 式响应式壳层与 Portal 适配

**日期**：2026-10-27

**状态**：v1.8（D0-1/D0-2 已随 `346ed62` 签署通过，UI 编码门禁解除。**两条修复线已落地并有真机实证**：PR1a 铺底根画布 `7c5ca6d`——白带真因确定为**铺底缺失而非高度缺失**（html/body/#app 全 transparent，#workspace 底边距可见底恒 20.0px＝#app 的 env(safe-area-inset-bottom)，且 color-scheme 从未声明），设备回报三背景已同为 rgb(11,16,32)、含缩放与平移态无白色；PR1b 钉高 pan 免疫 `a2b5d5f`+`0a8f584`——判据与目标只读 `vv.height`，妙控浮窗按产品决定出范围，existing-pin 门禁删除。§4.2 中依赖 `visibleBottom` 的旧条款已标注「已被实测推翻并重写」。**仍未关闭**：A0 尚缺 scale=1 下键盘开→关的钉高复测（会话 choiceA 轮询中）、键盘打开时刷新、Split View、dsh/reasonix iframe、iPhone/Chrome(iOS)、逐像素截图；故**本文件不代表高度问题已全部修复**）

**基线**：`fix/fix-issue116` @ `225c5d7`（与当时 `origin/develop` 一致）

**来源**：
- [issue #116](https://github.com/linletian/myworktree/issues/116)：iPadOS Safari 底部白带、可见高度残差、妙控键盘浮动栏；
- [issue #100](https://github.com/linletian/myworktree/issues/100) 追加要求：参考 **Gmail Web 的响应式布局行为**完善主 UI；
- [issue #118](https://github.com/linletian/myworktree/issues/118)：作为本计划的独立 Portal 工作包顺手完整承接；
- 2026-10-27 用户追加：顶栏 Restart / Stop / Delete 按实例状态转换显示。

> **本次会话边界**：只形成计划。A0 探针、代码修改、测试修改、`.pen` 修改和 PNG 导出均是评审通过后的实施任务，本次不执行。

---

## 0. 计划结论与硬决策

1. **#116 先测量、后修算法。** 不把“始终按 `visualViewport` 钉高”预设为正确答案；先区分外壳几何、safe-area/透明画布、flex 传导和 xterm 行取整四类问题。
2. **白带不能用背景色遮盖来冒充修复。** 根画布主题铺底是需要的白缝修复，但关闭 #116 还必须证明 `#app` / 工作区几何和终端最后一行正确。
3. **Gmail Web 只作为布局行为参考，不复制品牌视觉、DOM、邮箱信息架构或断点数字。** 本期借鉴固定应用壳、导航抽屉、内容优先、工具栏操作优先级、面板内滚动和触屏可达性。
4. **保留现有侧栏产品分档与单一状态机**：`≤768px` drawer、`769–1024px` 200px 并排、`>1024px` 240px 并排；继续使用一个 `sidebarCollapsed` / `.collapsed` / `mw.ui.sidebarCollapsed`，不引入 navigation rail 或第二个侧栏状态。Gmail 参考不等于照抄 Gmail 的宽度阈值。
5. **顶栏做一次合并重设计**：固定新建实例入口、可横向滚动的 tabs、窄/中屏紧凑内联、粗指针足够的真实高度，以及按实例状态派生的动作按钮一次完成，避免多轮修改同一区域。
6. **动作显示采用同一 DOM、同一纯函数派生**：当前各状态最多两个动作，所有宽度都完整内联；stopped 的 Restart + Delete 不得藏入菜单，也不为不存在的三动作场景引入 More/overflow。
7. **#118 纳入计划，但作为独立 PR、独立验收、独立回滚。** 它不阻塞 #116；若需裁剪，应整个 Portal 工作包拆出，不能留下“表格改了一半、登录或 CSP 未闭环”的半成品。
8. **UI 变更先过 `.pen` 设计门禁。** 主 UI 更新既有 `sidebar-states.pen`，Portal 新增独立 `.pen`；PNG 和预览同步，未评审通过不得进入对应 UI 实施 PR。

---

## 1. 目标、需求编号与范围

| ID | 目标 | 来源 | 完成口径 |
| --- | --- | --- | --- |
| H1 | 主 UI 外壳底边在定义场景下对齐“产品意图上的可见底边” | #116 / #100 | 真机几何误差 ≤1 CSS px；不以白带消失替代几何验收 |
| H2 | 区分软键盘硬遮挡与妙控键盘浮动提示/输入栏 | #116 | 浮动栏不多吃工作区；软键盘下输入区仍可达；无 UA 嗅探和未标定魔数 |
| H3 | 消除默认白画布、safe-area 透明缝及 flex 高度传导歧义 | #116 / #100 追加 | 根画布主题一致，内部滚动器明确，终端/iframe 获得正确尺寸 |
| U1 | Gmail 式应用壳、内容优先顶栏与动作优先级 | #100 追加 | 各宽度无正文级横滚，主操作稳定可达，tabs 独立滚动，当前最多两个实例动作完整内联 |
| U2 | Restart / Stop / Delete 按活动实例状态转换 | 用户追加 | running 仅 Stop；stopped 仅 Restart + Delete；其他既有状态按 §5.4 矩阵 |
| U3 | 触屏动作不再依赖 hover，命中区由真实布局容纳 | #100 追加 | 触屏 worktree 动作可点；相关控件 ≥44×44 CSS px；移除顶栏透明溢出凑命中区的补丁 |
| P1 | Portal 响应式列表、视口、安全区、触屏和登录浮层 | #118 | 桌面无回归，手机长路径不溢出，登录软键盘可达，CSP/CI 闭环 |
| D1 | 设计源、预览、文档与实现一致 | 用户要求 | `.pen` + PNG + 预览 + PRD/CHANGELOG 按阶段同步 |

### 1.1 实施文件边界

- 主 UI：`internal/ui/static/index.html`；若传导线证实需要 trailing resize，可同步修改 `internal/ui/static/kinds/pty.js` 的镜像 RO 路径。其他 kind 只做回归，**不修改 vendored xterm**。
- A0 页面诊断：`internal/ui/debugassets/viewport_debug.js`、`internal/ui/viewport_debug_{on,off}.go`、`internal/app/viewport_debug.go`、`internal/app/viewport_debug_off.go`、`internal/app/app.go`（仅注册路由/持有 store）及对应 `*_test.go` / `viewport_debug.test.mjs`。该通道只传几何与有限设备信息，不进入高度修复逻辑；**`viewportdebug` 编译标签控制前后端是否启用，稳定默认关闭，beta 开启**。
- `internal/ui/static/framework.js` 仅作遮蔽关系与 `window.state` 来源的只读审计；本期不删除其中的死代码、不改其运行时。per-id 锁、selection revision 和动作模型状态均放 `index.html` 模块级，不挂回 framework 的失效选择/refresh 路径。
- 主 UI 测试：`internal/ui/ui_test.go`、`internal/ui/terminal_status_test.go`、`internal/ui/testdata/*.test.mjs`。
- Portal：`internal/portal/dashboard.html`、`internal/portal/portal_test.go`（或新增 `dashboard_test.go`）、生成的 `internal/portal/csp_gen.go`，可新增 `internal/portal/testdata/*.test.mjs`。
- CI：若新增 Portal Node 测试，同步 `.github/workflows/go-ci.yml` 的直接 Node 门禁。
- 设计/文档：本目录、根目录 `ui/` 下的 `sidebar-states.pen`、PNG 与 `preview-responsive.html`、`ui/quick-keys/`（仅高度契约影响审查）、`docs/PRD.md`、`CHANGELOG.md`。`docs/plans/feature-v0.6.0-*` 仅作历史记录，不再回写。

### 1.2 非目标

- 不复制 Gmail 配色、字体、品牌图标、搜索框、阅读窗格或导航 rail；不做全面 Material Design 改造。
- 不改侧栏既有断点、持久化 key、`Ctrl/Cmd+B`、抽屉与 git accordion 的正交状态。
- 除用户明确要求的**临时 A0 几何诊断报告接口**外，不改后端 API、WS/HTTP 协议、实例状态枚举、认证/CSRF 语义；诊断接口复用现有认证且只接受严格白名单数据。UI 动作白名单可以比服务端更严，但不得把服务端现有检查描述成等价的全状态白名单，后端缺口单独立档而不在本期修补。
- 不引入 `interactive-widget=resizes-content`、UI 框架或新的运行时依赖。
- 不处理 iframe 内部页面自身的响应式；主 UI 只保证容器尺寸正确。
- 不改 IME 桥、终端触摸滚动、quick-keys 字节/剪贴板语义和 xterm bundle；只验证新壳层下不回归。
- 不重写 Portal 认证、Cookie、直连 URL、代理、5 秒轮询、网络错误或空态产品文案；原生 form/防重复只用于保证一次提交。
- 不给 drawer/顶栏加位移动画，避免在连续 ResizeObserver/fit 中放大 PTY resize 风暴。
- 不在主 UI 与 Portal 两个独立静态页之间强行共享运行时 JS/CSS；只共享测量口径、判定契约和测试方法。

---

## 2. 已核实事实与不能预设的根因

### 2.1 当前源码事实

| 事实 | 符号锚点 | 对计划的约束 |
| --- | --- | --- |
| `body, html` 没有主题背景；`#app` 也没有 `background` | CSS `body, html`、`#app` | issue 中“`#app` 背景是 `--bg-color`”与当前源码不符；白带来源不能只凭颜色断定 |
| `#app` 同时消费四向 safe-area padding；实际着色在 `#sidebar` / `#main` / `#workspace` | `#app`、`#sidebar`、`#main`、`#workspace` | 即使外壳底边已对齐，透明 padding 或面板余量仍可能露白画布 |
| 高度由 `100vh` fallback + `100dvh` 与条件式 inline 钉高共同拥有 | `#app`、`@supports (height: 100dvh)`、`applyVisualViewportHeight` | “未命中即清除 inline 交还 dvh”是独立分支，必须分别测量 |
| 当前钉高条件是 `vv.offsetTop + vv.height < documentElement.clientHeight - 1`，否则清除 | `applyVisualViewportHeight` | 不能证明静态残差和浮动栏来自同一路径 |
| 既有测试主要断言“设置/清除 inline height”，没有真实浏览器矩形等式 | `internal/ui/testdata/viewport_height.test.mjs` | 新测试必须覆盖几何目标与状态分类，不能只把旧布尔断言换个名字 |
| `#main` / `#workspace` / `#terminal-container` 的纵向 flex 约束不完全显式 | 同名 CSS 规则 | 需审计 `min-height:0`、flex basis 与 `height:100%` 的叠加，避免壳高正确但子层溢出 |
| 顶栏 40px，coarse 44px 控件靠 `top:3px` 和 z-index 向工作区溢出；三个实例动作只按“有无活动实例”整组显示 | `--header-height`、`#header .icon-btn`、`renderWorkspace` | 顶栏需要真实高度与状态化派生，不再叠加补丁 |
| 实例已有 live/pending/terminal 三桶；Restart 只拒绝 running/starting，Delete 只查 running map，Stop 对未知 id 返回 nil；这些并不等价于 UI 的全状态白名单 | `INSTANCE_*_STATUSES`、`Manager.Restart/Delete/Stop` | UI 按明确白名单收紧；starting 保留 Stop 可取消启动，unknown 不获得破坏性动作；后端缺口单列，不在本期改 |
| `refresh()` 会复用 `refreshInFlight`，Stop/Restart 的 await 后还有 active selection 副作用 | `refresh`、`stopInstance`、`restartInstance` | 状态化按钮必须包含 post-mutation refresh、per-id 锁和捕获 id 的选择/副作用守卫 |
| `framework.js` 先于 inline 块加载；其 `window.refresh/selectInstance/selectWorktree` 被 inline 同名函数遮蔽，`_initFramework` 则没有同名遮蔽但全仓库无调用；`_renderLegacy/_selectInstanceLegacyFallback` 只被这些失效路径引用，头部 Owns 注释已过时 | `framework.js`、index.html script 顺序、inline `refresh/selectInstance/selectWorktree/init` | 实际改动落点是 index.html；framework.js 只读审计，不在本期顺手删死代码；新增模块状态不依赖失效路径，既有 shadowing 测试必须继续绿 |
| 新建实例 `+` 是 `renderTabs()` 生成的滚动首子元素 | `renderTabs`、`.new-tab-btn` | 固定主操作应移出滚动序列，不能继续依赖吸附特例保证可达 |
| 纯触屏下 `.wt-actions` 被 `pointer-events:none` 禁用 | `.wt-actions`、`@media (hover:none)` | #100 追加应恢复“可见且可点”，而不是恢复不可见命中区 |
| Portal 是 5 列表格、`100vh`、无宽度断点，登录浮层无可见区补偿 | `internal/portal/dashboard.html` | #118 可独立完整落地 |
| Portal 静态 `<table style="display:none">` 与 JS `.style.display` 混用；当前 CSP 的 `style-src 'self' <hash>` 无 `unsafe-inline` / `unsafe-hashes`，按规则会忽略该静态 style 属性并产生违规，首屏表头可能因此露出 | `dashboard.html`、`handleDashboard`、`gen.go` | 这是现状缺陷而非仅未来风险：PR3 先记录改前控制台/首屏基线，再改 class/hidden；以“首屏无表头且无 CSP 违规”作旧实现失败证明 |

### 2.2 仍属假设、不得直接写成修复结论

- dvh 是否在某 iPadOS 版本少报约一行；
- `visualViewport.height/offsetTop` 是否也与截图像素所见的真实可见区同时短报或错报；
- 静态白带发生在 inline 钉高路径还是 dvh 路径；
- 妙控键盘浮动栏改变的是 `visualViewport`、dvh，还是二者；
- 浮动栏在 WebKit 暴露的几何中应如何与软键盘硬遮挡区分；
- 白带是在 `#app` 外、`#app` 的 safe-area padding 内、workspace/host 内，还是 xterm 行取整余量。

**这些假设只能由 A0 真机数据关闭。**

---

## 3. A0：真机测量门禁（任何高度算法修改之前）

### 3.1 探针与证据产出

已实现 opt-in 的页面内诊断通道，**以 `viewportdebug` 编译标签选择是否编入前端资产与后端报告接口**（稳定默认关闭，beta 与本地诊断构建使用 `-tags viewportdebug`，可同时带 `ttymetrics`）：`?viewport-debug=1` 显示浮层并自动生成/复用 `debug-session`。iPad 上传严格 schema 的快照到 `POST /api/viewport-debug/report`，诊断者可读取 `GET /api/viewport-debug/report?session=<id>&after=<seq>` 获取增量；不需要 Safari Web Inspector。浮层**默认每 1 秒自动连续采集（`periodic`）**，可 Start/Stop continuous；复用已有 `debug-session` 时先读取 `latestSeq` 并续号，刷新不会从 1 重新开始。同时支持手动 Capture、妙控浮动栏/软键盘标记、暂停和离线复制 JSON。按钮使用 `pointerdown + preventDefault`，避免诊断点击迁移终端焦点、隐藏软键盘或改变妙控浮动窗状态。

接口复用现有 token/cookie 认证与 same-origin 检查，响应 `Cache-Control:no-store`；正文 ≤64KiB，最多 8 个会话、每会话 120 个事件、30 分钟 TTL，会话隔离且过期即不可读。上传单槽合并、断线重试，不形成并发风暴。**没有高度算法或现有实例协议变更**。每次 `visualViewport.resize` / `scroll`、`window.resize`、焦点变化、方向变化后记录双 rAF 快照，并补 250ms `settle` 稳定快照。

每个快照至少包含：

- 有限设备信息：`navigator.userAgent` / `platform`、`maxTouchPoints`、屏幕方向/宽高、coarse；不采集地址，iPadOS 精确版本以 UA 并结合用户补充确认；
- `innerWidth/innerHeight`、`documentElement.clientWidth/clientHeight`；
- `vv.width/height/offsetTop/offsetLeft/scale`、原始 `vvBottom = vv.offsetTop + vv.height`；
- `#app` inline/computed height、`getBoundingClientRect()`、computed padding 和背景；
- `#main`、`#workspace`、活动 terminal host、活动 iframe panel 的 rect/computed height；
- `.xterm-screen`、`.xterm-rows` 的底边、当前 rows/cols（只取几何，不取内容）；
- `html/body/#app/#main/#workspace/host` 的背景链；
- **浮层可达性契约（A0 实测缺陷后新增）**：主按钮（`Magic bar` / `Soft keyboard` / `Capture` / `Move` / `More`）必须排在明细 JSON **之前**，面板高度按 `visualViewport` 实时夹紧（`panelLayoutFor` 纯函数 + `applyPanelLayout`），可视区变矮自动折叠次要控件；靠右居位时为 `#terminal-controls` 预留宽度；可拖拽、`Move` 循环四角、`?viewport-debug-pos=tl|tr|br|bl` 与 sessionStorage 记忆。理由：原实现固定 `72vh` 且按钮排在 JSON 之后，iPad 软键盘弹出时 `Soft keyboard` 与 quick-keys 入口**全部按不到**，直接导致 `keyboard-hint` 与 quick-keys 同场两项采不到。验收见 `evidence/panel-check.mjs`（vv 高 702/633.5/545/495.9/274 五档主按钮命中自身）与 `evidence/panel-occlusion.mjs`（四角 quick-keys 入口可命中）。
- **首项快检**：`getComputedStyle(#app).paddingBottom` 与白带 CSS 像素高度是否相近；并用截图物理像素 / `devicePixelRatio` 对照 `vv.height`、`100dvh` computed height，显式判定二者是否同时短报；
- 白带位置的 `document.elementFromPoint()` 结果及截图，判断它落在外壳外、padding 内还是终端余量内；
- 焦点宿主类别（xterm helper textarea、普通 input、quick-keys textarea、其他），不记录具体文本；
- 当前侧栏 collapsed、quick-keys open、活动实例 kind/status，便于复现。

结果回填 `docs/plans/fix-issue116/DATA-COLLECTION.md`，每个场景保留原始值、截图和派生差值，不只写“正常/异常”。

### 3.2 必采矩阵

| 场景 | 必做状态 | 要回答的问题 |
| --- | --- | --- |
| 静止基线 | Safari URL 栏显示/收起、暗色/浅色 | dvh、vv、clientHeight 谁与真实可见底边一致；白带属于哪一层 |
| 软键盘 | 终端聚焦、普通输入框、quick-keys paste textarea；弹出/收起 | 硬遮挡的 vv/焦点/事件顺序特征及稳定高度 |
| 妙控键盘 | 浮动提示/输入栏出现/消失，实体键盘输入，终端焦点保持 | 是否误缩、浮动栏与软键盘的可区分信号 |
| 方向与窗口 | 横/竖屏、iPad Split View 1/3、1/2、2/3、可调窗口 | 旧基线何时必须失效，避免把方向变化误当浮层 |
| 缩放与平移 | 双指缩放、双击缩放、scale 回 1、vv pan | 保持 zoom 豁免，不把缩放矩形当键盘 |
| safe-area | Home Indicator、刘海横屏、drawer 开/关 | 外壳全出血与内容内缩分别应落在哪个底边 |
| 子层传导 | pty、opencode/reasonix/dsh iframe、空状态、quick-keys 打开 | 壳高变化是否完整传到底层，是否出现 fit/ResizeObserver 循环 |
| 生命周期 | 前后台切换、pageshow、键盘已打开时刷新 | 首屏与恢复路径是否使用陈旧基线 |

### 3.3 根因判定树与允许采用的修复路线

| 测量结果 | 根因归类 | 修复路线 |
| --- | --- | --- |
| `#app` 未钉高，inline 为空，app 底边短于真实可见底边 | dvh/基准路径 | 以实测可靠的 visible rect 建立连续基准；CSS dvh/vh 只作无 API/豁免路径回退 |
| 已按 vv 钉高仍偏短或位置错误 | 钉高坐标/状态路径 | 修正坐标、offset、基线或状态机；真机 seq 305–312 已证明滚动时直接写 `visibleBottom` 会漏掉 `app.top`，目标高度应为 `visibleBottom - app.top` |
| vv 与 dvh 相对截图像素同时短报/错报 | 两个候选 API 都不可靠 | 再比较 innerHeight、clientHeight、方向变更前的稳定基线等独立来源；**本计划不接受常量偏移作为关闭路径**。仍无可靠基准时 H1 阻塞，保留最安全回退并记录设备证据，不宣称 ≤1px 修复 |
| app 外壳底边正确，但白带落在 safe-area padding/根画布 | 透明画布/铺底 | 根画布与出血背景修复；不修改高度算法 |
| app 正确但 workspace/host 未跟随 | flex/min-size/resize 传导 | 修 flex 约束及 refit 时序；不把 app 再拉高补偿子层 |
| host 正确，仅 xterm 最后一行上有小于行高的余量 | xterm fit 取整 | 保持同主题余量并验证行数；不为凑像素把壳多出一行 |
| 妙控浮动栏使 vv/dvh 变小，但内容在浮层下仍应布局 | 浮层误当硬遮挡 | 进入 §4.2 的分类/稳定基线路线；若无法可靠分类，启用稳定壳 + 局部避让备选 |
| **（已实测坐实）妙控浮动栏出现时 vv 与 innerHeight 完全相等、`inline` 为空** | 浮动栏是纯覆盖层，不进 visualViewport/dvh | **不需要**为浮动栏做“少扣一点”的补偿分支；只需保证准入守卫不误触发（`visibleBottom < clientHeight-1` 或 `abs(visibleBottom-app.bottom) > 1`），它天然不吃工作区。证据：`magic-hint` seq388 与前后 seq385–394 全程 `vv.height=702=innerHeight` |
| **（已实测坐实）钉高态底边误差全为 0，误差只出现在未钉高的准入判定** | 守卫漏判，不是钉高公式错 | 修准入守卫（补 `abs(visibleBottom - app.bottom) > 1`，并在 `app.top ≠ 0` 时用 `visibleBottom - app.top`）；**不得**为“看起来更稳”而重写已验证正确的钉高路径。证据：第二轮 483 个钉高事件误差 0.00；seq190 未钉高错 44px、上轮 seq624/736 错 340/428px |
| **（已实测坐实）`#app paddingBottom` 出现 20/10/0px 三值** | safe-area inset 不是常量 | 铺底与几何一律读 `env(safe-area-inset-*)`，**禁止写死 20px**（含 quick-keys、drawer、Portal 浮层） |
| **（新增待拍板）scale≠1 时 `#app` 按 `100vh` 铺到 936 而可见底 844.7，溢出 91.3 layout px** | zoom 豁免的固有代价 | 保持 zoom 豁免则接受末行不可见并记入 §10；若不可接受，须为缩放态建立独立可见基准。**不接受常量偏移** |

**第二轮（会话 `b90643bea91477cd5d816445362604fd`）已关闭的分支**：妙控浮动栏分类、Split View（`innerWidth` 592/392/618）、opencode-web iframe 子层、scale=1 静止基线补采、safe-area 非常量。**仍未关闭**：`keyboard-hint` 与缩减量命名、quick-keys 同场、键盘打开时刷新、Stage Manager、dsh/reasonix、竖屏静止基线、iPhone/Chrome(iOS)。归类与判读工具：`evidence/analyze-a0.mjs` → `evidence/CLUSTER-b90643be.md`。

**A0 关闭条件**：至少一台能复现 #116 的 iPad 完成静止、软键盘、妙控浮动栏、横竖屏和缩放矩阵。归因按**高度分量的可加性**进行：静止白带与妙控浮层增量可以是 safe-area/画布与 VV 政策两个叠加根因；每个独立增量都必须可量化、可落入上表分支并有可重放 fixture，再做组合回归。不能强迫整个现象只有一个根因。没有此证据，不能签字“#116 已修复”。

---

## 4. 高度与应用壳技术设计

### 4.1 三条独立修复线

1. **几何线**：只负责外壳与可见区。若 A0 证明需要改几何，则建立唯一的运行时高度决策者，删除“dvh 与 inline 条件式互相接管”的隐式不确定性；若现象全部来自铺底/子层，明确记录“高度算法无需修改”，不为执行方案而强行重写。
2. **铺底线**：为 `html/body/#app` 建立符合当前主题的背景链；drawer、main、workspace 的全出血背景与内容 safe-area padding 分离。根背景是白缝兜底，不参与高度计算。
3. **传导线**：审计并明确 `#main → #workspace → terminal host / web panels` 的 `min-height:0`、flex basis、overflow 与 `height:100%` 组合；`#header` 不参与纵向 shrink。布局稳定后沿用 `ResizeObserver` / `refitActiveTerminal()` 重排，不新增第二套 resize 发送链。`kinds/pty.js` 与 index.html legacy 两处工厂必须一起核对；现有 RO 的 100ms 发送门不是 trailing debounce，若实测快速变化后最终尺寸未发送，应补稳定后的 trailing 发送，并验证 `VV → RO → fit → sendResize → appliedTTYSize` 最终一致。FitAddon 的 `parseInt/floor` 亚行余量是正常取整，不属于外壳缺高。

### 4.2 visualViewport 决策器

实现形态建议收敛为可切片测试的纯函数，例如 `resolveViewportLayout(snapshot, previousState)`，输出目标模式、目标高度、原因和需要失效的旧基线；DOM 函数只负责采样、写样式和调度。

必须遵守：

- **基准与键盘补偿分离**：无硬键盘时维护正常窗口基准；确认硬遮挡（软键盘）后才施加补偿。**妙控浮动栏已按产品决定出范围（不补偿、不分类）**：iOS 允许用户把该浮窗拖到屏幕任意边角，属系统级可移动浮层，壳不得追逐它。
- **阈值已标定（原「不预写阈值」条款已满足）**：判据 `vv.height <= clientHeight × (1 − 25%)`，比例而非 px 常量。两实测锚点：软键盘 covered **428px = 60.9%**（iPad Pro 11 横屏 layout 702；iPadOS 27.2 重测逐位复现）＝硬遮挡；妙控浮窗 covered **68.5px = 9.8%**＝软遮挡。25% 落在两锚点之间并留有余量（iPhone 竖屏键盘 ≥40%，仍在硬侧）。焦点存在本身仍不是充分条件。
- **区间重叠不能硬分类**：若浮动栏与软键盘在可用信号上不可可靠区分，采用“稳定全高壳 + 终端聚焦区域局部平移/滚动避让”的备选，而不是发布一个会在两种状态间来回猜测的阈值。
- **【已被实测推翻并重写】pan 彻底退出判断与目标（产品选 A）**：旧条款要求用 `app.getBoundingClientRect().top` 算 `targetHeight = visibleBottom - app.top`、并以 `abs(visibleBottom - app.bottom) > 1` 作准入——`visibleBottom` 含 `offsetTop`，于是**同一个量同时驱动判据与目标**。真机实测（会话 `ios272recal…`，键盘全程打开、`vv.height` 恒 274、`clientHeight` 恒 702）：pan 12.5→237 时钉高被拉到 286.5→511px（seq297–310）；pan=265.5 时 covered 跌至 23.1% 穿透阈值，**键盘还开着就把钉高清掉**（seq311）；pan 回落时 covered 涨回 428px=61% 也不重新钉（seq362–396），因「scroll 不新建钉高」门禁拒绝复挂；整场仅靠 seq328 一次 `resize` 救回。净效果＝键盘无限期压住终端底部约 428px。**现行规则**：判据只读 `vv.height`，目标取 `vv.height`，`offsetTop` 不进入任何一侧；existing-pin 门禁删除；后果为产品选定——键盘打开时终端行数变少但**一行都不被遮**，竖向浏览由终端自身滚动条承担。锚点已禁止 `offsetTop`/`visibleBottom`/`covered` 再现于该函数代码（`stripJSComments` 剥注释后扫描）。新平台事实：27.2 拖拽期间 `window.innerHeight` 702→274 而 `documentElement.clientHeight` 恒 702，两源在手势中不一致，哨兵锁死后者。
- **保留 zoom 豁免**：`vv.scale ≠ 1` 不把缩放后的可见矩形当作键盘；回 1 后重新采样。纯 pan 不得自行进入键盘补偿（本轮加强为 pan 完全不参与）。实测：27.2 下 `scale=0.75` 时页面永不压缩正是此守卫所致，非系统变更。
- **基线失效明确**：方向、实际窗口尺寸、Split View 变化和 pageshow 必须重置旧基线；URL 栏变化不能被误冻结。
- **事件合并**：resize/scroll/focus/orientation 变化用 rAF 合并，写高度后在下一布局帧复核；不得形成 `height → ResizeObserver → fit → height` 循环。
- **无 visualViewport 降级**：保留 `100vh` fallback + `100dvh` 渐进增强；无 API 时不注册无效监听、不写 inline 高度。
- **可诊断**：debug 模式记录分类原因，不记录内容；生产默认无日志刷屏。

### 4.3 完成不变量

- 外壳：`abs(appRect.bottom - expectedVisibleBottom) ≤ 1 CSS px`。这里 `expectedVisibleBottom` 是经硬键盘/浮层分类后的产品意图底边，**不机械等于所有场景下的原始 vvBottom**。
- 内容：workspace/host 底边与 app 底边、computed safe-area padding 的关系符合设计；不能以外壳正确掩盖子层缺高。
- 终端：输入行和最后一行在硬键盘上方可见；xterm 的亚行取整余量只显示主题色，不出现默认白画布。
- 浮动栏：出现/消失不多扣工作区；软键盘：弹出/收起仍正确避让；两者不得共享一个未分类的“只要变小就缩”规则。

---

## 5. Gmail 式主 UI 响应式与动作状态设计

### 5.1 Gmail 参考的落地映射

| 参考行为 | myworktree 落地 | 明确不做 |
| --- | --- | --- |
| 应用是稳定壳，文档本身不滚，导航与内容各自滚动 | 固定根壳，明确 `#wt-list`、git panels、tabs、workspace、dialog 的滚动归属 | 不让终端或整页靠 body 滚动救布局 |
| 宽屏导航并排，窄屏抽屉按需覆盖 | 保留现有三档侧栏和 drawer/mask；统一全出血背景与安全区 | 不新增 rail，不改持久化状态机 |
| 顶栏按内容优先级保持操作可达 | 新建实例入口固定；tabs 独立滚动并吸收宽度压力；当前状态最多两个实例动作在所有宽度完整内联 | 不让 Restart/Stop/Delete 把 tabs 和 `+` 挤没，也不把 stopped 的 Delete 藏进菜单 |
| 触屏目标由布局真实容纳 | coarse 顶栏采用能容纳 44px 控件的高度（设计初值 52px，设计稿确认），sidebar title 同步；移除 `top:3px` 透明溢出补丁 | 不把 44px 写成 44pt，不靠不可见重叠区冒充可用性 |
| 列表在窄屏重排信息而不是硬挤列 | Portal 使用同一列表语义的桌面网格/移动卡片 | 不维护桌面表格与移动卡片两份数据 DOM |

**对 Gmail 参考的一处有意删减**：本期**不采用工具栏动作溢出**，因为状态化后当前同时可见动作上限为 2，继续隐藏 Restart/Delete 只会损害可达性并违反用户“stopped 只显示 Restart + Delete”的要求；弹性让位由 tabs 内滚承担，而不是虚构一个三动作菜单场景。

侧栏仍使用现有无缝媒体互补写法；`768.5px` 等分数宽度不得出现功能落空。顶栏在 `≤1024px` 使用紧凑内联只是**工具栏内容分档**，不改变侧栏 `768px` 分派，也不新增侧栏状态。

### 5.2 顶栏结构与滚动纪律

建议结构仍为 `#main` 内顶栏，层级不提升为压住侧栏的全宽顶栏：

1. 侧栏开关；
2. **固定的** New Instance `<button>`；
3. 唯一 `#tabs-container` 横向滚动区；
4. 同一 `#instance-actions` 内联按钮组，不增加 More/overflow 开关或第二动作 DOM。

要求：

- 把 `+` 从 `renderTabs()` 的滚动 HTML 中移到稳定宿主；无可用 worktree 时沿用当前不可用/隐藏语义。tabs 重绘不得重建或移动该按钮。
- tabs 的 scroll-snap **只保留在既有 `≤768px` 媒体块内**，不得扩展成桌面全局吸附；`+` 移出后删除仅为它存在的 snap 特例及旧测试。活动实例变化时保证活动 tab 可见，但 2 秒轮询不得无故重置用户横向滚动位置。
- `#instance-actions` 的三个按钮始终只有一份，由状态模型决定谁显示。当前任何状态最多两个可见动作，**在 320px、390px、768px、820px、1024px 及宽屏都全部内联**；尤其 stopped/failed/exited 的 Restart + Delete 必须同时直接可见，Delete 不得进入 overflow。
- `≤1024px` 的动作按钮改为**图标形态 + 原动作 `aria-label/title`**，coarse 每个 44×44，语义不变；紧凑块内把 `#instance-actions` 的 **gap 从当前 8px 改为 4px**，移除其左侧 margin；`+` 与动作组 `flex-shrink:0`，组内图标按钮 `flex:0 0 44px`，`#tabs-container { min-width:0; }` 吸收剩余宽度。外层 `#header` 既有 4px gap 不改。320px coarse 预算为：`320 − 16（header padding）− 44（侧栏开关）− 44（新建）− 92（两个44动作+4 gap）− 12（三个4px外层gap）= 112px` 留给 tabs；活动 tab 在该区域内 `max-width:100%`、文本省略并可滚动到完整框内，不要求完整显示长名称。桌面宽屏保留文本按钮。本期不创建 More 按钮、popover/menu、`role=menu` 或 disclosure 状态；若未来出现三个同时可见的动作，必须另开设计与需求评审，不在本期预设。
- coarse 顶栏真实容纳 44px 控件后，重算 drawer 打开态右缘 tab 的纵向对齐，使用共享尺寸 token，淘汰手调 `6px/3px` 堆叠补丁。

### 5.3 触屏与 worktree 动作

- coarse 下主 UI 的 button、tab、新建实例、worktree 动作、modal close 和表单控件以 `min-width/min-height` 保证 ≥44×44 CSS px；dialog 以自身滚动容纳增高后的内容。快速键已有 44px 规则保持。
- 不再用透明区域向 `#workspace` 溢出来满足命中尺寸；可见盒、命中盒与视口裁剪一致。
- `.wt-actions` 在 `hover:none` 下恢复**可见且可点**；桌面 hover 显示保持，键盘 `:focus-visible` / `:focus-within` 也能显形。删除现有“纯触屏禁用动作”的契约与测试。
- 将 worktree 的 Open Terminal / Open Finder / 激活行删除等 `span` 动作改为语义化 `button`，补可访问名称；所有动作 handler 保留 `stopPropagation`，点/双击动作不得切行或进入 rename。Delete 继续只在 `isActive` 的普通 worktree 渲染，主仓库不渲染 Delete。
- **结构迁移**：`.wt-row-actions` 成为主仓库与普通 worktree 共用的唯一外层动作行，两处模板都只使用 class、删除 wrapper 与动作的 inline 布局；`.wt-actions` 仅保留为 Open Terminal / Open Finder 的嵌套逻辑分组，不再形成第二条独立动作行。必须同时新增外层 CSS 规则，**只写 `display:contents` 不会让外层换行**：

  ```css
  /* fine：保持当前分支行、密度和 trailing 对齐 */
  .wt-row-actions { margin-left: auto; display: flex; gap: 4px; align-items: center; }

  @media (pointer: coarse) {
      /* 外层本身必须独占一行，内层 display:contents 只负责展开按钮 */
      .wt-branch { flex-wrap: wrap; }
      .wt-row-actions {
          flex: 1 0 100%;
          margin-left: 0;
          min-width: 0;
          justify-content: flex-end;
      }
      .wt-actions { display: contents; opacity: 1; pointer-events: auto; }
  }
  ```

  fine 不换行并沿用 `.wt-actions` 的 hover/focus 显形；coarse 由 `.wt-branch { flex-wrap:wrap }` + 外层 `flex:1 0 100%` 形成独立行，不再移动 DOM。`startWTRename` 的 `event.target.closest('.wt-row-actions')` 守卫覆盖全部嵌套动作，必须保留并补行为用例。主仓库 GitHub 链接留在 `.wt-name`，单独满足 44px，不计入动作行。
- **主仓库名称行也有明确预算**：`.wt-name-text` 继续 `flex:1; min-width:0`，GitHub link 在 coarse 下 `flex:0 0 44px` 且 44×44，既有 6px gap 不变。按同一内容宽度计算，200px 档名称文本预算约 `172−44−6=122px`，240px 档约 `212−44−6=162px`。D0-1 必须放入长主仓库名 + GitHub 的 200/240px 对照，实测名称与链接 rect 不重叠、链接不收缩，名称可通过换行/省略保持可读，不得只画普通 worktree。
- **coarse 使用独立动作行，不与名称/分支抢同一行**：名称、分支各据内容行并继续 `min-width:0` / 省略，Open Terminal / Open Finder / 激活 Delete 占独立行。按当前 `200px` 中间档、24px 横向 padding、3px 左标识与 1px 右边框计算，内容预算为 **172px**；最多 `3×44 + 2×4 = 140px`，可完整容纳，且文本不占用这 140px。D0-1 必须以真实 DOM 复测 200/240px 两档的 `getBoundingClientRect()`，不能只写“省略即可”。
- **local/remote 动作数分开**：`shortcuts()` 在 remote 返回空串。local 普通行非 active 为 Terminal+Finder（92px）、active 再加 Delete（140px），主仓库为 Terminal+Finder；remote 普通非 active 无动作、active 仅 Delete（44px），主仓库无本地动作。GitHub 若存在始终只在名称行、不计入这些数量。渲染时先计算动作集合，**只有非空才生成 `.wt-row-actions`**，同时覆盖 remote 空外层，不得先渲染空容器再靠 CSS 隐藏。
- **明确接受 coarse 密度下降**：独立动作行使每个有动作的 worktree 项增加约 44px 高度，fine 桌面密度不变；无动作时不渲染 `.wt-row-actions`，不得留下 44px 空行。D0-1 增加 320×568、390×844、820×1180 下 0/1/多 worktree 的一屏完整行数、`#wt-list` 可视高度/scrollHeight 与滚到最后一项的截图，确认 Staged/Unstaged 仍可达，并把“以滚动换可达性”写为接受后果。
- 保留 Pointer Events 拖拽和 `touch-action` 既有机制；4px 拖拽条的视觉/命中区加宽不在本期扩张。

### 5.4 实例动作状态矩阵（用户追加）

可见性由活动实例的**最新状态**纯函数派生，例如 `getInstanceActionModel(inst, inFlight)`；`renderWorkspace()` 每次调用统一刷新，所有宽度的同一内联按钮组消费同一个 model 和 dispatcher。按钮 handler 执行前再次核对实例 id、最新状态与 per-id in-flight 守卫。

| 当前条件 | Stop | Restart | Delete | 其他表现 |
| --- | --- | --- | --- | --- |
| `running` | **显示** | 隐藏 | 隐藏 | 严格满足“运行状态只显示 Stop” |
| `unhealthy`（进程仍 live） | **显示** | 隐藏 | 隐藏 | 沿用现有 live 桶，不因健康探针失败误给 Restart |
| `starting` | **显示** | 隐藏 | 隐藏 | Stop 作为“取消启动”能力，避免 pending 全禁造成能力回退；显示 `Starting…` |
| `stopping` | 隐藏 | 隐藏 | 隐藏 | 显示非按钮的 `Stopping…`，防止重复变更 |
| `stopped` | 隐藏 | **显示** | **显示** | 严格满足“停止状态只显示 Restart + Delete” |
| `failed` / `exited` | 隐藏 | **显示** | **显示** | 与既有 terminal 桶一致 |
| 无活动实例、找不到实例、未识别状态 | 隐藏 | 隐藏 | 隐藏 | 安全失败；不给未知对象提供变更操作 |

附加契约：

- Stop 只允许 live 或 `starting`；Restart/Delete 只对 `stopped/failed/exited` 白名单开放。现有 `isInstanceTerminalStatus()` 把 unknown 也归入 terminal 是为了传输安全收口，**不能直接复用为破坏性动作授权**。
- in-flight 使用独立的 **per-id token/锁**，不能挂在会被 poll 整体替换的 `inst` 对象上。同一 id 的 Stop/Restart/Delete 互斥，点击即设、`finally` 释放；poll render 不得提前重新启用。不同 id 的锁互不串扰。in-flight 只负责 disabled/busy，不作为第二套可见性真源。
- handler 入口按最新 state 再验动作，覆盖 rename 跳过 full render 造成的短暂陈旧按钮；stale click、双击、轮询交错不得发出与最新状态矛盾的请求。confirm cancel 不取锁、不发请求；API 失败必须解锁并保留反馈。既有 API endpoint 和服务端校验不变。
- `refresh()` 的 `refreshInFlight` 去重可能让动作后的 `await refresh()` 只等到一个**动作前已发出的 poll**。必须提供 `refreshAfterMutation` 或等价机制：等待旧请求后排一次真正的 post-mutation refresh/reconcile，不能把“await 过 refresh”当作新状态已到达的证明。
- Stop 的后续副作用按点击时捕获的 `stoppedId` 定位会话；不得 await 后向用户刚切换的 active terminal 写 `[Process Stopped]`。找不到同 id 会话则不写。
- Restart 使用**选择代次/版本**防 ABA：动作发起时同时捕获 old id 与 selection revision，实例或 worktree 选择变化都递增 revision；只有响应时 revision 未变、当前选择仍是 old id、且 post-mutation refresh 已能解析 new id 时才自动 `selectInstance(res.id)`。A→B→A 也算用户已切换，不得跳到 new id；Delete 的后续选择同样以捕获 id、revision 和最新 state 为准。
- 所有宽度读取同一个 model；`hidden` 必须有明确 CSS 消费规则，不能因 `.visible { display:flex }` 等规则意外重新显示。状态变化使当前焦点动作消失时，焦点移到仍可见的状态文本或活动 tab，不得留在隐藏按钮上。
- 这些时序修正只服务于状态化按钮的正确性，不重写 refresh、API 或实例生命周期。

---

## 6. Issue #118：独立 Portal 工作包

### 6.1 响应式列表

- viewport meta 补 `viewport-fit=cover`；body 保持文档语义的 `min-height:100vh`，再由后置 `@supports` 升级为 `100dvh`，不要改成主 UI 式固定 `height`。html/body 使用主题背景、`border-box` 和四向 safe-area；窄屏减小 body/card gutter，`.content-card` 补 `width:100%; min-width:0`。
- Portal 是列表文档，**不照搬主 UI 的 `overflow:hidden`、全局非 passive `touchmove preventDefault` 或 `touch-action:none`**。长列表必须能滚动；根 `overscroll-behavior` 只负责抑制链式回弹，浮层自身 `overscroll-behavior:contain`，不能把实例滚没。
- 将当前表格收敛为**一份有序列表/列表项语义的数据 DOM**：桌面用五列网格呈现 Repository / Port / Path / Status / Link，`≤640px` 重排为卡片，`641px` 回到五列。不要同时渲染 table + cards 两份数据，也避免用 table display 改造破坏可访问性。
- 窄屏卡片信息优先级：Repository + Status 首行，Path 独立行，Port 与 Open 位于动作行；长仓库名、长绝对路径、CJK、五位端口、无 Open 的 dead 项均不造成页面横向溢出或空白操作块。
- **Path 在所有宽度都使用 `min-width:0; overflow-wrap:anywhere` 多行完整换行**，桌面五列中的 Path track 使用可收缩的 `minmax(0, …)`，其他 track 不得按内容强制撑宽；不以单行 ellipsis + `title` 作为唯一查看手段（触屏无 hover）。用 200 字符连续路径在 320/390/640/641/820/1440px 验证 `scrollWidth ≤ clientWidth`，641px 也允许行高随完整路径增长。
- `renderList()` 继续以 `textContent` 填充服务端数据；不引入**数据驱动的 innerHTML**。既有 `error-msg.innerHTML` 只含源码常量 SVG + `Invalid token`，可原样保留并加注释/测试证明不含用户或服务端文本；其他新图标优先 CSS/textContent。loading、现有空列表结果、login-required 与列表显隐统一，但不新增网络错误/空态产品文案。
- 先记录改前基线：CSP 控制台已有静态 style 违规、首屏表头是否因被拦截而露出；再移除静态 `style="display:none"` 和 JS `.style.display` 切换，使用 `hidden`/class，并为 `[hidden]` 提供不会被 display 覆盖的消费规则。修复后首屏不得出现空表头、控制台不得再有对应违规；内联 `display:table` 不得压过移动布局，**不得添加 `unsafe-inline` 规避 CSP**。
- 链接、登录按钮、输入及其他操作在 coarse 下 ≥44×44 CSS px；所有 hover 只留在 `hover:hover`，补 `:focus-visible`。

### 6.2 登录浮层与软键盘

- 本小节依赖 A0 对硬遮挡/浮动系统 UI 的最终分类，**不得复制当前已有缺陷的 R6**。`#token-form` 使用自身 visible-rect 语义，主 UI 管 `#app`，Portal 只管浮层，不能改 body/content 的高度。
- 浮层有 `100vh` / `100dvh` 回退、safe-area padding、`overflow-y:auto` 和 `overscroll-behavior:contain`；用 auto margin 或等效结构做到“空间足够居中、空间不足从顶部可滚”，避免普通 flex 居中在短横屏裁顶。overlay 持 gutter，卡片不重复叠加 safe-area。
- 新增 Portal 本地 `setTokenFormVisible()`、`syncTokenFormViewport()`、`initPortalViewportHandling()`：401 显示、登录成功隐藏都走唯一显隐路径；启动接线先于首次 `fetchList()`。显示时同步几何，隐藏时清 top/height 临时值，下一次 401 不继承旧状态。
- 仅浮层 visible 且共享策略判定需要补偿、scale=1 时，按可见矩形设置自己的 `top≈vv.offsetTop`、`height≈vv.height`（或 A0 后的等效形式）；resize 完整判定，scroll 只跟随已补偿浮层，pinch/无补偿时清几何回 CSS。键盘或焦点变化后保证 `token-input` 与错误信息可经 `scrollIntoView` 到达，不自动记录 token 或强制唤起键盘。
- 将登录卡改为原生 `<form>` submit + submit button，submit handler 先 `preventDefault()` 再调用 `login()`，移除 `keypress`；普通 Enter 恰好提交一次，IME 合成 Enter（`isComposing` / keyCode 229）不得误提交。`login()` 增加 pending guard，并在 `finally` 中释放：快速双击只发起一轮 CSRF/auth，CSRF/auth reject 后仍可重试，但不改认证协议或新增网络错误文案。
- 增加 Portal 本地 `authEpoch`（或等价 stale-response 机制）：每次 `fetchList()` 捕获发起时 epoch，401 仅在它仍等于当前 epoch 时才可显示浮层；登录成功先推进 epoch 再隐藏并拉取新列表，因此登录前/登录中发出的旧 401 不得重开浮层。当前 epoch 的真实 401 仍必须显示，5 秒轮询与认证协议本身不改。
- 密码输入字号至少 **16px**，避免 iOS 聚焦自动放大；补 `autocapitalize=none`、`autocorrect=off`、`spellcheck=false`、label/error 关联、`role=alert` 或 aria-live、dialog 可访问名称。不得用 `user-scalable=no` 掩盖字号问题。

### 6.3 CSP、测试与 CI 闭环

- 每次 `dashboard.html` 变更后执行 `go generate ./internal/portal`，审查生成的 `csp_gen.go` 差异；不得手编 hash，生成/embed/CSP 组装生产逻辑预计不改。
- `gen.go` 与现有 hash 测试的正则都只识别**裸** `<script>` / `<style>`。本期保持恰好一个裸 script 块和一个裸 style 块，不给他们加属性、不新增 stylesheet `<link>`；新增独立 Go 结构守卫拒绝带属性的同类标签，防止生成器与测试因同一正则同时漏报而假绿。未来若确需属性，必须先单独扩展生成器和独立测试。
- 可新增 `internal/portal/dashboard_test.go`，或把用例留在 `portal_test.go`：从真实 served HTML/响应头断言语法锚点、hash 位于正确 directive 且无 `unsafe-inline`；增加 Node wrapper。现有 `TestCSPHashes_MatchesDashboardHTML` 继续保留。
- 新增 `internal/portal/testdata/dashboard_responsive.test.mjs` 时同步 `.github/workflows/go-ci.yml`，让直接 Node 门禁同时覆盖 `internal/portal/testdata/*.test.mjs`，不能只依赖会 skip 的 Go wrapper。
- 浏览器验收需检查控制台无 CSP style/script 违规；静态显隐、动态显隐和 CSSOM visible-rect 几何更新都包含，不以 hash 测试单独证明浏览器行为。
- Portal 与主 UI 只共享 A0 字段定义、最终遮挡分类和验收思路，不新增跨静态页共享脚本，避免改变 CSP 和资源部署模型。

---

## 7. `.pen` 设计稿与预览更新（UI 实施门禁）

### 7.1 主 UI：更新既有稿件

更新 `ui/sidebar-states.pen`，并重新导出对应 PNG；不得只改导出图而不改 `.pen` 源。

必须覆盖：

1. 桌面展开/折叠：running/unhealthy 仅 Stop，stopped/failed/exited 的 Restart + Delete 同时内联；
2. 769–1024 中间档：200px 侧栏、stopped 两动作以带可访问名称的图标完整内联且 tabs 可独立滚动；
3. ≤768 drawer 关闭/打开：固定 `+`、tabs、stopped 两图标动作完整内联、drawer 右缘按钮与安全区；
4. 390×844 和 320×568 coarse：52px 顶栏设计初值、44px 命中区、320px 时约 112px tabs 预算、无透明重叠补丁；
5. running、stopped、starting、stopping 四种顶栏状态对照，另列 failed/exited 与 stopped 同组，starting 明确 Stop=取消启动；
6. worktree 行动作在 touch 下可见、focus 下可见；统一 `.wt-row-actions` 外层与嵌套 `.wt-actions`，明确 fine 不换行、coarse `.wt-branch{flex-wrap:wrap}` + 外层 `flex:1 0 100%` 的真正独立行；标注 local/remote × active/inactive 的动作集合、active-only Delete、主仓库无 Delete、空集合不渲染；200/240px 两档标注 **172px 最小内容预算 / 三动作 140px**，另放长主仓库名 + GitHub 44px 的 **122/162px 名称预算**；补 320×568 与 820×1180 的密度、无动作无空行及滚到最后一项画面；
7. 软键盘打开、妙控浮动栏、Home Indicator/横屏安全区的几何标注；
8. quick-keys 打开与矮视口，确认新顶栏与终端浮层不互相遮挡；
9. `notes-decisions.png` 更新为本次状态矩阵、断点不变、全宽度动作内联与铺底/高度分离决策。

同步 `ui/preview-responsive.html` 作为交互预览；`.pen` 是设计源，preview 不能成为第二套漂移的设计真源。该 preview 没有完整 VV/真机系统 UI，不能作为 #116 高度证据。另需审查 `ui/quick-keys/quick-keys-panel.pen` 及软键盘场景 PNG：若新高度契约改变其 R6 假定，必须同步更新源稿与导出图；若无需改形态，也在稿件中注明依赖的新契约。

### 7.2 Portal：新增稿件

新增 `docs/plans/fix-issue116/design/portal-responsive.pen` 及导出 PNG，至少包含：

- 设计说明：640/641 归属、vh→dvh、安全区、44 CSS px、主 UI visibleBottom 与 Portal 浮层 visible rect 的差异；
- 桌面五列网格（1440×900，960px 内容卡）与 iPad 竖屏（820×1180）触屏对照；
- 320 / 640 / 641 三联：200 字符连续 repo/path、断点归属、Path 在卡片与五列中都完整多行换行、无横向溢出；
- 390×844 深色卡片与 844×390 横屏安全区/文档纵滚，含 dead/alive、Home Indicator；
- loading、现有空列表结果、login-required，不新增空态/网络错误文案；
- Token 默认、错误、focus 组件：16px input、44px coarse 目标、错误扩展；
- 手机竖屏软键盘登录：标出 layout viewport、visual viewport、键盘顶边与浮层 rect；
- 短横屏“键盘 + 错误”可滚动态：不裁顶、单滚动 owner、Login 最终可达；
- 妙控浮动栏若真机可构造则必画，否则只在说明帧记录契约，不伪造场景。

### 7.3 设计门禁

- **2026-10-28 设计门禁签署**：作者明确以稿件提交 `346ed62` 本身作为 D0-1 与 D0-2 的评审通过记录，本节下条"先评审再编码"及 PR1/PR2/PR3 的 UI 编码门禁视为**已解除**；上一条列举的全部产品决策（52px coarse 顶栏、图标内联与预算、`.wt-row-actions` 独立行、接受 +44px 密度、Portal 卡片化与 Path 全宽换行）一并按该稿件版本采纳。实施若偏离该版本稿件，仍须先回改 `.pen` 并记录原因。
- **2026-10-28 稿件交付状态**：D0-1 已更新主 UI `.pen`、16 张必需 PNG 与 preview，并完成 quick-keys 契约注记及两张受影响 PNG；D0-2 已新增 Portal `.pen` 与 10 张必需 PNG。frame id、pen 0.3.11 校验/导出记录、实际像素与 preview-only 冒烟结果见 `design/EXPORT-MANIFEST.md`。**当前仅完成稿件产出，设计评审尚未签字，不解除下述编码门禁。**
- 先评审 `.pen` 和 PNG，再开始 **PR1/PR2/PR3 的任何 UI 编码**；A0 探针、测量和纯数据分析可先行，但 PR1 的铺底、safe-area、flex/几何变更同样必须等待 D0-1，不得借高度 PR 绕过设计门禁。
- 设计评审特别确认：52px coarse 顶栏、≤1024 当前全部动作以可访问图标完整内联及 320px/112px tabs 预算、starting 可 Stop 取消而 stopping 无动作、统一 `.wt-row-actions` 的 fine 不换行 / coarse wrap+full-basis 独立行、200/240px 的 172px 动作预算与 122/162px 主仓库名称预算、local/remote 空集合不渲染、**接受 coarse 每动作项约 +44px 的密度下降并以滚动兜底**、Portal 单列表语义卡片化与全宽度 Path 换行。
- 实施若偏离稿件，先回改 `.pen` 并记录原因，再改代码；最终截图必须能回溯到同一版本设计源。

### 7.4 `.pen` 校验与 PNG 导出工具链

沿用 quick-keys 计划已验证的 `pen` CLI headless 路径，不用“能打开 JSON”代替 schema 校验，也不手工缩放截图。当前环境已用 **`pen version` = `0.3.11`** 核实（注意不是 `pen --version`）；实施时在稿件评审记录中重新记录实际版本，同一批源稿与 PNG 不混用不同 CLI/导出分辨率。

```bash
pen version
pen interactive --in <source-or-working.pen> --out <reviewed-working.pen>
# interactive 中依次执行：
#   get_app_state()                         # headless 打开 + schema/文档状态校验
#   execute({ input: 'Get((n,c)=>{c.skipChildren();Print(n.id,n.name)})' })
#   execute({ input: 'Export(["<实际frame-id>"], "png", "<export-dir>")' })
#   save()
#   exit()
```

- frame id 必须从 `get_app_state` / `Get` 取得，不能照抄占位符；interactive `Export` 只按 `Export(ids, type, dir)` 调用，**没有也不承诺 scale 参数**。每个 §7.1/§7.2 必需画面逐一导出并记录实际像素尺寸，禁止后期 resize 冒充重出；若确需固定倍率，只能另走 CLI 明确支持 `--export-scale` 的一次性 `--export` 路径，不得混入 interactive 调用。
- schema 校验通过、必需帧齐全、源稿保存成功、PNG 与源稿同版本且目视一致，才算 D0 完成。CLI 升级或 Export 行为变化时，同批全部 PNG 重出并记录差异，不能只更新受影响的一张。

---

## 8. 分阶段实施任务与 PR 边界

### Phase 0：证据与设计（可并行）

| 任务 | 产出 | 门禁 |
| --- | --- | --- |
| A0-1 主 UI 视口探针 | debug 快照、原始日志、截图 | 不记录内容/敏感信息 |
| A0-2 真机矩阵与判定 | `DATA-COLLECTION.md`、根因分类、选定路线 | §3.3 每个高度增量有证据，组合场景可重放 |
| A0-3 Portal 同机测量 | body/列表/#token-form rect、scrollWidth、safe-area、键盘/浮层快照 | 登录补偿采用 #116 最终分类，不复制旧 R6 |
| D0-1 Gmail 行为对照与主 UI `.pen` | 更新源文件、PNG、preview | 主 UI 设计评审通过 |
| D0-2 Portal `.pen` | 新源文件、PNG | Portal 设计评审通过 |

### PR1：#116 高度、铺底与子层传导

依赖 **A0 + D0-1（已签署）**；A0 依赖按 §3.3 的**分支闭合度**计算，不要求整张 §3.2 矩阵先跑完：已由真机 seq 坐实的分支（钉高坐标 `visibleBottom - app.top`、准入守卫补 `abs(visibleBottom - app.bottom)`、safe-area/根画布铺底、flex/min-height 传导）可以直接开工；尚未闭合的**遮挡分类**分支（§4.2 的阈值、"浮层不多扣高度"、稳定壳 vs 补偿模式）在带 hint 标记的妙控浮动栏/软键盘数据到手前**不得写入任何分类政策或常量**。不依赖 PR2 的顶栏改版。

- 按 §3.3 选定路线修改 `applyVisualViewportHeight` / `reapplyPinnedVisualViewportHeight` / `initVisualViewportHandling`，必要时抽纯决策函数；保留 zoom/no-API 降级。若 A0 证明这些函数不是故障分量，允许且必须记录“不修改”，只修实际故障层。
- 完成根画布、safe-area 全出血背景与 flex/min-height 传导；不改变终端数据/输入行为。两套 PTY 工厂同查，快速 VV 变化后若 100ms 门漏发最终尺寸则补 trailing，并验证 applied size 一致。
- 更新 `TestViewportAndHeightAnchors` 和既有 `viewport_height.test.mjs` 中已经过时的旧契约；新增 #116 专属行为测试，建议独立 `viewport_height_issue116.test.mjs`，避免混淆 #100 历史测试计数。
- **按实际根因分支提供失败证明**：分类/钉高分支至少有一个旧实现会失败的对应场景（含实际触及的妙控浮层）；纯铺底分支不强迫 inline 行为出现差异，而以计算背景链 + 改前/改后截图证明白缝消除；flex/传导分支用子层 rect 证明。只改布尔 inline 断言不算完成。
- 保留 A0 的最小 debug 诊断能力或明确移除临时探针；若保留，必须 query-gated 且无内容日志。
- **PR1 提交** `evidence/responsive-cdp.mjs` 与 `evidence/RESULTS.md` 模板，供 PR1/PR3 分别回填几何证据；初始 PLAN docs 提交仍不包含 evidence。
- **PR1 merge 门禁**：在同一真机上按 A0 矩阵做修后复跑，保存修前/修后截图与差值到 DATA-COLLECTION，证明 §4.3 不变量；不是只回填 A0 输入就算完成。设备不可得时 PR1 不得合并，也不得关闭 #116。
- 核对 `.pen`/PNG 与最终铺底、safe-area、几何行为一致；更新 `docs/PRD.md` §7 中 R5/R6 高度契约（纯铺底分支只修实际描述），CHANGELOG 单独一条 #116。

### PR2：#100 追加 Gmail 式主 UI + 状态化实例动作

依赖 D0-1；建议基于 PR1，避免同时调试高度与顶栏。

- 固定 New Instance、tabs 滚动归属、≤1024 可访问图标内联（`#instance-actions` gap 8→4、`+`/动作组不收缩）与 320px/112px tabs 预算、coarse 真实顶栏高度及 drawer 对齐；同一活动实例/顺序的轮询重绘保持 `scrollLeft`，仅活动实例变化等明确场景触发活动 tab 可见性调整。
- 落地 `getInstanceActionModel` / 统一 render+dispatcher / per-id in-flight / `refreshAfterMutation`（或等价保证）；处理 stale click、poll 替换对象、动作前已有 refresh、响应后已切 tab（含 A→B→A 的 selection revision 守卫）、Stop banner 归属与失败解锁。同一按钮 DOM 在所有宽度完整内联，stopped 的 Delete 不进菜单。
- 触屏 worktree 动作可见可点，语义化 button；两处模板统一 `.wt-row-actions` 并删除 inline，新增 fine 基础规则与 coarse `.wt-branch{flex-wrap:wrap}` + 外层 `flex:1 0 100%`；`.wt-actions` 仅作嵌套逻辑分组。按 local/remote 动作集合条件渲染外层，保留 active-only Delete、GitHub 在名称行及 122/162px 名称预算、`startWTRename` 外层守卫、无动作不渲染和 coarse +44px 密度契约。
- 新增 `instance_actions.test.mjs` 或等价行为测试；更新 `narrow_drawer.test.mjs`、`sidebar_collapse.test.mjs`、DOM stub 和 Go CSS/DOM 锚点中受影响的旧假设。**显式迁移/注入** `web_delete_autoselect.test.mjs` 与 `web_ghost_select.test.mjs` 的 `renderWorkspace/renderTabs/selectInstance` 切片依赖，以及 `sidebar_wt_rename.test.mjs` 的 `refresh` 切片依赖（若新 helper 被其闭包引用）；`web_ghost_select.test.mjs` 的 framework shadowing 前提和“index 不手写 `window.selectInstance`”断言必须继续绿。
- **新增独立 `worktree_actions.test.mjs` harness**，不替换 `sidebar_wt_rename.test.mjs` 现有 rename/IME 专用 renderSidebar 桩；按 §9.1 的依赖清单切真实 `renderSidebar/startWTRename`，覆盖 local/remote × active/inactive × main/regular、单/双击不切行不进 rename、无动作无空行、GitHub 名称预算。`sidebar_wt_rename.test.mjs` 继续只承担 Open Terminal/Finder finally render 的 rename 回归；`terminal_status_test.go` 为新文件补 Node wrapper。
- 更新 `.pen`/PNG/preview 的最终实施一致性、`docs/PRD.md` 与 CHANGELOG；旧响应式 PRD 只加后续变更指引，不篡改历史评审记录。

### PR3：#118 Portal 完整响应式

依赖 D0-2；列表/触屏部分可与 PR1/PR2 并行，登录 visible-rect 的实现与最终合并还依赖 A0-3 及 #116 最终遮挡分类。整个工作包独立验收和回滚。

- 先完成单数据 DOM 的响应式列表、safe-area/dvh、触屏、hidden/class 和滚动；记录改前 CSP 控制台与首屏表头基线，修后以“首屏无空表头、无对应违规”作为旧实现失败证明。Path 在 640/641 两态都完整换行。此部分可独立评审，但不单独宣称 #118 全量关闭。
- 再完成登录浮层自身 visible-rect + 内部滚动、原生 form/16px input/IME、finally 解锁与 authEpoch 陈旧 401 丢弃；不复制主 UI 全局状态机，不改 body/content 高度。
- 更新 `portal_test.go`（或新增 `dashboard_test.go`）、新增 Portal Node 测试、CI 直接 Node 门禁；执行 go generate 并提交 `csp_gen.go`；复用 PR1 提交的 `responsive-cdp.mjs` 回填 Portal 几何证据。
- 提交 `.pen`/PNG 最终版、CHANGELOG #118 与 `docs/PRD.md` 当前状态；若 #118 要延期，整个 PR3 拆出，PR1/PR2 不引用“Portal 已适配”。

---

## 9. 测试策略

### 9.1 自动化

1. **Go served-asset / 结构锚点**
   - 主 UI：更新 `TestViewportAndHeightAnchors`、`TestNarrowDrawerAnchors`、`TestTouchInteractionAnchors`，新增顶栏动作 DOM/状态派生/全宽度内联/图标可访问名称/语义 button 锚点。
   - Portal：meta、dvh fallback 顺序、safe-area、640/641、单列表 DOM、hidden 消费、登录 overflow/coarse/form、无静态 inline style、裸 script/style 标签结构守卫；实际 CSP 响应头的 hash directive 正确且无 `unsafe-inline`。
2. **Node 行为测试（从真实 served 源码切片）**
   - 切片锚点移动/改名必须在 slice 阶段响亮失败；新纯函数依赖显式注入，不用镜像实现代替 shipped source。
   - 高度：静态残差、inline/dvh 两路径、软键盘开关、妙控浮层、URL 栏、方向/窗口使基线失效、offsetTop、`app.top<0` 的滚动平移与底边等式、inline 为空但底边错位时允许新建、scale/pan、无 API、前后台恢复、collapsed/quick-keys 同场、重复事件无振荡、多个高度增量叠加，以及快速变化后的最终 fit/sendResize/applied size 一致。
   - 动作模型：所有 kind × running/unhealthy、starting/stopping、stopped/failed/exited、无实例/未知状态；320/390/768/820/1024/1440px 下当前动作全部内联，stopped 的 Restart + Delete 同时可见且无 More/menu，≤1024 图标动作保留原 `aria-label/title`，320px tabs 预算约 112px；poll 替换对象、rename 陈旧按钮、同 id 冲突/不同 id 并行、动作前已有 refreshInFlight、真正的 post-mutation refresh、响应后已切 tab、A→B→A 选择代次、Stop banner 归属、失败解锁、confirm cancel、焦点动作消失后的焦点落点。
   - tabs：同一 active instance 与 tab 顺序下，poll/render 前后 `scrollLeft` 保持；活动实例变化时调用 `scrollIntoView` 或以真实几何证明完整可见；固定 `+` 不参与滚动序列、不被 tabs 重绘重建。
   - **worktree 动作（独立 harness，不动现有 rename/IME 桩）**：真实 `renderSidebar` 切片注入 `MAIN_WT_ID`、完整 state、`document/#wt-list`、按用例返回 local/remote 的 `isRemoteAccess`、`loadingActions(Set)`、真实 `escapeHtml`、可置空的 `divergeBadges`、`selectWorktree` spy；`shortcuts` 闭包随函数切片，fixture 预设非空 `activeWT` 避免 auto-select 副作用。真实 `startWTRename` 切片注入 state、`renderTabs/renderSidebar` spies、rAF 与 input document stub，用包含 SVG 的 target 验证 `.wt-row-actions` 早退且两个 render spy 均未调用。
   - worktree 行为矩阵：local/remote × active/inactive × main/regular，Delete 仅 local active 普通行，remote active 仅 Delete、remote inactive 无外层，主仓库无 Delete；单/双击不切行不进 rename、无动作无空行、loading/finally 不破坏 rename。200/240px 验证 140px 动作宽、GitHub 44px 与 122/162px 名称预算、coarse +44px 密度/滚动行为；真实点击与 computed wrap 由 §9.1.4 CDP 兜底。
   - Portal：列表 alive/dead/空数据/长文本，hidden 状态，登录 visible 时 vv 补偿、offsetTop pan、scale 往返、隐藏后无旧几何、不会改 body/content 高度；普通 Enter/IME Enter、双击仅一轮认证、CSRF/auth 失败后解锁并可重试、登录成功后旧 401 不重开而当前 401 仍显示、成功路径。
3. **旧测试迁移纪律**
   - 迁移**实际存在**的旧契约：“静止一定清 inline”、触屏禁用 `.wt-actions`、`+` 必须是 snap 首子元素。当前测试里并不存在“所有实例总显示三个动作”的专门断言，不虚构删除项；本期新增全状态动作矩阵，防止该旧产品行为以任何形式回归。
   - `TestResponsiveLayerChangelogCounts` 位于 `internal/ui/terminal_status_test.go`，按 issue #95 房规同步；新增文件若在 CHANGELOG 声明数量，必须在该函数表加对应文件/短语行并保证计数一致。新增文件使用唯一短语，不篡改历史条目的覆盖含义，不在计划中预写会漂移的测试总数。
   - 新增 Go wrapper 在 Node 缺席时按既有约定 skip；CI 的直接 Node 步骤必须真实执行全部 UI + Portal 文件。
4. **真实 Chromium 几何检查（人工执行证据脚本，非现成 CI 库）**
   - 仓库只有一次性先例 `docs/plans/issue122-rename-worktree/evidence/cdp.mjs`，没有可复用 harness。**PR1 提交** `docs/plans/fix-issue116/evidence/responsive-cdp.mjs` 与 `RESULTS.md` 模板，PR3 复用该脚本；它们不属于 §11.4 的实施前纯 PLAN docs 提交。脚本仅复用 Node built-ins / CDP 思路，支持显式 `--url`、`--chrome` 与 `--scenario main|portal`，不引入框架、不写死 file URL/端口，也不宣称直接进 CI。
   - 脚本至少测 320、390、640、641、768、769、820、1024、1440px 与 coarse/fine，断言 `getBoundingClientRect()`、`scrollWidth/clientWidth`、命中区和当前动作完整内联；320px coarse 额外断言 `#instance-actions` computed gap=4px、`+`/动作组不收缩及 tabs 约 112px；CDP `Emulation.setDeviceMetricsOverride.width` 按整数输入，**768.5px 不放入脚本矩阵，改由 §9.2 人工页面缩放构造**。200/240px 侧栏实测 fine `flex-wrap:nowrap`、coarse `flex-wrap:wrap`、外层 `flex-basis:100%` 且动作行顶缘不低于分支文本底缘，覆盖 local/remote 动作数与长主仓库名+GitHub 的 122/162px 名称预算和列表密度；Portal 用 200 字符路径验证五列/卡片两态。
   - 脚本输出 JSON 差值与截图到本目录 `evidence/`，执行记录写入人工验收文档；§9.3 给出对应人工命令。VoiceOver 列表/表单/错误宣告另在 §9.2 人工过检。Chromium 只证明桌面布局与语义，**不能替代 iPadOS Safari 真机**。

### 9.2 真机人工矩阵

- 设备：可复现问题的 iPad 至少一台；iPhone Safari 至少一轮；条件允许补 iPadOS 不同版本与 Chrome(iOS)。
- 主 UI：横竖屏、Split View、URL 栏显隐、软键盘、妙控浮动栏、缩放/pan、safe-area、前后台恢复；另用桌面浏览器页面缩放人工构造 768.5 CSS px（如 1537px 物理宽、200% zoom），验证互补媒体查询，**不得用 CDP 整数 width 直接冒充该场景**。
- 实例：running 仅 Stop；停止后仅 Restart + Delete；failed/exited 同组；starting 可 Stop 取消、stopping 无重复动作；unhealthy 仍 Stop；320/390/768/820/1024px 下 stopped 两动作都直接可见，动作中切换 tab 不被响应抢回。
- 顶栏：多个 tabs + coarse 时 `+`、活动 tab、Stop/Restart/Delete 全部可达，无 More/menu，tabs 的 scroll-snap 只在 ≤768px 生效，轮询不重置 `scrollLeft`。
- 触屏：worktree Open Terminal/Open Finder/Delete 可见可点且不触发行切换；200/240px 独立动作行满足 44px 与 140px 总宽预算，local/remote × active/inactive 动作数正确，长主仓库名 + GitHub 44px 的 122/162px 名称预算无重叠；320×568、390×844、820×1180 下记录一屏完整行数与滚动距离，接受 coarse 每动作项约 +44px 的密度代价、无动作不留空行，并能滚到最后一项及 Staged/Unstaged；桌面 hover、键盘 focus、Ctrl/Cmd+B 不回归。
- 终端：pty 与三种 iframe 的底部尺寸、最后输入行、quick-keys、IME、触摸滚动、fit/resize 不回归。
- Portal：320/390/640/641/768/820/960 宽度，200 字符路径/CJK/五位端口/0/1/多行、alive/dead、登录错误与软键盘、普通 Enter/IME Enter/双击、Tailscale 裸 HTTP；所有内容可达且无页面横向溢出，input 16px 不触发聚焦自动放大；另做 VoiceOver 列表/表单/错误宣告 smoke。
- **PR1 修后真机复跑**：在同一设备重复 A0 的适用矩阵，逐项保存修前/修后截图、app/workspace/host rect 与差值；结果回填 DATA-COLLECTION 并作为 PR1 merge 签字，而非仅在 issue 关闭前补做。

### 9.3 最终命令门禁（实施最后一次改动之后执行）

```bash
go generate ./internal/portal
unformatted="$(gofmt -l .)"
if [ -n "${unformatted}" ]; then
    printf 'These files need gofmt:\n%s\n' "${unformatted}"
    exit 1
fi
go test ./internal/ui/ ./internal/portal/
go test ./...
node --test 'internal/ui/testdata/*.test.mjs' 'internal/portal/testdata/*.test.mjs'
```

真实 Chromium 几何门禁由以下**人工证据命令**承接（先启动被测 daemon / Portal；不是 CI 自动库）：

```bash
export CHROME_BIN="/path/to/chrome"
export MW_TEST_URL="http://127.0.0.1:PORT/"
export MW_PORTAL_URL="http://127.0.0.1:PORTAL_PORT/"
node docs/plans/fix-issue116/evidence/responsive-cdp.mjs --chrome "${CHROME_BIN:?}" --scenario main --url "${MW_TEST_URL:?}"
node docs/plans/fix-issue116/evidence/responsive-cdp.mjs --chrome "${CHROME_BIN:?}" --scenario portal --url "${MW_PORTAL_URL:?}"
```

PR1 另须完成 §9.2 的修后真机复跑并签字；仅有上述自动测试或桌面几何结果不能替代。

并按仓库既有 CI 构建 `cmd/myworktree` 与 `cmd/mw`；Node 命令以最终文件布局和 CI 写法为准，确保没有“glob 未匹配但显示绿色”。`csp_gen.go` 应在实施提交中已生成并提交；最终可重跑 generator 验证可复现，但 CI 不得靠现场 generate 掩盖漏提交生成物。

---

## 10. 验收与关闭条件

### #116 可关闭

- A0 根因分类与实施路线一致，真机复核通过 §3 矩阵；
- §4.3 的外壳、内容、终端三层不变量全部满足；
- 妙控浮动栏不再额外压缩，软键盘仍能避让，zoom/方向/URL 栏不回归；
- 暗色/浅色均无默认白画布缝，且测试证明不是仅换背景遮住错误高度；
- 主 UI `.pen`/PNG 已评审并与最终铺底、safe-area、软键盘/浮层几何一致。

### #100 追加可关闭

- Gmail 式稳定壳、固定主操作、tabs 内滚、≤1024 可访问图标内联和触屏动作落地；320px 起各目标宽度下 stopped 的 Restart + Delete 都直接可见，不以“展开后可达”替代；
- running/stopped 严格满足用户两条显示要求，其余状态符合 §5.4；
- 同一状态模型驱动所有宽度，handler 防 stale click；
- 主 UI `.pen`、PNG、preview 与实现一致，桌面和既有终端功能零回归。

### #118 可关闭

- Portal 桌面/手机/平板、长路径、登录键盘和触屏验收通过；密码字段无法构造妙控候选/浮层时记录 N/A 与证据，不伪造通过；
- 列表只有一份数据 DOM，200 字符连续路径在 320/390/640/641/820/1440px 均完整可达且 `scrollWidth ≤ clientWidth`，无裸横向表格溢出；
- 首屏无空表头、无静态 style CSP 违规；CSP hash、浏览器无 CSP 违规、Go/Node/CI 门禁全绿；
- Portal `.pen` 与 PNG 已提交。

---

## 11. 文档同步、风险与回滚

### 11.1 文档同步

- 每个实施 PR 更新自己的 CHANGELOG；不把三个 issue 混成一个无法回滚的长条目。
- PR1/PR2/PR3 落地后分别更新 `docs/PRD.md` 当前实现状态：PR1 必须同步 §7 的 R5/R6 高度与铺底描述，PR2/PR3 更新各自响应式条目；旧响应式 PRD 只增加指向本计划的后续变更注记。
- `docs/API.md` 必须记录临时诊断接口的用途、认证、schema、限制、TTL 与读取示例；`docs/ARCHITECTURE.md` 仅在高度修复改变架构时再更新，其他协议不变。
- DATA-COLLECTION 记录真机原始证据和未覆盖设备，不能把未测矩阵写成通过。

### 11.2 主要风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 背景铺底掩盖真实缺高 | 三层 rect/内容验收，背景修复与几何修复分别评审 |
| WebKit 不暴露可靠的浮层/键盘分类信号 | A0 先验；区间重叠时走稳定壳 + 局部避让，不猜 UA/阈值 |
| vv 与 dvh 同时短报/错报 | 先找独立几何基准；本计划不接受常量偏移关闭 H1，无可靠基准则阻塞而非宣称精确修复 |
| A0 设备或妙控键盘不可得，无法完成修前/修后矩阵 | PR1 不合并、#116 不关闭；不用桌面模拟签字替代 |
| `pen` CLI 版本或 Export 分辨率漂移，PNG 与源稿不一致 | §7.4 记录 `pen version`、`Export(ids,type,dir)` 调用和实际尺寸；版本变化时同批全部重出 |
| 后端动作检查弱于 UI 白名单（Restart 可接受 unhealthy/stopping，Delete 仅查 running map，Stop 对未知 id 幂等） | 本期只让 UI 更严并登记缺口，不宣称服务端等价保护、不顺手改后端；若需封堵直接 API，另立后端 issue/评审 |
| 高度写样式与 xterm fit 形成振荡 | rAF 合并、单一高度决策者、下一帧复核、ResizeObserver 回归 |
| RO 100ms 门漏发稳定后的最终尺寸 | 两套工厂同查；必要时 trailing 发送；以 appliedTTYSize 证明最终一致 |
| 动作后 poll/异步响应覆盖用户新选择 | per-id 锁、真正 post-mutation refresh、selection revision（含 ABA）与捕获 id 的副作用守卫 |
| Portal 旧 401 覆盖登录成功或失败后无法重试 | authEpoch 丢弃陈旧响应、pending finally 解锁、乱序/重试行为测试 |
| 顶栏响应式与状态化同时引入回归 | 同一 PR 内共享 DOM/model，状态矩阵 + 真实浏览器几何双门禁 |
| 旧测试锁定旧错误行为 | 明确迁移契约，关键新用例对旧实现应具有失败证明 |
| Portal inline style/CSP 或 CI 漏跑 | class/hidden、go generate、浏览器 CSP 检查、CI glob 同步 |
| 范围膨胀导致 #116 延误 | PR1/PR2/PR3 独立；Portal 整包延期不影响 #116 |
| 设计与实现漂移 | `.pen` 前置评审、偏离先回改设计、最终 PNG/预览对照 |

### 11.3 回滚

- PR1 可单独回滚高度/铺底，不携带顶栏和 Portal 变更。
- PR2 可单独回滚响应式顶栏与动作模型，不改变 PR1 的高度算法；实例 API 数据无需迁移。
- PR3 只触及 Portal 静态页、测试、生成 hash 与 CI 测试路径，可独立回滚，不影响主 UI。

### 11.4 实施前的文档交付纪律

- A0 页面诊断通道按用户明确指令先行实施，仅用于测量，不视为正式高度修复；**PLAN、DATA-COLLECTION 与带 `viewportdebug` 标签的诊断实现可同批提交**，以便后续 beta 和真机复核继续使用。正式 PR1/PR2 仍须另行提交，并包含 `REVIEW-TEMP.md` 之外的设计、测试与实现证据。
- `REVIEW-TEMP.md` 已按作者明确同意删除。后续若再产生临时评审文档，仍不 `git add`、不进入 `.gitignore`，全部销项后也须经作者明确同意才能删除；当前不擅自提交任何文件。
