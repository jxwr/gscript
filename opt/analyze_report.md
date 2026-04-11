# ANALYZE Report — R27 (2026-04-11)

## Architecture Audit

Quick read (`rounds_since_arch_audit=1`, full audit due next round).
`scripts/arch_check.sh` flags 4 files >800 lines:

- `emit_dispatch.go` 971 ⚠ (29 lines from cap) — not touched this round
- `graph_builder.go` 955 ⚠ (45 lines from cap) — not touched this round
- `tier1_arith.go` 903 ⚠ (97 lines from cap) — not touched this round; split queued
  for whichever round next modifies Tier 1 arith templates
- `tier1_table.go` 829 ⚠ — not touched this round

R27 target file is `tier1_call.go` (554 lines). No split required. No new
constraint entries needed.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| `recursive_call` | ackermann, fib, fib_recursive, mutual_recursion | 2.68s (ack 0.692s, fib 0.131s, mutual 0.278s) | **Yes (ceiling=2)** |
| `tier1_dispatch` | ackermann, method_dispatch, binary_trees, object_creation | ~1.2s (overlaps with recursive_call on ack) | No (failures=1) |
| `tier2_float_loop` | nbody, matmul, spectral_norm, mandelbrot | 0.46s | No (failures=1) |
| `field_access` | sieve, fannkuch, table_array | 0.19s | No (failures=1) |
| `allocation_heavy` | binary_trees, object_creation | ~1.3s regression | No |

## Blocked Categories

- `recursive_call` — failures=2 (R11 Tier 2 net-negative + R5 tier2 hang/diagnose).
  Any work here must go through `tier1_dispatch` (Tier 1 specialization), not Tier 2.

## Active Initiatives

- `tier1-call-overhead.md` — opened R26. **Item 1a** (drop dead `ctx.Constants` STR
  on self-call restore) is queued as "next round". R26 committed Task 0 fixture
  (878e64a) and aborted Tasks 1-3 on data-premise error (NativeCallDepth = goroutine
  stack budget). Item 1a is architecturally independent of NativeCallDepth and safe.
- `tier2-float-loops.md` — paused (3 consecutive `no_change` R18/R19/R23). Not active.
- `recursive-tier2-unlock.md` — closed (ceiling reached).

**Initiative retrospective check (tier1-call-overhead)**: 1 round so far (R26,
`data-premise-error`). Not at the ≥2-no-change-in-4-rounds threshold. Continuation
is justified: Item 1a is independently valid, surgical, and closes the R26 residual.

## Selected Target

- **Category**: `tier1_dispatch`
- **Initiative**: `tier1-call-overhead.md` Item 1a
- **Reason**: safe close-out of R26. R26 KB doc already identified the dead STR
  via disasm at offsets 1128-1132 in `/tmp/gscript_ack_tier1.bin`. No architectural
  dependency on the failed SP-floor work. Budget-friendly after R26's 82.5M-token
  burn (user R27 directive: "重点降低token消耗，每次只用1个Coder子agent").
- **Benchmarks**: ackermann primary; secondary benefit on any Tier 1 self-recursive
  code (fib, fib_recursive, mutual_recursion).
- **Constraints check**: no file >800 touched. No active ceiling. No broken subsystem.

## Architectural Insight

The Tier 1 CALL emitter currently maintains a single shared "restore" epilogue where
both the normal-call and self-call branches join before writing `ctx.Regs` and
`ctx.Constants` back to the context struct. The architectural observation: the two
branches have different invariants — self-call never clobbers `mRegConsts`/`ctx.Constants`
(by design of `emitSelfCallEntryPrologue`), so the shared join is write-backing state
that was never modified. This is an instance of the broader pattern: *shared epilogues
are dead-insn carriers when the branches they join had asymmetric writes*.

This pattern matters for the larger initiative: Item 3 ("drop `ctx.Regs` STR on both
paths via exit-lazy flush") and Item 4 ("compile two RETURN variants → remove CallMode
write") are the same pattern at a bigger scale. R27 Item 1a is the smallest instance
and the one that's guaranteed safe without structural change — it seeds the pattern
before we scale it.

V8 handles this structurally: each CallKind (direct, bind-trampoline, eval, with-spread)
emits its own specialized epilogue with no shared join. GScript's Tier 1 is a linear
template compiler so we can't have *separate functions* per kind, but we can have
*separate inline branches* per kind, which is what this plan does for one instruction.

## Prior Art Research

### Web Search Findings

**Skipped by design.** R26 review identified Research sub-agent overrun (113 calls,
7.3M tokens) as the #1 token waste vector. The feedback memory "Wrong data → stop
& fix tool" applies to this round's prior art too: the R26 KB (`opt/knowledge/tier1-call-overhead.md`)
already contains the full disasm breakdown with exact offsets for the dead STRs,
produced 36 hours ago. Re-researching the same question would burn tokens for
zero new information.

### Reference Source Findings

Reused from R26 KB:

- `/Users/jxwr/ai/ai_agent_experiment_gscript/gscript/internal/methodjit/tier1_call.go:437-438`
  — the exact two STRs at the shared join
- R26 disasm at offsets 1128-1132 of `/tmp/gscript_ack_tier1.bin` confirmed both
  STRs are emitted unconditionally post-merge
- `internal/methodjit/tier1_call.go:362-372` (self-call setup) — confirms
  `ctx.Constants` is not written during self-call setup
- `internal/methodjit/tier1_call.go:526-541` (`emitSelfCallEntryPrologue`) —
  confirms the self-call callee prologue explicitly skips the
  `LDR X27, ctx.Constants` that normal `emitDirectEntryPrologue` performs

### Knowledge Base Update

No new KB file written. `opt/knowledge/tier1-call-overhead.md` already documents
the dead-STR analysis (lines 2279-2285). Will update it during VERIFY with the
actual wall-time delta so future rounds know whether M4's store buffer hid the
change.

## Source Code Findings

### Files Read

- `internal/methodjit/tier1_call.go` (full file, 554 lines)
- `internal/methodjit/tier1_ack_dump_test.go` (existing insn-count fixture)

### Diagnostic Data

- `TestDumpTier1_AckermannBody` run at HEAD `dcc0dc5`:
  `tier1 code: size=3692 bytes (923 insns), DirectEntryOffset=84`
  **Matches R26 baseline exactly.** No drift.
- Proto: `ack`, NumParams=2, MaxStack=10, bytecode len=25.
- Compiled `.bin` written to `/tmp/gscript_ack_tier1.bin` (not disassembled this
  round — R26 disasm still current; no code changes between 878e64a and dcc0dc5
  in tier1_call.go).

### Diagnostic Cross-Check (R24 protocol)

1. `.bin` mtime matches current HEAD — ✓ (just rebuilt)
2. bytes from Tier 1 — ✓ (via `CompileBaseline` in the fixture)
3. disasm function = target — ✓ (fixture resolves `ack` by name)
4. insn classification — ✓ (923 matches R26 recorded value)
5. bottleneck share × wall-time ≈ predicted speedup — ✓ (1 STR / ~60 insns = 1.7%
   per-call, × hot-path share → 0.5–1.3% wall-time, within 2× of the R26 KB doc
   estimate)
6. reproducible — ✓ (deterministic compile; fixture asserts)

All six checks pass. No need to spawn a diagnostic sub-agent: the change is a
1-insn move verified entirely by source reading and an existing fixture.

### Actual Bottleneck (data-backed)

R26 KB diagnostic established that the self-call fast path is ~60 caller-side insns
(excluding the 4-insn `self_call_entry` prologue and 4-insn `direct_epilogue`).
Among those 60, R26 classified ~10 as "removable" (9 NativeCallDepth insns + 1
ctx.Constants STR). The NativeCallDepth removal is blocked by the goroutine stack
constraint (R26 data-premise-error). The ctx.Constants STR is independently
removable. Rounding per-call saving: 1 STR of 60 (~1.7%). On ackermann's 67M
self-calls, this is the expected wall-time floor.

## Plan Summary

Move a single `STR X27, [X19, #execCtxOffConstants]` from the shared
`restore_done` join of `emitBaselineNativeCall` in `tier1_call.go` into the
normal-call restore block only. Self-call path never clobbers `ctx.Constants`,
so the write is dead on that path. One Coder task, ≤15 lines changed, guarded
by the existing 923-insn fixture plus a new regression test verifying the STR
appears only inside the normal-call branch.

Expected: ack −0.5% to −1.3%. Primary risk: M4 store buffer may hide the
savings entirely (R22-R23 lesson). If that happens, the lesson is the real
deliverable — ctx-memory stores are also hidden, and the initiative pivots to
RETURN restructuring (Item 4) next round instead of peephole STR removal.
