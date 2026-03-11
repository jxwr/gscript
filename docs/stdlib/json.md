# json

The `json` library provides JSON encoding and decoding for GScript values.

## Functions

### json.encode(value) -> string

Converts a GScript value to a JSON string.

Type mappings:
- `nil` -> `null`
- `true` / `false` -> `true` / `false`
- integer -> number (no decimal point)
- float -> number (with decimal point)
- string -> quoted JSON string
- table (array-only integer keys 1..n) -> JSON array
- table (string keys) -> JSON object
- table (mixed int + string keys) -> JSON object (int keys become string keys)

```
json.encode(nil)                  -- "null"
json.encode(42)                   -- "42"
json.encode("hello")              -- "\"hello\""
json.encode({1, 2, 3})            -- "[1,2,3]"
json.encode({name: "test"})       -- "{\"name\":\"test\"}"
```

### json.decode(str) -> value [, error]

Parses a JSON string and returns the corresponding GScript value.

Type mappings:
- `null` -> `nil`
- `true` / `false` -> boolean
- integer number -> int
- decimal number -> float
- string -> string
- array -> table with 1-based integer keys
- object -> table with string keys

On parse failure, returns `nil, "error message"`.

```
json.decode("null")               -- nil
json.decode("42")                 -- 42 (int)
json.decode("3.14")               -- 3.14 (float)
json.decode("[1,2,3]")            -- {1, 2, 3}
json.decode("{\"a\":1}")          -- {a: 1}

val, err := json.decode("bad")   -- nil, "invalid character..."
```

### json.pretty(value [, indent]) -> string

Converts a GScript value to a pretty-printed JSON string with indentation.

- `indent` defaults to `"  "` (2 spaces) if not provided.

```
json.pretty({name: "test", age: 30})
-- {
--   "age": 30,
--   "name": "test"
-- }

json.pretty({a: 1}, "    ")
-- {
--     "a": 1
-- }
```
