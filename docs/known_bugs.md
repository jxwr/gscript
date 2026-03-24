# Known JIT Bugs (2026-03-24)

## 1. spectral_norm ERROR: float accumulator treated as int (Method JIT)

**Symptom**: `attempt to perform arithmetic on number and boolean` at multiplyAtv:23

**Root cause**: `findAccumulators()` in `codegen_loop.go` detects `sum` as a body accumulator via syntactic pattern matching (ADD/MOVE), pins it to a callee-saved ARM register, and loads/stores with `EmitUnboxInt`/`EmitBoxInt`. But `sum` is a float (`sum := 0.0`), so the NaN-boxed float bits get corrupted.

**Fix**: `findAccumulators()` must reject candidates whose initial value is float (check LOADK constant type).

**Files**: `internal/jit/codegen_loop.go:findAccumulators`

## 2. math_intensive HANG: inlined callee PC corruption + EQ polarity (Trace JIT)

**Symptom**: JIT mode hangs on `gcd_bench`

**Root cause (two bugs)**:
1. When trace JIT inlines a function call, SSA instructions from the callee carry PCs from the callee's proto. On side-exit, the exit PC is from the wrong function → interpreter jumps to garbage position → infinite loop.
2. `emitSSAIntCompare` in `ssa_codegen_emit_arith.go` always emits `BCond(CondNE, "side_exit")` for `SSA_EQ_INT`, ignoring `AuxInt` polarity. This inverts while-loop conditions (`for b != 0`), causing immediate side-exit on every execution.

**Fix**:
1. Store caller PC (not callee PC) in SSA instructions for inlined code
2. Use `inst.AuxInt` to select guard polarity in `emitSSAIntCompare`

**Files**: `internal/jit/ssa_codegen_emit_arith.go:emitSSAIntCompare`, `internal/jit/trace_record.go:buildTraceIR`

## 3. nbody guard-fail: slot reuse type mismatch (Trace JIT)

**Symptom**: nbody 1.8s (should be <0.1s), traces compile but guard-fail → blacklist

**Root cause**: Slot 13 in advance() is GETTABLE dest (table type at loop start, int type after GETTABLE). Guard checks Int but entry value is nil/table. `isWrittenBeforeFirstReadExt` doesn't recognize GETTABLE as a write.

**Fix**: Systematic — see `docs/guard-refactor-plan.md` P0 (unified classifySlots)
