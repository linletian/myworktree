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

class OpencodeWebRenderer {
    activate(session, container) {
        const termContainer = document.getElementById('terminal-container');
        const ocPanel = document.getElementById('opencode-panel');
        const ocIframe = document.getElementById('opencode-iframe');

        if (termContainer) termContainer.style.display = 'none';
        if (ocPanel) ocPanel.hidden = false;
        if (ocIframe) {
            ocIframe.style.visibility = 'hidden';
            ocIframe.src = 'about:blank';
        }

        this._ensureDebugBar(ocPanel);
        this._startPolling(session, ocPanel, ocIframe);
    }

    deactivate() {
        if (this._abort) this._abort.aborted = true;
        const ocPanel = document.getElementById('opencode-panel');
        const ocIframe = document.getElementById('opencode-iframe');
        if (ocIframe) ocIframe.style.visibility = '';
        const bar = ocPanel && ocPanel.querySelector('.opencode-debug-bar');
        if (bar) bar.hidden = true;
    }

    cleanup() {
        if (this._abort) this._abort.aborted = true;
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
                        bar.classList.remove('opencode-error');
                        bar.style.color = '';
                        bar.textContent = [
                            'instance:  ' + id,
                            'upstream:  http://' + data.host + ':' + data.port,
                            'proxy:     ' + (data.iframe_src || ''),
                            'worktree:  ' + (data.worktree_path || '')
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
}

window.registerRenderer('opencode-web', new OpencodeWebRenderer());
