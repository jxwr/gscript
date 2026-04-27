//go:build darwin && arm64

// tier1_fib_dump_test.go dumps the Tier 1 baseline ARM64 code for the
// fibonacci benchmark's recursive `fib` function so it can be disassembled
// externally with `xcrun otool -tv`. Mirrors tier1_ack_dump_test.go.

package methodjit

import (
	"os"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

// R29 baseline: fib is currently regressed; this fixture sentinels the self-call
// path insn count so R30+ can assert the guard removal actually trims instructions.
//
// Tier 1 CALL IC direct-entry versioning adds a Tier2Promoted guard to the
// non-self fallback path, taking fib from 635 to 639 instructions.
const fibTotalInsnBaseline = 639

func TestDumpTier1_FibBody(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/fib.gs")
	if err != nil {
		t.Fatalf("read fib.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))

	target := findProtoByName(top, "fib")
	if target == nil {
		t.Fatalf("function 'fib' not found in fib.gs")
	}
	t.Logf("=== fib (numParams=%d, maxStack=%d, bytecode len=%d) ===",
		target.NumParams, target.MaxStack, len(target.Code))

	for pc, inst := range target.Code {
		op := vm.DecodeOp(inst)
		t.Logf("  bc pc=%2d  op=%d A=%d B=%d C=%d Bx=%d sBx=%d",
			pc, int(op), vm.DecodeA(inst), vm.DecodeB(inst),
			vm.DecodeC(inst), vm.DecodeBx(inst), vm.DecodesBx(inst))
	}

	bf, err := CompileBaseline(target)
	if err != nil {
		t.Fatalf("CompileBaseline(fib) error: %v", err)
	}
	t.Cleanup(func() { bf.Code.Free() })

	size := bf.Code.Size()
	totalInsns := size / 4
	t.Logf("tier1 code: size=%d bytes (%d insns), DirectEntryOffset=%d",
		size, totalInsns, bf.DirectEntryOffset)

	if totalInsns > fibTotalInsnBaseline {
		t.Errorf("tier1 fib insn count REGRESSED: %d > baseline %d (+%d insns)",
			totalInsns, fibTotalInsnBaseline, totalInsns-fibTotalInsnBaseline)
	} else {
		t.Logf("insn count OK: %d <= baseline %d (delta=%d)",
			totalInsns, fibTotalInsnBaseline, totalInsns-fibTotalInsnBaseline)
	}

	t.Logf("Labels (bytecodePC -> codeOffset):")
	for pc := 0; pc < len(target.Code); pc++ {
		if off, ok := bf.Labels[pc]; ok {
			t.Logf("  pc=%2d -> off=0x%x (%d)", pc, off, off)
		}
	}

	src := unsafe.Slice((*byte)(bf.Code.Ptr()), size)
	out := make([]byte, size)
	copy(out, src)

	outPath := "/tmp/gscript_fib_tier1.bin"
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", outPath, err)
	}
	t.Logf("wrote %d bytes to %s", size, outPath)
}
