# iPadOS 前端 UI 兼容性机制 · 冻结设计

**冻结对象**：提交 **`5268869`** 对应的产品实现。

**冻结范围**：主 UI 的视口与外壳、safe-area 铺底、终端局部避让、妙控键盘/软键盘切换、横竖屏与窗口变化、触控板/触摸滚动、PTY refit/resize、终端快捷控件与 Transport，以及对应的诊断语义。

本文只描述该提交的实际机制。其他工作区、未提交实验、历史计划和候选版本都不属于本次冻结内容；如描述与代码存在差异，以 `5268869` 的实际代码为准。

## 1. 已确认的用户结果

该版本已经形成以下可工作的结果：

- 横屏连接妙控键盘时，软键盘退出后终端能够恢复全高度；
- 竖屏直接打开软键盘可以局部避让，收起后恢复；
- 竖屏第一次和后续再次打开软键盘都可以重新进入避让；
- 横竖屏转换不会把已有键盘会话误当成关闭，也不会把旧恢复状态错误带到新窗口；
- 外壳和侧栏保持满高，终端与 Transport 只做必要的局部避让；
- 整页不使用自动回顶来掩盖底部色带；
- 终端整行取整留下的不足一行余量被明确保留，不通过调整行距来消除。

用户确认“这次都好了”之后，本机制进入冻结状态。

## 2. 总体结构：满高外壳，而不是可滑动页面

### 2.1 根文档

`html` 与 `body`：

- `height: 100%`；
- 先声明 `overflow: hidden`，再声明 `overflow: clip`；
- `overscroll-behavior: none`；
- 根背景使用当前主题的 chrome 色，并声明 `color-scheme: light dark`。

`overflow: clip` 的目标是让根不形成常规滚动容器；它不是通过 JavaScript 把页面反复拉回顶部。

### 2.2 应用外壳

`#app`：

- `position: fixed`，`top: 0`、`left: 0`；
- 脱离正常文档流；
- 使用 `100vh`，并在支持时升级为 `100dvh`；
- 始终保持整个布局视口高度；
- 软键盘、妙控浮窗和 `visualViewport.height` 不再直接改变 `#app` 高度。

禁止重新引入：

- `body { position: fixed }` 的整包试验；
- 用 `100vh` 阴影/出血遮盖平移缺口；
- `window.scrollTo(0,0)`、根 `scrollTop=0` 或其他自动回顶；
- 通过 `transform` 反向补偿页面位移；
- 再次把整个 `#app` 钉到 `vv.height`。

## 3. 背景、safe-area 与真实底边

### 3.1 铺底与内容分离

- 根画布负责主题底色；
- `#app` 的 safe-area padding 用于内容内缩；
- `#sidebar` 与 `#workspace` 分别用自己的背景色通过 `box-shadow` 覆盖底部 safe-area；
- 未覆盖区域不能通过新增可滚动文档区域来“藏起来”。

### 3.2 侧栏分割线

宽档并排侧栏中，`#sidebar::after` 单独承担 1px 分割线：

- 不改变侧栏宽度或布局；
- `pointer-events: none`；
- 高度为 `100% + env(safe-area-inset-bottom)`，让线条穿过安全区到物理底边；
- collapsed/drawer 状态不能留下孤立线条。

### 3.3 Transport 与终端控制列

`#status-bar`：

- 使用 `position: fixed`；
- 无避让时 `bottom: 0`，贴物理视口底边；
- 有软键盘避让时，只写自己的 `bottom = occlusion`。

`#terminal-controls`：

- 保持绝对定位于工作区；
- `top: 20px` 不变，因此快捷入口本身不跳动；
- 原创 `bottom: 20px`，避让时为 `20 + occlusion`；
- 只有控制列高度缩短，快捷面板的 `max-height: 100%` 随列收缩并内部滚动。

## 4. 终端局部避让的唯一写入集合

所有局部避让集中在 `applyVisualViewportOcclusion()`。

原始缩减量统一定义为：

```text
raw = max(0, documentElement.clientHeight - visualViewport.height)
```

`vv.offsetTop` 不参与该算式。

有效避让量只允许写到：

1. `#terminal-container` 下每个 PTY 宿主的 `style.bottom = occlusion`；
2. `#terminal-controls` 的 `style.bottom = 20 + occlusion`；
3. `#status-bar` 的 `style.bottom = occlusion`。

不得写 `#app`、`#main`、`#workspace`、`#terminal-container` 的尺寸，也不得写根滚动。

所有宿主——包括隐藏和非活动宿主——都使用同一目标；新建宿主创建时立即应用当前值。所有写入仅在值变化时发生。

## 5. 键盘会话状态机

### 5.1 状态

当前实现保留以下最小状态：

- `terminalAvoidanceActive`：是否处于软键盘避让会话；
- `terminalAvoidanceRecovering`：是否刚从一次会话进入恢复；
- `terminalAvoidanceGeometryGrace`：已有会话刚遇到窗口几何变化，是否需要一次宽容；
- 上一次 raw、布局高度和 VV 宽度，用于识别回落与几何变化。

诊断通过 `window.__mwTerminalOcclusion` 读取实际有效值，不另建一套比例算法。

### 5.2 正常进入

在以下条件同时成立时允许进入：

- 当前存在可编辑焦点：`TEXTAREA`、`INPUT` 或 `contenteditable`；
- 非缩放状态；
- `raw > 1`；
- 没有恢复锁时，`raw >= layoutHeight × 25%`。

25% 是正常进入条件，不是对运行中会话持续应用的地板。它覆盖已有的竖屏约 32% 键盘场景，同时避免初始浅浮层进入避让。

### 5.3 会话内更新

已进入会话后，在焦点、缩放和几何条件仍成立时：

- 有效 `occlusion = raw`；
- 不再经过 25% 或 50% 的持续过滤；
- 对稳定或增大的键盘遮挡，终端按实际值更新。

### 5.4 恢复退出

同一窗口几何下，如果新的 raw 比上一次小超过 1px，视为开始恢复：

- 立即退出避让；
- 终端、控制列和 Transport 回到底部；
- 设置恢复状态，避免妙控浮窗中间值再次抬起终端。

不要求必须先收到一个完整高度帧；恢复方向本身就是退出条件。

### 5.5 恢复后的重新进入

恢复后不能仅凭 147px、206.1px 等浮窗中间状态重新进入。

只有以下新证据可以解除旧恢复状态：

- 一个新的明确软键盘：`raw >= layoutHeight × 50%`；或
- 用户重新点击终端；或
- 新的输入聚焦事件；或
- 窗口几何发生变化。

其中，点击使用 `click` 而不是 `touchstart`，因此滚动手势不会被误认为重新打开键盘。窗口变化清除的是旧窗口的恢复锁，避免旋转后第一次竖屏键盘被旧状态挡住。

这套组合覆盖：第一次打开、第二次打开、旋转后打开、妙控浮窗恢复，以及明确的新软键盘会话。

### 5.6 退出条件

任一条件成立都退出并令有效值为 0：

- `raw <= 1`；
- 焦点不再是可编辑元素；
- `abs(scale - 1) > 0.01`，即缩放状态。

初始静止页面没有会话，因此不会仅因处于满高就创建恢复锁。

### 5.7 旋转与 Split View 宽容

如果已有避让会话遇到窗口几何变化：

- 不立即把 raw 下降判为恢复；
- 给后续同几何变化保留一次 settle 宽容；
- 宽容只消费一次，之后恢复正常的方向判断。

这用于区分“旋转后的键盘重排”和“真正收起键盘”。

## 6. 输入事件与滚动所有权

### 6.1 触控板 / wheel

`initShellScrollLock()` 在 document 的 wheel 冒泡阶段工作：

- 对终端区域的 wheel 也执行 `preventDefault()`，阻止整页默认滚动；
- 不在锁里调用 `term.scrollLines()`，避免与 xterm 已有 wheel handler 双重滚动；
- 真正溢出的原生内部滚动器与 iframe 保持自己的滚动；
- `ctrlKey` 的缩放手势保持豁免。

该锁只负责默认行为，不做根位置修正。

### 6.2 触摸

- 终端自身的 touchmove 已阻止默认行为并映射终端滚动；
- document 的 touchmove 锁对终端豁免，避免双重处理；
- 其他可用的内部滚动器继续滚动；
- 多指触摸在全局锁中豁免；终端自身的 `touch-action: none` 与触摸处理保持既有行为。

## 7. PTY refit 与最终尺寸同步

两套 PTY 工厂必须保持一致：页面内 legacy 路径与 `kinds/pty.js` renderer 路径。

### 7.1 fit 与 ack 分离

- `ResizeObserver`、侧栏变化和实例激活只负责 fit 并请求新尺寸；
- fit 后不得立即调用 `syncTerminalToAppliedSize()` 套用上一次的旧 ack；
- 新尺寸只有在服务端 `resize` 控制帧到达时才成为 applied size；
- 重连的 ready / 首数据路径保留现有 applied-size 同步语义；
- 服务端跨查看者取最小尺寸的既有协议不变。

### 7.2 100ms 前沿 + 一个 trailing

`queueTTYResize()` 使用：

- 100ms 窗口；
- 满足前沿条件时立即发送；
- 窗口内只保留一个 trailing timer；
- 新尺寸替换 pending payload，但不重新推迟 timer；
- 连续 fit 最终必须发送最新尺寸；
- 已实际发送的相同尺寸去重；
- socket 非 OPEN 时不得把该尺寸记作已发送；
- disconnect/destroy 清理 pending、trailing timer 和旧发送键，重连后允许再次发送相同尺寸。

这解决“宿主已经恢复全高，但终端仍停留在旧键盘尺寸”的问题。

### 7.3 整行取整余量

xterm 按整行布局时，不足一行的高度余量是明确接受的行为：

- 不改行距；
- 不为了铺满而多裁掉一行；
- 终端背景继续同色铺底；
- 不把这个余量与历史上的大段高度未恢复混为一谈。

## 8. 诊断与证据边界

诊断版本为 `p9-full-shell-session-avoidance-v1`。

诊断通道：

- 仅由 `viewportdebug` 构建标签和 `viewport-debug=1` 启用；
- 采集几何、有限设备状态、焦点标签、宿主/控件尺寸和实际有效 occlusion；
- 不采集终端正文、token、worktree 名称/路径或实例内容；
- 版本戳用于防止把旧构建数据当成当前构建证据；
- DOM `elementFromPoint` 只是辅助，不等于实际像素验收；
- 截图和真机操作仍是视觉结论的必要证据；
- 缺字段、混版、序列缺口等只能得到 `INCONCLUSIVE`，不能伪造 PASS；
- Headless Chrome 不模拟 iPadOS 键盘或妙控浮窗，不能替代真机。

最后阶段的验收使用针对性 Node 用例、验收盘点 fixture、离线真实源码回放、Go 定向锚点和用户真机操作；不把未执行的全仓测试或浏览器检查写成已通过。

## 9. 回退与变更边界

关键回退点：

- `2e857de`：先前已提交的稳定基础版本；
- `5268869`：本文冻结的完整兼容性机制。

冻结不意味着永久禁止演进，而是意味着**没有用户新的明确需求指令，任何环节都不主动调整**。包括但不限于：

- 不改 CSS、视口状态机、阈值、焦点/点击规则或 PTY resize；
- 不改诊断字段、探针版本、验收脚本和对应测试来“适配”一个未获授权的新方案；
- 不改现有 `.pen`、PNG、preview 或历史记录；
- 不因发现小的清理、重构或“更合理”的写法而创建新提交；
- 新反馈先记录和复现，等待用户指令，不自动回退或继续试修。

未来如用户提出新需求，应先明确新的行为、允许范围和验证方式，再以新的原子提交叠加；不得覆盖上述回退点。

## 10. 后续叠加登记（issue #116，2026-11-07）

本节为**追加注记**，不修改、不取代上文 §1–§9：上文仍以 `5268869` 的机制为准，本节只登记在其之后经 owner 签署叠加的行为，判读以 `docs/plans/fix-issue116/PLAN.md` 为准。

- **缩放口径改为非对称**：`scale < 1` 不再沿用 §5 的整体退出，改由缩放 settle 先判定、只在**已 settle 的 `scale < 1`** 上才把该帧交给避让判定；`scale > 1` 维持保守豁免，既不进入候选也不参与 settle 判定。
- **让位归属终端自身**：避让只写入终端自己的三个底边，并由当前处于前台的 PTY 宿主决定是否抬升；不在前台的宿主不被代替移动，处于被遮挡底边下新建的宿主在被创建时即抬升。
- **前台切换与转屏收敛**：`activate()` 经与 resizer 相同的共享节流请求实测尺寸，不再等待下一帧 ResizeObserver；转屏 / Split View 几何变化只做一次 settle，外壳本身不回顶。

已知问题（`KNOWN-P4-SEQ-GRACE` / `KNOWN-P4-PAN`）维持接受不修，登记与复现台账见 PLAN §13–§18 与 §18.3；本节不构成任何真机验收结论，R1–R6 未整行关闭。
