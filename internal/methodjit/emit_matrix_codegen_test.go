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
