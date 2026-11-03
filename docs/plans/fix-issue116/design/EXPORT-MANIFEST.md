# Issue #116 D0-1 / D0-2 设计导出与评审清单

**日期**：2026-10-28（首版）／ 2026-11-02 修订 v2.1 ／ 2026-11-02 修订 v2.2 ／ 2026-11-03 修订 v3.0 ／ 2026-11-03 修订 v3.0a（局部避让范围补齐：控制列 + 状态栏）／ 2026-11-03 修订 v3.1（比值地板 + 状态条触底 + 滚轮隔离）／ **2026-11-03 修订 v3.1 终值：地板定 `SMALL_OCCLUSION_RATIO = 0.15`（0.25 已撤回）+ 状态条触底 + 滚轮隔离 · **`.pen` 已改稿、PNG 已用 pen 0.3.11 原生重出，无待拍板 / 待导出项**）**

**工具**：`pen version` = **0.3.11**  
**状态**：源稿、PNG 与主 UI preview 已生成并校验；**D0-1 / D0-2 设计评审待签，PR1 / PR2 / PR3 尚未获得编码门禁放行**。

## 0aaa. v3.1 设计同步（2026-11-03 · **本 pass 已真实改稿并重出 PNG**）

代码侧契约 **v3.1**（`PROBE_VERSION = p8-full-shell-overlay-wheel-v1`）本轮**已回写进 `.pen` 源稿，并用本机 `pen` 0.3.11 headless 重出 PNG**——不是待办。四张图全部按节点 id 原生导出（默认 scale 2，**未传 scale、未 resize / 裁剪 / 截图缩放**）。

### 0aaa.1 口径变更（写进源稿文本的四点）

1. **局部避让加比值地板（headline）**：`occlusion = max(0, clientHeight − vv.height)` 后 `if (occlusion < clientHeight × SMALL_OCCLUSION_RATIO) occlusion = 0`，**`SMALL_OCCLUSION_RATIO = 0.15`（已定值）**。702 布局地板 = **105.3px**：**274 → raw 428 = 61.0% → 让位（宿主 428 / 控制列 448 / 状态栏 428）**；**555 → raw 147 = 20.9% → 局部避让（宿主 `bottom:147px` → 真实底 535 / 控制列 167px / 状态栏 147px）**；**只有 633.5（raw 68.5 = 9.8%）在地板之下 → 豁免 → occlusion 0、终端铺满 682、浮窗盖上去**。凡旧文本里写 `555 → 豁免`、`633.5 → 68.5px → 613.5`、`555 → 147px → 535（作为已作废档）` 的标注，本轮一律改为上面这套数值。
   * **0.25 为什么被撤回（如实登记，已定不是待判）**：同一轮先写的是 0.25，702 布局地板 = 175.5px，**高于**实测 147px 那把键盘，算术上把 555 判成豁免——真会有一把真实键盘埋进键下，这个算术冲突经复核成立，故 0.25 撤回、改 0.15。地板落在两个实测 shortfall 之间并两侧留余量：比不许动的 68.5px（9.8%）高 36.8px，比必须让的 147px（20.9%）低 41.7px；满键盘 428px（61.0%）与 iPhone 竖屏键盘（≥ 40%）稳在让位一侧。依据与算式见 `DATA-COLLECTION.md` §5l ①（该节同一冲突已登记为「已定 0.15」，无遗留待判项）。
2. **`#status-bar` 由 `absolute` 改 `fixed`**：钉视口物理底，静止真实底 = `clientHeight`，不再继承 `#workspace` 的 `env(safe-area-inset-bottom)` 内缩一截；`#terminal-controls` 仍 absolute 在 #workspace，原创 20px 间距保留、`top:20px` 永不写。
3. **侧栏/实例分隔线触物理底**：`#sidebar` `position: relative` + `#sidebar::after` hairline（`width:1px; height: calc(100% + env(safe-area-inset-bottom))`）——1px 分隔线**延伸穿过 safe-area 到物理底**。
4. **滚轮隔离**：document `wheel`（shell lock）只 `preventDefault` 根默认、**绝不** `term.scrollLines`；终端缓冲区滚动只归终端自己的触摸拖拽，**不双滚、不自动回顶**。

### 0aaa.2 本轮实际改稿与导出记录（真实尺寸）

执行方式：`pen interactive --in <src> --out <src>`（headless，pen 0.3.11），在 `execute` 里 `get_app_state()` / `Get(...)` 读结构 → `Update(text.content)` → `save()` → `Export(ids,"png",dir)`；导出后按本清单既定文件名改名，**未做任何后期处理**。

| 源稿 | 改动节点（全部为 text 的 `content`，**无新增/删除节点、无尺寸/坐标/色板改动**） | 重出 PNG | 实际像素（本轮 ／ v3.0a） |
| --- | --- | --- | --- |
| `ui/sidebar-states.pen` | 本轮（0.15）改 `d1_text_0002`（版本戳 25%→15%）、`d1_text_0004`（地板 105.3 + `555 → 147 / 167 / 147 让位` + 633.5 豁免）、`d1_text_0014`（① 改「633.5 豁免，555 与满键盘让位」）、`d1_text_1201`（标题 25%→15%）、`d1_text_1202`（`SMALL_OCCLUSION_RATIO(0.15)`）、`d1_text_1210`（`raw < 15%→0` / `达到 15%→raw` + 加一行「702 地板 105.3：555 让位 147」）、`d1_text_1221`（`428 ≥ 702×0.15 = 105.3 → 让位` + 新增 555 档三盒数值）、`d1_text_1232`（地板 105.3、633.5 豁免、**555 → ≥ 地板 → 让位**）；上一版（v3.1 首稿）另改过 `d1_text_1211`（滚轮隔离）与 `d1_text_1242`（分隔线穿 safe-area + 状态栏 fixed），本轮未动 | `ui/notes-decisions.png` ／ `ui/viewport-geometry.png` | **2362×2082 ／ 2362×2082（未变）**；**2882×2482 ／ 2882×2482（未变）** |
| `ui/quick-keys/quick-keys-panel.pen` | 本轮（0.15）改 `qk0010b`（「带 15% 比值地板」+ `SMALL_OCCLUSION_RATIO(0.15)` + 「702 地板 105.3：633.5 豁免、555 让位 147、274 让位 428」，并就地压掉三处冗余措辞）、`qk0125`（画面名「遮挡 ≥15% 才让位」）、`qk0156`（`raw < clientHeight×0.15 → 豁免（702 地板 105.3：633.5 豁免、555 让位 147）`） | `quick-keys/notes-decisions.png` ／ `quick-keys/scene-iphone-keyboard-panel.png` | **1002×1694** ／ 1002×1728（`qk0010b` 是 `fill_container` 宽 + 自动高说明块：本轮文本净缩短 17 CSS px ⇒ −34px）；**802×1772 ／ 802×1772（未变）** |

命令序列（**pen 0.3.11**，同一 CLI，未升级；`Export` 按节点 id 命名，导出后按本清单既定文件名改名，**未传 `scale`、未 resize / 裁剪**）：

```
pen interactive --in ui/sidebar-states.pen --out ui/sidebar-states.pen
  execute(Update(d1_text_0002/0004/0014)) → execute(Update(d1_text_1201/1202/1210)) → execute(Update(d1_text_1221/1232)) → save()
  execute(Export(["d1_notes","d1_viewport_geometry"],"png",dir))
pen interactive --in ui/quick-keys/quick-keys-panel.pen --out ui/quick-keys/quick-keys-panel.pen
  execute(Update(qk0010b)) → execute(Update(qk0125/qk0156)) → save()
  execute(Export(["qk0001","qk0124"],"png",dir))
```

**CLI 坑（本轮再次踩到并记下）**：interactive shell **一行只解析一条命令**。把 `Update(...)` / `Export(...)` / 变量声明写成多行，会得到 `Unknown tool: Update` / `Unknown tool: Export` / `Invalid syntax`，而 `save()` 照样把**没改过的**文档存一遍——看上去成功、实际零改动。所以所有改动与导出都必须塞进**单行** `execute({ input: '…' })`，串内换行用 `String.fromCharCode(10)` 拼。本轮首版改稿就是这样静默失效的，靠回读 `Get(id).content` 才抓出来。

**CLI 坑 2（0.15 这一轮踩到）**：`execute` 的作用域**每次调用独立**——用 `const T=[...]` 声明的变量在下一条 `execute` 里是 `ReferenceError: 'T' is not defined`，要跨调用复用必须**不带 `const`/`let` 直接赋值**（`K1=Get(...)`），或者干脆把数据内联进那一条。另外 `Get("frame",(n,c)=>c.depth===1?...)` 拿到的文本节点 `bounds.height` 会是 0（该读法不解析自动高），量高度必须用**整文档 visitor** `Get((n,c)=>…)`。本轮两处都靠复读才没有把"零改动"当成改动完成。

**落位复核 · 0.15 这一轮（数值，非目视，改稿后逐块回读）**：同样**没有图像输入通道，作者没有看过任何一张 PNG**，全部按 pen 回读的解析数值核对（`h` = 文本块解析高，`gap` = 到同列下一兄弟 top 的余量）：`d1_text_0002` h=16 / gap=24；`d1_text_0004` h=95（行数未变）/ gap=19；`d1_text_0014` h=95 / 帧内底 945 < 帧高 1041；`d1_text_1201` h=27（仍是 1 行）/ gap=3；`d1_text_1202` h=32（仍 2 行）/ gap=10；`d1_text_1210` 171→**190**（新增那一行）/ gap=10；`d1_text_1221` 114→**133**（新增 555 档）/ gap=67；`d1_text_1232` h=152（行数未变）/ gap=48；`qk0010b` 238→**221**（净缩短，帧 `qk0001` 高随之 730 / 余量 20）；`qk0125` h=36（帧名，`qk0124` 帧 PNG 尺寸未变）；`qk0156` h=56 / 在 `qk0155` 内余量 145。全部 gap > 0，无重叠；两张 sidebar PNG 尺寸与上一版**逐位相同**，`scene-iphone-keyboard-panel.png` 亦然，只有 `quick-keys/notes-decisions.png` 因说明块变短而 1728→1694。上一版（0.25）的落位数字保留在下面一段作为历史。

**落位复核（0.25 首稿，历史）**：改稿后逐块回读文本并按兄弟节点 top 预算核对：`d1_text_1210` 高 133→171，下一兄弟 y=372 → 余 29px；`d1_text_1211` 68→85 → 余 263px；`d1_text_1221` 95→114 → 余 86px；`d1_text_1232` 133→152 → 余 48px；`d1_text_1242` 95→114 → 余 86px；`d1_text_0004` 首稿 95→133 与下一标题 y=232 **撞 19px**，已把正文压回 5 行（高回到 95、底 213 → 余 19px）并把细节移入 `d1_text_0014`；`ui/viewport-geometry.png` 与 `ui/notes-decisions.png` 导出尺寸与 v3.0a **逐位相同**，即版面无扩大。**D0-1 仍需人看图签字。**

`ui/preview-responsive.html` 本轮同步：只改说明文本（标题版本戳、⑤ 契约段、`preview-note`）——写进 15% 地板（`raw < clientHeight×0.15 → occlusion = 0`，702 地板 105.3，274 → 428 让位、**555 → 147 让位（宿主真实底 535、控制列 167、状态栏 147）**、只有 633.5 → 68.5 豁免；并写明 0.15 是标定在 9.8% 与 20.9% 两锚点之间、两侧有余量）、`#status-bar` fixed 触物理底、分隔线穿 safe-area、滚轮只锁根默认不 `scrollLines`。**未加任何 JS 高度、未模拟 visualViewport、未改 CSS 几何**，仍**不作为 #116 高度证据**（比值地板在这个 preview 里无从演示，已照实写明）。

## 0aa. 修订记录 v3.0a（2026-11-03 · 局部避让范围补齐：控制列 + 状态栏 · 最新）

v3.0 的文本把局部避让写成"只有终端宿主"。这句在本轮被否掉：**#workspace 恒为满高，`#terminal-controls`（快捷键入口/面板所在控制列）与 `#status-bar` 是它的贴底 overlay 子节点，它们不跟着抬就仍然压在键盘底下，而输入框与快捷键始终可达是原始需求本身，不是留给评审的选择题**。本轮只把这三块的算式与画面标注补进既有文本，**帧结构、色板、组件形态、层级一律不动**：

- 局部避让范围 = **PTY 宿主 + 终端控制列 + 状态栏**，三者各自写自己的 `bottom`；
- `宿主 = occlusion`、`#terminal-controls = 20 + occlusion`（原创 20px 状态栏间距保留；`top:20px` 永不写 ⇒ 入口按钮不跳，只有列高缩短；面板 `max-height:100%` 随列收缩并在盒内滚动）、`#status-bar = occlusion`（原创 `bottom:0`）；
- 702 布局三档：274 → 428 / **448** / 428px；555 → 147 / **167** / 147px；633.5 → 68.5 / **88.5** / 68.5px；`occlusion` 0 与缩放态 → 宿主 `0px`、控制列 `20px`、状态栏 `0px`。**448 与 428 必须不同**：把宿主值抄给控制列会让快捷键埋进状态栏那条 20px 带里。

| 源稿 | 改动节点（全部为 text 的 `content`，无新增/删除节点、无尺寸改动） | 重出 PNG | 实际像素（本轮 ／ 上轮） |
| --- | --- | --- | --- |
| `ui/sidebar-states.pen` | `d1_text_0003`（章节标题：只有终端让位→终端三盒各自让位）、`d1_text_0004`（让位三盒与 274→428/448/428px）、`d1_text_1202`（"只加在终端宿主 bottom"→三个 bottom）、`d1_text_1210`（不变量加控制列/状态栏两行）、`d1_text_1211`（避让只写终端三个 bottom）、`d1_text_1213`（帧内标题）、`d1_text_1219`（图层标注）、`d1_text_1221`（三档数值分列）、`d1_text_1233`（浮窗收回时控制列回 20px）、`d1_text_1295`（quick-keys 列 own bottom = 20 + occlusion） | `ui/notes-decisions.png` ／ `ui/viewport-geometry.png` | 2362×2082 ／ 2362×2082；**2882×2482 ／ 2882×2482（均未变）** |
| `ui/quick-keys/quick-keys-panel.pen` | `qk0010b`（复核说明：三个 bottom + 面板随列收缩内部滚动）、`qk0125`（画面名）、`qk0156`（软键盘标注） | `quick-keys/notes-decisions.png` ／ `quick-keys/scene-iphone-keyboard-panel.png` | **1002×1456** ／ 1002×1388（说明文本 +4 行，帧高随之 +68px）；802×1772 ／ 802×1772（未变） |
| `ui/preview-responsive.html` | 仅说明文本 ⑤ 的措辞（同上一条算式）；**未加任何 JS 高度、未改 CSS 几何**，仍不作为 #116 高度证据 | 无（preview 不出图） | — |

命令序列（**pen 0.3.11**，与首版同一 CLI，**未升级、未传 `scale`、未做任何 resize / 裁剪**；`Export` 按节点 id 命名，导出后按本清单既定文件名改名）：

```
pen interactive --in ui/sidebar-states.pen --out ui/sidebar-states.pen            # execute(Update(…9 个 text 的 content…)) → save() → execute(Export(["d1_notes","d1_viewport_geometry"],"png",dir))
pen interactive --in ui/quick-keys/quick-keys-panel.pen --out ui/quick-keys/quick-keys-panel.pen  # execute(Update(qk0010b/qk0125/qk0156)) → save() → execute(Export(["qk0001","qk0124"],"png",dir))
```

**CLI 语法坑（记下来，别再踩）**：`Export` 与 `Print` 不是顶层 shell 命令，必须包在 `execute({ input: … })` 里；`execute` 的输入里写 `\n` 或 `\u000a` 会先被 shell 展开成真换行，导致 `SyntaxError: unexpected end of string`，换行要用 `NL=String.fromCharCode(10)` 拼字符串。

本轮**唯一的尺寸变化**是 `quick-keys/notes-decisions.png` 从 1388 高变 1456 高：`qk0010b` 是 `fill_container` 宽 + 自动高的说明块，文本增加导致它多排 4 行（fs 12 / lh 1.45 ⇒ 每行 17.4px）。**没有改任何节点的宽高、坐标、色板或层级**，也没有为凑回原高度删过一句语义。复核仍按 .pen 源里的真实数值做（本执行环境没有图像输入通道，作者没有看过任何一张 PNG）：文本块按固定宽度逐行量（CJK = 1em、西文 = 0.55em），`d1_*` 九块逐块行数与上轮一致，故两张图的尺寸逐位不变；`qk0125` / `qk0156` 行数不变。**D0-1 仍需人看图签字。**

## 0. 修订记录 v3.0（2026-11-03 · 满高壳 + PTY 局部避让）

v2.2 把**整个壳**钉到 `vv.height`。你在同一台 iPad 上跑的对照实验否掉了这一半：**真参考应用的布局从头到尾满高，键盘是浮在上面的**，键盘缝里看到的就是键盘之前那同一份完整布局；它只是在键盘下方多长出一段**默认隐藏、要整页滑动才看得到**的区域，失焦即消失。本轮契约：**照抄前一半，明确不抄后一半**——壳恒满高，键盘覆盖；根永远没有滚动区，所以那段"能滑出来的隐藏条带"在本页根本不存在。

契约落点（几何真值，全部来自 `evidence/raw/sop6-3357d50e…jsonl` 的实测 702 布局 / 宿主原底 682）：

| `vv.height` | `occlusion = clientHeight − vv.height` | 写进**每个 PTY 宿主**的 `bottom` | 宿主真实底 | `#app.bottom` | `#app` inline height |
| ---: | ---: | ---: | ---: | ---: | --- |
| 274 | 428 | `428px` | 254 | 702 | 空 |
| 555 | 147 | `147px` | 535 | 702 | 空 |
| 633.5 | 68.5 | `68.5px` | 613.5 | 702 | 空 |
| 702（真浮窗纯覆盖，seq388） | 0 | `0px` | 682 | 702 | 空 |

设计源本轮改动（**只动文本、帧名与新增的示意图层；既有帧尺寸、色板、组件形态一律不动**）：

| 源稿 | 改动 | 重出 PNG | 实际像素（本轮 / 上轮） |
| --- | --- | --- | --- |
| `ui/sidebar-states.pen` | `d1_text_0002/0003/0004/0014` 文本改 v3.0（满高壳 + 键盘覆盖 + 明确拒绝参考应用的隐藏滚动区） | `ui/notes-decisions.png` | 2362×2082 ／ 2362×2082 |
| `ui/sidebar-states.pen` | `d1_text_1201/1202/1204/1209/1210/1211/1213/1219/1221/1222/1224/1230/1231/1232/1233/1242` 文本；帧名 `d1_rect_1203/1212/1223`；**新增图层**：四格各一个 `workspace ink (term-bg)` 与一个 `pty host` 矩形（静止/浮窗/安全区三格宿主贴底 682，软键盘格宿主被 `occlusion` 抬起并在其底缘加 `host bottom marker`），全部 `layoutPosition: absolute` 并按 z 序插在 `header` 之后、`visual viewport` / `soft keyboard` / `stable shell` / `safe area bottom` 之前 | `ui/viewport-geometry.png` | 2882×2482 ／ 2882×2482 |
| `ui/quick-keys/quick-keys-panel.pen` | `qk0010b`（说明帧文本，竖排自动高度 → 帧变高 +34px）、`qk0125`（画面名）、`qk0156`（软键盘标注） | `quick-keys/notes-decisions.png` ／ `quick-keys/scene-iphone-keyboard-panel.png` | **1002×1388** ／ 1002×1354；802×1772 ／ 802×1772 |

命令序列（**pen 0.3.11**，未升级 CLI；`Export` 按节点 id 命名，导出后按清单既定文件名改名，**未传 scale、未做任何 resize / 裁剪 / 截图缩放**）：

```
pen interactive --in ui/sidebar-states.pen --out ui/sidebar-states.pen        # Update(...) / Insert(...) / Move(...) → save() → Export(["d1_notes","d1_viewport_geometry"],"png",dir)
pen interactive --in ui/quick-keys/quick-keys-panel.pen --out ui/quick-keys/quick-keys-panel.pen  # Update(...) → save() → Export(["qk0001","qk0124"],"png",dir)
```

本轮的落位复核方式要说清楚，别写成"人眼看过"：**本轮执行环境没有图像输入通道，作者没有看过任何一张 PNG**。因此复核全部按 .pen 源里的真实数值做：① 文本块按固定宽度逐行量（CJK = 1em、西文 = 0.55em、`lineHeight` 取节点自身值）——只有 `d1_text_1210` 与 `d1_text_1232` 各增长一行（分别 6→7 行），其到同列下一个节点 top 的余量分别为 372−(172+134.4)=65.6px 与 920−(720+134.4)=65.6px，均通过；其余块行数不变。② 新增图层的矩形全部落在所属手机的 `app` 矩形之内，并按 `header → workspace → host → marker → viewport tint / keyboard / floating bar / safe area → text` 的 z 序插入（插入后逐一回读 `children` 索引确认）。③ 文本对比度按叠色自底向上合成后用 WCAG 计算：新增深色 `--term-bg` / `--term-panel` 图层之上的标注全部 ≥ 4.6:1（`d1_text_1219` 4.76、`d1_text_1220` 9.41、`d1_text_1230` 4.63、`d1_text_1241` 5.15）；一处 `d1_text_1231` 因落在浅色妙控浮窗上被从 `--term-muted` 改回 `--text-secondary`（2.06 → 合格）。**这些是数值复核，不替代评审眼睛：D0-1 仍需人看图签字。**

结构自检本轮实跑三份（`file://`，未另起服务）：`RESULTS-static-file-shell-v3.json`（真实 `index.html`：clip/clip、`#app` fixed 且 `app.bottom = clientHeight = 702`、inline height 空、根无可滚范围、**宿主 `bottom: 428px` 在真实引擎里赢过 `inset:0`**，PASS）、`RESULTS-fixture-v3-host-cascade.json`（fixture 补齐内部滚动 0→40 与同一 cascade，PASS）、`RESULTS-preview-responsive-shell-v3.json`（preview 无 `#terminal-container` → INCONCLUSIVE，不编造 PASS）。带令牌的认证壳真机跑与截图仍欠，见 `A0-SOP.md`。

`ui/preview-responsive.html` 同步：CSS 结构与 v3.0 一致（`html, body` `overflow: hidden` → `clip`、`#app` `position: fixed` + `top/left: 0` + `height: 100vh`，本轮**没有**给它加任何 JS 高度或根 scroll），说明文本 ⑤ 与 `preview-note` 改写为"满高壳 + PTY 局部避让 + 拒绝隐藏可滑区"，inline JavaScript 过 `node --check`。preview 仍不模拟 visualViewport、没有真 PTY 宿主可抬起，**不作为 #116 高度证据**。

键盘场景的几何在 quick-keys 里**有意未改**：那张手机场景是 flex 纵向堆叠，键盘不透明，"壳满高 / 键盘覆盖"与"壳被钉矮"在像素上不可区分；把这条区分画清楚是 `d1_viewport_geometry` 的职责（本轮新增的宿主层就在那里）。quick-keys 只改文字，避免用不可验证的图形冒充证据。

## 0a. 修订记录 v2.2（2026-11-02 · 脱流固定壳 + 根不可滚）

v2.1「固定壳 + 根原点」在真机上被否决：它的前提是根**能**滚，只是每次判定后被 `scrollTo(0, 0)` 拉回。用户看到的是带出现、再被拽走——一个每帧都要被拖回顶部的页面，仍然是一个**能滑**的页面。本轮把契约换成结构命题：**根没有滚动机制**（`html` / `body` 先 `overflow: hidden` 兜旧引擎、再 `overflow: clip`），**壳脱离普通流**（`#app` `position: fixed` + `top/left: 0`），因此没有 `scrollTop` 可写、没有 scroll 事件可追，也没有"回顶"这件事。验收话术随之改变：**"滑不动"是通过，"滑了一下又被拉回"是失败**（见 `A0-SOP.md` 第 6 项与 `evidence/structural-check.mjs`）。

回改的只是文本与一处帧名，几何、色板、层级、组件形态一律不动；钉高规则（scale = 1 且 `vv.height < clientHeight − 1` → `#app = vv.height`，无阈值）逐字保留，改的是"同一次判定还要归零根 scroll"这半句：

| 源稿 | 改动节点 | 重出 PNG | 实际像素（本轮 / 上轮） |
| --- | --- | --- | --- |
| `ui/sidebar-states.pen` | `d1_text_0002/0003/0004/0014` | `ui/notes-decisions.png` | 2362×2082 ／ 2362×2082 |
| `ui/sidebar-states.pen` | `d1_text_1201/1202/1204/1209/1210/1211/1221/1232/1233/1242` + 帧名 `d1_rect_1203` | `ui/viewport-geometry.png` | 2882×2482 ／ 2882×2482 |
| `ui/quick-keys/quick-keys-panel.pen` | `qk0010b`（竖排自动高度 → 帧变高） | `quick-keys/notes-decisions.png` | **1002×1354** ／ 1002×1320 |
| `ui/quick-keys/quick-keys-panel.pen` | `qk0156` | `quick-keys/scene-iphone-keyboard-panel.png` | 802×1772 ／ 802×1772 |

命令序列（**pen 0.3.11**，未升级 CLI；`Export` 只接受目录并按节点 id 命名，故导出后按清单既定文件名改名，**未做任何 resize / 裁剪 / 截图缩放，像素内容零改动**）：

```
pen interactive --in ui/sidebar-states.pen --out ui/sidebar-states.pen   # Update(...) → save() → Export(["d1_notes","d1_viewport_geometry"],"png",".")
pen interactive --in ui/quick-keys/quick-keys-panel.pen --out ui/quick-keys/quick-keys-panel.pen   # Update(...) → save() → Export(["qk0001","qk0124"],"png",".")
```

文本落位逐块复核：每个改动的文本节点单独 `Export` 出来量实际排版高度，再与其到下一个兄弟节点 top（或所属帧底边）的预算比对——14 块全部通过，最紧的三块余量为 `d1_text_1204` 9.5px、`d1_text_1201` 10.5px、`d1_text_1209` 13px。本轮导出时远端 `Noto Sans JP` 拉取失败（离线），中文按 CLI 的本地回退渲染，与既有 PNG 同一回退路径；`quick-keys/notes-decisions.png` 变高 34px 即 `qk0010b` 文案变长后的自然增长。其余 12 张主 UI PNG 与 Portal 10 张 PNG 源内容未变，不重出。

`ui/preview-responsive.html` 同步改结构（`html, body` 加 `overflow: clip`、`#app` 由 `relative` 改 `fixed` + `top/left: 0`，第 310 / 323 / 324 行文案改 v2.2），并用 `evidence/structural-check.mjs` 实跑验证：clip/clip、`#app` fixed 0/0、根无可滚范围，内部 `#sidebar-top-panel` 仍能滚（0 → 35，期间根 `scrollY` 全程 0）——preview 仍是交互预览，不模拟 visualViewport，不作 #116 高度证据。

## 0b. 修订记录 v2.1（2026-11-02 · 固定壳 + 根原点）

真机证据推翻了 25% 阈值与"633.5 = 妙控浮窗"的分类前提：

- `evidence/raw/b90643be…jsonl` 的人工 `magic-hint` **seq388**：真妙控浮窗在场时 `vv.height = innerHeight = clientHeight = 702`、`offsetTop = 0`、`inline=""` —— 真浮窗是**纯覆盖层**，不进 visualViewport，因此**不补偿**。
- `evidence/raw/sop6-3357d50e…jsonl`：`vv.height` 落在 **633.5 / 555** 时 `#app` 仍留在级联的 702（`gap = vv.height − app.bottom = −68.5 / −147`，内容越过可视底，`inline=""`）——收缩**没有换来自适应的壳高**。同一份捕获里另一批帧（seq23-34、seq44-47）显示 `document.scrollTop` 与 `pageTop` 随 `vv.offsetTop` 同步非零（0→428）——文档真的在滚。**两种症状分居不同帧**，所以两个方向都要判：钉高 + 根原点。对照组 `sop3-…jsonl` 里 633.5 / 555 时 `inline` 已经是 `633.5px` / `555px`、`app.bottom == vv.height`（钉高有效），根 `scrollTop` 仍在部分帧非零——只缺"归零"这一半。

据此删除阈值分类，改为**固定壳 + 根原点**：scale = 1 且 `vv.height < clientHeight − 1` → `#app = vv.height`；同一次判定把根 document scroll 归零；scale ≠ 1 是 zoom 豁免。

本轮按 `ui/README.md` 纪律只回改受影响的 `.pen` 文本与帧名，并用 **pen 0.3.11** headless `get_app_state()` 校验 + `Get(...)` 查询 + `Export(ids,"png",dir)` 原生重出，未传 scale、未做任何后期 resize / 裁剪 / 截图缩放：

| 源稿 | 改动节点 | 重出 PNG | 实际像素（本轮 / 上轮） |
| --- | --- | --- | --- |
| `ui/sidebar-states.pen` | `d1_text_0002/0003/0004/0014` | `ui/notes-decisions.png` | 2362×2082 ／ 2362×2082 |
| `ui/sidebar-states.pen` | `d1_text_1201/1202/1204/1209/1210/1211/1213/1219/1221/1222/1224/1230/1231/1232/1233/1242` + 帧名 `d1_rect_1203/1212/1223` | `ui/viewport-geometry.png` | 2882×2482 ／ 2882×2482 |
| `ui/quick-keys/quick-keys-panel.pen` | `qk0010b`（竖排自动高度 → 帧变高） | `quick-keys/notes-decisions.png` | **1002×1320** ／ 1002×1286 |
| `ui/quick-keys/quick-keys-panel.pen` | `qk0125`、`qk0156` | `quick-keys/scene-iphone-keyboard-panel.png` | 802×1772 ／ 802×1772 |

文本落位按固定宽度逐块复核（Chrome canvas 量宽，Inter 未本机安装 → 西文回退本地 sans，留 8% 余量）：所有块均未越过相邻节点的 top 或所属帧底边。其余 12 张主 UI PNG 与 Portal 10 张 PNG 的源内容本轮未变，不重出。

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
| `d1_notes` | `notes-decisions.png` | 2362×2082 | 状态矩阵、断点不变、全宽内联、**满高壳 + 键盘覆盖 + PTY 局部避让（并拒绝参考应用的隐藏可滑区）**、证据边界 |
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
| `d1_viewport_geometry` | `viewport-geometry.png` | 2882×2482 | v3.0 四格：壳恒满高 + 宿主贴底 682、软键盘下只有宿主被 `occlusion` 抬起（条带是 `--term-bg` 墨色）、妙控真浮窗不改 VV → 避让 0、横屏/Home Indicator 铺底与内缩 |
| `d1_quick_short` | `quick-keys-short-viewport.png` | 1642×1202 | 820×600 矮视口、quick-keys 与新顶栏互不遮挡 |

### quick-keys 既有稿件复核

设计源：[quick-keys-panel.pen](../../../../ui/quick-keys/quick-keys-panel.pen)

- 形态、键集、入口、左展与内部滚动不变；说明帧（`qk0010b`）已按 v2.1 改写为固定壳 + 根原点契约，不再引用 `visibleBottom − app.top`，也不再留"妙控浮动栏分类待复核"的悬置表述。
- iPhone 软键盘帧（`qk0125` 帧名 + `qk0156` 标注）的旧"经硬遮挡分类后 `visibleBottom − app.top`"描述已改为 `scale = 1 且 vv.height < clientHeight → #app = vv.height`，并写明同一次判定把根 scroll 归零；容器的 `max-height:100%` 内部滚动仍来自可见区收缩。
- `qk0001` → `notes-decisions.png`（**1002×1388**，v3.0 因 `qk0010b` 说明文本变长再增长 34px）；`qk0124` → `scene-iphone-keyboard-panel.png`（802×1772，未变）。

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
