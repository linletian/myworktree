# 端到端验证记录 — 真实 daemon + headless Chrome（2026-10-16）

对 §5 人工清单的机器化执行：**真实 daemon 二进制**（从本分支构建）+ **真实 git repo**（临时目录初始化）+ **headless Chrome over CDP**。不是桩，不是单测替身。

## 文件

- `e2e.mjs` — 主验收流（25 项断言）。建 scratch git repo → 起 daemon（`--listen 127.0.0.1:PORT --portal-port 0 --auth <token> --worktrees-dir data`）→ `POST /api/worktrees` 建工作树 → CDP 驱动页面：双击行 → 输入框出现/focus/select → 键入含 `<script>"'&` 和中文字符的名字 → Enter → Network 层核对 PATCH（恰好一条、trim、原样上车）→ 侧栏文本渲染（无注入）→ Esc / 空串 / 非空失焦三条退出路径 → IME 组合态跨 2s 轮询存活 → 主仓库行负向 → 至多一个 input → 新开 target（=用户重开页面）验证持久化 + `GET /api/worktrees` 读回 → `git worktree list` 与 Branch/Path 不变。
- `e2e-race.mjs` — B-F1 竞态场景（9 项断言）。用 CDP `Fetch` 域扣住所有 `*diverged*` 响应，双击**非激活**行（click1 经 `selectWorktree` 同步发出 diverged 请求、input 尚未创建），dblclick 后打起 IME 组合态，**再放行**被扣住的响应——正是「回包慢于双击间隔」的竞态窗口。断言 input/焦点/组合文本原样存活，且改名随后仍可正常提交。
- `e2e-main.log` / `e2e-race.log` — 实测输出：**25/25 PASS** 与 **9/9 PASS**。

## 复现

```bash
go build -o /tmp/issue122-e2e/myworktree ./cmd/myworktree   # 在仓库根执行
cd /tmp/issue122-e2e && node e2e.mjs && node e2e-race.mjs
```

## 过程中的 CDP 怪相（非产品缺陷，harness 适配）

- 页面一旦出现过 CDP 聚焦的 input，`Page.navigate` 不再响应（仅 dblclick 打开输入框即可复现，无提交也复现；bisect 证实）。
- `Page.reload` 可用，但会使该 target 的会话在本最小客户端里楔住（`Runtime.evaluate` 此后全线超时；`/json/list` 与页面 URL 正常）。
- 适配：持久化检查改用 `Target.createTarget` 开新 target（fresh renderer）——既是 harness 对策，也是「用户重开页面」的更忠实模型（新渲染实例从服务端状态完整重建，改名读回验证通过）。

## 本 E2E 抓到的真实缺陷（→ 双评审 → 修复 → 回归）

1. **blur 重入双重提交**：Enter 提交时 `renderSidebar()` 移除仍聚焦的 input，Chrome 在移除中途派发 `blur` 且节点仍可 `getElementById` → `onblur` 重入 `commitWTRename`（state 已清但 input 可读）→ 同一 body 第二条 PATCH；嵌套 `innerHTML` 写使渲染器不稳。调用栈与 Network 双 PATCH 均已留存（见 `e2e-main2.log` 时代的 debug 记录）。修复：两个 commit handler 顶部状态守卫。
2. 该竞态在桩测试中**结构性不可见**（桩的 render 同步删元素、`getElementById` 返回 null）——教训已写入 `sidebar_wt_rename.test.mjs` 的桩（移除中途调 shipped handler 并记录）。
