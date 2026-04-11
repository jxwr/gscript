# Token Reflection — 2026-04-11-transient-op-exit-classification

## Usage by Phase
```
SESSION                         TOTAL      INPUT    CACHE_W    CACHE_R     OUTPUT  CALLS
ANALYZE + PLAN                    8.2M        126     554.6K       7.5M     148.3K     86
VERIFY + DOCUMENT                 6.9M         97     232.0K       6.6M      45.1K     82
IMPLEMENT                         1.6M         47     112.3K       1.5M      13.2K     32
  └ Coder sub-agent             735.7K         38      55.1K     675.2K       5.3K     28

GRAND TOTAL                      17.4M
```

## Waste Points

- **VERIFY cache reads 6.6M / 82 calls** on a round that reverted one commit and did bookkeeping. `verify_dump.sh` produced a 59KB output that tripped the 2KB preview cap, forcing a second Read of the temp file. Then investigating the regression cost another ~20 tool calls because the initial full-package test failure only printed a runtime traceback — no `--- FAIL:` line to grep for, so I paged the log in chunks to locate the failing test.
- **ANALYZE 8.2M** is similar to R29. No obvious single waste point — this round did a full diagnostic re-read of R29's knowledge file, source reading of `handleNativeCallExit`/`tier1_manager.go`, and architecture audit. Within expected envelope for a planning round with a non-trivial control-flow hypothesis.
- **IMPLEMENT Coder 735K** landed a 49-line diff that was reverted. Not waste of the Coder's work — it's waste of the round's VERIFY cycle, which would have been cheap if the Coder had also run the full package. The curated correctness gate in the plan silently trained the Coder to treat the full-package test as optional.

## Saving Suggestions

- **Write verify_dump.sh output directly to a file and hand VERIFY the path instead of stdout** (stop re-reading through the preview cap). Est. saving: ~1.5M/round of VERIFY cache reads. Risk: none.
- **Cap `previous_rounds` in verify_dump.sh to the last 10 entries** — the tail is already summarized in INDEX.md. Est. saving: ~15% of ANALYZE input. Risk: low (if a sanity-check needs older rounds it can read state.json directly).
- **Add a grep-friendly failure marker to test output scraping**: VERIFY should run `go test ... | tee` and then `grep -E '--- FAIL|FAIL\s|panic:|fatal error'` on the saved log before paging. Would have shortened today's regression hunt from ~6 calls to 1. Est. saving: ~500K on rounds that hit regressions. Risk: none.
- **IMPLEMENT prompt rule change** (not a token saving, but directly prevents the kind of VERIFY waste this round incurred): every Coder task ends with `go test ./internal/methodjit/... -count=1` as the gating command, full stop. Curated subsets are development loops, not gates.
