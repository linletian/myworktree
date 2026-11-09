# E1 / E2 轮读（2026-11-09 23:15–23:20，iPhone，A 腿）

## E1 · emoji（B-5）—— ✅ 到达，但 ⚽️ 被写了两遍

`cat -v` 下 emoji 键盘连点 5 个，PTY 可见行原文：

    😀👍🩷🍎⚽⚽️你好^C

| 观察 | 数据 | 判定 |
| --- | --- | --- |
| emoji 是否到达终端 | PTY 内 `F0 9F` 高位序列 = 4 处，五个 emoji 字形均在 | ✅ **emoji 不丢**（B-5 主案通过） |
| 之后中文 | `你好` 紧随其后完整出现 | ✅ **敲完 emoji 不失步** |
| `⚽` 出现次数 | **2**，`VS16` 只有 **1** | ❌ **`⚽️` 被写成 `⚽` + `⚽️`**——多一份基字符 |

**事件链**（每个 emoji 同形）：`keydown keyCode=0 key=😀` → `beforeinput insertText composed:true isComposing:false` → `input insertText`。**桥接全程未 `deliver`**（无 `[ios-ime] deliver` 行）⇒ emoji 由 **xterm 原生路径**投递。
⇒ `⚽️` 重复**不是桥接所为**，属 **xterm/iOS IME 基线**在"基字符 + 变体选择符"组合上的双写：IME 先提交 `⚽`、随后补 `VS16`，两次都被当作独立插入写入终端。**记为 B-5 子项（上游，低危，视觉重复）**。

## E2 · autoindent 收口 —— ❌ **级联，B-8 复活**

`vi -c "set autoindent" /tmp/p2.txt` → INSERT → quick-keys Paste（5 行 4 空格）。落盘 `cat -A`：

| 行 | 行首 | 空格当量 |
| --- | --- | --- |
| 1 | `····` | 4 |
| 2 | `········` | **8** |
| 3 | `^I····` | tab+4 |
| 4 | `^I········` | tab+8 |
| 5 | `^I^I····` | 2tab+4 |

**单调递增 ⇒ vim 在逐行重新缩进 ⇒ 它没认出这是粘贴。** 与"上一轮 `autoindent` 关闭时不级联"完全互证。

### 责任归属（已排除两种误判）
- `term.modes.bracketedPasteMode` **真实存在**：`get modes(){ … bracketedPasteMode:e.bracketedPasteMode … }`，由 `case 2004: decPrivateModes.bracketedPasteMode=!0/!1` 维护。守卫**不是**读空字段。
- vim **确实申请过** bracketed paste：**备用屏区间内 `?2004h` = 2 次**（同区间 `?2004l` = 3 次）。
- 我们的封装**存在且正确**：`maybeWrapBracketedPaste` → `\x1b[200~ + text + \x1b[201~`（`index.html:4975`），调用点 `index.html:5044`。

⇒ 唯一剩下的解释：**粘贴那一刻 `decPrivateModes.bracketedPasteMode` 已是 false**（vim 在本会话里 `?2004l` 比 `?2004h` 多一次；`?1049h` 进备用屏亦会重置部分 DEC 模式）。
⇒ 采集器**没有 hook 到 QuickKey 的 Paste**（本轮零 `tap=paste`），所以拿不到粘贴那一刻的 `mode` 真值——**这是观测缺口，不是清白证明**。

### 决定性一招（E3，确定性、零改码）
强制把模式打开后立即粘贴并**落到文件**，让包装器无处可藏：

    printf '\e[?2004h'; cat > /tmp/pb.raw      ← 回车
    （quick-keys → Paste）
    Ctrl+D
    od -c /tmp/pb.raw | head

- 文件头出现 `033 [ 2 0 0 ~` ⇒ 封装有效，级联责任在 vim 侧的 mode 时序 ⇒ **B-8 转为"仅 vim + 长粘贴"场景的低危，可只写文档**。
- 文件头**没有** `033[` ⇒ **我们的 QuickKey Paste 根本没包裹 ⇒ B-8 成立为自身缺陷**，修 `index.html:5044`（改为自行跟踪 DEC 2004 末态，或在 quick-keys Paste 时无条件询问）。

## 撤回台账新增
**R6**：上一轮"sandbox vim 从未申请 `?1004`"正确，但**同一 vim 会话确实申请了 `?2004`**；因此不能再用"vim 不支持 bracketed paste"解释级联。上一轮的"粘贴无损"结论**降级为"在 `autoindent` 关闭时才无损"**。
