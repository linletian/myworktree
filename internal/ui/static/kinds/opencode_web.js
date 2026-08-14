// OpencodeWebRenderer — manages the iframes + ready-wait polling for
// opencode-web instances.
//
// Behaviour:
//   - When activate() is called, show the iframe panel and hide the
//     PTY terminal container.
//   - Poll /api/instances/opencode?id=... every 500ms up to 60s. The
//     endpoint returns the listening address (host + port) once
//     opencode has finished its 1-3s startup. While port is empty,
//     show a "starting..." placeholder so the user does not see the
//     503 JSON body that the /__opencode/<id>/ proxy returns when
//     not ready.
//   - When port is non-empty, set iframe.src = data.iframe_src. The
//     iframe loads opencode's official web UI from the proxy and fills
//     the whole panel (no debug bar — the page owns its own status UI).
//   - On timeout: show "opencode server failed to start within 60s"
//     with the last known status, and stop polling.
//   - When the user switches tabs (deactivate), cancel the poll so
//     it doesn't leak; the next activate() starts a fresh poll.
//
// Keep-alive across tab switches (mirrors the reasonix web-frame fix):
//   - Each instance gets its own iframe element, cached in a Map
//     keyed by instance id. The iframe is never reset to about:blank
//     and its src is only assigned when the target actually changes
//     (dataset.instance / dataset.src guard, same as ensureWebFrame).
//   - Switching between two running opencode-web instances only
//     hides/shows their dedicated iframes — the loaded page (chat
//     draft, scroll, app state) survives even across different
//     instances, unlike the previous single shared iframe which had
//     to re-navigate (and therefore reload) on every cross-instance
//     switch.
//   - When the instance is stopped, its iframe is released (destroyed):
//     the backend is gone so there is no page state to keep, and this
//     avoids a dead iframe lingering in memory. A later start gets a
//     fresh instance id, so a fresh iframe is built lazily.
//
// Scope monitoring (WORKTREE-ISOLATION.md):
//   - Poll /api/instances/opencode/scope?id=... every ~1.5s for the
//     out-of-scope state the reverse proxy records, and render a
//     persistent warning bar ABOVE the iframe (not inside opencode's
//     DOM) when the instance has navigated away from its worktree.
//   - Listen for the injected hide script's postMessage
//     (mw-oc/hidden-report) and show a "hiding not effective" danger
//     bar when the opencode version/dom is out of the supported range.

class OpencodeWebRenderer {
    constructor() {
        this._frames = new Map(); // instance id → iframe element
        // Points at the iframe of the instance the user is currently
        // viewing. Read (not captured) by the scope message listener to
        // filter which iframe's hidden-report drives the warning.
        // Lifecycle: set at the end of activate(); cleared to null by
        // deactivate(), cleanup(), destroyFrame() (when it removes the
        // current frame) and _pruneStaleFrames() (same condition).
        this._currentFrame = null;
    }

    activate(session, container) {
        const termContainer = document.getElementById('terminal-container');
        const ocPanel = document.getElementById('opencode-panel');
        const ocWarning = document.getElementById('opencode-scope-warning');

        if (termContainer) termContainer.style.display = 'none';
        if (ocPanel) ocPanel.hidden = false;

        // Drop iframes whose instance no longer exists (deleted).
        this._pruneStaleFrames();

        const inst = session.instance;
        if (inst && inst.status === 'stopped') {
            // Stopped instance: the backend is gone and the embedded
            // page is dead — release its iframe (there is no page state
            // worth keeping) so it doesn't linger in the DOM eating
            // memory, and don't poll a dead server. A later start gets a
            // fresh instance id, so a fresh iframe is built lazily.
            this.destroyFrame(session.id);
            if (ocWarning) {
                ocWarning.hidden = true;
                ocWarning.textContent = '';
            }
            this._stopScopeMonitoring();
            this._resetScopeState();
            this._currentFrame = null;
            this._showLoading(ocPanel, {
                title: 'opencode 实例已停止',
                lines: ['instance:  ' + session.id],
            });
            return;
        }

        // Get (or lazily create) the iframe dedicated to THIS instance.
        const frame = this._frameFor(session.id, ocPanel);
        // Visibility is split in two, on purpose:
        //   - _showOnly(frame) turns OFF every OTHER instance's iframe
        //     (it never touches the active frame), so only one frame is
        //     on screen at a time while the rest keep their page state.
        //   - The branch below owns the ACTIVE frame's own hidden state:
        //     already-loaded page → show; first activation → hide until
        //     the poll resolves the port. Don't fold these together, or
        //     _showOnly would end up showing the active frame even when
        //     it hasn't navigated yet.
        this._showOnly(frame);

        if (frame.dataset.instance === session.id && frame.dataset.src) {
            // Already showing this instance's page — keep it alive.
            // Do NOT reset src (no about:blank) and do not navigate:
            // the page (chat draft, scroll, app state) survives the
            // tab switch. The poll below only re-navigates when the
            // resolved src actually changes.
            frame.hidden = false;
            this._hideLoading(ocPanel);
        } else {
            // First activation for this instance: hide until the poll
            // resolves the port, then navigate. Show the loading
            // overlay meanwhile (starting status keeps polling — the
            // instance appears automatically once the server is up).
            frame.hidden = true;
            this._showLoading(ocPanel, {
                lines: ['instance:  ' + session.id],
            });
        }

        if (ocWarning) {
            ocWarning.hidden = true;
            ocWarning.textContent = '';
        }

        // Abort the previous instance's scope poll + message listener
        // before starting fresh ones, so switching instances does not
        // leak timers/listeners pointing at the old instance.
        this._stopScopeMonitoring();
        this._resetScopeState();
        this._currentFrame = frame;
        this._startPolling(session, ocPanel, frame);
        this._startScopeMonitoring(session, ocPanel, ocWarning);
    }

    deactivate() {
        if (this._abort) this._abort.aborted = true;
        this._stopScopeMonitoring();
        // Hide the current iframe when leaving the tab (the page state
        // survives — keep-alive is a visibility toggle, not a teardown).
        // Without this, switching worktrees/instances left the old
        // instance's page visible on an empty panel (cross-instance
        // “串台” while the new instance is still starting).
        if (this._currentFrame) this._currentFrame.hidden = true;
        this._currentFrame = null;
    }

    cleanup() {
        if (this._abort) this._abort.aborted = true;
        this._stopScopeMonitoring();
        if (this._frames) {
            for (const frame of this._frames.values()) {
                if (frame.isConnected) frame.remove();
            }
            this._frames.clear();
        }
        this._currentFrame = null;
    }

    // Remove the cached iframe for one instance (called when the
    // instance is deleted). Kept separate from cleanup() so a single
    // deletion does not tear down other instances' live frames.
    destroyFrame(id) {
        if (!this._frames) return;
        const frame = this._frames.get(id);
        if (frame) {
            if (frame.isConnected) frame.remove();
            this._frames.delete(id);
        }
        if (this._currentFrame === frame) this._currentFrame = null;
    }

    _resetScopeState() {
        this._scopeAbort = null;
        this._onMessage = null;
        this._versionUnsupported = false;
        this._hiddenStatus = 'ok';
        this._scope = 'in-scope';
        this._scopeDir = '';
        this._cspAnchorMissing = false;
    }

    // Loading overlay — shared pattern for web-ui kinds. Shown while the
    // upstream server is starting (or after it stops); info lines show which
    // instance is being waited on and its connection status. Future web-ui
    // kinds can reuse #opencode-loading and these helpers.
    _ensureLoading(ocPanel) {
        if (!ocPanel) return null;
        let el = ocPanel.querySelector('#opencode-loading');
        if (!el) {
            el = document.createElement('div');
            el.id = 'opencode-loading';
            el.innerHTML =
                '<div class="opencode-loading-spinner"></div>' +
                '<div class="opencode-loading-title">正在启动 opencode server…</div>' +
                '<div class="opencode-loading-info"></div>';
            const frames = ocPanel.querySelector('#opencode-frames');
            if (frames) ocPanel.insertBefore(el, frames);
            else ocPanel.appendChild(el);
        }
        return el;
    }

    _showLoading(ocPanel, info) {
        const el = this._ensureLoading(ocPanel);
        if (!el) return;
        if (info) {
            if (info.title) el.querySelector('.opencode-loading-title').textContent = info.title;
            const infoEl = el.querySelector('.opencode-loading-info');
            if (infoEl) infoEl.textContent = info.lines ? info.lines.join('\n') : '';
        }
        el.hidden = false;
    }

    _hideLoading(ocPanel) {
        const el = ocPanel && ocPanel.querySelector('#opencode-loading');
        if (el) el.hidden = true;
    }

    // Get or create the iframe dedicated to this instance. Each
    // instance keeps its own element so cross-instance switches are
    // pure show/hide (no src reassignment, no reload).
    _frameFor(id, ocPanel) {
        if (!this._frames) this._frames = new Map();
        let frame = this._frames.get(id);
        if (!frame || !frame.isConnected) {
            frame = document.createElement('iframe');
            frame.className = 'opencode-frame';
            frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms allow-popups');
            frame.dataset.instance = '';
            frame.dataset.src = '';
            frame.hidden = true;
            const host = ocPanel && ocPanel.querySelector('#opencode-frames');
            (host || ocPanel).appendChild(frame);
            this._frames.set(id, frame);
        }
        return frame;
    }

    // Hide every cached iframe except the active one (and drop any that
    // were detached from the DOM). The active frame's visibility is
    // decided by the caller.
    _showOnly(active) {
        if (!this._frames) return;
        for (const [id, frame] of this._frames) {
            if (!frame.isConnected) {
                this._frames.delete(id);
                continue;
            }
            if (frame !== active) frame.hidden = true;
        }
    }

    // Drop cached iframes whose instance is no longer present in state
    // (e.g. after the user deletes an opencode-web instance). Runs on every
    // activate(); it is O(cached frames × state.instances) but both are tiny
    // (a handful of instances), so the full scan is fine. If instance counts
    // ever grow into the hundreds, switch to a dirty-flag instead.
    _pruneStaleFrames() {
        if (!this._frames) return;
        const instances = (window.state && window.state.instances) || [];
        const live = new Set(instances.filter(i => i.kind === 'opencode-web').map(i => i.id));
        const activeId = window.state && window.state.activeInst;
        for (const [id, frame] of this._frames) {
            // Never drop the currently-active instance's frame, even if a
            // transient state update makes it look absent — dropping it
            // would force the next activate() to rebuild the iframe and
            // reload a running instance.
            const keep = frame.isConnected && (live.has(id) || id === activeId);
            if (!keep) {
                if (frame.isConnected) frame.remove();
                this._frames.delete(id);
                if (this._currentFrame === frame) this._currentFrame = null;
            }
        }
    }

    _startPolling(session, ocPanel, frame) {
        if (this._abort) this._abort.aborted = true;
        const abort = { aborted: false };
        this._abort = abort;

        const id = session.id;
        const startedAt = Date.now();
        const TIMEOUT_MS = 60000;
        const POLL_MS = 500;

        const tick = async () => {
            if (abort.aborted) return;
            if (Date.now() - startedAt > TIMEOUT_MS) {
                const inst = window.state && window.state.instances
                    ? window.state.instances.find(i => i.id === id)
                    : null;
                const status = inst ? inst.status : 'unknown';
                const lastError = inst ? (inst.last_error || '') : '';
                this._showLoading(ocPanel, {
                    title: '启动超时 (60s)',
                    lines: ['instance:  ' + id, 'status:    ' + status + (lastError ? ' — ' + lastError : '')],
                });
                return;
            }
            try {
                const resp = await fetch('/api/instances/opencode?id=' + encodeURIComponent(id));
                if (abort.aborted) return;
                if (resp.ok) {
                    const data = await resp.json();
                    if (data.port) {
                        // Record version support (advisory): only warn when a
                        // version was actually probed and is out of range.
                        this._versionUnsupported = !!(data.version && !data.version_supported);
                        this._refreshWarning(document.getElementById('opencode-scope-warning'));
                        if (frame) {
                            // Only navigate when the target changes
                            // (same guard as reasonix ensureWebFrame):
                            // assigning the same src would reload the
                            // embedded page and lose its state on every
                            // tab switch. The dataset persists across
                            // deactivate()/activate() cycles, so switching
                            // back to a running instance keeps the loaded
                            // page alive.
                            const src = data.iframe_src;
                            if (frame.dataset.instance !== id || frame.dataset.src !== src) {
                                frame.dataset.instance = id;
                                frame.dataset.src = src;
                                frame.src = src;
                            }
                            frame.hidden = false;
                        }
                        this._hideLoading(ocPanel);
                        return;
                    }
                }
            } catch (e) {
                // network blip; continue polling
            }
            var elapsed = Math.floor((Date.now() - startedAt) / 1000);
            this._showLoading(ocPanel, {
                lines: ['instance:  ' + id, 'elapsed:   ' + elapsed + 's'],
            });
            setTimeout(tick, POLL_MS);
        };
        tick();
    }

    _startScopeMonitoring(session, ocPanel, ocWarning) {
        // Hidden-report from the injected hide script (iframe → parent).
        this._onMessage = (event) => {
            const d = event && event.data;
            if (!d || d.type !== 'mw-oc/hidden-report') return;
            // Only trust same-origin senders (the iframe is same-origin);
            // ignore forged messages from other tabs / third-party iframes.
            if (event.origin !== window.location.origin) return;
            // Ignore reports from OTHER instances' iframes — they are all
            // same-origin, so only the currently-active frame's page state
            // should drive the warning. _currentFrame is read at event time
            // (not captured), so it always points at the instance the user
            // is looking at.
            if (!this._currentFrame || event.source !== this._currentFrame.contentWindow) return;
            this._hiddenStatus = d.status || 'ok';
            this._refreshWarning(ocWarning);
        };
        window.addEventListener('message', this._onMessage);

        // Poll the reverse proxy's out-of-scope state.
        const abort = { aborted: false };
        this._scopeAbort = abort;
        const id = session.id;
        const tick = async () => {
            if (abort.aborted) return;
            try {
                const resp = await fetch('/api/instances/opencode/scope?id=' + encodeURIComponent(id));
                if (abort.aborted) return;
                if (resp.ok) {
                    const data = await resp.json();
                    this._scope = data.scope || 'in-scope';
                    this._scopeDir = data.directory || '';
                    this._cspAnchorMissing = !!data.csp_anchor_missing;
                    this._refreshWarning(ocWarning);
                }
            } catch (e) {
                // network blip; keep last state
            }
            setTimeout(tick, 1500);
        };
        tick();
    }

    _stopScopeMonitoring() {
        if (this._scopeAbort) this._scopeAbort.aborted = true;
        this._scopeAbort = null;
        if (this._onMessage) {
            window.removeEventListener('message', this._onMessage);
            this._onMessage = null;
        }
    }

    _refreshWarning(ocWarning) {
        if (!ocWarning) return;
        // 1. Hiding not effective (version/dom/CSP out of range) — highest priority.
        if (this._versionUnsupported || this._cspAnchorMissing || (this._hiddenStatus && this._hiddenStatus !== 'ok')) {
            ocWarning.hidden = false;
            ocWarning.classList.add('opencode-warning-danger');
            ocWarning.textContent = '⚠ opencode web UI 版本过新或结构变化,切换入口禁用未生效,请升级 myworktree 或使用受支持版本(1.18.x)';
            return;
        }
        // 2. Out of scope (proxy observed a directory != worktree).
        if (this._scope === 'out-of-scope' || this._scope === 'cross-project') {
            ocWarning.hidden = false;
            ocWarning.classList.remove('opencode-warning-danger');
            ocWarning.textContent = '⚠ opencode 已离开 worktree 范围' + (this._scopeDir ? ': ' + this._scopeDir : '');
            return;
        }
        // 3. Normal.
        ocWarning.hidden = true;
        ocWarning.textContent = '';
    }
}

window.registerRenderer('opencode-web', new OpencodeWebRenderer());
