package dsh_web

import (
	"regexp"
	"strconv"
	"strings"
)

// Version-gate constants (PLAN.md §版本门):
//
//   - Hard gate: a resolved `dsh` whose core version is below
//     minVersion fails Spawn with a readable error (reasonix precedent,
//     issue #45) — the web profile flags / rows the driver relies on
//     (`--port 0`, `--patch`, the web-app row ids) did not exist yet.
//   - Advisory range [minVersion, supportedMaxVersion): the restrict
//     overlay row ids (`storage-json` / `directory-picker` /
//     `client-hmr`), the WS event paths and the ready-line format are
//     verified against this range; outside it the frontend shows a
//     persistent warning via Blob.VersionSupported=false.
//   - NpxPin is the exact npm version pinned for npx-mode launches and
//     surfaced as the suggested pin in the missing-dependency dialog.
//     It is the version the whole integration was verified against
//     (0.1.0-rc.6 — the launcher flag order, the directory-picker
//     composer behavior and the RPC wire were all tested on it; a
//     different pin could drift on any of them).
const (
	minVersion          = "0.1.0"
	supportedMaxVersion = "0.2.0" // exclusive
	NpxPin              = "0.1.0-rc.6"
)

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVersion extracts the first x.y.z core from `dsh --version`
// output ("0.1.0-rc.6" / "dsh 0.1.0" / "dev"). Prerelease suffixes
// (-rc.N) are tolerated: the core "0.1.0" is returned.
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

// isSupportedVersion reports whether core v is within
// [minVersion, supportedMaxVersion). An unknown (empty) version is
// tolerated — a "dev" build cannot be classified and must not be
// warned about.
func isSupportedVersion(v string) bool {
	if v == "" {
		return true
	}
	return !versionLess(v, minVersion) && versionLess(v, supportedMaxVersion)
}

// hardVersionOK reports whether core v passes the hard gate (empty =
// unknown = tolerated, matching the reasonix gate's dev-build policy).
func hardVersionOK(v string) bool {
	return v == "" || !versionLess(v, minVersion)
}
