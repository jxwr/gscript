---
module: architecture
description: Global invariants that every KB card depends on. Top-level, no per-module details.
files:
  - path: internal/methodjit/tiering_manager.go
  - path: internal/methodjit/pipeline.go
  - path: docs-internal/architecture/overview.md
last_verified: 2026-04-13
---

# Architecture — Top-Level Invariants

GScript is a three-tier Method JIT targeting ARM64 macOS. Modeled on V8 (Sparkplug → Maglev → TurboFan).

## Tier layout

```
Tier 0  internal/vm/               Interpreter. Collects type feedback. Runs on first call.
Tier 1  internal/methodjit/tier1_*.go  Baseline JIT: 1:1 bytecode templates, no IR, no optimization.
Tier 2  internal/methodjit/            Optimizing JIT: SSA IR → passes → RegAlloc → Emit.
```

`internal/jit/` is deprecated (a trace-shaped experiment), disconnected from the CLI. Do not extend it; substrate is locked by `docs-internal/decisions/adr-no-trace-jit.md`.

## Tier 2 pipeline (fixed order)

Defined in `(*Tier2Pipeline)` — the sole sanctioned entry is `TieringManager.compileTier2Pipeline`, called by both production `compileTier2` and diagnostic `CompileForDiagnostics`:

```
BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec
  → Inline → SimplifyPhis → TypeSpec
  → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → ScalarPromote
  → Validate → RegAlloc → Emit
```

Every pass lives in `internal/methodjit/pass_<name>.go` with a mandatory `pass_<name>_test.go`. Each card under `kb/modules/passes/` holds the per-pass invariants.

## Register convention (ARM64)

Fixed-purpose (non-allocatable):

| Register | Role |
|----------|------|
| X19 | `*ExecContext` pointer |
| X24 | Int NaN-box tag constant (`0xFFFE000000000000`) |
| X25 | Bool NaN-box tag constant |
| X26 | VM register base (`regs[base]`) |
| X27 | `*Constants` pointer |
| X21 | Closure cache (R19 optimization, Round 19) |
| X22 | Slot-0 (R(0)) pinned value (R19 optimization) |

Allocatable:

| Registers | Pool |
|-----------|------|
| X20, X23, X28 | GPR allocation pool (3 primary) |
| D4–D11 | FPR allocation pool (8) |

Callee-saved (must be spilled around calls): X19–X28 and D8–D15 per the AArch64 AAPCS.

## Value representation (NaN-boxing)

Every runtime value is a `uint64`:

| Tag bits | Sub-type | Meaning |
|----------|----------|---------|
| `0xFFFE` | int | 48-bit signed integer in low bits |
| `0xFFFD` | bool | low bit is the boolean |
| `0xFFFC` | nil | |
| `0xFFFF` | ptr | low 48 bits point to a heap object; sub-tag in bits 48..51 discriminates VMClosure (8), Table (0), String, etc. |
| anything else | float | raw IEEE 754 bits |

**MUST**: every new IR op, every new emitter, every new intrinsic preserves NaN-box invariants. A pointer written into a float slot is a silent corruption.

## Exit codes (JIT → Go)

| Code | Meaning |
|------|---------|
| 0 | Normal return |
| 2 | Deopt → interpreter |
| 3 | Call-exit (Tier 2 resume after Go handles the call) |
| 4 | Global-exit (Tier 2) |
| 5 | Table-exit (Tier 2) |
| 6 | Op-exit (Tier 2: generic unsupported op) |
| 7 | Baseline op-exit (Tier 1: exit-resume) |
| 8 | Native call exit (Tier 1: callee hit exit during BLR call) |
| 9 | OSR (Tier 1: loop counter expired, request Tier 2) |

Every emitter that can exit MUST either emit a normal return (0) or one of the documented exit codes. New exit codes require an ADR under `docs-internal/decisions/`.

## Microarchitecture facts (Apple M-series)

These have been observed empirically; every round respects them or wastes itself.

1. **M4 is 6–8-wide superscalar.** Removing an instruction from the critical path is usually free at the wall-clock level. Expect zero wall-time change from "fewer guards" / "fewer loads" optimizations unless the removed instructions fed a long latency chain. Validate with `scripts/diag.sh` + real benchmarks, never with insn-count alone.
2. **Branch predictor handles dispatch cascades for free.** IF/ELSE chains with ≤4 branches are predicted correctly for any predictable input. Don't optimize them.
3. **L1 load-to-use latency is 4 cycles.** Chains of dependent loads are the hot path cost. LoadElim and cross-block GetField CSE are high-ROI for this reason.
4. **FPR-GPR moves cost one cycle each way.** NaN-box unbox/rebox on every op is the reason FPR-resident values matter.

## What the diagnostic tool can tell you

`scripts/diag.sh all` produces for every benchmark:
- `diag/<bench>/stats.json` — insn count + histogram (load/store/dpi/dpr/fp/branch)
- `diag/<bench>/<proto>.ir.txt` — post-pipeline IR + regalloc map
- `diag/<bench>/<proto>.asm.txt` — ARM64 Go-syntax disasm
- `diag/<bench>/<proto>.bin` — raw code bytes
- `diag/summary.md` — top drifters vs `reference.json`, histogram anomalies

Parity with production is enforced by `TestDiag_ProductionParity_*` — if those ever fail, the tool is lying and every conclusion from it is invalid.

`scripts/diag.sh` is the only sanctioned Tier 2 evidence source. `profileTier2Func`, `NewTier2Pipeline` (unless you're writing a correctness oracle via `Diagnose()`), hand-rolled pipelines, synthetic IR test fixtures — all banned for performance diagnostics. See CLAUDE.md rule 5.

## Correctness oracle

`internal/methodjit/diagnose.go` provides `Diagnose(proto, args) → *DiagReport` comparing IR-interpreter output against native execution. Use this when a test fails with a wrong answer (as opposed to a performance regression). It does NOT measure performance and its pipeline (`NewTier2Pipeline`) is not bit-identical to production.

`internal/methodjit/interp.go` is the ground-truth IR interpreter — the authoritative reference for what every IR op means.

`internal/methodjit/validator.go` runs after every pass to catch structural invariant violations (dangling values, unreachable blocks, phi mismatch). Any new pass must leave the IR valid.

## Unpromotable / staying-at-Tier-1

Tier 2 compilation refuses the following classes, returning `proto` to Tier 1 permanently:

- **goroutine / channel ops** (`GO`, `MAKECHAN`, `SEND`, `RECV`) — blocked by `canPromoteToTier2`
- **variadic CALL** (`OP_CALL B==0`) — `BuildGraph` sets `fn.Unpromotable`; SSA can't model the runtime top tracker
- **OpCall inside a loop** — `hasCallInLoop(fn)` rejects; Tier 2's exit-resume cost multiplied in a hot loop is slower than Tier 1's BLR (R11 spectral_norm 7.10× → 0.82× regression)
- **IR that fails `Validate`** after the pipeline

Refusals are recorded in `tm.tier2FailReason[proto]`. Do not silently re-try failed protos; they go back to Tier 1 for the lifetime of the VM.

## Non-negotiable rules (also in CLAUDE.md)

1. No Go file > 1000 lines.
2. TDD mandatory.
3. Only `compileTier2Pipeline` via `compileTier2` or `CompileForDiagnostics` for Tier 2 evidence.
4. Median-of-N (≥3) for every benchmark comparison.
5. `benchmarks/data/reference.json` is frozen and does not rotate.
6. Architecture-first target selection: global > module > local.

## See also

- `docs-internal/architecture/overview.md` — narrative, slightly longer
- `docs-internal/architecture/constraints.md` — mechanical constraints enforced by `scripts/arch_check.sh`
- `docs-internal/decisions/` — ADRs
- `kb/modules/ir.md` — SSA IR data structures
- `kb/modules/tier1.md`, `kb/modules/tier2.md` — per-tier invariants
- `kb/modules/emit/overview.md` — ARM64 emit layer
