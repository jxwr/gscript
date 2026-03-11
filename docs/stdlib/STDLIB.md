# GScript Standard Library Reference

GScript ships with a comprehensive standard library covering application development, game development, and system scripting.

## Quick Overview

| Library | Global | Description |
|---------|--------|-------------|
| [string](#string) | `string` | String manipulation, Lua patterns |
| [table](#table) | `table` | Table/array operations |
| [math](#math) | `math` | Math functions and constants |
| [io](#io) | `io` | File I/O |
| [os](#os) | `os` | OS interface (time, env, exit) |
| [json](#json) | `json` | JSON encode/decode |
| [base64](#base64) | `base64` | Base64 encoding |
| [hash](#hash) | `hash` | Cryptographic hashes (MD5, SHA256, HMAC) |
| [fs](#fs) | `fs` | File system operations |
| [path](#path) | `path` | File path manipulation |
| [time](#time) | `time` | Time, sleep, formatting |
| [net](#net) | `net` | HTTP client |
| [vec](#vec) | `vec` | 2D/3D vector math |
| [color](#color) | `color` | RGBA/HSV color utilities |
| [regexp](#regexp) | `regexp` | Regular expressions (RE2) |
| [utf8](#utf8) | `utf8` | Unicode/UTF-8 string operations |
| [bit32](#bit32) | `bit32` | 32-bit bitwise operations |
| [http](#http) | `http` | HTTP server |
| [gl](#gl) | `gl` | OpenGL 2D drawing |
| [coroutine](#coroutine) | `coroutine` | Coroutine control (built-in) |

---

## Embedding API Library Presets

When embedding GScript in Go, use `LibFlags` to control which libraries are available:

```go
gscript.New(gscript.WithLibs(gscript.LibApp))   // app dev preset
gscript.New(gscript.WithLibs(gscript.LibGame))  // game dev preset
gscript.New(gscript.WithLibs(gscript.LibSafe))  // sandboxed (no I/O)
gscript.New(gscript.WithLibs(gscript.LibAll))   // everything (default)
```

| Preset | Included Libraries |
|--------|-------------------|
| `LibAll` | Everything |
| `LibApp` | string, table, math, io, os, json, base64, hash, fs, path, time, net, regexp, utf8, bit32 |
| `LibGame` | string, table, math, gl, vec, color, json, bit32, time |
| `LibSafe` | string, table, math, json, base64, hash, vec, color, regexp, utf8, bit32 |

---

## string

String manipulation with Lua-compatible pattern matching.

```go
s := "Hello, World!"
string.upper(s)                     // "HELLO, WORLD!"
string.lower(s)                     // "hello, world!"
string.sub(s, 1, 5)                 // "Hello"
string.len(s)                       // 13
string.rep("ab", 3, "-")            // "ab-ab-ab"
string.reverse("hello")             // "olleh"
string.byte("A")                    // 65
string.char(65, 66, 67)             // "ABC"
string.find("hello world", "w%a+")  // 7  11
string.match("2024-03-11", "(%d+)-(%d+)-(%d+)")  // "2024" "03" "11"
string.gsub("aaa", "a", "b", 2)    // "bba"  2
string.format("%.2f", 3.14159)      // "3.14"
for w := range string.gmatch("one two three", "%a+") { print(w) }
```

See individual function reference at: [GScript README Language Features](../../README.md)

---

## table

Array and hash table operations.

```go
t := {10, 20, 30}
table.insert(t, 40)              // {10,20,30,40}
table.insert(t, 2, 15)           // {10,15,20,30,40}
table.remove(t, 1)               // removes first element
table.concat(t, ", ")            // "15, 20, 30, 40"
table.sort(t)                    // sort ascending
table.sort(t, func(a,b){ return a > b })  // sort descending
a, b, c := table.unpack({1,2,3}) // 1  2  3
table.move(src, f, e, t, dst)    // move elements
p := table.pack(1, 2, 3)         // {1, 2, 3, n=3}
```

---

## math

```go
math.pi          // 3.14159...
math.huge        // +Inf
math.abs(-5)     // 5
math.floor(3.7)  // 3
math.ceil(3.2)   // 4
math.sqrt(16)    // 4.0
math.pow(2, 10)  // 1024
math.sin / math.cos / math.tan / math.atan
math.max(1,5,3)  // 5
math.min(1,5,3)  // 1
math.log(x [, base])
math.random([m [, n]])
math.randomseed(n)
math.type(x)     // "integer" | "float" | false
```

---

## io

```go
io.write("hello ")
io.write("world\n")
line := io.read()        // read line from stdin
all  := io.read("*a")   // read all
num  := io.read("*n")   // read number

f := io.open("file.txt", "r")  // "r" "w" "a" "rb" "wb"
content := f.read("*a")
for line := range f.lines() { print(line) }
f.close()
```

---

## os

```go
os.time()            // unix timestamp (seconds)
os.clock()           // cpu time (seconds)
os.date("%Y-%m-%d")  // formatted date
os.getenv("HOME")    // environment variable
os.exit(0)           // exit process
```

---

## json

Full JSON encode/decode. See [json.md](json.md).

```go
s := json.encode({name: "Alice", age: 30, tags: {"go", "lua"}})
// '{"age":30,"name":"Alice","tags":["go","lua"]}'

t := json.decode('{"x": 1, "y": [2, 3]}')
print(t.x)      // 1
print(t.y[1])   // 2

pretty := json.pretty({a: 1, b: {c: 2}})
// {
//   "a": 1,
//   "b": {
//     "c": 2
//   }
// }
```

| Function | Description |
|----------|-------------|
| `json.encode(v)` | Encode GScript value to JSON string |
| `json.decode(s)` | Parse JSON string to GScript value |
| `json.pretty(v [, indent])` | Pretty-print JSON (default 2-space indent) |

---

## base64

See [base64.md](base64.md).

```go
encoded := base64.encode("Hello, World!")   // "SGVsbG8sIFdvcmxkIQ=="
decoded := base64.decode(encoded)           // "Hello, World!"

url := base64.urlEncode("hello+world/test") // URL-safe, no padding
orig := base64.urlDecode(url)
```

| Function | Description |
|----------|-------------|
| `base64.encode(s)` | Standard base64 encode |
| `base64.decode(s)` | Standard base64 decode, returns nil, err on failure |
| `base64.urlEncode(s)` | URL-safe base64 (no padding) |
| `base64.urlDecode(s)` | URL-safe decode |

---

## hash

Cryptographic hash functions. See [hash.md](hash.md).

```go
hash.md5("hello")       // "5d41402abc4b2a76b9719d911017c592"
hash.sha1("hello")      // "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
hash.sha256("hello")    // "2cf24dba5fb0a30e26e83b2ac5b9e29e..."
hash.sha512("hello")    // long hex string
hash.crc32("hello")     // integer checksum
hash.hmacSHA256("secret-key", "message")  // hex string
```

| Function | Description |
|----------|-------------|
| `hash.md5(s)` | MD5 → 32-char hex string |
| `hash.sha1(s)` | SHA-1 → 40-char hex string |
| `hash.sha256(s)` | SHA-256 → 64-char hex string |
| `hash.sha512(s)` | SHA-512 → 128-char hex string |
| `hash.crc32(s)` | CRC-32 → integer |
| `hash.hmacSHA256(key, msg)` | HMAC-SHA256 → hex string |

---

## fs

File system operations. See [fs.md](fs.md).

```go
// Check existence
fs.exists("/tmp/foo")      // bool
fs.isfile("/tmp/foo.txt")  // bool
fs.isdir("/tmp")           // bool

// Stat
info := fs.stat("/tmp/foo.txt")
// {name="foo.txt", size=1234, mtime=1710000000.0, isfile=true, isdir=false}

// Read/write
content, err := fs.readfile("/tmp/foo.txt")
ok, err := fs.writefile("/tmp/out.txt", "hello\n")
ok, err := fs.appendfile("/tmp/log.txt", "line\n")

// Directory operations
ok, err := fs.mkdir("/tmp/mydir")
ok, err := fs.mkdirAll("/tmp/a/b/c")
entries, err := fs.readdir("/tmp")
// entries: {{name="foo", isdir=false, size=100}, ...}

// File manipulation
ok, err := fs.rename("/tmp/old.txt", "/tmp/new.txt")
ok, err := fs.copy("/tmp/src.txt", "/tmp/dst.txt")
ok, err := fs.remove("/tmp/file.txt")
ok, err := fs.removeAll("/tmp/mydir")

// Glob patterns
files, err := fs.glob("/tmp/*.txt")

// Misc
dir := fs.tempdir()                   // "/tmp"
path, err := fs.tempfile("/tmp", "gs")  // temp file path
cwd, err := fs.cwd()
ok, err := fs.chdir("/new/dir")
```

---

## path

File path manipulation. See [path.md](path.md).

```go
path.join("/usr", "local", "bin")  // "/usr/local/bin"
path.dir("/usr/local/bin/go")      // "/usr/local/bin"
path.base("/usr/local/bin/go")     // "go"
path.ext("file.tar.gz")            // ".gz"
path.abs("../foo")                 // absolute path or nil, err
path.isAbs("/foo")                 // true
path.clean("/../a//b/..")          // "/a"
dir, file := path.split("/usr/bin/go")   // "/usr/bin/"  "go"
ok, err := path.match("*.txt", "hello.txt")  // true
rel, err := path.rel("/usr", "/usr/local/bin")  // "local/bin"

path.separator       // "/" (or "\\" on Windows)
path.listSeparator   // ":" (or ";" on Windows)
```

---

## time

Time, sleep, and date arithmetic. See [time.md](time.md).

```go
// Current time
t := time.now()
// t.year, t.month, t.day, t.hour, t.min, t.sec, t.unix, t.weekday

// Sleep
time.sleep(0.5)          // sleep 500ms
time.sleep(time.SECOND)  // sleep 1 second

// Elapsed time
start := time.now()
// ... do work ...
elapsed := time.since(start)  // seconds as float

// Format / parse
s := time.format(t, "%Y-%m-%d %H:%M:%S")  // "2024-03-11 14:30:00"
t2 := time.parse("2024-03-11", "%Y-%m-%d")

// Arithmetic
tomorrow := time.add(t, time.DAY)
diff := time.diff(t1, t2)  // seconds, t2 - t1

// From unix timestamp
t3 := time.unix(1710000000)

// Named weekday/month
print(time.weekday(t))  // "Monday"
print(time.month(t))    // "March"
```

Constants: `time.SECOND` (1), `time.MINUTE` (60), `time.HOUR` (3600), `time.DAY` (86400)

---

## net

HTTP client. See [net.md](net.md).

```go
// Simple GET
resp, err := net.get("https://api.example.com/data")
if err != nil { error(err) }
print(resp.status)   // 200
print(resp.ok)       // true (status < 400)
print(resp.body)     // response body string

// Parse JSON response
data := resp.json()
print(data.name)

// POST with JSON
body := json.encode({user: "alice", pass: "secret"})
resp, err := net.post("https://api.example.com/login", body, {
    headers: {["Content-Type"]: "application/json"},
    timeout: 10,
})

// Full request control
resp, err := net.request({
    method: "PUT",
    url: "https://api.example.com/item/1",
    headers: {["Authorization"]: "Bearer " .. token},
    body: json.encode({title: "updated"}),
    timeout: 30,
})
```

| Function | Returns |
|----------|---------|
| `net.get(url [, opts])` | resp, err |
| `net.post(url, body [, opts])` | resp, err |
| `net.put(url, body [, opts])` | resp, err |
| `net.delete(url [, opts])` | resp, err |
| `net.patch(url, body [, opts])` | resp, err |
| `net.request(opts)` | resp, err |

Response fields: `status`, `statusText`, `body`, `headers`, `ok`, `json()` method.

Options: `headers` (table), `timeout` (seconds), `followRedirects` (bool).

---

## vec

2D and 3D vector math for games and simulations. See [vec.md](vec.md).

```go
// Vec2
v1 := vec.vec2(3, 4)
v2 := vec.vec2(1, 0)

sum := v1 + v2            // vec2(4, 4) — operator overloaded
diff := v1 - v2           // vec2(2, 4)
scaled := v1 * 2          // vec2(6, 8)

vec.length2(v1)           // 5.0
vec.normalize2(v1)        // vec2(0.6, 0.8)
vec.dot2(v1, v2)          // 3.0
vec.dist2(v1, v2)         // distance
vec.lerp2(v1, v2, 0.5)    // midpoint
vec.rotate2(v, math.pi/4) // rotate 45°
vec.perp2(v)              // perpendicular
vec.angle2(v)             // atan2(y, x) in radians
vec.reflect2(v, normal)   // reflection vector

// Convenience constructors
vec.zero2()   // vec2(0, 0)
vec.one2()    // vec2(1, 1)
vec.up()      // vec2(0, 1)
vec.right()   // vec2(1, 0)

// Vec3
v3 := vec.vec3(1, 2, 3)
vec.cross3(v3, vec.vec3(0,0,1))  // cross product
vec.normalize3(v3)
vec.length3(v3)
vec.dot3(v3, v3)
vec.lerp3(v3a, v3b, t)
```

---

## color

RGBA color utilities with HSV/HSL conversion. See [color.md](color.md).

```go
// Constructors
red  := color.new(1, 0, 0, 1)     // r,g,b,a in [0,1]
blue := color.rgb(0, 0, 255)      // r,g,b in [0,255]
c    := color.fromHex("#FF8800")   // parse hex
c2   := color.fromHSV(30, 1, 1)   // hue 30°, full sat/val → orange

// Conversion
color.toHex(red)         // "#FF0000"
h, s, v := color.toHSV(c)
h, s, l := color.toHSL(c)

// Manipulation
mid  := color.lerp(red, blue, 0.5)  // interpolate
dim  := color.darken(red, 0.3)      // 30% darker
lite := color.lighten(red, 0.2)
gray := color.grayscale(c)
inv  := color.invert(c)
c3   := color.alpha(c, 0.5)         // 50% transparent

// Operators
c4 := c1 + c2   // additive blend (clamped to 1)
c5 := c * 0.5   // scale brightness

// Named constants
color.RED        // (1, 0, 0, 1)
color.GREEN      // (0, 1, 0, 1)
color.BLUE       // (0, 0, 1, 1)
color.WHITE      // (1, 1, 1, 1)
color.BLACK      // (0, 0, 0, 1)
color.YELLOW     // (1, 1, 0, 1)
color.CYAN       // (0, 1, 1, 1)
color.MAGENTA    // (1, 0, 1, 1)
color.ORANGE     // (1, 0.5, 0, 1)
color.TRANSPARENT // (0, 0, 0, 0)
```

---

## regexp

Regular expressions using Go's RE2 engine (safe, no backtracking catastrophe). See [regexp.md](regexp.md).

```go
// Module-level shortcuts
regexp.match(`\d+`, "abc123")         // true
regexp.find(`\d+`, "abc123")          // "123"
regexp.findAll(`\d+`, "1 and 2 and 3")  // {"1","2","3"}
regexp.replace(`\s+`, "a  b  c", " ")   // "a b c"
regexp.replaceAll(`[aeiou]`, "hello", "*")  // "h*ll*"
regexp.split(`\s+`, "one two three")    // {"one","two","three"}

// Compiled regexp object
re, err := regexp.compile(`(\d{4})-(\d{2})-(\d{2})`)
if err != nil { error(err) }

re.match("today is 2024-03-11")     // true
re.find("today is 2024-03-11")      // "2024-03-11"
caps := re.findSubmatch("2024-03-11")
// caps[1]="2024-03-11", caps[2]="2024", caps[3]="03", caps[4]="11"

all := re.findAll("2024-01-01 and 2024-12-31")
// {"2024-01-01", "2024-12-31"}

re.replaceAll("2024-03-11", "$2/$3/$1")  // "03/11/2024"
re.split("a1b2c3")                        // {"a","b","c",""}
re.numSubexp()                            // 3
re.pattern                                // `(\d{4})-(\d{2})-(\d{2})`
```

---

## utf8

Unicode-aware string operations. See [utf8.md](utf8.md).

```go
utf8.len("hello")        // 5
utf8.len("中文")          // 2 (codepoints, not bytes)
utf8.valid("hello")      // true

utf8.char(0x4E2D, 0x6587)  // "中文" — from codepoints
utf8.codepoint("A")         // 65

-- Reverse by codepoint (not byte)
utf8.reverse("中文")       // "文中"
utf8.sub("中文英", 2, 3)   // "文英" (codepoint indices)

-- Case
utf8.upper("héllo")   // "HÉLLO"
utf8.lower("HÉLLO")   // "héllo"

-- Iterate codepoints
codes := utf8.codes("abc")
// {{pos=1, code=97}, {pos=2, code=98}, {pos=3, code=99}}

utf8.offset("中文英", 2)   // byte offset of 2nd codepoint
utf8.charclass(65)          // "L" (letter)
utf8.charpattern            // Lua-compatible pattern
```

---

## bit32

32-bit unsigned integer bitwise operations. See [bit32.md](bit32.md).

```go
bit32.band(0xFF, 0x0F)      // 0x0F (15)
bit32.bor(0xF0, 0x0F)       // 0xFF (255)
bit32.bxor(0xFF, 0x0F)      // 0xF0 (240)
bit32.bnot(0)               // 0xFFFFFFFF

bit32.lshift(1, 8)          // 256  (1 << 8)
bit32.rshift(256, 8)        // 1    (unsigned right shift)
bit32.arshift(-1, 1)        // -1   (arithmetic, sign-extending)

bit32.test(0b1010, 1)       // true  (bit 1 is set)
bit32.set(0b1010, 0)        // 0b1011
bit32.clear(0b1010, 1)      // 0b1000
bit32.toggle(0b1010, 0)     // 0b1011

bit32.extract(0xFF00, 8, 4) // 0xF  (bits 8..11)
bit32.replace(0xFF00, 0xA, 8, 4)  // 0xFA00

bit32.countbits(0xFF)       // 8
bit32.highbit(0b1010)       // 3 (highest set bit position)
bit32.toHex(255)            // "0x000000FF"
```

---

## http (server)

HTTP server using Go's `net/http`. See the main README for full documentation.

```go
router := http.newRouter()

router.get("/", func(req, res) {
    res.write("Hello!")
})
router.post("/data", func(req, res) {
    body := req.json()
    res.json({received: body})
})

router.listen(":8080")
```

---

## gl (OpenGL)

2D rendering and input via OpenGL 4.1 + GLFW. See the main README.

```go
win := gl.newWindow(800, 600, "Game")
for !win.shouldClose() {
    win.pollEvents()
    win.clear(0, 0, 0)
    gl.drawRect(100, 100, 200, 150, 1, 0, 0, 1)
    gl.drawText("Score: 0", 10, 10, 1, 1, 1, 1)
    if gl.isKeyDown(gl.KEY_LEFT) { x = x - 5 }
    win.swapBuffers()
}
```

---

## coroutine

Built-in coroutine control (no import needed).

```go
co := coroutine.create(func() {
    coroutine.yield(1)
    coroutine.yield(2)
})
ok, v := coroutine.resume(co)  // true  1
ok, v  = coroutine.resume(co)  // true  2

gen := coroutine.wrap(func() {
    for i := 1; i <= 5; i++ { coroutine.yield(i) }
})
for { v := gen(); if v == nil { break }; print(v) }
```

| Function | Description |
|----------|-------------|
| `coroutine.create(f)` | Create coroutine |
| `coroutine.resume(co, ...)` | Resume, returns ok, values... |
| `coroutine.yield(...)` | Suspend, returns values from next resume |
| `coroutine.wrap(f)` | Returns a resuming function |
| `coroutine.status(co)` | "suspended" / "running" / "dead" |
| `coroutine.isyieldable()` | Whether inside a coroutine |
