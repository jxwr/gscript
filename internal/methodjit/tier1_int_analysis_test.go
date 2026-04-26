//go:build darwin && arm64

package methodjit

import (
	"os"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// bit returns a uint64 with the specified slot bits set.
func bit(slots ...int) uint64 {
	var b uint64
	for _, s := range slots {
		b |= uint64(1) << uint(s)
	}
	return b
}

// TestKnownInt_SimpleAdd: a 2-param proto with LOADINT + ADD + RETURN.
// Only slot 0 is arith-used (slot 1 never appears as an operand), so the
// guardedParams seed contains {0} and the per-PC set tracks slots 0, 2, 3.
func TestKnownInt_SimpleAdd(t *testing.T) {
	proto := &vm.FuncProto{
		NumParams: 2,
		MaxStack:  8,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 1),  // R(2) = 1
			vm.EncodeABC(vm.OP_ADD, 3, 0, 2),    // R(3) = R(0) + R(2)
			vm.EncodeABC(vm.OP_RETURN, 3, 2, 0), // return R(3)
		},
	}
	info, ok := computeKnownIntSlots(proto)
	if !ok {
		t.Fatalf("expected eligible, got ok=false")
	}
	if got, want := info.guardedParams, bit(0); got != want {
		t.Errorf("guardedParams=%#x want %#x", got, want)
	}
	if got, want := info.knownIntAt(0), bit(0); got != want {
		t.Errorf("knownIntAt(0)=%#x want %#x", got, want)
	}
	if got, want := info.knownIntAt(1), bit(0, 2); got != want {
		t.Errorf("knownIntAt(1)=%#x want %#x", got, want)
	}
	if got, want := info.knownIntAt(2), bit(0, 2, 3); got != want {
		t.Errorf("knownIntAt(2)=%#x want %#x", got, want)
	}
}

func TestKnownInt_ModKeepsIntResult(t *testing.T) {
	proto := &vm.FuncProto{
		NumParams: 1,
		MaxStack:  6,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 7),
			vm.EncodeABC(vm.OP_MOD, 2, 0, 1),
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 0),
			vm.EncodeABC(vm.OP_EQ, 0, 2, 3),
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}
	info, ok := computeKnownIntSlots(proto)
	if !ok {
		t.Fatalf("expected eligible, got ok=false")
	}
	if got, want := info.guardedParams, bit(0); got != want {
		t.Errorf("guardedParams=%#x want %#x", got, want)
	}
	if !info.isKnownIntOperand(1, 0, proto.Constants) ||
		!info.isKnownIntOperand(1, 1, proto.Constants) {
		t.Fatalf("MOD operands not known-int at pc=1: known=%#x", info.knownIntAt(1))
	}
	if got := info.knownIntAt(2); got&bit(2) == 0 {
		t.Errorf("knownIntAt(2)=%#x missing MOD result slot 2", got)
	}
	if !info.isKnownIntOperand(3, 2, proto.Constants) ||
		!info.isKnownIntOperand(3, 3, proto.Constants) {
		t.Fatalf("EQ operands after MOD not known-int at pc=3: known=%#x", info.knownIntAt(3))
	}
}

// TestKnownInt_Ackermann: verifies that on real ackermann bytecode, the
// second EQ and both SUB PCs have both operands marked as known-int.
func TestKnownInt_Ackermann(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/ackermann.gs")
	if err != nil {
		t.Fatalf("read ackermann.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))
	ack := findProtoByName(top, "ack")
	if ack == nil {
		t.Fatalf("function 'ack' not found")
	}

	info, ok := computeKnownIntSlots(ack)
	if !ok {
		t.Fatalf("expected ack to be eligible, got ok=false")
	}

	// Walk the bytecode and verify every EQ/SUB has both operands known-int.
	foundEQ := 0
	foundSUB := 0
	for pc, inst := range ack.Code {
		op := vm.DecodeOp(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		switch op {
		case vm.OP_EQ:
			if !info.isKnownIntOperand(pc, b, ack.Constants) {
				t.Errorf("EQ at pc=%d: operand B=%d not known-int (known=%#x)",
					pc, b, info.knownIntAt(pc))
			}
			if !info.isKnownIntOperand(pc, c, ack.Constants) {
				t.Errorf("EQ at pc=%d: operand C=%d not known-int (known=%#x)",
					pc, c, info.knownIntAt(pc))
			}
			foundEQ++
		case vm.OP_SUB:
			if !info.isKnownIntOperand(pc, b, ack.Constants) {
				t.Errorf("SUB at pc=%d: operand B=%d not known-int (known=%#x)",
					pc, b, info.knownIntAt(pc))
			}
			if !info.isKnownIntOperand(pc, c, ack.Constants) {
				t.Errorf("SUB at pc=%d: operand C=%d not known-int (known=%#x)",
					pc, c, info.knownIntAt(pc))
			}
			foundSUB++
		}
	}
	if foundEQ < 2 {
		t.Errorf("expected >=2 OP_EQ in ack, got %d", foundEQ)
	}
	if foundSUB < 2 {
		t.Errorf("expected >=2 OP_SUB in ack, got %d", foundSUB)
	}
}

// TestKnownInt_FloatConstant: a proto whose constant pool contains a float
// referenced by OP_LOADK is rejected by the eligibility gate.
func TestKnownInt_FloatConstant(t *testing.T) {
	proto := &vm.FuncProto{
		NumParams: 1,
		MaxStack:  4,
		Constants: []runtime.Value{runtime.FloatValue(3.14)},
		Code: []uint32{
			vm.EncodeABx(vm.OP_LOADK, 1, 0),
			vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),
		},
	}
	info, ok := computeKnownIntSlots(proto)
	if ok || info != nil {
		t.Fatalf("expected (nil, false), got info=%+v ok=%v", info, ok)
	}
}

// TestKnownInt_MaxStackTooLarge: protos with MaxStack > maxTrackedSlots are
// rejected unconditionally.
func TestKnownInt_MaxStackTooLarge(t *testing.T) {
	proto := &vm.FuncProto{
		NumParams: 2,
		MaxStack:  128,
		Code:      []uint32{vm.EncodeABC(vm.OP_RETURN, 0, 1, 0)},
	}
	info, ok := computeKnownIntSlots(proto)
	if ok || info != nil {
		t.Fatalf("expected (nil, false) for MaxStack=128, got info=%+v ok=%v", info, ok)
	}
}

// TestKnownInt_BranchTargetParamsSurvive: params must remain in the known-int
// set at branch targets (the central invariant for cross-block spec).
func TestKnownInt_BranchTargetParamsSurvive(t *testing.T) {
	// Proto layout:
	//   pc=0 LOADINT R(2) = 5
	//   pc=1 JMP +1 (target=3)
	//   pc=2 LOADINT R(3) = 99 (skipped)
	//   pc=3 ADD R(2) = R(0) + R(1)   ← branch target; params must still be known
	//   pc=4 RETURN
	proto := &vm.FuncProto{
		NumParams: 2,
		MaxStack:  8,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 5),
			vm.EncodesBx(vm.OP_JMP, 1),
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 99),
			vm.EncodeABC(vm.OP_ADD, 2, 0, 1),
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}
	info, ok := computeKnownIntSlots(proto)
	if !ok {
		t.Fatalf("expected eligible, got ok=false")
	}
	// At pc=3 (the branch target), params {0,1} must be set.
	got := info.knownIntAt(3)
	if got&bit(0, 1) != bit(0, 1) {
		t.Errorf("knownIntAt(3)=%#x missing param bits 0,1", got)
	}
	// And ADD at pc=3 should have its result slot (2) in the set at pc=4.
	if info.knownIntAt(4)&bit(2) == 0 {
		t.Errorf("knownIntAt(4)=%#x missing slot 2 after ADD", info.knownIntAt(4))
	}
}

// TestKnownInt_CallClearsReturnSlot: OP_CALL A=4 B=2 C=2 should clear slot 4
// (the single return value) but not slot 5. A trailing ADD keeps the proto
// eligible and forces both params into the guardedParams seed.
func TestKnownInt_CallClearsReturnSlot(t *testing.T) {
	// pc=0 ADD R(3) = R(0) + R(1)  (arith use of both params)
	// pc=1 LOADINT R(4)=7
	// pc=2 LOADINT R(5)=8
	// pc=3 CALL A=4 B=2 C=2         (1 return value → clears slot 4 only)
	// pc=4 RETURN R(3), 2
	proto := &vm.FuncProto{
		NumParams: 2,
		MaxStack:  8,
		Code: []uint32{
			vm.EncodeABC(vm.OP_ADD, 3, 0, 1),
			vm.EncodeAsBx(vm.OP_LOADINT, 4, 7),
			vm.EncodeAsBx(vm.OP_LOADINT, 5, 8),
			vm.EncodeABC(vm.OP_CALL, 4, 2, 2),
			vm.EncodeABC(vm.OP_RETURN, 3, 2, 0),
		},
	}
	info, ok := computeKnownIntSlots(proto)
	if !ok {
		t.Fatalf("expected eligible, got ok=false")
	}
	// Before the CALL (pc=3), both 4 and 5 are known-int.
	if got := info.knownIntAt(3); got&bit(4, 5) != bit(4, 5) {
		t.Errorf("knownIntAt(3)=%#x missing slots 4,5 before CALL", got)
	}
	// After the CALL (pc=4), slot 4 must be cleared; slot 5 must remain,
	// and the arith-seeded params {0,1} must remain.
	got := info.knownIntAt(4)
	if got&bit(4) != 0 {
		t.Errorf("knownIntAt(4)=%#x: slot 4 (CALL return) not cleared", got)
	}
	if got&bit(5) == 0 {
		t.Errorf("knownIntAt(4)=%#x: slot 5 wrongly cleared (C=2 means only 1 ret)", got)
	}
	if got&bit(0, 1) != bit(0, 1) {
		t.Errorf("knownIntAt(4)=%#x: arith-used params wrongly cleared", got)
	}
}

// TestKnownIntInfo_NilSafe locks in that the accessors are nil-safe, so
// downstream callers (Task 2b emitter) can call knownIntAt without a guard.
func TestKnownIntInfo_NilSafe(t *testing.T) {
	var k *knownIntInfo
	if got := k.knownIntAt(0); got != 0 {
		t.Fatalf("nil knownIntInfo.knownIntAt should return 0, got %d", got)
	}
	if got := k.knownIntAt(-1); got != 0 {
		t.Fatalf("nil knownIntInfo.knownIntAt(-1) should return 0, got %d", got)
	}
	if k.isKnownIntOperand(0, 0, nil) {
		t.Fatalf("nil knownIntInfo.isKnownIntOperand should return false")
	}
	if k.isKnownIntOperand(0, vm.RKBit, nil) {
		t.Fatalf("nil knownIntInfo.isKnownIntOperand(RK) should return false")
	}
}
