# Known Issues

> Last updated: 2026-04-06

## Current

### method_dispatch: 0.85x regression (known)
- Native BLR call adds type-check + DirectEntryPtr-load overhead even when falling to slow path
- method_dispatch calls many small functions per iteration, some are GoFunctions (math.sqrt)
- Category: `gofunction_overhead`

### binary_trees: 0.84x regression (known)
- Allocation-heavy benchmark, JIT overhead with no compute benefit
- NEWTABLE is exit-resume, can't inline allocation
- Needs escape analysis + scalar replacement
- Category: `allocation_heavy`

### object_creation: 0.81x regression (known)
- NEWTABLE exit-resume overhead dominates
- Same root cause as binary_trees
- Category: `allocation_heavy`

### coroutine_bench: 1.0x (JIT = VM, no benefit)
- Coroutine yield/resume goes through Go runtime, JIT can't optimize
- Not a priority target

### emit_dispatch.go: 969 lines (approaching 1000 limit)
- Needs split: extract `emit_branch.go` for fused compare+branch logic
- Flagged by evaluator in Round 10, 14. Must split before next change to this file.

### emit_table.go: 978 lines (CRITICAL — 22 from limit)
- Four-way arrayKind dispatch in both GetTable and SetTable creates duplication
- Must split into `emit_table_get.go` / `emit_table_set.go` BEFORE any changes

### LICM GetField alias scan incomplete (Round 18)
- `OpAppend` and `OpSetList` not included in LICM's aliasing scan
- Both mutate the table but LICM won't block GetField hoisting if they appear in loop
- Low risk: named-field access on a table being appended to is rare
- Fix: add OpAppend/OpSetList to hasLoopCall or a separate flag in pass_licm.go

### LoadElim available map not invalidated by OpSetTable (pre-existing)
- `SetTable(obj, key, val)` with dynamic key could alias a GetField(obj, field)
- The available map only invalidates on SetField/Call/Self
- Pre-existing before Round 18, but store-to-load forwarding makes stale entries more visible
- Low risk: dynamic-key overwrites of named fields are unusual

### Tier 2 pipeline duplicated in 3 places — extract shared RunTier2Pipeline()
- `compileTier2()` in tiering_manager.go is the production pipeline (10 passes)
- `Diagnose()` in diagnose.go had its own copy (synced in Round 18 fix, but still a separate copy)
- `tier2_float_profile_test.go` still has a stale 4-pass copy (missing Intrinsic/Inline/LoadElim/Range/LICM)
- Other test files may also have stale copies
- **Fix**: extract `RunTier2Pipeline(fn) (*Function, error)` as a shared function. One definition, zero drift.
- Category: `arch_refactor`

### benchmarks/run_all.sh: VM/JIT suite may report inaccurate times
- Round 12 MEASURE discovered silent failures in suite mode
- Individual benchmark runs (`gscript -jit file.gs`) are reliable
- Suite mode output parsing can lose time values

### fib/ackermann/mutual_recursion: Tier 2 is net-negative (Round 11)
- Tier 2 BLR overhead (15-20ns) > Tier 1 BLR (10ns) for recursive functions
- SSA construction + type guards cost more than inlining gains
- These stay at Tier 1; speedup needs native recursive BLR or Tier 1 specialization
- Category: `recursive_call` (ceiling = 2)

## Fixed (2026-04-06, Rounds 13-15)

### spectral_norm: was 42x behind LuaJIT — now 7.1x (Round 15)
- Root cause: OSR was disabled since Round 4 hang. Single-call functions never promoted to Tier 2.
- Fix: re-enable OSR with `LoopDepth >= 2` gate. 11 rounds of Tier 2 improvements became visible.

### mandelbrot: was 6.4x behind LuaJIT — now 1.27x (Round 15)
- Same OSR root cause as spectral_norm
- mandelbrot 0.393s → 0.080s (−80%). First benchmark approaching LuaJIT parity.

### sieve: was 0.227s — now 0.085s (Rounds 13-14)
- Round 13: native ArrayBool/ArrayFloat fast paths in Tier 2 emit (−18%)
- Round 14: Tier 1 float/bool table fast paths + feedback infrastructure + Tier 2 raw-int/const-bool bypass (−54%)

### matmul: was 0.999s — now 0.152s (Rounds 9-15)
- Round 9: LICM-carry (−13%). Round 15: OSR re-enable (−29%)

## Fixed (2026-04-04-05, Rounds 1-12)

### fibonacci_iterative: FIXED (Round 3, phi regalloc clash)
- Register allocator assigned 2 phis the same physical register
- Fixed by pre-allocating all phis simultaneously

### sieve: was hanging — FIXED (Round 1)
- rawIntRegs build-time state corruption in deopt path emission

### sum_primes: wrong count — FIXED (Round 1)
- GPR phi move ordering

### nbody: energy not updating — FIXED (Round 2)
- resyncRegs() in Tier 1 execute loop reset ctx.Regs after BLR callee op-exit

### table_field_access: garbage checksum — FIXED (Round 2)
- Same resyncRegs corruption as nbody

### coroutine_bench: generator_sum wrong — FIXED (Round 2)
- int48 overflow truncation in EmitBoxIntFast

### object_creation: len_sq=0 — FIXED (Round 2)
- inline pass rewriteValueRefs only updated current block, not loop header phis

### string_bench: FIXED (Tier 1 string LT/LE exit-resume)
- FCMPd on NaN-boxed pointers returned "unordered"

## Historical (Trace JIT — deprecated)

### spectral_norm: float accumulator treated as int
### nbody: guard-fail from slot reuse type mismatch
### sort: stack overflow from recursive calls via call-exit
### GC scanTableRoots intermittent SIGSEGV (partially fixed)
