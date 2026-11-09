# 沙箱拆除记录（2026-11-09 23:5x）

**原则**：归档先于删除；只动自己创建的；生产进程与别人的会话一律不碰。

## 已终止进程
| pid | 命令 | 端口 |
| --- | --- | --- |
| 900711 | `/tmp/mw-on -listen 0.0.0.0:40251 -portal-port 0` | 40251 |
| 317083 | `/tmp/mw-off -listen 0.0.0.0:40252 -portal-port 0` | 40252 |
| 900584 / 317080 | 我启动的包装 bash（**argv 里带明文 token**） | — |

包装 bash 是在杀完 daemon 后**单独补杀**的——它的命令行里就是 token，只要活着就在 `ps` 里裸奔。现 **`ps` 中含 token 的行数 = 0**。

## 未触碰（明确保留）
- **生产 daemon `pid 4179735`（`:40751`）**：拆除前后均存活，`HTTP=200`，`etime` 连续未断。
- **`/tmp/mw-issue136-sandbox`**：别的问题（#136）的沙箱 worktree，原样保留。
- 其余分支 worktree（issue116/117/118/127/130/131/144、develop、p3devtest）全部原样。
- 别的会话遗留在 `/tmp` 的日志（见下"遗留风险"），**不代删**。

## 已删除
- 二进制：`/tmp/mw-on`、`/tmp/mw-off`、`/tmp/mw-sbx`
- 日志/ pid：`/tmp/mw-{on,off}.log`、`/tmp/mw-issue124-sandbox.log`、`/tmp/mw-{on,off}.pid`
- 仓库副本：`/tmp/mw-ab`、`/tmp/mw-sandbox-off`
- **git worktree 正规注销**：`git worktree remove --force /tmp/mw-issue124-sandbox`
  ⇒ `.git/worktrees/` 下我的条目已消失，未留悬空注册。
- 测试靶文与孤儿：`/tmp/{t5,p,p2,pb,f1,f2}.txt`、`/tmp/.p.txt.swp`（误开 vim 留下）
- 中间产物：`/tmp/index{,-bridge-on}.bak`、`/tmp/T5-SOP-*.md`、`/tmp/t5read.py`、`/tmp/mw-*.pty.bin`、`/tmp/mw-round-baseline.json`、`/tmp/mw-issue124-token.md5`、curl 碎片 `/tmp/{bb,hh,b2,h2,b3,h3,b4,h4,b5,h5,mf,…}`
- 沙箱实例目录（内容已归档进 `raw/`）：`~/.config/myworktree/{fee6eabdcbeb1789,224a3655d70a2085,0cc24e6d716fa2c8}`

`/tmp` 体积 984M → 936M。

## 安全核验
| 项 | 结果 |
| --- | --- |
| `auth.json` 是否被改写 | **未改写**，mtime 仍为 `17:11:33`、长度 32、前缀 `<redacted>…` ⇒ **token 未重新生成** |
| 已入库证据中 token 命中 | **0**（`docs/`、`CHANGELOG.md`、`internal/` 全扫） |
| `ps` 中 token 明文 | **0 行** |
| 门户注册 | 仅生产的 `4179735-*`／`4179911-*`＋`portal.json`；**沙箱痕迹 0**（全程 `-portal-port 0`） |
| 生产端口/进程 | `:40751` HTTP 200，pid 4179735 存活 |
| 仓库状态 | `git status` 仅剩两个**与本次无关的既有未跟踪文件**；`git diff --check` 干净；changelog 契约测试 `ok` |

## 遗留风险（不属本次会话，建议单独立单）
`grep -rlF <token> /tmp` 仍命中 **9 个文件**，均非本次产物，最大 2.6 MB：
`/tmp/mw-tty6.log`（**写入者＝生产进程 4179735**）、`/tmp/mw-tty5.log`、`/tmp/mw-opendsh3.log`、`/tmp/issue117/*`、`/tmp/mypserve.log`、`/tmp/p3-app-47231.log`、`/tmp/sess.jsonl`、`/tmp/sessread/6b9a.jsonl`。

⇒ **产品级密钥卫生问题**：token 会落进 tty 日志明文，且用 `-auth <token>` 启动时进 argv（同机所有用户可见）。建议：日志脱敏 ＋ 支持从环境变量/文件读 token 而非命令行。**因涉及生产数据，本次未代删。**

**另建议**：本次沙箱曾以 `-auth` 把该 token 带在命令行数小时，虽现已清空 `ps`，稳妥起见可考虑**轮换全局 token**（依你此前指示，我未执行）。
