# ADR: Tier 2 Inline Cache for field/table access

**Status:** Proposed (R13 architecture round, 2026-04-17)
**Supersedes:** nothing — new architectural addition.
**Related:** R3 diagnostic (sieve emit walk), R11 diagnostic (sort
drift), `kb/modules/runtime/table.md`, `docs-internal/decisions/
adr-tier2-design.md`.

---

## Context

Two diagnostics have converged on the same structural missing piece:

- **R3 (sieve)**: emit-layer micro-patches can close at best ~2% of
  the 8.8× LuaJIT gap on sieve. The actual gap comes from the
  per-access `GetTable`/`SetTable` dispatch through arrayKind + shape
  lookup + RawGetInt switch.
- **R11 (sort)**: the same pattern causes ~14% drift on sort vs the
  frozen reference. Shape system + typed-array dispatch adds a
  switch to every integer-keyed access.

The emit-layer classes (`emit-layer-micro-optimization`) have accrued
2 holds + 0 wins in the ledger. That class is structurally the wrong
abstraction for closing the LuaJIT gap — the gap is dispatch, not
instruction count.

LuaJIT closes this gap with **inline caches (ICs)**: each access site
remembers which concrete shape/arrayKind it saw, guards on that
identity, and emits a specialized load. On a miss the site falls
through to a generic path.

## Decision

Introduce a **Tier 2 inline-cache scheme** for the following opcodes:
- `OpGetField` / `OpSetField` (string-keyed property access)
- `OpGetTable` / `OpSetTable` (integer or arbitrary key)

Each IC site has:
- **Guard**: compare a 32-bit identifier (shapeID for fields,
  arrayKind<<24|shapeID for tables) against a cached slot value.
- **Hit path**: direct memory load/store from a precomputed offset
  (for fields) or from `t.intArray[key]` / `t.array[key]` (for tables).
- **Miss path**: fall through to the current generic dispatch path,
  updating the cached slot to the new identifier *if the site has
  not yet polymorphed*.

Sites start in **empty** state (no cached identifier), transition to
**monomorphic** on first hit, and freeze as **polymorphic** after
a configurable number of misses (initial: 3). Polymorphic sites skip
the guard and go directly to the generic path.

## Where the cache lives

Two candidates:

1. **Side table indexed by PC.** Each Tier 2 proto carries a
   `[]icSlot` with one entry per access site. Emit resolves PC →
   index at compile time; hit path reads the side table with a
   constant offset.

2. **Embedded in emitted code.** Guard immediate and offset patched
   into the instruction stream at compile time. First miss rewrites
   the immediate (self-modifying code; requires `sys_mprotect`).

Choose **option 1** for v1: simpler, no codegen rewrite, composes
with Wave 2 debugging tooling (`Diagnose()` can dump IC state).
Option 2 can be revisited for v2 if the extra memory load shows up
as a cost in diagnostics.

## Composition with existing mechanisms

- **R5 Tier 0 gate**: IC only applies to Tier 2. Tier 0 interpreter
  unchanged — a proto routed to Tier 0 doesn't pay IC overhead.
  This is consistent with R9's observation that runtime-level
  changes compose upward; tier-level changes compose with gates.
- **Shape system**: fields already have `shapeID`. The guard simply
  compares `t.shapeID == cached_shapeID`. Miss → `clearShape` or
  `applyShape` path unchanged.
- **Typed arrays (ArrayInt/Float/Bool)**: guard identifier is
  `arrayKind << 24 | 0` (shapeID=0 for array-mode). Miss on
  `demoteToMixed` transitions.
- **Correctness**: IC is a pure speedup when the guard is sound.
  The guard value fits in 32 bits and is computed from observable
  Table state, so it's safe to cache.

## Expected impact

From R3's structural finding (closing LuaJIT gap structurally, not
micro-patching) and R11 (sort +14% drift reversible by bypassing
arrayKind dispatch):

- **sieve**: estimated 2-3× speedup (from 8.8× gap). The benchmark
  is dominated by a tight `arr[i] = 1` loop — monomorphic
  `SetTable` on ArrayBool.
- **sort**: expected to close the +14% drift on `arr[i] <= pivot` /
  `arr[i] = arr[j]` patterns.
- **matmul**: moderate (float multiply dominates, not access).
- **method_dispatch**: significant — `obj.x + obj.y + obj.z`
  compiles to three IC-able GetField sites.

Risks:

- **Code size growth**: guard + hit + miss path per access triples
  emit bytes in the worst case. File-size-guard already enforces
  1000-line limit on Go files; IC implementation must split into
  multiple files from the start.
- **Cache pollution**: polymorphic-freeze threshold too low → sites
  freeze before useful caching; too high → miss cost dominates.
  Start at 3 misses, tune with diagnostics.

## Implementation plan (to be scheduled across future tactical rounds)

This ADR is scoped to DECISION only. Implementation breakdown:

1. **IC slot table in FuncProto** (runtime package change).
2. **IC emit for OpGetField monomorphic hit** (Tier 2 emit change).
3. **Extend to OpSetField** + tests for shape invalidation.
4. **OpGetTable/OpSetTable with arrayKind guard** — composes with R11's
   finding.
5. **Polymorphic freeze threshold + diagnostics integration**.

Each step is one tactical round under v5. Pre-flight for step 1
measures IC slot load cost (<5 ns on M4 Max) + guard-check cost
(<3 ns). Step 1 is gated on those pre-flight numbers; if guard check
costs more than the expected savings per site, the ADR is reconsidered.

## Ledger connection

- Promotes `tier2-inline-cache` from `forward_classes` to `classes`
  at implementation-round time (R14+ when the first tactical round
  on this plan lands, or when ADR is acknowledged).
- Supersedes the `emit-layer-micro-optimization` class for the
  sieve/sort/method_dispatch target set. That class remains in the
  ledger with the negative evidence recorded.
- Pairs with `tier2-typed-array-ic` (R11's forward class entry):
  tier2-typed-array-ic is the first integer-key specialization of
  this general IC scheme.

## Decision outcome

**Accepted** as the strategic answer to the LuaJIT-gap question.
Execution is multi-round; do not attempt a single-round landing.
First implementation round is R14 or later.
