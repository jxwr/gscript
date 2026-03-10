# 004: Metatable Design

## Status
Accepted

## Context
GScript needs a metatable system to support operator overloading, OOP-style inheritance, and custom behavior for tables. This design follows Lua's metatable semantics closely, providing a familiar and powerful mechanism for extending table behavior.

## Decisions

### Metatable Storage
Each `Table` already has a `metatable *Table` field. Metatables are themselves ordinary tables whose string keys (e.g., `"__index"`, `"__add"`) map to metamethod functions (or tables, in the case of `__index` and `__newindex`).

### Metamethod Lookup
A helper `getMetamethod(val Value, event string) (Value, bool)` checks whether the value is a table with a metatable that contains the given event key. Lookup uses `RawGet` on the metatable to avoid infinite recursion.

### Supported Metamethods (14 total)

| Metamethod   | Trigger                        | Signature               |
|-------------|--------------------------------|-------------------------|
| `__index`    | Key not found in table read   | `fn(table, key)` or table |
| `__newindex` | Setting a new (absent) key    | `fn(table, key, val)` or table |
| `__add`      | `a + b`                       | `fn(a, b) -> result`   |
| `__sub`      | `a - b`                       | `fn(a, b) -> result`   |
| `__mul`      | `a * b`                       | `fn(a, b) -> result`   |
| `__div`      | `a / b`                       | `fn(a, b) -> result`   |
| `__mod`      | `a % b`                       | `fn(a, b) -> result`   |
| `__pow`      | `a ** b`                      | `fn(a, b) -> result`   |
| `__unm`      | `-a`                          | `fn(a) -> result`      |
| `__eq`       | `a == b` (both tables)        | `fn(a, b) -> bool`     |
| `__lt`       | `a < b`                       | `fn(a, b) -> bool`     |
| `__le`       | `a <= b`                      | `fn(a, b) -> bool`     |
| `__concat`   | `a .. b`                      | `fn(a, b) -> result`   |
| `__len`      | `#a`                          | `fn(a) -> number`      |
| `__call`     | `a(args...)`                  | `fn(self, args...)`    |

### Binary Arithmetic Metamethods (__add, __sub, __mul, __div, __mod, __pow)
When normal arithmetic fails (operands are not coercible to numbers), the interpreter:
1. Tries the left operand's metamethod
2. If not found, tries the right operand's metamethod
3. If found, calls `mm(left, right)` and returns the first result
4. If neither has the metamethod, raises an error

### __index (Table Read)
When `t[k]` yields nil from `RawGet`:
- If `__index` is a **table**: recursively look up `k` in that table (supporting prototype chains)
- If `__index` is a **function**: call `__index(t, k)` and return the first result
- A depth limit of 50 prevents infinite recursion in circular `__index` chains

### __newindex (Table Write)
When setting `t[k] = v` and `k` does NOT already exist in the raw table:
- If `__newindex` is a **function**: call `__newindex(t, k, v)` (the raw table is not modified)
- If `__newindex` is a **table**: recursively set in that table
- If the key already exists (raw), always do a direct `RawSet` (no metamethod)

### __eq (Equality)
For primitive types (nil, bool, number, string), raw equality is always used. For tables:
- Same pointer identity returns `true`
- Otherwise, tries `__eq` from the left operand's metatable, then the right's
- The metamethod result is converted to boolean via `Truthy()`

### __lt and __le (Ordering)
For non-number, non-string comparisons, the interpreter tries `__lt`/`__le` metamethods from the left operand first, then the right. The `>` and `>=` operators are implemented by swapping operands and using `__lt`/`__le`.

### __call (Callable Tables)
When `callFunction` receives a non-function value that is a table, it checks for `__call`. The table itself is prepended as the first argument (matching Lua convention), then the metamethod is invoked recursively through `callFunction`.

### __unm (Unary Minus)
When `-t` fails because `t` is not a number, tries `__unm(t)`.

### __len (Length)
For tables, `__len` is checked before falling back to the default `Table.Length()`. This allows tables to define custom length semantics.

### __concat (Concatenation)
When `..` fails because operands are not strings/numbers, tries `__concat` from the left operand, then the right.

### Built-in Functions
Five new global functions support raw (metamethod-bypassing) operations:

- `setmetatable(t, mt)` -- sets the metatable, returns `t`
- `getmetatable(t)` -- returns the metatable or nil
- `rawget(t, k)` -- reads `t[k]` bypassing `__index`
- `rawset(t, k, v)` -- writes `t[k] = v` bypassing `__newindex`, returns `t`
- `rawequal(a, b)` -- compares by raw equality, bypassing `__eq`

### Infinite Recursion Protection
Both `tableGet` and `tableSet` track recursion depth and bail out at 50 levels with a clear error message, preventing stack overflow from circular `__index` or `__newindex` chains.

## Consequences
- Tables can now serve as objects with operator overloading and prototype-based inheritance
- The `__index` chain enables Lua-style OOP: `setmetatable(self, {__index: Class})`
- Method calls via `:` syntax correctly resolve methods through `__index` chains
- `rawget`/`rawset`/`rawequal` provide escape hatches for code that needs to bypass metamethods (e.g., implementing metamethods themselves)
- All existing tests continue to pass since metamethods only activate when metatables are explicitly set
