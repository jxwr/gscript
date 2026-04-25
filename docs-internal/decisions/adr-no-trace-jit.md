# ADR — No Trace JIT (Unless V8-Aligned)

**Status:** Accepted, 2026-04-25.
**Supersedes:** all forward-looking "Trace JIT is the next step" notes
across prior ADRs and session summaries (R20, R25, R29, R30, R31,
R102, R153, R155, R167).

## Decision

GScript will not adopt a trace JIT architecture.

The single exception: a trace-shaped feature MAY be considered if and
only if it ALSO fits inside the V8 4-tier method-JIT architecture
(Ignition / Sparkplug / Maglev / TurboFan). Examples that would
qualify: an "on-stack hot loop specializer" emitted by Maglev/TurboFan
that records loop-trip-shape, or a Sparkplug-level inline-loop-trace
that lowers to method JIT. Examples that would NOT qualify: a
LuaJIT-style standalone trace recorder/compiler, a TraceMonkey-style
pluggable trace tier replacing Tier 2, or a Mozilla-IonMonkey-style
trace-feedback front end fused into the existing JIT.

## Why

1. **Industry signal.** V8, JavaScriptCore, SpiderMonkey, Hermes, and
   Chakra all chose method-JIT over trace JIT. The only major shipped
   trace JIT (LuaJIT) is single-author, single-target, 15+ years old,
   and represents a substrate that nobody else ships at scale.

2. **WebKit FTL precedent.** WebKit spent 2014-2016 building FTL on
   top of LLVM (a method-JIT-style backend), then forked B3 to replace
   LLVM. They never returned to trace JIT. JSC's reasoning carries
   over: speculation + deopt + tier hand-off are awkward to graft onto
   a recording substrate.

3. **Architectural cost.** A real trace JIT is a 1-2 engineer-year
   substrate: trace recorder, trace cache, side-exit machinery, a
   second register allocator tuned for linear traces, a second
   constant-folding pipeline, hot-trace replacement under deopt,
   frame-flush logic at trace boundaries. None of this composes with
   our existing Tier 2 SSA pipeline; it would run alongside, doubling
   the maintenance surface.

4. **Our actual gaps don't need trace JIT.** R161 (escape analysis,
   100× win on object_creation), R166 (V8-style cumulative-bytecode
   inline budget), and the R107/R110/R111 family of static-self
   optimizations have all moved benchmarks substantially within the
   method-JIT substrate. The remaining ack/fib/mut_recursion gaps
   are characterized as "the BR-and-rebox cost per recursive call is
   irreducible without changing call shape" — which is a method-JIT
   problem with a method-JIT answer (cross-call EA, bigger inline
   budgets, IP-TCO across protos), not a recording-substrate problem.

5. **Hard rule #11 (V8 is the default).** GScript already commits to
   V8's architectural choices unless specific evidence overrides. The
   default for "would trace JIT help here" is therefore "no — V8
   chose otherwise."

## Consequences

- **No trace recorder, trace cache, side-exit branch table, or trace
  IR is to be added to the codebase.** The deprecated `internal/jit/`
  directory (legacy trace experiment) stays deprecated; do not
  resurrect.
- **ADRs and round cards stop citing "Trace JIT" as a future
  direction.** Older docs may still mention it historically; new
  docs do not.
- **Benchmark gaps that LuaJIT closes via trace JIT (ack 65×, fib 32×,
  mut_recursion 45×) are accepted as method-JIT-bounded** until
  cross-call EA, V8-style 4-tier feedback (Maglev-equivalent), or
  whole-program speculation closes them inside the method-JIT
  substrate. We will not pursue a trace recorder to close them.
- **A V8-aligned trace-shaped feature** (e.g., a Maglev-internal
  loop-trace specializer that emits a single straight-line method-
  JIT body for hot loops) is in scope; the gate is that it integrates
  as a pass inside the existing Tier 2 pipeline, not as a parallel
  substrate.

## Non-decisions

- **Reviving `internal/jit/`.** Out of scope.
- **LLVM as a backend.** Separately rejected on its own merits
  (compile latency + speculation friction; see WebKit FTL precedent
  again). Tracked here only as cross-reference: rejecting trace JIT
  does not imply LLVM is the alternative.
- **Whether method JIT can fully close the LuaJIT gap on
  call-recursion benchmarks.** Open. The ADR commits to attempting
  closure inside the method-JIT substrate; it does not promise the
  closure is achievable. If it isn't, the honest record is "GScript
  is method-JIT-bounded on these shapes" — not "we should adopt
  trace JIT."

## Sources

- WebKit FTL → B3 transition (2016).
- V8 Turbolev / Maglev architecture posts (2023-2024).
- R156 V8 research round (`adr-v8-alignment.md`).
- R170 Self-TCO halt — confirms M4-superscalar absorbs asm-insn
  reductions; the residual gap is structural, not codegen-density.
