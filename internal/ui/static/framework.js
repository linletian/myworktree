// framework.js — worktree-aware instance manager UI shell.
//
// Owns:
//   - global application state (worktrees, instances, tags, active
//     worktree + instance, tab order, diverged data)
//   - API helper (csrf / auth token injection)
//   - refresh() loop that polls /api/worktrees + /api/instances
//   - selectWorktree(id) and selectInstance(id) flow
//   - render() dispatch that calls the legacy render hooks
//     (renderSidebar / renderTabs / renderWorkspace / renderModals)
//     that remain in index.html for now
//   - openModal / closeModal modal lifecycle
//   - init() bootstrap that wires everything together on DOM ready
//
// Does NOT own:
//   - kind-specific renderers (see kinds/pty.js, kinds/opencode_web.js)
//   - the Start Instance modal form fields (those live in index.html
//     alongside the legacy render helpers)
//   - xterm.js / WebSocket plumbing
//   - iframe proxy URL construction

(function () {

// ---- application state ------------------------------------------------

window.state = {
    worktrees: [],
    instances: [],
    tags: [],
    branches: [],
    defaultBranch: "",
    activeWT: null,
    activeInst: null,
    tabOrder: {},
    diverged: null,
    refreshSeq: 0,
    renamingInst: null,
    version: 0,
};

// ---- shared helpers ---------------------------------------------------

// Single source of truth for remote-access detection (issue #72).
// Loopback hostnames (IPv4/IPv6 literals included) are local; anything
// else counts as remote. window.location.hostname keeps the brackets
// for IPv6 literals, so both "::1" and "[::1]" are accepted. Used by
// index.html (Remote badge, local-only shortcuts, LLM settings, the
// dsh-web Start gate) and kinds/dsh_web.js (iframe tokenization).
window.isRemoteAccess = function () {
    const h = window.location.hostname;
    return !(h === "localhost" || h === "127.0.0.1" || h === "::1" || h === "[::1]");
};

// ---- API helper ------------------------------------------------------

window.api = async function (path, opts = {}) {
    const headers = Object.assign({
        'Content-Type': 'application/json',
        'X-CSRF-Token': window._csrfToken || '',
    }, opts.headers || {});
    let token = '';
    // Try cookie first, then URL query param (portal access pattern).
    if (document.cookie) {
        const m = document.cookie.match(/(?:^|;\s*)mw_token=([^;]*)/);
        if (m) token = m[1];
    }
    if (!token) {
        const p = new URLSearchParams(window.location.search);
        token = p.get('token') || '';
    }
    if (token) headers['Authorization'] = 'Bearer ' + token;
    if (window._csrfToken) headers['X-CSRF-Token'] = window._csrfToken;

    const resp = await fetch(path, Object.assign({}, opts, { headers }));

    // 401 → redirect to login (GET requests only; POST may be form
    // submissions inside modals).
    if (resp.status === 401 && (opts.method || 'GET') === 'GET' && !path.startsWith('/login')) {
        window.location.href = '/login?next=' + encodeURIComponent(window.location.pathname);
        throw new Error('unauthorized');
    }
    if (resp.status === 204) return null;

    const ct = resp.headers.get('Content-Type') || '';
    if (ct.includes('application/json')) {
        const data = await resp.json();
        if (resp.ok) return data;
        const err = new Error(data.error || data.message || 'request failed');
        err.status = resp.status;
        err.body = data;
        throw err;
    }
    return resp.text();
};

// ---- refresh ----------------------------------------------------------

let _refreshSeq = 0;
let _refreshTimer = null;

window.refresh = async function () {
    _refreshSeq++;
    const seq = _refreshSeq;
    try {
        const [wts, insts, tags] = await Promise.all([
            window.api('/api/worktrees'),
            window.api('/api/instances'),
            window.api('/api/tags'),
        ]);
        if (seq !== _refreshSeq) return; // stale refresh
        window.state.worktrees = (wts && wts.worktrees) || [];
        window.state.instances = (insts && insts.instances) || [];
        window.state.version = (insts && insts.version) || 0;
        window.state.tags = (tags && tags.tags) || [];
        window.state.tabOrder = (insts && insts.tab_order) || {};
    } catch (e) {
        if ((e.status === 401 || e.status === 403) && window.location.pathname !== '/login') {
            window.location.href = '/login';
        }
        return;
    }
    // Render is a legacy callback defined in index.html. It calls
    // renderSidebar / renderTabs / renderWorkspace / renderModals
    // which remain there for DOM-heavy work.
    if (typeof window.render === 'function') {
        // The framework's render() is the one defined above; the
        // DOM helpers are in index.html and are referenced by
        // their global names.
        window._renderLegacy();
    }
    window._refreshReqId = requestAnimationFrame(() => {
        // next tick: nothing to do; the frame is already painted
    });
};

// ---- workspace selection ----------------------------------------------

window.selectWorktree = function (id) {
    if (window.state.activeWT === id) return;
    window.state.activeWT = id;
    window.state.activeInst = null;
    window.refresh();
};

window.selectWorktreeByID = function (id) {
    window.selectWorktree(id);
};

window.selectInstance = function (id) {
    if (window.state.activeInst === id) return;
    var previousID = window.state.activeInst;
    window.state.activeInst = id;

    var inst = id ? window.state.instances.find(function (i) { return i.id === id; }) : null;

    // Call the legacy re-render hooks (index.html owns the DOM
    // helpers until framework.js fully absorbs the sidebar/tab-
    // list rendering).
    if (typeof window._renderLegacy === 'function') window._renderLegacy();

    // Dispatch to the per-kind renderer.
    var kind = (inst && inst.kind) || 'pty';
    var renderer = (window.KindRenderers || {})[kind];
    var session = { id: id, instance: inst };

    // Deactivate the previously-active renderer.
    if (previousID && id !== previousID) {
        var prevInst = window.state.instances.find(function (i) { return i.id === previousID; });
        var prevKind = (prevInst && prevInst.kind) || 'pty';
        var prevRenderer = (window.KindRenderers || {})[prevKind];
        if (prevRenderer && prevRenderer !== renderer) {
            prevRenderer.deactivate();
        }
    }

    if (id && renderer) {
        renderer.activate(session, document.getElementById('workspace'));
        if (kind === 'opencode-web' && typeof window.updateStatus === 'function') {
            window.updateStatus('opencode web ui');
        }
        if (typeof window.maybeDestroyInactiveStoppedSession === 'function') {
            window.maybeDestroyInactiveStoppedSession(previousID);
        }
        return;
    }

    // Fallback: if no renderer is registered, behave like PTY
    // (legacy default). The body of this fallback delegates to
    // legacy helpers in index.html.
    if (typeof window._selectInstanceLegacyFallback === 'function') {
        window._selectInstanceLegacyFallback(id, inst, previousID);
    }

    if (typeof window.maybeDestroyInactiveStoppedSession === 'function') {
        window.maybeDestroyInactiveStoppedSession(previousID);
    }
};

// ---- modal helpers (shared across all modals) --------------------------

window.openModal = function (id) {
    var el = document.getElementById(id);
    if (!el) return;
    el.showModal();
};

window.closeModal = function (id) {
    var el = document.getElementById(id);
    if (!el) return;
    el.close();
};

// ---- init --------------------------------------------------------------

window._initFramework = function () {
    // Extract CSRF token from the page and start the first
    // refresh. The init() in index.html calls this first, then
    // wires the remaining DOM listeners.
    window._csrfToken = '';
    var csrfEl = document.getElementById('csrf-token');
    if (csrfEl) window._csrfToken = csrfEl.textContent || '';

    window.refresh().then(function () {
        // After the first refresh, start periodic refresh.
        _refreshTimer = setInterval(window.refresh, 8000);
    });
};

})();
