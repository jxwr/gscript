# ADR: Tier 2 tight-loop strategy (matmul, nbody, spectral_norm, sieve)

**Status:** Proposed (R21 architecture round, 2026-04-17)
**Scope:** close the 5-8× LuaJIT gaps on tight numeric loops.
**Related:** `internal/methodjit/emit_table_array.go`,
`emit_table_field.go`, `pass_typespec.go`, `pass_scalar_promote.go`,
`loops.go`, `regalloc.go`.

---

## 1. The problem

| Benchmark     | GScript JIT | LuaJIT | Gap  | Dominant pattern                          |
|---------------|------------:|-------:|-----:|-------------------------------------------|
| sieve         | 0.084 s     | 0.010s | 8.4× | `arr[i] = 1` on ArrayBool, inner 1M loop  |
| nbody         | 0.239 s     | 0.033s | 7.2× | `bi.vx * bi.vx + bi.vy * bi.vy + ...`     |
| spectral_norm | 0.043 s     | 0.007s | 6.1× | `sum += u[j] * A(i,j)` inner N loop        |
| matmul        | 0.115 s     | 0.021s | 5.5× | `sum += ai[k] * b[k][j]` inner N loop      |
| fannkuch      | 0.046 s     | 0.019s | 2.4× | int array perm + reverse                   |

All are monomorphic at the access sites (we confirmed in R19) — so the
existing monomorphic IC is active. The gap is in what happens AFTER
the typed load: the value immediately re-enters the generic
type-dispatch arith path.

## 2. Current state audit (per R19 discipline)

### 2.1 Typed array access (good)

`emit_table_array.go:emitGetTableNative` already emits a direct
load from `intArray[key]` / `floatArray[key]` with a single arrayKind
guard when feedback is monomorphic. For matmul's `ai[k]`:

```
LDR  x2, [x0, #TableOffArrayKind]   ; load kind
CMP  x2, #AKFloat
BCND eq, floatArrayLabel            ; expected
...
LDR  x2, [x0, #TableOffFloatArray]  ; floatArray pointer
LDR  x0, [x2, x1, LSL #3]           ; float bits = floatArray[k]
```

This is ~4 instructions. **Good.**

### 2.2 Type propagation post-load (gap)

The loaded value is **NaN-boxed float bits stored in a GPR**
(`raw float64 bits are also NaN-boxed floats — no conversion needed`).
It reaches emit_arith.go's `emitFloatBinOp` via OpMul / OpAdd.

In matmul inner loop: `ai[k] * b[k][j]`:
```
v31 = GetTable ai, k    ; any (but ArrayFloat-typed under the hood)
v33 = GetTable b, k     ; any
v35 = GetTable v33, j   ; any
v36 = Mul v31, v35      ; ANY — not float!
v38 = Add v49, v36      ; ANY
```

The IR declares `v36`, `v38` as `any` because the typespec pass
doesn't propagate "ArrayFloat load → float result type." Each Mul
and Add then runs the generic `emitFloatBinOp`:
- Check lhs NaN-box tag (branch)
- Check rhs NaN-box tag (branch)
- Both-int fast path (unused)
- Float path: FMOVtoFP D0, FMOVtoFP D1, FMULd, FMOVtoGP
- Store NaN-boxed result

~10 extra instructions per Mul/Add purely for the type checks we
already know are float. **This is the gap.**

### 2.3 FPR residency (partial)

`emit_call.go:emitTypedFloatBinOp` DOES have a "raw float mode"
(line 395-413) that skips FMOVtoFP/FMOVtoGP when the result type is
`TypeFloat` and an FPR is allocated. This path is GATED on the
typespec pass marking the instruction's result type as `TypeFloat`.
For matmul we DON'T hit this because typespec leaves the arith as
`any`.

Fixing typespec → fixing this automatically.

### 2.4 LICM / invariant hoisting (partial)

`pass_scalar_promote.go` + `loops.go` hoist loop-invariant loads into
the preheader. Matmul's `ai := a[i]` should be hoisted above the k
loop. Let me verify by grepping the IR — B1 has `v13 = GetTable v0, v70`
(where v70 = i). That's b1[i], hoisted above B3's inner k loop. **Good.**

But: `GetTable v1, v43` (b[k] in the inner loop) is NOT hoistable —
it depends on k. It runs once per inner iteration.

### 2.5 Loop-carried register residency

`regalloc.go` tracks loop-carried values. `v49` (the accumulator,
float) is held in D4 across iterations — good. `v40` (k) is held in
X20 — good.

**Summary:** the infrastructure is 80% there. The missing 20% is
**type propagation** from typed-array loads into downstream arith.

## 3. Strategies in priority order

### 3.1 TypeSpec propagates ArrayFloat/ArrayInt/ArrayBool through arith (**HIGH, LOW cost**)

**Claim:** The typespec pass already recognizes `Int → AddInt`
specialization for integer-feedback arith. Extend it to:
- If GetTable's feedback Kind is FBKindFloat → result type = `float`.
- If result type of Mul/Add inputs is `float` → result type = `float`,
  and flag instruction as "raw-float mode eligible."
- emit_arith picks up the existing `emitTypedFloatBinOp` raw-float
  path.

**Savings:** removes 6-8 insns per float arith in matmul/nbody. On
matmul's ~27M inner-loop arith ops × 2-3 ns = ~60 ms per op reduction.
Projected matmul: 0.115 → ~0.07 s ≈ 40% improvement.
**New matmul LuaJIT gap: ~3.3× (from 5.5×).**

**Implementation cost:** small. typespec change + field propagation.

Pre-flight: microbench of matmul inner-loop body post-specialization
vs current. Target ≥30% wall-time improvement on matmul kernel
alone.

### 3.2 GetField returns typed result (**HIGH, LOW cost**)

**Claim:** `bi.vx` currently returns NaN-boxed Value (loaded from
svals[idx]). For ArrayFloat-backing fields (most of nbody's struct
fields), typespec should propagate "field is float" → downstream
arith stays in FPR.

nbody reads `bi.vx, bi.vy, bi.vz, bi.mass` in every iteration.
Currently each is boxed-float; each arith op hits the generic
dispatch.

**Savings:** nbody inner loop: ~20 arith ops / body pair. ~10M pair
iterations. 3-5 ns × 20 × 10M = ~1 second savings → nbody 0.239 →
~0.10 s ≈ 58%. **New nbody LuaJIT gap: ~3× (from 7.2×).**

**Implementation cost:** small. Needs field-type feedback
(`TypeFeedback.Result` — already exists for fields).

### 3.3 SetField/SetTable typed fast path (**MEDIUM, LOW cost**)

For nbody: `bi.vx = ...` stores float into field. Currently the
store NaN-boxes the FPR value (FMOVtoGP) and stores. If the field
is ArrayFloat-backed, a direct FSTR from the FPR is 1 insn.

**Savings:** moderate. 5-10% of nbody arith-heavy body.

### 3.4 Loop-bounds specialization for IntArray (**MEDIUM, MODERATE cost**)

**Claim:** sieve's hot loop is `for i := 2; i <= n; i++ { arr[i] = 1 }`.
The `i <= n` comparison is int-int (known). The `arr[i] = 1` is a
SetTable on ArrayBool with constant RHS. Currently:
- Load arrayKind, compare AKBool, branch.
- Load boolArrayLen, compare i < len, branch.
- Load boolArray ptr.
- STORE byte 2 (true) at ptr+i.

That's ~8 insns per iteration. LuaJIT's IC + bounds-check elision
does ~2. Elide the bounds check via INDUCTION VARIABLE range
analysis: i is bounded by [2, n]; if n ≤ len, NO bounds check per
iteration.

**Savings:** ~3× speedup on sieve inner loop. Projected sieve:
0.084 → ~0.03 s. **LuaJIT gap: 3× (from 8.4×).**

**Implementation cost:** moderate. Need an induction-variable
range analyzer or feedback-driven "no-overflow" assertion.

### 3.5 NEON / SIMD for dense float loops (**HIGH potential, HIGH cost**)

**Claim:** M-series ARM64 has NEON 128-bit SIMD. matmul's inner loop
is 2-wide parallelizable (2 float64 per 128-bit reg). LuaJIT does
NOT do SIMD on ARM; they rely on scalar speed. We'd be ahead if we
did.

**Savings potential:** 2× on matmul/nbody/spectral_norm inner loops.

**Cost:** HIGH. Requires:
- Dependency analysis to confirm loop-carried deps are minimal.
- SIMD-aware emit paths (not present today).
- 6-8 week implementation.

**Recommendation:** DEFERRED to a future ADR. Only consider AFTER
3.1-3.4 close the first-order gap.

### 3.6 Int-arith same treatment (**HIGH, LOW cost**)

For fannkuch / sort: integer-array + int arith. Same fix as 3.1 but
for int.

**Savings:** fannkuch 0.046 → ~0.025 s. **New gap: ~1.3×.**
**Cost:** trivial if 3.1 is done (extend the same pass to ints).

## 4. Composed projection

If R26-R28 land 3.1 + 3.2 + 3.3 + 3.6:

| Benchmark | Current | After ADR | LuaJIT | Final gap |
|-----------|--------:|----------:|-------:|----------:|
| sieve         | 0.084 | (needs 3.4 too) 0.03 | 0.010 | 3× |
| nbody         | 0.239 | 0.10  | 0.033 | 3× |
| spectral_norm | 0.043 | 0.018 | 0.007 | 2.6× |
| matmul        | 0.115 | 0.07  | 0.021 | 3.3× |
| fannkuch      | 0.046 | 0.025 | 0.019 | 1.3× |

Gaps compress from 2.4-8.4× to 1.3-3.3×. Closing further needs 3.5
(SIMD) or trace JIT.

## 5. Non-goals

- **Full SIMD codegen.** Deferred.
- **Induction-variable analyzer with overflow handling.** Only
  attempt after simpler wins; scope-escalate risk.
- **Runtime profile-guided relayout of tables.** Too structural.

## 6. Implementation staging

| Round | Step | Priority | Prereqs            |
|------:|------|:---------|:-------------------|
| R26   | 3.1 typespec float propagation | HIGH | none |
| R27   | 3.2 GetField typed result + FPR  | HIGH | R26 |
| R28   | 3.6 int arith propagation        | HIGH | R26 |
| R29+  | 3.3 SetField/SetTable typed      | MED  | R26-R28 |
| R30+  | 3.4 sieve bounds-check elision   | MED  | evidence |
| R32+  | 3.5 SIMD                         | LOW  | far-future |

## 7. Decision

**Accepted.** R26 takes step 3.1 first as the unlock for 3.2 and 3.6.
Pre-flight gate: matmul kernel microbench shows ≥30% improvement
with typespec-propagated float arith. If not, revise.

Projected combined effect of R22-R28 (recursion + tight-loop):
fib 59× → 21× gap, ackermann 44× → 13×, sieve 8.4× → 3×,
nbody/spectral/matmul 5-7× → 2.6-3.3×, fannkuch 2.4× → 1.3×.

This is not LuaJIT parity. It's a 2-3× step toward it, inside the
method-JIT substrate we have. Beyond this point, honest progress
requires trace JIT — the topic of a future architecture round when
we're ready to commit 1-2 engineer-years.
