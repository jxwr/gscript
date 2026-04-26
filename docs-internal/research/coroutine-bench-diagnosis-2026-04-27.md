# coroutine_bench diagnosis, 2026-04-27

Branch: `codex/coroutine-bench-diagnosis-20260427`

Base commit: `8fd629dbbfd96904607f1fdb8946f8a8ccd3daed`

## Summary

`coroutine_bench` is not currently a bad Tier 2 codegen case. Production runs do
not attempt Tier 2 at all, and the Tier 2 exit profile is empty. The benchmark is
dominated by VM/runtime coroutine mechanics: two long tests repeatedly hand
control between goroutines via channels, so Go scheduler wake/sleep cost hides
almost all bytecode/JIT differences.

The only cheap part is `test_create_resume`, because the current VM already has
a `leafNoCall` fast path for one-shot coroutine bodies with no calls. That path
avoids launching a coroutine goroutine and runs in roughly 20-30 ms for the
benchmark's 50k short-lived coroutines.

## Reproduction data

Targeted single-shot diagnosis after reverting experiments:

```text
TIMEOUT_SEC=180 bash benchmarks/diagnose_tier2.sh coroutine_bench

| coroutine_bench | vm        | 1.589s | - | - | Time: 1.589s |
| coroutine_bench | default   | 1.619s | 0 | 0 | Time: 1.619s |
| coroutine_bench | no-filter | 1.595s | 0 | 0 | Time: 1.595s |
```

Five-run regression guard on the same base commit:

```text
python3 benchmarks/regression_guard.py \
  --runs 5 --timeout 180 --bench coroutine_bench --no-luajit \
  --json /tmp/gscript_coroutine_guard_before.json

VM median:        2.422s
default median:   1.619s
no-filter median: 1.856s
T2 a/e/f:         0/0/0
Tier2 exits:      0
Regressions:      0
```

The variance is high because the benchmark is scheduler dominated. Depending on
the run, default JIT can be slightly slower than VM or about 1.5x faster, but in
both cases no Tier 2 code runs.

## Warm JIT evidence

Command:

```bash
rm -rf /tmp/gscript_coroutine_warm
go build -o /tmp/gscript_coroutine_diag ./cmd/gscript
/tmp/gscript_coroutine_diag \
  -jit -jit-stats \
  -jit-timeline /tmp/gscript_coroutine_timeline.jsonl \
  -jit-dump-warm /tmp/gscript_coroutine_warm \
  benchmarks/suite/coroutine_bench.gs
```

Observed timeline:

```text
tier1_compile <main>             call_count=1 reason=not_ready_for_tier2
tier1_compile test_yield_loop    call_count=1 reason=not_ready_for_tier2
tier1_compile test_create_resume call_count=1 reason=not_ready_for_tier2
tier1_compile test_generator     call_count=1 reason=not_ready_for_tier2
```

Warm manifest:

```text
<anonymous>        status=not_attempted call_count=0 observed_feedback=0
<anonymous>        status=not_attempted call_count=0 observed_feedback=0
<anonymous>        status=not_attempted call_count=0 observed_feedback=0
<main>             status=not_attempted call_count=1 observed_feedback=10
test_create_resume status=not_attempted call_count=1 observed_feedback=2
test_generator     status=not_attempted call_count=1 observed_feedback=1
test_yield_loop    status=not_attempted call_count=1 observed_feedback=2
```

This explains why `GSCRIPT_TIER2_NO_FILTER=1` does not help: the coroutine body
closures run in child VMs created by `newChildVM`, and those child VMs do not
inherit the parent's Method JIT engine. Their protos stay at `call_count=0`.

## CPU profile

Commands:

```bash
/tmp/gscript_coroutine_diag -jit \
  -cpuprofile /tmp/gscript_coroutine_jit.prof \
  benchmarks/suite/coroutine_bench.gs
go tool pprof -top /tmp/gscript_coroutine_diag /tmp/gscript_coroutine_jit.prof

/tmp/gscript_coroutine_diag -vm \
  -cpuprofile /tmp/gscript_coroutine_vm.prof \
  benchmarks/suite/coroutine_bench.gs
go tool pprof -top /tmp/gscript_coroutine_diag /tmp/gscript_coroutine_vm.prof
```

Top JIT samples:

```text
58.46% runtime.pthread_cond_wait
36.61% runtime.pthread_cond_signal
 4.33% runtime.usleep
```

Top VM samples are nearly identical:

```text
56.02% runtime.pthread_cond_wait
40.24% runtime.pthread_cond_signal
 2.37% runtime.usleep
```

The profile has almost no useful GScript VM/JIT frames because the dominant cost
is parking and waking goroutines for `yield`/`resume`.

## Experiment not kept

I tested a small local patch that let coroutine child VMs inherit the parent
Method JIT and switched the shared TieringManager `callVM` pointer across
yield/resume boundaries.

Result:

```text
Tier 1 compiled: 6 functions instead of 4
Tier 2 attempted: 0
5-run median default: 1.619s -> 1.608s
```

This is less than 1% median improvement and requires dynamic mutation of a
shared JIT engine's `callVM` pointer. That is not a good low-risk tradeoff. The
experiment was reverted and is not included in this branch.

## Conclusion

No production code patch is recommended in this round.

Low-risk explanation:

- `test_yield_loop` and `test_generator` are scheduler/channel benchmarks more
  than bytecode benchmarks.
- `test_create_resume` already benefits from the existing `leafNoCall`
  coroutine fast path.
- Tier 2 does not run, and no-filter cannot help without a larger JIT/VM
  ownership change for child VMs.

Next reasonable design work, if this benchmark becomes a priority:

1. Introduce a JIT-safe per-execution call context instead of a single mutable
   `MethodJITEngine.SetCallVM` pointer, then allow child VMs to use JIT safely.
2. Consider a stackful/in-VM coroutine implementation for GScript closures so
   `yield`/`resume` does not require Go goroutine scheduler handoff per value.
3. Keep the current `leafNoCall` path for short-lived non-yield coroutines; it is
   already the right low-risk specialization.
