# Issue #144 Preview safety + large-file Tailscale tests

Temporary, unauthenticated test server. It serves the real
`internal/ui/static/preview.html`, its Markdown Worker, and generated Markdown
fixtures through a mock content API.

## Current safety model

- Backend text transport cap: **2 MiB** (overflow returns 413).
- Markdown render cap: **1 MiB / 20,000 lines / 32 KiB per line**; parsing runs
  in `preview_markdown_worker.js`.
- Source/plain render cap: **128 KiB / 2,000 lines / 12,000 characters per
  line / 2,000 ms**; highlighting runs in `preview_source_highlight_worker.js`
  and DOM insertion is adaptive-chunked.
- Files above a renderer's cap but below the backend transport cap reach the
  front-end and offer a bounded excerpt.
- Markdown raw HTML is escaped and unsafe link/image protocols are rejected
  inside the Worker.
- Unsupported file types, binary NUL and invalid UTF-8 are rejected by the
  content API before parsing; the page shows `Preview not supported` and does
  not force text parsing.

## Running a test instance

The previous Tailscale test instance has been stopped. There is no permanent
address or running service in this repository.

Start the server on the host that has the markdown fixtures, then open the
printed URL through that host's Tailscale address (replace `<tailscale-ip>`
with your own address):

```bash
cd /path/to/repo
LARGEST_MD_PATH=/path/to/largest.md \
  node docs/plans/issue144-preview-hang/live-test/server.mjs
# open http://<tailscale-ip>:41777/
```

When `LARGEST_MD_PATH` is set, the server also publishes `/case/largest`.
Without it, only the three generated cases are available.

Expected behaviour:

| Case | Expected |
|---|---|
| Small | Worker-parsed Markdown; should complete quickly. |
| Medium | Worker-parsed Markdown; the perf panel reports Worker round-trip and visible main-thread gaps. |
| Large | Under the Markdown render cap; Worker parsing keeps the main thread responsive while the content may take time to appear. |
| Largest local | Present only when `LARGEST_MD_PATH` is set; same Worker path as production. |
| Unsupported | API 415; no content rows; `Preview not supported` card; no forced text parse. |

## Jank measurement on `/case/medium`, `/case/large`, and `/case/largest`

`medium` and `largest` inject the selectable/copyable performance panel. `large`
is the same production page without the panel but exercises the same Worker
rendering path. The panel reports:

- fetch time;
- `Response.text()` decode time;
- main-thread `marked.parse()` time (normally 0 now);
- Worker round-trip wall time and input/result sizes; the wall time includes any Safari background throttling;
- hidden-tab duration and visibility-change count;
- time until the Markdown DOM is ready;
- post-Worker main-thread commit time (`DOM ready - worker round-trip`);
- forced layout time;
- DOM-to-first-frame time;
- time to first frame and a hidden-time-adjusted visible elapsed estimate;
- longest *visible-window* main-thread gap from a 10 ms `setInterval` heartbeat; this heartbeat is noisy across visibility changes and should be treated as an upper bound. Prefer `post-worker commit` + `DOM -> first frame` for commit jank.
- count of gaps over 50 ms;
- number of Markdown DOM nodes.

The heartbeat reports a stall after the main thread returns; it is a post-hoc
measurement, not a preemption mechanism. The same report is available as
`window.__issue144Perf`.

Hard-refresh the page after changing the server. The page is `no-store`, but
Safari may keep the old tab.

## Start

```bash
cd /path/to/repo
mkdir -p /tmp/issue144-live-test
LARGEST_MD_PATH=/path/to/largest.md \
  nohup node docs/plans/issue144-preview-hang/live-test/server.mjs \
  > /tmp/issue144-live-test/server.log 2>&1 &
echo $! > /tmp/issue144-live-test/server.pid
```

## Health/check

```bash
# The host shell may have an HTTP proxy for 100.x addresses; bypass it locally.
curl --noproxy '*' http://<tailscale-ip>:41777/healthz
```

Expected health JSON when `LARGEST_MD_PATH` is set:

```json
{"ok":true,"host":"0.0.0.0","port":41777,"cases":[
  {"slug":"small","path":"case-50k.md","bytes":55631,"perf":false,"unsupported":false},
  {"slug":"medium","path":"case-100k.md","bytes":111187,"perf":true,"unsupported":false},
  {"slug":"large","path":"case-900k.md","bytes":1000615,"perf":false,"unsupported":false},
  {"slug":"largest","path":"magpie-gateway-routing.md","bytes":644083,"perf":true,"unsupported":false},
  {"slug":"unsupported","path":"case-unsupported.png","bytes":10,"perf":false,"unsupported":true}
]}
```

## Stop

```bash
kill "$(cat /tmp/issue144-live-test/server.pid)"
rm -f /tmp/issue144-live-test/server.pid
```

## Security boundary

No authentication is enabled. Only run this on a trusted Tailnet while testing,
and stop it when finished. It does not start `mw serve`, create a worktree, or
execute repository HTML. The Markdown Worker is a safety boundary for raw HTML
and unsafe URLs, not a general-purpose document trust boundary.
