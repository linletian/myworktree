# 内存预警处置 Playbook(issue #111 P2)

> **myworktree 只检测,处置在你。**
> 本文档的每一条"动作"都是**用户动作**——myworktree 不 kill、不 reap、不设
> cgroup 限额、不自动重启、不替用户处置任何进程。预警的含义永远是
> "你该看看了",不是"我替你处理了"。

## 视野边界(先读这个)

myworktree 的可见范围 = **自己的实例子树**(每个 running 实例的主 PID +
全部后代)+ **系统级信号**(PSI / 可用内存)。

同一个终端 scope 里的兄弟进程——其他 daemon、会话 CLI(opencode / kimi /
dsh-tui)、浏览器栈——**不在视野内**。所以:

- 预警是**局部真相**。status bar 亮灯说明"myworktree 的树 + 系统水位"出问题了;
  它不亮,不代表你的 scope 是安全的(2026-10-13 21:48 那次 global OOM,
  受害者是一个 myworktree 完全看不见的 2GB node 进程)。
- 全景自查请用系统工具:`journalctl`、`systemctl --user`、`ps`、`df -h /tmp`。

## 信号逐条

每条信号前的 `<a id="...">` 是深链锚点:myworktree 的每条 advice 都指向
`docs/PLAYBOOK-memory-alerts.md#<id>`(如 `#psi-memory`)。这是**显式锚点**,
不是标题的自动 slug(后者会嵌入中文标题,无法稳定命中);请保持每个 id 与 advice
引用的锚点一致(由 `TestPlaybookAdviceAnchorsAreReachable` 钉住)。

<a id="psi-memory"></a>
### PSI 内存压力 `some avg10`(`#psi-memory`)

| 项 | 内容 |
|---|---|
| 来源 | `/proc/pressure/memory` 的 `some avg10` |
| 阈值 | **≥30% 琥珀 / ≥50% 红** |
| 含义 | 最近 10s 内有一定比例的墙钟时间,至少一个任务因内存回收而停滞。systemd-oomd 的裁决线正是 **>50% 持续 >20s**(issue #111 journalctl 实录)——预警阈值刻意低于它,保证**预警永远先于裁决**。红 = oomd 此刻已在挑选受害者。 |
| 用户动作 | 琥珀:立刻打开 Resource Monitor 看哪个实例子树在涨;优先**关闭不用的终端标签页**、**停掉空闲实例**。红:同上,但要**立即**执行——此刻每拖 20s 都可能有人被 SIGKILL;若一时找不到大户,`systemctl --user status` 查 scope 全貌(视野外的部分只能靠系统工具)。 |

<a id="available-ram"></a>
### 系统可用内存(`#available-ram`)

| 项 | 内容 |
|---|---|
| 来源 | gopsutil `VirtualMemory()`(Available / Total) |
| 阈值 | **<15% 琥珀** |
| 含义 | 物理内存余量进入回收墙前的最后一段安全区。事故机(22G、无 swap)两次 OOM 前可用内存都落在 10% 上下;15% 是"现在动手还来得及"的哨兵。 |
| 用户动作 | 关标签页、停实例、清 `/tmp`(见下);机器长期如此,**加 swap(zram)** 是唯一改变"死不死"的杠杆(issue #111 评论四 P0,需要机主 sudo 决策——myworktree 不会也不该碰)。 |

<a id="subtree-rss"></a>
### 单实例子树 RSS(`#subtree-rss`)

| 项 | 内容 |
|---|---|
| 来源 | Resource Monitor 的进程树聚合(主 PID + 全部后代 RSS)÷ 系统总内存 |
| 阈值 | **>40% 琥珀** |
| 含义 | 一个你托管的实例树吃掉了机器五分之二以上的内存。在 #111 P2 之前这一整块是盲区(UI 原文案就是 "Child processes spawned by instances are not counted")。 |
| 用户动作 | 展开该实例的子树明细,按 RSS 从大到小**逐个确认身份**(name/pid/ppid/存活时长);确属泄漏 → **由你停掉该实例**(或杀掉明细里那个明知的漏——用你自己的终端命令,myworktree 不动手);确属正常负载 → 记录基线,考虑降低并发。 |

<a id="child-count"></a>
### 单实例子进程数(`#child-count`)

| 项 | 内容 |
|---|---|
| 来源 | 同一进程树聚合的 descendant 计数 |
| 阈值 | **>50 琥珀** |
| 含义 | 数量是泄漏最便宜的指纹。事故当天该信号会在约 200 个 `fake-serve.py` 孤儿上长亮——正常工具链很少超过 50。 |
| 用户动作 | 展开明细,看 name 是否重复成串(同一脚本 ×N = 泄漏实锤);确认来源后由你决定:停实例、修产生它的脚本/测试。计数带 `walk capped` 标注时是**下限**,真实数只会更多。 |

<a id="daemon-rss"></a>
### mw daemon 自身 RSS(`#daemon-rss`)

| 项 | 内容 |
|---|---|
| 来源 | daemon 进程自己的 RSS(现有采样) |
| 阈值 | **>1 GiB 琥珀** |
| 含义 | 取证显示 mwbeta 稳态 300–700MB、无单调增长;破 1GiB 说明 daemon 本体偏离健康带(如 ring buffer 顶到上限、连接堆积)。 |
| 用户动作 | 打开 Resource Monitor 看 daemon 行与各实例 buffer;必要时**由你择机重启 daemon**(这是用户动作——选一个没有活跃会话的窗口)。 |

## 通用回路

1. **看见**:status bar 亮灯(`⚠ N alerts`,琥珀/红)。点击 → Resource Monitor。
2. **定位**:modal 顶部 alerts 区告诉你哪个信号越过哪条线;展开实例子树看是谁。
3. **处置(全是你)**:关标签页 / 停实例 / 清 `/tmp`(tmpfs 是内存公民,
   `df -hT /tmp` 看一眼)/ 加 swap(机主 sudo)。
4. **复核**:处置后看现值回落;若预警灭不掉且系统 PSI 持续高,受害者大概率在
   视野外的 scope 兄弟进程——转系统工具排查。

## 为什么阈值长这样(与 oomd 同源)

systemd-oomd 的裁决:**内存压力 >50% 持续 >20s 且有回收活动 → 杀**。本 playbook
与检测端的琥珀线钉在 **30%**:同一个信号源(`/proc/pressure/memory`),阈值更低,
所以只要 PSI 沿曲线爬,灯必先于刀。这是"预警先行于裁决"原则(issue #111 评论四)
的落地——不是玄学调参,是把哨兵钉在刽子手的同一块仪表上。
