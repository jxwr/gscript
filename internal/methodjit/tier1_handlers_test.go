//go:build darwin && arm64

// tier1_handlers_test.go contains unit tests for the Tier 1 exit handlers,
// verifying that type feedback is recorded correctly when exiting to Go.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestIsTransientOpExit verifies the classification of exit opcodes as
// transient (cache-backed, one-shot) vs persistent (recurring). Transient
// exits must preserve DirectEntryPtr and skip globalCacheGen bumps.
func TestIsTransientOpExit(t *testing.T) {
	if !isTransientOpExit(vm.OP_GETGLOBAL) {
		t.Errorf("OP_GETGLOBAL should be transient")
	}
	if isTransientOpExit(vm.OP_CALL) {
		t.Errorf("OP_CALL should not be transient")
	}
	if isTransientOpExit(vm.OP_NEWTABLE) {
		t.Errorf("OP_NEWTABLE should not be transient")
	}
}

// TestHandleGetField_RecordsFeedback verifies that handleGetField records
// result type feedback into proto.Feedback so Tier 2 can specialize.
func TestHandleGetField_RecordsFeedback(t *testing.T) {
	// Build a minimal FuncProto with one GETFIELD instruction at PC 0.
	// GETFIELD A B C: R(A) = R(B).Constants[C]
	// A=0, B=1, C=0 (constant index for field name)
	proto := &vm.FuncProto{
		Code:      []uint32{vm.EncodeABC(vm.OP_GETFIELD, 0, 1, 0)},
		Constants: []runtime.Value{runtime.StringValue("x")},
		MaxStack:  4,
	}
	proto.EnsureFeedback()

	// Verify feedback starts as unobserved.
	if proto.Feedback[0].Result != vm.FBUnobserved {
		t.Fatalf("expected FBUnobserved initially, got %d", proto.Feedback[0].Result)
	}

	// Create a table with a float field.
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.FloatValue(3.14))

	// Set up registers: R[1] = table, R[0] = destination.
	regs := make([]runtime.Value, 4)
	regs[1] = runtime.TableValue(tbl)

	// Set up ExecContext: BaselinePC = pc + 1 = 1 (resume PC).
	ctx := &ExecContext{
		BaselineA:  0,
		BaselineB:  1,
		BaselineC:  0,
		BaselinePC: 1, // resume PC; current instruction PC = 1 - 1 = 0
	}

	engine := &BaselineJITEngine{}
	err := engine.handleGetField(ctx, regs, 0, proto)
	if err != nil {
		t.Fatalf("handleGetField returned error: %v", err)
	}

	// Verify the result register got the correct value.
	if !regs[0].IsFloat() {
		t.Errorf("expected float result in R[0], got type %v", regs[0].Type())
	}

	// Verify feedback was recorded as FBFloat.
	if proto.Feedback[0].Result != vm.FBFloat {
		t.Errorf("expected FBFloat feedback at PC 0, got %d", proto.Feedback[0].Result)
	}
}

// TestHandleGetTable_RecordsFeedback verifies that handleGetTable records
// result type feedback into proto.Feedback so Tier 2 can specialize.
func TestHandleGetTable_RecordsFeedback(t *testing.T) {
	// Build a minimal FuncProto with one GETTABLE instruction at PC 0.
	// GETTABLE A B C: R(A) = R(B)[RK(C)]
	// A=0, B=1, C=256+0 (RK bit + constant index 0) => key is Constants[0]
	proto := &vm.FuncProto{
		Code:      []uint32{vm.EncodeABC(vm.OP_GETTABLE, 0, 1, vm.RKBit+0)},
		Constants: []runtime.Value{runtime.StringValue("y")},
		MaxStack:  4,
	}
	proto.EnsureFeedback()

	// Verify feedback starts as unobserved.
	if proto.Feedback[0].Result != vm.FBUnobserved {
		t.Fatalf("expected FBUnobserved initially, got %d", proto.Feedback[0].Result)
	}

	// Create a table with a float field.
	tbl := runtime.NewTable()
	tbl.RawSetString("y", runtime.FloatValue(2.71))

	// Set up registers: R[1] = table, R[0] = destination.
	regs := make([]runtime.Value, 4)
	regs[1] = runtime.TableValue(tbl)

	// Set up ExecContext: BaselinePC = pc + 1 = 1 (resume PC).
	ctx := &ExecContext{
		BaselineA:  0,
		BaselineB:  1,
		BaselineC:  int64(vm.RKBit + 0),
		BaselinePC: 1, // resume PC; current instruction PC = 1 - 1 = 0
	}

	engine := &BaselineJITEngine{}
	err := engine.handleGetTable(ctx, regs, 0, proto)
	if err != nil {
		t.Fatalf("handleGetTable returned error: %v", err)
	}

	// Verify the result register got the correct value.
	if !regs[0].IsFloat() {
		t.Errorf("expected float result in R[0], got type %v", regs[0].Type())
	}

	// Verify feedback was recorded as FBFloat.
	if proto.Feedback[0].Result != vm.FBFloat {
		t.Errorf("expected FBFloat feedback at PC 0, got %d", proto.Feedback[0].Result)
	}
}
