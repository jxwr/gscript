//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestEmitFieldSvalsCache_ReusesSvalsForConsecutiveFields(t *testing.T) {
	fn := &Function{NumRegs: 1}
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	gf1 := &Instr{
		ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Block: b0,
		Args: []*Value{tbl.Value()}, Aux: 1, Aux2: packedFieldCache(7, 0),
	}
	gf2 := &Instr{
		ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Block: b0,
		Args: []*Value{tbl.Value()}, Aux: 2, Aux2: packedFieldCache(7, 1),
	}
	gf3 := &Instr{
		ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Block: b0,
		Args: []*Value{tbl.Value()}, Aux: 3, Aux2: packedFieldCache(7, 2),
	}
	ret := &Instr{
		ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b0,
		Args: []*Value{gf3.Value()},
	}
	b0.Instrs = []*Instr{tbl, gf1, gf2, gf3, ret}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	secondLoads := countLoadsForIRInstr(cf, gf2.ID)
	if secondLoads < 2 {
		t.Fatalf("test setup expected second GetField to load svals+field, got %d load(s)", secondLoads)
	}
	thirdLoads := countLoadsForIRInstr(cf, gf3.ID)
	if thirdLoads != 1 {
		t.Fatalf("third consecutive GetField emitted %d load(s), want only the field load", thirdLoads)
	}
}

func TestEmitFieldSvalsCache_SurvivesTypedFloatArithmetic(t *testing.T) {
	fn := &Function{NumRegs: 1}
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	gf1 := &Instr{
		ID: fn.newValueID(), Op: OpGetField, Type: TypeFloat, Block: b0,
		Args: []*Value{tbl.Value()}, Aux: 1, Aux2: packedFieldCache(7, 0),
	}
	gf2 := &Instr{
		ID: fn.newValueID(), Op: OpGetField, Type: TypeFloat, Block: b0,
		Args: []*Value{tbl.Value()}, Aux: 2, Aux2: packedFieldCache(7, 1),
	}
	sum := &Instr{
		ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b0,
		Args: []*Value{gf1.Value(), gf2.Value()},
	}
	gf3 := &Instr{
		ID: fn.newValueID(), Op: OpGetField, Type: TypeFloat, Block: b0,
		Args: []*Value{tbl.Value()}, Aux: 3, Aux2: packedFieldCache(7, 2),
	}
	ret := &Instr{
		ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b0,
		Args: []*Value{sum.Value(), gf3.Value()},
	}
	b0.Instrs = []*Instr{tbl, gf1, gf2, sum, gf3, ret}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	if loads := countLoadsForIRInstr(cf, gf3.ID); loads != 1 {
		t.Fatalf("GetField after typed FP arithmetic emitted %d load(s), want only the field load", loads)
	}
}

func TestEmitSetField_StoresFPRResidentFloatDirectly(t *testing.T) {
	fn := &Function{NumRegs: 1}
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	lhs := &Instr{
		ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0,
		Aux: int64(math.Float64bits(1.25)),
	}
	rhs := &Instr{
		ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0,
		Aux: int64(math.Float64bits(2.5)),
	}
	sum := &Instr{
		ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b0,
		Args: []*Value{lhs.Value(), rhs.Value()},
	}
	sf1 := &Instr{
		ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown, Block: b0,
		Args: []*Value{tbl.Value(), sum.Value()}, Aux: 1, Aux2: packedFieldCache(7, 0),
	}
	sf2 := &Instr{
		ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown, Block: b0,
		Args: []*Value{tbl.Value(), sum.Value()}, Aux: 2, Aux2: packedFieldCache(7, 1),
	}
	ret := &Instr{
		ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b0,
		Args: []*Value{tbl.Value()},
	}
	b0.Instrs = []*Instr{tbl, lhs, rhs, sum, sf1, sf2, ret}

	alloc := AllocateRegisters(fn)
	if pr, ok := alloc.ValueRegs[sum.ID]; !ok || !pr.IsFloat {
		t.Fatalf("test setup expected sum v%d in an FPR, got %+v ok=%v", sum.ID, pr, ok)
	}
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	if moves := countFMOVToGPForIRInstr(cf, sf2.ID); moves != 0 {
		t.Fatalf("second SetField emitted %d FPR-to-GPR move(s), want direct FPR store", moves)
	}
	if stores := countFSTRdForIRInstr(cf, sf2.ID); stores != 1 {
		t.Fatalf("second SetField emitted %d FSTRd store(s), want 1", stores)
	}
}

func packedFieldCache(shapeID uint32, fieldIdx int32) int64 {
	return int64(uint64(shapeID)<<32 | uint64(uint32(fieldIdx)))
}

func countLoadsForIRInstr(cf *CompiledFunction, instrID int) int {
	if cf == nil {
		return 0
	}
	var loads int
	code := unsafeCodeSlice(cf)
	for _, r := range cf.InstrCodeRanges {
		if r.InstrID != instrID || r.Pass != "normal" {
			continue
		}
		start, end := r.CodeStart, r.CodeEnd
		if start < 0 {
			start = 0
		}
		if end > len(code) {
			end = len(code)
		}
		for off := start; off+4 <= end; off += 4 {
			insn := binary.LittleEndian.Uint32(code[off : off+4])
			if arm64Class(insn) == "load" {
				loads++
			}
		}
	}
	return loads
}

func countFMOVToGPForIRInstr(cf *CompiledFunction, instrID int) int {
	return countMatchingIRInstr(cf, instrID, func(insn uint32) bool {
		return insn&0xFFFFFC00 == 0x9E660000
	})
}

func countFSTRdForIRInstr(cf *CompiledFunction, instrID int) int {
	return countMatchingIRInstr(cf, instrID, func(insn uint32) bool {
		return insn&0xFFC00000 == 0xFD000000
	})
}

func countMatchingIRInstr(cf *CompiledFunction, instrID int, match func(uint32) bool) int {
	if cf == nil {
		return 0
	}
	var count int
	code := unsafeCodeSlice(cf)
	for _, r := range cf.InstrCodeRanges {
		if r.InstrID != instrID || r.Pass != "normal" {
			continue
		}
		start, end := r.CodeStart, r.CodeEnd
		if start < 0 {
			start = 0
		}
		if end > len(code) {
			end = len(code)
		}
		for off := start; off+4 <= end; off += 4 {
			insn := binary.LittleEndian.Uint32(code[off : off+4])
			if match(insn) {
				count++
			}
		}
	}
	return count
}
