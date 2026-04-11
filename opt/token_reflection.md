# Token Reflection — 2026-04-11-fib-regression-root-cause

## Usage by Phase
```
SESSION                             TOTAL      INPUT    CACHE_W    CACHE_R     OUTPUT  CALLS
ANALYZE + PLAN                       7.4M        118     496.5K       6.8M      76.0K     78
  └ diagnostic sub-agent             7.0M        127     202.8K       6.7M      34.8K    121
VERIFY + DOCUMENT                    4.8M         65     174.2K       4.6M      27.3K     55
  └ Evaluator sub-agent            154.4K         18      69.4K      83.4K       1.6K     10
IMPLEMENT                            1.5M         41      59.7K       1.4M      12.3K     31

GRAND TOTAL                         20.8M
```

## Waste Points

- **ANALYZE diagnostic sub-agent: 7.0M tokens / 121 tool calls** for what ended up as a single instrumentation run (`handleNativeCallExit` fires=1). The sub-agent appears to have explored multiple dead hypotheses (int-spec deopt, EvictCompiled, DirectEntryPtr timing) before landing on the cold-GETGLOBAL trigger. Most of the 121 calls were reading source files and running repeated diagnostic probes rather than converging on the single decisive counter.
- **VERIFY at 4.8M** is high for a round with no production code and one test-file diff. Much of it is repeatedly re-reading the 80KB verify_dump.sh output because the file exceeded the 10K-token Read limit and had to be paged in 400-line chunks.

## Saving Suggestions

- **ANALYZE sub-agent prompt should lead with the decisive experiment**, not the hypothesis space. A single-line ask — "instrument `handleNativeCallExit` with a counter, run fib(35) and ack(3,4), print counts" — costs ~20K tokens. Letting the sub-agent explore costs 7M. For root-cause rounds where the symptom is already clear, the parent should pre-pick the experiment. Saving: ~5M/round. Risk: medium (parent might pick the wrong experiment; mitigation: budget a second experiment if the first comes back null).
- **`verify_dump.sh` output is 80KB** and exceeds the single Read call limit. Split into scoped dumps (`verify_dump_plan.sh`, `verify_dump_state.sh`, `verify_dump_bench.sh`) so VERIFY can load only the section it needs for a given step. Saving: ~1-2M/round. Risk: none.
- **Evaluator at 154K is healthy** — keep Sonnet for this role. No change.
