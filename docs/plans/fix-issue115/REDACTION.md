# 分支证据脱敏记录（REDACTION）

**对应 issue**：#124
**执行日期**：2026-11-10
**范围**：`develop..fix/issue-124-t5-followups` 的全部 13 个提交（仅 docs / CHANGELOG）

---

## 一、扫描结论

| 类别 | 命中 | 处理 |
| --- | --- | --- |
| 完整 auth token（32 字符） | **0**（逐提交全树 `git grep -F`，并与本机 `auth.json` 比对） | 无需处理 |
| token 前 4 位（TEARDOWN 的核验记录） | 1 处（`TEARDOWN.md`） | 删除，改为“前缀已脱敏” |
| 私网 / Tailscale IP | 18 个文件、50 处（`plan.md`、`T5-IPHONE-AB.md`、raw 捕获与 PTY） | 等长替换为 RFC 5737 TEST-NET-3 占位 |
| 本机主机名 | raw 捕获与 PTY 的 shell 提示符 | 等长替换 |
| 本机账户名 | raw 捕获与 PTY 的 shell 提示符 | 等长替换 |
| 密码 / API key / 私钥 / Bearer | 0 | 无需处理 |
| commit message 中的上述内容 | 0 | 无需处理 |

`docs/plans/fix-issue115/plan.md` 正文里作为**公开 GitHub handle** 出现的 `linletian`（评审确认人、`github.com/linletian/myworktree` 链接）**保留**——它不是密钥，且改写会破坏可点击的仓库/issue 链接。raw 证据里的**本机账户名**（shell 提示符）按机器身份信息处理并替换。

---

## 二、替换映射（等长）

| 原类别 | 占位符 | 长度 | 说明 |
| --- | --- | --- | --- |
| 私网 IPv4 | `203.0.113.23` | 12 = 12 | RFC 5737 TEST-NET-3，文档专用地址 |
| 主机名 | `workstation-VM01` | 16 = 16 | 保留 `用户名@主机` 的提示符形态 |
| 本机账户名 | `developer` | 9 = 9 | 同上 |
| token 前缀 | 删除 | — | 只出现在 TEARDOWN 的核验表 |

**等长替换是硬约束**：raw 的 `.bin` / `.visible.txt` / `.capture.console.txt` 里存在按字节偏移引用的证据（F1 的 `?2004h/l` 时间轴、`@7019` 落屏点等）。替换后逐个文件核对字节数不变，偏移仍可复算。`plan.md` / `T5-IPHONE-AB.md` 等纯文本允许长度变化（仅 `.md`）。

另外，重写会让 `T5-IPHONE-AB.md` 里指向分支首个提交的短哈希失效，已改为“分支基点提交”的措辞（`develop` 上的 `781f30f` 等基线哈希不受影响，保留）。

---

## 三、提交重写

按当次要求对**本分支 fork 后的 13 个提交**执行 `git filter-branch --tree-filter`：逐提交脱敏 + 逐提交重算 `raw/MANIFEST.md5`。提交作者、时间、message 保持不变；**只改内容**。

| 旧 | 新 |
| --- | --- |
| 分支基点提交 | `11c3792` |
| `2a68a09` | `19a4d52` |
| `be95844` | `ebda163` |
| `0d17fb5` | `2205d23` |
| `c8b2027` | `b4bfa4c` |
| `4e08e05` | `7223fc1` |
| `38cb9bf` | `1c239bc` |
| `352acd5` | `1e19ef2` |
| `4168dba` | `ed2cc21` |
| `c7ce3a4` | `f14fd11` |
| `40e4650` | `963492b` |
| `1d3e388` | `e0cf98f` |
| `19c48db` | `231ae6d` |

- 分支**未推送、无 PR**；重写后删除了 `refs/original`、过期 reflog 并 `git gc --prune=now`，旧对象已不可从对象库恢复。
- `develop` 及任何既有分支/回退点**未改写**。

---

## 四、验证

| 检查 | 结果 |
| --- | --- |
| 13 个提交逐树扫描：完整 token | 0 |
| 13 个提交逐树扫描：token 前缀 / 主机名 / 账户名 | 0 |
| 13 个提交逐树扫描：私网 IP（仅本分支新增文件） | 0 |
| raw 38 个证据文件字节数（重写前后） | 全部不变 |
| 每个有 `MANIFEST.md5` 的提交执行 `md5sum -c` | 全部 OK |
| `git diff --check` | 干净 |
| CHANGELOG 契约测试（`go test ./internal/ui/ -run 'ChangelogCount'`） | 通过 |

> 逐提交扫描用 `git grep -a` 覆盖二进制证据；`-a` 是必要的——libgit2/git 默认把 PTY dump 当二进制跳过。

---

## 五、边界

1. **`develop` 已入库的历史文档**（`docs/plans/dsh-native-ui/`、`docs/plans/fix-issue116/`、`docs/plans/issue144-preview-hang/` 等）仍含同一私网 IP 与 `/home/...` 绝对路径。它们**不是本分支新增**，本分支未改动；如需全仓脱敏，应作为独立任务并单独决定是否重写 `develop` 历史。
2. **占位符不是原值**。需要重放真机实验时，请以当时的真实网络环境为准，不要反推占位符。
3. **旧哈希已清除**。引用历史提交时请用第三节右列；`T5-FINDINGS.md` 等文档已不再引用重写前短哈希。
4. **本记录不保存原值**。若需审计“哪些字符被替换”，只能结合当时的运行环境与本节类别说明判断。
