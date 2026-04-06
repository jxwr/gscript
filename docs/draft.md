---
layout: default
title: "1.4% Compute, 98.6% Overhead"
permalink: /27-one-point-four-percent
---

# 1.4% Compute, 98.6% Overhead

We disassembled nbody's inner loop and found that out of 1887 ARM64 instructions per iteration, only 27 are actual floating-point arithmetic. The rest is overhead: type checks, shape guards, NaN-box dispatch, and table-exit stubs. The question isn't "how do we make the math faster?" — it's "why is there so much non-math?"

## What we found

nbody simulates 5 gravitational bodies for 500K timesteps. The hot code is `advance()`, called 500,000 times, with a nested j-loop that runs 10 iterations per call (5M total). Each iteration accesses 14 fields (`bi.x`, `bj.vx`, etc.) and does 27 float ops (3 FSUB, 15 FMUL, 5 FADD, 1 FDIV).

Here's where the 1887 instructions go:

| Category | Insns | % | What |
|----------|-------|---|------|
| Field access overhead | 320 | 17% | 20 GetField/SetField × 16 insns each (type check + ptr extract + shape guard + svals deref) |
| Arithmetic type dispatch | 240 | 12.7% | 80 lsr#48 checks: "is this operand float or int?" before every add/sub/mul |
| Table-exit stubs | 550 | 29% | 22 cold-path stubs for shape guard failures (code cache pollution) |
| Loop control + misc | 750 | 40% | Counter management, register spills, NaN-boxing |
| **Actual compute** | **27** | **1.4%** | **FSUB, FMUL, FADD, FDIV** |

For comparison, LuaJIT does nbody in 0.034s. We're at 0.590s — 17.4x slower. LuaJIT's traced inner loop has ~50 instructions: direct field loads (1 insn each, no shape check), unboxed float arithmetic (no type dispatch), and that's it.

## The feedback gap

The 240 instructions of arithmetic type dispatch shouldn't exist. We have a complete feedback pipeline:
1. Tier 1 records that `body.x` returns float → FeedbackVector[pc] = FBFloat
2. Graph builder reads feedback → inserts GuardType(getfield_result, TypeFloat)
3. TypeSpecialize sees float → converts `OpMul(any, any)` to `OpMulFloat(float, float)`
4. Emitter generates bare FMUL instead of dispatch-check-then-FMUL

The integration test verifies this end-to-end. So why does the production code show generic arithmetic?

Because **feedback is never collected on the first call.**

Here's the timeline:
1. **Call 1**: Tier 1 compiles advance(). Every GETFIELD misses the inline field cache (empty) and exits to the Go handler. The Go handler loads the field and populates the FieldCache for next time — but **does not record the result type into FeedbackVector**.
2. **Call 2**: Tiering decides to promote to Tier 2. Reads FeedbackVector: all `FBUnobserved`. Compiles with generic arithmetic.

The ARM64 inline cache fast path records feedback perfectly. But on call 1, we never reach that path — everything goes through Go. And Go forgot to take notes.

The integration test passes because it explicitly runs the function through the VM interpreter (which does record feedback) before building the IR. The production code goes Tier 1 → Go exit → Tier 2, and the Go exit is the gap.

## The plan

**Fix 1**: Add feedback recording to the Go exit handler for GETFIELD. It's ~10 lines:
```go
// In handleGetField, after the field access:
if proto.Feedback != nil && pc < len(proto.Feedback) {
    proto.Feedback[pc].Result.Observe(regs[absA].Type())
}
```

This closes the cold-start gap. Call 1 records feedback via Go. Call 2 compiles Tier 2 with typed GetField → specialized arithmetic. The 240 instructions of type dispatch disappear.

**Fix 2**: Shape guard deduplication in the emitter. nbody accesses 7 fields on `bi` and 7 on `bj` per iteration. Each access does a full type-check + shape-guard sequence (~10 insns). After the first access on each table object, subsequent accesses on the same object with the same shapeID can skip the guard entirely. Saves ~162 insns/iter.

Combined: ~400 insns eliminated from 1887 = 21% instruction reduction. Conservative wall-time estimate (halved for ARM64 superscalar): **nbody −10-13%**.

That won't close the 17x gap — that needs unboxed representation selection and direct field offset loads, which are deeper architectural changes. But fixing the feedback pipeline is a prerequisite for everything downstream, and it's the kind of bug where one line of missing code costs 12.7% of your hot loop.

## What we built

Both fixes landed cleanly with TDD. No surprises, no deviations from the plan.

**Fix 1 (feedback recording)** was exactly the ~10 lines predicted. Two additions to `tier1_handlers.go`: after `handleGetField` performs the field access, it now calls `proto.Feedback[pc].Result.Observe(regs[absA].Type())`. Same for `handleGetTable`. The existing integration test (`TestFeedbackGuards_GetField_Integration`) already covered the pipeline — it just happened to bypass the Tier 1 Go exit path by running through the VM interpreter. With the fix, the real production path now produces the same result: GETFIELD → GuardType(float) → MulFloat/AddFloat in the TypeSpecialized IR.

**Fix 2 (shape guard dedup)** added a `shapeVerified map[int]uint32` to `emitContext`, tracking which table SSA values have been shape-verified in the current block. On the dedup fast path, `emitGetField` skips EmitCheckIsTableFull (6 insns), CBZ (1 insn), and the shape comparison (4 insns) — going straight to EmitExtractPtr + svals load. The map resets at block boundaries and after OpCall/OpSelf (calls can modify any table's shape). SetField to existing fields doesn't invalidate the cache, which is correct: writing a value to an existing slot doesn't change the table's shape.

The dedup path is straight-line code with no deopt branch. For nbody's inner loop, where `bi` and `bj` each get 7 field accesses, that's 12 deduped accesses × ~11 instructions saved = ~132 instructions eliminated per iteration.

### Results

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| nbody | 0.590s | 0.515s | **-12.7%** |
| matmul | 0.125s | 0.122s | -2.4% |
| spectral_norm | 0.046s | 0.042s | -8.7% |

nbody improved by 12.7%, right in the predicted 10-13% range. The calibrated estimate (halved for ARM64 superscalar) was accurate. Interestingly, spectral_norm also improved despite not being the primary target — it benefits from the same GETFIELD feedback fix, since its `A()` function accesses table fields in the inner loop.

We're at 0.515s vs LuaJIT's 0.034s — still 15x slower. The remaining gap isn't about missing guards or feedback anymore. It's structural: NaN-boxed representation (every float load/store goes through 64-bit tag encoding), svals indirection (LuaJIT inlines field offsets; we chase a pointer), and no register allocation across field accesses (each GetField clobbers X0-X2). Those are Phase 9-10 of the initiative.

*[Results coming next...]*
