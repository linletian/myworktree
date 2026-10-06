package dsh_web

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0.2.0-rc.2", "0.2.0", true},
		{"0.2.0", "0.2.0", true},
		{"dsh 0.2.0\n", "0.2.0", true},
		{"0.10.2", "0.10.2", true},
		{"dev", "", false},
		{"", "", false},
		{"version unknown", "", false},
	}
	for _, c := range cases {
		got, ok := parseVersion(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseVersion(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.1.9", "0.1.10", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.0", false},
		{"1.0.0", "0.9.9", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// The hard floor is the CORE 0.2.0: 0.1.x support was dropped when dsh
// replaced the dotted RPC method names with the slash-style
// /api/<ns>/<method> endpoints, so a 0.1 binary cannot serve this
// driver's data plane at all. Literals only — never the constants — so
// the pins stay meaningful when the constants move.
func TestHardVersionOK(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.1.0", false},
		{"0.1.9", false},
		// CORE-ONLY floor: the -rc suffix is stripped by parseVersion,
		// so a 0.1.x PRERELEASE must fail the gate just like its core —
		// with the raw string versionLess answered false and this gate
		// let a 0.1.9-rc.1 binary boot.
		{"0.1.9-rc.1", false},
		{"0.1.0-rc.5", false},
		{"0.2.0", true},
		// The exact npx pin (NpxPin): the hard gate is what Spawn runs
		// on a probed version, so a prerelease core 0.2.0 must pass.
		{"0.2.0-rc.2", true},
		{"0.2.0-rc.1", true},
		{"0.2.9", true},
		{"0.3.0", true}, // passes the hard gate; advisory range flags it separately
		{"0.0.9", false},
		{"0.0.0", false},
		{"", true}, // unparseable (dev build) tolerated
		{"dev", true},
		{"dsh version unknown", true},
	}
	for _, c := range cases {
		if got := hardVersionOK(c.v); got != c.want {
			t.Errorf("hardVersionOK(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

// The remote floor is the core 0.2.0 (version.go): prerelease suffixes
// are tolerated, the comparison is core-only.
func TestIsRemoteCapable(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.1.0", false},
		{"0.1.9", false},
		{"0.2.0-rc.1", true}, // core-only floor: the -rc suffix is ignored
		{"0.2.0", true},
		{"0.2.0-rc.2", true},
		// deliberate: core-only floor, any 0.2.0-rc.N passes
		{"0.2.0-rc.0", true},
		{"0.2.10", true},
		{"0.3.0", true}, // floor only; supported range is a separate advisory
		{"", true},      // unknown tolerated
		{"x.y", true},   // unparseable tolerated
		{"1", true},     // unparseable tolerated
		{"0.2.0-", true},
	}
	for _, c := range cases {
		if got := isRemoteCapable(c.v); got != c.want {
			t.Errorf("isRemoteCapable(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestRemoteMinVersion(t *testing.T) {
	if got := RemoteMinVersion(); got != "0.2.0" {
		t.Errorf("RemoteMinVersion() = %q, want %q", got, "0.2.0")
	}
}

// Advisory supported range [0.2.0, 0.3.0) — the range the slash-style
// RPC endpoints, the ready-line format and the restrict overlay rows were
// verified against (0.2.0-rc.2). The comparison is CORE-ONLY: parseVer-
// sion strips the "-rc.N" suffix, exactly like isRemoteCapable. Literals
// only — never the constants — so the pins stay meaningful when the
// constants move.
//
// The prerelease rows are the npx-mode regression pin: NpxPin is the
// exact prerelease "0.2.0-rc.2" and Spawn feeds it straight in
// (driver.go, npx branch). With the RAW string compare, splitVersion
// choked on the "0-rc.2" segment, versionLess said false against BOTH
// bounds and every npx-mode instance reported VersionSupported=false —
// the frontend's permanent red "version too new / overlay not effective"
// bar on the missing-dependency dialog's primary fallback path.
func TestIsSupportedVersion(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.1.0", false},
		{"0.1.9", false},
		// core-only: a 0.1.x prerelease is still below the floor
		{"0.1.9-rc.1", false},
		{"0.2.0", true},
		// the exact npx pin (NpxPin): core 0.2.0 is inside the range
		{"0.2.0-rc.2", true},
		{"0.2.0-rc.1", true},
		{"0.2.9", true},
		{"0.2.10", true},
		{"0.3.0", false}, // exclusive upper bound
		{"0.3.0-rc.1", false},
		{"0.0.9", false},
		{"", true},    // unknown tolerated
		{"dev", true}, // unparseable (dev build) tolerated
		{"y.z", true}, // unparseable tolerated
	}
	for _, c := range cases {
		if got := isSupportedVersion(c.v); got != c.want {
			t.Errorf("isSupportedVersion(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

// The two gates and the remote floor must agree on the CORE version: all
// three run parseVersion before comparing, so a prerelease is classified
// identically by every gate (the asymmetry between the raw-string
// isSupportedVersion and the core-parsing isRemoteCapable is what made
// npx mode report version_supported:false while the remote bridge armed).
func TestVersionGatesAgreeOnPrereleaseCore(t *testing.T) {
	for _, v := range []string{"0.2.0-rc.2", "0.2.0-rc.1", "0.2.0", "0.1.9-rc.1", "0.3.0-rc.1"} {
		core, ok := parseVersion(v)
		if !ok {
			t.Fatalf("parseVersion(%q) not parseable", v)
		}
		if got := isSupportedVersion(v); got != isSupportedVersion(core) {
			t.Errorf("isSupportedVersion(%q) = %v, want the core verdict for %q", v, got, core)
		}
		if got := hardVersionOK(v); got != hardVersionOK(core) {
			t.Errorf("hardVersionOK(%q) = %v, want the core verdict for %q", v, got, core)
		}
		if got := isRemoteCapable(v); got != isRemoteCapable(core) {
			t.Errorf("isRemoteCapable(%q) = %v, want the core verdict for %q", v, got, core)
		}
	}
}
