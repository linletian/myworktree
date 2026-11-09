# Issue #144 Preview safety + large-file Tailscale tests

Temporary, unauthenticated test server. It serves the real
`internal/ui/static/preview.html`, its Markdown Worker, and generated Markdown
fixtures through a mock content API.

## Current safety model

- Supported text ceiling: **1 MiB**, matching the backend text cap.
- Maximum rendered Markdown/source line count: **20,000**.
- Maximum single-line size: **32 KiB**.
- Markdown `marked.parse()` runs in a Web Worker; the main thread stays
  interruptible during parsing.
- Source/code rendering remains adaptive-chunked and can be stopped.
- Markdown raw HTML is escaped and unsafe link/image protocols are rejected
  inside the Worker.
- Unsupported file types are rejected by the content API with **415** before
  any body is returned. The page shows `Preview not supported` and does not
  fall back to forced text parsing.

## Running instance

Tailscale IPv4: `100.86.87.23`
Port: `41777`

- Landing page: <http://100.86.87.23:41777/>
- Small Markdown: <http://100.86.87.23:41777/case/small> — 55,631 bytes / 732 list lines
- Medium Markdown (perf panel): <http://100.86.87.23:41777/case/medium> — 111,187 bytes / 1,463 list lines
- Large Markdown: <http://100.86.87.23:41777/case/large> — 1,000,615 bytes / 13,166 list lines
- Largest local Markdown (perf panel): <http://100.86.87.23:41777/case/largest> — present when the server is started with `LARGEST_MD_PATH`; the measured instance used a 644,083-byte / 2,720-line file with a 29,297-char longest line.
- Unsupported file: <http://100.86.87.23:41777/case/unsupported> — PNG-like bytes; must show `Preview not supported`

Expected behaviour:

| Case | Expected |
|---|---|
| Small | Worker-parsed Markdown; should complete quickly. |
| Medium | Worker-parsed Markdown; main-thread gap should be dominated by DOM/layout, not parsing. |
| Large | Under the 1 MiB / 20k-line cap; Worker parse may take several seconds on iPad but the page should stay responsive and show progress. |
| Largest local | Under production limits; Worker parse; panel reports Worker round-trip and main-thread gaps. |
| Unsupported | API 415; no content rows, no forced source/plain parser; `Preview not supported` card. |

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
curl --noproxy '*' http://100.86.87.23:41777/healthz
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
