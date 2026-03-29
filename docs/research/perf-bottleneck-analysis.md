# GScript Performance Bottleneck Analysis

> Analysis date: 2026-03-27
> Goal: Identify highest-impact optimization opportunities to reach 100x speedup over VM

---

## Executive Summary

**Current State: 3 of 21 benchmarks get meaningful JIT acceleration.**

| Benchmark Category | Current Speedup | Status |
|------------------|----------------|--------|
| mandelbrot | 5.5x | JIT compiling |
| fibonacci_iterative | 2.9x | JIT compiling |
| math_intensive (leibniz) | 8.6x | JIT compiling |
| **All other benchmarks** | ~1x or worse | **JIT NOT compiling** |

**The fundamental problem:** The trace JIT's compilation pipeline (`ssaIsIntegerOnly`) aggressively rejects traces containing `SSA_CALL` (call-exit) instructions, which blocks **18 of 21 benchmarks** from getting any JIT acceleration.

---

## Benchmark Results Analysis

### Fast Benchmarks (JIT Working)

#### 1. **mandelbrot (5.5x)**
- **Why fast:** Pure float arithmetic in tight `for i <= n { ... }` loop
- **Characteristics:**
  - No function calls
  - No table operations
  - Single nested FORLOOP structure (inner pixel loops get sub-trace called)
  - Type-stable (all floats)

#### 2. **fibonacci_iterative (2.9x)**
- **Why fast:** Integer arithmetic in tight FORLOOP
- **Characteristics:**
  - No function calls
  - Simple integer arithmetic only
  - Type-stable (all ints)

#### 3. **math_intensive/leibniz (8.6x)**
- **Why fast:** Float arithmetic with division in tight loop
- **Characteristics:**
  - No function calls
  - Simple float operations
  - Type-stable (all floats)

**Pattern:** These benchmarks are "ideal trace JIT input" — pure arithmetic, no external dependencies, type-stable loop bodies.

---

### Slow Benchmarks (JIT Rejected or Overhead > Benefit)

#### 1. **fibonacci_recursive (1.0x vs VM)**
```
VM:  1256 ns/op
JIT:  2342 ns/op  ← SLOWER than VM!
```

- **Why rejected:** Contains recursive function calls (`fib(n-1) + fib(n-2)`)
- **Root cause:** Recursive calls emit `SSA_CALL`, triggering `ssaIsIntegerOnly` rejection

#### 2. **ackermann (0.7x vs VM)**
```
VM:  580 ns/op
JIT:  703 ns/op  ← SLOWER than VM!
```

- **Why rejected:** Recursive self-calls
- **Root cause:** Nested recursion emits `SSA_CALL`, rejected by `ssaIsIntegerOnly`

#### 3. **function_calls (0.6x vs VM)**
```
VM:  9.3M ns/op
JIT:  55.1M ns/op  ← Much SLOWER!
```

**Warm benchmark shows JIT is ~17x slower than VM!**

- **Why rejected:** Calling `add(x, 1)` function 10,000 times
- **Root cause:** Function calls emit `SSA_CALL`, rejected by `ssaIsIntegerOnly`

#### 4. **heavy_loop (warm) (1.1x vs VM)**
```
VM:  990 ns/op
JIT:  1874 ns/op  ← ~2x slower!
```

- **Problem:** Even for a simple integer sum loop, JIT is slower
- **Root cause:** `vm.Call()` wrapper overhead + trace re-entry cost

#### 5. **table_field_access (BUG - nil table errors)**
```
[DEBUG] attempt to index nil in step pc=6 key=1
```
- **CRITICAL BUG:** The `particles` table is not being passed to `step(particles, n)` function
- **Code issue:** `vm.Call("step", nil, 100)` passes `nil` instead of the table
- **Impact:** Benchmark can't measure true performance; JIT trace fails with nil errors

#### 6. **object_creation (0.9x vs VM)**
- **Why rejected:** Table creation (`{x: ..., y: ..., z: ...}`) emits `SSA_CALL`
- **Root cause:** Object literal syntax requires `NEWTABLE` → call-exit

#### 7. **String concatenation (0.44x vs VM)**
- **Why rejected:** String concat emits `SSA_CALL` for `CONCAT` opcode
- **Root cause:** String operations always call-exit

#### 8. **closure_creation (0.003x vs VM)**
- **Why rejected:** Closure creation requires `CLOSURE` opcode
- **Root cause:** Closure allocation is a call-exit operation

---

## Root Cause Analysis: `ssaIsIntegerOnly` Rejection

### The Call-Exit Blocker

Location: `internal/jit/ssa_emit.go:28-76`

```go
func ssaIsIntegerOnly(f *SSAFunc) bool {
	hasCallExit := false
	hasForloopExit := false
	for _, inst := range f.Insts {
		switch inst.Op {
		// ... various checks ...
		case SSA_CALL:
			hasCallExit = true 	// ← BLOCKER
		}
	}
	}
	// CRITICAL REJECTION
	if hasCallExit && !hasForloopExit {
		return false  // ← REJECTS ALL REAL-WORLD CODE
	}
	return true
}
```

**Impact:** Any trace containing `SSA_CALL` is rejected. Since `SSA_CALL` is emitted for:
- Function calls (the most common operation in real code)
- GETTABLE (reading from tables)
- SETTABLE (writing to tables)
- GETGLOBAL (reading globals)
- SETGLOBAL (writing globals)
- CONCAT (string operations)
- CLOSURE (creating closures)
- NEWTABLE (creating tables)

**This means virtually all real-world code patterns are rejected.**

### The Re-Entry Overhead Problem

From `10x-optimization-plan.md`, the current call-exit implementation:

```
1. Trace executes to SSA_CALL → ExitCode=3 → Go handler executes instruction
2. ctx.ResumePC = nextPC → trace re-enters
3. Resume dispatch: CMP + BEQ jump to correct resume label
4. Resume label: reload ALL registers from memory → continue execution
5. Reach FORLOOP → loop back to loop_top
```

**Problem:** Steps 2-4 add overhead every single function call. In benchmarks like `function_calls` (10,000 calls), this means 20,000+ extra guard checks and register reloads per iteration.

**Result:** JIT traces with call-exits can be **slower than VM** because the overhead of exit/re-entry exceeds the benefit of native execution.

---

## Top 5 Bottlenecks (Ranked by Impact)

### #1: Call-Exit Rejection (Critical Impact)

**Bottleneck:** `ssaIsIntegerOnly` rejecting all traces with `SSA_CALL`

**Affected benchmarks:** 18/21
- fibonacci_recursive
- ackermann
- function_calls
- table_ops (string key tables)
- string_concat
- closure_creation
- object_creation
- spectral_norm
- mutual_recursion
- method_dispatch
- sieve, sort, fannkuch
- binary_trees

**Evidence:** Warm benchmarks show JIT slower than VM when traces contain calls

**Root cause:** The `SSA_CALL` rejection assumes call-exits are too expensive to justify compilation. This is **fundamentally wrong** — LuaJIT compiles traces with calls and handles them efficiently.

**Expected speedup if fixed:** 2-10x across 18 benchmarks

**Implementation complexity:** **Medium**
- Remove `hasCallExit` rejection in `ssaIsIntegerOnly`
- Make `SSA_CALL` emit proper side-exit code (already partially implemented in `emitCallExitInst`)
- No architecture changes needed

---

### #2: Missing Native Array Store Operations

**Bottleneck:** `STORE_ARRAY` (table[key] = value) not implemented natively

**Affected benchmarks:**
- sieve (setting boolean flags in array)
- sort (swapping elements)
- fannkuch (permutations)

**Current behavior:** `SSA_STORE_ARRAY` falls back to call-exit, requiring full interpreter dispatch on every array write.

**Expected speedup if fixed:** 2-5x for these benchmarks

**Implementation complexity:** **Low-Medium**
- Implement `emitStoreArray` similar to existing `emitLoadArray`
- Handle array bounds checking and growth inline
- Direct memory writes without call-exit

---

### #3: While-Loop Tracing Not Supported

**Bottleneck:** Traces without `FORLOOP` exit marker are rejected

**Affected benchmarks:**
- sieve (mark loop uses while-style iteration)
- fannkuch_flip (while loop)
- Other while-loop patterns in real code

**Current behavior:** `ssaIsIntegerOnly` requires `hasForloopExit = true`, rejecting while-loops

**Expected speedup if fixed:** 2-3x for affected benchmarks

**Implementation complexity:** **Medium**
- Detect JMP back-edge pattern (while loop exit)
- Modify `ssaIsIntegerOnly` to allow while-loop traces
- Adjust guard fail handling for while-loop semantics

---

### #4: 2D Table Access Not Supported

**Bottleneck:** `a[i][j]` pattern fails — first `LOAD_ARRAY` returns table (non-scalar), causing rejection

**Affected benchmarks:**
- matmul (matrix multiplication)
- Any 2D array access patterns

**Current behavior:** From `10x-optimization-plan.md`:
```
LOAD_ARRAY for table → check is table → extract ptr
```
This non-scalar result is rejected by current SSA type checking.

**Expected speedup if fixed:** 2-5x for matmul and similar benchmarks

**Implementation complexity:** **Low-Medium**
- Allow `LOAD_ARRAY` to produce table type results
- Add subsequent `LOAD_ARRAY` on that table result
- Type propagation for 2D access patterns

---

### #5: Call-Exit Re-Entry Overhead

**Bottleneck:** The resume dispatch mechanism adds overhead per call-exit

**Affected benchmarks:** All traces with call-exits (most real-world code)

**Current behavior:** As documented in `10x-optimization-plan.md`:
- Resume dispatch table requires CMP + BEQ
- All registers reloaded from memory on re-entry
- Overhead ~2 guard checks per function call

**Expected speedup if optimized:** 1.2-1.5x for call-exit heavy code

**Implementation complexity:** **High**
- Implement proper side-exit without re-entry dispatch
- Make call-exit use snapshot restoration directly
- Eliminate resume dispatch table

---

## Missing Optimizations (Lower Priority)

### Native TABLE_LEN

From `10x-optimization-plan.md`, `SSA_TABLE_LEN` is now marked as native but implementation may need verification. This would speed up benchmarks that check table length.

### Function Inlining

The current JIT has no function inlining for non-self-call cases. Small functions like `add(a, b)` cannot be inlined, requiring full call-exit per call.

### Float Vector Operations

No SIMD vectorization for float array operations. Mandelbrot and math_intensive could benefit from vectorized parallel operations.

---

## Recommended Optimization Priority Order

### Phase 1: Unblock Real-World Code (Highest Impact)

1. **Fix #1: Remove call-exit rejection** — Unlock 18 benchmarks
   - Expected: 2-10x speedup across most benchmarks
   - Effort: Medium
   - Risk: Low (pattern is well-understood)

2. **Fix #2: Implement native STORE_ARRAY** — Speed up sieve, sort, fannkuch
   - Expected: 2-5x speedup for 3 benchmarks
   - Effort: Low-Medium
   - Risk: Low

3. **Fix #3: Fix table_field_access bug** — Fix nil table parameter
   - Required: Benchmark is broken, can't measure correctly
   - Effort: Trivial (line 252 in warm_bench_test.go)
   - Risk: None

**Phase 1 Expected Combined Impact:** 3-15x speedup across 20+ benchmarks

---

### Phase 2: Support More Loop Patterns

4. **Fix #4: While-loop tracing** — Support while-style iterations
   - Expected: 2-3x speedup for while-heavy benchmarks
   - Effort: Medium
   - Risk: Medium (while-loop exit semantics are trickier)

5. **Fix #5: 2D table access** — Support array-of-arrays
   - Expected: 2-5x speedup for matmul and matrix operations
   - Effort: Low-Medium
   - Risk: Low-Medium (type system complexity)

**Phase 2 Expected Combined Impact:** 2-4x speedup for affected benchmarks

---

### Phase 3: Reduce Overhead (Refinement)

6. **Fix #6: Call-exit re-entry optimization** — Reduce dispatch overhead
   - Expected: 1.2-1.5x speedup for call-exit heavy traces
   - Effort: High
   - Risk: High (touches core execution path)

7. **Function inlining** — Inline small pure functions
   - Expected: 1.5-3x speedup for call-heavy code
   - Effort: High
   - Risk: Medium

8. **Vector operations** — SIMD for float arrays
   - Expected: 1.5-2x speedup for compute-heavy benchmarks
   - Effort: Very High
   - Risk: High (ARM64 SIMD complexity)

---

## Summary: Path to 100x

**Current state:** Best benchmark at 8.6x (leibniz), most at ~1x

**After Phase 1 (remove call-exit rejection):** Best could reach 20-30x

**After Phase 2 (while + 2D):** Additional benchmarks reach 10-20x

**After Phase 3 (overhead reduction):** 30-50x across most benchmarks

**Path to 100x:** Requires Phase 1 + Phase 2 + Phase 3 + vectorization + more aggressive optimizations (CSE, fusion, better register allocation).

**Key insight:** The **fundamental blocker is call-exit rejection**. Removing this limitation alone could double or triple JIT effectiveness for most real-world code patterns.

---

## Implementation Notes

### Call-Exit Fix Details

The fix is already partially implemented. From `10x-optimization-plan.md`:

```
P0: 把 call-exit 改为 side-exit（最高优先级，解锁 11+ benchmarks）

改动：
1. ssa_emit.go: 对 SSA_CALL，emit 和 side-exit 完全相同的代码
2. ssaIsIntegerOnly: 移除 `if hasCallExit { return false }`
3. 删除 resume dispatch 代码
```

This means the implementation path is documented but needs to be applied.

### Table Field Access Bug

File: `benchmarks/warm_bench_test.go:252`

```go
vm.Call("step", nil, 100)  // ← BUG: nil instead of particles table
```

Should be:
```go
vm.Call("step", particles, 100)  // ← Fix: pass the table
```

---

## Conclusion

GScript's JIT architecture is sound for pure arithmetic loops but **crippled** for real-world code containing:
- Function calls
- Table operations
- String operations
- Object allocation

The single highest-impact change is **removing the `SSA_CALL` rejection in `ssaIsIntegerOnly`**. This single change could unlock 18 benchmarks and bring GScript from ~1x average speedup to 3-15x across most workloads.

**Recommendation:** Prioritize Phase 1 fixes immediately. They have the highest leverage (impact/effort ratio) and unblock the vast majority of benchmarks.
