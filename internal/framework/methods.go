package framework

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"myworktree/internal/store"
)

// --- instance record edits ---

// UpdateName renames an instance.
func (m *Manager) UpdateName(id, name string) (store.ManagedInstance, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.ManagedInstance{}, errors.New("id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return store.ManagedInstance{}, errors.New("name cannot be empty")
	}
	var updated store.ManagedInstance
	err := m.updateStore(id, func(inst *store.ManagedInstance) error {
		inst.Name = name
		updated = *inst
		return nil
	})
	if err != nil {
		return store.ManagedInstance{}, err
	}
	return updated, nil
}

// Delete removes an instance from the store. The instance MUST be in
// a terminal state; running instances must be Stopped first.
func (m *Manager) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	m.mu.Lock()
	_, isRunning := m.running[id]
	m.mu.Unlock()
	if isRunning {
		return fmt.Errorf("instance is running: %s", id)
	}

	m.dropBuffer(id)

	m.mu.Lock()
	delete(m.subscribers, id)
	m.connsMu.Lock()
	delete(m.conns, id)
	m.connsMu.Unlock()
	m.mu.Unlock()

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return err
	}
	idx := -1
	for i, inst := range st.Instances {
		if inst.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil // already gone; idempotent
	}
	st.Instances = append(st.Instances[:idx], st.Instances[idx+1:]...)
	return m.Store.SaveWithVersion(st, st.Version)
}

// Restart creates a new instance from a stopped one's configuration.
func (m *Manager) Restart(id string) (store.ManagedInstance, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.ManagedInstance{}, errors.New("id is required")
	}
	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedInstance{}, err
	}
	var old store.ManagedInstance
	idx := -1
	for i, inst := range st.Instances {
		if inst.ID == id {
			old = st.Instances[i]
			idx = i
			break
		}
	}
	if idx == -1 {
		return store.ManagedInstance{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	if old.Status == StatusRunning.String() || old.Status == StatusStarting.String() {
		return store.ManagedInstance{}, fmt.Errorf("instance is running: %s", id)
	}

	startIn := StartParams{
		WorktreeID: old.WorktreeID,
		TagID:      old.TagID,
		Name:       old.Name,
		Kind:       old.Kind,
	}
	if old.WorktreeID == MainWorktreeID && m.Root != "" {
		startIn.Root = m.Root
	}

	newInst, err := m.Start(context.Background(), startIn)
	if err != nil {
		return store.ManagedInstance{}, err
	}

	// Save the RestartedFrom link. The runLifecycle goroutine may
	// concurrently mark the new instance "running", so retry on
	// version conflict.
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		m.stateMu.Lock()
		st2, err := m.Store.Load()
		m.stateMu.Unlock()
		if err != nil {
			return newInst, nil
		}
		oldIdx := -1
		for i, inst := range st2.Instances {
			if inst.ID == id {
				oldIdx = i
				break
			}
		}
		if oldIdx >= 0 {
			st2.Instances = append(st2.Instances[:oldIdx], st2.Instances[oldIdx+1:]...)
		}
		foundNew := false
		for i := range st2.Instances {
			if st2.Instances[i].ID == newInst.ID {
				st2.Instances[i].RestartedFrom = id
				foundNew = true
				break
			}
		}
		if !foundNew {
			return newInst, nil
		}
		if err := m.Store.SaveWithVersion(st2, st2.Version); err != nil {
			if errors.Is(err, store.ErrVersionConflict) && attempt < maxRetries-1 {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return newInst, nil
		}
		break
	}
	return newInst, nil
}

// ReorderInstances sets the tab order for a worktree.
func (m *Manager) ReorderInstances(worktreeID string, orderIDs []string, expectedVersion int64) error {
	worktreeID = strings.TrimSpace(worktreeID)
	if worktreeID == "" {
		return errors.New("worktree_id is required")
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return err
	}
	idSet := make(map[string]bool, len(orderIDs))
	for _, id := range orderIDs {
		idSet[id] = true
	}
	for _, inst := range st.Instances {
		if inst.WorktreeID == worktreeID && !idSet[inst.ID] {
			return fmt.Errorf("order list is missing instance: %s", inst.ID)
		}
	}
	for _, id := range orderIDs {
		found := false
		for _, inst := range st.Instances {
			if inst.ID == id && inst.WorktreeID == worktreeID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("instance %s does not belong to worktree %s", id, worktreeID)
		}
	}
	idToInst := make(map[string]store.ManagedInstance, len(st.Instances))
	for _, inst := range st.Instances {
		idToInst[inst.ID] = inst
	}
	newOrder := make([]store.ManagedInstance, 0, len(st.Instances))
	for _, id := range orderIDs {
		newOrder = append(newOrder, idToInst[id])
	}
	for _, inst := range st.Instances {
		if inst.WorktreeID != worktreeID {
			newOrder = append(newOrder, inst)
		}
	}
	st.Instances = newOrder
	if st.TabOrder == nil {
		st.TabOrder = map[string][]string{}
	}
	st.TabOrder[worktreeID] = orderIDs
	return m.Store.SaveWithVersion(st, expectedVersion)
}

// ReconcileRunningOnStartup marks stale "running" / "starting"
// instances as "stopped". The previous server's PIDs are dead.
func (m *Manager) ReconcileRunningOnStartup() (int, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	changed := 0
	for i := range st.Instances {
		if st.Instances[i].Status != StatusRunning.String() &&
			st.Instances[i].Status != StatusStarting.String() {
			continue
		}
		st.Instances[i].Status = StatusStopped.String()
		if strings.TrimSpace(st.Instances[i].StoppedAt) == "" {
			st.Instances[i].StoppedAt = now
		}
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	if err := m.Store.SaveWithVersion(st, st.Version); err != nil {
		return changed, err
	}
	return changed, nil
}

// PurgeOrphanLogFiles removes stray *.log files from DataDir/logs/.
func (m *Manager) PurgeOrphanLogFiles() (int, error) {
	dir := strings.TrimSpace(m.DataDir)
	if dir == "" {
		return 0, nil
	}
	logDir := dir + string(os.PathSeparator) + "logs"
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	var failed []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		full := logDir + string(os.PathSeparator) + e.Name()
		if err := os.Remove(full); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", full, err))
			continue
		}
		removed++
	}
	if m.Logger != nil {
		if removed > 0 {
			m.Logger.Printf("purge: removed %d orphan log files from %s", removed, logDir)
		}
		for _, f := range failed {
			m.Logger.Printf("purge: %s", f)
		}
	}
	return removed, nil
}

// --- buffer / output ---

// BufferCapBytesFor returns the ring buffer cap for an instance.
func (m *Manager) BufferCapBytesFor(id string) int64 {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	p, ok := m.buffers[id]
	if !ok || p == nil {
		return 0
	}
	rb := p.Load()
	if rb == nil {
		return 0
	}
	return rb.CapBytes()
}

// BufferUsedBytesFor returns the ring buffer's current used bytes.
func (m *Manager) BufferUsedBytesFor(id string) int64 {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	p, ok := m.buffers[id]
	if !ok || p == nil {
		return 0
	}
	rb := p.Load()
	if rb == nil {
		return 0
	}
	return rb.BytesUsed()
}

// SubscribeOutput returns a channel that receives output chunks
// from the instance. PTY kind broadcasts to this channel; other
// kinds return an error.
//
// The id is passed to the kind so kinds that fan output can scope
// the subscription to one instance. Without it, every PTY tab would
// receive every other PTY tab's output.
func (m *Manager) SubscribeOutput(id string) (<-chan string, func(), error) {
	m.mu.Lock()
	ri, ok := m.running[id]
	m.mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("instance not running: %s", id)
	}
	type outputSub interface {
		SubscribeOutput(id string) (<-chan string, func(), error)
	}
	k, kerr := m.Registry.Get(ri.handle.KindName)
	if kerr != nil {
		return nil, nil, kerr
	}
	if s, ok := k.(outputSub); ok {
		return s.SubscribeOutput(id)
	}
	return nil, nil, fmt.Errorf("kind %q does not support output subscription", ri.handle.KindName)
}

// --- connection type tracking ---

// SetConnectionType records the active transport for an instance.
func (m *Manager) SetConnectionType(id, connType string) {
	m.connsMu.Lock()
	defer m.connsMu.Unlock()
	if m.conns == nil {
		m.conns = map[string]string{}
	}
	if connType == "" {
		delete(m.conns, id)
		return
	}
	m.conns[id] = connType
}

// ConnectionType returns the active transport for an instance.
func (m *Manager) ConnectionType(id string) string {
	m.connsMu.Lock()
	defer m.connsMu.Unlock()
	return m.conns[id]
}

// AllConnectionTypes returns a snapshot of all active connection types.
func (m *Manager) AllConnectionTypes() map[string]string {
	m.connsMu.Lock()
	defer m.connsMu.Unlock()
	out := make(map[string]string, len(m.conns))
	for k, v := range m.conns {
		out[k] = v
	}
	return out
}

// RegisterSSEConnection marks an instance as having an active SSE
// connection and returns a cleanup function.
func (m *Manager) RegisterSSEConnection(id string) func() {
	m.SetConnectionType(id, "sse")
	return func() {
		m.connsMu.Lock()
		defer m.connsMu.Unlock()
		if m.conns != nil && m.conns[id] == "sse" {
			delete(m.conns, id)
		}
	}
}

// --- buffer bookkeeping ---

// dropBuffer removes the ring buffer for an instance and decrements
// the global cap counter. Caller must hold no locks.
func (m *Manager) dropBuffer(id string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	p, ok := m.buffers[id]
	if !ok {
		return
	}
	if rb := p.Swap(nil); rb != nil {
		if cap := rb.CapBytes(); cap > 0 {
			m.totalBufBytes.Add(-cap)
		}
		rb.Close()
	}
	delete(m.buffers, id)
}

// AllocateBuffer assigns a new ring buffer to an instance. Kinds
// that capture output (PTY) call this in Spawn.
func (m *Manager) AllocateBuffer(id string, capBytes int64) *RingBuffer {
	buf := NewRingBuffer(capBytes)
	m.stateMu.Lock()
	if m.buffers == nil {
		m.buffers = map[string]*atomic.Pointer[RingBuffer]{}
	}
	p := &atomic.Pointer[RingBuffer]{}
	p.Store(buf)
	m.buffers[id] = p
	m.stateMu.Unlock()
	m.totalBufBytes.Add(capBytes)
	return buf
}

// --- ensure unused imports stay referenced ---
//
// (kept removed: methods.go's `sync` package is genuinely used by the
// mutex types threaded through Manager; the previous `var (_ sync.Mutex)`
// placeholder was vestigial and has been deleted along with it.)
