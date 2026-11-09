# iOS 真机 A/B 编译对照（iPhone + iPad）—— 证据与结论

**对应 issue**：#124（#115 T5 真机验证矩阵收尾）
**执行日期**：2026-11-09
**设备**：iPhone，iOS 18.7（`Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)`）
**结论状态**：**B-1 归桥接（放大器）· B-2 归桥接（纯桥接，上游争议为零）· 桥接必要性区间已在 iPhone 与 iPad 双重量化 · 新增 B-6 / B-8 · 本文撤回 4 条早先论断**

原始证据：`raw/`（控制台采集、PTY 原始字节、净化可见文本、指标包 + `MANIFEST.md5`）。本文只放判读，原始行请引用 `raw/`。

---

## 一、仪器（本轮自建，沙盒专用；**采集器代码不进仓库**，但**本文引用的原始证据全在 `raw/`**）

| 件 | 说明 |
| --- | --- |
| **A 腿（桥接开）** | `:40251`，`/tmp/mw-on`，分支基点提交 + 诊断件 |
| **B 腿（桥接关）** | `:40252`，`/tmp/mw-off`，**同一棵树删掉 `index.html:5264–5675` 整块 + `:8066`/`:8502` 两个调用点 = 414 行**；残留仅 1 处注释，无任何可执行桥接代码（`grep` 验） |
| 页面字节差 | A `591889` / B `563941`，difflib 相似度 `0.9758`，删除块 = 3（桥接块 + 两个调用点） |
| 采集器 | `iosime_capture.js`，**`probe-v2`**，两端同版本注入；页面 `Cache-Control: no-store` 保证不会拿到旧构建 |
| 探针 | 独立于桥接：`document` 捕获阶段挂 `keydown/keyup/beforeinput/input/composition*`，记 `keyCode/key/defaultPrevented/isComposing/target/data/value` |
| quick-keys 挂钩 | 包装 `sendQuickKey(name)` → 每次点击记 `[quick-keys] tap=<name>` |
| 回传 | `POST /api/ios-ime-capture`（每 5s + `visibilitychange` + `pagehide`），落 `~/.config/myworktree/<repoHash>/iosime-capture.log` |

> **验明正身**：每段日志首行 `[ios-ime] capture-installed {"probe":"probe-v2","bridge":"ON"/"OFF"}` —— A/B 腿各自 6 次安装记录，`bridge` 字段与预期一致。**没有这个字段的日志属于上一代仪器（probe-v1），不可与本轮混读。**

**两腿必须跑同一台设备、同一输入法、同一串动作**；差异只允许"桥接是否在编译期存在"。

---

## 二、计数与取流可靠性声明（重要，先读）

> **流的方位先于计数**：`/api/instances/log` 是**输出流**（PTY→浏览器）。凡**客户端→PTY 的输入语义**（bracketed-paste 标记、被 readline 消费的按键序列）**不会出现在这里**——「输出流里没有」**不是**「没发生」。输入侧结论必须由输入侧证据给出。**R5 就是踩了这条。**

PTY 输出流里同一字符会被**终端重绘反复绘制**，所以**次数不是量词**。本轮所有结论只使用两类判据：

1. **0 vs 非 0**（硬）：某字符在整段流里完全不出现 = 硬证据。
2. **净化后的可见命令行内容**（硬）：见 `raw/iphone-*.visible.txt`。

*软* 的部分（本轮已据此撤回两条，见 §六）：任何"N 次 = M 次"式的精确对账。上一份口头结论里"终端 7 个 `，` 精确等于桥接 7 次补发"这类说法**不作数**，本文不采用。

---

## 三、B-2（候选栏点选不删前置标点）—— 定案：桥接覆盖不足

### 3.1 完整事件链（A 腿，`raw/iphone-on.capture.console.txt`，一次 `,` → 候选栏点 `。`）

```
keydown k=229 key=，  comp=0     ← 按 ',' ，有 keydown
  BRIDGE pending base=""
  beforeinput insertText data="，"
  input  val="，"  dt=23
  keyup  k=0
  BRIDGE deliver delta="，" branch=A via=keyup-defer
  BRIDGE clear                    ← ★ 窗口在此关闭
────── 以下全部发生在窗口外，且没有任何 keydown ──────
  beforeinput deleteContentBackward        value="，"   ← IME 删掉了 textarea 里的 ，
  input      val=""              dt=4368
  compositionstart
  beforeinput insertCompositionText data="。"
  input      val="。"            dt=4388
  beforeinput deleteCompositionText
  input      val=""              dt=4389
  beforeinput insertFromComposition data="。"
  input      val="。"            dt=4390
  compositionend data="。"
  （全程零 deliver —— 桥接按设计不感知）
```

### 3.2 判据与结论

- **判据①（选中候选那一刻有没有 `keydown`）＝ 没有**。候选栏点选**只发 composition 事件**，不发 keydown。⇒ **机制 1（窗口外改写）成立**。（此前"机制 1 被排除"的推断作废，见 §六。）
- **`dt` 达 4368–4390ms**＝这些 `input` 距上一次 keydown 已 4.4 秒 ⇒ "无配对 keydown"的量化证据。
- IME **确实删了**（`deleteContentBackward`，`value` 由 `"，"` → `""`），但**没有任何一方把这次删除翻译成终端的 DEL**：桥接窗口已关；vendored xterm **不监听 `beforeinput`**（plan §七 实证 0 处，只在 `compositionend` 投文本、不投删除）。⇒ **textarea 与终端自这一刻起永久失步**，终端留下 `，`。
- **可观测域（2026-11-09 收窄，见 §六 R4）**：B-2 **只有在桥接把逗号送进终端之后才可见**——桥接关时终端连逗号都没有（B 腿 `，`＝0），谈不上「不删」。且它需要**符号候选栏**存在。**实测唯一落点＝ iPhone 9键**（A 腿可见 `，。，。，。` 四组）；**iPad 26键不可达**（无候选栏，`deleteContentBackward`＝0）。⇒ **归属＝纯桥接，上游争议为零。**
- **修复归属**：**只能在桥接侧补**（不改 vendored 代码的前提下）——需扩 §3.1 入口或 §3.2 开窗条件，覆盖 `compositionstart`/`deleteContentBackward`/`insertFromComposition` 触发的替换。**不补则该缺陷无解。**

---

## 四、B-1（成对符号后输入被扰乱）—— 定案：桥接为主，上游有既有基数

### 4.1 机理（桥接，铁证一行）

```
baseline = ，。。。“你好”        current = ，。。。“你好1”
BRIDGE deliver → delta = "\x7f 1 ”"      branch=A via=keyup-defer
```

`“”` 落在 textarea 后**光标停在引号中间**（经候选栏/composition 进入时如此），此后每敲一个字符，LCP 差分都把**右引号卷进 delta 尾部重发**，而只配一个 `\x7f`。桥接 delta 分布（A 腿全集）：

```
'，'×7  '。'×3  '？'×2  '！'×2  '（'×2  '）'×2  '《'×2  '》'×2  '、'×2
'[DEL]1”'×1  '[DEL]2”'×1  '[DEL]3”'×1   '“'×1  '”'×1  '1'×1  '2'×1  '3'×1
```

### 4.2 键盘布局决定该缺陷能否出现（净化可见行）

| 腿 | 可见命令行（净化后） | 野生 `”`（不算 `“”` 自身那一个） |
| --- | --- | --- |
| A · 9键 | `，。，。，。，“”好”⌫⌫1”⌫⌫2”⌫⌫3”⇥↑↓^C` | **4**（每个字符后各一个） |
| A · 26键 | `，。，。，。“”你好123⇥↑↓^C` | **0** |
| B · 9键（无桥接） | `。。。“”好”⇥↑↓^C` | **1** |

⇒ **野生右引号只在「成对符号经候选栏/composition 进入、光标居中」时出现**；26键直提交那一对是 `“` 与 `”` 分两次投递、光标在末尾，**不触发**。
⇒ **归属：桥接是放大器（4/5），vendored xterm + iOS IME 有既有基数（1/5）。** 不是纯上游、也不是纯桥接。
⇒ 根因边界：**桥接的 LCP + 单 DEL 模型无法处理"光标不在末尾的中间插入"**（即 §六"未投递残留 + 转换的 over-delete"的放大形态）。

---

## 五、桥接必要性区间（本轮最有价值的产出）

同一台 iPhone、同一串 26键全角标点：

| | A 腿（桥接开） | B 腿（桥接关） |
| --- | --- | --- |
| 终端可见 | `？！（）《》、：未找到命令` ← **7/7 全部到达 shell** | **该行完全不存在**（0 字符） |
| `，` | 非零（`，`×7 区段可见） | **0** |
| `？！ （）《》、` | 全部非零 | **全部 0** |
| `。` | 非零 | **非零**（走候选栏 composition） |
| `“”` | 非零 | 非零（走 composition） |
| `keydown 229` 的 composition 标志 | **`comp=0` 35 次 / `comp=1` 13 次** | 27 / 8 |

⇒ **分界线不是"9键 vs 26键"，也不是设备，而是 composition 有没有参与**：

- **`keydown 229` + `insertText` + `comp=0`（直提交、无 composition）** ⇒ vendored xterm **完全不投递** ⇒ **只有桥接能救**。9键恰好把标点推进了 composition，所以看起来"9键没问题"。
- **composition 路径（拼音合成、候选栏点选）** ⇒ xterm 的 `CompositionHelper` 在 `compositionend` 自己投递 ⇒ 桥接必须**保持静默**（现有 `composing` 守卫正确，本轮 25/19 次 `compositionupdate` 零泄漏）。

⇒ **这句要写进 plan §3.2/§六：桥接的必要性区间 = 「`keydown 229` 且 `comp=0` 且无 composition」。** 同时也是桥接的作用域上界——越过这条线（composition）它必须闭嘴，而 B-2 正落在这条线**内侧的删除动作**上，那是桥接目前唯一没接的活。

---

## 六、本轮撤回的三条早先论断（含我自己上一条的错）

| # | 撤回的论断 | 事实 | 错在哪 |
| --- | --- | --- | --- |
| R1 | 「quick-keys `Ctrl+C` 从未送达 PTY」 | **送达**：净化流里字面 `^C`×2、`^[`(Esc)×2、`⇥`(Tab)×2、`ESC[A`(↑)×2；`tap=esc/tab/up/down/ctrl-c` 各 2 次，A/B 都有 | canonical 模式下回显是**字面 `^C`** 两个字符，我拿 raw `0x03` 去数，必然得 0 |
| R2 | 「野生右引号 100% 由桥接产生」 | **4/5 桥接、1/5 上游**（§4.2 表） | 我只统计了桥接自己的 delta 行数，没统计终端可见内容；且样本里混了不同腿与被粘进终端的 URL |
| R3 | 「`，` 到达终端 7 个 = 桥接补发 7 次，精确对账」 | **不作数** | 终端重绘会重复绘制同一字符，PTY 字节计数不能当量词（§二） |
| **R5** | 「quick-keys `Paste` 无 bracketed-paste 包裹（`[200~`＝0）」——**判据用错了流**，缺陷**未证实**，撤回登记 | `\x1b[200~` 是**客户端→PTY 的输入**；bash/readline 收到后**消费且不回显**，故**输出流里本来就不该出现它**，「输出流没有」推不出「没发」。源码侧两处**都有条件包裹**：`index.html:4975` `maybeWrapBracketedPaste`（`:5044` 读 `term.modes.bracketedPasteMode`，该 getter 存在并映射 `coreService.decPrivateModes`）；vendored xterm 自身粘贴路径按 `decPrivateModes.bracketedPasteMode && !ignoreBracketedPasteMode` 包。日志里 `?2004h` 出现 3–7 次＝readline 确实开了这个模式。**真要取证须在输入侧**：探针记 send 前 payload，或在 canonical 回显的 `cat` 下粘贴看 `^[[200~` |
| **R4** | 「iPad 软键盘**同样复现 B-2** ⇒ 与设备无关」 | **不成立**。B-2 可观测域＝桥接开 **+** 存在符号候选栏 ⇒ 实测**只有 iPhone 9键** | ① **iPad 26键没有符号候选栏**（iPad 段 `deleteContentBackward`＝0、`compositionstart` 仅 1 次＝拼音 `你好`），符号走 `keydown 229 + comp=0` **直提交**，与 iPhone 26键同路径；② **桥接关时终端里连逗号都没有**（B 腿 `，`＝0），「不删逗号」无从谈起 |

**过程性缺陷（已修）**：probe-v1 只收 `[ios-ime]` 前缀 ⇒ **B 腿（桥接关）整段静默、A/B 不可比**。probe-v2 自带独立探针后才有本轮数据。

---

## 七、新增登记

- **B-6｜composition 期间的删除全盲（与 B-2 同根因，合并修）**：连续退格/候选替换都以 `deleteContentBackward` 落在 textarea，窗口外、无 keydown ⇒ 终端一个 DEL 都收不到，失步持续累积。证据：A 腿 `raw/iphone-on.capture.console.txt` 中反复出现的 `deleteContentBackward` + `value` 递减而无任何 `deliver`。
- ~~**B-8｜quick-keys `Paste` 无 bracketed-paste 包裹**~~ → **已撤回（R5），未证实**。**保留的正向事实：粘贴本身在 A/B 两机都工作正常**，剪贴板内容完整到达终端。

---

## 八、长按语义（按设备 × 布局分叉，矩阵必须写死）

| 动作 | iPad 软键盘（26键） | iPhone（9键，本轮维护者确认） |
| --- | --- | --- |
| 长按空格 | **自动重复**：1939ms 内 17 次 `keydown keyCode=32`、间隔 102ms、末尾 1 次 keyup、**229 窗口数 0**（§5.2） | **光标移动**（caret scrubbing），不产生任何字符 |
| 长按数字 `1`（英文） | — | 就是 `1` |
| 长按数字 `1`（中文 9键） | **弹字符选择器，默认选中「一」**（提交 `一`，非 `1111`） | 就是 `1`，日志里 `keydown 229 → insertText "1"` |

⇒ **§5.2「长按空格发 keyCode 32」的结论必须限定为 iPad**；原 #4 的收口判据（"长按数字键看是否同为 32"）在两台设备上都不可执行，**作废**，改写为：「长按选择器/光标移动提交那一次，走的是 `keydown 229` 还是一次裸 `input`，是否丢字」。

---

## 九之补 · iPad 26键 A/B（2026-11-09 同日补测）

两腿按键流**逐键相同**（iPad 软键盘，26键全键盘）：

| | A 桥接开 | B 桥接关 |
| --- | --- | --- |
| `keydown 229 + comp=0`（**直提交**） | **10 键** `，，。，。“”n？！` | **10 键** 同 |
| `keydown 229 + comp=1`（合成中） | 4 键 `ihao`（拼音 `你好`） | 同 |
| `compositionstart` | **1 次**（仅 `你好`） | 1 次 |
| `deleteContentBackward` | **0 次** | **0 次** |
| 桥接 `deliver` | **9 条** `，，。，。“”？！`（`branch=A via=keyup-defer`） | **0 条**（桥不在） |
| 终端可见（`raw/ipad-%s.visible.txt`） | `？！（）《》、：未找到命令`<br>`，，。，。“”你好？！` **与 textarea 逐字同步** | **整个 iPad 段只剩两行**：`你好`（composition 产物）与被粘贴的 URL，**零全角标点** |
| 野右引号 | **0**（`“”` 直提交、光标落在末尾 ⇒ 不触发 §四 机理） | 0 |

**一眼对照（净化后的终端可见行，全文见 `raw/ipad-on.visible.txt` / `raw/ipad-off.visible.txt`）**：

```
A 桥接开   ？！（）《》、：未找到命令
           ，，。，。“”你好？！              ← 标点/中文全部到位
B 桥接关   你好
           203.0.113.23:40251/?iosime-debug=1   ← 只剩拼音成形的中文与粘贴，标点零
```

**三条读数**：

1. **iPad 26键与 iPhone 26键是同一条路径**：`keydown 229 + comp=0` 直提交、**无符号候选栏**（维护者口头确认，并被 `deleteContentBackward`＝0 证实）⇒ §五 的分界线在 iPad 上**同样成立**。
2. **桥接必要性在 iPad 上更彻底**：iPad 26键没有候选栏兜底，关桥 ⇒ 全角标点**全灭**；开桥 ⇒ 逐字同步、**零野字符**。
3. **B-2 在 iPad 上不可达**（§六 R4）：没有候选栏就没有「替换」，也就没有「不删逗号」。**归属收窄为纯桥接。**

> **iPad 9键：设备不可用**——系统内已添加九宫格，切换后仍出全键盘，**无法进入 9键布局** ⇒ 记为 **N/A（设备限制，非「未执行」）**，不再作为 #124 关闭前置。

---

## 九、覆盖度与剩余项

| 项 | 状态 |
| --- | --- |
| B-1 定性 | ✅ **定案**（桥接为主 4/5 + 上游基数 1/5；触发条件＝候选栏成对符号 + 光标居中） |
| B-2 定性 | ✅ **定案**（候选栏/composition 替换的删除动作无人翻译成 DEL；只能桥接侧补） |
| 桥接必要性 | ✅ **定案**（关桥 ⇒ 直提交全角标点/数字全丢，`？！（）《》、` 在 B 腿命令行完全不存在） |
| quick-keys 共存（#2） | ✅ **iPhone 与 iPad 均已覆盖**（`Esc/Tab/↑/↓/Ctrl+C` 两机均送达；粘贴送达但无 bracketed 包裹 → B-8） |
| 全角标点 26键 | ✅ 已覆盖（A 腿 7/7） |
| 长按（#4） | ✅ 按设备/布局结案（§八） |
| **iPad 26键 A/B** | ✅ **已完成（2026-11-09）**（直提交 10 键：A 全达、B 全灭；无候选栏 ⇒ B-2 不可达，见 §九之补） |
| **iPad 9键 A/B** | 🚫 **N/A（设备限制）** ← 已加九宫格但切换后仍出全键盘，无法进入该布局。**记为不可测，非未执行**；分界线已由「iPhone 9键↔26键同机对照」**加**「iPad 26键同路径」双重锁定 |
| vim INSERT + 分支 C | 🔶 **iPhone 侧 ASCII 面口头确认通过**：`vi` 里按英文 `i` **正常进 INSERT**、正常输入、`Esc` 回命令模式、`:q` 正常退出。**仍未覆盖：INSERT 内输入中文/全角**（矩阵原条目）。**取证性质必须标明**：本沙盒四段 PTY 中 `?1049h`＝0、`?1004h`＝0 ⇒ **vim 在本沙盒从未运行**，故此笔属**维护者口头确认、非本包证据**；原「本环境 vim 未申请 `?1004`」只对本沙盒会话成立，不能算到 vim 头上 |
| B-3 / B-4 / B-5 | ⬜ 各自单独立 issue（#124 仅登记） |

**#124 关闭条件**：✅ iPad 26键 A/B 已跑完（iPad 9键＝N/A·设备限制）；余下仅 **B-3/B-4/B-5 立单 + vim INSERT + 分支 C**。**B-1/B-2 修复本身不在 #124 范围内**（属 #115 后续），但归属已定，可直接开修。

---

## 十、复现方法（他人重跑）

1. A 腿 `http://<tailscale-ip>:40251/?token=<token>&iosime-debug=1`、B 腿 `…:40252/?token=<token>&iosime-debug=1`（两腿同设备同输入法）。
2. **不需要 Mac Web Inspector**：页面自动装缓冲并每 5s 回传服务端。
3. **每个动作之间停 5 秒**（靠 flush 分段对齐，日志内事件顺序本身即真实顺序）。
4. 每段验首行 `capture-installed` 的 `probe` 与 `bridge` 字段。
5. 证据落 `~/.config/myworktree/<repoHash>/iosime-capture.log`，取回后按 `MANIFEST.md5` 校验。
