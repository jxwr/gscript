---
module: passes/intrinsic
description: Recognize math-builtin call patterns (currently math.sqrt) and rewrite them in place to a single IR op. Eliminates the GetGlobal → GetField → Call chain.
files:
  - path: internal/methodjit/pass_intrinsic.go
last_verified: 2026-04-13
---

# Intrinsic Pass

## Purpose

Detect calls to known math builtins and rewrite them into dedicated IR ops (e.g. `math.sqrt(x)` → `OpSqrt x`). The original `OpGetGlobal("math")` and `OpGetField("sqrt")` instructions become dead once the `OpCall`'s `Args[0]` is dropped and are collected by `DCEPass` later in the pipeline. This turns a three-instruction table-chasing call into a single ARM64 instruction (`FSQRT`).

## Public API

- `func IntrinsicPass(fn *Function) (*Function, []string)` — note: returns `[]string` rewrite notes, NOT `error`; the only non-`PassFunc` pass in the pipeline. `pipeline.go` wraps it in a closure to fit `PassFunc`.

## Invariants

- **MUST**: rewrite is in-place — the `OpCall` instruction's `Op` is mutated to `OpSqrt`, `Type` set to `TypeFloat`, and `Args` truncated to `[x]` (the single argument). The instruction's `ID` is preserved so downstream `replaceAllUses` remains valid.
- **MUST**: pattern match is exact: `OpCall` with exactly two `Args` (`[fnValue, arg]`), `fnValue.Def` is `OpGetField` whose `Args[0].Def` is `OpGetGlobal`, and both the global name (`"math"`) and field name (`"sqrt"`) resolve through `constString(fn, aux)` against `fn.Proto.Constants`.
- **MUST**: the pass returns its rewrite notes in the `[]string` result; the notes are surfaced by `compileTier2Pipeline` and stored in `CompiledFunction.IntrinsicNotes` so Tier 1 callers know to dispatch via Tier 2.
- **MUST**: when any rewrite happens, `proto.NeedsTier2` is set by the caller — Tier-1-only execution of the same proto would produce different observable behavior (no intrinsic rewrite).
- **MUST NOT**: create new `Value`s — the pass only mutates existing ones. Value IDs and block membership are unchanged.
- **MUST NOT**: rewrite a call whose `fn.Proto` is nil (defensive early return).

## Hot paths

- `spectral_norm` — the Eval-A kernel calls `math.sqrt` inside the outer reduction; rewriting eliminates one full call-exit cycle per inner-loop trip.
- `nbody` — `math.sqrt` in the distance computation runs once per pair per timestep.
- `mandelbrot` — `math.sqrt` is not used; intrinsic pass is a no-op there.

## Known gaps

- **Only `math.sqrt`.** Not recognized: `math.abs`, `math.floor`, `math.ceil`, `math.sin/cos/tan`, `math.log/exp`, `math.max/min`, `math.pow`, `math.random`, `math.pi`. Each would need its own pattern + IR op.
- **No `string.*` or `table.*` intrinsics.** Table builtins still go through the full call path.
- **No two-arg intrinsics.** `len(instr.Args) != 2` short-circuits, so `math.pow(x, y)` cannot be rewritten without changing the pattern matcher.
- **No monomorphic-call-site polyfill.** If the user shadows `math` with a local, the pattern still matches on the SSA shape and silently mis-compiles — relies on `math` being a global and not reassigned. Absent in current benchmarks but a correctness landmine.

## Tests

- No dedicated `pass_intrinsic_test.go` exists. Coverage is indirect through `emit_tier2_correctness_test.go` (notes the `math.sqrt → OpSqrt` rewrite) and `loops_preheader_test.go` (exercises `IntrinsicPass(fn)` as a fixture). **Gap: pattern-match edge cases (shadowed `math`, arg-count mismatch, non-string constant) have no targeted unit test.**
