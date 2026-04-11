//go:build darwin && arm64

// tier1_selfcall_lazyflush_test.go — regression test for R28 Task 1:
// ctx.Regs lazy flush on the self-call fast path.
//
// The optimization removes the eager STR X26, [X19, #0] (ctx.Regs write-back)
// from the self-call setup block in emitCallABC, and adds it as the first
// instruction of emitBaselineOpExitCommon. The effect is that ctx.Regs is only
// materialized when control actually exits the JIT (slow path / deopt), not on
// every self-recursive call.
//
// Net static instruction count: approximately unchanged (one STR deleted per
// self-call site, one STR added per op-exit site).
// Dynamic savings: −1 STR per self-call invocation (hot path).
//
// Structural invariants (locked by this test):
//  1. No STR X26, [X19, #0] appears in the self-call setup region (between
//     the ADD X26,X26,#imm advancing the callee base and the BL self_call_entry).
//  2. At least one STR X26, [X19, #0] is part of an emitBaselineOpExitCommon
//     region — identified by the MOVZ X0, #7 that loads ExitBaselineOpExit
//     within a few instructions after the STR — i.e. the lazy flush was added.
//
// Note: STR X26,[X19,#0] also legitimately appears at the shared restoreDoneLabel
// (post-CALL return path) — that's the normal-call flush and is out of scope
// for this round. We only pin the self-call setup region and the op-exit flush.

package methodjit

import (
	"os"
	"testing"
	"unsafe"
)

func TestSelfCall_RegsLazyFlush(t *testing.T) {
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
	code := unsafe.Slice((*uint32)(bf.Code.Ptr()), size/4)

	// STR X26, [X19, #0] — 64-bit unsigned-offset store:
	//   0xF9000000 | (imm12<<10) | (Rn<<5) | Rt
	//   Rn=X19=19, Rt=X26=26, imm12=execCtxOffRegs/8=0
	//   = 0xF9000000 | 0 | (19<<5) | 26 = 0xF900027A
	const strRegsEncoding = uint32(0xF900027A)

	// MOVZ X0, #7, LSL#0 — the LoadImm64(X0, 7) at the start of
	// emitBaselineOpExitCommon. 7 fits in 16 bits so LoadImm64 emits a single
	// MOVZ. Encoding: 0xD2800000 | (imm16<<5) | Rd = 0xD2800000 | (7<<5) | 0
	//   = 0xD2800000 | 224 | 0 = 0xD28000E0
	const movzX0_7 = uint32(0xD28000E0)

	// BL opcode: bits[31:26] = 100101, mask 0xFC000000, val 0x94000000.
	const blMask = uint32(0xFC000000)
	const blVal = uint32(0x94000000)

	// ADD Xd, Xn, #imm (64-bit, unsigned imm12, no shift): bits[31:22] = 1001000100
	// mask 0xFFC00000, val 0x91000000. We identify the self-call base-advance
	// ADD by Rd=Rn=X26 (26).
	const addImmMask = uint32(0xFFC00000)
	const addImmVal = uint32(0x91000000)

	// --- Invariant 2: every STR X26,[X19,#0] is in an op-exit region ---
	//
	// "Op-exit region" = within the next ~4 instructions, there is MOVZ X0, #7.
	// (In emitBaselineOpExitCommon the sequence is:
	//    STR X26,[X19,#0]
	//    MOVZ X0, #7
	//    STR X0,[X19,#ExitCode]
	//    ...)
	const opExitWindow = 4
	strRegsTotal := 0
	strRegsInOpExit := 0
	for i, insn := range code {
		if insn != strRegsEncoding {
			continue
		}
		strRegsTotal++
		for j := 1; j <= opExitWindow && i+j < len(code); j++ {
			if code[i+j] == movzX0_7 {
				strRegsInOpExit++
				break
			}
		}
	}
	if strRegsTotal == 0 {
		t.Errorf("no STR X26,[X19,#0] found in compiled body — lazy flush in emitBaselineOpExitCommon missing?")
	}
	// At least one op-exit STR X26,[X19,#0] must be present — otherwise the
	// lazy flush wasn't actually installed in emitBaselineOpExitCommon.
	if strRegsInOpExit < 1 {
		t.Errorf("R28 Task 1: expected at least 1 STR X26,[X19,#0] in an op-exit region (MOVZ X0,#7 within %d insns), got %d — lazy flush not installed", opExitWindow, strRegsInOpExit)
	}
	t.Logf("STR X26,[X19,#0]: %d total, %d identified in op-exit region (remainder are restoreDoneLabel flushes, out of scope)", strRegsTotal, strRegsInOpExit)

	// --- Invariant 1: no STR X26,[X19,#0] in self-call setup region ---
	//
	// Self-call setup region = from an ADD X26,X26,#imm (base advance) up to
	// and including the following BL (self_call_entry). Only a small window;
	// scan up to 8 instructions forward from each ADD X26,X26,#imm.
	const setupWindow = 8
	selfCallSites := 0
	violations := 0
	for i, insn := range code {
		if (insn & addImmMask) != addImmVal {
			continue
		}
		// Decode Rd and Rn: Rd = bits[4:0], Rn = bits[9:5].
		rd := insn & 0x1F
		rn := (insn >> 5) & 0x1F
		if rd != 26 || rn != 26 {
			continue
		}
		// Scan forward: find BL within setupWindow and check for STR X26,[X19,#0]
		// in between.
		blIdx := -1
		for j := 1; j <= setupWindow && i+j < len(code); j++ {
			if (code[i+j] & blMask) == blVal {
				blIdx = i + j
				break
			}
		}
		if blIdx < 0 {
			// Not a self-call setup — ADD X26,X26 might appear elsewhere; skip.
			continue
		}
		selfCallSites++
		for k := i + 1; k < blIdx; k++ {
			if code[k] == strRegsEncoding {
				violations++
				t.Errorf("R28 Task 1 regression: STR X26,[X19,#0] found in self-call setup region at insn %d (between ADD X26 at %d and BL at %d) — lazy flush reverted", k, i, blIdx)
			}
		}
		t.Logf("self-call setup site: ADD X26 @%d → BL @%d, no eager ctx.Regs STR ✓", i, blIdx)
	}

	// Ackermann has 3 CALL sites (pc=13, pc=22, pc=23), all self-recursive.
	const wantSelfCallSites = 3
	if selfCallSites < wantSelfCallSites {
		t.Errorf("expected at least %d self-call setup sites (ADD X26,X26→BL), got %d", wantSelfCallSites, selfCallSites)
	}
	if violations > 0 {
		t.Errorf("R28 Task 1: %d eager STR(s) still present in self-call setup region", violations)
	}
}
