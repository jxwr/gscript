# Introducing SSA IR: The Architecture That Makes JITs Fast

*March 2026 — Beyond LuaJIT, Post #2*

## Where We Left Off

In [Post #1](01-from-interpreter-to-tracing-jit), we built a tracing JIT that records hot loop iterations and compiles them to ARM64. The result was disappointing: 2.4x on the chess AI benchmark, with the trace covering only ~5 instructions before side-exiting.

The fundamental issue: our trace compiler goes directly from bytecode recording to ARM64. There's no intermediate representation where we can reason about types, eliminate redundant operations, or keep values in registers.

Every `sum = sum + i` compiles to ~15 ARM64 instructions:

```arm64
LDRB W0, [regs, sum*32]      // load sum.typ
CMP  W0, #2                  // guard: TypeInt?
B.NE side_exit
LDRB W0, [regs, i*32]        // load i.typ
CMP  W0, #2                  // guard: TypeInt?
B.NE side_exit
LDR  X1, [regs, sum*32+8]    // load sum.data
LDR  X2, [regs, i*32+8]      // load i.data
ADD  X0, X1, X2              // the actual addition
STR  X0, [regs, sum*32+8]    // store sum.data
MOV  W0, #2
STRB W0, [regs, sum*32]      // store sum.typ = TypeInt
```

In LuaJIT, the same operation compiles to:

```arm64
ADD  X20, X20, X21            // that's it. one instruction.
```

The difference: LuaJIT's SSA IR tracks that `sum` and `i` are always integers, keeps them unboxed in ARM64 registers, and eliminates all type checks inside the loop.

## What is SSA IR?

Static Single Assignment (SSA) is an intermediate representation where every variable is assigned exactly once. Instead of:

```
sum = 0
sum = sum + i    // sum is reassigned!
sum = sum + i    // and again!
```

SSA uses versioned variables:

```
sum_0 = 0
sum_1 = sum_0 + i_0
sum_2 = sum_1 + i_1
```

At loop back-edges, PHI nodes merge versions:

```
loop:
  sum_n = PHI(sum_0, sum_prev)   // sum_0 on first entry, sum_prev on loop-back
  i_n   = PHI(i_0, i_prev)
  ...
  sum_prev = sum_n + i_n
  i_prev   = i_n + 1
  if i_prev <= limit: goto loop
```

### Why SSA Matters for JITs

1. **Type propagation**: If `sum_0 = IntValue(0)` and `sum_1 = sum_0 + i_0` where both operands are int, then `sum_1` is statically known to be int. No runtime type check needed.

2. **Dead code elimination**: If `sum_3` is never read, remove it. In SSA, "never read" is trivial to determine — just check if any instruction references `sum_3`.

3. **Common subexpression elimination**: `a + b` computed twice produces the same SSA value. Hash the instruction → find the duplicate → reuse the result.

4. **Register allocation**: SSA values have clear live ranges (from definition to last use). Linear scan allocation directly on SSA is efficient and well-studied.

## Our SSA IR Design

Inspired by LuaJIT's compact IR format, each instruction is a fixed-size struct:

```go
type SSAInst struct {
    Op      SSAOp       // operation (ADD_INT, GUARD_INT, LOAD_FIELD, ...)
    Type    SSAType     // result type (Int, Float, Table, Unknown)
    Arg1    SSARef      // first operand (reference to another SSA value)
    Arg2    SSARef      // second operand
    Slot    int16       // VM register slot (for loads/stores)
    AuxInt  int64       // auxiliary integer constant
    AuxPtr  unsafe.Pointer // auxiliary pointer (table, string, etc.)
}
```

SSA references are indices into the instruction array. Constants are negative indices (growing downward, LuaJIT style).

### Type System

```go
type SSAType uint8
const (
    TypeUnknown SSAType = iota  // runtime type check needed
    TypeInt                      // known int64, can be unboxed in ARM64 register
    TypeFloat                    // known float64, can be in SIMD register
    TypeBool
    TypeNil
    TypeTable                    // known *Table pointer
    TypeString                   // known string
)
```

When the type is known at compile time (e.g., `TypeInt`), the value can be **unboxed** — stored as a raw int64 in an ARM64 register, not as a 32-byte Value in memory.

### Key Operations

```
// Guards (check assumptions, side-exit on failure)
GUARD_TYPE   ref, expectedType    // type check, side-exit if wrong
GUARD_NNIL   ref                  // nil check, side-exit if nil
GUARD_NOMETA ref                  // metatable check, side-exit if present

// Integer arithmetic (unboxed)
ADD_INT      ref, ref → int       // raw int64 addition, no boxing
SUB_INT      ref, ref → int
MUL_INT      ref, ref → int
MOD_INT      ref, ref → int
NEG_INT      ref → int

// Memory access
LOAD_SLOT    slot → unknown       // load VM register (boxed Value)
STORE_SLOT   slot, ref            // store to VM register
LOAD_FIELD   tableRef, keyRef → unknown  // table field access
STORE_FIELD  tableRef, keyRef, valRef    // table field write

// Control flow
PHI          ref, ref → type      // merge at loop back-edge
LOOP                              // loop header marker
SNAPSHOT     [slot→ref mapping]   // state capture for side-exit
```

### The Pipeline

```
TraceIR (bytecode recording)
    ↓
SSA Builder (type inference + PHI insertion)
    ↓
SSA Optimization
    ├── Guard hoisting (move type checks to loop entry)
    ├── Constant folding
    ├── Dead code elimination
    └── Common subexpression elimination
    ↓
Register Allocation (linear scan on SSA live intervals)
    ↓
ARM64 Code Generation (from typed SSA, not from bytecode)
```

## The Key Optimization: Integer Unboxing

Consider this GScript loop:

```go
sum := 0
for i := 1; i <= 1000; i++ {
    sum = sum + i
}
```

### Before SSA (current trace compiler)

Each iteration: 2 type guards + 2 loads + 1 add + 1 store + 1 type write = **~12 instructions**.

### After SSA with type specialization

```
// Loop entry (execute once):
sum_0 = LOAD_SLOT 4          // load initial sum
GUARD_TYPE sum_0, Int         // verify it's int
sum_unbox = UNBOX_INT sum_0   // extract raw int64

i_0 = LOAD_SLOT 3            // load initial i
GUARD_TYPE i_0, Int
i_unbox = UNBOX_INT i_0

limit = LOAD_SLOT 1
GUARD_TYPE limit, Int
limit_unbox = UNBOX_INT limit

// Loop body (repeated):
LOOP:
  sum_phi = PHI(sum_unbox, sum_next)  : Int
  i_phi   = PHI(i_unbox, i_next)     : Int

  sum_next = ADD_INT sum_phi, i_phi   // one instruction!
  i_next   = ADD_INT i_phi, 1

  CMP i_next, limit_unbox
  BLE LOOP

// Loop exit:
STORE_SLOT 4, BOX_INT(sum_phi)       // box result back to Value
```

The loop body compiles to **3 ARM64 instructions**: ADD + ADD + CMP/BLE. That's 4x fewer than the current approach.

## Implementation Plan

### Phase 1: SSA IR + Builder
- Define SSAInst, SSARef, SSAType
- Build SSA from TraceIR: convert each trace instruction to SSA operations
- Insert PHI nodes at loop back-edges
- Type inference: propagate known types through the SSA graph

### Phase 2: Optimizations
- Guard hoisting: type checks move to before the LOOP marker
- Dead code elimination: remove SSA values with no references
- Constant folding: `ADD_INT const, const` → const

### Phase 3: Register Allocation
- Compute live intervals for each SSA value
- Linear scan allocation: map SSA values to X20-X24
- Spill decisions: least-used values go to memory

### Phase 4: ARM64 Codegen from SSA
- Each SSAInst maps to 1-3 ARM64 instructions
- Unboxed values use allocated ARM64 registers directly
- Guards emit CMP + B.NE side_exit
- PHI nodes are resolved by the register allocator (no code emitted)

## Actual Results

Full benchmark suite comparing baseline (pre-optimization) vs optimized (all optimizations + SSA IR):

| Benchmark | Baseline | Optimized | Speedup |
|-----------|----------|-----------|---------|
| **sieve(1M×3)** | 2.502s | **0.182s** | **×13.7** |
| **nbody(500K)** | 9.572s | **3.054s** | **×3.13** |
| **mandelbrot(1000)** | 4.782s | **2.106s** | **×2.27** |
| **spectral_norm(500)** | 2.057s | **0.905s** | **×2.27** |
| **matmul(300)** | 2.945s | **1.341s** | **×2.20** |
| **chess d=5** | 6.826s | **3.772s** | **×1.81** |
| **chess parallel** | 886K nodes | **2.01M nodes** | **×2.27** |
| fib(35) | 0.078s | 0.082s | ~1x |

The sieve benchmark shows **13.7x speedup** — the sparse array optimization converts hash map lookups to direct array indexing for integer keys < 1024. This is a pure interpreter optimization, not JIT.

The SSA IR infrastructure is in place with type inference, guard hoisting, and dead code elimination. The SSA codegen produces unboxed integer arithmetic (raw ADD/SUB/MUL on ARM64 registers) for simple integer loops. For complex loops with table access, the existing TraceIR codegen is used as fallback.

## References

- Mike Pall, [LuaJIT 2.0 SSA IR](http://wiki.luajit.org/SSA-IR-2.0)
- V8 Team, [Maglev - V8's Fastest Optimizing JIT](https://v8.dev/blog/maglev)
- Keith Cooper & Linda Torczon, *Engineering a Compiler* (SSA construction algorithms)
- Christian Wimmer & Michael Franz, [Linear Scan Register Allocation on SSA Form](https://dl.acm.org/doi/10.1145/1772954.1772979)
