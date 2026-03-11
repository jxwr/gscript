# url

The `url` library provides functions for parsing, building, and manipulating URLs.

## Functions

### url.parse(str) -> table [, error]

Parse a URL string into a table with the following fields:
- `scheme` -- e.g. "https"
- `host` -- hostname without port
- `port` -- port number as string
- `path` -- URL path
- `query` -- table of query parameters
- `fragment` -- fragment identifier
- `user` -- username (if present)
- `password` -- password (if present)
- `raw` -- the original URL string

```
u := url.parse("https://user:pass@example.com:8080/path?q=1#frag")
-- u.scheme == "https"
-- u.host == "example.com"
-- u.port == "8080"
-- u.path == "/path"
-- u.query.q == "1"
-- u.fragment == "frag"
-- u.user == "user"
-- u.password == "pass"
```

### url.build(t) -> string

Build a URL string from a table with the same fields as returned by `url.parse`.

```
s := url.build({
    scheme: "https",
    host: "example.com",
    port: "8080",
    path: "/api/v1",
    query: {key: "value"}
})
-- s == "https://example.com:8080/api/v1?key=value"
```

### url.encode(str) -> string

Percent-encode a string for use in URL query parameter values.

```
url.encode("hello world")   -- "hello+world"
url.encode("a=b&c=d")       -- "a%3Db%26c%3Dd"
```

### url.decode(str) -> string [, error]

Percent-decode a URL-encoded string.

```
url.decode("hello+world")   -- "hello world"
```

### url.queryEncode(t) -> string

Encode a table as a URL query string.

```
url.queryEncode({a: "1", b: "hello world"})
-- "a=1&b=hello+world"
```

### url.queryDecode(str) -> table [, error]

Decode a URL query string into a table.

```
t := url.queryDecode("a=1&b=hello+world")
-- t.a == "1", t.b == "hello world"
```

### url.join(base, ref) -> string [, error]

Resolve a reference URL relative to a base URL.

```
url.join("https://example.com/base/", "../other")
-- "https://example.com/other"
```

### url.isValid(str) -> bool

Check if a string is a valid URL (has both scheme and host).

```
url.isValid("https://example.com")   -- true
url.isValid("not a url")             -- false
```

### url.getHost(str) -> string [, error]

Extract just the hostname from a URL string.

```
url.getHost("https://example.com:8080/path")   -- "example.com"
```

### url.getPath(str) -> string [, error]

Extract just the path from a URL string.

```
url.getPath("https://example.com/foo/bar")   -- "/foo/bar"
```
