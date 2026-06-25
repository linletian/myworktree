package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// KindInfo describes a registered instance kind for the UI / API.
// The framework stores this in memory; it is NOT persisted (the kind
// name on the instance record is enough — the registry lookup happens
// at runtime).
type KindInfo struct {
	Name        string `json:"name"`        // e.g. "pty", "opencode-web"
	Label       string `json:"label"`       // human-facing label, e.g. "Terminal"
	Description string `json:"description"` // one-line explanation, shown in tooltips / pickers
	Interactive bool   `json:"interactive"` // true for PTY-style (bidirectional stdin/stdout); false for HTTP-backed kinds
}

// SpawnParams carries the framework-supplied inputs to a kind's Spawn.
// It deliberately omits anything kind-specific (the auth token is
// passed through because every managed kind needs SOME form of secret
// to identify itself to the worktree framework).
type SpawnParams struct {
	WorktreeID   string
	WorktreePath string
	WorktreeName string
	TagID        string
	Name         string // user-chosen display name; "" means "auto"
	KindEnv      map[string]string
	AuthToken    string            // myworktree bearer; reused as upstream password by opencode-web
	ExtraEnv     map[string]string // from tag.Env
}

// Handle is the opaque per-instance state owned by a kind. The
// framework holds onto it for the lifetime of the instance and passes
// it back to the kind on Stop / Status / ReadLogs calls. Framework
// code MUST NOT interpret or cast this value; doing so would couple
// the framework back to specific kinds.
type Handle struct {
	KindName string
	inner    any
}

// Unwrap exposes the kind-private handle value. Only code inside the
// owning kind package should call this — the framework never does.
func (h Handle) Unwrap() any { return h.inner }

// NewHandle wraps a kind-private value. Kinds call this once at
// Spawn time and pass the resulting Handle back to the framework.
func NewHandle(kind string, inner any) Handle {
	return Handle{KindName: kind, inner: inner}
}

// ReadySignal is closed when an instance has reached Running state.
// For PTY it closes immediately after Spawn returns (PTY is "ready"
// as soon as pty.Start succeeds). For opencode-web it closes when
// the listening address has been parsed from stdout and persisted.
// Spawn callers select on this with a timeout; on timeout the
// framework marks the instance StatusFailed.
type ReadySignal struct {
	C    chan struct{}
	once sync.Once
}

// NewReadySignal returns a fresh signal. The kind calls Close when
// the instance is ready.
func NewReadySignal() *ReadySignal {
	return &ReadySignal{C: make(chan struct{})}
}

// Close marks the instance ready. Safe to call multiple times; only
// the first call has any effect.
func (r *ReadySignal) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() { close(r.C) })
}

// Channel returns the receive end. Callers should select on this
// alongside ctx.Done() to enforce startup timeouts.
func (r *ReadySignal) Channel() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.C
}

// Kind is the contract every instance kind implements. The framework
// only knows about this interface — concrete kinds live in their own
// subpackages and Register themselves at init() time.
//
// All methods MUST be safe to call from multiple goroutines. The
// framework may invoke Spawn / Stop / Status concurrently for
// different instance IDs, and Status / ReadLogs may be called
// concurrently with a Stop in flight.
//
// The framework calls Stop exactly once after a successful Spawn.
// After Stop returns, the framework will not invoke any further
// methods on the kind for that handle.
type Kind interface {
	// Manifest returns static metadata. Called once at registration
	// time; the result is cached by the framework.
	Manifest() KindInfo

	// Spawn launches the instance process. The kind MUST:
	//   - Return a non-nil Handle wrapping whatever state it needs
	//     to manage the running process.
	//   - Return a ReadySignal that closes when the instance reaches
	//     a usable state (or never close, for kinds that are
	//     immediately ready).
	//   - Persist any startup-time metadata (e.g. opencode-web's
	//     worktree_abs) into the instance record before returning.
	//
	// If Spawn returns a non-nil error the framework will not retry;
	// it will mark the instance StatusFailed with the error.
	Spawn(ctx context.Context, params SpawnParams) (Handle, *ReadySignal, error)

	// Stop terminates the instance gracefully (within grace), then
	// forcefully. The kind is responsible for releasing any resources
	// (PTY file, goroutines, child processes) before returning.
	//
	// Stop is best-effort: a returned error is logged but does NOT
	// prevent the framework from transitioning the instance to
	// StatusStopped. Callers that need stronger guarantees should
	// implement escalation in the kind itself.
	Stop(handle Handle, graceSeconds int) error

	// Status returns the kind's current view of the instance state.
	// The framework reads this periodically (every refresh tick) and
	// persists it. Kinds may return Status values that the framework
	// has not yet observed; the framework will write them through.
	//
	// For StatusFailed, the framework also expects an error string —
	// return it via the second return value. Returning ("", nil) is
	// equivalent to no error.
	Status(handle Handle) (Status, string)

	// ReadLogs returns up to maxBytes of captured process output
	// starting at byte offset since. Returns the bytes, the new
	// cursor (== since when no new data is available), and any error.
	//
	// The framework uses this to drive the `/api/instances/<id>/log`
	// and `/api/instances/<id>/log/stream` endpoints; kinds MUST
	// implement log capture themselves (the framework does not
	// understand how to read from a PTY or a pipe).
	ReadLogs(handle Handle, since int64, maxBytes int64) (string, int64, error)

	// HTTPHint returns the prefix path under which the kind wants to
	// register its own HTTP handlers (e.g. "/sessions/<id>/opencode-web"
	// for the opencode-web reverse proxy). Return "" if the kind has
	// no HTTP surface.
	//
	// The framework routes requests whose path begins with this prefix
	// to RegisterHTTP. Kinds that do not expose HTTP return "" from
	// HTTPHint and the framework will never call RegisterHTTP for them.
	HTTPHint(instanceID string) string

	// RegisterHTTP wires the kind's own HTTP handlers onto the
	// supplied mux. Called once per instance, after Spawn returns
	// successfully and after SetPublishers (if the kind implements
	// PublisherBinder). The handle passed in is the same value Spawn
	// returned; the kind uses it to access kind-private state from
	// inside the request handler.
	//
	// Implementations are responsible for parsing the path, extracting
	// the instance id, and returning 404 for unknown instances.
	RegisterHTTP(mux *http.ServeMux, instanceID string, handle Handle)

	// KindBlob returns the JSON-encodable blob this kind wants
	// persisted alongside the instance record. The framework does not
	// interpret the result — it is stored verbatim in the instance
	// record's KindBlob field and returned verbatim via Blob.
	//
	// Each kind owns its blob schema; the framework treats it as
	// opaque bytes.
	KindBlob(handle Handle) (json.RawMessage, error)
}

// ErrUnknownKind is returned by Registry methods when the requested
// kind name has not been registered. It is a sentinel for callers
// that want to distinguish "kind not implemented" from other errors.
var ErrUnknownKind = errors.New("unknown instance kind")

// Registry stores the set of registered kinds. The framework owns one
// global Registry; kinds add themselves via Register at init() time.
//
// Registry is safe for concurrent reads after the program has finished
// initialising (i.e. after all init() functions have run). Register
// MUST NOT be called after the framework has started serving requests.
type Registry struct {
	mu    sync.RWMutex
	kinds map[string]Kind
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{kinds: map[string]Kind{}}
}

// Register adds a kind to the registry. Panics if the kind is already
// registered (a duplicate init() is a programming error).
func (r *Registry) Register(k Kind) {
	info := k.Manifest()
	if info.Name == "" {
		panic("framework: kind Manifest().Name must be non-empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.kinds[info.Name]; exists {
		panic(fmt.Sprintf("framework: kind %q registered twice", info.Name))
	}
	r.kinds[info.Name] = k
}

// Get returns the kind registered under name, or ErrUnknownKind.
func (r *Registry) Get(name string) (Kind, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.kinds[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, name)
	}
	return k, nil
}

// Names returns the registered kind names sorted alphabetically.
// Used by the API to enumerate the kinds known to the framework.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.kinds))
	for name := range r.kinds {
		out = append(out, name)
	}
	return out
}

// Default is the global registry that init()-time Register calls land
// in. Production code uses this; tests can construct their own
// registry to swap in fakes.
var Default = NewRegistry()

// Register is a convenience for init()-time registration. Equivalent
// to Default.Register(k).
func Register(k Kind) { Default.Register(k) }

// Get / Names pass-throughs for the default registry.
func Get(name string) (Kind, error) { return Default.Get(name) }
func Names() []string               { return Default.Names() }

// Publisher is the channel through which kinds notify the framework
// of state transitions. The framework creates one Publisher per
// instance and hands it to the kind via SetPublishers. Kinds MUST NOT
// cache the Publisher across instances — it is bound to a specific
// instance id.
//
// Implementations are provided by the framework Manager. Kinds call
// MarkRunning / MarkFailed / MarkExited when their internal state
// machine reaches the corresponding transition.
type Publisher interface {
	InstanceID() string
	MarkRunning() error
	MarkFailed(reason string) error
	MarkExited(exitCode int) error
	UpdateKindBlob(blob json.RawMessage) error
}

// SetPublishers is an optional extension kinds implement when they
// need to push state changes back to the framework. The Manager
// calls SetPublishers(handle, publisher) exactly once, between Spawn
// returning and any goroutines starting.
//
// If a kind does not implement SetPublishers (i.e. is just a plain
// Kind), the Manager treats its state as fully described by what
// Spawn returned and the periodic Status() poll.
type PublisherBinder interface {
	SetPublishers(handle Handle, p Publisher)
}
