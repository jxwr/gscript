//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"
)

func TestEmitMatrixLoadFAtUsesDirectFPLoad(t *testing.T) {
	proto := compileFunction(t, `
func read(m, i, j) {
    return matrix.getf(m, i, j)
}`)
	fn, _, err := RunTier2Pipeline(BuildGraph(proto), nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	if countOpHelper(fn, OpMatrixLoadFAt) != 1 {
		t.Fatalf("expected one MatrixLoadFAt after lowering:\n%s", Print(fn))
	}
	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	code := unsafeCodeSlice(cf)
	asm := disasmARM64(code)
	if !strings.Contains(asm, "FMOVD (R5)(R4<<3), F") {
		t.Fatalf("MatrixLoadFAt should emit a direct FP indexed load:\n%s", asm)
	}
	if strings.Contains(asm, "MOVD (R5)(R4<<3), R0") {
		t.Fatalf("MatrixLoadFAt should not load through a GPR before the FP result:\n%s", asm)
	}
}

func TestEmitMatrixStoreFAtUsesDirectFPStore(t *testing.T) {
	proto := compileFunction(t, `
func write(m, i) {
    matrix.setf(m, i, 0, 1.25)
}`)
	fn, _, err := RunTier2Pipeline(BuildGraph(proto), nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	var store *Instr
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpMatrixStoreFAt {
				store = instr
				break
			}
		}
	}
	if store == nil {
		t.Fatalf("expected MatrixStoreFAt after lowering:\n%s", Print(fn))
	}
	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	if moves := countFMOVToGPForIRInstr(cf, store.ID); moves != 0 {
		t.Fatalf("MatrixStoreFAt emitted %d FPR-to-GPR move(s), want direct FP store", moves)
	}
	if stores := countFSTRdRegForIRInstr(cf, store.ID); stores != 1 {
		t.Fatalf("MatrixStoreFAt emitted %d FSTRd register-offset store(s), want 1", stores)
	}
}

func TestEmitMatrixStoreFRowUsesDirectFPStore(t *testing.T) {
	proto := compileFunction(t, `
func write_row(m, i) {
    matrix.setf(m, i, 0, 1.25)
    matrix.setf(m, i, 1, 2.5)
}`)
	fn, _, err := RunTier2Pipeline(BuildGraph(proto), nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	var store *Instr
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpMatrixStoreFRow || instr.Op == OpMatrixStoreFRowConst {
				store = instr
				break
			}
		}
	}
	if store == nil {
		t.Fatalf("expected MatrixStoreFRow after row factoring:\n%s", Print(fn))
	}
	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	if moves := countFMOVToGPForIRInstr(cf, store.ID); moves != 0 {
		t.Fatalf("MatrixStoreFRow emitted %d FPR-to-GPR move(s), want direct FP store", moves)
	}
	if stores := countFSTRdForIRInstr(cf, store.ID); stores != 1 {
		t.Fatalf("MatrixStoreFRow emitted %d FSTRd store(s), want 1", stores)
	}
}

func countFSTRdRegForIRInstr(cf *CompiledFunction, instrID int) int {
	return countMatchingIRInstr(cf, instrID, func(insn uint32) bool {
		return insn&0xFFE00C00 == 0xFC200800
	})
}
