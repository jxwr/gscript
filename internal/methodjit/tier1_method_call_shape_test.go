//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestTier1MethodValueCallShapeDetectsGetFieldMoveCall(t *testing.T) {
	top := compileProto(t, `
func step(self, n) { return n + 1 }
func caller(o, n) {
    f := o.step
    x := f(o, n)
    return x
}
`)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("caller proto not found")
	}
	callPC, callA := firstMethodShapeTestCall(caller)
	if callPC < 0 {
		t.Fatal("CALL not found")
	}
	inst := caller.Code[callPC]
	if !isBaselineMethodValueCall(caller, callPC, callA, vm.DecodeB(inst), vm.DecodeC(inst)) {
		t.Fatalf("CALL at pc %d A=%d was not classified as method-value dispatch", callPC, callA)
	}
}

func TestTier1MethodValueCallShapeRejectsPlainClosureCall(t *testing.T) {
	top := compileProto(t, `
func make_accumulator(start, delta) {
    value := start
    return func() {
        value = value + delta
        return value
    }
}
func caller() {
    acc := make_accumulator(7, 3)
    return acc()
}
`)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("caller proto not found")
	}
	callPC, callA := -1, 0
	for pc, inst := range caller.Code {
		if vm.DecodeOp(inst) == vm.OP_CALL && vm.DecodeB(inst) == 1 {
			callPC, callA = pc, vm.DecodeA(inst)
			break
		}
	}
	if callPC < 0 {
		t.Fatal("accumulator CALL not found")
	}
	inst := caller.Code[callPC]
	if isBaselineMethodValueCall(caller, callPC, callA, vm.DecodeB(inst), vm.DecodeC(inst)) {
		t.Fatalf("plain closure CALL at pc %d A=%d classified as method-value dispatch", callPC, callA)
	}
}

func TestTier1MethodValueCallShapeRejectsLibraryFieldCall(t *testing.T) {
	top := compileProto(t, `
func caller(x) {
    f := math.sqrt
    y := f(x)
    return y
}
`)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("caller proto not found")
	}
	callPC, callA := firstMethodShapeTestCall(caller)
	if callPC < 0 {
		t.Fatal("CALL not found")
	}
	inst := caller.Code[callPC]
	if isBaselineMethodValueCall(caller, callPC, callA, vm.DecodeB(inst), vm.DecodeC(inst)) {
		t.Fatalf("library field CALL at pc %d A=%d classified as receiver method dispatch", callPC, callA)
	}
}

func TestTier1DynamicMethodDispatchLoopStaysTier0(t *testing.T) {
	top := compileProto(t, `
func step(self, n) { return n + 1 }
func caller(items, n) {
    total := 0
    for i := 1; i <= n; i++ {
        o := items[i]
        f := o.step
        total = total + f(o, i)
    }
    return total
}
`)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("caller proto not found")
	}
	profile := analyzeFuncProfile(caller)
	if !shouldStayTier0DynamicMethodDispatchLoop(caller, profile) {
		t.Fatal("dynamic method-value dispatch loop was not classified as Tier0")
	}
}

func TestTier1StaticFunctionLoopDoesNotUseDynamicMethodTier0Gate(t *testing.T) {
	top := compileProto(t, `
func inc(n) { return n + 1 }
func caller(n) {
    total := 0
    for i := 1; i <= n; i++ {
        total = total + inc(i)
    }
    return total
}
`)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("caller proto not found")
	}
	profile := analyzeFuncProfile(caller)
	if shouldStayTier0DynamicMethodDispatchLoop(caller, profile) {
		t.Fatal("static function-call loop was classified as dynamic method dispatch")
	}
}

func TestTier1FieldHeavyLeafStaysTier0(t *testing.T) {
	top := compileProto(t, `
func step(a, tick) {
    a.x = a.x + a.vx
    a.y = a.y + a.vy
    return a.x + a.y + tick
}
`)
	step := findProtoByName(top, "step")
	if step == nil {
		t.Fatal("step proto not found")
	}
	if !shouldStayTier0FieldHeavyLeaf(step, analyzeFuncProfile(step)) {
		t.Fatal("field-heavy leaf was not classified as Tier0")
	}
}

func firstMethodShapeTestCall(proto *vm.FuncProto) (pc, a int) {
	for pc, inst := range proto.Code {
		if vm.DecodeOp(inst) == vm.OP_CALL {
			return pc, vm.DecodeA(inst)
		}
	}
	return -1, 0
}
