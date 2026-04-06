## Injected Tasks (prepend to plan as Task 0)

- [ ] 0. Tier 1 self-call optimization for recursive functions (fib/ackermann/mutual_recursion)

  **Background**: In the old trace JIT, fib(20) ran at 24μs (beat LuaJIT's 26μs) using three techniques.
  These were deleted during the Method JIT pivot. Current Tier 1 BLR call is ~10ns/call, fib(35) = 1.365s (55x behind LuaJIT).
  The techniques are calling-convention optimizations, not trace-specific — they work in Tier 1.

  **Three sub-tasks (implement in order):**

  0a. **Self-call detection + direct branch**: In `tier1_call.go`, when CALL target is the current function
      (proto == callee proto), skip the 6-instruction type-check + DirectEntryPtr load sequence.
      Emit a direct `B` (branch) to function entry instead of `BLR`. Save ~6 insns per self-call.
      Reference: blog post #7 "The Day We Beat LuaJIT", Part 2 (Argument Source Tracing).
      Test: `TestSelfCallDirect` — fib(20) should use direct branch, verify correct result.

  0b. **Argument register direct-pass**: When calling self, arguments are already in known VM register slots.
      Instead of storing to Value array then loading in callee entry, pass via physical ARM64 registers directly.
      Pin argument R(0) to a callee-saved register (X19 or similar) so it survives across the self-call
      without spill/reload.
      Reference: blog post #7, "Accumulator Pinning" + "R(0) Pin to X19".
      Test: `TestSelfCallRegisterPass` — verify no memory round-trip for self-call args.

  0c. **Return value register direct-write**: When self-call returns, write result directly to the
      destination register (not through Value array). Complements 0b.
      Test: verify fib(20) result correctness + measure wall-time improvement.

  **Expected effect**: Tier 1 self-call ~10ns → ~3ns. fib(35) from 1.365s to ~0.4s (55x → ~16x vs LuaJIT).
  Also helps ackermann, mutual_recursion.

  **Files**: `tier1_call.go`, `tier1_compile.go` (calling convention), tests
  **Budget**: 3 commits
  **Failure signal**: if fib(35) doesn't improve ≥30%, the overhead is elsewhere (not call overhead)
