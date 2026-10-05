// PtyRenderer owns the lifecycle of xterm.js Terminal sessions for
// PTY-backed instances. Sessions are created lazily on activate()
// and disposed on cleanup(); deactivating (i.e. switching to a
// different tab) pauses but keeps the session around so
// re-activation is cheap.
//
// Responsibilities:
//   - create / dispose xterm.js Terminal objects + DOM hosts
//   - resize-driven FitAddon refresh + server-side pty resize
//   - delegate connect / disconnect / log-load to index.html
//     helpers (kept there for now; they depend on the global
//     `state` object and `api()` helper that this renderer does
//     not own).
//
// The renderer is a single instance registered globally for kind
// "pty". All session state lives in `this._sessions` (a Map keyed
// by instance id).

// The "no valid byte offset" cursor state, as this file needs it (issue #86):
//   PTY_CURSOR_UNKNOWN  -1  nothing is painted on this screen, so the client
//                           wants the tail: `since` is OMITTED and the
//                           server's tail default applies (since=0 would
//                           instead replay the oldest 64KB, #81).
//
// This is this file's OWN literal, not a link to index.html's. index.html
// declares the same number as CURSOR_UNKNOWN and is loaded AFTER this file,
// so nothing here could read it at parse time: every <script> in this page is
// a classic script sharing ONE global lexical environment, so declaring the
// same top-level `const` in both files throws SyntaxError in the second one
// and kills the whole application script (no state, no session map, no
// terminal). The PTY_ prefix is what keeps the two files apart, and
// testdata/terminal_status.test.mjs holds the numbers together —
// "no two classic scripts declare the same top-level name" fails if the
// prefix is ever dropped, and the literal comparison fails if either number
// drifts; TestFollowLiveEndSentinelAgreesAcrossTheWire then pins index.html's
// sentinel against the server's sinceFollowLiveEnd.
//
// This file deliberately does NOT declare the follow-live-end state
// (index.html's CURSOR_FOLLOW_LIVE_END, -2). Nothing in the pty renderer
// ever assigns it: both factories reach it only through index.html's loadLog,
// which is the single place that learns a tail was painted without an
// X-Log-Offset header.
const PTY_CURSOR_UNKNOWN = -1;

class PtyRenderer {
    constructor() {
        this._sessions = new Map(); // id → session
        this._fitAttached = false;
    }

    activate(session, container) {
        const id = session.id;
        const termContainer = document.getElementById('terminal-container');
        const ocPanel = document.getElementById('opencode-panel');
        if (termContainer) termContainer.style.display = '';
        if (ocPanel) ocPanel.hidden = true;

        // Lazily create the xterm session for this instance.
        let s = this._sessions.get(id);
        if (!s) {
            s = this._createSession(id, termContainer);
            if (!s) return;
            this._sessions.set(id, s);
        }
        // Show this session's host; hide all other hosts so the
        // user sees a clean switch.
        this._sessions.forEach((other, otherID) => {
            if (otherID !== id && other.container) {
                other.container.style.display = 'none';
            }
        });
        s.container.style.display = 'block';

        // Fit + resize forward so xterm.js picks up the visible size.
        if (s.fitAddon) {
            try { s.fitAddon.fit(); } catch (e) { console.error('xterm fit error:', e); }
        }

        // Hand off the connect / log-load / focus dance to the
        // existing global helpers. These depend on the page-wide
        // `state` and `api` and stay in index.html.
        const inst = (window.state && window.state.instances)
            ? window.state.instances.find(i => i.id === id)
            : null;
        if (!inst) return;
        s.lastKnownStatus = inst.status;

        // `status !== 'running'` does NOT mean stopped: Manager.Start persists
        // a new record as `starting` and flips it to `running` on the kind's
        // ready signal in a separate goroutine, so a freshly created instance
        // is routinely observed as `starting` here. activate() runs exactly
        // once per selection change, so treating that transient state as
        // terminal stranded the session with no WS and no SSE — [Process
        // Stopped] over a live PTY, and input that only reached the process
        // through the silent HTTP fallback (issue #80).
        //
        // The bucket helpers live in index.html and are re-exported on window;
        // the inline fallbacks keep this file unit-testable in isolation.
        // All three are guarded the same way: an unguarded call here would
        // throw when the page has not published it yet.
        const isPending = window.isInstancePendingStatus
            ? window.isInstancePendingStatus(inst.status)
            : (inst.status === 'starting' || inst.status === 'stopping');
        const isLive = window.isInstanceLiveStatus
            ? window.isInstanceLiveStatus(inst.status)
            : (inst.status === 'running' || inst.status === 'unhealthy');
        const isTerminal = window.isInstanceTerminalStatus
            ? window.isInstanceTerminalStatus(inst.status)
            : (!isLive && !isPending);

        if (isPending) {
            // Transient. No banner, no disconnect: reconcileTerminalSessions()
            // promotes this session as soon as a poll observes a live status.
            if (window.updateStatus) window.updateStatus(inst.status + "...");
            return;
        }

        if (isTerminal) {
            if (window.disconnectTTY) window.disconnectTTY(s);
            if (window.loadLog) window.loadLog(s);
            if (window.updateStatus) window.updateStatus('stopped');
            return;
        }

        if (window.hasLiveTTYConnection && window.hasLiveTTYConnection(id)) {
            if (window.updateStatus) window.updateStatus('websocket live');
            if (window.focusTerminalIfPossible) window.focusTerminalIfPossible();
            return;
        }

        // A poll-driven promotion (reconcileTerminalSessions) or an earlier
        // activation already owns this bring-up. hasLiveTTYConnection() is
        // false while that socket is still CONNECTING, so without this guard
        // selecting the tab again would reset the screen and open a second
        // socket — replaying the ring-buffer tail twice (issue #87). The
        // guard is single-sourced in index.html so the two paths cannot drift.
        //
        // ignoreQueuedRetry: re-selecting the tab is an explicit request, so
        // it takes over from a pending reconnect rather than leaving the
        // previous screen up until the timer fires. The other four conditions
        // still block, so this cannot race an activation already mid-loadLog.
        if (window.hasTerminalTransportInFlight
            && window.hasTerminalTransportInFlight(s, { ignoreQueuedRetry: true })) {
            // Staying silent would read as "nothing happened": deactivate()
            // leaves the status bar on "idle", so re-selecting a tab mid
            // bring-up would look inert until the connect finished.
            // Speak up only when no other transport is reporting itself — an
            // SSE session is live rather than connecting, and its own message
            // is the accurate one. A queued retry cannot be the reason we got
            // here: the takeover below clears it first, so whatever stopped us
            // is a connect already under way.
            if (window.updateStatus && !s.logStream) {
                window.updateStatus('connecting...');
            }
            return;
        }

        // Take over from the queued retry, and cancel it here rather than
        // leaving it to disconnectTTY: connectTTY only runs once loadLog
        // settles, so a retry firing in between would open one socket and
        // replay the tail, and the connect that follows would open a second
        // and replay it again (issue #87).
        if (s.ttyReconnectTimer) {
            clearTimeout(s.ttyReconnectTimer);
            s.ttyReconnectTimer = null;
        }

        if (window.resetTerminalForSwitch) window.resetTerminalForSwitch(s);
        // The screen was just cleared, so any previous cursor is meaningless. If
        // loadLog then fails, keeping it would make startSSE request
        // since=<oldOffset> and repaint only the bytes produced since then onto
        // an empty terminal. Reset to PTY_CURSOR_UNKNOWN so startSSE omits
        // `since` and the server's tail default repaints the whole screen.
        //
        // The value IS load-bearing now (issue #86): the other unknown-cursor
        // state, index.html's CURSOR_FOLLOW_LIVE_END, means the opposite —
        // the screen holds the tail and must not be replayed — and index.html's
        // loadLog sets it (for this factory too) when the fetch succeeds
        // WITHOUT an X-Log-Offset header. Here the screen is empty, so this is
        // PTY_CURSOR_UNKNOWN, and the -1 must match ensureTerminalSession in
        // index.html so the two session factories cannot drift apart.
        s.logCursor = PTY_CURSOR_UNKNOWN;
        // Same rule for the WebSocket cursor (issue #87): ttyOffset counts
        // the ring-buffer bytes rendered on this screen by the TTY
        // transport, and the screen was just cleared. Keeping it would make
        // connectTTY send since=<oldOffset> and paint only the delta onto
        // an empty terminal. Must match the reset in index.html.
        s.ttyOffset = PTY_CURSOR_UNKNOWN;
        if (window.loadLog) {
            window.loadLog(s).finally(() => {
                if (window.connectTTY) window.connectTTY(s);
                if (window.focusTerminalIfPossible) window.focusTerminalIfPossible();
            });
        } else if (window.connectTTY) {
            window.connectTTY(s);
        }
    }

    deactivate() {
        // Hide all PTY hosts when switching away. We keep the
        // session objects alive so re-activating is instant.
        //
        // The WebSocket is deliberately NOT disconnected here.
        // Keeping it live means the terminal buffer stays
        // up-to-date while hidden, so re-activating shows the
        // latest content immediately (matches the pre-renderer
        // behavior on main/develop). Disconnecting on every
        // switch forced a log replay on the way back, and that
        // replay is capped at 64KB per request, so long-running
        // agent TUI sessions rendered stale content.
        this._sessions.forEach(s => {
            if (s.container) s.container.style.display = 'none';
        });
        const ocPanel = document.getElementById('opencode-panel');
        if (ocPanel) ocPanel.hidden = true;
    }

    cleanup() {
        // Called when the instance is deleted. Dispose the xterm
        // session entirely.
        this._sessions.forEach(s => this._destroySession(s));
        this._sessions.clear();
    }

    // --- internals ---

    _createSession(id, termContainer) {
        if (!window.Terminal || !termContainer) return null;
        const host = document.createElement('div');
        host.dataset.instanceId = id;
        host.style.cssText = 'position:absolute; inset:0; display:none;';
        termContainer.appendChild(host);

        const session = {
            id,
            container: host,
            term: null,
            fitAddon: null,
            termDataDisposable: null,
            resizeObserver: null,
            // Nothing painted yet, so CURSOR_UNKNOWN — the same state
            // index.html bootstraps. loadLog replaces it with the real
            // offset from X-Log-Offset, or with CURSOR_FOLLOW_LIVE_END if
            // that header never arrives (issue #86).
            logCursor: PTY_CURSOR_UNKNOWN,
            // WebSocket-path byte cursor (issue #87): tracks the ring-buffer
            // bytes rendered over the live TTY transport so a reconnect can
            // send it back as `since` and resume incrementally instead of
            // re-appending the whole tail. Same states and semantics as
            // ensureTerminalSession in index.html — keep the two session
            // factories in agreement.
            ttyOffset: PTY_CURSOR_UNKNOWN,
            // Status observed by the previous reconcileTerminalSessions()
            // tick; null until the first observation (issue #80).
            lastKnownStatus: null,
            // Outcome of the last loadLog() and its consecutive-failure
            // count, which the reconciler turns into a retry backoff.
            lastLogLoadFailed: false,
            lastLogLoadFailures: 0,
            lastLogLoadAttemptAt: 0,
            ttySocket: null,
            ttyState: 'IDLE',
            appliedTTYSize: null,
            lastResizeTime: 0,
        };

        session.term = new window.Terminal({
            convertEol: true,
            cursorBlink: true,
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            fontSize: 13,
            theme: { background: '#0b1020' },
            scrollback: 10000,
        });

        if (window.FitAddon && window.FitAddon.FitAddon) {
            session.fitAddon = new window.FitAddon.FitAddon();
            session.term.loadAddon(session.fitAddon);
        }

        session.term.open(host);
        if (session.fitAddon) {
            try { session.fitAddon.fit(); } catch (e) { console.error('xterm fit error:', e); }
        }

        session.resizeObserver = new ResizeObserver(entries => {
            if (!session.term) return;
            const entry = entries[0];
            if (entry && (entry.contentRect.width === 0 || entry.contentRect.height === 0)) return;
            if (session.fitAddon) {
                try { session.fitAddon.fit(); } catch (e) { console.error('xterm fit error:', e); }
            } else {
                const w = entry ? entry.contentRect.width : host.clientWidth;
                const h = entry ? entry.contentRect.height : host.clientHeight;
                session.term.resize(Math.floor(w / 9), Math.floor(h / 17));
            }
            const now = Date.now();
            if (now - session.lastResizeTime > 100 && session.term.cols > 0 && session.term.rows > 0) {
                session.lastResizeTime = now;
                if (window.sendResize) window.sendResize(session, session.term.cols, session.term.rows);
                if (window.syncTerminalToAppliedSize) window.syncTerminalToAppliedSize(session);
            }
        });
        session.resizeObserver.observe(host);

        session.termDataDisposable = session.term.onData(data => {
            const inst = window.state && window.state.instances
                ? window.state.instances.find(i => i.id === session.id)
                : null;
            // Only refuse input for a process that is gone. A `starting`
            // instance already accepts keystrokes, and dropping them there was
            // part of the "cannot type" half of issue #80.
            const isTerminal = window.isInstanceTerminalStatus
                ? window.isInstanceTerminalStatus(inst && inst.status)
                : !inst || inst.status === 'stopped' || inst.status === 'failed' || inst.status === 'exited';
            if (inst && isTerminal) return;
            if (window.isTerminalQueryResponse && window.isTerminalQueryResponse(data)) return;

            // Forward raw keystrokes to the daemon. Prefer WS
            // (real-time) when live; fall back to HTTP POST for
            // input buffering.
            if (session.ttySocket && session.ttySocket.readyState === WebSocket.OPEN) {
                session.ttySocket.send(data);
            } else if (window.api) {
                if (!session._inputBuffer) session._inputBuffer = '';
                session._inputBuffer += data;
                if (!session._inputFlushTimer) {
                    session._inputFlushTimer = setTimeout(async () => {
                        const input = session._inputBuffer;
                        session._inputBuffer = '';
                        session._inputFlushTimer = null;
                        try {
                            await window.api('/api/instances/input', {
                                method: 'POST',
                                body: JSON.stringify({ id: session.id, input }),
                            });
                        } catch (e) {
                            if (window.reportSessionError) window.reportSessionError(session, 'input error', e);
                        }
                    }, 40);
                }
            }
        });

        return session;
    }

    _destroySession(s) {
        try {
            if (window.disconnectTTY) window.disconnectTTY(s);
        } catch (_) {}
        if (s.termDataDisposable && typeof s.termDataDisposable.dispose === 'function') {
            try { s.termDataDisposable.dispose(); } catch (_) {}
        }
        if (s.resizeObserver) {
            try { s.resizeObserver.disconnect(); } catch (_) {}
        }
        if (s.term) {
            try { s.term.dispose(); } catch (_) {}
        }
        if (s.container && s.container.parentNode) {
            try { s.container.parentNode.removeChild(s.container); } catch (_) {}
        }
    }

    // Public accessors used by index.html helpers (loadLog /
    // connectTTY etc.) that still live outside the renderer.
    getSession(id) { return this._sessions.get(id); }
    hasSession(id) { return this._sessions.has(id); }

    /**
     * All live pty sessions. The returned array is a fresh copy, but
     * the session objects are renderer-owned and MUST NOT be mutated
     * by callers — this is a read-only view (UI helpers only).
     * @returns {Array<{id: string, container: HTMLElement, term: object, ttyState: string}>}
     */
    allSessions() { return Array.from(this._sessions.values()); }
    destroySession(id) {
        const s = this._sessions.get(id);
        if (s) {
            this._destroySession(s);
            this._sessions.delete(id);
        }
    }
}

window.registerRenderer('pty', new PtyRenderer());

// Expose the singleton for legacy global helpers in index.html
// (loadLog / connectTTY / disconnectTTY) to read/write the same
// session map. They still live in index.html because they depend
// on `state`, `api`, and the WebSocket layer; the renderer is
// the owner of the session lifecycle.
window.__ptyRenderer = (window.KindRenderers || {})['pty'];
