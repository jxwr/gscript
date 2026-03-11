# bit32

The `bit32` library provides 32-bit bitwise operations, compatible with Lua 5.2 semantics. All input values are truncated to `uint32`; results are returned as GScript integers.

## Functions

### bit32.band(...) -> int

Bitwise AND of all arguments. Returns `0xFFFFFFFF` (4294967295) with no arguments.

```gscript
bit32.band(255, 15)      -- 15
bit32.band(255, 15, 7)   -- 7
```

### bit32.bor(...) -> int

Bitwise OR of all arguments. Returns `0` with no arguments.

```gscript
bit32.bor(240, 15)  -- 255
```

### bit32.bxor(...) -> int

Bitwise XOR of all arguments. Returns `0` with no arguments.

```gscript
bit32.bxor(255, 15)  -- 240
```

### bit32.bnot(n) -> int

Bitwise NOT (complement) of `n`, treated as a 32-bit unsigned integer.

```gscript
bit32.bnot(0)    -- 4294967295
bit32.bnot(255)  -- 4294967040
```

### bit32.lshift(n, disp) -> int

Logical left shift. If `disp` is negative, shifts right instead. Shifts of 32 or more return 0.

```gscript
bit32.lshift(1, 4)  -- 16
```

### bit32.rshift(n, disp) -> int

Logical right shift (unsigned, fills with zeros). If `disp` is negative, shifts left instead.

```gscript
bit32.rshift(16, 4)  -- 1
```

### bit32.arshift(n, disp) -> int

Arithmetic right shift. The value is treated as a signed 32-bit integer, so the sign bit is extended.

```gscript
bit32.arshift(2147483648, 4)  -- -134217728
```

### bit32.test(n, pos) -> bool

Returns `true` if the bit at position `pos` (0-based) is set.

```gscript
bit32.test(10, 1)  -- true  (10 = 0b1010, bit 1 is set)
bit32.test(10, 2)  -- false (bit 2 is not set)
```

### bit32.set(n, pos) -> int

Sets the bit at position `pos` (0-based).

```gscript
bit32.set(0, 3)  -- 8
```

### bit32.clear(n, pos) -> int

Clears the bit at position `pos` (0-based).

```gscript
bit32.clear(255, 0)  -- 254
```

### bit32.toggle(n, pos) -> int

Toggles (flips) the bit at position `pos` (0-based).

```gscript
bit32.toggle(0, 3)  -- 8
bit32.toggle(8, 3)  -- 0
```

### bit32.extract(n, field, width) -> int

Extracts `width` bits starting at bit position `field` (0-based).

```gscript
bit32.extract(65280, 8, 4)  -- 15
```

### bit32.replace(n, v, field, width) -> int

Replaces `width` bits starting at bit position `field` with the value `v`.

```gscript
bit32.replace(65280, 10, 8, 4)  -- 64000
```

### bit32.countbits(n) -> int

Returns the number of set bits (population count / Hamming weight).

```gscript
bit32.countbits(0)           -- 0
bit32.countbits(255)         -- 8
bit32.countbits(4294967295)  -- 32
```

### bit32.highbit(n) -> int

Returns the 0-based position of the highest set bit. Returns `-1` if `n` is 0.

```gscript
bit32.highbit(0)           -- -1
bit32.highbit(1)           -- 0
bit32.highbit(2147483648)  -- 31
```

### bit32.toHex(n [, digits]) -> string

Returns the hexadecimal string representation of `n`. If `digits` is provided, the result is zero-padded to that width.

```gscript
bit32.toHex(255)     -- "0xFF"
bit32.toHex(255, 8)  -- "0x000000FF"
```
