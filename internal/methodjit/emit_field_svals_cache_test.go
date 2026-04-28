//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
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
