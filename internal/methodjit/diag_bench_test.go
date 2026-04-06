//go:build darwin && arm64

// diag_bench_test.go dumps full-pipeline Tier 2 IR + ARM64 code for nbody
// and sieve benchmarks. Uses the SAME pipeline as compileTier2() in production.

package methodjit

import (
	"fmt"
	"os"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

// fullPipelineTier2 runs the production compileTier2 pipeline on a function.
// This mirrors tiering_manager.go:compileTier2() exactly.
func fullPipelineTier2(t *testing.T, benchFile, fnName, outName string) {
	t.Helper()

	srcBytes, err := os.ReadFile("../../benchmarks/suite/" + benchFile)
	if err != nil {
		t.Fatalf("read %s: %v", benchFile, err)
	}

	top := compileTop(t, string(srcBytes))

	target := findProtoByName(top, fnName)
	if target == nil {
		// Dump proto tree for debugging
		var walk func(p *vm.FuncProto, depth int)
		walk = func(p *vm.FuncProto, depth int) {
			t.Logf("%*s- %q (numParams=%d)", depth*2, "", p.Name, p.NumParams)
			for _, sub := range p.Protos {
				walk(sub, depth+1)
			}
		}
		walk(top, 0)
		t.Fatalf("function %q not found in %s", fnName, benchFile)
	}

	t.Logf("=== %s / %s (numParams=%d, maxStack=%d) ===",
		benchFile, fnName, target.NumParams, target.MaxStack)

	// --- Full production pipeline (mirrors compileTier2 exactly) ---
	fn := BuildGraph(target)
	t.Logf("--- IR BEFORE passes ---\n%s", Print(fn))

	// Validate
	if errs := Validate(fn); len(errs) > 0 {
		t.Logf("Validation errors: %v", errs)
	}

	// TypeSpecialize (1st)
	fn, _ = TypeSpecializePass(fn)
	t.Logf("--- IR after TypeSpecialize(1) ---\n%s", Print(fn))

	// Intrinsic pass (math.sqrt -> OpSqrt etc)
	fn, intrinsicNotes := IntrinsicPass(fn)
	t.Logf("--- IR after IntrinsicPass (notes: %v) ---\n%s", intrinsicNotes, Print(fn))

	// TypeSpecialize (2nd, after intrinsic)
	fn, _ = TypeSpecializePass(fn)

	// Inline pass (no globals available in test, but run anyway)
	fn, _ = InlinePassWith(InlineConfig{MaxSize: 40, MaxRecursion: 2})(fn)

	// TypeSpecialize (3rd, after inline)
	fn, _ = TypeSpecializePass(fn)
	t.Logf("--- IR after Inline+TypeSpecialize(3) ---\n%s", Print(fn))

	// ConstProp
	fn, _ = ConstPropPass(fn)

	// LoadElimination
	fn, _ = LoadEliminationPass(fn)

	// DCE
	fn, _ = DCEPass(fn)
	t.Logf("--- IR after ConstProp+LoadElim+DCE ---\n%s", Print(fn))

	// RangeAnalysis
	fn, _ = RangeAnalysisPass(fn)

	// LICM
	fn, _ = LICMPass(fn)
	fn.CarryPreheaderInvariants = true
	t.Logf("--- IR FINAL (after all passes) ---\n%s", Print(fn))

	// Register allocation
	alloc := AllocateRegisters(fn)
	t.Logf("--- Register Allocation ---")
	for id, reg := range alloc.ValueRegs {
		regName := "X"
		if reg.IsFloat {
			regName = "D"
		}
		t.Logf("  v%d -> %s%d", id, regName, reg.Reg)
	}

	// Compile to ARM64
	cf, compileErr := Compile(fn, alloc)
	if compileErr != nil {
		t.Logf("Compile error (expected for some benchmarks): %v", compileErr)
		return
	}
	t.Cleanup(func() { cf.Code.Free() })

	t.Logf("code: size=%d bytes, DirectEntryOffset=%d, NumSpills=%d",
		cf.Code.Size(), cf.DirectEntryOffset, cf.NumSpills)

	// Write binary for disassembly
	size := cf.Code.Size()
	src2 := unsafe.Slice((*byte)(cf.Code.Ptr()), size)
	out := make([]byte, size)
	copy(out, src2)

	outPath := "/tmp/gscript_" + outName + "_full.bin"
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", outPath, err)
	}
	t.Logf("wrote %d bytes to %s", size, outPath)

	// Also count instruction types for quick summary
	totalInsns := size / 4
	t.Logf("Total ARM64 instructions: %d", totalInsns)
	_ = fmt.Sprintf("done") // avoid unused import
}

func TestFullPipeline_Nbody(t *testing.T) {
	fullPipelineTier2(t, "nbody.gs", "advance", "nbody_advance")
}

func TestFullPipeline_Sieve(t *testing.T) {
	fullPipelineTier2(t, "sieve.gs", "sieve", "sieve")
}
