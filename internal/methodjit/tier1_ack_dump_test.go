//go:build darwin && arm64

// tier1_ack_dump_test.go dumps the Tier 1 baseline ARM64 code for the
// ackermann benchmark's recursive `ack` function so it can be disassembled
// externally with `xcrun otool -tv`. Round 24 diagnostic artifact.

package methodjit

import (
	"os"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

func TestDumpTier1_AckermannBody(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/ackermann.gs")
	if err != nil {
		t.Fatalf("read ackermann.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))

	target := findProtoByName(top, "ack")
	if target == nil {
		t.Fatalf("function 'ack' not found in ackermann.gs")
	}
	t.Logf("=== ack (numParams=%d, maxStack=%d, bytecode len=%d) ===",
		target.NumParams, target.MaxStack, len(target.Code))

	// Log bytecode instructions so we can correlate code offsets with
	// source-level GETGLOBAL and CALL sites.
	for pc, inst := range target.Code {
		op := vm.DecodeOp(inst)
		t.Logf("  bc pc=%2d  op=%d A=%d B=%d C=%d Bx=%d sBx=%d",
			pc, int(op), vm.DecodeA(inst), vm.DecodeB(inst),
			vm.DecodeC(inst), vm.DecodeBx(inst), vm.DecodesBx(inst))
	}

	bf, err := CompileBaseline(target)
	if err != nil {
		t.Fatalf("CompileBaseline(ack) error: %v", err)
	}
	t.Cleanup(func() { bf.Code.Free() })

	size := bf.Code.Size()
	t.Logf("tier1 code: size=%d bytes (%d insns), DirectEntryOffset=%d",
		size, size/4, bf.DirectEntryOffset)

	// Log PC-to-offset labels so the disassembly can be correlated with
	// bytecode PCs.
	t.Logf("Labels (bytecodePC -> codeOffset):")
	for pc := 0; pc < len(target.Code); pc++ {
		if off, ok := bf.Labels[pc]; ok {
			t.Logf("  pc=%2d -> off=0x%x (%d)", pc, off, off)
		}
	}

	src := unsafe.Slice((*byte)(bf.Code.Ptr()), size)
	out := make([]byte, size)
	copy(out, src)

	outPath := "/tmp/gscript_ack_tier1.bin"
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", outPath, err)
	}
	t.Logf("wrote %d bytes to %s", size, outPath)
}
