---
round: 2 (reverted)
date: 2026-04-14
outcome: hypothesis-wrong (second time — stopping GC-angle rounds)
next_round: 3
---

# Round 2 — Reverted (same class of mistake as Round 1)

## What I predicted

A 1-line change: `vm.go:225` `MakeNilSlice(1024)` → `MakeNilSlice(128)`. Expected: `ScanGCRoots` scan range shrinks from 1024 → 128 for shallow benchmarks, unlocking a few percent on allocation-heavy workloads. Explicit failure criterion: "revert if ANY benchmark regresses by >5%".

## What happened

| Benchmark | Pre-R2 | Round 2 | Delta |
|-----------|-------:|--------:|------:|
| `object_creation` | 1.086s | 1.078s | −0.7% (noise) |
| `fannkuch` | 0.048s | 0.871s | **+1714%** CATASTROPHIC |
| `fibonacci_iterative` | 0.295s | 0.333s | +12.9% |
| `coroutine_bench` | 14.0s | 16.1s | +15% (noisy) |

fannkuch 17× slower than baseline. Failure criterion triggered. Reverted.

## Root cause (likely)

The small initial size means the VM grows `vm.regs` via `MakeNilSlice + copy` on the first deep call. But the JIT caches `execCtx.RegsEnd = &regs[0] + len(regs)*8` at compile time based on the pre-growth size. When `vm.regs` is replaced with a new slice (different base pointer), the old compiled JIT code's `RegsEnd` becomes stale — it points into the freed slice. This probably causes either spurious slow-path exits or actual data corruption (caught as benchmark slowdown, not crash).

Didn't investigate further because the revert path was clear. If I ever revisit initial-slice sizing, the fix would require either (a) not reallocating `vm.regs` (grow in place via `append` on a pre-oversized cap), or (b) forcibly resyncing `RegsEnd` on every slice swap via a callback.

## Pattern alert: two rounds on the same class of mistake

Both Round 1 and Round 2 tried to close `object_creation` by reducing GC scan overhead. Both failed. This is evidence that **GC scan is not the dominant cost**, and the +42% drift vs reference.json is either:

- Baseline noise in `reference.json` (it was frozen at `a388f782` and may reflect a slightly different GC schedule that happened to favor object_creation)
- A real Go-GC-level cost that can't be removed by JIT changes

Either way: **stop trying to close object_creation via scan/pointer changes**. That drift is accepted.

## Lessons for the next round

The R28-R35 blog post called out the "trying harder at the same wall" pattern as the primary workflow failure mode. I'm one round away from matching it. Breaking the pattern:

- Round 3 picks a DIFFERENT angle entirely — not GC, not allocation, not ScanGCRoots
- Round 3 prediction is based on specific disassembly evidence, not a narrative from a knowledge doc
- Round 3 scope is tightly bounded to a single instruction-level change

## Next: Round 3

Pick from the LuaJIT-gap list (recursive benchmarks excluded — they're call-dominated and Tier 1's BLR already handles them as best it can):

| Benchmark | JIT | LuaJIT | Gap |
|-----------|----:|-------:|----:|
| `sieve` | 0.088 | 0.010 | 8.8× |
| `nbody` | 0.248 | 0.034 | 7.3× |
| `spectral_norm` | 0.045 | 0.007 | 6.4× |
| `matmul` | 0.123 | 0.022 | 5.6× |
| `sort` | 0.051 | 0.010 | 5.1× |
| `mandelbrot` | 0.063 | 0.058 | **1.09×** (near-optimal) |

`mandelbrot` at 1.09× proves this JIT can be near-optimal when the hot loop is well-shaped. The 5–9× gaps on the others are about specific emit-layer costs, not fundamental limits.

Round 3 direction to be written after reading sieve's actual disasm — no pre-commitment to a specific fix yet.
