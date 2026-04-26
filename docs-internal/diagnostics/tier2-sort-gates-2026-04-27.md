# Tier2 sort gate diagnostic - 2026-04-27

Scope: sort benchmark remaining Tier2 blockers:

- `make_random_array`: generic `OpMod` in loop
- `quicksort`: self-recursive loop with residual table mutation
- `<main>`: LoopDepth<2 loop-call gate

## Baseline observation

Before this change, `bash benchmarks/diagnose_tier2.sh sort fannkuch sum_primes table_array_access` reported for `sort`:

- default: `T2 entered=0`, failures:
  - `<main>`: LoopDepth<2 call in loop
  - `make_random_array`: generic `OpMod` in loop
  - `quicksort`: self-recursive loop residual `SetTable`
- no-filter: `T2 entered=1`, failures:
  - `make_random_array`: generic `OpMod` in loop
  - `quicksort`: self-recursive loop residual `SetTable`

Warm dump showed `make_random_array` had:

```text
v24 = Phi B4:v1, B1:v13 : any
v9  = Mul v24, 1103515245 : any
v11 = Add v9, 12345 : any
v13 = Mod v11, 2147483648 : any
v16 = SetTable arr, i, v13
```

The seed param only reached arithmetic through the Phi cycle, so the existing
direct-param guard heuristics did not mark it as int-like. Once guarded, the
pipeline can specialize the recurrence, then `OverflowBoxingPass` correctly
backs unsafe int48 arithmetic to boxed generic numeric ops. The generic `%`
emitter is native for int/float numeric operands, so the gate only needs to
block generic `%` when numeric provenance is not proven.

## Change

Implemented two generic unlocks:

1. Type specialization now inserts an int guard for LoadSlot params that seed a
   loop-carried integer recurrence through `Phi -> Add/Sub/Mul/Mod -> Phi`.
2. The Tier2 generic `OpMod` gate now allows loops where both operands are
   proven native numeric values through constants, numeric guards, phis, and
   numeric arithmetic.
3. `ArrayMixed` sequential append now has the same capacity-only native fast
   path as typed arrays. This avoids per-iteration table exits when boxed
   numeric overflow forces an array from typed storage back to mixed storage.

## Remaining blockers

`quicksort` remains blocked intentionally. The existing skipped repro
`TestTier2_Quicksort_LCG_N11` documents a correctness failure for LCG-generated
mixed numeric input at `N >= 11`. The successful descending-int control case is
not enough to open the production gate for the sort benchmark, whose input is
mixed due int48 overflow promotion in the LCG.

`<main>` remains blocked because one of its loop callees (`quicksort`) is still
not a native Tier2 loop-call candidate. No-filter compiling `<main>` was not a
clear win before this change and should remain diagnostic-only until the callee
set is fully native-safe.

## Post-change observation

After the change, the same diagnostic reported:

- `sort` default: `T2 entered=1`, failures only `<main>` and `quicksort`
- `sort` no-filter: `T2 entered=2`, failure only `quicksort`
- `fannkuch`: `T2 entered=1`, no failures
- `sum_primes`: unchanged default `<main>` read/write global loop gate
- `table_array_access`: `T2 entered=5`, no failures

Risk: medium for table writes because `ArrayMixed` append changes emitted code,
mitigated by only taking the fast path when key equals len, capacity is already
available, and both `imap` and `hash` are nil. Capacity growth and sparse cases
still exit to Go.
