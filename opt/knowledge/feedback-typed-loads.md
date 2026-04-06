# Feedback-Typed Heap Loads

> Last Updated: 2026-04-06 | Rounds: (proposed for next round)

## Technique

Use interpreter-collected type feedback to specialize the *result type* of heap load operations (GetTable, GetField). When the FeedbackVector shows a monomorphic result type (e.g., always float), insert a GuardType check after the load. Downstream arithmetic auto-specializes via existing TypeSpecialize pass.

## How V8 Does It

V8's approach (TurboFan pipeline):

1. **FeedbackVector collection**: Each bytecode has a FeedbackSlot. For element loads (LdaKeyedProperty), the IC (Inline Cache) records the "element kind" of arrays accessed. For named properties, it records a Map (shape) chain.

2. **BytecodeGraphBuilder reads feedback**: `src/compiler/bytecode-graph-builder.cc` — when lowering LdaKeyedProperty, reads the feedback slot to determine the expected element kind. If monomorphic (e.g., PACKED_DOUBLE_ELEMENTS), it inserts:
   - `CheckMaps` node (verify receiver has expected map)
   - `LoadElement` with known representation (Float64)
   - Result type is `Type::Number()` instead of `Type::Any()`

3. **Simplified lowering**: `src/compiler/simplified-lowering.cc` uses the known representation to emit unboxed loads (LoadFloat64Element directly into FPR, no boxing).

4. **Deoptimization**: If CheckMaps fails, deopts to interpreter. The deopt frame descriptor records where each value lives (register or stack slot). Materialization code re-boxes register values.

Key thresholds:
- Feedback must be monomorphic (single element kind). Polymorphic feedback falls back to generic path.
- `v8/src/objects/feedback-vector.h`: FeedbackSlotKind::kLoadKeyed

## How SpiderMonkey Does It (Warp/WarpBuilder)

1. **CacheIR snapshots**: SpiderMonkey's Baseline JIT records CacheIR (a bytecode for IC stubs) for each property access. Warp reads these snapshots during compilation.

2. **WarpBuilder**: `js/src/jit/WarpBuilder.cpp` — reads CacheIR to determine the result type of element loads. If the IC always returned Float64, Warp inserts:
   - GuardShape (check receiver shape)
   - LoadFloat64 (unboxed element load)
   - Result MIRType = MIRType::Double

3. **Range Analysis + Type Policy**: downstream float ops stay unboxed because the MIRType is known.

## How JSC Does It (DFG/FTL)

1. **Value profiling**: JSC's LLInt and Baseline JIT record the last N observed types per bytecode via `ValueProfile`. DFG reads this during compilation.

2. **SpeculatedType**: `Source/JavaScriptCore/dfg/DFGSpeculativeJIT.cpp` — element loads get `SpeculatedType` from the value profile. If `SpecDouble`, inserts:
   - CheckStructure (shape guard)
   - GetByVal with Double speculation
   - Result is SpecDouble → enables downstream ArithMul/ArithAdd speculation

Key: JSC uses `maximumInliningDepth=5`, `maximumFunctionForCallInlineCandidateBytecodeCostForDFG=80`.

## Key Differences Across Engines

| Aspect | V8 | SpiderMonkey | JSC |
|--------|-----|-------------|-----|
| Feedback source | FeedbackVector slots | CacheIR snapshots | ValueProfile |
| Guard type | CheckMaps (shape) | GuardShape | CheckStructure |
| Result representation | Machine representation | MIRType | SpeculatedType |
| Polymorphic handling | Generic path | Polymorphic IC | Generic fallback |
| Deopt mechanism | Eager (immediate) | Eager | OSR exit |

**Universal pattern**: All three engines (1) collect per-bytecode result type info during lower tiers, (2) use it in the optimizing tier's IR construction to insert guards and specialize the result type, (3) rely on existing type propagation to cascade specialization to downstream ops.

## Applicability to GScript

GScript already has all the infrastructure:
- **FeedbackVector**: `internal/vm/feedback.go` — records Left/Right/Result FeedbackType per PC. The interpreter already calls `fb.Result.Observe()` for GETTABLE/GETFIELD.
- **GuardType**: `OpGuardType` is fully implemented (emit, interp, validator, DCE-safe, LICM-safe).
- **TypeSpecialize**: `pass_typespec.go` already handles `OpGuardType` → returns `Type(instr.Aux)`.
- **Graph builder**: `graph_builder.go` has `proto *vm.FuncProto` with `proto.Feedback`.

**Missing piece**: The graph builder emits `TypeAny` for GetTable/GetField results (line 620). It never reads `proto.Feedback[pc].Result`. Adding ~20 lines to read feedback and insert GuardType would cascade through the existing pipeline.

**Cascade effect**: When GetTable result is known-float:
1. Downstream `OpMul(any, any)` → `OpMulFloat(float, float)` — saves ~10 insns
2. Loop accumulator phi becomes TypeFloat → gets FPR allocation → loop-carried carry
3. Per-iteration NaN-boxing of accumulator eliminated — saves ~4 insns

**Risk**: If feedback is wrong (type changed after interpreter run), GuardType deopts. This is safe but wastes compilation. GScript's monotonic lattice (once FBAny, stays FBAny) prevents deopt-reopt cycles.
