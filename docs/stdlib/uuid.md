# uuid

The `uuid` library provides functions for generating and working with UUIDs (Universally Unique Identifiers).

UUID v4 generation uses `crypto/rand` for cryptographically secure random bytes.

## Functions

### uuid.v4() -> string

Generate a random UUID v4 string in standard format: `xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx`.

```
id := uuid.v4()
-- e.g. "550e8400-e29b-41d4-a716-446655440000"
```

### uuid.v4Raw() -> string

Generate a UUID v4 without hyphens (32 hex characters).

```
id := uuid.v4Raw()
-- e.g. "550e8400e29b41d4a716446655440000"
```

### uuid.isValid(s) -> bool

Check if a string is a valid UUID format (with hyphens, any version).

```
uuid.isValid("550e8400-e29b-41d4-a716-446655440000")   -- true
uuid.isValid("not-a-uuid")                              -- false
uuid.isValid("550E8400-E29B-41D4-A716-446655440000")   -- true (case insensitive)
```

### uuid.parse(s) -> table [, error]

Parse a UUID string and return a table with:
- `version` (int) -- UUID version number (e.g. 4)
- `variant` (string) -- UUID variant (e.g. "RFC4122")
- `bytes` (string) -- 32-character hex string without hyphens

Returns `nil, "error message"` for invalid UUIDs.

```
info := uuid.parse("550e8400-e29b-41d4-a716-446655440000")
-- info.version == 4
-- info.variant == "RFC4122"
-- info.bytes == "550e8400e29b41d4a716446655440000"
```

### uuid["nil"]() -> string

Return the nil UUID: `"00000000-0000-0000-0000-000000000000"`.

Note: Since `nil` is a keyword in GScript, use bracket notation to access this function.

```
nilUUID := uuid["nil"]()
-- "00000000-0000-0000-0000-000000000000"
```
