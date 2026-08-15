package dsh_web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Workspace bootstrap (PLAN.md §数据面): dsh's workspace registry only
// adopts a directory via the workspace.create RPC — the process cwd and
// session.create do NOT register it (upstream
// packages/workspace/workspace/src/index.ts). Without the bootstrap the
// sidebar would show an empty workspace list on first run, breaking
// "启动就是对应工作区". After the ready line, the driver POSTs the RPC
// envelope DIRECTLY to the upstream loopback (Go's client sends no
// Origin → passes the /api trust fence) and records the worktree's own
// workspace id in the blob (scope classification by workspaceId).
// Failures are warn-only: the instance still runs, the sidebar just
// shows an empty workspace list until the user adopts one manually.
//
// bootstrapCreateSession optionally preseeds a blank session so the UI
// lands on a session page right away.

const (
	bootstrapCreateSession = true
	bootstrapAttempts      = 3
	bootstrapBackoff       = 2 * time.Second
	bootstrapTimeout       = 5 * time.Second
)

type rpcResponse struct {
	Type  string `json:"type"`
	RPCID string `json:"rpcId"`
	// Result.ok is the wire truth; Value is parsed by the caller.
	Result struct {
		OK    bool            `json:"ok"`
		Value json.RawMessage `json:"value"`
	} `json:"result"`
}

// bootstrap adopts the worktree into the instance's workspace registry
// once the upstream is ready. Runs in its own goroutine; failures log
// and never block the instance.
func (d *Driver) bootstrap(ctx context.Context, h *Handle) {
	select {
	case <-h.ready.Channel():
	case <-ctx.Done():
		return
	}
	h.mu.Lock()
	host, port := h.host, h.port
	h.mu.Unlock()
	if host == "" || port == "" {
		return
	}
	base := "http://" + net.JoinHostPort(host, port)

	if wsID, ok := d.createWorkspace(ctx, base, h.cwd); ok {
		h.mu.Lock()
		changed := h.blob.WorkspaceID != wsID
		h.blob.WorkspaceID = wsID
		blob, err := json.Marshal(h.blob)
		h.mu.Unlock()
		if changed && err == nil {
			if p := h.loadPublisher(); p != nil {
				_ = p.UpdateKindBlob(blob)
			}
		}
	}
	if bootstrapCreateSession {
		d.createSession(ctx, base, h.cwd)
	}
}

// createWorkspace adopts the worktree path and returns the workspace
// id from the response value ({workspace: {workspaceId, …}, created}).
func (d *Driver) createWorkspace(ctx context.Context, base, worktree string) (string, bool) {
	payload := map[string]string{"path": worktree}
	var value struct {
		Workspace struct {
			WorkspaceID string `json:"workspaceId"`
		} `json:"workspace"`
	}
	if err := d.callRPC(ctx, base, "workspace.create", payload, &value); err != nil {
		d.logf("dsh: workspace bootstrap failed (warn-only): %v", err)
		return "", false
	}
	return value.Workspace.WorkspaceID, true
}

// createSession preseeds a blank session for the worktree so the UI
// lands on a session page immediately (optional, warn-only).
func (d *Driver) createSession(ctx context.Context, base, worktree string) {
	payload := map[string]string{"cwd": worktree}
	if err := d.callRPC(ctx, base, "session.create", payload, nil); err != nil {
		d.logf("dsh: session preseed failed (warn-only): %v", err)
	}
}

// callRPC performs one unary RPC against the upstream with retries —
// the ready line can appear before the API surface is fully up.
//
// WIRE CONTRACT (verified against the installed dsh): the endpoint is
// derived from the URL PATH — POST /api/<method> — and the envelope's
// `method` field must equal the endpoint (rpcFetchHandler: "method …
// does not match endpoint"); posting the envelope to bare /api returns
// 404 "not found". Content-Type must be application/json.
func (d *Driver) callRPC(ctx context.Context, base, method string, payload any, value any) error {
	env, err := json.Marshal(map[string]any{
		"type":    "client-request",
		"rpcId":   "mw-" + method,
		"method":  method,
		"payload": payload,
	})
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < bootstrapAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(bootstrapBackoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		reqCtx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/api/"+method, bytes.NewReader(env))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("dsh: %s returned %d: %s", method, resp.StatusCode, truncate(string(body), 300))
			continue
		}
		var rpc rpcResponse
		if err := json.Unmarshal(body, &rpc); err != nil {
			lastErr = fmt.Errorf("dsh: %s response parse: %w", method, err)
			continue
		}
		if !rpc.Result.OK {
			lastErr = fmt.Errorf("dsh: %s rejected: %s", method, truncate(string(body), 300))
			continue
		}
		if value != nil && len(rpc.Result.Value) > 0 {
			if err := json.Unmarshal(rpc.Result.Value, value); err != nil {
				return fmt.Errorf("dsh: %s value parse: %w", method, err)
			}
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dsh: %s failed after %d attempts", method, bootstrapAttempts)
	}
	return lastErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
