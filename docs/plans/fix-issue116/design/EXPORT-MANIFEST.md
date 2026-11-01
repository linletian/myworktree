# Issue #116 D0-1 / D0-2 设计导出与评审清单

**日期**：2026-10-28  
**工具**：`pen version` = **0.3.11**  
**状态**：源稿、PNG 与主 UI preview 已生成并校验；**D0-1 / D0-2 设计评审待签，PR1 / PR2 / PR3 尚未获得编码门禁放行**。

## 1. 校验与导出纪律

- 三份源稿均通过 `pen interactive` 的 `get_app_state()` 文档/schema 校验，并由 `save()` 保存。
- frame id 通过 `Get((n,c)=>{c.skipChildren();Print(n.id,n.name,n.width,n.height)})` 查询确认；未使用占位 id。
- 所有 PNG 均通过 interactive `Export(ids, "png", dir)` 原生导出，**未传入 scale，也未做任何后期 resize、裁剪或截图缩放**。下表记录实际 PNG 像素尺寸；其大于 CSS 场景尺寸是 CLI 当前导出行为。
- 本轮未升级 CLI，未混用不同 CLI/Export 路径。quick-keys 仅说明帧与 iPhone 软键盘帧的源内容发生变化，因此只重出这两帧；其余五帧源内容与既有 PNG 保持不变。
- 已逐项目检主 UI 与 Portal 必需画面。初次主 UI 导出中发现的终端坐标遮挡、密度说明重叠及 Portal 空态表头换行问题均已回改 `.pen` 并重新导出，不以修图代替源稿修正。

## 2. D0-1 主 UI

设计源：[sidebar-states.pen](../../../../ui/sidebar-states.pen)  
交互预览：[preview-responsive.html](../../../../ui/preview-responsive.html)

| Frame id | PNG（`ui/`） | 实际像素 | 覆盖内容 |
| --- | --- | ---: | --- |
| `d1_notes` | `notes-decisions.png` | 2362×2082 | 状态矩阵、断点不变、全宽内联、铺底/高度分离、证据边界 |
| `d1_desktop_expanded` | `desktop-expanded.png` | 2882×1802 | 1440×900、240px 展开侧栏、running 仅 Stop |
| `d1_desktop_collapsed` | `desktop-collapsed.png` | 2882×1802 | 折叠零宽、stopped 的 Restart + Delete 内联 |
| `d1_action_states` | `desktop-action-states.png` | 2882×1962 | running / starting / stopping / stopped 四态及完整矩阵 |
| `d1_middle` | `middle-tier-narrowed.png` | 2050×1538 | 1024×768、200px 侧栏、图标动作与独立 tabs |
| `d1_drawer_closed` | `narrow-drawer-closed.png` | 1538×2050 | 768×1024、drawer 关闭、固定 `+` 与两动作 |
| `d1_drawer_open` | `narrow-drawer-open.png` | 1538×2050 | drawer 打开、右缘 44px 开关、安全区 |
| `d1_coarse390` | `coarse-390.png` | 782×1690 | 390×844、52px 顶栏、44px 命中区 |
| `d1_coarse320` | `coarse-320.png` | 642×1138 | 320×568、112px tabs 预算、无透明重叠补丁 |
| `d1_worktree_actions` | `worktree-actions.png` | 2882×2442 | fine/coarse、local/remote、200/240px、140px 与 122/162px 预算 |
| `d1_density320` | `worktree-density-320.png` | 642×1138 | 320×568、240px drawer、完整壳层、末项/Git 可达 |
| `d1_density390` | `worktree-density-390.png` | 782×1690 | 390×844、240px drawer、无动作无空行 |
| `d1_density820` | `worktree-density-820.png` | 1642×2362 | 820×1180、200px 并排侧栏与滚动后果 |
| `d1_density_matrix` | `worktree-density-matrix.png` | 3362×2602 | 320/390/820 × 0/1/multi 设计矩阵；真实滚动数据待回填 |
| `d1_viewport_geometry` | `viewport-geometry.png` | 2882×2482 | 静止、软键盘、妙控浮动栏、横屏/Home Indicator 几何标注 |
| `d1_quick_short` | `quick-keys-short-viewport.png` | 1642×1202 | 820×600 矮视口、quick-keys 与新顶栏互不遮挡 |

### quick-keys 既有稿件复核

设计源：[quick-keys-panel.pen](../../../../ui/quick-keys/quick-keys-panel.pen)

- 形态、键集、入口、左展与内部滚动不变；已在说明帧注明依赖的新坐标公式、根画布铺底、52px coarse 顶栏与 A0 分类边界。
- iPhone 软键盘帧的旧“直接钉到键盘顶”描述已改为经硬遮挡分类后的 `visibleBottom − app.top`。
- `qk0001` → `notes-decisions.png`（1002×1286）；`qk0124` → `scene-iphone-keyboard-panel.png`（802×1772）。

### preview 验证（只验证 preview，不是 PR1/真机证据）

- inline JavaScript 通过 `node --check`；桌面 Chrome 以 1440×900 渲染成功。
- 临时 CDP 冒烟以 **320×568、coarse/touch、drawer 关闭、stopped** 运行：header = 52px，侧栏开关/New Instance = 44×44，tabs = **112px**，`#instance-actions` gap = 4px，Restart/Delete = 44×44 且完整内联。drawer 打开时开关 fixed 脱离 flex 流，tabs 预算变为 160px，不与 112px 混用。
- running/unhealthy/starting、stopping、stopped/failed/exited、unknown 的 action model 均符合计划；模拟 poll 前后 `scrollLeft=40` 不变。
- local active 动作为 Open Terminal/Open Finder/Delete；remote active 仅 Delete；remote inactive 与 remote main active 均不生成 `.wt-row-actions`；主仓库无 Delete。
- 真实 preview DOM 复测（**coarse、无经典滚动条**）：200px 档内容宽 **172px**、长主仓库名称宽 **122px**；240px 档分别为 **212px / 162px**；GitHub 均 44px、名称间距 6px，紧凑动作 gap=4px、宽屏 gap=8px。fine 的 GitHub 为 28px；经典滚动条会另外占用可用宽度（例如 15px），不与上述 coarse 条件混写。
- 密度基线按条件区分（行高含 1px border-bottom、无外边距）：fine 普通行 **65.8px**，无经典滚动条时两行主仓库行 **84.7px**；200px 档在 15px 经典滚动条导致三行时为 **103.5px**；coarse 三行/两行主仓库行分别约 **139.5px / 126.9px**。名称为 13px、branch 为 11px，preview 行圆角为 0。
- coarse `.wt-branch` computed `flex-wrap:wrap`，外层 `flex-basis:100%`；fine + `hover:none` 下 `.wt-actions` 仍为 `opacity:1; pointer-events:auto`。
- 焦点动作隐藏后分别落到活动 tab / 可见状态文本；状态在 confirm 前改变时 stale Delete 被拦截，Cancel 不取锁。
- quick-keys 面板在入口列左侧且顶缘对齐、位于 workspace 内；页面 `scrollWidth=innerWidth=320`。
- 密度帧的 clientHeight、scrollHeight、首屏完整行数与滚动距离仍是 **PR2/CDP 待回填项**，不由上述 preview 结果替代。
- 这些结果不证明真实 `index.html`、VV 分类、per-id 锁、post-mutation refresh、selection revision 或 iPadOS 行为已经完成。

## 3. D0-2 Portal

设计源：[portal-responsive.pen](portal-responsive.pen)

| Frame id | PNG（同目录） | 实际像素 | 覆盖内容 |
| --- | --- | ---: | --- |
| `d2_notes` | `portal-notes.png` | 2482×2322 | 640/641、vh→dvh、安全区、44px、visible rect 与完整生命周期 |
| `d2_desktop` | `portal-desktop.png` | 2882×1802 | 1440×900、960px 内容卡、五列网格与长路径 |
| `d2_ipad` | `portal-ipad.png` | 1642×2362 | 820×1180、coarse 44px、五列 Path 完整换行 |
| `d2_breakpoints` | `portal-breakpoints.png` | 3602×2242 | 320/640/641 三联、200+ 字符路径、CJK、五位端口、Alive/Dead |
| `d2_mobile_dark` | `portal-mobile-dark.png` | 782×1690 | 390×844 深色卡片、dead/alive、Home Indicator |
| `d2_landscape` | `portal-landscape-safe-area.png` | 1690×782 | 844×390 横屏安全区、文档纵滚 |
| `d2_states` | `portal-list-states.png` | 2482×1442 | loading、现有空列表结果、login-required，未新增产品文案 |
| `d2_token` | `portal-token-components.png` | 2482×1682 | Token 默认/错误/focus、16px input、44px 目标、form/IME/authEpoch |
| `d2_login_keyboard` | `portal-login-keyboard.png` | 782×1690 | 390×844 软键盘、layout/visual viewport、浮层 rect |
| `d2_login_short` | `portal-login-short-landscape.png` | 1690×782 | 初始 Login 在 fold 下 → 滚动后可达；overlay 单滚动 owner |

妙控候选/浮动栏在密码字段场景目前无法可靠构造，按 §7.2 只在说明帧记录契约并标为 N/A；未伪造通过场景。

## 4. 评审签字（待完成）

- [ ] D0-1：确认 52px coarse 顶栏、≤1024 全部动作以可访问图标内联及 320px/112px tabs 预算；宽屏动作 gap=8、紧凑块 gap=4。
- [ ] D0-1：确认 starting 可 Stop 取消、stopping 无动作、焦点动作消失后落到状态文本或活动 tab；stale Delete 与 confirm cancel 行为正确。
- [ ] D0-1：确认统一 `.wt-row-actions` 的 fine/coarse、touch/focus/`hover:none`，以及 local/remote × active/inactive × main/regular 全部组合。
- [ ] D0-1：确认 `.pen` 与真实 preview DOM 均满足 200/240px 的 172/212 内容预算、122/162 名称预算、140px 三动作预算，GitHub 不收缩且不重叠。
- [ ] D0-1：接受 coarse 每动作项约 +44px 的密度下降；320/390/820 × 0/1/multi 设计覆盖完整，真实滚动数据留待 PR2/CDP，不以示意值签字。
- [ ] D0-1：确认 quick-keys 在入口列左侧顶缘对齐、320 两列、矮视口内部滚动，且不遮顶栏、tabs、状态栏或入口。
- [ ] D0-2：确认 Portal 单列表语义、640/641 归属、320/390/640/641/820/1440 的完整 200+ 字符 Path 换行。
- [ ] D0-2：确认 44px coarse 目标、16px 密码输入、既有 loading/empty/login/Invalid token 文案，以及 hidden/class 与 CSP 闭环要求未降低。
- [ ] D0-2：确认登录浮层初始化/显隐/隐藏清理/resize/scroll 职责完整；短横屏由 overlay 单 owner 滚动，Login 从初始不可达到滚动后可达。
- [ ] **签字的后续验证条件**：PR2/CDP 回填 320/390/820 的真实 `#wt-list` clientHeight、scrollHeight、首屏完整行数与滚动距离；当前仅设计覆盖完整，未回填前不得写成密度实测通过。
- [ ] 评审人 / 日期 / 结论：**待填写**。
