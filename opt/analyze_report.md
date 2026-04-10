# Analyze Report — Round 24 (2026-04-10)

## Architecture Audit

**Full audit performed** (rounds_since_arch_audit=3 at entry, ≥2 threshold).

Findings (deltas from Round 21 audit):
- **File sizes unchanged** for the three flagged files: `emit_dispatch.go` 971, `graph_builder.go` 955, `tier1_table.go` 829. R22–R23 did not touch them. Still CRITICAL for any plan that touches dispatch, graph builder, or Tier 1 table paths.
- **New watchlist entry**: `tier1_arith.go` at 728 lines. Round 24's plan adds int-spec templates here — will push toward 800. Flagged for split decision in R26 audit if it crosses 800.
- **Pipeline unchanged**: `BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit`. No new passes added in R22–R23.
- **Regalloc carry infrastructure is stable**: LICM-invariant FPR carry (R9), loop-bound GPR carry, and tight-body phi pre-allocation (R7) coexist cleanly. No churn in `regalloc.go` in R22–R23.
- **New constraint documented**: Tier 1 has no forward type tracking. Every `emitBaselineArith` pays ~10 insns of dispatch, every `emitBaselineEQ` pays ~35 insns. This surfaced from the R24 ackermann diagnostic and had never been written down. Added to constraints.md under Tier Constraints.
- **Tech debt markers**: 1 (unchanged).
- **Test coverage**: 88% (up from 86% at R19). 27 files without test files (same as R21).

Counter: reset `rounds_since_arch_audit` → 0.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| `recursive_call` | fib (0.116s), ackermann (0.589s), mutual_recursion (0.219s), fib_recursive (n/a), method_dispatch (0.096s) | **~1.02s** | **YES** (ceiling=2, R4+R11) |
| `tier2_float_loop` | nbody (0.212s), matmul (0.097s), spectral_norm (0.037s), mandelbrot (0.011s), math_intensive (n/a) | ~0.36s | 1 failure (R23) — 1 away from ceiling |
| `field_access` | sieve (0.074s), table_array (n/a), fannkuch (0.028s) | ~0.10s | 1 failure (R19) |
| `allocation_heavy` | binary_trees (JIT 2.25s > VM 1.58s ⚠ regression), object_creation (JIT 0.76s > VM 0.64s ⚠ regression) | wall-time ~+0.8s regression | 0 failures |
| `gofunction_overhead` | method_dispatch (0.10s), coroutine_bench (n/a) | ~0.10s | 0 failures |
| `tier1_dispatch` (**NEW**) | ack, fib, mutual_recursion, fibonacci_iterative, sum_primes, sieve (int-counter loops) | affects all int-heavy hot paths | 0 failures |

## Blocked Categories

- `recursive_call` (ceiling reached R4+R11)

## Active Initiatives

- `tier2-float-loops` (paused this round — see Initiative Retrospective)
- `recursive-tier2-unlock` (paused — blocked by recursive_call ceiling, Phase 6 gated on Tier 1 prerequisites)

## Initiative Retrospective (tier2-float-loops)

**Rule trigger check**: last 4 rounds on this initiative — R20 improved, R21 improved, R22 improved, R23 no_change. That's **1 no_change in 4 rounds**, so the "≥2 no_change" exhaustion rule does NOT formally trigger.

**However**, the R23 review noted the same pattern with stronger framing: "tier2_float_loop has delivered 12 phases of productive work and is approaching diminishing returns. Round 23's `no_change` on 'remove predicted branches' is a signal that the M4 superscalar floor has been reached at the IPC level." R23 recommended a pivot the next time the user engaged at strategy level; the user gave no strategic guidance, so the harness is deciding.

**Decision**: **pause, do not close**. Reasons:
1. The initiative has NOT delivered two consecutive no_changes, so the hard rule does not require closing.
2. Long-term phases (unboxed float SSA; loop unrolling) are still valid but require multi-round architectural work — not a single-round target.
3. Near-term items in "Next Step" (Phase 6 range analysis for float loops) remain on the board and can be resumed later.
4. `category_failures[tier2_float_loop]=1` — one more no_change hits the ceiling. Continuing to grind on the same category is high-risk.

**What we do instead**: pivot to a **different category** (`tier1_dispatch`, new) that (a) has concrete diagnostic motivation from R24's ack probe, (b) addresses multiple benchmarks (ack, fib, mutual_recursion, fibonacci_iterative, sum_primes), and (c) is off the float-loop treadmill.

## Selected Target

- **Category**: `tier1_dispatch` (**new canonical category** — added to the taxonomy this round)
- **Initiative**: standalone (may spawn `tier1-int-specialization` initiative if this round improves)
- **Reason**: R23's pivot signal + R24 diagnostic shows Tier 1 type dispatch is 40–70% of ack's hot-path overhead, not GetGlobal gen check (which is 2.8%). Fresh category with no ceiling.
- **Benchmarks (primary)**: ackermann (0.595s → target 0.52–0.56s), fib (0.140s → 0.125–0.135s), mutual_recursion (0.224s → 0.19–0.21s). Secondary: fibonacci_iterative (0.277s), sum_primes (0.004s).

## Architectural Insight

The design decision that causes this gap: **Tier 1 is a stateless 1:1 bytecode→ARM64 template compiler with no inter-op type information**. V8/JSC Baseline (Sparkplug / LLInt) have the same model but use **handler-local ICs** with runtime feedback, or fall through to a higher tier. GScript already has Tier 2 for feedback-specialized IR, but Tier 2 is architecturally wrong for recursive integer code (R11: Tier 2 BLR is 15–20ns vs Tier 1's 10ns). That leaves ack and its cousins running the polymorphic dispatch path *forever*.

The fix is to push a **sliver of type specialization into Tier 1 itself** — a simple forward-tracking pass that marks slots as KnownInt after a function-entry guard, and switches arithmetic/compare templates to int-only emission when the analysis is satisfied. This is *not* Tier 1.5 or a new tier; it's an in-place template choice driven by a trivial bytecode walk. It occupies exactly the semantic space between "generic Tier 1 template" and "fully-specialized Tier 2 SSA," where recursive int code belongs.

V8 analogue: Sparkplug's `BaselineAssembler` stays generic, but the Ignition→Sparkplug tier transition doesn't do this because V8 has Maglev / TurboFan for that case. GScript can't lean on higher tiers for recursion, so it has to put specialization earlier in the pipeline.

## Prior Art Research

### Web Search Findings

Research sub-agent delivered findings in `opt/knowledge/global-cache-stable-opt.md` — but the target pivoted after the ackermann diagnostic (GetGlobal is 2.8% of the body, not the bottleneck). That knowledge file is still useful if we revisit global-cache work later, but is not the driver this round.

### Reference Source Findings

Not pursued this round — V8 Sparkplug does not do what we're proposing (it relies on higher tiers), so the cross-engine comparison wouldn't be productive for a fast round. If this approach improves benchmarks, R25 can mine LuaJIT's interpreter specialization patterns.

### Knowledge Base Update

Will add `opt/knowledge/tier1-int-spec.md` in IMPLEMENT phase documenting the emit pattern and the KnownInt tracking algorithm, after we verify the approach works.

## Source Code Findings

### Files Read (for this plan)

- `internal/methodjit/tier1_arith.go:156–221` — `emitBaselineArith` generic template (ADD/SUB/MUL). Dispatch = LSR+MOV+CMP+BCond twice (10 insns), then int path with SBFX+op+overflow-check+box (12 insns).
- `internal/methodjit/tier1_arith.go:430–512` — `emitBaselineEQ` full polymorphic compare (raw-bit-eq fast path, float fallback, type dispatch). ~35 insns total.
- `internal/methodjit/tier1_arith.go:36–56` — `loadSlot`/`storeSlot` pin R(0) to X22 (Round 21).
- `internal/methodjit/tier1_table.go:24–77` — `emitBaselineGetGlobal`: 12-insn fast path, confirmed as non-bottleneck (2.8% of ack).
- `internal/methodjit/tier1_compile.go:165` — Tier 1 op dispatch in the template emission loop. This is where we'd gate specialized vs generic templates.
- `internal/methodjit/tier1_compile.go:300–311` — existing per-proto scan pattern (bytecode walk producing `hasFieldOps`, `hasGetGlobal`). Good template for the new KnownInt analysis.
- `internal/methodjit/regalloc.go` (read for audit, not this plan) — no changes needed; this is Tier 2 only.
- `internal/methodjit/emit_call.go:40–89` — `emitGuardType` is Tier 2 only; the Tier 1 parameter guards will need similar logic in a new helper.

### Diagnostic Data

R24 diagnostic sub-agent produced `/tmp/gscript_ack_tier1.bin` + `/tmp/gscript_ack_tier1.disasm`. Key numbers from actual ARM64 disassembly:

- **ack body: 846 instructions** (pc=0 start 0x90 → RET 0xec4).
- **140 LDR + 176 STR + 15 LDP + 9 STP = 340 memory ops / 846 body insns = 40%** of the hot path is NaN-boxed slot traffic.
- **GETGLOBAL sequences are 12 insns each**, 2 sites per hot recursive call → 24 insns of gen-check-and-load. That is **only 2.8%** of the body.
- **EQ (m==0, n==0) and SUB (m-1, n-1) are the real cost**: each EQ ~35 insns, each SUB ~22 insns of full int/float dispatch. Four of these per recursive call = ~114 insns = ~13% of the body.
- Three CALL sites in the body, each with a `blr x2` + `bl 0x80` post-call re-pin of X22/X21 (re-load R(0) and closure cache after callee returns).

### Actual Bottleneck (data-backed)

The top three overheads in ack's Tier 1 body:
1. **Type dispatch in EQ/SUB/ADD templates: ~114 insns/call (13%)** — addressable by int-specialized templates.
2. **NaN-box slot-file memory traffic: ~340 insns/call (40%)** — partially addressable by pinning more hot params to callee-saved registers (not this round).
3. **BLR call overhead + post-call re-pin: ~30 insns × 3 calls (11%)** — architectural, `recursive_call` ceiling, not addressable this round.

Round 24 targets #1 directly.

## Plan Summary

**What**: Add a simple forward KnownInt tracking pass to Tier 1 compilation (Task 1), then emit int-specialized arithmetic/compare templates with function-entry parameter guards (Task 2). The analysis marks parameter slots as KnownInt after an entry guard (deopt-on-non-int), then forward-propagates through arithmetic and compare writes. Each arith/compare op checks the KnownInt state of both operands (or recognizes integer constants) and picks between the generic and int-spec template. Generic template remains the default; int-spec is opt-in when analysis permits.

**Expected impact** (halved for ARM64 superscalar): ackermann 0.595s → 0.52–0.56s (-6 to -12%), fib 0.140s → 0.125–0.135s (-4 to -10%), mutual_recursion 0.224s → 0.19–0.21s (-6 to -15%). Integer-loop benchmarks (fibonacci_iterative, sum_primes) may see secondary gains. Zero regressions expected on float-heavy code (specialization is opt-in).

**Key risk**: parameter guard semantics must match existing Tier 1 deopt handlers; a mistake here causes correctness regressions on mixed int/float calls. Mitigated by Task 1 being a pure analysis task that writes findings to `opt/knowledge/tier1-int-spec.md` before Task 2 consumes them — conforms to the R23 review's conceptual-complexity split rule.

## Mandatory pre-plan checklist

- [x] If Diagnose() or arch_check found broken tooling / pipeline mismatches → is there a fix Task? — **No broken tooling found this round.**
- [x] If constraints.md flags files >800 lines that this plan touches → is there a split Task 0? — **Plan touches `tier1_arith.go` (728, below limit) and `tier1_compile.go` (not flagged). NO split needed.**
- [x] If known-issues.md has items in this plan's category → are quick-fix items included? — **The ackermann +137% regression is in known-issues.md under `tier2_call_overhead`; the diagnostic showed it's actually dispatch-dominated, not GetGlobal-dominated. Quick-fix (GetGlobal skip) was investigated and rejected as low-ROI. Dispatch fix IS the quick fix.**
