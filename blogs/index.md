# Beyond LuaJIT

A technical blog documenting the journey of building a high-performance JIT compiler for GScript — a scripting language with Go syntax and Lua semantics.

## Posts

### [01. From Interpreter to Tracing JIT: What We Learned the Hard Way](01-from-interpreter-to-tracing-jit.md)

Our first attempt at reaching LuaJIT-level performance: 18 optimizations across 4 execution tiers, from bytecode interpreter tweaks to a full tracing JIT with ARM64 codegen. We achieved 2.4x on a chess AI benchmark. Here's what worked, what didn't, and why SSA IR is the missing piece.

*March 2026*

---

*GScript: [github.com/jxwr/gscript](https://github.com/jxwr/gscript)*
