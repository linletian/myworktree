package dsh_web

import (
	"strings"
	"testing"
)

func TestBuildRestrictOverlay(t *testing.T) {
	root := "/tmp/x y/.config/myworktree/abc/dsh/def/storages" // spaces: must be quoted
	out := string(buildRestrictOverlay(root))

	for _, want := range []string{
		"- id: storage-json",
		"root: \"" + root + "\"",
		"- id: directory-picker",
		"disabled: true",
		"- insert:",
		"- id: directory-picker-browse",
		"name: '@deepseek-ai/dsh-host-directory-picker-browse'",
		"- id: client-hmr",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("overlay missing %q:\n%s", want, out)
		}
	}
	// The disabled flag must appear exactly twice (directory-picker +
	// client-hmr) — the inserted browse backend row must NOT be disabled
	// (it provides the directoryPicker service the api-gateway needs).
	if n := strings.Count(out, "disabled: true"); n != 2 {
		t.Errorf("disabled: true count = %d, want 2:\n%s", n, out)
	}
}

func TestYamlQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`/a/b`, `"/a/b"`},
		{`/a b/c`, `"/a b/c"`},
		{`/a"b`, `"/a\"b"`},
		{`/a\b`, `"/a\\b"`},
	}
	for _, c := range cases {
		if got := yamlQuote(c.in); got != c.want {
			t.Errorf("yamlQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVerifyOverlayDump(t *testing.T) {
	root := "/tmp/storages"
	overlay := string(buildRestrictOverlay(root))
	good := "# == dump (mock), patched by /tmp/restrict.yml\n" + overlay

	if !verifyOverlayDump(good, root) {
		t.Error("verifyOverlayDump(good dump) = false, want true")
	}

	// Missing storage root in the row block.
	badRoot := strings.Replace(good, root, "/other", 1)
	if verifyOverlayDump(badRoot, root) {
		t.Error("verifyOverlayDump(dump with wrong root) = true, want false")
	}

	// Missing row entirely.
	noPicker := strings.Replace(good, "- id: directory-picker\n  disabled: true\n", "", 1)
	if verifyOverlayDump(noPicker, root) {
		t.Error("verifyOverlayDump(dump without directory-picker) = true, want false")
	}

	// The browse backend DISABLED must fail the check (the service would
	// be missing → api-gateway cannot activate → boot fails). The row is
	// an INSERT entry, so it is INDENTED in the overlay.
	disabledBrowse := strings.Replace(good,
		"    - id: directory-picker-browse\n      name: '@deepseek-ai/dsh-host-directory-picker-browse'\n",
		"    - id: directory-picker-browse\n      name: '@deepseek-ai/dsh-host-directory-picker-browse'\n      disabled: true\n", 1)
	if verifyOverlayDump(disabledBrowse, root) {
		t.Error("verifyOverlayDump(disabled browse backend) = true, want false")
	}

	// The disabled flag from an UNRELATED row must not satisfy the
	// directory-picker check (block-scoped scanning).
	unrelated := strings.Replace(good, "- id: directory-picker\n  disabled: true\n", "- id: directory-picker\n", 1)
	unrelated = strings.Replace(unrelated, "- id: storage-json\n  config:\n    root: \""+root+"\"\n",
		"- id: storage-json\n  config:\n    root: \""+root+"\"\n    unrelated-disabled: true\n", 1)
	if verifyOverlayDump(unrelated, root) {
		t.Error("verifyOverlayDump(disabled flag in wrong block) = true, want false")
	}

	// Empty / garbage input.
	if verifyOverlayDump("", root) {
		t.Error("verifyOverlayDump(\"\") = true, want false")
	}
	if verifyOverlayDump("not a dump at all", root) {
		t.Error("verifyOverlayDump(garbage) = true, want false")
	}
}

// TestVerifyOverlayDumpFoldedRoot reproduces the macOS failure: dsh's
// YAML dump emits a long storage root as a `>-` FOLDED block scalar and
// wraps it AT the space in "Application Support" (folding only breaks at
// whitespace), so the literal root never appears contiguously and the
// plain substring check reported overlay_verified=false on every macOS
// instance (live report, dsh 0.2.0-rc.2). The whitespace-stripped
// comparison must still pass; a wrong folded root must still fail.
func TestVerifyOverlayDumpFoldedRoot(t *testing.T) {
	root := "/Users/linletian/Library/Application Support/myworktree/c3959526305f4656/dsh/1e03391dfca37c9c/storages"
	folded := `# == @deepseek-ai/dsh-base, patched by /Users/.../restrict.yml
- id: storage-json
  name: '@deepseek-ai/dsh-storage-json'
  config:
    root: >-
      /Users/linletian/Library/Application
      Support/myworktree/c3959526305f4656/dsh/1e03391dfca37c9c/storages
- id: directory-picker
  name: '@deepseek-ai/dsh-host-directory-picker-auto'
  disabled: true
- id: directory-picker-browse
  name: '@deepseek-ai/dsh-host-directory-picker-browse'
- id: client-hmr
  name: '@deepseek-ai/dsh-client-hmr'
  disabled: true
`
	if !verifyOverlayDump(folded, root) {
		t.Error("verifyOverlayDump(folded root dump) = false, want true")
	}

	// A DIFFERENT root folded the same way must not match.
	other := strings.Replace(folded, "1e03391dfca37c9c", "ffffffffffffffff", 1)
	if verifyOverlayDump(other, root) {
		t.Error("verifyOverlayDump(folded dump with wrong root) = true, want false")
	}
}
