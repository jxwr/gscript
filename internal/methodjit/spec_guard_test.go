//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestSpecGuard_StringFormatIntCandidate(t *testing.T) {
	src := `
func format_case(i) {
    return string.format("key%05d", i)
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "format_case")
	if proto == nil {
		t.Fatal("proto format_case not found")
	}
	fn := BuildGraph(proto)
	cands := BuildSpecializationCandidates(fn)
	if len(cands) != 1 {
		t.Fatalf("candidate count=%d, want 1", len(cands))
	}
	cand := cands[0]
	if cand.Kind != SpecStringFormatInt || cand.Pattern != "key%05d" {
		t.Fatalf("candidate=%+v", cand)
	}
	if len(cand.Guards) != 3 {
		t.Fatalf("guard count=%d, want 3", len(cand.Guards))
	}
	if g := cand.Guards[0]; g.Kind != SpecGuardCalleeNativeIdentity ||
		g.NativeKind != runtime.NativeKindStdStringFormat ||
		g.NativeData != uintptr(runtime.StdStringFormatIdentityPtr()) {
		t.Fatalf("callee guard=%+v", g)
	}
	if g := cand.Guards[1]; g.Kind != SpecGuardConstString || g.Arg != 1 || g.Const != "key%05d" {
		t.Fatalf("const guard=%+v", g)
	}
	if g := cand.Guards[2]; g.Kind != SpecGuardArgType || g.Arg != 2 || g.Type != TypeInt {
		t.Fatalf("type guard=%+v", g)
	}
}

func TestSpecGuard_StringFormatRejectsDynamicPattern(t *testing.T) {
	src := `
func format_case(pattern, i) {
    return string.format(pattern, i)
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "format_case")
	if proto == nil {
		t.Fatal("proto format_case not found")
	}
	if cands := BuildSpecializationCandidates(BuildGraph(proto)); len(cands) != 0 {
		t.Fatalf("dynamic pattern candidates=%+v", cands)
	}
}

func TestSpecGuard_StringFormatFeedbackStableDynamicPatternCandidate(t *testing.T) {
	src := `
func format_case(pattern, i) {
    return string.format(pattern, i)
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "format_case")
	if proto == nil {
		t.Fatal("proto format_case not found")
	}
	proto.EnsureFeedback()
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("Execute top: %v", err)
	}
	fn := v.GetGlobal("format_case")
	args := []runtime.Value{runtime.StringValue("dyn%04d"), runtime.IntValue(7)}
	for i := 0; i < 2; i++ {
		if _, err := v.CallValue(fn, args); err != nil {
			t.Fatalf("CallValue: %v", err)
		}
	}
	cands := BuildSpecializationCandidates(BuildGraph(proto))
	if len(cands) != 1 {
		t.Fatalf("candidate count=%d, want 1", len(cands))
	}
	if cands[0].StaticPattern {
		t.Fatal("feedback-derived dynamic pattern candidate should not be marked static-lowerable")
	}
	if cands[0].Pattern != "dyn%04d" {
		t.Fatalf("candidate pattern=%q", cands[0].Pattern)
	}
}

func TestSpecGuard_StringFormatFeedbackRejectsWrongNativeCallee(t *testing.T) {
	src := `
func format_case(pattern, i) {
    string.format = string.rep
    return string.format(pattern, i)
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "format_case")
	if proto == nil {
		t.Fatal("proto format_case not found")
	}
	proto.EnsureFeedback()
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("Execute top: %v", err)
	}
	fn := v.GetGlobal("format_case")
	if _, err := v.CallValue(fn, []runtime.Value{runtime.StringValue("%04d"), runtime.IntValue(7)}); err != nil {
		t.Fatalf("CallValue: %v", err)
	}
	if cands := BuildSpecializationCandidates(BuildGraph(proto)); len(cands) != 0 {
		t.Fatalf("wrong native callee should not produce string.format candidates: %+v", cands)
	}
}

func TestSpecGuard_FieldShapeLoadCandidate(t *testing.T) {
	src := `
func read_x(t) {
    return t.x
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "read_x")
	if proto == nil {
		t.Fatal("proto read_x not found")
	}
	getPC := -1
	for pc, inst := range proto.Code {
		if vm.DecodeOp(inst) == vm.OP_GETFIELD {
			getPC = pc
			break
		}
	}
	if getPC < 0 {
		t.Fatal("GETFIELD not found")
	}
	proto.FieldCache = make([]runtime.FieldCacheEntry, len(proto.Code))
	proto.FieldCache[getPC] = runtime.FieldCacheEntry{ShapeID: 123, FieldIdx: 2}

	cands := BuildSpecializationCandidates(BuildGraph(proto))
	var found SpecializationCandidate
	for _, cand := range cands {
		if cand.Kind == SpecFieldShapeLoad {
			found = cand
			break
		}
	}
	if found.Kind != SpecFieldShapeLoad {
		t.Fatalf("field shape candidate not found: %+v", cands)
	}
	if found.ShapeID != 123 || found.FieldIndex != 2 {
		t.Fatalf("field candidate shape=%d index=%d", found.ShapeID, found.FieldIndex)
	}
	if len(found.Guards) != 1 || found.Guards[0].Kind != SpecGuardTableShape {
		t.Fatalf("field candidate guards=%+v", found.Guards)
	}
}

func TestCallExitFastHelpersIgnoreNegativeSlots(t *testing.T) {
	regs := []runtime.Value{runtime.IntValue(1), runtime.IntValue(2)}
	args := collectCallExitArgs(regs, -4, 3)
	if len(args) != 3 {
		t.Fatalf("args len=%d, want 3", len(args))
	}
	for i, arg := range args {
		if !arg.IsNil() {
			t.Fatalf("arg[%d]=%s, want nil", i, arg.String())
		}
	}

	gf := &runtime.GoFunction{
		FastArg1: func(v runtime.Value) (runtime.Value, error) {
			t.Fatalf("FastArg1 should not be called for negative slot")
			return runtime.NilValue(), nil
		},
	}
	if _, ok, err := callGoFunctionFast(gf, regs, -2, 1); err != nil || ok {
		t.Fatalf("callGoFunctionFast ok=%v err=%v, want false nil", ok, err)
	}

	storeCallExitSingleResult(regs, -1, 2, runtime.IntValue(9))
	if !regs[0].IsNil() {
		t.Fatalf("regs[0]=%s, want nil second return write", regs[0].String())
	}
}

func TestSpecGuard_FieldShapeLoadCandidateFromRuntimeFeedback(t *testing.T) {
	src := `
func read_x(t) {
    return t.x
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "read_x")
	if proto == nil {
		t.Fatal("proto read_x not found")
	}
	proto.EnsureFeedback()
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("Execute top: %v", err)
	}
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(42))
	fn := v.GetGlobal("read_x")
	if _, err := v.CallValue(fn, []runtime.Value{runtime.TableValue(tbl)}); err != nil {
		t.Fatalf("CallValue: %v", err)
	}
	proto.FieldCache = nil

	cands := BuildSpecializationCandidates(BuildGraph(proto))
	var found SpecializationCandidate
	for _, cand := range cands {
		if cand.Kind == SpecFieldShapeLoad {
			found = cand
			break
		}
	}
	if found.Kind != SpecFieldShapeLoad {
		t.Fatalf("feedback-derived field shape candidate not found: %+v", cands)
	}
	if found.ShapeID == 0 || found.FieldIndex != 0 {
		t.Fatalf("field candidate shape=%d index=%d", found.ShapeID, found.FieldIndex)
	}
}

func TestSpecGuard_FieldShapeLoadRejectsPolymorphicRuntimeFeedback(t *testing.T) {
	src := `
func read_x(t) {
    return t.x
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "read_x")
	if proto == nil {
		t.Fatal("proto read_x not found")
	}
	proto.EnsureFeedback()
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("Execute top: %v", err)
	}
	fn := v.GetGlobal("read_x")
	t1 := runtime.NewTable()
	t1.RawSetString("x", runtime.IntValue(1))
	t2 := runtime.NewTable()
	t2.RawSetString("z", runtime.IntValue(0))
	t2.RawSetString("x", runtime.IntValue(2))
	if _, err := v.CallValue(fn, []runtime.Value{runtime.TableValue(t1)}); err != nil {
		t.Fatalf("CallValue t1: %v", err)
	}
	if _, err := v.CallValue(fn, []runtime.Value{runtime.TableValue(t2)}); err != nil {
		t.Fatalf("CallValue t2: %v", err)
	}
	proto.FieldCache = nil

	for _, cand := range BuildSpecializationCandidates(BuildGraph(proto)) {
		if cand.Kind == SpecFieldShapeLoad {
			t.Fatalf("polymorphic field feedback should not produce candidate: %+v", cand)
		}
	}
}
