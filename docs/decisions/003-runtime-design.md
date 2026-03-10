# 003: Runtime Design

## Status
Accepted

## Context
GScript needs a runtime to execute parsed ASTs. Key design decisions involve value representation, scope management, function closures, and multiple return value handling.

## Decisions

### Value Type: Tagged Union
Values use a struct-based tagged union (`Value`) with a `ValueType` tag and separate fields for each payload type (`ival int64`, `fval float64`, `sval string`, `ptr any`). This avoids heap allocation for scalar types (int, float, bool) while still supporting reference types (table, function, coroutine) via the `ptr` field.

Trade-offs:
- **Pro:** No boxing for numbers/bools; values are passed by value with no GC pressure for scalars.
- **Pro:** Single `Value` type simplifies the interpreter API (no `interface{}` type switches everywhere).
- **Con:** The struct is larger than a single `interface{}` (40+ bytes), but this is offset by avoiding heap allocations.

### Multiple Return Value Handling
Functions return `[]Value`. The interpreter handles expansion rules:
- When the **last expression** in an expression list is a function call, all its return values are expanded.
- When a function call appears in a **non-tail position**, only the first return value is used.
- This mirrors Lua's semantics exactly and enables patterns like `a, b := f()` and `g(a, f())`.

### Scope Chain (Environment)
Lexical scoping is implemented via a linked list of `Environment` structs. Each environment holds a `map[string]*Upvalue` and a pointer to its parent scope.

- `Define(name, val)` creates a new binding in the **current** scope (shadowing).
- `Set(name, val)` walks the chain to find an existing binding and updates it.
- `Get(name)` walks the chain to read the nearest binding.

### Environment + Upvalue Design for Closures
Variables are stored as `*Upvalue` (pointer to a `*Value`) rather than directly as `Value`. This enables closures to capture variables by reference: both the closure and the defining scope share the same `*Upvalue`, so mutations are visible from both sides.

In Phase 3 we lay the groundwork but full upvalue capture (closing over variables from outer scopes) is deferred to Phase 4.

### Arithmetic Type Coercion
- `int OP int` stays `int` (except non-exact division, which produces `float`).
- `int OP float` or `float OP float` produces `float`.
- Strings that look like numbers can be coerced in arithmetic contexts (Lua behavior).
- String `+` is not concatenation; use `..` for concat.

### Truthiness
Following Lua: only `nil` and `false` are falsy. Zero, empty string, and empty table are all truthy.

## Consequences
- The interpreter is a straightforward tree-walk over the AST.
- Performance is adequate for scripting use cases; a bytecode compiler can be added later if needed.
- The Upvalue-based environment design supports closure capture without requiring a separate "close upvalues" pass.
