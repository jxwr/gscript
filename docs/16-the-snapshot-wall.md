# The Snapshot Wall

Six rounds of fixes. Five different approaches. Zero success. This is the story of how a "simple" guard elimination exposed the deepest architectural gap in our JIT compiler — and what it taught us about the limits of AI-driven development.

## Where We Are

GScript's trace JIT compiles hot loops to ARM64 native code. It works beautifully for pure integer workloads — fib runs at LuaJIT parity. But nbody (floating-point physics simulation) runs at 1.8 seconds, 55x behind LuaJIT's 0.035s. The trace compiles, the guards fire, and the native code... never executes.

The immediate blocker: pre-loop type guards keep failing. The trace expects slot 13 to hold an integer at loop entry, but it holds nil (the function was just called, locals aren't initialized yet). Five consecutive guard failures → blacklist → interpreter forever.

The "obvious" fix: don't guard slots that are written before read (WBR) in the loop body. If the first thing the loop does is write to slot 13, who cares what it held before?

Obvious. Simple. Six attempts. All failed.

## The Six Rounds

### Round 1: Only eliminate FORLOOP control slot guards
Safe and conservative. Tests pass. nbody unchanged — slot 13 isn't a FORLOOP control variable.

### Round 2: Eliminate all non-live-in guards using SSA use-def chains
Five tests break. Turns out `emitSSALoadField` reads table pointers from *memory* (`regs[slot*ValueSize]`), not from SSA refs. The use-def chain sees "no SSA users" but the codegen reads the slot through a memory indirection the SSA doesn't model.

### Round 3: Unified `classifySlots` replacing all WBR variants
Mandelbrot breaks (pure numeric trace, no tables). The new unified write list (including arithmetic destinations) is more aggressive than the old numeric-specific path. Float slots that need D register initialization get classified as WBR and don't get pre-loop loads.

### Round 4: Add MOVE to WBR write list in legacy path
Unit tests pass! nbody crashes: "attempt to index a number value." MOVE was added as a write, slot 13's guard was eliminated, traces started *actually executing* — and immediately exposed a *different* bug: the store-back mechanism corrupts slot 6 (a table) with a float value.

### Round 5: Type-aware store-back to prevent corruption
Skip int store-back for multi-type slots. Still crashes. The corruption doesn't come from the trace's int register — it comes from a *different opcode in the same trace* writing a float to the same slot via `GETFIELD A=6` (the bytecode compiler reuses slot 6 for both `bi` (table) and `bi.x` (float)).

### Round 6: Revert to safe state, diagnose properly
Finally understand the root cause. It's not a guard problem. It's not a store-back type problem. **It's a missing snapshot mechanism.**

## The Real Problem

When a trace side-exits, the current code calls `emitSlotStoreBack` which writes ALL modified registers back to memory. But "all modified" means "everything the loop body ever wrote" — not "what should be in memory at this specific exit point."

Consider nbody's `advance()` inner loop body:

```
GETTABLE  slot 6 = bodies[i]     → slot 6 is TABLE (bi)
GETFIELD  slot 13 = slot6.x      → reads slot 6 as table base
GETFIELD  slot 6 = slot11.x      → slot 6 is now FLOAT (bj.x) ← OVERWRITE!
SUB       slot 12 = slot13 - slot6
...
[guard fails here]
→ store-back writes slot 6 as float to memory
→ interpreter resumes at ExitPC
→ interpreter does GETFIELD B=6 → reads float → "attempt to index a number value"
```

The bytecode compiler reuses slot 6. Within a single loop iteration, slot 6 holds a table, then a float, then something else. The interpreter handles this fine — it reads operands before writing results within each instruction. But when the trace side-exits mid-iteration, the store-back dumps the *current* register state, which may be a *different type* than what the interpreter expects at that PC.

This is exactly what LuaJIT's snapshot mechanism solves. Every guard point has a snapshot that records what each slot *should* hold at that specific point. Side-exit restores from the snapshot, not from "whatever the registers happen to contain."

## What the Pros Do

We studied four major JIT compilers:

**LuaJIT 2** (Mike Pall): The gold standard. `lj_snap.c` (~800 lines) maintains sparse snapshots at guard points. Each snapshot is a slot→IR-ref mapping. On side-exit, `lj_snap_restore` reads values from the saved register file (ExitState) using the IR ref's register/spill assignment. Only modified slots are in the snapshot — unmodified slots are still on the Lua stack. Optimizations include snapshot merging (adjacent snapshots without guards coalesce), use-def purging (dead slots cleared before snapshot), and NORESTORE flags (entries for side-trace inheritance only). Memory overhead: ~3KB per trace.

**PyPy RPython**: Uses loop peeling — unroll once, optimize both copies, split into preamble + loop. Guards in the preamble run once; loop body guards are minimal. Guard failure triggers *bridge compilation* (a new trace from the failure point), not interpreter fallback. After 200 failures, a bridge trace is compiled. Type instability creates new specialized loop versions.

**TraceMonkey** (Mozilla): Entry type map validates all slots before entering a trace. Type-unstable loops create multiple specialized trace trees. Was eventually abandoned because JavaScript's type diversity caused trace explosion and constant mode-switching overhead.

**HotSpot C2**: SafePoints with OopMaps provide precise GC and deoptimization info. Not a trace JIT, but the same principle: every deopt point knows exactly what state to restore.

The common pattern: **every system that compiles speculative code needs precise state restoration at every speculation failure point.** There are no shortcuts.

## Why the AI Kept Failing

This is worth reflecting on honestly. Claude (me) attempted this fix six times over several hours, each time with a slightly different approach, and failed every time. Why?

### The "Patch and Pray" Loop

The fundamental failure mode was attempting local fixes to a global problem. Each round followed the same pattern:

1. Identify what seemed like the specific cause (guard too strict, write list incomplete, store-back type wrong)
2. Make a targeted change
3. Tests pass (the test suite doesn't exercise the exact slot-reuse pattern in nbody)
4. nbody crashes in a new way
5. Diagnose the new crash → discover a deeper issue
6. Repeat

An experienced JIT engineer would have recognized at round 2 that this is a **snapshot problem**, not a guard problem. The clue was there: "the codegen reads slots through memory indirections the SSA doesn't model." That's the textbook definition of needing precise deoptimization state.

### Why AI misses architectural insights

Three factors:

**1. Context window as working memory.** A JIT compiler has deeply interconnected subsystems: the recorder, the SSA builder, the optimizer, the register allocator, the codegen, and the exit mechanism. Understanding how a change in guard analysis propagates through register allocation into store-back behavior requires holding 5+ subsystems in mind simultaneously. Each subsystem is 300-700 lines. The total context exceeds what fits comfortably, even with 1M tokens. The AI processes each subsystem sequentially, losing nuance from earlier reads.

**2. Reasoning about invariants is hard.** The snapshot insight requires reasoning about an *invariant*: "at every point where execution could transfer to the interpreter, the VM state must be consistent with what the interpreter expects at that PC." This is not a local property of any function — it's a system-wide contract. AI excels at local reasoning (this function does X, that function does Y) but struggles with global invariants that constrain the *relationships between* components.

**3. No backpressure from failure.** A human engineer who fails twice will stop and reconsider the architecture. The AI, prompted to "keep going," dutifully tries a third, fourth, fifth approach. Each attempt is locally reasonable but globally misguided. The human's frustration is information — it signals "you're solving the wrong problem." The AI doesn't have that signal. It was the human (the user) who finally said: "stop patching, think architecturally."

### The human-AI collaboration pattern that works

Looking back, the successful moments in this project all followed the same pattern:

1. **AI does the grunt work**: reading code, running tests, collecting evidence, writing boilerplate
2. **Human provides architectural direction**: "this is a snapshot problem, not a guard problem"
3. **AI does the research**: studying LuaJIT's implementation, gathering specific code evidence
4. **Human validates the design**: "is this the right approach?"
5. **AI implements**: with a clear, validated plan

The failure mode is when step 2 is skipped — when the AI tries to provide its own architectural direction. For straightforward tasks (file splitting, dead code removal, function extraction), AI direction works fine. For deep architectural decisions that require understanding system-wide invariants, human judgment is still essential.

## The Plan

The snapshot mechanism design is in `docs/snapshot-plan.md`. The key ideas:

1. **Per-guard snapshot**: At each guard point in the loop body, record which slots have been modified and what SSA ref holds their current value.

2. **Per-guard exit stub**: Instead of one shared `side_exit` label with a blanket store-back, each guard gets its own cold stub that restores only the slots from its snapshot.

3. **Type-correct restoration**: Each snapshot entry records the value's type (int/float/table). The exit stub uses the correct boxing function. Table/string values that live in memory (not registers) are simply left alone.

4. **~280 lines of new code**: Modest change. No register allocator rewrite. No new SSA opcodes. Just wiring snapshot data through the existing pipeline.

This won't make nbody fast by itself — it removes the *crash*. After that, the P0 guard elimination (which already works in unit tests) will let the trace actually execute. Then we'll see what the ARM64 native code can really do.

## What's Next

After snapshot:
1. P0 guard elimination activates (MOVE in WBR write list, already implemented, waiting for snapshot safety net)
2. nbody traces execute natively with float arithmetic in D registers
3. Measure actual performance — target < 0.5s (from current 1.8s)
4. If bottlenecked by call-exits (math.sqrt, GETFIELD/SETFIELD), optimize those next

The gap to LuaJIT (0.035s) is 14x at our target. Closing it will require:
- Float register allocation improvements (more D registers in the hot loop)
- Inlining GETFIELD into native code (eliminate call-exit overhead for field access)
- Sub-trace calling for the inner `for j` loop
- Eventually: proper loop peeling (à la PyPy) for invariant hoisting

But first: snapshots. The foundation that should have been there from day one.
