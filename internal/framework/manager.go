package framework

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"myworktree/internal/store"
	"myworktree/internal/tag"
)

// MainWorktreeID is the sentinel ID for the primary worktree (the
// git repo itself, not a `git worktree add`-created sibling). Kept
// in the framework package because every kind needs to interpret it
// to resolve the absolute worktree path.
const MainWorktreeID = "__main__"

// ErrInstanceNotFound is returned by Get / Stop / Tail / etc. when the supplied id does not match any instance.
var ErrInstanceNotFound = errors.New("unknown instance id")

// InstanceStore is the subset of store.FileStore the Manager needs.
// Defined here so tests can supply a fake store that injects errors.
type InstanceStore interface {
	Load() (store.State, error)
	SaveWithVersion(st store.State, expectedVersion int64) error
}

// Manager is the single instance-lifecycle coordinator. One per
// server. Spawns new instances by delegating to the registered kind,
// watches them via a single goroutine per instance that owns the
// kind's Handle and the ReadySignal.
//
// Lock order (if a function must hold more than one of mu / connsMu /
// stateMu at once, acquire them in this order to avoid deadlock):
//
//	mu → connsMu → stateMu
//
// Single-lock acquisitions may take any of the three; the order only
// matters when nesting (e.g. Manager.Delete at methods.go:55-60).
type Manager struct {
	Registry *Registry

	Store InstanceStore
	// Logger is the structured logger kinds and the proxy use for
	// diagnostic output. NewManager defaults this to io.Discard; operators
	// must wire a real logger here to see per-request proxy lines and
	// per-instance state-transition logs.
	Logger  *log.Logger
	Root    string // git repo root for MainWorktreeID resolution
	DataDir string

	// AuthToken is the myworktree bearer token, reused by kinds that
	// need an upstream credential (opencode-web). Empty means no
	// token is injected; kinds that require one will surface that.
	AuthToken string

	// Tags resolves tag.Env / tag.preStart / tag.Command / tag.Cwd at
	// Start time (restored tag semantics shared by every kind). The
	// zero value disables tag resolution: Start with a TagID then
	// errors instead of silently ignoring the tag.
	Tags tag.Manager

	// LogBufferBytes is the per-instance ring buffer cap from config. 0 means use adaptive sizing.
	LogBufferBytes int64

	memSampler MemSampler

	stateMu       sync.Mutex
	mu            sync.Mutex
	running       map[string]*runningInstance // id -> live instance state
	totalBufBytes atomic.Int64

	// buffers holds the per-instance ring buffer pointers used by kinds that capture output (PTY).
	buffers map[string]*atomic.Pointer[RingBuffer]

	// subscribers holds the active output-subscriber channels per instance.
	subscribers map[string]map[chan string]struct{}

	// conns tracks the active transport per instance ("ws" / "sse" / "").
	conns   map[string]string
	connsMu sync.Mutex

	// stopGraceSeconds is the grace period before SIGKILL during Stop. Defaults to 5s.
	stopGraceSeconds int

	// httpHandler is the server-supplied mux onto which kinds register HTTP routes via RegisterHTTP. nil disables HTTP routing.
	httpHandler *http.ServeMux
}

type runningInstance struct {
	handle Handle
	ready  *ReadySignal
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

// NewManager constructs a Manager. Registry defaults to framework.Default if nil.
func NewManager(reg *Registry, s InstanceStore, logger *log.Logger) *Manager {
	if reg == nil {
		reg = Default
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Manager{
		Registry:         reg,
		Store:            s,
		Logger:           logger,
		stopGraceSeconds: 5,
	}
}

// SetMemorySampler overrides the production gopsutil sampler. Tests use this.
func (m *Manager) SetMemorySampler(s MemSampler) {
	m.memSampler = s
}

// SetHTTPHandler wires the kind-registration target. Production code passes the server's main mux here.
func (m *Manager) SetHTTPHandler(mux *http.ServeMux) {
	m.httpHandler = mux
}

// StartParams is the user-facing input to Manager.Start. It mirrors the pre-refactor instance.StartInput.
type StartParams struct {
	WorktreeID string
	Root       string // optional override (used for MainWorktreeID)
	TagID      string
	Command    string // ad-hoc initial command (ignored when TagID is set)
	Name       string
	Kind       string // "pty", "opencode-web", ...; must be registered
	ExtraEnv   map[string]string
}

// Start launches a new instance.
func (m *Manager) Start(ctx context.Context, in StartParams) (store.ManagedInstance, error) {
	if strings.TrimSpace(in.WorktreeID) == "" {
		return store.ManagedInstance{}, errors.New("worktree_id is required")
	}
	kindName := strings.TrimSpace(in.Kind)
	if kindName == "" {
		kindName = "pty"
	}

	k, err := m.Registry.Get(kindName)
	if err != nil {
		return store.ManagedInstance{}, fmt.Errorf("kind %q not registered", kindName)
	}

	// Restored tag semantics (pre-kind-refactor parity): a TagID loads
	// the tag and resolves its command / cwd / preStart / env; an empty
	// TagID with a Command is an ad-hoc start; both empty is an idle
	// shell. Tag command-required enforcement is left to the kind (PTY
	// requires one; opencode-web / reasonix deliberately ignore it).
	tagID := strings.TrimSpace(in.TagID)
	if tagID == "adhoc" || tagID == "idle" {
		// Pre-refactor sentinel values persisted on the record and passed
		// back by Restart; treat them as "no tag".
		tagID = ""
	}
	var (
		command        string
		cwdRel         string
		preStart       string
		extraEnv       map[string]string
		effectiveTagID = tagID
	)
	if tagID != "" {
		if m.Tags.GlobalPath == "" && m.Tags.ProjectPath == "" {
			return store.ManagedInstance{}, errors.New("tag support is not configured")
		}
		tags, err := m.Tags.LoadMerged()
		if err != nil {
			return store.ManagedInstance{}, err
		}
		t, ok := tags[tagID]
		if !ok {
			return store.ManagedInstance{}, fmt.Errorf("unknown tag id: %s", tagID)
		}
		command = t.Command
		cwdRel = t.Cwd
		preStart = t.PreStart
		extraEnv = t.Env
	} else {
		effectiveTagID = "adhoc"
		command = strings.TrimSpace(in.Command)
		if command == "" {
			effectiveTagID = "idle"
		}
	}

	wtPath, wtName, err := m.resolveWorktree(in)
	if err != nil {
		return store.ManagedInstance{}, err
	}
	if strings.TrimSpace(cwdRel) != "" && cwdRel != "." {
		wtPath = filepath.Join(wtPath, cwdRel)
	}

	capBytes, err := ResolveCap(m.LogBufferBytes, m.sampler(), m.totalBufBytes.Load())
	if err != nil {
		return store.ManagedInstance{}, err
	}

	id, err := newID()
	if err != nil {
		return store.ManagedInstance{}, err
	}

	instName := strings.TrimSpace(in.Name)
	if instName == "" {
		instName = effectiveTagID
	}
	if len(extraEnv) == 0 {
		extraEnv = nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	inst := store.ManagedInstance{
		ID:           id,
		WorktreeID:   in.WorktreeID,
		WorktreeName: wtName,
		TagID:        effectiveTagID,
		Name:         instName,
		Command:      command,
		Cwd:          wtPath,
		Env:          extraEnv,
		Kind:         kindName,
		Status:       StatusStarting.String(),
		CreatedAt:    now,
	}
	if err := m.persistNewInstance(inst); err != nil {
		return store.ManagedInstance{}, err
	}

	// Pre-allocate the per-instance ring buffer so kinds that capture
	// output (PTY) write into the SAME buffer the framework tracks.
	// AllocateBuffer already adds capBytes to totalBufBytes; the
	// explicit m.totalBufBytes.Add below was the old (pre-AllocateBuffer)
	// bookkeeping and would double-count if left in place.
	buf := m.AllocateBuffer(id, capBytes)

	params := SpawnParams{
		WorktreeID:   in.WorktreeID,
		WorktreePath: wtPath,
		WorktreeName: wtName,
		TagID:        tagID,
		Name:         instName,
		AuthToken:    m.AuthToken,
		ExtraEnv:     extraEnv,
		Command:      command,
		PreStart:     preStart,
		InstanceID:   id,
		Buffer:       buf,
	}

	handle, ready, err := k.Spawn(ctx, params)
	if err != nil {
		m.dropBuffer(id)
		m.markFailed(id, err)
		return store.ManagedInstance{}, err
	}

	// Kind-specific publisher wiring.
	if binder, ok := k.(PublisherBinder); ok {
		binder.SetPublishers(handle, &kindPublisher{m: m, id: id})
	}

	// Per-instance HTTP routing is opt-in. For opencode-web the proxy is registered globally in app.go.
	if prefix := k.HTTPHint(id); prefix != "" && m.httpHandler != nil {
		kindsMux := http.NewServeMux()
		k.RegisterHTTP(kindsMux, id, handle)
		m.httpHandler.Handle(prefix, http.StripPrefix(prefix, kindsMux))
	}

	runCtx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(1)

	ri := &runningInstance{handle: handle, ready: ready, cancel: cancel, wg: wg}

	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*runningInstance{}
	}
	m.running[id] = ri
	m.mu.Unlock()

	go m.runLifecycle(runCtx, inst, ri)

	return inst, nil
}

// runLifecycle waits for ReadySignal then polls the kind for terminal status.
func (m *Manager) runLifecycle(ctx context.Context, inst store.ManagedInstance, ri *runningInstance) {
	defer ri.wg.Done()
	// Funnel buffer cleanup through dropBuffer (the same path used by
	// Stop / Delete / Restart per docs/ARCHITECTURE.md §"Buffer
	// lifecycle"): Swap(nil) the ring buffer pointer, decrement
	// totalBufBytes via rb.CapBytes(), and delete the map entry.
	// Previously this deferred only m.totalBufBytes.Add(-capBytes),
	// which left m.buffers[id] holding a stale *RingBuffer for every
	// instance that exited via lifecycle (Stop / ready-timeout / kind
	// reported terminal status). Per-instance PTY buffers are ~4 MiB;
	// long-running daemons accumulated dead buffers proportional to
	// total stop/start cycles.
	defer m.dropBuffer(inst.ID)

	select {
	case <-ri.ready.Channel():
		m.setStatus(inst.ID, StatusRunning.String(), "")
	case <-ctx.Done():
		return
	case <-time.After(60 * time.Second):
		// TODO(kinds): surface per-kind timeouts via ReadySignal context or
		// a SpawnParams knob. PTY is ready in <1s, opencode-web in 1-3s; a
		// slow CI box can need 30s+ for opencode-web to compile its plugin
		// chain. The 60s value is empirically safe but is a ceiling, not a
		// target.
		m.markFailed(inst.ID, errors.New("instance did not become ready within 60s"))
		m.mu.Lock()
		delete(m.running, inst.ID)
		m.mu.Unlock()
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			k, kerr := m.Registry.Get(inst.Kind)
			if kerr != nil {
				continue
			}
			st, errStr := k.Status(ri.handle)
			switch st {
			case StatusStopped, StatusFailed, StatusExited:
				m.setStatus(inst.ID, st.String(), errStr)
				m.mu.Lock()
				delete(m.running, inst.ID)
				m.mu.Unlock()
				return
			}
		}
	}
}

// Stop terminates the instance.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	ri, ok := m.running[id]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	k, err := m.Registry.Get(ri.handle.KindName)
	if err == nil {
		if err := k.Stop(ri.handle, m.stopGraceSeconds); err != nil {
			m.logf("Stop(%s): kind returned error: %v", id, err)
		}
	}
	ri.cancel()
	ri.wg.Wait()
	m.setStatus(id, StatusStopped.String(), "")
	m.mu.Lock()
	delete(m.running, id)
	m.mu.Unlock()
	return nil
}

// Get returns a copy of the instance record from the store.
func (m *Manager) Get(id string) (store.ManagedInstance, error) {
	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedInstance{}, err
	}
	for _, it := range st.Instances {
		if it.ID == id {
			return it, nil
		}
	}
	return store.ManagedInstance{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
}

// List returns all instances from the store.
func (m *Manager) List() ([]store.ManagedInstance, error) {
	st, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	return st.Instances, nil
}

// Tail returns up to n bytes of captured log.
func (m *Manager) Tail(id string, n int64) (string, error) {
	m.mu.Lock()
	ri, ok := m.running[id]
	m.mu.Unlock()
	if !ok {
		return "", nil
	}
	if n <= 0 {
		n = 4096
	}
	k, err := m.Registry.Get(ri.handle.KindName)
	if err != nil {
		return "", err
	}
	body, _, err := k.ReadLogs(ri.handle, 0, n)
	return body, err
}

// ReadSince returns log content starting at byte offset since.
func (m *Manager) ReadSince(id string, since int64, maxBytes int64) (string, int64, error) {
	m.mu.Lock()
	ri, ok := m.running[id]
	m.mu.Unlock()
	if !ok {
		return "", since, nil
	}
	k, err := m.Registry.Get(ri.handle.KindName)
	if err != nil {
		return "", since, err
	}
	return k.ReadLogs(ri.handle, since, maxBytes)
}

// Resize forwards a terminal size update to the kind.
func (m *Manager) Resize(id string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("invalid terminal size")
	}
	m.mu.Lock()
	ri, ok := m.running[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("instance not running: %s", id)
	}
	k, kerr := m.Registry.Get(ri.handle.KindName)
	if kerr != nil {
		return kerr
	}
	type resizer interface {
		Resize(handle Handle, cols, rows int) error
	}
	if r, ok := k.(resizer); ok {
		return r.Resize(ri.handle, cols, rows)
	}
	return errors.New("kind does not support resize")
}

// SendInput forwards a user input string to the kind.
func (m *Manager) SendInput(id string, input string) error {
	m.mu.Lock()
	ri, ok := m.running[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("instance input unavailable: %s", id)
	}
	k, kerr := m.Registry.Get(ri.handle.KindName)
	if kerr != nil {
		return kerr
	}
	type inputter interface {
		SendInput(handle Handle, input string) error
	}
	if s, ok := k.(inputter); ok {
		return s.SendInput(ri.handle, input)
	}
	return errors.New("kind does not support input")
}

// UpdateKindBlob persists a kind's blob for the given instance.
func (m *Manager) UpdateKindBlob(id string, blob json.RawMessage) error {
	return m.updateStore(id, func(inst *store.ManagedInstance) error {
		inst.KindBlob = blob
		return nil
	})
}

// MarkRunning transitions an instance from StatusStarting to StatusRunning.
func (m *Manager) MarkRunning(id string) error {
	return m.setStatus(id, StatusRunning.String(), "")
}

// MarkFailed transitions an instance to StatusFailed.
func (m *Manager) MarkFailed(id string, reason string) error {
	return m.setStatus(id, StatusFailed.String(), reason)
}

// MarkExited transitions an instance to StatusExited with the given exit code.
func (m *Manager) MarkExited(id string, exitCode int) error {
	return m.updateStore(id, func(inst *store.ManagedInstance) error {
		inst.Status = StatusExited.String()
		inst.ExitCode = exitCode
		inst.StoppedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
}

// SetInstancePID persists the spawned process id for an instance.
// Kinds call this once after Spawn (via the Publisher) so resource
// stats can sample the right process.
func (m *Manager) SetInstancePID(id string, pid int) error {
	return m.updateStore(id, func(inst *store.ManagedInstance) error {
		inst.PID = pid
		return nil
	})
}

// registerReattached wires a re-attached handle (see RestartSurvivor)
// into the in-memory running map and starts the lifecycle watcher,
// mirroring the tail of Start.
func (m *Manager) registerReattached(inst store.ManagedInstance, handle Handle, ready *ReadySignal) {
	runCtx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(1)
	ri := &runningInstance{handle: handle, ready: ready, cancel: cancel, wg: wg}

	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*runningInstance{}
	}
	m.running[inst.ID] = ri
	m.mu.Unlock()

	go m.runLifecycle(runCtx, inst, ri)
}

// KindFromInstance returns the kind registered for the given instance.
func (m *Manager) KindFromInstance(inst store.ManagedInstance) (Kind, error) {
	if inst.Kind == "" {
		inst.Kind = "pty"
	}
	return m.Registry.Get(inst.Kind)
}

// kindPublisher is the per-instance Publisher handed to kinds via PublisherBinder.
type kindPublisher struct {
	m  *Manager
	id string
}

func (p *kindPublisher) InstanceID() string        { return p.id }
func (p *kindPublisher) MarkRunning() error        { return p.m.MarkRunning(p.id) }
func (p *kindPublisher) MarkFailed(r string) error { return p.m.MarkFailed(p.id, r) }
func (p *kindPublisher) MarkExited(c int) error    { return p.m.MarkExited(p.id, c) }
func (p *kindPublisher) SetPID(pid int) error      { return p.m.SetInstancePID(p.id, pid) }
func (p *kindPublisher) UpdateKindBlob(b json.RawMessage) error {
	return p.m.UpdateKindBlob(p.id, b)
}

// --- internal helpers ---

func (m *Manager) sampler() MemSampler {
	if m.memSampler != nil {
		return m.memSampler
	}
	return DefaultSampler()
}

func (m *Manager) logf(format string, args ...interface{}) {
	if m.Logger != nil {
		m.Logger.Printf(format, args...)
	}
}

func (m *Manager) resolveWorktree(in StartParams) (path, name string, err error) {
	if in.Root != "" {
		return in.Root, filepath.Base(in.Root), nil
	}
	st, err := m.Store.Load()
	if err != nil {
		return "", "", err
	}
	for _, wt := range st.Worktrees {
		if wt.ID == in.WorktreeID {
			return wt.Path, wt.Name, nil
		}
	}
	return "", "", fmt.Errorf("unknown worktree id: %s", in.WorktreeID)
}

func (m *Manager) persistNewInstance(inst store.ManagedInstance) error {
	// Retry on version conflict: the runLifecycle watcher of a previous
	// Start can flip that instance to "running" concurrently, so a rapid
	// batch of Starts must not lose the append to a stale version.
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		st, err := m.Store.Load()
		if err != nil {
			return err
		}
		st.Instances = append(st.Instances, inst)
		if st.TabOrder == nil {
			st.TabOrder = map[string][]string{}
		}
		st.TabOrder[inst.WorktreeID] = append(st.TabOrder[inst.WorktreeID], inst.ID)
		if err := m.Store.SaveWithVersion(st, st.Version); err != nil {
			if errors.Is(err, store.ErrVersionConflict) {
				lastErr = err
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("persist instance: %w (after %d retries)", lastErr, maxRetries)
}

func (m *Manager) setStatus(id, status, lastErr string) error {
	return m.updateStore(id, func(inst *store.ManagedInstance) error {
		inst.Status = status
		if lastErr != "" {
			inst.LastError = lastErr
		}
		if status == StatusStopped.String() || status == StatusFailed.String() || status == StatusExited.String() {
			inst.StoppedAt = time.Now().UTC().Format(time.RFC3339)
		}
		return nil
	})
}

func (m *Manager) markFailed(id string, reason error) {
	_ = m.MarkFailed(id, reason.Error())
}

func (m *Manager) updateStore(id string, fn func(*store.ManagedInstance) error) error {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		st, err := m.Store.Load()
		if err != nil {
			return err
		}
		found := false
		for i := range st.Instances {
			if st.Instances[i].ID == id {
				if err := fn(&st.Instances[i]); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
		}
		if err := m.Store.SaveWithVersion(st, st.Version); err != nil {
			if errors.Is(err, store.ErrVersionConflict) {
				lastErr = err
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("update store: %w (after %d retries)", lastErr, maxRetries)
}

func newID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
