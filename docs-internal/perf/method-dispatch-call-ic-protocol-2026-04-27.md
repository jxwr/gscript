# Method dispatch / SELF / cached direct entry diagnostic

Date: 2026-04-27
Base: `bd5bd5c79ed573ccf5ed8244018a82c8655a7ac7`
Branch: `codex/review-call-ic-formalization-r1`

## Existing suite targets

Relevant discovered benchmark files:

- `benchmarks/suite/method_dispatch.gs`
- `benchmarks/suite/string_bench.gs`
- SELF / direct-entry sensitivity checks:
  - `benchmarks/suite/fib_recursive.gs`
  - `benchmarks/suite/ackermann.gs`
  - `benchmarks/suite/mutual_recursion.gs`

## Finding

`KeepCachedDirectEntry` should be treated as a formal Tier 2 call-IC protocol, not as a one-off fallback. The required rule is:

1. A call-IC hit is valid only after the boxed closure value matches.
2. The cached direct-entry address must be refreshed from `FuncProto.DirectEntryPtr`.
3. If `DirectEntryPtr` is zero, refresh from `FuncProto.Tier2DirectEntryPtr`.
4. If both are zero, go to the slow path.

This preserves the important split between generic native callers and Tier 2 callers: clearing `DirectEntryPtr` for baseline/native caller safety must not force Tier 2-to-Tier 2 call ICs into `ExitCallExit` while the Tier 2 direct entry remains published.

## Implemented low-risk fix

Tier 2 call IC slots keep their existing two-word shape:

- boxed closure value
- direct-entry address

On a cache hit the emitter re-derives the raw closure and proto from the current boxed closure value, then refreshes the cached entry with the rule above. That avoids storing raw closure/proto pointers in the `[]uint64` cache while still applying the same direct-entry refresh protocol to tail calls.

## Validation

Tests:

```bash
go test ./internal/methodjit -run 'TestTier2(Call|TailCall|DirectEntry)|TestShouldPromoteTier2_MutualNumericUsesTier2EntryProtocol'
go test ./internal/methodjit -run 'Call|Tail|TCO|R107'
```

Benchmarks:

```bash
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh method_dispatch string_bench
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh fib_recursive ackermann mutual_recursion
```

Base vs branch results:

| Benchmark | Mode | Base | Branch | Note |
| --- | --- | ---: | ---: | --- |
| method_dispatch | default | 0.002s | 0.002s | flat, 1 Tier 2 entry, 0 failures |
| method_dispatch | no-filter | 0.002s | 0.002s | flat, 1 Tier 2 entry, 0 failures |
| string_bench | default | 0.031s | 0.029s | default has 0 Tier 2 entries |
| string_bench | no-filter | 0.025s | 0.022s | 1 Tier 2 entry, 0 failures |
| fib_recursive | default | 0.685s | 0.655s | flat, 2 Tier 2 entries, 0 failures |
| fib_recursive | no-filter | 0.672s | 0.653s | flat, 2 Tier 2 entries, 0 failures |
| ackermann | default | 0.017s | 0.015s | flat, 2 Tier 2 entries, 0 failures |
| ackermann | no-filter | 0.017s | 0.015s | flat, 2 Tier 2 entries, 0 failures |
| mutual_recursion | default | 0.015s | 0.015s | flat, 3 Tier 2 entries, 0 failures |
| mutual_recursion | no-filter | 0.015s | 0.015s | flat, 3 Tier 2 entries, 0 failures |

## Risk

Risk is low:

- IC hit validity is still guarded by boxed closure equality.
- Raw closure/proto pointers are re-derived from the current boxed closure value on each hit rather than stored in the untracked cache.
- Direct-entry staleness is guarded by the refresh rule before the call or tail jump.
- Existing non-tail protocol coverage remains, and a tail-call-specific protocol test was added.

Expected performance impact is neutral: this formalizes the entry refresh behavior and extends it to tail calls without changing the Tier 2 call-cache footprint.
