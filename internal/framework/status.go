// Package framework provides the worktree-aware instance lifecycle
// orchestration that all instance kinds share. It contains the status
// enum, the kind registry, the manager that drives lifecycle, and the
// shared utilities (ring buffer, memory sampler) that kinds consume.
//
// The framework knows NOTHING about any specific kind (PTY, opencode-web,
// …). Concrete kinds live in subpackages and register themselves with
// the framework registry via init().
package framework

import (
	"encoding/json"
	"fmt"
)

// Status is the lifecycle state of a managed instance. The framework
// advances status forward only — a kind reports its current Status via
// the Kind interface, and the manager writes that to state.json. Kinds
// MUST NOT regress to an earlier state without going through the
// framework's transition helpers (e.g. markStopped, markFailed).
//
// JSON wire format preserves the string values the original ad-hoc
// instance record used, so existing state.json files (pre-refactor)
// round-trip without migration. New statuses use the same convention.
type Status int

const (
	// StatusStarting means Spawn has returned successfully but the
	// process is not yet interactive / ready to serve traffic. For
	// PTY this is roughly the time between pty.Start and the first
	// output. For opencode-web this is the time between exec.Start
	// and the "listening on" line being parsed from stdout.
	StatusStarting Status = iota

	// StatusRunning means the instance is fully ready: PTY can
	// accept input, opencode-web is responding to /global/health.
	StatusRunning

	// StatusUnhealthy means the instance was Running but a health
	// probe just failed. The framework will keep the process alive
	// and re-probe; consecutive failures transition to StatusFailed.
	StatusUnhealthy

	// StatusStopping means Stop was called and the framework is
	// waiting for the process to exit (within the configured grace
	// period before SIGKILL).
	StatusStopping

	// StatusStopped means Stop succeeded (process exited cleanly
	// or was killed after the grace period). StatusStopped is the
	// only state from which Start may be invoked again without
	// going through Restart.
	StatusStopped

	// StatusFailed means the instance could not reach Running
	// within the startup window, or it died after Running without
	// a Stop call, or it tripped the health-fail threshold.
	// LastError carries the reason.
	StatusFailed

	// StatusExited means the process exited on its own (not via
	// Stop). Distinct from StatusStopped so the UI can distinguish
	// "user clicked Stop" from "the program crashed".
	StatusExited
)

// String returns the canonical lowercase form used on the wire
// (state.json, JSON API responses, log lines). This is the value
// the frontend / CLI sees, so it MUST be stable across releases.
func (s Status) String() string {
	switch s {
	case StatusStarting:
		return "starting"
	case StatusRunning:
		return "running"
	case StatusUnhealthy:
		return "unhealthy"
	case StatusStopping:
		return "stopping"
	case StatusStopped:
		return "stopped"
	case StatusFailed:
		return "failed"
	case StatusExited:
		return "exited"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// MarshalJSON encodes the status as its canonical string form. The
// wire format is the same lowercase string the original ad-hoc
// ManagedInstance.Status used, so pre-refactor state.json files load
// without a migration step.
func (s Status) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON decodes the wire string back into the enum. Unknown
// strings map to StatusStarting (the most conservative fallback —
// callers will re-probe and reclassify the instance on the next
// health check or stdout parse).
func (s *Status) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	parsed, err := ParseStatus(raw)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// ParseStatus maps a wire string to its enum value. Used by
// UnmarshalJSON and by tests that build Status values from external
// input. Unknown strings return an error rather than silently
// defaulting, so a typo in state.json surfaces immediately.
func ParseStatus(s string) (Status, error) {
	switch s {
	case "starting":
		return StatusStarting, nil
	case "running":
		return StatusRunning, nil
	case "unhealthy":
		return StatusUnhealthy, nil
	case "stopping":
		return StatusStopping, nil
	case "stopped":
		return StatusStopped, nil
	case "failed":
		return StatusFailed, nil
	case "exited":
		return StatusExited, nil
	default:
		return StatusStarting, fmt.Errorf("unknown status: %q", s)
	}
}
