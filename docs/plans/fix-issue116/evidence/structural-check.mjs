#!/usr/bin/env node
// 结构自检（issue #116 契约 v3.2 = v3.0「满高壳 + PTY 局部避让 + 根不可滚」
// 底座，外加软键盘会话门（避让只在会话内：raw≥50% 布局高 + 可编辑焦点进入，
// 会话内按 raw 跟随，raw≤1/缩放/失焦离开；v3.1 的 0.25 无状态地板已退役）、
// #status-bar 改 fixed 触底、滚轮隔离）。
//
// 为什么单独要一个脚本，而不是只靠 node / go 的源码锚点：
// 源码锚点读的是**文本**，本脚本读的是**已服务的页面在真实浏览器引擎里
// 解析出来的计算样式、实际滚动范围与真实 rect**。它证明的是三条：
//
//     A. 这一页在当前浏览器里根本没有滚动机制（clip 无滚动机制）；
//     B. 壳是**满高**的，且满高不是"被钉成一个奇怪的数"；
//     C. 写进 PTY 宿主的 `bottom` 长写在真实引擎里**赢过**它创建时的
//        `inset` 简写——v3.0 的全部让位都压在这一个长写上。
//
// 而不是 v2.1 那种"滚动发生了，但被拉回 0"，也不是 v2.2 那种"壳被钉到
// vv.height"。所以断言分七组：
//
//   A. 根不可滚（结构）
//      - getComputedStyle(html).overflow === "clip" 且 body 同
//      - getComputedStyle(#app).position === "fixed"
//      - scrollingElement.scrollHeight <= clientHeight（没有可滚范围）
//      - window.scrollTo(0, 100) 之后 scrollY 仍然是 0
//      - documentElement.scrollTop = 100 之后仍然是 0
//   B. 内部滚动区必须仍能滚（S8，防止"把整页锁死"冒充通过）
//      - 找一个真正溢出的内部滚动容器，scrollTo 之后 scrollTop 真的变了，
//        并且此刻根 scrollY 仍然是 0
//   C. 壳满高（v3.0 改的就是这一组）
//      - appRect.bottom === clientHeight（±1）：**不再**拿 vv.height 比
//      - appRect.bottom > visualViewport.height 是**预期**（壳故意伸到键盘底下），
//        本脚本把它记成 note，永远不记 FAIL——v2.2 的"越底"判据已作废
//   D. PTY 宿主的局部避让在真实引擎里成立（v3.0 新增）
//      - 若页面里已有宿主：所有宿主的 inline bottom 必须一致（无残留），
//        且此刻无遮挡时必须都是 "0px"
//      - 无论有没有真宿主：用**和出厂工厂一模一样的 cssText**
//        （position:absolute; inset:0）临时挂一个 visibility:hidden 的盒子，
//        写 bottom:428px，量它的 rect.bottom 是否真的上移 428px
//        ——证明长写赢过简写；然后把临时盒子摘掉，不留痕
//   E. 控制列 / 状态栏的让位（本轮补齐：quick-keys 必须始终可达）
//      - #terminal-controls 计算样式必须 absolute + top:20px + bottom:20px
//        （原创间距，永不写 top：入口按钮不许跳）
//      - #status-bar 计算样式必须 bottom:0px（它只欠 occlusion 本身）
//      - 真页面上那两个盒子**一个都不写**（写了就挪动用户正在看的东西），
//        而是在 #workspace 里按出厂偏移临时挂一个 top:20/bottom:20 的盒子
//        和一个 max-height:100% 的子面板：写 bottom:448px 后必须抬升 428px、
//        top 一动不动、列缩短 428px、面板跟着缩短 428px 且内部可滚
//        计算样式（computedBottom/computedTop/panelMaxHeight）**必须**在写入
//        448px 之后、恢复 20px 之前取样——它们描述的是抬升态；恢复态另记
//        restored* 字段（自检里有专门区分两者的 fixture）
//      - 448 而不是 428：把宿主的让位值直接复制到控制列上，会让快捷键埋进
//        状态栏那条 20px 带里，所以这两个数**必须不同**，脚本按此判据核对
//   F. 侧栏分隔线是一根真实被采用的 hairline（本轮新增：#sidebar::after）
//      - 读 getComputedStyle(#sidebar, '::after') 的 content/position/top/
//        height/width/background/pointer-events，外加所在 @media 是否命中
//      - content 为 none 时 Chrome 返回的是**未解析的原创值**（height 会读成
//        "100%"→100px），所以几何判据必须在"盒子不存在"之前停下：一个不存在
//        的盒子报"高度错了"是机器造的假话
//      - headless 的 env(safe-area-inset-bottom) 恒为 0，computed height 因此
//        必须等于 #sidebar 的 padding-box 高度（clientHeight），
//        derivedInsetPx = heightPx - clientHeight 就是引擎实际采用的 inset；
//        headless 里它是 0。能区分 `100%` 与 `calc(100% + env(...))` 的**只有
//        被命中的那条原创声明文本**（CSSOM 读出来的，不是正则扫文件），
//        所以本脚本核对它。这证明浏览器采用了这条公式，**不**证明 iPad 的
//        safe-area 行为——那要真机（A0 SOP）。
//   G. 滚轮（本轮新增：trackpad/滚轮是通向同一个房间的第二扇门）
//      - CDP Input.dispatchMouseEvent type=mouseWheel 在**命中测试落在终端内**
//        的一点发一次真实输入（中心被浮层挡住就按网格挪开，挪不开就
//        INCONCLUSIVE，绝不对着浮层发然后假装量到了终端）
//      - 探针是一个后装的 document 冒泡监听器：它**永不** preventDefault，
//        所以它读到的 defaultPrevented=true 只可能来自比它更早注册的监听器，
//        也就是既有 initShellScrollLock；安装前后各取一次 document 上的
//        wheel 监听器数量（CDP DOMDebugger.getEventListeners），把
//        "后装"这件事变成观测值而不是断言
//      - 未产生 wheel / 未 preventDefault / 根 scrollY 动了 → FAIL
//      - 没有可测终端（无可见 PTY 宿主、无可见 #terminal-container）
//        → INCONCLUSIVE，不伪造 PASS
//      - 活动 term 的 buffer.viewportY before/after 只是**辅助**读数：
//        滚到底部时它本来就不动，缺 scrollback 也不许 FAIL；
//        本脚本**从不调用** term.scrollLines（不制造被观察的行为）
//      - 探针监听器与它建的全局一律拆干净；拆完再数一次监听器作清洁证据
//
// 用法（人工证据脚本，不进 CI；只用 Node 内建模块，令牌/端口一律外部传入）：
//   node docs/plans/fix-issue116/evidence/structural-check.mjs \
//       --chrome /usr/bin/google-chrome \
//       --url "http://127.0.0.1:38231/?token=***" \
//       [--width 1194] [--height 702] [--port 0] [--label authenticated-shell] \
//       [--wheel-delta 80] [--wheel-wait-ms 100]
//
// 也可以只解析参数与做接线自检： --self-test （不开浏览器）
//
// 注意：headless Chrome **不是 iOS Safari**。本脚本证明的是结构在 W3C 语义下
// 成立（clip 无滚动机制、fixed 脱离普通流、bottom 长写覆盖 inset 简写、
// ::after hairline 被采用、真实 mouseWheel 被后装监听器读到 defaultPrevented），
// 不能替代 iPadOS 真机对 visualViewport / 键盘 / 浮窗 / safe-area 的验收：
// headless 的 safe-area inset 恒为 0，滚轮也不是 iPad 的双指手势。
//
// 落盘证据不含令牌：日志与 JSON 里的 query token 统一脱敏。

import { mkdirSync, writeFileSync } from "node:fs";
import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const args = process.argv.slice(2);
const arg = (name, fallback = "") => {
  const i = args.indexOf("--" + name);
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};

const redact = (u) => String(u).replace(/([?&]token=)[^&]*/gi, "$1<redacted>");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// --- the page-side measurement, run verbatim inside the page ---------------
// Returns plain JSON. It MUST NOT scroll anything except where a check says so;
// a script that scrolls the page to see whether it comes back is the v2.1 shape
// this file exists to replace.
const MEASURE = `(() => {
  const cs = (el) => getComputedStyle(el);
  const html = document.documentElement;
  const body = document.body;
  const app = document.getElementById("app");
  const root = document.scrollingElement || html;
  const out = {
    htmlOverflow: cs(html).overflow,
    bodyOverflow: cs(body).overflow,
    htmlPosition: cs(html).position,
    bodyPosition: cs(body).position,
    appPosition: app ? cs(app).position : null,
    appTop: app ? cs(app).top : null,
    appLeft: app ? cs(app).left : null,
    rootScrollHeight: root ? root.scrollHeight : null,
    clientHeight: html.clientHeight,
    // The tier the media queries resolved against. The divider's ::after lives in
    // '@media not all and (max-width: 768px)', so which layout is in force — flex
    // sibling with a hairline, or absolute drawer whose edge already reaches the
    // physical bottom — is a fact about the WIDTH, and must be read as one.
    clientWidth: html.clientWidth,
    vvHeight: window.visualViewport ? window.visualViewport.height : null,
    scrollBefore: window.scrollY,
    scrollAfterScrollTo: null,
    scrollTopAfterWrite: null,
    inner: { candidate: null, scrolled: null, rootScrollYDuringInner: null },
    divider: { sidebarFound: false, authoredRules: [] },
  };
  // A. the root must refuse to move, through both doors.
  window.scrollTo(0, 100);
  out.scrollAfterScrollTo = window.scrollY;
  try { html.scrollTop = 100; } catch (e) { /* refusing is also a pass */ }
  out.scrollTopAfterWrite = html.scrollTop;
  // B. an INNER scroller must still work. Pick one that actually overflows and
  //    whose computed overflow-y says auto|scroll — a page where nothing scrolls
  //    is a locked page, not a fixed shell.
  const nodes = Array.prototype.slice.call(document.querySelectorAll('*'));
  for (const el of nodes) {
    if (el === html || el === body) continue;
    const oy = cs(el).overflowY;
    if ((oy === 'auto' || oy === 'scroll') && el.scrollHeight > el.clientHeight + 1) {
      const before = el.scrollTop;
      el.scrollTop = before + 40;
      out.inner = {
        candidate: el.id ? '#' + el.id : (el.className ? String(el.className).split(' ')[0] : el.tagName),
        before: before,
        scrolled: el.scrollTop,
        rootScrollYDuringInner: window.scrollY,
      };
      el.scrollTop = before;
      break;
    }
  }
  if (app) {
    const r = app.getBoundingClientRect();
    out.appRect = { top: r.top, bottom: r.bottom, height: r.height };
    // v3.0: the shell is FULL HEIGHT, so the number that describes it is the
    // LAYOUT height. visualViewport is recorded next to it only so a reader can
    // see the deliberate overlap — appRect.bottom > vvHeight is the contract.
    out.layoutClientHeight = html.clientHeight;
    out.appInlineHeight = app.style.height || "";
  }
  // F. THE SIDEBAR / INSTANCE DIVIDER (contract v3.1), read out of a real engine.
  //    The rail's border-right stops inside #app's safe-area padding, so the
  //    hairline that carries the divider across the last strip is a pseudo-element:
  //    '#sidebar::after', 1px wide, top 0, height calc(100% + env(safe-area-inset-
  //    bottom)), pointer-events:none, painted with --border-color. Computed styles
  //    are what an engine DECIDED, which is why they are read here and not grepped.
  //      * content says whether the box exists AT ALL. When it does not, Chrome
  //        hands back the UNSPECIFIED values (height reads "100%" → 100px), so the
  //        geometry must not be judged in that case — reporting "the divider is
  //        100px tall" about a box that was never generated would be a machine-made
  //        lie. 'generated' is the gate every geometry judgement runs behind.
  //      * headless Chrome reports env(safe-area-inset-bottom) as 0, so the
  //        computed height equals #sidebar's padding-box height (clientHeight) and
  //        derivedInsetPx (height − clientHeight) is the inset the engine actually
  //        adopted, 0 here. What separates 'height:100%' from the authored
  //        'calc(100% + env(safe-area-inset-bottom))' in a browser whose inset is 0
  //        is ONLY the declaration that won — so the adopted rule text is read out
  //        of the CSSOM too (recursing into the @media block, which is where that
  //        rule lives and which a flat walk over styleSheets misses entirely).
  //      * the extra numbers (rect, clientWidth, left) pin WHERE the hairline is:
  //        left resolves to #sidebar's padding-box right edge, so [left, left+width]
  //        is the border's own column and the rail is never widened.
  const sidebar = document.getElementById("sidebar");
  const num = (v) => { const n = parseFloat(v); return Number.isFinite(n) ? n : null; };
  if (sidebar) {
    const sr = sidebar.getBoundingClientRect();
    const sc = getComputedStyle(sidebar);
    const ps = getComputedStyle(sidebar, "::after");
    const authored = [];
    let unreadableSheets = 0;
    const walk = (rules) => {
      for (const rule of rules) {
        if (!rule) continue;
        if (rule.selectorText && /#sidebar\s*::after/.test(rule.selectorText)) {
          const mediaText = rule.parentRule && rule.parentRule.media ? rule.parentRule.media.mediaText : null;
          let mediaMatches = true;
          if (mediaText !== null) {
            try { mediaMatches = window.matchMedia(mediaText).matches; } catch (e) { mediaMatches = null; }
          }
          const st = rule.style || {};
          authored.push({
            selectorText: rule.selectorText,
            mediaText: mediaText,
            mediaMatches: mediaMatches,
            content: st.content || "",
            position: st.position || "",
            top: st.top || "",
            width: st.width || "",
            height: st.height || "",
            pointerEvents: st.pointerEvents || "",
          });
        }
        if (rule.cssRules && rule.cssRules.length) walk(rule.cssRules);
      }
    };
    for (const sheet of document.styleSheets) {
      let rules = null;
      try { rules = sheet.cssRules; } catch (e) { unreadableSheets++; continue; }
      walk(rules);
    }
    const borderTop = num(sc.borderTopWidth) || 0;
    const rawHeight = num(ps.height);
    const rawWidth = num(ps.width);
    const rawLeft = num(ps.left);
    const contentRaw = ps.content || "";
    const isGen = contentRaw !== "none" && contentRaw !== "";
    // When the box is not generated the raw computed STRINGS stay in the record —
    // they are exactly what the engine reported, and 'content: none with height
    // "100%"' IS the finding — but every DERIVED number is nulled. Arithmetic on
    // values that describe no box is not a measurement, and a "derived inset -602"
    // sitting next to real numbers would read like one.
    const heightPx = isGen ? rawHeight : null;
    const widthPx = isGen ? rawWidth : null;
    const leftPx = isGen ? rawLeft : null;
    out.divider = {
      sidebarFound: true,
      appCollapsed: !!(document.getElementById("app") && document.getElementById("app").classList.contains("collapsed")),
      sidebarRect: { top: sr.top, bottom: sr.bottom, left: sr.left, right: sr.right, width: sr.width, height: sr.height },
      sidebarClientHeight: sidebar.clientHeight,
      sidebarClientWidth: sidebar.clientWidth,
      sidebarPosition: sc.position,
      sidebarBorderRightWidth: sc.borderRightWidth,
      // The pseudo's own containing block is #sidebar's PADDING box, so the length
      // height:100% was resolved against is clientHeight — recorded next to the
      // resolved height so a reader can do the subtraction without trusting us.
      content: contentRaw,
      generated: isGen,
      position: ps.position,
      top: ps.top,
      left: ps.left,
      width: ps.width,
      height: ps.height,
      pointerEvents: ps.pointerEvents,
      visibility: ps.visibility,
      background: (() => { try { return ps.background || null; } catch (e) { return null; } })(),
      backgroundColor: ps.backgroundColor,
      backgroundImage: ps.backgroundImage,
      heightPx: heightPx,
      widthPx: widthPx,
      leftPx: leftPx,
      // what the engine added past the rail's own box: the inset it adopted.
      // 0 in headless Chrome, ~20 on the measured iPad. NOT a simulation: this
      // number only says which formula the browser resolved, nothing about iOS.
      derivedInsetPx: heightPx === null ? null : Math.round((heightPx - sidebar.clientHeight) * 100) / 100,
      // the hairline's own edges, in the same coordinate space as #app's rect
      pseudoTopPx: heightPx === null ? null : Math.round((sr.top + borderTop) * 100) / 100,
      pseudoBottomPx: heightPx === null ? null : Math.round((sr.top + borderTop + heightPx) * 100) / 100,
      pseudoRightEdgePx: (leftPx === null || widthPx === null) ? null : Math.round((leftPx + widthPx) * 100) / 100,
      authoredRules: authored,
      unreadableSheets: unreadableSheets,
    };
  }
  // D. the PTY host's own bottom, in a real engine. The whole of v3.0's local
  //    avoidance rides on ONE longhand written AFTER an 'inset' shorthand, so the
  //    cascade question has to be asked of an engine and not of a regex.
  const container = document.getElementById("terminal-container");
  out.host = { containerFound: !!container, containerBottom: null, inlineBottoms: [], rectBottoms: [], probe: null };
  if (!container) return out;
  out.host.containerBottom = container.getBoundingClientRect().bottom;
  const live = container.querySelectorAll(':scope > [data-instance-id]');
  for (const el of live) {
    out.host.inlineBottoms.push(el.style.bottom || "");
    out.host.rectBottoms.push(el.getBoundingClientRect().bottom);
  }
  // The temporary box is built with the EXACT cssText the shipped factories use
  // (createTerminalHost here, PtyRenderer._createSession in kinds/pty.js), kept
  // visibility:hidden so it cannot alter what the user sees, and removed at the
  // end. This measures the cascade, nothing else.
  const probe = document.createElement("div");
  probe.dataset.instanceId = "structural-probe-host";
  probe.style.cssText = "position:absolute; inset:0; display:none; visibility:hidden;";
  probe.style.display = "block";
  container.appendChild(probe);
  const flush = probe.getBoundingClientRect().bottom;
  probe.style.bottom = "428px";
  const lifted = probe.getBoundingClientRect().bottom;
  out.host.probe = {
    insetBottom: flush,
    writtenBottom: "428px",
    liftedBottom: lifted,
    movedBy: Math.round((flush - lifted) * 100) / 100,
    computedBottom: getComputedStyle(probe).bottom,
  };
  probe.remove();
  // E. 控制列与状态栏在真实引擎里的让位（v3.0 本轮补齐的一半）。#workspace 恒为
  //    满高，所以 quick-keys 入口/面板与传输状态栏若不被同一次避让抬起，就会被键盘
      //    压在底下——而"输入框与快捷键始终可达"一直是原始需求，不是留给评审的选择题。
  //    这里不碰真页面上的那两个盒子（写它们会挪动用户正在看的东西），而是在
  //    #workspace 里按**出厂 CSS 一模一样的偏移**（top:20px; bottom:20px 的控制列，
  //    以及 max-height:100% 的面板）临时挂一个 visibility:hidden 的盒子，量三件事：
  //      1) 只写 bottom 长写（20 + 428 = 448px）能不能把盒子抬 428px；
  //      2) top:20px 是否**纹丝不动**（入口按钮不许跳）；
  //      3) 盒子缩短 428px 时，max-height:100% 的子面板是否跟着缩短 428px
  //         —— 这才是"面板跟着变矮、内部滚动"的引擎级证明。
  //    量完立刻摘掉，不留痕。
  const workspace = document.getElementById("workspace");
  out.overlay = { workspaceFound: false, controls: null, statusBar: null, quickKeys: null, probe: null };
  if (!workspace) return out;
  out.overlay.workspaceFound = true;
  const peek = (id) => {
    const el = document.getElementById(id);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    const c = getComputedStyle(el);
    return {
      id: id,
      computedPosition: c.position,
      computedTop: c.top,
      computedBottom: c.bottom,
      inlineBottom: el.style.bottom || "",
      display: c.display,
      rect: { top: r.top, bottom: r.bottom, height: r.height },
    };
  };
  out.overlay.controls = peek("terminal-controls");
  out.overlay.statusBar = peek("status-bar");
  out.overlay.quickKeys = peek("quick-keys-panel");
  const column = document.createElement("div");
  column.style.cssText = "position:absolute; left:0; top:20px; right:0; bottom:20px; height:auto; visibility:hidden; pointer-events:none;";
  const panel = document.createElement("div");
  panel.style.cssText = "position:absolute; top:0; left:0; width:10px; max-height:100%; overflow-y:auto; visibility:hidden;";
  const filler = document.createElement("div");
  filler.style.cssText = "height:4000px;";
  panel.appendChild(filler);
  column.appendChild(panel);
  workspace.appendChild(column);
  const rest = column.getBoundingClientRect();
  const panelRest = panel.getBoundingClientRect();
  column.style.bottom = "448px";
  const liftedRect = column.getBoundingClientRect();
  const liftedPanel = panel.getBoundingClientRect();
  // getComputedStyle returns a LIVE declaration: the strings are resolved at the
  // moment you read the property, not at the moment you obtained the declaration.
  // So every lifted-state computed value has to be COPIED OUT HERE, while bottom
  // is still the written 448px. Reading them after the restore below is what made
  // this instrument report the resting 20px as if the lift had lost the cascade
  // (the #116 v3.0 tool bug): the rect numbers said 428px of lift, the computed
  // number said 20px, and the FAIL was about the instrument's ordering.
  const liftedComputed = {
    computedBottom: getComputedStyle(column).bottom,
    computedTop: getComputedStyle(column).top,
    panelMaxHeight: getComputedStyle(panel).maxHeight,
    panelScrollable: panel.scrollHeight > panel.clientHeight,
    // Recorded next to panelScrollable so a reader can see WHY it says what it
    // says: with the 4000px filler these two should be ~4000 vs the lifted
    // height. A 0/0 pair means the subtree had no layout at all, which is a
    // NOTE about the DOM state, never a verdict about the product.
    panelScrollHeight: panel.scrollHeight,
    panelClientHeight: panel.clientHeight,
    inlineBottomWhileLifted: column.style.bottom || "",
  };
  column.style.bottom = "20px";
  const backRect = column.getBoundingClientRect();
  const backPanel = panel.getBoundingClientRect();
  // The RESTORED state gets its own fields, never the lifted ones: "did the lift
  // happen" and "did the probe clean up after itself" are two different claims,
  // and one field cannot answer both.
  const restoredComputed = {
    restoredComputedBottom: getComputedStyle(column).bottom,
    restoredComputedTop: getComputedStyle(column).top,
    restoredPanelMaxHeight: getComputedStyle(panel).maxHeight,
    inlineBottomWhileRestored: column.style.bottom || "",
  };
  out.overlay.probe = {
    writtenBottom: "448px",
    restBottom: rest.bottom,
    restHeight: rest.height,
    liftedBottom: liftedRect.bottom,
    liftedHeight: liftedRect.height,
    topStaysAt: liftedRect.top,
    restTop: rest.top,
    backBottom: backRect.bottom,
    // lifted-state reads (the judgement reads THESE)
    computedBottom: liftedComputed.computedBottom,
    computedTop: liftedComputed.computedTop,
    panelMaxHeight: liftedComputed.panelMaxHeight,
    panelScrollable: liftedComputed.panelScrollable,
    inlineBottomWhileLifted: liftedComputed.inlineBottomWhileLifted,
    // restored-state reads (cleanup evidence, separate fields)
    restoredComputedBottom: restoredComputed.restoredComputedBottom,
    restoredComputedTop: restoredComputed.restoredComputedTop,
    restoredPanelMaxHeight: restoredComputed.restoredPanelMaxHeight,
    inlineBottomWhileRestored: restoredComputed.inlineBottomWhileRestored,
    restoredHeight: backRect.height,
    panelRestHeight: panelRest.height,
    panelLiftedHeight: liftedPanel.height,
    panelBackHeight: backPanel.height,
  };
  column.remove();
  return out;
})()`;

// --- G. the wheel door: three page-side steps around ONE real input event -------
// A trackpad two-finger scroll emits `wheel`, not `touchmove`, and iPadOS does not
// honour the root clip against it (measured on p7, session 854069ba…: scrollTop
// walked non-zero in step while scrollHeight == clientHeight == 702 and html/body
// computed to overflow:clip). So the wheel path needs its OWN proof, and it cannot
// come from a script: it comes from CDP Input.dispatchMouseEvent type=mouseWheel,
// which enters the browser's input pipeline and produces a TRUSTED wheel event.
//
// WHEEL_PREP picks the point and installs the observer. WHEEL_READ collects and
// dismantles it. The observer never calls preventDefault — that single fact is
// what makes the reading meaningful: `defaultPrevented === true` seen at a bubble
// listener registered now can only have been set by a listener registered EARLIER,
// i.e. the shipped initShellScrollLock. Counting the document's wheel listeners
// either side of the install turns "installed after the lock" from an assumption
// into an observed number.
const WHEEL_PREP = `(() => {
  const out = {
    ready: false, reason: null, boxKind: null, point: null, hit: null,
    boxRect: null, centerPoint: null, centerHit: null, centerHitInsideBox: false,
    relocated: false, candidatesTried: 0, instrumentWalkSaysNativeScroller: null,
    termFound: false, termViewportYBefore: null, termBufferLength: null,
    scrollYBefore: null, scrollTopBefore: null, installed: false,
  };
  const describe = (el) => {
    if (!el) return null;
    if (el.id) return '#' + el.id;
    const cn = el.className && typeof el.className === 'string' ? String(el.className).trim().split(/\\s+/)[0] : "";
    return cn ? el.tagName.toLowerCase() + '.' + cn : el.tagName.toLowerCase();
  };
  const isVisible = (el) => {
    if (!el) return false;
    const c = getComputedStyle(el);
    if (c.display === 'none' || c.visibility === 'hidden' || parseFloat(c.opacity) === 0) return false;
    const r = el.getBoundingClientRect();
    return r.width > 8 && r.height > 8;
  };
  // THIS INSTRUMENT's own walk-up, mirroring the exemption the shell documents in
  // initShellScrollLock (a real overflow scroller or an iframe keeps its own scroll).
  // It is a heuristic for PICKING a point, never a verdict: the verdict is what the
  // page reports for the real event.
  const SCROLLABLE = /(auto|scroll)/;
  const nativeScroller = (el) => {
    let node = el;
    while (node && node !== document.body && node !== document.documentElement) {
      if (node.tagName === 'IFRAME') return true;
      let st = null;
      try { st = getComputedStyle(node); } catch (e) { st = null; }
      if (st && SCROLLABLE.test(st.overflowY || '') && node.scrollHeight > node.clientHeight + 1) return true;
      node = node.parentElement;
    }
    return false;
  };
  // The active xterm, if there is one. Read-only: this instrument never calls
  // scrollLines, never writes the buffer, never focuses — observing must not
  // create the behaviour it is about to measure.
  const activeTerm = () => {
    try {
      if (typeof window.getActiveTerminalSession === 'function') {
        const s = window.getActiveTerminalSession();
        if (s && s.term) return s;
      }
    } catch (e) { /* the shell may not be booted; that is not a failure */ }
    try {
      const r = window.__ptyRenderer;
      if (r && typeof r.allSessions === 'function') {
        for (const s of r.allSessions()) if (s && s.term) return s;
      }
    } catch (e) { /* same */ }
    return null;
  };
  const bufferY = (term) => {
    try { return term.buffer.active.viewportY; } catch (e) { return null; }
  };
  const container = document.getElementById('terminal-container');
  const hosts = container ? Array.prototype.slice.call(container.querySelectorAll(':scope > [data-instance-id]')) : [];
  let box = null;
  for (const el of hosts) { if (isVisible(el)) { box = el; out.boxKind = 'pty-host'; break; } }
  if (!box && isVisible(container)) { box = container; out.boxKind = 'terminal-container'; }
  if (!box) {
    out.reason = hosts.length
      ? hosts.length + ' PTY host(s) exist but none is visible, and #terminal-container is not visible either'
      : 'no visible PTY host and no visible #terminal-container in this DOM state';
    return out;
  }
  const r = box.getBoundingClientRect();
  out.boxRect = { top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width, height: r.height };
  const cx = r.left + r.width / 2, cy = r.top + r.height / 2;
  const clampX = (x) => Math.max(1, Math.min(window.innerWidth - 2, Math.round(x)));
  const clampY = (y) => Math.max(1, Math.min(window.innerHeight - 2, Math.round(y)));
  const inside = (el) => !!(el && (el === box || box.contains(el)));
  // The viewport-debug panel, a quick-keys panel or a modal can sit on top of the
  // terminal. Firing at a point owned by one of those and then crediting the result
  // to the terminal would be a lie (and the debug panel is itself an overflow
  // scroller, so the shell would be CORRECT not to preventDefault there). So the
  // point must be hit-tested: the centre first, then a ring, and only points that
  // resolve INSIDE the terminal box and are not native scrollers.
  const cand = [[cx, cy]];
  const stepX = Math.max(24, r.width / 6), stepY = Math.max(24, r.height / 6);
  for (let ring = 1; ring <= 4; ring++) {
    for (const [dx, dy] of [[-1, 0], [1, 0], [0, -1], [0, 1], [-1, -1], [1, -1], [-1, 1], [1, 1]]) {
      cand.push([cx + dx * stepX * ring, cy + dy * stepY * ring]);
    }
  }
  const centerX = clampX(cx), centerY = clampY(cy);
  const centerEl = document.elementFromPoint(centerX, centerY);
  out.centerPoint = { x: centerX, y: centerY };
  out.centerHit = describe(centerEl);
  out.centerHitInsideBox = inside(centerEl);
  let picked = null, hitEl = null;
  for (const [x, y] of cand) {
    const px = clampX(x), py = clampY(y);
    const el = document.elementFromPoint(px, py);
    out.candidatesTried++;
    if (!inside(el)) continue;
    if (nativeScroller(el)) continue;
    picked = { x: px, y: py }; hitEl = el; break;
  }
  if (!picked) {
    out.reason = inside(centerEl)
      ? 'every point that resolves inside the terminal box is a native scroller — the shell is entitled to hand this wheel to it, so the root door cannot be judged here'
      : 'every candidate point resolved outside the terminal box (covered by ' + (describe(centerEl) || 'nothing') + ')';
    return out;
  }
  out.point = picked;
  out.hit = describe(hitEl);
  out.relocated = picked.x !== centerX || picked.y !== centerY;
  out.instrumentWalkSaysNativeScroller = nativeScroller(hitEl);
  const sess = activeTerm();
  out.termFound = !!(sess && sess.term);
  out.termViewportYBefore = out.termFound ? bufferY(sess.term) : null;
  out.termBufferLength = out.termFound ? (bufferY(sess.term) === null ? null : (() => { try { return sess.term.buffer.active.length; } catch (e) { return null; } })()) : null;
  out.scrollYBefore = window.scrollY;
  out.scrollTopBefore = (document.scrollingElement || document.documentElement).scrollTop;
  window.__dshWheelProbe = { events: [] };
  window.__dshWheelListener = function __dshStructuralWheelProbe(e) {
    // Read, never act. No preventDefault, no scrollLines, no stopPropagation.
    window.__dshWheelProbe.events.push({
      type: e.type,
      eventPhase: e.eventPhase,
      atDocument: e.currentTarget === document,
      target: describe(e.target),
      clientX: e.clientX, clientY: e.clientY,
      deltaX: e.deltaX, deltaY: e.deltaY,
      deltaMode: e.deltaMode,
      ctrlKey: !!e.ctrlKey,
      isTrusted: !!e.isTrusted,
      defaultPrevented: !!e.defaultPrevented,
      scrollYAtEvent: window.scrollY,
    });
  };
  document.addEventListener('wheel', window.__dshWheelListener, { passive: false, capture: false });
  out.installed = true;
  out.ready = true;
  return out;
})()`;

const WHEEL_READ = `(() => {
  const p = window.__dshWheelProbe || { events: [] };
  let term = null, viewportY = null, bufferLength = null;
  try {
    if (typeof window.getActiveTerminalSession === 'function') {
      const s = window.getActiveTerminalSession();
      if (s && s.term) term = s.term;
    }
    if (!term && window.__ptyRenderer && typeof window.__ptyRenderer.allSessions === 'function') {
      for (const s of window.__ptyRenderer.allSessions()) if (s && s.term) { term = s.term; break; }
    }
    if (term) { viewportY = term.buffer.active.viewportY; bufferLength = term.buffer.active.length; }
  } catch (e) { /* auxiliary readings only */ }
  const out = {
    instrumentRan: true,
    events: p.events,
    eventCount: p.events.length,
    scrollYAfter: window.scrollY,
    scrollTopAfter: (document.scrollingElement || document.documentElement).scrollTop,
    termFoundAfter: !!term,
    termViewportYAfter: viewportY,
    termBufferLengthAfter: bufferLength,
  };
  // Dismantle the probe: the listener comes off the document, then the global handle
  // that named it goes too. Neither fact is asserted from the other: page-side JS
  // cannot enumerate listeners, so 'listenerDismantled' only proves the page holds no
  // reference to the handler any more, and the DOM-level proof is the third CDP
  // enumeration in main(), which must land back on the pre-install count.
  if (typeof window.__dshWheelListener === 'function') {
    document.removeEventListener('wheel', window.__dshWheelListener, { capture: false });
    out.removedViaHandle = true;
  } else {
    out.removedViaHandle = false;
  }
  delete window.__dshWheelListener;
  delete window.__dshWheelProbe;
  out.listenerDismantled = typeof window.__dshWheelListener === 'undefined';
  out.globalsRemoved = typeof window.__dshWheelProbe === 'undefined' && typeof window.__dshWheelListener === 'undefined';
  return out;
})()`;

// The document's wheel listeners, as the browser reports them. This is the only way
// to see a registration ORDER from outside: page-side JS cannot enumerate listeners.
// Chrome hands them back in registration order, and each entry carries passive /
// capture flags — the flags matter, because a passive listener's preventDefault is
// ignored, so a PASS that did not check them could be reading a prevented flag that
// no engine would honour.
async function documentWheelListeners(send, sessionId) {
  try {
    const doc = await send("Runtime.evaluate", { expression: "document" }, sessionId);
    if (!doc.result || !doc.result.objectId) return { available: false, reason: "no objectId for document" };
    const res = await send("DOMDebugger.getEventListeners", { objectId: doc.result.objectId, depth: 0 }, sessionId);
    const wheels = (res.listeners || []).filter((l) => l.type === "wheel");
    return {
      available: true,
      count: wheels.length,
      bubbleNonPassive: wheels.filter((l) => !l.useCapture && !l.passive).length,
      listeners: wheels.map((l) => ({ useCapture: !!l.useCapture, passive: !!l.passive, scriptId: l.scriptId, line: l.lineNumber, column: l.columnNumber })),
    };
  } catch (e) {
    return { available: false, reason: String((e && e.message) || e) };
  }
}

function check(m) {
  const fail = [];
  const note = [];
  const inconclusive = [];
  // The served UI answers without a session with a LOGIN gate that has no
  // #app at all. Asserting shell structure against that DOM would be a
  // machine-made lie in both directions (a PASS could not be about a shell
  // that is not there, and a FAIL would not be about the product), so the
  // absence of the shell is reported as its own verdict.
  if (m.appPosition === null) {
    inconclusive.push("no #app in the rendered DOM — this is the login gate or a page that never booted; run against the authenticated shell");
    return { fail, note, inconclusive };
  }
  const okOverflow = m.htmlOverflow === "clip" && m.bodyOverflow === "clip";
  if (!okOverflow) fail.push(`root overflow must be clip/clip, got ${m.htmlOverflow}/${m.bodyOverflow}`);
  if (m.htmlPosition === "fixed" || m.bodyPosition === "fixed") {
    fail.push(`neither html nor body may be positioned (2ac6c83's reverted shape); got html=${m.htmlPosition} body=${m.bodyPosition}`);
  }
  if (m.appPosition !== "fixed") fail.push(`#app must be position:fixed, got ${m.appPosition}`);
  if (m.appTop !== "0px" || m.appLeft !== "0px") fail.push(`#app must be anchored at 0/0, got ${m.appTop}/${m.appLeft}`);
  if (!(m.rootScrollHeight <= m.clientHeight)) {
    fail.push(`root has a scroll range: scrollHeight ${m.rootScrollHeight} > clientHeight ${m.clientHeight}`);
  }
  if (m.scrollAfterScrollTo !== 0) fail.push(`window.scrollTo(0,100) moved the page to ${m.scrollAfterScrollTo} — the root is still scrollable`);
  if (m.scrollTopAfterWrite !== 0) fail.push(`documentElement.scrollTop = 100 stuck at ${m.scrollTopAfterWrite} — clip is not in effect`);
  if (!m.inner || !m.inner.candidate) {
    note.push("no overflowing inner scroller was present in this DOM state (S8 unproven, not passed)");
  } else if (!(m.inner.scrolled > m.inner.before)) {
    fail.push(`inner scroller ${m.inner.candidate} refused to scroll (${m.inner.before} -> ${m.inner.scrolled}) — a locked page is not a fixed shell`);
  } else if (m.inner.rootScrollYDuringInner !== 0) {
    fail.push(`the inner scroller moved the ROOT too (scrollY ${m.inner.rootScrollYDuringInner})`);
  }
  // C. THE SHELL IS FULL HEIGHT (v3.0). The retired comparison against
  // visualViewport is gone on purpose: the shell now deliberately extends under
  // the keyboard, so appRect.bottom sitting BELOW vvHeight is the contract being
  // honoured, not a band. What must hold is the anchor to the layout height.
  if (m.appRect) {
    const layoutGap = (m.layoutClientHeight === null || m.layoutClientHeight === undefined ? m.clientHeight : m.layoutClientHeight) - m.appRect.bottom;
    if (Math.abs(layoutGap) > 1) fail.push(`#app must be FULL HEIGHT: clientHeight ${m.clientHeight} vs app bottom ${m.appRect.bottom} (layoutGap ${layoutGap.toFixed(2)}px) — a shorter shell is the retired v2.2 pin`);
    if (m.appInlineHeight) fail.push(`#app must carry NO inline height, got ${JSON.stringify(m.appInlineHeight)} — v2.2's pin to vv.height is back`);
    if (m.vvHeight !== null && m.appRect.bottom > m.vvHeight + 1) {
      note.push(`#app bottom ${m.appRect.bottom} is below visualViewport ${m.vvHeight}: EXPECTED under v3.0 (the shell extends under the keyboard; this is not a band)`);
    }
  }
  // D. THE HOST'S OWN BOTTOM, resolved by a real engine.
  const h = m.host;
  if (!h || h.containerFound !== true) {
    inconclusive.push("no #terminal-container in this DOM state — the PTY-local avoidance could not be measured");
  } else {
    const distinct = [...new Set(h.inlineBottoms)];
    if (h.inlineBottoms.length) {
      if (distinct.length > 1) fail.push(`PTY hosts disagree on their own bottom (${distinct.join(" / ")}) — a stale host is the band the next switch shows`);
      if (distinct.length === 1 && distinct[0] !== "0px") fail.push(`nothing is covered in headless Chrome, so every host should own bottom 0px, got ${JSON.stringify(distinct[0])}`);
      for (const bottom of h.rectBottoms) {
        if (Math.abs(bottom - h.containerBottom) > 1) fail.push(`an uncovered host must sit flush at the container bottom ${h.containerBottom}, got ${bottom}`);
      }
    } else {
      note.push("no live PTY host in this DOM state (web kind / no session): the host list is empty, the cascade probe below still runs");
    }
    const p = h.probe;
    if (!p) {
      inconclusive.push("the host cascade probe never ran");
    } else if (Math.abs(p.movedBy - 428) > 0.5 || p.computedBottom !== "428px") {
      fail.push(`writing host bottom 428px moved the host by ${p.movedBy}px (computed ${p.computedBottom}) — the longhand is losing to the inset shorthand or to another rule, so the keyboard avoidance does nothing`);
    }
  }
  // E. 控制列 / 状态栏的让位在真实引擎里成立（v3.0 本轮补齐）。headless 里键盘不占
  //    高度，所以 occlusion=0：两个盒子必须**恰好停在各自的原创值**上（控制列
  //    bottom 20px、状态栏 bottom 0），并且"只写 bottom 长写"必须能把控制列抬起来
  //    且让 max-height:100% 的面板跟着缩短——这两个数不同（448 与 428）正是本轮的
  //    要点：把宿主的 428 复制到控制列上，别的全对，快捷键却会埋进状态栏那条带里。
  const ov = m.overlay;
  if (!ov || ov.workspaceFound !== true) {
    inconclusive.push("no #workspace in this DOM state — the overlay column (quick-keys) and the status bar could not be measured");
  } else {
    const column = ov.controls;
    const strip = ov.statusBar;
    if (!column || !strip) {
      inconclusive.push(`#${column ? "status-bar" : "terminal-controls"} is absent from this DOM state — quick-keys reachability was never observed`);
    } else {
      if (column.computedPosition !== "absolute") fail.push(`#terminal-controls must be position:absolute inside #workspace, got ${column.computedPosition} — its bottom would not resolve against a full-height container`);
      if (column.computedBottom !== "20px") fail.push(`#terminal-controls' authored bottom must be 20px (the clearance over the transport strip), got ${column.computedBottom}`);
      if (column.computedTop !== "20px") fail.push(`#terminal-controls' top must stay authored at 20px — the quick-keys ENTRY button is not allowed to move, got ${column.computedTop}`);
      // #status-bar is position:FIXED since contract v3.1 — it resolves against the
      // viewport, not #workspace, so it stays pinned at the physical bottom and the
      // safe-area inset no longer shortens its rest position. #terminal-controls is
      // still absolute inside #workspace (its bottom resolves against that container).
      if (strip.computedPosition !== "fixed") fail.push(`#status-bar must be position:fixed (v3.1: pinned to the viewport bottom, immune to the safe-area inset that shortens #workspace), got ${strip.computedPosition}`);
      if (strip.computedBottom !== "0px") fail.push(`#status-bar' authored bottom must be 0px (it owes the occlusion itself, nothing more), got ${strip.computedBottom}`);
      if (column.display !== "none") {
        // The authored clearance is 20px, but since v3.1 the two boxes no longer
        // share a containing block: #terminal-controls is absolute inside #workspace
        // (whose padding box stops env(safe-area-inset-bottom) above the edge) while
        // #status-bar is FIXED to the viewport. The honest expectation is therefore
        // the authored 20px PLUS the safe-area inset the strip no longer inherits —
        // 40px on the measured iPad, and still exactly 20px in headless Chrome where
        // the inset is 0. Hardcoding 20 here would FAIL a correct iPad.
        const gap = strip.rect.bottom - column.rect.bottom;
        const anchor = m.host && Number.isFinite(m.host.containerBottom) ? m.host.containerBottom : null;
        if (anchor === null) {
          inconclusive.push("no measured #terminal-container bottom, so the safe-area inset behind the column/strip gap is unknown — the gap was not judged");
        } else {
          const expectedGap = 20 + Math.max(0, m.clientHeight - anchor);
          if (Math.abs(gap - expectedGap) > 1) fail.push(`the column must sit ${expectedGap}px above the strip's bottom edge (authored 20px + the ${Math.max(0, m.clientHeight - anchor)}px safe-area inset the fixed strip no longer inherits), got a gap of ${gap.toFixed(2)}px`);
        }
      }
    }
    const p = ov.probe;
    if (!p) {
      inconclusive.push("the overlay column lift probe never ran");
    } else {
      if (Math.abs(p.restBottom - p.liftedBottom - 428) > 0.5) fail.push(`writing the column bottom 448px lifted it by ${(p.restBottom - p.liftedBottom).toFixed(2)}px, not 428px — the quick-keys column does not follow the keyboard`);
      if (Math.abs(p.topStaysAt - p.restTop) > 0.5) fail.push(`lifting the column moved its TOP (${p.restTop} -> ${p.topStaysAt}) — the avoidance must write bottom only, or the quick-keys entry jumps`);
      if (Math.abs(p.restHeight - p.liftedHeight - 428) > 0.5) fail.push(`the column shortened by ${(p.restHeight - p.liftedHeight).toFixed(2)}px instead of 428px — its usable height does not shrink with the keyboard`);
      if (Math.abs(p.panelRestHeight - p.panelLiftedHeight - 428) > 0.5) fail.push(`the max-height:100% panel shortened by ${(p.panelRestHeight - p.panelLiftedHeight).toFixed(2)}px instead of 428px — the percentage is not resolving against the shortened column, so the key set runs under the keyboard`);
      if (Math.abs(p.backBottom - p.restBottom) > 0.5) fail.push(`restoring bottom 20px left the column at ${p.backBottom} instead of ${p.restBottom} — the lift is stranded`);
      // These two numbers come from the LIFTED state (see MEASURE): this is the
      // judgement that says "the avoidance longhand won the cascade right now",
      // and it is the one the v3.0 tool bug used to answer with the resting 20px.
      if (p.computedBottom !== "448px") fail.push(`computed bottom MEASURED WHILE LIFTED (inline bottom written to ${p.inlineBottomWhileLifted || "448px"}) is ${p.computedBottom} — another rule is beating the avoidance`);
      if (p.computedTop !== "20px") fail.push(`computed top while lifted is ${p.computedTop} — top must stay authored at 20px`);
      if (p.inlineBottomWhileLifted !== undefined && p.inlineBottomWhileLifted !== "448px") fail.push(`the probe's inline bottom while it was supposed to be lifted is ${JSON.stringify(p.inlineBottomWhileLifted)} — the sample was not taken in the lifted state, so the computed pair above proves nothing`);
      if (typeof p.restoredComputedBottom === "string") {
        if (p.restoredComputedBottom !== "20px") fail.push(`after restoring bottom 20px the computed bottom is still ${p.restoredComputedBottom} — the temporary lift is stranded in the cascade`);
        if (p.computedBottom === p.restoredComputedBottom) fail.push(`the lifted and restored computed bottoms are identical (${p.computedBottom}) — two samples taken in one state, or the restore never took; a column at bottom:448px cannot compute the same bottom as the same column at bottom:20px`);
      } else {
        note.push("this instrument did not record the restored computed bottom — cleanup is unproven (the lifted judgement is still read from the lifted fields)");
      }
      if (typeof p.restoredComputedTop === "string" && p.restoredComputedTop !== "20px") fail.push(`the restored computed top is ${p.restoredComputedTop} — the probe left #workspace's authored top changed`);
      if (!p.panelScrollable) note.push("the probe panel was not overflowable in this state (internal scroll unproven, not failed)");
    }
  }
  // F. THE DIVIDER (contract v3.1): a 1px hairline the engine actually adopted.
  //    Every number here came out of getComputedStyle, so the engine is the author of
  //    the claim. Three traps decide the shape of this block:
  //      1. no #sidebar → nothing was measured → INCONCLUSIVE, never a silent pass;
  //      2. `content: none` means the box does not EXIST, and Chrome then reports the
  //         UNSPECIFIED values (height reads back "100%" → 100px), so `generated`
  //         gates every geometry judgement: a FAIL about the height of a box that
  //         was never generated would be a fabricated defect;
  //      3. the narrow tier is the drawer form, where the wide-tier ::after is
  //         scoped off by design and the divider already reaches the physical bottom
  //         on its own — judging the hairline there would FAIL a correct layout.
  const dv = m.divider;
  if (!dv || dv.sidebarFound !== true) {
    inconclusive.push("no #sidebar in this DOM state — the ::after divider could not be observed");
  } else if (dv.appCollapsed === true) {
    // Collapsed rail: the collapse block hides #sidebar and the pseudo inherits the
    // visibility, so the claim is "no orphan hairline survives", not "1px at 0".
    if (dv.generated && dv.visibility === "visible") {
      fail.push(`the rail is collapsed but #sidebar::after is still generated and visible (content ${JSON.stringify(dv.content)}, visibility ${dv.visibility}) — an orphan hairline over the freed space`);
    } else {
      note.push(`the rail is collapsed, so the divider is hidden by inheritance (visibility ${dv.visibility}) — the hairline's geometry was not judged in this state`);
    }
  } else if (Number.isFinite(m.clientWidth) && m.clientWidth <= 768) {
    note.push(`narrow tier (${m.clientWidth}px <= 768): #sidebar is the absolute drawer at bottom:0 and the wide-tier ::after is scoped off by design — the divider was not judged here`);
  } else if (!Number.isFinite(dv.sidebarClientHeight) || !(dv.sidebarClientWidth > 0)) {
    inconclusive.push(`#sidebar has no measurable box (clientHeight ${dv.sidebarClientHeight}, clientWidth ${dv.sidebarClientWidth}) — the divider had no containing block to resolve against`);
  } else if (!dv.generated) {
    fail.push(`#sidebar::after is not generated (computed content ${JSON.stringify(dv.content)}) — nothing carries the divider across the strip the rail's border never reached; its geometry is left unjudged because the box does not exist`);
  } else {
    if (dv.position !== "absolute") fail.push(`#sidebar::after must be position:absolute (out of flow, so it never widens the rail), got ${dv.position}`);
    if (dv.widthPx !== 1) fail.push(`the divider must be exactly 1px wide, got ${JSON.stringify(dv.width)} (${dv.widthPx}px)`);
    if (dv.top !== "0px") fail.push(`the divider must start at the rail's top (top 0px), got ${JSON.stringify(dv.top)}`);
    if (dv.pointerEvents !== "none") fail.push(`the divider must be pointer-events:none (it must never eat a tap meant for the rail), got ${JSON.stringify(dv.pointerEvents)}`);
    const painted = (dv.backgroundColor && dv.backgroundColor !== "transparent") || (dv.backgroundImage && dv.backgroundImage !== "none");
    if (!painted) fail.push(`the divider has no paint (backgroundColor ${JSON.stringify(dv.backgroundColor)}, backgroundImage ${JSON.stringify(dv.backgroundImage)}) — an invisible hairline is not a divider`);
    if (dv.sidebarPosition === "static") fail.push(`#sidebar must be a containing block for the divider (position relative/absolute/fixed), got static — the hairline would resolve against another box entirely`);
    // Where it is: left resolves to the rail's PADDING-box right edge, so the hairline
    // covers [clientWidth, clientWidth + width] — the border's own column, flush with
    // the border-box edge. A hairline offset from that column paints a double line.
    if (Number.isFinite(dv.leftPx) && Number.isFinite(dv.widthPx)) {
      if (Math.abs(dv.leftPx - dv.sidebarClientWidth) > 1) fail.push(`the divider must sit on the rail's padding-box right edge (left ${dv.leftPx} vs clientWidth ${dv.sidebarClientWidth}) — offset by more than a pixel it paints a second line or a gap`);
      if (Number.isFinite(dv.pseudoRightEdgePx) && Math.abs(dv.pseudoRightEdgePx - dv.sidebarRect.right) > 1) fail.push(`the divider's outer edge ${dv.pseudoRightEdgePx} does not land on the rail's border-box right edge ${dv.sidebarRect.right} — it either widens the rail or leaves a gap`);
    } else {
      inconclusive.push(`the divider's computed left/width did not resolve to px (left ${JSON.stringify(dv.left)}, width ${JSON.stringify(dv.width)}) — its column could not be located`);
    }
    // The size FORMULA. At headless inset 0 the resolved height is clientHeight for
    // BOTH `height:100%` and `calc(100% + env(safe-area-inset-bottom))`, so the
    // resolved number cannot tell them apart and the declaration that won has to be
    // read. This is the honest boundary: the engine adopted the pseudo and the formula
    // it was authored with; what the formula does at inset 20 on an iPad is A0's job.
    const readable = Array.isArray(dv.authoredRules) && dv.authoredRules.length > 0;
    const adopted = (dv.authoredRules || []).filter((r) => r.mediaMatches !== false);
    const insetRules = adopted.filter((r) => /env\(\s*safe-area-inset-bottom/i.test(r.height || ""));
    if (Number.isFinite(dv.heightPx)) {
      if (Math.abs(dv.heightPx - dv.sidebarClientHeight) > 1) fail.push(`the divider is ${dv.heightPx}px tall against a rail padding box of ${dv.sidebarClientHeight}px (derived inset ${dv.derivedInsetPx}) — it stops short of, or overshoots, the bottom the formula describes`);
      if (Number.isFinite(m.clientHeight) && Number.isFinite(dv.pseudoBottomPx) && Math.abs(dv.pseudoBottomPx - m.clientHeight) > 1) fail.push(`the divider ends at ${dv.pseudoBottomPx} while the layout bottom is ${m.clientHeight} — the short stop is the exact defect v3.1 removed`);
    } else {
      inconclusive.push(`the divider's computed height did not resolve to px (height ${JSON.stringify(dv.height)}) — its length could not be judged`);
    }
    if (!readable) {
      inconclusive.push(`no #sidebar::after rule text could be read (${dv.unreadableSheets || 0} stylesheet(s) unreadable) — the box exists, but the size formula it was authored with is unproven`);
    } else if (!adopted.length) {
      inconclusive.push(`every #sidebar::after rule sits in a @media that does not match at ${m.clientWidth}px, yet the box is generated — the generating rule was not identified, so the formula was not judged`);
    } else if (!insetRules.length) {
      fail.push(`no matching #sidebar::after rule declares the safe-area height formula (heights seen: ${adopted.map((r) => JSON.stringify(r.height)).join(", ")}) — a plain 100% stops short of the physical bottom the moment env(safe-area-inset-bottom) is not 0`);
    } else {
      note.push(`divider adopted by the engine: content ${JSON.stringify(dv.content)} ${dv.position} top ${dv.top} width ${dv.width} height ${dv.height} (= rail clientHeight ${dv.sidebarClientHeight} + derived inset ${dv.derivedInsetPx}) pointer-events ${dv.pointerEvents}; winning declaration height ${JSON.stringify(insetRules[insetRules.length - 1].height)}${adopted[0].mediaText ? ` under @media ${JSON.stringify(adopted[0].mediaText)} (matches)` : ""}`);
      note.push("headless Chrome reports env(safe-area-inset-bottom) as 0 (derivedInsetPx " + dv.derivedInsetPx + "): this certifies that the browser adopted the pseudo-element and the size formula, NOT the iPad safe-area behaviour");
    }
  }
  // G. THE WHEEL DOOR (issue #116 p7): one real, trusted mouseWheel, observed by a
  //    bubble listener installed AFTER the shipped lock. The probe never prevents, so
  //    a prevented reading can only be the shell's doing — and the listener counts
  //    either side of the install are what make "after" an observation, not a hope.
  const wh = m.wheel;
  if (!wh || wh.instrumentRan !== true) {
    inconclusive.push("the wheel instrument never ran — the trackpad/wheel door was never tested");
  } else if (!wh.dispatched) {
    inconclusive.push(`no wheel input was dispatched: ${wh.reason || wh.dispatchError || "unknown"} — the wheel door is unmeasured, which is not the same as passed`);
  } else {
    const evs = Array.isArray(wh.events) ? wh.events : [];
    if (!evs.length) {
      fail.push(`a real mouseWheel (deltaY ${wh.deltaY}px) at (${wh.point ? `${wh.point.x},${wh.point.y}` : "?"}) produced NO wheel event at the document bubble listener — the input never arrived, so no prevention could be observed`);
    } else {
      const mine = evs.filter((e) => Number(e.deltaY) === Number(wh.deltaY));
      const judged = mine.length ? mine : evs;
      const prevented = judged.every((e) => !!e.defaultPrevented);
      const trusted = judged.every((e) => !!e.isTrusted);
      if (!trusted) fail.push(`the wheel event observed was NOT trusted (isTrusted false) — CDP input did not reach the browser's input pipeline, so nothing here was a real wheel`);
      if (!prevented) fail.push(`the wheel that landed on ${wh.hit || "the terminal"} was not preventDefault-ed at the document bubble listener — the root's default pan is still open (this instrument's own walk-up says native scroller at the point: ${wh.instrumentWalkSaysNativeScroller}; shell wheel listeners on document before the probe: ${wh.listenersBefore && wh.listenersBefore.available ? wh.listenersBefore.count : "?"})`);
      if (evs.length !== 1) note.push(`${evs.length} wheel events reached the document listener for one dispatch — the reading is the ${judged.length} carrying deltaY ${wh.deltaY}`);
      if (Number.isFinite(wh.scrollYAfter) && wh.scrollYAfter !== 0) fail.push(`the wheel moved the ROOT: scrollY ${wh.scrollYBefore} -> ${wh.scrollYAfter} — clip holds the range but not this door`);
      if (Number.isFinite(wh.scrollTopAfter) && wh.scrollTopAfter !== 0) fail.push(`documentElement.scrollTop reads ${wh.scrollTopAfter} after the wheel (was ${wh.scrollTopBefore}) — the root moved through the second door`);
      // The three counts, in the order they were taken: before the install (the
      // shell's own listeners), between install and input (one more = the probe sits
      // after them), after cleanup (back to the first count = nothing was left).
      const before = wh.listenersBefore, installed = wh.listenersInstalled, cleaned = wh.listenersAfterCleanup;
      if (before && before.available && prevented && before.count === 0) {
        inconclusive.push("the wheel was prevented, but NO document wheel listener existed before this probe was installed — the prevention cannot be credited to initShellScrollLock");
      }
      if (installed && installed.available && before && before.available) {
        if (installed.count !== before.count + 1) {
          note.push(`document wheel listeners: ${before.count} before the install, ${installed.count} with the probe installed — expected exactly one more; something else registered or unregistered in the window, so the ORDER evidence is soft and the reading rests on the probe never calling preventDefault`);
        }
      } else {
        note.push(`document wheel listeners could not be enumerated (${(installed && installed.reason) || (before && before.reason) || "no data"}) — "installed after the existing lock" rests on the probe never calling preventDefault alone`);
      }
      if (cleaned && cleaned.available && before && before.available) {
        if (cleaned.count > before.count) fail.push(`wheel listeners were left on the document: ${cleaned.count} after cleanup against ${before.count} before the probe — the instrument permanently changed the page it was measuring`);
      } else if (!cleaned || !cleaned.available) {
        note.push(`post-cleanup listener enumeration unavailable (${(cleaned && cleaned.reason) || "no data"}) — DOM-level cleanup is unproven, only the probe's own handle is confirmed gone`);
      }
      if (wh.termFoundBefore && wh.termFoundAfter && Number.isFinite(wh.termViewportYBefore) && Number.isFinite(wh.termViewportYAfter)) {
        note.push(`auxiliary xterm buffer viewportY ${wh.termViewportYBefore} -> ${wh.termViewportYAfter} (buffer length ${wh.termBufferLengthBefore ?? "?"}; a wheel DOWN at the bottom of the buffer cannot move it — never a verdict, and this instrument never called scrollLines)`);
      } else {
        note.push("no active xterm buffer was readable in this state — the auxiliary scrollback reading is simply absent, never a failure");
      }
    }
    if (wh.listenerDismantled !== true) fail.push(`the probe's wheel listener handle was still live after cleanup (listenerDismantled ${JSON.stringify(wh.listenerDismantled)}) — the instrument left a handler behind on the page`);
    if (wh.globalsRemoved !== true) fail.push(`the probe left globals behind (globalsRemoved ${JSON.stringify(wh.globalsRemoved)}) — the page was not restored`);
  }
  return { fail, note, inconclusive };
}

function selfTest() {
  const good = {
    htmlOverflow: "clip", bodyOverflow: "clip", htmlPosition: "static", bodyPosition: "static",
    appPosition: "fixed", appTop: "0px", appLeft: "0px",
    rootScrollHeight: 702, clientHeight: 702, vvHeight: 702, clientWidth: 1194,
    scrollBefore: 0, scrollAfterScrollTo: 0, scrollTopAfterWrite: 0,
    inner: { candidate: "#wt-list", before: 0, scrolled: 40, rootScrollYDuringInner: 0 },
    appRect: { top: 0, bottom: 702 }, layoutClientHeight: 702, appInlineHeight: "",
    host: {
      containerFound: true, containerBottom: 682,
      inlineBottoms: ["0px", "0px"], rectBottoms: [682, 682],
      probe: { insetBottom: 682, writtenBottom: "428px", liftedBottom: 254, movedBy: 428, computedBottom: "428px" },
    },
    // E 的"通过形状"：两个盒子停在各自的原创值上（控制列 20px / 状态栏 0），
    // 临时列写 448px 后抬升 428px、top 不动、面板跟着缩短 428px。
    overlay: {
      workspaceFound: true,
      controls: { id: "terminal-controls", computedPosition: "absolute", computedTop: "20px", computedBottom: "20px", inlineBottom: "20px", display: "flex", rect: { top: 60, bottom: 662, height: 602 } },
      // v3.1: `fixed` — anchored to the VIEWPORT, so its rest rect bottoms out at
      // clientHeight (702), NOT at #workspace's safe-area-inset bottom (682).
      statusBar: { id: "status-bar", computedPosition: "fixed", computedTop: "685px", computedBottom: "0px", inlineBottom: "0px", display: "flex", rect: { top: 685, bottom: 702, height: 17 } },
      quickKeys: { id: "quick-keys-panel", computedPosition: "absolute", computedTop: "0px", computedBottom: "auto", inlineBottom: "", display: "none", rect: { top: 0, bottom: 0, height: 0 } },
      probe: {
        writtenBottom: "448px", restBottom: 662, restHeight: 642,
        liftedBottom: 234, liftedHeight: 214, topStaysAt: 20, restTop: 20,
        backBottom: 662, restoredHeight: 642,
        // LIFTED-state samples — the judgement reads these
        computedBottom: "448px", computedTop: "20px", panelMaxHeight: "214px",
        inlineBottomWhileLifted: "448px", panelScrollable: true,
        // RESTORED-state samples — cleanup evidence, never the judgement
        restoredComputedBottom: "20px", restoredComputedTop: "20px",
        restoredPanelMaxHeight: "100%", inlineBottomWhileRestored: "20px",
        panelRestHeight: 642, panelLiftedHeight: 214, panelBackHeight: 642,
      },
    },
    // F 的"通过形状"：真实 Chrome 在 1194x702 的 p8 壳上报回来的那组计算值
    // （headless safe-area=0，所以 height 恰好等于 rail 的 clientHeight）。
    divider: {
      sidebarFound: true, appCollapsed: false,
      sidebarRect: { top: 0, bottom: 702, left: 0, right: 260, width: 260, height: 702 },
      sidebarClientHeight: 702, sidebarClientWidth: 259,
      sidebarPosition: "relative", sidebarBorderRightWidth: "1px",
      content: "\"\"", generated: true,
      position: "absolute", top: "0px", left: "259px", width: "1px", height: "702px",
      pointerEvents: "none", visibility: "visible",
      background: "rgb(51, 65, 85) none repeat scroll 0% 0% / auto padding-box border-box",
      backgroundColor: "rgb(51, 65, 85)", backgroundImage: "none",
      heightPx: 702, widthPx: 1, leftPx: 259, derivedInsetPx: 0,
      pseudoTopPx: 0, pseudoBottomPx: 702, pseudoRightEdgePx: 260,
      authoredRules: [{
        selectorText: "#sidebar::after",
        mediaText: "not all and (max-width: 768px)", mediaMatches: true,
        content: "\"\"", position: "absolute", top: "0px", width: "1px",
        height: "calc(100% + env(safe-area-inset-bottom, 0px))", pointerEvents: "none",
      }],
      unreadableSheets: 0,
    },
    // G 的"通过形状"：一次真实 mouseWheel 落在 .xterm-screen 上，后装的冒泡监听器
    // 读到 defaultPrevented=true，根一动没动，监听器与全局都拆干净了。
    wheel: {
      instrumentRan: true, ready: true, reason: null, dispatched: true, dispatchError: null,
      deltaY: 80, wheelWaitMs: 100, boxKind: "pty-host",
      point: { x: 727, y: 351 }, hit: "div.xterm-screen",
      boxRect: { top: 0, bottom: 702, left: 260, right: 1194, width: 934, height: 702 },
      centerPoint: { x: 727, y: 351 }, centerHit: "div.xterm-screen", centerHitInsideBox: true,
      relocated: false, candidatesTried: 1, instrumentWalkSaysNativeScroller: false,
      scrollYBefore: 0, scrollTopBefore: 0, installed: true,
      events: [{
        type: "wheel", eventPhase: 3, atDocument: true, target: "div.xterm-screen",
        clientX: 727, clientY: 351, deltaX: 0, deltaY: 80, deltaMode: 0,
        ctrlKey: false, isTrusted: true, defaultPrevented: true, scrollYAtEvent: 0,
      }],
      eventCount: 1, scrollYAfter: 0, scrollTopAfter: 0,
      termFoundBefore: true, termFoundAfter: true,
      termViewportYBefore: 20, termViewportYAfter: 20,
      termBufferLengthBefore: 44, termBufferLengthAfter: 44,
      listenersBefore: { available: true, count: 1, bubbleNonPassive: 1, listeners: [{ useCapture: false, passive: false, scriptId: "5", line: 1183, column: 21 }] },
      listenersInstalled: { available: true, count: 2, bubbleNonPassive: 2, listeners: [{ useCapture: false, passive: false, scriptId: "5", line: 1183, column: 21 }, { useCapture: false, passive: false, scriptId: "8", line: 131, column: 12 }] },
      listenersAfterCleanup: { available: true, count: 1, bubbleNonPassive: 1, listeners: [{ useCapture: false, passive: false, scriptId: "5", line: 1183, column: 21 }] },
      removedViaHandle: true, listenerDismantled: true, globalsRemoved: true,
    },
  };
  // want: true = PASS, false = FAIL, "INCONCLUSIVE" = the state cannot be judged
  const cases = [
    ["the passing shape: full-height shell, hosts flush, bottom longhand wins", good, true],
    ["scrollTo still works (overflow hidden, not clip)", { ...good, htmlOverflow: "hidden", bodyOverflow: "hidden" }, false],
    ["scrollTo moved the page", { ...good, scrollAfterScrollTo: 100 }, false],
    ["scrollTop write stuck (no clip)", { ...good, scrollTopAfterWrite: 100 }, false],
    ["root carries overflow content (a scroll range exists)", { ...good, rootScrollHeight: 900 }, false],
    ["body pinned (the reverted 2ac6c83 shape)", { ...good, bodyPosition: "fixed" }, false],
    ["shell back in normal flow", { ...good, appPosition: "relative" }, false],
    ["inner scroller frozen (locked page, not fixed shell)", { ...good, inner: { ...good.inner, scrolled: 0 } }, false],
    ["no inner scroller present at all", { ...good, inner: { candidate: null } }, true],
    // THE RETIRED JUDGEMENT: a shell shorter than the layout height is the v2.2
    // pin returning, and that — not "it crossed vv.height" — is what fails now.
    ["shell resized shorter than the layout height (retired pin)", { ...good, appRect: { top: 0, bottom: 633.5 } }, false],
    ["#app carries an inline height again", { ...good, appInlineHeight: "633.5px" }, false],
    ["a host kept a stale bottom while the rest moved (residue)", { ...good, host: { ...good.host, inlineBottoms: ["0px", "428px"] } }, false],
    ["hosts agree on a non-zero bottom with nothing covered", { ...good, host: { ...good.host, inlineBottoms: ["428px", "428px"] } }, false],
    ["host bottom longhand LOSES to the inset shorthand (avoidance does nothing)", { ...good, host: { ...good.host, probe: { ...good.host.probe, liftedBottom: 682, movedBy: 0, computedBottom: "0px" } } }, false],
    ["host bottom lands 1px off the written value (sub-pixel, tolerated)", { ...good, host: { ...good.host, probe: { ...good.host.probe, movedBy: 427.6 } } }, true],
    ["no #terminal-container (web kind) — INCONCLUSIVE, not a fabricated PASS", { ...good, host: { containerFound: false, containerBottom: null, inlineBottoms: [], rectBottoms: [], probe: null } }, "INCONCLUSIVE"],
    ["live hosts empty (no session) — PASS with the cascade probe still run", { ...good, host: { ...good.host, inlineBottoms: [], rectBottoms: [] } }, true],
    ["login gate (no #app at all) — INCONCLUSIVE, not a fabricated PASS", { ...good, appPosition: null, appTop: null, appLeft: null, appRect: undefined }, "INCONCLUSIVE"],
    // E：控制列 / 状态栏的引擎级判据。
    ["the column got the HOST target (428px, no clearance) — the probe's computed bottom says so", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, computedBottom: "428px", liftedBottom: 254, liftedHeight: 234, panelLiftedHeight: 234 } } }, false],
    ["lifting the column moved its top (the quick-keys entry jumped)", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, topStaysAt: 448, restTop: 20 } } }, false],
    ["the max-height:100% panel did NOT follow the shortened column", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, panelLiftedHeight: 642 } } }, false],
    ["the column's authored 20px clearance was edited away in CSS", { ...good, overlay: { ...good.overlay, controls: { ...good.overlay.controls, computedBottom: "0px" } } }, false],
    ["the strip was given the column's clearance (bottom 20px)", { ...good, overlay: { ...good.overlay, statusBar: { ...good.overlay.statusBar, computedBottom: "20px" } } }, false],
    ["the lift was stranded (restore did not come back)", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, backBottom: 234 } } }, false],
    // THE READ-ORDER BUG (the one that made the authenticated-shell run report its
    // single FAIL): the instrument restored bottom to 20px BEFORE sampling the
    // computed styles, so the LIFTED field carried the RESTING number while every
    // rect in the same record said the lift was real (682 -> 254). The judgement
    // must read the lifted fields, and these fixtures are what pin that: the rects
    // stay lifted in all three, so only the read order can flip the verdict.
    ["READ-ORDER BUG: computed styles sampled AFTER restoring 20px (rects still lifted)", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, computedBottom: "20px" } } }, false],
    ["READ-ORDER BUG: one state sampled twice (lifted == restored, both resting 20px)", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, computedBottom: "20px", restoredComputedBottom: "20px" } } }, false],
    ["the lift was stranded in the cascade (restored computed bottom still 448px)", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, restoredComputedBottom: "448px" } } }, false],
    ["the probe's own inline bottom was not 448px when the lifted pair was sampled", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, computedBottom: "20px", inlineBottomWhileLifted: "20px" } } }, false],
    ["the resting top was edited (restored computed top not 20px)", { ...good, overlay: { ...good.overlay, probe: { ...good.overlay.probe, restoredComputedTop: "0px" } } }, false],
    ["older instrument without restored-state fields — PASS with the cleanup left unproven (note)", { ...good, overlay: { ...good.overlay, probe: (() => { const q = { ...good.overlay.probe }; delete q.restoredComputedBottom; delete q.restoredComputedTop; delete q.restoredPanelMaxHeight; delete q.inlineBottomWhileRestored; return q; })() } }, true],
    ["no #workspace — the overlay half is INCONCLUSIVE, not a fabricated PASS", { ...good, overlay: { workspaceFound: false, controls: null, statusBar: null, quickKeys: null, probe: null } }, "INCONCLUSIVE"],
    ["no overlay probe (older instrument) — INCONCLUSIVE", { ...good, overlay: { ...good.overlay, probe: null } }, "INCONCLUSIVE"],
    ["the web-kind hidden column: skipped, its 20px clearance still authored", { ...good, overlay: { ...good.overlay, controls: { ...good.overlay.controls, display: "none" } } }, true],
    // F：分隔线 hairline。每一例都是真实引擎会返回的形状（负例来自 /tmp 的三个反例
    // fixture 在真 Chrome 里实测到的读值），不是凭空拼的数字。
    ["no #sidebar at all — the divider was never observed, INCONCLUSIVE not a pass", { ...good, divider: { sidebarFound: false, authoredRules: [] } }, "INCONCLUSIVE"],
    ["older instrument without divider fields — INCONCLUSIVE", (() => { const q = { ...good }; delete q.divider; return q; })(), "INCONCLUSIVE"],
    ["THE DIVIDER IS GONE: content:none — and its geometry must NOT be what fails", { ...good, divider: { ...good.divider, content: "none", generated: false, position: "absolute", top: "0px", left: "100%", width: "1px", height: "100%", heightPx: null, widthPx: null, leftPx: null, pseudoBottomPx: null, pseudoRightEdgePx: null, derivedInsetPx: null } }, false, /not generated/],
    ["the hairline is 2px wide (a visible double line, not a hairline)", { ...good, divider: { ...good.divider, width: "2px", widthPx: 2, pseudoRightEdgePx: 261 } }, false],
    ["the hairline eats pointer events (a tap on the rail stops working)", { ...good, divider: { ...good.divider, pointerEvents: "auto" } }, false],
    ["the hairline stopped short at the rail's own bottom (682 of 702)", { ...good, divider: { ...good.divider, height: "682px", heightPx: 682, pseudoBottomPx: 682, derivedInsetPx: -20 } }, false],
    ["the formula lost its inset term (plain 100% — correct at inset 0, wrong on iPad)", { ...good, divider: { ...good.divider, authoredRules: [{ ...good.divider.authoredRules[0], height: "100%" } ] } }, false, /safe-area height formula/],
    ["the hairline has no paint (an invisible divider)", { ...good, divider: { ...good.divider, backgroundColor: "transparent", background: "transparent none repeat scroll 0% 0% / auto padding-box border-box" } }, false],
    ["the hairline is off the border column (left 0 → it paints inside the rail)", { ...good, divider: { ...good.divider, left: "0px", leftPx: 0, pseudoRightEdgePx: 1 } }, false],
    ["#sidebar is not a containing block (static) — the hairline anchors elsewhere", { ...good, divider: { ...good.divider, sidebarPosition: "static" } }, false],
    ["the pseudo is in flow (position:static) — it would widen the rail", { ...good, divider: { ...good.divider, position: "static" } }, false],
    ["collapsed rail, orphan hairline still visible over the freed space", { ...good, divider: { ...good.divider, appCollapsed: true, visibility: "visible" } }, false],
    ["collapsed rail, hairline hidden by inheritance — PASS with a note", { ...good, divider: { ...good.divider, appCollapsed: true, visibility: "hidden" } }, true],
    ["narrow tier (drawer form): the wide-tier hairline is scoped off by design — PASS with a note", { ...good, clientWidth: 600, divider: { ...good.divider, content: "none", generated: false } }, true],
    ["every authored rule sits in a @media that does not match — formula unjudged, INCONCLUSIVE", { ...good, divider: { ...good.divider, authoredRules: [{ ...good.divider.authoredRules[0], mediaMatches: false }] } }, "INCONCLUSIVE"],
    ["stylesheets unreadable: the box exists but the formula is unproven — INCONCLUSIVE", { ...good, divider: { ...good.divider, authoredRules: [], unreadableSheets: 3 } }, "INCONCLUSIVE"],
    ["the rail has no measurable box (clientWidth 0 without .collapsed) — INCONCLUSIVE", { ...good, divider: { ...good.divider, sidebarClientWidth: 0 } }, "INCONCLUSIVE"],
    // G：滚轮那扇门。负例同样来自反例 fixture 的实测（去掉锁之后 defaultPrevented
    // 真的是 false、根真的滚到 80），所以这些 FAIL 是仪器抓得住的，不是摆设。
    ["no wheel event reached the document for one real dispatch — FAIL", { ...good, wheel: { ...good.wheel, events: [], eventCount: 0 } }, false],
    ["THE LOCK IS NOT THERE: wheel over the terminal was not prevented", { ...good, wheel: { ...good.wheel, events: [{ ...good.wheel.events[0], defaultPrevented: false }] } }, false],
    ["the wheel moved the root: scrollY 0 -> 80 (clip holds the range, not this door)", { ...good, wheel: { ...good.wheel, events: [{ ...good.wheel.events[0], scrollYAtEvent: 80 }], scrollYAfter: 80 } }, false],
    ["the wheel moved documentElement.scrollTop (the second door opened)", { ...good, wheel: { ...good.wheel, scrollTopAfter: 80 } }, false],
    ["the observed wheel was untrusted (CDP input never reached the pipeline)", { ...good, wheel: { ...good.wheel, events: [{ ...good.wheel.events[0], isTrusted: false }] } }, false],
    ["the probe's listener handle is still live after cleanup — the instrument changed the product", { ...good, wheel: { ...good.wheel, listenerDismantled: false } }, false],
    ["a wheel listener was still on the document after cleanup (2 against 1 before)", { ...good, wheel: { ...good.wheel, listenersAfterCleanup: { ...good.wheel.listenersAfterCleanup, count: 2 } } }, false],
    ["cleanup enumeration unavailable — PASS, but DOM-level cleanup is only noted as unproven", { ...good, wheel: { ...good.wheel, listenersAfterCleanup: { available: false, reason: "target detached" } } }, true],
    ["prevented, but NO shell wheel listener existed before the probe — INCONCLUSIVE", { ...good, wheel: { ...good.wheel, listenersBefore: { ...good.wheel.listenersBefore, count: 0, bubbleNonPassive: 0, listeners: [] }, listenersInstalled: { ...good.wheel.listenersInstalled, count: 1, bubbleNonPassive: 1, listeners: [good.wheel.listenersInstalled.listeners[1]] }, listenersAfterCleanup: { ...good.wheel.listenersAfterCleanup, count: 0, bubbleNonPassive: 0, listeners: [] } } }, "INCONCLUSIVE"],
    ["the probe left globals behind", { ...good, wheel: { ...good.wheel, globalsRemoved: false } }, false],
    ["no visible terminal: nothing was dispatched — INCONCLUSIVE, never a fabricated PASS", { ...good, wheel: { ...good.wheel, ready: false, reason: "no visible PTY host and no visible #terminal-container in this DOM state", dispatched: false, deltaY: null, events: [], eventCount: 0, point: null, hit: null } }, "INCONCLUSIVE"],
    ["the wheel instrument never ran at all — INCONCLUSIVE", (() => { const q = { ...good }; delete q.wheel; return q; })(), "INCONCLUSIVE"],
    ["listener enumeration unavailable — PASS rests on the probe never preventing, noted", { ...good, wheel: { ...good.wheel, listenersBefore: { available: false, reason: "DOMDebugger.getEventListeners not supported" }, listenersInstalled: { available: false, reason: "DOMDebugger.getEventListeners not supported" }, listenersAfterCleanup: { available: false, reason: "DOMDebugger.getEventListeners not supported" } } }, true],
    ["no active xterm (web kind / empty buffer) — PASS, the auxiliary reading is just absent", { ...good, wheel: { ...good.wheel, termFoundBefore: false, termFoundAfter: false, termViewportYBefore: null, termViewportYAfter: null, termBufferLengthBefore: null, termBufferLengthAfter: null } }, true],
    ["the centre was covered by the debug panel and the probe relocated — PASS", { ...good, wheel: { ...good.wheel, relocated: true, centerHit: "#viewport-debug-panel", centerHitInsideBox: false, candidatesTried: 2, point: { x: 571, y: 351 } } }, true],
    ["every in-terminal point is a native scroller — INCONCLUSIVE, not a FAIL against the shell", { ...good, wheel: { ...good.wheel, ready: false, dispatched: false, reason: "every point that resolves inside the terminal box is a native scroller — the shell is entitled to hand this wheel to it, so the root door cannot be judged here", events: [], deltaY: null } }, "INCONCLUSIVE"],
  ];
  let bad = 0;
  // A 4th entry pins WHICH failure fired, not just that one did: "the divider is
  // 100px tall" and "the divider was never generated" are different claims about
  // different products, and a verdict alone cannot tell them apart.
  for (const [name, m, want, wantFailMatches] of cases) {
    const { fail, inconclusive } = check(m);
    const got = fail.length ? false : (inconclusive.length ? "INCONCLUSIVE" : true);
    if (got !== want) { bad++; console.log(`FAIL  want=${String(want)} got=${String(got)}  ${name}`); continue; }
    if (wantFailMatches && !fail.some((f) => wantFailMatches.test(f))) {
      bad++;
      console.log(`FAIL  want=FAIL matching ${wantFailMatches}  got other failures: ${fail.join(" | ")}  ${name}`);
      continue;
    }
    console.log(`ok    ${got === "INCONCLUSIVE" ? "(inconclusive) " : (got ? "PASS          " : "FAIL          ")}${name}`);
  }
  // The fixture table above proves the JUDGEMENT reads lifted fields; this proves
  // the INSTRUMENT FILLS them from the lifted state. MEASURE is a page-side string,
  // so the only thing to assert is the statement order inside it: the sampled
  // computed numbers must be created AFTER writing bottom:448px and BEFORE writing
  // bottom:20px. One swapped pair here is exactly the bug that turned the honest
  // rect numbers 682 -> 254 into a fabricated "computed bottom is 20px" FAIL.
  const order = [
    ["lifted rect", MEASURE.indexOf("const liftedRect = column.getBoundingClientRect()")],
    ["lifted computed sample", MEASURE.indexOf("const liftedComputed = {")],
    ["restore to 20px", MEASURE.indexOf('column.style.bottom = "20px"')],
    ["restored computed sample", MEASURE.indexOf("const restoredComputed = {")],
    ["assembly of out.overlay.probe", MEASURE.indexOf("out.overlay.probe = {")],
  ];
  const ascending = order.every(([, at], i) => at >= 0 && (i === 0 || at > order[i - 1][1]));
  if (!ascending) {
    bad++;
    console.log("FAIL  MEASURE samples the computed styles outside the lifted window: " + order.map(([n, a]) => `${n}@${a}`).join(" < "));
  } else {
    console.log("ok    PASS          MEASURE samples the computed styles INSIDE the lifted window (448px write -> sample -> 20px restore)");
  }
  // The reference-app contrast, recorded as a NOTE so nobody re-tightens it: the
  // shell reaching below the visual viewport is v3.0 working, and this script must
  // never turn that into a FAIL.
  const under = { ...good, vvHeight: 274 };
  const { fail: underFail, note: underNote } = check(under);
  const underOk = underFail.length === 0 && underNote.some((n) => /EXPECTED/.test(n));
  if (!underOk) { bad++; console.log("FAIL  the shell extending under the keyboard must be a NOTE, never a FAIL"); }
  else console.log("ok    PASS          the shell under the keyboard (vv 274 < app 702) is a note, not a failure");
  // G's instrument order, asserted on the wiring itself. Swap two of these and the
  // wheel "evidence" turns into a story: dispatching before the install observes
  // nothing, reading before the dispatch observes nothing, and dismantling before the
  // second count leaves the page changed while the record still claims it is clean.
  let guardCount = 2;
  const src = main.toString();
  const wheelOrder = [
    ["count wheel listeners BEFORE the install", src.indexOf("const listenersBefore = await documentWheelListeners")],
    ["install the probe (WHEEL_PREP)", src.indexOf("await pageEval(WHEEL_PREP)")],
    ["count wheel listeners WITH the probe installed", src.indexOf("const listenersInstalled = await documentWheelListeners")],
    ["dispatch ONE real mouseWheel", src.indexOf('type: "mouseWheel"')],
    ["read and dismantle (WHEEL_READ)", src.indexOf("await pageEval(WHEEL_READ)")],
    ["count wheel listeners AFTER cleanup", src.indexOf("const listenersAfterCleanup = await documentWheelListeners")],
    ["hand the record to check()", src.indexOf("const { fail, note, inconclusive } = check(m);")],
  ];
  guardCount++;
  const wheelOrderOk = wheelOrder.every(([, at], i) => at >= 0 && (i === 0 || at > wheelOrder[i - 1][1]));
  if (!wheelOrderOk) { bad++; console.log("FAIL  the wheel phase is wired out of order: " + wheelOrder.map(([n, a]) => `${n}@${a}`).join(" < ")); }
  else console.log("ok    PASS          the wheel phase is wired count -> install -> dispatch -> read -> count, so 'after the existing lock' is observed and not assumed");
  // The probe has to STAY an observer. The moment it calls preventDefault itself the
  // prevented reading is self-inflicted; the moment it calls scrollLines the terminal
  // scroll it reports is its own doing. Pinned on the source of both page-side
  // programs, because a comment claiming restraint proves nothing.
  guardCount++;
  if (/\.(preventDefault|scrollLines|stopPropagation)\(/.test(WHEEL_PREP + WHEEL_READ)) {
    bad++; console.log("FAIL  the wheel probe acts instead of observing (preventDefault / scrollLines / stopPropagation appears in WHEEL_PREP or WHEEL_READ)");
  } else {
    console.log("ok    PASS          the wheel probe only reads: no preventDefault, no scrollLines, no stopPropagation in either page-side program");
  }
  // Install happens inside PREP before it reports ready; removal happens inside READ
  // before it reports the cleanup flag. Both are statement-order claims, so both are
  // checked as statement order rather than trusted from a comment.
  guardCount++;
  const prepOk = WHEEL_PREP.indexOf("addEventListener('wheel'") >= 0 && WHEEL_PREP.indexOf("addEventListener('wheel'") < WHEEL_PREP.indexOf("out.ready = true");
  const readOk = WHEEL_READ.indexOf("removeEventListener('wheel'") >= 0 && WHEEL_READ.indexOf("removeEventListener('wheel'") < WHEEL_READ.indexOf("out.listenerDismantled");
  if (!prepOk || !readOk) {
    bad++; console.log(`FAIL  the wheel probe's bookkeeping is out of order (install-before-ready ${prepOk}, remove-before-cleanup-flag ${readOk})`);
  } else {
    console.log("ok    PASS          WHEEL_PREP installs the listener before reporting ready; WHEEL_READ removes it before reporting cleanup");
  }
  console.log(bad ? `\n${bad} fixture(s)/guard(s) misjudged` : `\nall ${cases.length + guardCount} fixtures/judgements behaved as specified (read-order + wheel-order + observer-restraint guards included)`);
  process.exit(bad ? 1 : 0);
}

async function main() {
  const URL = arg("url", process.env.MW_TEST_URL || "");
  if (!URL) { console.error("need --url (a served UI, token included if the server requires one)"); process.exit(2); }
  const CHROME = arg("chrome", process.env.CHROME_BIN || "/usr/bin/google-chrome");
  const WIDTH = Number(arg("width", "1194"));
  const HEIGHT = Number(arg("height", "702"));
  // Port 0 would be nicer, but the DevTools endpoint has to be discoverable: an
  // explicit port is requested rather than parsed out of Chrome's log file.
  const DEBUG_PORT = Number(arg("port", "0")) || 9337;
  // One wheel notch, and how long to let it settle before the reading. A notch is
  // ~100px on a trackpad; 80 keeps the input small enough that a shell which DOES
  // honour the lock has nothing to recover from, while still being the gesture the
  // p7 log recorded.
  const WHEEL_DELTA = Number(arg("wheel-delta", "80")) || 80;
  const WHEEL_WAIT_MS = Number(arg("wheel-wait-ms", "100")) || 100;

  const chrome = spawn(CHROME, [
    "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
    "--remote-debugging-port=" + DEBUG_PORT, "--window-size=" + WIDTH + "," + HEIGHT, "about:blank",
  ], { stdio: "ignore" });

  let ws = null;
  let idSeq = 0;
  const pending = new Map();
  const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++idSeq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });

  try {
    let info = null;
    for (let i = 0; i < 60 && !info; i++) {
      try {
        const res = await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/version`);
        info = await res.json();
      } catch (_) { await sleep(500); }
    }
    if (!info) throw new Error("chrome devtools endpoint not reachable on port " + DEBUG_PORT);
    ws = new WebSocket(info.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      ws.addEventListener("open", resolve, { once: true });
      ws.addEventListener("error", reject, { once: true });
    });
    ws.addEventListener("message", (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && pending.has(msg.id)) {
        const p = pending.get(msg.id);
        pending.delete(msg.id);
        if (msg.error) p.reject(new Error(msg.error.message));
        else p.resolve(msg.result);
      }
    });

    const { targetId } = await send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
    await send("Page.enable", {}, sessionId);
    await send("Emulation.setDeviceMetricsOverride", { width: WIDTH, height: HEIGHT, deviceScaleFactor: 1, mobile: false }, sessionId);
    await send("Page.navigate", { url: URL }, sessionId);
    await sleep(2500);

    const evaluated = await send("Runtime.evaluate", { expression: MEASURE, returnByValue: true }, sessionId);
    if (evaluated.exceptionDetails) throw new Error("page-side measurement threw: " + JSON.stringify(evaluated.exceptionDetails.text || evaluated.exceptionDetails));
    const m = evaluated.result.value;

    // --- G. the wheel phase: enumerate → install → ONE real input → read → verify.
    // The order IS the instrument: the probe listener has to be on the page before
    // the dispatch (or there is nothing to observe) and off it again after the read
    // (or the check leaves a change behind on the user's page). The listener never
    // prevents and never scrolls, so anything reported as prevented was prevented by
    // code that was already there. Nothing in this phase scrolls anything by hand —
    // a script that scrolls the page to see whether it comes back is the shape this
    // file exists to replace.
    const pageEval = async (expression) => {
      const r = await send("Runtime.evaluate", { expression, returnByValue: true }, sessionId);
      if (r.exceptionDetails) throw new Error("page-side probe threw: " + JSON.stringify(r.exceptionDetails.text || r.exceptionDetails));
      return r.result.value;
    };
    const listenersBefore = await documentWheelListeners(send, sessionId);
    const prep = await pageEval(WHEEL_PREP);
    // Counted AGAIN between the install and the input: this is the number that says
    // the probe sits AFTER the shell's own listener (exactly one more than before),
    // and it must be taken before the dispatch, because WHEEL_READ takes it back off.
    const listenersInstalled = await documentWheelListeners(send, sessionId);
    let dispatched = false;
    let dispatchError = null;
    if (prep && prep.ready) {
      await send("Input.dispatchMouseEvent", {
        type: "mouseWheel", x: prep.point.x, y: prep.point.y,
        deltaX: 0, deltaY: WHEEL_DELTA, modifiers: 0, pointerType: "mouse",
      }, sessionId).then(() => { dispatched = true; }, (e) => { dispatchError = String((e && e.message) || e); });
      await sleep(WHEEL_WAIT_MS);
    }
    const read = await pageEval(WHEEL_READ);
    const listenersAfterCleanup = await documentWheelListeners(send, sessionId);
    m.wheel = {
      ...(prep || {}),
      ...(read || {}),
      instrumentRan: true,
      dispatched,
      dispatchError,
      deltaY: dispatched ? WHEEL_DELTA : null,
      wheelWaitMs: WHEEL_WAIT_MS,
      termFoundBefore: !!(prep && prep.termFound),
      termViewportYBefore: prep ? prep.termViewportYBefore : null,
      termBufferLengthBefore: prep ? prep.termBufferLength : null,
      listenersBefore,
      listenersInstalled,
      listenersAfterCleanup,
    };

    const { fail, note, inconclusive } = check(m);

    console.log(`url: ${redact(URL)}   viewport: ${WIDTH}x${HEIGHT}`);
    console.log(`root overflow html/body: ${m.htmlOverflow}/${m.bodyOverflow}   positions html/body/#app: ${m.htmlPosition}/${m.bodyPosition}/${m.appPosition} (${m.appTop}/${m.appLeft})`);
    console.log(`root scroll range: scrollHeight ${m.rootScrollHeight} vs clientHeight ${m.clientHeight}   scrollY before/after scrollTo(0,100): ${m.scrollBefore}/${m.scrollAfterScrollTo}   scrollTop after direct write: ${m.scrollTopAfterWrite}`);
    console.log(`inner scroller: ${m.inner && m.inner.candidate ? `${m.inner.candidate} ${m.inner.before} -> ${m.inner.scrolled} (root scrollY during: ${m.inner.rootScrollYDuringInner})` : "(none in this state)"}`);
    console.log(`shell full height: app bottom ${m.appRect ? m.appRect.bottom : "-"} vs clientHeight ${m.layoutClientHeight} (inline height ${JSON.stringify(m.appInlineHeight || "")}, vv.height ${m.vvHeight} for context only)`);
    console.log(`PTY hosts: ${m.host && m.host.containerFound ? `${m.host.inlineBottoms.length} live host(s), inline bottoms [${m.host.inlineBottoms.join(", ")}], container bottom ${m.host.containerBottom}` : "(no #terminal-container)"}`);
    console.log(`host bottom cascade probe: inset ${m.host && m.host.probe ? m.host.probe.insetBottom : "-"} -> written 428px -> ${m.host && m.host.probe ? m.host.probe.liftedBottom : "-"} (moved ${m.host && m.host.probe ? m.host.probe.movedBy : "-"}px, computed ${m.host && m.host.probe ? JSON.stringify(m.host.probe.computedBottom) : "-"})`);
    const ov = m.overlay || {};
    const ovp = ov.probe || {};
    console.log(`overlay authored: #terminal-controls ${ov.controls ? `${ov.controls.computedPosition} top=${ov.controls.computedTop} bottom=${ov.controls.computedBottom} display=${ov.controls.display}` : "(absent)"}   #status-bar ${ov.statusBar ? `${ov.statusBar.computedPosition} bottom=${ov.statusBar.computedBottom}` : "(absent)"}`);
    console.log(`overlay lift probe (temporary box, the live page is never written): bottom 20px -> 448px  bottom ${ovp.restBottom ?? "-"} -> ${ovp.liftedBottom ?? "-"} (lifted ${(ovp.restBottom ?? 0) - (ovp.liftedBottom ?? 0)}px), top ${ovp.restTop ?? "-"} -> ${ovp.topStaysAt ?? "-"} (must not move), column height ${ovp.restHeight ?? "-"} -> ${ovp.liftedHeight ?? "-"}, max-height:100% panel ${ovp.panelRestHeight ?? "-"} -> ${ovp.panelLiftedHeight ?? "-"} (scrollable ${ovp.panelScrollable ?? "-"})`);
    // Sampled inside vs. outside the lifted window — the two columns must disagree,
    // and the judgement only ever reads the LIFTED column (v3.0 read-order bug).
    console.log(`overlay computed styles: LIFTED (inline bottom ${JSON.stringify(ovp.inlineBottomWhileLifted ?? "-")}) bottom=${JSON.stringify(ovp.computedBottom ?? "-")} top=${JSON.stringify(ovp.computedTop ?? "-")} panel max-height=${JSON.stringify(ovp.panelMaxHeight ?? "-")}   RESTORED (inline bottom ${JSON.stringify(ovp.inlineBottomWhileRestored ?? "-")}) bottom=${JSON.stringify(ovp.restoredComputedBottom ?? "-")} top=${JSON.stringify(ovp.restoredComputedTop ?? "-")} panel max-height=${JSON.stringify(ovp.restoredPanelMaxHeight ?? "-")}  height ${ovp.restHeight ?? "-"} -> ${ovp.liftedHeight ?? "-"} -> ${ovp.restoredHeight ?? "-"}`);
    // F/G: the divider hairline and the wheel door, both as the engine reported them.
    const dv = m.divider || {};
    const dvRule = ((dv.authoredRules || []).filter((r) => r.mediaMatches !== false).slice(-1)[0]) || {};
    console.log(`divider #sidebar::after: rail rect ${dv.sidebarRect ? `${dv.sidebarRect.top}..${dv.sidebarRect.bottom} (clientHeight ${dv.sidebarClientHeight}, clientWidth ${dv.sidebarClientWidth})` : "(no #sidebar)"}   content ${JSON.stringify(dv.content ?? "-")} ${dv.position ?? "-"} top ${JSON.stringify(dv.top ?? "-")} left ${JSON.stringify(dv.left ?? "-")} (=${dv.leftPx ?? "-"}px) width ${JSON.stringify(dv.width ?? "-")} height ${JSON.stringify(dv.height ?? "-")} (=${dv.heightPx ?? "-"}px, derived inset ${dv.derivedInsetPx ?? "-"}) background ${JSON.stringify(dv.backgroundColor ?? "-")} pointer-events ${JSON.stringify(dv.pointerEvents ?? "-")} visibility ${JSON.stringify(dv.visibility ?? "-")}`);
    console.log(`divider winning declaration: ${dvRule.height ? `height ${JSON.stringify(dvRule.height)}${dvRule.mediaText ? ` under @media ${JSON.stringify(dvRule.mediaText)}` : ""}` : "(no matching #sidebar::after rule text was readable)"}   — headless safe-area is 0, so this certifies the browser adopted the pseudo and the formula, not the iPad`);
    const wh = m.wheel || {};
    const evs = Array.isArray(wh.events) ? wh.events : [];
    console.log(`wheel input: ${wh.dispatched ? `dispatched deltaY ${wh.deltaY}px at (${wh.point.x},${wh.point.y}) on ${wh.hit} inside ${wh.boxKind}${wh.relocated ? ` (relocated from the covered centre ${wh.centerPoint.x},${wh.centerPoint.y} which hit ${wh.centerHit})` : " (centre was hit-testable)"} — ${evs.length} event(s) seen` : `NOT dispatched: ${wh.reason || wh.dispatchError || "unknown"}`}`);
    console.log(`wheel reading: defaultPrevented ${evs.length ? evs.map((e) => String(e.defaultPrevented)).join("/") : "-"} (isTrusted ${evs.length ? evs.map((e) => String(e.isTrusted)).join("/") : "-"}), eventPhase ${evs.length ? evs.map((e) => String(e.eventPhase)).join("/") : "-"}, target ${evs.length ? evs.map((e) => e.target).join("/") : "-"}   root scrollY ${wh.scrollYBefore ?? "-"} -> ${wh.scrollYAfter ?? "-"}, scrollTop ${wh.scrollTopBefore ?? "-"} -> ${wh.scrollTopAfter ?? "-"}`);
    console.log(`wheel listeners on document: ${wh.listenersBefore && wh.listenersBefore.available ? `${wh.listenersBefore.count} before the install (bubble non-passive ${wh.listenersBefore.bubbleNonPassive})` : `before: unavailable (${wh.listenersBefore && wh.listenersBefore.reason})`}   ${wh.listenersInstalled && wh.listenersInstalled.available ? `${wh.listenersInstalled.count} installed (probe included)` : "installed: unavailable"}   ${wh.listenersAfterCleanup && wh.listenersAfterCleanup.available ? `${wh.listenersAfterCleanup.count} after cleanup` : "after cleanup: unavailable"}   handle dismantled: ${JSON.stringify(wh.listenerDismantled ?? "-")}, globals removed: ${JSON.stringify(wh.globalsRemoved ?? "-")}   xterm viewportY ${wh.termViewportYBefore ?? "-"} -> ${wh.termViewportYAfter ?? "-"} (auxiliary)`);
    for (const n of note) console.log(`note: ${n}`);
    for (const i of inconclusive) console.log(`INCONCLUSIVE: ${i}`);
    for (const f of fail) console.log(`FAIL: ${f}`);

    const here = dirname(fileURLToPath(import.meta.url));
    const outDir = join(here, "structural-check");
    mkdirSync(outDir, { recursive: true });
    const verdict = fail.length ? "FAIL" : (inconclusive.length ? "INCONCLUSIVE" : "PASS");
    // One file per target: pointing the same script at the app shell, at a
    // hand-built fixture and at the LOGIN page are three different claims, and
    // a single RESULTS.json would let the newest run silently erase the other
    // two. The label travels with the file; the login page is EXPECTED to
    // fail here (it has no #app at all), and that run is only worth keeping
    // next to the passing ones as a warning about what a naked `PASS` means.
    const LABEL = (arg("label", "unlabelled") || "unlabelled").replace(/[^A-Za-z0-9._-]/g, "-");
    writeFileSync(join(outDir, `RESULTS-${LABEL}.json`), JSON.stringify({ url: redact(URL), label: LABEL, viewport: { WIDTH, HEIGHT }, measured: m, fail, note, inconclusive, verdict }, null, 2));
    console.log(`\nVERDICT: ${verdict}   (headless Chrome is NOT iOS Safari: this certifies structure, not the device)`);
    process.exit(fail.length ? 1 : (inconclusive.length ? 2 : 0));
  } finally {
    try { if (ws) ws.close(); } catch (_) {}
    chrome.kill();
  }
}

if (process.argv.includes("--self-test")) selfTest();
else main().catch((e) => { console.error("structural check failed to run: " + e.message); process.exit(2); });
