# 真机轮次索引（ROUND-INDEX）

**设备**：iPhone iOS 18.7 ／ iPadOS 18.7（同一 WiFi，同一 token，沙盒 A/B 双 daemon）
**A 腿** `:40251` 桥接开 ｜ **B 腿** `:40252` 桥接整块编掉（差 414 行）
**采集器** `probe-v2`（每段首行自带 `probe` / `bridge` / `ua` 自证）
**时间**＝该段第一条带时间戳事件的本地时间；B 腿无桥接埋点故为 `—`，其先后由 PTY 顺序与落盘文件 mtime 锚定。

## A 腿（`raw/iphone-on.capture.console.txt`，13 段 / `round-index-iphone-on.tsv`）

| 轮 | 时刻 | 设备 | 干了什么 | 对应结论 |
| --- | --- | --- | --- | --- |
| 1 | — | iPhone | 装探针 / 建立会话 | 仪器自证 |
| 2 | 20:38:58 | iPhone **9键** | `cat -v` → `，`×N → 候选栏 `。` → `123` → quick-keys 全套 | **B-2 留 `,。`**、**B-1 野引号**（§三/§四） |
| 4 | 20:42:07 | iPhone 9键 | 第二轮（含改错重敲、退格） | **B-6 合成期删除全盲** |
| 6 | 20:53:51 | iPhone **26键** | `？！（）《》、` 全角标点整串 | A 腿 **7/7 到达**（§五） |
| 5 / 8 | 21:23:24 | **iPad 26键** | `，，。，。“”你好？！` + quick-keys | **iPad 同路径直提交**（§九之补） |
| 10 | 21:56:34 | iPhone 9键 | **S1：`vi /tmp/t5.txt` → `i` → 中文＋全角＋候选引号＋`123` → `:wq`** | **落盘 31 B 全对**、**B-1＝双写冲突**（§4.3） |
| 11 | 22:12:50 | iPhone | **C 组：`printf '\e[?1004h'; cat -v`**，切后台×3（首次输错，用回车隔开重做） | **分支 C 结案**：`^[[I`×3 / `^[[O`×6 穿透、未混入正文 |
| 12 | 22:20:09 | iPhone | **S3：关页 30s 重开 → 敲 `abc123`** | **B-3 本轮未复现** |
| 13 | 22:26:11 | iPhone | **S2：`vi /tmp/p.txt` → `i` → 粘贴 5 行 4 空格 → `:wq`** | **粘贴无损**，B-8 维持撤回（`raw/s2-*`） |

## B 腿（`raw/iphone-off.capture.console.txt`，9 段 / `round-index-iphone-off.tsv`）

| 轮 | 设备 | 干了什么 | 对应结论 |
| --- | --- | --- | --- |
| 3 | iPhone 9键 | 同 A 轮 2 | **`，` 一个都没到**（终端 `，`＝0） |
| 5 | iPhone 9键 | 同 A 轮 4 | 同上 |
| 6 | iPhone 26键 | 同 A 轮 6 | **`？！（）《》、` 整行不存在** |
| 7 | **iPad 26键** | 同 A 轮 5 | **只剩 `你好`＋粘贴的 URL，标点零** |
| 8 / 9 | iPhone 9键 | **S1（B 腿）**：`vi /tmp/t5.txt` → `i` → 同串 | 落盘仅 `你好“”`（12 B）＝**只有 composition 产物**（§4.3） |

> **两段共用一个文件**：A 先写（vim 状态行自证 31 bytes），B 后插 ⇒ 文件＝`[B 12B][A 31B]`＝43 B，算术吻合。

> **拆机前快照 `final-on.capture.console.txt` 比 `iphone-on` 多第 14 段**（一次探针重装），前 13 段逐字节不变。上表只覆盖前 13 段。

## F1 轮（独立实例 `306fe8fa9254`，`raw/final-hook-instance.capture.console.txt`）

| 轮 | 时刻 | 设备 | 干了什么 | 对应结论 |
| --- | --- | --- | --- | --- |
| 1–2 | 23:42 | iPhone | `vi /tmp/f1.txt`（`set autoindent`）INSERT 粘贴 5 行；`cat > /tmp/f2.txt` 对照粘贴 | `mode=OFF wrapped=no`；PTY 时间轴证明粘贴落点晚于 `?2004l` ⇒ **B-8 非我方缺陷**（`raw/f1-attribution.md`） |

> **TSV 边界**：`round-index-*.tsv` 的“按键序列”字段在生成时丢了若干字符（如 `ntercatpace`），只能用作轮次索引，**不能当逐键证据**；逐键以 `capture.console.txt` / `probe` 行为准。
