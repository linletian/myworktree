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

    /* ── issue #116 Phase 2 task 1: ownership, host identity, session, panel ──
     * PHASE1-CONTRACT "验收前置条件（工具依赖，不得绕过）" plus D1's precise
     * evidence gap demand three things a p9 capture never carried:
     *   ① WHO holds focus and which ownership category (HO-1..HO-4) that is;
     *   ② the focused element's and the terminal host's client rect IN ONE FRAME;
     *   ③ the sub-frame order of events against the geometry change.
     * Everything below is ADDITIVE: no p9 field is renamed, removed or re-meaned,
     * so the frozen verifier keeps reading the same numbers while a new one can
     * finally answer HO-1..HO-4, F18 and the §5.5 unlock question.
     *
     * PRIVACY (PLAN Phase 2 task 1): identity is TAG + BOOLEAN ROLE MARKERS only.
     * Nothing here reads textContent, innerText, value, placeholder, src, href,
     * URL, cookie or an instance identifier — a PTY host is identified by whether
     * it CARRIES the `data-instance-id` attribute and whether that value EQUALS
     * the app's active instance, and both answers are reported as booleans.
     *
     * UNAVAILABLE IS A VALUE (contract 验收前置条件: "缺字段一律 INCONCLUSIVE"):
     * every block carries an explicit unavailableReason string ("" when the block
     * IS available) instead of an absent field, so "could not be read" can never
     * be mistaken for "read, and it is fine".
     */
    const OWNERSHIP = Object.freeze({
        NONE: "HO-1",
        PTY: "HO-2",
        WEB: "HO-3",
        IFRAME: "HO-4",
        UNKNOWN: "unknown",
    });

    // The four session states of the frozen §5 machine (PHASE1-CONTRACT F18:
    // active / recovering / geometry grace) plus "none" and the explicit
    // "unknown". These are the words the DERIVED hint below may answer with —
    // they are NOT a second verdict machine (see keyboardSessionOf): the
    // probe never re-runs §5's entry / re-entry ratios, it reads the applier's
    // own mirrored occlusion, correlates consecutive probe frames, and labels HOW
    // it knows (`evidence`) and what it cannot know (`unavailableReason`).
    const SESSION_STATE = Object.freeze({
        ACTIVE: "active",
        RECOVERING: "recovering",
        GEOMETRY_GRACE: "geometryGrace",
        NONE: "none",
        UNKNOWN: "unknown",
    });

    // Additive schema marker. PROBE_VERSION stays `p9-full-shell-session-avoidance-v1`
    // because the SHELL BEHAVIOUR under measurement is unchanged and the frozen
    // verifier's stamp has to keep matching; this string says which ADDITIVE field
    // set a frame carries, so a capture taken before Phase 2 task 1 is told apart
    // from one after it by a field, not by bumping the frozen stamp.
    const SCHEMA_REV = "p9-additive-ownership-panel-v1";

    const TRACE_MAX_ENTRIES = 16;
    const XTERM_MARKER = "xterm";
    const HELPER_MARKER = "helper";

    function clientBoxOf(element) {
        if (!element || typeof element.getBoundingClientRect !== "function") return null;
        const r = element.getBoundingClientRect();
        if (!r) return null;
        return {
            top: finite(r.top),
            bottom: finite(r.bottom),
            left: finite(r.left),
            right: finite(r.right),
            width: finite(r.width),
            height: finite(r.height),
        };
    }

    // The SAME guard the product's session machine uses (index.html: TEXTAREA /
    // INPUT / isContentEditable). Re-declared here because the probe must classify
    // focus with the product's definition of "editable host" and not with its own;
    // it is a read, and it decides nothing about geometry.
    function isEditableHost(element) {
        if (!element || typeof element !== "object") return false;
        const tag = typeof element.tagName === "string" ? element.tagName.toUpperCase() : "";
        return tag === "TEXTAREA" || tag === "INPUT" || !!element.isContentEditable;
    }

    // The applier's mirror, kept TRISTATE: `null` means the applier never published
    // (no pass ran, or this build has no applier) and is not the same claim as a
    // published 0. `occlusionOf` above keeps its frozen "unset reads 0" meaning.
    function mirroredOcclusionOf(win) {
        const value = win ? win.__mwTerminalOcclusion : undefined;
        return Number.isFinite(value) ? value : null;
    }

    function markerOf(text, marker) {
        const haystack = typeof text === "string" ? text.toLowerCase() : "";
        return haystack.indexOf(marker) >= 0;
    }

    // m-4: the host's OWN visibility, with the engine's own API when it has one.
    // `checkVisibility()` (CSSOM, Safari 17+) answers for `display` / `visibility`
    // of the node AND its ancestors — opacity excluded at the spec default, which
    // is what we want here (a 0.99-faded host is still a host on screen). On an
    // engine without it the read narrows to the host's own computed `display`, and
    // on a total loss of both it is `null` = NOT OBSERVED, never `false`.
    function nodeVisibilityOf(win, node) {
        if (!node || typeof node !== "object") return null;
        try {
            if (typeof node.checkVisibility === "function") return !!node.checkVisibility();
        } catch (_) { /* fall through to the narrower read below */ }
        const computed = win && typeof win.getComputedStyle === "function"
            ? win.getComputedStyle(node)
            : null;
        if (computed && typeof computed.display === "string" && computed.display) return computed.display !== "none";
        if (node.style && typeof node.style.display === "string" && node.style.display) return node.style.display !== "none";
        return null;
    }

    /* Ancestry, read as STRUCTURE only: tagName, the presence of the host
     * attribute, and class-name MARKERS. `reachedRoot` is the honesty flag — a
     * walk that cannot reach the root box (a stand-in page, a detached node, a
     * shadow boundary) has NOT established that the focus is outside the terminal,
     * so the caller must answer "unavailable" instead of guessing HO-3.
     *
     * m-3: the INNERMOST `data-instance-id` owns the answer. PTY hosts nest (a
     * host cloned into a detached stack, a host inside a cached host), and a walk
     * that kept ascending let the OUTER host overwrite the INNER one's foreground
     * / visibility answers — the capture then described a box the caret was not
     * in. The first hit is therefore the last one read; the walk keeps ascending
     * afterwards, but only for the STRUCTURAL markers (`#terminal-container`,
     * `#quick-keys-panel`, the root) so `reachedRoot` stays honest about whether
     * "outside the terminal" was proven.
     *
     * m-4: `hostVisible` is the host's OWN visibility, read through
     * `checkVisibility()` where the engine has it (CSS `display` / `visibility`
     * of the host and its ancestors, opacity excluded per spec default), and
     * through the host's own computed `display` where it does not. The fallback
     * is the narrower read — an ancestor hidden by `display:none` still answers
     * true there — and that asymmetry is the documented boundary of the field,
     * not a bug the caller may repair. */
    function focusAncestryOf(win, doc, element, activeInstanceId) {
        const out = {
            reachedRoot: false,
            walkable: !!element && typeof element === "object" && typeof element.tagName === "string",
            insideTerminalContainer: false,
            insideQuickKeysPanel: false,
            insideTerminalHost: null,
            hostIsForeground: null,
            hostVisible: null,
            attributeReadable: true,
            xtermMarker: false,
            helperMarker: false,
            ancestorsRead: 0,
        };
        if (!out.walkable) return out;
        let node = element;
        let hostRead = false;
        for (let depth = 0; depth < 32 && node; depth++) {
            if (typeof node.tagName !== "string") break;
            const tag = node.tagName.toUpperCase();
            const classes = typeof node.className === "string" ? node.className : "";
            out.ancestorsRead++;
            if (markerOf(classes, XTERM_MARKER)) out.xtermMarker = true;
            if (markerOf(classes, HELPER_MARKER)) out.helperMarker = true;
            if (typeof node.id === "string") {
                if (node.id === "terminal-container") out.insideTerminalContainer = true;
                if (node.id === "quick-keys-panel") out.insideQuickKeysPanel = true;
            }
            if (!hostRead) {
                const hostId = typeof node.getAttribute === "function"
                    ? node.getAttribute("data-instance-id")
                    : undefined;
                if (typeof node.getAttribute !== "function") out.attributeReadable = false;
                if (hostId !== undefined && hostId !== null && hostId !== "") {
                    // Only the COMPARISON leaves this function, never the value.
                    hostRead = true;
                    out.insideTerminalHost = true;
                    out.hostVisible = nodeVisibilityOf(win, node);
                    out.hostIsForeground = typeof activeInstanceId === "string" && activeInstanceId !== ""
                        ? hostId === activeInstanceId
                        : null;
                }
            }
            if (tag === "HTML" || (doc && node === doc.documentElement)) {
                out.reachedRoot = true;
                break;
            }
            node = node.parentNode;
        }
        if (out.insideTerminalHost === null && out.reachedRoot && out.attributeReadable) {
            // The walk reached the root, every step could read the attribute, and no
            // step carried `data-instance-id`: positively OUTSIDE a PTY host.
            out.insideTerminalHost = false;
        }
        return out;
    }

    // HO-4 without a cross-origin protocol (PLAN R3): the parent only ever asks
    // its OWN child document "is your focused element editable". A thrown access
    // (cross-origin, the normal iPad case) answers null = not inspectable, and the
    // caller reports `unknown` instead of guessing.
    function iframeEditableFocusOf(element) {
        try {
            const childDoc = element && typeof element.contentDocument !== "undefined"
                ? element.contentDocument
                : (element && element.contentWindow ? element.contentWindow.document : null);
            if (!childDoc || typeof childDoc.activeElement === "undefined") return null;
            return isEditableHost(childDoc.activeElement);
        } catch (_) {
            return null;
        }
    }

    // PHASE1-CONTRACT B章 disambiguation order, as data. `basis` names the branch
    // that decided, `unavailableReason` names the reason a category could not be
    // decided ("" only when it was). Never a guess: an unreadable ancestry, an
    // undeterminable foreground instance or an uninspectable iframe all answer
    // `unknown` with a reason.
    function focusOwnershipOf(win, doc, state, vv) {
        const activeElementAvailable = !!doc && typeof doc === "object" && "activeElement" in doc;
        const element = activeElementAvailable ? doc.activeElement : null;
        const tag = element && typeof element.tagName === "string" ? element.tagName.toUpperCase() : "";
        const editable = isEditableHost(element);
        const activeInstanceId = state && typeof state.activeInst === "string" ? state.activeInst : "";
        const ancestry = focusAncestryOf(win, doc, element, activeInstanceId);
        const box = clientBoxOf(element);
        let category = OWNERSHIP.UNKNOWN;
        let basis = "not-evaluated";
        let unavailableReason = "";

        if (!activeElementAvailable) {
            unavailableReason = "document-active-element-unavailable";
        } else if (!element) {
            category = OWNERSHIP.NONE;
            basis = "no-focused-element";
        } else if (editable && ancestry.insideTerminalHost === true) {
            if (ancestry.hostIsForeground === true && ancestry.hostVisible !== false) {
                category = OWNERSHIP.PTY;
                basis = "editable-focus-inside-foreground-terminal-host";
            } else if (ancestry.hostIsForeground === false || ancestry.hostVisible === false) {
                // B.5: a hidden / non-foreground host has NO right to ownership.
                category = OWNERSHIP.NONE;
                basis = "editable-focus-inside-hidden-or-inactive-terminal-host";
            } else {
                unavailableReason = "foreground-instance-not-determinable";
                basis = "editable-focus-inside-terminal-host";
            }
        } else if (editable && ancestry.insideTerminalContainer) {
            unavailableReason = "terminal-host-membership-not-determinable";
            basis = "editable-focus-inside-terminal-container";
        } else if (editable && ancestry.reachedRoot) {
            category = OWNERSHIP.WEB;
            basis = "editable-focus-on-page-web-input";
        } else if (editable) {
            unavailableReason = "focus-ancestor-chain-truncated";
        } else if (tag === "IFRAME") {
            const inner = iframeEditableFocusOf(element);
            if (inner === true) {
                category = OWNERSHIP.IFRAME;
                basis = "editable-focus-inside-iframe-document";
            } else if (inner === false) {
                category = OWNERSHIP.NONE;
                basis = "iframe-focus-not-editable";
            } else {
                unavailableReason = "iframe-inner-focus-not-inspectable";
                basis = "focus-on-iframe-element";
            }
        } else if (ancestry.reachedRoot) {
            category = OWNERSHIP.NONE;
            basis = "focus-on-non-editable-element";
        } else {
            unavailableReason = "focus-ancestor-chain-truncated";
        }

        let role = "unknown";
        if (!element) {
            role = "none";
        } else if (tag === "TEXTAREA" && ancestry.helperMarker && ancestry.xtermMarker) {
            role = "xterm-helper-textarea";
        } else if (editable && ancestry.insideTerminalHost === true) {
            role = "terminal-host-child";
        } else if (tag === "TEXTAREA") {
            role = "web-textarea";
        } else if (tag === "INPUT") {
            role = "web-input";
        } else if (editable) {
            role = "web-contenteditable";
        } else if (tag === "IFRAME") {
            role = "iframe";
        } else if (ancestry.reachedRoot) {
            role = "non-editable";
        }

        return {
            ownership: { category, basis, unavailableReason },
            activeInput: {
                observed: !!element,
                tag: tag,
                editable: editable,
                role: role,
                xtermMarker: ancestry.xtermMarker,
                helperMarker: ancestry.helperMarker,
                insideTerminalContainer: ancestry.insideTerminalContainer,
                insideTerminalHost: ancestry.insideTerminalHost,
                insideQuickKeysPanel: ancestry.insideQuickKeysPanel,
                hostIsForeground: ancestry.hostIsForeground,
                hostVisible: ancestry.hostVisible,
                ancestryReachedRoot: ancestry.reachedRoot,
                // Same-frame client rect: this is D1's ②, the number the p9 schema
                // never carried. Client space, compared against [0, vv.height].
                rect: box,
                rectUnavailableReason: box ? "" : (element ? "focus-client-rect-unavailable" : "no-focused-element"),
                insideVisibleViewport: box && vv ? (box.top >= 0 && box.bottom <= finite(vv.height) + 1) : null,
                unavailableReason: unavailableReason,
            },
        };
    }

    // The applier's published facts, read in one pass. `rawShortfall` is the
    // contract's MEASUREMENT (clientHeight − vv.height) kept for the record so a
    // reader can see what the keyboard covered; it is never fed into `occlusion`
    // (that stays the applier's mirror) and never into any compensation, so this
    // is the contract's "测量 ≠ 补偿", not the §3-forbidden "offsetTop into raw".
    function keyboardObservationOf(win, doc) {
        const vv = win && win.visualViewport ? win.visualViewport : null;
        const layoutHeight = doc && doc.documentElement ? finite(doc.documentElement.clientHeight) : 0;
        const vvHeight = vv ? finite(vv.height) : 0;
        const mirror = mirroredOcclusionOf(win);
        return {
            mirrorExposed: mirror !== null,
            mirrorOcclusion: mirror === null ? 0 : mirror,
            rawShortfall: vv ? Math.max(0, layoutHeight - vvHeight) : 0,
            layoutHeight: layoutHeight,
            vvHeight: vvHeight,
            vvWidth: vv ? finite(vv.width) : 0,
            vvOffsetTop: vv ? finite(vv.offsetTop) : 0,
            scale: vv ? finite(vv.scale || 1) : 1,
            zoomed: !!(vv && vv.scale && Math.abs(vv.scale - 1) > 0.01),
            vvObserved: !!vv,
            focusEditable: isEditableHost(doc ? doc.activeElement : null),
        };
    }

    /* B-3 (方案 b): THIS IS A DERIVED HINT, NEVER THE ACCEPTANCE STATE.
     * `derivedState` names which DIRECTION the applier's mirror moved across two
     * probe frames. It is not a §5 machine: the applier keeps `terminalAvoidance
     * Active` / `...Recovering` / `...GeometryGrace` module-local, so from outside
     * the latches are invisible and any label here is an inference over samples the
     * probe happened to take. The AUTHORITIES stay where they always were — the
     * frozen judge (evidence/verify-sop.mjs → makeKeyboardSessionJudge, which
     * replays §5 from clientHeight / vv.height / vv.width / ui.activeElementTag)
     * and the probe's measurement fields (`occlusion`, `rawShortfall`, the rects).
     * A verifier reads THOSE; `derivedState` only tells a reader which way to look,
     * so every branch states how it knows (`evidence` / `basis`) and every branch
     * that cannot know answers "unknown" plus the reason. Three inferences the
     * first review named as over-claims are held to that line:
     *   * grace needs the PREVIOUS frame to have owed a raw too (§5.7 grace is
     *     `active && geometryChanged`, not "geometry changed somewhere");
     *   * a zoom frame is session-exempt (§ D2) and is reported before the
     *     recovering branch, so this label never contradicts its own rule;
     *   * an unlock (§5.5 click / focusin) journalled AFTER the previous frame
     *     means the latch may already be cleared, so recovering is not derivable
     *     any more — the trace records the unlock, the label says unknown. */
    function keyboardSessionOf(observation, previous, unlocks) {
        const obs = observation || keyboardObservationOf(null, null);
        const span = unlocks || {};
        const marker = {
            derived: true,
            derivedState: SESSION_STATE.UNKNOWN,
            basis: "not-evaluated",
            evidence: "unavailable",
            unavailableReason: "",
            geometryChangedSincePrevious: null,
            previousMirrorOcclusion: null,
            unlockTick: Number.isFinite(span.tick) ? span.tick : null,
            previousUnlockTick: Number.isFinite(span.previousTick) ? span.previousTick : null,
        };
        if (!obs.mirrorExposed) {
            marker.basis = "no-occlusion-pass-observed";
            marker.unavailableReason = "applier-session-mirror-unset";
            return marker;
        }
        marker.evidence = "applier-mirror";
        marker.previousMirrorOcclusion = previous ? finite(previous.mirrorOcclusion) : null;
        // §5.5 / §5.7 geometry change: layout height and visual-viewport WIDTH,
        // the pair the applier compares — a vv.height-only change is the keyboard,
        // not new geometry, and must not be reported as grace.
        marker.geometryChangedSincePrevious = previous
            ? (finite(obs.layoutHeight) !== finite(previous.layoutHeight)
                || finite(obs.vvWidth) !== finite(previous.vvWidth))
            : null;
        // An unlock landed between the two frames: the latch may already be open,
        // so nothing on this frame says which. Recorded, not judged. A null
        // previous tick means the previous frame had journalled NO unlock yet, so
        // any unlock now present is, by definition, one that landed since then.
        const unlockedSincePrevious = marker.unlockTick !== null
            && (marker.previousUnlockTick === null || marker.unlockTick > marker.previousUnlockTick);
        if (obs.zoomed) {
            marker.derivedState = SESSION_STATE.NONE;
            marker.basis = "zoomed-session-exempt";
            return marker;
        }
        if (obs.mirrorOcclusion > 1) {
            if (marker.geometryChangedSincePrevious === true && marker.previousMirrorOcclusion > 1) {
                marker.derivedState = SESSION_STATE.GEOMETRY_GRACE;
                marker.basis = "applier-mirror-positive-and-geometry-changed-while-active";
                marker.evidence = "applier-mirror+frame-history";
            } else if (marker.geometryChangedSincePrevious === true) {
                // Geometry moved but the previous frame never owed a raw: grace is
                // not the reading, and neither is a session that just started.
                marker.basis = "applier-mirror-positive-with-geometry-change-but-never-active";
                marker.unavailableReason = "never-active-before-geometry-change";
                return marker;
            } else {
                marker.derivedState = SESSION_STATE.ACTIVE;
                marker.basis = "applier-mirror-positive";
            }
            return marker;
        }
        if (previous && previous.mirrorOcclusion > 1) {
            if (unlockedSincePrevious) {
                marker.basis = "applier-mirror-zero-after-unlock-was-journalled";
                marker.unavailableReason = "unlocked-after-previous-frame-latch-state-unpublished";
                return marker;
            }
            marker.derivedState = SESSION_STATE.RECOVERING;
            marker.basis = "applier-mirror-zero-after-an-observed-active-frame";
            marker.evidence = "applier-mirror+frame-history";
            // A §5.5 unlock (click / focusin / geometry) inside the two-frame span
            // would already have cleared the latch; `unlockTrace` records exactly
            // those, and this marker does not consume them.
            marker.unavailableReason = "recovery-latch-not-published-by-applier";
            return marker;
        }
        if (!obs.vvObserved) {
            marker.basis = "applier-mirror-zero-without-visualViewport";
            marker.unavailableReason = "no-visualViewport";
            return marker;
        }
        if (!obs.focusEditable) {
            marker.derivedState = SESSION_STATE.NONE;
            marker.basis = "no-editable-focus";
            return marker;
        }
        if (obs.rawShortfall <= 1) {
            marker.derivedState = SESSION_STATE.NONE;
            marker.basis = "no-keyboard-shortfall";
            return marker;
        }
        marker.basis = "applier-mirror-zero-with-unexplained-shortfall";
        marker.unavailableReason = "never-entered-or-latched-recovery-indistinguishable";
        return marker;
    }

    /* §5.5 unlock evidence, RECORD ONLY ("仅记录不参与判定"). A `click` and a
     * `touchstart` are kept apart on purpose: frozen §5.5 unlocks the recovery
     * latch on click and focusin, never on touchstart, and the p9 capture carried
     * zero click frames — which is the whole GI-2 seq 373–377 INCONCLUSIVE.
     * Each entry stamps the tick at which the probe OBSERVED the handler plus the
     * geometry visible at that instant, so the sub-frame order D1 asks for
     * (event → vv.height/offsetTop change → geometry sample) is derivable: an
     * entry's tick is before the frame's `captureTick`, and its own numbers say
     * whether the geometry had already moved when the handler ran.
     *
     * M-6: the ring holds the DECISION events only. A continuous pan emits
     * `scroll` / `resize` on every frame — dozens per second — and a single shared
     * ring of 16 let that flood evict the one click §5.5 is asking for (the
     * journal then reported its own blindness as "no click happened"). High-
     * frequency geometry events therefore stay COUNTS: `counts.vvScroll` /
     * `counts.vvResize` keep their exact totals, only the entry ring is reserved
     * for click / touchstart / focusin / focusout, the events a verdict reads.
     * N-1: the visualViewport's `resize` and the window's `resize` are counted
     * separately (`vvResize` vs `windowResize`) so a window resize can never be
     * read as a keyboard-shaped viewport change. */
    const TRACE_JOURNAL_TYPES = Object.freeze({
        click: true, touchstart: true, focusin: true, focusout: true,
    });
    const TRACE_GEOMETRY_TYPES = Object.freeze({
        resize: true, scroll: true, "window-resize": true,
    });

    function createEventTrace(win, doc) {
        const counts = {
            click: 0, clickOnTerminalHost: 0, touchstart: 0, focusin: 0,
            focusout: 0, vvResize: 0, vvScroll: 0, windowResize: 0, other: 0,
        };
        let entries = [];
        let tick = 0;
        let unlockTick = null;
        return {
            note(type, event) {
                tick++;
                const name = typeof type === "string" ? type.toLowerCase() : "";
                // The client passes the visualViewport's own `resize` / `scroll` as
                // those bare names and the window's as `window-resize`, so the bare
                // names map to the VV counters, `window-resize` to its own, and
                // everything else is `other`.
                if (name === "click") counts.click++;
                else if (name === "touchstart") counts.touchstart++;
                else if (name === "focusin") counts.focusin++;
                else if (name === "focusout") counts.focusout++;
                else if (name === "resize") counts.vvResize++;
                else if (name === "scroll") counts.vvScroll++;
                else if (name === "window-resize") counts.windowResize++;
                else counts.other++;
                // §5.5 unlock events: the tick is remembered so a two-frame span can
                // state that an unlock landed between them (advisory, never applied).
                if (name === "click" || name === "focusin") unlockTick = tick;
                if (!TRACE_JOURNAL_TYPES[name]) return;
                const target = event && event.target ? event.target : null;
                const ancestry = target
                    ? focusAncestryOf(win, doc, target, "")
                    : { insideTerminalHost: null, xtermMarker: false, helperMarker: false };
                if (name === "click" && ancestry.insideTerminalHost === true) counts.clickOnTerminalHost++;
                const vv = win && win.visualViewport ? win.visualViewport : null;
                const mirror = mirroredOcclusionOf(win);
                entries.push({
                    tick: tick,
                    type: name,
                    tag: target && typeof target.tagName === "string" ? target.tagName.toUpperCase() : "",
                    editable: isEditableHost(target),
                    onTerminalHost: ancestry.insideTerminalHost,
                    xtermMarker: ancestry.xtermMarker,
                    vvHeight: vv ? finite(vv.height) : null,
                    vvWidth: vv ? finite(vv.width) : null,
                    vvOffsetTop: vv ? finite(vv.offsetTop) : null,
                    // N-3: the layout side of the same pair, so §5.7's
                    // `layoutHeight`/`vvWidth` comparison is derivable from the
                    // journal alone and does not need a second frame.
                    layoutHeight: doc && doc.documentElement ? finite(doc.documentElement.clientHeight) : null,
                    scale: vv ? finite(vv.scale || 1) : null,
                    mirrorOcclusion: mirror,
                });
                if (entries.length > TRACE_MAX_ENTRIES) entries = entries.slice(entries.length - TRACE_MAX_ENTRIES);
            },
            unlockTick() {
                return unlockTick;
            },
            report() {
                // `geometryCountedOnly:true` is the frame's own word for M-6 — the
                // server checks it, because a journal that silently dropped a click
                // would otherwise report its own blindness as "no click happened".
                return {
                    advisoryOnly: true,
                    captureTick: tick,
                    geometryCountedOnly: true,
                    counts: Object.assign({}, counts),
                    entries: entries.map((item) => Object.assign({}, item)),
                };
            },
        };
    }

    // R6 / GI-9, the tool's own introspection: where the diagnostic panel is and
    // whether it is reachable. The corner and the placement verdict are the ones
    // applyPanelLayout just authored, mirrored onto the panel as data attributes so
    // the capture states the tool's own geometry instead of a reader guessing it.
    //
    // B-2: `support` answers from ONE vocabulary — supported / unsupported /
    // unavailable — the same set the server whitelists
    // (viewportDebugPanelSupportPattern). "The panel exists but nobody wrote the
    // dataset yet" is UNAVAILABLE, never a fourth word: an invented value there was
    // every upload answered 400 and a panel stuck on "upload failed; retrying".
    // UNAVAILABLE always carries its reason, because a verdict may not read an
    // unreported corner as a reachable one.
    function panelReportOf(win, doc, vv) {
        const el = doc && typeof doc.getElementById === "function"
            ? doc.getElementById("viewport-debug-panel")
            : null;
        if (!el) {
            return {
                exists: false,
                corner: "unavailable",
                support: "unavailable",
                unsupportedReason: "panel-absent",
                lifted: null,
                panCorrected: null,
                nominalHeight: null,
                rect: null,
                rectUnavailableReason: "panel-absent",
                keyboardOverlap: null,
                insideVisibleViewport: null,
            };
        }
        const data = (el.dataset && typeof el.dataset === "object") ? el.dataset : {};
        const box = clientBoxOf(el);
        const nominal = Number.parseFloat(data.debugNominalHeight);
        const corner = typeof data.debugCorner === "string" && data.debugCorner ? data.debugCorner : "unknown";
        const authoredReason = typeof data.debugUnsupportedReason === "string" ? data.debugUnsupportedReason : "";
        const datasetAuthored = typeof data.debugSupport === "string" && data.debugSupport;
        const support = datasetAuthored ? data.debugSupport : "unavailable";
        return {
            exists: true,
            corner: corner,
            support: support,
            unsupportedReason: datasetAuthored ? authoredReason : "panel-layout-not-authored",
            lifted: data.debugLifted === "1" ? true : (data.debugLifted === "0" ? false : null),
            panCorrected: data.debugPanCorrected === "1" ? true : (data.debugPanCorrected === "0" ? false : null),
            nominalHeight: Number.isFinite(nominal) ? nominal : null,
            rect: box,
            rectUnavailableReason: box ? "" : "panel-client-rect-unavailable",
            keyboardOverlap: box && vv ? Math.max(0, finite(box.bottom) - finite(vv.height)) : null,
            insideVisibleViewport: box && vv ? (box.top >= 0 && box.bottom <= finite(vv.height) + 1) : null,
        };
    }

    /* m-2: the degraded frame. ONLY self-describing fields — every measurement is
     * absent (null) rather than zero, so a verifier cannot mistake a failed read
     * for a page whose viewport collapsed. `reason` is the reason the capture was
     * attempted and stays inside the server whitelist; `probeReadFailedReason`
     * carries the fixed marker the SOP greps for. */
    function probeReadFailedSnapshot(reason, capturedAt) {
        return {
            probeVersion: PROBE_VERSION,
            schemaRev: SCHEMA_REV,
            capturedAt,
            reason,
            probeReadFailed: true,
            probeReadFailedReason: "probe-read-failed",
            innerWidth: 0,
            innerHeight: 0,
            clientWidth: 0,
            clientHeight: 0,
            dpr: 1,
            device: { userAgent: "", platform: "", touchPoints: 0, orientation: "", screenWidth: 0, screenHeight: 0, coarse: false },
            document: null,
            vv: null,
            app: null,
            main: null,
            workspace: null,
            terminal: null,
            occlusion: null,
            ptyHosts: null,
            controls: null,
            statusBar: null,
            quickKeys: null,
            webPanel: null,
            xtermScreen: null,
            xtermRows: null,
            ui: null,
            ownership: null,
            activeInput: null,
            keyboardSession: null,
            unlockTrace: null,
            panel: null,
        };
    }

    function snapshotFromPage(page, reason, capturedAt, context) {
        const win = page.window;
        const doc = page.document;
        const ctx = context || {};
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
        // Phase 2 task 1, all read in this one pass so the ownership category, the
        // focused host's rect and the session marker come from ONE frame — D1 ②.
        const focusReport = focusOwnershipOf(win, doc, state, vv);
        const keyboardObservation = ctx.keyboardObservation || keyboardObservationOf(win, doc);
        // The two-frame span the derived session label may look across, INCLUDING the
        // §5.5 unlock tick: a click that landed between the frames means the recovery
        // latch may already be open, so `recovering` is no longer derivable (B-3).
        const sessionSpan = { tick: ctx.unlockTick, previousTick: ctx.previousUnlockTick };
        const keyboardSession = Object.assign({}, keyboardObservation,
            keyboardSessionOf(keyboardObservation, ctx.keyboardPrevious, sessionSpan));
        const unlockTrace = ctx.eventTrace
            ? ctx.eventTrace.report()
            : {
                advisoryOnly: true,
                captureTick: 0,
                geometryCountedOnly: true,
                counts: null,
                entries: null,
                unavailableReason: "event-trace-unavailable",
            };
        const panelReport = panelReportOf(win, doc, vv);

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
            // ── issue #116 Phase 2 task 1, additive fields (see the block comment
            // above focusOwnershipOf). Every one of them is a READ of a fact the
            // p9 schema could not carry, and every one states its own reason when
            // the fact was not obtainable.
            schemaRev: SCHEMA_REV,
            ownership: focusReport.ownership,
            activeInput: focusReport.activeInput,
            keyboardSession: keyboardSession,
            unlockTrace: unlockTrace,
            panel: panelReport,
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
        // Phase 2 task 1: the §5.5 unlock journal (record only) and one frame of
        // keyboard-session memory, so a frame can state `recovering` / `geometryGrace`
        // from the applier's mirror plus the previous frame instead of staying mute.
        const eventTrace = createEventTrace(page.window, page.document);
        let previousKeyboardObservation = null;
        // The §5.5 unlock tick as of the LAST captured frame: the span between it
        // and the current tick is the only window in which `recovering` may be
        // inferred at all (B-3 — derived, never authoritative).
        let previousUnlockTick = null;

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

        /* m-2: NO UNCAUGHT EXCEPTION MAY REACH THE PRODUCT PAGE FROM THE PROBE.
         * The probe walks live DOM (getComputedStyle, getBoundingClientRect,
         * elementFromPoint, a host's `.xterm-screen`) inside a page it does not
         * own, and one throw inside a listener would surface as an error overlay on
         * the very iPad being measured — where the tester cannot open a debugger.
         * A failed read is therefore DATA: the frame goes out marked
         * `probeReadFailed` / `probeReadFailedReason:"probe-read-failed"` with the
         * geometry fields left UNREAD (null), never zero-filled, so the frozen
         * verifier reads it as INCONCLUSIVE (missing `vv.height`) instead of as a
         * measurement of a zero-sized page. The capture `reason` stays inside the
         * server's whitelist — the failure lives in its own field, because
         * inventing a reason value there is a 400 on every upload. */
        function capture(reason) {
            if (!running || paused) return null;
            let snapshot;
            try {
                const observation = keyboardObservationOf(page.window, page.document);
                snapshot = snapshotFromPage(page, reason, now().toISOString(), {
                    keyboardObservation: observation,
                    keyboardPrevious: previousKeyboardObservation,
                    eventTrace,
                    unlockTick: eventTrace.unlockTick(),
                    previousUnlockTick: previousUnlockTick,
                });
                previousKeyboardObservation = observation;
                previousUnlockTick = eventTrace.unlockTick();
            } catch (_) {
                lastError = "probe-read-failed";
                snapshot = probeReadFailedSnapshot(reason, now().toISOString());
            }
            seq++;
            latestReport = { session: sessionId, seq, snapshot };
            try {
                onUpdate(snapshot);
            } catch (_) { /* the panel's own render is advisory to the upload */ }
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

        /* B-1 + M-7: the two registration paths, and the rule that a READ may not
         * silence a MEASUREMENT.
         *
         * B-1: every registration records its OWN capture flag and `stop()` hands
         * that flag straight back. `removeEventListener` matches on the
         * (type, handler, capture) TRIPLE, so removing a capture-phase listener with
         * the two-argument form never removed anything: every start()/stop() cycle
         * left another `click` / `touchstart` handler on the product's document, on a
         * page whose owner cannot be told they are instrumented.
         * M-7: the advisory journal runs inside its own try/catch and `schedule()`
         * runs UNCONDITIONALLY after it. A diagnostic that failed to write its
         * journal must not be the reason the geometry of the very state under
         * investigation never gets captured. */
        function listen(target, type, reason) {
            if (!target || typeof target.addEventListener !== "function") return;
            const handler = (event) => {
                try {
                    // N-1: the JOURNAL is keyed by the capture REASON, not by the DOM
                    // event name. The visualViewport fires `resize` and so does the
                    // window; journalling the bare event name counted a window resize
                    // as vvResize, which is exactly the keyboard-shaped signal a
                    // reader must not get from the wrong source.
                    eventTrace.note(reason, event);
                } catch (_) { /* advisory journal only; the capture below still runs */ }
                schedule(reason);
            };
            target.addEventListener(type, handler);
            listeners.push({ target, type, handler, capture: false });
        }

        // §5.5 unlock evidence: `click` and `touchstart` are observed and journalled
        // WITHOUT scheduling a capture, so recording the unlock never adds frames —
        // and never feeds back into the applier, which is passive on both.
        function observeOnly(target, type) {
            if (!target || typeof target.addEventListener !== "function") return;
            const handler = (event) => {
                try {
                    eventTrace.note(type, event);
                } catch (_) { /* the unlock stays recorded by the counters that did run */ }
            };
            target.addEventListener(type, handler, { passive: true, capture: true });
            listeners.push({ target, type, handler, capture: true });
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
            observeOnly(page.document, "click");
            observeOnly(page.document, "touchstart");
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
            // B-1: detach with the SAME triple it was registered with — the capture
            // flag is part of the identity, and the recorded flag is what the
            // Node test asserts on.
            for (const item of listeners) {
                item.target.removeEventListener(item.type, item.handler, item.capture === true);
            }
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
    //
    // R6 / GI-9（issue #116 Phase 2 task 2）修的是诊断工具自己的两个真缺陷：
    //   * 底部居位：`position:fixed` 的包含块是 LAYOUT viewport，不是可视 viewport。
    //     软键盘弹出时 vv.height 缩小而 layout 高不变，于是 bottom 居位的面板
    //     client rect 落在 vv.height 之下 = 键盘覆盖区，主按钮整排不可按。
    //   * 顶边居位：外壳被 Safari pan 平移（vv.offsetTop ≠ 0）时，fixed 盒子的
    //     client rect = 书写的 layout 位置 − vv.offsetTop，顶部居位整块出屏。
    // 修正只有一句话：把面板这个工具自己的盒子放进「可见窗口」内——client 坐标
    // [0, vv.height]，等价于 layout 坐标 [vv.offsetTop, vv.offsetTop + vv.height]
    // （PHASE1-CONTRACT 术语章：同一个窗口的两种坐标描述，禁止相加混用）。写出的
    // 仍只是面板自己的 fixed inset，与产品几何（index.html 的避让写入集合）零耦合：
    // 这里不写任何产品 DOM / 样式，不参与 occlusion / raw 算式，也不读 applier 的
    // 状态去重新判定会话（sessionActive 只是回声字段）。放不进去时显式
    // support="unsupported" + 原因，绝不静默把面板摆在键盘下或屏外
    // （R6：可读可操作，或明确标记该居位不支持）。
    const PANEL_MIN_HEIGHT = 88;            // 折叠态：主按钮一排的最低可用高度
    const PANEL_MIN_HEIGHT_EXPANDED = 96;  // 展开态地板，沿用既有 clamp 下界
    const PANEL_MIN_WIDTH = 180;

    function panelLayoutFor(input) {
        const src = input || {};
        const innerWidth = finite(src.innerWidth) || 360;
        const innerHeight = finite(src.innerHeight) || 600;
        const vvWidth = finite(src.vvWidth) || innerWidth;
        const vvHeight = finite(src.vvHeight) || innerHeight;
        // fixed 包含块 = LAYOUT viewport；缺输入时退回 innerHeight（旧调用口径）。
        const layoutWidth = finite(src.layoutWidth) || innerWidth;
        const layoutHeight = finite(src.layoutHeight) || innerHeight;
        const vvOffsetTop = finite(src.vvOffsetTop);
        const vvOffsetLeft = finite(src.vvOffsetLeft);
        const visibleWidth = Math.max(120, Math.min(innerWidth, vvWidth) - PANEL_MARGIN * 2);
        const visibleHeight = Math.max(96, Math.min(innerHeight, vvHeight) - PANEL_MARGIN * 2);
        const autoCollapse = visibleHeight < PANEL_COLLAPSE_THRESHOLD;
        const collapsed = src.collapsed === undefined ? autoCollapse : !!src.collapsed;
        const safeTop = Math.max(0, finite(src.safeTop));
        const safeRight = Math.max(0, finite(src.safeRight));
        const safeBottom = Math.max(0, finite(src.safeBottom));
        // 控制列（#terminal-controls，含 quick-keys 入口）贴在右缘；靠右居位时为它
        // 预留宽度，使诊断浮层在默认右上也不埋掉 quick-keys。
        const reservedRight = Math.max(0, finite(src.reservedRight));
        const corner = src.corner || "tr";

        // 只读测量，非补偿：键盘覆盖了多少屏显区域。它不进入 occlusion（那个数字
        // 永远是 applier 的 mirror），也不进入任何产品几何算式。
        const keyboardShortfall = Math.max(0, layoutHeight - vvHeight);
        const keyboardPresent = keyboardShortfall > 1;
        const bottomDock = corner === "bl" || corner === "br";
        const freeDock = corner === "free";
        const leftDock = freeDock || corner.endsWith("l");

        // 面板期望高度 = 可视高 − 已占用安全区；折叠时只留主按钮一排所需高度。
        const topBudget = Math.max(96, visibleHeight - safeTop);
        const desiredHeight = collapsed
            ? clamp(112, 88, topBudget)
            : clamp(Math.min(visibleHeight - safeTop, 520), 96, topBudget);
        const minHeight = collapsed ? PANEL_MIN_HEIGHT : PANEL_MIN_HEIGHT_EXPANDED;

        // 落位分两步：先在 layout 空间写出 fixed inset，再由 inset 反推 client 落点
        // 做校验。术语章禁止把两种坐标相加混用，这里做的是同一个窗口的坐标换算
        // （fixed 盒子的 client 位置 = 书写的 layout 位置 − vv.offsetTop），不是把
        // offsetTop 加进产品补偿式——面板不写产品几何，只写自己这五个 style 值。
        //   * 顶边 / 左边：跟着可见窗口平移。pan ≠ 0 而不把 offsetTop 写进 inset，
        //     client 落点就是 inset − offsetTop，直接出屏（A0 实测 app.top=−403）。
        //   * 底边：只有键盘真的压进来（raw > 1；冻结 §5：raw ≤ 1 不算键盘）才贴
        //     可见窗口底边抬起，否则保持原书写的 rest 位，不做 1px 的无谓挪动。
        const visibleBottomLayout = Math.min(layoutHeight, vvOffsetTop + vvHeight);
        const availableClientHeight = bottomDock
            ? (keyboardPresent ? visibleBottomLayout : layoutHeight) - vvOffsetTop - safeBottom - PANEL_MARGIN
            : (freeDock ? vvHeight - PANEL_MARGIN : vvHeight - safeTop - PANEL_MARGIN);

        let support = "supported";
        let unsupportedReason = "";
        // W-5 / m-1: 双轴同时不足时的确定性优先级。纵向先判、横向不得覆盖它：
        // 键盘采证失败（主按钮排被埋）是本轮唯一真正的不可达，宽度不足往往还能
        // 靠换角 / 折叠救回来，把纵向原因擦掉换成横向原因会让报告指错方向。
        // 顺序固定为 纵向 → 横向 → 落位越界，第一个原因胜出，且不重复覆写。
        if (availableClientHeight < minHeight) {
            // 连折叠态一排按钮都放不下：这是唯一允许纵向放弃的场合，必须说出来。
            support = "unsupported";
            unsupportedReason = "visible-window-too-short-for-panel";
        }
        const height = clamp(Math.min(desiredHeight, availableClientHeight), 0, Math.max(0, availableClientHeight));

        // 横向：右缘为 quick-keys 预留；窄到放不下最小宽度时同样显式声明。
        const availableClientWidth = leftDock
            ? vvWidth - PANEL_MARGIN
            : vvWidth - safeRight - PANEL_MARGIN - reservedRight;
        let width = Math.max(PANEL_MIN_WIDTH, Math.min(PANEL_MAX_WIDTH, visibleWidth));
        if (availableClientWidth < PANEL_MIN_WIDTH) {
            support = "unsupported";
            if (!unsupportedReason) unsupportedReason = "visible-window-too-narrow-for-panel";
        } else {
            width = clamp(width, PANEL_MIN_WIDTH, availableClientWidth);
        }

        // M-4：free 居位的上界含 PANEL_MARGIN，与顶/底/左/右四边的贴边口径一致
        // （冻结基线里面板从不贴边）。少了这个边距，拖到极限的面板会压在键盘顶边
        // 或屏幕边缘那 1px 上，自报"在可见窗口内"而像素上已经出界。
        const freeTop = clamp(finite(src.dragY) + PANEL_MARGIN + safeTop, 0, Math.max(0, vvHeight - height - PANEL_MARGIN));
        const freeLeft = clamp(finite(src.dragX) + PANEL_MARGIN, 0, Math.max(0, vvWidth - width - PANEL_MARGIN));
        // authored(offsetTop, offsetLeft) = 这个 pan 下要写进 style 的 fixed inset。
        // 同一函数对照 pan=0 的写法，才能诚实回答"这一格真的被平移修正过吗"。
        function authored(offsetTop, offsetLeft) {
            const anchoredBottom = keyboardPresent
                ? Math.min(layoutHeight, offsetTop + vvHeight)
                : layoutHeight;
            return {
                top: bottomDock ? null : offsetTop + (freeDock ? freeTop : safeTop + PANEL_MARGIN),
                bottom: bottomDock ? layoutHeight - (anchoredBottom - safeBottom - PANEL_MARGIN) : null,
                left: leftDock ? offsetLeft + (freeDock ? freeLeft : PANEL_MARGIN) : null,
                right: leftDock ? null : layoutWidth - (offsetLeft + availableClientWidth),
            };
        }
        const written = authored(vvOffsetTop, vvOffsetLeft);
        const atRest = authored(0, 0);
        // fixed 的 inset 不能是负数（负值在 overscroll 下落在 layout 之外，画不出
        // 来）：夹到 0，然后按**真正写出去的值**反推 client 落点——落点如果越出
        // 可见窗口，就是这一格放不下，如实报告，不许用理想值粉饰。
        const insetTop = written.top === null ? null : Math.max(0, written.top);
        const insetBottom = written.bottom === null ? null : Math.max(0, written.bottom);
        const insetLeft = written.left === null ? null : Math.max(0, written.left);
        const insetRight = written.right === null ? null : Math.max(0, written.right);
        const layoutTop = insetBottom === null ? insetTop : layoutHeight - insetBottom - height;
        const layoutLeft = insetRight === null ? insetLeft : layoutWidth - insetRight - width;
        const clientTop = layoutTop - vvOffsetTop;
        const clientLeft = layoutLeft - vvOffsetLeft;
        const clientRight = clientLeft + width;

        if (support === "supported"
            && (layoutTop < 0 || layoutTop + height > layoutHeight
                || layoutLeft < 0 || layoutLeft + width > layoutWidth
                || clientTop < 0 || clientTop + height > vvHeight
                || clientLeft < 0 || clientRight > vvWidth)) {
            // 写不出去（inset 被夹）或落点越出可见窗口：这一格无处可放，明说
            // "不支持"，不硬摆一个落在键盘下或屏外的面板让人误以为采到了证。
            support = "unsupported";
            unsupportedReason = "visible-window-outside-layout-viewport";
        }

        const lifted = insetBottom !== null && insetBottom > safeBottom + PANEL_MARGIN + 0.5;
        // panCorrected 是"pan 真的改变了写出去的值"，不是"pan ≠ 0"的复读：与
        // pan=0 的同一份算法结果逐项对照（null = 这一边不写，两边都 null 才算同）。
        const sameInset = (a, b) => (a === null && b === null)
            || (a !== null && b !== null && Math.abs(a - b) <= 0.5);
        const restInset = (value) => (value === null ? null : Math.max(0, value));
        const panCorrected = (vvOffsetTop !== 0 || vvOffsetLeft !== 0)
            && !(sameInset(insetTop, restInset(atRest.top))
                && sameInset(insetBottom, restInset(atRest.bottom))
                && sameInset(insetLeft, restInset(atRest.left))
                && sameInset(insetRight, restInset(atRest.right)));

        const box = Object.assign(
            {},
            bottomDock
                ? { top: "auto", bottom: insetBottom + "px" }
                : { top: insetTop + "px", bottom: "auto" },
            leftDock
                ? { left: insetLeft + "px", right: "auto" }
                : { right: insetRight + "px", left: "auto" },
        );

        return {
            corner: corner,
            collapsed: collapsed,
            autoCollapse: autoCollapse,
            width: width + "px",
            maxHeight: height + "px",
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
            // ── R6 / GI-9 新增：面板自身落位的自省，供 Node 断言与快照自报 ──
            support: support,
            unsupportedReason: unsupportedReason,
            lifted: lifted,
            panCorrected: panCorrected,
            keyboardShortfall: keyboardShortfall,
            keyboardPresent: keyboardPresent,
            // 回声字段：来自 applier 的 mirror；面板落位不看它。
            sessionActive: src.sessionActive === undefined ? null : !!src.sessionActive,
            vvOffsetTop: vvOffsetTop,
            vvOffsetLeft: vvOffsetLeft,
            layoutWidth: layoutWidth,
            layoutHeight: layoutHeight,
            nominalHeight: height,
            clientRect: { top: clientTop, bottom: clientTop + height, left: clientLeft, right: clientRight },
            visibleWindowClient: { top: 0, bottom: vvHeight },
            visibleWindowLayout: { top: vvOffsetTop, bottom: vvOffsetTop + vvHeight },
        };
    }

    /* M-1: 落位结论必须回到人的眼睛里，不只回到上传的 JSON。
     * R6 的口径是"可读可操作，或明确标记该居位不支持"——一个自报 unsupported 却
     * 把结论只写进 dataset 的面板，测试者在 iPad 上看到的仍是"面板在那儿"，于是
     * 一次不可达的采证会被当成采到了。所以每次 relayout 都把居位结论写进面板顶部
     * 自己那一行（#viewport-debug-placement）：supported 时灰字回显居位，
     * unsupported 时琥珀字写明 `corner <c> unsupported: <reason>`。
     * 这一步只写工具自己的 DOM：产品 DOM / 样式一个字节都不碰。 */
    function renderPanelPlacement(win, panel, layout) {
        if (!panel) return;
        let line = null;
        try {
            if (typeof panel.querySelector === "function") {
                line = panel.querySelector("#viewport-debug-placement");
            } else if (win && win.document && typeof win.document.getElementById === "function") {
                line = win.document.getElementById("viewport-debug-placement");
            }
        } catch (_) {
            return;
        }
        if (!line) return;
        const supported = layout.support === "supported";
        const text = "corner " + layout.corner + " " + layout.support
            + (supported ? "" : ": " + (layout.unsupportedReason || "unknown"))
            + (layout.panCorrected ? " · pan-corrected" : "")
            + (layout.lifted ? " · lifted" : "");
        try {
            line.textContent = text;
            if (line.style) {
                // 两种结论都留在屏幕上：supported 灰字常规回显当前居位，unsupported
                // 琥珀加粗写明原因——来回切换不能只改 dataset 而把上一次的琥珀警告
                // 留在屏幕上。display 也显式写回 block，被上一态藏过必须重新亮起来。
                line.style.display = "block";
                line.style.color = supported ? "#9aa7b4" : "#f0b429";
                line.style.fontWeight = supported ? "400" : "700";
            }
        } catch (_) { /* a stand-in node without a text setter stays unrendered */ }
    }

    function applyPanelLayout(win, panel, state) {
        const vv = win.visualViewport || null;
        const docEl = win.document && win.document.documentElement ? win.document.documentElement : null;
        // N-5: 居位缺省归一到 "tr"。调用方（Move 按钮之外的任何路径）传一个没有
        // corner 的 state 时，这里不得抛 TypeError——诊断工具在产品页上抛异常，
        // 就是把被测量的页面弄成不可测。
        const corner = typeof state.corner === "string" && state.corner ? state.corner : "tr";
        // 靠右居位时实测控制列宽度并预留；控制列隐藏（无活动实例）则不预留。
        let reservedRight = 0;
        if (!corner.endsWith("l") && corner !== "free" && win.document && typeof win.document.getElementById === "function") {
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
            layoutWidth: docEl ? docEl.clientWidth : win.innerWidth,
            layoutHeight: docEl ? docEl.clientHeight : win.innerHeight,
            vvWidth: vv ? vv.width : win.innerWidth,
            vvHeight: vv ? vv.height : win.innerHeight,
            // pan 事实：只用于把面板这个工具盒子拉回可见窗口内。
            vvOffsetTop: vv ? vv.offsetTop : 0,
            vvOffsetLeft: vv ? vv.offsetLeft : 0,
            sessionActive: Number.isFinite(win.__mwTerminalOcclusion) ? win.__mwTerminalOcclusion > 1 : null,
            safeTop: state.safeTop,
            safeRight: state.safeRight,
            safeBottom: state.safeBottom,
            reservedRight,
            corner: corner,
            dragX: state.dragX,
            dragY: state.dragY,
            collapsed: state.collapsed,
            scale: vv ? vv.scale : 1,
        });
        panel.style.width = layout.width;
        panel.style.maxHeight = layout.maxHeight;
        panel.style.left = layout.left;
        panel.style.top = layout.top;
        panel.style.bottom = layout.bottom;
        panel.style.right = layout.right;
        // 自报落位（快照的 panel 字段读它）：居位 + 支持性 + 是否被抬起 / pan 修正。
        if (panel.dataset) {
            panel.dataset.debugCorner = layout.corner;
            panel.dataset.debugSupport = layout.support;
            panel.dataset.debugUnsupportedReason = layout.unsupportedReason;
            panel.dataset.debugLifted = layout.lifted ? "1" : "0";
            panel.dataset.debugPanCorrected = layout.panCorrected ? "1" : "0";
            panel.dataset.debugNominalHeight = String(layout.nominalHeight);
        }
        renderPanelPlacement(win, panel, layout);
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
        // M-1：落位状态行。居位不支持时必须在这里看得见（琥珀字 + 原因），不能只
        // 进上传数据；supported 时灰字回显当前居位，让测试者一眼知道面板自认为
        // 落在哪儿。内容由 applyPanelLayout 每次 relayout 重写。
        const placement = doc.createElement("div");
        placement.id = "viewport-debug-placement";
        placement.setAttribute("role", "status");
        placement.textContent = "corner tr supported";
        placement.style.cssText = "margin-bottom:4px;font:11px/1.35 ui-monospace,monospace;color:#9aa7b4;";
        panel.appendChild(placement);
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
                // m-7：拖拽侧与模型侧的边距必须逐项互逆。这里记的是面板在 CLIENT
                // 空间里想去的那个左上角，再剥掉 panelLayoutFor 会原样加回的
                // PANEL_MARGIN（纵向再加 safeTop）——两边算式一对一对得上，面板才
                // 不会在松手时自己跳开 8px；越界由模型侧统一夹紧（含边距，M-4）。
                const targetLeft = baseLeft + (ev.clientX - startX);
                const targetTop = baseTop + (ev.clientY - startY);
                state.dragX = Math.max(0, targetLeft - PANEL_MARGIN);
                state.dragY = Math.max(0, targetTop - PANEL_MARGIN - state.safeTop);
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

    return {
        snapshotFromPage, createClient, createBrowserClient, installBrowser, panelLayoutFor,
        // Phase 2 task 1 / task 2 pure pieces, exported so a Node test can pin
        // them without a browser — including applyPanelLayout, the browser-side
        // writer whose dataset handshake panelReportOf reads back into the capture.
        applyPanelLayout, focusOwnershipOf, keyboardObservationOf, keyboardSessionOf,
        createEventTrace, panelReportOf, isEditableHost,
    };
});
