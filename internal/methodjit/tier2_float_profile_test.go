//go:build darwin && arm64

// tier2_float_profile_test.go dumps Tier 2 IR + ARM64 code bytes for the
// inner-loop functions of the five float-heavy benchmarks tracked in the
// Tier 2 Float Loops initiative (opt/initiatives/tier2-float-loops.md).
//
// Each TestProfile_* test:
//   1. reads ../../benchmarks/suite/<name>.gs,
//   2. walks proto.Protos to find the named inner-loop function,
//   3. compiles it through BuildGraph → TypeSpec → ConstProp → DCE → RegAlloc
//      → Compile,
//   4. logs the IR (Print(fn)) and writes the raw ARM64 byte buffer to
//      /tmp/gscript_<name>_t2.bin so it can be disassembled externally with
//      otool / llvm-objdump.
//
// The harness itself performs no optimization work; it produces artifacts
// for Phase 1 of the float-loops initiative.

package methodjit

import (
	"os"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

// findProtoByName walks a proto tree depth-first and returns the first
// proto whose Name matches. Returns nil if not found.
func findProtoByName(top *vm.FuncProto, name string) *vm.FuncProto {
	if top == nil {
		return nil
	}
	if top.Name == name {
		return top
	}
	for _, p := range top.Protos {
		if got := findProtoByName(p, name); got != nil {
			return got
		}
	}
	return nil
}

// profileTier2Func reads a benchmark source file, extracts the named
// function, runs it through the Tier 2 compile pipeline, logs IR, and
// writes the emitted ARM64 bytes to /tmp/gscript_<outName>_t2.bin.
func profileTier2Func(t *testing.T, benchFile, fnName, outName string) {
	t.Helper()

	// Read the benchmark source (path is relative to the package dir).
	srcBytes, err := os.ReadFile("../../benchmarks/suite/" + benchFile)
	if err != nil {
		t.Fatalf("read %s: %v", benchFile, err)
	}

	// Compile full source to bytecode.
	top := compileTop(t, string(srcBytes))

	// Locate the inner-loop function.
	target := findProtoByName(top, fnName)
	if target == nil {
		// Dump the proto tree to aid debugging.
		t.Logf("proto tree (looking for %q):", fnName)
		var walk func(p *vm.FuncProto, depth int)
		walk = func(p *vm.FuncProto, depth int) {
			t.Logf("%*s- %q (numParams=%d, maxStack=%d, %d sub-protos)",
				depth*2, "", p.Name, p.NumParams, p.MaxStack, len(p.Protos))
			for _, sub := range p.Protos {
				walk(sub, depth+1)
			}
		}
		walk(top, 0)
		t.Fatalf("function %q not found in %s", fnName, benchFile)
	}

	t.Logf("=== %s / %s (numParams=%d, maxStack=%d) ===",
		benchFile, fnName, target.NumParams, target.MaxStack)

	// Run Tier 2 pipeline (production order via RunTier2Pipeline).
	fn := BuildGraph(target)
	fn, _, pipeErr := RunTier2Pipeline(fn, nil)
	if pipeErr != nil {
		t.Fatalf("%s/%s: pipeline error: %v", benchFile, fnName, pipeErr)
	}
	fn.CarryPreheaderInvariants = true
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("%s/%s: Compile error: %v", benchFile, fnName, err)
	}
	t.Cleanup(func() { cf.Code.Free() })

	// Log IR.
	t.Logf("--- Tier 2 IR for %s/%s ---\n%s", benchFile, fnName, Print(fn))

	// Log key layout offsets that round-7 work will use to map pprof
	// samples back to IR blocks.
	t.Logf("code: size=%d bytes, DirectEntryOffset=%d, NumSpills=%d",
		cf.Code.Size(), cf.DirectEntryOffset, cf.NumSpills)

	// Copy the executable bytes into a fresh slice and write to /tmp.
	// cf.Code.Ptr() points into an mmap'd region; we must not retain
	// that pointer after cf.Code.Free() runs.
	size := cf.Code.Size()
	src := unsafe.Slice((*byte)(cf.Code.Ptr()), size)
	out := make([]byte, size)
	copy(out, src)

	outPath := "/tmp/gscript_" + outName + "_t2.bin"
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", outPath, err)
	}
	t.Logf("wrote %d bytes to %s", size, outPath)
}

// TestProfile_SpectralNorm dumps Tier 2 IR + ARM64 bytes for
// spectral_norm's inner-loop function multiplyAv (nested float loop with
// table indexing + A(i,j) call).
func TestProfile_SpectralNorm(t *testing.T) {
	profileTier2Func(t, "spectral_norm.gs", "multiplyAv", "spectral_norm")
}

// TestProfile_Nbody dumps Tier 2 IR + ARM64 bytes for nbody's advance
// function (double nested loop with table-field access + math.sqrt).
func TestProfile_Nbody(t *testing.T) {
	profileTier2Func(t, "nbody.gs", "advance", "nbody")
}

// TestProfile_Matmul dumps Tier 2 IR + ARM64 bytes for matmul's matmul
// function (triple-nested loop, 2D table-of-tables access, float accum).
func TestProfile_Matmul(t *testing.T) {
	profileTier2Func(t, "matmul.gs", "matmul", "matmul")
}

// TestProfile_Mandelbrot dumps Tier 2 IR + ARM64 bytes for the mandelbrot
// function (triple-nested loop, pure float arithmetic, early break).
func TestProfile_Mandelbrot(t *testing.T) {
	profileTier2Func(t, "mandelbrot.gs", "mandelbrot", "mandelbrot")
}

// TestProfile_MathIntensive dumps Tier 2 IR + ARM64 bytes for
// math_intensive's distance_sum function (float loop with math.sqrt call).
func TestProfile_MathIntensive(t *testing.T) {
	profileTier2Func(t, "math_intensive.gs", "distance_sum", "math_intensive")
}
