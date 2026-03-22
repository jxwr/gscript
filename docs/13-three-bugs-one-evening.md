# Three Bugs, One Evening

We sat down to clean up the project structure and run the test suite. What we found was three separate bugs — each in a different subsystem, each with a different root cause, and each blocking a real use case. This post is a quick record of what happened and how each was fixed.

## Bug 1: The Goroutine That Couldn't Talk Back

**Symptom:** The Chinese Chess AI demo (`chess_ai.gs`) launched, you could move a piece, but the AI would think forever. The timer ticked up. The AI never moved.

**Root cause:** The game loop spawned a goroutine to run the AI search (so the UI wouldn't freeze). Inside that goroutine, `getAIMove()` spawned *more* goroutines (6 parallel search workers) and used channels + a polling loop to coordinate them. In GScript's VM, goroutines get their own VM instance. Shared variable mutations (`aiDone = true`) in the child goroutine were not visible to the parent — the game loop never saw the result.

**Fix:** Replaced the Lazy SMP parallel search with a single-threaded iterative deepening loop. The search runs synchronously inside one goroutine; time limits are checked after each depth iteration completes. No channels, no nested goroutines, no polling.

The UI freezes briefly while the AI thinks (3-5 seconds). Not ideal, but it works.

```
// Before: 6 goroutines + channels + polling
resultCh := make(chan, NUM_WORKERS)
for w := 0; w < NUM_WORKERS; w++ {
    go func() { ... resultCh <- result }()
}
for { time.sleep(0.1); if elapsed >= 3.0 && maxDepth >= 5 { break } }

// After: direct loop with time check
for depth := 1; depth <= 12; depth++ {
    // ... search at this depth ...
    elapsed := time.since(startTime)
    if elapsed >= 3.0 && depth >= 5 { break }
}
```

## Bug 2: The GC That Collected Too Early

**Symptom:** `TestChessAI_Completes` crashed with SIGSEGV inside `scanTableRoots` during GC compaction. The crash was intermittent — sometimes it passed, sometimes it segfaulted.

**Root cause:** GScript uses NaN-boxing: pointers are hidden inside `uint64` values that Go's GC can't see. A custom `gcRootLog` tracks these pointers so they survive Go GC cycles. Periodically, `gcCompact()` scans all VM registers to find live pointers and discards dead entries.

The problem: `gcCompact()` was triggered from inside `keepAlive()`, which is called by `TableValue()`. At that moment, the new table had been added to the gcLog but had NOT yet been stored into a VM register (the calling instruction was still mid-execution). So `gcCompact` scanned registers, didn't find the new table, and removed its gcLog entry. Later, Go's GC collected the underlying `*Table`. Next time something touched that register — SIGSEGV.

**Fix:** `keepAlive` now sets a flag (`gcNeedsCompact`) instead of calling `gcCompact` directly. A new `CheckGC()` function runs the actual compaction, and it's called only at *safe points* — function call boundaries in the VM loop and JIT exit-resume points — where all register writes are guaranteed complete.

```go
func keepAlive(p unsafe.Pointer, _ any) {
    // ... add to gcLog ...
    if idx > 0 && idx%gcCompactInterval == 0 {
        atomic.StoreInt32(&gcNeedsCompact, 1)  // signal, don't compact
    }
}

func CheckGC() {
    if atomic.CompareAndSwapInt32(&gcNeedsCompact, 1, 0) {
        gcCompact()  // safe: all values are in registers
    }
}
```

Key insight: per-instruction CheckGC (atomic load every instruction) caused a 4x regression on mandelbrot. Moving it to function call boundaries — much less frequent — had zero measurable overhead.

## Bug 3: The Self-Call That Corrupted the Stack

**Symptom:** `binary_trees.gs` crashed with SIGSEGV under JIT. `pc=0x1` — the CPU jumped to address 1. Even `makeTree(1)` crashed. VM mode worked fine.

**Root cause:** The JIT has a self-call optimization for recursive functions. When `fib(n)` calls `fib(n-1)`, instead of exiting to Go for `OP_CALL`, the JIT emits a direct `BL self_call_entry` — a native ARM64 branch-and-link. This pushes a return address on the ARM64 stack.

The problem: `makeTree` is self-recursive, so the JIT enabled self-calls. But `makeTree` also has `OP_NEWTABLE` (creating `{left: ..., right: ...}`), which is *not* natively supported by the JIT. Unsupported opcodes become *call-exits*: the JIT spills state and jumps to the epilogue, which pops the 96-byte prologue frame and returns to Go.

But when a call-exit happens inside a self-recursive call (depth > 0), the ARM64 stack has self-call frames on it. The epilogue doesn't know about them — it pops from the wrong SP position, restoring garbage into LR/X29/X19. When execution eventually does `RET`, it jumps to address 0x1.

The existing guard checked for `OP_CALL` call-exits but not for `OP_NEWTABLE`, `OP_LEN`, or other non-CALL call-exit opcodes.

**Fix:** `hasCrossCallExits()` now detects *any* unsupported call-exit opcode, not just `OP_CALL`. When such opcodes are present alongside self-calls, the self-call optimization is disabled and the function falls back to the safe call-exit mechanism.

The check is precise: natively-supported ops (GETFIELD, SETTABLE, etc.) don't count, and GETGLOBAL instructions consumed by self-call analysis are skipped. So `fib`, `ackermann`, and other pure-integer recursive functions keep their self-call optimization.

```
makeTree(1) before fix:  SIGSEGV at pc=0x1
makeTree(1) after fix:   {left: {left: nil, right: nil}, right: {left: nil, right: nil}}
binary_trees(15) JIT:    2.088s (was: crash)
fib(35) JIT:             0.034s (unchanged — self-calls still active)
ackermann JIT:           0.011s (unchanged)
```

## Results

All 21 benchmarks pass. All tests pass. The Chinese Chess AI works (in synchronous mode).

| What | Before | After |
|------|--------|-------|
| Tests passing | 9/11 | 11/11 |
| Benchmarks passing (JIT) | 19/21 | 21/21 |
| binary_trees JIT | crash | 2.088s |
| Chess AI | stuck forever | plays moves |
