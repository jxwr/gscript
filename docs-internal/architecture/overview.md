# GScript JIT Architecture

Multi-tier Method JIT, modeled on V8 (Sparkplug → Maglev → TurboFan).

## Tiers

```
Tier 0: Interpreter (internal/vm/)
  Executes all bytecodes. Collects type feedback (FeedbackVector).
  → Tier 1 on first call (threshold=1)

Tier 1: Baseline JIT (internal/methodjit/tier1_*.go)
  1:1 bytecode → ARM64 templates, no IR, no optimization.
  Every value stays NaN-boxed. Key features:
  - Native BLR call (direct ARM64 branch to compiled callee)
  - Inline field cache (shape-guarded, per-PC)
  - GETGLOBAL value cache (generation-based invalidation)
  - Two entry points per function (normal 96B frame + direct 16B frame)
  - BLR call counter: increments callee's CallCount + falls to slow path at threshold
  - OSR counter in FORLOOP: triggers ExitOSR after N iterations for Tier 2 upgrade
  → Tier 2 via smart tiering (profile-based, see func_profile.go)

Tier 2: Optimizing JIT (internal/methodjit/)
  Bytecode → CFG SSA IR → Optimization passes → RegAlloc → ARM64
  Type-specialized registers, deopt guards, function inlining
  Two entry points: normal (128B frame for Execute) + direct (128B frame for BLR)
  After Tier 2 promotion, proto.DirectEntryPtr points to Tier 2's direct entry
  Tier 1 BLR callers seamlessly call Tier 2 code via DirectEntryPtr

Legacy: Trace JIT (internal/jit/) — deprecated, disconnected from CLI
```

## Tier 1 Architecture (current focus)

```
CompileBaseline(proto)
  → Scan bytecodes
  → Emit ARM64 templates per bytecode:
      Native: ADD, SUB, MUL, DIV, MOD, LT, LE, EQ, TEST,
              MOVE, LOADINT/K/BOOL/NIL, FORPREP, FORLOOP, JMP,
              RETURN, GETFIELD/SETFIELD (IC), GETTABLE/SETTABLE (bounds),
              GETGLOBAL (cache), CALL (native BLR)
      Exit-resume: NEWTABLE, CONCAT, LEN, CLOSURE, CLOSE,
              GETUPVAL, SETUPVAL, SELF, VARARG, TFORCALL, TFORLOOP,
              POW, SETLIST, APPEND
  → Two entry points: normal (for Execute loop) + direct (for BLR callee)
  → Resume stubs for each exit-resume PC
```

### Native BLR Call (R4)

Each compiled function has two entry points:
- **Normal entry** (96-byte frame): saves all callee-saved registers (X19-X28, FP, LR). Used by Go's `Execute()` loop via `callJIT`.
- **Direct entry** (16-byte frame): saves only FP+LR, reloads X26/X27 from ctx. Used by native BLR from caller JIT code.

Caller-side native CALL sequence (~18 ARM64 instructions):
1. Load function value, check VMClosure sub-type
2. Load `FuncProto.DirectEntryPtr` (zero = uncompiled → slow path)
3. Bounds check: callee register window fits in register file
4. Increment callee's `proto.CallCount` (3 insns: LDR, ADD, STR)
5. If CallCount == Tier2Threshold → fall to slow path (triggers Tier 2 compilation)
6. Save caller state (X26, X27, FP, LR) on native stack
7. Copy args to callee register window
8. Set up callee context (Regs, Constants, ClosurePtr, CallMode=1)
9. `BLR` to callee's direct entry
10. Restore caller state, read return value from `ctx.BaselineReturnValue`

Slow path fallback (exit-resume) for: GoFunctions, uncompiled closures, metatable __call,
and callees that just crossed the Tier 2 threshold (one-time detour to trigger compilation).

### Inline Field Cache

GETFIELD/SETFIELD use per-PC shape-guarded inline caches:
1. Load `FieldCache[pc]` from proto
2. Compare table's `shapeID` with cached `shapeID`
3. Hit: direct `svals[fieldIdx]` access (~5 ARM64 instructions)
4. Miss: exit to Go handler which does lookup + updates cache

### GETGLOBAL Cache

Per-PC value cache with generation-based invalidation:
1. Compare `engine.globalCacheGen` with `bf.CachedGlobalGen`
2. Hit: load value directly from `GlobalValCache[pc]`
3. Miss: exit to Go, load from globals, update cache
4. SETGLOBAL increments generation, clearing all caches

## Tier 2 Pipeline

```
BuildGraph (Braun et al. 2013)
  → Validate
  → TypeSpecialize   (generic OpAdd → OpAddInt when both int)
  → Intrinsic        (math.sqrt → OpSqrt, etc.)
  → TypeSpecialize   (propagate intrinsic result types)
  → Inline           (monomorphic small callees, bounded recursion)
  → TypeSpecialize   (re-run over inlined bodies)
  → ConstProp        (fold arithmetic on constants)
  → DCE              (remove unused values)
  → RangeAnalysis    (populate Int48Safe set; loop-counter exemption)
  → LICM             (hoist pure invariants into loop pre-header)
  → Validate
  → RegAlloc         (forward-walk: 5 GPR (X20-X23,X28), 8 FPR (D4-D11),
                       loop-phi FPR carry, int-counter GPR carry,
                       loop-bound GPR pinning, LICM-invariant FPR pinning)
  → Emit             (ARM64 code generation, fused compare+branch for
                       single-use comparisons)
```

## Tier 2 Opcode Coverage

Tier 2 handles ALL IR ops that the graph builder can produce:

**Native ARM64 fast paths:**
- Arithmetic: Add/Sub/Mul/Div/Mod + Int/Float specialized variants
- Comparison: Lt/Le/Eq + Int/Float specialized variants
- Unary: Unm/Not/NegInt/NegFloat
- Constants: ConstInt/Float/Bool/Nil/String(exit)
- Registers: LoadSlot/StoreSlot
- Tables: GetTable/SetTable(native), GetField/SetField(IC), NewTable(exit)
- Control: Branch/Jump/Return/Phi/Nop
- Guards: GuardType/GuardTruthy
- CALL: emitCallNative (selective spill/reload BLR, fallback to exit-resume)
- Globals: GetGlobal(native value cache, ~5ns hit, exit-resume on miss)

**Exit-resume (exit to Go, execute, resume):**
- SetGlobal, Self, Concat, Len, Pow, Append, Close, SetList
- Closure, GetUpval, SetUpval (closure/upvalue state from VM)
- Vararg (vararg state from VM frame)
- ForPrep, ForLoop, TForCall, TForLoop (rare: decomposed by graph builder)

**canPromoteToTier2 blocks only:**
- GO, MAKECHAN, SEND, RECV: goroutine/channel ops (require Go runtime)

## Key Design Points

- **Universal compilation**: Every function compiles on first call. Unsupported ops use exit-resume (exit to Go, execute, resume JIT at next PC).
- **Native BLR calls**: Both Tier 1 and Tier 2 support ARM64 `BLR` for compiled function calls. Tier 1: ~10ns per call. Tier 2: ~15-20ns per call (spill/reload SSA registers around BLR).
- **Deoptimization**: Type guard failures bail to interpreter.
- **NaN-boxing**: Every value is uint64. Float64 = raw IEEE 754 bits. Tagged values use quiet-NaN space (int=0xFFFE, bool=0xFFFD, ptr=0xFFFF, nil=0xFFFC). VMClosure uses ptr sub-type 8 for fast type checks.

## Register Convention (ARM64)

| Register | Role |
|----------|------|
| X19 | ExecContext pointer |
| X24 | Int tag constant (0xFFFE000000000000) |
| X25 | Bool tag constant |
| X26 | VM register base (callee's `regs[base]`) |
| X27 | Constants pointer |
| X20-X23 | Allocatable GPRs (4 primary) |
| X28 | Allocatable GPR (5th, freed from trace JIT) |
| D4-D11 | Allocatable FPRs (8) |

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Normal return |
| 2 | Deopt → interpreter |
| 3 | Call-exit (Tier 2: resume after Go handles call) |
| 4 | Global-exit (Tier 2) |
| 5 | Table-exit (Tier 2) |
| 6 | Op-exit (Tier 2: generic unsupported op) |
| 7 | Baseline op-exit (Tier 1: exit-resume) |
| 8 | Native call exit (Tier 1: callee hit exit during BLR call) |
| 9 | OSR (Tier 1: loop counter expired, request Tier 2 upgrade) |

## Smart Tiering (func_profile.go)

Profile-based promotion replaces simple call-count threshold:
- **Pure-compute + loop + arith (no calls/globals)**: Tier 2 at callCount=2
- **Loop + calls + arith**: Tier 2 at callCount=2
- **Loop + table ops**: Tier 2 at callCount=3
- **Calls only (no loops)**: stay Tier 1 (BLR is faster)
- **Default**: stay Tier 1
- **OSR**: currently disabled (comment in tiering_manager.go:151-155)

## On-Stack Replacement (OSR)

Simplified OSR: FORLOOP back-edge decrements `ctx.OSRCounter`. When zero:
1. Exit with `ExitOSR` (code 9)
2. TieringManager compiles Tier 2
3. Re-enters the entire function from start at Tier 2
4. If Tier 2 fails, disables OSR and re-runs at Tier 1

## Infrastructure

- **IR Interpreter** (`interp.go`): Correctness oracle. `Interpret(graph, args)` must match `VM.Execute(proto, args)`.
- **IR Validator** (`validator.go`): Structural invariants after every pass.
- **IR Printer** (`printer.go`): Human-readable dump for debugging.
- **Diagnose** (`diagnose.go`): One-call full pipeline diagnostic.
