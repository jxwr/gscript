---
round: 5 (WIN — binary_trees −21.4%)
date: 2026-04-14
follows: rounds 1-4 (2 reverts, 2 diagnostic/meta)
---

# Round 5 — Tier 0 gate for tiny recursive table builders

## What I did

Added a `shouldStayTier0` heuristic in `internal/methodjit/func_profile.go` and wired it into `TieringManager.TryCompile`. The gate matches small (≤ 25 bytecodes), no-loop, allocation-heavy (NewTableCount > 0), call-having functions and returns nil from the compile path, routing them back to Tier 0.

The canonical match: `binary_trees.gs`'s `makeTree` — 21 bytecodes, 2 NEWTABLEs, 2 recursive calls, no loops.

## Why this worked when rounds 1–4 didn't

Rounds 1–4 all operated from narrative: "the KB says pointer X is dead, remove it"; "the KB says scan Y is too large, shrink it"; "the asm shows dispatch Z is 8 instructions, specialize it". All four rounds ran the same failure pattern: trust a narrative, predict a win, measure, revert or null.

Round 5 worked from a directly observed fact — **binary_trees runs slower under JIT than under the interpreter** — and asked the shape-based question: what kind of function is it? The answer (tiny, recursive, allocates) immediately suggested the mechanism (exit-resume overhead dominates native-template win). One gate, one commit, measured win.

## Measurement

Full benchmark suite, median-of-5:

| Benchmark | Pre-R5 | Round 5 | Delta |
|-----------|-------:|--------:|------:|
| `binary_trees` | 1.997s | **1.570s** | **−21.4%** |
| `object_creation` | 1.086s | 1.039s | −4.3% (noise) |
| `nbody` | 0.252s | 0.238s | −5.6% (noise) |
| `mandelbrot` | 0.063s | 0.060s | −4.8% (noise) |
| `fibonacci_iterative` | 0.295s | 0.280s | −5.1% (noise) |
| All others | — | — | within ±2% |

No regressions. The only claim is binary_trees. Everything else is noise-level noise.

## Gate parameters (from `func_profile.go`)

```go
func shouldStayTier0(profile FuncProfile) bool {
    return profile.BytecodeCount <= 25 &&
        profile.NewTableCount > 0 &&
        !profile.HasLoop &&
        profile.CallCount > 0
}
```

- `BytecodeCount <= 25` — tight enough to exclude real compute functions.
- `NewTableCount > 0` — only allocation-heavy shapes pay the exit-resume cost.
- `!HasLoop` — loops benefit from Tier 1 native templates and should stay compiled.
- `CallCount > 0` — discriminates recursive allocators (compile themselves) from leaf allocators (called from a JIT'd loop, caller gets the win).

## KB updates

- `kb/modules/runtime/gc.md`: removed the "binary_trees JIT slower than VM" Known gap; noted it as closed in Round 5.
- `kb/modules/tier1.md`: added an explicit Known gap about exit-resume overhead on NEWTABLE and related ops, and documented the `shouldStayTier0` mitigation. Future rounds will know this gate exists.

## Lesson

When predictions based on narrative fail, pivot to shape-based observation. "JIT slower than VM" is a provable falsehood that directly points at a fix; "GC pointer trace costs X%" is a narrative that may not hold.

## Round summary (1-5)

| Round | Outcome | Real change? |
|------:|---------|:-------------|
| 1 | reverted (hypothesis wrong) | No |
| 2 | reverted (same class of mistake) | No |
| 3 | diagnostic only (no local fix exists at this level) | No |
| 4 | KB update recording Rounds 1–3 negatives | Meta |
| 5 | **binary_trees −21.4%** via Tier 0 gate | **Yes** |

5 rounds, 1 code win. The v4 workflow caught 2 bad hypotheses cleanly with its revert discipline and produced a meta-correction (Round 4) that stopped the pattern from continuing. Round 5 broke the pattern by picking evidence-first instead of narrative-first.
