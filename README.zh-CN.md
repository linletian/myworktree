# myworktree

一个轻量的单人管理工具：用于管理 **git worktree** 与在 worktree 中运行的 **coding CLI instance（长期运行进程）**，并提供最小可用 Web UI 与输出回放。

- English: [README.md](./README.md)
- 文档： [PRD](./docs/PRD.md) · [架构](./docs/ARCHITECTURE.md) · [API](./docs/API.md)

## 背景与痛点
当你在同一个项目里并行多个需求/修复（尤其需要多个 AI coding CLI 工具并行协作与互相审核）时，常见问题是：
- 一个工作目录被“半成品改动 + 依赖安装 + 临时脚本”污染，切换任务成本高
- 终端窗口越开越多：跑测试/构建/搜索/Review，不知道哪个还在跑、输出去哪了
- 关闭/刷新页面后，长时间运行的 CLI 进程容易中断，或无法找回之前输出

一个典型工作流可能是：GPT/GLM 起草文档，Claude/MiniMax 负责 coding 落地，Qwen 负责 review；要让这种分工高效运转，往往需要“按角色隔离工作区 + 长连接可重连的终端进程”。

## myworktree 的做法
myworktree 只做管理，不碰项目具体内容：
- 每个任务用 **git worktree** 给你一个隔离目录（通常对应独立分支）
- 在每个 worktree 下托管多个 **instance**，后端持续运行，可随时重连
- 提供最小 Web UI：统一查看、停止、以及**输出回放**
- 通过 **Tag** 模板（`command/env/preStart/cwd`）启动 instance，方便为不同类型工具准备环境

## 功能（MVP）
- 受管 worktree：创建/列表/纳入管理(import)/删除（严格删除：dirty 则拒绝）
- 受管 instance：基于 Tag 启动模板启动/停止/列表
- 前端页面关闭/刷新后：后端 instance 继续运行；重新打开可回放输出
- 可选内置 HTTPS（`--tls-cert/--tls-key`），非 loopback 监听必须 `--auth`
- 回放落盘日志脱敏（覆盖常见 secret 与 `sk-...`）
- 为未来 MCP 扩展预留接口（`/api/mcp/tools`）

## 运行环境
- macOS 12+
- `git`
- Go 工具链（构建用；无外部 Go 依赖）

## 快速开始
```bash
# 1）构建（在 myworktree 源码仓库内）
cd /path/to/myworktree
go build -o myworktree ./cmd/myworktree

# 可选：构建别名命令 `mw`（等效于 `myworktree`）
#（`mw` 默认会自动打开浏览器；可用 `-open=false` 关闭）
go build -o mw ./cmd/mw

# 注意：如果二进制就在当前目录，需要用 ./mw 运行（而不是 mw）
# ./mw

# 2）运行（进入你要管理的目标 git 仓库）
cd /path/to/target/git/repo

# 最简单：用绝对路径运行（无需配置 PATH）
/path/to/myworktree/mw -listen 127.0.0.1:0

# 可选：安装到 PATH（以下两种方式任选其一）
# A）用户级目录
# mkdir -p ~/bin
# mv /path/to/myworktree/myworktree ~/bin/myworktree
# mv /path/to/myworktree/mw ~/bin/mw
# B）系统级目录（通常默认就在 PATH 里）
# 注意：install 需要的是“已编译好的二进制文件”，不是 Go 源码目录（所以不要写 cmd/mw）。
# cd /path/to/myworktree && go build -o mw ./cmd/mw && go build -o myworktree ./cmd/myworktree
# sudo install -m 755 /path/to/myworktree/myworktree /usr/local/bin/myworktree
# sudo install -m 755 /path/to/myworktree/mw /usr/local/bin/mw

# 替代方案：go install（安装到 GOBIN/GOPATH/bin）
# go install ./cmd/mw
# go install ./cmd/myworktree

# 端口号写 `0` 表示“自动选择一个空闲端口”，避免端口冲突。
# myworktree 会输出完整 URL（包含实际端口）。
/path/to/myworktree/myworktree -listen 127.0.0.1:0

# （可选）使用固定端口
# /path/to/myworktree/myworktree -listen 127.0.0.1:50053

# 用浏览器打开输出的 URL
```

myworktree 会用**当前工作目录**定位目标项目（git root），并基于该 git root 计算独立的数据目录，因此要管理其他项目时，只需要在另一个项目仓库目录下运行同一个 myworktree 二进制即可。

默认情况下，新建 worktree 会放在主仓库的同级目录下：
`<repo父目录>/<repo目录名>-myworktree/<worktree名>/`。
如果你想切回旧行为（放在每个项目的数据目录下），用 `-worktrees-dir=data`；也可以把 `-worktrees-dir` 设置为自定义路径。

## 常用命令
```bash
# worktree
myworktree worktree new "修复登录 401 并补测试"
myworktree worktree list
myworktree worktree delete <worktreeId>

# tags
myworktree tag list

# instance
myworktree instance start --worktree <worktreeId> --tag <tagId>
myworktree instance start --worktree <worktreeId> --cmd "echo hello && ls"
myworktree instance start --worktree <worktreeId>  # 启动一个 idle（当前 MVP 非交互）instance
myworktree instance list
myworktree instance stop <instanceId>
```

## 远程访问
- 默认只监听本机回环地址。
- 监听到非 loopback（如 `0.0.0.0` 或局域网 IP）时必须提供 `--auth`。
- 需要 HTTPS 时提供 `--tls-cert` 与 `--tls-key`。

## License
待定。
