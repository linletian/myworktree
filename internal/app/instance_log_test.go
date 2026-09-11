package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/store"
)

// logReplayKind returns fixed ReadLogs data and records the since
// argument, pinning the issue #81 contract at the HTTP layer: a bare
// /api/instances/log request must be served from the tail (newest
// bytes) and X-Log-Offset must be the current end offset.
type logReplayKind struct {
	lastSince atomic.Int64
}

func (k *logReplayKind) Manifest() framework.KindInfo {
	return framework.KindInfo{Name: "log-replay", Label: "Log Replay"}
}

func (k *logReplayKind) Spawn(ctx context.Context, p framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	ready := framework.NewReadySignal()
	ready.Close()
	return framework.NewHandle("log-replay", "inner"), ready, nil
}

func (k *logReplayKind) Stop(h framework.Handle, graceSeconds int) error { return nil }

func (k *logReplayKind) Status(h framework.Handle) (framework.Status, string) {
	return framework.StatusRunning, ""
}

func (k *logReplayKind) ReadLogs(h framework.Handle, since, max int64) (string, int64, error) {
	k.lastSince.Store(since)
	if since < 0 {
		return "newest-bytes", 4242, nil
	}
	return "chunk-from-cursor", since + 5, nil
}

func (k *logReplayKind) KindBlob(h framework.Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k *logReplayKind) HTTPHint(instanceID string) string { return "" }

func (k *logReplayKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h framework.Handle) {}

// emptyLogsKind mimics the non-buffering kinds (reasonix /
// opencode-web / dsh-web): ReadLogs returns no data and echoes since.
type emptyLogsKind struct{ logReplayKind }

func (k *emptyLogsKind) Manifest() framework.KindInfo {
	return framework.KindInfo{Name: "empty-logs", Label: "Empty Logs"}
}

func (k *emptyLogsKind) Spawn(ctx context.Context, p framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	ready := framework.NewReadySignal()
	ready.Close()
	return framework.NewHandle("empty-logs", "inner"), ready, nil
}

func (k *emptyLogsKind) ReadLogs(h framework.Handle, since, max int64) (string, int64, error) {
	return "", since, nil
}

// syncStreamWriter is a goroutine-safe http.ResponseWriter +
// http.Flusher for streaming handler tests (httptest.ResponseRecorder
// is not safe for concurrent read/write).
type syncStreamWriter struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
}

func newSyncStreamWriter() *syncStreamWriter {
	return &syncStreamWriter{header: http.Header{}}
}

func (s *syncStreamWriter) Header() http.Header { return s.header }

func (s *syncStreamWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncStreamWriter) WriteHeader(code int) {}

func (s *syncStreamWriter) Flush() {}

func (s *syncStreamWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func newLogTestServer(t *testing.T, kinds ...framework.Kind) (*Server, *framework.Manager) {
	t.Helper()
	reg := framework.NewRegistry()
	for _, k := range kinds {
		reg.Register(k)
	}
	dataDir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: t.TempDir()}},
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	m := framework.NewManager(reg, fs, nil)
	m.DataDir = dataDir
	m.Root = dataDir
	return &Server{instanceMgr: m}, m
}

func startKindInstance(t *testing.T, m *framework.Manager, kind string) string {
	t.Helper()
	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: kind, Name: "t1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor := time.Now().Add(5 * time.Second)
	for {
		got, err := m.Get(inst.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == framework.StatusRunning.String() {
			break
		}
		if time.Now().After(waitFor) {
			t.Fatal("status did not reach running within 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(func() {
		if err := m.Stop(inst.ID); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return inst.ID
}

// runStreamUntilCancel drives handleInstanceLogStream until the body
// contains want (or a 3s deadline), then cancels the request context
// and waits for the handler to return.
func runStreamUntilCancel(t *testing.T, srv *Server, url, want string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx)
	w := newSyncStreamWriter()
	done := make(chan struct{})
	go func() {
		srv.handleInstanceLogStream(w, req)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(w.String(), want) {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("stream body never contained %q; got %q", want, w.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	return w.String()
}

func TestHandleInstanceLog_TailReturnsNewestBytes(t *testing.T) {
	t.Parallel()
	k := &logReplayKind{}
	srv, m := newLogTestServer(t, k)
	instID := startKindInstance(t, m, "log-replay")

	req := httptest.NewRequest(http.MethodGet, "/api/instances/log?id="+instID, nil)
	w := httptest.NewRecorder()
	srv.handleInstanceLog(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "newest-bytes" {
		t.Fatalf("tail body = %q, want newest-bytes", body)
	}
	if off := w.Header().Get("X-Log-Offset"); off != "4242" {
		t.Fatalf("X-Log-Offset = %q, want 4242 (end offset)", off)
	}
	if got := k.lastSince.Load(); got >= 0 {
		t.Fatalf("ReadLogs since = %d, want negative (tail semantics)", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/instances/log?id="+instID+"&since=7", nil)
	w = httptest.NewRecorder()
	srv.handleInstanceLog(w, req)

	if body := w.Body.String(); body != "chunk-from-cursor" {
		t.Fatalf("incremental body = %q, want chunk-from-cursor", body)
	}
	if off := w.Header().Get("X-Log-Offset"); off != "12" {
		t.Fatalf("X-Log-Offset = %q, want 12", off)
	}
}

func TestHandleInstanceLogStream_StartsFromTail(t *testing.T) {
	t.Parallel()
	k := &logReplayKind{}
	srv, m := newLogTestServer(t, k)
	instID := startKindInstance(t, m, "log-replay")

	body := runStreamUntilCancel(t, srv, "/api/instances/log/stream?id="+instID, `"chunk":"newest-bytes"`)
	if !strings.HasPrefix(body, "event: log\ndata: {\"chunk\":\"newest-bytes\",\"next\":4242}") {
		t.Fatalf("first frame = %q, want tail with next=end offset", body)
	}
}

func TestHandleInstanceLogStream_NonTailKindDoesNotReplay(t *testing.T) {
	t.Parallel()
	srv, m := newLogTestServer(t, &emptyLogsKind{})
	instID := startKindInstance(t, m, "empty-logs")

	// First frame is an empty tail with the clamped 0 cursor; after
	// that only : ping keep-alives — no replay loop.
	body := runStreamUntilCancel(t, srv, "/api/instances/log/stream?id="+instID, ": ping")
	if !strings.Contains(body, `{"chunk":"","next":0}`) {
		t.Fatalf("first frame = %q, want empty chunk with next=0", body)
	}
	if n := strings.Count(body, "event: log"); n != 1 {
		t.Fatalf("log events = %d, want 1 (no replay loop)", n)
	}
}
