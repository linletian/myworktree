/* Read-only viewport diagnostics for issue #116. No terminal text, tokens,
 * worktree names, paths, or instance IDs are collected or uploaded. */
(function (root, factory) {
    const api = factory();
    if (typeof module === "object" && module.exports) module.exports = api;
    if (root) {
        root.MWViewportDebug = api;
        if (root.document) api.installBrowser(root);
    }
})(typeof window !== "undefined" ? window : null, function () {
    "use strict";

    const SESSION_PATTERN = /^[A-Za-z0-9_-]{16,64}$/;

    function finite(value) {
        return Number.isFinite(value) ? value : 0;
    }

    function rect(element) {
        if (!element || typeof element.getBoundingClientRect !== "function") return null;
        const r = element.getBoundingClientRect();
        return {
            top: finite(r.top),
            bottom: finite(r.bottom),
            width: finite(r.width),
            height: finite(r.height),
        };
    }

    function backgroundOf(win, element) {
        if (!element || typeof win.getComputedStyle !== "function") return "";
        return win.getComputedStyle(element).backgroundColor || "";
    }

    function styledRect(win, element) {
        const value = rect(element);
        if (!value) return null;
        value.background = backgroundOf(win, element);
        return value;
    }

    function snapshotFromPage(page, reason, capturedAt) {
        const win = page.window;
        const doc = page.document;
        const app = doc.getElementById("app");
        const main = doc.getElementById("main");
        const workspace = doc.getElementById("workspace");
        const vv = win.visualViewport || null;
        const appRect = rect(app) || { top: 0, bottom: 0, width: 0, height: 0 };
        const appStyle = app && typeof win.getComputedStyle === "function" ? win.getComputedStyle(app) : {};
        const session = typeof page.getActiveTerminalSession === "function"
            ? page.getActiveTerminalSession()
            : null;
        const host = session && session.container ? session.container : null;
        const webPanel = typeof doc.querySelector === "function"
            ? doc.querySelector("#opencode-panel:not([hidden]), #dsh-panel:not([hidden]), #reasonix-panel:not([hidden])")
            : null;
        const state = page.state || win.state || {};
        const instances = Array.isArray(state.instances) ? state.instances : [];
        const active = state.activeInst
            ? instances.find((item) => item && item.id === state.activeInst)
            : null;
        const navigator = win.navigator || {};
        const screen = win.screen || {};
        const coarse = typeof win.matchMedia === "function" && win.matchMedia("(pointer: coarse)").matches;

        return {
            capturedAt,
            reason,
            innerWidth: finite(win.innerWidth),
            innerHeight: finite(win.innerHeight),
            clientWidth: doc.documentElement ? finite(doc.documentElement.clientWidth) : 0,
            clientHeight: doc.documentElement ? finite(doc.documentElement.clientHeight) : 0,
            dpr: finite(win.devicePixelRatio || 1),
            device: {
                userAgent: navigator.userAgent || "",
                platform: navigator.platform || "",
                touchPoints: finite(navigator.maxTouchPoints),
                orientation: screen.orientation && screen.orientation.type ? screen.orientation.type : "",
                screenWidth: finite(screen.width),
                screenHeight: finite(screen.height),
                coarse: !!coarse,
            },
            document: {
                bodyBackground: backgroundOf(win, doc.body),
                htmlBackground: backgroundOf(win, doc.documentElement),
            },
            vv: vv ? {
                width: finite(vv.width),
                height: finite(vv.height),
                offsetTop: finite(vv.offsetTop),
                offsetLeft: finite(vv.offsetLeft),
                scale: finite(vv.scale || 1),
            } : null,
            app: Object.assign(appRect, {
                computedHeight: appStyle.height || "",
                inlineHeight: app && app.style ? (app.style.height || "") : "",
                paddingTop: appStyle.paddingTop || "0px",
                paddingRight: appStyle.paddingRight || "0px",
                paddingBottom: appStyle.paddingBottom || "0px",
                paddingLeft: appStyle.paddingLeft || "0px",
                background: appStyle.backgroundColor || "",
            }),
            main: rect(main) || { top: 0, bottom: 0, width: 0, height: 0 },
            workspace: styledRect(win, workspace),
            terminal: styledRect(win, host),
            webPanel: styledRect(win, webPanel),
            xtermScreen: host ? rect(host.querySelector(".xterm-screen")) : null,
            xtermRows: host ? rect(host.querySelector(".xterm-rows")) : null,
            ui: {
                collapsed: !!(app && app.classList && app.classList.contains("collapsed")),
                quickKeysOpen: !!(session && session.quickKeysOpen),
                kind: active && active.kind ? active.kind : "pty",
                status: active && active.status ? active.status : "",
                activeElementTag: doc.activeElement && doc.activeElement.tagName ? doc.activeElement.tagName : "",
            },
        };
    }

    function createClient(options) {
        const sessionId = options.sessionId;
        if (!SESSION_PATTERN.test(sessionId)) throw new Error("invalid diagnostic session");
        if (typeof options.upload !== "function") throw new Error("upload is required");
        const page = options.page;
        const now = options.now || (() => new Date());
        const raf = options.requestAnimationFrame || page.window.requestAnimationFrame.bind(page.window);
        const caf = options.cancelAnimationFrame || page.window.cancelAnimationFrame.bind(page.window);
        const setTimer = options.setTimeout || page.window.setTimeout.bind(page.window);
        const clearTimer = options.clearTimeout || page.window.clearTimeout.bind(page.window);
        const upload = options.upload;
        const onUpdate = options.onUpdate || (() => {});
        const onStatus = options.onStatus || (() => {});

        let seq = Number.isInteger(options.initialSeq) && options.initialSeq >= 0 ? options.initialSeq : 0;
        let running = false;
        let paused = false;
        let scheduledFrame = null;
        let settleTimer = null;
        let retryTimer = null;
        let continuousTimer = null;
        let continuousWanted = false;
        let continuousMs = 1000;
        let lastReason = "manual";
        let latestReport = null;
        let queuedReport = null;
        let activeUpload = false;
        let dropped = 0;
        let lastUploadedSeq = 0;
        let lastError = "";
        const listeners = [];

        function status() {
            return {
                running, paused, activeUpload, dropped, lastUploadedSeq, lastError,
                queued: !!queuedReport,
                continuous: continuousWanted,
                continuousMs,
            };
        }

        function emitStatus() {
            onStatus(status());
        }

        function enqueue(report) {
            if (queuedReport) dropped++;
            queuedReport = report;
            emitStatus();
            void drain();
        }

        async function drain() {
            if (!running || activeUpload || !queuedReport || retryTimer !== null) return;
            const report = queuedReport;
            queuedReport = null;
            activeUpload = true;
            emitStatus();
            try {
                await upload(report);
                lastUploadedSeq = Math.max(lastUploadedSeq, report.seq);
                lastError = "";
            } catch (error) {
                lastError = "upload failed; retrying";
                // A newer capture wins over retrying this stale report.
                if (!queuedReport) queuedReport = report;
                if (running) retryTimer = setTimer(() => {
                    retryTimer = null;
                    void drain();
                }, 2000);
            } finally {
                activeUpload = false;
                emitStatus();
                if (running && queuedReport && retryTimer === null) void drain();
            }
        }

        function capture(reason) {
            if (!running || paused) return null;
            const snapshot = snapshotFromPage(page, reason, now().toISOString());
            seq++;
            latestReport = { session: sessionId, seq, snapshot };
            onUpdate(snapshot);
            enqueue(latestReport);
            return latestReport;
        }

        function schedule(reason) {
            if (!running || paused) return;
            lastReason = reason;
            if (scheduledFrame === null) {
                scheduledFrame = raf(() => {
                    scheduledFrame = raf(() => {
                        scheduledFrame = null;
                        capture(lastReason);
                    });
                });
            }
            if (settleTimer !== null) clearTimer(settleTimer);
            settleTimer = setTimer(() => {
                settleTimer = null;
                capture("settle");
            }, 250);
        }

        function armContinuous() {
            if (!continuousWanted || !running || paused || continuousTimer !== null) return;
            continuousTimer = setTimer(() => {
                continuousTimer = null;
                if (continuousWanted && running && !paused) {
                    capture("periodic");
                    armContinuous();
                }
            }, continuousMs);
        }

        function startContinuous(ms = 1000) {
            continuousWanted = true;
            continuousMs = Math.max(250, ms);
            armContinuous();
            emitStatus();
        }

        function stopContinuous() {
            continuousWanted = false;
            if (continuousTimer !== null) {
                clearTimer(continuousTimer);
                continuousTimer = null;
            }
            emitStatus();
        }

        function listen(target, type, reason) {
            if (!target || typeof target.addEventListener !== "function") return;
            const handler = () => schedule(reason);
            target.addEventListener(type, handler);
            listeners.push({ target, type, handler });
        }

        function start() {
            if (running) return;
            running = true;
            const vv = page.window.visualViewport;
            listen(vv, "resize", "resize");
            listen(vv, "scroll", "scroll");
            listen(page.window, "resize", "window-resize");
            listen(page.window, "orientationchange", "orientationchange");
            listen(page.window, "pageshow", "pageshow");
            listen(page.document, "focusin", "focusin");
            listen(page.document, "focusout", "focusout");
            listen(page.document, "visibilitychange", "visibilitychange");
            capture("init");
            emitStatus();
        }

        function stop() {
            if (!running) return;
            running = false;
            paused = false;
            stopContinuous();
            if (scheduledFrame !== null) {
                caf(scheduledFrame);
                scheduledFrame = null;
            }
            if (settleTimer !== null) {
                clearTimer(settleTimer);
                settleTimer = null;
            }
            if (retryTimer !== null) {
                clearTimer(retryTimer);
                retryTimer = null;
            }
            for (const item of listeners) item.target.removeEventListener(item.type, item.handler);
            listeners.length = 0;
            queuedReport = null;
            emitStatus();
        }

        function setPaused(value) {
            if (!running) return;
            paused = !!value;
            if (paused) {
                if (scheduledFrame !== null) {
                    caf(scheduledFrame);
                    scheduledFrame = null;
                }
                if (settleTimer !== null) {
                    clearTimer(settleTimer);
                    settleTimer = null;
                }
                if (continuousTimer !== null) {
                    clearTimer(continuousTimer);
                    continuousTimer = null;
                }
            } else {
                capture("manual");
                armContinuous();
            }
            emitStatus();
        }

        return {
            start, stop, capture, schedule, setPaused, startContinuous, stopContinuous,
            getLatestReport: () => latestReport,
            getStatus: status,
        };
    }

    function randomSessionId(win) {
        if (win.crypto && typeof win.crypto.getRandomValues === "function") {
            const bytes = new Uint8Array(16);
            win.crypto.getRandomValues(bytes);
            return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
        }
        return (Date.now().toString(16) + Math.random().toString(16).slice(2, 28)).slice(0, 32);
    }

    function makeButton(doc, label, title) {
        const button = doc.createElement("button");
        button.type = "button";
        button.textContent = label;
        button.title = title;
        button.setAttribute("aria-label", title);
        button.style.cssText = "min-height:44px;min-width:44px;padding:6px 8px;border:1px solid #555;border-radius:5px;background:#202632;color:#e6edf3;cursor:pointer;";
        return button;
    }

    /* A0 采证发现：浮层原先固定 72vh 且按钮排在明细 JSON 之后，iPad 软键盘
     * 弹出（visualViewport 缩到 274–633px）时按钮整排被键盘埋掉，无法标记
     * keyboard-hint。因此布局改为：主按钮（Magic bar / Soft keyboard / Capture）
     * 永远排在第一排，可视区变矮时自动折叠次要内容，面板高度按可视区夹紧，
     * 并且可拖拽/换角以避开被遮挡的 quick-keys 控件。 */
    const PANEL_MARGIN = 8;
    const PANEL_MAX_WIDTH = 360;
    const PANEL_COLLAPSE_THRESHOLD = 420;

    function clamp(value, min, max) {
        const lo = Number.isFinite(min) ? min : 0;
        const hi = Number.isFinite(max) ? max : lo;
        if (!Number.isFinite(value)) return lo;
        return Math.min(Math.max(value, lo), Math.max(lo, hi));
    }

    // panelLayoutFor 是纯函数：输入视口几何与位置意图，输出面板样式分量。
    // 可被 Node 直接断言，不依赖真实浏览器。
    function panelLayoutFor(input) {
        const src = input || {};
        const innerWidth = finite(src.innerWidth) || 360;
        const innerHeight = finite(src.innerHeight) || 600;
        const vvWidth = finite(src.vvWidth) || innerWidth;
        const vvHeight = finite(src.vvHeight) || innerHeight;
        const visibleWidth = Math.max(120, Math.min(innerWidth, vvWidth) - PANEL_MARGIN * 2);
        const visibleHeight = Math.max(96, Math.min(innerHeight, vvHeight) - PANEL_MARGIN * 2);
        const autoCollapse = visibleHeight < PANEL_COLLAPSE_THRESHOLD;
        const collapsed = src.collapsed === undefined ? autoCollapse : !!src.collapsed;
        const width = Math.max(180, Math.min(PANEL_MAX_WIDTH, visibleWidth));
        const safeTop = Math.max(0, finite(src.safeTop));
        const safeRight = Math.max(0, finite(src.safeRight));
        const safeBottom = Math.max(0, finite(src.safeBottom));

        // 面板可用高度 = 可视高 − 已占用安全区。折叠时只留主按钮一排所需高度。
        const topBudget = Math.max(96, visibleHeight - safeTop);
        const maxHeight = collapsed
            ? clamp(112, 88, topBudget)
            : clamp(Math.min(visibleHeight - safeTop, 520), 96, topBudget);

        const corner = src.corner || "tr";
        // 控制列（#terminal-controls，含 quick-keys 入口）贴在右缘；靠右居位时为它
        // 预留宽度，使诊断浮层在默认右上也不埋掉 quick-keys。
        const reservedRight = Math.max(0, finite(src.reservedRight));
        let box = {};
        if (corner === "free") {
            const x = clamp(finite(src.dragX), 0, Math.max(0, visibleWidth - width));
            const y = clamp(finite(src.dragY), 0, Math.max(0, visibleHeight - Math.min(maxHeight, 120)));
            box = { left: x + PANEL_MARGIN + "px", top: y + PANEL_MARGIN + safeTop + "px", right: "auto", bottom: "auto" };
        } else {
            // 角落命名：第 1 个字符 = 纵向（t/b），第 2 个字符 = 横向（l/r）。
            const vertical = corner.startsWith("b")
                ? { top: "auto", bottom: safeBottom + PANEL_MARGIN + "px" }
                : { top: safeTop + PANEL_MARGIN + "px", bottom: "auto" };
            const horizontal = corner.endsWith("l")
                ? { left: PANEL_MARGIN + "px", right: "auto" }
                : { right: safeRight + PANEL_MARGIN + reservedRight + "px", left: "auto" };
            box = Object.assign({}, vertical, horizontal);
        }
        return {
            corner: corner,
            collapsed: collapsed,
            autoCollapse: autoCollapse,
            width: width + "px",
            maxHeight: maxHeight + "px",
            visibleHeight: visibleHeight,
            safeTop: safeTop,
            safeRight: safeRight,
            safeBottom: safeBottom,
            reservedRight: reservedRight,
            scale: finite(src.scale),
            left: box.left,
            top: box.top,
            right: box.right,
            bottom: box.bottom,
        };
    }

    function applyPanelLayout(win, panel, state) {
        const vv = win.visualViewport || null;
        // 靠右居位时实测控制列宽度并预留；控制列隐藏（无活动实例）则不预留。
        let reservedRight = 0;
        if (!state.corner.endsWith("l") && state.corner !== "free" && win.document && typeof win.document.getElementById === "function") {
            const col = win.document.getElementById("terminal-controls");
            const cr = col && typeof col.getBoundingClientRect === "function" ? col.getBoundingClientRect() : null;
            if (cr && cr.width > 0 && (!col.hidden) && win.getComputedStyle(col).display !== "none") {
                // 预留 = 控制列宽 + 两侧边距，保证浮层右缘落在控制列左缘之外。
                reservedRight = cr.width + PANEL_MARGIN * 2;
            }
        }
        const layout = panelLayoutFor({
            innerWidth: win.innerWidth,
            innerHeight: win.innerHeight,
            vvWidth: vv ? vv.width : win.innerWidth,
            vvHeight: vv ? vv.height : win.innerHeight,
            safeTop: state.safeTop,
            safeRight: state.safeRight,
            safeBottom: state.safeBottom,
            reservedRight,
            corner: state.corner,
            dragX: state.dragX,
            dragY: state.dragY,
            collapsed: state.collapsed,
            scale: vv ? vv.scale : 1,
        });
        panel.style.width = layout.width;
        panel.style.maxHeight = layout.maxHeight;
        panel.style.left = layout.left;
        panel.style.top = layout.top;
        panel.style.right = layout.right;
        panel.style.bottom = layout.bottom;
        return layout;
    }

    function createPanel(doc, sessionId, options) {
        const opts = options || {};
        const env = (name) => {
            try {
                const raw = opts.env && opts.env[name];
                return raw ? parseFloat(raw) : 0;
            } catch (_) {
                return 0;
            }
        };
        const state = {
            corner: opts.corner || "tr",
            collapsed: !!opts.collapsed,
            dragX: 0,
            dragY: 0,
            safeTop: env("safe-area-inset-top"),
            safeRight: env("safe-area-inset-right"),
            safeBottom: env("safe-area-inset-bottom"),
        };
        const panel = doc.createElement("section");
        panel.id = "viewport-debug-panel";
        panel.setAttribute("role", "region");
        panel.setAttribute("aria-label", "Viewport diagnostics");
        panel.style.cssText = "position:fixed;z-index:100000;padding:8px;border:1px solid #4b5563;border-radius:8px;background:rgba(13,17,23,0.96);color:#e6edf3;font:12px/1.4 -apple-system,BlinkMacSystemFont,sans-serif;box-shadow:0 4px 24px rgba(0,0,0,0.35);touch-action:manipulation;overflow:hidden auto;overscroll-behavior:contain;";

        const title = doc.createElement("div");
        title.textContent = "Viewport diagnostics";
        title.style.cssText = "font-weight:700;margin-bottom:4px;cursor:grab;touch-action:none;user-select:none;";
        panel.appendChild(title);
        const session = doc.createElement("div");
        session.textContent = "Session: " + sessionId;
        session.style.cssText = "font:10px/1.4 ui-monospace,monospace;color:#9aa7b4;overflow-wrap:anywhere;margin-bottom:6px;";
        panel.appendChild(session);
        const status = doc.createElement("div");
        status.textContent = "Starting…";
        status.style.cssText = "margin-bottom:6px;color:#9aa7b4;";
        panel.appendChild(status);
        // 主按钮区：紧贴状态行，永远在面板最上方，键盘弹出时仍然可达。
        const rowPrimary = doc.createElement("div");
        rowPrimary.id = "viewport-debug-row-primary";
        rowPrimary.style.cssText = "display:flex;flex-wrap:wrap;gap:6px;";
        panel.appendChild(rowPrimary);
        const rowSecondary = doc.createElement("div");
        rowSecondary.id = "viewport-debug-row-secondary";
        rowSecondary.style.cssText = "display:none;flex-wrap:wrap;gap:6px;margin-top:6px;";
        panel.appendChild(rowSecondary);
        const pre = doc.createElement("pre");
        pre.style.cssText = "display:none;max-height:180px;overflow:auto;padding:8px;margin-top:6px;background:#0b1020;border-radius:5px;white-space:pre-wrap;overflow-wrap:anywhere;font:10px/1.35 ui-monospace,monospace;";
        panel.appendChild(pre);
        const copyArea = doc.createElement("textarea");
        copyArea.readOnly = true;
        copyArea.setAttribute("aria-label", "Latest diagnostic JSON");
        copyArea.style.cssText = "display:none;width:100%;height:140px;margin-top:8px;background:#0b1020;color:#e6edf3;border:1px solid #4b5563;border-radius:5px;font:10px/1.35 ui-monospace,monospace;";
        panel.appendChild(copyArea);
        doc.body.appendChild(panel);

        // 应用与可视区同步的布局；软键盘/地址栏/旋转变化后由调用方重新触发。
        const relayout = () => applyPanelLayout(opts.win, panel, state);
        // 首次必须夹一次，否则 fixed 面板没有 left/top 会落到 (0,0)。
        relayout();

        // 拖拽：标题栏按下后自由拖动，边界由 applyPanelLayout 夹紧；
        // 只读诊断层，不影响终端命中。
        title.addEventListener("pointerdown", (event) => {
            if (event.pointerType === "mouse" && event.button !== 0) return;
            const rectNow = panel.getBoundingClientRect();
            const startX = event.clientX;
            const startY = event.clientY;
            const baseLeft = rectNow.left;
            const baseTop = rectNow.top;
            let moved = false;
            const move = (ev) => {
                moved = true;
                state.corner = "free";
                state.dragX = Math.max(0, baseLeft + (ev.clientX - startX) - PANEL_MARGIN);
                state.dragY = Math.max(0, baseTop + (ev.clientY - startY) - PANEL_MARGIN - state.safeTop);
                relayout();
            };
            const up = () => {
                if (moved) opts.onCornerChange && opts.onCornerChange("free");
                if (typeof title.releasePointerCapture === "function") {
                    try { title.releasePointerCapture(event.pointerId); } catch (_) { /* already released */ }
                }
                title.removeEventListener("pointermove", move);
                title.removeEventListener("pointerup", up);
                title.removeEventListener("pointercancel", up);
            };
            if (typeof title.setPointerCapture === "function") {
                try { title.setPointerCapture(event.pointerId); } catch (_) { /* no capture support */ }
            }
            title.addEventListener("pointermove", move);
            title.addEventListener("pointerup", up);
            title.addEventListener("pointercancel", up);
        });

        return { panel, title, status, pre, rowPrimary, rowSecondary, copyArea, state, relayout };
    }

    async function createBrowserClient(win, params) {
        let sessionId = params.get("debug-session") || "";
        if (!SESSION_PATTERN.test(sessionId)) {
            sessionId = randomSessionId(win);
            params.set("debug-session", sessionId);
            try {
                win.history.replaceState(null, "", win.location.pathname + "?" + params.toString() + win.location.hash);
            } catch (_) { /* URL rewriting is optional; uploads still work. */ }
        }
        let initialSeq = 0;
        try {
            const previous = await win.api("/api/viewport-debug/report?session=" + encodeURIComponent(sessionId) + "&after=0");
            initialSeq = Number.isInteger(previous.latestSeq) && previous.latestSeq > 0 ? previous.latestSeq : 0;
        } catch (_) {
            // A new/expired session starts at zero; auth/network failures surface
            // later through the normal upload retry path.
        }
        const cornerKey = "mw.vvdbg.corner";
        let corner = "tr";
        try {
            corner = (params.get("viewport-debug-pos") || win.sessionStorage.getItem(cornerKey) || "tr");
        } catch (_) { /* storage 不可用时回右上角 */ }
        if (!["tl", "tr", "bl", "br", "free"].includes(corner)) corner = "tr";
        const ui = createPanel(win.document, sessionId, {
            win,
            corner,
            env: (name) => {
                try {
                    const probe = win.document.createElement("div");
                    probe.style.cssText = "position:absolute;visibility:hidden;padding-top:env(" + name + ",0px);";
                    win.document.body.appendChild(probe);
                    const value = win.getComputedStyle(probe).paddingTop;
                    probe.remove();
                    return value;
                } catch (_) {
                    return "0px";
                }
            },
            onCornerChange: (value) => {
                try { win.sessionStorage.setItem(cornerKey, value); } catch (_) { /* ignore */ }
            },
        });
        const showSecondary = (on) => {
            ui.rowSecondary.style.display = on ? "flex" : "none";
            ui.pre.style.display = on && ui.pre.dataset.want === "1" ? "block" : "none";
            ui.relayout();
        };
        // 可视区变化（软键盘、地址栏、旋转、Split View）后重新夹紧面板，
        // 保证主按钮不被键盘埋掉。只读几何，不写业务样式。
        if (win.visualViewport) {
            const relayout = () => ui.relayout();
            win.visualViewport.addEventListener("resize", relayout);
            win.visualViewport.addEventListener("scroll", relayout);
        }
        win.addEventListener("orientationchange", () => setTimeout(() => ui.relayout(), 80));
        const client = createClient({
            sessionId,
            initialSeq,
            page: {
                window: win,
                document: win.document,
                state: win.state,
                getActiveTerminalSession: typeof win.getActiveTerminalSession === "function"
                    ? win.getActiveTerminalSession
                    : () => null,
            },
            upload: (report) => win.api("/api/viewport-debug/report", {
                method: "POST",
                body: JSON.stringify(report),
            }),
            onUpdate: (snapshot) => { ui.pre.textContent = JSON.stringify(snapshot, null, 2); },
            onStatus: (state) => {
                ui.status.textContent = state.lastError
                    ? state.lastError
                    : state.paused
                        ? "Paused"
                        : state.activeUpload
                            ? "Uploading…"
                            : (state.continuous ? "Live 1s · " : "") + "Uploaded seq " + state.lastUploadedSeq + (state.dropped ? " · coalesced " + state.dropped : "");
            },
        });

        function add(label, title, handler, secondary) {
            const button = makeButton(win.document, label, title);
            button.addEventListener("pointerdown", (event) => {
                // Keep terminal focus and the software keyboard / IME floating
                // bar alive while a diagnostic button is used (same reason as
                // quick-keys: cancelling pointerdown suppresses mouse-focus).
                event.preventDefault();
                handler(event);
            });
            // Keyboard-generated clicks (detail=0) still work; real pointer
            // clicks were already handled on pointerdown and must not run twice.
            button.addEventListener("click", (event) => {
                if (event.detail !== 0) return;
                event.preventDefault();
                handler(event);
            });
            (secondary ? ui.rowSecondary : ui.rowPrimary).appendChild(button);
            return button;
        }
        add("Magic bar", "Mark the Magic Keyboard floating bar as visible", () => client.capture("magic-hint"));
        add("Soft keyboard", "Mark the software keyboard as visible", () => client.capture("keyboard-hint"));
        add("Capture", "Capture viewport now", () => client.capture("manual"));
        add("Move", "Move the panel to the next corner (keeps it clear of covered controls)", (event) => {
            const order = ["tr", "tl", "br", "bl"];
            const next = order[(order.indexOf(ui.state.corner) + 1) % order.length] || "tr";
            ui.state.corner = next;
            ui.state.dragX = 0;
            ui.state.dragY = 0;
            ui.relayout();
            event.currentTarget.textContent = "Move ▸ " + next.toUpperCase();
            try { win.sessionStorage.setItem(cornerKey, next); } catch (_) { /* ignore */ }
        });
        add(client.getStatus().continuous ? "Stop continuous" : "Start continuous", "Toggle one-second continuous capture", (event) => {
            if (client.getStatus().continuous) {
                client.stopContinuous();
                event.currentTarget.textContent = "Start continuous";
            } else {
                client.startContinuous(1000);
                event.currentTarget.textContent = "Stop continuous";
            }
        }, true);
        add("JSON", "Show or hide the live geometry JSON", (event) => {
            const want = ui.pre.dataset.want === "1" ? "0" : "1";
            ui.pre.dataset.want = want;
            if (want === "0") ui.copyArea.style.display = "none";
            showSecondary(want === "1" || ui.rowSecondary.style.display === "flex");
            ui.pre.style.display = want === "1" ? "block" : "none";
            event.currentTarget.textContent = want === "1" ? "Hide JSON" : "JSON";
        }, true);
        add("Copy JSON", "Copy the latest diagnostic JSON", async () => {
            const report = client.getLatestReport();
            if (!report) return;
            const text = JSON.stringify(report, null, 2);
            if (win.navigator.clipboard && win.isSecureContext) {
                try {
                    await win.navigator.clipboard.writeText(text);
                    ui.status.textContent = "JSON copied";
                    return;
                } catch (_) { /* fall through to the visible textarea */ }
            }
            ui.copyArea.style.display = "block";
            ui.copyArea.value = text;
            ui.copyArea.focus();
            ui.copyArea.select();
            ui.status.textContent = "JSON selected; use the system Copy action";
        }, true);
        const pause = add("Pause", "Pause or resume diagnostic captures", (event) => {
            const next = !client.getStatus().paused;
            client.setPaused(next);
            event.currentTarget.textContent = next ? "Resume" : "Pause";
        }, true);
        void pause;
        add("More", "Show or hide secondary controls", (event) => {
            const on = ui.rowSecondary.style.display !== "flex";
            showSecondary(on);
            event.currentTarget.textContent = on ? "Less" : "More";
        });
        add("Stop", "Stop diagnostics and remove the panel", () => {
            client.stop();
            ui.panel.remove();
            if (win.__mwViewportDebug === client) delete win.__mwViewportDebug;
        }, true);
        client.start();
        // Auto-start so the iPad user never has to click into the diagnostic
        // panel and risk dismissing the IME floating bar before measurement.
        client.startContinuous(1000);
        return client;
    }

    function installBrowser(win) {
        const boot = async () => {
            const params = new win.URLSearchParams(win.location.search);
            if (params.get("viewport-debug") !== "1" || win.__mwViewportDebug) return;
            win.__mwViewportDebug = await createBrowserClient(win, params);
        };
        if (win.document.readyState === "loading") {
            win.document.addEventListener("DOMContentLoaded", boot, { once: true });
        } else {
            boot();
        }
    }

    return { snapshotFromPage, createClient, createBrowserClient, installBrowser, panelLayoutFor };
});
