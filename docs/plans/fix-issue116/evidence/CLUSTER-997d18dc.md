## 会话 997d18dc48ee49c84f6fbf5b797969f7.jsonl  事件 211  seq 1–211

### 几何聚类（按事件数排序）

| n | 签名 | vv.height | padBot | 底边误差 vvBottom−appBot | workspace−xtermRows | 根背景链 | seq 区间 | hint |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 114 | `scale=1 pinned occl=68.5 landscape kind=pty - -` | 633.5 | 20px | 0.0 | -55.5…12.5 | transparent/transparent/transparent | 11–13,74–125,141–162,166–183,191–200,202–210 | manual |
| 63 | `scale=1 css occl=0 landscape kind=pty - -` | 702.0 | 20px | 0.0 | 13.0…438.0 | transparent/transparent/transparent | 1–10,29–73,163–165,187–190,201 | manual |
| 15 | `scale=1 pinned occl=428 landscape kind=pty - -` | 274.0 | 20px | 0.0 | -347.0…10.0 | transparent/transparent/transparent | 14–28 | softkb |
| 15 | `scale=1 pinned occl=68.5 landscape kind=pty QK -` | 633.5 | 20px | 0.0 | 12.5 | transparent/transparent/transparent | 126–140 | manual |
| 4 | `scale=1 pinned occl=0 landscape kind=pty - -` | 702.0 | 20px | 68.5 | 12.5 | transparent/transparent/transparent | 184–186,211 | - |

### 人工 hint 事件（分类的唯一直接证据）

| seq | hint | vvH | vvTop | inner | appBot | inline | padBot | wsBot | termBot | kind | status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 24 | keyboard-hint | 274.0 | 0.0 | 702 | 274.0 | 274px | 20px | 254.0 | 254.0 | pty | running |

### 覆盖矩阵自动判定

| 覆盖项 | 事件数 | 判定 |
| --- | --- | --- |
| 静止 scale=1 未钉高（外壳基线） | 63 | ✅ 有数据 |
| 钉高态（有遮挡补偿） | 148 | ✅ 有数据 |
| pan / offsetTop>0.5（坐标线证据） | 0 | ❌ 缺 |
| 负 app.top（拖动平移） | 0 | ❌ 缺 |
| 缩放豁免 scale≠1 | 0 | ❌ 缺 |
| 横屏 | 211 | ✅ 有数据 |
| 竖屏 | 0 | ❌ 缺 |
| iframe kinds（webPanel 有高度） | 0 | ❌ 缺 |
| pty kind | 211 | ✅ 有数据 |
| quick-keys 打开 | 15 | ✅ 有数据 |
| 妙控 hint | 0 | ❌ 缺 |
| 软键盘 hint | 1 | ✅ 有数据 |
| focusin（焦点进入） | 4 | ✅ 有数据 |
| pageshow / visibilitychange（生命周期） | 0 | ❌ 缺 |
| Split View（innerWidth < 屏幕短边） | 0 | ❌ 缺 |

### 三不变量原始分量（scale=1 且未钉高的静止段）

| inner | vv.height | app.bottom | padBot | ws.bottom | term.bottom | xtermRows.bottom | 方向 | 次数 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 702 | 702.0 | 702.0 | 20px | 682.0 | 682.0 | 669.0 | landscape-primary | 58 |
| 702 | 702.0 | 702.0 | 20px | 682.0 | 682.0 | 601.0 | landscape-primary | 4 |
| 702 | 702.0 | 702.0 | 20px | 682.0 | 682.0 | 244.0 | landscape-primary | 1 |
