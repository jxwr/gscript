# ADR-001: Lexer Design

## Status
Accepted

## Context
GScript is a scripting language with Go-like syntax and Lua-like semantics. The lexer is the first phase of the language pipeline, responsible for converting source text into a stream of tokens.

## Decisions

### Token Design
- Tokens carry four fields: `Type` (enum), `Value` (string), `Line`, and `Column`.
- `Value` holds the raw text for numbers and identifiers, the decoded (unescaped) content for strings, and the literal operator text for operators.
- Token types are represented as `int` constants via `iota`. A `String()` method on `TokenType` provides human-readable names for error messages and debugging.
- Keywords (func, return, if, else, elseif, for, range, break, continue, in, var) and boolean/nil literals (true, false, nil) are identified via a lookup table after reading an identifier.

### Whitespace Handling
Newlines are **not** significant in GScript. Unlike Go (which injects semicolons at newlines), GScript treats newlines the same as spaces and tabs. This choice:
- Simplifies the lexer (no semicolon insertion rules).
- Matches Lua's approach and makes the language more forgiving about line formatting.
- Means statements are delimited by context (braces, keywords) rather than newlines.

### Number Representation
Numbers are stored as their raw string representation in the token value. Parsing into `int64` or `float64` is deferred to the parser or runtime. The lexer recognizes:
- Integers: `0`, `42`, `1234567890`
- Floats: `3.14`, `0.5` (a decimal point followed by non-dot characters)
- Scientific notation: `1e10`, `1.5e-3`, `2E+4`

A decimal point is only consumed as part of a number if the next character is not also a dot. This prevents `1..2` from being misread as a float — instead it lexes as `NUMBER("1") CONCAT("..") NUMBER("2")`.

### Operator Disambiguation
Multi-character operators are resolved by greedy matching (longest match):
- `*` vs `**` vs `*=`
- `.` vs `..` vs `...`
- `+` vs `++` vs `+=`
- `:` vs `:=`
- `!` vs `!=`
- `=` vs `==`

Single `&` and `|` are errors with a hint to use `&&` or `||`.

### Error Handling
The lexer reports errors for:
- Unterminated string literals (including strings with unescaped newlines)
- Unterminated block comments
- Unexpected/invalid characters (e.g., `@`, `~`, bare `&` or `|`)

Errors include line and column information.

### String Escapes
Recognized escapes: `\n`, `\t`, `\r`, `\\`, `\"`. Unrecognized escapes (e.g., `\z`) are passed through as-is (backslash + character), rather than causing an error. This is lenient by design, allowing future escape sequences to be added without breaking existing code.

## Consequences
- The lexer is simple and stateless — no semicolon injection, no indentation tracking.
- Position tracking is accurate for error reporting in later phases.
- The `Tokenize()` method produces a complete token slice (including trailing EOF), while `NextToken()` supports incremental/streaming use.
