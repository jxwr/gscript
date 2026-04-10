# R25 Analyze Report — 2026-04-11-measurement-repair

## Architecture Audit

Quick read (off-round; `rounds_since_arch_audit=0`). No new findings since R24.
`constraints.md` current. `arch_check.sh` flags four files ≥800 lines:

- `emit_dispatch.go` 971 (unchanged, still 29 lines from cap)
- `graph_builder.go` 955 (unchanged)
- `tier1_arith.go` 895 (+167 from R24's int-spec templates — now at warning)
- `tier1_table.go` 829 (unchanged)

R25 touches `tier1_arith.go` only to change the `emitIntSpecDeopt` signature
(~5-line diff). Does not push the file past the limit. No split Task 0 required.
R26 must include a `tier1_arith.go` split if it adds any new templates.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| measurement | ALL 22 | unknown — data is noisy | **YES — root cause of this round** |
| recursive_call | ackermann, mutual_recursion, fib_recursive | 0.79s gap | ceiling (category_failures=2) |
| tier2_float_loop | nbody, spectral, matmul, fannkuch | 0.34s gap | no (1 failure) |
| field_access | sieve, table_* | 0.09s gap | no (1 failure) |
| tier1_dispatch | ack, fib, mutual_recursion | 0.75s gap | no (R24 first attempt → improved) |
| allocation_heavy | binary_trees, object_creation | 1.49s regression | unbounded (architectural) |

## Blocked Categories

- `recursive_call` — ceiling (see `constraints.md`)
- `allocation_heavy` — architectural (NEWTABLE exit-resume; needs escape analysis)

## Active Initiatives

- `opt/initiatives/tier2-float-loops.md` — 13 rounds, R23 was no_change. Not
  exhausted (R20-22 improved), but R25 deliberately does not pick it up because
  the measurement tool being broken would make any perf claim untrustable.
- `opt/initiatives/recursive-tier2-unlock.md` — closed at R11 (ceiling).

No new initiative created this round.

## Selected Target

- **Category**: `other` (harness/tooling repair)
- **Initiative**: standalone
- **Reason**: diagnostic revealed R24's reported regressions are single-shot
  measurement noise. Per "Wrong data → stop & fix tool" rule — all other work
  is blocked until measurements are trustworthy. See `## Source Code Findings →
  Diagnostic Data`.
- **Benchmarks affected**: all 22 (indirectly — the round produces a trusted
  re-baseline for future rounds to use).

## Initiative Retrospective

Not applicable — R25 does not continue an active initiative. A reminder from the
retrospective rule (R23): `tier2_float_loop` has 13 rounds; 3 of its last 4 were
improvements (R20, R21, R22) with R23 being no_change. Does not trigger the
≥2-no_change-in-4 exhaustion rule, but the running total is worth watching. R26
will reassess after R25's re-baseline exposes where the real gaps now sit.

## Architectural Insight

This is not a compiler bottleneck — it's a **process** bottleneck. The
self-evolving workflow meta-principle applies directly: the harness observed its
own failure (a confident regression claim that was noise), diagnosed it (single-
shot measurement), and must now change its own tooling. That is the harness
evolving. No compiler-level architectural reasoning needed for R25; architectural
work resumes at R26 with trusted data.

The secondary correctness task (overflow deopt PC resume) is a micro-level
architectural point: GScript's Tier 1 deopt model restarts execution at pc=0,
while V8 and LuaJIT both resume at the exact failing PC. The design gap is
small and local; the fix is bounded.

## Prior Art Research

### Web Search Findings

Research sub-agent report (full text: `opt/knowledge/tier1-int-overflow-handling.md`):

**V8 (Q1):** `SpeculativeNumberAdd` with `kSignedSmall` → `Int32AddWithOverflow`
with a `DeoptimizeIf` on the overflow projection (`simplified-lowering.cc:1834`).
Overflow deopts at the exact IR node; frame state reconstructs the interpreter
at the bytecode offset of the failing op. Never an in-place float fallback mid-
function.

**LuaJIT (Q2):** `IR_ADDOV` side-exits at the guard IR. VM resumes interpretation
at the bytecode PC recorded in the snapshot (`lj_snap.c`). Additionally,
`lj_opt_narrow.c:583` predicts at recording time whether a FORL induction
variable will fit int32; if not, it stays `IRT_NUM` from the start.

**JSC/SpiderMonkey (Q3):** Neither does "int-first, float-sticky per-slot."
Both use per-callsite polymorphic IC stubs — int first, double fallback IC
attached on overflow, per PC. GScript's baseline has no IC infra so this doesn't
transfer directly.

**Overflow fix ranking (Q4):**
1. **D — Correct restart at overflow PC** (Task 3 this round)
2. A — Accumulator loop detection in analyzer
3. C — Disable int-spec if FORLOOP present (safety valve)
4. E — Runtime sticky bitmap (deferred)
5. B — SCVTF in-place float fallback — **DEALBREAKER** (unsound without E or D)

### Reference Source Findings

- V8: `src/compiler/simplified-lowering.cc:1834` (ChangeToInt32OverflowOp)
- LuaJIT: `src/lj_opt_narrow.c:526` (lj_opt_narrow_arith), `:583`
  (lj_opt_narrow_forl), `src/lj_snap.c` (snapshot-based PC resume)
- Benchmark runners (all use median-of-N): LuaJIT `bench/bench.lua`, V8
  `benchmarks/v8.js`, SpiderMonkey `js/src/shell/js.cpp` bench mode

### Knowledge Base Update

- NEW: `opt/knowledge/tier1-int-overflow-handling.md` — Q1-Q4 from research
  sub-agent. Cited for Task 3 design.
- EXISTING: `opt/knowledge/tier1-int-spec.md` — unchanged; the round validates
  its baseline design, only its overflow restart path needs fixing.

## Source Code Findings

### Files Read

- `benchmarks/run_all.sh` (239 lines) — single-shot VM/JIT/LuaJIT loops at
  lines 86, 109, 134. JSON writer at 198-232 parses stdout by pipe field
  splitting — any change to output format must preserve the exact `Time: X.XXXs`
  pattern.
- `internal/methodjit/tier1_int_analysis.go` (339 lines) — eligibility gate +
  forward scan. Correct; the regression scare was not caused by this file.
- `internal/methodjit/tier1_arith.go:739-822` — int-spec templates and
  `emitIntSpecDeopt`. The deopt emitter takes no `pc` argument; Task 3 will
  add one.
- `internal/methodjit/tier1_compile.go:82-97, 129-177` — wiring of int-spec
  into the per-op dispatch. Correct.
- `internal/methodjit/tier1_manager.go:47-61, 176-193` — `DisableIntSpec`
  map + `Execute` / `executeInner` retry loop. Restart is from function entry
  (pc=0), not from overflow PC. Task 3 targets this.
- `internal/methodjit/tier1_ack_dump_test.go` (new R24) — pattern for dumping
  a specific proto's compiled code. Task 3's test will follow this shape.

### Diagnostic Data

**Diagnostic sub-agent report** (budget: 53 tool calls; above the 25-soft but
within acceptable range for a measurement validation task):

**fibonacci_iterative regression status: NOISE**
- 15-run HEAD mean: 0.292s ± 0.008s
- 15-run baseline (df2e2ec) mean: 0.298s ± 0.011s
- Delta: −2.2% (HEAD is slightly faster)
- |Delta| = 0.67σ — within noise
- Verify commit's `0.306s` was a single-shot outlier (~1.8σ high)
- `latest.json` currently shows 0.277s, matching baseline

**mutual_recursion regression status: NOISE**
- 15-run HEAD mean: 0.240s ± 0.003s
- 15-run baseline mean: 0.237s ± 0.012s (baseline has high variance)
- Delta: +1.5% (with cold outlier) / +2.9% (without)
- |Delta| = 0.47σ — within noise

**Mechanism confirmation:**
- fib_iter: int-spec IS enabled; overflow deopt fires exactly once per benchmark
  invocation (on the last iteration of fib_iter(70) where the accumulator
  exceeds int48). `DisableIntSpec` is sticky across 999,999 subsequent calls.
  Recompile cost: microseconds. Not the source of any visible regression.
- mutual_recursion: int-spec IS applied (params are arith-used integers). No
  overflow risk with n=25. 0 deopts.

**Root tool bug:** `benchmarks/run_all.sh` is single-shot per mode per
benchmark. 3-5% CV on M4 + thermal + GC → ~1/3 of "regression" reports on any
benchmark with modest noise are false. Four rounds in 2026-04 likely affected.

**Cross-checks passed:**
- `.bin` mtime matches HEAD ✓
- disasm function = target function ✓ (via tier1_ack_dump_test pattern)
- numbers reproduce ±5% on re-run ✓ (15 runs show stable distribution)
- (Instruction counts skipped — unnecessary once regression proved to be noise)

### Actual Bottleneck (data-backed)

**The bottleneck this round is the measurement tool itself.** The data shows
R24 is a clean win with no real regressions. Fixing the tool unblocks R26+ by
establishing a trusted baseline. Secondary: a correctness hardening in the
overflow deopt path (research-identified latent bug).

## Plan Summary

Three mandatory tasks + one stretch. Task 0: median-of-3 wrapper in
`benchmarks/run_all.sh`. Task 1: re-run with `--runs=5`, copy latest→baseline.
Task 2: document the pitfall in `known-issues.md` and `lessons-learned.md`.
Task 3 (stretch): fix overflow deopt PC resume via interpreter fallback in
`tier1_manager.go`, with a test for single-execution of pre-overflow side
effects. Expected perf: zero wall-time change. Expected value: unblocks every
future round by replacing a noisy one-shot measurement with a stable
median-of-N. Key risk: bash-scripting the median wrapper breaks the JSON
writer; mitigated by preserving the exact `Time: X.XXXs` line format.
