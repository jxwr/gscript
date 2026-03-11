# regexp

The `regexp` library provides regular expression matching using Go's RE2 syntax. RE2 guarantees linear-time execution with no backtracking catastrophes.

## Module Functions

### regexp.compile(pattern) -> re, err

Compiles a regular expression pattern. Returns a regexp object on success, or `nil` and an error message on failure.

```gscript
re, err := regexp.compile("[0-9]+")
if err != nil {
    print("bad pattern: " .. err)
}
```

### regexp.mustCompile(pattern) -> re

Compiles a regular expression pattern. Raises an error if the pattern is invalid.

```gscript
re := regexp.mustCompile("[0-9]+")
```

### regexp.match(pattern, str) -> bool

Returns `true` if the pattern matches anywhere in the string.

```gscript
regexp.match("^hello", "hello world")  -- true
```

### regexp.find(pattern, str) -> string | nil

Returns the first match of the pattern in the string, or `nil` if no match.

```gscript
regexp.find("[0-9]+", "abc123def")  -- "123"
```

### regexp.findAll(pattern, str [, n]) -> table

Returns a table (array) of all matches. Pass `n` to limit the number of matches (`-1` for all, which is the default).

```gscript
regexp.findAll("[0-9]+", "a1b22c333")  -- {"1", "22", "333"}
```

### regexp.replace(pattern, str, repl) -> string

Replaces the first match of the pattern with the replacement string.

```gscript
regexp.replace("[0-9]+", "a1b22", "X")  -- "aXb22"
```

### regexp.replaceAll(pattern, str, repl) -> string

Replaces all matches of the pattern with the replacement string.

```gscript
regexp.replaceAll("[0-9]+", "a1b22c333", "X")  -- "aXbXcX"
```

### regexp.split(pattern, str [, n]) -> table

Splits the string around matches of the pattern. Pass `n` to limit the number of parts (`-1` for all, which is the default).

```gscript
regexp.split(",\\s*", "a, b, c")  -- {"a", "b", "c"}
```

## Regexp Object Methods

Objects returned by `regexp.compile()` and `regexp.mustCompile()` have the following fields and methods:

### re.pattern

A string field containing the original pattern.

### re.match(str) -> bool

Tests if the pattern matches anywhere in the string.

### re.find(str) -> string | nil

Returns the first match, or `nil`.

### re.findSubmatch(str) -> table | nil

Returns a table where index 1 is the full match and indices 2, 3, ... are the captured subgroups. Returns `nil` if no match.

```gscript
re := regexp.mustCompile("(\\w+)@(\\w+)")
m := re.findSubmatch("user@host")
-- m[1] = "user@host", m[2] = "user", m[3] = "host"
```

### re.findAll(str [, n]) -> table

Returns all matches as an array of strings.

### re.findAllSubmatch(str [, n]) -> table

Returns all matches with subgroups. Each element is a table like `findSubmatch` returns.

### re.replace(str, repl) -> string

Replaces the first match.

### re.replaceAll(str, repl) -> string

Replaces all matches.

### re.split(str [, n]) -> table

Splits the string around matches.

### re.numSubexp() -> int

Returns the number of capture groups in the pattern.
