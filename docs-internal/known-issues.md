# Known Issues

> Last updated: 2026-03-29

## Historical (Trace JIT — deprecated, for reference only)

The following issues are from the Trace JIT era (`internal/jit/`, now disconnected). They are preserved for context but the code is scheduled for deletion.

### spectral_norm: float accumulator treated as int (Method JIT tier2)
- `findAccumulators()` detected `sum` as int accumulator, but `sum := 0.0` is float → NaN-boxed float bits corrupted
- Fix: reject candidates whose initial value is float

### nbody: guard-fail from slot reuse type mismatch (Trace JIT)
- Slot reuse across loop iterations caused type mismatch at guard checks
- Root cause: GETTABLE not recognized as a write by `isWrittenBeforeFirstReadExt`

### sort: stack overflow from recursive calls via call-exit (Trace JIT)
- Trace JIT's call-exit went through `vm.call` (pushes VM frames), deep recursion exceeded `maxCallDepth=200`

### SSA_CALL result handling (Trace JIT)
- Call-exit register reload didn't handle all register state scenarios after nested calls
- Workaround: traces with `HasCallInLoop()` disabled

### GC scanTableRoots intermittent SIGSEGV
- GC compaction encountered stale NaN-boxed pointers in edge cases
- Partially fixed via safe-point mechanism

## Current (Method JIT)

_None tracked in this file. Check GitHub Issues for current bugs._
