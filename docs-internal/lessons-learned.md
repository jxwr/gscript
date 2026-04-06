# Lessons Learned — GScript JIT Compiler

> **Purpose**: Detours and mistakes. ANALYZE reads this every round before choosing a target.
> Last updated: 2026-04-06 (Round 15)

---

## Detour 1: Trace JIT (LuaJIT-style) — ~3 weeks

Traces are blind to functions. fib 1.0x, mutual_recursion 0.8x. Three attempts to fix (trace-through-calls, function-entry tracing, trace-only rewrite) all failed. Replaced by V8-style Method JIT.

**Early warning**: function-heavy benchmarks showing zero improvement = structural problem, not tuning.

## Detour 2: Memory-to-Memory Middle Tier

Slower than Tier 1 on most benchmarks. Every instruction had redundant loads/stores. Deleted ~5,600 lines.

**Early warning**: new tier slower than the tier below it on ANY benchmark = wrong design.

## Detour 3: Function-Entry Tracing

fib improved to 46ms but 8/21 benchmarks broken. Trace recorder assumes loop-shaped traces; function-entry traces violate these invariants.

**Early warning**: >2 benchmarks wrong = architecture problem, not implementation bugs.

---

## Lessons from Method JIT Optimization (Rounds 1-15)

### 4. Read your own code before planning (Rounds 7-8)

Rounds 7-8 predicted −35%/−40% on mandelbrot. Got −1.88%/−1.6%. The ANALYZE phase read V8 source and web-searched techniques but **never read `regalloc.go`**. The `carried` map doesn't include LICM-hoisted values — you'd know this from reading the code.

Round 9 read the ARM64 disasm first: 47 insns/iter, 72% overhead. Targeted the exact cause. Got −12~15%.

**Rule**: ANALYZE must read the source files it plans to change AND run diagnostics (IR dump, ARM64 disasm) before writing a plan.

### 5. ARM64 superscalar hides instruction-count savings (Round 10)

GPR int counter optimization removed ~23% of inner-loop instructions. Wall-time improved only 1-7%. Apple M-series executes 6-8 instructions per cycle; reducing instruction count doesn't linearly reduce time.

**Rule**: halve instruction-count-based predictions on ARM64. Cross-check with dependency chain analysis, not just instruction count.

### 6. Tier 2 is net-negative for recursive functions (Round 11)

Predicted 2-4× improvement on fib/ackermann by promoting to Tier 2. Had to revert — Tier 2 BLR is 15-20ns vs Tier 1's 10ns. SSA construction + type guards cost more than inlining gains for call-dominated code.

**Rule**: don't assume Tier 2 is always better. Check BLR overhead vs compute ratio.

### 7. Disabled features silently block improvements (Round 15)

OSR was disabled in Round 4 after a hang bug. 11 rounds of Tier 2 improvements (LICM, FPR carry, GPR counter, feedback guards) were invisible to single-call compute functions (mandelbrot, spectral_norm, matmul, fannkuch). Re-enabling OSR with a `LoopDepth >= 2` gate gave mandelbrot −80% in one commit.

**Rule**: after fixing a bug that caused a feature to be disabled, re-enable the feature. Check `constraints.md` for disabled features periodically.

### 8. Redundant phases waste context and produce no value (harness)

The original 7-phase loop (MEASURE→ANALYZE→RESEARCH→PLAN→IMPLEMENT→VERIFY→DOCUMENT) had massive redundancy: ANALYZE did web search, PLAN did web search again; MEASURE ran benchmarks, VERIFY re-ran them; PLAN just reformatted ANALYZE's output.

**Rule**: each phase must produce unique value. If two phases read the same inputs and produce overlapping outputs, merge them.

### 9. The process is the bottleneck, not the technique (harness)

12 rounds, 3 delivered real perf gain. The failing rounds had correct techniques (LICM, recursive inlining) applied to wrong assumptions about the code. The process of deciding what to do was the bottleneck.

**Rule**: invest in the workflow (diagnostics, source reading, knowledge base, architecture audit) before investing in more optimization rounds.

---

## General Principles

1. **Multiple regressions = architecture problem**, not implementation bug
2. **Two consecutive failures on same category = ceiling** — research alternatives
3. **V8's architectural choices have been correct every time** — default to V8 when unsure
4. **Correctness first** — wrong-but-fast poisons all future comparisons
5. **Diagnostic tools over guessing** — Diagnose(), Validate(), Print(), ARM64 disasm
6. **Profile before optimizing** — pprof for Go code, ARM64 disasm for JIT code
7. **Never stack on unverified code** — all tests pass before adding next pass
8. **Sunk cost is not a reason to keep broken code**

---

## How This Document Is Used

ANALYZE reads this every round. Before choosing a target:
- "Am I about to repeat a known detour?"
- "Does this match a ceiling pattern?"
- "Am I reading the code or just guessing?"
