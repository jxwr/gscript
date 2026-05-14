//go:build darwin && arm64

package methodjit

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestObserveTier2CallExitFeedbackUsesCallSourcePC(t *testing.T) {
	callee := &vm.FuncProto{Name: "callee", Code: []uint32{vm.EncodeABC(vm.OP_RETURN, 0, 1, 0)}}
	caller := &vm.FuncProto{
		Code: []uint32{
			vm.EncodeABC(vm.OP_MOVE, 1, 0, 0),
			vm.EncodeABC(vm.OP_CALL, 0, 2, 2),
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
		MaxStack: 4,
	}
	caller.EnsureFeedback()

	cl := vm.NewClosure(callee)
	regs := []runtime.Value{
		runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl),
		runtime.IntValue(42),
		runtime.NilValue(),
		runtime.NilValue(),
	}
	cf := &CompiledFunction{
		Proto: caller,
		ExitSites: map[int]ExitSiteMeta{
			99: {PC: 1, Op: "Call", Reason: "Call"},
		},
	}
	ctx := &ExecContext{CallID: 99, CallSlot: 0, CallNArgs: 1, CallNRets: 1}

	observeTier2CallExitFeedback(caller, cf, ctx, regs, 0)

	if got := caller.CallSiteFeedback[1].Count; got != 1 {
		t.Fatalf("call feedback at source PC count=%d, want 1", got)
	}
	if got := caller.CallSiteFeedback[0].Count; got != 0 {
		t.Fatalf("call feedback leaked to preceding PC count=%d, want 0", got)
	}
	if got := caller.CallSiteFeedback[1].NArgs; got != 1 {
		t.Fatalf("NArgs=%d want 1", got)
	}
	if got := caller.CallSiteFeedback[1].ResultArity; got != 2 {
		t.Fatalf("ResultArity=%d want raw CALL C=2", got)
	}
	if got, ok := caller.CallSiteFeedback[1].StableCalleeVMProto(); !ok || got != callee {
		t.Fatalf("StableCalleeVMProto=(%v,%v), want callee", got, ok)
	}
}

func TestObserveTier2CallExitResultFeedbackUsesCallSourcePC(t *testing.T) {
	caller := &vm.FuncProto{
		Code: []uint32{
			vm.EncodeABC(vm.OP_MOVE, 1, 0, 0),
			vm.EncodeABC(vm.OP_CALL, 0, 2, 2),
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
		MaxStack: 4,
	}
	caller.EnsureFeedback()
	cf := &CompiledFunction{
		Proto: caller,
		ExitSites: map[int]ExitSiteMeta{
			99: {PC: 1, Op: "Call", Reason: "Call"},
		},
	}
	ctx := &ExecContext{CallID: 99, CallSlot: 0, CallNArgs: 1, CallNRets: 1}

	observeTier2CallExitResultFeedback(caller, cf, ctx, runtime.IntValue(17), true)

	min, max, ok := caller.CallSiteFeedback[1].ResultRange.StableRange()
	if !ok || min != 17 || max != 17 {
		t.Fatalf("result range=(%d,%d,%v), want 17..17", min, max, ok)
	}
	if _, _, ok := caller.CallSiteFeedback[0].ResultRange.StableRange(); ok {
		t.Fatalf("result feedback leaked to preceding PC")
	}
}

func TestObserveTier2CallExitResultFeedbackProjectsFieldCallFloor(t *testing.T) {
	caller := &vm.FuncProto{
		Code: []uint32{
			vm.EncodeABC(vm.OP_CALL, 0, 2, 2),
		},
	}
	caller.EnsureFeedback()
	cf := &CompiledFunction{
		Proto: caller,
		ExitSites: map[int]ExitSiteMeta{
			7: {PC: 0, Op: "FieldCallFloor", Reason: "FieldCallFloor"},
		},
	}
	ctx := &ExecContext{CallID: 7, CallSlot: 0, CallNArgs: 1, CallNRets: 0}

	observeTier2CallExitResultFeedback(caller, cf, ctx, runtime.FloatValue(17.9), true)

	min, max, ok := caller.CallSiteFeedback[0].ResultRange.StableRange()
	if !ok || min != 17 || max != 17 {
		t.Fatalf("projected result range=(%d,%d,%v), want 17..17", min, max, ok)
	}
}

func TestMergeTier2CallCacheFeedbackRecordsPolymorphicVMProtos(t *testing.T) {
	calleeA := &vm.FuncProto{Name: "a"}
	calleeB := &vm.FuncProto{Name: "b"}
	caller := &vm.FuncProto{
		Name:             "caller",
		Code:             []uint32{vm.EncodeABC(vm.OP_CALL, 0, 2, 2)},
		CallSiteFeedback: make([]vm.CallSiteFeedback, 1),
	}
	cf := &CompiledFunction{}
	mergeTier2CallCacheEntryForTest(caller, cf, 0, 0, calleeA, calleeB)

	mergeTier2CallCacheFeedback(caller, cf)

	fb := caller.CallSiteFeedback[0]
	if fb.Count < wholeCallKernelMinStableObservations {
		t.Fatalf("feedback count=%d, want at least %d", fb.Count, wholeCallKernelMinStableObservations)
	}
	if fb.NArgs != 1 || fb.ResultArity != 2 {
		t.Fatalf("arity feedback nArgs=%d result=%d", fb.NArgs, fb.ResultArity)
	}
	if fb.Flags&vm.CallSiteCalleePolymorphic == 0 {
		t.Fatalf("callee polymorphic flag not set: %#v", fb)
	}
	protos := fb.PolymorphicVMProtos()
	if len(protos) != 2 || protos[0] != calleeA || protos[1] != calleeB {
		t.Fatalf("polymorphic protos=%#v, want [%p %p]", protos, calleeA, calleeB)
	}
}

func TestMergeBaselineCallCacheFeedbackRecordsStableVMClosure(t *testing.T) {
	callee := &vm.FuncProto{Name: "callee", Code: []uint32{vm.EncodeABC(vm.OP_RETURN, 0, 1, 0)}}
	caller := &vm.FuncProto{
		Name:             "caller",
		Code:             []uint32{vm.EncodeABC(vm.OP_CALL, 0, 1, 2)},
		CallSiteFeedback: make([]vm.CallSiteFeedback, 1),
	}
	cl := vm.NewClosure(callee)
	boxed := runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)
	bf := &BaselineFunc{CallCache: make([]uint64, baselineCallCacheStride)}
	bf.CallCache[baselineCallCacheBoxedOff/8] = uint64(boxed)
	bf.CallCache[baselineCallCacheProtoOff/8] = uint64(uintptr(unsafe.Pointer(callee)))

	mergeBaselineCallCacheFeedback(caller, bf)

	fb := caller.CallSiteFeedback[0]
	if fb.Count < wholeCallKernelMinStableObservations {
		t.Fatalf("feedback count=%d, want at least %d", fb.Count, wholeCallKernelMinStableObservations)
	}
	if fb.NArgs != 0 || fb.ResultArity != 2 {
		t.Fatalf("arity feedback nArgs=%d result=%d", fb.NArgs, fb.ResultArity)
	}
	closure, gotCallee, ok := fb.StableCalleeVMClosure()
	if !ok || gotCallee != callee || closure != uintptr(unsafe.Pointer(cl)) {
		t.Fatalf("StableCalleeVMClosure=(%#x,%p,%v), want (%#x,%p,true)",
			closure, gotCallee, ok, uintptr(unsafe.Pointer(cl)), callee)
	}
}
