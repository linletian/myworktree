// DshWebRenderer — manages the iframes + ready-wait polling for
// dsh-web instances (the DeepSeek Harness web UI, embedded via a
// per-instance dedicated origin — PLAN.md "embed form" section).
//
// Behaviour (mirrors OpencodeWebRenderer):
//   - activate() shows #dsh-panel and hides the PTY terminal container.
//   - Polls /api/instances/dsh?id=... every 500ms up to 60s for the
//     iframe src (the per-instance reverse-proxy origin). While empty,
//     a "starting…" placeholder covers the panel.
//   - Each instance keeps its own iframe (per-instance keep-alive:
//     switching tabs only hides/shows frames — the loaded page state
//     survives). Stopped instances release their frame.
//   - Scope monitoring: polls /api/instances/dsh/scope?id=... every
//     ~1.5s and renders a persistent warning bar ABOVE the iframe when
//     the instance has navigated away from its worktree (record-only —
//     the request is never blocked, PLAN.md "workspace restriction"
//     section). The same
//     response carries foreign_active_sessions (shared-pool sessions
//     actively written by another dsh process — opening them can
//     corrupt their log, dsh single-writer boundary) and the bar warns
//     about those too. Priority: remote-unavailable > remote-auth-missing
//     > restriction effectiveness (version/overlay) > foreign active
//     sessions > out-of-scope.
//   - Remote access (issue #72): the SPA syncs its session/workspace
//     lists over WebSocket, which fails on remote networks, so a remote
//     page gets an English warning bar ABOVE the iframe and the whole
//     frame area is masked (gray overlay, _updateRemoteMask) — the embed
//     is unusable remotely and the mask makes that explicit.
//   - Advisory warnings: version_supported=false (dsh outside the
//     supported range) or overlay_verified=false (the restrict overlay
//     rows were not confirmed by the spawn-time --dump-config check)
//     surface the "isolation may not be effective" danger bar.
//   - Missing-dependency dialog: when the instance is failed and the
//     info endpoint reports missing_dsh, a dialog offers three options:
//     npx launch / install now / cancel (PLAN.md "missing dependency"
//     section).
//   - Remote mode: the mw_token cookie is HttpOnly (page JS can never
//     read it) and portal/login flows carry no ?token= in the address
//     bar, so the SERVER appends ?token= to iframe_src itself when the
//     proxy's token gate is on (handleInstanceDshInfo) — the embed is a
//     dedicated origin and its first navigation must carry the token.
//     The proxy validates it, syncs the HttpOnly cookie on its own
//     origin, and 302-redirects to a token-free URL, so the embedded
//     document never keeps the token in its location.search. The
//     renderer only detects a server-tokenized src (no double-append),
//     falls back to an address-bar token, and warns when a remote page
//     ends up with a token-less src. Locally the iframe is never
//     token-bearing (loopback clients bypass the gate).

class DshWebRenderer {
    constructor() {
        this._frames = new Map(); // instance id → iframe element
        this._currentFrame = null;
    }

    activate(session, container) {
        const termContainer = document.getElementById('terminal-container');
        const dshPanel = document.getElementById('dsh-panel');
        const dshWarning = document.getElementById('dsh-scope-warning');

        if (termContainer) termContainer.style.display = 'none';
        if (dshPanel) dshPanel.hidden = false;

        this._pruneStaleFrames();
        this._updateRemoteMask(dshPanel);

        const inst = session.instance;
        if (inst && inst.status === 'stopped') {
            this.destroyFrame(session.id);
            if (dshWarning) {
                dshWarning.hidden = true;
                dshWarning.textContent = '';
            }
            this._stopMonitoring();
            this._currentFrame = null;
            this._showLoading(dshPanel, {
                title: 'dsh instance stopped',
                lines: ['instance:  ' + session.id],
            });
            return;
        }

        const frame = this._frameFor(session.id, dshPanel);
        this._showOnly(frame);

        if (frame.dataset.instance === session.id && frame.dataset.src) {
            frame.hidden = false;
            this._hideLoading(dshPanel);
        } else {
            frame.hidden = true;
            this._showLoading(dshPanel, {
                lines: ['instance:  ' + session.id],
            });
        }

        if (dshWarning) {
            dshWarning.hidden = true;
            dshWarning.textContent = '';
        }

        this._stopMonitoring();
        this._currentFrame = frame;
        this._startPolling(session, dshPanel, frame);
        this._startScopeMonitoring(session, dshPanel, dshWarning);
    }

    deactivate() {
        if (this._abort) this._abort.aborted = true;
        this._stopMonitoring();
        if (this._currentFrame) this._currentFrame.hidden = true;
        this._currentFrame = null;
        const dshPanel = document.getElementById('dsh-panel');
        if (dshPanel) dshPanel.hidden = true;
    }

    cleanup() {
        if (this._abort) this._abort.aborted = true;
        this._stopMonitoring();
        if (this._frames) {
            for (const frame of this._frames.values()) {
                if (frame.isConnected) frame.remove();
            }
            this._frames.clear();
        }
        this._currentFrame = null;
    }

    destroyFrame(id) {
        if (!this._frames) return;
        const frame = this._frames.get(id);
        if (frame) {
            if (frame.isConnected) frame.remove();
            this._frames.delete(id);
        }
        if (this._currentFrame === frame) this._currentFrame = null;
    }

    // ---- loading overlay (same pattern as opencode-web's) ----

    _ensureLoading(dshPanel) {
        if (!dshPanel) return null;
        let el = dshPanel.querySelector('#dsh-loading');
        if (!el) {
            el = document.createElement('div');
            el.id = 'dsh-loading';
            el.innerHTML =
                '<div class="dsh-loading-spinner"></div>' +
                '<div class="dsh-loading-title">Starting dsh server…</div>' +
                '<div class="dsh-loading-info"></div>';
            const frames = dshPanel.querySelector('#dsh-frames');
            if (frames) dshPanel.insertBefore(el, frames);
            else dshPanel.appendChild(el);
        }
        return el;
    }

    _showLoading(dshPanel, info) {
        const el = this._ensureLoading(dshPanel);
        if (!el) return;
        if (info) {
            if (info.title) el.querySelector('.dsh-loading-title').textContent = info.title;
            const infoEl = el.querySelector('.dsh-loading-info');
            if (infoEl) infoEl.textContent = info.lines ? info.lines.join('\n') : '';
        }
        el.hidden = false;
    }

    _hideLoading(dshPanel) {
        const el = dshPanel && dshPanel.querySelector('#dsh-loading');
        if (el) el.hidden = true;
    }

    _frameFor(id, dshPanel) {
        if (!this._frames) this._frames = new Map();
        let frame = this._frames.get(id);
        if (!frame || !frame.isConnected) {
            frame = document.createElement('iframe');
            frame.className = 'dsh-frame';
            frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms allow-popups');
            frame.dataset.instance = '';
            frame.dataset.src = '';
            frame.hidden = true;
            const host = dshPanel && dshPanel.querySelector('#dsh-frames');
            (host || dshPanel).appendChild(frame);
            this._frames.set(id, frame);
        }
        return frame;
    }

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

    _pruneStaleFrames() {
        if (!this._frames) return;
        const instances = (window.state && window.state.instances) || [];
        const live = new Set(instances.filter(i => i.kind === 'dsh-web').map(i => i.id));
        const activeId = window.state && window.state.activeInst;
        for (const [id, frame] of this._frames) {
            const keep = frame.isConnected && (live.has(id) || id === activeId);
            if (!keep) {
                if (frame.isConnected) frame.remove();
                this._frames.delete(id);
                if (this._currentFrame === frame) this._currentFrame = null;
            }
        }
    }

    // ---- ready polling + info ----

    _startPolling(session, dshPanel, frame) {
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
                this._showLoading(dshPanel, {
                    title: 'Startup timed out (60s)',
                    lines: ['instance:  ' + id, 'status:    ' + status + (lastError ? ' — ' + lastError : '')],
                });
                return;
            }
            try {
                const resp = await fetch('/api/instances/dsh?id=' + encodeURIComponent(id));
                if (abort.aborted) return;
                if (resp.ok) {
                    const data = await resp.json();
                    if (data.iframe_src) {
                        // The instance recovered (restart / install):
                        // drop any dismissed-missing state and close the
                        // dialog if it is still open.
                        this._dismissedMissingFor = null;
                        this._closeMissingDialog();
                        if (frame) {
                            const src = this._tokenizeIframeSrc(data.iframe_src);
                            this._versionUnsupported = !!(data.version && !data.version_supported);
                            // overlay_verified is only meaningful once the
                            // blob carried a version (pre-ready polls return
                            // the zero value).
                            this._overlayIneffective = !!(data.version && data.overlay_verified === false);
                            this._refreshWarning(document.getElementById('dsh-scope-warning'));
                            if (frame.dataset.instance !== id || frame.dataset.src !== src) {
                                frame.dataset.instance = id;
                                frame.dataset.src = src;
                                frame.src = src;
                            }
                            frame.hidden = false;
                        }
                        this._hideLoading(dshPanel);
                        return;
                    }
                    // Failed instance with a structured missing-dsh
                    // state → the three-way dialog (npx / install /
                    // cancel). Polling keeps running so an install or
                    // PATH fix clears the dialog automatically; a
                    // cancel only suppresses the dialog (never stops
                    // the poll) so it can re-appear after a manual
                    // restart that still fails.
                    if (data.missing_dsh) {
                        this._showMissingDialog(id, data.missing_dsh);
                    }
                }
            } catch (e) {
                // network blip; continue polling
            }
            const elapsed = Math.floor((Date.now() - startedAt) / 1000);
            this._showLoading(dshPanel, {
                lines: ['instance:  ' + id, 'elapsed:   ' + elapsed + 's'],
            });
            setTimeout(tick, POLL_MS);
        };
        tick();
    }

    // _tokenizeIframeSrc: iframe URL token handling for remote mode.
    // The mw_token cookie is HttpOnly — JavaScript can never read it,
    // and portal/login flows have no ?token= in the address bar, so the
    // SERVER appends ?token= to iframe_src itself when the proxy's
    // token gate is on (handleInstanceDshInfo). Here we only:
    //   - accept a server-tokenized src (no warning, no double-append);
    //   - fall back to the address-bar token for a src that somehow
    //     lacks one (belt and braces);
    //   - warn when a remote page ends up with a token-less src (the
    //     embed will 401).
    // Locally the iframe stays token-free (loopback clients bypass the
    // gate). The proxy validates the first navigation, syncs the
    // HttpOnly cookie on its own origin, and 302-redirects to a
    // token-free URL, so the embedded document never KEEPS the
    // credential in its location.search.
    _tokenizeIframeSrc(src) {
        // Remote detection: window.isRemoteAccess() (framework.js,
        // single source of truth; loopback IPv4/IPv6 literals included).
        if (window.isRemoteAccess()) {
            if (src.indexOf('token=') !== -1) {
                this._remoteAuthMissing = false;
                return src;
            }
            const p = new URLSearchParams(window.location.search);
            const token = p.get('token') || '';
            if (token) {
                this._remoteAuthMissing = false;
                const sep = src.indexOf('?') === -1 ? '?' : '&';
                return src + sep + 'token=' + encodeURIComponent(token);
            }
            // Remote page with a token-less src and no address-bar
            // token: the proxy's mandatory token gate will 401 the
            // first iframe navigation. Keep the frame hidden behind
            // the loading placeholder and surface a hint
            // (REVIEW-2026-08-15.md #13).
            this._remoteAuthMissing = true;
            return src;
        }
        this._remoteAuthMissing = false;
        return src;
    }

    // ---- remote-access mask ----

    _remoteUnavailableMessage() {
        return 'Remote access is not available for dsh-web instances: ' +
            'session and workspace sync cannot work over remote networks. ' +
            'Please use it from the local 127.0.0.1 environment.';
    }

    // Issue #72: remote pages cannot sync the SPA's session/workspace
    // lists (its WebSocket connection fails on remote networks), so the
    // whole frame area is covered with a gray mask that also blocks
    // interaction. Local pages never see the mask.
    _updateRemoteMask(dshPanel) {
        const frames = dshPanel && dshPanel.querySelector('#dsh-frames');
        if (!frames) return;
        let mask = frames.querySelector('#dsh-remote-mask');
        if (window.isRemoteAccess()) {
            if (!mask) {
                mask = document.createElement('div');
                mask.id = 'dsh-remote-mask';
                const card = document.createElement('div');
                card.className = 'dsh-remote-mask-card';
                card.textContent = '⚠ ' + this._remoteUnavailableMessage();
                mask.appendChild(card);
                frames.appendChild(mask);
            }
            mask.hidden = false;
        } else if (mask) {
            mask.hidden = true;
        }
    }

    // ---- scope monitoring + warning bar ----

    _startScopeMonitoring(session, dshPanel, dshWarning) {
        const abort = { aborted: false };
        this._scopeAbort = abort;
        const id = session.id;
        const tick = async () => {
            if (abort.aborted) return;
            try {
                const resp = await fetch('/api/instances/dsh/scope?id=' + encodeURIComponent(id));
                if (abort.aborted) return;
                if (resp.ok) {
                    const data = await resp.json();
                    this._scope = data.scope || 'in-scope';
                    this._scopeDir = data.directory || '';
                    this._foreignSessions = data.foreign_active_sessions || [];
                    this._refreshWarning(dshWarning);
                }
            } catch (e) {
                // network blip; keep last state
            }
            setTimeout(tick, 1500);
        };
        tick();
    }

    _stopMonitoring() {
        if (this._scopeAbort) this._scopeAbort.aborted = true;
        this._scopeAbort = null;
        this._versionUnsupported = false;
        this._overlayIneffective = false;
        this._scope = 'in-scope';
        this._scopeDir = '';
        this._remoteAuthMissing = false;
        this._foreignSessions = [];
    }

    _refreshWarning(dshWarning) {
        if (!dshWarning) return;
        // 0. Remote access (issue #72) — the SPA cannot sync its
        //    session/workspace lists over remote networks (WebSocket
        //    upgrade dies), so the embed is unusable; the frame area is
        //    masked (see _updateRemoteMask). Highest priority: every
        //    other warning is moot while this one applies.
        if (window.isRemoteAccess()) {
            dshWarning.hidden = false;
            dshWarning.classList.add('dsh-warning-danger');
            dshWarning.textContent = '⚠ ' + this._remoteUnavailableMessage();
            return;
        }
        // 1. Remote auth missing — the iframe cannot load at all
        //    (highest priority; the page will never render).
        if (this._remoteAuthMissing) {
            dshWarning.hidden = false;
            dshWarning.classList.add('dsh-warning-danger');
            dshWarning.textContent = '⚠ Remote access requires authentication: this page has no usable token, so the dsh panel cannot load. Open it via a ?token= URL (or log in on the main page first), then refresh this page';
            return;
        }
        // 2. Restriction effectiveness (version / overlay).
        if (this._versionUnsupported || this._overlayIneffective) {
            dshWarning.hidden = false;
            dshWarning.classList.add('dsh-warning-danger');
            dshWarning.textContent = '⚠ dsh version is too new or the restrict overlay is not effective — cross-worktree entries may not be disabled. Upgrade myworktree or use a supported version (0.1.x)';
            return;
        }
        // 3. Foreign-process active sessions (dsh upstream single-writer
        //    boundary): opening them in the embed can corrupt the log.
        if (this._foreignSessions && this._foreignSessions.length > 0) {
            dshWarning.hidden = false;
            dshWarning.classList.remove('dsh-warning-danger');
            dshWarning.textContent = '⚠ Detected ' + this._foreignSessions.length +
                ' dsh session(s) being actively written by other processes; opening them in the embedded UI may corrupt their logs (dsh upstream limitation). Wait until the sessions are idle before opening them';
            return;
        }
        // 4. Out of scope (the proxy observed a workspace/session RPC
        // targeting a directory != worktree).
        if (this._scope === 'out-of-scope') {
            dshWarning.hidden = false;
            dshWarning.classList.remove('dsh-warning-danger');
            dshWarning.textContent = '⚠ dsh has left the worktree scope' + (this._scopeDir ? ': ' + this._scopeDir : '');
            return;
        }
        // 5. Normal.
        dshWarning.hidden = true;
        dshWarning.textContent = '';
    }

    // ---- missing-dependency dialog ----

    _showMissingDialog(id, missing) {
        if (this._dialogShownFor === id) return; // already open for this instance
        if (this._dismissedMissingFor === id) return; // user cancelled; stay quiet until a restart
        this._dialogShownFor = id;
        this._installConfirmFor = null;
        const dlg = document.getElementById('modal-dsh-missing');
        if (!dlg) return;
        document.getElementById('dsh-missing-title').textContent = 'dsh is not installed';
        document.getElementById('dsh-missing-body').textContent =
            'No dsh executable found in PATH. Choose how to start this instance (suggested pin: ' +
            (missing.suggested_pin || '') + '):';
        const btnInstall = document.getElementById('dsh-missing-install');
        if (btnInstall) {
            btnInstall.style.display = missing.npm_available ? '' : 'none';
            btnInstall.textContent = 'Install now (npm install -g)';
        }
        document.getElementById('dsh-missing-progress').hidden = true;
        document.getElementById('dsh-missing-error').hidden = true;
        dlg.showModal();
    }

    _closeMissingDialog() {
        this._dialogShownFor = null;
        const dlg = document.getElementById('modal-dsh-missing');
        if (dlg && dlg.open) dlg.close();
    }

    async _chooseMissingAction(id, action) {
        const dlg = document.getElementById('modal-dsh-missing');
        const progress = document.getElementById('dsh-missing-progress');
        const errorEl = document.getElementById('dsh-missing-error');
        const setBusy = (busy, text) => {
            const buttons = dlg.querySelectorAll('button');
            for (const b of buttons) b.disabled = busy;
            if (progress) {
                progress.hidden = !busy;
                if (text) progress.textContent = text;
            }
            if (errorEl) errorEl.hidden = true;
        };
        try {
            if (action === 'npx') {
                this._dismissedMissingFor = null;
                await api('/api/instances/dsh/launch', {
                    method: 'POST',
                    body: JSON.stringify({ id, mode: 'npx' }),
                });
            } else if (action === 'install') {
                // Two-step confirm: the install rewrites the global npm
                // prefix (may touch every user's environment when the
                // daemon runs as root) — make the user click twice.
                if (this._installConfirmFor !== id) {
                    this._installConfirmFor = id;
                    const btn = document.getElementById('dsh-missing-install');
                    if (btn) btn.textContent = 'Confirm install? This runs npm install -g and modifies the global npm prefix (needs write permission, ~30s)';
                    return;
                }
                this._installConfirmFor = null;
                this._dismissedMissingFor = null;
                setBusy(true, 'Running npm install -g @deepseek-ai/dsh …');
                const res = await api('/api/instances/dsh/install', {
                    method: 'POST',
                    body: JSON.stringify({ id }),
                });
                if (res && res.resolved_bin) {
                    setBusy(true, 'Installed: ' + res.resolved_bin + ' — restarting the instance…');
                }
            } else {
                // Cancel: keep the instance failed (per the dialog
                // contract) but do not nag — suppress the dialog until
                // a restart yields a new outcome. Polling continues.
                this._dismissedMissingFor = id;
                this._closeMissingDialog();
                return;
            }
            // Restart the failed instance with the new launch mode.
            await api('/api/instances/restart', {
                method: 'POST',
                body: JSON.stringify({ id }),
            });
            this._closeMissingDialog();
            await refresh();
            selectInstance(id);
        } catch (e) {
            setBusy(false);
            if (errorEl) {
                errorEl.hidden = false;
                errorEl.textContent = (e && (e.message || e)) || 'Operation failed';
            }
        }
    }
}

const dshRenderer = new DshWebRenderer();
window.registerRenderer('dsh-web', dshRenderer);

// Dialog bridge for the modal's inline onclick handlers (✕ and the
// cancel button). Both behave as a dismiss: the dialog stays closed
// while the background polling keeps watching for a recovery.
window.dshWebCloseMissingDialog = () => {
    dshRenderer._dismissedMissingFor = dshRenderer._dialogShownFor;
    dshRenderer._closeMissingDialog();
};
window.dshWebChooseMissingAction = (action) => dshRenderer._chooseMissingAction(dshRenderer._dialogShownFor || '', action);
