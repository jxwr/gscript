# 002: Parser Design

## Operator Precedence

The parser uses recursive descent with a separate function per precedence level. Operators are listed from lowest to highest precedence:

| Level | Operators         | Associativity | Parser Function        |
|-------|-------------------|---------------|------------------------|
| 1     | `\|\|`            | Left          | `parseOr`              |
| 2     | `&&`              | Left          | `parseAnd`             |
| 3     | `== != < <= > >=` | Left          | `parseComparison`      |
| 4     | `..`              | Right         | `parseConcat`          |
| 5     | `+ -`             | Left          | `parseAdditive`        |
| 6     | `* / %`           | Left          | `parseMultiplicative`  |
| 7     | `**`              | Right         | `parsePower`           |
| 8     | `- ! #` (unary)   | Prefix        | `parseUnary`           |
| 9     | `. [] () :`       | Left/Postfix  | `parsePostfix`         |

Right-associative operators (`..` and `**`) recurse into themselves rather than looping, producing right-leaning AST trees. For example, `a .. b .. c` becomes `a .. (b .. c)`.

## For Loop Disambiguation

GScript has three `for` variants that share the `for` keyword. The parser disambiguates using lookahead:

1. **`for { ... }`** (infinite loop / ForStmt with nil condition): detected when the token immediately after `for` is `{`.

2. **`for k, v := range expr { ... }`** (ForRangeStmt): detected by scanning ahead for the pattern `IDENT [, IDENT] := range`. The parser checks if the first token is an identifier, optionally followed by `, IDENT`, then `:=`, then `range`.

3. **`for init; cond; post { ... }`** (ForNumStmt / C-style): detected by scanning ahead at depth 0 for a semicolon before encountering an opening brace. The scan tracks parenthesis/bracket/brace depth to avoid false positives from semicolons inside sub-expressions.

4. **`for cond { ... }`** (while-style ForStmt): the fallback when none of the above patterns match. The condition expression is parsed normally.

## Table Literal Key Formats

Table literals support three field forms:

1. **Named key**: `name: value` -- the identifier is converted to a StringLit key. Detected by checking `IDENT` followed by `COLON`.

2. **Computed key**: `[expr]: value` -- the expression inside brackets becomes the key. Detected by a leading `[`.

3. **Array-style**: `value` -- no key (Key field is nil in the AST). This is the fallback when neither of the above patterns matches.

Fields are separated by commas or semicolons (both optional). Trailing separators are allowed.

## Statement vs Expression Parsing

The parser has no separate lexical concept of "statement terminators" -- whitespace including newlines is not significant in the token stream. Instead, statements are parsed greedily:

1. **Keyword-led statements** (`func`, `if`, `for`, `return`, `break`, `continue`) are identified by their leading keyword and dispatched directly.

2. **Expression-led statements** start by parsing an expression, then examining the next token to determine the statement type:
   - `:=` after one or more identifiers -> DeclareStmt
   - `=` after expressions -> AssignStmt
   - `+=`, `-=`, `*=`, `/=` -> CompoundAssignStmt
   - `++`, `--` -> IncDecStmt
   - `,` followed by more expressions then `:=` or `=` -> multi-target declare/assign
   - If the expression is a CallExpr or MethodCallExpr -> CallStmt
   - Otherwise -> parse error

3. **Semicolons** act as optional statement separators. They are consumed between statements but are never required.

4. **Function declarations vs function literals**: `func IDENT (` is parsed as FuncDeclStmt; `func (` is parsed as a FuncLitExpr within an expression.
