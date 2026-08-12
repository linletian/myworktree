package instance

import (
	"errors"
	"path/filepath"
	"testing"

	"myworktree/internal/store"
)

// fakeMemSaturated reports a tiny system so the 25% budget is small enough
// to be exceeded by a single buffer cap.
type fakeMemSaturated struct{}

func (fakeMemSaturated) VirtualMemory() (MemStat, error) {
	return MemStat{
		Total:     256 << 20, // 256 MB total → 64 MB budget at 25%
		Available: 256 << 20,
		Used:      0,
	}, nil
}

// TestStart_BudgetExceededReturnsLogBufferBudgetError verifies that Manager
// returns a *LogBufferBudgetError (with correctly-populated fields) when a
// new instance would push the global cap past MaxTotalFraction × system_RAM.
// This is the source of the HTTP 503 response the dashboard surfaces as a
// "log_buffer_budget_exceeded" modal.
func TestStart_BudgetExceededReturnsLogBufferBudgetError(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := store.FileStore{Path: path}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt-budget-test", Name: "wt-budget-test", Path: workDir, Branch: "main"},
		},
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	m := &Manager{Store: fs}
	m.setMemSamplerForTest(fakeMemSaturated{})
	// Pre-seed the running sum so any new 16 MB+ cap exceeds 64 MB.
	m.setTotalBufferBytesForTest(100 << 20)

	_, err := m.Start(StartInput{
		WorktreeID: "wt-budget-test",
		Name:       "budget-test",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var budgetErr *LogBufferBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("expected *LogBufferBudgetError, got %T: %v", err, err)
	}
	if budgetErr.UsedBytes != 100<<20 {
		t.Fatalf("UsedBytes = %d, want %d", budgetErr.UsedBytes, 100<<20)
	}
	wantLimit := int64(float64(256<<20) * MaxTotalFraction)
	if budgetErr.LimitBytes != wantLimit {
		t.Fatalf("LimitBytes = %d, want %d", budgetErr.LimitBytes, wantLimit)
	}
	if budgetErr.SystemBytes != 256<<20 {
		t.Fatalf("SystemBytes = %d, want %d", budgetErr.SystemBytes, 256<<20)
	}
	if !errors.Is(err, ErrLogBufferBudgetExceeded) {
		t.Fatalf("errors.Is(err, ErrLogBufferBudgetExceeded) = false")
	}
}
