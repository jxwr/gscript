# Token Reflection — 2026-04-11-loop-scalar-promote-nbody

## Usage by Phase

```
SESSION                             TOTAL      INPUT    CACHE_W    CACHE_R     OUTPUT  CALLS
ANALYZE + PLAN                       8.8M        127     431.0K       8.3M      82.6K     82
  └ Step 4 diagnostic sub-agent      4.6M         90     192.9K       4.4M      22.8K     84
  └ Research sub-agent               1.4M         51     154.8K       1.3M       8.8K     43
IMPLEMENT                            4.6M         76     167.4K       4.4M      26.0K     61
  └ Coder                            3.1M         64     147.2K       3.0M      23.8K     54
VERIFY + DOCUMENT                    6.7M        683     179.8K       6.5M      54.8K     82
  └ Evaluator (sonnet)              585.1K        27      57.2K     524.6K       3.2K     23

GRAND TOTAL                         29.8M
```

## Waste Points

- **ANALYZE Step 4 diagnostic burned 4.6M / 84 calls for a one-shot report.** The
  diagnostic produced the right numbers (9 loop-carried pairs, instruction breakdown)
  but the report was never re-run in VERIFY against post-pass IR. 4.6M of tokens
  bought a pre-pass snapshot that became useless the moment the pass landed. A
  closed-loop use (pre-pass run + assertion-bearing post-pass run) would have
  made this investment 10× more valuable — and would have caught R32's float-gate
  bug in IMPLEMENT instead of post-VERIFY.
- **VERIFY dump overshot the Read 10K-token cap (41.3KB) forcing chunked reads.**
  Same pattern for 5+ rounds. Each round costs ~8K extra tokens in offset reads.
- **Evaluator at 585K / 23 calls is efficient.** The Sonnet downgrade is working.
  Keep it.

## Saving Suggestions

- **Make the ANALYZE diagnostic re-runnable as a test assertion in VERIFY.** Every
  round's diagnostic should land as a test file (e.g. `r32_nbody_loop_carried_test.go`)
  that (a) captures pre-pass counts, (b) runs post-pass and asserts the transform
  fired. Saving: 0.5–1M tokens/round when a round includes a diagnostic. Risk: none —
  strictly tightens the feedback loop. **Would have saved R31 and R32 from landing
  inert passes.**
- **Trim `verify_dump.sh` to essentials.** Drop INDEX.md, workflow_log, constraints.md
  from the default dump; keep state.json + current_plan.md + baseline.json + git diff
  stat. Phases Read-on-demand when needed. Saving: ~8K tokens/round. Risk: low.
- **Skip the evaluator sub-agent for diffs <400 LOC that are already well-tested.**
  Main VERIFY context can do its own checklist read. Saving: ~585K tokens/applicable
  round. Risk: medium — evaluator independence is a real check, not ceremony.
