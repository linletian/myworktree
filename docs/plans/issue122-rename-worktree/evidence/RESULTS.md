# 决策 E 实测记录 — headless Chrome CDP 探针（2026-10-16）

复现 issue #122 计划决策 E 的问题：**第一次 click 触发 `selectWorktree` → `render()` → `renderSidebar()` 重建 `#wt-list` innerHTML 之后，第二次 click 的 dblclick 是否仍能命中行上的内联 `ondblclick`？**

## 环境

- `google-chrome`（headless=new，CDP over WebSocket）、Node v24（内置 `WebSocket` 直连 CDP，无外部依赖）
- Firefox 158 在本环境无法启动可调试实例（`--remote-debugging-port` 不暴露 HTTP 端点），未覆盖

## 复现命令

```bash
google-chrome --headless=new --no-sandbox --remote-debugging-port=9333 about:blank &
node evidence/cdp.mjs      # cdp.mjs 内 /json/list 发现 target → Page.navigate file://.../test.html → Input.dispatchMouseEvent 双击
```

## 探针结构（test.html）

忠实复刻应用结构：`state.activeWT` 跟踪激活行；行 click 时若该行已激活则早退（不重建 DOM，对齐 `selectWorktree` 的 `if (state.activeWT === id) return;`），否则切换激活态并整体重建 `#wt-list` innerHTML（对齐 `render()` → `renderSidebar()`）；每行渲染均带内联 `ondblclick="startRename(...)"`（对齐计划 T3.2 的挂载方式）。

## 实测输出（`node cdp.mjs`）

```
REPLACE-mode dblclick on non-active row #b: activated@b | RENAME-STARTED@b | DOC-dblclick@b
KEEP-mode    dblclick on same row     #b: RENAME-STARTED@b | DOC-dblclick@b
KEEP-mode    dblclick on same row     #a: activated@a | RENAME-STARTED@a | DOC-dblclick@a
```

（REPLACE 模式 = 真实应用的非激活行路径：第一次 click 重建 DOM；KEEP = 已激活行早退路径。两路径均 `RENAME-STARTED`。）

## 早期变体探针的补充事实

当第二次 click **也**触发重建时（比真实应用多一次重建），dblclick 派发到已分离节点：该节点自身监听触发（`NEW1-dblclick@b(detached)`），document 级监听收不到（证明目标节点已脱离文档），但**内联 attribute handler 照常执行**。

## 结论

1. 非激活行双击：第一次 click 激活并同步重建 DOM，dblclick 仍触发行内联 handler（Chrome 实测）。
2. 已激活行双击：无 DOM 重建窗口，dblclick 命中同一连接节点，handler 执行——此路径为 issue 硬验收项，确定性成立、不依赖浏览器。
3. 结合代码事实（所有非主行均带同一内联 handler、首次重建同步、激活行早退），结论不依赖浏览器事件派发差异。

## 注意

- `cdp.mjs` 中 `Input.dispatchMouseEvent` 的第二次点击需带 `clickCount: 2` 才会合成 dblclick（两次 `clickCount: 1` 不触发）；这是探针 harness 的细节，与应用行为无关。
- 探针为一次性验证工具，不进应用代码路径；存证目的已达成，保留于本目录。
