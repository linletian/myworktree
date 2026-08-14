// ReasonixRenderer — manages the per-instance iframes for reasonix
// web-UI instances, ported from the inline ensureWebFrame /
// removeWebFrame / invalidateWebFrame logic (v0.4.0) into the kind
// renderer architecture.
//
// Behaviour:
//   - activate() shows #reasonix-panel and hides the PTY terminal
//     container; each reasonix instance keeps its own iframe cached in
//     a Map (keyed by instance id), so cross-instance tab switches are
//     pure show/hide and the embedded page (chat draft, scroll,
//     sidebar session list) survives (issue #53).
//   - The iframe src is guarded by dataset.instance / dataset.src:
//     navigation only happens when the resolved target actually
//     changes (issue #53 — assigning the same src reloads the page).
//     web_url is a runtime-assigned port (view-only, not persisted),
//     so after a server restart the same instance id resolves to a new
//     web_url and the guard re-navigates instead of keeping a dead
//     pre-restart port.
//   - src resolution: inst.web_url (independent cross-origin listener,
//     issue #44) when present, otherwise the same-origin fallback
//     "/rx/<id>/" (TLS / network-listener mode).
//   - xterm leftovers: when activating a reasonix instance the PTY
//     container may carry a stopped-instance grayscale/opacity style
//     (issue #55) and leftover positioned xterm hosts that would
//     overlay/block the iframe (issue #54) — both are normalized on
//     activation.
//   - Stopped instance: the frame is released (backend is gone, no
//     page state to keep); stop→start allocates a fresh instance id,
//     so a fresh iframe is built lazily (same regression coverage as
//     invalidateWebFrame).

class ReasonixRenderer {
    constructor() {
        this._frames = new Map(); // instance id → iframe element
        this._currentFrame = null;
    }

    activate(session, container) {
        const termContainer = document.getElementById('terminal-container');
        const rxPanel = document.getElementById('reasonix-panel');

        if (termContainer) termContainer.style.display = 'none';
        if (rxPanel) rxPanel.hidden = false;

        // Issue #55: normalize container styles that an xterm instance
        // may have left behind (grayscale/opacity for a stopped
        // instance). CSS filter applies to the iframe content too, so a
        // leftover grayscale would grey out the whole web UI.
        if (termContainer) {
            termContainer.style.opacity = "1";
            termContainer.style.filter = "none";
        }
        // Issue #54: hide any leftover xterm hosts. They are positioned
        // (position:absolute; inset:0) and would overlay/block the
        // static iframe, leaving it blank.
        if (typeof renderTerminalSessions === 'function') renderTerminalSessions();

        // Drop iframes whose instance no longer exists (deleted).
        this._pruneStaleFrames();

        const inst = session.instance;
        if (inst && inst.status === 'stopped') {
            // Stopped instance: the backend is gone and the embedded
            // page is dead — release its iframe (there is no page state
            // worth keeping), same coverage as invalidateWebFrame: the
            // next start gets a fresh instance id, so the next
            // activation builds a fresh iframe.
            this.destroyFrame(session.id);
            this._currentFrame = null;
            return;
        }

        // Get (or lazily create) the iframe dedicated to THIS instance.
        const frame = this._frameFor(session.id, rxPanel);
        // Hide every OTHER instance's iframe; the active frame's own
        // visibility is decided below.
        this._showOnly(frame);

        const id = session.id;
        // Only navigate when the target changes: the dataset guard
        // compares BOTH the instance id and the resolved src. web_url
        // is a runtime-assigned port: after a server restart the
        // independent listener gets a fresh port, so the same instance
        // id resolves to a new web_url — the poll-driven refresh picks
        // up the new web_url and this check navigates to it.
        const src = (inst && inst.web_url) ? inst.web_url : "/rx/" + id + "/";
        if (frame.dataset.instance !== id || frame.dataset.src !== src) {
            frame.dataset.instance = id;
            frame.dataset.src = src;
            frame.src = src;
        }
        frame.hidden = false;
        this._currentFrame = frame;
    }

    deactivate() {
        // Hide the current iframe when leaving the tab (the page state
        // survives — keep-alive is a visibility toggle, not a teardown).
        if (this._currentFrame) this._currentFrame.hidden = true;
        this._currentFrame = null;
    }

    cleanup() {
        if (this._frames) {
            for (const frame of this._frames.values()) {
                if (frame.isConnected) frame.remove();
            }
            this._frames.clear();
        }
        this._currentFrame = null;
    }

    // Remove the cached iframe for one instance (called when the
    // instance is stopped or deleted). Kept separate from cleanup() so
    // a single teardown does not touch other instances' live frames.
    destroyFrame(id) {
        if (!this._frames) return;
        const frame = this._frames.get(id);
        if (frame) {
            if (frame.isConnected) frame.remove();
            this._frames.delete(id);
        }
        if (this._currentFrame === frame) this._currentFrame = null;
    }

    // Get or create the iframe dedicated to this instance. A fresh
    // iframe starts with an empty navigation cache (dataset.instance /
    // dataset.src = ''), so the first activation always navigates.
    _frameFor(id, rxPanel) {
        if (!this._frames) this._frames = new Map();
        let frame = this._frames.get(id);
        if (!frame || !frame.isConnected) {
            frame = document.createElement('iframe');
            frame.className = 'reasonix-frame';
            frame.dataset.instance = '';
            frame.dataset.src = '';
            frame.hidden = true;
            const host = rxPanel && rxPanel.querySelector('#reasonix-frames');
            (host || rxPanel).appendChild(frame);
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
    // (e.g. after the user deletes a reasonix instance). Runs on every
    // activate(); O(cached frames × state.instances) is fine for a
    // handful of instances (same tradeoff as the opencode-web
    // renderer).
    _pruneStaleFrames() {
        if (!this._frames) return;
        const instances = (window.state && window.state.instances) || [];
        const live = new Set(instances.filter(i => i.kind === 'reasonix').map(i => i.id));
        const activeId = window.state && window.state.activeInst;
        for (const [id, frame] of this._frames) {
            // Never drop the currently-active instance's frame, even if
            // a transient state update makes it look absent — dropping
            // it would force the next activate() to rebuild the iframe
            // and reload a running instance.
            const keep = frame.isConnected && (live.has(id) || id === activeId);
            if (!keep) {
                if (frame.isConnected) frame.remove();
                this._frames.delete(id);
                if (this._currentFrame === frame) this._currentFrame = null;
            }
        }
    }
}

window.registerRenderer('reasonix', new ReasonixRenderer());
