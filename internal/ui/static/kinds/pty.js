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

        if (inst.status !== 'running') {
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

        if (window.resetTerminalForSwitch) window.resetTerminalForSwitch(s);
        s.logCursor = 0;
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
            logCursor: 0,
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
            if (inst && inst.status !== 'running') return;
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
