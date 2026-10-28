# Issue #116 A0 真机测量记录

**状态**：A0 主体数据已采集；坐标、准入守卫与根画布修复待实施；浮动栏分类复核和剩余设备矩阵待补。

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

## 5. 1–8 全量操作完成（latestSeq=747）

- 用户已按清单完成 1–8 全部操作，缩放 100%；最新事件为 seq747 的 `settle`。
- 第 7 步竖屏可点 Capture，第 7→8 步横屏时 Capture 被系统 UI 遮挡无法点击；**自动 `periodic` 连续采集未中断**，因此不影响证据完整性。
- `after=343` 的报告保留 seq624–747 共 120 个事件（达到单会话历史上限）；此前窗口已分别读取并记录，不能把 624 当作操作起点。
- 关键末态（seq739–747）：`innerHeight=vv.height=clientHeight=702`、`app.bottom=702`、`inlineHeight=""`、`paddingBottom=20px`、workspace 底边 682。系统回到无钉高状态后，外壳高度正确，safe-area 透明缝仍是独立分量。
- 最新窗口覆盖 `periodic/scroll/resize/focusin/focusout/settle/manual/window-resize`，包含输入激活、页面上滑、URL 栏和横竖屏变化；没有使用 hint 按钮，浮动栏类型按用户完成的操作顺序解释，不凭单个 vv 值猜测。
- 当前已完成的主要验收面：75% 与 100% 缩放、软键盘操作、妙控键盘及浮动窗操作、URL 栏、横竖屏、页面平移、前后台/visibilitychange、连续采集与续号。仍需补测的项目见下方矩阵。

## 6. 真机记录模板

| 场景 | 操作 | 报告 session / seq | 关键几何与截图 | 初步判定 |
| --- | --- | --- | --- | --- |
| 静止基线 | 软键盘和妙控浮动栏均不可见，URL 栏稳定 | ✅ seq1–3、46–47、337–338、739–747 | inline 为空，app 与 vv/innerHeight 一致；paddingBottom=20px，根背景透明 | 高度正确，safe-area/画布白缝为独立分量 |
| 妙控浮动栏 | 让底部浮动提示/输入栏实际出现，再消失 | ✅ seq48 及后续完整操作 | 有 app/vv 变化及焦点事件；按用户操作顺序解释，不凭 vv 单值命名 | 已完成操作；修复后复核浮层不应重复扣高 |
| 软键盘 | 终端聚焦弹出，再收起 | ✅ 已完成 | vv 缩小/恢复、inline 钉高与 scroll/focus 事件齐全 | 已完成操作；坐标与准入守卫先修，再复测输入行 |
| URL 栏 | 显示 → 收起 → 显示 | ✅ 已完成 | innerHeight/vv 与 app 底边序列完整 | 不使用常量偏移；按动态可见区判断 |
| 横竖屏 | 竖屏 ↔ 横屏 | ✅ seq651 及后续方向变化 | portrait/landscape 与窗口高度重算完整 | 旧基线必须随方向失效 |
| Split View / 窗口 | 1/3、1/2、2/3 或可调窗口 | 待采集 | 待采集 | 待判定 |
| 缩放与平移 | 双指/双击缩放、平移、回 scale=1 | ✅ 75% 与 100%；seq305–312、330、333、624、736 | 负 app.top 与底边误差逐点对应；无 inline 时也出现 340/428 错位 | 已坐实 `visibleBottom-app.top` 及准入守卫两项修复需求 |
| 子层 | pty、opencode/reasonix/dsh iframe、quick-keys | ⚠️ pty 已测；其余待采集 | xtermRows 与 workspace 有余量；webPanel/其他 kind 未覆盖 | xterm fit 余量与外壳 padding 分开；iframe/quick-keys 待补 |
| 生命周期 | 前后台切换、键盘已打开时刷新 | ⚠️ visibilitychange 已测；刷新待补 | 有 visibilitychange/pageshow 事件，尚无完整键盘打开时刷新证据 | 不把未测生命周期项写成通过 |

## 7. 快检判读

- `app.bottom - (vv.offsetTop + vv.height)` 明显为负且接近一行：外壳缺高，继续区分 dvh 与 inline 钉高路径。
- 上述差值约为 0，但白带高度接近 `app.paddingBottom`：优先 safe-area/透明画布，不能据此改高度算法。
- app 正确、`workspace.bottom < app.bottom`：检查 flex 与子层传导。
- app/workspace 正确、仅终端缺行：检查 xterm fit/行取整，不能拉高 app 补偿。
- 妙控浮动栏出现时 vv/dvh 变小且 app 再短一段：浮层被误当硬遮挡；需单独分类，不与静止白带混为一谈。
- vv 与 dvh 相对截图同时短报：先找独立几何基准；不以常量偏移冒充精确修复。

## 8. 真机环境与剩余未覆盖项

- iPad 型号：**iPad Pro 11 英寸（第二代），MY322CH/A**。
- iPadOS 版本：**27.0.1**（与报告 UA 中 Safari 27.0.1 一致）。
- 浏览器：**Safari**，主要矩阵在 100% zoom 下完成，首轮另有 75% zoom 数据。
- 测试构建：`fix/fix-issue116` 基线 `225c5d7` + `viewportdebug` 编译标签诊断实现；**尚未包含高度算法修复**。
- 访问方式：**Tailscale**，地址 `100.86.87.23:38231`；同会话报告通过 daemon `GET /api/viewport-debug/report` 读取。
- 妙控键盘：iPad 配套妙控键盘，**无额外型号区分需求**；分析关注其系统浮动提示/输入窗，而非键盘硬件型号。
- 会话：`d5222b167a61be314fdba725fcfedff3`，latestSeq=747；完整操作 1–8 已完成。
- 原始证据：首轮 74 个事件、seq98–343 的 100% 平移数据、seq624–747 的末段数据，以及本文件中的几何差值和用户操作说明。
- **剩余未覆盖**：Split View / Stage Manager；opencode/reasonix/dsh iframe 与 quick-keys 同场；键盘打开时刷新；iPhone、Chrome(iOS) 及其他 iPadOS 版本；实体截图逐像素比对（本轮以 JSON 几何 + 用户目视描述为主）。这些项目在矩阵补齐前不得标为通过。
