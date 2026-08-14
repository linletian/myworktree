package framework

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunLifecycle_DropsBufferOnTerminalStatus guards against the
// regression where Manager.runLifecycle exited without dropping the
// per-instance ring buffer: m.buffers[id] would hold a stale
// *RingBuffer for every instance that exited via lifecycle (Stop /
// ready-timeout / kind reported terminal status). PTY instances use
// ~4-32 MiB buffers; a long-running daemon accumulated dead buffers
// proportional to total stop/start cycles.
//
// Per docs/ARCHITECTURE.md §"Buffer lifecycle", Stop / wait / Restart /
// Delete all funnel into dropBuffer — which is what runLifecycle's
// defer now does.
func TestRunLifecycle_DropsBufferOnTerminalStatus(t *testing.T) {
	t.Parallel()
	k := fakeKindPTY()
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "fake-pty", Name: "t1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the instance to reach running — that is when runLifecycle
	// enters its terminal-status poll.
	for time.Now().Add(5 * time.Second).After(time.Now()) {
		got, err := mgr.Get(inst.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == StatusRunning.String() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Sanity: buffer is allocated while running.
	if _, ok := mgr.buffers[inst.ID]; !ok {
		t.Fatalf("pre-condition: m.buffers[%q] missing while running", inst.ID)
	}
	if got := mgr.totalBufBytes.Load(); got <= 0 {
		t.Fatalf("pre-condition: totalBufBytes=%d, want > 0", got)
	}

	// Make the kind report a terminal status on the next poll. The poll
	// ticker is 2 s; allow up to 5 s for runLifecycle to notice and exit.
	k.mu.Lock()
	k.status = StatusExited
	k.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mgr.stateMu.Lock()
		_, stillThere := mgr.buffers[inst.ID]
		mgr.stateMu.Unlock()
		if !stillThere {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mgr.stateMu.Lock()
	_, stillThere := mgr.buffers[inst.ID]
	mgr.stateMu.Unlock()
	if stillThere {
		t.Fatalf("m.buffers[%q] still present after runLifecycle exit; expected dropBuffer to fire", inst.ID)
	}
	if got := mgr.totalBufBytes.Load(); got != 0 {
		t.Fatalf("totalBufBytes=%d after lifecycle exit, want 0", got)
	}
}

// TestRunLifecycle_DropsBufferOnReadyTimeout covers the 60s ready-timeout
// branch in runLifecycle — that path also needs to drop the buffer,
// not just decrement the cap counter.
func TestRunLifecycle_DropsBufferOnReadyTimeout(t *testing.T) {
	t.Parallel()
	k := fakeKindBlocking() // blockReady=true → ready never closes
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "fake-block", Name: "t1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Sanity: buffer is allocated while starting.
	if _, ok := mgr.buffers[inst.ID]; !ok {
		t.Fatalf("pre-condition: m.buffers[%q] missing while starting", inst.ID)
	}

	// Cancel the lifecycle context — runLifecycle hits the <-ctx.Done()
	// branch (the timeout branch is exercised by integration / manual
	// runs; both paths share the same defer, so covering the ctx-cancel
	// branch proves the dropBuffer-on-exit contract).
	cancel, ok := mgr.running[inst.ID]
	if !ok {
		t.Fatalf("instance not in m.running")
	}
	cancel.cancel()

	// Wait for the lifecycle goroutine to exit and drop the buffer.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mgr.stateMu.Lock()
		_, stillThere := mgr.buffers[inst.ID]
		mgr.stateMu.Unlock()
		if !stillThere {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mgr.stateMu.Lock()
	_, stillThere := mgr.buffers[inst.ID]
	mgr.stateMu.Unlock()
	if stillThere {
		t.Fatalf("m.buffers[%q] still present after ctx cancel; expected dropBuffer to fire", inst.ID)
	}
	if got := mgr.totalBufBytes.Load(); got != 0 {
		t.Fatalf("totalBufBytes=%d after ctx cancel, want 0", got)
	}
}

// TestRunLifecycle_ReadyTimeoutStopsInstance pins the 60s ready-timeout
// branch: on timeout the framework must call the kind's Stop (the
// kind contract — Stop exactly once after a successful Spawn —
// otherwise the child process and the kind's goroutines leak with no
// way to stop them, because the id is removed from m.running), and
// only then mark the instance failed and drop the buffer.
func TestRunLifecycle_ReadyTimeoutStopsInstance(t *testing.T) {
	t.Parallel()
	k := fakeKindBlocking() // blockReady=true → ready never closes
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())
	mgr.readyTimeout = 200 * time.Millisecond

	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "fake-block", Name: "t1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the timeout branch to run: status flips to failed and the
	// instance leaves m.running.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		_, stillRunning := mgr.running[inst.ID]
		mgr.mu.Unlock()
		if !stillRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The kind must have observed exactly one Stop.
	select {
	case <-k.stopped:
	default:
		t.Fatal("ready-timeout branch did not call kind.Stop; child process would leak")
	}

	got, err := mgr.Get(inst.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusFailed.String() {
		t.Fatalf("status=%s, want failed", got.Status)
	}
	if !strings.Contains(got.LastError, "did not become ready") {
		t.Fatalf("LastError=%q, want ready-timeout reason", got.LastError)
	}

	// Buffer must be dropped by the same defer.
	mgr.stateMu.Lock()
	_, stillThere := mgr.buffers[inst.ID]
	mgr.stateMu.Unlock()
	if stillThere {
		t.Fatalf("m.buffers[%q] still present after ready-timeout", inst.ID)
	}
	if got := mgr.totalBufBytes.Load(); got != 0 {
		t.Fatalf("totalBufBytes=%d after ready-timeout, want 0", got)
	}
}

// TestStop_DropsBuffer guards the user-visible Stop path — Stop →
// cancel lifecycle ctx → runLifecycle exits → dropBuffer must fire
// (same contract as the terminal-status branch, end-to-end).
func TestStop_DropsBuffer(t *testing.T) {
	t.Parallel()
	k := fakeKindPTY()
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	inst, _ := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "fake-pty"})
	for time.Now().Add(5 * time.Second).After(time.Now()) {
		got, _ := mgr.Get(inst.ID)
		if got.Status == StatusRunning.String() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := mgr.Stop(inst.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Stop waits on ri.wg.Wait(); by the time it returns, runLifecycle's
	// defer has already fired.
	mgr.stateMu.Lock()
	_, stillThere := mgr.buffers[inst.ID]
	mgr.stateMu.Unlock()
	if stillThere {
		t.Fatalf("m.buffers[%q] still present after Stop", inst.ID)
	}
	if got := mgr.totalBufBytes.Load(); got != 0 {
		t.Fatalf("totalBufBytes=%d after Stop, want 0", got)
	}
}
