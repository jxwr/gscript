//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestFieldShapeCallSplit_SingleBlockCaseCompilesNative(t *testing.T) {
	top := compileTop(t, `func step_a(actor, tick) {
    return actor.count + tick + 1
}
func step_b(actor, tick) {
    return actor.count + tick + 2
}`)
	stepA := findProtoByName(top, "step_a")
	stepB := findProtoByName(top, "step_b")
	if stepA == nil || stepB == nil {
		t.Fatalf("missing protos: step_a=%v step_b=%v", stepA != nil, stepB != nil)
	}
	clA := vm.NewClosure(stepA)

	actor := runtime.NewTable()
	actor.RawSetString("count", runtime.IntValue(3))
	actor.RawSetString("step", runtime.VMClosureFunctionValue(unsafe.Pointer(clA), clA))
	shapeID := actor.ShapeID()

	fn := &Function{
		Proto:   &vm.FuncProto{Name: "caller", NumParams: 2, MaxStack: 6},
		NumRegs: 2,
		nextID:  4,
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{
			2: {
				{
					ShapeID:   shapeID,
					FieldIdx:  0,
					VMProto:   stepA,
					VMClosure: uintptr(unsafe.Pointer(clA)),
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    shapeID,
						FieldNames: []string{"count", "step"},
						FieldTypes: map[string]Type{"count": TypeInt, "step": TypeFunction},
					},
				},
				{
					ShapeID:  shapeID + 1,
					FieldIdx: 0,
					VMProto:  stepB,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    shapeID + 1,
						FieldNames: []string{"count", "step"},
						FieldTypes: map[string]Type{"count": TypeInt, "step": TypeFunction},
					},
				},
			},
		},
	}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}
	recv := &Instr{ID: 0, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	tick := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	call := &Instr{ID: 2, Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), tick.Value()}, Aux: 2, Aux2: 2, Block: entry}
	ret := &Instr{ID: 3, Op: OpReturn, Args: []*Value{call.Value()}, Block: entry}
	entry.Instrs = []*Instr{recv, tick, call, ret}

	out, err := FieldShapeCallSplitPass(fn)
	if err != nil {
		t.Fatalf("FieldShapeCallSplitPass: %v", err)
	}
	if out != fn {
		t.Fatal("pass replaced function unexpectedly")
	}
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("split IR failed validation:\n%s\nerrs=%v", Print(fn), errs)
	}
	text := Print(fn)
	for _, want := range []string{"TableShapeID", "FieldCallFloor", "Phi"} {
		if !strings.Contains(text, want) {
			t.Fatalf("split IR missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "GuardCalleeProto") {
		t.Fatalf("split IR still contains inline-only guard:\n%s", text)
	}
	if got := len(fn.FieldPolyShapeFacts[call.ID]); got != 1 {
		t.Fatalf("fallback cases=%d want 1", got)
	}

	out, _, err = RunTier2Pipeline(out, &Tier2PipelineOpts{InlineMaxSize: 100})
	if err != nil {
		t.Fatalf("RunTier2Pipeline:\n%s\nerr=%v", Print(out), err)
	}
	alloc := AllocateRegisters(out)
	cf, err := Compile(out, alloc)
	if err != nil {
		t.Fatalf("Compile:\n%s\nerr=%v", Print(out), err)
	}
	defer cf.Code.Free()
}

func TestFieldShapeCallSplit_SingleCaseIsVisibleToEmitter(t *testing.T) {
	top := compileTop(t, `func step_a(actor, tick) {
    return actor.count + tick + 1
}
func step_b(actor, tick) {
    return actor.count + tick + 2
}`)
	stepA := findProtoByName(top, "step_a")
	stepB := findProtoByName(top, "step_b")
	if stepA == nil || stepB == nil {
		t.Fatalf("missing protos: step_a=%v step_b=%v", stepA != nil, stepB != nil)
	}
	clA := vm.NewClosure(stepA)
	actor := runtime.NewTable()
	actor.RawSetString("count", runtime.IntValue(3))
	actor.RawSetString("step", runtime.VMClosureFunctionValue(unsafe.Pointer(clA), clA))
	shapeID := actor.ShapeID()

	fn := &Function{
		Proto:   &vm.FuncProto{Name: "caller", NumParams: 2, MaxStack: 6},
		NumRegs: 2,
		nextID:  4,
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{
			2: {
				{
					ShapeID:   shapeID,
					FieldIdx:  0,
					VMProto:   stepA,
					VMClosure: uintptr(unsafe.Pointer(clA)),
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    shapeID,
						FieldNames: []string{"count", "step"},
						FieldTypes: map[string]Type{"count": TypeInt, "step": TypeFunction},
					},
				},
				{
					ShapeID:  shapeID + 1,
					FieldIdx: 0,
					VMProto:  stepB,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    shapeID + 1,
						FieldNames: []string{"count", "step"},
						FieldTypes: map[string]Type{"count": TypeInt, "step": TypeFunction},
					},
				},
			},
		},
	}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}
	recv := &Instr{ID: 0, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	tick := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	call := &Instr{ID: 2, Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), tick.Value()}, Aux: 2, Aux2: 2, Block: entry}
	ret := &Instr{ID: 3, Op: OpReturn, Args: []*Value{call.Value()}, Block: entry}
	entry.Instrs = []*Instr{recv, tick, call, ret}

	out, err := FieldShapeCallSplitPass(fn)
	if err != nil {
		t.Fatalf("FieldShapeCallSplitPass: %v", err)
	}
	var caseCall *Instr
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr != nil && instr.Op == OpFieldCallFloor && len(out.FieldPolyShapeFacts[instr.ID]) == 1 {
				caseCall = instr
				break
			}
		}
	}
	if caseCall == nil {
		t.Fatalf("missing monomorphic case call\nIR:\n%s", Print(out))
	}
	ec := &emitContext{fn: out}
	cases := ec.fieldShapeTypedPeerMethodCallCases(caseCall)
	if len(cases) != 1 {
		t.Fatalf("single-case field call cases=%d want 1\nIR:\n%s\nfacts=%#v", len(cases), Print(out), out.FieldPolyShapeFacts[caseCall.ID])
	}
}

func TestFieldShapeCallSplit_RejectsExitResumeCallee(t *testing.T) {
	top := compileTop(t, `func step_cache(actor, tick) {
    coroutine.yield(tick)
    return actor.hits
}
func step_b(actor, tick) {
    actor.hits = actor.hits + tick + 2
    return actor.hits
}`)
	stepCache := findProtoByName(top, "step_cache")
	stepB := findProtoByName(top, "step_b")
	if stepCache == nil || stepB == nil {
		t.Fatalf("missing protos: step_cache=%v step_b=%v", stepCache != nil, stepB != nil)
	}

	fn := &Function{
		Proto:   &vm.FuncProto{Name: "caller", NumParams: 2, MaxStack: 6},
		NumRegs: 2,
		nextID:  4,
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{
			2: {
				{
					ShapeID:  101,
					FieldIdx: 1,
					VMProto:  stepCache,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    101,
						FieldNames: []string{"hits", "step"},
						FieldTypes: map[string]Type{"hits": TypeInt, "step": TypeFunction},
					},
				},
				{
					ShapeID:  202,
					FieldIdx: 0,
					VMProto:  stepB,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    202,
						FieldNames: []string{"step"},
						FieldTypes: map[string]Type{"step": TypeFunction},
					},
				},
			},
		},
	}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}
	recv := &Instr{ID: 0, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	tick := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	call := &Instr{ID: 2, Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), tick.Value()}, Aux: 2, Aux2: 2, Block: entry}
	ret := &Instr{ID: 3, Op: OpReturn, Args: []*Value{call.Value()}, Block: entry}
	entry.Instrs = []*Instr{recv, tick, call, ret}

	out, err := FieldShapeCallSplitPass(fn)
	if err != nil {
		t.Fatalf("FieldShapeCallSplitPass: %v", err)
	}
	if out != fn {
		t.Fatal("pass replaced function unexpectedly")
	}
	text := Print(fn)
	if strings.Contains(text, "TableShapeID") || len(fn.Blocks) != 1 {
		t.Fatalf("unsafe callee was split unexpectedly:\n%s", text)
	}
	if got := len(fn.FieldPolyShapeFacts[call.ID]); got != 2 {
		t.Fatalf("fallback cases=%d want unchanged 2", got)
	}
}

func TestFieldShapeMethodFieldStableInCallee(t *testing.T) {
	top := compileTop(t, `func step_keep(actor, tick) {
    actor.count = actor.count + tick
    return actor.count
}
func step_replace(actor, tick) {
    actor.step = step_keep
    return tick
}`)
	stepKeep := findProtoByName(top, "step_keep")
	stepReplace := findProtoByName(top, "step_replace")
	if stepKeep == nil || stepReplace == nil {
		t.Fatalf("missing protos: keep=%v replace=%v", stepKeep != nil, stepReplace != nil)
	}
	fact := FixedShapeTableFact{
		ShapeID:    101,
		FieldNames: []string{"count", "step"},
		FieldTypes: map[string]Type{"count": TypeInt, "step": TypeFunction},
	}
	if !fieldShapeMethodFieldStableInCallee(FieldPolyShapeCase{
		ShapeID:      101,
		FieldIdx:     1,
		VMProto:      stepKeep,
		ReceiverFact: fact,
	}) {
		t.Fatal("callee that does not write dispatch field should allow stable method guard")
	}
	if fieldShapeMethodFieldStableInCallee(FieldPolyShapeCase{
		ShapeID:      101,
		FieldIdx:     1,
		VMProto:      stepReplace,
		ReceiverFact: fact,
	}) {
		t.Fatal("callee that writes dispatch field must keep epoch guard")
	}
}

func TestFieldShapeCallSplitPreInline_MakesMonomorphicCallArm(t *testing.T) {
	top := compileTop(t, `func step_a(actor, tick) { return tick + 1 }
func step_b(actor, tick) { return tick + 2 }`)
	stepA := findProtoByName(top, "step_a")
	stepB := findProtoByName(top, "step_b")
	if stepA == nil || stepB == nil {
		t.Fatalf("missing protos: step_a=%v step_b=%v", stepA != nil, stepB != nil)
	}

	fn := &Function{
		Proto:   &vm.FuncProto{Name: "caller", NumParams: 2, MaxStack: 6},
		NumRegs: 2,
		nextID:  5,
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{
			2: {
				{
					ShapeID:  101,
					FieldIdx: 1,
					VMProto:  stepA,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    101,
						FieldNames: []string{"id", "step"},
						FieldTypes: map[string]Type{"id": TypeInt, "step": TypeFunction},
					},
				},
				{
					ShapeID:  202,
					FieldIdx: 1,
					VMProto:  stepB,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    202,
						FieldNames: []string{"id", "step"},
						FieldTypes: map[string]Type{"id": TypeInt, "step": TypeFunction},
					},
				},
			},
		},
	}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}
	recv := &Instr{ID: 0, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	tick := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	method := &Instr{ID: 2, Op: OpGetField, Type: TypeFunction, Args: []*Value{recv.Value()}, Aux: 0, Block: entry}
	call := &Instr{ID: 3, Op: OpCall, Type: TypeInt, Args: []*Value{method.Value(), recv.Value(), tick.Value()}, Aux: 2, Aux2: 1, Block: entry}
	ret := &Instr{ID: 4, Op: OpReturn, Args: []*Value{call.Value()}, Block: entry}
	entry.Instrs = []*Instr{recv, tick, method, call, ret}

	out, err := FieldShapeCallSplitPreInlinePass(fn)
	if err != nil {
		t.Fatalf("FieldShapeCallSplitPreInlinePass: %v", err)
	}
	if out != fn {
		t.Fatal("pass replaced function unexpectedly")
	}
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("split IR failed validation:\n%s\nerrs=%v", Print(fn), errs)
	}
	text := Print(fn)
	for _, want := range []string{"TableShapeID", "Phi", "Call"} {
		if !strings.Contains(text, want) {
			t.Fatalf("split IR missing %q:\n%s", want, text)
		}
	}
	sawMono := false
	for id, cases := range fn.FieldPolyShapeFacts {
		if id != method.ID && len(cases) == 1 && cases[0].VMProto == stepA {
			sawMono = true
		}
	}
	if !sawMono {
		t.Fatalf("missing monomorphic case call fact: %#v", fn.FieldPolyShapeFacts)
	}
	if got := len(fn.FieldPolyShapeFacts[method.ID]); got != 1 {
		t.Fatalf("fallback cases=%d want 1", got)
	}
}

func TestFieldShapeCallSplitPreInline_AllowsExistingInlinePass(t *testing.T) {
	top := compileTop(t, `func step_a(actor, tick) {
    if tick > 3 {
        return tick + 10
    }
    return tick + 1
}
func step_b(actor, tick) { return tick + 2 }`)
	stepA := findProtoByName(top, "step_a")
	stepB := findProtoByName(top, "step_b")
	if stepA == nil || stepB == nil {
		t.Fatalf("missing protos: step_a=%v step_b=%v", stepA != nil, stepB != nil)
	}

	fn := &Function{
		Proto: &vm.FuncProto{
			Name:      "caller",
			NumParams: 2,
			MaxStack:  6,
			Constants: []runtime.Value{
				runtime.StringValue("step"),
			},
		},
		NumRegs: 2,
		nextID:  5,
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{
			2: {
				{
					ShapeID:  101,
					FieldIdx: 1,
					VMProto:  stepA,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    101,
						FieldNames: []string{"id", "step"},
						FieldTypes: map[string]Type{"id": TypeInt, "step": TypeFunction},
					},
				},
				{
					ShapeID:  202,
					FieldIdx: 1,
					VMProto:  stepB,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    202,
						FieldNames: []string{"id", "step"},
						FieldTypes: map[string]Type{"id": TypeInt, "step": TypeFunction},
					},
				},
			},
		},
	}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}
	recv := &Instr{ID: 0, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	tick := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	method := &Instr{ID: 2, Op: OpGetField, Type: TypeFunction, Args: []*Value{recv.Value()}, Aux: 0, Block: entry}
	call := &Instr{ID: 3, Op: OpCall, Type: TypeInt, Args: []*Value{method.Value(), recv.Value(), tick.Value()}, Aux: 2, Aux2: 1, Block: entry}
	ret := &Instr{ID: 4, Op: OpReturn, Args: []*Value{call.Value()}, Block: entry}
	entry.Instrs = []*Instr{recv, tick, method, call, ret}

	out, err := FieldShapeCallSplitPreInlinePass(fn)
	if err != nil {
		t.Fatalf("FieldShapeCallSplitPreInlinePass: %v", err)
	}
	out, err = InlinePassWith(InlineConfig{MaxSize: 100, MaxCumulativeSize: 200})(out)
	if err != nil {
		t.Fatalf("InlinePassWith: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("split+inline IR failed validation:\n%s\nerrs=%v", Print(out), errs)
	}
	text := Print(out)
	if strings.Contains(text, "Call            v") {
		t.Fatalf("expected split hot arm to inline at least one call:\n%s", text)
	}
}

func TestFieldShapeCallSplitPreInline_HotArmExecutesAfterPipeline(t *testing.T) {
	top := compileTop(t, `func step_a(actor, tick) {
    if tick > 3 {
        return tick + 10
    }
    return tick + 1
}
func step_b(actor, tick) { return tick + 2 }`)
	stepA := findProtoByName(top, "step_a")
	stepB := findProtoByName(top, "step_b")
	if stepA == nil || stepB == nil {
		t.Fatalf("missing protos: step_a=%v step_b=%v", stepA != nil, stepB != nil)
	}
	clA := vm.NewClosure(stepA)
	actor := runtime.NewTable()
	actor.RawSetString("id", runtime.IntValue(1))
	actor.RawSetString("step", runtime.VMClosureFunctionValue(unsafe.Pointer(clA), clA))
	shapeID := actor.ShapeID()

	fn := &Function{
		Proto: &vm.FuncProto{
			Name:      "caller",
			NumParams: 2,
			MaxStack:  6,
			Constants: []runtime.Value{
				runtime.StringValue("step"),
			},
		},
		NumRegs: 2,
		nextID:  5,
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{
			2: {
				{
					ShapeID:  shapeID,
					FieldIdx: 1,
					VMProto:  stepA,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    shapeID,
						FieldNames: []string{"id", "step"},
						FieldTypes: map[string]Type{"id": TypeInt, "step": TypeFunction},
					},
				},
				{
					ShapeID:  shapeID + 1,
					FieldIdx: 1,
					VMProto:  stepB,
					ReceiverFact: FixedShapeTableFact{
						ShapeID:    shapeID + 1,
						FieldNames: []string{"id", "step"},
						FieldTypes: map[string]Type{"id": TypeInt, "step": TypeFunction},
					},
				},
			},
		},
	}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}
	recv := &Instr{ID: 0, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	tick := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	method := &Instr{ID: 2, Op: OpGetField, Type: TypeFunction, Args: []*Value{recv.Value()}, Aux: 0, Block: entry}
	call := &Instr{ID: 3, Op: OpCall, Type: TypeInt, Args: []*Value{method.Value(), recv.Value(), tick.Value()}, Aux: 2, Aux2: 1, Block: entry}
	ret := &Instr{ID: 4, Op: OpReturn, Args: []*Value{call.Value()}, Block: entry}
	entry.Instrs = []*Instr{recv, tick, method, call, ret}

	out, err := FieldShapeCallSplitPreInlinePass(fn)
	if err != nil {
		t.Fatalf("FieldShapeCallSplitPreInlinePass: %v", err)
	}
	out, _, err = RunTier2Pipeline(out, &Tier2PipelineOpts{InlineMaxSize: 100})
	if err != nil {
		t.Fatalf("RunTier2Pipeline:\n%s\nerr=%v", Print(out), err)
	}
	alloc := AllocateRegisters(out)
	cf, err := Compile(out, alloc)
	if err != nil {
		t.Fatalf("Compile:\n%s\nerr=%v", Print(out), err)
	}
	defer cf.Code.Free()
	result, err := cf.Execute([]runtime.Value{runtime.TableValue(actor), runtime.IntValue(5)})
	if err != nil {
		t.Fatalf("Execute: %v\nIR:\n%s", err, Print(out))
	}
	if len(result) != 1 || !result[0].IsInt() || result[0].Int() != 15 {
		t.Fatalf("result=%v want 15\nIR:\n%s", result, Print(out))
	}
}
