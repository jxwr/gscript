# nbody Field Hoisting: Research Notes

> Created: 2026-04-07 | Round: 21 (research)

## Context

nbody `advance()` inner j-loop: GetField on bi/bj objects + float arithmetic + SetField on
velocities. Current time: 0.284s vs LuaJIT 0.038s (7.5x gap). This file covers how V8/LuaJIT
handle field load hoisting in nested loops, ARM64 load costs, and gaps in GScript's LICM.

---

## 1. V8 TurboFan: Field-Sensitive Loop Kill Analysis

**Source**: `src/compiler/load-elimination.cc`

V8's `LoadElimination` pass maintains per-program-point abstract state: `map[(object, field-slot-index) → known-value]`.

**Key function: `ComputeLoopState`** (`load-elimination.cc:1363`):
- Called on every loop back-edge (effect phi at loop header)
- Walks the loop body over the effect chain collecting writes
- Per write type:
  - `StoreField(obj, offset)`: kills only `(obj, offset)` via `KillField` (`l-e.cc:1357`) — **field-sensitive**
  - `StoreElement(obj, index)`: kills array elements for that object
  - Unknown effect (call, unrecognized node): calls `KillAll(zone())` — kills everything (`l-e.cc:1458`)
- `KillField` uses `AliasStateInfo` which tracks object identity by SSA node, not type

**Implication for GScript**: V8's loop analysis is field-sensitive — a `StoreField(bj, "vx")` only
kills the `bj.vx` entry, not `bj.x`, `bj.y`, `bi.x`, etc. A generic call kills everything.

**V8 outer-loop behavior**: After `ComputeLoopState` kills only the written fields, a GetField on
an unwritten field (e.g., `bi.x`) that was loaded before the loop remains available through the
loop (dominator-based propagation). V8's load elimination is not just block-local; it propagates
through the dominator tree.

**GScript current behavior** (`pass_licm.go:174-194`): GScript's LICM sets `hasLoopCall = true`
for any `OpCall` or `OpSelf` in the loop body, which blocks ALL `OpGetField` and `OpGetGlobal`
hoisting. For nbody's inner j-loop, after `IntrinsicPass` converts `math.sqrt` to `OpSqrt`,
there are no `OpCall` instructions remaining. So `hasLoopCall = false` and GetField hoisting
should work — but only for fields not written by the loop (bi.x, bi.y, bi.z are NOT written in
the j-loop; only bi.vx/vy/vz are written via SetField).

---

## 2. LuaJIT: Nested Loop Trace Compilation

**Source**: web search + community docs

LuaJIT compiles nested loops as separate traces:
1. Inner loop compiled first as a trace with full loop-invariant code motion
2. When inner loop exits (j > n), a side-trace covers the outer loop back-edge
3. The inner trace is re-entered on each i-iteration

**LuaJIT loop optimization ("loop unrolling")**: LuaJIT compiles a specialized "iteration 1" and
"iteration n>1" version. Disabling this optimization causes ~50% slowdown. For nbody, the inner
loop re-uses forward-substituted values from prior HLOAD operations within the same trace.

**LuaJIT `lj_opt_fwd_hload`** (`lj_opt_mem.c`): During trace recording, walks backward to find a
prior HLOAD or HSTORE on the same table+key. If found with no aliasing store in between,
substitutes the prior value. This is trace-wide CSE, not basic-block-local.

**GScript analog**: GScript's `LoadEliminationPass` (block-local CSE) is the equivalent. Already
implemented (Round 19). Eliminates redundant `bj.mass` loads within the inner j-loop block.

---

## 3. ARM64 M-Series Field Load Cost

**Source**: dougallj.github.io/applecpu (Firestorm microarch), 7-cpu.com/cpu/Apple_M1.html

| Metric | Value |
|--------|-------|
| L1D cache hit latency | **3 cycles** (vs Intel Sunny Cove: 5 cycles) |
| L1D cache size (P-core) | 128 KB |
| Load throughput | 2× 128-bit loads/cycle (2 load AGUs) |
| `FSQRT` (double, scalar) latency | **13 cycles** |
| `FSQRT` throughput | 1 per 2 cycles (one FP div/sqrt unit per 4 FP pipes) |
| `FDIV` (double) latency | ~11 cycles |
| `FMUL` (double) latency | 3 cycles |

**Field load chain**: GScript's `OpGetField` currently emits ~16 ARM64 instructions (shape guard +
table type check + field load + NaN-box). After LoadElimination, redundant loads are CSE'd.
A hoisted GetField in the pre-header runs once per outer-loop iteration instead of once per
inner-loop iteration (5× reduction for nbody with 5 bodies).

**Key insight**: An L1-hit load (3 cycles) is cheap, but GScript's ~16-instruction GetField
wrapper is not. Hoisting eliminates the wrapper overhead, not just the memory latency.

---

## 4. GScript LICM Gaps for nbody

### Gap 1: `OpSqrt` not in `canHoistOp`

`pass_licm.go:527-548` — `canHoistOp` does not list `OpSqrt`. After `IntrinsicPass` converts
`math.sqrt(dsq)` to `OpSqrt(dsq)`, LICM cannot hoist it even if `dsq` is invariant.

In nbody's j-loop, `dsq` is NOT invariant (it depends on bi.x-bj.x etc. which vary per j).
So this gap has no impact on nbody specifically — but for other loops using `math.sqrt` of a
loop-invariant value (e.g., precomputed distance), it would matter.

**Fix**: Add `OpSqrt` to `canHoistOp`. Pure single-input float op, no side effects.

### Gap 2: `OpGetField` of non-written fields in j-loop

In nbody's j-loop, `bi.x`, `bi.y`, `bi.z`, `bi.mass` are read but never written (only
`bi.vx/vy/vz` are written via `SetField`). The LICM alias check correctly allows hoisting:

- `setFields[loadKey{objID: bi.ID, fieldAux: "x"}]` → false (x not written)
- `hasLoopCall` → false (after IntrinsicPass removes math.sqrt call)

So these should already be hoistable to the j-loop pre-header. Whether this is happening
correctly in production can be verified with `Diagnose()` + IR dump on `advance()`.

### Gap 3: `bi` object itself (from outer loop `GetTable`)

`bi := bodies[i]` is a `GetTable` (not `GetField`), executed once per outer-loop iteration.
This produces the `bi` SSA value. For the j-loop, `bi` is loop-invariant — it doesn't change
per j-iteration. LICM should hoist GetField on `bi` (if `bi` is invariant w.r.t. j-loop).

The issue: `bi`'s def is in the outer-loop body (not outside any loop), so it IS invariant
w.r.t. the inner j-loop. LICM Seed 1 (`pass_licm.go:143-151`) marks values defined outside the
j-loop body as invariant — this correctly marks `bi` as invariant for the j-loop. GetFields
on `bi` for non-written fields should therefore be hoistable to the j-loop pre-header.

---

## 5. Next Opportunity: What LICM Cannot Currently Hoist

After the current implementation:
- `bodies` global: hoisted to function pre-header (GetGlobal, Round 20)
- `bj.mass`, `bi.mass` CSE: eliminated by LoadElimination (Round 19)
- `bi.x/y/z/mass` per j-iteration: these are GetField where bi is j-invariant and fields
  are not written in j-loop → **should already hoist** (verify with IR dump)

**Remaining opportunity**: If `bi.x/y/z` are not being hoisted, the fix is verifying that
`bi`'s definition (OpGetTable result) is correctly treated as invariant in j-loop.

**Ackermann regression**: Self-call check adds ~13 instructions per Tier 1 call site
(proto comparison + 4× CBNZ flag checks). For tight recursion with millions of calls, this
dominates. Fix: emit self-call path only when function actually calls itself (compile-time
detection via proto walking).

---

## Sources

- V8 `src/compiler/load-elimination.cc` — ComputeLoopState:1363, KillField:616, KillAll:662
- [dougallj Firestorm microarch](https://dougallj.github.io/applecpu/firestorm-simd.html) — FSQRT latency/throughput
- [7-cpu Apple M1](https://www.7-cpu.com/cpu/Apple_M1.html) — L1D 3-cycle hit latency
- [LuaJIT nested loops](https://lua-l.lua.narkive.com/cE0ggdYv/luajit-nested-loops-performance-difference) — 2 traces for nested loops, loop opt = 50% of perf
