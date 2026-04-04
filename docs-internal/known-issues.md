# Known Issues

> Last updated: 2026-04-04

## Current

### fibonacci_iterative: FIXED (phi regalloc clash)
- Root cause was NOT overflow — register allocator assigned 2 phis the same physical register
- Loop header with 3+ phis (a, b, i) triggered the clash
- Fixed by pre-allocating all phis simultaneously in allocateBlock

### string_bench: FIXED (Tier 1 string LT/LE exit-resume)
- Root cause: emitBaselineLT/LE fell through to float fallback for string operands
- FCMPd on NaN-boxed pointers returned "unordered", branch never taken, swap never fired
- Fixed by exiting to Go via ExitBaselineOpExit for string-tagged operands

### spectral_norm: 2.0x vs VM (still slower than pre-overflow-check baseline)
- Was 0.138s before int48 overflow check, now 0.502s
- Loop-counter exemption (Aux2=1) + range analysis eliminate most checks but wall-time unchanged
- Likely bottleneck elsewhere: FPR spills, guard overhead, or Tier 2 float codegen
- Next investigation: pprof Tier 2 emitted code for spectral_norm inner loop

### method_dispatch: 0.85x regression (known)
- Native BLR call adds type-check + DirectEntryPtr-load overhead even when falling to slow path
- method_dispatch calls many small functions per iteration, some are GoFunctions (math.sqrt)

### binary_trees: 0.84x regression (known)
- Allocation-heavy benchmark, JIT overhead with no compute benefit

### object_creation: 0.81x regression (known)
- NEWTABLE exit-resume overhead dominates
- BLR callee re-execution (post fix) adds overhead for functions with op-exits

## Fixed (2026-04-04)

### sieve: was hanging (infinite loop) — FIXED
- Root cause: rawIntRegs build-time state corruption in deopt path emission
- emitReloadAllActiveRegs deleted rawIntRegs entries, corrupting while-loop body arithmetic
- Fix: save/restore rawIntRegs around deopt path emission + emitUnboxRawIntRegs

### sum_primes: wrong count — FIXED
- Root cause: GPR phi move ordering (same as sieve)

### nbody: energy not updating — FIXED
- Root cause: resyncRegs() in Tier 1 execute loop reset ctx.Regs to outer function's base after BLR callee op-exit
- Fix: re-execute callee from scratch via e.Execute(), disable DirectEntryPtr

### table_field_access: garbage checksum — FIXED
- Root cause: same as nbody (resyncRegs corruption)

### coroutine_bench: generator_sum wrong — FIXED
- Root cause: int48 overflow truncation in EmitBoxIntFast (UBFX silently dropped bits > 47)
- Fix: SBFX+CMP overflow check after ADD/SUB/MUL, Tier 1 promotes to float, Tier 2 deopts

### object_creation: len_sq=0 — FIXED
- Root cause: inline pass rewriteValueRefs only updated current block, not loop header phis
- Fix: scan ALL blocks after inlining to rewrite references to dead CALL ID

## Historical (Trace JIT — deprecated, for reference only)

### spectral_norm: float accumulator treated as int
- `findAccumulators()` detected `sum` as int accumulator, but `sum := 0.0` is float

### nbody: guard-fail from slot reuse type mismatch
- GETTABLE not recognized as a write by `isWrittenBeforeFirstReadExt`

### sort: stack overflow from recursive calls via call-exit
- Trace JIT's call-exit went through `vm.call`, deep recursion exceeded `maxCallDepth=200`

### GC scanTableRoots intermittent SIGSEGV
- GC compaction encountered stale NaN-boxed pointers in edge cases
- Partially fixed via safe-point mechanism
