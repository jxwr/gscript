# Stable Global Load Optimization

## Problem

`GetGlobal` today emits a generation-check fast-path: load cell, compare
cached generation tag, branch to slow path on mismatch. For globals that
are never written after program start (functions, constants, imported
libs), this check is pure overhead on every load.

## V8 Approach (PropertyCell + CompilationDependency)

V8 tags every global slot with a `PropertyCellType`:
- `kConstant` — value has never changed since first store
- `kConstantType` — value's type has never changed
- `kMutable` — written multiple times

**Code-gen uses cell type at compile time**
`src/compiler/js-native-context-specialization.cc:1087-1091`: when the
cell is `kConstant` or `kUndefined`, TurboFan constant-folds the load
entirely — **no check emitted, just a constant in the IR**. For
`kConstantType` (line 1101-1123) it emits a typed `LoadField` with
no cell check; it depends on a stable map instead.

**Invalidation via CompilationDependency, not watchpoint**
`src/compiler/compilation-dependencies.cc:871-914`: a
`GlobalPropertyDependency` is recorded. Before installing the optimized
code, V8 calls `IsValid()` which checks the cell's current type and
whether its value has been replaced with the `property_cell_hole`
sentinel. On subsequent writes that violate the assumption,
`PropertyCell::InvalidateAndReplaceEntry`
(`src/objects/property-cell.h:66`) swaps the cell out and marks the old
one invalid, which deopts all dependent code.

So: **compile-time assume-constant + deopt-on-violation**. No runtime
generation check in the hot path.

## JSC Approach

JSC uses `Watchpoint` objects on `JSGlobalObject`'s `SymbolTableEntry`.
A `VariableWatchpointSet` starts `IsWatched`; the first store transitions
it to `IsInvalidated` and fires a watchpoint that jettisons dependent
code. Equivalent to V8's dependency, slightly different machinery:
watchpoint = subscription list, dependency = validation record.

## Applicability to GScript

GScript does **not** have arbitrary runtime globals the way JS does —
our SETGLOBAL sites are statically visible in the bytecode of all
loaded scripts. This means we can do the V8 trick *without* the deopt
machinery:

1. **Compile-time scan**: walk all loaded functions, collect the set
   `MUTATED = {names written by any SETGLOBAL}`.
2. **Cheap specialization**: for any `GetGlobal name` where
   `name ∉ MUTATED`, emit a direct cell-pointer load with **no
   generation check**. Optionally constant-fold if the cell holds a
   function/immutable primitive at compile time.
3. **Soundness**: if new code is loaded later (REPL, eval, dynamic
   require) that introduces a SETGLOBAL for a previously-stable name,
   invalidate affected JIT code — same mechanism we already use for
   shape-dependent code.

## Risk

- Must hook script loader to rerun the scan and invalidate stale
  machine code; if we don't support dynamic loading yet, this is free.
- Constant-folding the *value* (not just skipping the check) requires
  proving the cell is written exactly once before first read — harder;
  skip for v1.
- Name-set must include indirect writes (`_G[name] = ...` style) if
  GScript supports them. Audit the bytecode for any such ops.

## Recommendation

**Do the compile-time name-set analysis. Skip the watchpoint/deopt
mechanism for v1.** GScript's static-program assumption makes V8's
full PropertyCell + dependency infrastructure overkill. A single pass
over loaded bytecode before codegen gives the whole benefit (killed
generation check) with ~50 LOC. Add invalidation-on-dynamic-load only
when we actually support dynamic load.

## Citations

- `/tmp/research-cache/v8/src/compiler/js-native-context-specialization.cc:1031-1130`
- `/tmp/research-cache/v8/src/compiler/compilation-dependencies.cc:871-914,1292-1296`
- `/tmp/research-cache/v8/src/objects/property-cell.h:22-81`
