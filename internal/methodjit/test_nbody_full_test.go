//go:build darwin && arm64

package methodjit

import (
	"os"
	"testing"
	"unsafe"
)

func TestProfile_NbodyFull(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/nbody.gs")
	if err != nil {
		t.Fatalf("read nbody.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))
	target := findProtoByName(top, "advance")
	if target == nil {
		t.Fatal("advance not found")
	}

	t.Logf("=== nbody.gs / advance (numParams=%d, maxStack=%d) ===", target.NumParams, target.MaxStack)

	// Full production pipeline (same as tiering_manager.go)
	fn := BuildGraph(target)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = IntrinsicPass(fn) // converts math.sqrt -> OpSqrt
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = LoadEliminationPass(fn)
	fn, _ = DCEPass(fn)
	fn, _ = RangeAnalysisPass(fn)
	fn, _ = LICMPass(fn)
	fn.CarryPreheaderInvariants = true

	t.Logf("--- Tier 2 IR (FULL PIPELINE + IntrinsicPass) for nbody.gs/advance ---\n%s", Print(fn))

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	t.Cleanup(func() { cf.Code.Free() })

	t.Logf("code: size=%d bytes, DirectEntryOffset=%d, NumSpills=%d",
		cf.Code.Size(), cf.DirectEntryOffset, cf.NumSpills)

	size := cf.Code.Size()
	src := unsafe.Slice((*byte)(cf.Code.Ptr()), size)
	out := make([]byte, size)
	copy(out, src)

	if err := os.WriteFile("/tmp/gscript_nbody_full_t2.bin", out, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %d bytes to /tmp/gscript_nbody_full_t2.bin", size)
}
