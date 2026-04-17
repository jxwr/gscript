# ADR: Tier 2 dense matrix layout for matmul-class

**Status:** Proposed — PHASED (R30 architecture round, 2026-04-17)
**Scope:** close matmul's 5.4× LuaJIT gap without trace JIT.
**Related:** R21/R25 ADRs (tight-loop), R29 ADR (struct field residency),
`internal/runtime/table.go` ArrayKind.

---

## 1. Current state audit (per rule 23)

**Measurement performed:** read matmul's Tier 2 IR + emit paths for
GetTable with ArrayMixed/Float feedback + existing tableVerified cache.

### 1.1 Anatomy of matmul's inner loop

```
for i:
  ai := a[i]               // hoisted to j-loop preheader (ArrayFloat)
  for j:
    sum := 0.0
    for k:
      sum = sum + ai[k] * b[k][j]    // 3 GetTables per iter
```

Inner k loop (10M iterations at N=300):

| Op | Cost | Hoistable? |
|----|-----:|------------|
| `ai[k]` ArrayFloat GetTable | ~5 insns (kind guard + bounds + load) | NO — k varies |
| `b[k]` ArrayMixed GetTable  | ~8 insns (type check + bounds + load) | NO — k varies, returns *Table |
| `v = v[j]` ArrayFloat on b[k] | ~5 insns | NO — v33 changes per k |
| FMUL + FADD | ~2 insns (already raw-FPR) | — |

Total ~20 insns per iteration × 10M = 200M insns ≈ 100 ms. Matches
the measured 114 ms matmul wall time.

**tableVerified / shapeVerified caches:**
- tableVerified skips type/nil/metatable checks when the SSA value is
  already verified — active for ai within k loop, skipping ai's
  Table-ness recheck.
- But the ArrayKind check + floatArray pointer load happens EVERY
  inner iteration on ai. `asm.LDR(X2, X0, TableOffFloatArray)` is 1
  LDR × 10M = ~5 ms that could be hoisted.

### 1.2 What LuaJIT does

LuaJIT's matmul uses flat `ffi.new("double[?]", N*N)` or flat Lua
arrays. `b[k*N+j]` = 1 bounds check + 1 FLDR. That's 2 insns per
access vs our 8 (for b[k]) + 5 (for v[j]) = 13.

**The 5.4× gap is memory-pattern-bound.** To close most of it within
method-JIT scope, we'd need either:
- (A) **Dense 2D array runtime type**: a new Table kind storing a
  flat `[N×M]float64` with compile-time-fixed stride, JIT-emitted
  as `FLDR d0, [base, k*stride+j]`.
- (B) **b[k] row pointer caching across k**: impossible because k
  varies in innermost loop. Only helps if loop order is {i, k, j}
  instead of {i, j, k}. **Not a compiler job.**

Option (A) is what LuaJIT achieves via FFI. Option (B) is the
algorithm author's responsibility.

### 1.3 R29's incidental benefit

R29's struct field residency pass will hoist `ai`'s
`floatArray` pointer across the k loop as a side effect (it's
loop-invariant within k). Saves 1 LDR per k iteration ≈ 5 ms.

Conclusion of audit: matmul's remaining 5.4× gap is NOT closable
beyond ~5% within method-JIT scope without the DenseMatrix runtime
type. This ADR documents the design for future implementation.
**NOT ALREADY DONE, but requires multi-phase delivery.**

## 2. Decision

Introduce `ArrayDenseMatrix` as a new `ArrayKind` for Table, with the
following semantics:

```go
type Table struct {
    ...
    denseMatrix    []float64   // flat N*M row-major storage
    denseRowStride int         // columns (M)
    denseRowCount  int         // rows (N)
    ...
}
```

**Creation path:** runtime `NewDenseMatrix(rows, cols)` returns a
Table with `arrayKind = ArrayDenseMatrix`. Explicit API — user must
opt in (e.g. via a future `math.matrix(N, M)` or a compiler-inserted
conversion).

**Access path:** `t[i][j]` where `t.arrayKind == ArrayDenseMatrix`
is compiled to:
```
; tblVerified(t) already in X0
LDR X2, [X0, #TableOffDenseMatrix]        ; base ptr
LDR X3, [X0, #TableOffDenseRowStride]     ; stride (can be hoisted)
MADD X4, Xi, X3, Xj                       ; X4 = i*stride + j
LDR Xd, [X2, X4, LSL #3]                  ; load float64 bits
; 4 insns for the access, vs current 13
```

Stride is table-invariant once allocated → hoistable to enclosing
loop preheader.

## 3. Why this isn't trace JIT

The kind is a static runtime type, recognized at compile time via
feedback (Kind = ArrayDenseMatrix). No trace recording. The JIT emits
the same code shape for every site that accesses the kind — just
specialized to the known stride.

## 4. Phased delivery (HONEST)

This ADR does NOT land in R30's scope. Implementation requires:

- **Phase 1** (future): runtime `NewDenseMatrix` + new ArrayKind +
  promotion logic (detect `{}` + `m[i] = {}` pattern, promote to
  DenseMatrix on matching fill).
- **Phase 2** (future): Tier 2 emit for DenseMatrix GetTable/SetTable.
- **Phase 3** (future): compiler-side auto-conversion (opt-in via
  hint syntax or profile-driven).

Each phase is a separate multi-round effort. **For R30–R38 in this
session, matmul gets at most ~5% from R29's incidental hoisting.**

The user's goal (超过 luajit on matmul) is NOT achievable in this
session's method-JIT scope. Honest acknowledgment recorded.

## 5. Non-goals

- Automatic table-of-tables → DenseMatrix conversion without explicit
  hint. Too ambiguous in a general-purpose scripting language.
- SIMD (NEON 2-wide FMULd). Deferred to a separate ADR post-DenseMatrix.
- Trace JIT. Excluded by user direction.

## 6. Implementation deferred

No R32+ rounds in THIS phase implement DenseMatrix. The ADR is filed
for future session work. R30's deliverable is the architecture
document + honest disclosure that matmul is a multi-session project.

## 7. What R30-R38 CAN still do for matmul

- R29 struct field residency will benefit matmul's `ai.floatArray`
  pointer hoisting (~5%).
- R31 call specialization benefits recursion-heavy, not matmul.

**Realistic matmul projection for this session: 5.4× → ~5.1×.** Not
a meaningful close. Acknowledged.

## 8. Decision outcome

**Accepted as design document.** Implementation priority: LOW within
this session. Promoted to `forward_classes` as
`tier2-dense-matrix-kind`; revisit when nbody/ackermann work has
landed and there's appetite for runtime-type scope.
