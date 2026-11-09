/* Source highlighting worker for /preview (issue #144 review follow-up).
 *
 * highlight.js tokenizes the complete source synchronously. Running it on the
 * page main thread made Stop/tab-switch unable to interrupt large files. This
 * worker performs the full highlight off-main-thread and returns per-line HTML,
 * so the page can keep the existing line-number DOM construction and chunking.
 */
/* global importScripts, hljs */
importScripts('/static/vendor/highlight/highlight.min.js');

function splitHighlightedLines(text, lang) {
    var result = hljs.highlight(String(text || ''), { language: lang });
    var htmlLines = result.value.split('\n');
    var out = [];
    var stack = [];

    for (var i = 0; i < htmlLines.length; i++) {
        var line = htmlLines[i];
        for (var k = stack.length - 1; k >= 0; k--) line = stack[k] + line;

        var re = /<span[^>]*>|<\/span>/g;
        var match;
        while ((match = re.exec(line)) !== null) {
            if (match[0].indexOf('</span>') === 0) {
                if (stack.length > 0) stack.pop();
            } else {
                stack.push(match[0]);
            }
        }
        for (var j = 0; j < stack.length; j++) line += '</span>';
        out.push(line);
    }
    if (out.length > 0 && stack.length > 0) {
        for (var j = 0; j < stack.length; j++) out[out.length - 1] += '</span>';
    }
    return out;
}

self.onmessage = function(event) {
    var data = event.data || {};
    if (data.type !== 'highlight') return;
    try {
        var lines = splitHighlightedLines(data.text, data.language);
        self.postMessage({ type: 'result', id: data.id, lines: lines });
    } catch (err) {
        self.postMessage({
            type: 'error',
            id: data.id,
            message: err && err.message ? err.message : String(err)
        });
    }
};
