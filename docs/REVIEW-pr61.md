# Code review response — PR #61 (chore(release): v0.4.1)

Reviewer comment analyzed point-by-point on the PR head
(`chore/v0.4.1-release-prep` @ `1d85371`, base `main` @ `99aed42`).

## Scope note (review background)

Accurate. GitHub compares the PR head against `main` (`99aed42`, the
v0.4.0 release), and the branch carries the whole `develop` history —
including the #60 merge (`09a0693`) — so the visible diff is 70 files,
+10614 / −3616. The description's "No code changes; release prep only"
describes the delta **relative to develop**, not to the PR base.
Resolution: the PR body now states that explicitly (feature work was
already reviewed as #60 and merged to develop; this PR brings develop +
release prep onto main so v0.4.1 can be tagged from main HEAD).

## HIGH-1 — `state.json` `kind: "tty"` migration

**Not accurate (premise wrong).** v0.4.0's PTY instance record does
not persist a kind at all: `kind := store.KindTTY` was only a local
dispatch variable, and the `ManagedInstance` built for PTY starts has
no `Kind` field (`omitempty` → not written). Only the reasonix record
wrote an explicit `"reasonix"`, which is unchanged. No released
version ever wrote `"tty"` to state.json, and `""` → `pty` was already
handled at every entry point. So v0.4.0 → v0.4.1 upgrades do **not**
hit `unknown instance kind`, and the CHANGELOG's "Breaking changes:
None" stands. The claimed "stop/start/get/log 全部 500" is likewise
overstated (only a restart of a hypothetically-`tty` record would
fail).

Hardening applied anyway (cheap): `store.CanonicalKind` maps
`""`/`"tty"` → `"pty"` and is used at every framework kind-resolution
site, so hand-edited or develop-built stores can never brick an
instance.

## HIGH-2 — 60s ready-timeout leaks the child process

**Accurate, fixed.** The timeout branch of `runLifecycle` marked the
instance failed and removed it from `m.running` without calling
`k.Stop` — violating the kind contract ("Stop exactly once after a
successful Spawn", `framework/kind.go`), leaking the child process and
its pump/health/wait goroutines, and making any later `Stop(id)` a
no-op. Fix: the branch now calls `k.Stop(handle, stopGraceSeconds)`
first (letting the kind's terminal writes land), then cancels the
lifecycle ctx and marks the instance failed. The timeout is now a
`Manager.readyTimeout` field (default 60s) so the branch is testable.
Pinned by `TestRunLifecycle_ReadyTimeoutStopsInstance`.

## HIGH-3 — `runCtx` / kind-internal `ctx` linkage

**Partially accurate; real gap found and fixed.** Correct that the
manager's `runCtx` cancellation alone never stops a process and that
`h.cancel` is only invoked from the kind's `wait()` after process
exit. On the Shutdown path, reasonix is covered (`StopAllKind` →
`k.Stop` → process exit → `wait()` → `h.cancel`). The genuine gap:
Shutdown did **not** stop opencode-web serves, and opencode-web has no
`Reattach`/`RestartSurvivor` — a surviving serve was orphaned (next
startup marks the record stopped; nothing can ever stop the process).
Fix: Shutdown now also calls `StopAllKind(store.KindOpenCodeWeb)`,
restoring the Stop-once contract for every spawned instance.

## MEDIUM-4 — preStart output not redacted (auth token)

**Accurate for opencode-web, fixed.** `buildEnv` forces
`OPENCODE_SERVER_PASSWORD = cfg.AuthToken` and preStart runs with that
env, so a debug preStart (`env` / `printenv`) leaked the myworktree
main token into the error surfaced to API callers. Note: the existing
`redact.Text` only covers `sk-*`/`Bearer` patterns and would NOT have
caught the token — added `redact.Secret(s, secret)` (exact-string
replacement) and applied `redact.Secret(redact.Text(out), authToken)`
in opencode-web; PTY and reasonix preStart outputs now go through
`redact.Text` as well. (For reasonix the review's premise was slightly
off: its preStart env never contains the myworktree token — the
per-instance reasonix token is generated after preStart and passed via
`--token-file` — but the redaction is still worthwhile for tag env
secrets.)

## MEDIUM-5 — preStart has no timeout

**Accurate, fixed — and worse than stated.** The unbounded
`CombinedOutput()` runs inside `Spawn`, before the framework's
ready-timeout has even started, so a hanging template stalled the
Start request indefinitely (the 60s ceiling from #2 never applied).
All three kinds (the review missed PTY) now run preStart under a
2-minute `exec.CommandContext` with a distinct timeout error. The
review's suggested 5–10s was rejected: README's own tag example uses
`preStart: "npm install"`, which legitimately exceeds that.

## MEDIUM-6 — auth token reused as OPENCODE_SERVER_PASSWORD

Accurate, but a documented design decision (CHANGELOG v0.4.1
"Unified auth token for opencode-web", threat model in
`docs/ARCHITECTURE.md` §8). Accepted for now; per-instance random
passwords + proxy-side caching remain the medium-term option. No
change.

## MEDIUM-7 — `mw_token` cookie SameSite=Lax

Observation accurate; kept Lax deliberately. Every mutating endpoint
is POST, which Lax already blocks cross-site, while Strict would force
re-auth on top-level GET navigations from external links (Tailscale /
remote bookmarks) for no real gain — the embedded iframes are
same-origin either way. Documented with a code comment at the cookie
site.

## MEDIUM-8 — `defuseBlockingFontLinks` injects inline `onload=`

Description accurate, but the CSP concern is hypothetical: reasonix
pages currently send no restrictive CSP (the proxy's own injected
inline script runs), and `media="print"` + `onload` flip is the
standard loadCSS non-blocking pattern. No change; revisit if reasonix
ever ships `script-src 'self'`.

## MEDIUM-9 — `<head>` injection matches lowercase only

**Accurate, fixed.** `bytes.Replace(..., "<head>", ..., 1)` silently
skipped `<HEAD>` / `<head lang=…>`, disabling the single-worktree
isolation with no warning. Injection now uses a case-insensitive,
attribute-tolerant first-match regex (`headTagRe` +
`injectAfterHead`). Pinned by `TestFixProxyHTMLInjectsCaseInsensitiveHead`.

## Verification

- `go build ./...`, `go vet ./...` clean.
- `go test ./...` green; `-race` clean for framework + opencode-web.
- New tests: `TestCanonicalKind`, `TestSecret`,
  `TestRunLifecycle_ReadyTimeoutStopsInstance`,
  `TestFixProxyHTMLInjectsCaseInsensitiveHead`.
