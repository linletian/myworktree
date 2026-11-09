/* Markdown parsing worker for /preview (issue #144).
 *
 * marked.parse() runs synchronously and can block the UI for seconds on large
 * Markdown files. Keeping it here preserves the formatted Markdown preview
 * without blocking the page's main thread; the page still enforces its
 * preflight size/line limits before creating this worker.
 *
 * Safety: marked v12 no longer implements `sanitize`. Raw HTML tokens are
 * escaped here, and link/image destinations are restricted to relative or
 * http(s)/mailto/tel URLs. This keeps repository Markdown from injecting raw
 * markup or javascript:/data: URLs into the same-origin preview page.
 */
/* global importScripts, marked */
importScripts('/static/vendor/marked/marked.min.js');

function escapeHtml(value) {
    return String(value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function decodeHrefEntities(value) {
    return value
        .replace(/&#x([0-9a-f]+);?/gi, function(_, hex) {
            return String.fromCharCode(parseInt(hex, 16));
        })
        .replace(/&#([0-9]+);?/g, function(_, dec) {
            return String.fromCharCode(parseInt(dec, 10));
        })
        .replace(/&colon;/gi, ':')
        .replace(/&tab;|&newline;/gi, ' ');
}

function safePreviewUrl(href) {
    // Marked may leave encoded entities in the destination. Browsers decode
    // those when they consume the attribute, so decode first and then make the
    // scheme decision on the effective URL.
    var decoded = decodeHrefEntities(String(href || ''));
    var compact = decoded.replace(/[\u0000-\u0020]+/g, '');
    if (compact === '') return true;
    if (/^(https?:|mailto:|tel:)/i.test(compact)) return true;
    // Any other explicit scheme (javascript:, data:, vbscript:, file:, ...) is
    // rejected; protocol-relative and relative URLs are allowed.
    if (/^[a-z][a-z0-9+.-]*:/i.test(compact)) return false;
    return true;
}

function createSafeRenderer() {
    var renderer = new marked.Renderer();
    var fallbackLink = renderer.link.bind(renderer);
    var fallbackImage = renderer.image.bind(renderer);

    renderer.html = function(html) {
        return escapeHtml(html);
    };
    renderer.link = function(href, title, text) {
        if (!safePreviewUrl(href)) return text;
        return fallbackLink(href, title, text);
    };
    renderer.image = function(href, title, text) {
        if (!safePreviewUrl(href)) return text;
        return fallbackImage(href, title, text);
    };
    return renderer;
}

var safeRenderer = createSafeRenderer();

self.onmessage = function(event) {
    var data = event.data || {};
    if (data.type !== 'parse') return;
    try {
        var html = marked.parse(String(data.text || ''), { renderer: safeRenderer });
        self.postMessage({ type: 'result', id: data.id, html: html });
    } catch (err) {
        self.postMessage({
            type: 'error',
            id: data.id,
            message: err && err.message ? err.message : String(err)
        });
    }
};
