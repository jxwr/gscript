# R26 ANALYZE Report

- **Date**: 2026-04-11
- **Cycle ID**: 2026-04-11-tier1-selfcall-overhead
- **Target**: Tier 1 self-call fast path overhead reduction (ackermann)
- **Category**: `tier1_dispatch` (0 failures)
- **Initiative**: `opt/initiatives/tier1-call-overhead.md` (new, opened this round)
- **Baseline**: `cec1e151` post-R25

## Step 0 — Architecture audit (FULL, rounds_since_arch_audit was 2)

Executed via `bash scripts/arch_check.sh` + source scan.

**File size budget** (limit 1000, warning 800):
- `internal/methodjit/emit_dispatch.go` — **971** (warning, no touch needed this round)
- `internal/methodjit/graph_builder.go` — 955 (warning, no touch needed)
- `internal/methodjit/tier1_arith.go` — **903** (warning, **this round won't touch but R27+ Tier 1 work will force a split**)
- `internal/methodjit/emit_table_array.go` — 696
- `internal/methodjit/emit_compile.go` — 640
- `internal/methodjit/pass_licm.go` — 594
- `internal/methodjit/tier1_call.go` — **554** (this round's target file, plenty of headroom)
- `internal/methodjit/tier1_control.go` — 251
- All pass files under 600

**No files over 1000**. Three files in the 800-1000 warning band — all in `internal/methodjit`. R27 planning needs to factor a split of `tier1_arith.go` into the backlog if Tier 1 work continues (it will per the new initiative).

**Pipeline audit** (per `docs-internal/architecture/constraints.md`):
- Current order `BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit` — unchanged, healthy.
- Tier 1 has no IR / no passes (it's template-based). The new initiative stays entirely in template emission — no pipeline risk.

**Test coverage audit**: diff `internal/methodjit/*.go` vs `*_test.go` — `tier1_call.go` has no dedicated test file; tests live in integration tests only. R26 Task 0 fixes this by adding `tier1_ack_dump_test.go`.

**Constraint deltas written back to `docs-internal/architecture/constraints.md`**:
- Added: "Tier 1 template files in the 800-1000 warning band (`tier1_arith.go=903`, `emit_dispatch.go=971`, `graph_builder.go=955`). Split required on the next touch that crosses 1000."
- Added: "Tier 1 JIT is slower than the interpreter on 6 CALL-heavy benchmarks (R26 measurement). The baseline template pays ~60 insns/call vs VM's inlined `continue`. New initiative `tier1-call-overhead.md` opened."
- Added: "`tier1_control.go:224` RETURN reads `ctx.CallMode` and branches on it — constrains any scheme that removes the CallMode write on Tier 1 self-call setup."

## Step 1 — Gap classification and target selection

From `benchmarks/data/latest.json` (median-of-5, R25 baseline):

| Bench | JIT | LuaJIT | Gap | VM | JIT/VM | Category |
|---|---|---|---|---|---|---|
| ackermann | 0.563 | 0.006 | **94×** | 0.294 | **1.91×** | recursive_call BLOCKED → but Tier 1 work belongs to `tier1_dispatch` |
| fib | 0.135 | 0.027 | 5.0× | 1.680 | 0.08× | tier1_dispatch |
| sieve | 0.086 | 0.011 | 7.8× | 0.247 | 0.35× | tier2_float_loop |
| matmul | 0.120 | 0.022 | 5.5× | 1.038 | 0.12× | field_access |
| nbody | 0.256 | 0.035 | 7.3× | 1.936 | 0.13× | tier2_float_loop |
| spectral_norm | 0.045 | 0.007 | 6.4× | 1.003 | 0.04× | tier2_float_loop |
| mandelbrot | 0.061 | 0.058 | 1.05× | 1.404 | 0.04× | — (near parity) |
| mutual_recursion | 0.237 | 0.005 | 47× | 0.205 | **1.16×** | tier1_dispatch |
| method_dispatch | 0.104 | 0.000 | large | 0.088 | **1.18×** | tier1_dispatch |
| binary_trees | 2.323 | N/A | N/A | 1.633 | **1.42×** | tier1_dispatch |
| coroutine_bench | 18.438 | N/A | N/A | 15.247 | **1.21×** | tier1_dispatch |
| object_creation | 0.765 | N/A | N/A | 0.639 | **1.20×** | tier1_dispatch |

**Category failures state** (`opt/state.json`):
- `recursive_call`: 2 — **BLOCKED**
- `tier2_float_loop`: 1 — high risk
- `field_access`: 1 — high risk
- `tier1_dispatch`: **0** — SAFE
- `other`: 0

**The new signal nobody else framed**: JIT-slower-than-VM on 6 benchmarks. That's a clustering fact that points at one bottleneck (CALL/RETURN emission) rather than six separate ones. And it lives in the only non-blocked, zero-failure category. That's the pick.

**Rejected alternatives**:
- `matmul` / `tableVerified` cross-loop persistence (my first choice earlier this session): field_access category at ceiling=1, and R22 showed that cross-block shape propagation produced no_change because M4 hides branches. A second no_change would BLOCK field_access. Also the matmul preamble savings overlap with work R22 already did.
- `ackermann` stable-global gen-check removal: **REFUTED** by a R26 diagnostic — the supposed 67M GetGlobals figure in the knowledge base was from a different benchmark config; actual ackermann is 5.15M calls, saving gen-checks is <2% of ackermann's time. That diagnostic save validated the "observation beats reasoning" rule again.
- Tier 2 recursive-call unlock: category BLOCKED (2 failures). Off-limits.

## Step 2 — External research (bounded, ≤5 tool calls)

Not invoked this round. The target is internal (our Tier 1 template emission), not algorithmic. Prior art on LuaJIT's call mechanism is already captured in `opt/knowledge/tier1-call-overhead.md` Open Question 3 and queued as Item 5 of the initiative for a dedicated research round. Per the "constraints are cost, not block" rule, skipping external research here is explicit — the raw machine-code disasm is higher-information than any blog post would be.

## Step 3 — Source code reading

- `internal/methodjit/tier1_call.go:95-488` (full `emitBaselineNativeCall`)
- `internal/methodjit/tier1_call.go:493-554` (direct_entry, self_call_entry, direct_epilogue)
- `internal/methodjit/tier1_control.go:199-227` (emitBaselineReturn — **critical**: line 224 `LDR ctx.CallMode` constrains the plan)
- `internal/vm/vm.go:1136-1237` (OP_CALL VMClosure inline in dispatch loop — the comparison baseline)

## Step 4 — Micro diagnostics (diagnostic sub-agent)

Ran the diagnostic sub-agent with a rebuild + disasm of ackermann's Tier 1 output.

**Measured caller-side fast-self-call path in `/tmp/gscript_ack_tier1.bin` offsets 608→1176**:
- Total: ~60 insns caller-side + 8 insns callee entry/epilogue = ~68 per call
- 5 branches, 14 memory stores to the ExecContext cache line, 6 stack save/restore ops
- LuaJIT gap on ackermann: 94×. Interpreter lives at ~50-80 Go-compiled insns per call with no BL — that's the structural reason the JIT is 1.91× slower.

**Removable (this round)**: 10 insns per self-call.
**Load-bearing (deferred)**: 12 insns (ctx.Regs writes, CallMode writes, frame save/restore).

**Cross-check with `object_creation`**: same disease, worse — cross-function calls take the full ~100-insn normal path, not the 60-insn self-call shortcut. Same fixes, one extra step to apply to the normal path, deferred to R27.

**Halved superscalar prediction** (Rule 5): 10 insns × 5.15M calls × 0.5 / (3 IPC × 4 GHz) ≈ 0.0021s raw → 0.03-0.06s wall-time after accounting for serialized LDR/ADD/STR chains to the same cache line. Target: ackermann 0.563 → **0.510 s (−9.4%)**, floor at 0.545s (−3%).

## Step 5 — Plan

Written to `opt/current_plan.md`. Three tasks:
- Task 0: commit the diagnostic fixture test (`tier1_ack_dump_test.go`) so we can measure insn-count deltas across rounds
- Task 1: SP-floor prologue guard replaces NativeCallDepth counter on self-call path (−9 insns/call)
- Task 2: Drop dead `ctx.Constants` STR in shared restore (−1 insn/call)
- Task 3: Benchmark verify with stop-condition

**Budget**: 3 hours / ≤120 LOC / 3 commits.

## Step 6 — Meta / self-evolution note

This round's ANALYZE almost planned the wrong target twice:
1. First it was talked into `ackermann stable-global` by a stale knowledge-base figure; the diagnostic sub-agent refuted the hypothesis with actual numbers.
2. Second it was talked into `matmul tableVerified`; reading `opt/state.json` revealed R22 had already done cross-block shape propagation with outcome `no_change` (M4 hides branches). That would have cost field_access's second category failure (category block).

Both saves came from **reading the prior data before committing to a plan**. The workflow already has this rule (Lesson 10, Hard-Won Rule 1). What's worth noting is that the rule protected the round when applied DURING planning, not just during implementation. No harness change needed — the rule is working. But the analyze report should continue to record "rejected alternatives with reasons" because it forces the agent to justify the choice against data rather than against speculation.

**Knowledge base correction queued**: the R20 `getglobal-native-licm` note mentions "67M GetGlobals in ackermann" which is wrong — actual ackermann is 5.15M total calls. Add a correction note to `opt/knowledge/global-cache-stable-opt.md` in VERIFY.
