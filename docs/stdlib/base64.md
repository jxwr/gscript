# base64

The `base64` library provides Base64 encoding and decoding.

## Functions

### base64.encode(str) -> string

Encodes a string using standard Base64 encoding (with `+`, `/`, and `=` padding).

```
base64.encode("hello world")    -- "aGVsbG8gd29ybGQ="
base64.encode("")               -- ""
```

### base64.decode(str) -> string [, error]

Decodes a standard Base64 encoded string. On failure, returns `nil, "error message"`.

```
base64.decode("aGVsbG8gd29ybGQ=")    -- "hello world"

val, err := base64.decode("!!!")      -- nil, "illegal base64 data..."
```

### base64.urlEncode(str) -> string

Encodes a string using URL-safe Base64 encoding (uses `-` and `_` instead of `+` and `/`, no padding).

```
base64.urlEncode("hello world")    -- "aGVsbG8gd29ybGQ"
```

### base64.urlDecode(str) -> string [, error]

Decodes a URL-safe Base64 encoded string (no padding expected). On failure, returns `nil, "error message"`.

```
encoded := base64.urlEncode("hello world")
base64.urlDecode(encoded)    -- "hello world"

val, err := base64.urlDecode("!!!")    -- nil, "illegal base64 data..."
```
