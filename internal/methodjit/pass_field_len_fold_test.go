package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestFieldLenFold_FoldsJoinConstStringStores(t *testing.T) {
	proto := &vm.FuncProto{
		Constants: []runtime.Value{
			runtime.StringValue("busy"),
			runtime.StringValue("idle"),
		},
	}
	fn := &Function{Proto: proto}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}
	b2 := &Block{ID: 2}
	b3 := &Block{ID: 3}
	b0.Succs = []*Block{b1, b2}
	b1.Preds = []*Block{b0}
	b2.Preds = []*Block{b0}
	b1.Succs = []*Block{b3}
	b2.Succs = []*Block{b3}
	b3.Preds = []*Block{b1, b2}
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3}

	tbl := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b0}
	s0 := &Instr{ID: 2, Op: OpConstString, Type: TypeString, Aux: 0, Block: b1}
	set0 := &Instr{ID: 3, Op: OpSetField, Args: []*Value{tbl.Value(), s0.Value()}, Aux: 7, Block: b1}
	s1 := &Instr{ID: 4, Op: OpConstString, Type: TypeString, Aux: 1, Block: b2}
	set1 := &Instr{ID: 5, Op: OpSetField, Args: []*Value{tbl.Value(), s1.Value()}, Aux: 7, Block: b2}
	get := &Instr{ID: 6, Op: OpGetField, Type: TypeString, Args: []*Value{tbl.Value()}, Aux: 7, Block: b3}
	ln := &Instr{ID: 7, Op: OpLen, Type: TypeInt, Args: []*Value{get.Value()}, Block: b3}
	b0.Instrs = []*Instr{tbl, {Op: OpBranch, Block: b0}}
	b1.Instrs = []*Instr{s0, set0, {Op: OpJump, Block: b1}}
	b2.Instrs = []*Instr{s1, set1, {Op: OpJump, Block: b2}}
	b3.Instrs = []*Instr{get, ln, {Op: OpReturn, Args: []*Value{ln.Value()}, Block: b3}}

	out, err := FieldLenFoldPass(fn)
	if err != nil {
		t.Fatalf("FieldLenFoldPass: %v", err)
	}
	if out == nil || ln.Op != OpConstInt || ln.Aux != 4 || len(ln.Args) != 0 {
		t.Fatalf("len op not folded: %s aux=%d args=%d", ln.Op, ln.Aux, len(ln.Args))
	}
}

func TestFieldLenFold_LowersProfiledPolyFieldLen(t *testing.T) {
	proto := &vm.FuncProto{
		Constants: []runtime.Value{
			runtime.StringValue("kind"),
		},
	}
	fn := &Function{Proto: proto}
	b0 := &Block{ID: 0}
	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	tbl := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b0}
	get := &Instr{ID: 2, Op: OpGetField, Type: TypeString, Args: []*Value{tbl.Value()}, Aux: 0, Block: b0}
	ln := &Instr{ID: 3, Op: OpLen, Type: TypeInt, Args: []*Value{get.Value()}, Block: b0}
	b0.Instrs = []*Instr{tbl, get, ln, {Op: OpReturn, Args: []*Value{ln.Value()}, Block: b0}}
	fn.FieldPolyShapeFacts = map[int][]FieldPolyShapeCase{
		get.ID: {
			{ShapeID: 101, FieldIdx: 0, Type: TypeString, ReceiverFact: FixedShapeTableFact{
				ShapeID: 101, FieldLenRanges: map[string]intRange{"kind": pointRange(2)},
			}},
			{ShapeID: 102, FieldIdx: 1, Type: TypeString, ReceiverFact: FixedShapeTableFact{
				ShapeID: 102, FieldLenRanges: map[string]intRange{"kind": pointRange(5)},
			}},
		},
	}

	out, err := FieldLenFoldPass(fn)
	if err != nil {
		t.Fatalf("FieldLenFoldPass: %v", err)
	}
	if out == nil || ln.Op != OpFieldPolyLen || ln.Type != TypeInt || len(ln.Args) != 1 || ln.Args[0].ID != tbl.ID {
		t.Fatalf("len op not lowered to FieldPolyLen:\n%s", Print(fn))
	}
	cases := fn.FieldPolyShapeFacts[ln.ID]
	if len(cases) != 2 {
		t.Fatalf("FieldPolyLen facts not copied, got %d cases", len(cases))
	}
}

func TestFieldLenFold_FoldsProfiledExactLen(t *testing.T) {
	fn := &Function{
		Proto:             &vm.FuncProto{Name: "profiled_len"},
		NumRegs:           1,
		ProfiledLenRanges: map[int]intRange{2: pointRange(4)},
	}
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	s := &Instr{ID: 2, Op: OpGetField, Type: TypeString, Aux: 0, Block: b0}
	ln := &Instr{ID: 3, Op: OpLen, Type: TypeInt, Args: []*Value{s.Value()}, Block: b0}
	ret := &Instr{ID: 4, Op: OpReturn, Args: []*Value{ln.Value()}, Block: b0}
	b0.Instrs = []*Instr{s, ln, ret}
	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	out, err := FieldLenFoldPass(fn)
	if err != nil {
		t.Fatalf("FieldLenFoldPass: %v", err)
	}
	if ln.Op != OpConstInt || ln.Aux != 4 {
		t.Fatalf("profiled exact len not folded:\n%s", Print(out))
	}
}

func TestProfiledStringLenFold_FoldsAfterFieldLowering(t *testing.T) {
	fn := &Function{
		Proto:             &vm.FuncProto{Name: "profiled_len_after_lower"},
		NumRegs:           1,
		ProfiledLenRanges: map[int]intRange{2: pointRange(4)},
	}
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	svals := &Instr{ID: 1, Op: OpFieldSvals, Type: TypeInt, Block: b0}
	load := &Instr{ID: 2, Op: OpFieldLoad, Type: TypeString, Args: []*Value{svals.Value()}, Block: b0}
	ln := &Instr{ID: 3, Op: OpLen, Type: TypeInt, Args: []*Value{load.Value()}, Block: b0}
	ret := &Instr{ID: 4, Op: OpReturn, Args: []*Value{ln.Value()}, Block: b0}
	b0.Instrs = []*Instr{svals, load, ln, ret}
	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	out, err := ProfiledStringLenFoldPass(fn)
	if err != nil {
		t.Fatalf("ProfiledStringLenFoldPass: %v", err)
	}
	if ln.Op != OpConstInt || ln.Aux != 4 {
		t.Fatalf("profiled exact len not folded after field lowering:\n%s", Print(out))
	}
}

func TestProfiledStringLenFold_ReplacesLenOfPhi(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "profiled_len_phi"},
		NumRegs: 1,
		ProfiledLenRanges: map[int]intRange{
			10: pointRange(4),
			11: pointRange(5),
		},
	}
	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	left := &Block{ID: 1, defs: make(map[int]*Value)}
	right := &Block{ID: 2, defs: make(map[int]*Value)}
	join := &Block{ID: 3, defs: make(map[int]*Value)}
	entry.Succs = []*Block{left, right}
	left.Preds = []*Block{entry}
	right.Preds = []*Block{entry}
	left.Succs = []*Block{join}
	right.Succs = []*Block{join}
	join.Preds = []*Block{left, right}

	j0 := &Instr{ID: fn.newValueID(), Op: OpJump, Block: entry}
	s0 := &Instr{ID: 10, Op: OpLoadSlot, Type: TypeString, Aux: 0, Block: left}
	j1 := &Instr{ID: fn.newValueID(), Op: OpJump, Block: left}
	s1 := &Instr{ID: 11, Op: OpLoadSlot, Type: TypeString, Aux: 1, Block: right}
	j2 := &Instr{ID: fn.newValueID(), Op: OpJump, Block: right}
	phi := &Instr{ID: 12, Op: OpPhi, Type: TypeString, Args: []*Value{s0.Value(), s1.Value()}, Block: join}
	ln := &Instr{ID: 13, Op: OpLen, Type: TypeInt, Args: []*Value{phi.Value()}, Block: join}
	ret := &Instr{ID: 14, Op: OpReturn, Args: []*Value{ln.Value()}, Block: join}
	entry.Instrs = []*Instr{j0}
	left.Instrs = []*Instr{s0, j1}
	right.Instrs = []*Instr{s1, j2}
	join.Instrs = []*Instr{phi, ln, ret}
	fn.Entry = entry
	fn.Blocks = []*Block{entry, left, right, join}

	out, err := ProfiledStringLenFoldPass(fn)
	if err != nil {
		t.Fatalf("ProfiledStringLenFoldPass: %v", err)
	}
	if ln.Op != OpNop {
		t.Fatalf("len should be removed:\n%s", Print(out))
	}
	if len(ret.Args) != 1 || ret.Args[0].Def == nil || ret.Args[0].Def.Op != OpPhi || ret.Args[0].Def.Type != TypeInt {
		t.Fatalf("return should use int length phi:\n%s", Print(out))
	}
}

func TestFieldLenFold_StepIOPipeline(t *testing.T) {
	src := `func step_io(a, tick) {
    a.queue = (a.queue + tick + a.id) % 211
    a.bytes = a.bytes + a.queue * 13 + tick
    if a.queue % 2 == 0 {
        a.state = "busy"
    } else {
        a.state = "idle"
    }
    return a.bytes % 100000 + #a.state
}`
	top := compileTop(t, src)
	stepIO := findProtoByName(top, "step_io")
	if stepIO == nil {
		t.Fatal("step_io proto not found")
	}
	fn := BuildGraph(stepIO)
	out, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpLen {
				t.Fatalf("OpLen survived:\n%s", Print(out))
			}
		}
	}
}
