# Fannkuch Tier2 gap diagnostic - 2026-04-27

## Scope

Investigated the current `fannkuch` slight regression/LuaJIT gap on
`bd5bd5c79ed573ccf5ed8244018a82c8655a7ac7` against the nearby references:

- `4165a8ea0d2ab3d2f6e4a9ca8e0ce67d430ed1e0`
- `d7c0248c4528bf0a72dcc37caa47581873bdf0ec`

Environment:

- Darwin arm64
- Go: `go version go1.25.7 darwin/arm64`
- LuaJIT: `/opt/homebrew/bin/luajit`

Primary commands:

```bash
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh fannkuch sort table_array_access

bash benchmarks/regression_guard.sh --runs=7 --timeout=90 \
  --bench fannkuch --bench table_array_access --bench sort \
  --json /tmp/guard_current_fann.json

/tmp/gscript_fann_final -jit -jit-stats -exit-stats \
  -jit-dump-warm /tmp/fann_final_dump -jit-dump-proto fannkuch \
  benchmarks/suite/fannkuch.gs
```

## Current observation

`fannkuch` is correct and Tier2-stable:

- VM: `0.561s`
- default JIT median: `0.054s`
- no-filter JIT median: `0.055s`
- LuaJIT: `0.020s`
- JIT/LuaJIT: `2.70x`
- Tier2: `attempted=1`, `entered=1`, `failed=0`
- checksum: `8629`

The requested focused diagnose on current main:

| Benchmark | VM | Default | No-filter | Tier2 |
|-----------|----:|--------:|----------:|-------|
| `fannkuch` | `0.562s` | `0.056s` | `0.056s` | `1/1/0` |
| `sort` | `0.181s` | `0.049s` | `0.054s` | default `1 entered, 2 failed`; no-filter `2 entered, 1 failed` |
| `table_array_access` | `0.429s` | `0.043s` | `0.046s` | `5/5/0` |

## Exit profile

The old "12 table exits" description matches `4165a8e`, not current
`bd5bd5c`/`d7c0248`.

Current `bd5bd5c` and `d7c0248` both report only 6 `ExitTableExit`s:

```text
1  proto=fannkuch exit=ExitTableExit id=1   pc=0   reason=NewTable
1  proto=fannkuch exit=ExitTableExit id=2   pc=1   reason=NewTable
1  proto=fannkuch exit=ExitTableExit id=3   pc=2   reason=NewTable
1  proto=fannkuch exit=ExitTableExit id=164 pc=121 reason=NewTable
1  proto=fannkuch exit=ExitTableExit id=166 pc=123 reason=SetField
1  proto=fannkuch exit=ExitTableExit id=168 pc=125 reason=SetField
```

`4165a8e` reports 12 exits. The additional exits are constructor-style
`SetTable`s while filling `perm1`, `count`, and `perm`:

```text
2  proto=fannkuch exit=ExitTableExit id=10 pc=9  reason=SetTable
2  proto=fannkuch exit=ExitTableExit id=12 pc=12 reason=SetTable
2  proto=fannkuch exit=ExitTableExit id=35 pc=24 reason=SetTable
```

So current main has already removed the hot issue named in the task. The
remaining exits are allocation/result-construction exits, not repeated table
access exits in the permutation loops.

## Warm dump / asm comparison

The optimized IR after type specialization is identical between `4165a8e`,
`d7c0248`, and current `bd5bd5c`. The performance movement comes from codegen
shape, not a different SSA graph.

Warm dump summary:

| Commit | Exits | Insns | Code bytes | Direct entry | Spills |
|--------|------:|------:|-----------:|-------------:|-------:|
| `4165a8e` | 12 | 3405 | 13620 | 12044 | 21 |
| `d7c0248` | 6 | 3611 | 14444 | 12868 | 21 |
| `bd5bd5c` | 6 | 3611 | 14444 | 12868 | 21 |

The added code is from the mixed/typed array append paths and precise mixed
fallback kind checks. That is a general table-write improvement for constructor
and append-heavy workloads, but it makes `fannkuch`'s compiled body larger:

- branches: `691 -> 782`
- data-processing-immediate: `968 -> 1024`
- loads: `540 -> 571`
- stores: `627 -> 636`

This explains the small `fannkuch` median movement despite fewer exits.

## Failed fix experiment

I tested a conservative variant that allowed native `ArrayMixed` append only
when feedback was unknown or explicitly mixed, sending typed-feedback mixed
appends back to runtime so the runtime could promote fresh tables to typed
arrays.

Result:

- `fannkuch`: worsened to `0.062s`, `3.10x` LuaJIT, 12 exits
- `table_array_access`: exits rose back near old levels (`2994`)
- `sort`: unchanged enough to not justify the change

That experiment should not be landed. It preserves typed promotion opportunity,
but the reintroduced exit/resume cost dominates here.

## Conclusion

No code fix was landed because the obvious general fix candidate regressed the
target and table workload. Current `fannkuch`'s remaining LuaJIT gap is not an
exit-storm problem: it is a generated-code-density/table fast-path shape issue.

The next useful general direction is a code-size and branch-count reduction in
`emitSetTableNative`:

- avoid emitting all four typed store bodies when `Aux2` is monomorphic and the
  fallback policy does not require them;
- split cold append/deopt continuations farther out of the hot in-bounds store
  path;
- keep constructor append wins, but make them less expensive for steady-state
  in-bounds stores.

That should be handled as a table-codegen cleanup rather than a
`fannkuch`-specific benchmark patch.
