// OpencodeWebRenderer — manages the iframe + ready-wait polling for
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
//     iframe loads opencode's official web UI from the proxy. The
//     bottom debug bar stays visible showing instance connection info.
//   - On timeout: show "opencode server failed to start within 60s"
//     with the last known status, and stop polling.
//   - When the user switches tabs (deactivate), cancel the poll so
//     it doesn't leak; the next activate() starts a fresh poll.
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
    activate(session, container) {
        const termContainer = document.getElementById('terminal-container');
        const ocPanel = document.getElementById('opencode-panel');
        const ocIframe = document.getElementById('opencode-iframe');
        const ocWarning = document.getElementById('opencode-scope-warning');

        if (termContainer) termContainer.style.display = 'none';
        if (ocPanel) ocPanel.hidden = false;
        if (ocIframe) {
            ocIframe.style.visibility = 'hidden';
            ocIframe.src = 'about:blank';
        }
        if (ocWarning) {
            ocWarning.hidden = true;
            ocWarning.textContent = '';
        }

        this._resetScopeState();
        this._ensureDebugBar(ocPanel);
        this._startPolling(session, ocPanel, ocIframe);
        this._startScopeMonitoring(session, ocPanel, ocWarning);
    }

    deactivate() {
        if (this._abort) this._abort.aborted = true;
        this._stopScopeMonitoring();
        const ocPanel = document.getElementById('opencode-panel');
        const ocIframe = document.getElementById('opencode-iframe');
        if (ocIframe) ocIframe.style.visibility = '';
        const bar = ocPanel && ocPanel.querySelector('.opencode-debug-bar');
        if (bar) bar.hidden = true;
    }

    cleanup() {
        if (this._abort) this._abort.aborted = true;
        this._stopScopeMonitoring();
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

    _ensureDebugBar(ocPanel) {
        if (!ocPanel) return null;
        let bar = ocPanel.querySelector('.opencode-debug-bar');
        if (!bar) {
            bar = document.createElement('div');
            bar.className = 'opencode-debug-bar';
            bar.style.cssText = 'flex:0 0 auto;width:100%;max-height:4.5em;overflow-y:auto;color:var(--text-secondary,#586069);font-size:12px;padding:4px 12px;text-align:left;font-family:monospace;border-top:1px solid var(--border-color,#e1e4e8);background:var(--hover-bg,#f6f8fa);line-height:1.4;white-space:pre-wrap;word-break:break-all;';
            ocPanel.appendChild(bar);
        }
        bar.hidden = false;
        bar.classList.remove('opencode-error');
        return bar;
    }

    _startPolling(session, ocPanel, ocIframe) {
        const bar = this._ensureDebugBar(ocPanel);
        if (!bar) return;

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
                bar.textContent = 'opencode server failed to start within 60s (status: ' + status + (lastError ? ', ' + lastError : '') + '). Use Stop and try again.';
                bar.classList.add('opencode-error');
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
                        bar.classList.remove('opencode-error');
                        bar.style.color = '';
                        bar.textContent = [
                            'instance:  ' + id,
                            'upstream:  http://' + data.host + ':' + data.port,
                            'proxy:     ' + (data.iframe_src || ''),
                            'worktree:  ' + (data.worktree_path || ''),
                            'version:   ' + (data.version || 'unknown') + (data.version && !data.version_supported ? ' (unsupported)' : '')
                        ].join('\n');
                        if (ocIframe) {
                            ocIframe.style.visibility = '';
                            ocIframe.src = data.iframe_src;
                        }
                        return;
                    }
                }
            } catch (e) {
                // network blip; continue polling
            }
            var elapsed = Math.floor((Date.now() - startedAt) / 1000);
            bar.textContent = 'opencode server starting... (' + elapsed + 's)';
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
            ocWarning.textContent = '⚠ opencode web UI 版本过新或结构变化,切换入口隐藏未生效,请升级 myworktree 或使用受支持版本(1.18.x)';
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
