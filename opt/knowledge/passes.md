> Category: general | Generated: 2026-04-11 | Auto-load: yes

# GScript MethodJIT — Pass & Module Reference

Pipeline order: `BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit`

---

## pass_typespec.go (529 lines)
**Purpose**: Forward type-propagates through SSA, replacing generic ops (OpAdd) with typed variants (OpAddInt/OpAddFloat), and inserts speculative GuardType on parameters.
**Key types**: `typeSpecializer` — internal propagation state
**Key functions**:
- `TypeSpecializePass(fn) (*Function, error)` — 3-phase: insert param guards → propagate types → specialize ops
- `(ts) insertParamGuards(fn)` — emits GuardType on params used in int contexts
- `(ts) insertFloatParamGuards(fn)` — emits GuardType on params used in float contexts
- `(ts) inferType(instr) Type` — returns known type for an instruction result
**Pass position**: position 3 (first run), 5, 7 in pipeline; runs 3× to catch newly-inlined code
**Dependencies**: ir.go, ir_ops.go
**Coder notes**: runs 3× in `RunTier2Pipeline`. `replaceValueUses` patches all references when rewriting ops. Adding a new typed op requires updating `inferType`, `specialize`, and both `insertParamGuards`/`insertFloatParamGuards`.

---

## pass_intrinsic.go (89 lines)
**Purpose**: Rewrites `math.sqrt(x)` call patterns (OpGetGlobal+OpGetField+OpCall) into `OpSqrt`, eliminating the call overhead.
**Key functions**:
- `IntrinsicPass(fn) (*Function, []string)` — detects and rewrites known intrinsic call patterns; returns notes for debugging
**Pass position**: position 4 in pipeline (after first TypeSpec, before Inline)
**Dependencies**: ir.go, ir_ops.go
**Coder notes**: Dead OpGetGlobal/OpGetField after rewrite are cleaned by DCE. To add a new intrinsic, pattern-match on OpCall arg chain in `IntrinsicPass`.

---

## pass_inline.go (726 lines)
**Purpose**: Inlines small, non-recursive callees into the caller by splicing their IR blocks; runs to fixed point, enabling transitive inlining.
**Key types**: `InlineConfig` — controls max callee size (MaxSize), globals map, and iteration cap
**Key functions**:
- `InlinePassWith(config) PassFunc` — returns a pass function configured for the given inline budget
- `inlineTrivial(...)` — inlines single-block callees (simple case)
- `inlineMultiBlock(...)` — inlines multi-block callees, remaps value IDs, inserts result phi
- `resolveCallee(callInstr, fn, config) (string, *vm.FuncProto)` — resolves callee proto from OpGetGlobal
**Pass position**: position 6 in pipeline; runs to fixed point within `inlineMaxIterations`
**Dependencies**: graph_builder.go, ir.go, vm
**Coder notes**: After inlining, `relinkValueDefs` must be called to fix value→instruction back-pointers. Mutual/transitive recursion is detected via `isRecursiveOrMutualCached`. Do not increase MaxSize without re-tuning the budget.

---

## pass_constprop.go (256 lines)
**Purpose**: Single forward pass that folds arithmetic on constant operands (both int and float) into new ConstInt/ConstFloat instructions.
**Key types**: `constVal` — wraps a known int64 or float64 constant; `constProp` — pass state
**Key functions**:
- `ConstPropPass(fn) (*Function, error)` — single forward sweep; rewrites in-place
- `(cp) foldIntBinary/foldFloatBinary/foldGenericBinary(instr)` — per-category folding
**Pass position**: position 8 in pipeline
**Dependencies**: ir.go, ir_ops.go
**Coder notes**: Runs once (not to fixed point). Relies on DCE to clean up dead original instructions afterward. Constant folding of generic ops (OpAdd when both operands known) also handled.

---

## pass_load_elim.go (129 lines)
**Purpose**: Block-local CSE for GetField loads, store-to-load forwarding for SetField, and GuardType CSE (converts redundant guards to OpNop).
**Key types**: `loadKey` — (objID, fieldAux) key for available loads; `guardKey` — (valueID, typeTag) key for guard CSE
**Key functions**:
- `LoadEliminationPass(fn) (*Function, error)` — processes each block independently
- `replaceAllUses(fn, oldID, newInstr)` — rewires all references to a value
**Pass position**: position 9 in pipeline
**Dependencies**: ir.go, ir_ops.go
**Coder notes**: Conservative invalidation: OpCall/OpSelf clear ALL available entries. OpSetField on same (obj,field) kills that entry only. GuardType CSE converts (not removes) to OpNop to preserve side-effect ordering; DCE won't delete it, so OpNop emission is a no-op.

---

## pass_dce.go (96 lines)
**Purpose**: Fixed-point dead code elimination — removes instructions with no uses and no side effects.
**Key functions**:
- `DCEPass(fn) (*Function, error)` — fixed-point loop until no changes
- `hasSideEffect(instr) bool` — returns true for stores, calls, branches, returns, guards, table mutations
- `computeUseCounts(fn) map[int]int` — counts references to each value ID
**Pass position**: position 10 in pipeline
**Dependencies**: ir.go, ir_ops.go
**Coder notes**: Runs to fixed point. OpNop (produced by LoadElim's guard CSE) is side-effect-free and will be removed. Any new op that mutates state must be added to `hasSideEffect`.

---

## pass_range.go (511 lines)
**Purpose**: Forward integer range analysis that populates `fn.Int48Safe` — the set of AddInt/SubInt/MulInt/NegInt whose result provably fits int48, allowing the emitter to skip overflow checks.
**Key types**: `intRange` — [min, max] with saturation arithmetic
**Key functions**:
- `RangeAnalysisPass(fn) (*Function, error)` — 3-phase: seed loop counters, fixed-point propagation (≤5 RPO passes), populate Int48Safe
- `seedLoopRanges(fn, ranges)` — seeds FORLOOP counter ranges from concrete start/limit/step values
- `computeRange(instr, ranges)` — per-instruction range from operand ranges
**Pass position**: position 11 in pipeline
**Dependencies**: ir.go, ir_ops.go, loops.go (for RPO order)
**Coder notes**: `fn.Int48Safe` is checked at emit time (`int48Safe(id)`). Adding a new integer op requires updating both `computeRange` and the emitter's overflow-check logic.

---

## pass_licm.go (594 lines)
**Purpose**: Loop-Invariant Code Motion — hoists ops whose operands don't change inside a loop into a new pre-header block; processes innermost loops first.
**Key functions**:
- `LICMPass(fn) (*Function, error)` — outer driver: innermost-first order via `loopDepths`
- `hoistOneLoop(fn, li, hdr)` — fixed-point invariant detection + pre-header creation + move
- `canHoistOp(op) bool` — conservative whitelist: pure arithmetic, loads, GuardType (NOT GuardTruthy/call/store)
- `insertBlockBefore(fn, blk, target)` — inserts a new pre-header block before target
**Pass position**: position 12 in pipeline
**Dependencies**: loops.go, ir.go, ir_ops.go
**Coder notes**: GuardType IS hoisted (GScript deopt has no PC-dependent state). GuardTruthy/call/store are NOT hoisted. Pre-header phi reordering: `hoistOneLoop` patches the header's phi first-args to point to the pre-header. After hoisting, `loopInfo` is recomputed (block membership changes). `tableVerified` tracking for table shape guards: any SetTable in loop blocks suppresses GetTable/GetField hoisting.

---

## graph_builder.go (955 lines) + graph_builder_ssa.go (237 lines)
**Purpose**: Converts FuncProto bytecode to CFG SSA IR using the Braun et al. 2013 single-pass algorithm with lazy phi insertion.
**Key types**: `graphBuilder` — transient build state (pcToBlock, variable maps, etc.)
**Key functions**:
- `BuildGraph(proto) *Function` — entry point; returns complete IR
- `(b) emitBlocks()` — main bytecode walk, 1 case per opcode
- `(b) sealBlock(block)` / `readVariable` / `writeVariable` — SSA variable resolution
- `(b) tryRemoveTrivialPhi(phi)` — collapses trivial phis post-construction
- `feedbackToIRType(fb) (Type, bool)` — maps interpreter feedback to IR type hints
**Pass position**: always first (step 0)
**Dependencies**: vm.FuncProto, ir.go
**Coder notes**: `lastMultiRetReg` tracks multi-return call results. Type feedback from `proto.FeedbackVector` seeds initial types. New bytecodes must be added in `emitBlocks` switch. `graph_builder_ssa.go` contains all SSA rename/phi helpers — do not add bytecode emission there.

---

## loops.go (429 lines)
**Purpose**: Platform-agnostic loop structure analysis — dominators (Cooper algorithm), RPO, natural loop identification, pre-header computation.
**Key types**: `loopInfo` — loop block sets, headers, phis, per-header block membership; `domInfo` — idom table
**Key functions**:
- `computeLoopInfo(fn) *loopInfo` — full loop analysis from CFG
- `computeDominators(fn) *domInfo` — iterative dominator computation
- `computeRPO(fn) []*Block` — RPO block ordering
- `computeLoopPreheaders(fn, li) map[int]int` — header→preheader block ID map
- `loopDepths(li) map[int]int` — depth per block, used by LICM for innermost-first order
**Dependencies**: ir.go
**Coder notes**: No build tag — runs on all platforms. Everything ARM64-specific goes in `emit_loop.go`. `loopInfo.headerBlocks` maps each header to its per-header body set (distinct from `loopBlocks` union).

---

## regalloc.go (684 lines)
**Purpose**: Forward-walk LRU register allocator mapping SSA values to ARM64 physical registers (X20-X23, X28 for GPR; D4-D11 for FPR).
**Key types**: `PhysReg` — {Reg int, IsFloat bool}; `RegAllocation` — complete value→PhysReg mapping; `regState` — per-type LRU pool
**Key functions**:
- `AllocateRegisters(fn) *RegAllocation` — entry point; walks blocks in RPO
- `allocateBlock(block, alloc, lastUse, carried)` — per-block allocation
- `preAllocateHeaderPhis(block, alloc)` — assigns stable registers to loop header phi nodes
- `computeLastUse(fn) map[int]int` — computes last-use block index per value
**Dependencies**: ir.go, loops.go
**Coder notes**: GPRs: X20=20, X21=21, X22=22, X23=23, X28=28. FPRs: D4-D11. X19 is reserved (ExecContext). X24/X25/X26/X27 are pinned constants. Loop header phis get pre-allocated stable registers to avoid ping-pong on back-edges. Eviction uses LRU order. `carried` map passes register state from block to block.

---

## tiering_manager.go (743 lines)
**Purpose**: Multi-tier JIT engine (vm.MethodJITEngine) that delegates Tier 1 to BaselineJITEngine and auto-promotes hot functions to Tier 2 based on FuncProfile analysis.
**Key types**: `TieringManager` — main engine; wraps BaselineJITEngine
**Key functions**:
- `NewTieringManager() *TieringManager` — constructor
- `(tm) TryCompile(proto) interface{}` — compile decision: nil/Tier1/Tier2
- `(tm) Execute(compiled, regs, base, proto)` — dispatches to Tier 1 or 2 execute
- `(tm) compileTier2(proto) (*CompiledFunction, error)` — full Tier 2 pipeline
- `(tm) handleOSR(...)` — on-stack replacement from Tier 1 to Tier 2
- `canPromoteToTier2(proto) bool` / `canPromoteWithInlining(proto, globals) bool` — tiering heuristics
**Build tag**: `darwin && arm64`
**Dependencies**: tier1_manager.go, func_profile.go, pipeline.go, regalloc.go, emit_compile.go
**Coder notes**: Tier 2 failure is permanent (proto added to `tier2Failed` map). Globals map for inlining built from VM at compile time. OSR transfers registers from Tier 1 frame.

---

## tiering_manager_exit.go (500 lines)
**Purpose**: Exit handlers for TieringManager's Tier 2 execute loop (call-exit, global-exit, table-exit, op-exit, closure/upval/vararg exits).
**Key functions**:
- `(tm) executeCallExit(ctx, regs, base, proto)` — handles function calls via VM
- `(tm) executeGlobalExit(ctx, regs, base, proto, cf)` — resolves global variable
- `(tm) executeTableExit(ctx, regs, base, proto)` — handles table get/set ops
- `(tm) executeOpExit(ctx, regs, base, proto)` — handles remaining ops (concat, len, etc.)
**Build tag**: `darwin && arm64`
**Dependencies**: tiering_manager.go, emit.go (ExecContext layout), vm
**Coder notes**: Slot indices in ExecContext are relative to the callee's frame (base=0 in JIT); add `base` for absolute positions. Mirrors `emit_execute.go` exit handlers for the Tier 2 standalone path.

---

## tier1_compile.go (539 lines)
**Purpose**: Entry point for Tier 1 baseline compiler; walks bytecodes linearly and emits fixed ARM64 templates (no SSA, no optimization).
**Key types**: `BaselineFunc` — holds generated native code pointer, resume map, etc.
**Key functions**:
- `CompileBaseline(proto) (*BaselineFunc, error)` — main compile entry; dispatches per opcode
- `emitBaselinePrologue/Epilogue(asm)` — standard frame setup/teardown
- `emitBaselineOpExit(asm, inst, pc, op)` — emits exit descriptor for unhandled ops
- `intSpecEligible(enabled, info, pc, inst, proto) bool` — decides if int-spec path is valid
**Build tag**: `darwin && arm64`
**Dependencies**: tier1_arith.go, tier1_call.go, tier1_control.go, tier1_table.go, jit assembler
**Coder notes**: Pinned registers in Tier 1: X19=ctx, X21=self-closure, X22=R(0), X24=int tag, X25=bool tag, X26=regs base, X27=consts. New opcodes need a case in `CompileBaseline`'s switch.

---

## tier1_manager.go (426 lines)
**Purpose**: Manages the Tier 1 JIT engine lifecycle: compile threshold, execute loop, exit-resume dispatch.
**Key types**: `BaselineJITEngine` — vm.MethodJITEngine implementation
**Key functions**:
- `NewBaselineJITEngine() *BaselineJITEngine` — constructor
- `(e) TryCompile(proto) interface{}` — compile if call count ≥ threshold
- `(e) Execute(compiled, regs, base, proto)` — run-loop with exit-resume
- `(e) executeInner(...)` — inner loop handling all exit codes
**Build tag**: `darwin && arm64`
**Dependencies**: tier1_compile.go, tier1_handlers.go, tier1_handlers_misc.go, emit.go
**Coder notes**: `ExecContext` pool via `acquireCtx`/`releaseCtx`. `errIntSpecDeopt` triggers int-spec eviction and retry. `SetOuterCompiler` wires Tier 2 promotion callback.

---

## emit_compile.go (640 lines)
**Purpose**: Tier 2 compile pipeline — takes Function+RegAllocation, emits executable ARM64; contains `emitContext`, slot assignment, prologue/epilogue, block emission driver.
**Key types**: `emitContext` — holds asm, fn, alloc, slot map, loop info, deferred resumes
**Key functions**:
- `Compile(fn, alloc) (*CompiledFunction, error)` — top-level Tier 2 code generation
- `(ec) emitPrologue()` / `emitEpilogue()` — frame setup, save/restore callee-saved regs
- `(ec) emitBlock(block)` — drives per-instruction emission
- `(ec) assignSlots()` — assigns stack slots for spilled values
**Build tag**: `darwin && arm64`
**Dependencies**: emit_dispatch.go, emit_arith.go, emit_call.go, all other emit_*.go
**Coder notes**: `emitContext.slotMap` maps value ID to stack slot offset. `isFusableComparison` determines if a branch can fuse with preceding comparison. Adding a new op requires a case in `emitInstr` (emit_dispatch.go).

---

## emit_dispatch.go (971 lines)
**Purpose**: Instruction emission dispatch (`emitInstr`), phi move resolution (with cycle detection), control flow (jump/branch/return), and loop-exit boxing.
**Key functions**:
- `(ec) emitInstr(instr, block)` — large switch over all Ops; calls per-op emitters
- `(ec) emitPhiMoves(from, to)` — resolves parallel phi moves, handles cycles
- `(ec) emitGPRPhiMovesOrdered(to, predIdx, isLoopHeader)` — topological ordering for GPR phi moves
- `(ec) emitBranch(instr, block)` / `emitJump` / `emitReturn` — control flow
- `(ec) emitLoopExitBoxing(exitingHeaderID)` — boxes raw-int phi values on loop exit
**Build tag**: `darwin && arm64`
**Dependencies**: emit_compile.go, emit_arith.go, emit_call.go, emit_reg.go, emit_loop.go
**Coder notes**: Phi move ordering is critical — cycles must use a scratch register. `emitGPRPhiMovesOrdered` detects write-through slot conflicts. `emitLoopExitBoxing` must run before any value is used as a NaN-boxed value outside the loop.

---

## emit_arith.go (319 lines)
**Purpose**: ARM64 emission for integer arithmetic, constants, slot loads/stores, int comparisons, and overflow checks.
**Key functions**:
- `(ec) emitIntBinOp(instr, op)` — emits add/sub/mul with optional int48 overflow check
- `(ec) emitInt48OverflowCheck(result, instr)` — SBFX+CMP+B.NE overflow guard (skipped if `int48Safe`)
- `(ec) emitIntCmp(instr, cond)` — int comparison to NaN-boxed bool
- `(ec) emitConstInt/Float/Bool/Nil(instr)` — constant materialization
- `(ec) emitLoadSlot/StoreSlot(instr)` — NaN-boxed VM register file access
**Build tag**: `darwin && arm64`
**Coder notes**: `int48Safe(id)` checks `fn.Int48Safe` to skip overflow check. Raw-int ops (emitRawIntBinOp) skip NaN-boxing entirely for loop-body arithmetic.

---

## emit_call.go (534 lines)
**Purpose**: Float arithmetic, typed float binary ops, div, unary ops, guard emission (GuardType, GuardTruthy), sqrt, and deopt.
**Key functions**:
- `(ec) emitGuardType(instr)` — checks NaN-box tag; deopt on mismatch (ExitCode=2)
- `(ec) emitDeopt(instr)` — emits jump to `deopt_epilogue`
- `(ec) emitFloatBinOp/TypedFloatBinOp(instr, op)` — generic and typed float arithmetic
- `(ec) emitDiv(instr)` — integer or float division with type dispatch
- `(ec) emitSqrtFloat(instr)` — single FSQRT instruction
**Build tag**: `darwin && arm64`
**Coder notes**: `emitGuardType` must match the deopt exit handlers in `tiering_manager_exit.go`. ExitCode=2 means deopt (no resume). GuardTruthy emits conditional branch, not deopt.

---

## emit_call_exit.go (360 lines)
**Purpose**: Call-exit (ExitCode=3) and global-exit (ExitCode=4) emission — store registers, write descriptor, exit, re-enter at resume label.
**Key functions**:
- `(ec) emitCallExit(instr)` — slow-path call through Go VM (used when native BLR not available)
- `(ec) emitGlobalExit(instr)` — exits for OpGetGlobal resolution
- `(ec) emitDeferredResumes()` — emits all deferred resume points at end of function
- `(ec) emitStoreAllActiveRegs()` / `emitReloadAllActiveRegs()` — spill/reload around exits
**Build tag**: `darwin && arm64`
**Coder notes**: Resume labels use `callExitResumeLabel(instrID)`. All active SSA values must be spilled before exit. `emitUnboxRawIntRegs` boxes raw-int registers before exit resume for cross-block correctness.

---

## emit_call_native.go (404 lines)
**Purpose**: Native BLR call emission for Tier 2 — ~10ns vs ~80ns for call-exit; spills/reloads all live SSA registers around the BLR.
**Key functions**:
- `(ec) emitCallNative(instr)` — full native call sequence (store args, spill, BLR, reload, check exit)
- `(ec) emitCallExitFallback(instr, ...)` — falls back to slow call-exit path
- `(ec) computeLiveAcrossCall(callInstr) (gprLive, fprLive map[int]bool)` — liveness for selective spill
- `(ec) emitSpillSelectiveForCall/ReloadSelectiveForCall(...)` — selective spill/reload
**Build tag**: `darwin && arm64`
**Coder notes**: Selective spill: only live-across-call values are spilled. Callee may be uncompiled (DirectEntryPtr=0) — falls to slow path. Callee register window bounds checked before BLR.

---

## emit_execute.go (427 lines)
**Purpose**: `CompiledFunction.Execute` — the Tier 2 execution loop for standalone (non-TieringManager) compiled functions; handles all exit codes.
**Key functions**:
- `(cf) Execute(args) ([]runtime.Value, error)` — main execute entry; runs JIT in a loop
- `(cf) executeCallExit(ctx, regs)` — call-exit handler (standalone path)
- `(cf) executeGlobalExit(ctx, regs)` — global-exit handler
- `(cf) executeTableExit(ctx, regs)` — table op handler
- `(cf) executeOpExit(ctx, regs)` — generic op handler
**Build tag**: `darwin && arm64`
**Coder notes**: TieringManager uses `tiering_manager_exit.go` instead. `Execute` is used by tests and the standalone Tier 2 path. Exit handler logic mirrors `tiering_manager_exit.go`.

---

## emit_table_array.go (696 lines)
**Purpose**: ARM64 emission for OpNewTable, OpGetTable, OpSetTable — integer-keyed array ops with inline fast paths and exit-resume fallbacks.
**Key functions**:
- `(ec) emitGetTableNative(instr)` — inline bounds-checked array read with type-specialized result
- `(ec) emitSetTableNative(instr)` — inline bounds-checked array write
- `(ec) emitNewTableExit/GetTableExit/SetTableExit(instr)` — exit-resume fallbacks
**Build tag**: `darwin && arm64`
**Coder notes**: Inline path checks array bounds and element type. SetTable exit sets `tableVerified=false` in LICM context. Shape checks for array vs hash need IC fast-path guards.

---

## emit_table_field.go (341 lines)
**Purpose**: ARM64 emission for OpGetField and OpSetField — named field access with inline shape-checked fast paths and exit-resume fallbacks.
**Key functions**:
- `(ec) emitGetField(instr)` — inline fast path: shape check + direct slot offset load
- `(ec) emitSetField(instr)` — inline fast path: shape check + direct slot offset store
- `(ec) emitGetFieldExit/SetFieldExit(instr)` — exit-resume fallbacks
**Build tag**: `darwin && arm64`
**Coder notes**: Shape/IC checking uses `proto.FieldCache`. `emitGetField` can be hoisted by LICM when the object and shape are loop-invariant.

---

## emit_op_exit.go (202 lines)
**Purpose**: Generic op-exit (ExitCode=5) for ops the JIT cannot handle natively (concat, closures, etc.) and SetList exit.
**Key functions**:
- `(ec) emitOpExit(instr)` — stores op descriptor in ExecContext and exits with code=5
- `(ec) emitSetListExit(instr)` — table literal initialization exit
**Build tag**: `darwin && arm64`
**Coder notes**: OpExit handlers in `emit_execute.go`/`tiering_manager_exit.go` decode ExitCode=5 by opcode. Adding a new op-exit requires handler cases in both execute files.

---

## emit_reg.go (413 lines)
**Purpose**: Register resolution helpers — maps value IDs to physical registers or stack slots, handles raw-int/raw-float modes, cross-block liveness.
**Key functions**:
- `(ec) physReg(valueID) jit.Reg` / `physFPReg(valueID) jit.FReg` — look up physical register
- `(ec) resolveValueNB(valueID, scratch)` — load NaN-boxed value into a register
- `(ec) resolveRawInt/RawFloat(...)` — load raw integer/float (loop body mode)
- `(ec) storeResultNB/storeRawInt/storeRawFloat(...)` — write result back
- `computeCrossBlockLive(fn) map[int]bool` — values live across block boundaries need memory slots
**Build tag**: `darwin && arm64`
**Coder notes**: Values with physical register allocations AND cross-block liveness have both a register and a memory home. `invalidateReg` tracks which value ID owns each physical register (eviction bookkeeping).

---

## emit_loop.go (364 lines)
**Purpose**: Loop raw-int mode analysis — computes which phi values can stay as raw int64 throughout the loop body, and which FPR values are safe at loop headers.
**Key functions**:
- `computeLoopPhiArgs(fn, li, alloc, ...) loopPhiArgSet` — identifies values only used as loop phi args
- `(li) computeHeaderExitRegs/FPRegs(fn, alloc)` — registers that need boxing at each loop exit
- `computeSafeHeaderRegs/FPRegs(...)` — registers stable across the loop header
**Build tag**: `darwin && arm64`
**Dependencies**: loops.go, regalloc.go
**Coder notes**: Raw-int mode is per-loop-header. `isRawIntOp(op)` / `isRawFloatOp(op)` define which ops produce raw typed values (vs NaN-boxed). Phi moves to loop headers skip boxing when the source is raw-int.

---

## ir.go (138 lines)
**Purpose**: Core IR type definitions — Function, Block, Instr, Value, Type.
**Key types**:
- `Function` — entry block, all blocks (RPO), Proto, NumRegs, Int48Safe map, Globals map
- `Block` — ID, Instrs, Preds, Succs, sealed/filled flags
- `Instr` — ID, Op, Type, Args ([]*Value), Aux/Aux2 (int64 payload), Def (*Value)
- `Value` — ID, Type, Def (*Instr) back-pointer
- `Type` — TypeUnknown/Int/Float/Bool/Nil/String/Table (uint8 enum)
**Coder notes**: `fn.Int48Safe` populated by RangeAnalysisPass; consumed by emitter. `fn.Globals` set by TieringManager before calling `RunTier2Pipeline` for inline candidate lookup. `Aux` stores constant values, slot numbers, field indices; `Aux2` stores extra flags (e.g., loop-counter exemption flag =1).

---

## ir_ops.go (221 lines)
**Purpose**: Op enum with every IR opcode and their docstrings; `IsTerminator()` predicate.
**Key types**: `Op` — uint8 opcode enum
**Key categories**:
- Constants: OpConstInt/Float/Bool/Nil/String
- Slot access: OpLoadSlot/StoreSlot
- Generic arithmetic: OpAdd/Sub/Mul/Div/Mod/Pow/Unm/Not
- Typed arithmetic: OpAddInt/SubInt/MulInt/NegInt/ModInt (int48-range); OpAddFloat/SubFloat/MulFloat/DivFloat/NegFloat
- Comparisons: OpEq/Ne/Lt/Le/Gt/Ge + Int/Float variants
- Guards: OpGuardType/GuardTruthy/GuardNonNil
- Table: OpGetField/SetField/GetTable/SetTable/NewTable/SetList/Append
- Calls: OpCall/Self/CallNative/GetGlobal/SetGlobal
- Misc: OpPhi/Jump/Branch/Return/Deopt/Nop/LoadSlotToReg/Sqrt
**Coder notes**: Adding a new Op requires updating: `ir_ops.go` (enum + name), `ir_ops.go IsTerminator` if needed, `pass_dce.go hasSideEffect`, `pass_typespec.go inferType/specialize`, emitter dispatch in `emit_dispatch.go emitInstr`, and optionally `canHoistOp` in LICM.

---

## pipeline.go (364 lines)
**Purpose**: Ordered named pass pipeline with enable/disable, IR snapshotting, diff, and built-in Tier 2 pipeline constructor.
**Key types**: `Pipeline` — ordered pass list; `PassFunc func(*Function) (*Function, error)`; `Tier2PipelineOpts` — inlining/intrinsic config
**Key functions**:
- `NewPipeline() *Pipeline` — empty pipeline
- `RunTier2Pipeline(fn, opts) (*Function, []string, error)` — canonical Tier 2 pipeline
- `NewTier2Pipeline() *Pipeline` — returns pipeline without running it
- `(p) EnableDump(on)` / `Dump()` / `Diff(a, b)` — debugging snapshots
**Coder notes**: Pass order is hardcoded in `RunTier2Pipeline`. TypeSpec runs 3× (index 1, 3, 5) to handle newly inlined code. Validator runs between passes when `p.SetValidator(Validate)` is called.

---

## validator.go (266 lines)
**Purpose**: Structural IR invariant checker — run after every pass to catch bugs early.
**Key functions**:
- `Validate(fn) []error` — checks entry block, terminator placement, succ/pred consistency, branch arg counts, value ID uniqueness, reachability
**Coder notes**: Call `Validate` in tests with `assertValidates(t, fn, "after pass X")`. Pipeline calls it automatically when `SetValidator` is set. Empty error list = well-formed IR.

---

## diagnose.go (275 lines)
**Purpose**: One-call diagnostic tool — compiles through full pipeline, runs IR interpreter and native ARM64, compares results.
**Key types**: `DiagReport` — full diagnostic output with IR before/after, regalloc dump, interpreter vs JIT results
**Key functions**:
- `Diagnose(proto, args) *DiagReport` — end-to-end diagnostic
- `(r) String()` — human-readable report with diff summary
**Build tag**: `darwin && arm64`
**Coder notes**: Use `Diagnose()` as first debugging step before reading source code. `report.Match` is false when interpreter and JIT disagree. `IRBefore`/`IRAfter` show pre/post optimization IR.

---

## func_profile.go (142 lines)
**Purpose**: Static bytecode analysis that drives smart Tier 2 promotion thresholds.
**Key types**: `FuncProfile` — HasLoop, LoopDepth, BytecodeCount, ArithCount, CallCount, TableOpCount, HasClosure
**Key functions**:
- `analyzeFuncProfile(proto) FuncProfile` — single-pass bytecode scan
- `shouldPromoteTier2(proto, profile, runtimeCallCount) bool` — tiering decision
**Build tag**: `darwin && arm64`
**Coder notes**: Used by TieringManager only. Profile is computed once and cached per proto. Adding a new promotion heuristic: update `shouldPromoteTier2`, not `TryCompile` directly.

---

## tier1_arith.go (895 lines)
**Purpose**: Tier 1 baseline ARM64 emission for arithmetic, comparisons, boolean ops, and int-specialization fast paths.
**Key functions**:
- `emitBaselineArith(asm, inst, op)` — generic ADD/SUB/MUL with type-dispatch
- `emitBaselineArithIntSpec(asm, inst, op)` — int-specialized fast path (no type check)
- `emitBaselineEQ/LT/LE/Test/TestSet(asm, inst, pc, code)` — comparison + conditional jump
- `emitParamIntGuards(asm, guardedParams)` — bitfield-driven int guards at function entry
- `emitToFloat(asm, fpReg, gpReg, ...)` — NaN-unbox to floating point
**Build tag**: `darwin && arm64`

---

## tier1_call.go (554 lines)
**Purpose**: Tier 1 native BLR call sequence — the fast intra-JIT call path (~10ns).
**Key functions**:
- `emitBaselineNativeCall(asm, inst, pc, callerProto)` — full native call: type-check callee, BLR, restore state
- `emitDirectEntryPrologue/Epilogue(asm)` — called callee's entry/exit for direct BLR
**Build tag**: `darwin && arm64`

---

## tier1_table.go (829 lines)
**Purpose**: Tier 1 ARM64 emission for table/field/global ops and feedback collection.
**Key functions**:
- `emitBaselineGetField/SetField(asm, inst, pc)` — shape-checked inline fast paths
- `emitBaselineGetTable/SetTable(asm, inst, pc)` — array-indexed fast paths
- `emitBaselineGetGlobal(asm, inst, pc)` — global variable read with IC cache
- `emitBaselineFeedbackResult(asm, pc, expectedFB, suffix)` — writes type feedback for Tier 2
**Build tag**: `darwin && arm64`

---

## tier1_int_analysis.go (339 lines)
**Purpose**: Static dataflow analysis to identify bytecode slots that always hold integers, enabling int-spec compilation path in Tier 1.
**Key types**: `knownIntInfo` — per-PC bitfield of known-int slots
**Key functions**:
- `computeKnownIntSlots(proto) (*knownIntInfo, bool)` — returns false if analysis fails
- `(k) isKnownIntOperand(pc, idx, consts) bool` — per-instruction query

---

## interp.go (562 lines) + interp_ops.go (98 lines)
**Purpose**: IR interpreter for testing/debugging — executes the SSA IR directly without code generation; used by `Diagnose` to produce reference output.
**Key functions**:
- `Interpret(fn, args) ([]runtime.Value, error)` — entry point
- `(s) execInstr(instr, block) ([]runtime.Value, bool, error)` — per-instruction execution
- `(s) resolveTerminator(instr, block) (*Block, error)` — control flow resolution
**Coder notes**: Used only in tests and `Diagnose`. Not performance-critical. New Op must be handled here too or `Diagnose` will report errors for that op.

---

## printer.go (140 lines)
**Purpose**: Human-readable IR dump — `Print(fn)` returns all blocks, instructions, phi nodes, and edges as a string.
**Key functions**:
- `Print(fn) string` — top-level IR printer
**Coder notes**: Use `Print(fn)` in tests for golden-file comparisons. Pipeline's `Dump()` calls this internally.

---

## emit.go (257 lines)
**Purpose**: Shared ARM64 data structures — ExecContext layout, exit codes, pinned register aliases, CompiledFunction struct.
**Key types**: `ExecContext` — Go/JIT calling convention struct (Regs, Constants, ExitCode, CallSlot, etc.); `CompiledFunction` — native code pointer, metadata
**Key constants**: `mRegCtx=X19`, `mRegTagInt=X24`, `mRegTagBool=X25`, `mRegRegs=X26`, `mRegConsts=X27`
**Exit codes**: 0=normal, 2=deopt, 3=call-exit, 4=global-exit, 5=table/op-exit
**Build tag**: `darwin && arm64`
**Coder notes**: ExecContext layout is ABI — any change must update both emit_*.go (writer side) and tiering_manager_exit.go/emit_execute.go (reader side). Do not reorder fields.

---

## tier.go (23 lines)
**Purpose**: Tier threshold constants and architecture overview comment.
**Key constants**: `Tier0Threshold=0`, `Tier1Threshold=2`, `Tier2Threshold=100`
