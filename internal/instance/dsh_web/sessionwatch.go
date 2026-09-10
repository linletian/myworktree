package dsh_web

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SessionWatch observes the SHARED dsh sessions pool
// ($DSH_HOME/sessions) for sessions that are actively being written by
// a process OTHER than the instances we manage (REVIEW-2026-08-15.md
// #2 / CROSS-PROCESS-SESSION.md). dsh is a single-writer-per-process
// system: opening an actively-written session from a second process
// appends an unguarded `session/end-seed` that collides with the
// writer's next seq and permanently corrupts the log. The watch is the
// advisory (record-only) mitigation: while a foreign process is
// actively writing, the frontend shows a persistent warning bar
// telling the user to wait before opening those sessions. It never
// blocks, redirects or rewrites anything.
//
// Detection is mtime-based (the session log is zstd-framed JSONL —
// reading it would need a decoder, and mtime is all we need): a
// session.jsonl.zstd modified within sessionFreshWindow counts as
// active. Attribution is traffic-based: session ids this myworktree
// daemon itself drives — MarkOwn via the proxy's RPC observation and
// the workspace bootstrap — are excluded for sessionOwnWindow, so a
// user chatting inside the embed does not warn about themselves.
//
// Layout (verified against a real 0.1.0-rc.6 DSH_HOME):
//
//	$DSH_HOME/sessions/<worktree-slug>/<session-id>/session.jsonl.zstd
const (
	sessionFreshWindow = 90 * time.Second
	sessionOwnWindow   = 30 * time.Minute
	sessionPollEvery   = 3 * time.Second
)

// DshSessionsDir resolves the shared sessions pool the same way the dsh
// subprocess does: $DSH_HOME/sessions, defaulting to ~/.dsh/sessions.
// Note: a per-instance tag env override of DSH_HOME is not visible here
// (the daemon's own environment is the source of truth for the pool).
func DshSessionsDir() string {
	if home := os.Getenv("DSH_HOME"); home != "" {
		return filepath.Join(home, "sessions")
	}
	if u, err := os.UserHomeDir(); err == nil {
		return filepath.Join(u, ".dsh", "sessions")
	}
	return ""
}

// SessionWatch is safe for concurrent use.
type SessionWatch struct {
	dir    string
	logger *log.Logger

	mu      sync.Mutex
	own     map[string]time.Time // sessionId → last traffic driven by us
	foreign map[string]time.Time // sessionId → last observed active mtime

	stopOnce sync.Once
	stop     chan struct{}
}

// NewSessionWatch returns a watch over dir ("" = never active). Call
// Start to begin the background scan, Stop on shutdown.
func NewSessionWatch(dir string, logger *log.Logger) *SessionWatch {
	return &SessionWatch{
		dir:     dir,
		logger:  logger,
		own:     map[string]time.Time{},
		foreign: map[string]time.Time{},
		stop:    make(chan struct{}),
	}
}

// Start launches the background scan loop. Idempotent-ish: only the
// first call starts the goroutine (the loop shares one stop channel).
func (w *SessionWatch) Start() {
	if w.dir == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(sessionPollEvery)
		defer ticker.Stop()
		_ = w.ScanNow()
		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
				if err := w.ScanNow(); err != nil && w.logger != nil {
					w.logger.Printf("dsh-web session watch: %v", err)
				}
			}
		}
	}()
}

// Stop terminates the background scan loop.
func (w *SessionWatch) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
}

// MarkOwn records that sessionID is driven by our own managed dsh
// process (a session.* RPC observed through the per-instance proxy, or
// the workspace-bootstrap preseed). Own sessions are excluded from the
// foreign-active report for sessionOwnWindow.
func (w *SessionWatch) MarkOwn(sessionID string) {
	if w == nil || sessionID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.own[sessionID] = time.Now()
}

// ScanNow walks the sessions pool once and refreshes the foreign-active
// set. Also called by Start's loop; exported so tests can drive it
// deterministically.
func (w *SessionWatch) ScanNow() error {
	if w == nil || w.dir == "" {
		return nil
	}
	slugs, err := os.ReadDir(w.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	now := time.Now()
	active := map[string]time.Time{}
	for _, slug := range slugs {
		if !slug.IsDir() {
			continue
		}
		sessions, err := os.ReadDir(filepath.Join(w.dir, slug.Name()))
		if err != nil {
			continue
		}
		for _, sd := range sessions {
			if !sd.IsDir() {
				continue
			}
			info, err := os.Stat(filepath.Join(w.dir, slug.Name(), sd.Name(), "session.jsonl.zstd"))
			if err != nil {
				continue
			}
			if mt := info.ModTime(); now.Sub(mt) <= sessionFreshWindow {
				active[sd.Name()] = mt
			}
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	foreign := map[string]time.Time{}
	for id, mt := range active {
		if t, ok := w.own[id]; ok && now.Sub(t) <= sessionOwnWindow {
			continue
		}
		foreign[id] = mt
	}
	w.foreign = foreign
	for id, t := range w.own {
		if now.Sub(t) > sessionOwnWindow {
			delete(w.own, id)
		}
	}
	return nil
}

// ForeignActive returns the count and sorted ids of sessions currently
// classified as actively written by another process.
func (w *SessionWatch) ForeignActive() (int, []string) {
	if w == nil {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := make([]string, 0, len(w.foreign))
	for id := range w.foreign {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return len(ids), ids
}
