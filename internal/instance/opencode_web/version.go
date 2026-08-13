package opencode_web

import (
	"regexp"
	"strconv"
	"strings"
)

// Supported version range for the injected hide script (WORKTREE-ISOLATION.md
// §4.7 L1). The hide script targets specific DOM anchors in the new layout;
// outside this range it may silently stop hiding, so the frontend shows a
// persistent "hiding not verified" warning. Unlike reasonix's fail-fast gate,
// this is advisory only: the instance still starts and serves.
const (
	supportedVersionMin = "1.18.0"
	supportedVersionMax = "1.19.0" // exclusive
)

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVersion extracts the first x.y.z semver from `opencode --version`
// output ("1.18.16" / "opencode 1.18.16" / "dev").
func parseVersion(out string) (string, bool) {
	m := versionRe.FindStringSubmatch(out)
	if len(m) != 4 {
		return "", false
	}
	return m[1] + "." + m[2] + "." + m[3], true
}

// versionLess reports whether a < b for x.y.z versions.
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

// isSupportedVersion reports whether v is within [min, max). An unknown
// (empty) version is tolerated so a "dev" build cannot be blocked.
func isSupportedVersion(v string) bool {
	if v == "" {
		return true
	}
	return !versionLess(v, supportedVersionMin) && versionLess(v, supportedVersionMax)
}
