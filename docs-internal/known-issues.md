# Known Issues

> Last updated: 2026-04-04

## Current

### fibonacci_iterative: fib(70) returns fib(68) — deopt state at overflow boundary
- Int48 overflow check fires when a+b exceeds 2^47 at iteration 69→70
- Deopt exits before the loop body completes (a=b, b=temp assignments not executed)
- Returns `a` = fib(68) instead of continuing in interpreter to complete the iteration
- Root cause: deopt at mid-instruction granularity doesn't complete the current loop body
- Fix needed: deopt should restore state so interpreter can complete the current iteration

### string_bench: compare sub-test gives different sort order
- VM: first..last=key_00000 .. key_00999
- JIT: first..last=key_00007 .. key_00000
- String comparison or sort correctness issue in JIT mode
- Not yet investigated

### spectral_norm: 3.7x regression from int48 overflow check
- Was 0.138s (7.2x vs VM), now 0.506s (2.0x vs VM)
- The int48 overflow check (SBFX+CMP+B.NE after every ADD/SUB/MUL) adds overhead to tight numeric loops
- Fix needed: skip overflow check on loop counters with known-small range, or use type-specialized overflow-free paths

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
