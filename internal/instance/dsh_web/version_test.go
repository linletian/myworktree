package dsh_web

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0.1.0-rc.6", "0.1.0", true},
		{"0.1.0", "0.1.0", true},
		{"dsh 0.1.0\n", "0.1.0", true},
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

func TestHardVersionOK(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.1.0", true},
		{"0.1.9", true},
		{"0.2.0", true}, // passes the hard gate; advisory range flags it separately
		{"0.0.9", false},
		{"0.0.0", false},
		{"", true}, // unparseable (dev build) tolerated
	}
	for _, c := range cases {
		if got := hardVersionOK(c.v); got != c.want {
			t.Errorf("hardVersionOK(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestIsSupportedVersion(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.1.0", true},
		{"0.1.9", true},
		{"0.2.0", false}, // exclusive upper bound
		{"0.0.9", false},
		{"", true}, // unknown tolerated
	}
	for _, c := range cases {
		if got := isSupportedVersion(c.v); got != c.want {
			t.Errorf("isSupportedVersion(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}
