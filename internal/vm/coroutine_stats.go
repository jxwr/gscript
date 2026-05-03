package vm

import (
	"sync/atomic"

	rt "github.com/gscript/gscript/internal/runtime"
)

// CoroutineStatsSnapshot is a point-in-time copy of VM coroutine counters.
// It separates the synchronous leaf fast path from the goroutine/channel path
// so production runs can identify which coroutine mechanism dominates a script.
type CoroutineStatsSnapshot struct {
	Created         uint64
	Wrapped         uint64
	ResumeCalls     uint64
	YieldCalls      uint64
	LeafFastPath    uint64
	LeafFallbacks   uint64
	GoroutineStarts uint64
	Completed       uint64
	ResumeErrors    uint64
}

type coroutineStats struct {
	created         atomic.Uint64
	wrapped         atomic.Uint64
	resumeCalls     atomic.Uint64
	yieldCalls      atomic.Uint64
	leafFastPath    atomic.Uint64
	leafFallbacks   atomic.Uint64
	goroutineStarts atomic.Uint64
	completed       atomic.Uint64
	resumeErrors    atomic.Uint64
}

// EnableCoroutineStats enables coroutine runtime counters for this VM and any
// child VMs it creates after this call.
func (vm *VM) EnableCoroutineStats() {
	if vm.coroutineStats == nil {
		vm.coroutineStats = &coroutineStats{}
	}
}

// CoroutineStats returns a snapshot of the current coroutine counters.
func (vm *VM) CoroutineStats() CoroutineStatsSnapshot {
	if vm == nil || vm.coroutineStats == nil {
		return CoroutineStatsSnapshot{}
	}
	return vm.coroutineStats.snapshot()
}

func (s *coroutineStats) snapshot() CoroutineStatsSnapshot {
	if s == nil {
		return CoroutineStatsSnapshot{}
	}
	return CoroutineStatsSnapshot{
		Created:         s.created.Load(),
		Wrapped:         s.wrapped.Load(),
		ResumeCalls:     s.resumeCalls.Load(),
		YieldCalls:      s.yieldCalls.Load(),
		LeafFastPath:    s.leafFastPath.Load(),
		LeafFallbacks:   s.leafFallbacks.Load(),
		GoroutineStarts: s.goroutineStarts.Load(),
		Completed:       s.completed.Load(),
		ResumeErrors:    s.resumeErrors.Load(),
	}
}

func (vm *VM) recordCoroutineCreated(wrapped bool) {
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.created.Add(1)
	if wrapped {
		vm.coroutineStats.wrapped.Add(1)
	}
}

func (vm *VM) recordCoroutineResume() {
	rt.RecordRuntimePathCoroutineResume()
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.resumeCalls.Add(1)
}

func (vm *VM) recordCoroutineYield() {
	rt.RecordRuntimePathCoroutineYield()
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.yieldCalls.Add(1)
}

func (vm *VM) recordCoroutineLeafFastPath() {
	rt.RecordRuntimePathCoroutineFast()
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.leafFastPath.Add(1)
}

func (vm *VM) recordCoroutineLeafFallback() {
	rt.RecordRuntimePathCoroutineFallback()
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.leafFallbacks.Add(1)
}

func (vm *VM) recordCoroutineGoroutineStart() {
	rt.RecordRuntimePathCoroutineFallback()
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.goroutineStarts.Add(1)
}

func (vm *VM) recordCoroutineCompleted() {
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.completed.Add(1)
}

func (vm *VM) recordCoroutineResumeError() {
	rt.RecordRuntimePathCoroutineResumeError()
	if vm == nil || vm.coroutineStats == nil {
		return
	}
	vm.coroutineStats.resumeErrors.Add(1)
}
