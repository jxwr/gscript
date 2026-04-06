# ANALYZE Report — 2026-04-06

## Gap Classification

| Category (canonical) | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|---------------------|------------|---------------------|----------|
| recursive_call | fib (56.7x), ackermann (42.8x), mutual_recursion (37.4x) | 1.83s | **YES (2)** |
| tier2_float_loop | spectral_norm (42.0x), matmul (37.9x), nbody (19.1x), mandelbrot (6.4x), sieve (20.6x) | 2.27s | No (0) |
| gofunction_overhead | method_dispatch (>100x) | 0.10s | No |
| allocation_heavy | binary_trees (1.25x vs VM), object_creation (1.19x vs VM) | N/A (no LuaJIT) | No |
| other | fannkuch (3.6x), sort (4.7x), sum_primes (2.0x) | 0.09s | No |

Note: sieve is an integer loop benchmark but classified under tier2_float_loop because the bottleneck is Tier 2 loop codegen quality (same category of techniques: type specialization, overflow check elimination, register allocation).

## Blocked Categories (from state.json)

- **recursive_call: BLOCKED (failures=2, last round: 2026-04-06-recursive-tier2-phase3-5)**. Tier 2 is net-negative for recursive functions (27-50% regressions); needs native recursive BLR or Tier 1 specialization before another attempt.

## Active Initiatives

- **opt/initiatives/tier2-float-loops.md**: Status `active`. B3 peephole items exhausted (rounds 6-10). Next steps: Phase 3 (feedback-typed heap loads), Phase 5 (matmul tier-up), Phase 6 (range analysis), or new direction (unboxed float SSA).
- **opt/initiatives/recursive-tier2-unlock.md**: Status `active`, but category BLOCKED. Phase 5 invalidated by round 11 results. Waiting for Tier 2 recursion to be net-positive.
- **opt/initiatives/tier2-float-loops-b3-analysis.md**: Status `complete` (diagnostic document from round 9 research).

## Selected Target

- **Category**: `tier2_float_loop`
- **Initiative**: `opt/initiatives/tier2-float-loops.md`, Phase 3 (feedback-typed heap loads)
- **Reason**: (1) Largest total wall-time gap (2.27s vs LuaJIT) among non-blocked categories. (2) Active initiative with Phase 3 clearly identified as next step. (3) The infrastructure already exists (FeedbackVector records result types, OpGuardType is fully operational, TypeSpecialize handles guards) — only the graph builder needs a ~20-line change. (4) Cascade effect: specializing GetTable results enables FPR-resident loop accumulators, a second-order win.
- **Benchmarks**: Primary: matmul (37.9x, 0.812s gap), spectral_norm (42.0x, 0.328s gap). Secondary: nbody (19.1x, 0.598s gap — has GetTable in some inner paths).

## Detour Check

Not repeating a known detour:
- Detour 1 (trace JIT): not applicable — this is a Method JIT pass change.
- Detour 2 (memory-to-memory tier): not applicable — reusing existing Tier 2 pipeline.
- Detour 3 (function-entry tracing): not applicable.
- Lesson #2 (ceiling after 2 failures): tier2_float_loop has 0 failures. The previous 5 rounds (6-10) all produced measurable progress or useful infrastructure.
- INDEX pattern: rounds 6-10 exhausted peephole improvements. Phase 3 is a qualitatively different technique (IR-level type propagation, not emit-level peephole).

## Prior Art Research

### Web Search Findings

All three major JS engines use the same pattern: collect per-bytecode result type feedback in lower tiers, read it during optimizing compilation to insert speculative type guards, cascade type info to downstream operations via existing type propagation.

- **V8 TurboFan**: BytecodeGraphBuilder reads FeedbackVector slots for element loads. Inserts `CheckMaps` + typed `LoadElement`. Result gets machine representation (Float64). If monomorphic, downstream arithmetic fully specializes. Deopt on CheckMaps failure.
- **SpiderMonkey Warp**: WarpBuilder reads CacheIR snapshots. Inserts `GuardShape` + `LoadFloat64`. MIRType propagates to downstream ops.
- **JSC DFG**: Reads ValueProfile. SpeculatedType drives speculation for GetByVal. SpecDouble enables downstream ArithMul/ArithAdd speculation.

### Reference Source Findings

GScript's existing codebase already implements 90% of the technique:

1. **FeedbackVector** (`internal/vm/feedback.go`): Monotonic type lattice (Unobserved->concrete->Any). `TypeFeedback.Result` records the result type. Already populated by interpreter for GETTABLE/SETTABLE/GETFIELD/SETFIELD (confirmed in `internal/vm/vm.go:680-735`).

2. **OpGuardType** (`internal/methodjit/ir_ops.go:85`): Fully implemented in emit (`emit_call.go:42`), interp (`interp.go:475`), validator, DCE-safe (`pass_dce.go:74`), LICM-safe (not hoisted, `pass_licm.go:28`).

3. **TypeSpecialize** (`pass_typespec.go:119`): Already handles `OpGuardType` -> returns `Type(instr.Aux)`. Forward type propagation through phis already works.

4. **Graph builder** (`graph_builder.go:614-620`): Has `proto *vm.FuncProto` -> `proto.Feedback`. Currently emits `TypeAny` for GetTable results without reading feedback.

5. **insertParamGuards** (`pass_typespec.go:194-288`): Existing pattern for inserting GuardType. Creates guard instruction, inserts after the target instruction, replaces downstream uses via `replaceValueUses`.

### Knowledge Base Update

Created `opt/knowledge/feedback-typed-loads.md` with detailed V8/SpiderMonkey/JSC comparison and GScript applicability analysis.

## Source Code Findings

### Files Read

| File | Lines | Key Observations |
|------|-------|-----------------|
| `graph_builder.go` | 14-32, 609-665 | GetTable emits `TypeAny` (line 620). Has access to `proto` and thus `proto.Feedback`. |
| `pass_typespec.go` | 1-300 | Forward type propagation + specialize. Handles `OpGuardType` at line 119. `insertParamGuards` (line 194) is the exact pattern to follow. |
| `feedback.go` | 1-79 | `FeedbackType` lattice with `Observe()`. `TypeFeedback.Result` for table access result type. |
| `vm.go` | 680-735 | Interpreter records `fb.Result.Observe()` for GETTABLE/SETTABLE/GETFIELD/SETFIELD. Confirmed. |
| `emit_call.go` | 272-351 | `emitFloatBinOp` (generic Mul/Add): ~15 ARM64 insns for float path (type dispatch + unbox + compute + rebox). |
| `emit_arith.go` | 1-213 | `emitRawIntBinOp`/float specialized: ~3-5 insns. Savings vs generic: ~10 insns/op. |
| `emit_reg.go` | 1-365 | Cross-block write-through behavior. `loopPhiOnlyArgs` optimization. Raw float store path. |
| `emit_table.go` | 369-465 | `emitGetTableNative`: ~30 ARM64 insns fast path. Result stored as NaN-boxed via `storeResultNB`. |
| `regalloc.go` | 1-684 | Forward-walk allocator. Phi FPR carry requires TypeFloat on phi. LICM invariant carry. |
| `func_profile.go` | 1-143 | matmul (HasLoop, ArithCount>=1, no calls) -> promote at callCount>=2. |

### Diagnostic Data

**matmul inner loop structure** (from `benchmarks/suite/matmul.gs`):
```
for k := 0; k < n; k++ {
    sum = sum + ai[k] * b[k][j]
}
```

Per inner iteration (k), current Tier 2 codegen estimate:
| Category | Insns/iter | % of ~103 |
|----------|-----------|-----------|
| GetTable native (ai[k], b[k], b[k][j]) | ~60 | 58% |
| Generic Mul (float dispatch path) | ~15 | 15% |
| Generic Add (float dispatch path) | ~15 | 15% |
| Int counter + branch | ~8 | 8% |
| Phi moves + overhead | ~5 | 5% |
| **Total** | **~103** | 100% |

**Generic Mul/Add breakdown** (`emitFloatBinOp` at `emit_call.go:272`):
```
resolveValueNB(lhs)           ; 1-2 insns (load from memory)
resolveValueNB(rhs)           ; 1-2 insns
emitCheckIsInt(x0) + B.NE    ; 4 insns  (type check LHS)
FMOVtoFP d0, x0              ; 1 insn   (float path)
emitCheckIsInt(x1) + B.NE    ; 4 insns  (type check RHS)
FMOVtoFP d1, x1              ; 1 insn   (both float)
FMUL/FADD                    ; 1 insn   (actual compute)
FMOVtoGP x0, d0              ; 1 insn   (rebox)
storeResultNB                 ; 1-2 insns (store)
Total: ~15 insns per generic float op
```

**Specialized MulFloat/AddFloat** (from `emit_arith.go` raw float path):
```
resolveRawFloat(lhs)          ; 0-2 insns (may be in FPR already)
resolveRawFloat(rhs)          ; 0-2 insns
FMUL/FADD                    ; 1 insn   (actual compute)
storeRawFloat                 ; 0-2 insns
Total: ~1-7 insns per specialized float op
```

### Actual Bottleneck (data-backed)

**matmul's inner loop has ~103 insns/iter. ~30 insns (29%) are generic type dispatch + unbox/rebox for Mul and Add operations whose operands are ALWAYS float (from GetTable results).** This overhead exists because GetTable produces TypeAny, preventing TypeSpecialize from promoting Mul->MulFloat, Add->AddFloat.

The FeedbackVector already records that these GetTable results are always float (`fb.Result == FBFloat`). The graph builder simply doesn't read it.

**Secondary cascade effect**: the `sum` loop accumulator phi is currently TypeUnknown (because Add produces TypeAny). If Add is promoted to AddFloat -> TypeFloat, the phi cascade makes `sum` float-typed -> FPR-allocated -> phi-carried -> eliminates per-iteration NaN-boxing (~4 insns/iter saved).

**Estimated savings**: 30 insns (generic Mul+Add) -> 7 insns (specialized) = 23 insns. Plus 4 insns (phi carry). Minus 8 insns (2-3 GuardType checks). **Net: ~19 insns/iter or ~18% of the inner loop.**

## Technique Summary for PLAN

**Feedback-typed heap loads**: Extend the graph builder to read `proto.Feedback[pc].Result` when emitting `OpGetTable` and `OpGetField` instructions. When the feedback is monomorphic (FBFloat, FBInt, or FBTable -- NOT FBUnobserved or FBAny), insert an `OpGuardType` instruction immediately after the heap load, with `Aux = int64(observedType)`. Replace all downstream uses of the GetTable/GetField result with the GuardType's value.

**Algorithm**:
1. In `graph_builder.go`, after emitting GetTable/GetField (lines 620, 646), check `b.proto.Feedback[pc]` for the current PC.
2. Map `FeedbackType` to IR `Type`: FBFloat->TypeFloat, FBInt->TypeInt, FBTable->TypeTable.
3. If monomorphic AND not FBUnobserved/FBAny, emit `OpGuardType` with the mapped type.
4. Wire the guard's result value into all downstream uses (same as the existing `readVariable`/`writeVariable` mechanism in the graph builder -- just call `b.writeVariable(a, block, guardInstr.Value())` to overwrite the GetTable's result variable with the guard's output).
5. TypeSpecialize's forward propagation handles the rest automatically.

**No changes needed** to: TypeSpecialize pass, regalloc, LICM, emit_arith, emit_table, or any other pass. The existing infrastructure propagates the type information automatically.

**Edge cases**:
- Tier 1 does NOT record feedback -- only interpreter runs populate the FeedbackVector. Functions promoted via OSR on first call have feedback from the interpreter's single run (sufficient for stable loops like matmul).
- `FBUnobserved` entries: skip (no guard). Occurs when the interpreter never executed that bytecode.
- `FBAny` entries: skip (polymorphic site). Guard would deopt frequently.
- `FBTable` for intermediate table access (e.g., `b[k]` returns a table row): inserting `GuardType(table)` helps the downstream GetTable emit path (knows the receiver is a table, can skip the table type check).

**Expected impact** (calibrated per plan_template -- halve instruction-count estimates for ARM64 superscalar):
- matmul: ~18% insn reduction -> ~9-12% wall-time (0.834s -> ~0.74s)
- spectral_norm: ~10-15% if `u[j]` GetTable benefits similarly
- nbody: small effect (fewer table accesses in hot loop)
- Conservative aggregate: -8% to -12% across float-loop benchmarks

**Risk**: Low. GuardType is fully tested. If feedback is wrong, guard deopts to interpreter (safe). Monotonic lattice prevents deopt-reopt cycles. No changes to emit layer, regalloc, or existing passes.

## Research Depth

- **`shallow`**
- Justification: The technique is well-understood (V8/SpiderMonkey/JSC all do it). GScript's infrastructure is 90% complete -- the FeedbackVector records types, OpGuardType is implemented, TypeSpecialize propagates guard results. The only missing piece is ~20 lines in graph_builder.go to read feedback and insert guards. No RESEARCH phase needed; PLAN can implement directly.
