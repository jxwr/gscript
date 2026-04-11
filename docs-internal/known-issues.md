# Known Issues

> Last updated: 2026-04-11 (R28 review)

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

### ackermann: regression from GetGlobal native cache (+144%)
- Native cache adds ~10 instructions of generation checking per GetGlobal
- ack has 2 GetGlobals per call × millions of recursive calls in tight loop
- Cache always hits — overhead is the check itself, not misses
- Fix: consider skip-cache path for functions with no SetGlobal in module scope
- Category: `tier2_call_overhead`

### TestDeepRecursionRegression: GC stack-scan crash + goroutine deadlock (pre-existing)
- `quicksort_5000` sub-test: Go GC scanner hits JIT frames → "fatal error: traceback did not unwind completely" or SIGSEGV during `mgcmark.go:scanstack`
- `linear_recursion_500` sub-test: goroutine hangs in `chanrecv` — test never completes
- Both reproduce at baseline (test_deep_recursion_test.go:132, commit 0ecdc5e)
- Root cause: JIT-compiled native frames are not annotated for Go GC unwinding; GC triggered during deep JIT recursion can't walk the stack
- Workaround: run full suite with `-short` flag; the two sub-tests inside `TestDeepRecursionRegression` are affected
- Category: `tier2_recursion`

### TestQuicksortSmall: SIGBUS crash in JIT-generated code (pre-existing)
- `callJIT` hits SIGBUS executing generated ARM64 code for quicksort
- Reproduces consistently at baseline commit (before Round 22 changes)
- Likely partition/swap codegen issue with table array access + recursive calls
- Category: `tier2_recursion`

### sort: intermittent hang (pre-existing)
- sort.gs hangs or takes 375s intermittently
- Reproduces even at baseline commit — pre-existing issue
- Related to Tier 2 recursive function compilation
- Category: `tier2_recursion`

### emit_dispatch.go: 969 lines (approaching 1000 limit)
- Needs split: extract `emit_branch.go` for fused compare+branch logic
- Flagged by evaluator in Round 10, 14. Must split before next change to this file.

### LoadElim available map not invalidated by OpSetTable (pre-existing)
- `SetTable(obj, key, val)` with dynamic key could alias a GetField(obj, field)
- The available map only invalidates on SetField/Call/Self
- Pre-existing before Round 18, but store-to-load forwarding makes stale entries more visible
- Low risk: dynamic-key overwrites of named fields are unusual

### fib/ackermann/mutual_recursion: Tier 2 is net-negative (Round 11)
- Tier 2 BLR overhead (15-20ns) > Tier 1 BLR (10ns) for recursive functions
- SSA construction + type guards cost more than inlining gains
- These stay at Tier 1; speedup needs native recursive BLR or Tier 1 specialization
- Category: `recursive_call` (ceiling = 2)

## Historical (>5 rounds old — not read by ANALYZE)

### Fixed (Rounds 13-15, 2026-04-06)
- spectral_norm 42x→7.1x (R15): OSR re-enabled with LoopDepth≥2 gate
- mandelbrot 6.4x→1.27x (R15): same OSR fix; 0.393s→0.080s
- sieve (R13-14): native bool/float fast paths + Tier 1 bypass; 0.227s→0.085s
- matmul (R9-15): LICM-carry −13%, OSR −29%; 0.999s→0.152s
- Tier 2 pipeline duplicated (R19): extracted RunTier2Pipeline() in pipeline.go
- benchmarks: single-shot runner (R25): replaced with median-of-N; 3-5% CV eliminated

### Fixed (Rounds 1-12, 2026-04-04-05)
- fibonacci_iterative (R3): phi regalloc clash — 2 phis assigned same GPR
- sieve hang (R1): rawIntRegs state corruption in deopt path
- sum_primes wrong count (R1): GPR phi move ordering
- nbody/table_field_access (R2): resyncRegs() reset ctx.Regs after BLR op-exit
- coroutine_bench wrong (R2): int48 overflow truncation in EmitBoxIntFast
- object_creation len_sq=0 (R2): inline pass phi rewrite missed loop header
- string_bench (early): FCMPd on NaN-boxed pointers returned unordered

### Historical (Trace JIT — deprecated)

### spectral_norm: float accumulator treated as int
### nbody: guard-fail from slot reuse type mismatch
### sort: stack overflow from recursive calls via call-exit
### GC scanTableRoots intermittent SIGSEGV (partially fixed)
