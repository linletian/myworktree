package instance

import (
	"errors"
	"fmt"

	"github.com/shirou/gopsutil/v4/mem"
)

const (
	// DefaultBufferCap is the per-instance ring buffer cap when no
	// user override is supplied and adaptive sampling fails.
	DefaultBufferCap int64 = 32 << 20 // 32 MB

	// MinBufferCap is the floor for per-instance cap regardless of
	// user override or adaptive computation.
	MinBufferCap int64 = 16 << 20 // 16 MB

	// MaxBufferCap is the hard ceiling per instance. Never exceeded.
	MaxBufferCap int64 = 256 << 20 // 256 MB

	// MaxTotalFraction caps the sum of all live buffers at this fraction
	// of system RAM. Exceeding this rejects new instance creation with
	// ErrLogBufferBudgetExceeded.
	MaxTotalFraction = 0.25
)

// ErrLogBufferBudgetExceeded is the sentinel wrapped by LogBufferBudgetError
// when a new instance would push total buffer memory past the allowed budget.
var ErrLogBufferBudgetExceeded = errors.New("log buffer budget exceeded")

// LogBufferBudgetError is a structured error returned by Manager.Start when
// the new instance would exceed the global buffer budget. HTTP handlers use
// errors.As to render it as a 503 with a structured JSON body.
type LogBufferBudgetError struct {
	UsedBytes   int64 // current sum of all live buffer caps
	LimitBytes  int64 // MaxTotalFraction * system RAM (rounded down)
	SystemBytes int64 // total system RAM (informational)
}

func (e *LogBufferBudgetError) Error() string {
	return fmt.Sprintf(
		"log buffer budget exceeded: used %d / limit %d (system %d bytes)",
		e.UsedBytes, e.LimitBytes, e.SystemBytes,
	)
}

// Unwrap enables errors.Is(err, ErrLogBufferBudgetExceeded).
func (e *LogBufferBudgetError) Unwrap() error { return ErrLogBufferBudgetExceeded }

// MemStat is the abstract memory snapshot consumed by the buffer sizing
// logic. It is intentionally decoupled from any specific memory-sampling
// library so tests can construct deterministic values without pulling in
// gopsutil.
type MemStat struct {
	Total     int64
	Available int64
	Used      int64
}

// MemSampler abstracts platform memory queries. The production implementation
// wraps gopsutil/v4/mem; tests inject a fake.
type MemSampler interface {
	VirtualMemory() (MemStat, error)
}

// gopsutilMem is the production MemSampler backed by gopsutil/v4/mem.
type gopsutilMem struct{}

func (gopsutilMem) VirtualMemory() (MemStat, error) {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return MemStat{}, err
	}
	if vm == nil {
		return MemStat{}, nil
	}
	return MemStat{
		Total:     int64(vm.Total),
		Available: int64(vm.Available),
		Used:      int64(vm.Used),
	}, nil
}

// resolveCap computes the per-instance ring buffer cap.
//
// Rules (applied in order):
//  1. If cfgBytes > 0, use it, clamped to [MinBufferCap, MaxBufferCap].
//  2. Otherwise, take availableMem / 16, clamped to [MinBufferCap, MaxBufferCap].
//     "Available" prefers sampler.Available, falls back to Total - Used.
//  3. Return ErrLogBufferBudgetExceeded (as *LogBufferBudgetError) if the new
//     cap would push the sum of all live caps past MaxTotalFraction × system
//     memory.
//
// Pass sampler=nil to disable the budget check (used by tests and by callers
// that already validated the budget elsewhere).
func resolveCap(cfgBytes int64, sampler MemSampler, totalBufBytes int64) (int64, error) {
	return resolveCapFromAvailable(cfgBytes, sampler, totalBufBytes, -1)
}

// resolveCapFromAvailable is like resolveCap but takes a pre-computed
// `availableBytes` value (≥ 0) for the adaptive-cap path, decoupling it from
// a fresh sampler call. This is used by the Manager's 60-second memory-sample
// cache: the cap is allowed to be slightly stale, but the budget check below
// still queries the sampler live.
//
// `availableBytes = -1` means "compute available from sampler" (preserves the
// original resolveCap semantics).
func resolveCapFromAvailable(cfgBytes int64, sampler MemSampler, totalBufBytes int64, availableBytes int64) (int64, error) {
	capBytes := adaptiveCapFromAvailable(cfgBytes, sampler, availableBytes)
	if sampler != nil {
		vm, err := sampler.VirtualMemory()
		if err == nil && vm.Total > 0 {
			limit := int64(float64(vm.Total) * MaxTotalFraction)
			if totalBufBytes+capBytes > limit {
				return 0, &LogBufferBudgetError{
					UsedBytes:   totalBufBytes,
					LimitBytes:  limit,
					SystemBytes: vm.Total,
				}
			}
		}
	}
	return capBytes, nil
}

// adaptiveCapFromAvailable returns the per-instance cap (without budget
// enforcement). `availableBytes = -1` falls back to sampling the platform
// memory (gated by sampler != nil).
func adaptiveCapFromAvailable(cfgBytes int64, sampler MemSampler, availableBytes int64) int64 {
	if cfgBytes > 0 {
		return clampCap(cfgBytes)
	}
	if availableBytes >= 0 {
		return clampCap(availableBytes / 16)
	}
	if sampler == nil {
		return DefaultBufferCap
	}
	vm, err := sampler.VirtualMemory()
	if err != nil {
		return DefaultBufferCap
	}
	avail := vm.Available
	if avail <= 0 {
		avail = vm.Total - vm.Used
	}
	if avail <= 0 {
		return DefaultBufferCap
	}
	return clampCap(avail / 16)
}

// adaptiveCap returns the per-instance cap (without budget enforcement).
// Exposed for tests.
func adaptiveCap(cfgBytes int64, sampler MemSampler) int64 {
	return adaptiveCapFromAvailable(cfgBytes, sampler, -1)
}

func clampCap(v int64) int64 {
	if v < MinBufferCap {
		return MinBufferCap
	}
	if v > MaxBufferCap {
		return MaxBufferCap
	}
	return v
}
