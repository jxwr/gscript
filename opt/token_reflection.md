# Token Reflection — 2026-04-10-tier1-int-spec

## Usage by Phase
SESSION                             TOTAL      INPUT    CACHE_W    CACHE_R     OUTPUT  CALLS
------------------------------ ---------- ---------- ---------- ---------- ---------- ------
ANALYZE + PLAN P                     4.3M         60     314.4K       3.9M      50.6K     45
  └ Diagnostic sub-agent             1.9M         71      98.0K       1.8M      10.5K     65
  └ Research                         3.0M         85     143.0K       2.9M      13.3K     81
VERIFY + DOCUMENT P                 17.8M        489     348.1K      17.4M     130.6K    201

GRAND TOTAL                         27.0M

## Waste Points
- VERIFY session (17.8M, 201 calls): extensive test-failure debugging (flaky crash bisection across 15+ test runs) consumed ~40% of the session. The crash was pre-existing; a known-flaky-tests register in state.json would have saved 30+ tool calls.
- ANALYZE diagnostic sub-agent (1.9M, 65 calls): 65 calls is high for a diagnostic. Could have been bounded to 30 calls with a tighter prompt specifying exactly which disasm regions to count.
- Research sub-agent (3.0M, 81 calls): broad web search for prior art (V8/LuaJIT/JSC approaches) used 81 calls. Most relevant findings were in the knowledge base already.

## Saving Suggestions
- Register pre-existing flaky tests in `docs-internal/known-issues.md` with a "do not debug in VERIFY" tag. Saves ~40 tool calls per VERIFY that hits one. **Risk: low** — the test legitimately passes in isolation.
- Bound diagnostic sub-agent to ≤30 calls with `--max-turns=30` flag. **Saving: ~35 calls, ~1M tokens. Risk: low** — diagnostic tasks are well-defined and don't need open-ended exploration.
- Skip web research for well-covered topics (V8 Sparkplug, LuaJIT interpreter dispatch) already in the knowledge base. ANALYZE should check `opt/knowledge/` first and only search for gaps. **Saving: ~1.5M tokens. Risk: low.**
