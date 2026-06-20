# Vendor Libraries for Text File Preview

The vendor JS/CSS libraries below are distributed as part of the Go binary
via `embed.FS` (`//go:embed static/*`). No runtime download or CDN access is
required — they are compiled in at build time, same as xterm.js.

## Included libraries

### highlight.js v11.9.0 (BSD-3-Clause)
- `internal/ui/static/vendor/highlight/highlight.min.js` — full library (121 KB)
- `internal/ui/static/vendor/highlight/github.min.css` — light theme
- `internal/ui/static/vendor/highlight/github-dark.min.css` — dark theme

### marked.js v12.0.2 (MIT)
- `internal/ui/static/vendor/marked/marked.min.js` — full library (35 KB)

## How to update

Replace the files under `internal/ui/static/vendor/` and rebuild:

```bash
curl -o internal/ui/static/vendor/highlight/highlight.min.js \
  https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js
curl -o internal/ui/static/vendor/marked/marked.min.js \
  https://cdnjs.cloudflare.com/ajax/libs/marked/12.0.2/marked.min.js
```

Open Source License entries are in `index.html` under `#monitor-licenses-list`.
