# The Grand Refactor

20,000 lines of JIT compiler code. Six files over 1,000 lines. Functions spanning 478 lines. Three god objects. No human could hold it all in context — and neither could the AI.

## The Problem

The GScript JIT grew organically over 14 blog posts. Each optimization added code to wherever it fit at the moment. The result:

- **`ssa.go`** (1,479 lines): SSA IR definitions, builder, guard analysis, and pipeline orchestration — all in one file
- **`codegen.go`** (1,367 lines): 50+ methods on one struct, mixing analysis, value access, and code emission
- **`codegen_call.go`** (1,335 lines): inline analysis, self-recursion, cross-call handling — three completely different concerns
- **`ssa_codegen.go`** (1,309 lines): a 478-line `emitSSA` function that generates an entire ARM64 trace in one shot
- **`executor.go`** (1,036 lines): a 370-line switch statement re-implementing half the VM interpreter
- **`trace.go`** (994 lines): hotness tracking, recording, blacklisting, and compilation trigger — all in one struct

The `Codegen` struct had 20+ fields and 50+ methods spread across 4 files. The `emitSSA` function had 11 distinct compilation phases crammed into one function body. The same call-exit opcode handling was duplicated between `executor_callexit.go` and `trace_exec.go`.

When you can't read a file without scrolling for 5 minutes, you can't safely change it. The JIT was becoming unmaintainable.

## The Strategy

**Pure refactoring. Zero behavior changes. All tests must pass after every step.**

The key insight: with 20K lines of code, no single agent can hold the full context. So we split the work into parallel agents, each handling one file split or extraction. Six rounds of changes, all verified by the test suite.

### Round 1: Break the God Objects

Split the 6 largest files into focused modules:

| Before | After | What moved |
|--------|-------|------------|
| `ssa.go` (1,479) | `ssa_ir.go` (116) + `ssa_builder.go` (642) + `ssa_guard_analysis.go` (593) + `ssa.go` (135) | IR types, builder, analysis, entry points |
| `codegen.go` (1,367) | `codegen.go` (714) + `codegen_analysis.go` (394) + `codegen_value.go` (270) | Analysis pass, value access helpers |
| `executor.go` (1,036) | `executor.go` (381) + `executor_callexit.go` (667) | The big switch statement |
| `trace.go` (994) | `trace.go` (504) + `trace_record.go` (497) | Per-instruction recording logic |

All four splits ran in parallel. Total time: ~7 minutes.

### Round 2: Continue Splitting 1000+ Line Files

| Before | After |
|--------|-------|
| `codegen_call.go` (1,335) | `codegen_inline.go` (680) + `codegen_selfcall.go` (377) + `codegen_call.go` (293) |
| `ssa_codegen.go` (1,309) | `ssa_codegen.go` (864) + `ssa_codegen_resolve.go` (256) + `ssa_codegen_analysis.go` (201) |
| `ssa_builder.go` (1,229) | `ssa_builder.go` (642) + `ssa_guard_analysis.go` (593) |

### Round 3: Deduplicate Call-Exit Handling

This was the first change that wasn't purely cosmetic. The method JIT's `handleCallExit` and the trace JIT's `handleTraceCallExit` both interpret unsupported bytecodes in Go. The method JIT handled 16 opcodes. The trace JIT handled only 1 (OP_CALL).

We extracted a shared `ExecuteCallExitOp` function into `callexit_ops.go` — a single source of truth for all 16 opcodes. Both JIT tiers now call this shared function.

**The trace JIT gained 15 new opcodes for free.** Traces that previously had to side-exit when hitting GETGLOBAL, GETTABLE, LEN, CONCAT, etc. can now handle them via call-exit and continue executing. This is a real functional improvement, not just cleanup.

### Round 4: Extract Large Functions

The worst offenders were monolithic functions. We extracted helper methods:

| Function | Before | After | Helpers |
|----------|--------|-------|---------|
| `emitSSA` | 478 lines | 26 lines | 11 phase methods on `ssaCodegen` struct |
| `OnInstruction` | 376 lines | 130 lines | 8 per-opcode recording helpers |
| `emitSSAInstSlot` | 360 lines | 110 lines | 7 per-category emission helpers |
| `convertIR` | 332 lines | 67 lines | 6 conversion helpers + shared `inferResultType` |
| `emitSetTable` | 298 lines | 101 lines | 6 per-array-kind helpers + deduplicated append |
| `analyzeInlineCandidates` | 318 lines | 155 lines | 3 argument tracing helpers |

The `emitSSA` refactoring was the most impactful. The 478-line function became an `ssaCodegen` struct with clearly labeled phases:

```
emitSSA (orchestrator, 26 lines)
  ├── emitSSAPrologue        — save callee-saved registers
  ├── emitSSAResumeDispatch  — call-exit re-entry dispatch table
  ├── emitSSAPreLoopGuards   — type guards before the loop
  ├── emitSSAPreLoopLoads    — load VM slots into ARM64 registers
  ├── emitSSAPreLoopTableGuards — loop-invariant table verification
  ├── emitSSALoopBody        — the hot loop + back-edge
  ├── emitSSAColdPaths       — side-exit, guard-fail, inner-escape
  ├── emitSSAEpilogue        — restore registers, RET
  └── emitSSAFinalize        — assemble, allocate, write executable memory
```

### Round 5: Remove Dead Code

A thorough analysis found 73 dead symbols:

- **Legacy NaN-boxing constants** (15): `OffsetTyp`, `OffsetIval`, `NB_TagMask`, etc. — vestiges from before NaN-boxing
- **Superseded functions** (4): `Compile()`, `CompileWithGlobals()` (replaced by `CompileWithEngine`), `floatRegAllocLR` (replaced by `floatRefAllocLR`)
- **Unused ARM64 instructions** (13): `ADDSreg`, `CSEL`, `DMB`, `ISB`, etc. — defined for completeness, never emitted
- **Dead trace methods** (5): `RecordResult`, `SetDebug`, `IsBlacklisted` — never wired into the VM interface
- **Dead codegen methods** (9): `emitSideExit`, `isJITProductive`, `storeRegFval`, etc.

~460 lines removed. Every remaining symbol has at least one production caller.

### Round 6: File-Level Polish

Split `assembler.go` by instruction category (arithmetic, memory, branch, float). Split remaining 700+ line files. Moved `ssa_codegen.go` phase methods into `ssa_codegen_loop.go`.

## Results

### Code Metrics

| Metric | Before | After |
|--------|--------|-------|
| Source files | 24 | 48 |
| Max file size | 1,479 lines | 645 lines |
| Files over 1,000 lines | 6 | 0 |
| Average file size | ~563 lines | ~426 lines |
| Dead code removed | — | ~460 lines |

### Module Map

```
Core Infrastructure:     assembler (5), memory (2), value_layout, trampoline
Method JIT Compilation:  codegen, codegen_dispatch, codegen_analysis, codegen_value,
                         codegen_arith, codegen_data, codegen_loop, codegen_table,
                         codegen_field, codegen_call, codegen_inline,
                         codegen_inline_emit, codegen_selfcall
Method JIT Execution:    executor, executor_callexit
Trace Recording:         trace, trace_record
SSA Pipeline:            ssa, ssa_ir, ssa_builder, ssa_guard_analysis,
                         ssa_const_hoist, ssa_cse, ssa_fma
SSA Register Allocation: ssa_regalloc, ssa_float_regalloc
SSA Code Generation:     ssa_codegen, ssa_codegen_alloc, ssa_codegen_resolve,
                         ssa_codegen_analysis, ssa_codegen_emit,
                         ssa_codegen_emit_arith, ssa_codegen_loop,
                         ssa_codegen_table, ssa_codegen_array
Trace Execution:         trace_exec
Shared:                  callexit_ops
```

### Performance Impact

This was a pure refactoring — performance should be unchanged. And mostly it is. But the call-exit deduplication gave the trace JIT 15 new opcodes, producing measurable wins on benchmarks that exercise call-exit paths:

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| binary_trees(15) | 2.222s | 1.698s | **-24%** |
| coroutine_bench | 4.771s | 3.174s | **-33%** |
| mutual_recursion | 0.331s | 0.219s | **-34%** |

All other benchmarks are within measurement noise (+/- 5%).

## What We Learned

### 1. Parallel agents handle large refactors well

Each file split is independent — 4 agents can split 4 files simultaneously. The key is giving each agent a precise scope ("move these functions to this file") and running the compiler after each round.

### 2. Tests are the safety net

We ran `go build && go test` after every round. No test failures at any point. This is what makes aggressive refactoring possible — a comprehensive test suite that catches regressions immediately.

### 3. Deduplication has functional benefits

We didn't set out to improve performance. But unifying call-exit handling gave the trace JIT capabilities it was missing, producing 24-34% improvements on three benchmarks. Sometimes cleaning up code reveals capabilities that were hidden by duplication.

### 4. God objects are the root cause

The `Codegen` struct with 50+ methods across 4 files was the main source of complexity. Breaking it into focused modules (analysis, value access, dispatch, arithmetic, table, field, call, inline, self-call, loop, data) made each module independently comprehensible.

### 5. 600 lines is fine if the structure is right

We almost fell into the trap of splitting everything under 500 lines. But a 600-line file with clear internal structure (e.g., `ssa_guard_analysis.go` — all guard elimination, tightly coupled) is better than two 300-line files with artificial boundaries. Split by concern, not by line count.

## What's Next

The refactoring exposed the architecture more clearly. The remaining LuaJIT gap is dominated by:

1. **Float-heavy benchmarks** (matmul 55x, nbody 55x, fannkuch 29x) — Method JIT can't do float arithmetic, and the trace JIT doesn't activate for Method-JIT-compiled functions
2. **Call-heavy benchmarks** (mutual_recursion 55x, method_dispatch 220x) — function call overhead in the interpreter fallback path
3. **Memory-heavy benchmarks** (binary_trees 10x, sort 17x) — GC and allocation overhead

The cleaner codebase makes each of these easier to attack. The path forward: let the trace JIT activate inside Method-JIT-compiled functions for float-heavy loops.
