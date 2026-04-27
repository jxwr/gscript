package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestFieldNumToFloatFusion_FusesSingleUseGetFieldAtOriginalLoad(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_num_fuse")
	gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 7, Aux2: 123, Block: b}
	cf := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 1, Block: b}
	neg := &Instr{ID: fn.newValueID(), Op: OpNegFloat, Type: TypeFloat,
		Args: []*Value{cf.Value()}, Block: b}
	conv := &Instr{ID: fn.newValueID(), Op: OpNumToFloat, Type: TypeFloat,
		Args: []*Value{gf.Value()}, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat,
		Args: []*Value{conv.Value(), neg.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gf, cf, neg, conv, add, ret}

	result, err := FieldNumToFloatFusionPass(fn)
	if err != nil {
		t.Fatalf("FieldNumToFloatFusionPass: %v", err)
	}
	if gf.Op != OpGetFieldNumToFloat {
		t.Fatalf("expected original GetField to become fused op, got %s\nIR:\n%s", gf.Op, Print(result))
	}
	if conv.Op != OpNop || len(conv.Args) != 0 {
		t.Fatalf("expected converter to become Nop, got %s args=%d\nIR:\n%s", conv.Op, len(conv.Args), Print(result))
	}
	if add.Args[0].ID != gf.ID {
		t.Fatalf("expected AddFloat to use fused field value v%d, got v%d", gf.ID, add.Args[0].ID)
	}
	if gf.Args[0].ID != obj.ID || gf.Aux != 7 || gf.Aux2 != 123 || gf.Type != TypeFloat {
		t.Fatalf("fused op did not preserve field metadata: %s", Print(result))
	}

	result, err = DCEPass(result)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}
	for _, instr := range result.Entry.Instrs {
		if instr.ID == conv.ID {
			t.Fatalf("dead NumToFloat/Nop was not removed:\n%s", Print(result))
		}
	}
}

func TestFieldNumToFloatFusion_DoesNotFuseMultiUseGetField(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_num_multi_use")
	gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 7, Block: b}
	conv := &Instr{ID: fn.newValueID(), Op: OpNumToFloat, Type: TypeFloat,
		Args: []*Value{gf.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{conv.Value(), gf.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gf, conv, ret}

	result, err := FieldNumToFloatFusionPass(fn)
	if err != nil {
		t.Fatalf("FieldNumToFloatFusionPass: %v", err)
	}
	if gf.Op != OpGetField || conv.Op != OpNumToFloat {
		t.Fatalf("fusion should not rewrite multi-use GetField:\n%s", Print(result))
	}
}

func TestFieldNumToFloatFusion_DoesNotCrossMutationCallOrDeopt(t *testing.T) {
	cases := []struct {
		name    string
		barrier func(fn *Function, b *Block, obj *Instr) *Instr
	}{
		{
			name: "setfield",
			barrier: func(fn *Function, b *Block, obj *Instr) *Instr {
				val := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 1, Block: b}
				sf := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
					Args: []*Value{obj.Value(), val.Value()}, Aux: 7, Block: b}
				b.Instrs = append(b.Instrs, val)
				return sf
			},
		},
		{
			name: "call",
			barrier: func(fn *Function, b *Block, obj *Instr) *Instr {
				return &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny,
					Args: []*Value{obj.Value()}, Block: b}
			},
		},
		{
			name: "guard",
			barrier: func(fn *Function, b *Block, obj *Instr) *Instr {
				return &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeTable,
					Args: []*Value{obj.Value()}, Aux: int64(TypeTable), Block: b}
			},
		},
		{
			name: "gettable-exit",
			barrier: func(fn *Function, b *Block, obj *Instr) *Instr {
				key := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
				gt := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
					Args: []*Value{obj.Value(), key.Value()}, Block: b}
				b.Instrs = append(b.Instrs, key)
				return gt
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn, b, obj := newFieldNumFusionFn("field_num_no_cross_" + tc.name)
			gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
				Args: []*Value{obj.Value()}, Aux: 7, Block: b}
			conv := &Instr{ID: fn.newValueID(), Op: OpNumToFloat, Type: TypeFloat,
				Args: []*Value{gf.Value()}, Block: b}
			ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{conv.Value()}, Block: b}
			b.Instrs = []*Instr{obj, gf}
			barrier := tc.barrier(fn, b, obj)
			b.Instrs = append(b.Instrs, barrier, conv, ret)

			result, err := FieldNumToFloatFusionPass(fn)
			if err != nil {
				t.Fatalf("FieldNumToFloatFusionPass: %v", err)
			}
			if gf.Op != OpGetField || conv.Op != OpNumToFloat {
				t.Fatalf("fusion crossed %s barrier:\n%s", tc.name, Print(result))
			}
		})
	}
}

func newFieldNumFusionFn(name string) (*Function, *Block, *Instr) {
	fn := &Function{Proto: &vm.FuncProto{Name: name}, NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	return fn, b, obj
}
