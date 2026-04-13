> Category: general | Generated: 2026-04-11 | Auto-load: yes

# GScript MethodJIT — Codebase Index

Quick navigation index for `internal/methodjit/`. All files, key types, and key functions.

---

## File Index

| File | Lines | Purpose |
|------|-------|---------|
| ir.go | 138 | Core IR types: Function, Block, Instr, Value, Type |
| ir_ops.go | 221 | Op enum with all IR opcodes; IsTerminator() |
| pipeline.go | 364 | Named pass pipeline, IR snapshots, RunTier2Pipeline |
| validator.go | 266 | Structural IR invariant checker (Validate) |
| printer.go | 140 | Human-readable IR dump (Print) |
| diagnose.go | 275 | One-call JIT diagnostic: IR interp vs native comparison |
| interp.go | 562 | IR interpreter for testing/debug |
| interp_ops.go | 98 | Call + global lookup for IR interpreter |
| graph_builder.go | 955 | Bytecode → CFG SSA IR (Braun 2013 algorithm) |
| graph_builder_ssa.go | 237 | SSA variable resolution, phi insertion helpers |
| loops.go | 429 | Loop analysis: dominators, RPO, natural loops, pre-headers |
| regalloc.go | 684 | Forward-walk LRU register allocator (GPR X20-X23,X28; FPR D4-D11) |
| pass_typespec.go | 529 | Type specialization: generic→typed ops, param guards |
| pass_intrinsic.go | 89 | math.sqrt() → OpSqrt rewrite |
| pass_inline.go | 726 | Function inlining (trivial + multi-block, transitive) |
| pass_constprop.go | 256 | Constant folding: int, float, generic arithmetic |
| pass_load_elim.go | 129 | Block-local GetField CSE, store-to-load forwarding, GuardType CSE |
| pass_dce.go | 96 | Dead code elimination (fixed-point) |
| pass_range.go | 511 | Integer range analysis → fn.Int48Safe (skip overflow checks) |
| pass_licm.go | 594 | Loop-invariant code motion with pre-header insertion |
| func_profile.go | 142 | Static bytecode analysis for tiering thresholds |
| tier.go | 23 | Tier threshold constants and architecture overview |
| tier1_compile.go | 539 | Tier 1 baseline compiler entry point (CompileBaseline) |
| tier1_manager.go | 426 | Tier 1 engine lifecycle, execute loop, exit-resume dispatch |
| tier1_arith.go | 895 | Tier 1 ARM64 arithmetic, comparisons, int-spec fast paths |
| tier1_call.go | 554 | Tier 1 native BLR call sequence |
| tier1_control.go | 251 | Tier 1 control flow: JMP, FORPREP, FORLOOP, RETURN, TFORLOOP |
| tier1_handlers.go | 697 | Tier 1 exit handlers: calls, globals, tables, fields |
| tier1_handlers_misc.go | 276 | Tier 1 exit handlers: concat, closure, upval, vararg, etc. |
| tier1_int_analysis.go | 339 | Static int-slot analysis for Tier 1 int-spec eligibility |
| tier1_table.go | 829 | Tier 1 table/field/global ARM64 emission + feedback collection |
| tiering_manager.go | 743 | Multi-tier engine: auto-promotion Tier1→Tier2 |
| tiering_manager_exit.go | 500 | TieringManager Tier 2 exit handlers (call/global/table/op) |
| emit.go | 257 | Shared ARM64 data: ExecContext, exit codes, pinned regs, CompiledFunction |
| emit_compile.go | 640 | Tier 2 compile pipeline: emitContext, prologue/epilogue, block driver |
| emit_dispatch.go | 971 | Instruction dispatch, phi moves, control flow, loop-exit boxing |
| emit_arith.go | 319 | ARM64 integer arithmetic, slot access, int48 overflow checks |
| emit_call.go | 534 | Float arithmetic, typed float ops, div, guards, deopt |
| emit_call_exit.go | 360 | Call-exit (ExitCode=3) and global-exit (ExitCode=4) emission |
| emit_call_native.go | 404 | Native BLR call in Tier 2 (fast path, ~10ns) |
| emit_execute.go | 427 | CompiledFunction.Execute loop + all exit handlers |
| emit_loop.go | 364 | Raw-int loop mode analysis (phi args, header/exit regs) |
| emit_op_exit.go | 202 | Generic op-exit (ExitCode=5) and SetList exit |
| emit_reg.go | 413 | Register resolution helpers, cross-block liveness |
| emit_table_array.go | 696 | ARM64 OpNewTable/GetTable/SetTable emission (array fast path) |
| emit_table_field.go | 341 | ARM64 OpGetField/SetField emission (shape-checked fast path) |

---

## Type Index

| Type | File | Purpose |
|------|------|---------|
| Function | ir.go | Complete IR for one compiled function (blocks, proto, Int48Safe) |
| Block | ir.go | Basic block with Instrs, Preds, Succs, sealed/filled flags |
| Instr | ir.go | SSA instruction: ID, Op, Type, Args, Aux/Aux2, Def |
| Value | ir.go | SSA value: ID, Type, Def back-pointer to Instr |
| Type | ir.go | TypeUnknown/Int/Float/Bool/Nil/String/Table (uint8) |
| Op | ir_ops.go | IR opcode enum (uint8) — all ~60 ops |
| PassFunc | pipeline.go | `func(*Function) (*Function, error)` — pass signature |
| Pipeline | pipeline.go | Ordered named pass list with snapshot/diff support |
| Tier2PipelineOpts | pipeline.go | Inlining config + intrinsic enable for RunTier2Pipeline |
| Snapshot | pipeline.go | Named IR string snapshot between passes |
| DiagReport | diagnose.go | Full diagnostic: IR before/after, regalloc, interp vs JIT results |
| loopInfo | loops.go | Natural loop structure: headers, block sets, phis, nesting |
| domInfo | loops.go | Immediate dominator table |
| intRange | pass_range.go | [min, max] int range with saturation arithmetic |
| constVal | pass_constprop.go | Known constant: int64 or float64 |
| constProp | pass_constprop.go | Constant propagation pass state |
| typeSpecializer | pass_typespec.go | Type propagation state: value ID → Type map |
| InlineConfig | pass_inline.go | Inlining budget: MaxSize, globals map, iteration cap |
| loadKey | pass_load_elim.go | (objID, fieldAux) key for available loads |
| guardKey | pass_load_elim.go | (valueID, typeTag) key for GuardType CSE |
| knownIntInfo | tier1_int_analysis.go | Per-PC bitfield of known-int slots for Tier 1 int-spec |
| BaselineFunc | tier1_compile.go | Generated native code for Tier 1 baseline function |
| BaselineJITEngine | tier1_manager.go | Tier 1 JIT engine (vm.MethodJITEngine implementation) |
| TieringManager | tiering_manager.go | Multi-tier engine: Tier1 + Tier2 with auto-promotion |
| FuncProfile | func_profile.go | Static bytecode characteristics (loops, arith, calls, tables) |
| PhysReg | regalloc.go | Physical ARM64 register: {Reg int, IsFloat bool} |
| RegAllocation | regalloc.go | Complete SSA value ID → PhysReg mapping |
| regState | regalloc.go | Per-type (GPR/FPR) LRU allocation pool |
| ExecContext | emit.go | Go/JIT calling convention struct (passed via X19) |
| CompiledFunction | emit.go | Tier 2 native code + metadata (numRegs, exitPoints, etc.) |
| emitContext | emit_compile.go | Per-function ARM64 emission state (asm, fn, alloc, slotMap) |
| gprPhiMove | emit_dispatch.go | Single GPR phi move for dependency-aware ordering |
| loopPhiArgSet | emit_loop.go | Value IDs used only as loop phi args (skip memory write-through) |
| loopRegEntry | emit_loop.go | Register info at loop header exit point |
| deferredResume | emit_call_exit.go | Deferred call/global-exit resume point |
| ExecContext | emit.go | See above — also used by tier1_manager.go and tiering_manager.go |

---

## Function Index (key functions only)

### IR Construction & Analysis

| Function | File | What it does |
|----------|------|--------------|
| `BuildGraph(proto) *Function` | graph_builder.go | Entry point: bytecode → CFG SSA IR |
| `(b) emitBlocks()` | graph_builder.go | Main bytecode→IR walk (one case per opcode) |
| `(b) sealBlock(block)` | graph_builder_ssa.go | Seal a block for SSA phi resolution |
| `(b) readVariable(slot, block)` | graph_builder_ssa.go | SSA variable lookup (recursive phi insertion) |
| `(b) writeVariable(slot, block, val)` | graph_builder_ssa.go | SSA variable definition |
| `(b) tryRemoveTrivialPhi(phi)` | graph_builder_ssa.go | Collapse single-value phis |
| `feedbackToIRType(fb) (Type, bool)` | graph_builder.go | Map interpreter feedback to IR type hint |
| `Validate(fn) []error` | validator.go | Check all CFG SSA structural invariants |
| `Print(fn) string` | printer.go | Human-readable IR dump |
| `Interpret(fn, args)` | interp.go | Reference IR interpreter (for Diagnose/testing) |
| `Diagnose(proto, args) *DiagReport` | diagnose.go | Full JIT diagnostic: compile + interp + native comparison |

### Loop Analysis

| Function | File | What it does |
|----------|------|--------------|
| `computeLoopInfo(fn) *loopInfo` | loops.go | Full natural loop analysis from CFG |
| `computeDominators(fn) *domInfo` | loops.go | Cooper iterative dominator computation |
| `computeRPO(fn) []*Block` | loops.go | Reverse post-order block ordering |
| `computeLoopPreheaders(fn, li)` | loops.go | Map each loop header to its pre-header block ID |
| `loopDepths(li) map[int]int` | loops.go | Per-block loop depth (used by LICM for innermost-first) |
| `loopPreds(li, hdr)` | loops.go | Split loop header predecessors into inside/outside |
| `collectLoopBlocks(block, header, set)` | loops.go | Collect all blocks in a natural loop |

### Optimization Passes

| Function | File | What it does |
|----------|------|--------------|
| `TypeSpecializePass(fn)` | pass_typespec.go | Type propagation + param guards + op specialization |
| `(ts) insertParamGuards(fn)` | pass_typespec.go | Insert GuardType on params in int contexts |
| `(ts) insertFloatParamGuards(fn)` | pass_typespec.go | Insert GuardType on params in float contexts |
| `(ts) specialize(instr)` | pass_typespec.go | Replace generic op with typed variant |
| `IntrinsicPass(fn)` | pass_intrinsic.go | Rewrite math.sqrt() → OpSqrt |
| `InlinePassWith(config) PassFunc` | pass_inline.go | Returns configured inlining pass |
| `inlineTrivial(...)` | pass_inline.go | Inline single-block callee |
| `inlineMultiBlock(...)` | pass_inline.go | Inline multi-block callee with ID remapping |
| `resolveCallee(callInstr, fn, config)` | pass_inline.go | Resolve callee proto from OpGetGlobal |
| `relinkValueDefs(fn)` | pass_inline.go | Relink value→instr back-pointers after inline |
| `ConstPropPass(fn)` | pass_constprop.go | Fold constant arithmetic expressions |
| `LoadEliminationPass(fn)` | pass_load_elim.go | GetField CSE + store-to-load forwarding + GuardType CSE |
| `replaceAllUses(fn, oldID, newInstr)` | pass_load_elim.go | Rewire all references to a value ID |
| `DCEPass(fn)` | pass_dce.go | Remove dead side-effect-free instructions (fixed-point) |
| `hasSideEffect(instr) bool` | pass_dce.go | Returns true for stores/calls/branches/guards |
| `RangeAnalysisPass(fn)` | pass_range.go | Compute int ranges, populate fn.Int48Safe |
| `seedLoopRanges(fn, ranges)` | pass_range.go | Seed FORLOOP counter ranges from concrete values |
| `computeRange(instr, ranges)` | pass_range.go | Per-instruction range from operand ranges |
| `LICMPass(fn)` | pass_licm.go | Loop-invariant code motion with pre-header insertion |
| `hoistOneLoop(fn, li, hdr)` | pass_licm.go | Hoist invariants for one loop header |
| `canHoistOp(op) bool` | pass_licm.go | Conservative hoist-safe whitelist |
| `insertBlockBefore(fn, blk, target)` | pass_licm.go | Insert new pre-header block before target |

### Pipeline

| Function | File | What it does |
|----------|------|--------------|
| `RunTier2Pipeline(fn, opts)` | pipeline.go | Canonical Tier 2 optimization pipeline (TypeSpec×3, Inline, ConstProp, LoadElim, DCE, Range, LICM) |
| `NewTier2Pipeline() *Pipeline` | pipeline.go | Returns configured pipeline without running it |
| `(p) Run(fn)` | pipeline.go | Execute all enabled passes in order |
| `(p) Add(name, fn)` | pipeline.go | Register a pass by name |
| `(p) Enable/Disable(name)` | pipeline.go | Toggle individual passes |
| `(p) SetValidator(v)` | pipeline.go | Run Validate after every pass (debug mode) |
| `(p) EnableDump(on)` | pipeline.go | Snapshot IR after every pass |
| `(p) Dump()` / `Diff(a, b)` | pipeline.go | Print all snapshots / diff two pass outputs |

### Register Allocation

| Function | File | What it does |
|----------|------|--------------|
| `AllocateRegisters(fn) *RegAllocation` | regalloc.go | Top-level: allocate all SSA values to physical registers |
| `allocateBlock(block, alloc, lastUse, carried)` | regalloc.go | Per-block forward-walk allocation |
| `preAllocateHeaderPhis(block, alloc)` | regalloc.go | Stable register assignment for loop header phis |
| `computeLastUse(fn) map[int]int` | regalloc.go | Last-use block index per value (for free-at-last-use) |
| `collectLoopBoundGPRs(hdr, alloc)` | regalloc.go | GPRs that stay live across the loop back-edge |
| `(rs) findFree() int` | regalloc.go | Find a free physical register in the pool |
| `(rs) evictLRU() (reg, evictedID)` | regalloc.go | Evict least-recently-used register |

### Tier 2 Emission (ARM64, darwin only)

| Function | File | What it does |
|----------|------|--------------|
| `Compile(fn, alloc) (*CompiledFunction, error)` | emit_compile.go | Top-level Tier 2 code generation |
| `(ec) emitPrologue()` / `emitEpilogue()` | emit_compile.go | Frame setup/teardown, callee-saved reg save/restore |
| `(ec) emitBlock(block)` | emit_compile.go | Drive per-instruction emission for one block |
| `(ec) assignSlots()` | emit_compile.go | Assign stack slots for spilled values |
| `(ec) emitInstr(instr, block)` | emit_dispatch.go | Dispatch to per-op emitter |
| `(ec) emitPhiMoves(from, to)` | emit_dispatch.go | Parallel phi move resolution with cycle detection |
| `(ec) emitGPRPhiMovesOrdered(to, predIdx, isLoopHeader)` | emit_dispatch.go | Topological GPR phi move ordering |
| `(ec) emitBranch(instr, block)` | emit_dispatch.go | Conditional branch with optional comparison fusion |
| `(ec) emitJump(instr, block)` | emit_dispatch.go | Unconditional jump |
| `(ec) emitReturn(instr, block)` | emit_dispatch.go | Return from function |
| `(ec) emitLoopExitBoxing(exitingHeaderID)` | emit_dispatch.go | Box raw-int phi values when exiting loop |
| `(ec) emitGuardType(instr)` | emit_call.go | NaN-box tag check; deopt (ExitCode=2) on mismatch |
| `(ec) emitDeopt(instr)` | emit_call.go | Jump to deopt_epilogue |
| `(ec) emitGuardTruthy(instr)` | emit_call.go | Conditional branch on truthy check |
| `(ec) emitIntBinOp(instr, op)` | emit_arith.go | Integer add/sub/mul with optional int48 overflow check |
| `(ec) emitInt48OverflowCheck(result, instr)` | emit_arith.go | SBFX+CMP+B.NE guard (skipped if int48Safe) |
| `(ec) emitIntCmp(instr, cond)` | emit_arith.go | Integer comparison → NaN-boxed bool |
| `(ec) emitFloatBinOp/TypedFloatBinOp(instr, op)` | emit_call.go | Float arithmetic (generic and typed) |
| `(ec) emitSqrtFloat(instr)` | emit_call.go | Single FSQRT instruction |
| `(ec) emitCallNative(instr)` | emit_call_native.go | Native BLR call (~10ns); spills/reloads live regs |
| `(ec) emitCallExit(instr)` | emit_call_exit.go | Slow call-exit via Go (~80ns) |
| `(ec) emitGlobalExit(instr)` | emit_call_exit.go | Exit for OpGetGlobal resolution |
| `(ec) emitDeferredResumes()` | emit_call_exit.go | Emit all resume points at end of function |
| `(ec) emitStoreAllActiveRegs()` | emit_call_exit.go | Spill all live SSA registers before exit |
| `(ec) emitReloadAllActiveRegs()` | emit_call_exit.go | Reload all live SSA registers after resume |
| `(ec) emitOpExit(instr)` | emit_op_exit.go | Generic op-exit (ExitCode=5) |
| `(ec) emitSetListExit(instr)` | emit_op_exit.go | SetList table literal initialization exit |
| `(ec) emitGetTableNative(instr)` | emit_table_array.go | Inline array read (bounds + type check) |
| `(ec) emitSetTableNative(instr)` | emit_table_array.go | Inline array write |
| `(ec) emitGetField(instr)` | emit_table_field.go | Shape-checked inline field read |
| `(ec) emitSetField(instr)` | emit_table_field.go | Shape-checked inline field write |
| `(ec) physReg(valueID) jit.Reg` | emit_reg.go | Look up physical GPR for a value |
| `(ec) physFPReg(valueID) jit.FReg` | emit_reg.go | Look up physical FPR for a value |
| `(ec) resolveValueNB(valueID, scratch)` | emit_reg.go | Load NaN-boxed value into register |
| `(ec) resolveRawInt(valueID, scratch)` | emit_reg.go | Load raw int64 (loop mode) |
| `(ec) resolveRawFloat(valueID, scratch)` | emit_reg.go | Load raw float64 (loop mode) |
| `(ec) storeResultNB(src, valueID)` | emit_reg.go | Write NaN-boxed result back |
| `(ec) int48Safe(id) bool` | emit_arith.go | Check fn.Int48Safe for overflow-check skip |
| `computeCrossBlockLive(fn) map[int]bool` | emit_reg.go | Values that need memory home slots |
| `computeLoopPhiArgs(fn, li, alloc, ...)` | emit_loop.go | Values only used as loop phi args |
| `(li) computeHeaderExitRegs(fn, alloc)` | emit_loop.go | GPRs that need boxing at loop exits |

### Execute / Exit Handlers

| Function | File | What it does |
|----------|------|--------------|
| `(cf) Execute(args)` | emit_execute.go | Run Tier 2 compiled function; handles all exit codes |
| `(cf) executeCallExit(ctx, regs)` | emit_execute.go | Standalone Tier 2 call handler |
| `(cf) executeGlobalExit(ctx, regs)` | emit_execute.go | Standalone Tier 2 global handler |
| `(cf) executeTableExit(ctx, regs)` | emit_execute.go | Standalone Tier 2 table handler |
| `(cf) executeOpExit(ctx, regs)` | emit_execute.go | Standalone Tier 2 generic op handler |
| `(tm) executeCallExit(ctx, regs, base, proto)` | tiering_manager_exit.go | TieringManager call handler |
| `(tm) executeGlobalExit(ctx, regs, base, proto, cf)` | tiering_manager_exit.go | TieringManager global handler |
| `(tm) executeTableExit(ctx, regs, base, proto)` | tiering_manager_exit.go | TieringManager table handler |
| `(tm) executeOpExit(ctx, regs, base, proto)` | tiering_manager_exit.go | TieringManager generic op handler |
| `(tm) executeClosureOpExit(ctx, regs, base)` | tiering_manager_exit.go | Closure creation exit |
| `(tm) executeGetUpvalOpExit(ctx, regs, base)` | tiering_manager_exit.go | Upvalue read exit |
| `(tm) executeSetUpvalOpExit(ctx, regs, base)` | tiering_manager_exit.go | Upvalue write exit |

### Tier 1 Baseline

| Function | File | What it does |
|----------|------|--------------|
| `CompileBaseline(proto) (*BaselineFunc, error)` | tier1_compile.go | Tier 1 compile entry: linear bytecode→ARM64 templates |
| `emitBaselinePrologue/Epilogue(asm)` | tier1_compile.go | Tier 1 frame setup/teardown |
| `emitBaselineOpExit(asm, inst, pc, op)` | tier1_compile.go | Store op descriptor and exit to Go |
| `emitBaselineArith(asm, inst, op)` | tier1_arith.go | Generic arithmetic (type-dispatch at runtime) |
| `emitBaselineArithIntSpec(asm, inst, op)` | tier1_arith.go | Int-spec fast path (no type check) |
| `emitBaselineEQ/LT/LE(asm, inst, pc, code)` | tier1_arith.go | Comparison + conditional jump |
| `emitParamIntGuards(asm, guardedParams)` | tier1_arith.go | Bitfield-driven int guards at function entry |
| `emitBaselineNativeCall(asm, inst, pc, callerProto)` | tier1_call.go | Native BLR call sequence |
| `emitBaselineGetField/SetField(asm, inst, pc)` | tier1_table.go | Shape-checked inline field ops |
| `emitBaselineGetTable/SetTable(asm, inst, pc)` | tier1_table.go | Array-indexed fast paths |
| `emitBaselineGetGlobal(asm, inst, pc)` | tier1_table.go | Global read with IC cache |
| `emitBaselineFeedbackResult(asm, pc, ...)` | tier1_table.go | Write type feedback for Tier 2 |
| `emitBaselineForPrep/ForLoop(asm, inst, pc)` | tier1_control.go | Numeric for-loop setup and step |
| `computeKnownIntSlots(proto) (*knownIntInfo, bool)` | tier1_int_analysis.go | Static known-int slot analysis |
| `(e) handleCall(ctx, regs, base, proto)` | tier1_handlers.go | Tier 1 exit handler for function calls |
| `(e) handleGetField/SetField(ctx, regs, ...)` | tier1_handlers.go | Tier 1 exit handler for field ops |
| `(e) handleGetTable/SetTable(ctx, regs, ...)` | tier1_handlers.go | Tier 1 exit handler for table ops |
| `(e) handleNativeCallExit(ctx, regs, ...)` | tier1_handlers.go | Tier 1 native BLR call return handler |
| `(e) handleClosure(ctx, regs, ...)` | tier1_handlers_misc.go | Tier 1 closure creation handler |

### Tiering

| Function | File | What it does |
|----------|------|--------------|
| `NewTieringManager() *TieringManager` | tiering_manager.go | Construct multi-tier engine |
| `(tm) TryCompile(proto)` | tiering_manager.go | Compile decision: nil/Tier1/Tier2 |
| `(tm) Execute(compiled, regs, base, proto)` | tiering_manager.go | Dispatch to Tier 1 or Tier 2 execute |
| `(tm) compileTier2(proto)` | tiering_manager.go | Full Tier 2 pipeline (graph→optimize→regalloc→emit) |
| `(tm) handleOSR(regs, base, proto)` | tiering_manager.go | On-stack replacement: Tier 1 → Tier 2 |
| `(tm) CompileTier2(proto) error` | tiering_manager.go | Public: compile proto to Tier 2 explicitly |
| `canPromoteToTier2(proto) bool` | tiering_manager.go | Pure compute + loop heuristic |
| `canPromoteWithInlining(proto, globals) bool` | tiering_manager.go | Checks call sites for inlineable globals |
| `analyzeFuncProfile(proto) FuncProfile` | func_profile.go | Single-pass static bytecode analysis |
| `shouldPromoteTier2(proto, profile, callCount)` | func_profile.go | Tiering promotion decision |
| `NewBaselineJITEngine() *BaselineJITEngine` | tier1_manager.go | Construct Tier 1 engine |
| `(e) TryCompile(proto)` | tier1_manager.go | Compile if call count ≥ threshold |
| `(e) Execute(compiled, regs, base, proto)` | tier1_manager.go | Tier 1 execute loop |

---

## Key Constants & Variables

| Name | File | Value / Purpose |
|------|------|-----------------|
| `allocatableGPRs` | regalloc.go | `[5]int{20,21,22,23,28}` — X20-X23, X28 |
| `allocatableFPRs` | regalloc.go | `[8]int{4,5,6,7,8,9,10,11}` — D4-D11 |
| `mRegCtx` | emit.go | `X19` — ExecContext pointer (pinned) |
| `mRegTagInt` | emit.go | `X24` — NaN int tag 0xFFFE000000000000 |
| `mRegTagBool` | emit.go | `X25` — NaN bool tag 0xFFFD000000000000 |
| `mRegRegs` | emit.go | `X26` — VM register base pointer |
| `mRegConsts` | emit.go | `X27` — constants pointer |
| `Tier0Threshold` | tier.go | `0` — interpreter always runs |
| `Tier1Threshold` | tier.go | `2` — baseline JIT after 2 calls |
| `Tier2Threshold` | tier.go | `100` — optimizing JIT after 100 calls |
| ExitCode=0 | emit.go | Normal return |
| ExitCode=2 | emit.go | Deopt (no resume) |
| ExitCode=3 | emit.go | Call-exit (resume after Go handles call) |
| ExitCode=4 | emit.go | Global-exit (resume after Go resolves global) |
| ExitCode=5 | emit.go | Table/op-exit (resume after Go handles op) |
