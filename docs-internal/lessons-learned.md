# Lessons Learned — GScript JIT Compiler

> **Purpose**: This document captures detours and mistakes made during the project.
> The gs-opt-loop agent MUST read this during every ANALYZE phase before choosing an optimization direction.

---

## Detour 1: Trace JIT (LuaJIT-style) — ~3 weeks

**What was built**: A full trace recording + SSA compilation pipeline modeled on LuaJIT. Recorded hot loop traces, compiled them to ARM64 via a simple linear IR.

**Results**: nbody 5.8x, table_field_access 11x — looked promising. But fib 1.0x, spectral_norm 0.9x, mutual_recursion 0.8x.

**Why it failed**: Traces are blind to functions. A trace captures a straight-line path through a loop body. It cannot represent function boundaries, recursion, or branchy call-heavy code. Three attempts to fix this all failed:
1. Trace-through-calls: followed execution into callees, but shared `regRegs` caused store-back corruption across call depths.
2. Function-entry tracing: triggered compilation at function entry instead of loop back-edges. Got fib(35) to 46ms but broke 8 benchmarks (segfaults, wrong results).
3. Trace-only rewrite: deleted 13,000 lines including the working Method JIT in progress. Wrong direction entirely.

**Early warning signals that were missed**:
- fib and mutual_recursion showed no improvement within the first week of trace JIT work. Function-heavy benchmarks that don't benefit at all are a sign the approach is structurally wrong, not a tuning issue.
- Each fix attempt broke something new. That pattern means the design has a fundamental flaw.

**What replaced it**: V8-style Method JIT — compile entire functions, not loop traces (ADR-001).

---

## Detour 2: Memory-to-Memory Middle Tier

**What was built**: A three-tier system where "Tier 2" was memory-to-memory (load operands from stack slots, compute, store result back) and "Tier 3" was the register-allocated tier. The intention was to have a fast-to-compile intermediate tier.

**Results**: The memory-to-memory Tier 2 was actually slower than Tier 1's template JIT on most benchmarks. Every instruction had redundant loads and stores that Tier 1's templates didn't have. The "middle tier" added code complexity and maintenance burden without any performance benefit.

**Why it failed**: A tier that is slower than the tier below it is not a tier — it is dead code. Memory-to-memory code cannot beat template JIT because templates already handle the common case optimally. The extra load/store pairs per instruction add latency that dwarfs any savings from skipping IR construction.

**The code was deleted**: ~5,600 lines of Tier 2 emission code removed (ADR-002: "Keeping broken code because it was expensive to write is the sunk cost fallacy").

**Early warning signal**: If a new tier is slower than the previous tier on ANY benchmark, the tier design is wrong. Do not iterate on it — stop and reconsider the design.

**What replaced it**: Direct two-tier system: Tier 1 (baseline template JIT) → Tier 2 (optimizing: SSA + passes + register allocation + ARM64). The existing "Tier 3" register-allocated emit became the real Tier 2.

---

## Detour 3: Function-Entry Tracing

**What was attempted**: Trigger trace recording at function entry points rather than loop back-edges. The goal was to capture function bodies as traces to get speedup on recursive functions like fib.

**Results**: fib(35) improved to 46ms — but 8 out of 21 benchmarks produced wrong results or segfaulted.

**Why it failed**: The trace recorder was built around the assumption that traces are loop-shaped (back-edge closes the trace). Function-entry traces have different control flow: they can have multiple exit points, early returns, and recursive calls that violate the loop-trace invariants. The recording machinery did not handle these cases, producing corrupted state on non-trivial functions.

**Early warning signal**: More than 2 benchmarks producing wrong results means a fundamental approach problem, not implementation bugs. When 8 benchmarks are broken, no amount of targeted fixing will work — the design must change.

**What replaced it**: Per-function compilation with universal op-exit-resume (ADR-004). Every function gets compiled in its entirety. Unsupported ops use exit-resume (exit to Go, execute op, resume JIT at next PC) rather than trying to record through them.

---

## General Lessons

### 1. Multiple regressions = architecture problem, not implementation bug

If an optimization makes multiple benchmarks worse, stop and rethink direction. Do not attempt targeted fixes benchmark by benchmark. The pattern of widespread regression means the approach itself is wrong.

### 2. Two consecutive failed rounds on the same bottleneck = ceiling

If two consecutive optimization rounds show no progress on the same bottleneck category (e.g., two different approaches to speeding up function calls both plateau), the approach has hit an architectural ceiling. Research alternatives before continuing. Ask: "What does V8/SpiderMonkey/LuaJIT do here?"

### 3. V8's architectural choices have been correct every time

Method JIT over trace JIT, three-tier architecture (with clean separation), CFG SSA IR (not Sea of Nodes), forward-walk register allocation — every time this project adopted V8's approach after initially trying something else, it got the right result. Default to V8's approach when unsure.

### 4. Correctness first, always

A wrong-but-fast result is worse than a slow-but-correct one. It poisons all future benchmark comparisons — you cannot trust any number from a run that includes incorrect benchmarks. Rule: run the full suite; never remove a broken benchmark; show "ERROR"/"HANG" rather than hiding it.

### 5. Use diagnostic tools instead of guessing

Don't read code and guess what's wrong. Use `Diagnose()`, IR interpreter, `Validate()`, register dumps, and `Print()` first. Identify the exact file and pass responsible before reading source. The project has seen five hours of guessing resolved in five minutes by dumping the IR and reading the output.

### 6. Profile before optimizing

The #1 bottleneck is often not what intuition says. NEWTABLE dominated benchmarks that looked like they should be compute-bound. GETGLOBAL dominated benchmarks that looked like call-bound. Measure with pprof before choosing what to optimize.

### 7. Never stack on unverified code

Before adding optimization pass N+1, all tests must pass with passes 1..N. Run `Validate()` after every pass in the pipeline. If a pass breaks the IR, the validator tells you exactly which pass — without it, debugging takes an order of magnitude longer.

### 8. Sunk cost is not a reason to keep broken code

5,600 lines of broken emission code were deleted in one decision. The IR, passes, and regalloc infrastructure were preserved; only the broken emission layer was removed. Expensive-to-write code that is wrong should be deleted, not worked around.

---

## How This Document Is Used

- The `gs-opt-loop` skill reads this document during every ANALYZE phase.
- Before choosing an optimization direction: "Am I about to repeat a known detour?"
- Before continuing a stalled optimization: "Does this match the ceiling pattern from lesson #2?"
- Before shipping a multi-benchmark regression: "Does this match lesson #1?"

The three detours above cost roughly 4-5 weeks of work combined. The lessons are not abstract — they are specific patterns that recurred in this codebase.
