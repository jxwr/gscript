//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
	"strings"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestLoopExitStorePhis_DefersBoxedPhiWriteThrough(t *testing.T) {
	src := `
func fib_iter(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
result := fib_iter(70)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "fib_iter")
	if proto == nil {
		t.Fatal("function fib_iter not found")
	}
	proto.EnsureFeedback()

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("warm Execute: %v", err)
	}

	tm := NewTieringManager()
	art, err := tm.CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics: %v", err)
	}
	if !strings.Contains(art.IRAfter, "= Add") {
		t.Fatalf("test must exercise boxed loop-carried arithmetic, IR:\n%s", art.IRAfter)
	}

	addBlock := -1
	for _, entry := range art.SourceMap {
		if entry.IROp == "Add" && entry.CodeStart >= 0 {
			addBlock = entry.BlockID
			break
		}
	}
	if addBlock < 0 {
		t.Fatalf("no mapped Add block; source map entries=%d", len(art.SourceMap))
	}

	var jump *IRASMMapEntry
	for i := range art.SourceMap {
		entry := &art.SourceMap[i]
		if entry.BlockID == addBlock && entry.IROp == "Jump" && entry.CodeEnd > entry.CodeStart {
			jump = entry
			break
		}
	}
	if jump == nil {
		t.Fatalf("no mapped backedge Jump for Add block %d", addBlock)
	}
	if stores := countStoresInCodeRange(art.CompiledCode, jump.CodeStart, jump.CodeEnd); stores != 0 {
		t.Fatalf("boxed loop phi backedge Jump emitted %d store(s), want deferred exit-only stores\nIR:\n%s",
			stores, art.IRAfter)
	}
}

func TestLoopExitStorePhis_AllowsGuardTypeAndNumToFloatLoops(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   Op
		typ  Type
		aux  int64
	}{
		{name: "guard_type", op: OpGuardType, typ: TypeInt, aux: int64(TypeInt)},
		{name: "num_to_float", op: OpNumToFloat, typ: TypeFloat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn, backedgeJumpID := buildLoopExitStoreDirectDeoptIR(tc.op, tc.typ, tc.aux)
			alloc := AllocateRegisters(fn)
			cf, err := Compile(fn, alloc)
			if err != nil {
				t.Fatalf("Compile: %v\nIR:\n%s", err, Print(fn))
			}
			defer cf.Code.Free()

			code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())
			var jumpRange *InstrCodeRange
			for i := range cf.InstrCodeRanges {
				r := &cf.InstrCodeRanges[i]
				if r.InstrID == backedgeJumpID {
					jumpRange = r
					break
				}
			}
			if jumpRange == nil {
				t.Fatalf("missing backedge Jump code range; ranges=%v\nIR:\n%s", cf.InstrCodeRanges, Print(fn))
			}
			if stores := countStoresInCodeRange(code, jumpRange.CodeStart, jumpRange.CodeEnd); stores != 0 {
				t.Fatalf("%s backedge Jump emitted %d store(s), want exit-only phi stores with direct-deopt flush\nIR:\n%s",
					tc.name, stores, Print(fn))
			}
		})
	}
}

func buildLoopExitStoreDirectDeoptIR(directOp Op, directType Type, directAux int64) (*Function, int) {
	fn := &Function{
		Proto:     &vm.FuncProto{Name: "loop_direct_deopt", NumParams: 2, MaxStack: 2},
		NumRegs:   2,
		Int48Safe: make(map[int]bool),
	}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}
	b2 := &Block{ID: 2}
	b3 := &Block{ID: 3}
	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, b2}
	b1.Succs = []*Block{b2, b3}
	b2.Preds = []*Block{b1}
	b2.Succs = []*Block{b1}
	b3.Preds = []*Block{b1}

	cKeep := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: b0}
	cZero := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: b0}
	cOne := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b0}
	limit := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b0}
	entryJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0}
	b0.Instrs = []*Instr{cKeep, cZero, cOne, limit, entryJump}

	keep := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeBool, Block: b1}
	i := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	cmp := &Instr{ID: fn.newValueID(), Op: OpLtInt, Type: TypeBool,
		Args: []*Value{i.Value(), limit.Value()}, Block: b1}
	branch := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown,
		Args: []*Value{cmp.Value()}, Block: b1}

	probe := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 1, Block: b2}
	direct := &Instr{ID: fn.newValueID(), Op: directOp, Type: directType, Aux: directAux,
		Args: []*Value{probe.Value()}, Block: b2}
	inc := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{i.Value(), cOne.Value()}, Block: b2}
	backedgeJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2}
	b2.Instrs = []*Instr{probe, direct, inc, backedgeJump}

	keep.Args = []*Value{cKeep.Value(), keep.Value()}
	i.Args = []*Value{cZero.Value(), inc.Value()}
	b1.Instrs = []*Instr{keep, i, cmp, branch}
	b3.Instrs = []*Instr{{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown,
		Args: []*Value{keep.Value()}, Block: b3}}

	fn.Int48Safe[inc.ID] = true
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3}
	return fn, backedgeJump.ID
}

func countStoresInCodeRange(code []byte, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(code) {
		end = len(code)
	}
	stores := 0
	for off := start; off+4 <= end; off += 4 {
		insn := binary.LittleEndian.Uint32(code[off : off+4])
		if arm64Class(insn) == "store" {
			stores++
		}
	}
	return stores
}
