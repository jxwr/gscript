//go:build darwin && arm64

// tier1_handlers_test.go contains unit tests for the Tier 1 exit handlers,
// verifying that type feedback is recorded correctly when exiting to Go.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

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

// TestHandleLen_RecordsFeedback verifies that OP_LEN exits record int result
// feedback so Tier 2 can guard and specialize loop bounds fed by #table/#string.
func TestHandleLen_RecordsFeedback(t *testing.T) {
	proto := &vm.FuncProto{
		Code:     []uint32{vm.EncodeABC(vm.OP_LEN, 0, 1, 0)},
		MaxStack: 4,
	}
	proto.EnsureFeedback()

	tbl := runtime.NewTable()
	tbl.RawSetInt(1, runtime.IntValue(10))
	tbl.RawSetInt(2, runtime.IntValue(20))

	regs := make([]runtime.Value, 4)
	regs[1] = runtime.TableValue(tbl)
	ctx := &ExecContext{
		BaselineA:  0,
		BaselineB:  1,
		BaselinePC: 1,
	}

	engine := &BaselineJITEngine{}
	if err := engine.handleLen(ctx, regs, 0, proto); err != nil {
		t.Fatalf("handleLen returned error: %v", err)
	}
	if !regs[0].IsInt() {
		t.Fatalf("expected int result in R[0], got type %v", regs[0].Type())
	}
	if proto.Feedback[0].Result != vm.FBInt {
		t.Fatalf("expected FBInt feedback at PC 0, got %d", proto.Feedback[0].Result)
	}
}

func TestHandleGetTable_RecordsDenseMatrixFeedback(t *testing.T) {
	proto := &vm.FuncProto{
		Code:      []uint32{vm.EncodeABC(vm.OP_GETTABLE, 0, 1, vm.RKBit+0)},
		Constants: []runtime.Value{runtime.IntValue(0)},
		MaxStack:  4,
	}
	proto.EnsureFeedback()

	tbl := runtime.NewDenseMatrix(2, runtime.AutoDenseMatrixMinStride)
	regs := make([]runtime.Value, 4)
	regs[1] = runtime.TableValue(tbl)
	ctx := &ExecContext{
		BaselineA:  0,
		BaselineB:  1,
		BaselineC:  int64(vm.RKBit + 0),
		BaselinePC: 1,
	}

	engine := &BaselineJITEngine{}
	if err := engine.handleGetTable(ctx, regs, 0, proto); err != nil {
		t.Fatalf("handleGetTable returned error: %v", err)
	}
	if got := proto.TableKeyFeedback[0].DenseMatrix; got != vm.FBDenseMatrixYes {
		t.Fatalf("dense matrix feedback = %d, want yes", got)
	}
}

func TestTier2TableExitRecordsStableStringShapeFieldFeedback(t *testing.T) {
	proto := &vm.FuncProto{
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETTABLE, 0, 1, 2),
			vm.EncodeABC(vm.OP_SETTABLE, 1, 2, 3),
		},
		MaxStack: 4,
	}
	proto.EnsureFeedback()

	tbl := runtime.NewTable()
	tbl.RawSetString("name", runtime.IntValue(10))
	regs := []runtime.Value{
		runtime.NilValue(),
		runtime.TableValue(tbl),
		runtime.StringValue("name"),
		runtime.IntValue(11),
	}
	tm := NewTieringManager()

	getCtx := &ExecContext{
		TableOp:      TableOpGetTable,
		TableSlot:    1,
		TableKeySlot: 2,
		TableAux:     0,
		TableAux2:    0,
	}
	if err := tm.executeTableExit(getCtx, regs, 0, proto, nil); err != nil {
		t.Fatalf("execute get table exit: %v", err)
	}
	if !regs[0].IsInt() || regs[0].Int() != 10 {
		t.Fatalf("get result = %v, want 10", regs[0])
	}
	if key, shapeID, fieldIdx, ok := proto.TableKeyFeedback[0].StableStringShapeField(); !ok || key != "name" || shapeID == 0 || fieldIdx < 0 {
		t.Fatalf("GETTABLE feedback did not expose stable string shape field: key=%q shape=%d field=%d ok=%v feedback=%#v",
			key, shapeID, fieldIdx, ok, proto.TableKeyFeedback[0])
	}

	setCtx := &ExecContext{
		TableOp:      TableOpSetTable,
		TableSlot:    1,
		TableKeySlot: 2,
		TableValSlot: 3,
		TableAux2:    1,
	}
	if err := tm.executeTableExit(setCtx, regs, 0, proto, nil); err != nil {
		t.Fatalf("execute set table exit: %v", err)
	}
	if got := tbl.RawGetString("name"); !got.IsInt() || got.Int() != 11 {
		t.Fatalf("stored value = %v, want 11", got)
	}
	if key, shapeID, fieldIdx, ok := proto.TableKeyFeedback[1].StableStringShapeField(); !ok || key != "name" || shapeID == 0 || fieldIdx < 0 {
		t.Fatalf("SETTABLE feedback did not expose stable string shape field: key=%q shape=%d field=%d ok=%v feedback=%#v",
			key, shapeID, fieldIdx, ok, proto.TableKeyFeedback[1])
	}
}
