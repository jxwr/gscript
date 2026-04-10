# R26 Premise Error — Task 1: SP-floor cannot replace NativeCallDepth

**Date**: 2026-04-11  
**Cycle**: 2026-04-11-tier1-selfcall-overhead  
**Task failed**: Task 1 — Remove NativeCallDepth bookkeeping on self-call path  
**Protocol**: IMPLEMENT abort; hand to VERIFY as `data-premise-error`

---

## The claim (from plan)

> "Replace NativeCallDepth guard on self-call path with a single `CMP sp, StackFloor` check at function entry (3 insns once vs 6 insns per call). StackFloor = SP_at_entry − 2MB, set once per outermost entry."

The plan further stated:

> "The ARM64 stack already enforces the depth limit — when `sp` runs off the bottom of the guard page, the OS delivers SIGSEGV. Our depth counter is a redundant safety net."

---

## What the code actually does

**Go goroutines do not have 2MB of stack at JIT entry.**

Go goroutines start with an **8KB** stack segment (source: Go runtime `stack.go`, `_StackMin = 8192`). When a goroutine's stack overflows, Go inserts a `morestack` call that allocates a new, larger segment. This only works for Go-compiled functions, which carry stack-growth metadata (function prologue checks `SP < stackguard0`).

JIT code is not Go-compiled. It is a raw machine-code blob allocated via `mmap`. It has no prologue, no stack-growth metadata, and never calls `morestack`. The JIT code must fit within whatever goroutine stack space is already allocated when `callJIT` is invoked.

**The depth limit that NativeCallDepth enforces:**  
With NativeCallDepth limit = 48 and 64 bytes per self-call frame, the maximum native stack growth from JIT code is 48 × 64 = 3072 bytes — comfortably within the 8KB goroutine stack.

**What removing NativeCallDepth tracking from self-calls does:**  
If inc/dec are removed from the self-call branch, the counter never accumulates during recursion. The pre-check `LDR depth; CMP 48; b.ge slow` never fires. The result is unlimited native recursion. `countdown(900)` → 900 × 64 bytes = 57.6KB of native stack growth → overflows the goroutine stack segment → memory corruption.

---

## Observed failure (TestDeepRecursionSimple)

```
SIGSEGV: segmentation violation
fault addr: 0xfffe0000000002ed
goroutine 21 [running]:
runtime: unexpected return pc for sync.poolCleanup called from 0xfffe0000000002ed
```

`0xfffe0000000002ed` is a NaN-boxed integer (integer value 749, tag `0xFFFE...`). The corrupted goroutine stack was read by the GC's `sync.Pool` cleanup as a pointer. The GC tried to call it as a function.

This crash occurs within the first test run after removing NativeCallDepth tracking from the self-call path. It is not a transient failure.

---

## Why StackFloor = SP - 2MB doesn't work

1. `SP - 2MB` produces a negative (or zero-crossing) address. A goroutine at entry has ~7KB remaining below SP. Subtracting 2MB underflows into virtual address space below the goroutine's stack segment — an address that is never mapped. The `CMPreg SP, StackFloor` check would fire immediately on the first call (SP > the gigantic computed floor address after wraparound), or would never fire (if the comparison wraps around to look valid).

2. Even if the floor were computed as `current_SP - 4096` (leaving 4KB headroom), this still allows hundreds of native recursion levels before crashing — it just fails later.

3. **The architectural constraint**: JIT code cannot grow the goroutine stack. Any approach that allows unbounded native recursion from JIT code will corrupt memory on sufficiently deep recursion.

---

## Secondary failure: insn count regression

The StackFloor approach added more instructions than it removed:

| Change | Delta |
|---|---|
| Remove pre-check at 3 call sites | −9 insns |
| StackFloor compute in normal prologue | +6 insns |
| SP-floor check in direct_entry prologue | +4 insns |
| SP-floor check in self_call_entry prologue | +4 insns |
| ExitStackOverflow handler + exitHandleLabel check | +25 insns |
| **Net** | **+30 insns** |

Result: `TestDumpTier1_AckermannBody` FAILED: 953 > baseline 923 (+30 insns).

---

## Disposition

- **Task 0** (insn-count regression fixture): COMMITTED at `878e64a`. Stays.
- **Task 1** changes: REVERTED. All source files restored to pre-Task-1 state.
- **Tasks 2 and 3**: SKIPPED. Task 2 (drop Constants STR) could land independently, but per protocol no silent replanning during IMPLEMENT abort.

---

## Root cause of planning error

The plan stated "the ARM64 stack already enforces the depth limit via SIGSEGV guard page." This is correct for normal OS processes (where the main thread has ~8MB of stack), but not for Go goroutines (which start at 8KB). The ANALYZE session did not check the Go goroutine stack model before asserting the claim.

---

## What ANALYZE should do next round

1. Enumerate what NativeCallDepth actually protects — specifically, how many bytes of frame each self-call level allocates, and what the goroutine stack budget is.
2. Research whether Go's `runtime.entersyscall` / stack growth machinery can be used to pre-grow the goroutine stack before entering the JIT outermost frame.
3. Consider whether the `countdown(900)` test can itself pre-grow the stack (via a Go allocation) before calling into Tier 1.
4. Alternatively: accept NativeCallDepth as a permanent constraint and focus on reducing the insns *within* the inc/dec pair (e.g., storing to a register instead of memory).
5. Task 2 (drop dead `ctx.Constants` STR on self-call restore) is independently safe — no architectural dependency on NativeCallDepth. Plan it as Task 1 next round if the ceiling hasn't moved.
