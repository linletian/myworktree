# Issue #115 T5 真机走查 —— 遗留问题清单

**对应 issue**：#124（PR #123 合并后收尾）
**最后更新**：2026-11-10（T5 收口）
**状态**：**T5 矩阵已签字，无未执行项。** B-1 / B-2 / B-6 归属桥接（修复属 #115 后续，不卡 #124）；B-8 定案**非我方缺陷**并关闭；B-5 降级为上游基线双写；B-3 本轮未复现、B-4 属浏览器 UI——两者与 B-5 均已单独立单。

> 证据归档：`raw/`（38 个证据文件 + `MANIFEST.md5`，共 39 个）。脱敏映射、校验方式与分支重写说明见 `REDACTION.md`；本分支所有提交由 2026-11-10 的脱敏重写生成。

---

## 一句话结论

iPhone（9键 / 26键）与 iPad（26键）的 A/B 编译对照、S1 vim 落盘、S2/C/S3（粘贴 / 焦点 / 重连）、E1（emoji）、E2/E3/F1（autoindent 粘贴级联）全部跑完并归档。桥接必要性区间、B-1/B-2 归属、B-8 责任均已钉死；**剩余工作只有 #115 后续的桥接修复本身**（B-2+B-6 合并修、B-1 的中间插入多 DEL 展开），不属 #124。

---

## 一、问题清单（终态）

| ID | 问题 | 终态 | 归属 | 证据 |
| --- | --- | --- | --- | --- |
| **B-1** | 候选栏选中**成对双引号**后，后续输入被扰乱（每个字符后冒一个右引号） | **已定案**：本次观测 **4:1**（桥接 4 / 上游基数 1）；触发＝成对符号经候选栏进入 + 光标居中 **+ 其后又走 composition 的双写** | 桥接为主 + 上游既有基数 | `T5-IPHONE-AB.md` §四、`raw/s1-vim-decomposition.md` |
| **B-2** | 候选栏选句号时**不删除前置逗号**，终端留 `,。` | **已定案**：桥接覆盖缺口；候选栏点选只发 composition、无 keydown，删除动作无人翻译成终端 DEL | **纯桥接**（不补无解） | `T5-IPHONE-AB.md` §三 |
| **B-6** | composition 期间的删除全盲（连续退格 / 候选替换落在窗口外） | **已登记**：与 B-2 同根因，合并修 | 桥接 | `T5-IPHONE-AB.md` §七 |
| **B-3** | `tty ws closed: 1005` 重连间隙内键入的字符整段丢失 | **本轮未复现**；已立单 **#150** | 终端连接层（与桥接无关） | `raw/s2-c-s3-readout.md`、`plan.md` §5.2 |
| **B-4** | iOS Chrome 终端页底部常驻密码/自动填充提示条，遮挡终端 | **已立单 #151** | 浏览器 UI 层（与桥接无关） | `plan.md` §5.5.1 |
| **B-5** | emoji 键盘插入走 `keydown keyCode 0`，疑似未抵达 PTY | **已降级并立单 #152**：5 个 emoji **全部到达**、之后中文不失步；`⚽️` 被写成 `⚽` + `⚽️`（上游基线双写，低危） | vendored xterm / iOS IME 基线 | `raw/e1-e2-readout.md` §E1、`raw/iphone-e1e2.pty.bin` |
| **B-7** | quick-keys `Ctrl+C` 疑似未送达 | **撤回（误报）**：实测送达 `^C`×2、`^[`×2、`⇥`×2、`ESC[A`×2；误报源于拿 raw `0x03` 去数 canonical 的字面 `^C` 回显 | — | `T5-IPHONE-AB.md` §六 R1 |
| ~~**B-8**~~ | ~~quick-keys `Paste` 无 bracketed-paste 包裹~~ | **关闭：非我方缺陷**。E3 输入侧正证模式为真时发 `\x1b[200~…\x1b[201~`（6+85+6=97）；F1 发送时刻读到 `mode=OFF`，PTY 时间轴证明粘贴落在 vim 的 `?2004l` 之后 ⇒ 不包裹是协议正确行为 | 应用侧（vim）mode 时序 + 用户 `autoindent` | `raw/e3-paste-wrapped.readout.md`、`raw/f1-attribution.md` |

> **B-3 / B-4 / B-5 已单独立单，本文档只作登记与证据留存。**
> **B-1 / B-2 的修复本身属 #115 后续**：B-2 + B-6 同根因合并修；B-1 的 LCP+单 DEL 模型需按 textarea/终端 caret 之差展开多 DEL，并保证 composition 期间桥接完全让位。

---

## 二、B-1 / B-2 详解（已定案，保留推导链）

### 2.1 B-2 事件链（A 腿，一次 `,` → 候选栏点 `。`）

```
keydown k=229 key=，  comp=0
  beforeinput insertText data="，"
  input  val="，"  dt=23
  keyup  k=0
  BRIDGE deliver delta="，" branch=A via=keyup-defer
  BRIDGE clear                    ← 窗口在此关闭
────── 以下全部发生在窗口外，且没有任何 keydown ──────
  beforeinput deleteContentBackward        value="，"
  input      val=""              dt=4368
  compositionstart
  beforeinput insertCompositionText data="。"
  input      val="。"            dt=4388
  beforeinput deleteCompositionText
  input      val=""              dt=4389
  beforeinput insertFromComposition data="。"
  input      val="。"            dt=4390
  compositionend data="。"
  （全程零 deliver）
```

- **判据①**：候选栏点选**只发 composition 事件，不发 keydown**；对应 `input` 距上次 keydown **4368–4390ms**。
- IME 确实在 textarea 里删了（`deleteContentBackward`，`"，"` → `""`），但**没有任何一方把删除翻译成终端 DEL**：桥接窗口已关；vendored xterm **不监听 `beforeinput`**（`xterm.js` 全文 0 处，只在 `compositionend` 投文本、不投删除）。
- **可观测域**：B-2 只在“桥接开 + 存在符号候选栏”时可观测。iPad 26键**无候选栏**（`deleteContentBackward`=0），桥接关时终端连逗号都没有 ⇒ **实测唯一落点＝iPhone 9键**。

### 2.2 B-1 机理（本次观测 4:1，不是率）

```
baseline = ，。。。“你好”        current = ，。。。“你好1”
BRIDGE deliver → delta = "\x7f1”"      branch=A via=keyup-defer
```

`“”` 落在 textarea 后**光标停在引号中间**（经候选栏/composition 进入时如此），此后每敲一个字符，LCP 差分都把**右引号卷进 delta 尾部重发**，而只配一个 `\x7f`。

| 腿 | 净化可见行（节选） | 野生 `”`（不算 `“”` 自身那一个） |
| --- | --- | --- |
| A · 9键 | `，。，。，。，“”好”⌫⌫1”⌫⌫2”⌫⌫3”` | **4** |
| A · 26键 | `，。，。，。“”你好123` | **0** |
| B · 9键（无桥接） | `。。。“”好”` | **1** |

⇒ 本次抓取中桥接 4、上游 1。**这是单次抓取的描述统计，不是发生率**；26键直提交那一对（`“` 与 `”` 分两次投递、光标在末尾）不触发。

### 2.3 B-1 触发再收窄（S1 vim 落盘，逐字节）

`/tmp/t5.txt` 43 字节按腿切开：`[B 桥接关] 你好“”`（12B）+ `[A 桥接开] 中文，。？！，“123”\n`（31B）。vim 状态行自证 A 先写入（`1 line, 31 bytes`）。

| 场景 | `“”` 之后敲什么 | 写者 | 结果 |
| --- | --- | --- | --- |
| iPhone shell（首轮） | **中文**（走 composition） | xterm `CompositionHelper` ＋ 桥接 | ❌ 错乱 `“”好”` |
| iPhone vim（S1） | **数字**（直提交 `comp=0`） | 只有桥接 | ✅ 正确 `“123”` |

⇒ **真实触发＝“候选栏成对符号之后又走 composition”的双写冲突**，不是“成对符号之后的一切输入”。

### 2.4 B-6（composition 期间删除全盲）

连续退格 / 候选替换都以 `deleteContentBackward` 落在 textarea，窗口外、无 keydown ⇒ 终端一个 DEL 都收不到，失步持续累积。与 B-2 同根因，**必须合并修**。

---

## 三、矩阵终态

| 项 | 状态 | 证据 |
| --- | --- | --- |
| iPhone 候选栏 / composition 替换 | ✅ 已采、已定案 | `T5-IPHONE-AB.md` §三/§四 |
| iPhone 26键全角标点 | ✅ A 腿 7/7、B 腿 0 字符 | `raw/iphone-*.visible.txt` |
| iPad 软键盘 26键 A/B | ✅ 直提交 10 键：A 全达、B 全灭 | `T5-IPHONE-AB.md` §九之补 |
| iPad 软键盘 9键 A/B | 🚫 **N/A（设备限制）**：已加九宫格但切换后仍出全键盘 | `T5-IPHONE-AB.md` §九之补 |
| Chrome(iOS) | ✅ 输入无回归；发现 B-4 | `plan.md` §5.5.1 |
| 桌面中文 IME 全角符号回归 | ✅ 无回归（维护者日常使用验证，非一次性矩阵） | `plan.md` §5.5 |
| 选字栏候选全角符号 | ✅ 无回归（候选引号、候选句号路径已覆盖） | §二、§四 |
| vim INSERT 中文 / 全角 / 引号 / 数字 | ✅ A 腿 31B 逐字节正确；B 腿仅 composition 产物 12B | `raw/s1-vim-decomposition.md` |
| 分支 C（focus-events） | ✅ 结案：手动 `?1004h` 后获焦 `^[[I`×3 / 失焦 `^[[O`×6，全部穿透且未混入正文 | `raw/s2-c-s3-readout.md`、`raw/iphone-on.pty.full.bin` |
| 快速连续输入 / quick-keys / Esc·Ctrl 共存 | ✅ `tap=esc/tab/up/down/ctrl-c` 全部送达 | `raw/iphone-on.capture.console.txt` |
| 粘贴（quick-keys Paste） | ✅ 内容无损；bracketed 包裹见 B-8 | `raw/s2-vim-paste-target.txt`、`raw/e3-paste-wrapped.raw` |
| 既存问题观察项（等长替换 duplicated） | ✅ 未复现 | `plan.md` §5.3.4 |
| B-3 断连丢字 | ✅ 本轮未复现（`abc123` 完整） | `raw/s2-c-s3-readout.md` |
| B-5 emoji | ✅ 全部到达；`⚽️` 上游双写（#152） | `raw/e1-e2-readout.md` |
| B-8 / autoindent 粘贴 | ✅ 关闭（非我方缺陷） | `raw/f1-attribution.md` |

---

## 四、已定案（不需再处理）

- **failsafe = 750ms，维持不下调。** 合并四轮 n=73 敲击，keydown→keyup max 246ms，>750ms 的敲击 0 次，余量恒为 3.0×；`via` 全程 `keyup-defer`，`failsafe` **零触发**。issue #124 原计划的 200–300ms 前提被实测证伪。
- **第 3 环假设**：47/50 成立，3 例 `dt:null` 全部由 branch B 正确抑制、无丢字（A-6 / R2-MINOR-1 正面作答）。
- **空格→。 等长替换**：delta = `\x7f。`，LCP 差分路径确认。
- **选字栏合成**：零 deliver、中间态拼音无泄漏；A 腿 11 个 composition 窗口**逐条核对为零 deliver**。
- **死键路径**：CapsLock/Shift 的 keydown 有 probe 但非 229，状态机零介入。
- **硬件键盘对照组**：无回归；**229 是 IME 属性而非硬件属性**。
- **桥接必要性区间**：`keydown 229` + `insertText` + `comp=0`（无 composition）⇒ vendored xterm 完全不投递，只有桥接能救；composition 路径由 xterm 于 `compositionend` 自行投递，桥接必须静默。**分界线不是键盘布局、不是设备，而是 composition 有没有参与。**
- **长按语义按设备/布局分叉**：iPad 长按空格＝自动重复（`keyCode 32`）；iPhone 长按空格＝光标移动；iPad 长按数字＝字符选择器（默认「一」）；iPhone 长按数字＝该数字。
- **分支 C**：焦点回报全部穿透、未混入正文。
- **B-8**：封装有效（E3）且服从终端模式（F1）；级联只发生在“应用关了 DEC 2004 + 用户开 autoindent”，硬件终端同样复现。
- **B-5**：emoji 不丢；`⚽️` 双写属上游基线（#152）。

详见 `plan.md` §3.2 / §5.1–§5.5.5 与 `DATA-COLLECTION.md` §四。

---

## 五、后续动作

1. **#115 后续（不属 #124）**：B-2 + B-6 同根因合并修——桥接需覆盖 `compositionstart` / `deleteContentBackward` / `insertFromComposition` 触发的替换与删除；同时评估 **B-1** 的“中间插入多 DEL 展开”，并确保 composition 期间桥接完全让位。
2. **已单独立单**：#150（B-3 断连丢字）、#151（B-4 iOS Chrome 遮挡）、#152（B-5 emoji 变体双写）。
3. **#124 可关**：本文档、`T5-IPHONE-AB.md`、`plan.md` §5.5.5、`DATA-COLLECTION.md` §4.1 已回填；关闭核对见 §六。

---

## 六、#124 关闭核对

| #124 子项 | 结论 | 证据 |
| --- | --- | --- |
| 1. T0 探针数据 + 第 3 环假设 | ✅ 47/50；3 例 `dt:null` 由 branch B 正确抑制、无丢字 | `plan.md` §5.1/§5.2、`DATA-COLLECTION.md` §四 |
| 2. failsafe 750ms 校准 | ✅ 维持 750ms（n=73，max 246ms，零触发） | `plan.md` §3.2/§5.2、`DATA-COLLECTION.md` §4.1 |
| 3. plan §五 矩阵签字 | ✅ 全部条目落到通过 / 不适用 / 不可构造 / N/A，无未执行项 | 本文档 §三、`plan.md` §5.5.5 |
| 迟到 input 观察项（A-6/R2-MINOR-1） | ✅ 正面作答（无丢字） | `plan.md` §5.1 |
| astral `cat -v` 观察项（A-6/R2-MINOR-2） | ✅ 追加方向通过；收缩方向设备不可构造，由单测 scenario 11/14 覆盖 | `plan.md` §5.3.2 |
| 分支 C 观察项 | ✅ 结案 | `raw/s2-c-s3-readout.md` |

---

## 七、证据口径与已知边界

1. **`4:1` 是一次抓取的描述统计**，不是发生率；正文已按此措辞。
2. **F1 的发送时刻读数样本有限**：vim 阶段 2 次 `tap=paste` 只有 1 次产生 `bytes` 行（另一次点击未产生发送，疑似 iOS 剪贴板读取在手势外被拒），`cat` 阶段另有 1 次有效读数；状态条文案不在采集范围。**结论不依赖该样本**——E3 已独立正证“模式为真时包裹有效”，“我们没包裹”的指控被输入侧原始字节否证；F1 只用于解释“为什么这次级联”。
3. **分支 C 的口径变更**：矩阵原条目写的是“vim（启用 focus-events 的终端）”，实际用 `printf '\e[?1004h'; cat -v` 手动开启，验证的是“任何真开 focus reporting 的程序”。原始窄场景（窗口内恰好混入用户提交的单字符）**仍未构造**；本轮观测到的是焦点码与正文不混。
4. **iPad 9键 = N/A** 是单次尝试后的设备限制判定，非“未执行”；不再作为 #124 关闭前置。
5. **桌面 IME 回归**是维护者日常使用验证，强于一次性矩阵，但**没有采集快照**。
6. **astral 收缩方向**、**iPad 9键**、**F1 状态条文案** 属“未构造/未采集”，不记作“已验证”。
7. **等价副本**：`final-A-instance306fe8fa9254.pty.bin` ≡ `f1-hook.pty.bin`；`final-B-instance227ff8642158.pty.bin` ≡ `iphone-off.pty.full.bin`；`final-off.capture.console.txt` ≡ `iphone-off.capture.console.txt`；`f1-vim-cascade.txt` ≡ `e2-vim-p2.txt`。拆机前快照 `final-on` 是 `iphone-on` 的超集（多第 14 段：一次探针重装）。
8. **脱敏**：私网 IP、主机名/用户名与 token 前缀已在全分支替换为等长占位符（字节偏移不变）；`MANIFEST.md5` 已按脱敏后内容重算。详见 `REDACTION.md`。
