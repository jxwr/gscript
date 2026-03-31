# ADR: Tier 2 Optimizing JIT Design

**Date**: 2026-03-31
**Status**: Proposed
**Decision**: Tier 2 architecture and implementation plan

## Context

Tier 1 baseline JIT achieves 19/22 benchmarks faster than VM (1.2-8x). Three benchmarks remain slower (NEWTABLE-dominated). The goal for Tier 2 is 5-10x faster than Tier 1 on compute-heavy benchmarks, approaching LuaJIT performance.

### Existing Infrastructure (all tested and working)

| Component | File | Lines | Status |
|-----------|------|-------|--------|
| SSA IR | ir.go, ir_ops.go | 130+230 | 45 ops including type-specialized (OpAddInt) |
| Graph Builder | graph_builder.go | 832 | Braun SSA construction |
| IR Interpreter | interp.go | 558 | Correctness oracle |
| Validator | validator.go | 266 | Post-pass invariant checks |
| Pipeline | pipeline.go | 238 | Pass registration + execution |
| TypeSpecialize | pass_typespec.go | 258 | OpAdd → OpAddInt when feedback says both int |
| ConstProp | pass_constprop.go | 256 | Fold constant expressions |
| DCE | pass_dce.go | 96 | Remove unused values |
| Inline | pass_inline.go | 517 | Inline small callees |
| RegAlloc | regalloc.go | 264 | Forward-scan, 5 GPR + 8 FPR |
| Tier 2 Emit | tier2_compile.go + tier2_emit.go | 375+811 | Memory-to-memory ARM64 |
| Tier 3 Emit | tier3_compile.go + tier3_emit.go | 258+866 | Register-allocated ARM64 |
| Diagnose | diagnose.go | 260 | Full pipeline diagnostic |

### Tier 2 Standalone Performance (micro-benchmarks, no VM integration)

| Workload | VM | Tier 2 | Tier 3 |
|----------|-----|--------|--------|
| Sum(10000) | 89µs | 29µs (3.1x) | 24µs (3.7x) |
| Add(3,4) | 37ns | 30ns (1.2x) | 29ns (1.2x) |
| Branch(15) | 48ns | 31ns (1.5x) | 30ns (1.6x) |

### What's Missing for Tier 2 VM Integration

1. **Tiering manager**: Automatic promotion Tier 1 → Tier 2
2. **Call-exit in VM context**: Tier 2's Execute handles calls standalone, not via VM
3. **Deopt**: Type guard failure → bail to Tier 1 (not implemented)
4. **FeedbackVector integration**: TypeSpecialize reads feedback, but VM doesn't collect it for Tier 2

## Decision

### Architecture

```
                    ┌──────────────────────────────┐
                    │     TieringManager (NEW)      │
                    │  Implements vm.MethodJITEngine │
                    │                                │
                    │  TryCompile(proto) → decides:  │
                    │    count < 1   → nil            │
                    │    count < 100 → Tier1 code     │
                    │    count ≥ 100 → Tier2 code     │
                    │                                │
                    │  Execute(compiled, regs, ...)   │
                    │    → dispatches to Tier1 or    │
                    │      Tier2 Execute              │
                    └──────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼                               ▼
     ┌─────────────────┐            ┌─────────────────┐
     │ BaselineJITEngine│            │  Tier2Engine     │
     │  (existing)      │            │  (NEW)           │
     │                  │            │                  │
     │  Tier 1 code:    │            │  Tier 2 code:    │
     │  bytecode→ARM64  │            │  SSA IR→passes  │
     │  inline cache    │            │  →regalloc→ARM64│
     │  native BLR call │            │  type guards     │
     └─────────────────┘            │  deopt → Tier 1  │
                                    └─────────────────┘
```

### Key Design Decisions

**1. TieringManager as the single vm.MethodJITEngine**

- Replaces both `BaselineJITEngine` and `MethodJITEngine`
- Internally holds both engines, routes calls to the right one
- Proto tracks which tier it's currently compiled at
- Clean upgrade path: `proto.Tier` field (0=interp, 1=baseline, 2=optimized)

**2. Tier 2 target: type-specialized register code**

Tier 2 = existing SSA IR pipeline (BuildGraph → TypeSpec → ConstProp → DCE → RegAlloc → Emit)

But with these improvements over standalone:
- **VM integration**: Execute uses the VM's register file, handles call-exit via VM
- **Deopt**: Type guard failures bail to Tier 1 code for the same function
- **FeedbackVector-driven**: TypeSpecialize uses real runtime type data

**3. No Tier 3 for now**

Tier 2 (with regalloc, which is really what the existing "Tier 3" does) IS the optimizing tier. The naming is:
- Tier 0: Interpreter
- Tier 1: Baseline (existing BaselineJITEngine)
- Tier 2: Optimizing (SSA + passes + regalloc + ARM64)

The existing "Tier 2 memory-to-memory" code is dropped — it's slower than Tier 1 for most workloads.

**4. Deopt strategy: bail to Tier 1**

When a type guard fails:
1. Store the current SSA register state to VM register file (spill all)
2. Set exit code to ExitDeopt
3. TieringManager catches ExitDeopt, re-enters the function via Tier 1 code
4. Record deopt count; after N deopts, permanently downgrade to Tier 1

**5. Call handling: call-exit with optimization**

Tier 2 handles function calls via call-exit:
1. Spill live SSA registers to VM register file
2. Exit with ExitCallExit, including call metadata
3. TieringManager's Execute loop handles the call via VM
4. Reload SSA registers from VM register file
5. Continue execution

Future: inline small callees during SSA construction (pass_inline.go already works).

### Implementation Phases

**Phase 1: TieringManager + basic integration**
- New `tiering_manager.go`: implements vm.MethodJITEngine
- Manages both Tier 1 and Tier 2 compilation
- Tier 2 uses existing pipeline: BuildGraph → TypeSpec → ConstProp → DCE → RegAlloc → Compile
- Execute dispatches to appropriate tier
- Test: Sum(10000) runs through VM with Tier 2

**Phase 2: Call-exit in VM context**
- Tier 2's Execute handles ExitCallExit by calling VM.CallValue
- Test: fib(10) runs through VM with Tier 2

**Phase 3: Deopt framework**
- Type guards in generated ARM64 code
- Deopt → bail to Tier 1 for the same function
- Test: function that changes type mid-execution deopts correctly

**Phase 4: Full benchmark suite**
- All 22 benchmarks pass correctly with tiering
- Performance comparison: Tier 2 vs Tier 1 vs VM

### File Plan

| File | Purpose | Size |
|------|---------|------|
| `tiering_manager.go` (NEW) | TieringManager engine | ~400 lines |
| `tiering_manager_test.go` (NEW) | Tests | ~300 lines |
| `tier2_engine.go` (NEW) | Tier 2 compilation + Execute | ~500 lines |
| `tier2_engine_test.go` (NEW) | Tests | ~300 lines |
| `deopt.go` (NEW) | Deoptimization: SSA regs → VM regs | ~200 lines |
| `deopt_test.go` (NEW) | Tests | ~200 lines |

### Performance Target

| Benchmark | T1/VM | T2/T1 target | T2/VM target |
|-----------|-------|-------------|-------------|
| mandelbrot | 3.82x | 3-5x | 12-20x |
| fibonacci_iterative | 4.84x | 2-3x | 10-15x |
| fannkuch | 8.07x | 1.5-2x | 12-16x |
| sieve | 1.19x | 5-8x | 6-10x |
| matmul | 1.28x | 5-8x | 6-10x |
| fib | 1.24x | 3-5x | 4-6x |

### Risks

1. **Deopt complexity**: SSA register → VM register mapping requires precise snapshots
2. **Call-exit overhead**: Spilling all SSA registers per call is expensive
3. **Compile latency**: SSA construction + passes + regalloc takes ~1ms per function
4. **Code correctness**: IR interpreter is the oracle — every optimization must match

### Non-Goals for This Phase

- Tier 3 (deferred)
- OSR (on-stack replacement) — functions upgrade on next call, not mid-execution
- Polymorphic inline caches at Tier 2 (Tier 1 already handles field access well)
- NEWTABLE optimization (stays as exit-resume in Tier 2 too)
