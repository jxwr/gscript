# Token Reflection — 2026-04-11-tier1-selfcall-overhead

## Usage by Phase
```
SESSION                             TOTAL      INPUT    CACHE_W    CACHE_R     OUTPUT  CALLS
------------------------------ ---------- ---------- ---------- ---------- ---------- ------
ANALYZE + PLAN P                     6.9M        111     515.1K       6.2M     185.6K     76
  └ diagnostic sub-agent              1.7M         53     168.6K       1.5M      13.3K     38
  └ architecture audit sub-agent      1.7M        191      92.6K       1.6M       5.0K     56
  └ Research                          7.3M        123     165.6K       7.1M      31.1K    113
  └ Diagnostic sub-agent              3.0M         79     113.3K       2.9M      16.2K     69
VERIFY + DOCUMENT P                  6.6M        104     208.6K       6.3M      49.5K    100
  └ Evaluator                        118.8K         16      40.4K      76.7K       1.8K     10
IMPLEMENT P                         15.8M        229     794.4K      14.8M     256.6K    181
  └ Coder                           39.3M        436     909.1K      38.2M     132.1K    408

GRAND TOTAL                         82.5M
```

## Waste Points
- **IMPLEMENT / Coder (39.3M)**: Coder attempted and fully reverted Task 1 including writing SP-floor infrastructure, ExitStackOverflow handler, and two test files before hitting the goroutine stack constraint. All reverted. ~30M tokens wasted building code that couldn't work.
- **Research sub-agent (7.3M)**: Ran web searches on ARM64 stack growth and morestack semantics that were available in the codebase (Go runtime stack.go). External search was redundant given the answer was a one-line grep of `_StackMin` in the Go source.
- **ANALYZE two diagnostic sub-agents (4.7M total)**: Both agents ran ARM64 disasm + instruction counting. One was sufficient — the second was a redundancy from ANALYZE spawning both agents in the same session.

## Saving Suggestions
- **Coder abort earlier**: Coder made 3 test runs confirming the SIGSEGV before aborting. The first SIGSEGV should trigger immediate abort and revert with premise-error write. Saves ~20M tokens. **Risk: none** — the premise error would still be fully documented.
- **Skip external research when answer is in runtime source**: ANALYZE should grep Go runtime first (`grep _StackMin`) before spawning a web-search sub-agent for goroutine stack questions. Saves ~3M tokens, risk: low (local source may lag upstream, but _StackMin has been stable for years).
- **Single diagnostic sub-agent per ANALYZE phase**: Two disasm sub-agents ran in parallel. One result was sufficient for the plan. Saves ~1.7M tokens. **Risk: none if ANALYZE sees the first result before deciding to spawn the second**.
