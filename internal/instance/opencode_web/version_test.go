package opencode_web

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1.18.16", "1.18.16", true},
		{"opencode 1.18.16", "1.18.16", true},
		{"opencode v1.18.16", "1.18.16", true},
		{"dev", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := parseVersion(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("parseVersion(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("1.18.0", "1.18.1") {
		t.Fatal("1.18.0 < 1.18.1")
	}
	if versionLess("1.18.1", "1.18.0") {
		t.Fatal("1.18.1 !< 1.18.0")
	}
	if versionLess("1.18.0", "1.18.0") {
		t.Fatal("equal versions must not be <")
	}
	if versionLess("2.0.0", "1.99.0") {
		t.Fatal("2.0.0 !< 1.99.0")
	}
}

func TestIsSupportedVersion(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1.18.0", true},
		{"1.18.16", true},
		{"1.18.99", true},
		{"1.17.19", false},
		{"1.19.0", false},
		{"2.0.0", false},
		{"", true}, // unknown → tolerated
	}
	for _, tt := range tests {
		if got := isSupportedVersion(tt.in); got != tt.want {
			t.Fatalf("isSupportedVersion(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
