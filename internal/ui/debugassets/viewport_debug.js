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

    function createPanel(doc, sessionId) {
        const panel = doc.createElement("section");
        panel.id = "viewport-debug-panel";
        panel.setAttribute("role", "region");
        panel.setAttribute("aria-label", "Viewport diagnostics");
        panel.style.cssText = "position:fixed;top:calc(env(safe-area-inset-top,0px) + 8px);right:calc(env(safe-area-inset-right,0px) + 8px);z-index:100000;width:min(360px,calc(100vw - 16px));max-height:72vh;overflow:auto;padding:10px;border:1px solid #4b5563;border-radius:8px;background:rgba(13,17,23,0.96);color:#e6edf3;font:12px/1.4 -apple-system,BlinkMacSystemFont,sans-serif;box-shadow:0 4px 24px rgba(0,0,0,0.35);touch-action:manipulation;";

        const title = doc.createElement("div");
        title.textContent = "Viewport diagnostics";
        title.style.cssText = "font-weight:700;margin-bottom:4px;";
        panel.appendChild(title);
        const session = doc.createElement("div");
        session.textContent = "Session: " + sessionId;
        session.style.cssText = "font:10px/1.4 ui-monospace,monospace;color:#9aa7b4;overflow-wrap:anywhere;margin-bottom:8px;";
        panel.appendChild(session);
        const status = doc.createElement("div");
        status.textContent = "Starting…";
        status.style.cssText = "margin-bottom:8px;color:#9aa7b4;";
        panel.appendChild(status);
        const pre = doc.createElement("pre");
        pre.style.cssText = "max-height:220px;overflow:auto;padding:8px;background:#0b1020;border-radius:5px;white-space:pre-wrap;overflow-wrap:anywhere;font:10px/1.35 ui-monospace,monospace;";
        panel.appendChild(pre);

        const row = doc.createElement("div");
        row.style.cssText = "display:flex;flex-wrap:wrap;gap:6px;margin-top:8px;";
        panel.appendChild(row);
        const copyArea = doc.createElement("textarea");
        copyArea.readOnly = true;
        copyArea.setAttribute("aria-label", "Latest diagnostic JSON");
        copyArea.style.cssText = "display:none;width:100%;height:160px;margin-top:8px;background:#0b1020;color:#e6edf3;border:1px solid #4b5563;border-radius:5px;font:10px/1.35 ui-monospace,monospace;";
        panel.appendChild(copyArea);
        doc.body.appendChild(panel);
        return { panel, status, pre, row, copyArea };
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
        const ui = createPanel(win.document, sessionId);
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

        function add(label, title, handler) {
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
            ui.row.appendChild(button);
            return button;
        }
        add(client.getStatus().continuous ? "Stop continuous" : "Start continuous", "Toggle one-second continuous capture", (event) => {
            if (client.getStatus().continuous) {
                client.stopContinuous();
                event.currentTarget.textContent = "Start continuous";
            } else {
                client.startContinuous(1000);
                event.currentTarget.textContent = "Stop continuous";
            }
        });
        add("Capture", "Capture viewport now", () => client.capture("manual"));
        add("Magic bar", "Mark the Magic Keyboard floating bar as visible", () => client.capture("magic-hint"));
        add("Soft keyboard", "Mark the software keyboard as visible", () => client.capture("keyboard-hint"));
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
        });
        const pause = add("Pause", "Pause or resume diagnostic captures", (event) => {
            const next = !client.getStatus().paused;
            client.setPaused(next);
            event.currentTarget.textContent = next ? "Resume" : "Pause";
        });
        void pause;
        add("Hide", "Hide or show diagnostic details", (event) => {
            const hidden = ui.pre.style.display === "none";
            ui.pre.style.display = hidden ? "block" : "none";
            ui.copyArea.style.display = "none";
            event.currentTarget.textContent = hidden ? "Hide" : "Show";
        });
        add("Stop", "Stop diagnostics and remove the panel", () => {
            client.stop();
            ui.panel.remove();
            if (win.__mwViewportDebug === client) delete win.__mwViewportDebug;
        });
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

    return { snapshotFromPage, createClient, createBrowserClient, installBrowser };
});
