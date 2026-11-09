# Evidence round map

This directory mixes exploratory evidence from several rounds. Use only
`final-*` files as evidence for the shipped implementation.

| Files | Meaning |
|---|---|
| `h1-cpu1.jsonl`, `h1-cpu6.jsonl`, `h2-cpu6.jsonl` | Pre-fix baseline (no front-end safety envelope). |
| `interaction-cpu6.jsonl` | Earlier interaction check under the first 128 KiB / 2,000-line / 12,000-char / 2,000 ms round. |
| `post-fix-h1-cpu6.jsonl`, `post-fix-h2-cpu6.jsonl` | Intermediate round with the same earlier thresholds; **not evidence for the shipped limits**. Their `statusText` and errors make this explicit. |
| `final-source-h1-cpu6.jsonl` | Shipped source/code limits: 128 KiB / 2,000 lines / 12,000 chars per line / 2,000 ms. Highlighter runs in the source Worker. |
| `final-markdown-live.jsonl` | Shipped Markdown Worker path, captured through the Tailscale mock page and perf panel. |

The pre-fix and intermediate files are retained for attribution only; do not
quote their threshold-dependent rows as shipped behavior.
