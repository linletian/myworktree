package framework

import (
	"errors"
	"fmt"

	"github.com/shirou/gopsutil/v4/mem"
)

// Buffer-cap sizing constants for ring buffers. Same defaults the
// pre-refactor internal/instance/sizing.go exposed; kept here so the
// framework owns the policy and kinds just ask for "a buffer".
const (
	DefaultBufferCap int64 = 32 << 20 // 32 MB
	MinBufferCap     int64 = 16 << 20 // 16 MB
	MaxBufferCap     int64 = 256 << 20 // 256 MB
	MaxTotalFraction       = 0.25       // sum of live buffers ≤ 25% of system RAM
)

// ErrLogBufferBudgetExceeded is the sentinel wrapped by
// LogBufferBudgetError when a new instance would push total buffer
// memory past the allowed budget.
var ErrLogBufferBudgetExceeded = errors.New("log buffer budget exceeded")

// LogBufferBudgetError is returned when the budget check fails. HTTP
// handlers use errors.As to render it as a 503.
type LogBufferBudgetError struct {
	UsedBytes   int64
	LimitBytes  int64
	SystemBytes int64
}

func (e *LogBufferBudgetError) Error() string {
	return fmt.Sprintf(
		"log buffer budget exceeded: used %d / limit %d (system %d bytes)",
		e.UsedBytes, e.LimitBytes, e.SystemBytes,
	)
}

func (e *LogBufferBudgetError) Unwrap() error { return ErrLogBufferBudgetExceeded }

// MemStat is the abstract memory snapshot consumed by the buffer
// sizing logic. It is intentionally decoupled from any specific
// memory-sampling library so tests can construct deterministic
// values without pulling in gopsutil.
type MemStat struct {
	Total     int64
	Available int64
	Used      int64
}

// MemSampler abstracts platform memory queries. Production wraps
// gopsutil/v4/mem; tests inject a fake.
type MemSampler interface {
	VirtualMemory() (MemStat, error)
}

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

// DefaultSampler returns the production MemSampler (gopsutil).
func DefaultSampler() MemSampler { return gopsutilMem{} }

// ResolveCap computes the per-instance ring buffer cap and enforces
// the global budget. Kinds call this in Spawn BEFORE forking the
// process so a budget-exceeded error short-circuits the heavy work.
//
// Rules:
//  1. If cfgBytes > 0, use it, clamped to [MinBufferCap, MaxBufferCap].
//  2. Otherwise, take availableMem / 16, clamped.
//  3. Return *LogBufferBudgetError if the new cap would push the sum
//     past MaxTotalFraction × system memory.
//
// Pass sampler = nil to disable the budget check (tests use this).
func ResolveCap(cfgBytes int64, sampler MemSampler, totalBufBytes int64) (int64, error) {
	capBytes := AdaptiveCap(cfgBytes, sampler)
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

// AdaptiveCap returns the per-instance cap without budget enforcement.
// Used by callers that validate the budget elsewhere.
func AdaptiveCap(cfgBytes int64, sampler MemSampler) int64 {
	if cfgBytes > 0 {
		return clampCap(cfgBytes)
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

func clampCap(v int64) int64 {
	if v < MinBufferCap {
		return MinBufferCap
	}
	if v > MaxBufferCap {
		return MaxBufferCap
	}
	return v
}
