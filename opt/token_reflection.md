# Token Reflection — 2026-04-11-tier1-selfcall-constants-str

## Usage by Phase
SESSION                             TOTAL      INPUT    CACHE_W    CACHE_R     OUTPUT  CALLS
------------------------------ ---------- ---------- ---------- ---------- ---------- ------
ANALYZE + PLAN P                     5.4M         76     431.0K       5.0M      41.1K     51
VERIFY + DOCUMENT P                  4.9M         83     175.5K       4.7M      48.6K     79
IMPLEMENT P                         17.0M        215     400.7K      16.4M     238.8K    195
IMPLEMENT P (R27 REVIEW)             1.3M        114     100.6K       1.2M      32.8K     36
  └ Coder sub-agent                732.7K         36     102.8K     622.1K       7.7K     24

GRAND TOTAL                         29.4M

## Waste Points
- IMPLEMENT (17M): 195 calls for a 2-line code change + 110-line test. Coder sub-agent likely re-read tier1_call.go multiple times and ran full test suite iteratively rather than targeted tests.
- VERIFY/DOCUMENT (4.9M): 79 calls for this phase alone. Benchmark run (single 4-minute bash command) probably stalled while conversation context was warm, burning retries.

## Saving Suggestions
- Run targeted tests in IMPLEMENT instead of full suite: `go test ./internal/methodjit/... -run TestSelfCall_ConstantsStrMoved|TestDumpTier1_Ackermann` saves ~10 full-suite runs × ~30s × parse overhead. Estimated saving: 3-5M tokens. Risk to effectiveness: low — correctness suite still runs in VERIFY.
- VERIFY/DOCUMENT could skip benchmark JSON parse and go straight to summary table grep: 4.9M for a 5-minute verify phase is high. Estimated saving: 1-2M. Risk: low.
- IMPLEMENT cap: 1-file surgical edits should cap Coder sub-agent at 15 tool calls max. Add explicit cap to IMPLEMENT prompt for scope ≤2 files. Estimated saving: 8-10M on single-file rounds. Risk: none for simple moves.
