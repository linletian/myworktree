// ReasonixRenderer — per-instance iframe cache for reasonix web-UI
// instances, extending the v0.4.0 keep-alive idea (issue #53) to
// CROSS-instance switches:
//
//   - Each reasonix instance owns its iframe, cached in a Map keyed by
//     instance id. Switching between two running reasonix instances
//     only hides/shows their dedicated iframes — the page of instance
//     A (chat draft, scroll, sidebar session list) survives even after
//     switching to B and back. main's single shared #web-frame reloaded
//     on every cross-instance switch, losing the draft.
//   - Switching AWAY to a non-reasonix tab only hides the current
//     frame (issue #53) — never destroyed, never reloaded.
//   - Navigation is guarded by dataset.instance / dataset.src: src is
//     assigned only when the resolved target actually changes, so
//     re-activation and polling never reload a running page. web_url
//     is a runtime-assigned port (view-only, not persisted), so after
//     a server restart the same instance id resolves to a new web_url
//     and the guard re-navigates instead of keeping a dead
//     pre-restart port (main re-checked this on every 2s render; this
//     renderer runs the same check on its own 2s poll).
//   - src resolution: inst.web_url (independent cross-origin listener,
//     issue #44) when present, otherwise the same-origin fallback
//     "/rx/<id>/" (TLS / network-listener mode).
//   - Only a running (or still-flipping starting) instance has a live
//     page — Driver.Start already waited for the listen port, so a
//     "starting" record is servable. Anything else hides the frame
//     and clears its navigation cache so the next activation reloads
//     (invalidateWebFrame semantics: on the same-origin fallback the
//     src never changes, so without clearing the dataset a stop→start
//     cycle would keep showing the stale pre-stop page).
//   - Stopping/deleting an instance releases its iframe (destroyFrame
//     from the shell): the backend is gone, there is no page state to
//     keep; the next start gets a fresh instance id and builds a fresh
//     frame lazily.
//   - xterm leftovers: activating a reasonix instance normalizes the
//     PTY container's grayscale/opacity (issue #55) and hides leftover
//     positioned xterm hosts that would overlay/block the iframe
//     (issue #54).
//
// Diagnostics: the renderer logs its lifecycle to the console under
// the [reasonix-renderer] prefix, so white-screen reports can carry
// the actual activation/navigation decisions.

class ReasonixRenderer {
    constructor() {
        this._frames = new Map(); // instance id → iframe element
        this._currentFrame = null;
        this._activeId = null;
        this._timer = null;
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

        this._activeId = session.id;
        console.debug('[reasonix-renderer] activate', session.id, session.instance && session.instance.status);
        this._tick(session.instance);
        this._startPolling();
    }

    deactivate() {
        this._stopPolling();
        this._activeId = null;
        // Hide the current iframe when leaving the tab (the page state
        // survives — keep-alive is a visibility toggle, not a teardown).
        if (this._currentFrame) this._currentFrame.hidden = true;
        this._currentFrame = null;
        // Hide the panel itself: web-ui panels are mutually exclusive,
        // and leaving it visible would stack it with the next kind's
        // panel (half/half layout).
        const rxPanel = document.getElementById('reasonix-panel');
        if (rxPanel) rxPanel.hidden = true;
    }

    cleanup() {
        this._stopPolling();
        this._activeId = null;
        if (this._frames) {
            for (const frame of this._frames.values()) {
                if (frame.isConnected) frame.remove();
            }
            this._frames.clear();
        }
        this._currentFrame = null;
    }

    // Remove the cached iframe for one instance (called by the shell
    // when the instance is stopped or deleted). Kept separate from
    // cleanup() so a single teardown does not touch other instances'
    // live frames.
    destroyFrame(id) {
        if (!this._frames) return;
        const frame = this._frames.get(id);
        if (frame) {
            if (frame.isConnected) frame.remove();
            this._frames.delete(id);
        }
        if (this._currentFrame === frame) this._currentFrame = null;
    }

    // Re-check the active instance every 2s: re-navigate on web_url
    // port changes (daemon restart), invalidate on stop/failure, and
    // retry a fresh frame whose first navigation raced a transition.
    _startPolling() {
        if (this._timer) clearInterval(this._timer);
        this._timer = setInterval(() => {
            if (this._activeId) this._tick(null);
        }, 2000);
    }

    _stopPolling() {
        if (this._timer) {
            clearInterval(this._timer);
            this._timer = null;
        }
    }

    _tick(inst) {
        const id = this._activeId;
        if (!id) return;
        if (!inst) {
            inst = ((window.state && window.state.instances) || []).find(i => i.id === id) || null;
        }
        const rxPanel = document.getElementById('reasonix-panel');

        if (!inst || (inst.status !== 'running' && inst.status !== 'starting')) {
            // The active instance has no live page: hide its frame and
            // clear its navigation cache so the next activation reloads.
            const frame = this._frames && this._frames.get(id);
            if (frame) {
                frame.hidden = true;
                frame.dataset.instance = '';
                frame.dataset.src = '';
            }
            if (this._currentFrame === frame) this._currentFrame = null;
            return;
        }

        // Get (or lazily create) the iframe dedicated to THIS instance.
        const frame = this._frameFor(id, rxPanel);
        // Hide every OTHER instance's iframe; the active frame's own
        // visibility is decided below.
        this._showOnly(frame);

        // Only navigate when the target changes: the dataset guard
        // compares BOTH the instance id and the resolved src. web_url
        // is a runtime-assigned port: after a server restart the
        // independent listener gets a fresh port, so the same instance
        // id resolves to a new web_url — the guard picks that up and
        // re-navigates instead of keeping a dead pre-restart port.
        const src = (inst && inst.web_url) ? inst.web_url : "/rx/" + id + "/";
        if (frame.dataset.instance !== id || frame.dataset.src !== src) {
            console.debug('[reasonix-renderer] navigate', id, src);
            frame.dataset.instance = id;
            frame.dataset.src = src;
            frame.src = src;
        }
        frame.hidden = false;
        this._currentFrame = frame;
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
