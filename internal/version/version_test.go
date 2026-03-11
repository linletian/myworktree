package version

import "testing"

func TestInfoUsesInjectedValues(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := Version, Commit, BuildDate
	Version = "v0.1.0"
	Commit = "1234567890abcdef"
	BuildDate = "2026-03-11T10:00:00Z"
	t.Cleanup(func() {
		Version = oldVersion
		Commit = oldCommit
		BuildDate = oldBuildDate
	})

	got := Info("mw")
	want := "mw v0.1.0 (1234567890ab) built 2026-03-11T10:00:00Z"
	if got != want {
		t.Fatalf("unexpected version output:\nwant %q\ngot  %q", want, got)
	}
}

func TestShortCommit(t *testing.T) {
	if got := shortCommit("abcdef"); got != "abcdef" {
		t.Fatalf("short commit should stay unchanged, got %q", got)
	}
	if got := shortCommit("1234567890abcdef"); got != "1234567890ab" {
		t.Fatalf("long commit should be shortened, got %q", got)
	}
}
