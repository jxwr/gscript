# ADR: Tier 2 polymorphic inline cache (IC upgrade)

**Status:** Proposed (R19 architecture round, 2026-04-17)
**Supersedes:** `adr-tier2-inline-cache.md` (R13) — revises the ADR
after reading the actual emit code. R13 underestimated what already
shipped; R19 rescopes the plan.
**Related:** `internal/methodjit/emit_table_field.go`,
`emit_table_array.go`, `internal/vm/feedback.go`, ledger class
`tier2-inline-cache`.

---

## 1. What R13 missed (existing state)

The existing code is MORE advanced than R13 implied. Every target
operation already has a monomorphic inline cache:

| Op         | Cache key              | Fast path                             | Miss path |
|------------|------------------------|---------------------------------------|-----------|
| OpGetField | `Aux2 = shape<<32\|idx` | shapeID guard + direct svals[idx] load | deopt → table-exit |
| OpSetField | `Aux2` same             | shapeID guard + direct svals[idx] store | deopt → table-exit |
| OpGetTable | `Aux2 = FBKind` (1-4)   | arrayKind guard + direct typed load   | deopt → table-exit |
| OpSetTable | same                   | arrayKind guard + direct typed store  | deopt → table-exit |

Feedback lives in `vm.TypeFeedback` (per-PC), recorded by the
interpreter. At Tier 2 compile, the graph builder bakes the feedback
into `Instr.Aux2`. On shape/kind mismatch at runtime, the emit branches
to a deopt label → exits Tier 2 and drops to Tier 1.

**This is already a monomorphic IC.** The R13 ADR treated the ground
as greenfield; it is not.

## 2. Actual gap

Three structural problems with the current scheme:

### 2.1 Monomorphic-only (max 1 shape per site)

`vm.TypeFeedback.Kind` is a single `uint8`. First mismatch flips it to
`FBKindPolymorphic` (0xFF), after which the Tier 2 compile bakes NO
guard (falls to generic cascade). Any site that sees ≥2 distinct
shapes over its lifetime permanently loses IC benefit.

Real-world: `method_dispatch` has 2-3 shapes per site in a typical
program. sieve has 1 (ArrayBool). matmul has 1 (ArrayFloat). So the
big gaps (sieve 8.4×, matmul 5.5×) are NOT from polymorphism; they're
from something else (see §3). But `object_creation`-style dispatch
WOULD benefit from polymorphic IC — currently it pays generic cascade
cost for any call site that sees vec3 plus particle.

### 2.2 No adaptive miss — guard miss = full Tier 1 drop

Currently a shape mismatch at an IC site causes:
1. Deopt path runs.
2. Tier 2 frame exits.
3. Tier 1 (or interpreter) takes over.
4. Function continues at Tier 1 unless re-promoted (R5/R6's gate
   logic owns re-promotion).

That's a HEAVY penalty for a single misprediction. LuaJIT-style ICs
handle a miss locally: update the cached shape in the inline code,
continue at full speed. No tier drop, no re-promote.

### 2.3 Feedback is frozen at Tier 2 compile time

`TypeFeedback` updates during Tier 0 / Tier 1 execution. Once the
function reaches Tier 2, feedback is baked into Aux2 and never
refreshed — new shapes discovered post-promotion don't adapt.

## 3. What is NOT in scope for R19 and why

The BIGGEST LuaJIT gaps are on:
- fib 59× (recursion / call)
- ackermann 44× (recursion / call)
- sieve 8.4× (tight loop with SetTable on ArrayBool — monomorphic)
- nbody 7.2× (tight numeric loop with ArrayFloat — monomorphic)
- spectral_norm 6.1× (same as nbody)
- matmul 5.5× (same as nbody)

Of these, NONE are caused by polymorphic IC gaps. Sieve/matmul/nbody
are already monomorphic; their gap is **loop-body efficiency** (R21
ADR). fib/ackermann are calls; their gap is **recursion/call cost**
(R20 ADR).

**R19 IC upgrade does not directly close any single-digit-LuaJIT-gap
benchmark.** Its value is:
- Future-proofing: unlocks benchmarks whose gap is currently MASKED
  by the FBAny/FBPolymorphic → no-IC path. E.g., method_dispatch at
  8-10% would plausibly close if polymorphic IC existed.
- Compositional: enables escape-analysis-scalar-replacement (forward
  class) which requires stable shape knowledge.
- Correctness: reduces deopt-reopt oscillation on polymorphic sites.

**Recommendation:** R20 (recursion) and R21 (tight loops) have bigger
expected LuaJIT-gap-closure impact. IC upgrade (R19) should be
LATER, possibly R24+ after R20/R21 tactical wins. R19's ADR is
accepted in principle but implementation deferred.

## 4. Design (when we do build it)

### 4.1 Storage

Extend `Instr.Aux2` / feedback slot to carry UP TO 4 cached shapes
per site. Encoding:

```
FeedbackSlot {
    kind      uint8       // 0=unobserved, 1-4=mono(kind), 5=poly(2-4), 0xFF=megamorphic
    shapes    [4]uint32   // up to 4 shapeIDs
    offsets   [4]uint16   // for GetField/SetField: field index per shape
    misses    uint16      // count of cache misses since last widen
}
```

Total: 4 + 16 + 8 + 2 = 30 bytes per site. At typical 10 IC sites
per hot function, overhead is ~300 bytes per function — negligible.

Storage location: NEW `IntMutableFeedback []FeedbackSlot` on
`vm.FuncProto`. The existing `Feedback []TypeFeedback` stays for
unmigrated ops.

### 4.2 Emit contract (per site)

```
(pseudocode for OpGetField polymorphic IC)

tbl = load(instr.Args[0])
check_is_table(tbl) ; deopt if not

shape = load(tbl.shapeID)

; polymorphic check — up to 4 comparisons
cmp shape, slot.shapes[0]
beq hit_0
cmp shape, slot.shapes[1]
beq hit_1
cmp shape, slot.shapes[2]
beq hit_2
cmp shape, slot.shapes[3]
beq hit_3

; all 4 missed → invoke the update path
b ic_miss

hit_0: load result from svals[slot.offsets[0]]; b done
hit_1: load result from svals[slot.offsets[1]]; b done
hit_2: ...
hit_3: ...
ic_miss:
  call runtime.ICMissHandler(slot_ptr, tbl, key)
  ; handler either:
  ;   - fills a free slot (kind < 4) and returns hit offset
  ;   - flips to megamorphic (kind = 0xFF)
  ; result is returned in X0
done:
```

Key properties:
- No deopt on miss. Handler returns a result and the site continues
  at full speed.
- Handler can update `slot.shapes[n]` in place — Go writes are fine;
  Tier 2 code re-reads slot on next entry.
- Megamorphic site falls through to `table-exit` once, similar to
  today's FBAny, but WITHOUT dropping the whole function to Tier 1.

### 4.3 ICMissHandler

Pure Go, runs in the JIT-caller goroutine (no sync needed; VM is
single-threaded). Signature:

```go
//go:noinline
func ICMissHandler(slot *FeedbackSlot, tbl *runtime.Table, key string) runtime.Value {
    // 1. compute offset for this (shape, key)
    // 2. try to fill free shapes[] slot
    //    - atomic-ish: write shapes[n] and offsets[n] before bumping kind
    // 3. if all 4 taken: set kind = megamorphic
    // 4. return the value
}
```

Note: no atomics needed (single-threaded VM), but writes must be in
the right order so that an interrupted handler leaves the slot in
a consistent state.

### 4.4 Invalidation

Shape change on a table → all ICs that cached that shape become stale
but SAFE (guard will miss and hit `ICMissHandler`). No invalidation
broadcast needed.

For SetField: the SetField itself may cause a shape transition. The
emit MUST re-read `tbl.shapeID` AFTER the store if any subsequent op
in the block depends on the shape.

### 4.5 Diagnostics integration

Add to `Diagnose()` output:
- Per IC site: (kind, shape counts, miss counts, miss rate).
- Round 6 diagnostic will need IC state to explain future regressions.

## 5. Implementation staging (deferred)

Each step is a tactical round. Pre-flight gates stated.

- **Step 1 (deferred): FeedbackSlot struct + Diagnose() integration.**
  No emit change. Pre-flight: `go test` regression; memory overhead
  measurement ≤ 500 B per hot function.
- **Step 2: ICMissHandler impl + call convention.** No emit yet, just
  the handler. Pre-flight: microbench ICMissHandler ≤ 200 ns (vs
  ~700 ns deopt path).
- **Step 3: emitGetField polymorphic guard cascade.** Gate: sieve /
  matmul / nbody unchanged (they're monomorphic, don't care);
  method_dispatch wall-time ≥ −5%.
- **Step 4: emitSetField.** Gate: same as step 3.
- **Step 5: OpGetTable / OpSetTable polymorphic kind cache.** Gate:
  sort −10% (R11's diagnosed drift path uses arrayKind dispatch).

## 6. Decision

**Accepted in principle.** Implementation priority is LOW (R24+);
R20 and R21 have higher expected LuaJIT-gap-closure impact. R19's
value is correcting R13's overscoped plan and clarifying that the
existing monomorphic IC is already shipping.

Key takeaway for future rounds: when an ADR says "build X," first
read the code to see if X is partly already built. R13 would have
benefited from that check.
