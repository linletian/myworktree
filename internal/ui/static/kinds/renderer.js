// Renderer interface + registry. Each kind (pty, opencode-web, …)
// registers a renderer that knows how to:
//   - activate(): show its UI in the workspace and connect to its
//     backend (WebSocket for PTY, iframe URL for opencode-web)
//   - deactivate(): tear down the active connection (but keep UI
//     elements around so re-activating is cheap)
//   - cleanup(): release everything (called when the instance is
//     deleted or the page unloads)
//
// The framework (framework.js) calls these — it has no knowledge of
// the kind-specific logic. New kinds are added by registering another
// renderer here; framework.js / index.html do not need to change.

class Renderer {
    activate(session, container) {
        throw new Error('Renderer.activate() not implemented');
    }
    deactivate() {}
    cleanup() {}
}

const kindRenderers = {
    // "pty" and "opencode-web" keys are filled in by kinds/pty.js and
    // kinds/opencode_web.js when they load.
};

function registerRenderer(kindName, renderer) {
    kindRenderers[kindName] = renderer;
}

window.KindRenderers = kindRenderers;
window.registerRenderer = registerRenderer;
