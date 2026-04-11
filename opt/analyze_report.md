# Analyze Report — Round 32

> Date: 2026-04-11
> Cycle: 2026-04-11-loop-scalar-promote-nbody
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (Phase 13: Scalar Promotion)

## Architecture Audit (Step 0 — quick read)

`rounds_since_arch_audit = 1` in `opt/state.json` → quick read, no full audit.

Quick scan: no new ≥1000-line file (R31 SimplifyPhisPass landed at 226 LOC).
`pass_licm.go` at 594 lines still the largest pass file; `loops.go` at 429 lines;
both well under the 800-line warn line. Tier 2 pipeline composition is clean
(`pipeline.go:280-351`). No new `docs-internal/architecture/constraints.md` entries
required this round.

## User Priority Honored

`opt/user_priority.md` (updated 2026-04-11 19:20) directs this round to:

1. **tier2_float_loop (PRIMARY)** — initiative un-paused, nbody is #1 target.
2. **Hard constraint**: do NOT use `profileTier2Func` as evidence. Either instrument
   `compileTier2()` end-to-end or read ARM64 disasm from a real run.

Honored: the diagnostic in Step 4 below was produced by calling `RunTier2Pipeline`
directly on `advanceProto` with Tier 1 feedback populated from 11 TieringManager
warm-up runs. No `profileTier2Func` involvement. The output binary was written to
`/tmp/gscript_nbody_advance_r32.bin` and disassembled with Capstone — this is the
production code path.

## Gap Classification (Step 1)

| Benchmark | JIT | LuaJIT | Ratio | Category | Ceiling status |
|-----------|-----|--------|-------|----------|----------------|
| fib_recursive | 14.120s | N/A | — | tier1_dispatch | **3 failures — blocked** |
| nbody | 0.248s | 0.033s | **7.6×** | tier2_float_loop | 1 failure, open |
| matmul | 0.119s | 0.022s | 5.7× | tier2_float_loop | 1 failure, open |
| spectral_norm | 0.045s | 0.007s | 5.6× | tier2_float_loop | 1 failure, open |
| fib | 1.410s | 0.026s | 54× | tier1_dispatch | **blocked** |
| ackermann | 0.267s | 0.006s | 44× | tier1_dispatch | **blocked** |
| sieve | 0.084s | 0.011s | 7.6× | field_access | **2 failures — skip 3 rounds** |
| mandelbrot | 0.062s | 0.058s | 1.07× | tier2_float_loop | ~parity |
| fannkuch | 0.049s | 0.020s | 2.4× | tier2_float_loop | |

### Blocked Categories

- `tier1_dispatch` — 3 category failures (R29–R30 transient OP_GETGLOBAL,
  R24–R28 self-call micro-opts). Blocked until ceiling decay.
- `field_access` — 2 failures (R19 table-kind-specialize, R31 Braun phi cleanup).
  User priority: "skip 3 rounds, retry with fresh approach; MUST NOT use
  profileTier2Func as evidence."

### Active Initiatives

- `opt/initiatives/tier2-float-loops.md` — **un-paused by user for R32**. Backlog:
  Phase 6 (range analysis for float), Phase 13 (new, this round: scalar promotion),
  long-term (unboxed float SSA, loop unrolling). R21–R22 were the most recent wins;
  R23 infrastructure-only (M4 absorbed the guard savings).
- `opt/initiatives/tier1-call-overhead.md` — paused (category blocked).
- `opt/initiatives/recursive-tier2-unlock.md` — paused (category blocked).

## Selected Target

**nbody's `advance()` j-loop — loop scalar promotion of `bi.vx/vy/vz`.**

Matches user priority #1. Fresh approach: no round has ever implemented
cross-iteration scalar replacement in GScript. R18 and R23 hit the wall at LICM
GetField hoisting (cannot help because SetField is present) and LICM guard hoisting
(M4 absorbed into predicted branches). This is a structurally different transform:
it touches memory traffic, not branches.

## Architectural Insight

GScript's optimizer has **in-block** memory elision (R16 block-local GetField CSE)
and **pre-header hoisting** (R8–R23 LICM, which requires pure loads), but no
**cross-iteration** memory elision. The missing piece is the standard LLVM
mem2reg-for-loops trick: when a loop-invariant object has a read-modify-write pattern
on a field, lift the field into a phi in the loop header. That's exactly what
`bi.vx = bi.vx - dx*bj.mass*mag` is in nbody's j-loop.

R18 already documented this wall:

> Phase 9 (round 18): LICM GetField hoisting works for loops without same-object
> writes. nbody's inner loop has SetField on same objects as GetField targets,
> blocking hoisting.

R23 confirmed instruction-count savings don't move wall-time when the savings are on
branches (M4 absorbs them). The lesson: to beat LuaJIT on nbody we need to cut
**memory traffic**, not branches or guards. Scalar promotion is precisely a
memory-traffic transform.

## Prior Art Research (Step 2)

Full algorithm pseudocode + citations written to
`opt/knowledge/loop-scalar-promotion.md` (new file, ~290 lines).

Key findings:

- **LLVM**: `promoteLoopAccessesToScalars` in `lib/Transforms/Scalar/LICM.cpp`
  (~line 1800, LLVM 17). Canonical reference. Structure: pre-header load, header phi,
  in-loop use replacement, in-loop store removal, exit-block store materialization.
  Uses MemorySSA for alias queries; GScript equivalent is the `setFields` map.
- **V8 TurboFan**: does NOT do this at `LoadElimination`
  (`src/compiler/load-elimination.cc:1363` `ComputeLoopState` *kills* fields with
  loop stores, doesn't promote). TurboFan relies on `EscapeAnalysis` for
  non-escaping allocations. nbody's `bi` escapes (global `bodies` element), so V8's
  model doesn't apply — GScript needs the LLVM-style transform.
- **LuaJIT**: `src/lj_opt_loop.c:77` trace re-emission achieves equivalent forwarding
  implicitly. "Load/store forwarding works across loop iterations. `self.idx =
  self.idx + 1` may become a forwarded loop-recurrence after inlining."
- **Academic**: standard mem2reg adapted for loops; Aho/Sethi/Ullman Dragon Book §9.4.

GScript infrastructure already provides: pre-header blocks (`pass_licm.go:337-380`),
`invariant` map (`pass_licm.go:141`), `setFields` map (`pass_licm.go:173`),
`hasLoopCall` flag (`pass_licm.go:175`), `loopPhis` tracking (`loops.go:21`),
`replaceAllUses` helper (`pass_load_elim.go:118`), `TypeFloat` phi type
(`ir.go:112`), `CarryPreheaderInvariants` regalloc flag (`ir.go:55`), exit-phi
store patterns (`emit_compile.go:40`). Nothing new; the pass is pure composition.

## Source Code Findings (Step 3)

Read the files the new pass will interact with:

- `pipeline.go:280-351` — `RunTier2Pipeline` wiring point. New pass goes after
  `LICMPass` at line 345. Also add to `NewTier2Pipeline` at line 357-375.
- `pass_licm.go:141-210` — `invariant` map, `setFields` map, `hasLoopCall` flag.
  These are the three inputs the new pass needs.
- `pass_licm.go:337-380` — pre-header creation; guaranteed to exist when we run.
- `loops.go` — `computeLoopInfo`, `computeDominators`, `computeLoopPreheaders`,
  `collectPreheaderInvariants`. Reusable.
- `pass_load_elim.go:102-129` — `replaceAllUses` helper. Reuse for in-loop GetField
  substitution.
- `ir.go` — `Instr{Op, Args, Aux, ID}`, `OpGetField`, `OpSetField`, `OpPhi`,
  `TypeFloat`.

No new helper files needed.

## Micro Diagnostics (Step 4)

Authoritative diagnostic: `opt/diagnostics/r32-nbody-loop-carried.md` (186 lines).

**How the data was produced**:
`internal/methodjit/r32_nbody_loop_carried_test.go::TestR32_NbodyLoopCarried` runs
TieringManager on `advance()` 11 times (populating Tier 1 feedback), then calls
`RunTier2Pipeline(fn, advanceProto)` → `AllocateRegisters` → `Compile`, writes the
binary to `/tmp/gscript_nbody_advance_r32.bin` (5464 bytes, 1366 insns), and
disassembles with Capstone. This is the real Tier 2 production path — **no
`profileTier2Func`** (honors user priority hard constraint).

**IR-level findings**:

- `advance()`: 20 `OpGetField`, 9 `OpSetField`, 35 float arith ops, 21 `OpGuardType`,
  123 total instrs.
- j-loop body (block B2) has **6 loop-carried `(obj, field)` pairs**:
  - `bi.vx`, `bi.vy`, `bi.vz` — **promotable** (`bi` invariant across j-loop).
  - `bj.vx`, `bj.vy`, `bj.vz` — **not promotable** (`bj = bodies[j]`).
- i-loop body B6 has 3 additional pairs `b.x/y/z` — promotable at the i-loop level
  but only 5 outer iterations, lower ROI.

**ARM64 disasm findings** (j-loop body):

| Category | Insns | % |
|----------|------:|--:|
| Memory (LDR/STR) | 174 | **33.1 %** |
| MOV/MOVK | 119 | 22.6 % |
| Box/unbox (SBFX/MOVK#FFFE) | 82 | 15.6 % |
| Branches (B.cc/TBNZ/CBZ) | 78 | 14.8 % |
| Guard checks (CMP/CCMP) | 40 | 7.6 % |
| **Float compute (FADD/FSUB/FMUL/FMOV)** | **29** | **5.5 %** |
| Total | **526** | 100 % |

The j-loop body is **memory-bandwidth dominated**, not compute-bound. Float compute
is 1/6 of memory traffic. Any instruction-count savings on branches or guards are
absorbed by M4 superscalar (R23 lesson). Savings on LDR/STR *do* move wall-time
because the M4 load/store queue and D-cache port count are finite.

Promoting `bi.vx/vy/vz` removes 3 × (GetField ≈ 14 insns) + 3 × (SetField ≈ 12 insns)
per j-iteration ≈ 78 insns, of which ~36 are LDR/STR. That's 20 % of the memory
category and 14.8 % of the loop body.

All 5 cross-checks PASS.

**Wall-time estimate**: halve for superscalar (R24 rule) → ~7 %; adjust for
partially-overlapped load-use latency → ~3.5 %; add back small memory-queue headroom
→ **≈4 % nbody wall-time = 0.248s → 0.238s**.

## Plan Summary

New pass `LoopScalarPromotionPass` wired after `LICMPass`. Per `(obj, field)` pair
meeting the gate (obj invariant, ≥1 GetField + ≥1 SetField in loop body, no OpCall,
no dynamic-key kill, single-exit loop, single-SetField per iteration): insert
pre-header load, insert header phi, replace in-loop GetFields with phi, remove
in-loop SetFields, insert exit-block store. Single Coder task. Budget: 3 files,
350 LOC, 1 functional commit. Full algorithm in `opt/knowledge/loop-scalar-promotion.md`.
Target: nbody −4 %. Failure signal if > −2 %.

Plan file: `opt/current_plan.md`.

## Artifacts Produced This Phase

- `opt/current_plan.md` (new) — R32 plan, calibrated target, failure signals.
- `opt/knowledge/loop-scalar-promotion.md` (new, ~290 lines) — algorithm pseudocode,
  LLVM/V8/LuaJIT citations, GScript infra reuse table.
- `opt/diagnostics/r32-nbody-loop-carried.md` (new, 186 lines) — production-path
  diagnostic; 6 loop-carried pairs identified; 33.1 % memory vs 5.5 % compute; all
  cross-checks PASS.
- `internal/methodjit/r32_nbody_loop_carried_test.go` (new, diagnostic harness only,
  not production code).
- `opt/analyze_report.md` (this file).
- `docs/draft.md` (pending — Step 7).

## Not Done This Round (deferred)

- Multi-SetField-per-iteration handling (out of R32 scope; nbody doesn't need it).
- Multi-exit loops (out of R32 scope; nbody's j-loop is single-exit).
- Integer/bool field promotion (nbody is float-only; cost-benefit lower).
- i-loop `b.x/y/z` promotion — the new pass will apply automatically if the gate
  passes; no special handling, not listed as primary target.
