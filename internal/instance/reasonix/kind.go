// Kind implements framework.Kind for reasonix serve subprocesses, the
// third managed instance kind alongside pty and opencode-web. It is the
// framework-native port of the pre-refactor Manager.startReasonix /
// StopAllReasonix / ReconcileRunningOnStartup integration: the Driver
// owns the subprocess (token/port/pid state dir under
// <DataDir>/reasonix/<id>), while this Kind translates the framework
// lifecycle contract (Spawn / Stop / Status / Reattach / Cleanup) onto
// it.
//
// Behavioural parity with the v0.4.0 release (main branch):
//   - serve runs with the user's real ~/.reasonix (no REASONIX_HOME
//     override) — sessions/history/config are shared per project.
//   - Each Start opens a fresh session (no --resume); Restart migrates
//     onto a fresh id and thus a fresh session.
//   - Tag env is injected into the serve process; tag preStart runs
//     before serve with REASONIX_HOME / REASONIX_STATE_HOME stripped
//     exactly like serve (host-exported overrides take effect only via
//     tag env, identically in both phases). The tag command is ignored.
//   - Stop tears down the serve process and the per-instance
//     management dir (the id is never reused).
//   - A live serve process survives a myworktree server restart and is
//     re-attached via Reattach (ReconcileRunningOnStartup).
//   - The record's Status stays "running" until Stop; a dead serve is
//     reported as framework.StatusUnhealthy (which the lifecycle
//     watcher does not turn into a terminal transition).
package reasonix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/redact"
)

// KindName is the framework registry name for the reasonix kind. Kept
// in sync with store.KindReasonix (a store-level constant; asserted by
// TestKindNameMatchesStoreConstant).
const KindName = "reasonix"

// preStartTimeout bounds tag preStart execution (see Spawn). Long
// enough for the documented `npm install` example, short enough that a
// hanging template cannot stall a Start request indefinitely.
const preStartTimeout = 2 * time.Minute

// Kind implements framework.Kind for reasonix serve instances.
type Kind struct {
	drv *Driver
}

// NewKind wraps a Driver into a framework.Kind. The caller owns the
// Driver (app.New creates one with DataDir/Logger) and registers the
// returned Kind with the framework registry.
func NewKind(drv *Driver) *Kind {
	return &Kind{drv: drv}
}

// Handle is the per-instance state: the instance id plus the framework
// Publisher used to persist the spawned PID.
type Handle struct {
	id  string
	pub framework.Publisher
}

// Manifest returns the static KindInfo for the reasonix kind.
func (k *Kind) Manifest() framework.KindInfo {
	return framework.KindInfo{
		Name:        KindName,
		Label:       "Reasonix-Web",
		Description: "Reasonix agent running in this worktree with its web chat UI.",
		Interactive: false,
	}
}

// Spawn starts the serve subprocess via the driver and waits until it
// is listening. Tag preStart runs inside the driver's Start via the
// PreStart callback so it sees the exact same environment as serve.
func (k *Kind) Spawn(ctx context.Context, params framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	if k.drv == nil {
		return framework.Handle{}, nil, errors.New("reasonix driver is not configured")
	}
	// Serve environment: inherited env minus REASONIX_HOME /
	// REASONIX_STATE_HOME (so preStart and serve resolve the same
	// ~/.reasonix; a host-exported override takes effect only via the
	// instance tag env), plus the tag env.
	env := RemoveEnv(os.Environ(), "REASONIX_HOME", "REASONIX_STATE_HOME")
	for key, val := range params.ExtraEnv {
		env = append(env, key+"="+val)
	}
	if _, err := k.drv.Start(StartInput{
		InstanceID:   params.InstanceID,
		WorktreePath: params.WorktreePath,
		Env:          env,
		PreStart: func() error {
			if strings.TrimSpace(params.PreStart) == "" {
				return nil
			}
			// Bounded: a hanging template must not stall Start forever
			// (Spawn has not returned, so the framework's ready-timeout has
			// not started either). Output is redacted before it is surfaced
			// to the caller (a debug preStart may echo tag env values).
			preCtx, cancel := context.WithTimeout(context.Background(), preStartTimeout)
			pre := exec.CommandContext(preCtx, "zsh", "-lc", params.PreStart)
			pre.Dir = params.WorktreePath
			pre.Env = env
			out, err := pre.CombinedOutput()
			cancel()
			if preCtx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("preStart timed out after %s", preStartTimeout)
			}
			if err != nil {
				return fmt.Errorf("preStart failed: %w: %s", err, strings.TrimSpace(redact.Text(string(out))))
			}
			return nil
		},
	}); err != nil {
		return framework.Handle{}, nil, err
	}

	ready := framework.NewReadySignal()
	ready.Close() // Driver.Start already waited for the listen port

	// The PID is persisted by SetPublishers (via Driver.Health) — Spawn
	// runs before the framework hands out the Publisher.
	return framework.NewHandle(KindName, &Handle{id: params.InstanceID}), ready, nil
}

// SetPublishers wires the framework Publisher and persists the serve
// PID so resource stats can sample the right process.
func (k *Kind) SetPublishers(handle framework.Handle, p framework.Publisher) {
	h := handle.Unwrap().(*Handle)
	h.pub = p
	if info, ok, _ := k.drv.Health(h.id); ok {
		_ = p.SetPID(info.PID)
	}
}

// Stop terminates the serve process and tears down the per-instance
// management dir (token/port/pid/serve.log) — parity with the
// v0.4.0 Stop: the id is never reused after Stop, so the state dir and
// its Start lock are dropped now instead of accumulating until Delete.
func (k *Kind) Stop(handle framework.Handle, graceSeconds int) error {
	h := handle.Unwrap().(*Handle)
	if err := k.drv.Stop(h.id); err != nil {
		return err
	}
	return k.drv.Cleanup(h.id)
}

// Status reports the driver's health view. A dead serve is Unhealthy,
// not terminal: like the v0.4.0 integration, only Stop transitions a
// reasonix record out of "running".
func (k *Kind) Status(handle framework.Handle) (framework.Status, string) {
	h := handle.Unwrap().(*Handle)
	if _, ok, err := k.drv.Health(h.id); err == nil && ok {
		return framework.StatusRunning, ""
	}
	return framework.StatusUnhealthy, "reasonix serve is not healthy"
}

// ReadLogs returns no data: reasonix instances have no ring buffer
// (parity with v0.4.0, where the log endpoints saw no reasonix
// output). The serve.log tail is used for startup failure messages.
func (k *Kind) ReadLogs(handle framework.Handle, since int64, maxBytes int64) (string, int64, error) {
	return "", since, nil
}

// KindBlob returns no blob: the auth token stays in driver memory and
// is never persisted (issue #46).
func (k *Kind) KindBlob(handle framework.Handle) (json.RawMessage, error) {
	return nil, nil
}

// HTTPHint returns "": the /rx/ reverse proxy is a global app-level
// route (both the independent loopback listener and the same-origin
// fallback), not a per-instance framework route.
func (k *Kind) HTTPHint(instanceID string) string { return "" }

// RegisterHTTP is a no-op (see HTTPHint).
func (k *Kind) RegisterHTTP(mux *http.ServeMux, instanceID string, handle framework.Handle) {}

// Cleanup wipes the per-instance serve-management dir. The framework
// calls this after Delete and after Restart migrates onto a fresh id;
// Stop already cleans, so this is a safety net — including a Stop of
// the serve process itself, for stale-stopped records whose process
// outlived the "stopped" marking (parity with the v0.4.0 Delete).
func (k *Kind) Cleanup(instanceID string) error {
	_ = k.drv.Stop(instanceID)
	return k.drv.Cleanup(instanceID)
}

// Reattach re-attaches to a live serve process after a myworktree
// server restart (RestartSurvivor). The driver verifies health from
// the persisted pid/port files; on success the framework keeps the
// record "running" and registers the handle so Stop/Delete can manage
// it.
func (k *Kind) Reattach(ctx context.Context, instanceID string) (framework.Handle, *framework.ReadySignal, error) {
	if k.drv == nil {
		return framework.Handle{}, nil, errors.New("reasonix driver is not configured")
	}
	if _, ok, err := k.drv.Health(instanceID); err != nil || !ok {
		if err != nil {
			return framework.Handle{}, nil, err
		}
		return framework.Handle{}, nil, fmt.Errorf("reasonix serve for %s is not healthy", instanceID)
	}
	ready := framework.NewReadySignal()
	ready.Close()
	return framework.NewHandle(KindName, &Handle{id: instanceID}), ready, nil
}
