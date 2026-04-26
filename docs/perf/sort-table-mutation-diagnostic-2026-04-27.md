# Sort Tier2 table mutation diagnostic - 2026-04-27

Branch: `codex/sort-table-mutation-20260427`
Base: `9b2887cd18fd345304016a208a51ea011237441f`
Worktree: `/tmp/gscript-agent-sort`

## Scope

Investigated the remaining `sort` / `quicksort` Tier2 gap around:

- loop table mutation gates
- mixed array table writes in quicksort's swap loop
- repeated table type/metatable/kind/bounds guards

No production optimization was committed in this round. The safe outcome is to
keep the current gates and record the negative evidence, because temporarily
opening the recursive table-mutation gate makes full `sort` time out.

## Baseline

Command:

```sh
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh sort
```

Result on `9b2887c`:

| Mode | Time | Tier2 entered | Tier2 failed | Failures |
| --- | ---: | ---: | ---: | --- |
| VM | `0.179s` | - | - | - |
| default | `0.049s` | 1 | 2 | `<main>` loop-call gate; `quicksort` self-recursive residual `SetTable` gate |
| no-filter | `0.048s` | 2 | 1 | `quicksort` self-recursive residual `SetTable` gate |

The current gate is therefore not hiding an obvious benchmark win in normal
mode; `make_random_array` and no-filter `<main>` already enter Tier2, while
`quicksort` remains Tier1.

## Trace

Command:

```sh
go build -o /tmp/gscript_sort_diag ./cmd/gscript/
rm -rf /tmp/gscript_sort_warm && mkdir -p /tmp/gscript_sort_warm
GSCRIPT_TIER2_NO_FILTER=1 /tmp/gscript_sort_diag \
  -jit -jit-stats -exit-stats \
  -jit-dump-warm /tmp/gscript_sort_warm \
  -jit-dump-proto quicksort benchmarks/suite/sort.gs
```

Observed output:

- `Sorted correctly: true`
- `Time: 0.047s`
- Tier2 entered: `<main>`, `make_random_array`
- Tier2 failed: `quicksort`
- Exit profile total: 119 exits
- `make_random_array` still has 48 `SetTable` exits from array growth; this is
  expected because the native append path only handles capacity-present appends.

`/tmp/gscript_sort_warm/quicksort.ir.after.txt` shows the remaining quicksort
loop shape:

```text
B4:
    v23  = GetTable    v0, v56 : any
    v25  = GetTable    v0, v34 : any
    v26  = SetTable    v0, v56, v25
    v27  = SetTable    v0, v34, v23
    v29  = Add         v56, v28

B6:
    v41  = GetTable    v0, v56 : any
    v43  = GetTable    v0, v59 : any
    v44  = SetTable    v0, v56, v43
    v45  = SetTable    v0, v59, v41
```

`quicksort.feedback.txt` reports all quicksort table sites as `kind=mixed`.
That means the hot swap loop is not a typed scalar `ArrayInt`/`ArrayFloat`
write. It is an `ArrayMixed` swap of arbitrary NaN-boxed values loaded from the
same table.

## Gate experiment

Temporary local probe only, not committed:

- changed `firstSelfRecursiveTableMutationInLoop` to return `(OpNop, false)`
- temporarily unskipped `TestTier2_Quicksort_LCG_N11`

Focused correctness tests passed under the probe:

```sh
go test ./internal/methodjit \
  -run 'TestTier2_Quicksort_LCG_N11|TestTier2_Quicksort_Descending|TestMainT2_SortPattern' \
  -count=1
```

Result:

```text
ok github.com/gscript/gscript/internal/methodjit 0.927s
```

But the full benchmark regressed catastrophically:

```sh
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh sort
```

With the recursive table-mutation hard gate disabled:

| Mode | Time | Tier2 entered | Tier2 failed | Failures |
| --- | ---: | ---: | ---: | --- |
| VM | `0.182s` | - | - | - |
| default | `0.048s` | 1 | 2 | `<main>` loop-call gate; `quicksort` LoopDepth<2 `SetTable` performance gate |
| no-filter | `TIMEOUT` | 0 | 0 | none printed before timeout |

The default performance gate would still block quicksort, but no-filter proves
that compiling the recursive mixed-array swap loop is currently unsafe as a
performance change. The hard gate should remain as a guardrail until recursive
mixed table mutation has a dedicated implementation strategy.

## Diagnosis

The remaining gap is not primarily duplicate guard overhead inside the existing
emitted quicksort loop, because quicksort is not emitted in production. The
first-order blocker is that recursive quicksort with mixed table swaps does not
have a viable Tier2 execution model yet.

Current guard caches already address the local duplication class:

- `tableVerified` skips repeated table type/nil/metatable checks within a block.
- `kindVerified` can skip repeated `ArrayMixed` kind guards within a block.
- `keysDirtyWritten` elides repeated idempotent `keysDirty = 1` writes.
- Single-predecessor propagation carries these states across straight-line
  blocks, while loop headers and merge points reset conservatively.

The quicksort swap loop still needs four dynamic mixed table accesses per swap,
and its recursive call shape makes no-filter Tier2 pathologically slow. A
general next step should be a recursive-safe mixed array swap strategy, not a
benchmark-specific gate exception.

## Recommended next work

1. Add a production diagnostic counter for `SetTable` fast-path miss reason
   (`kind mismatch`, `bounds/capacity`, `metatable`, `hash/imap`, `value type`)
   so mixed swap loops can be measured without patching gates.
2. Model an explicit "same-table mixed array swap" metadata class only if it can
   prove both stores are in-bounds before either mutation. That would avoid
   partial mutation on later deopt and is more precise than allowing all
   recursive `SetTable`.
3. Keep `firstSelfRecursiveTableMutationInLoop` and the LoopDepth<2 `SetTable`
   performance gate in place until full `sort` no-filter no longer times out.

## Verification

Final, unmodified production code:

```sh
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh sort
```

Result:

| Mode | Time | Tier2 entered | Tier2 failed | Failures |
| --- | ---: | ---: | ---: | --- |
| VM | `0.186s` | - | - | - |
| default | `0.055s` | 1 | 2 | `<main>` loop-call gate; `quicksort` self-recursive residual `SetTable` gate |
| no-filter | `0.052s` | 2 | 1 | `quicksort` self-recursive residual `SetTable` gate |

Targeted tests:

```sh
go test ./internal/methodjit \
  -run 'TestTier2ExitStormGateBlocksNoFilterRecursiveTableMutation|TestTier2LoopGateAllowsNativeNumericSetTableLoop|TestLoopCallGateKeepsQuicksortBlocked|TestTier2_Quicksort_Descending|TestMainT2_SortPattern' \
  -count=1
```

Result:

```text
ok github.com/gscript/gscript/internal/methodjit 0.782s
```

Full suite:

```sh
go test ./...
```

Result: failed in `github.com/gscript/gscript/gscript` at
`TestPool_concurrent` with `fatal error: concurrent map writes` during stdlib
table construction (`runtime.Table.RawSetString`). The changed file in this
round is documentation only; the target `internal/methodjit` package passed.

## Files changed

- `docs/perf/sort-table-mutation-diagnostic-2026-04-27.md`
