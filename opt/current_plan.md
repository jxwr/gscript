---
round: R29
cycle_id: 2026-04-11-fib-regression-root-cause
category: tier1_dispatch
initiative: opt/initiatives/tier1-call-overhead.md
target_item: 8
outcome_goal: diagnostic + fixture (no perf fix; R30 implements)
---

# R29 Plan — Fib regression root cause

## Premise

R28 user-led bisect pinned commit `598bc1e` (self-call `DirectEntryPtr` correctness fix) as the pivot giving ackermann −50% and fib **+988%** on the same self-call code path. Initiative item 8 explicitly assigns R29 to analysis, R30+ to fix.

The diagnostic sub-agent has already collected the runtime data (see `opt/knowledge/r29-fib-root-cause.md`). Summary of findings:

- `handleNativeCallExit` fires **exactly once** per benchmark (fib: 1, ack: 1)
- Both `fibProto.DirectEntryPtr` and `ackProto.DirectEntryPtr` are permanently zeroed on the first self-call (triggered by `OP_GETGLOBAL` cache miss inside the first BLR'd callee)
- After zeroing, the 598bc1e guard at `tier1_call.go:316-317` (`LDR X3, [X1, funcProtoOffDirectEntryPtr]; CBZ X3, slowLabel`) redirects every subsequent self-call to `emitBaselineOpExitCommon(OP_CALL)`
- For fib(35) with ~29M recursive calls, that's ~29M Go/JIT roundtrips via `handleCall → e.Execute()`
- Ack is fine because its call depth is tiny (ack(3,4) ~ thousands of calls, not tens of millions)
- No int-spec deopt fires — `EvictCompiled` is not involved

**The `CBZ` check permanently flips fib into a slow mode after the *very first* cold-start GETGLOBAL miss.**

## What R29 changes

**Nothing in production code.** This is the last round of pure analysis for item 8. Two deliverables:

1. Cement the diagnostic findings as durable knowledge (`opt/knowledge/r29-fib-root-cause.md`, already written)
2. Add a fib dump/fixture test mirroring `tier1_ack_dump_test.go` so R30's fix has a sentinel for insn-count movement on the fib side

## Task 0 (infra, Coder) — add fib Tier 1 dump fixture — DONE

Measured baseline: `fibTotalInsnBaseline = 635` (2540 bytes). File `tier1_fib_dump_test.go`, 78 lines. Ack baseline (936) untouched. Full `./internal/methodjit/` package passes.


**Scope**: new test file `internal/methodjit/tier1_fib_dump_test.go`, mirroring `tier1_ack_dump_test.go` exactly in structure.

**Concrete steps**:

1. Copy `tier1_ack_dump_test.go` → `tier1_fib_dump_test.go`
2. Rename symbols:
   - `ackTotalInsnBaseline` → `fibTotalInsnBaseline`
   - `TestDumpTier1_AckermannBody` → `TestDumpTier1_FibBody`
3. Change source file path: `"../../benchmarks/suite/ackermann.gs"` → `"../../benchmarks/suite/fib.gs"`
4. Change target name: `"ack"` → `"fib"`
5. Change dump path: `"/tmp/gscript_ack_tier1.bin"` → `"/tmp/gscript_fib_tier1.bin"`
6. Run the test once to record the **current** total insn count. Set `fibTotalInsnBaseline = <measured>`.
7. Replace the history comment block with a single line: `// R29 baseline: fib is currently regressed; this fixture sentinels the self-call path insn count so R30+ can assert the guard removal actually trims instructions.`

**Strictly NOT in scope**:

- Do NOT touch `tier1_call.go` or any production file
- Do NOT change `ackTotalInsnBaseline`
- Do NOT add behavioral assertions (no wall-time checks, no exit-code counters) — this is a pure insn-count sentinel
- Do NOT attempt the R30 fix, even if it seems obvious

**Acceptance**:

- `go test ./internal/methodjit/ -run TestDumpTier1_FibBody -v` passes
- `go test ./internal/methodjit/ -run TestDumpTier1_AckermannBody -v` still passes (unchanged)
- `go test ./internal/methodjit/ -count=1` — full package passes
- `go vet ./...` clean
- File size: `tier1_fib_dump_test.go` ≤ 100 lines

**Budget**: ≤ 6 tool calls, ≤ 5 minutes.

## Task 1 (Coder, implementation) — NONE

R29 deliberately has no implementation task. The initiative file commits R29 to analysis. Attempting the fix in the same round as root-cause analysis would collapse the "hypothesis → test → confirm" discipline that the harness is meant to enforce.

## Fix direction for R30 (NOT part of R29)

Two candidates the diagnostic agent surfaced; R30's ANALYZE phase must pick one after re-reading the code:

- **Candidate A — drop the self-call `CBZ` only**. Remove `tier1_call.go:316-317` from the `selfCallExecLabel` path. Keep the check on the normal-call path (which dispatches via `BLR X2` to a foreign proto's `DirectEntryPtr` and genuinely needs the guard). Rationale: `self_call_entry` is a static label in the current binary, so the callee code is guaranteed to exist as long as we're executing it. Risk: must re-verify `TestDeepRecursionRegression` + `TestQuicksortSmall` still pass — they exist precisely to catch the `handleNativeCallExit → executeInner` nesting overflow that 598bc1e was fixing.
- **Candidate B — indirection flag**. Add `HasOpExits bool` to `FuncProto`, set once when `handleNativeCallExit` fires for that proto, checked on the normal-call path only. Leave `DirectEntryPtr` non-zero so the self-call fast path keeps running. More surgical but requires a field addition and `handleNativeCallExit` rewrite.

R30 ANALYZE must run the deep-recursion regression test against candidate A before committing to it.

## Predictions (R29 itself)

- Fixture adds 1 test file, +90 LOC
- No wall-time change (no production code touched)
- Fixture catches the R30 fix when it lands: expected delta is **−2 insns** on fib (the `LDR` + `CBZ` removal), which halves to 0 insns if the splitter branch lets superscalar fold them — we'll learn whether the guard is on the hot path from that measurement
- Round outcome: `diagnostic`

## Anti-drift checks

- [x] Respects initiative R29 assignment (analysis only)
- [x] 1-Coder rule: Task 0 only, no Task 1 implementation
- [x] No scope creep into R30 fix territory
- [x] No touching `tier1_call.go` or call-path production files
- [x] Does not regress `ackTotalInsnBaseline` (left alone)

## Success criteria

1. `opt/knowledge/r29-fib-root-cause.md` documents which mechanism zeroes `DirectEntryPtr`, how many times, and why ack is OK — DONE by sub-agent
2. `tier1_fib_dump_test.go` exists and the insn-count sentinel is recorded
3. R30 ANALYZE has enough data to pick between candidate A and B without re-running the diagnostic
