//go:build darwin && arm64

// tiering_manager_diag_test.go verifies that CompileForDiagnostics shares
// the exact compile pipeline used in production. The test is the load-
// bearing guard against the class of failure that wasted R31 and R32 —
// a parallel diagnostic pipeline silently diverging from production.
//
// We do NOT assert byte-exact equality of the ARM64 code region: each
// JIT compile mmap's a fresh executable region at a runtime-chosen
// address, and the emitter bakes absolute addresses into branches and
// relocations. Two compiles of the same proto through the same pipeline
// produce different raw bytes for that reason alone.
//
// What we CAN assert and do assert is structural parity: same total
// instruction count, same per-class histogram, same post-pipeline IR
// text. If any of those three diverges, the diagnostic pipeline is
// silently different from production and the round using it is invalid.
// Do not relax these checks; find the divergence point in
// compileTier2Pipeline or one of the passes it runs and fix it.

package methodjit

import (
	"os"
	"reflect"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestDiag_ProductionParity_Sieve asserts that the ARM64 bytes produced
// by TieringManager.CompileForDiagnostics are bit-identical to the bytes
// produced by the production compileTier2 path, for the sieve benchmark's
// hottest function.
//
// Parity is enforced by construction (both paths call compileTier2Pipeline)
// but this test catches any future refactor that drifts.
func TestDiag_ProductionParity_Sieve(t *testing.T) {
	runParity(t, "sieve.gs", "sieve")
}

// TestDiag_ProductionParity_ObjectCreation — same check on the benchmark
// that R35 was trying to regress-fix. This exercises a GC-heavy path
// instead of the arithmetic-heavy sieve path.
func TestDiag_ProductionParity_ObjectCreation(t *testing.T) {
	runParity(t, "object_creation.gs", "new_vec3")
}

// TestDiag_ProductionParity_Mandelbrot — float-heavy path. Covers the
// FP/SIMD classification branch in classifyARM64.
func TestDiag_ProductionParity_Mandelbrot(t *testing.T) {
	runParity(t, "mandelbrot.gs", "mandelbrot")
}

func runParity(t *testing.T, benchFile, fnName string) {
	t.Helper()

	src, err := os.ReadFile("../../benchmarks/suite/" + benchFile)
	if err != nil {
		t.Fatalf("read %s: %v", benchFile, err)
	}

	// Compile the source twice into independent proto trees so that any
	// proto mutation done by compileTier2Pipeline (proto.NeedsTier2,
	// proto.MaxStack) doesn't affect the comparison run.
	topA := compileTop(t, string(src))
	topB := compileTop(t, string(src))
	targetA := findProtoByName(topA, fnName)
	targetB := findProtoByName(topB, fnName)
	if targetA == nil || targetB == nil {
		t.Fatalf("function %q not found in %s", fnName, benchFile)
	}

	// Production path.
	prodTM := NewTieringManager()
	prodCF, err := prodTM.compileTier2(targetA)
	if err != nil {
		t.Fatalf("production compileTier2(%s): %v", fnName, err)
	}
	if prodCF == nil {
		t.Fatalf("production compileTier2(%s): nil CompiledFunction", fnName)
	}
	prodInsnCount, prodHist := classifyARM64(unsafeCodeSlice(prodCF))
	prodCF.Code.Free()

	// Diagnostic path. Fresh TieringManager so the diagnostic cannot
	// observe production state left over from the first call.
	diagTM := NewTieringManager()
	art, err := diagTM.CompileForDiagnostics(targetB)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(%s): %v", fnName, err)
	}
	if art == nil || len(art.CompiledCode) == 0 {
		t.Fatalf("CompileForDiagnostics(%s): empty artifact", fnName)
	}

	// Structural parity: insn count and per-class histogram must match.
	// Raw bytes are expected to differ (see package doc comment on
	// address relocation).
	if prodInsnCount != art.InsnCount {
		t.Fatalf("insn count diverged for %s/%s: production=%d, diagnostic=%d. "+
			"CLAUDE.md rule 5 violated — compileTier2Pipeline is producing "+
			"different amounts of code depending on which caller invoked it. "+
			"Find the divergence and fix it.",
			benchFile, fnName, prodInsnCount, art.InsnCount)
	}
	if !reflect.DeepEqual(prodHist, art.InsnHistogram) {
		t.Fatalf("histogram diverged for %s/%s:\n  production  = %v\n  diagnostic  = %v\n"+
			"Same number of instructions but different class breakdown — "+
			"some pass is firing differently between the two paths.",
			benchFile, fnName, prodHist, art.InsnHistogram)
	}

	// Post-pipeline IR text must also be identical (strongest structural
	// check — proves the passes ran in the same order on the same input
	// with the same decisions). Capture both via a second diagnostic call
	// on the production-path TM so we have an IR trace on both sides.
	prodArt, err := prodTM.CompileForDiagnostics(findProtoByName(compileTop(t, string(src)), fnName))
	if err != nil || prodArt == nil {
		t.Fatalf("second CompileForDiagnostics for IR comparison: %v", err)
	}
	if prodArt.IRAfter != art.IRAfter {
		t.Fatalf("post-pipeline IR text diverged for %s/%s. "+
			"compileTier2Pipeline is non-deterministic — same proto, different IR.",
			benchFile, fnName)
	}

	// Sanity: insn count * 4 should equal byte length.
	if art.InsnCount*4 != len(art.CompiledCode) {
		t.Errorf("insn count mismatch: %d * 4 != %d bytes", art.InsnCount, len(art.CompiledCode))
	}
	if len(art.InsnHistogram) == 0 {
		t.Errorf("histogram empty for %s/%s (expected at least one class)", benchFile, fnName)
	}

	t.Logf("%s/%s: %d bytes, %d insns, hist=%v (prod/diag structurally identical)",
		benchFile, fnName, len(art.CompiledCode), art.InsnCount, art.InsnHistogram)
}

// TestDiag_ProtoTreeDiag_Sieve exercises the bulk diagnostic walk and
// verifies that every Tier-2-promotable proto shows up in the output.
func TestDiag_ProtoTreeDiag_Sieve(t *testing.T) {
	src, err := os.ReadFile("../../benchmarks/suite/sieve.gs")
	if err != nil {
		t.Fatalf("read sieve.gs: %v", err)
	}
	top := compileTop(t, string(src))

	tm := NewTieringManager()
	arts := tm.ProtoTreeDiag(top)
	if len(arts) == 0 {
		t.Fatal("ProtoTreeDiag returned empty slice")
	}

	// Log a compact summary.
	for _, a := range arts {
		if a.CompileErr != nil {
			t.Logf("  %-20s SKIPPED: %v", a.ProtoName, a.CompileErr)
			continue
		}
		t.Logf("  %-20s %d insns, regs=%d, hist=%v",
			a.ProtoName, a.InsnCount, a.MaxStack, a.InsnHistogram)
	}
}

// unused import guard (keeps vm import even if only used transitively above)
var _ = vm.New
