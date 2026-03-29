# GScript JIT Architecture

Three-tier Method JIT, modeled on V8 (Sparkplug → Maglev → TurboFan).

## Tiers

```
Tier 0: Interpreter (internal/vm/)
  Executes all bytecodes, collects type feedback (FeedbackVector)
  → Tier 1 at N calls

Tier 1: Baseline JIT (internal/methodjit/tier1_*.go)
  1:1 bytecode → ARM64 templates, no IR, no optimization
  Every value stays NaN-boxed. All ops handled (native or exit-resume).
  → Tier 2 at N calls with stable feedback

Tier 2: Optimizing JIT (internal/methodjit/)
  Bytecode → CFG SSA IR → Optimization passes → RegAlloc → ARM64
  Type-specialized registers, deopt guards, function inlining
```

## Tier 2 Pipeline

```
BuildGraph (Braun et al. 2013)
  → Validate
  → TypeSpecialize   (generic OpAdd → OpAddInt when both int)
  → Validate
  → ConstProp        (fold arithmetic on constants)
  → Validate
  → DCE              (remove unused values)
  → Validate
  → Inline           (monomorphic small callees)
  → Validate
  → RegAlloc         (forward-walk: 5 GPR, 8 FPR)
  → Emit             (ARM64 code generation)
```

Validation runs after every pass.

## Key Design Points

- **Universal compilation**: Every function compiles. Unsupported ops use op-exit resume (exit to Go, execute, resume JIT at next PC). ~55ns per exit.
- **Raw-int loop mode**: When type feedback confirms integers, loop body keeps raw int64 in registers (no NaN-box overhead). 21.4x on Sum10000.
- **Deoptimization**: Type guard failures bail to interpreter. Future: resume at exact bytecode PC (not restart).
- **NaN-boxing**: Every value is uint64. Float64 = raw IEEE 754 bits. Tagged values use quiet-NaN space (int=0xFFFE, bool=0xFFFD, ptr=0xFFFF, nil=0xFFFC).

## Register Convention (ARM64)

| Register | Role |
|----------|------|
| X19 | ExecContext pointer |
| X24 | Int tag constant (0xFFFE000000000000) |
| X25 | Bool tag constant |
| X26 | VM register base |
| X27 | Constants pointer |
| X20-X23, X28 | Allocatable GPRs (5) |
| D4-D11 | Allocatable FPRs (8) |

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Normal return |
| 2 | Deopt → interpreter |
| 3 | Call-exit (resume after Go handles call) |
| 4 | Global-exit |
| 5 | Table-exit |
| 6 | Op-exit (generic unsupported op) |
| 7 | Baseline op-exit (Tier 1) |

## Infrastructure

- **IR Interpreter** (`interp.go`): Correctness oracle. `Interpret(graph, args)` must match `VM.Execute(proto, args)`.
- **IR Validator** (`validator.go`): Structural invariants after every pass.
- **IR Printer** (`printer.go`): Human-readable dump for debugging.
- **Diagnose** (`diagnose.go`): One-call full pipeline diagnostic.
