# Matmul Tier-Up Gap

> Last Updated: 2026-04-06 | Round: 13 (analysis)

## Finding

The `matmul` function (O(n^3) triple-nested loop, N=300 → 27M inner iterations) is called exactly ONCE in the benchmark. `shouldPromoteTier2` requires `runtimeCallCount >= 2` for the "pure-compute loop" clause. OSR is disabled. Result: **the most compute-intensive function runs entirely at Tier 1.**

## Details

| Function | Calls | Threshold | Reaches Tier 2? |
|----------|-------|-----------|-----------------|
| `matgen` | 2 | 2 | Yes (2nd call) |
| `matmul` | 1 | 2 | **No** |
| `<main>` | 1 | N/A | No (correct) |

matmul: JIT 0.830s vs VM 1.053s = 1.27x speedup only. Vs LuaJIT 0.022s = 37.7x gap.

## Even at Tier 2, Inner Loop is Untyped

The inner loop IR shows:
```
v31 = GetTable v13, v43 : any    // a[i][k]
v35 = GetTable v33, v58 : any    // b[k][j]
v36 = Mul v31, v35 : any         // untyped
v38 = Add v49, v36 : any         // untyped
```

GetTable results are `:any` → TypeSpecialize can't specialize Mul/Add → generic dispatch per iteration. Fixing tier-up alone won't close the gap. Needs feedback-typed loads (blocked per round 12) or table result type inference.

## Fix Options

1. **Lower threshold for LoopDepth >= 2**: `runtimeCallCount >= 1` if `profile.LoopDepth >= 2`
2. **Re-enable OSR**: currently disabled due to mandelbrot regression (may not apply to matmul)
3. **Profile-based single-call promotion**: detect O(n^k) loop nests and promote on first call

CAUTION: Tiering policy changes require CLI integration testing (lesson from round 4 hang).

## Priority

Separate from round 13 (field_access). Queue for a future round — needs both tier-up fix AND GetTable type specialization to see meaningful improvement.
