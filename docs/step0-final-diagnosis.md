# Final Diagnosis: nbody crash root cause

## The Exact Crash

```
OP_GETTABLE A=11, B=12 at advance pc=16
slot 12 holds float (0xc01c1eb851eb851e) instead of table (bodies)
```

**Not slot 6, not GETFIELD.** Previous diagnosis had wrong PC (frame.pc was already incremented).

## Root Cause Chain

1. Trace IR for advance()'s `for j` inner loop starts with:
   ```
   IR 0: LOADK A=12       ← writes float (dt constant) to slot 12
   IR 2: GETTABLE A=11, B=12, C=13  ← reads slot 12 as table (bodies)
   ```

2. In the bytecode, slot 12 is reused: first holds `bodies` (table, from outer scope), then gets overwritten by LOADK with `dt` (float constant).

3. **Without trace execution** (guard fail → interpreter): interpreter runs from normal bytecodes. GETTABLE reads slot 12 = table (set by outer loop). LOADK hasn't overwritten it yet at that PC. Everything works.

4. **With trace execution** (after MOVE WBR eliminates guard for slot 13):
   - Trace native code runs
   - LOADK writes float to slot 12 **register** (not memory, in original code)
   - GETTABLE is a **call-exit**: interpreter executes it, reads slot 12 from **memory** → still table → works
   - Call-exit returns, trace resumes

5. **With write-through**: LOADK writes float to slot 12 **memory** → GETTABLE call-exit reads float from memory → crash.

6. **With original store-back**: GETTABLE call-exit does `emitSlotStoreBack` which writes all writtenSlots to memory. If slot 12 is in writtenSlots and has an int register, `EmitBoxInt` writes int to memory → crash.

## Why Write-Through Makes It Worse

Write-through makes EVERY instruction write to memory immediately. For slot 12:
- LOADK writes float to memory (slot 12 = dt)
- Next instruction: GETTABLE call-exit reads memory → float → crash

Without write-through:
- LOADK writes float to **register only** (memory still holds table)
- GETTABLE call-exit reads **memory** → table → works
- At exit, store-back writes float register to memory → may corrupt, but only at loop boundary

## The Fundamental Conflict

**Call-exit reads from memory. Instructions write to registers. The two are decoupled.**

In a normal trace execution loop:
1. Instructions compute in registers (fast)
2. Call-exits read from memory (interpreter needs memory state)
3. Store-back writes registers to memory at exit

This works as long as **memory holds the "interpreter state" during execution, not the "trace state."** Call-exits expect to read the interpreter's values from memory.

But when trace writes to memory (write-through), or when store-back writes before call-exit, memory gets "trace state" which may differ from "interpreter state."

## The Correct Solution

**Per-call-exit operand restoration**: Before each call-exit, restore the call-exit instruction's operand slots from their **pre-loop values** (saved at trace entry).

For GETTABLE B=12:
1. At trace entry: save `regs[base+12]` (table bodies) to a spill slot
2. Before call-exit for GETTABLE: restore `regs[base+12]` from spill slot
3. Call-exit: interpreter reads slot 12 → table → works
4. After call-exit resume: reload register from memory (which now has GETTABLE result in slot 11)

This is essentially a **micro-snapshot** for call-exit points: save the pre-loop value of each call-exit operand, restore before call-exit.

## Impact

This fix would:
1. Allow MOVE WBR to safely eliminate guards
2. Allow traces to execute without corrupting call-exit state
3. Not need full snapshot mechanism (only need pre-loop saves for call-exit operands)
4. Be ~50 lines of code (save at entry, restore before call-exit)
