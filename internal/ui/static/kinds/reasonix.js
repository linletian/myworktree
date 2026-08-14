// ReasonixRenderer — renders the reasonix web-UI iframe for reasonix
// instances, ported 1:1 from the v0.4.0 inline ensureWebFrame /
// removeWebFrame / invalidateWebFrame model (one SHARED iframe):
//
//   - One iframe element total. Switching between two reasonix
//     instances re-navigates (reloads) the shared frame — exactly the
//     proven main-branch behaviour. This deliberately avoids keeping
//     several reasonix SPA pages alive at once under the same origin.
//   - Switching AWAY to a non-reasonix tab only hides the frame
//     (issue #53): the embedded page (chat draft, scroll, sidebar
//     session list) survives the tab switch.
//   - Navigation is guarded by dataset.instance / dataset.src:
//     assigning the same src would reload the embedded page on every
//     activate (the 2s refresh does not touch the renderer, but
//     re-activation and web_url port changes do). web_url is a
//     runtime-assigned port (view-only, not persisted), so after a
//     server restart the same instance id resolves to a new web_url
//     and the guard re-navigates instead of keeping a dead
//     pre-restart port.
//   - src resolution: inst.web_url (independent cross-origin listener,
//     issue #44) when present, otherwise the same-origin fallback
//     "/rx/<id>/" (TLS / network-listener mode).
//   - Stopped instance: the frame is hidden AND its navigation cache
//     is cleared (invalidateWebFrame semantics) — on the same-origin
//     fallback the src never changes, so without clearing the dataset
//     a stop→start cycle would keep showing the stale pre-stop page;
//     the next start gets a fresh instance id and re-navigates.
//   - xterm leftovers: activating a reasonix instance normalizes the
//     PTY container's grayscale/opacity (issue #55) and hides leftover
//     positioned xterm hosts that would overlay/block the iframe
//     (issue #54).

class ReasonixRenderer {
    constructor() {
        this._frame = null; // the single shared iframe (created lazily)
        this._currentId = null;
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

        const inst = session.instance;
        const id = session.id;

        // Only a running (or still-flipping starting) instance has a live
        // page — Driver.Start already waited for the listen port, so a
        // "starting" record is servable. Anything else hides the frame
        // and clears the navigation cache so the next activation reloads.
        if (!inst || (inst.status !== 'running' && inst.status !== 'starting')) {
            this._invalidate();
            this._currentId = null;
            return;
        }

        const frame = this._ensureFrame(rxPanel);
        // Only navigate when the target changes: the dataset guard
        // compares BOTH the instance id and the resolved src. web_url
        // is a runtime-assigned port: after a server restart the
        // independent listener gets a fresh port, so the same instance
        // id resolves to a new web_url — the guard picks that up and
        // re-navigates instead of keeping a dead pre-restart port.
        const src = (inst && inst.web_url) ? inst.web_url : "/rx/" + id + "/";
        if (frame.dataset.instance !== id || frame.dataset.src !== src) {
            frame.dataset.instance = id;
            frame.dataset.src = src;
            frame.src = src;
        }
        frame.hidden = false;
        this._currentId = id;
    }

    deactivate() {
        // Hide the frame instead of destroying it (issue #53): switching
        // away must only toggle visibility so the embedded page (chat
        // draft, scroll position, sidebar session list) survives tab
        // switches. The dataset guard in activate() still re-navigates
        // only when the resolved target actually changes.
        if (this._frame) this._frame.hidden = true;
        this._currentId = null;
        // Hide the panel itself: web-ui panels are mutually exclusive,
        // and leaving it visible would stack it with the next kind's
        // panel (half/half layout).
        const rxPanel = document.getElementById('reasonix-panel');
        if (rxPanel) rxPanel.hidden = true;
    }

    cleanup() {
        if (this._frame && this._frame.isConnected) this._frame.remove();
        this._frame = null;
        this._currentId = null;
    }

    // Called by the shell when a reasonix instance is stopped or
    // deleted. InvalidateWebFrame semantics: hide AND clear the
    // navigation cache so the next running activation re-navigates
    // (a stop→start cycle allocates a fresh instance id anyway, but the
    // cleared dataset also covers the same-id src fallback).
    destroyFrame(id) {
        if (this._frame && (id == null || this._frame.dataset.instance === id)) {
            this._invalidate();
        }
        if (this._currentId === id) this._currentId = null;
    }

    // Hide AND invalidate the frame's navigation cache.
    _invalidate() {
        if (this._frame) {
            this._frame.hidden = true;
            this._frame.dataset.instance = '';
            this._frame.dataset.src = '';
        }
    }

    // Get or create the single shared iframe. A fresh iframe starts
    // with an empty navigation cache (dataset.instance / dataset.src
    // = ''), so the first activation always navigates.
    _ensureFrame(rxPanel) {
        if (this._frame && this._frame.isConnected) return this._frame;
        const frame = document.createElement('iframe');
        frame.className = 'reasonix-frame';
        frame.dataset.instance = '';
        frame.dataset.src = '';
        frame.hidden = true;
        const host = rxPanel && rxPanel.querySelector('#reasonix-frames');
        (host || rxPanel).appendChild(frame);
        this._frame = frame;
        return frame;
    }
}

window.registerRenderer('reasonix', new ReasonixRenderer());
