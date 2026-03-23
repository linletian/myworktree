# myworktree

一个轻量的 agents team 管理工具：充分利用 **git worktree** 工作区独立特性，多样化 **coding CLI instance（长期运行进程）** 的能力，并提供最小可用 Web UI 与输出回放。

- English: [README.md](./README.md)
- 文档： [PRD](./docs/PRD.md) · [架构](./docs/ARCHITECTURE.md) · [API](./docs/API.md)

![](docs/codecliteams.png)

![](docs/webui.png)

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
- 受管 instance：基于 Tag 启动模板启动/停止/重启/列表
- instance 重启会保留 worktree、tag/命令、labels，并串联旧/新实例记录
- 支持可选 instance labels（`k=v`），可用于 UI 过滤与搜索
- 默认 WebSocket Web TTY 交互（并保留 SSE/HTTP 兜底）
- 前端页面关闭/刷新后：后端 instance 继续运行；重新打开可回放输出并继续交互
- UI 提供传输状态标记（websocket/sse/polling）和 WS 重连按钮
- 服务重启后会自动把历史残留的 `running` 记录回收到 `stopped`
- 可选内置 HTTPS（`--tls-cert/--tls-key`），非 loopback 监听必须 `--auth`
- 回放落盘日志脱敏（覆盖常见 secret 与 `sk-...`）
- MCP 接口（`/api/mcp/tools`、`/api/mcp/call`）

## 运行环境
- macOS 12+ 其他平台未验证
- `git`
- `zsh`
- `script`（用于托管可交互 shell）
- Go 工具链（构建用；Go 模块在编译时静态链接到二进制文件中，运行时无外部依赖）

## 快速开始

### 发布版使用

如果你只是想直接使用 `myworktree`，推荐从 GitHub Releases 下载已打包的发布版：

- Apple Silicon Mac：`myworktree_vX.Y.Z_darwin_arm64.tar.gz`
- Intel Mac：`myworktree_vX.Y.Z_darwin_amd64.tar.gz`
- 校验文件：`checksums.txt`

示例：

```bash
# 根据你的 Mac 机型选择对应压缩包，然后校验并解压
curl -LO https://github.com/linletian/myworktree/releases/download/v0.2.0/myworktree_v0.2.0_darwin_arm64.tar.gz
curl -LO https://github.com/linletian/myworktree/releases/download/v0.2.0/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf myworktree_v0.2.0_darwin_arm64.tar.gz

# 可选：安装到 PATH
sudo install -m 755 ./mw /usr/local/bin/mw
sudo install -m 755 ./myworktree /usr/local/bin/myworktree

# 验证下载下来的二进制
mw --version
```

建议从 `v0.2.0` 或更新版本开始使用公开发布版二进制。更早的 `v0.1.0` GitHub Release 资产在补充实测中发现严重终端交互问题后已撤回，而 `v0.2.0` 是当前推荐的公开发布版本。

每个发布压缩包内都包含 `mw`、`myworktree`、`README.md`、`LICENSE` 和 `CHANGELOG.md`。
如果当前还没有预发布/正式发布压缩包，或者你的平台暂无对应产物，就直接使用下面的源码编译步骤。

### Build & install

```bash
构建（在 myworktree 源码仓库内）
cd /path/to/myworktree
go build -o myworktree ./cmd/myworktree

# 可选：构建别名命令 `mw`（等效于 `myworktree`）
#（`mw` 默认会自动打开浏览器；可用 `-open=false` 关闭）
go build -o mw ./cmd/mw
```

强烈建议将构建的命令安装到用户目录下(示例为 macOS)

```bash
# 可选：安装到 PATH（以下两种方式任选其一）
# A）用户级目录
# mkdir -p ~/bin
# mv /path/to/myworktree/myworktree ~/bin/myworktree
# mv /path/to/myworktree/mw ~/bin/mw
# B）系统级目录（通常默认就在 PATH 里）
# 注意：install 需要的是“已编译好的二进制文件”，不是 Go 源码目录（所以不要写 cmd/mw）。
# cd /path/to/myworktree && go build -o mw ./cmd/mw && go build -o myworktree ./cmd/myworktree
# sudo install -m 755 ./myworktree /usr/local/bin/myworktree
sudo install -m 755 ./mw /usr/local/bin/mw

# 替代方案：go install（安装到 GOBIN/GOPATH/bin）
# go install ./cmd/mw
# go install ./cmd/myworktree
```

查看当前构建版本信息：

```bash
myworktree --version
mw version
```

### Run

```bash
# 运行（进入你要管理的目标 git 仓库）
cd /path/to/target/git/repo

# 用绝对路径运行（无需配置 PATH）
# /path/to/myworktree/mw -listen 127.0.0.1:0


# -listen 是选填参数，端口号写 `0` 表示“自动选择并持久化一个与当前 repo 绑定的端口”。
# 同一 repo 后续启动会优先复用该端口（若端口可用）。
# myworktree 会输出完整 URL（包含实际端口）。
# /path/to/myworktree/myworktree -listen 127.0.0.1:0
# /path/to/myworktree/mw -listen 127.0.0.1:0
mw

# （可选）使用固定端口
# /path/to/myworktree/myworktree -listen 127.0.0.1:50053
# /path/to/myworktree/myworktree -open=true
```
运行成功后，`mw` 默认会自动打开浏览器访问对应 URL。
`myworktree` 默认只打印 URL；如果也想自动打开浏览器，可传 `-open=true`。

myworktree 会用**当前工作目录**定位目标项目（git root），并基于该 git root 计算独立的数据目录，因此要管理其他项目时，只需要在另一个项目仓库目录下运行同一个 myworktree 二进制即可。

默认情况下，新建 worktree 会放在主仓库的同级目录下：
`<repo父目录>/<repo目录名>-myworktree/<worktree名>/`。
如果你想切回旧行为（放在每个项目的数据目录下），用 `-worktrees-dir=data`；也可以把 `-worktrees-dir` 设置为自定义路径。

## 常用命令
```bash
# version
myworktree --version
mw version

# worktree
myworktree worktree new "修复登录 401 并补测试"
myworktree worktree list
myworktree worktree delete <worktreeId>

# tags
myworktree tag list

# instance
myworktree instance start --worktree <worktreeId> --tag <tagId>
myworktree instance start --worktree <worktreeId> --cmd "echo hello && ls"
myworktree instance start --worktree <worktreeId>  # 启动一个可交互 shell instance
myworktree instance list
myworktree instance stop <instanceId>
```

## Tag 配置

Tag 会从以下位置合并加载：
- 全局：`$(os.UserConfigDir())/myworktree/tags.json`
- 项目：`$(os.UserConfigDir())/myworktree/<repoHash>/tags.json`

示例：

```json
{
  "tags": [
    {
      "id": "backend-dev",
      "command": "npm run dev",
      "preStart": "npm install",
      "cwd": "apps/backend",
      "env": {
        "NODE_ENV": "development"
      }
    }
  ]
}
```

## 本地测试与 CI

建议在发起 PR 前先执行本地检查：

```bash
test -z "$(gofmt -l .)"
go test ./...
go build -o myworktree ./cmd/myworktree
go build -o mw ./cmd/mw
```

GitHub Actions（`.github/workflows/go-ci.yml`）会在以下场景运行：
- push 到 `develop` 和 `main`
- 目标分支为 `develop` 或 `main` 的 Pull Request（`opened`、`synchronize`、`reopened`、`ready_for_review`）

工作流会在 Ubuntu 和 macOS 上校验 `gofmt`、执行 `go test ./...`，并构建两个二进制。

带 `v*` 标签的发布会触发 `.github/workflows/release.yml`，产出 darwin `amd64` / `arm64` 压缩包和 SHA256 校验文件。

## 远程访问
- 默认只监听本机回环地址。
- 监听到非 loopback（如 `0.0.0.0` 或局域网 IP）时必须提供 `--auth`。
- 需要 HTTPS 时提供 `--tls-cert` 与 `--tls-key`。
- 简单客户端可用 `?token=<token>`，但更推荐 `Authorization: Bearer <token>`，避免 token 落入浏览器历史或 shell 历史。

## License
MIT 协议，详见 [LICENSE](./LICENSE)。
