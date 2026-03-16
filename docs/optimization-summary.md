# GScript VM Optimization Summary

## Performance Results

### Chess AI Benchmark (chess_bench_parallel.gs, Xiangqi AI)

| Metric | Baseline | Optimized | Improvement |
|--------|----------|-----------|-------------|
| Single-thread d=5 | 6.8s | **3.7s** | **×1.84** |
| Single-thread depth (10s) | 6 | **7** | +1 level |
| Single-thread total nodes | 251K | **606K** | **×2.41** |
| Parallel depth | 4 | **6** | +2 levels |
| Parallel throughput | 892K | **2.00M** | **×2.25** |
| Parallel per-worker | 149K | **334K** | **×2.25** |

## Architecture

### Three execution tiers:

```
┌─────────────────────────┐
│   Tracing JIT (ARM64)   │  Hot loop traces compiled to native code
│   - Register allocation │  X20-X24 for hot VM registers
│   - Guard hoisting      │  Skip redundant type checks
│   - Intrinsic inlining  │  bit32.bxor → EOR instruction
│   - Self-recursive call │  BL trace_loop with depth counter
├─────────────────────────┤
│   Method JIT (ARM64)    │  Per-function native compilation
│   - Native GETFIELD     │  Inline skeys scan
│   - Native GETTABLE     │  Array bounds check + direct load
│   - Call-exit batching  │  Reduce exit/re-entry overhead
├─────────────────────────┤
│   Bytecode Interpreter  │  Optimized Go switch-dispatch VM
│   - Inline call/return  │  No Go stack growth per GScript call
│   - Global indexing     │  array[idx] instead of map[string]
│   - Compact Value (32B) │  Merged ival/fval into data field
│   - Typed table maps    │  imap + flat skeys for fast access
│   - Lock-free parallel  │  Isolated child VMs with globals snapshot
└─────────────────────────┘
```

## Optimization Details

### VM Interpreter (4 optimizations)

1. **Table optional mutex + typed maps**: Tables start without a mutex
   (nil check ~0.5ns vs lock ~25ns). Separate `imap map[int64]Value`
   for integer keys and flat `skeys/svals` slices for ≤12 string keys.

2. **Inline call/return**: OP_CALL for Closures pushes a frame and
   updates cached locals directly in the run() loop. OP_RETURN pops
   and restores. No Go recursive stack frames.

3. **Global variable indexing**: `[]Value` array with lazy `GlobalCache`
   per FuncProto. First access resolves string name → index, subsequent
   accesses are O(1) array lookups.

4. **Compact Value 56→32B**: Merged `ival int64` and `fval float64`
   into `data uint64` (float stored as bits via Float64bits). Removed
   separate `sval string` field (stored in `ptr any`).

### Method JIT (3 optimizations)

5. **Native GETFIELD**: ARM64 inline table field lookup — type guard,
   metatable check, skeys linear scan with string comparison.

6. **Native GETTABLE**: Array fast path — type guard, bounds check,
   direct array[key] load. Sparse array auto-expansion for
   board[col*100+row] patterns.

7. **Call-exit batching**: When JIT exits for a call-exit op, check
   if the next instruction is also call-exit and handle immediately.

### Parallel (2 optimizations)

8. **Lock-free child VMs**: OP_GO creates isolated child VMs with
   copied globalArray + globalIndex. Children run with noGlobalLock=true.

9. **JIT factory**: Each child VM gets its own JIT engine with proper
   globals/call-handler configuration.

### Tracing JIT (9 components)

10. **Trace recorder**: Hooks into interpreter loop, records hot loop
    iterations as linear instruction sequences.

11. **ARM64 code emitter**: Compiles trace IR to native ARM64 with
    prologue/epilogue, side-exit, loop back-edge.

12. **GETFIELD/GETTABLE/SETFIELD/SETTABLE**: Native table operations
    in traces with type guards and metatable checks.

13. **EQ/LT/LE/TEST**: Integer and string comparisons with correct
    guard direction for both A=0 and A=1 semantics.

14. **MOD/UNM/LEN**: Integer modulo (SDIV+MSUB), negation, table length.

15. **Intrinsic GoFunction inlining**: bit32.bxor→EOR, bit32.band→AND,
    bit32.bor→ORR compiled directly as ARM64 instructions.

16. **Self-recursive CALL**: Native BL trace_loop with X25 depth counter
    and stack frame save/restore for negamax-like patterns.

17. **Register allocator**: Frequency-based allocation of top 5 VM
    registers to X20-X24. Pure register arithmetic for hot paths.

18. **Trace optimizer**: Redundant type guard elimination based on
    known-integer tracking across the trace IR.

### Attempted but reverted

- **NaN-boxing (32→16B)**: Sign-extension overhead on every integer
  operation exceeded the bandwidth savings in the interpreter.
- **Table object pool**: sync.Pool overhead + stale reference leaks
  caused 2x regression.

## Code Statistics

- **22 commits** on the optimization branch
- **~5000 lines** of new/modified code
- **30+ TDD tests** for tracing JIT alone
- Files modified: value.go, table.go, vm.go, proto.go, coroutine.go,
  codegen.go, executor.go, value_layout.go, assembler.go, plus 8 new files

## Remaining Gap to LuaJIT

Current ~46K nodes/s vs LuaJIT ~500K-2M nodes/s (~10-40x gap).

Root causes (Go language limitations):
1. Switch dispatch (~2-3x slower than C computed goto)
2. Go GC with write barriers (~23% CPU overhead)
3. 32-byte Value vs LuaJIT's 8-byte NaN-boxed values
4. Cannot call Go functions from JIT code (ABI incompatibility)
5. Trace coverage limited by GETGLOBAL/CALL side-exits
