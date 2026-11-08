# A0 真机验证 SOP（issue #116 · 契约 v3.1「满高壳 + PTY 局部避让（0.15 比值地板）」）

> **v3.1 = v3.0 底座（满高壳 + 宿主局部让位 + 根不可滚 + overlay 两块）之上，新增三件事：**
> ① 让位加了一道**比值地板** `SMALL_OCCLUSION_RATIO = 0.15`（本轮**已定**，标定在 68.5px=9.8% 与 147px=20.9% 这两个实测 shortfall 之间，两侧各留 36.8px / 41.7px 余量；702 布局地板 = **105.3px**）——遮挡 shortfall **达到** `clientHeight × 0.15` 就是真实键盘，把原始 shortfall 全额写进宿主（274 → 428、555 → 147）；**低于**它的是一条薄浮窗带，**豁免**（欠 0，宿主铺满、浮窗直接盖在一个没动过的页面上——"浮窗不压缩任何终端区域"就是这条，实测 633.5 → 68.5）。地板本轮从 0.25 改成 0.15：0.25 在 702 布局上是 175.5px，**高于**实测 147px 那把键盘，会把它错误豁免。② `#status-bar` 由 `absolute` 改成 `fixed`，钉在**物理视口底**，不再继承 `#workspace` 那条 `env(safe-area-inset-bottom)`（~20px）而差一截。③ 滚轮隔离：`initShellScrollLock` 的 `wheel` 只 `preventDefault` 根默认滚动、**绝不** `term.scrollLines`，终端缓冲区滚动只归终端自己的触摸拖拽管，滚轮盖在终端上也不会双滚。
> 这一版和 v3.0 一样：壳满高、根不可滚那两条底座不变，下面凡是讲"任意 shortfall 全额让位 / 没有阈值 / 633.5 走同一条算式"的老口径，一律以 v3.1 的比值地板为准。

> 每个步骤做完按一次浮层上的 **Capture**，我按本文对账。
> 关键前提：浮层标题必须是 **`Viewport diagnostics · p8-full-shell-overlay-wheel-v1`**。
> 若标题里没有 `· p8-...`，说明跑的是旧代码 → 关掉标签页重开（服务端已禁缓存）。
> 机器半：`node evidence/verify-sop.mjs --file evidence/raw/<id>.jsonl`（或 `--session/--base`）；`--self-test` 跑内置 fixture，不需要网络。

## 这一版和上一版最大的不同（先读这段再动手）

v2.1「固定壳 + 根原点」的做法是：根**能**滚，但每次判定后把它 `scrollTo(0, 0)` 拉回原点，
并在下一帧、500ms 定时里反复拉。真机反馈这是**掩盖症状**：带先出现、再被拽走，用户看得见
"页面被拖回顶部"。**该机制已整体删除**（不是改名、不是加开关关掉）：没有 `resetRootScrollOrigin`、
没有 `window.scrollTo`、没有根 `scrollTop = 0`、没有为了回顶而挂的根 `scroll` 监听、没有为此而排的 rAF。

v2.2「脱流固定壳 + 根不可滚」把**整个壳**钉到 `vv.height`：键盘出现 → `#app` 内联高度 = `vv.height`。
结构那半保留，钉高那半**已被删除**。删除的依据是你在同一台 iPad 上做的对照实验：**真 Gmail 的布局
从头到尾是满高的，键盘是浮在上面的**——键盘弹起时从键盘缝里看到的，就是键盘弹起之前那一模一样的
完整布局；Gmail 只是在键盘下方**多长出一段默认隐藏、需要整页滑动才能看到**的区域，失焦/收起后又消失。
本设计**照抄前一半（满高布局 + 键盘覆盖），并且比 Gmail 更严：明确不抄后一半**——根永远没有滚动区，
那段"能滑出来的隐藏区域"在这里根本不存在（`overflow: clip` + `#app` 脱流 ⇒ 没有滚动范围，滑不出来也拉不回来）。

所以本 SOP 里判定基准整体换轴：

* 壳**不再变矮**：`app.bottom` 永远对 `documentElement.clientHeight` 负责（实测 702），验收看
  `layoutGap = clientHeight − app.bottom ≈ 0`，**不再看 `vv.height − app.bottom`**。
  **旧口径的负数（"越过可视底"）从今天起不是失败，而是本契约主动要的结果**：壳就是要伸到键盘底下。
* 让位的是**终端宿主自己**：`#terminal-container` 内每个 PTY 宿主的 `style.bottom` =
  `occlusion = max(0, clientHeight − vv.height)`，**再按比值地板收一次**：`if (occlusion < clientHeight × 0.15) occlusion = 0`（v3.1；宿主本来就是 `position:absolute; inset:0`）。
  实测 702 布局、宿主原底 682，地板 = `702 × 0.15 = 105.3px`：`vv.height` 274 → shortfall 428 ≥ 105.3 → `bottom: 428px` → 宿主真实底 254；
  **555 → shortfall 147 = 20.9% ≥ 105.3 → 让位 → `bottom: 147px` → 宿主真实底 535**（控制列 167px、状态栏 147px）；**633.5 → shortfall 68.5 = 9.8% < 105.3 → 豁免 → occlusion 0 → 宿主铺满、bottom 回到 `0px`**（浮窗盖在一个没动的满高终端上，这正是用户要的）。达到地板那档的终端几何与旧钉高**完全一致**，但 `#app` / `#main` / `#workspace` / 侧栏 / header **一像素都不动**。
* **S5 / S6 的期望仍是"根本推不动"**（不是"被拉回 0"）：非 0 的根滚动量仍然是 FAIL，而这个构建
  结构上就不会产生它。验收话术必须区分：**"滑不动"是通过，"滑了一下又被拉回"是失败。**

## 本构建的契约

1. **壳永远满高**：scale=1 时 `occlusion = max(0, documentElement.clientHeight − vv.height)`，**再按 v3.1 比值地板收一次**：shortfall **达到** `clientHeight × SMALL_OCCLUSION_RATIO (0.15)` 全额写进每个 PTY 宿主的 `style.bottom`；**低于**它则 `occlusion = 0`、宿主写回 `bottom: 0px`（薄浮窗豁免）。`#app` 的 inline height **在验收里必须恒为空**——非空即"旧钉高回来了"，直接 FAIL。这里**仍然没有** v2.x 那个"低于某高度就不让位"的 `HARD_OCCLUSION_RATIO` / 0.75 悬崖（方向相反，已被 Ban 掉）；v3.1 的地板只对**低于 15% 的浅遮挡**豁免：274（428 = 61.0%）与 555（147 = 20.9%）都 ≥ 105.3 地板 → 全额让位，只有 633.5（68.5 = 9.8%）豁免为 0。
2. **真妙控浮窗天然为 0**：人工 `magic-hint` seq388 实测 `vv.height = innerHeight = clientHeight = 702`、`offsetTop = 0` ⇒ shortfall 0 ⇒ `occlusion = 0`（算术结果）。v3.1 之后**薄浮窗（633.5 → 68.5 = 9.8% < 105.3 地板）也落到 occlusion 0**，走的是比值地板而非 shortfall=0；而 555 → 147 = 20.9% **在地板上，它让位**，不归这一条；两者都让终端铺满、浮窗直接盖上去。**判据只看 shortfall 与地板的大小关系，不再问"这是不是妙控浮窗"这种分类。**
3. **根不可滚（结构，不是行为）**：`html` / `body` 计算样式必须 `overflow: clip`（源码 `hidden` 在前、`clip` 在后，后者生效）；`#app` 必须 `position: fixed` 且**满高**；`document.rootScrollHeight ≤ clientHeight`（没有可滚范围）。p8 探针每次上报 `htmlOverflow` / `bodyOverflow` / `computedPosition` / `rootScrollHeight` / `layoutGap`——**只报 `scrollTop = 0` 不足以证明滑不动**，那只能证明"此刻还没动"。
4. **能被移动的只有终端自己的三个盒子**：`ptyHosts[]` 列出 `#terminal-container` 内**每一个**宿主（含隐藏/非活动）的 `bottom`、自己写入的 `inlineBottom` 与 `visible`。**所有宿主的 inlineBottom 必须相同**——隐藏宿主留着旧 bottom 就是"切过去那一刻的白带"。同一次避让还写另两个贴底的 overlay 子节点：`#terminal-controls`（快捷键入口/面板所在控制列）`bottom = 20 + occlusion`（`absolute`，包含块是 `#workspace`，不缩短，不抬就压在键盘底下）；`#status-bar` `bottom = occlusion`，但 v3.1 起它是 `position: fixed`——包含块是**视口**，原创 `bottom:0` 就是**物理视口底**，不再继承 `#workspace` 的 `env(safe-area-inset-bottom)` 而差一截。**只在"让位帧"（shortfall ≥ 地板）里要求真实底 ≤ vv.height + 1**；豁免帧（薄浮窗）里宿主/控制列/状态栏本就该保持满高、被浮窗盖住，那底下越界是**预期**，不是 FAIL。控制列的 `top:20px` **永不写**（入口按钮不许跳），缩短的只有列高，面板 `max-height:100%` 随列收缩并在盒内滚。`html` / `body` / `#app` / `#workspace` / `#terminal-container` 的尺寸与位置脚本一律不写。**终端内部滚动区（`.xterm-viewport`）、侧栏 `#wt-list`、弹窗内部照旧必须能滚**（S8），脚本一列 `scrollTop` 都不写。
5. **zoom 豁免**：scale ≠ 1 时 `occlusion = 0`、宿主回 `bottom: 0px`，**不做任何补偿、也不碰根位置**，不参与下面任何判定。
6. **验收读几何与结构**：`app.bottom` / `layoutGap`、`occlusion`、每个 `ptyHosts[i].bottom` 与 `inlineBottom`、`xtermRows.bottom`、根 `scrollTop` 与第 3 条的结构字段。只看 inline 有没有被写过不算验收。

## 步骤

| # | 操作 | 期望 | 我会核对的量 |
| --- | --- | --- | --- |
| S1 | 什么都不开（无软键盘、无浮窗、静止）→ `Capture` | 底部无任何色带；终端铺到物理底 | `app.inlineHeight` 为空；`layoutGap = clientHeight − app.bottom`，\|layoutGap\| ≤ 1；`occlusion = 0` 且每个宿主 `inlineBottom == "0px"`；根 `scrollTop = 0`；`htmlOverflow/bodyOverflow == clip`、`computedPosition == fixed`、`rootScrollHeight ≤ clientHeight` |
| S2 | 点终端唤起**软键盘** → `Soft keyboard` → `Capture` | 页面布局**完全不变**（键盘浮在满高页面上），**一行终端文字都不被压住**；底部无白带 | `app.bottom == clientHeight`（`layoutGap ≈ 0`）、`inlineHeight` 仍为空；`occlusion == 让位 shortfall`（软键盘 shortfall ≥ 地板，等于 `clientHeight − vv.height`）；每个宿主 `inlineBottom == occlusion+"px"` 且**宿主真实底 ≤ vv.height + 1**（实测 682−occlusion）；`xtermRows.bottom ≤ vv.height + 1`；根 `scrollTop = 0` |
| S3 | 保持软键盘 → **接入妙控键盘** → 等 2 秒 → `Capture` | 底部不残留上一键盘状态的带；**并且页面不会自己滑一下** | `layoutGap ≈ 0`；`occlusion` 与当时 `vv.height` 一致，宿主 bottom 随之改写；根 `scrollTop` 全程 = 0（**这里没有"先动了再拉回"**：非 0 即 FAIL） |
| S4 | 已接妙控键盘 → 用**浮窗**展开软键盘 → 等 2 秒 → `Capture` | 不出现键盘高度的宽白带；薄浮窗（实测 `vv.height` 633.5）**盖在满高终端上不压缩它**；若 shortfall 真的达到地板（较大的键盘）才只有终端自己的三个盒子让位，壳始终不动 | `layoutGap ≈ 0`；**shortfall ≥ `clientHeight × 0.15`（702 布局地板 105.3px）时** `occlusion > 0` 且宿主 `bottom == occlusion`（**555 → 147 就在这一侧，必须让位**）；**shortfall < 地板（633.5 → 68.5 = 9.8%）时 `occlusion == 0`**、宿主 `inlineBottom == "0px"`、终端满高被浮窗盖住；`vv.height == clientHeight`（真浮窗纯覆盖）时 `occlusion == 0`；根 `scrollTop = 0` |
| S5 | 浮窗存在时，在**实例区外**用**触摸板双指**上下滚 → `Capture` | 页面**推不动**（不是"推得动但被拉回"） | 根 `scrollTop = 0`；`rootScrollHeight ≤ clientHeight`（**没有可滚范围**，Gmail 那种能滑出来的隐藏区在这里必须不存在）；`pageTop` / `vv.offsetTop` 只作旁证 |
| S6 | 同上但用**手指触摸**在实例区外拖 → `Capture` | 页面**推不动** | 根 `scrollTop = 0`；`pageTop` / `offsetTop` 仅记录；若出现"滑动后回弹/回顶"的观感，**记为 FAIL 并写明帧号** |
| S7 | 收起软键盘 / 断开妙控键盘 → 等 2 秒 → `Capture` | 恢复满高铺底，无残留 | `occlusion == 0`；每个宿主 `inlineBottom == "0px"`、宿主真实底回到 682；`app.bottom == clientHeight`；`inlineHeight` 为空；根 `scrollTop = 0` |
| S8 | 终端内部用手指上下滚；侧栏/监控弹窗上下滚 | **必须仍能滚**（根不可滚不得误伤内部滚动） | 不做数值断言，肉眼确认即可；若滚不动即为回归，直接判 FAIL |

## 我这边自动核对的不变量（`verify-sop.mjs`）

全部在 **client 坐标**里读，不再把任何 offset 加回 rect：

1. **壳满高**：`layoutGap = clientHeight − app.bottom`，\|layoutGap\| > 1 即失败（壳被缩短了——旧 v2.2 钉高回来的形状）。判定用**原始 CSS px**，只有打印出来的表格才 round。
   **`vv.height − app.bottom < 0` 不再是失败**：满高壳本来就伸到键盘底下，这条旧判据（"越底/crossing"）已作废。
2. **`#app` 没有内联高度**：`app.inlineHeight` 必须恒为空字符串；非空（例如 `633.5px`）= 旧的整页钉高复活 → **FAIL**。
3. **局部避让对账（含 v3.1 比值地板）**：scale=1 时 `occlusion` 必须等于**地板后的 shortfall**——`raw = max(0, clientHeight − vv.height)`，`raw ≥ clientHeight × 0.15`（702 布局 105.3px）取 `raw`，否则取 `0`；每个 PTY 宿主的 `inlineBottom` 必须等于 `occlusion + "px"`；且**所有宿主取值必须一致**（隐藏宿主残留旧值 → FAIL，因为那是切换瞬间的带）。**只有让位帧（occlusion > 0）**才要求宿主真实底 `≤ vv.height + 1`；**豁免帧（浅遮挡，occlusion 被地板归 0）**里宿主保持满高、真实底越界是**预期覆盖**，不是 FAIL。zoom 帧豁免本项。
4. **根不滚、且没有滚动范围**：判 `document.scrollTop` 与 `document.scrollingElementScrollTop`（容差 0.5），并要求 `rootScrollHeight / scrollingElementScrollHeight ≤ clientHeight + 0.5`。非 0 → **FAIL，且本构建不会自动恢复**：v2.1 的原点对账已删除，这里出现非 0 说明结构漏了，不是"iOS 自己会修好"。`pageTop` / `vv.offsetTop` 仅 informational。
5. **底部像素归属（辅助证据，不是终审）**：`probe.visualBottom` 命中 `BODY` / `HTML` = 根画布真被露出的**强证据** → 直接判 FAIL。命中其他 DOM（终端宿主、`#workspace`，也包括 `.xterm-screen` 这类本身透明的元素——它们是**常见合法命中**）**只是辅助信息，不足以证明实际像素归属**：非 `BODY`/`HTML` 命中既不判失败，也不等于"没有带"。非 zoom 帧缺 `probe` 或缺 `probe.visualBottom` → 该项**未观测**，整轮 **INCONCLUSIVE**。**真正的视觉验收仍以把诊断面板移开底部后拍下的真机截图为准**（面板自己就压在它要证明的那条边上）。
6. **末行可达**：**稳定帧**（`manual` / `settle` / `periodic` / `keyboard-hint` / `magic-hint`，排除 `resize` / `scroll` 过渡）且有 `xtermRows` 时，`xtermRows.bottom ≤ vv.height + 1`。
7. **结构不可滚**：`htmlOverflow` / `bodyOverflow` 必须 `clip`（报 `hidden` 即 FAIL——`hidden` 仍是一个滚动机制，只是 iOS 不理它）；`app.computedPosition` 必须 `fixed`；`rootScrollHeight ≤ clientHeight + 1`。这些字段缺失（旧仪器）→ **INCONCLUSIVE**：`scrollY = 0` 本身不能证明滑不动。
8. **快捷键与状态条落在键盘之上（稳定帧）**：`controls.inlineBottom` 必须等于 `20 + occlusion`、`statusBar.inlineBottom` 必须等于 `occlusion`（`#status-bar` v3.1 起 `position: fixed`，但原创 `bottom:0` + 让位写 `occlusion` 不变，两个数**必须不同**，抄成宿主值即 FAIL）；**只有让位帧（occlusion > 0）**才要求二者真实底 `≤ vv.height + 1`，豁免帧里二者满高被浮窗盖住是预期；`#quick-keys-panel` 的 `clientHeight` 必须 `≤` 列高（`max-height:100%` 的解析结果）且列高按 occlusion 缩短。`.oc-hidden`（web kind）隐藏的列跳过几何判定，不误判零 rect；整帧缺 `controls` / `statusBar`、或整轮从未出现可见列 / 打开的面板 → **INCONCLUSIVE**（缺证据不等于通过）。zoom 帧与 `resize` / `scroll` 过渡帧豁免本项。
9. **证据完整性**：缺 `probeVersion`、缺关键字段（`vv.height` / `app.bottom` / 根 `scrollTop` / `scrollingElementScrollTop` / `occlusion` / `controls` / `statusBar`）、缺第 7 项的结构字段、非 zoom 帧缺 `probe` 或 `probe.visualBottom`、stamp 不是 `p8-full-shell-overlay-wheel-v1`、seq 重复或有缺口 → 明确 **INCONCLUSIVE**（不得静默 PASS）；有第 1–8 项的实测失败 → **FAIL**；全部干净且证据齐全才 **PASS**。退出码 0/1/2 对应 PASS/FAIL/INCONCLUSIVE。
10. **最后一眼是人眼**：以上全是几何与命中的间接证据。跑完请把诊断面板**拖离屏幕底部**，再对每个状态（静止 / 软键盘 / 妙控接入 / 浮窗展开 / 收起）各拍一张截图——第 5 项的探针只能说明"命中了谁"，面板压在底部时它拍不到自己盖住的那条边。

## 已知的"允许失败"边界与本轮有意保留的后果

- **缩放态**（`scale ≠ 1`）整个豁免：不写宿主 bottom、不碰根位置，也不参与判定（豁免的固有代价见 DATA-COLLECTION 的 91.3px 溢出记录）。
- 键盘/浮窗**动画当帧**可能有一帧 `xtermRows` 尚未 refit（`ResizeObserver` 下一帧完成），所以 `resize` / `scroll` 帧不纳入第 6 项；但它们的 `layoutGap`、`occlusion` 与根滚动仍然要落回干净。
- **局部避让的范围 = PTY 宿主 + 终端控制列（`#terminal-controls`，含快捷键入口与面板）+ `#status-bar`，三者各自写自己的 `bottom`**。这是本轮已定的契约，**不再是待判事项**：`#workspace` 恒为满高，这两块贴底的 overlay 若不跟着抬，快捷键与传输状态条就仍压在键盘底下——而输入框与快捷键始终可达正是原始需求本身。算式：`occlusion = clientHeight − vv.height`；宿主 `bottom = occlusion`；`#terminal-controls` `bottom = 20 + occlusion`（保留它原创的 20px 状态栏间距；`top:20px` **永不写**，所以快捷键入口按钮不移动，缩短的只有列高，面板的 `max-height:100%` 随列高收缩并在盒内滚动）；`#status-bar` `bottom = occlusion`（原创 `bottom:0`）。702 布局（地板 = `702 × 0.15 = 105.3px`）：`vv.height` 274 → shortfall 428 ≥ 地板 → 宿主 428px / 控制列 448px / 状态栏 428px；**555 → shortfall 147 = 20.9% ≥ 地板 → 让位 → 宿主 147px / 控制列 167px / 状态栏 147px（宿主真实底 535）**；**633.5 → shortfall 68.5 = 9.8% < 地板 → 豁免 → 宿主 0px / 控制列回到 20px / 状态栏 0px**（终端满高、浮窗盖上去）；`occlusion` 0 与缩放态 → 宿主 0px、控制列 20px、状态栏 0px。**448 与 428 必须不同**（555 那档同理：167 与 147 必须不同）：把宿主的让位值直接复制到控制列上，会让快捷键埋进状态栏那条 20px 带里。地板 **0.15 是本轮已定值，没有待拍板的阈值**——先写的 0.25 把 702 布局地板抬到 175.5px、压在实测 147px 键盘之上把它错误豁免，已在代码 / 测试 / 真机判据里一并撤回。壳（`#app` / `#main` / `#workspace` / 侧栏 / 顶栏）与其余区域一律不动、仍然满高。S2 / S4 请顺带核对控制列与状态栏是否都落在键盘之上。
- **滚轮隔离（v3.1）**：`initShellScrollLock` 装了 `{ passive: false }` 的 document `wheel` 监听，`preventDefault` 的只是**根页面默认滚动**（触控板/鼠标滚轮绝不能把整页滚走）；它**绝不调用** `term.scrollLines`。终端缓冲区的滚动只归终端自己的触摸拖拽 `initTerminalTouchScroll`（`term.scrollLines`）。所以滚轮停在终端上**只会锁住根、不会双滚**；终端内容仍靠触摸拖动滚动。真机核对：在终端里用妙控板双指滚——页面不整页滚、终端按拖动滚动，二者不叠。
- **`#status-bar` 改 `fixed` 的后果（v3.1）**：传输状态条现在钉在**物理视口底**，不再随 `#workspace` 的 `env(safe-area-inset-bottom)` 内缩——静止态它的真实底 = 视口物理底（`clientHeight`），让位态 = `clientHeight − occlusion`。`#terminal-controls` 仍是 `absolute` 包在 `#workspace` 里，两者因此不再共用同一包含块；控制的 20px 原创间距与面板 `max-height:100%` 仍对**列**解析，不受影响。headless（safe-area=0）下二者真实底之差仍是 20px；真机（safe-area≈20）下状态条比控制列底更靠下一截，这是把状态条推到物理底的**预期**结果。
- Chrome / CDP 的结构自检（`evidence/structural-check.mjs`）只能证明**结构**成立（含"写进宿主的 `bottom` 长写能赢过创建时的 `inset` 简写"这一条），**不能替代真机**：iOS Safari 的 visualViewport 行为不在这台 Chrome 上复现。
  跑法（令牌外部传入，不落盘）：`node evidence/structural-check.mjs --url "http://<host>:<port>/?token=***" --label authenticated-shell`。落盘按 label 分文件（`evidence/structural-check/RESULTS-<label>.json`），因为"认证后的壳"、"手写 fixture"和"登录页"是三个不同的命题：**登录页没有 `#app`，脚本对它判 INCONCLUSIVE 而不是 PASS/FAIL**。p6 那轮的三份留档不动（`RESULTS-fixture-p6-structure.json` / `RESULTS-static-served-shell-no-backend.json` / `RESULTS-preview-responsive-shell.json`）。本轮 v3.1 把 `#status-bar` 改成了 `fixed`，`structural-check.mjs` 相应改判 `#status-bar` 计算样式必须 `fixed`（下面三份是 v3.0 那轮跑出来的留档，**v3.1 需按新脚本重跑一次 `--label authenticated-shell` 才能重新认证状态条触底**）（都用 `file://`，**没有为取证另起服务**）：

- `RESULTS-static-file-shell-v3.json`（PASS）：真实 `internal/ui/static/index.html` 在真实 Chrome 里 `clip/clip`、`#app` fixed 0/0 且 **`app.bottom = clientHeight = 702`、inline height 为空**、`scrollTo(0,100)` 后 `scrollY=0`、直写 `scrollTop` 仍 0；并且**宿主 `bottom` 长写在真实引擎里赢过创建时的 `inset` 简写**（写入 428px → rect 上移正好 428px，computed 值就是 428px）。该 DOM 态无会话 ⇒ 没有真 PTY 宿主、也没有溢出的内部滚动区，这两项记 note 未观测，不记通过。
- `RESULTS-fixture-v3-host-cascade.json`（PASS）：手写 fixture 复刻同一结构（clip 根 + 脱流满高壳 + `#terminal-container` + `position:absolute; inset:0` 的宿主），补齐上面两项未观测：内部 `#sidebar` 仍能滚（0 → 40，期间根 `scrollY` 全程 0），宿主 cascade 同样上移 428px。
- `RESULTS-preview-responsive-shell-v3.json`（**INCONCLUSIVE**）：设计 preview 页没有 `#terminal-container`，脚本对它判 INCONCLUSIVE 而不是编造 PASS——preview 不是 PTY 宿主命题的载体。

**还欠你的**：一次 `--label authenticated-shell` 的带令牌真机服务端跑（令牌由你外部传入，不落盘），加上第 9 项要求的五张截图。Chrome 只证明结构与 cascade，不证明 iPadOS 的键盘/浮窗行为。
