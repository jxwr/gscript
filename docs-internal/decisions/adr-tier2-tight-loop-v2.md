# ADR: Tier 2 tight-loop strategy — v2 (reshape after R24 data)

**Status:** Proposed (R25 architecture round, 2026-04-17)
**Supersedes:** `adr-tier2-tight-loop.md` (R21) — main premise disproven.
**Related:** R24 diagnostic, `internal/methodjit/emit_table_field.go`,
`emit_table_array.go`, `pass_typespec.go`.

---

## 1. What R21 got wrong

The R21 ADR claimed: "typespec doesn't propagate types from typed-array
GetTable/GetField to downstream arithmetic → emitFloatBinOp takes the
generic path instead of raw-FPR." Projected: 40% on matmul/nbody.

R24 dumped production feedback. Findings:

- matmul inner-loop GetTables (ai[k], b[k][j]): **already have
  Res=FBFloat**. Existing `feedbackToIRType(fb.Result)` inserts
  OpGuardType TypeFloat. TypeSpec rewrites OpMul → OpMulFloat.
  emit picks the raw-FPR path.
- nbody energy() GetFields (bi.mass, bi.vx, ...): **all Res=FBFloat**.
  Already guarded. ShapeVerified cache dedups redundant guards across
  consecutive fields of the same table.

The R21 gap doesn't exist in production. R23's "fix" would have fired
on zero sites and added nothing.

## 2. Where the 5-8× gaps actually come from

### 2.1 matmul (5.5×): nested-table memory pattern

Source:
```
sum = sum + ai[k] * b[k][j]
```

Per inner-loop iteration:
- `ai[k]` — 1 GetTable into an ArrayFloat (~6 insns: guard kind, bounds,
  load array ptr, index, box). ai is hoisted to the j-loop preheader.
- `b[k]` — 1 GetTable into a generic table (returns *Table). ~8 insns.
- `b[k][j]` — 1 GetTable on that inner table (~6 insns).
- 1 FMULd + 1 FADDd.

Total ~22-25 insns per inner iteration for reads alone. Plus register
movement around FPR residency. Plus loop bookkeeping.

LuaJIT's matmul uses arrays-of-floats at the language level (no nested
tables). `b[k*N+j]` = 1 bounds check + 1 load. 3× fewer accesses per
iteration.

**This is not a typespec issue. It's a memory layout issue.**
GScript's `{}` literal for a row always creates a Table with overhead
(GC header, metadata, shape field). A flat []Value would be much
denser but requires a new runtime type.

### 2.2 nbody (7.2×): object-of-floats pattern

Source:
```
e = e + 0.5 * bi.mass * (bi.vx * bi.vx + bi.vy * bi.vy + bi.vz * bi.vz)
```

ShapeVerified cache already handles this: first `bi.mass` does the full
shape check; `bi.vx`, `bi.vy`, `bi.vz` skip checks. Each GetField is
~3-4 insns post-dedup.

LuaJIT's nbody uses FFI structs or tightly-packed tables. Same struct
access but without GetField dispatch at all — direct field offsets
baked into the trace. That's a trace JIT feature.

**Not easily closed within method JIT scope.**

### 2.3 sieve (8.4×): tight array write loop

Source: `for i := 2; i <= n; i++ { arr[i] = 1 }`

Per iteration: ~8 insns (SetTable with ArrayBool kind guard + bounds +
store). LuaJIT: 1 bounds check + 1 store + 1 increment + 1 compare.

Possible wins:
- Bounds check elision via induction-variable range analysis (sieve's
  i is bounded [2, n]; if n ≤ boolArray.len, no bounds check needed).
  Medium-complex but tractable.
- ArrayKind cache: if this site always hits ArrayBool, skip the kind
  check. Already done (Aux2 carries FBKindBool).

**Bounds-check elision is the most tractable single tight-loop win left.**

### 2.4 spectral_norm, fannkuch: mid-gap

Spectral and fannkuch sit at 2.4-6.1×. Each has their own pattern
(numerical recurrence, permutation). Analysis not done; likely similar
mix of memory-pattern + dispatch-overhead issues.

## 3. What R21 ADR's 3.1-3.6 become after R24

| R21 step | Status after R24 | Disposition                                |
|----------|------------------|--------------------------------------------|
| 3.1 typespec float prop | DONE in prod | Close out; no work needed |
| 3.2 GetField typed | DONE in prod  | Close out; no work needed |
| 3.3 SetField typed | Mostly done   | Minor follow-up; defer to an IC round |
| 3.4 sieve bounds-elision | Tractable | **R26 candidate** |
| 3.5 SIMD | Deferred       | Out of scope |
| 3.6 int-arith prop | Partial   | Lower priority, defer |

## 4. The honest projection for remaining gaps

LuaJIT's advantage on tight-loop benchmarks is largely **architectural**:
- Trace JIT specializes end-to-end; guards are elided across traces.
- FFI / packed data structures bypass table overhead.
- SSA + trace specialization allow cross-call inlining that method JIT
  can't replicate.

Inside the method-JIT box:
- sieve bounds-elision: achievable, maybe 2× (8.4× → 4.2×).
- nested-table layout: large structural change, not a single round.
- nbody struct access: minimal low-hanging fruit remains.

**Revised projection**: if R26+ lands sieve bounds-elision, sieve
closes ~2×. matmul/nbody remain at current gaps (5-7×) pending
a future "dense data layout" architecture round — that's a 6-12 month
project, out of this phase's scope.

## 5. Decision

**Reshape ADR ACCEPTED.** Close out the original R21 ADR's premise.
The remaining tight-loop rounds in this session (R26-R28) should:

1. **R26** — attempt sieve bounds-check elision via induction-variable
   analysis (tractable single-round win). OR pivot to something
   completely different.
2. **R27** — tactical or diagnostic based on R26 outcome.
3. **R28** — final session retrospective.

**IMPORTANT lesson recorded to workflow-evolution class**: architecture
round's current-state audit is load-bearing. R21 projected 40% gains
based on an unverified assumption about the current pipeline. R24's
one-test diagnostic would have cost 30 min of R21 time and saved
R23's broken implementation round.

Retroactive gate: any future architecture round MUST include a
§1 "Current state audit" that produces at least one concrete
production measurement disproving the null hypothesis "this is
already done." Adding this as a rule in ledger's workflow-evolution
mitigation_description.
