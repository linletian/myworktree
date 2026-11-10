# Issue #115 真机采集 — 控制台片段（2026-10-17，历史）

> **已被 `probe-v2` 取代（2026-11-09）**：现行方法见 `T5-IPHONE-AB.md` §一 / §十——页面自带探针 + 每 5s 回传服务端，不需要 Mac Web Inspector，也不需要手工装 `window.__im`。
> 本文件只作历史留存（复现旧轮次时参考）。原文两个 “Step 3” 为笔误：第一个是导出，第二个是“一键分析”。


采集日期 2026-10-17　目标实例 `docs`（`1d13eeebd40b`，唯一 running）
补 §5.1 的 5 项待补：`cat -v` 字节验证 / 长按重臂 / 实体键盘对照 / `via` 坐实 / astral 观察项

> 先切到 `docs` 实例、跑 `cat -v`，再按 §三 敲字。**装完 Step 0 不要刷新页面。**

---

## Step 0 — 装日志缓冲

整段粘进控制台输入框，回车。**必须看到 `buffer ready, len = 0` 才继续。**

```js
window.__im=[];const _o=console.log;console.log=function(){try{const s=arguments[0];if(String(s).indexOf("[ios-ime]")===0)window.__im.push(Array.prototype.map.call(arguments,x=>{try{return typeof x==="object"?JSON.stringify(x):String(x)}catch(e){return"?"}}).join(" "))}catch(e){}return _o.apply(console,arguments)};console.log("buffer ready, len =",window.__im.length)
```

装完先验一下缓冲活着（在终端敲一个字，然后）：

```js
__im.length
```

数字 > 0 就对了。

---

## Step 3 — 导出原始日志

```js
copy(__im.join("\n"))
```

整份日志进 Mac 剪贴板。存文件用：Mac 终端里 `pbpaste > /tmp/iosime-run2.txt`，然后把文件内容贴给我。

---

## Step 3 — 一键分析（这条替代手工读原始日志）

比 Step 3 导出更省事：直接粘进控制台回车，输出就是要的数字。

```js
(()=>{const kd=[],ku=[],dl=[],ins=[],pend=[],cnt={};for(const l of(__im||[])){const i=l.indexOf(" "),j=l.indexOf(" ",i+1);if(j<0)continue;const n=l.slice(i+1,j);let f={};try{f=JSON.parse(l.slice(j+1))}catch(e){}cnt[n]=(cnt[n]||0)+1;if(n==="keydown"&&f.keyCode===229&&f.t)kd.push(f.t);else if(n==="keyup"&&f.t)ku.push(f.t);else if(n==="input")ins.push(f);else if(n==="deliver")dl.push(f);else if(n==="pending")pend.push(f)}const d=[],q=kd.slice();for(const t of ku){if(q.length){const x=t-q.shift();if(x>0)d.push(x)}}d.sort((a,b)=>a-b);const qq=y=>d.length?d[Math.min(d.length-1,Math.floor((d.length-1)*y))]:null;const bc=(a,k)=>a.reduce((o,x)=>{const v=x[k]===undefined?"?":x[k];o[v]=(o[v]||0)+1;return o},{});const re=pend.filter(x=>x.rearmed===true);console.log("行数:",JSON.stringify(cnt),"\n敲击(仅229) n="+d.length,"min="+d[0],"p50="+qq(.5),"p90="+qq(.9),"max="+d[d.length-1],"\nvia:",JSON.stringify(bc(dl,"via")),"\nbranch:",JSON.stringify(bc(dl,"branch")),"\ndt: 非null="+ins.filter(x=>x.dt!=null).length,"null="+ins.filter(x=>x.dt==null).length,"\n重臂 pending 行数:",re.length,"| 其 baseline 长度序列:",JSON.stringify(re.map(x=>x.len)))})()
```

### 怎么读输出

| 输出项 | 判定 |
|---|---|
| `via` 分布 | `failsafe` 计数 = 0 → 坐实「750ms 从未触发」；若 > 0 → 说明有 keyup 丢失，failsafe 在干活 |
| `重臂 pending 行数` | > 0 → 长按连击确实连发 229 |
| `baseline 长度序列` | 全部为同一个数 → 「保留最早 baseline + 每次 keydown 重臂」语义证实 |
| `max` 敲击间隔 | 验证第一轮的 246ms 峰值可复现性（决定 750ms 余量结论稳不稳） |
| `dt: null` 计数 | 应落在个位数；全为 0 说明本轮没触发那条异序路径 |

---

## 采集顺序

1. 装 Step 0 → 验 `buffer ready`
2. 切到 `docs`，跑 `cat -v`
3. 敲 `！@123`（字节验证：应见 `M-oM-<M-!`、`@`、`1`）
4. **长按空格键 2–3 秒**（重臂语义——最关键的一项）
5. 敲 `abc`（英文直输，上轮样本不足）
6. 敲 `nihao` + 选字栏出 `你好`（合成路径对照）
7. 接实体键盘重复第 3 步（对照组）
8. astral：`a😀` 用 IME 换成 `a😃`，看 `cat -v` 是否为一个 `^?` 紧跟新 emoji 字节
9. 跑 Step 3 分析（+ `copy` 导出）→ 发我
