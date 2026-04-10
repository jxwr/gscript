# Token Reflection — 2026-04-11-measurement-repair

## Usage by Phase
```
SESSION                             TOTAL      INPUT    CACHE_W    CACHE_R     OUTPUT  CALLS
ANALYZE + PLAN                       7.6M         92     382.2K       7.2M      81.2K     67
  └ Diagnostic sub-agent             1.9M         71      98.0K       1.8M      10.5K     65
  └ Research                         3.0M         85     143.0K       2.9M      13.3K     81
IMPLEMENT                           23.0M       1.4K     293.9K      22.6M     152.7K    235
VERIFY + DOCUMENT                   12.4M        171     249.1K      12.1M      82.7K    163

GRAND TOTAL                         47.9M
```

## Waste Points
- **IMPLEMENT (23M, 235 calls)**: Bash scripting for the median runner likely required 3-4 iterations to get awk parsing and sort-and-pick-median correct. 235 calls for 4 tasks (~40 lines of bash + small Go changes) is high.
- **VERIFY crash investigation**: Pre-existing TestDeepRecursion crash consumed ~1M tokens before git-stash confirmation. Should have stashed immediately on first appearance of JIT stack crash.
- **Research sub-agent (3M, 81 calls)**: V8/LuaJIT deopt PC prior-art is findable in 2-3 targeted fetches (lj_snap.c, simplified-lowering.cc). 81 calls over-indexed on breadth.

## Saving Suggestions
- **Bash iteration limit in IMPLEMENT prompt**: add "max 3 attempts for any bash task; if still failing, write a minimal failing case and report" — saves ~3-5M on harness rounds. **Risk: low.**
- **VERIFY crash protocol**: add rule "on JIT stack crash, run `git stash && go test -run <failing test>; git stash pop` immediately before any further investigation" — saves ~1M per occurrence. **Risk: none.**
- **Research cap**: limit web fetches to 5 per prior-art query; check `opt/knowledge/` first — saves ~1.5M. **Risk: low.**
