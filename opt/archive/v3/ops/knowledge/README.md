# Compiler Techniques Knowledge Base

Persistent, growing collection of compiler optimization techniques relevant to GScript JIT.
**ANALYZE reads this before every round; updates it with new findings.**

## Structure

One file per technique area. Each file:
- Describes the technique and its variants
- Cites concrete implementations in V8 / SpiderMonkey / JSC / LLVM
- Records specific thresholds, heuristics, file:line citations
- Notes which GScript rounds have used or attempted this technique
- Has a `Last Updated` date

## Files

| File | Topic | Last Updated |
|------|-------|-------------|
| feedback-typed-loads.md | Speculative type guards after heap loads (GetTable/GetField) using FeedbackVector | 2026-04-06 |
| unboxed-loop-ssa.md | Unboxed loop-carried values: representation selection, deopt metadata, spill cost heuristics | 2026-04-06 |

## Rules

1. **ANALYZE updates this after every web search / source read.** Don't discard findings.
2. **Concrete > vague.** "V8 uses register pressure threshold 16" beats "V8 manages register pressure."
3. **Cite file:line** when reading source. `v8/src/compiler/backend/register-allocator.cc:1423` not "V8's register allocator."
4. **Date every update.** Techniques evolve; stale info is marked.
5. **Link to rounds.** "Used in Round 9 (2026-04-06-tier2-licm-carry)" helps future ANALYZE avoid retreading.
