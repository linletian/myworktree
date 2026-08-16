package dsh_web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"myworktree/internal/gitx"
)

// Launch modes (PLAN.md §缺失依赖):
//
//   - launchPath (default): resolve `dsh` via exec.LookPath at every
//     Start — a global install, or whatever the user's PATH provides.
//   - launchNpx: spawn `npx --yes @deepseek-ai/dsh@<pin> web …`. The
//     pin is exact (NpxPin), so no interactive prompt and no
//     version-resolution surprise. The spawned process MUST run in its
//     own process group (Setpgid) and be stopped by killing the group:
//     npx is dsh's parent process, and killing only the npx PID would
//     orphan the server holding its port.
//   - launchInstall: spawn the absolute global bin path recorded after
//     `npm install -g @deepseek-ai/dsh` (resolved via `npm prefix -g`).
//     Falls back to LookPath when the recorded path is gone.
//
// The mode is persisted PER WORKTREE at
// <DataDir>/dsh/<worktreeHash>/launch.json, NOT per instance: the
// framework allocates a fresh instance id on every Start/Restart and
// wipes the old per-instance state dir (methods.go Restart), so a
// per-instance file would be orphaned exactly when the user needs it
// (failed instance → choose npx → Restart). The choice is naturally a
// worktree property ("how this worktree gets a dsh binary") and
// survives myworktree restarts without framework changes.

type launchMode string

const (
	launchPath    launchMode = "path"
	launchNpx     launchMode = "npx"
	launchInstall launchMode = "install"
)

type launchConfig struct {
	Mode launchMode `json:"mode"`
	// ResolvedBin is the absolute executable path recorded by the
	// install flow (mode "install"). Never trusted for mode "path".
	ResolvedBin string `json:"resolved_bin,omitempty"`
}

// ErrDshNotFound is returned by Spawn's preflight when no dsh
// executable can be resolved. The framework marks the instance failed
// with its Error(); the frontend detects it via the info endpoint's
// `missing_dsh` field and offers the three-way dialog (npx launch /
// install now / cancel).
type ErrDshNotFound struct {
	NpmAvailable bool
	SuggestedPin string
}

func (e *ErrDshNotFound) Error() string {
	msg := "dsh not found in PATH"
	if !e.NpmAvailable {
		msg += " (npm is not available either — install @deepseek-ai/dsh manually)"
	}
	msg += fmt.Sprintf("; suggested pin: %s", e.SuggestedPin)
	return msg
}

// NpmAvailable reports whether the npm CLI is on PATH (used by the
// frontend dialog to decide whether the "install now" option is
// offered).
func NpmAvailable() bool {
	_, err := exec.LookPath("npm")
	return err == nil
}

// worktreeKeyDir is the per-worktree dsh data dir: the launch choice
// AND the storages registry root both live under it.
func worktreeKeyDir(dataDir, worktreePath string) string {
	return filepath.Join(dataDir, "dsh", gitx.HashPath(worktreePath))
}

func launchPathOf(dataDir, worktreePath string) string {
	return filepath.Join(worktreeKeyDir(dataDir, worktreePath), "launch.json")
}

func readLaunch(dataDir, worktreePath string) (launchConfig, error) {
	b, err := os.ReadFile(launchPathOf(dataDir, worktreePath))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return launchConfig{Mode: launchPath}, nil
		}
		return launchConfig{}, fmt.Errorf("dsh: read launch.json: %w", err)
	}
	var c launchConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return launchConfig{}, fmt.Errorf("dsh: parse launch.json: %w", err)
	}
	if c.Mode == "" {
		c.Mode = launchPath
	}
	return c, nil
}

func writeLaunch(dataDir, worktreePath string, c launchConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := worktreeKeyDir(dataDir, worktreePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(launchPathOf(dataDir, worktreePath), b, 0o600)
}

// SetLaunchMode persists the launch mode for a worktree (the
// missing-dependency dialog's npx / path / install choices).
func (d *Driver) SetLaunchMode(worktreePath, mode string) error {
	cfg, err := readLaunch(d.DataDir, worktreePath)
	if err != nil {
		return err
	}
	switch launchMode(mode) {
	case launchPath, launchNpx, launchInstall:
		cfg.Mode = launchMode(mode)
	default:
		return fmt.Errorf("dsh: unknown launch mode %q (want path|npx|install)", mode)
	}
	if cfg.Mode != launchInstall {
		cfg.ResolvedBin = ""
	}
	return writeLaunch(d.DataDir, worktreePath, cfg)
}

// SetInstallBin records the resolved global bin path after
// `npm install -g @deepseek-ai/dsh` and switches the worktree to
// install mode (the next Start spawns that absolute path).
func (d *Driver) SetInstallBin(worktreePath, bin string) error {
	cfg, err := readLaunch(d.DataDir, worktreePath)
	if err != nil {
		return err
	}
	cfg.Mode = launchInstall
	cfg.ResolvedBin = bin
	return writeLaunch(d.DataDir, worktreePath, cfg)
}

// resolveLaunch turns the persisted mode into the executable and the
// argument prefix that precedes the web-app invocation. Returns
// killGroup=true for npx mode (Stop must signal the whole process
// group). The caller appends the web invocation (webArgs) or the
// config dump (dumpArgs).
func resolveLaunch(override string, cfg launchConfig) (exe string, prefix []string, killGroup bool, err error) {
	switch cfg.Mode {
	case launchNpx:
		if _, lerr := exec.LookPath("npx"); lerr != nil {
			return "", nil, false, fmt.Errorf("dsh: launch mode npx requested but npx is not in PATH: %w", lerr)
		}
		return "npx", []string{"--yes", "@deepseek-ai/dsh@" + NpxPin}, true, nil

	case launchInstall:
		bin := cfg.ResolvedBin
		if bin == "" {
			bin = override
		}
		if bin == "" {
			bin, _ = exec.LookPath("dsh")
		}
		if bin == "" {
			return "", nil, false, &ErrDshNotFound{NpmAvailable: NpmAvailable(), SuggestedPin: NpxPin}
		}
		return bin, nil, false, nil

	default: // launchPath
		bin := override
		if bin == "" {
			bin, err = exec.LookPath("dsh")
		}
		if err != nil || bin == "" {
			return "", nil, false, &ErrDshNotFound{NpmAvailable: NpmAvailable(), SuggestedPin: NpxPin}
		}
		return bin, nil, false, nil
	}
}

// webArgs is the fixed web invocation every launch mode shares. The
// flags are hardcoded (PLAN.md §安全姿态): --host 127.0.0.1 (dsh
// rejects 0.0.0.0 itself), --port 0 (dsh fails loud on port conflict —
// never pick a fixed port), --patch <restrict overlay>.
//
// ORDER MATTERS (dsh launcher pitfall, PLAN.md §踩坑): the `web`
// subcommand parses with allowUnknownOption + passThroughOptions — the
// first option it does not know (the app-level --host/--port) switches
// it into pass-through mode, so any launcher-level option AFTER it
// (--patch) is forwarded to the web app, which rejects it with
// "unknown option '--patch'" and the server never boots. Launcher
// flags must therefore come FIRST: `web --patch … --host … --port …`.
func webArgs(overlayPath string) []string {
	return []string{"web", "--patch", overlayPath, "--host", "127.0.0.1", "--port", "0"}
}

// dumpArgs is the boot-free config dump invocation used by the L2
// overlay verification (PLAN.md §裁剪有效性兜底). The dump prints the
// composed web-profile tree including --patch overlays, then exits.
// --patch precedes everything else for the same launcher-parsing
// reason as webArgs.
func dumpArgs(overlayPath string) []string {
	return []string{"web", "--patch", overlayPath, "--dump-config"}
}
