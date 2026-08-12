package instance

// Test-only hooks for Manager state. These methods are package-private and
// live in a _test.go file, so production code (which never sees this file at
// build time) cannot reach them. The exported SetMemSampler /
// SetTotalBufferBytesForTest methods that previously sat on *Manager have
// been removed; their callers in the app package's integration tests have
// been relocated to this package.

// setMemSamplerForTest overrides the platform memory sampler. Used by tests
// in this package to inject a deterministic sampler so the budget-exceeded
// path can be exercised without depending on the host's actual RAM.
func (m *Manager) setMemSamplerForTest(s MemSampler) {
	m.memSampler = s
}

// setTotalBufferBytesForTest pre-seeds the running sum of live buffer caps
// so budget-exceeded tests can force the error path without spinning up
// dozens of real instances.
func (m *Manager) setTotalBufferBytesForTest(n int64) {
	m.totalBufBytes.Store(n)
}
