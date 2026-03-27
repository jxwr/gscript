# JIT Debugging Guide

Debugging a tracing JIT is fundamentally different from debugging an interpreter. The bug is never "this line is wrong" — it's "somewhere in a 7-stage pipeline, one stage produced wrong output that only manifests as a crash or incorrect result after native code execution."

This guide documents the diagnostic tools built into GScript's JIT compiler.

## Philosophy

Inspired by LLVM's `-print-after-all` and LuaJIT's `-jdump`:

1. **Observe, don't guess.** Dump register state before/after execution. Five minutes of observation beats five hours of code reading.
2. **Binary search the pipeline.** If the output is wrong, dump SSA at each pass and find which pass introduced the error.
3. **Tests are diagnostic units.** A failing test should produce enough output to diagnose the bug without reading other files.

## Pipeline Overview

```
Source → Bytecode → TraceIR → BuildSSA → OptimizeSSA → ConstHoist → CSE → FMA → RegAlloc → Emit → ARM64
                                  ↑           ↑            ↑          ↑      ↑       ↑         ↑
                              dump point   dump point   dump point  ...    ...     ...     disasm
```

Every stage takes `*SSAFunc` and returns `*SSAFunc`. You can dump the IR at any point.

## Tool Reference

All tools live in `internal/jit/`. Build tag: `darwin && arm64`.

### 1. `DiagnoseTrace` — One-call full diagnostic

**The primary tool.** Given a trace + registers, it compiles, executes, and returns a structured report.

```go
diag := DiagnoseTrace(trace, regs, proto, DiagConfig{
    WatchSlots: []int{0, 1, 2, 3, 4},  // which registers to show
    ShowASM:    true,                    // include ARM64 disassembly
    MaxIter:    10,                      // cap iterations (0 = unlimited)
})
t.Log(diag)        // full report
t.Log(diag.Summary()) // one-line: "exit=loop-done pc=3 iter=50"
```

**Output includes:**
- Pipeline stage status (ok/error for each pass)
- Final SSA IR after all optimizations
- Register allocation map
- Registers BEFORE execution (with raw hex: `[0]=0xfffe000000000001 int(1)`)
- Registers AFTER execution
- Exit info: code (loop-done/side-exit/guard-fail/call-exit), PC, iteration count
- ARM64 disassembly (optional)

**Key fields for programmatic checks:**
```go
diag.GuardFail  // pre-loop type guard failed
diag.SideExit   // loop exited via side-exit
diag.LoopDone   // loop completed normally
diag.ExitCode   // raw exit code (0-5)
diag.ExitPC     // bytecode PC where trace exited
diag.Iterations // number of loop iterations executed
```

**File:** `trace_diag.go`

### 2. `CompileWithDump` — LLVM-style per-pass dump

Runs the full compilation pipeline and records SSA state at every stage.

```go
ct, dump := CompileWithDump(trace)
t.Log(dump.String())                          // all stages
t.Log(dump.Stage("RegAlloc").SSA)             // SSA after a specific stage
t.Log(dump.Diff("BuildSSA", "ConstHoist"))    // what changed
```

**Stages recorded:**
1. `BuildSSA` — trace IR → SSA
2. `OptimizeSSA` — while-loop exit detection
3. `ConstHoist` — hoist loop-invariant constants
4. `CSE` — common subexpression elimination
5. `FuseMultiplyAdd` — MUL+ADD → FMADD fusion
6. `RegAlloc` — linear scan register allocation
7. `Emit` — ARM64 code generation

**File:** `pipeline_dump.go`

### 3. `DiagnoseCompiled` — Diagnose pre-compiled traces

When you build/compile manually and want to diagnose execution only:

```go
ct := buildAndCompileSSA(t, trace)
diag := DiagnoseCompiled(ct, regs, DiagConfig{
    WatchSlots: []int{0, 3, 4},
})
t.Log(diag)
```

**File:** `trace_diag.go`

### 4. Low-level dump functions

For when you need individual pieces:

| Function | Returns | Use case |
|----------|---------|----------|
| `SSAToString(f)` | string | Compact SSA IR dump |
| `RegMapToString(rm)` | string | Register allocation: `s0→X20 s4→X22` |
| `RegsToString(regs, slots)` | string | Register values with raw hex |
| `DumpSSA(f)` | (prints) | SSA IR to stdout |
| `DumpRegAlloc(rm)` | (prints) | RegAlloc to stdout |
| `DumpRegisters(regs, slots)` | (prints) | Register values to stdout |
| `DumpARM64(ct)` | string | Disassemble generated ARM64 |
| `DumpAsm(ct)` | (prints) | ARM64 via llvm-objdump |

**Files:** `pipeline_dump.go`, `debug_tools.go`, `disasm.go`

### 5. Runtime debug flags

```go
SetDebugTrace(true)   // verbose trace recording/execution logs to stderr
SetDebugExecTrace(true) // per-callJIT register dumps
```

## Debugging Recipes

### Recipe 1: "Test fails with wrong result"

```go
func TestMyBug(t *testing.T) {
    trace := buildTrace(...)
    regs := setupRegs(...)

    diag := DiagnoseTrace(trace, regs, proto, DiagConfig{
        WatchSlots: []int{0, 1, 2, 3},
    })
    t.Log(diag)

    // Check: is the pipeline producing correct SSA?
    // Check: are registers correct after execution?
    // Check: did the loop exit normally or guard-fail?
}
```

**What to look for:**
- `Exit: guard-fail` → pre-loop type check failed, check register types vs SSA GUARD_TYPE expectations
- `Exit: side-exit` with 0 iterations → first loop-body instruction caused an exit
- Registers unchanged after execution → trace did nothing (guard fail or immediate exit)

### Recipe 2: "Which optimization pass broke it?"

```go
ct, dump := CompileWithDump(trace)
// Find where SSA changes unexpectedly
for i := 1; i < len(dump.Stages); i++ {
    prev := dump.Stages[i-1]
    curr := dump.Stages[i]
    if prev.SSA != curr.SSA {
        t.Logf("Changed in %s:\n%s", curr.Name,
            dump.Diff(prev.Name, curr.Name))
    }
}
```

### Recipe 3: "Guard fail but types look correct"

Check the raw NaN-boxing bits:

```
Registers BEFORE:
  [0]=0xfffe000000000001 int(1)     ← tag 0xFFFE = int ✓
  [5]=0xfffc000000000000 nil        ← tag 0xFFFC = nil
```

Then check what the SSA guard expects:
```
GUARD_TYPE type=int slot=0   ← expects int, gets int ✓
GUARD_TYPE type=nil slot=5   ← expects nil, should be skipped
```

If a nil guard is NOT being skipped, check DeoptMetadata — the emitter may have a fallback bug (see commit `5ea612c`).

### Recipe 4: "Trace compiles but produces wrong values"

Compare interpreter vs JIT:
```go
// Run interpreter only
g1 := runtime.NewInterpreterGlobals()
vm.New(g1).Execute(proto)

// Run with JIT
g2 := runWithSSAJIT(t, src)

// Compare
if g1["result"].Int() != g2["result"].Int() {
    t.Errorf("mismatch: interp=%d jit=%d", g1["result"].Int(), g2["result"].Int())
}
```

Then use `DiagnoseTrace` with `MaxIter: 1` to step through one iteration at a time.

## Exit Codes

| Code | Name | Meaning |
|------|------|---------|
| 0 | `loop-done` | FORLOOP/while condition became false, normal completion |
| 1 | `side-exit` | Hit an instruction that can't run in JIT (CALL, unsupported op) |
| 2 | `guard-fail` | Pre-loop type guard failed (register type != expected type) |
| 3 | `call-exit` | Legacy, no longer emitted |
| 4 | `break-exit` | Break statement inside loop |
| 5 | `max-iterations` | Hit MaxIterations limit (testing only) |

## NaN-Boxing Tags (for reading hex dumps)

| Tag (upper 16 bits) | Type |
|---------------------|------|
| `0xFFFE` | Int |
| `0xFFFD` | Bool |
| `0xFFFF` | Pointer (Table/String/Function) |
| `0xFFFC` | Nil (`0xFFFC000000000000`) |
| anything else | Float (raw IEEE 754 double) |

## SSA Instruction Quick Reference

**Pre-loop (before `LOOP` marker):**
- `LOAD_SLOT` — load NaN-boxed value from register array
- `UNBOX_INT` / `UNBOX_FLOAT` — extract raw int/float from NaN-boxed value
- `GUARD_TYPE` — verify register type matches expectation, guard-fail if not

**Loop body (after `LOOP` marker):**
- `ADD_INT`, `SUB_INT`, `MUL_INT`, `DIV_INT` — integer arithmetic
- `ADD_FLOAT`, `SUB_FLOAT`, `MUL_FLOAT`, `DIV_FLOAT` — float arithmetic
- `FMADD`, `FMSUB` — fused multiply-add/subtract
- `LE_INT`, `LT_INT`, `LE_FLOAT`, `LT_FLOAT` — loop exit comparisons
- `MOVE` — copy value between slots
- `CALL` — emitted as side-exit (interpreter handles the call)
- `LOAD_FIELD`, `STORE_FIELD` — table field access
- `LOAD_ARRAY`, `STORE_ARRAY` — table array access
