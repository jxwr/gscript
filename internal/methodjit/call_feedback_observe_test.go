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
