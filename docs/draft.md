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

*[This post is being written live. Implementation next...]*
