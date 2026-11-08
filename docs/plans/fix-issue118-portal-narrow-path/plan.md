# issue #118 · Portal Dashboard 窄屏适配 · 修复计划

**当次目标（一句话）**：让 Portal Dashboard 在 iPhone 窄屏上**完整显示 Path（不省略、自动换行）且表格不再撑破屏幕边框**，并把顺带发现的高确定性缺陷一并修掉；其余问题只记录、等指令。

**允许修改**：`internal/portal/dashboard.html`、`internal/portal/csp_gen.go`（仅由 `go generate` 产出）、`internal/portal/portal_test.go`（锚点用例）、本计划文档。
**明确不做**：主 UI（`internal/ui/static/index.html`）的任何改动；`docs/IPADOS-UI-COMPATIBILITY.md` 冻结基线（提交 `5268869`）所述机制不得移植、不得回退、不得"顺手统一"；不引入 CSS 框架/构建步骤/新依赖；不重构 portal Go 逻辑。

---

## 1. 根因（已用一次性 headless Chrome 实测确认）

Path 列的 CSS 没有任何换行许可：`.col-path` 只有 `font-size/font-family/color`，**没有 `overflow-wrap`、没有 `word-break`**，`table` 也没有 `table-layout`（默认 `auto`）。

CSS auto 表格布局按"列的 min-content 宽度"分配：文件系统路径里 `/` **不是换行机会**（UAX #14 的 `SY` 类，后接字母数字禁止断行），只有 `-` 可以断。所以 `/home/linletian/SoftwareWorkspace/myworktree-myworktree/fix-fix-issue118` 这一列的最小宽度 = 第一个连字符之前的整段 `/home/linletian/SoftwareWorkspace/myworktree-`，实测 **357px**——比 iPhone 上整个卡片内容区还宽，5 列表格必然整体溢出屏幕。

实测数据（12px monospace，headless Chrome 一次进程，`file://`，`--dump-dom` 读几何值）：

| 变体 | 卡片内容区 | 表格实宽 | 溢出 | Path 列宽 | Path 行数 | 完整可见 |
|---|---|---|---|---|---|---|
| 现状（auto + 无换行）@390 | 348 | **685** | **+337** | 357 | 2 | 是（但表头/Link 掉出屏幕） |
| 现状 @320 | 278 | **685** | **+407** | 357 | 2 | 是（同上） |
| 只加 `overflow-wrap:break-word` @390 | 458¹ | 680 | +222 | 357 | 2 | **没修好**（`break-word` 不缩小 min-content） |
| `overflow-wrap:anywhere` @390 | 348 | 384 | +36 | 56 | 25 | 是，但溢出未清零 |
| `table-layout:fixed`+`anywhere`+窄内边距 @390 | 348 | 347 | −1 | 50 | **19–22** | 是，但一列竖排不可读 |
| **窄屏卡片化 + 窄内边距 @390** | 368 | **340** | **−28** | **318** | **2** | **是** |
| **窄屏卡片化 @320** | 298 | **270** | **−28** | **248** | **3** | **是** |
| 表格档 `fixed`+`anywhere` @768 / @1024 | 726 / 958 | 698 / 930 | −28 / −64 | 237 / 316 | 3–4 / 2–3 | 是 |

结论：

1. **溢出是结构性**的，不是"某列稍宽"。`th/td` 左右 16px 内边距 × 5 列 = **160px**，在 390px 视口上卡片内容区只有 348px，其余四列（Repository / Port / Status badge / Open 按钮）又都是不可断的单词，地板高度叠起来就把表格顶到 384px 以上——**所以"只给 Path 加换行"仍会溢出**（表中第 4 行实测 +36px）。
2. 在 5 列表格里满足"完整 Path 不换行省略"这个组合的代价是把 Path 列压到 50px、换行 19–22 行（表中第 5 行）：几何上"不溢出 + 不省略"成立，**可读性上不成立**。
3. 唯一同时满足"完整可见 + 不溢出 + 可读（2–3 行）"的是**窄屏卡片化**（表中第 6–7 行）：Path 独占整行、拿到 248–318px 宽度。

> ¹ 该行来自第一轮探测（卡片内边距与第二轮不同，所以卡片宽度是 458）；结论只取"表格仍为 680、Path 列仍卡在 357"这个不变量，不与前几行比绝对值。

> 设备差异说明：以上 headless 数值来自 Linux 上的 DejaVu Sans Mono（12px 字宽 ≈7.9px）。iPhone 用 SF Mono（≈7.2px/字），真实溢出量会略小，但 5 列表格对 ~340px 内容区的数倍溢出、以及 `/` 不断行这一机制完全一致。**真机数值仍未测**（见 §5）。

## 2. 修复方案

### C1（核心，直接对应本次反馈）：窄屏卡片化 + Path 允许断行

- `table { table-layout: fixed }` + `<colgroup>` 百分比列宽：让表格宽度**由容器决定**而不是由内容决定——这是"永不再撑破"的结构保证（比 auto 布局的内容地板可靠）。
- `.col-path { overflow-wrap: anywhere; word-break: break-all; }`：`anywhere` 负责把 min-content 真正降到 0（`break-word` 已实测无效），`break-all` 兜底老 WebKit（iOS 15.4 之前不支持 `anywhere`）。**不加** `text-overflow`、**不加** `-webkit-line-clamp`、**不加** `white-space: nowrap`：Path 永远整条可读。
- 窄屏档 `@media (max-width: 768px)`：`table/thead/tbody/tr/td` 转 block，行变卡片，`thead` 隐藏，字段名由 `td::before { content: attr(data-label) }` 提供；Path 独占整行换行。断点取 768px 与主 UI #100 的窄屏档一致；实测依据：430px 上表格档要 19+ 行（不可读），768px 上表格档 3–4 行（可用）。
- `renderList()` 里给 5 个 `<td>` 补 `data-label`（Repository/Port/Path/Status/Link）——卡片化需要字段名，这是本步骤唯一的 JS 改动。
- 窄屏档收紧内边距（`td` 16px→10px、卡片 32px→14px、页面 40/20→16/10）：把 160px 的 padding 地板降下来，Path 行宽从 50px 涨回 318px。桌面档内边距与视觉保持不变。

### C2：视口与外壳（页面级，独立可回退）

- viewport meta 补 `viewport-fit=cover`（**必须与 `env(safe-area-inset-*)` 同一次改动**：不加 cover，`env()` 恒为 0）。
- `min-height: 100vh` → `100vh` + `@supports (height: 100dvh) { min-height: 100dvh }` 回退对，消 iOS URL 栏显隐造成的高度跳动。
- `body` 加 `overscroll-behavior: none` 与 `env(safe-area-inset-*)` 内边距（border-box 下生效）。
- **不**移植主 UI 的 `overflow: clip` + `#app: fixed` 满高外壳——那是冻结基线里为终端壳设计的契约；portal 是普通可滚文档，移植会把页面钉死，属越界。

### C3：登录浮层与触控目标（独立可回退）

- `#token-form`：`height:100%` → `min-height:100dvh`（+`100vh` 回退）+ `overflow-y:auto` + 短视口下 `align-items:flex-start`，保证键盘抬起时登录卡仍可见可滚。
- 输入框 `font-size` 14px → 触屏档 16px：iOS Safari 对 <16px 的输入框聚焦会**自动放大整页**（这是与键盘遮挡独立的第二个症状）。
- `.action-link` 等触屏最小 44×44（`@media (pointer: coarse)`）；`tr:hover` 收进 `@media (hover: hover)`，否则 iOS 点一下就把高亮"粘"在行上。

### 每次改 `dashboard.html` 之后

必须 `go generate ./internal/portal` 刷新 `csp_gen.go`（否则 `TestCSPHashes_MatchesDashboardHTML` 失败）。**保持单个 `<style>`、单个 `<script>` 块**，不新增第三个内联块。

## 3. 顺带发现的问题（分级，未授权的不修）

**建议并入本次（与溢出/窄屏同一行为链）**

- `th/td` 16px×5 列的 160px padding 地板（C1 已含）。
- Path 之外，Repository 列同样是不可断单词，也在抬高表格地板（探测表里含一条 `client-very-long-repository-name`，但**未单独隔离它的 min-content 数值**）→ `.col-repo { overflow-wrap: anywhere }`（C1 已含）。
- `th` 缺 `scope="col"`、表格缺 `<caption>`（改 markup 顺手加，零视觉影响）。

**已发现但需你确认再动（独立任务）**

- **内联 `style` 属性 vs CSP**：页面现有 `<table id="instances-table" style="display:none">` 与 `#loading`，而 CSP 是 `style-src 'self' 'sha256-…'`，**没有** `'unsafe-inline'`/`'unsafe-hashes'`。按 CSP 规范，哈希/nonce 不覆盖 style 属性，属性只认 `'unsafe-inline'` → 该 `display:none` 可能被浏览器忽略，表现为"加载中"时空表头闪一下。**未验证**：我为此搭了带 CSP 头的本地探测服务，Chrome `--dump-dom` 取不到输出（连接挂住），已按预算停掉并确认端口 18719 已释放、无残留进程。修 C1 时顺带把两处 `style="display:none"` 改成 class 切换即可消除依赖，无需为此单独开验证。
- `setInterval(fetchList, 5000)` 每 5s 整表重建：会清掉用户正在做的文本选择、`document.hidden` 时仍在轮询（耗电）。属于行为改动，不在本次。
- 空列表无空态（只剩表头）；`p.port` 为 0 时 Port 列渲染 `0`、Link 列为空单元格——窄屏卡片化时会更显眼，需你确认口径（隐藏 / 显示 `—`）。
- `renderList()` 里承载 Path 的变量名叫 `idTd`（历史命名，来自 instance id 时代）——纯命名问题，未授权前不改。

## 4. 验证方式与停止条件（按 §3 成本阶梯，取最低够用）

1. **纯源码/锚点测试（首选，零浏览器）**：在 `internal/portal/portal_test.go` 加 `TestDashboardNarrowPathAnchors`，锚定：
   - `table-layout: fixed` 与 `<colgroup>` 存在；
   - `.col-path` 含 `overflow-wrap: anywhere` 与 `word-break: break-all`；
   - **反向锚点（守护你这次的要求）**：`.col-path` 规则块内**不得**出现 `text-overflow`、`-webkit-line-clamp`、`white-space: nowrap`；
   - 窄屏档 `@media (max-width: 768px)` 存在且**位于桌面规则之后**（层叠顺序）；
   - viewport meta 精确串、`100vh`→`@supports (height: 100dvh)` 成对且顺序正确。
2. `go generate ./internal/portal` + `go test ./internal/portal/...`（单包，不起浏览器）。
3. **几何门（可选，一次一个 Chrome 进程）**：沿用本次探测脚本 `docs/plans/fix-issue118-portal-narrow-path/evidence/probe*.html`（已拷进仓库作证据），跑法：
   `timeout 45 google-chrome --headless=new --disable-gpu --no-sandbox --hide-scrollbars --window-size=1400,900 --virtual-time-budget=3000 --dump-dom file://<probe>` → 从 DOM 里读几何值，对改后页面断言 `table.offsetWidth <= card.clientWidth` 且 Path 行数 ≤ 4、无省略号。已在用后即停：无残留 Chrome，无监听端口。
4. **真机（由你在 iPhone/iPad 上验收）**：iPhone SE(320)/13(390)/Pro Max(430) + iPad 竖横屏；每条报 Path 是否整条可读、行数、页面是否还能横向滑出。这一步未做之前，本计划不写"已修复真机"。

**停止条件**：1+2 通过且第 3 步几何门为负溢出，即停止；不为"更完整"再加测试层，不扩到主 UI。

## 5. 提交拆分与回退

| 提交 | 内容 | 回退效果 |
|---|---|---|
| C1 | `table-layout:fixed` + 列宽 + Path/Repo 断行 + 窄屏卡片化 + `data-label` + 内边距档 + CSP 重生成 + 锚点测试 | 回到现状：窄屏溢出、真机不可用 |
| C2 | viewport-fit + safe-area + dvh 回退对 + overscroll | 回到 URL 栏跳动 |
| C3 | 登录浮层键盘 + 44pt + hover 门控 | 回到键盘遮挡/粘滞高亮 |

三个提交各自可独立解释、验证、回退；不混入 §3 中待确认项。

## 6. 未验证项（诚实清单）

- 真机（iOS Safari）几何数值未测；本文所有 px 来自 Linux headless Chrome 的字体回退，绝对值会偏、机制不偏。
- `overflow-wrap: anywhere` 在目标 iOS 版本的支持面（iOS 15.4+）未实测；已用 `word-break: break-all` 兜底，但兜底后的真机行数未测。
- 内联 style 属性是否真的被现有 CSP 拦掉：未验证（探测工具挂起后已停止，未重试）。
- Portal 认证后真实数据下的最长 Path（多仓库、多工作树并存）未取样：只用了仓库内真实路径与一条 88 字符构造路径。

---

## 7. 实施记录（本轮已落到 `internal/portal/dashboard.html`）

**已改**：C1 + C2 + C3 全部落地（单一 `<style>` / 单一 `<script>` 未变，`go generate ./internal/portal` 已重跑）。

- C1：`table-layout: fixed` + `<colgroup>` 列宽写在 CSS（不是 `col style=""`，CSP 不覆盖属性）；`.col-path{overflow-wrap:anywhere; word-break:break-all}`；`.col-repo{overflow-wrap:anywhere}`；窄屏档行转卡片 + `td::before{content:attr(data-label)}`；`renderList` 改用 `cell()` 统一注 `data-label`，开关可见性全走 class（`.hidden`），markup **0 个内联 `style=""`**。
- C2：`viewport-fit=cover`、`env(safe-area-inset-*)` 四边内边距、`min-height:100vh` + `@supports (height:100dvh)` 回退对、`overscroll-behavior:none`。**未**引入主 UI 的 `overflow:clip`/`#app:fixed`（锚点测试反向守这条）。
- C3：登录浮层 `inset:0` + dvh 对 + `overflow-y:auto` + `@media (max-height:620px)` 贴顶；触屏档 `.action-link`/按钮 ≥44px、输入框 16px（防 iOS 聚焦自动放大）；`tr:hover` 收进 `@media (hover:hover)`。
- 额外（预览已认可）：空表格文案 `#empty-hint`；dead/无端口行 Port 与 Link 显示 `—` 而不是 `0`/空白。

### 字号单一来源（本轮反馈：PATH 字号不一致）

发现的**不是** PATH 自己跳，而是一行里挤了四种字号，其中三个是我上一轮违规引入的：

| 角色 | 上一轮 | 现在 | 来源 |
|---|---|---|---|
| Repository | 14px | 14px | `td` 基线（原值） |
| Port | **13px** ❌ | 14px（继承 `td`） | 我引入的无必要字号，已删 |
| **PATH** | 12px 等宽 | 12px 等宽（`--path-font`） | 原值，全宽度单一变量 |
| Status badge | 12px | 12px | 原值 |
| Open 链接 | 12px | 12px | 原值 |
| 卡片字段名 | **10px** ❌ | 11px（`--label-font`，= 桌面 `<th>`） | 我引入的，已对齐 |

现在 `--path-font: 12px` 与 `--label-font: 11px` 在 `:root` 各声明一次，窄屏档不再重抄字体规则；`TestDashboardResponsiveAnchors/one_role_one_font_size` 守这条（并禁止 `.col-port` 出现 `font-size`）。
留存的有意差异：PATH 12px 等宽 vs 正文 14px 无衬线（原设计）、h1 24px→20px（窄屏档）、登录输入 14px→16px（coarse，防 iOS 聚焦自动放大）。

### 生产页实测（`probe-iframe-prod.html`，拿的是 `internal/portal/dashboard.html` 本体，经同目录符号链接 `dashboard-under-test.html`，不是预览复刻稿）

| 视口 | 档位 | 卡片 / 表格宽 | 页面横溢 | PATH 字号 | 仓库名 / Port | 字段名 | PATH 列宽 / 行数 | 被裁 |
|---|---|---|---|---|---|---|---|---|
| 320 | 卡片 | 298 / 270 | **0px** | 12px | 14 / 14 | 11px | 244px / 3 | 0 |
| 390 | 卡片 | 368 / 340 | **0px** | 12px | 14 / 14 | 11px | 314px / 3 | 0 |
| 430 | 卡片 | 408 / 380 | **0px** | 12px | 14 / 14 | 11px | 354px / 3 | 0 |
| 810 | 表格 | 768 / 704 | **0px** | 12px | 14 / 14 | （无伪元素） | 296px / 3 | 0 |
| 1440 | 表格 | 958 / 894 | **0px** | 12px | 14 / 14 | （无伪元素） | 375px / 2 | 0 |

> 表格档那行“无伪元素”是预期的：字段名 `td::before` 只在 ≤768px 输出（那里 `thead` 隐藏）；探测脚本读不到伪元素时会回退成继承的 14px，不是缺陷。
> 上一轮的 PATH 行数读数用 `selectNodeContents(<td>)` 测到的是块盒（1 个 rect），**行数被少算**；本轮 `lines()` 改成选文本节点。上面的行数是修正后的值。

### 测试

- 新增 `TestDashboardResponsiveAnchors`（8 个子用例：viewport cover / 容器定宽 / PATH 换行与反向禁省略号 / 一个角色一个字号 / dvh 回退对顺序 / 窄屏卡片化 / hover 门控 + 44pt / class 切换与不移植主 UI 壳）。
- `go test ./internal/portal/ -count=1` → **ok 8.9s**（含 `TestCSPHashes_MatchesDashboardHTML`）；`gofmt -l` 与 `go vet ./internal/portal/` 无输出。
- **仍未验证**：真机 iOS Safari 键盘遮挡与安全区（需你在 iPhone/iPad 上看）。

---

## 8. 评审轮修复（本轮，边界仍只到 dashboard）

评审（只读）在这一版改动里找到 1 个真缺陷和 2 处自伤回归，已在 `internal/portal/dashboard.html` 内修掉并补锚点；`go generate ./internal/portal` 已重跑。

| 问题 | 修法 | 证据 |
|---|---|---|
| **`.hidden` 在 ≤768px 失效**：窄屏档 `.instances-table{display:block}` 与 `.hidden{display:none}` 特异性相同（0,1,0）且声明在后，级联里后者胜 → 401 时旧行不会被隐藏，`#loading`/`#empty-hint` 只是碰巧没有竞争规则 | 新增 `.instances-table.hidden{display:none}`（0,2,0，不靠 `!important` 也不靠源码顺序） | 锚点 `.instances-table.hidden` 必须声明 `display:none`；**反向验证**：删掉该规则后 `TestDashboardResponsiveAnchors/visibility_toggles…` 立刻 FAIL（"CSS rule not found"），恢复后字节级还原（md5 一致）再 PASS |
| 桌面 hover 动效被静音：`.action-link`/`.login-card button` 的 `transition` 随 hover 规则一起被挪/删，桌面悬停变成硬切 | `transition` 放回两条基规则（放 `:hover` 里只动画进、退出会跳变） | 无行为测试；纯视觉，靠真机/肉眼 |
| `role="alert"` 同时写 `aria-live="polite"` 自相矛盾（`role=alert` 隐含 assertive） | 删 `aria-live="polite"`，保留 `role="alert"` | 无测试 |
| `table` 根因注释写"953px / +605px"，与实测（685px / +337px @390）不一致 | 注释改用实测值并指向本文件的探测表 | 本文 §1 表 |
| `color-scheme` + `html` 背景是 plan 未列的行为改动 | 就地加注释说明（safe-area 条带用 canvas 色、深色模式原生 chrome），代码不动 | 无测试 |

### 评审提出但**未**改，等你决定（都不在"窄屏 Path 可读"这一条行为链上）

- **三提交方案与聚合测试互斥**（§5）：C1/C2/C3 的锚点都在同一个 `TestDashboardResponsiveAnchors` 里，只提交 C1 必红。要么拆成三个测试函数随各自提交，要么如实改成一次提交并改 §5 口径。
- 计划外功能改动仍与 C1 同处一坨：`#empty-hint` 空态、port/link 用 `—` 代替 `0`/空白、`a.rel="noopener"`、`autocomplete="off"`、`<caption>`/`scope="col"`。按原子提交要求应单列或剔除。
- 窄屏档 `table/tr/td` 变 block/grid 后表格语义丢失（AT 不再播报，`<caption>`/`scope` 在该档失效）；要保住需补 `role="table"/"row"/"cell"`。
- `.col-path` 的 `word-break: break-all` 与 `overflow-wrap: anywhere` 叠加，老 WebKit 兜底可读性代价未量化。
- 窄屏档 `<colgroup>` 已不参与列宽（table 变 block），当前靠"无内容所以看不到"掩盖；`--cell-pad-y/x` 在该档也基本是死配置（`td` 走 `padding:3px 0`，`th` 隐藏）。
- `.col-port{color:var(--text-secondary)}`、`.action-link{justify-content:center}`、`.status-badge{white-space:nowrap}` 是桌面端新增视觉变化（Port 的灰色与预览稿一致，判定为设计本意，故保留并在此登记）。
- 测试脆性：`cssRuleBody`/`mediaBlockRanges` 依赖精确写法（`"sel {"`、prelude 原串）与写死的 20/10/42/14/14，改版式即误报。
- `docs/plans/fix-issue118-portal-narrow-path/` 内的 `preview-narrow-path.html`（手抄 mock，会漂移）、`serve-preview.py`（绑 0.0.0.0、无鉴权）、`dashboard-under-test.html`（指向产品目录的符号链接）建议不进产品提交；两个 `probe-iframe*.html` 属证据，可留。

---

## 9. 字体层级重设计（本轮，依据真机反馈）

反馈三条：① path 内容比自己的 `PATH` 字段名还显大，层级倒置；② 页面标题偏小，且文案要变成 `myworktree Portal Dashboard`；③ 整体字号重新设计（竖屏标题小、横屏刚好）。

### 新的字号阶梯（单一代来源 `:root`，窄屏档只覆盖 token，不新增第二条 font-size）

| token | 值 | 角色 | 用在哪 |
|---|---|---|---|
| `--fs-h1` | **24px（表格档）/ 22px（卡片档）** | display | 页面标题（原 24/20） |
| `--fs-h2` | 20px | display | 登录卡标题 |
| `--fs-body` | 14px | content | `td`、输入框、按钮 |
| `--fs-meta` | 13px | secondary | 页面描述、登录副标题/表单标签/错误、`#loading`、`#empty-hint` |
| `--fs-label` | **12px** | caption | `th` 与卡片字段名（原 11px） |
| `--fs-path` | **11px** | caption | path 值本身（原 12px） |
| `--fs-badge` | 12px | caption | 状态胶囊 |
| `--fs-action` | 12px | caption | `Open` 链接 |
| `--fs-input-coarse` | 16px | — | `@media (pointer: coarse)` 下输入框（防 iOS 聚焦放大） |

- **path 比自己的 label 低一级**（11 < 12）：等 px 时等宽数字/斜杠看起来比全大写 label 更大，正是本次反馈的倒置。想换来更多 path 像素，改 `--fs-path` 与 `--fs-label` 两个 token 即可，不要单改一处。
- 标题：`myworktree` 用 `<span class="brand">` 内联在**同一个** h1/h2 里（`font-size: .82em` + 次要色），既显示品牌又保持单一标题语义，且字号跟随 `--fs-h1/--fs-h2` 而不是再硬编码一个尺寸。h1 加 `line-height: 1.25` 与 `text-wrap: balance`，320px 上两行也断得整齐。
- 卡片档标题 20px → 22px（竖屏偏小），表格档保持 24px（横屏"刚好"，不动）。
- `td` 加 `line-height: 1.45`、path 加 `line-height: 1.45`，多行 path 不再挤在一起。

### 测试（`TestDashboardResponsiveAnchors` 重写/新增 3 条不动点）

- `type scale is a single source`：9 个 token 在 `:root` 各声明恰好一次；**全文件每个 `font-size:` 都必须是 `var(--fs-*)`**（唯一例外 `.brand` 的 `0.82em` 相对尺寸）；`path ≤ label`、`path < body` 由 `tokenPx()` 解析 token 数值断言；`.col-port` 仍不得有 `font-size`；窄屏档不得出现 `--fs-path`（不许二次选字号），且必须 `--fs-h1: 22px`。
- `title carries the product name`：markup 必须是 `<h1><span class="brand">myworktree</span> Portal Dashboard</h1>`（h2 同），`.brand` 保持 `0.82em`。
- 反向验证：把 `--fs-path` 改成 13px + 给 `.empty-hint` 塞一个 `font-size: 15px` → 两条断言分别报 “path (13px) must not exceed its label (12px)” 与 “font-size: 15px is outside the type scale”；还原后 md5 一致、全绿。

`go test ./internal/portal/ -count=1` → ok 8.7s；`go generate` 已重跑（style 哈希随之变化）。

### 仍未验证

真机观感（22px 标题在 iPhone 竖屏是否够大、11px path 是否偏小、`text-wrap: balance` 在 iOS 17.4 以下不生效时的换行）。已按上述尺寸重建 `mwalpha` 并重启沙箱 daemon 待测。

---

## 10. 全等标题 + 竖横屏字号一致性（本轮）

反馈：① 标题里 `myworktree` 不要特殊显示，与 `Portal Dashboard` 同字体同字号；② 竖屏下 PATH 内容比横屏大，问为什么（竖屏标题 22px 已合适，不动）。

### 改了什么

- **去掉品牌 span**：`<h1>myworktree Portal Dashboard</h1>` / `<h2>…</h2>` 单一文本、单一 `--fs-h1/--fs-h2`、同色同字重；`.brand` 规则删除。测试同时反向守：markup 不得再出现 `class="brand"`、CSS 不得再有 `.brand {`。
- **`html { -webkit-text-size-adjust: 100%; text-size-adjust: 100% }`**：把文字尺寸钉死在样式表声明的值上，退出 iOS Safari 的自动放大（WebKit text autosizing）。新增子用例 `orientation_differences_cannot_resize_text` 锚住这条。
- 字号阶梯其余不动（h1 22/24、label 12、path 11、body 14、meta 13、badge/action 12、coarse input 16）。

### "为什么竖屏比横屏大"的实测答案

用 `probe-iframe-prod.html`（量的是生产 HTML 本体，一次 Chrome 进程，用后即停、18800 已释放）在五个视口读 `.col-path` 的 `getComputedStyle().fontSize`：

| 视口 | 档位 | h1 | label | **path** | path 列宽/行数 | 页面横溢 |
|---|---|---|---|---|---|---|
| 320 | 卡片 | 22px | 12px | **11px** | 244 / 3 行 | 0 |
| 390 | 卡片 | 22px | 12px | **11px** | 314 / 3 行 | 0 |
| 430 | 卡片 | 22px | 12px | **11px** | 354 / 3 行 | 0 |
| 810 | 表格 | 24px | 12px（`th`） | **11px** | 296 / 3 行 | 0 |
| 1440 | 表格 | 24px | 12px（`th`） | **11px** | 375 / 3 行 | 0 |

> 表格档 `label` 那列是探针读伪元素失败后回退成 `td` 的 14px（同一份脚本的已知读法），`th` 实测 12px——与卡片档同名同值，符合"一个角色一个字号"。

结论：**CSS 里两档的 path 都是 11px，没有任何规则让它按方向变化**。同一个 11px 在一种方向显得更大，只可能来自渲染侧：

1. **iOS Safari 文本自动放大**（WebKit text autosizing：按布局宽度/方向对小字号做倍数放大）——本轮已用 `text-size-adjust: 100%` 关掉，这是唯一能解释"同 CSS、不同方向"的机制；
2. **缓存了旧页面**：`handleDashboard` 只写 `Content-Type`/`nosniff`/CSP，**没有 `Cache-Control`**，Safari 可能沿用旧副本（上一版 path 是 12px，比现在的 11px 大 9%）。这类差异靠"关掉标签页重新打开 URL"即可排除；要根治可以给该响应加 `Cache-Control: no-store`（属 `portal.go` 改动，未授权不动）。

`go test ./internal/portal/ -count=1` → ok 10.0s；`go generate` 已重跑（style 哈希 `sha256-fyNoS3cF0qmamHRvJlpAO4Do780kjhNHFA8ioo+U+J4=`），沙箱已用新二进制重启（12344 / 12347）。
