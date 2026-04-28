//go:build darwin && arm64

// tier1_call_regression_test.go contains regression tests for Tier 1 call path
// optimizations. Each test locks a specific invariant established by a
// named optimization round.

package methodjit

import (
	"os"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

// TestSelfCall_ConstantsStrMoved verifies that the R28 Task 1 optimization
// (ctx.Constants STR moved from shared restore_done join into normal-call-only
// restore block) has not regressed.
//
// The optimization moves:
//
//	STR X27, [X19, #execCtxOffConstants]
//
// from the shared restore_done join (executed on both normal AND self-call
// returns) into the normal-call restore block only (executed only when
// X20==0 at return time). The self-call restore block skips the STR
// entirely, saving one dependent memory store per self-recursive call.
//
// Net static instruction count: unchanged (one STR moved, not removed).
// Dynamic savings: −1 STR per self-call invocation.
//
// Structural invariant: for each CALL site in the compiled binary, the
// STR X27, [X19, #8] that write-backs ctx.Constants after a normal-call
// restore must be immediately followed by a forward B instruction (B
// restoreDoneLabel). Before the optimization it was in the join and would
// be followed by STR X26, [X19, #0] (the ctx.Regs write-back), not a B.
//
// Regression signal: if the count of (STR X27 → B<forward>) pairs drops
// below 3 (one per CALL site in ackermann), the optimization was reverted
// or the STR was merged back into the shared join.
func TestSelfCall_ConstantsStrMoved(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/ackermann.gs")
	if err != nil {
		t.Fatalf("read ackermann.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))
	target := findProtoByName(top, "ack")
	if target == nil {
		t.Fatalf("function 'ack' not found in ackermann.gs")
	}

	bf, err := CompileBaseline(target)
	if err != nil {
		t.Fatalf("CompileBaseline(ack): %v", err)
	}
	t.Cleanup(func() { bf.Code.Free() })

	size := bf.Code.Size()
	// View code as []uint32 (ARM64 instructions are 4 bytes, little-endian).
	code := unsafe.Slice((*uint32)(bf.Code.Ptr()), size/4)

	// STR X27, [X19, #8] — ARM64 unsigned-offset store (64-bit):
	//   size=11, V=0, opc=00, imm12=1 (8/8), Rn=X19=19, Rt=X27=27
	//   encoding = 0xF9000000 | (1<<10) | (19<<5) | 27 = 0xF900067B
	const strConstEncoding = uint32(0xF900067B)

	// B (unconditional branch) opcode: bits[31:26] = 000101 → mask 0xFC000000, val 0x14000000.
	const bMask = uint32(0xFC000000)
	const bVal = uint32(0x14000000)

	// Count occurrences of the pattern:
	//   insn[i]   == STR X27, [X19, #8]
	//   insn[i+1] == B <forward>   (imm26 > 0)
	//
	// This pattern identifies the normal-call restore STR (placed immediately
	// before B restoreDoneLabel). The other STR X27,[X19,#8] in each CALL site
	// (the setup STR that writes the callee's constants) is followed by another
	// STR (ClosurePtr write-back), not by B.
	normalCallRestoreSTRs := 0
	for i, insn := range code {
		if insn != strConstEncoding {
			continue
		}
		if i+1 >= len(code) {
			continue
		}
		next := code[i+1]
		if (next & bMask) != bVal {
			continue
		}
		// Sign-extend imm26 to check direction.
		imm26 := int32(next & 0x03FFFFFF)
		if imm26&0x02000000 != 0 {
			imm26 |= ^int32(0x03FFFFFF)
		}
		if imm26 > 0 {
			normalCallRestoreSTRs++
			t.Logf("restore STR at insn %d → B +%d ✓", i, imm26)
		}
	}

	// Ackermann has 3 CALL sites (pc=13, pc=22, pc=23). Each should have
	// exactly one STR X27,[X19,#8] in the normal-call restore block.
	const wantRestoreSTRs = 3
	if normalCallRestoreSTRs != wantRestoreSTRs {
		t.Errorf("R28 Task 1 regression: expected %d restore STR(s) (STR X27→B<fwd>) per CALL site, got %d; optimization may be reverted or the join structure changed",
			wantRestoreSTRs, normalCallRestoreSTRs)
	} else {
		t.Logf("STR X27,[X19,#8] in normal-call restore block: %d/%d ✓", normalCallRestoreSTRs, wantRestoreSTRs)
	}
}

func TestSelfTailNoReturn_EligibilityQuicksortSecondCallOnly(t *testing.T) {
	top := compileTop(t, `
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}
`)
	qs := findProtoByName(top, "quicksort")
	if qs == nil {
		t.Fatal("quicksort proto not found")
	}
	eligible := 0
	for pc, inst := range qs.Code {
		if vm.DecodeOp(inst) == vm.OP_CALL && isBaselineStaticSelfTailNoReturnCall(qs, inst, pc) {
			eligible++
		}
	}
	if eligible != 1 {
		t.Fatalf("eligible self tail no-return calls = %d, want exactly quicksort's second recursive call", eligible)
	}
}

func TestSelfTailNoReturn_RejectsClosureState(t *testing.T) {
	top := compileTop(t, `
func outer() {
    x := 0
    func f(n) {
        if n <= 0 { return }
        x = x + 1
        f(n - 1)
    }
    f(3)
    return x
}
`)
	f := findProtoByName(top, "f")
	if f == nil {
		t.Fatal("f proto not found")
	}
	for pc, inst := range f.Code {
		if vm.DecodeOp(inst) == vm.OP_CALL && isBaselineStaticSelfTailNoReturnCall(f, inst, pc) {
			t.Fatalf("capturing self tail call at pc %d was incorrectly eligible", pc)
		}
	}
}
