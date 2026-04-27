# Table mutation recovery/admission diagnostic - 2026-04-27

Scope: Task B, sort/quicksort restart-safe table mutation direction.

## Design

The self-recursive table-mutation gate is now backed by generic recovery
metadata instead of a flat "any SetTable blocks" scan.

Recovery classes:

- `idempotent-overwrite`: `t[k] = v` where `v` is the exact SSA value loaded by
  an earlier `t[k]` read in the loop block. This is restart/replay safe because
  it writes the same value back to the same slot. The hard gate admits this
  class under `GSCRIPT_TIER2_NO_FILTER=1`.
- `read-backed-overwrite`: a same-table/same-key read witness exists, but the
  stored value is different. This proves the mutation is likely an overwrite
  pattern, but replay can still change table contents, so it remains
  diagnostic-only.
- `none`: field writes, append, setlist, and SetTable without a read witness.

The implementation is intentionally not benchmark-specific. It analyzes
post-pipeline IR loop blocks and records recovery metadata for table mutation
sites. The production hard gate only admits the replay-safe class.

## Quicksort status

The quicksort swap loop is classified as `read-backed-overwrite`: both stores
have same-key read witnesses, but they write the opposite side of the swap.
That is useful progress over an opaque blocker, but it is not yet restart-safe.

Opening quicksort requires a stronger protocol, likely one of:

1. all table/key guards for the swap are checked before either store mutates the
   table, then both stores execute without later exit points;
2. mutation undo metadata for swap pairs; or
3. a runtime native-read witness bit that proves the prior read used the native
   in-bounds path, not an exit-resume path that could have observed a missing
   key.

Until one of those exists, quicksort remains blocked by the self-recursive
residual `SetTable` gate and the previous no-filter timeout path stays closed.

## Verification

Targeted tests:

```sh
go test ./internal/methodjit -run 'TestTier2ExitStormGateBlocksNoFilterRecursiveTableMutation|TestTableMutationRecoveryClassifiesQuicksortSwapAsDiagnosticOnly|TestTier2ExitStormGateAllowsNoFilterSelfRecursiveIdempotentTableOverwrite|TestTier2LoopGateAllowsNativeNumericSetTableLoop|TestTier2_Quicksort_Descending|TestMainT2_SortPattern' -count=1
```

Result: pass.

Package test:

```sh
go test ./internal/methodjit -count=1
```

Result: pass.

Sort diagnostic:

```sh
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh sort
```

Result:

| Mode | Time | Tier2 entered | Tier2 failed | Failures |
| --- | ---: | ---: | ---: | --- |
| VM | `0.191s` | - | - | - |
| default | `0.052s` | 1 | 2 | `<main>` loop-call gate; `quicksort` self-recursive residual `SetTable` gate |
| no-filter | `0.052s` | 2 | 1 | `quicksort` self-recursive residual `SetTable` gate |

Current dirty-worktree note: after the successful targeted/package runs above,
another worktree change outside this task left `internal/methodjit/pass_typespec.go`
with a build error:

```text
internal/methodjit/pass_typespec.go:217:1: missing return
```

That currently blocks re-running `go test ./internal/methodjit` and
`go test ./...` from a clean build in this shared worktree. This task did not
modify `pass_typespec.go`.

## Files

- `internal/methodjit/table_mutation_recovery.go`
- `internal/methodjit/tiering_manager.go`
- `internal/methodjit/tier2_exit_storm_gate_test.go`
