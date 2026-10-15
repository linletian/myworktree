package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"myworktree/internal/framework"
	"myworktree/internal/instance/dsh_web"
	"myworktree/internal/instance/opencode_web"
	"myworktree/internal/store"
)

// scopeStubKind is a spawn-nothing Kind registered under the REAL web-UI
// kind names, so these tests exercise the production lifecycle handlers
// (Stop / Restart / Delete, and the framework bookkeeping behind them)
// without launching opencode or dsh. The names must match the store records
// because Manager.Restart resolves the kind from the record it is restarting.
type scopeStubKind struct{ name string }

func (k scopeStubKind) Manifest() framework.KindInfo {
	return framework.KindInfo{Name: k.name, Label: k.name}
}

func (k scopeStubKind) Spawn(ctx context.Context, p framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	ready := framework.NewReadySignal()
	ready.Close()
	return framework.NewHandle(k.name, p.InstanceID), ready, nil
}

func (k scopeStubKind) Stop(h framework.Handle, graceSeconds int) error { return nil }

func (k scopeStubKind) Status(h framework.Handle) (framework.Status, string) {
	return framework.StatusRunning, ""
}

func (k scopeStubKind) ReadLogs(h framework.Handle, since, max int64) (string, int64, error) {
	return "", since, nil
}

func (k scopeStubKind) HTTPHint(instanceID string) string { return "" }

func (k scopeStubKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h framework.Handle) {
}

func (k scopeStubKind) KindBlob(h framework.Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// newScopeResetServer builds a Server whose two scope trackers are the live
// ones the handlers read, backed by spawn-nothing kinds under the real kind
// names. It returns the instance ids it seeded.
func newScopeResetServer(t *testing.T, st store.State) *Server {
	t.Helper()
	dataDir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	reg := framework.NewRegistry()
	reg.Register(scopeStubKind{name: "opencode-web"})
	reg.Register(scopeStubKind{name: store.KindDsh})
	m := framework.NewManager(reg, fs, nil)
	m.DataDir = dataDir
	m.Root = dataDir
	return &Server{
		cfg:         Config{ListenAddr: "127.0.0.1:0"},
		dataDir:     dataDir,
		root:        dataDir,
		store:       fs,
		instanceMgr: m,
		ocScope:     opencode_web.NewScopeTracker(),
		dshScope:    dsh_web.NewScopeTracker(),
		mux:         http.NewServeMux(),
		authFails:   map[string]authFail{},
	}
}

// postInstance calls an instance-teardown endpoint on the handler under test
// (the mux is not mounted here, so the handler is invoked directly — the
// same shape the existing app handler tests use).
func postInstance(t *testing.T, h func(http.ResponseWriter, *http.Request), path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// TestHandleInstanceStopClearsScopeTrackers pins the S1 wiring: a recorded
// out-of-scope state must not outlive the instance it describes. Both
// trackers are asserted because dropScopeState owns both, and a SURVIVING
// second instance is asserted so the test cannot pass by wiping everything.
//
// Non-vacuity, stated exactly: before the wiring the two Get calls returned
// ok=true forever (Reset had no production caller), so this fails red if any
// of the dropScopeState calls is removed.
func TestHandleInstanceStopClearsScopeTrackers(t *testing.T) {
	srv := newScopeResetServer(t, store.State{
		Version: 1,
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: t.TempDir()},
		},
		Instances: []store.ManagedInstance{
			managedTestInstance("oc-stop", "opencode-web", "/wt", "running", nil),
			managedTestInstance("dsh-stop", store.KindDsh, "/wt", "running", nil),
			managedTestInstance("oc-keep", "opencode-web", "/wt", "running", nil),
		},
	})

	// Record on both trackers, plus the untouched neighbour.
	srv.ocScope.Record("oc-stop", opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/elsewhere"})
	srv.dshScope.Record("dsh-stop", dsh_web.ScopeState{Scope: dsh_web.ScopeOutOfScope, Directory: "/elsewhere"})
	srv.ocScope.Record("oc-keep", opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/kept"})

	if _, ok := srv.ocScope.Get("oc-stop"); !ok {
		t.Fatal("precondition: opencode state was not recorded")
	}
	if _, ok := srv.dshScope.Get("dsh-stop"); !ok {
		t.Fatal("precondition: dsh state was not recorded")
	}

	w := postInstance(t, srv.handleInstanceStop, "/api/instances/stop", `{"id":"oc-stop"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("stop oc-stop: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.ocScope.Get("oc-stop"); ok {
		t.Error("opencode tracker still holds oc-stop after Stop")
	}

	w = postInstance(t, srv.handleInstanceStop, "/api/instances/stop", `{"id":"dsh-stop"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("stop dsh-stop: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.dshScope.Get("dsh-stop"); ok {
		t.Error("dsh tracker still holds dsh-stop after Stop")
	}

	// The third instance was never touched — its warning must survive, which
	// is what makes the two clears above a targeted delete rather than a
	// coincidental empty map.
	if _, ok := srv.ocScope.Get("oc-keep"); !ok {
		t.Error("untouched instance oc-keep lost its scope state")
	}
}

// TestHandleInstanceDeleteClearsScopeTrackers pins the Delete wiring, and
// with it the crash backstop: a process that died on its own never reaches
// the Stop handler, so Delete is where that residue is reclaimed. The
// instance is seeded stopped to be exactly that shape.
func TestHandleInstanceDeleteClearsScopeTrackers(t *testing.T) {
	srv := newScopeResetServer(t, store.State{
		Version: 1,
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: t.TempDir()},
		},
		Instances: []store.ManagedInstance{
			managedTestInstance("oc-del", "opencode-web", "/wt", "stopped", nil),
			managedTestInstance("dsh-del", store.KindDsh, "/wt", "stopped", nil),
		},
	})

	srv.ocScope.Record("oc-del", opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/elsewhere"})
	srv.dshScope.Record("dsh-del", dsh_web.ScopeState{Scope: dsh_web.ScopeOutOfScope, Directory: "/elsewhere"})

	w := postInstance(t, srv.handleInstanceDelete, "/api/instances/delete", `{"id":"oc-del"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("delete oc-del: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.ocScope.Get("oc-del"); ok {
		t.Error("opencode tracker still holds oc-del after Delete")
	}

	w = postInstance(t, srv.handleInstanceDelete, "/api/instances/delete", `{"id":"dsh-del"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("delete dsh-del: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.dshScope.Get("dsh-del"); ok {
		t.Error("dsh tracker still holds dsh-del after Delete")
	}
}

// TestHandleInstanceRestartClearsOldIdOnly pins the Restart trade-off: the
// teardown belongs to the OLD id, and the fresh id Restart returns must NOT
// be wiped (it is the session that is only now starting, and wiping it is at
// best a no-op and at worst a race with the proxy's first record).
//
// F-M4 — why the assertions cross calls: recording on the NEW id AFTER the
// handler ran cannot detect a handler that also wiped the new id (the record
// simply lands after the damage). So the test instead seeds a SECOND stale id
// before the restart and asserts the handler dropped exactly ONE entry —
// the old one — while the decoration survives and the fresh id never had a
// state to lose. A handler that also wiped the decoration (or dropped both)
// drops two and fails here.
func TestHandleInstanceRestartClearsOldIdOnly(t *testing.T) {
	srv := newScopeResetServer(t, store.State{
		Version: 1,
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: t.TempDir()},
		},
		Instances: []store.ManagedInstance{
			managedTestInstance("oc-old", "opencode-web", "/wt", "stopped", nil),
			managedTestInstance("dsh-old", store.KindDsh, "/wt", "stopped", nil),
		},
	})

	srv.ocScope.Record("oc-old", opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/elsewhere"})
	srv.dshScope.Record("dsh-old", dsh_web.ScopeState{Scope: dsh_web.ScopeOutOfScope, Directory: "/elsewhere"})
	// A second, unrelated stale id in the SAME tracker — the canary. The
	// restart of oc-old has no business touching it.
	srv.ocScope.Record("oc-decoration", opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/decoration"})
	// The tracker length BEFORE the handler runs: the two seeded ids.
	before := ocScopePresent(srv, "oc-old", "oc-decoration")
	if before != 2 {
		t.Fatalf("precondition: tracker holds %d of the seeded ids, want 2", before)
	}

	w := postInstance(t, srv.handleInstanceRestart, "/api/instances/restart", `{"id":"oc-old"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("restart oc-old: status %d, want 201 (%s)", w.Code, w.Body.String())
	}
	var view map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("restart response: %v", err)
	}
	newID, _ := view["id"].(string)
	if newID == "" || newID == "oc-old" {
		t.Fatalf("restart did not migrate onto a fresh id (got %q)", newID)
	}
	if _, ok := srv.ocScope.Get("oc-old"); ok {
		t.Error("opencode tracker still holds the old id after Restart")
	}

	// F-M4: the canary SURVIVES, and the tracker lost exactly one entry.
	// ScopeTracker exposes Get/Record/Reset only, so "length" is probed over
	// the id universe under test — every id this test seeded, plus the fresh
	// id Restart allocated. The handler can only touch the id it was given
	// (dropped) and the fresh id (never recorded), so the count over that
	// universe is the observable tracker length here.
	if _, ok := srv.ocScope.Get("oc-decoration"); !ok {
		t.Error("the seeded decoration id lost its scope state — the restart wiped an unrelated entry")
	}
	after := ocScopePresent(srv, "oc-old", "oc-decoration", newID)
	if before-after != 1 {
		t.Errorf("tracker length dropped by %d entries, want exactly 1 (old id only)", before-after)
	}

	// Quiesce BEFORE the next restart. Restart spawned a live instance whose
	// runLifecycle goroutine writes the store, and persistNewInstance gives up
	// on a version conflict after 3 × 10 ms — a documented framework race, not
	// something this test may paper over in the framework. Two restarts
	// back-to-back manufacture that contention; Stop waits for the goroutine
	// (ri.wg.Wait), so the writer is gone before the second Restart runs.
	// Stopping does not touch the trackers, so every assertion below stands.
	if err := srv.instanceMgr.Stop(newID); err != nil {
		t.Fatalf("stop %s: %v", newID, err)
	}

	w = postInstance(t, srv.handleInstanceRestart, "/api/instances/restart", `{"id":"dsh-old"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("restart dsh-old: status %d, want 201 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.dshScope.Get("dsh-old"); ok {
		t.Error("dsh tracker still holds the old id after Restart")
	}
	var view2 map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &view2); err != nil {
		t.Fatalf("dsh restart response: %v", err)
	}
	if dshNewID, _ := view2["id"].(string); dshNewID != "" {
		if err := srv.instanceMgr.Stop(dshNewID); err != nil {
			t.Fatalf("stop %s: %v", dshNewID, err)
		}
	}

	// A handler that ALSO dropped the fresh id would still pass the canary
	// check above (the fresh id had no state yet) — so record on it now and
	// prove the tracker still accepts it: the fresh session is the proxy's
	// to write, and nothing in the restart path may have poisoned it.
	srv.ocScope.Record(newID, opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/elsewhere"})
	if _, ok := srv.ocScope.Get(newID); !ok {
		t.Errorf("fresh id %q must keep its own state", newID)
	}
	if ocScopePresent(srv, "oc-old", "oc-decoration", newID) != 2 {
		t.Errorf("tracker should hold exactly the canary + fresh id: %v", ocScopePresent(srv, "oc-old", "oc-decoration", newID))
	}
}

// ocScopePresent counts how many of the given ids the opencode tracker still
// holds — the observable "length" of the tracker over the universe under
// test (see the F-M4 comment in TestHandleInstanceRestartClearsOldIdOnly).
func ocScopePresent(srv *Server, ids ...string) int {
	n := 0
	for _, id := range ids {
		if _, ok := srv.ocScope.Get(id); ok {
			n++
		}
	}
	return n
}

// TestTeardownRefusalKeepsScopeState pins the other half of the wiring
// decision: the wipe sits AFTER the Manager call precisely so a REFUSED
// teardown leaves the live instance's warning intact. The instance has to be
// genuinely running for the refusal to be real — Manager.Delete refuses on the
// framework's running map, not on the status string in the store, so a seeded
// "running" record nothing ever spawned would be deleted without complaint.
// Start allocates the id, so that id (not a hand-written one) is under test.
func TestTeardownRefusalKeepsScopeState(t *testing.T) {
	srv := newScopeResetServer(t, store.State{
		Version: 1,
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: t.TempDir()},
		},
	})
	inst, err := srv.instanceMgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1", Kind: "opencode-web", Name: "oc-run",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = srv.instanceMgr.Stop(inst.ID) })

	srv.ocScope.Record(inst.ID, opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/elsewhere"})

	// Delete refuses a running instance.
	w := postInstance(t, srv.handleInstanceDelete, "/api/instances/delete", `{"id":"`+inst.ID+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete of a running instance: status %d, want 400 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.ocScope.Get(inst.ID); !ok {
		t.Error("a refused delete must leave the live instance's warning in place")
	}

	// Restart refuses it too (running is not a restartable state).
	w = postInstance(t, srv.handleInstanceRestart, "/api/instances/restart", `{"id":"`+inst.ID+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("restart of a running instance: status %d, want 400 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.ocScope.Get(inst.ID); !ok {
		t.Error("a refused restart must leave the live instance's warning in place")
	}

	// And the Stop that finally does tear it down clears what both refusals
	// preserved — so the two assertions above are the shape of the refusal,
	// not a leak.
	w = postInstance(t, srv.handleInstanceStop, "/api/instances/stop", `{"id":"`+inst.ID+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("stop: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.ocScope.Get(inst.ID); ok {
		t.Error("the accepted Stop must clear the state the refusals preserved")
	}

	// F-M5: the third refusal shape — a Stop that FAILS. Delete and Restart
	// refuse on the Manager's own state; Stop has no such gate (and
	// framework.Manager.Stop swallows a kind's error), so the failure is
	// driven through the managerStop seam. The wiring must return BEFORE
	// dropScopeState: a live instance whose teardown failed keeps its
	// warning, exactly like the two refusals above. Moving the wipe before
	// the Manager call turns this RED.
	t.Run("stop failure keeps the scope state", func(t *testing.T) {
		srv := newScopeResetServer(t, store.State{
			Version: 1,
			Worktrees: []store.ManagedWorktree{
				{ID: "wt1", Name: "wt1", Path: t.TempDir()},
			},
		})
		inst, err := srv.instanceMgr.Start(context.Background(), framework.StartParams{
			WorktreeID: "wt1", Kind: "opencode-web", Name: "oc-run",
		})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		t.Cleanup(func() { _ = srv.instanceMgr.Stop(inst.ID) })

		srv.ocScope.Record(inst.ID, opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/elsewhere"})

		oldStop := managerStop
		t.Cleanup(func() { managerStop = oldStop })
		managerStop = func(*framework.Manager, string) error { return errors.New("stop failed: injected") }

		w := postInstance(t, srv.handleInstanceStop, "/api/instances/stop", `{"id":"`+inst.ID+`"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("stop of a failing instance: status %d, want 400 (%s)", w.Code, w.Body.String())
		}
		if _, ok := srv.ocScope.Get(inst.ID); !ok {
			t.Error("a failed Stop must leave the live instance's warning in place (F-M5)")
		}

		// Non-vacuity: with the seam restored the SAME handler accepts the
		// Stop and clears the state — the retained warning above is the
		// failure's doing, not a wiring that never clears anything.
		managerStop = oldStop
		w = postInstance(t, srv.handleInstanceStop, "/api/instances/stop", `{"id":"`+inst.ID+`"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("stop after restore: status %d, want 200 (%s)", w.Code, w.Body.String())
		}
		if _, ok := srv.ocScope.Get(inst.ID); ok {
			t.Error("the accepted Stop must clear the state the failed one preserved")
		}
	})
}

// TestMCPCallTeardownClearsScopeTrackers pins the second door: the MCP /
// CLI verbs reach the very same Manager.Stop / Manager.Delete, so an unwired
// path here would leave the leak fully intact for every non-browser client.
func TestMCPCallTeardownClearsScopeTrackers(t *testing.T) {
	srv := newScopeResetServer(t, store.State{
		Version: 1,
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: t.TempDir()},
		},
		Instances: []store.ManagedInstance{
			managedTestInstance("oc-mcp", "opencode-web", "/wt", "stopped", nil),
			managedTestInstance("dsh-mcp", store.KindDsh, "/wt", "stopped", nil),
		},
	})

	srv.ocScope.Record("oc-mcp", opencode_web.ScopeState{Scope: opencode_web.ScopeOutOfScope, Directory: "/elsewhere"})
	srv.dshScope.Record("dsh-mcp", dsh_web.ScopeState{Scope: dsh_web.ScopeOutOfScope, Directory: "/elsewhere"})

	call := func(tool, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/mcp/call",
			strings.NewReader(`{"tool":"`+tool+`","args":{"id":"`+id+`"}}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.handleMCPCall(w, req)
		return w
	}

	if w := call("instance_stop", "oc-mcp"); w.Code != http.StatusOK {
		t.Fatalf("instance_stop oc-mcp: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.ocScope.Get("oc-mcp"); ok {
		t.Error("opencode tracker still holds oc-mcp after MCP instance_stop")
	}

	if w := call("instance_delete", "dsh-mcp"); w.Code != http.StatusOK {
		t.Fatalf("instance_delete dsh-mcp: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, ok := srv.dshScope.Get("dsh-mcp"); ok {
		t.Error("dsh tracker still holds dsh-mcp after MCP instance_delete")
	}
}
