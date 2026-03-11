# hash

The `hash` library provides cryptographic hash functions and checksums.

## Functions

### hash.md5(str) -> string

Returns the MD5 hash of the input string as a lowercase hex string (32 characters).

```
hash.md5("hello")    -- "5d41402abc4b2a76b9719d911017c592"
hash.md5("")         -- "d41d8cd98f00b204e9800998ecf8427e"
```

### hash.sha1(str) -> string

Returns the SHA-1 hash of the input string as a lowercase hex string (40 characters).

```
hash.sha1("hello")    -- "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
```

### hash.sha256(str) -> string

Returns the SHA-256 hash of the input string as a lowercase hex string (64 characters).

```
hash.sha256("hello")    -- "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
```

### hash.sha512(str) -> string

Returns the SHA-512 hash of the input string as a lowercase hex string (128 characters).

```
hash.sha512("hello")    -- "9b71d224bd62f3785d96d46ad3ea3d73..."
```

### hash.crc32(str) -> integer

Returns the CRC-32 checksum (IEEE polynomial) of the input string as an integer.

```
hash.crc32("hello")    -- 907060870
hash.crc32("")         -- 0
```

### hash.hmacSHA256(key, message) -> string

Returns the HMAC-SHA256 of the message using the given key, as a lowercase hex string (64 characters).

```
hash.hmacSHA256("key", "hello")    -- "9307b3b915efb5171ff14d8cb55fbcc798c6c0ef1456d66ded1a6aa723a58b7b"
```
