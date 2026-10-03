package dsh_web

import (
	"regexp"
	"strconv"
	"strings"
)

// Version-gate constants (PLAN.md §版本门) — dsh 0.2.x ONLY. The 0.1.x
// compatibility shims were dropped: 0.2 replaced the dotted RPC method
// names with `<namespace>/<method>` endpoints and wrapped every verb's
// arguments in {"args":{"request":…}}, so the 0.1 wire is not
// addressable from this driver at all (see proxy.go / bootstrap.go).
//
//   - Hard gate: a resolved `dsh` whose core version is below
//     minVersion fails Spawn with a readable error (reasonix precedent,
//     issue #45) — below 0.2.0 the genuinely 0.2-only surface the
//     driver speaks did not exist yet: the `<namespace>/<method>`
//     slash endpoints the whole data plane uses, the
//     `payload.args.request` argument nesting every verb carries,
//     the `--no-open` web-app option (issue #84) and the plural
//     `subagents` namespace.
//   - Advisory range [minVersion, supportedMaxVersion): the restrict
//     overlay row ids (`storage-json` / `directory-picker` /
//     `client-hmr` + the `insert: directory-picker-browse` row), the
//     slash-style RPC endpoints and the ready-line format are verified
//     against this range; outside it the frontend shows a persistent
//     warning via Blob.VersionSupported=false. Verified on the installed
//     0.2.0-rc.2: `dsh web --patch <restrict> --dump-config` composes
//     all four rows, and a live instance's pluginInventory/list shows
//     directory-picker-auto / dsh-client-hmr disabled with
//     dsh-host-directory-picker-browse active. The `--port 0` /
//     `--patch` flags, the restrict overlay rows and the browser-auth
//     ready line were re-verified there too, but are NOT floor
//     justifications — they existed in 0.1.x (ready line since
//     dsh 0.1.2, see the remote floor below).
//   - Remote floor: a dsh whose core version is below minRemoteVersion
//     predates the remote-access bridge. The floor gates ONLY the
//     remote bridge/shim (the WS<->SSE bridge on the per-instance proxy
//     plus the injected connection shim). The auth relay is separate:
//     it is built for ANY token-bearing ready line (dsh >= 0.1.2, and
//     0.2.0's exchange still answers GET /?token= with 303 + a
//     dsh-auth-* Set-Cookie) and serves loopback mode too, so an older
//     dsh keeps its loopback-only behavior WITH browser-session auth
//     relayed. isRemoteCapable encodes this floor; it is independent of
//     the advisory supported range above. With the 0.2.x-only policy the
//     floor and the hard gate coincide at 0.2.0 — kept as two constants
//     because they gate different machinery (bridge vs. spawn).
//   - NpxPin is the exact npm version pinned for npx-mode launches and
//     surfaced as the suggested pin in the missing-dependency dialog.
//     0.2.0-rc.2 is what npm publishes as BOTH `latest` and `next`
//     (npm dist-tags, checked while bumping this gate) and the exact
//     build every 0.2 finding above was verified against by hand — the
//     slash endpoints, the {"args":{"request":…}} payload, --no-open and
//     the restrict overlay rows; a different pin could drift on any of
//     them.
const (
	minVersion          = "0.2.0"
	supportedMaxVersion = "0.3.0" // exclusive
	minRemoteVersion    = "0.2.0"
	NpxPin              = "0.2.0-rc.2"
)

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVersion extracts the first x.y.z core from `dsh --version`
// output ("0.2.0-rc.2" / "dsh 0.2.0" / "dev"). Prerelease suffixes
// (-rc.N) are tolerated: the core "0.2.0" is returned.
func parseVersion(out string) (string, bool) {
	m := versionRe.FindStringSubmatch(out)
	if len(m) != 4 {
		return "", false
	}
	return m[1] + "." + m[2] + "." + m[3], true
}

// versionLess reports whether a < b for x.y.z core versions.
func versionLess(a, b string) bool {
	pa, oka := splitVersion(a)
	pb, okb := splitVersion(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func splitVersion(v string) ([3]int, bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// isSupportedVersion reports whether v is within
// [minVersion, supportedMaxVersion). The comparison is CORE-ONLY: v is
// run through parseVersion first, so a prerelease like "0.2.0-rc.2"
// compares as its core "0.2.0" — same shape as isRemoteCapable. Passing
// the raw string through versionLess instead would make splitVersion
// fail on the "0-rc.2" segment, versionLess answer false for BOTH
// bounds and every npx-mode launch (pinned to the exact prerelease
// NpxPin) report VersionSupported=false — the frontend's permanent
// "version too new / overlay not effective" warning bar. An unknown
// (empty) or unparseable ("dev") version is tolerated: it cannot be
// classified and must not be warned about.
func isSupportedVersion(v string) bool {
	core, ok := parseVersion(v)
	if v == "" || !ok {
		return true
	}
	return !versionLess(core, minVersion) && versionLess(core, supportedMaxVersion)
}

// hardVersionOK reports whether v passes the hard gate. CORE-ONLY like
// isSupportedVersion / isRemoteCapable: parseVersion strips the
// prerelease suffix before the floor is compared, so "0.2.0-rc.2"
// passes and "0.1.9-rc.1" fails. (Comparing the raw string instead made
// versionLess answer false for ANY prerelease, so a 0.1.x prerelease
// slipped through the floor.) Empty / unparseable = unknown = tolerated,
// matching the reasonix gate's dev-build policy.
func hardVersionOK(v string) bool {
	core, ok := parseVersion(v)
	if v == "" || !ok {
		return true
	}
	return !versionLess(core, minVersion)
}

// isRemoteCapable reports whether core v supports the remote-access
// bridge (WS<->SSE + connection shim). Empty is tolerated for the same
// dev-build reason as isSupportedVersion. Both "0.2.0" and
// "0.2.0-rc.N" parse to the core "0.2.0", which meets the floor —
// deliberate: the floor is core-only, any 0.2.0-rc.N passes. An
// unparseable version is tolerated (cannot be classified). This is a
// floor only; the supported range is a separate advisory.
func isRemoteCapable(v string) bool {
	if v == "" {
		return true
	}
	core, ok := parseVersion(v)
	if !ok {
		return true
	}
	return !versionLess(core, minRemoteVersion)
}

// RemoteMinVersion exposes the remote floor for the API layer.
func RemoteMinVersion() string {
	return minRemoteVersion
}
