# Issue #116 A0 真机测量记录

**状态**：A0 主体数据已采集（会话 `d5222b167a61be314fdba725fcfedff3` + `b90643bea91477cd`，合计逾 1700 事件）；妙控浮动栏**不进 visualViewport**已实测坐实；坐标、准入守卫与根画布修复待实施；软键盘 hint 与命名、quick-keys 同场、键盘打开时刷新待补。

**诊断方式**：页面内 `?viewport-debug=1` + daemon 会话报告，不依赖 Safari Web Inspector。

**隐私**：只记录几何、有限设备元数据与 UI 状态；不记录 token、终端文本、worktree 名称/路径或实例 ID。

## 1. 使用流程

1. 在目标仓库构建带诊断标签的版本：`go build -tags viewportdebug ./cmd/myworktree`（需要既有 beta 埋点时用 `-tags ttymetrics,viewportdebug`）。未打标签的稳定构建不注入脚本，资产与报告接口均返回 404。
2. iPad 打开 `http://<host>:<port>/?token=<token>&viewport-debug=1`，页面自动生成 `debug-session`。
3. 页面默认每 1 秒自动连续采集，无需点击 Capture；保持当前地址不变，按需操作 URL 栏、软键盘和妙控浮动栏。可点 **Magic bar** / **Soft keyboard** 标记对应状态，按钮使用 pointerdown 防止终端焦点迁移；也可用 Start/Stop continuous 控制采集。
4. 诊断者读取：

   ```text
   GET /api/viewport-debug/report?session=<debug-session>&after=<last-seen-seq>
   ```

5. 会话 30 分钟无更新即过期；断线时可用页面 **Copy JSON** 作为备用。

## 2. 本地 Chrome 自测（不是 Safari 证据）

- 日期：2026-10-27。
- 环境：隔离临时 Git 仓库、真实 headless Chrome、实际 daemon。
- 结果：浮层正常生成；自动生成会话；`init/pageshow/settle/magic-hint` 均成功上传；增量 GET 返回 4 个事件，`latestSeq=4`；报告未包含 token、路径或终端文本。
- 限制：仅证明诊断链路和真实浏览器 DOM 行为，不证明 iPadOS 高度、妙控浮动栏或 safe-area 结论。

## 3. iPad 首轮真实报告（2026-10-27，未闭环）

- 会话：`d5222b167a61be314fdba725fcfedff3`，共 74 个事件；访问方式为 Tailscale `100.86.87.23:38231`。
- 环境：屏幕 834×1194、Safari 27.0.1 UA、coarse=true；**全部事件 `scale=0.75`，没有 scale=1 基线，也没有 `keyboard-hint`**。
- 静止基线（seq 1–3、46–47）：`innerHeight=936`、`vvBottom=936`、`app.bottom=935.984375`，差值仅 **−0.016px**；`inlineHeight` 为空。当前静止状态不支持“#app 少一行”的假设。
- 同一状态：`app.paddingBottom=20px`、`workspace.bottom=915.984375`，即 workspace 在 app 底边前 **20px** 结束；`body/html/#app` 背景均透明，workspace 为 `rgb(11,16,32)`。**这 20px 是最强的 safe-area/画布白缝候选**（75% zoom 下约 15 物理 px）。
- 终端行余量（seq 46–47）：`xtermRows.bottom=907`，workspace 底边 915.984，余量约 8.984px，属于小于行高的 fit 取整余量，不能与 20px 外壳 padding 混为一谈。
- 妙控标记（seq 48）：app/vv 未变化，且没有明确的软键盘标记；vv 在 936→844.656→365.328 等值间变化，不能在无人工标记时归因于妙控或软键盘。
- **初步结论**：高度算短不是首轮静止数据的主线，safe-area/根画布是更强候选；但 75% zoom 会启用 zoom 豁免，且缺少 100% 静止基线与明确浮动栏/软键盘状态，不能关闭 A0 或开始固定高度算法。
- **后续正式采集**：使用不带旧 `debug-session` 的基础链接生成全新会话，避免把本地 smoke 与真机数据混用。新版默认每秒 `periodic` 连续采集，按钮 pointerdown 保焦，复用会话时从 `latestSeq` 续号。

## 4. 100% 连续采集（seq 98–343，滚动坐标证据）

- 数据来源：同一 iPad 会话，100% zoom；`after=97` 读取，latestSeq=343。报告含滚动、resize、settle、periodic 和方向变化，未使用人工 hint 按钮。
- **静止且未钉高（seq 337–338）**：`innerHeight=vv.height=702`、`app.bottom=702`、`inlineHeight=""`；`paddingBottom=20px`、workspace 底边 682。静止高度正确，safe-area 透明缝仍存在。
- **可见区缩小并钉高（seq 339–343）**：`innerHeight=702`、`vv.height=633.5`、`app.bottom=633.5`、`inlineHeight=633.5px`，workspace 底边 613.5。`innerHeight - vv.height = 68.5px`，其中仍有 app padding 20px；系统 UI 分量需与 safe-area 分开判断。
- **页面被拖动（代表 seq 305–312）**：`vv.offsetTop` 从 0 依次增至 12/29/47.5/80/99.5/118，`app.top` 从 0 变为 −12/−29/−48/−80/−100/−118，`inlineHeight` 从 274 依次变为 286/303/321.5/354/373.5/392，但 `app.bottom` 始终约 274。`expectedVisibleBottom - app.bottom` 从 0 增至 118，**与 offsetTop 完全一致**。
- **反向及另一组拖动（seq 330、333）**：offsetTop=6.5/54 时，底边误差为 7/54，仅有亚像素级差异，进一步印证同一公式问题。
- **未钉高也会错位（seq 624、736）**：seq624 的 `visibleBottom=clientHeight=1062`，`app.top=-340`、`app.bottom=722`、`inlineHeight=""`，底边差 340；seq736 的 `visibleBottom=clientHeight=702`，`app.top=-428`、`app.bottom=274`、`inlineHeight=""`，底边差 428。旧条件 `visibleBottom < clientHeight-1` 为 false，且 scroll 只允许已有 inline 钉高更新，因此不会补偿。**这证明守卫还必须检查 `visibleBottom - app.bottom`，不能只检查 visibleBottom 与 clientHeight 的关系。**
- **坐标结论**：当前 `applyVisualViewportHeight` 用 `visibleBottom=vv.offsetTop+vv.height` 直接设置高度，未扣除 `app.getBoundingClientRect().top`。目标高度应为 `visibleBottom - app.top`，否则滚动/系统自动聚焦平移时底边偏差等于拖动量。补偿条件也应是 `visibleBottom < clientHeight - 1` **或** `abs(visibleBottom - app.bottom) > 1`；scroll 仅在“无 inline 且底边已对齐”时保持纯平移不新建钉高，底边错位时应允许新建钉高。此项与 safe-area 是独立故障分量。
- **尚未据此改变浮动栏政策**：后段没有人工 hint，不能把 vvHeight=274 或 633.5 直接命名为妙控浮动栏或软键盘。修改硬遮挡/浮层分类前，仍需按 seq 与用户操作顺序核对；但滚动坐标修复与根画布铺底可以先独立进行。

## 5a. 第二轮采集（会话 `b90643bea91477cd`，2026-11-01，877 事件）

本轮不需要重新操作就已关闭多个缺项；归类表由 `evidence/analyze-a0.mjs` 产出（`evidence/CLUSTER-b90643be.md`）。

- **妙控浮动栏不改 visualViewport（本轮最重要结论）**：seq388 `magic-hint` 与前后 seq385–394 全程 `innerHeight=702 / vv.height=702 / offsetTop=0 / inline=""`，app 底边与可见底边一致。结论：浮动栏是**纯覆盖层**，不进 visualViewport 也不进 dvh，因此不需要为它做“少扣一点”的补偿分支；只要准入守卫不误触发，它天然不会吃工作区。
- **钉高后的几何是对的**：本轮 483 个钉高事件的 `vvBottom − app.bottom` 全为 0.00。缺陷集中在**未钉高的准入条件**（seq190：`app.top=−95 / inline=""` 时底边错 44px），与上轮 seq624/736 的 340/428 同性质。因此 PR1 几何线的主战场是守卫，不是钉高公式本身。
- **safe-area 不是常量**：`#app paddingBottom` 实测出现 **20px / 10px / 0px** 三值。上轮只见过 20px。**新约束：铺底与几何必须读 `env(safe-area-inset-*)`，不得写死 20px。**
- **Split View 已覆盖**：`innerWidth` 实测 592（112 事件）/ 392（34）/ 618（12），对应半屏与窄分栏；上轮标“待采集”的 Split View 项就此基本关闭。
- **iframe kind 已覆盖**：`opencode-web` 138 事件且 `webPanel` 有高度（含钉高态 `occl=206.1` 与全高态）；仍缺 dsh / reasonix。
- **zoom 豁免的代价（需产品拍板）**：scale=0.75 静止段 `app.bottom=936` vs 可见底 `844.7`，**溢出 91.3 layout px**（含终端最后几行）。这是现有 zoom 豁免政策的固有行为，不是回归；是否给缩放态也建基准需单独决定。
- **缩放态与遮挡缩减量清单**（待命名）：`inner − vv.height` 实测 68.5（210）、87（12）、126（6）、154.1（6）、157（36）、206.1（160）、428（53）。除 seq388 外本轮未打 hint，**不得凭数值命名遮挡类型**；软键盘 hint 仍缺。

## 5b. 诊断浮层自身缺陷（已修，A0 采证因此中断过）

- **缺陷**：诊断浮层原为 `fixed` 右上、`max-height:72vh`、按钮排在明细 JSON **之后**。iPad 软键盘弹出（`vv.height≈274/495.9/633.5`）时按钮整排在可视区外，**`Soft keyboard` 根本按不到**；又因其占住右上，**`#terminal-controls` 的 quick-keys 入口被遮**，quick-keys 同场场景无法采集。这不是产品缺陷，是 A0 探针缺陷。
- **修复**（仅 `viewportdebug` 资产，不碰高度算法）：主按钮（Magic bar / Soft keyboard / Capture / Move / More）提到第一排；次要控件折进第二排默认隐藏；JSON 默认折叠；面板高度按 `visualViewport` 实时夹紧（`panelLayoutFor` 纯函数 + `applyPanelLayout`）；标题可拖拽、`Move` 循环四角、`?viewport-debug-pos=tl|tr|br|bl` 与 sessionStorage 记忆；靠右居位时为 `#terminal-controls` 预留宽度。
- **证据**（headless Chromium + CDP，实测于本地隔离实例与 `~/GithubRepo/eur-lex-mcp`）：`evidence/panel-check.mjs` 在 vv 高 702/633.5/545/495.9/274 五档下主按钮全部可见且 `elementFromPoint` 命中自身；`evidence/panel-occlusion.mjs` 在 tr/tl/br/bl 四角 quick-keys 入口全部可命中（修前 tr 为 `hit=NO, blocker=viewport-debug-panel`）。截图与 JSON 在 `evidence/panel-visibility/`、`evidence/panel-occlusion/`。
- **仸言**：headless Chrome **不模拟 iOS 软键盘**，上述档位是用实测 `visualViewport.height` 反向压视口，只证明浮层自身布局契约，**不能替代 iPadOS 真机**；无活动实例时控制列是 hidden，脚本会临时 unhide 它来量几何并在输出中标注 `harness-forced`。


## 5. 1–8 全量操作完成（latestSeq=747）

- 用户已按清单完成 1–8 全部操作，缩放 100%；最新事件为 seq747 的 `settle`。
- 第 7 步竖屏可点 Capture，第 7→8 步横屏时 Capture 被系统 UI 遮挡无法点击；**自动 `periodic` 连续采集未中断**，因此不影响证据完整性。
- `after=343` 的报告保留 seq624–747 共 120 个事件（达到单会话历史上限）；此前窗口已分别读取并记录，不能把 624 当作操作起点。
- 关键末态（seq739–747）：`innerHeight=vv.height=clientHeight=702`、`app.bottom=702`、`inlineHeight=""`、`paddingBottom=20px`、workspace 底边 682。系统回到无钉高状态后，外壳高度正确，safe-area 透明缝仍是独立分量。
- 最新窗口覆盖 `periodic/scroll/resize/focusin/focusout/settle/manual/window-resize`，包含输入激活、页面上滑、URL 栏和横竖屏变化；没有使用 hint 按钮，浮动栏类型按用户完成的操作顺序解释，不凭单个 vv 值猜测。
- 当前已完成的主要验收面：75% 与 100% 缩放、软键盘操作、妙控键盘及浮动窗操作、URL 栏、横竖屏、页面平移、前后台/visibilitychange、连续采集与续号。仍需补测的项目见下方矩阵。

## 5d. 第三轮：白带现场被抓到（会话 `997d18dc48ee49c84f6fbf5b797969f7`，200 事件零缺口）

本轮在带修复浮层的隔离实例上采集（仓库 `legal-court-mcp-server`、真 pty 实例 running、绑 `*:38233`），首次拿到 `keyboard-hint` 与 quick-keys 同场。

- **★ 白带现场（seq184–186，约 3 秒自愈）**：

  | seq | reason | vv.height | inline | app.bottom | 可见底 | 白带 |
  | --- | --- | --- | --- | --- | --- | --- |
  | 183 | periodic | 633.5 | 633.5px | 633.5 | 633.5 | 0.0 |
  | **184** | settle | **702.0** | **633.5px** | **633.5** | **702.0** | **68.5** |
  | **185** | periodic | **702.0** | **633.5px** | **633.5** | **702.0** | **68.5** |
  | **186** | periodic | **702.0** | **633.5px** | **633.5** | **702.0** | **68.5** |
  | 187 | resize | 702.0 | — | 702.0 | 702.0 | 0.0 |

  `visualViewport.height` 恢复全高的那一帧起，`#app` 的 inline 仍钉在旧的 633.5px，底边因此比可见底边短 **68.5px**，露出默认白画布；直到 seq187 才由 `resize` 分支清掉 inline 自愈。**归因：钉高的撤销滞后于遮挡消失，属高度决策时序缺陷，不是外壳缺高，也不是纯铺底问题。** PR1 必须让每次决策都按当前可见矩形重算（`targetHeight = visibleBottom − app.top`）并在恢复帧立即生效，而不是等下一次 resize；同时保留铺底作为兜底。
- **★ `keyboard-hint` 首次拿到（seq24）**：`vv.height=274 / occl=428 / inline=274px / focus=TEXTAREA` → **428px 缩减量归属全尺寸软键盘**，坐实。
- **★ quick-keys 同场首次拿到（seq126–140，`quickKeysOpen=true`，含 manual seq131）**：此前两轮均为 0，浮层遮挡修复后闭合。
- **68.5px 缩减量行为特征**：出现在终端 textarea 聚焦后（seq11、74、166、191），可持续数十秒，消失即触发上面的白带窗口。**用户确认：妙控候选条是浮在光标边、不影响布局**，因此 68.5 不是候选条；仍待确认是 Safari 底部工具栏还是妙控键盘输入条。
- **刷新语义澄清**：主 UI 是固定壳（`overflow:hidden` + 抑制链式回弹），**页面下拉刷新按设计不会出现**，用户改用整页刷新（seq1、163 两次 `init`）；但两次刷新时键盘均已收起（focus=BODY），所以**“键盘打开时刷新”仍未覆盖**。
- **仍未覆盖**：软键盘候选条（用户漏测）、68.5 的归属命名、键盘打开时刷新、Stage Manager、dsh/reasonix iframe、竖屏静止基线、iPhone/Chrome(iOS)、逐像素截图。

## 5e. 白带根因定案 + 铺底修复落地（PR1a）

### 定案证据（同一会话 263/263 快照，字段路径为 `document.htmlBackground` 等）

| 层 | 实测 computed background | 真源 |
| --- | --- | --- |
| `html` | `rgba(0,0,0,0)` | `internal/ui/static/index.html` 的 `body, html` 规则原本无 `background` |
| `body` | `rgba(0,0,0,0)` | 同上 |
| `#app` | `rgba(0,0,0,0)` | `#app` 规则只有 `display:flex` + `padding: env(safe-area-inset-*)`，无 `background` |
| `#workspace` | `rgb(11,16,32)` | `background: var(--term-bg)`（`--term-bg: #0b1020`，全文仅定义一次） |

- **常量**：`#workspace.bottom → 可见底边` 在横屏、竖屏、软键盘展开、浮窗钉高**四种状态下恒为 20.0px**，等于 `#app` 的 `padding-bottom: env(safe-area-inset-bottom)`（实测 `paddingBottom=20px`，横竖屏同值）。
- `color-scheme` 声明在修复前**全文不存在**（仅两处 `@media (prefers-color-scheme: dark)`）→ UA 按系统默认把根画布画成白色。
- **结论**：那条始终存在、高度一致的白带 = 未被绘制的安全区 padding 透出 UA 白色根画布。属**铺底缺失**，不是高度缺失（`#app.bottom` 本就等于可见底边，任何高度算法改动都消不掉它）。浮窗出现时扩到 88.5px，是因为钉高使 `#app` 底边又比物理底高 68.5px。

### 修复（PR1a，仅铺底，零布局改动）

- `body, html` 增加 `background: var(--term-bg)` + `color-scheme: light dark`。
- 选 `--term-bg` 的依据：它在 `@media (prefers-color-scheme: dark)` 块（79–142 行）内**未被覆盖**，两套主题同为 `#0b1020`，与 `#workspace` 实测 `rgb(11,16,32)` 逐字节一致 → 安全区与钉高缺口都读作终端延续（对设计 D0-1 的 letterbox 判定）。
- 回归测试：`internal/ui/testdata/canvas_paint.test.mjs`（7 项），钉住"根画布必须是裸 token、不得为字面量、`--term-bg` 不得按主题分叉、`#workspace` 与 `#app` inset 前提不得漂移"。

### 过程事故记录（工程纪律，勿删）

首版 `canvas_paint.test.mjs` 在循环里组合了全局正则 `/([^{}]+)\{/g` 与手工 `open.lastIndex = i` 推进，导致 `node --test` **无限挂起 90 分钟无输出**；拆分单独测试各步均毫秒级，确认是二者组合的推进缺陷。重写为**一次线性扫描 + 缓存**后 7 项 0.237s 通过。教训：源码切片一律用线性扫描或既有 `sliceBlock()`，禁止在全局正则循环里手改 `lastIndex`；所有新增测试首跑必须套 `timeout`。

## 5f. 浮窗遮挡按产品决定关闭；钉高改为按遮挡比例分类（PR1b）

**产品决定（用户）**：iPadOS 允许把妙控键盘的浮窗拖到屏幕任意边角，属系统级可移动浮层，**其遮挡不做代码补偿，本项直接关闭**。

### 数据驱动的重新归因（会话 `f3ce260c2ed63af6698a03a28d1c45d7`）

| seq | vv.height | pan | inline | #app 底 | 可见底 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| 25 | 633.5 | 0 | 633.5px | 633.5 | 633.5 | 钉高正确 |
| 30→33 | 633.5 | 15→68 | 648.5→695px | 633.5 | 648.5→695 | 平移跟随正确 |
| **34/43** | **634** | **68** | **—** | **633** | **702** | **清掉是对的**（`covered = 702−(68+634) = 0`，无可遮挡） |
| 47–53 | 633.5 | 0 | 633.5px | 633.5 | 633.5 | 浮窗遮挡持续 |
| **54** | **702** | 0 | **633.5px** | **633.5** | **702** | **残留：`vv` 已恢复但 Safari 未发 resize/scroll**（reason 仅我方 `settle`），`applyVisualViewportHeight()` 无从执行 |

- **自我纠正**：上一轮把 seq34/43 判为"平移误清钉高"是**错的**。那两帧可视口已平移贴住布局底边，`covered=0`，交还 CSS 是正确的。已在测试里把该行为钉死，防止将来被"修"回去。
- **唯一真实缺陷**：seq54 的**事件缺失型残留**。浮窗既会无事件收缩、也会无事件恢复，任何为它引入的钉高都必然残留 → 与产品决定合并后的解法是**根本不为浮窗钉高**，从源头消除该类缺陷。

### 落地规则

`covered = documentElement.clientHeight − (vv.offsetTop + vv.height)`，仅当 `covered ≥ 25% × clientHeight` 才钉高。比例而非 px 常量，跨设备/横竖屏成立。实测锚点（iPad Pro 11 横屏，layout 702px）：软键盘 covered **428px = 60.9%** → 硬遮挡钉高；浮窗 covered **68.5px = 9.8%** → 不钉高。失败方向安全：把键盘误判为浮窗才会让文字压在键盘下，而把浮窗误判为键盘正是上述残留缺陷的成因。

## 5g. iPadOS 27.2 重标定：pan 必须从判断与目标中彻底摘除（产品选 A）

会话 `ios272recalfd0c34958bbd3d5c`。升级 27.2 后重测，`scale=1` 下键盘**仍然压缩**（`vv.height` 274、`clientHeight` 702、covered 428px=**61.0%**，与升级前锚点逐位复现）；`scale=0.75` 下**永不压缩**——不是系统变了，而是 zoom 守卫（`vv.scale != 1` 直接清空 inline）按设计关闭了全部补偿。

### 我上一轮改动引入的回归（键盘全程打开，`vv.height` 恒 274）

| seq | pan | inline | 事实 |
| --- | --- | --- | --- |
| 296 | 0 | 274px | ✅ 钉高正确 |
| 297–310 | 12.5→237 | 286.5→511px | 旧目标 `offsetTop+height` 追着平移增高 |
| **311** | 265.5 | **—** | covered 跌到 23.1% < 25% → 键盘还开着就把钉高清掉 |
| **362–396** | 234→0→428 | **—** | covered 涨回 428px(61%) 也不重新钉：R6「scroll 永不新引入钉高」门禁拒绝复挂 |

整场捕获中唯一一次重新钉上是 `seq328 reason=resize`。**净效果：键盘无限期压住终端底部约 428px。** 根因是把 `visibleBottom`（含 pan）当成了遮挡判断的输入。

### 产品决定 A（用户）与落地

键盘打开时终端**变矮但一行都不被遮**，竖向浏览交给终端自身滚动条，而不是平移文档。因此：

- 判断：`vv.height <= clientHeight × (1 − 25%)`，**只读 `vv.height`**。
- 目标：`vv.height`，**不再用 `offsetTop + height`**。
- R6 的 `if (!app.style.height) return;` 门禁**删除**：`offsetTop` 一旦退出公式，该门禁唯一残留作用就是"拒绝复挂已清除的钉高"，即上表缺陷。
- 边界改为闭区间，让"至少 25% 即钉"在散文与代码中读法一致。
- 新事实：**27.2 上手势期间 `window.innerHeight` 从 702 掉到 274，而 `documentElement.clientHeight` 恒为 702**——两个"布局高度"源在拖拽中不一致，故哨兵继续锁 `documentElement.clientHeight`，手势派生值一律不入判断。

### 锚点加固

`ui_test.go` 新增禁止锚点：`applyVisualViewportHeight` 的代码（剥掉 `//` 与 `/* */` 注释后）不得再出现 `offsetTop` / `visibleBottom` / `covered`；`reapplyPinnedVisualViewportHeight` 不得重新加回旧门禁。为此新增 `stripJSComments`（引号感知，URL/字符串里的 `//` 不误伤）。

## 5h. A 方案真机复测通过（会话 `choiceA-5359054bbf8d96654ca6`，244 事件 / scale=1 241）

| 验证项 | 结果 | 证据 |
| --- | --- | --- |
| 键盘打开钉高 | ✅ | seq14/25/38/532 `vv.height` 702→274，covered 428px=**61.0%**，`inline=274px` |
| 键盘收起交还 CSS | ✅ | seq18/34/518/544 `inline` 清空，`vv.height` 回 702 |
| **pan 免疫（本轮核心）** | ✅ | pan>0 帧中 `inline` **取值集合仅 {274px}**；seq350–514 共 142 个 scroll，pan 从 0→428→0 全程钉住 274px，一帧未丢 |
| 缩放豁免 | ✅ | seq4–6 `scale=0.85` 时 `inline` 恒空 |
| 键盘开/关沿均有事件 | ✅ | 10 个高度跳变沿 reason 全为 `resize`，无"静默恢复"（对比 §5g 浮窗的无事件恢复） |
| 首次拿到 `visibilitychange` | ✅ | 补齐此前缺失的生命周期事件证据 |

### A 方案完成不变量：终端末行不被遮

钉高态且 `pan=0` 受检 32 帧中，**seq25 一帧 `xtermRows.bottom=669 > 274`**。这不是缺陷而是**同帧未 refit 的固有瞬态**：`resize` 帧里我们刚改 `#app` 高度，xterm 的 refit 由 `ResizeObserver` 在下一布局帧完成。seq26 起 `xtermRows.bottom=244 ≤ 274` 并稳定保持；用户按 `Soft keyboard` 的 seq538 亦为 244。**结论：除 resize 当帧的一帧外，终端末行始终在键盘顶（274）之上，一行都不被遮——A 方案的完成不变量成立。**

### 线上产物级验证

`verify-live-choiceA.mjs` 对 `:38234` **实际响应字节**（495325B）切出 `applyVisualViewportHeight` 跑几何回放：代码零 `offsetTop/visibleBottom/covered`；六种 pan 全应答 274px；浮窗 9.8% 不钉；`scale≠1` 豁免；25% 闭区间边界两侧正确。全部通过。

### 仍未覆盖（不得宣布关闭）

Split View、dsh/reasonix iframe、iPhone/Chrome(iOS)、逐像素截图、键盘打开时整页刷新。

## 5i. 安全区 UI 设计复盘：根画布是 chrome 色，每列背景各自全出血

### 设计真源原文（`ui/sidebar-states.pen`，5 个视口稿重复标注，属刻意契约）

- `横屏刘海 + Home Indicator：env(safe-area-inset-*) 四向内容内缩。html/body/#app 同主题铺底；drawer/main/workspace 背景全出血。`
- `20px safe-area · html/body/#app 同主题；内容内缩，背景全出血`
- 变量表：`bg-root = #F4F6FA`（根画布）、`bg-sidebar = #F7F7F9`、`bg-header = #FBFBFC`、`border = #D9DDE7`、`term-bg = #1B1A23`
- 8 个 `safe-area 主题铺底（非透明白缝）` 图元：**整宽 20px 矩形，fill 全为 `$bg-root`，stroke 全为 None**（设计**没有**给这条带加描边或渐变）
- 桌面稿纵向堆叠实测：`根画布 $bg-root(0..900)` → `#header $bg-header(0..40)` → `#workspace $bg-app(40..858)` → `状态栏 $bg-header(858..880)` → `safe-area $bg-root(880..900)`

### 结论：安全区不是"一条要上色的带"

设计让它**消失**的方式是「每列把自己的背景铺到物理底，只有内容内缩」，而不是给带子填一个颜色。带子本身无描边、无渐变——任何"给带子加线条/过渡"都是设计源没有的装饰。

### 我此前两版实现都偏离了设计

| 版本 | 做法 | 偏离点 |
| --- | --- | --- |
| 第一版 | `body/html` 涂 `var(--term-bg)` | 用了**终端墨色**当根画布；设计是 `$bg-root`（chrome 中性色）。在浅色侧栏下方形成一条深色板 |
| 当前版 | `body/html` 改 `var(--bg-color)` + `#sidebar`/`#workspace` 各自 box-shadow 出血 | 符合「背景全出血，内容内缩」 |

### 落地（第三版）

- `body, html { background: var(--bg-color); color-scheme: light dark; }` —— 根画布为 chrome 色，仅兜住列未覆盖处（overscroll、zoom-out）。
- `#sidebar { box-shadow: 0 env(safe-area-inset-bottom, 0px) 0 0 var(--bg-color); }`
- `#workspace { box-shadow: 0 env(safe-area-inset-bottom, 0px) 0 0 var(--term-bg); }`
- 选 `box-shadow`（0 blur / 0 spread / 向下偏移 env）而非 `::after` 出血层：`#workspace` 是 `overflow:hidden`（xterm 需要裁剪），绝对定位在 `top:100%` 的伪元素会被裁掉，而 box-shadow 画在自身溢出上下文之外不受影响；且影子零布局影响，`.collapsed` 的 `width:0` 与抽屉 transform 全部照旧。
- 偏移量直接取 `env()`，无 px 常量（20px 只是当前 iPad 值，随设备/朝向变化）。
- 门禁：`canvas_paint.test.mjs` 扩到 12 项，钉住"根画布必须是 chrome token 而非终端墨色""两列各自用自己的墨色出血""出血不得出现 px 常量""`#workspace` 必须保留 `overflow:hidden`"。另 `TestUICopyIsEnglishOnly` 抓到 CSS 注释里的中文设计原文，已改写为英文（该仓库 UI 资产禁 CJK）。

### 若要做"可见的设计化处理"（待用户确认）

设计源目前**没有**给安全区加线条/渐变。若要加（例如带顶部分隔线、或向终端色的渐变过渡），按 `ui/README.md` 维护纪律第 4 条，必须**先改 `.pen` 设计源并重出 PNG**，再改代码；`pen` CLI 0.3.11 本机可用，符合 README 指定版本。

## 5j. 键盘形态切换的三类残留（真机反馈）与两个机制

用户实测（当前部署）：静态与触摸拖拽已正确，但仍有三种残留，且都发生在**键盘形态切换**时：

| # | 场景 | 现象 |
| --- | --- | --- |
| 1 | 平板模式已激活光标、软键盘展开中，接入妙控键盘 | 弹出一段白带 |
| 2 | 已接妙控键盘，用浮窗展开软键盘 | 延展出很宽一截白带（≈键盘高度） |
| 3 | 浮窗存在时，用妙控键盘**触摸板双指**上下滚 | 整页上滑至浮窗上沿，露出白带；**屏幕触摸不能上滑** |

### 机制一：钉高滞留（对应 1、2）

`applyVisualViewportHeight()` 只在 `visualViewport` 的 `resize`/`scroll` 上执行，而**键盘形态切换可以完全不发事件**（浮窗收缩与恢复即无事件，§5g seq47–54 已实测）。于是 `#app` 留着上一个键盘状态的 inline 高度：软键盘收起后仍是 274px，而可见区已回到 702 → 底部露出 428px 根画布 = 很宽一截白带；接入妙控键盘时同理但量级不同。

**修法**：对账（reconcile）。判断函数保持幂等并改为**仅在目标值变化时写样式**（否则 500ms 定时器会不断使布局失效并触发终端 ResizeObserver），然后：
- `pageshow` / `orientationchange`（window）、`visibilitychange` / `focusin` / `focusout`（document）各跑一次；
- 500ms 定时对账（`document.hidden` 时跳过）。

### 机制二：`wheel` 是第二扇门（对应 3）

页面锁原本只监听 `touchmove`。妙控键盘**触摸板双指滚动产生 `wheel`**，不是 `touchmove` —— 所以触摸拖拽被锁住了（用户实测"屏幕触摸不能上滑"），触摸板却仍能把整壳上滑。**修法**：对 `wheel` 加同款锁（`{passive:false}`，`ctrlKey` 豁免以保留捏合缩放）。

### 顺带确认：`body{position:fixed}` 是死路

已撤销并写进测试。它没有阻止平移，且在软键盘态因 body 仍占满视口、`#app` 被钉到键盘顶边而多出一片键盘高度画布。

## 5k. S5 失败的真因：文档比可视区高出"可滚动死区"（会话 `sop-7746830267fa01879804470f`）

首份**探针字段齐全**的真机数据（`probeVersion=p4-reconcile-wheel`，120 帧窗口）。三不变量全部 FAIL，但失败形态与之前所有轮次不同，且指向一个结构性缺陷：

```
seq433  pageTop=16.5  scrollTop=17    probe.visualBottom → BODY  rgb(249,249,249)
seq435  pageTop=56    scrollTop=56    band=112
seq440  pageTop=138   scrollTop=138   band=276
```

- `scrollTop` 与 `pageTop` **同步增长** → 这是**文档真的在滚**，不是 iOS 平移可视视口（平移时 `scrollTop` 应为 0）。
- 底部命中 **BODY**（`rgb(249,249,249)`）→ 露出的确实是根画布。
- 72/120 帧 `vv.offsetTop>0`，42 帧底部命中 BODY。

### 根因

`#app` 已被正确钉到 274px（键盘态），但**`html/body` 仍是布局视口的 `height:100%` = 702px**。于是文档比可视区高出 `702 − 274 = 428px` —— **正好是一个软键盘高度**，且这段是**真实可滚动**的：滚过去就是 BODY 浅色画布。手势锁（`touchmove`/`wheel`）对此无效，因为它不是"手势被放行"，而是文档本来就有滚动范围。

### 修法（结构性，非手势）

> **⚠️ 本节的两条手段已被否决，保留为分析记录，不是当前契约。** "用 JS 写 `documentElement.style.height` / `body.style.height`"与"在 `scroll` 事件里把根滚动归零"正是后来被判定为掩盖症状的做法：一个每帧被拖回顶部的页面仍然**能滑**，用户看得见带出现再被拽走。本节的**诊断成立**（文档确实有一段比可视区高、真实可滚的死区），手段已换：根两盒的 `height` / `position` 永不被 JS 改写，改为 `html` / `body` 声明 `overflow: hidden` 后接 `overflow: clip`（clip 的框没有滚动机制，死区无从产生），`#app` 以 `position: fixed; top/left: 0` 脱离普通流。见 `A0-SOP.md` 第 3 / 4 条与 `evidence/structural-check.mjs`。

让**文档盒永远不超过可视视口**：钉高时同步把 `documentElement.style.height` 与 `body.style.height` 设为 `vv.height`；恢复时清空。浮窗态（`vv.height=633.5`、不钉 `#app`）同样把根盒设为 633.5 —— 那 68.5px 也是富余量。`#app` 自身高度不变，超出根盒的部分由 `body` 的 `overflow:hidden` 裁掉（本来就被浮窗遮住）。

另加兜底：`scroll` 事件里把任何残余根滚动立即归零（有些 iPadOS 触摸板滚动走原生 web-view 滚轮，页面监听看不到）。

zoom 态整体豁免，与其余补偿一致。

## 5l. 契约 v3.1：让位加比值地板 + 状态条触底 + 滚轮隔离（PROBE `p8-full-shell-overlay-wheel-v1`）

**这一节是当前生效的判读口径，覆盖 §5f/§5g/§9 里所有"任意 shortfall 全额让位 / 没有阈值 / 633.5 走同一条算式"的老写法。** v3.1 = v3.0 底座（满高壳、根不可滚、overlay 两块让位）不变，只加三件事：

**① 遮挡的比值地板（headline）。** `applyVisualViewportOcclusion` 里 `occlusion = max(0, clientHeight − vv.height)` 之后再加一行 `if (occlusion < clientHeight * SMALL_OCCLUSION_RATIO) occlusion = 0;`，**`SMALL_OCCLUSION_RATIO = 0.15`（本轮已定，不是待判）**——线画在 68.5px=9.8% 与 147px=20.9% 这两个实测 shortfall 之间。

* **为什么加**：v3.0 把**每一个** shortfall 都全额写进宿主，结果薄薄一条妙控浮动栏（实测 `vv.height` 633.5，shortfall 仅 68.5px）也把终端顶上抬了一截——用户明确说这条薄浮窗**不许压缩终端**（"浮窗不压缩任何终端区域"）。
* **达到地板**（shortfall ≥ `clientHeight × 0.15`）是真实键盘，全额让位（"明确较大键盘遮挡仍要局部避让"那半）；**低于地板**是薄浮窗，**豁免**（欠 0，终端铺满，浮窗直接盖在一个没动过的页面上）。这个比例把三个真实例子摆在正确的两侧：
  | 视口 | shortfall | `clientHeight×0.15` | 判定 | occlusion | 宿主真实底 |
  | --- | --- | --- | --- | --- | --- |
  | 702 布局, vv 274 | 428 (61.0%) | 105.3 | 让位 | 428 | 254 |
  | 702 布局, vv 555 | 147 (20.9%) | 105.3 | **让位** | **147** | **535** |
  | 702 布局, vv 633.5 | 68.5 (9.8%) | 105.3 | **豁免** | **0**（终端满高被盖） | 682 |
  | 400 布局, vv 340 | 60 | 60 | 让位（边界含） | 60 | — |
  | 400 布局, vv 341 | 59 | 60 | **豁免** | **0** | — |
* **这是比值不是像素**：地板随 `clientHeight` 走，横竖屏/跨设备都成立。它和被 Ban 掉的 `HARD_OCCLUSION_RATIO`（0.75 悬崖，方向相反——那个是"矮键盘不让位"）是两回事；0.15 只对**低于 15% 的浅遮挡**豁免，方向和被否决的老门禁相反。
* **0.15 是怎么定下来的（本轮已定，不再是标定缺口）**：本轮先写的是 0.25，`702 × 0.25 = 175.5px` 这条地板**高于**实测 147px 那把键盘（20.9%），算术上把它判成豁免——真有一把真实键盘会因此埋进键下，这个冲突经复核成立，0.25 已撤回。地板现在画在**必须动的**与**不许动的**两个实测 shortfall 之间：不许动的下界是 68.5px（9.8%，薄浮窗），必须动的上界是 147px（20.9%，vv 555 的真实键盘），`0.15 × 702 = 105.3px` 落在中间且**两侧都有余量**（比 68.5 高 36.8px，比 147 低 41.7px）；428px（61.0%）的满键盘离地板更远，iPhone 竖屏键盘（≥ 40%）同样稳在让位一侧。再往下压到 68.5 之下会让薄浮窗重新压缩终端，正是用户明令禁止的方向；再抬到 147 之上就回到本轮撤回的那个错误。§5f 那条 25% 是**旧 `HARD_OCCLUSION_RATIO` 门禁**的阈值（`vv.height ≤ clientHeight × (1 − 25%)`，算的是可视高），与这条作用在 shortfall 上的地板不是同一个量，本节不复用它。
* **验收含义**：`≤ vv.height + 1` 这类"不许越可视底"只在**让位帧**（occlusion>0）成立；**豁免帧**里宿主/控制列/状态栏满高、真实底越界是**预期覆盖**，不是 FAIL（`verify-sop.mjs` 的 underKeyboard / controlsUnder / statusBarUnder / rows 判定都按 `owed>1` 门控）。

**② `#status-bar` 改 `position: fixed`。** 原创 `bottom:0`、让位仍写 `= occlusion`，但包含块从 `#workspace`（其 padding 底被 `env(safe-area-inset-bottom)`≈20px 内缩）换到**视口**——静止态真实底 = 视口物理底 `clientHeight`，让位态 = `clientHeight − occlusion`。`#terminal-controls` 仍 `absolute` 在 `#workspace` 内，只有状态条移动，二者原创 20px 间距与面板 `max-height:100%`（对列解析）不受影响。`structural-check.mjs` 相应改判状态条计算样式为 `fixed`。

**③ 滚轮隔离。** `initShellScrollLock` 的 document `wheel`（`{passive:false}`）只 `preventDefault` **根默认滚动**、**绝不** `term.scrollLines`；终端缓冲区滚动只归终端自己的 `initTerminalTouchScroll`。滚轮停在终端上只锁根、不双滚。

**④ 探针 `p8`。** `PROBE_VERSION = p8-full-shell-overlay-wheel-v1`（浮层标题、`viewport_debug.js`、`internal/app/viewport_debug_test.go` 两份固定 payload 同步；其中"接受任意字段"那份已改写成**豁免薄浮窗**的自洽几何：vv.height 1100、shortfall 80 < `1180 × 0.15 = 177`、occlusion 0、宿主铺满、状态条静止真实底 = 视口物理底 1180）。§5f 的 25% 分类（旧 HARD 门禁，已作废的口径）与 §5g 的 pan 摘除结论保留其历史记录地位；本节只在其上补这条 0.15 地板与那两处触底/隔离。

## 6. 真机记录模板

| 场景 | 操作 | 报告 session / seq | 关键几何与截图 | 初步判定 |
| --- | --- | --- | --- | --- |
| 静止基线 | 软键盘与妙控浮动栏均不可见 | ✅ seq1–3、46–47、337–338、739–747；第二轮 scale=1 共 262 事件 | inline 为空，app 与 vv/innerHeight 一致；`paddingBottom` 实测 20/10/0px 三值，根背景全透明 | 高度正确；safe-area/画布白缝为独立分量，**inset 不是常量，铺底必须读 `env()`** |
| 妙控浮动栏 | 底部浮动提示/输入条出现再消失 | ✅ `magic-hint` seq388（`b90643bea91477cd5d816445362604fd`） | seq385–394 全程 `vv.height=702=innerHeight`、`offsetTop=0`、`inline=""` | **已关闭：浮动栏不进 visualViewport/dvh**，属纯覆盖层；守卫不误触发即不多吃工作区 |
| 软键盘 | 终端聚焦弹出，再收起 | ✅ **`keyboard-hint` seq24**（第三轮） | `vv.height=274`、缩减 **428px**、`inline=274px`、focus=TEXTAREA | **428px = 全尺寸软键盘，已坐实**；恢复态 seq29 回全高 |
| URL 栏 | 显示 → 收起 → 显示 | ✅ 已完成 | innerHeight/vv 与 app 底边序列完整 | 不使用常量偏移；按动态可见区判断 |
| 横竖屏 | 竖屏 ↔ 横屏 | ✅ seq651 及后续方向变化 | portrait/landscape 与窗口高度重算完整 | 旧基线必须随方向失效 |
| Split View / 窗口 | 1/2、1/3 或可调窗口 | ✅ 第二轮 `innerWidth` 592×112、392×34、618×12 | 窄宽下 innerWidth/innerHeight 与钉高态完整 | 基本关闭；Stage Manager 仍未测 |
| 缩放与平移 | 双指/双击缩放、平移、回 scale=1 | ✅ 75% 与 100%；seq305–312、330、333、624、736；第二轮 seq190 | 负 app.top 与底边误差逐点对应；无 inline 时 340/428/44 错位；scale=0.75 静止段 app 溢出可见底 91.3px | 坐实 `visibleBottom-app.top` 与准入守卫；**zoom 豁免的 91.3px 溢出属固有代价，待产品拍板** |
| 子层 | pty、opencode/reasonix/dsh iframe、quick-keys | ✅ pty + opencode-web；**quick-keys seq126–140（`quickKeysOpen=true`）** | xtermRows 与 workspace 余量分开；钉高态下 webPanel 同步 | quick-keys 同场闭合；仍缺 dsh/reasonix |
| 生命周期 | 前后台切换、键盘已打开时刷新 | ❌ 第二轮 `pageshow`/`visibilitychange` 为 0 | 本轮未触发该两项 | 未采就是未采，不记为通过 |

## 7. 快检判读（v3.1 轴；旧的"壳缺高 = 钉高没生效"读法已作废）

> 本节按契约 v3.1 判读（v3.0 底座 + `SMALL_OCCLUSION_RATIO=0.15` 地板，702 布局 = 105.3px，见 §5l）。v2.1 / v2.2 时代"`vv.height − app.bottom` 为负 → 外壳缺高 → 该钉没钉"这条判读**整体作废**：满高壳本来就要伸到键盘底下，负值是契约生效的样子。判读轴换成两问：**壳动不动**（`app.bottom` 对 `clientHeight`）与**终端让没让**（每个宿主的 `bottom` 对**地板后的** `occlusion`）。

- `layoutGap = clientHeight − app.bottom` 明显不为 0（尤其等于一个键盘高度）：**壳被动了**——v2.2 的整页钉高复活，或有人又给 `#app` 写了 inline height（`app.inlineHeight` 必须恒为空）。这是 FAIL，不要再去"补钉"。
- `vv.height − app.bottom` 为负：**不是缺陷，是预期**（键盘浮在满高页面之上）。旧文档里凡是把它写成"外壳缺高"的，都按本条重新理解。
- `occlusion`（**地板后** = shortfall ≥ `clientHeight×0.15` 取 shortfall、否则 0）与宿主的 `inlineBottom` 不一致，或 `ptyHosts[]` 里出现两个不同的 inline 值：局部避让漏了宿主 / 隐藏宿主留了旧值 —— 后者就是切过去那一瞬的白带。
- 差值约为 0，但白带高度接近 `app.paddingBottom`：优先 safe-area/透明画布，不能据此改高度算法。
- **让位帧**（occlusion > 0）里宿主 `bottom` 已抬起、`xtermRows.bottom` 仍 > `vv.height + 1`：检查 xterm fit/行取整，不要靠继续加大宿主 `bottom` 或缩短 `#workspace` 补偿。（**豁免帧**——薄浮窗 shortfall < 地板——里 occlusion 归 0、终端满高被盖，真实底越界是预期，不判此项。）
- 妙控浮动栏在场而 `vv.height == clientHeight`（seq388）：shortfall 0 ⇒ `occlusion` 天然 0。v3.1 起**薄浮窗**（633.5 → 68.5 = 9.8% < 105.3 地板）也落到 `occlusion 0`——不过是走比值地板，同样不需要为它找"硬/软"分类；分类门禁（旧 25% / 0.75）继续作废。注意 555 → 147 = 20.9% **不属这一条**：它在 105.3 地板之上，必须让位到 147px。
- vv 与 dvh 相对截图同时短报：先找独立几何基准；不以常量偏移冒充精确修复。

## 8. 真机环境与剩余未覆盖项

- iPad 型号：**iPad Pro 11 英寸（第二代），MY322CH/A**。
- iPadOS 版本：**27.0.1**（与报告 UA 中 Safari 27.0.1 一致）。
- 浏览器：**Safari**，主要矩阵在 100% zoom 下完成，首轮另有 75% zoom 数据。
- 测试构建：`fix/fix-issue116` 基线 `225c5d7` + `viewportdebug` 编译标签诊断实现；**尚未包含高度算法修复**。
- 访问方式：**Tailscale**，地址 `100.86.87.23:40751`（第二轮；首轮为 `:38231`）；同会话报告通过 daemon `GET /api/viewport-debug/report` 读取。主 UI 端口随 daemon 启动而变（默认 `0.0.0.0:0`），每轮记录实际端口。
- 妙控键盘：iPad 配套妙控键盘，**无额外型号区分需求**；分析关注其系统浮动提示/输入窗，而非键盘硬件型号。
- 会话：第一轮 `d5222b167a61be314fdba725fcfedff3`（latestSeq=747）；第二轮 `b90643bea91477cd5d816445362604fd`（latestSeq=622，本地留存 877 事件于 `evidence/raw/`）。
- 原始证据：首轮 74 个事件、seq98–343 的 100% 平移数据、seq624–747 的末段数据，以及本文件中的几何差值和用户操作说明。
- **已由第二轮关闭**：Split View（`innerWidth` 592/392/618）、opencode-web iframe 子层（138 事件）、`magic-hint` 遮挡分类（seq388）、scale=1 静止基线补采（262 事件）、safe-area 非常量（20/10/0px）。
- **剩余未覆盖**：`keyboard-hint` 与 68.5/157/206.1/428 缩减量的命名；quick-keys 同场（此前被诊断浮层遮挡，浮层已修待重采）；键盘打开时刷新（第二轮 `pageshow`/`visibilitychange` 为 0）；Stage Manager；dsh/reasonix iframe；竖屏静止基线（仅 8 事件）；iPhone、Chrome(iOS) 及其他 iPadOS 版本；实体截图逐像素比对。这些项目在矩阵补齐前不得标为通过。
- **待产品拍板**：scale≠1 时 `#app` 按 `100vh` 铺到 936 而可见底为 844.7，**溢出 91.3 layout px**（终端最后几行落在可见区外）。这是 zoom 豁免的固有代价而非回归；是否给缩放态建基准需单独决定，不得顺手用常量偏移掩盖。
- **诊断浮层缺陷（A0 工具侧，已修）**：见 §5b 与 `evidence/panel-check.mjs`、`evidence/panel-occlusion.mjs`。修前 `tr` 角下 `#quick-keys-toggle` 的 `elementFromPoint` 命中者为 `viewport-debug-panel`；修后四角均可命中。

## 9. v3.1 的重新判读（参考应用对照 + 新验收轴；地板见 §5l）

用户在**同一台 iPad**上跑了一次真参考应用（Gmail）作为对照，事实按用户提供记录为真：

1. 无活动输入时布局铺满视口，没有白带，整页滑动**永远露不出**白带。
2. 有活动输入时，妙控键盘的浮动面板 / 软键盘在视觉上是**浮在上面**的，透过它们看到的页面与键盘弹起之前**一模一样**。
3. Gmail 随后会长出一段**默认隐藏、高度约等于键盘高度**的底部区域，**只有手动整页滑动才能看到**，失焦后消失。

据此本契约只抄前两条：

| 参考应用的做法 | v3.0 采纳？ | 落点 |
| --- | --- | --- |
| 布局恒满高，键盘作为覆盖层浮在页面之上 | ✅ 采纳 | `#app` `position: fixed` + `top/left: 0` + `100vh` → `@supports 100dvh`；`app.bottom` 恒对 `clientHeight`；`inlineHeight` 恒空 |
| 透过键盘看到的仍是同一份布局 | ✅ 采纳 | 键盘/浮窗不改壳、侧栏、页面布局；只允许 PTY 宿主 `bottom` 变化 |
| 键盘下方多长出一段隐藏、可整页滑出的区域 | ❌ **明确拒绝** | `html` / `body` `overflow: hidden` → `clip` + 壳脱流 ⇒ 根**没有可滚范围**（`rootScrollHeight ≤ clientHeight`），滑不出、也拉不回；比参考应用更严 |

新的验收轴（`evidence/verify-sop.mjs` p8 的不变量就是这个表的机器版）：

| 观测量 | v3.1 期望 | 旧的错误判读 |
| --- | --- | --- |
| `layoutGap = clientHeight − app.bottom` | ≈ 0（每个键盘态） | — |
| `vv.height − app.bottom` | **可为负，不判失败** | 旧版把负值记作"越底/crossing"FAIL —— 已作废 |
| `app.inlineHeight` | 恒为空 | 旧版要求它等于 `vv.height` —— 已作废 |
| `occlusion` | **地板后** shortfall：shortfall ≥ `clientHeight×0.15` 取 shortfall、否则 0；zoom 为 0 | 旧版有 25% / 0.75 阈值与硬/软遮挡分类 —— 已作废（v3.1 的地板只对薄浮窗豁免，方向相反） |
| `ptyHosts[i].inlineBottom` | 每个宿主都等于 `occlusion + "px"`，且彼此相同 | 旧版只看壳，看不见隐藏宿主的残留值 |
| 宿主真实 `bottom` | **仅让位帧**（occlusion>0）≤ `vv.height + 1`（实测 682 − occlusion）；豁免帧满高越界是预期 | — |
| `controls.inlineBottom` | `20 + occlusion`（原创 `bottom:20px` + 让位），稳定帧；缺失 → INCONCLUSIVE | 旧版只核对宿主，看不见快捷键是否压在键盘底下 |
| `controls` 真实 `bottom` / `height` | 让位帧 ≤ `vv.height + 1`；列高按 occlusion 缩短，`top:20px` 恒定（入口不跳） | 把宿主值 `occlusion` 抄给控制列会少抬 20px，快捷键埋进状态栏带 |
| `statusBar` | **`position: fixed`**（v3.1，钉视口物理底）；`inlineBottom` = `occlusion`（原创 `bottom:0`），稳定帧；让位帧真实底 ≤ `vv.height + 1` | 旧版是 `absolute` 包在 `#workspace` 里，真实底被 safe-area 内缩一截 |
| `quickKeys`（`maxHeight` / `clientHeight` / `scrollHeight`） | 面板高度 ≤ 列高且随列高缩短；`scrollHeight > clientHeight` 说明内部滚动仍在 | 面板钉在原高度 ⇒ 键位伸到键盘底下 |
| 根 `scrollTop` / `rootScrollHeight` | 0 / ≤ `clientHeight`（既不能滚，也没有可滚范围） | 旧 v2.1 允许滚然后 `scrollTo(0,0)` 拉回 —— 已永久撤回 |
| `wheel`（滚轮） | document `wheel` 只 `preventDefault` 根默认、**绝不** `term.scrollLines`；终端滚动只归 `initTerminalTouchScroll` | 滚轮在终端上触发 `term.scrollLines` → 双滚 |

实测回放（702 布局 / 宿主原底 682 / 地板 = `702×0.15 = 105.3px`）：`vv.height` 274 → shortfall 428 ≥ 地板 → 宿主 `bottom: 428px` → 254；**555 → shortfall 147 = 20.9% ≥ 地板 → 让位 → `147px` → 宿主真实底 535**；**633.5 → shortfall 68.5 = 9.8% < 地板 → 豁免 → `0px` → 宿主铺满 682**（浮窗盖在一个没动的满高终端上）；702（真浮窗纯覆盖，seq388）→ `0px` → 682。让位那两档终端可见几何与 v2.2 逐像素相同，**区别只在于被移动的是宿主而不是壳**；只有 633.5 这一档 v3.1 起不让位。

本轮已定（不再是待判事项）：局部避让的范围是 **PTY 宿主 + 终端控制列 + 状态栏**，三者各自写自己的 `bottom`（状态栏 v3.1 起 `fixed` 钉视口底）。`#workspace` 恒为满高，两块贴底 overlay 若不跟着抬，快捷键与传输状态条就压在键盘底下——而输入框与快捷键始终可达是原始需求本身，不是可选项。算式与实测（702 布局 / 宿主原底 682 / 控制列原创 `top:20px` `bottom:20px` / 地板 105.3）：

| 目标 | 算式 | 274 | 555 | 633.5（< 地板→豁免） | 702（occlusion 0）与缩放态 |
| --- | --- | --- | --- | --- | --- |
| PTY 宿主 `bottom` | `occlusion`（地板后） | `428px` → 254 | `147px` → 535 | `0px` → 682 | `0px` → 682 |
| `#terminal-controls` `bottom` | `20 + occlusion` | `448px` | `167px` | `20px`（原创值） | `20px`（原创值） |
| `#status-bar` `bottom`（`fixed`） | `occlusion` | `428px` | `147px` | `0px` | `0px`（原创值） |

`#terminal-controls` 的 `top:20px` **永不写**，所以快捷键入口按钮不动，缩短的只有列高；`#quick-keys-panel` 的 `max-height:100%` 随列高一起缩短，其余键位在盒内滚动。**448 与 428 必须不同**：把宿主值复制到控制列上会让快捷键埋进状态栏那条 20px 带里，这正是 `verify-sop.mjs` 第 12 项专门核对的点。
