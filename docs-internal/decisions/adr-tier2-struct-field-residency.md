# ADR: Tier 2 struct field residency (LICM for GetField)

**Status:** Proposed (R29 architecture round, 2026-04-17)
**Scope:** close nbody's 7.2× LuaJIT gap without trace JIT.
**Related:** R21 ADR (disproven by R24), R25 ADR v2, `pass_scalar_promote.go`,
`loops.go`, `emit_table_field.go`.

---

## 1. Current state audit (per rule 23)

**Measurement performed:** read nbody.gs `advance()` source + existing
emit for GetField + existing LICM infrastructure.

### 1.1 What nbody's hot path actually looks like

`advance()` runs 500k outer iterations × O(n²) pairwise body loop
(n=5) ≈ 10M inner iterations. Inner body:

```
for j := i + 1; j <= n; j++ {
    bj := bodies[j]
    dx := bi.x - bj.x     // bi.x: loop-invariant, bj.x: varies with j
    dy := bi.y - bj.y     // same
    dz := bi.z - bj.z     // same
    dsq := dx*dx + dy*dy + dz*dz
    dist := math.sqrt(dsq)
    mag := dt / (dsq * dist)
    bi.vx = bi.vx - dx * bj.mass * mag   // bi.mass invariant
    ...
}
```

**Per inner iteration observed accesses:**
- `bi.x, bi.y, bi.z`: read-only, **loop-invariant** across j.
- `bi.mass`: read-only, **loop-invariant** (only used when computing
  bj force, so also invariant).
- `bi.vx, bi.vy, bi.vz`: read + write each iteration — NOT invariant.
- `bj.x, bj.y, bj.z, bj.mass, bj.vx, bj.vy, bj.vz`: vary with j.

**GetField access cost** (existing emit, from `emit_table_field.go`):

First access (full shape guard):
```
EmitExtractPtr            ; 1 insn  (extract *Table)
CBZ slowpath              ; 1 insn  (nil check)
LDRW shapeID              ; 1 insn
CMP shapeID, known        ; 2 insns (LoadImm + CMP)
BCond NE deopt            ; 1 insn
LDR svals data ptr        ; 1 insn
LDR svals[fieldIdx]       ; 1 insn
FMOVtoFP (NaN-box→FPR)    ; 1 insn
                          ; = ~9 insns
```

Subsequent accesses on same table (`shapeVerified` cache active):
```
resolveValueNB tbl        ; ~1 insn (reg already loaded)
EmitExtractPtr            ; 1 insn
LDR svals data ptr        ; 1 insn   ← NOT cached; reloaded every time
LDR svals[fieldIdx]       ; 1 insn
FMOVtoFP                  ; 1 insn
                          ; = ~5 insns per field
```

At 10M iterations × ~14 GetField per iter × 4 insns average =
**~560M insns spent on GetField alone**. On M4 at ~2 insn/ns pipelined,
that's ~280 ms — and nbody's total wall time is 237 ms. So GetField
overhead is approximately equal to the entire benchmark.

LuaJIT's trace JIT caches struct fields in traces; its `advance()`
inner body compiles to ~15-20 total insns per iteration, not ~70.

### 1.2 What existing LICM does and doesn't do

Existing preheader-invariant pass (`pass_scalar_promote.go`,
`loops.go`):
- Hoists loop-invariant ARITHMETIC computations into the preheader.
- Handles `CarryPreheaderInvariants`: invariant FPR values stay in
  FPRs across loop-body blocks.
- Does NOT hoist GetField / GetTable. They're treated as side-
  effecting and kept in the loop body.

The treatment is conservative for a reason: any SetField in the
loop body COULD invalidate the field. But nbody's `advance()`
loop writes only bi.vx/vy/vz — not bi.x/y/z/mass. A smarter
analysis could prove those are invariant.

### 1.3 The shapeVerified emit-time cache is block-local

`emit_table_field.go` lines 43 / 133: `shapeVerified` is reset at
block boundaries and after calls. Loop body = multiple blocks in
general. So the cache doesn't survive across iterations even when
it COULD — the current pass doesn't reason about loop-carry.

**Conclusion of audit:** the nbody gap is dominated by missing LICM
for GetField on loop-invariant struct fields. This is a compiler
pass, not a runtime layout change. Not "already done."

## 2. Decision

Introduce a **struct field residency pass** that runs after typespec
and before register allocation:

1. For each loop, identify GetField sites where:
   - The table operand is a phi from outside the loop (or a LoadSlot
     of a loop-invariant variable), AND
   - No SetField in the loop body writes to that (shape, field) pair,
     AND
   - The field has feedback type float (raw-FPR eligible).

2. Hoist the GetField to the loop's preheader. The resulting value
   enters the loop body as a phi input in the natural way.

3. Register allocator (already aware of `CarryPreheaderInvariants`)
   pins the hoisted float to an FPR across iterations.

### 2.1 Safety argument

- Shape invalidation requires a SetField that writes to the same
  (table, field). If we verify no such SetField exists in the loop,
  the hoisted value is provably live-correct.
- Cross-call safety: if the loop contains a function call, that call
  could mutate bi via aliasing. MITIGATION: conservative — only
  hoist when the loop has NO intervening calls. (nbody's advance()
  inner loop has `math.sqrt` which is an intrinsic; we should mark
  intrinsics as "doesn't mutate arbitrary tables." Already done in
  `pass_intrinsics_*`.)

### 2.2 Why this isn't "trace JIT"

Trace JIT records runtime paths and re-compiles them. This pass is
static: it analyzes the SSA graph at compile time without any
runtime recording. The specialization is keyed on the function's
IR structure + shape feedback, not on an executed trace.

## 3. Expected impact

Conservative estimate based on §1.1 measurement:

- Hoist 4 invariant bi.* reads out of nbody's advance inner loop.
- Saves ~4 × 5 insns = 20 insns per iteration × 10M = 200M insns.
- At 2 insn/ns → ~100 ms saved.
- nbody 0.237s → projected **~0.14 s**. LuaJIT 0.033. New gap **~4×**.

Not LuaJIT parity, but **~2× improvement on nbody**, and the pass
generalizes to any "struct-in-loop" pattern (energy(), advance(),
spectral_norm's dot product, etc.).

## 4. Non-goals

- Dense struct runtime type change. Rejected for R29 scope: existing
  GetField from svals is only 2 LDRs once shape-verified; the
  structural win is HOISTING, not storage-layout change.
- Cross-function LICM (inline `math.sqrt`). Future work; separate ADR.
- Handling SetField inside the hoist-candidate loop. Conservative
  v1 skips any loop containing a SetField to the same shape as the
  hoist candidate.

## 5. Implementation staging

| Round | Step | Priority |
|------:|------|----------|
| R32   | Implement `pass_struct_field_residency.go` + safety analysis | HIGH |
| R33   | Composition test: hoisting + existing preheader-invariants + FPR residency | HIGH |
| R34   | Apply to matmul's `ai` row-access pattern (related but distinct) | MEDIUM |

Each step is TDD. Pre-flight before integration: show the IR for
nbody's advance() inner loop with GetField hoisted → fewer LDRs
per iteration.

## 6. Decision outcome

**Accepted.** Current state audit confirmed the gap is missing LICM
for GetField, not typespec / dense-struct / trace JIT.
R32 takes implementation first; R33 validates composition; R34
extends to matmul.
