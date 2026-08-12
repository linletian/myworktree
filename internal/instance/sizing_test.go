package instance

import (
	"errors"
	"testing"
)

type fakeMem struct {
	total, available, used int64
	err                    error
}

func (f fakeMem) VirtualMemory() (MemStat, error) {
	if f.err != nil {
		return MemStat{}, f.err
	}
	return MemStat{
		Total:     f.total,
		Available: f.available,
		Used:      f.used,
	}, nil
}

func TestResolveCap_UserOverrideClamped(t *testing.T) {
	t.Parallel()
	// Below floor
	cap, err := resolveCap(1<<20, nil, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap != MinBufferCap {
		t.Fatalf("below-floor cap = %d, want %d", cap, MinBufferCap)
	}
	// Above ceiling
	cap, _ = resolveCap(1<<30, nil, 0)
	if cap != MaxBufferCap {
		t.Fatalf("above-ceiling cap = %d, want %d", cap, MaxBufferCap)
	}
	// Within range
	cap, _ = resolveCap(64<<20, nil, 0)
	if cap != 64<<20 {
		t.Fatalf("within-range cap = %d, want %d", cap, 64<<20)
	}
}

func TestResolveCap_AdaptiveFromAvailable(t *testing.T) {
	t.Parallel()
	sampler := fakeMem{total: 16 << 30, available: 8 << 30, used: 8 << 30}
	// available / 16 = 0.5 GB → clamp to MaxBufferCap
	cap, err := resolveCap(0, sampler, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap != MaxBufferCap {
		t.Fatalf("adaptive cap = %d, want %d (clamped)", cap, MaxBufferCap)
	}
}

func TestResolveCap_AdaptiveFromAvailable_4GB(t *testing.T) {
	t.Parallel()
	sampler := fakeMem{total: 4 << 30, available: 2 << 30, used: 2 << 30}
	// available / 16 = 128 MB → within range
	cap, err := resolveCap(0, sampler, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap != 128<<20 {
		t.Fatalf("adaptive cap = %d, want %d", cap, 128<<20)
	}
}

func TestResolveCap_AdaptiveFallsBackToDefaultOnError(t *testing.T) {
	t.Parallel()
	sampler := fakeMem{err: errors.New("sampler failed")}
	cap, err := resolveCap(0, sampler, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap != DefaultBufferCap {
		t.Fatalf("fallback cap = %d, want %d", cap, DefaultBufferCap)
	}
}

func TestResolveCap_RejectsOverBudget(t *testing.T) {
	t.Parallel()
	// 4 GB total, 25% budget = 1 GB
	sampler := fakeMem{total: 4 << 30, available: 4 << 30}
	// Simulate 30 instances already at 32 MB each = 960 MB used
	used := int64(30 * 32 << 20)
	// New adaptive cap would be 4GB/16 = 256MB (clamped to MaxBufferCap)
	// 960 + 256 = 1216 MB > 1024 MB limit → reject
	_, err := resolveCap(0, sampler, used)
	if err == nil {
		t.Fatalf("expected budget error, got nil")
	}
	var budgetErr *LogBufferBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("expected *LogBufferBudgetError, got %T", err)
	}
	wantLimit := int64(float64(4<<30) * MaxTotalFraction)
	if budgetErr.LimitBytes != wantLimit {
		t.Fatalf("LimitBytes = %d, want %d", budgetErr.LimitBytes, wantLimit)
	}
	if budgetErr.UsedBytes != used {
		t.Fatalf("UsedBytes = %d, want %d", budgetErr.UsedBytes, used)
	}
	if budgetErr.SystemBytes != 4<<30 {
		t.Fatalf("SystemBytes = %d, want %d", budgetErr.SystemBytes, 4<<30)
	}
	if !errors.Is(err, ErrLogBufferBudgetExceeded) {
		t.Fatalf("errors.Is(err, ErrLogBufferBudgetExceeded) = false")
	}
}

func TestResolveCap_AllowsWhenWithinBudget(t *testing.T) {
	t.Parallel()
	sampler := fakeMem{total: 16 << 30, available: 16 << 30}
	used := int64(10 * 32 << 20) // 320 MB used
	// 25% of 16 GB = 4 GB limit; 320 + 32 = 352 MB << 4 GB → allow
	cap, err := resolveCap(32<<20, sampler, used)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap != 32<<20 {
		t.Fatalf("cap = %d, want %d", cap, 32<<20)
	}
}

func TestResolveCap_NilSamplerSkipsBudget(t *testing.T) {
	t.Parallel()
	// Even with absurd usage, no error when sampler is nil
	_, err := resolveCap(32<<20, nil, 1<<40)
	if err != nil {
		t.Fatalf("nil-sampler err: %v", err)
	}
}

func TestClampCap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want int64
	}{
		{1, MinBufferCap},
		{MinBufferCap, MinBufferCap},
		{32 << 20, 32 << 20},
		{MaxBufferCap, MaxBufferCap},
		{1 << 30, MaxBufferCap},
	}
	for _, c := range cases {
		if got := clampCap(c.in); got != c.want {
			t.Errorf("clampCap(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
