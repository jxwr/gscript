# utf8

The `utf8` library provides Unicode-aware string operations. GScript strings are byte strings internally; this library provides codepoint-level access using Go's `unicode/utf8` package.

## Constants

### utf8.charpattern

A string pattern that matches a single UTF-8 encoded codepoint. Provided for Lua compatibility.

## Functions

### utf8.len(s) -> int | nil, err

Returns the number of UTF-8 codepoints in the string. If the string is not valid UTF-8, returns `nil` and an error message.

```gscript
utf8.len("hello")  -- 5
utf8.len("中文")    -- 2
```

### utf8.char(...) -> string

Creates a string from one or more Unicode codepoint numbers.

```gscript
utf8.char(72, 101, 108, 108, 111)  -- "Hello"
utf8.char(20013, 25991)            -- "中文"
```

### utf8.codepoint(s, i [, j]) -> codepoints...

Returns the codepoint values for positions `i` through `j` (1-based codepoint indices). Defaults to a single codepoint at position `i`.

```gscript
utf8.codepoint("A", 1)       -- 65
a, b, c := utf8.codepoint("ABC", 1, 3)  -- 65, 66, 67
```

### utf8.codes(s) -> table

Returns a table (array) of `{pos, code}` entries for each codepoint in the string. `pos` is the 1-based byte offset, `code` is the Unicode codepoint value.

```gscript
result := utf8.codes("AB")
-- result[1] = {pos: 1, code: 65}
-- result[2] = {pos: 2, code: 66}
```

### utf8.offset(s, n [, i]) -> int

Returns the 1-based byte position of the `n`-th codepoint, optionally starting the search from byte position `i` (1-based, default 1).

```gscript
utf8.offset("中文测试", 2)  -- 4 (second codepoint starts at byte 4)
```

### utf8.valid(s) -> bool

Returns `true` if the string is valid UTF-8.

```gscript
utf8.valid("hello")  -- true
utf8.valid("中文")    -- true
```

### utf8.reverse(s) -> string

Reverses the string by codepoint (not by byte).

```gscript
utf8.reverse("abc")  -- "cba"
utf8.reverse("中文")  -- "文中"
```

### utf8.sub(s, i [, j]) -> string

Returns a substring by codepoint indices (1-based). Negative indices count from the end. Defaults `j` to the end of the string.

```gscript
utf8.sub("hello", 2, 4)     -- "ell"
utf8.sub("中文测试", 2, 3)   -- "文测"
utf8.sub("hello", 3)        -- "llo"
```

### utf8.upper(s) -> string

Returns the string with all codepoints converted to uppercase using Unicode rules.

```gscript
utf8.upper("hello")  -- "HELLO"
```

### utf8.lower(s) -> string

Returns the string with all codepoints converted to lowercase using Unicode rules.

```gscript
utf8.lower("HELLO")  -- "hello"
```

### utf8.charclass(cp) -> string

Returns a single-letter classification for the given codepoint:

- `"L"` -- Letter
- `"N"` -- Number/digit
- `"S"` -- Space/whitespace
- `"P"` -- Punctuation or symbol
- `"O"` -- Other

```gscript
utf8.charclass(65)   -- "L" (letter 'A')
utf8.charclass(48)   -- "N" (digit '0')
utf8.charclass(32)   -- "S" (space)
utf8.charclass(33)   -- "P" (exclamation mark)
```
