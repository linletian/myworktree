package dsh_web

import (
	"fmt"
	"strings"
)

// restrict.yml — the --patch overlay every dsh-web instance spawns
// with (PLAN.md §数据面 / §工作区限制). The patch rows mirror the ids in
// the upstream web-app bundle (packages/bundle/web-app/cordis.patch.yml)
// and are applied AFTER the profile's own layer and the user's home
// layer (apps/cli/src/profile-boot.ts), so the instance's config is the
// last word:
//
//   - storage-json.config.root → the per-worktree storages directory
//     (the workspace registry is isolated per worktree; sessions stay
//     in the shared ~/.dsh/sessions pool).
//   - directory-picker.disabled + insert directory-picker-browse: the
//     `directory-picker` row is the auto-composer that mounts BOTH the
//     host backend (the `directoryPicker` service) AND the client
//     surface (the "Add workspace…" entry in the ui-workspace
//     directory-flow holes). Simply disabling it would leave the
//     api-gateway (@deepseek-ai/dsh-host-apiproxy) pending on the
//     directoryPicker service and the whole plugin tree fails to load
//     (observed on dsh 0.1.0-rc.6: "1 entry did not activate"). The
//     overlay therefore disables the composer and INSERTs a bare host
//     backend row (@deepseek-ai/dsh-host-directory-picker-browse —
//     works headless and remote, unlike -native) that provides the
//     service with NO client surface: the directory-flow hole stays
//     empty and the entry never renders.
//   - client-hmr.disabled → hygiene: only active under a dev:web
//     watcher, harmless, but disabled so the production embed can never
//     hot-reload.
//
// The YAML is hand-written (no third-party yaml dependency); the file
// is machine-consumed by dsh's own yaml parser, so only the row shape
// matters. The `insert` directive is the patch loader's way to ADD
// entries (applyEntryPatches: a patch row `{insert: [...]}` without an
// id pushes the entries onto the root list).
func buildRestrictOverlay(storagesRoot string) []byte {
	return []byte(fmt.Sprintf(`- id: storage-json
  config:
    root: %s
- id: directory-picker
  disabled: true
- insert:
    - id: directory-picker-browse
      name: '@deepseek-ai/dsh-host-directory-picker-browse'
- id: client-hmr
  disabled: true
`, yamlQuote(storagesRoot)))
}

// yamlQuote renders a path as a double-quoted YAML scalar, escaping
// backslashes and quotes. Paths with spaces / colons / hashes are then
// safe in the config value.
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// verifyOverlayDump checks a `dsh web --dump-config --patch <overlay>`
// output for the restrict overlay rows (PLAN.md §裁剪有效性兜底 L2): each
// expected row id must appear with the expected value in its own block.
// Block-scoped scanning keeps a `disabled: true` from another row from
// satisfying the directory-picker check.
func verifyOverlayDump(dump, storagesRoot string) bool {
	checks := map[string]func(block []string) bool{
		"storage-json": func(block []string) bool {
			return blockContains(block, storagesRoot)
		},
		"directory-picker": func(block []string) bool {
			return blockContains(block, "disabled: true")
		},
		"directory-picker-browse": func(block []string) bool {
			// The inserted host backend must be mounted (not disabled):
			// it provides the directoryPicker service the api-gateway
			// depends on.
			return blockContains(block, "@deepseek-ai/dsh-host-directory-picker-browse") &&
				!blockContains(block, "disabled: true")
		},
		"client-hmr": func(block []string) bool {
			return blockContains(block, "disabled: true")
		},
	}
	seen := make(map[string]bool)
	var current string
	var block []string
	flush := func() {
		if current == "" {
			return
		}
		if fn, ok := checks[current]; ok && fn(block) {
			seen[current] = true
		}
		current = ""
		block = nil
	}
	for _, line := range strings.Split(dump, "\n") {
		trimmed := strings.TrimSpace(line)
		if id, ok := strings.CutPrefix(trimmed, "- id: "); ok {
			flush()
			current = strings.TrimSpace(id)
			block = []string{line}
			continue
		}
		if current != "" {
			block = append(block, line)
		}
	}
	flush()
	return seen["storage-json"] && seen["directory-picker"] &&
		seen["directory-picker-browse"] && seen["client-hmr"]
}

func blockContains(block []string, needle string) bool {
	for _, line := range block {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
