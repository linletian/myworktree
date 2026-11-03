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

    // Stamped into EVERY snapshot. Two rounds of on-device verification were
    // spent on reports that turned out to describe a page still running the
    // previous build (the document carried no cache directive, so a plain reload
    // could reuse it). A capture now states which instrument produced it, so a
    // report can never be ambiguous again — bump this whenever the probe or the
    // shell behaviour it measures changes.
    //
    // p9-full-shell-session-avoidance-v1 measures the issue #116 contract v3.2:
    // "out-of-flow FULL-HEIGHT fixed shell + non-scrollable root + PTY-local
    // avoidance scoped to soft-keyboard sessions + a wheel door that blocks the root".
    // Same schema as p7/p8 (no new fields); what changed is the arithmetic one verdict
    // checks against and one gesture surface the probe's parent now hardens:
    //
    //   * THE SHELL NEVER MOVES. appComputedPosition reads "fixed",
    //     htmlOverflow / bodyOverflow read "clip", rootScrollHeight does not
    //     exceed clientHeight — the document has no scroll mechanism and no
    //     scroll range — and the shell's own box is FULL HEIGHT in every
    //     keyboard state: app.bottom answers to documentElement.clientHeight
    //     (layoutGap = clientHeight − app.bottom), NOT to vv.height. v2.2
    //     pinned #app to vv.height and that pairing is retired. The real-Gmail
    //     control the user ran on the same iPad is what settled it — with the
    //     keyboard up, what shows through the keyboard is the same unchanged
    //     full-height layout.
    //   * THE TERMINAL AVOIDS LOCALLY, INSIDE A KEYBOARD SESSION. The boxes that
    //     move are the TERMINAL'S OWN: every PTY host inside #terminal-container,
    //     the overlay #terminal-controls column, and #status-bar — which v3.1 made
    //     `position:fixed` so the Transport sits at the PHYSICAL viewport bottom
    //     with no keyboard, not the safe-area-inset bottom #workspace would give
    //     it. Occlusion is v3.2 keyboard-session state read straight from the
    //     applier (window.__mwTerminalOcclusion, written every pass): the session
    //     enters at raw >= 50% of the layout height with editable focus, holds at
    //     raw while that focus is kept and raw > 1, and leaves at raw <= 1, zoom
    //     or focus loss — so a shallow 147 / 206.1 / 68.5 strip AFTER a closed
    //     session is owed NOTHING (host bottom 0, controls 20, status 0, terminal
    //     full height, strip simply overlaps it) while the SAME 147 mid-session
    //     belongs to one live keyboard and moves exactly what the keyboard moved.
    //     The probe reports the FLOORED-history-free EFFECTIVE number the applier
    //     actually owes, not a raw reconstruction; the retired 0.25 small-overlay
    //     floor that used to be re-derived here is gone, not renamed. `ptyHosts`
    //     reports EVERY host (hidden and inactive included) with its rect bottom,
    //     its own inline `bottom` and visibility; `controls` / `statusBar` report
    //     the same three numbers for the two overlays at the offsets they are
    //     AUTHORED at plus the effective occlusion; `quickKeys` reports the panel's
    //     computed max-height against its clientHeight / scrollHeight. Anything that
    //     disagrees with its authored offset is the miss. A shell whose layoutGap
    //     is not ~0 is a shell that was still resized.
    //   * THE WHEEL DOOR IS BLOCKED, NOT DOUBLED. A trackpad two-finger scroll
    //     emits `wheel`; the p7 capture showed document.scrollTop walking non-zero
    //     under overflow:clip, so the root clip alone does not hold it. The shell
    //     lock now preventDefaults a wheel even over the terminal — xterm's own
    //     wheel handler is closer to the target and has already scrolled the
    //     buffer, so the lock never calls scrollLines itself; it owes the page a
    //     preventDefault only. This is not directly a captured number; it is why
    //     scrollTop stays 0 in a p8 capture and why the p7 trackpad frames that
    //     showed non-zero scrollTop must not recur.
    //
    // scrollTop / scrollingElementScrollTop stay EVIDENCE and never an input: a
    // non-zero scroll is a product failure that no code here may correct, and no
    // hidden scroll area under the keyboard is allowed (the reference app grows
    // one; this contract forbids it). vv.offsetTop and pageTop stay
    // informational — they describe iOS' own panning and must never be added back
    // onto a rect. (p7-full-shell-local-avoidance-v1 measured the no-floor build,
    // p6-structural-no-scroll-v1 the pinned-shell build and p5-root-origin-v1 the
    // build that PARKED the root at 0; none of those stamps can certify this one,
    // which is exactly what a stamp is for.)
    const PROBE_VERSION = "p9-full-shell-session-avoidance-v1";

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

    // issue #116 contract v2.2: `overflow` as a FACT captured from the live
    // engine, not as a claim in a stylesheet. An engine that never learned
    // `clip` answers "hidden" here, and that answer is the honest signal that
    // this browser is running the fallback floor — which is exactly why the
    // value is reported instead of being assumed from the CSS text.
    function overflowOf(win, element) {
        if (!element || typeof win.getComputedStyle !== "function") return "";
        return win.getComputedStyle(element).overflow || "";
    }

    function styledRect(win, element) {
        const value = rect(element);
        if (!value) return null;
        value.background = backgroundOf(win, element);
        return value;
    }

    // issue #116: "what is painted at the bottom edge?" as data rather than
    // inference. Reports the hit-tested element's identity and its own computed
    // background at two y coordinates: the last visible pixel of the VISUAL
    // viewport, and the last pixel of the LAYOUT viewport. They differ precisely
    // when the visual viewport is panned, which is the state the Magic Keyboard
    // bar produces. A BODY/HTML hit means the root canvas is exposed (a real
    // band); a hit on the terminal host or #workspace means the pixels belong to
    // the app's own ink.
    function probeBottomEdge(doc, vv, win) {
        if (!doc || typeof doc.elementFromPoint !== "function") return null;
        const describe = (y) => {
            let el = null;
            try {
                el = doc.elementFromPoint(finite(win.innerWidth) / 2, y);
            } catch (err) {
                return null;
            }
            if (!el) return null;
            return {
                y: finite(y),
                tag: el.tagName || "",
                id: el.id || "",
                className: typeof el.className === "string" ? el.className : "",
                background: backgroundOf(win, el),
            };
        };
        // COORDINATE SYSTEMS: elementFromPoint takes CLIENT coordinates (relative
        // to the visual viewport), so the last visible pixel is always at
        // vv.height - 2 — NOT at vv.offsetTop + vv.height - 2, which is a LAYOUT
        // coordinate and produced phantom "the bottom is BODY" hits whenever the
        // page was panned (50 of 120 frames in one capture). The layout-bottom
        // probe keeps client coordinates too, which is what a client-space API
        // expects; it simply lands outside the panned viewport and is clamped.
        const visual = vv ? finite(vv.height) - 2 : finite(win.innerHeight) - 2;
        const layout = finite(doc.documentElement ? doc.documentElement.clientHeight : win.innerHeight) - 2;
        return {
            visualBottom: describe(visual),
            layoutBottom: describe(layout),
            // Recorded so a reader can convert the app rect (client space) into
            // layout space without guessing which system a number is in.
            pageTop: vv ? finite(vv.pageTop) : 0,
        };
    }

    // issue #116 contract v3.0: the PTY hosts are the ONLY boxes the shell
    // writes, so they are reported individually — every host inside
    // #terminal-container, hidden and inactive included. `bottom` is the rect in
    // CLIENT space (the same space vv.height lives in), `inlineBottom` is the
    // host's OWN written value, and `visible` says whether the host is the one
    // on screen right now. A hidden host with a stale inline bottom is the band
    // the user meets on the NEXT switch, so it has to be in the capture even
    // though nothing is visibly wrong at this instant.
    function ptyHostsOf(win, doc) {
        const container = doc && typeof doc.getElementById === "function"
            ? doc.getElementById("terminal-container")
            : null;
        if (!container || typeof container.querySelectorAll !== "function") return [];
        const hosts = container.querySelectorAll(":scope > [data-instance-id]");
        const out = [];
        for (let i = 0; i < hosts.length; i++) {
            const host = hosts[i];
            const value = rect(host);
            if (!value) continue;
            const style = host.style || {};
            const computed = typeof win.getComputedStyle === "function"
                ? win.getComputedStyle(host)
                : null;
            out.push({
                index: out.length,
                top: value.top,
                bottom: value.bottom,
                height: value.height,
                inlineBottom: style.bottom || "",
                computedBottom: computed ? (computed.bottom || "") : "",
                visible: style.display !== "none",
            });
        }
        return out;
    }

    // v3.2's occlusion: READ, not re-derived. The applier keeps the keyboard-session
    // state machine (enter at raw >= 50% of the layout height with editable focus,
    // hold at raw inside the session, leave at raw <= 1 / zoom / focus loss) and
    // mirrors the EFFECTIVE number it owed on the last pass to
    // window.__mwTerminalOcclusion (see applyVisualViewportOcclusion in index.html).
    // The probe reports exactly that number, because a probe that re-ran its own
    // stateless rule (the retired 0.25 floor, or the raw shortfall pre-v3.1) would
    // judge this build against arithmetic it does not perform — a mid-session 147px
    // retract frame is a keyboard that still owes its lift, and a post-session 147
    // is a strip that owes nothing, and geometry alone cannot tell them apart.
    // Unset (no pass has run yet) reads 0, exactly as the applier owes nothing.
    function occlusionOf(win) {
        const value = win ? win.__mwTerminalOcclusion : undefined;
        return Number.isFinite(value) ? value : 0;
    }

    // issue #116 contract v3.0, the second half of the LOCAL avoidance: the two
    // absolutely positioned overlay children of #workspace — #terminal-controls
    // (the quick-keys entry / panel column) and #status-bar (the transport strip).
    // #workspace keeps its full height in every keyboard state, so these two are
    // covered by the keyboard unless THEY move, and the whole point of the fix is
    // that the quick-keys entry and the input stay reachable. Each is reported
    // with the same three numbers a host is reported with — the rect it resolved
    // to, the value the applier wrote, and whether it is on screen — so a verdict
    // can check the authored offset plus the occlusion instead of guessing:
    //     #terminal-controls bottom = 20 + occlusion   (authored bottom 20px)
    //     #status-bar          bottom = occlusion       (authored bottom 0)
    // The COLUMN's height is the number that matters twice over: it is what
    // shortens as the bottom rises, and it is the definite containing block the
    // panel's `max-height: 100%` resolves against, so a column that did NOT
    // shorten means a panel that still runs under the keyboard.
    // Element missing => null, and a null is answered as "not observed" by the
    // verifier, never as a pass.
    function overlayOf(win, doc, id) {
        const el = doc && typeof doc.getElementById === "function"
            ? doc.getElementById(id)
            : null;
        if (!el) return null;
        const value = rect(el);
        if (!value) return null;
        const style = el.style || {};
        const computed = typeof win.getComputedStyle === "function"
            ? win.getComputedStyle(el)
            : null;
        return {
            top: value.top,
            bottom: value.bottom,
            width: value.width,
            height: value.height,
            inlineBottom: style.bottom || "",
            computedBottom: computed ? (computed.bottom || "") : "",
            // COMPUTED display, not the inline one: #terminal-controls is hidden
            // by a CLASS (.oc-hidden on web kinds), so an inline-only read would
            // call a hidden column visible and the verifier would judge geometry a
            // zeroed rect never had.
            visible: computed ? computed.display !== "none" : style.display !== "none",
        };
    }

    // The quick-keys panel itself. `maxHeight` is the COMPUTED value, so the
    // capture says what the percentage resolved to rather than what the stylesheet
    // wished; scrollHeight against clientHeight is the internal-scroll claim
    // (content taller than the box, box still reachable). Only reported when the
    // panel exists, and it stays absent (`null`) when it does not.
    function quickKeysOf(win, doc) {
        const el = doc && typeof doc.getElementById === "function"
            ? doc.getElementById("quick-keys-panel")
            : null;
        if (!el) return null;
        const value = rect(el);
        if (!value) return null;
        const computed = typeof win.getComputedStyle === "function"
            ? win.getComputedStyle(el)
            : null;
        return {
            top: value.top,
            bottom: value.bottom,
            width: value.width,
            height: value.height,
            maxHeight: computed ? (computed.maxHeight || "") : "",
            clientHeight: finite(el.clientHeight),
            scrollHeight: finite(el.scrollHeight),
            open: !!(el.classList && el.classList.contains("open")),
        };
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
            probeVersion: PROBE_VERSION,
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
                // issue #116 contract v3.0: the STRUCTURE is the claim, so the
                // structure is reported. htmlOverflow / bodyOverflow are the
                // computed `overflow` of the two root boxes — under v3.0 both read
                // "clip" (a clipped box has no scroll mechanism), and the pair
                // arriving together with appComputedPosition = "fixed" and a
                // rootScrollHeight that never exceeds clientHeight is what makes a
                // capture say "this document cannot scroll" instead of "this
                // capture happens to show scroll 0 right now". The v3.0 addition is
                // that this stays true WITH the keyboard up: the reference app grows
                // a hidden, user-scrollable strip below the shell when the keyboard
                // arrives, and this contract forbids copying that — a capture that
                // shows a root scroll range appearing under the keyboard is a
                // regression, not a feature.
                // scrollTop / scrollingElementScrollTop therefore changed meaning:
                // they are EVIDENCE, never an input. There is no code in this build
                // that writes them. A non-zero value here is the product failure the
                // SOP is looking for, and it must stay in the capture exactly as
                // found — v2.1 parked them at 0, and a probe that only ever reads
                // back 0 cannot tell a fixed structure from a symptom that is being
                // repeatedly repaired.
                // pageTop and vv.offsetTop stay INFORMATIONAL context (they say how
                // far iOS panned, which is how a pan is told apart from a scroll);
                // they are never added onto an app rect by the verifier. The two
                // backgrounds say whose ink a band is painted in.
                htmlOverflow: overflowOf(win, doc.documentElement),
                bodyOverflow: overflowOf(win, doc.body),
                rootScrollHeight: doc.documentElement ? finite(doc.documentElement.scrollHeight) : null,
                pageTop: vv ? finite(vv.pageTop) : null,
                scrollTop: finite(win.scrollY || win.pageYOffset || 0),
                scrollingElementScrollTop: doc.scrollingElement ? finite(doc.scrollingElement.scrollTop) : null,
                scrollingElementScrollHeight: doc.scrollingElement ? finite(doc.scrollingElement.scrollHeight) : null,
                bodyScrollTop: doc.body ? finite(doc.body.scrollTop) : null,
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
                // issue #116 contract v3.0: the shell's computed position is part
                // of the claim — "fixed" here plus the two clipped root boxes is
                // the pair that says the document has neither scroll content nor
                // scroll mechanism. Read it together with document.htmlOverflow.
                computedPosition: appStyle.position || "",
                // v3.0: `inlineHeight` is the NEGATIVE proof — it must stay "" in
                // every state, because v2.2's full-page pin to vv.height is gone
                // and the shell is sized by CSS alone (100vh → 100dvh).
                // `layoutGap` is the POSITIVE proof: clientHeight − app.bottom,
                // ~0 in every state including keyboard-up. The retired v2.2 number
                // was vv.height − app.bottom, whose negative branch ("crossing")
                // is exactly what v3.0 WANTS — the shell deliberately extends under
                // the keyboard — so it is not computed here any more.
                inlineHeight: app && app.style ? (app.style.height || "") : "",
                layoutGap: finite(doc.documentElement ? doc.documentElement.clientHeight : win.innerHeight) - finite(appRect.bottom),
                paddingTop: appStyle.paddingTop || "0px",
                paddingRight: appStyle.paddingRight || "0px",
                paddingBottom: appStyle.paddingBottom || "0px",
                paddingLeft: appStyle.paddingLeft || "0px",
                background: appStyle.backgroundColor || "",
            }),
            main: rect(main) || { top: 0, bottom: 0, width: 0, height: 0 },
            workspace: styledRect(win, workspace),
            terminal: styledRect(win, host),
            // v3.0: occlusion + every PTY host, so a verdict can check the LOCAL
            // avoidance instead of a resized shell. v3.2: occlusion is the applier's
            // own session value read off the window, never re-derived here.
            // See ptyHostsOf / occlusionOf.
            occlusion: occlusionOf(win),
            ptyHosts: ptyHostsOf(win, doc),
            // v3.0, same pass, same occlusion: the two overlay children of
            // #workspace the applier now lifts as well — the quick-keys control
            // column and the transport status strip — plus the panel whose
            // max-height resolves against that column. See overlayOf/quickKeysOf.
            controls: overlayOf(win, doc, "terminal-controls"),
            statusBar: overlayOf(win, doc, "status-bar"),
            quickKeys: quickKeysOf(win, doc),
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
            // issue #116: THE decisive field for the Magic Keyboard white band.
            // Grab what is actually painted at the last visible pixel (bottom
            // edge of the visual viewport, horizontally centred) rather than
            // inferring it from backgrounds. If it reports BODY/HTML the UA or
            // root canvas is exposed — a band exists; if it reports the terminal
            // host or #workspace the pixels are the app's own ink and any band
            // is a child element's, which is a completely different fix. Also
            // record the same probe at the layout bottom, because the two
            // disagree exactly when the visual viewport is panned.
            probe: probeBottomEdge(doc, vv, win),
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
        // The probe version rides in the title so a tester can confirm at a glance
        // that the page is running the build they think it is. issue #116 lost two
        // verification rounds to exactly that ambiguity.
        title.textContent = "Viewport diagnostics" + " · " + PROBE_VERSION;
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
