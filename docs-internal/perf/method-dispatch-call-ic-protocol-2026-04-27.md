# Method dispatch / SELF / cached direct entry diagnostic

Date: 2026-04-27
Base: `93e25ff653d1cece3074a7c09b8b3a697c4413d0`
Branch: `codex/method-dispatch-ic-note`

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

## Implemented low-risk optimization

Tier 2 call IC slots now mirror the Tier 1 four-word shape:

- boxed closure value
- direct-entry address
- raw `*vm.Closure`
- raw `*vm.FuncProto`

That removes hit-path pointer extraction and closure-to-proto reloads, and applies the same direct-entry refresh protocol to tail calls. The memory cost is +16 bytes per Tier 2 call site.

## Validation

Tests:

```bash
go test ./internal/methodjit -run 'TestTier2(Call|TailCall|DirectEntry)|TestShouldPromoteTier2_MutualNumericUsesTier2EntryProtocol'
go test ./internal/methodjit
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
| string_bench | default | 0.031s | 0.032s | default has 0 Tier 2 entries |
| string_bench | no-filter | 0.025s | 0.024s | within noise, 1 Tier 2 entry |
| fib_recursive | default | 0.685s | 0.684s | flat, 2 Tier 2 entries, 0 failures |
| fib_recursive | no-filter | 0.672s | 0.659s | small noisy win |
| ackermann | default | 0.017s | 0.017s | flat, 2 Tier 2 entries, 0 failures |
| ackermann | no-filter | 0.017s | 0.017s | flat, 2 Tier 2 entries, 0 failures |
| mutual_recursion | default | 0.015s | 0.016s | noise-level change, 3 Tier 2 entries, 0 failures |
| mutual_recursion | no-filter | 0.015s | 0.016s | noise-level change, 3 Tier 2 entries, 0 failures |

## Risk

Risk is low:

- IC hit validity is still guarded by boxed closure equality.
- Cached raw closure/proto pointers are derived only on a successful typed miss path.
- Direct-entry staleness is guarded by the refresh rule before the call or tail jump.
- Existing non-tail protocol coverage remains, and a tail-call-specific protocol test was added.

Expected performance impact is small but general: fewer hit-path instructions at monomorphic Tier 2 call sites, with no material movement on the tiny benchmark timings above.
