// DshWebRenderer — manages the iframes + ready-wait polling for
// dsh-web instances (the DeepSeek Harness web UI, embedded via a
// per-instance dedicated origin — PLAN.md §嵌入形态).
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
//     the request is never blocked, PLAN.md §工作区限制).
//   - Advisory warnings: version_supported=false (dsh outside the
//     supported range) or overlay_verified=false (the restrict overlay
//     rows were not confirmed by the spawn-time --dump-config check)
//     surface the "隔离可能未生效" danger bar.
//   - Missing-dependency dialog: when the instance is failed and the
//     info endpoint reports missing_dsh, a dialog offers three options:
//     npx launch / install now / cancel (PLAN.md §缺失依赖).
//   - Remote mode: when this page is served remotely (non-loopback
//     host) the iframe src gets ?token= appended (read via the same
//     cookie → address-bar pattern as framework.js window.api) so the
//     proxy's mandatory token gate authenticates the first navigation;
//     the proxy then syncs the HttpOnly cookie on its own origin and
//     302-redirects to a token-free URL, so the embedded document never
//     keeps the token in its location.search. Locally the iframe is
//     never token-bearing (loopback clients bypass the gate).

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
                title: 'dsh 实例已停止',
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
                '<div class="dsh-loading-title">正在启动 dsh server…</div>' +
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
                    title: '启动超时 (60s)',
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
                        this._versionUnsupported = !!(data.version && !data.version_supported);
                        // overlay_verified is only meaningful once the
                        // blob carried a version (pre-ready polls return
                        // the zero value).
                        this._overlayIneffective = !!(data.version && data.overlay_verified === false);
                        this._refreshWarning(document.getElementById('dsh-scope-warning'));
                        if (frame) {
                            const src = this._tokenizeIframeSrc(data.iframe_src);
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

    // _tokenizeIframeSrc appends ?token= only when THIS page is served
    // remotely (non-loopback): the proxy's token gate is mandatory
    // there, and the token is read with the same cookie → address-bar
    // fallback as framework.js window.api. The proxy validates the
    // first navigation, syncs the HttpOnly cookie, and 302-redirects
    // to a token-free URL, so the embedded document never KEEPS the
    // credential in its location.search. Locally the iframe stays
    // token-free (loopback clients bypass the gate).
    _tokenizeIframeSrc(src) {
        const host = window.location.hostname;
        if (!(host === 'localhost' || host === '127.0.0.1' || host === '::1')) {
            let token = '';
            if (document.cookie) {
                const m = document.cookie.match(/(?:^|;\s*)mw_token=([^;]*)/);
                if (m) token = m[1];
            }
            if (!token) {
                const p = new URLSearchParams(window.location.search);
                token = p.get('token') || '';
            }
            if (token) {
                const sep = src.indexOf('?') === -1 ? '?' : '&';
                return src + sep + 'token=' + encodeURIComponent(token);
            }
        }
        return src;
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
    }

    _refreshWarning(dshWarning) {
        if (!dshWarning) return;
        // 1. Restriction effectiveness (version / overlay) — highest priority.
        if (this._versionUnsupported || this._overlayIneffective) {
            dshWarning.hidden = false;
            dshWarning.classList.add('dsh-warning-danger');
            dshWarning.textContent = '⚠ dsh 版本过新或 restrict overlay 未生效，跨 worktree 入口可能未被禁用，请升级 myworktree 或使用受支持版本 (0.1.x)';
            return;
        }
        // 2. Out of scope (the proxy observed a workspace/session RPC
        // targeting a directory != worktree).
        if (this._scope === 'out-of-scope') {
            dshWarning.hidden = false;
            dshWarning.classList.remove('dsh-warning-danger');
            dshWarning.textContent = '⚠ dsh 已离开 worktree 范围' + (this._scopeDir ? ': ' + this._scopeDir : '');
            return;
        }
        // 3. Normal.
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
        document.getElementById('dsh-missing-title').textContent = 'dsh 未安装';
        document.getElementById('dsh-missing-body').textContent =
            '未在 PATH 中找到 dsh 可执行文件。可选用以下方式启动此实例（建议 pin: ' +
            (missing.suggested_pin || '') + '）：';
        const btnInstall = document.getElementById('dsh-missing-install');
        if (btnInstall) {
            btnInstall.style.display = missing.npm_available ? '' : 'none';
            btnInstall.textContent = '立即安装 (npm install -g)';
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
                    if (btn) btn.textContent = '确认安装？将执行 npm install -g，修改全局 npm prefix（需写权限，约 30s）';
                    return;
                }
                this._installConfirmFor = null;
                this._dismissedMissingFor = null;
                setBusy(true, '正在执行 npm install -g @deepseek-ai/dsh …');
                const res = await api('/api/instances/dsh/install', {
                    method: 'POST',
                    body: JSON.stringify({ id }),
                });
                if (res && res.resolved_bin) {
                    setBusy(true, '已安装: ' + res.resolved_bin + ' — 正在重启实例…');
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
                errorEl.textContent = (e && (e.message || e)) || '操作失败';
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
