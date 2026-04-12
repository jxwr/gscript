# Token Reflection — 2026-04-12-object-creation-regression-bisect (R35)

## Usage by Phase
| Phase | Total | Cache Write | Cache Read | Output | Calls |
|-------|-------|-------------|------------|--------|-------|
| ANALYZE + PLAN | 5.4M | 297.1K | 5.0M | 80.6K | 49 |
| IMPLEMENT | 6.7M | 401.6K | 6.2M | 72.6K | 106 |
| └ Task 0 (insn fixture) | 2.5M | 112.3K | 2.4M | 10.9K | 70 |
| └ Task 1 (bisect + doc) | 603.4K | 88.7K | 509.6K | 5.1K | 28 |
| VERIFY + DOCUMENT | 4.4M | 192.8K | 4.2M | 29.1K | 63 |
| └ Evaluator | 30.4K | 29.9K | 0 | 482 | 2 |
| **TOTAL** | **19.5M** | | | | |

## Waste Points
- **Task 0: 2.5M / 70 calls for 113-line test file** — ARM64 instruction classification and production-pipeline compilation setup required iteration. Could have used R29's `tier1_fib_dump_test.go` as template to save ~40% of calls.
- **IMPLEMENT 106 calls total** — Task 0 dominated (70 calls); Task 1 was efficient (28 calls for bisect + knowledge doc). The Coder-per-task split worked well here.

## Saving Suggestions
- **Template provision for fixture tests** — provide existing insn-count fixture (R29 `tier1_fib_dump_test.go`) as context when spawning a Coder that writes a similar fixture. Est. −1M tokens. Risk: none.
- **Task 1 at 603K is a model of efficiency** — bisect + knowledge doc in 28 calls. No change needed.
- **VERIFY at 4.4M** — reasonable for full benchmark suite + state update + blog finalization. No savings without cutting quality.
