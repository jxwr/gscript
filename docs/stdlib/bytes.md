# bytes

The `bytes` library provides a mutable byte buffer and functions for low-level byte manipulation.

## Buffer Creation

### bytes.new() -> buffer

Create a new empty byte buffer.

```
buf := bytes.new()
```

### bytes.fromString(s) -> buffer

Create a buffer initialized with the contents of a string.

```
buf := bytes.fromString("hello")
```

### bytes.fromHex(hexStr) -> buffer [, error]

Create a buffer from a hex-encoded string.

```
buf := bytes.fromHex("68656c6c6f")
buf.toString()   -- "hello"
```

## Buffer Methods

All buffer methods are called on the buffer table returned by the creation functions.

### buf.write(s)

Append a string to the buffer.

```
buf := bytes.new()
buf.write("hello")
buf.write(" world")
buf.toString()   -- "hello world"
```

### buf.writeByte(n)

Append a single byte (integer 0-255) to the buffer.

```
buf := bytes.new()
buf.writeByte(65)   -- 'A'
buf.writeByte(66)   -- 'B'
buf.toString()      -- "AB"
```

### buf.writeInt8/16/32/64(n)

Append an integer in little-endian byte order.

```
buf := bytes.new()
buf.writeInt16(256)
-- Writes bytes: 0x00, 0x01 (little-endian)
```

### buf.writeFloat32/64(n)

Append a floating-point number in little-endian IEEE 754 format.

### buf.toString() -> string

Get the buffer contents as a string.

### buf.toHex() -> string

Get the buffer contents as a hex-encoded string.

```
buf := bytes.fromString("AB")
buf.toHex()   -- "4142"
```

### buf.len() -> int

Get the buffer length in bytes.

### buf.reset()

Clear the buffer contents.

### buf.bytes() -> table

Return a table of byte values (integers) with 1-based keys.

```
buf := bytes.fromString("AB")
b := buf.bytes()
-- b[1] == 65, b[2] == 66
```

### buf.readString(from, to) -> string

Read a substring from byte positions (1-based, inclusive).

```
buf := bytes.fromString("hello world")
buf.readString(1, 5)   -- "hello"
```

### buf.readByte(pos) -> int | nil

Read a single byte at position (1-based). Returns nil if out of range.

```
buf := bytes.fromString("ABC")
buf.readByte(1)   -- 65
buf.readByte(4)   -- nil
```

## Standalone Functions

### bytes.toHex(s) -> string

Convert a string to its hex representation.

```
bytes.toHex("hello")   -- "68656c6c6f"
```

### bytes.xor(s1, s2) -> string

XOR two byte strings of equal length. Returns a new string.

```
result := bytes.xor("ab", "cd")
```

### bytes.compare(s1, s2) -> int

Lexicographic comparison of two byte strings. Returns -1, 0, or 1.

```
bytes.compare("abc", "abd")   -- -1
bytes.compare("abc", "abc")   -- 0
bytes.compare("abd", "abc")   -- 1
```

### bytes.repeat(s, n) -> string

Repeat a byte string n times.

```
bytes.repeat("ab", 3)   -- "ababab"
```

### bytes.concat(...) -> string

Concatenate multiple strings or buffer values.

```
bytes.concat("hello", " ", "world")   -- "hello world"
```
