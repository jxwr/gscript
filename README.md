# GScript

> **AI Experiment**: This project was built entirely by an AI agent (Claude) to test the limits of AI capability in designing and implementing a complete programming language interpreter from scratch — including lexer, parser, AST, runtime, type system, closures, metatables, coroutines, standard library, embedding API, and OpenGL-based game engine. See [About this Project](#about-this-project) for details.

---

GScript is a scripting language with **Go-like syntax and Lua semantics**, implemented in Go with dual execution backends: a tree-walking interpreter and a register-based bytecode VM.

- Syntax close to Go (`:=` declarations, `func`, `for`, `if`)
- Full Lua semantics (table, metatable, closure, coroutine, multiple return values)
- **Bytecode VM** (register-based, Lua 5.x style) for 3-5x faster execution
- Embeddable in Go programs via a clean reflection-based API
- Built-in HTTP server and Raylib 2D drawing support

```
              ┌──────────────┐     ┌─────────────────────┐
 .gs source → │ Lexer/Parser │ → AST ─┬→ Tree-walker     │ → result
              └──────────────┘     │   └→ Compiler → VM   │ → result
                                   └─────────────────────┘
```

---

## Installation

```bash
git clone https://github.com/jxwr/gscript
cd gscript
go build -o gscript ./cmd/gscript/
```

---

## Quick Start

```bash
# Run a file
./gscript examples/fib.gs

# Run with bytecode VM (3-5x faster)
./gscript --vm examples/fib.gs

# Evaluate a string
./gscript -e 'print("Hello, GScript!")'

# Interactive REPL
./gscript
```

---

## Language Features

### 1. Types

| Type | Example |
|------|---------|
| nil | `nil` |
| bool | `true`, `false` |
| int | `42`, `-100` |
| float | `3.14`, `1e10` |
| string | `"hello"` |
| table | `{1, 2, 3}` |
| function | `func(x) { return x }` |
| coroutine | `coroutine.create(f)` |

```go
a := 42
b := 3.14
s := "hello"
ok := true
nothing := nil
```

Truthiness follows Lua rules: **only `nil` and `false` are falsy**; `0` and `""` are truthy.

---

### 2. Operators

```go
// Arithmetic
1 + 2       // 3
10 - 3      // 7
4 * 5       // 20
10 / 4      // 2.5
10 % 3      // 1
2 ** 8      // 256  (power)

// String concatenation and length
"hello" .. " world"   // "hello world"
#"hello"              // 5

// Comparison
1 == 1    // true
1 != 2    // true
1 < 2     // true
1 <= 1    // true

// Logic (short-circuit, return operand not bool)
true && false   // false
true || false   // true
!true           // false
nil || "default"  // "default"  (idiomatic default value)

// Compound assignment
x += 1
x -= 1
x *= 2
x /= 2
x++
x--
```

---

### 3. Variables

```go
// Declaration (:=)
x := 10
a, b := 1, 2

// Assignment (=)
x = 20
a, b = b, a   // swap

// Multiple assignment from function
q, r := divmod(17, 5)
```

---

### 4. Control Flow

#### if / elseif / else

```go
score := 85

if score >= 90 {
    print("A")
} elseif score >= 80 {
    print("B")
} elseif score >= 70 {
    print("C")
} else {
    print("F")
}
```

#### for (while-style)

```go
n := 1
for n < 100 {
    n = n * 2
}
print(n)  // 128
```

#### for (C-style)

```go
sum := 0
for i := 1; i <= 100; i++ {
    sum = sum + i
}
print(sum)  // 5050
```

#### for range (iterate table)

```go
fruits := {"apple", "banana", "cherry"}
for i, v := range fruits {
    print(i, v)
}
// 1  apple
// 2  banana
// 3  cherry
```

#### break / continue

```go
for i := 1; i <= 10; i++ {
    if i == 5 { break }
    if i % 2 == 0 { continue }
    print(i)   // 1 3
}
```

---

### 5. Functions

```go
// Basic function
func add(a, b) {
    return a + b
}
print(add(3, 4))  // 7

// Multiple return values
func divmod(a, b) {
    return math.floor(a / b), a % b
}
q, r := divmod(17, 5)
print(q, r)  // 3  2

// Recursion
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
print(fib(10))  // 55

// Functions are first-class values
apply := func(f, x) { return f(x) }
double := func(x) { return x * 2 }
print(apply(double, 21))  // 42

// Function as table field
ops := {
    add: func(a, b) { return a + b },
    mul: func(a, b) { return a * b },
}
print(ops.add(3, 4))  // 7
```

#### Variadic functions

```go
func sum(...args) {
    total := 0
    for _, v := range args {
        total = total + v
    }
    return total
}
print(sum(1, 2, 3, 4, 5))  // 15
```

---

### 6. Closures / Upvalues

GScript supports full lexical closures. Multiple closures from the same scope share the same upvalue reference — mutations are visible to all.

```go
func makeCounter(start) {
    n := start
    return {
        inc: func() { n = n + 1; return n },
        dec: func() { n = n - 1; return n },
        get: func() { return n },
    }
}

c := makeCounter(0)
print(c.inc())  // 1
print(c.inc())  // 2
print(c.dec())  // 1
print(c.get())  // 1
```

```go
// Two closures sharing the same variable
x := 0
inc := func() { x = x + 1 }
get := func() { return x }

inc()
inc()
print(get())  // 2
```

```go
// Memoization
func memoize(f) {
    cache := {}
    return func(n) {
        if cache[n] != nil { return cache[n] }
        result := f(n)
        cache[n] = result
        return result
    }
}

fib := memoize(func(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
})
print(fib(30))  // 832040 (fast)
```

---

### 7. Table

Table is GScript's core data structure, serving as both array and hash map.

```go
// Array-style (1-based index)
arr := {10, 20, 30, 40, 50}
print(arr[1])   // 10
print(#arr)     // 5

// Hash-style
person := {name: "alice", age: 30}
print(person.name)       // alice
print(person["age"])     // 30

// Mixed
data := {
    title: "GScript",
    tags: {"fast", "dynamic", "fun"},
    version: 1,
}

// Dynamic modification
arr[6] = 60
person.email = "alice@example.com"

// Nested
matrix := {{1,2,3},{4,5,6},{7,8,9}}
print(matrix[2][2])  // 5

// Iteration
t := {a: 1, b: 2, c: 3}
for k, v := range t {
    print(k, v)
}
```

---

### 8. Metatable

All 14 Lua metamethods are supported, enabling operator overloading, OOP inheritance, and reactive patterns.

```go
// Operator overloading
Vec2 := {}
Vec2.__index = Vec2
Vec2.new = func(x, y) {
    v := {x: x, y: y}
    setmetatable(v, Vec2)
    return v
}
Vec2.__add = func(a, b) { return Vec2.new(a.x+b.x, a.y+b.y) }
Vec2.__sub = func(a, b) { return Vec2.new(a.x-b.x, a.y-b.y) }
Vec2.__mul = func(a, b) { return a.x*b.x + a.y*b.y }  // dot product
Vec2.__eq  = func(a, b) { return a.x==b.x && a.y==b.y }

v1 := Vec2.new(1, 2)
v2 := Vec2.new(3, 4)
v3 := v1 + v2
print(v3.x, v3.y)  // 4  6
```

```go
// __index for OOP inheritance
func Class(parent) {
    cls := {}
    cls.__index = cls
    if parent != nil {
        setmetatable(cls, {__index: parent})
    }
    cls.new = func(...) {
        inst := {}
        setmetatable(inst, cls)
        if cls.init != nil { cls.init(inst, ...) }
        return inst
    }
    return cls
}

Animal := Class(nil)
Animal.init  = func(self, name) { self.name = name }
Animal.speak = func(self) { return self.name .. " makes a sound" }

Dog := Class(Animal)
Dog.speak = func(self) { return self.name .. " says woof!" }

rex := Dog.new("Rex")
print(rex.speak(rex))  // Rex says woof!
```

```go
// Read-only table via __newindex
func readonly(t)  {
    proxy := {}
    setmetatable(proxy, {
        __index: t,
        __newindex: func(_, k, v) {
            error("attempt to modify read-only table")
        },
    })
    return proxy
}

config := readonly({host: "localhost", port: 8080})
print(config.host)  // localhost
config.host = "x"   // error: attempt to modify read-only table
```

**Supported metamethods:**

| Metamethod | Triggered by |
|------------|-------------|
| `__index` | Reading a missing field (table or function) |
| `__newindex` | Writing a missing field (table or function) |
| `__add` | `a + b` |
| `__sub` | `a - b` |
| `__mul` | `a * b` |
| `__div` | `a / b` |
| `__mod` | `a % b` |
| `__pow` | `a ** b` |
| `__unm` | `-a` |
| `__concat` | `a .. b` |
| `__len` | `#a` |
| `__eq` | `a == b` |
| `__lt` | `a < b` |
| `__le` | `a <= b` |
| `__call` | `a(args)` |

---

### 9. Coroutines

Implemented with goroutines + channels. API is fully compatible with Lua.

```go
// Basic yield/resume
co := coroutine.create(func() {
    coroutine.yield(1)
    coroutine.yield(2)
    coroutine.yield(3)
})

ok, v := coroutine.resume(co)  // true  1
ok, v  = coroutine.resume(co)  // true  2
ok, v  = coroutine.resume(co)  // true  3
```

```go
// Generator pattern
gen := coroutine.wrap(func() {
    for i := 1; i <= 5; i++ {
        coroutine.yield(i * i)
    }
})

for {
    v := gen()
    if v == nil { break }
    print(v)   // 1  4  9  16  25
}
```

```go
// Bidirectional value passing
co := coroutine.create(func(init) {
    x := init
    for {
        x = coroutine.yield(x * 2)
    }
})

_, v := coroutine.resume(co, 1)   // start with 1 → v=2
_, v  = coroutine.resume(co, 3)   // send 3 → v=6
_, v  = coroutine.resume(co, 5)   // send 5 → v=10
```

```go
// Producer-consumer pipeline
func producer() {
    return coroutine.create(func() {
        data := {1, 4, 9, 16, 25}
        for _, v := range data {
            coroutine.yield(v)
        }
    })
}

func consumer(prod) {
    total := 0
    for {
        ok, v := coroutine.resume(prod)
        if !ok || v == nil { break }
        total = total + v
    }
    return total
}

print(consumer(producer()))  // 55
```

**API:**

| Function | Description |
|----------|-------------|
| `coroutine.create(f)` | Create a coroutine |
| `coroutine.resume(co, ...)` | Resume execution, returns `ok, values...` |
| `coroutine.yield(...)` | Suspend, returns values passed to next resume |
| `coroutine.wrap(f)` | Returns a function that resumes on each call |
| `coroutine.status(co)` | `"suspended"` / `"running"` / `"dead"` |
| `coroutine.isyieldable()` | Whether currently inside a coroutine |

---

### 10. Error Handling

```go
// pcall catches errors
ok, err := pcall(func() {
    error("something went wrong")
})
print(ok)   // false
print(err)  // something went wrong

// Errors can be any value
ok, e := pcall(func() {
    error({code: 404, msg: "not found"})
})
print(e.code)  // 404
print(e.msg)   // not found

// xpcall with a message handler
ok, msg := xpcall(
    func() { error("oops") },
    func(err) { return "caught: " .. err }
)
print(msg)  // caught: oops

// assert
assert(1 + 1 == 2, "math is broken")

ok2, e2 := pcall(func() {
    assert(false, "failed!")
})
print(e2)  // failed!
```

```go
// Result-type pattern (no exceptions in normal flow)
func safeDivide(a, b) {
    if b == 0 { return nil, "division by zero" }
    return a / b, nil
}

result, err := safeDivide(10, 0)
if err != nil {
    print("Error:", err)
} else {
    print("Result:", result)
}
```

---

## Standard Library

GScript ships with 30+ standard libraries. Full reference: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md)

| Library | Global | Description |
|---------|--------|-------------|
| string | `string` | String manipulation, Lua patterns, trim/split/join/pad |
| table | `table` | Table/array ops, filter/map/reduce, unique/flatten/zip |
| math | `math` | Math functions, clamp/lerp/sign/round/hypot |
| io | `io` | File I/O |
| os | `os` | OS interface (time, env, exit, hostname, args) |
| **json** | `json` | JSON encode/decode/pretty |
| **base64** | `base64` | Base64 URL-safe encoding |
| **hash** | `hash` | MD5, SHA256, CRC32, HMAC |
| **fs** | `fs` | File system (mkdir, readdir, stat, copy, glob…) |
| **path** | `path` | Path manipulation (join, dir, base, ext, abs…) |
| **time** | `time` | Time, sleep, format, parse, add, diff |
| **net** | `net` | HTTP client (get, post, request, headers, timeout) |
| **vec** | `vec` | 2D/3D vectors with operator overloading |
| **color** | `color` | RGBA/HSV colors and named constants |
| **regexp** | `regexp` | Regular expressions (RE2, compile/match/replace) |
| **utf8** | `utf8` | Unicode-aware string operations |
| **bit32** | `bit32` | 32-bit bitwise operations |
| **process** | `process` | Run commands, shell, env, pid, which |
| **csv** | `csv` | CSV parse/encode with headers support |
| **url** | `url` | URL parse/build/encode, query strings |
| **uuid** | `uuid` | UUID v4 generation and validation |
| **bytes** | `bytes` | Binary buffer, hex encode/decode, XOR |
| **rl** | `rl` | Raylib: window, drawing, input, audio, textures |
| http | `http` | HTTP server (router, handlers) |
| coroutine | `coroutine` | Coroutine control (built-in) |

### string

```go
string.upper("hello")                    // "HELLO"
string.sub("hello", 2, 4)               // "ell"
string.find("hello world", "w%a+")      // 7  11
string.match("2024-03-11", "(%d+)-(%d+)-(%d+)")  // "2024" "03" "11"
string.gsub("aaa", "a", "b", 2)         // "bba"  2
string.format("%.2f", 3.14159)          // "3.14"
for w := range string.gmatch("one two", "%a+") { print(w) }
```

### json

```go
s := json.encode({name: "Alice", scores: {10, 20, 30}})
// '{"name":"Alice","scores":[10,20,30]}'

t := json.decode('{"x": 1, "y": [2, 3]}')
print(t.x, t.y[1])   // 1  2

print(json.pretty({a: 1, b: {c: 2}}))
// {
//   "a": 1,
//   "b": { "c": 2 }
// }
```

### base64

```go
enc := base64.encode("Hello, World!")   // "SGVsbG8sIFdvcmxkIQ=="
dec := base64.decode(enc)               // "Hello, World!"
url := base64.urlEncode("data+/test")   // URL-safe, no padding
```

### hash

```go
hash.md5("hello")     // "5d41402abc4b2a76b9719d911017c592"
hash.sha256("hello")  // "2cf24dba5fb0a30e26e83b2ac5b9e29e..."
hash.crc32("hello")   // integer checksum
hash.hmacSHA256("key", "message")  // hex string
```

### fs

```go
ok := fs.exists("/tmp/foo.txt")
info := fs.stat("/tmp/foo.txt")   // {name, size, mtime, isdir, isfile}
content, err := fs.readfile("/tmp/foo.txt")
ok, err := fs.writefile("/tmp/out.txt", "hello\n")
entries, err := fs.readdir("/tmp")  // {{name, isdir, size}, ...}
ok, err := fs.mkdir("/tmp/newdir")
ok, err := fs.copy("/tmp/a.txt", "/tmp/b.txt")
files, err := fs.glob("/tmp/*.txt")
```

### path

```go
path.join("/usr", "local", "bin")  // "/usr/local/bin"
path.dir("/usr/local/bin/go")      // "/usr/local/bin"
path.base("/usr/local/bin/go")     // "go"
path.ext("file.tar.gz")            // ".gz"
dir, file := path.split("/usr/bin/go")  // "/usr/bin/"  "go"
abs, err := path.abs("../foo")
```

### time

```go
t := time.now()               // {year, month, day, hour, min, sec, unix, weekday}
time.sleep(0.5)               // sleep 500ms
elapsed := time.since(t)      // seconds as float
s := time.format(t, "%Y-%m-%d %H:%M:%S")
t2 := time.parse("2024-03-11", "%Y-%m-%d")
tomorrow := time.add(t, time.DAY)
diff := time.diff(t1, t2)    // seconds
```

### net

```go
resp, err := net.get("https://api.example.com/data")
print(resp.status, resp.ok, resp.body)
data := resp.json()

resp, err := net.post(url, json.encode(body), {
    headers: {["Content-Type"]: "application/json"},
    timeout: 10,
})

resp, err := net.request({method: "PUT", url: url, body: payload})
```

### vec

```go
v1 := vec.vec2(3, 4)
v2 := vec.vec2(1, 0)
v3 := v1 + v2              // vec2(4, 4)  — operator overloaded
v4 := v1 * 2               // vec2(6, 8)
vec.length2(v1)            // 5.0
vec.normalize2(v1)         // vec2(0.6, 0.8)
vec.dot2(v1, v2)           // 3.0
vec.lerp2(v1, v2, 0.5)    // midpoint
vec.rotate2(v, math.pi/2)  // 90° rotation

v3d := vec.vec3(1, 2, 3)
vec.cross3(v3d, vec.vec3(0, 1, 0))  // cross product
```

### color

```go
red  := color.new(1, 0, 0, 1)         // r,g,b,a in [0,1]
blue := color.rgb(0, 0, 255)          // r,g,b in [0,255]
c    := color.fromHex("#FF8800")       // from hex
c2   := color.fromHSV(30, 1, 1)       // hue 30° → orange
mid  := color.lerp(red, blue, 0.5)    // blend
gray := color.grayscale(c)
color.RED / color.GREEN / color.BLUE / color.WHITE / color.BLACK
color.YELLOW / color.CYAN / color.MAGENTA / color.ORANGE
```

### regexp

```go
regexp.match(`\d+`, "abc123")         // true
regexp.find(`\d+`, "abc123")          // "123"
regexp.replaceAll(`\s+`, "a  b", " ") // "a b"
regexp.split(`\s+`, "one two three")  // {"one","two","three"}

re, err := regexp.compile(`(\d{4})-(\d{2})-(\d{2})`)
caps := re.findSubmatch("2024-03-11")
// caps[1]="2024-03-11", caps[2]="2024", caps[3]="03", caps[4]="11"
re.replaceAll("2024-03-11", "$2/$3/$1")  // "03/11/2024"
```

### utf8

```go
utf8.len("中文")           // 2  (codepoints, not bytes)
utf8.reverse("abc")        // "cba"
utf8.sub("中文英", 2, 3)   // "文英"  (codepoint indices)
utf8.upper("héllo")        // "HÉLLO"
utf8.valid("hello")        // true
utf8.char(0x4E2D, 0x6587)  // "中文"
```

### bit32

```go
bit32.band(0xFF, 0x0F)   // 0x0F  (AND)
bit32.bor(0xF0, 0x0F)    // 0xFF  (OR)
bit32.lshift(1, 8)        // 256   (<<)
bit32.rshift(256, 8)      // 1     (>>)
bit32.test(0b1010, 1)     // true  (bit 1 set?)
bit32.set(0b1010, 0)      // 0b1011
bit32.extract(0xFF00, 8, 4)  // 0xF
bit32.toHex(255)          // "0x000000FF"
```

### process

```go
result := process.run("ls -la")   // {ok, stdout, stderr, code}
result := process.shell("echo a && echo b")
out := process.exec("echo", "hello")     // stdout as string
pid := process.pid()
env := process.env()             // table of all env vars
path := process.which("go")      // full path or nil
```

### csv

```go
rows := csv.parse("a,b,c\n1,2,3\n4,5,6")
// rows[1] = {"a","b","c"}, rows[2] = {"1","2","3"}

records := csv.parseWithHeaders("name,age\nAlice,30")
// records[1] = {name: "Alice", age: "30"}

csv.encode({{1,2},{3,4}})         // "1,2\n3,4\n"
csv.encodeWithHeaders(rows, {"name","age"})
```

### url

```go
u := url.parse("https://example.com:8080/path?q=1#sec")
// u = {scheme:"https", host:"example.com", port:"8080",
//       path:"/path", query:"q=1", fragment:"sec"}

url.build({scheme:"https", host:"example.com", path:"/api"})
url.encode("hello world")   // "hello%20world"
url.decode("hello%20world") // "hello world"
url.queryEncode({q: "go", n: 10})  // "n=10&q=go"
```

### uuid

```go
id := uuid.v4()              // "f47ac10b-58cc-4372-a567-0e02b2c3d479"
uuid.isValid(id)             // true
uuid.nil()                   // "00000000-0000-0000-0000-000000000000"
```

### bytes

```go
buf := bytes.new()
buf.writeInt32(42)
buf.writeString("hello")
hex := buf.toHex()           // "0000002a68656c6c6f"
raw := buf.bytes()           // byte table

bytes.toHex("AB\x00")       // "414200"
bytes.fromHex("414200")     // "AB\x00"
bytes.xor("secret", "key")
```

### rl (Raylib)

```go
rl.initWindow(800, 600, "My Game")
rl.setTargetFPS(60)

while !rl.windowShouldClose() {
    rl.beginDrawing()
    rl.clearBackground(rl.RAYWHITE)
    rl.drawText("Hello, Raylib!", 200, 200, 30, rl.DARKGRAY)
    rl.drawCircle(400, 300, 50, {r:255, g:0, b:100, a:255})
    rl.drawRectangle(100, 100, 200, 80, rl.BLUE)
    rl.endDrawing()
}

// Input
if rl.isKeyDown(rl.KEY_LEFT)  { x = x - 5 }
if rl.isKeyPressed(rl.KEY_SPACE) { jump() }
mx, my := rl.getMousePosition()
if rl.isMouseButtonPressed(0) { click(mx, my) }

// Audio
rl.initAudioDevice()
snd := rl.loadSound("beep.wav")
rl.playSound(snd)

// Textures
tex := rl.loadTexture("sprite.png")
rl.drawTexture(tex, 100, 100, rl.WHITE)
rl.closeWindow()
```

### Built-in functions

```go
print(...)                 // print tab-separated values with newline
type(v)                    // "nil" | "boolean" | "number" | "string" | "table" | "function" | "coroutine"
tostring(v) / tonumber(s)
#v                         // length of string or table
pairs(t) / ipairs(t) / next(t)
setmetatable(t, mt) / getmetatable(t)
rawget(t, k) / rawset(t, k, v) / rawequal(a, b)
pcall(f, ...) / xpcall(f, handler, ...) / error(msg) / assert(v, msg)
require(name) / dofile(path) / loadstring(src)
select(n, ...) / unpack(t) / table.pack(...)
```

---

## HTTP Server

Built-in `http` library backed by Go's `net/http`.

```go
// Router with multiple routes
router := http.newRouter()

counter := 0

router.get("/", func(req, res) {
    res.header("Content-Type", "text/html")
    res.write("<h1>GScript Web Server</h1>")
})

router.get("/count", func(req, res) {
    counter = counter + 1
    res.write("Count: " .. counter)
})

router.get("/greet", func(req, res) {
    name := req.param("name")
    if name == nil { name = "stranger" }
    res.write("Hello, " .. name .. "!")
})

router.post("/echo", func(req, res) {
    res.json({body: req.body, method: req.method})
})

router.listen(":9988")
```

**Request object:**

| Field / Method | Description |
|----------------|-------------|
| `req.method` | HTTP method (GET, POST, …) |
| `req.path` | Request path |
| `req.url` | Full URL |
| `req.body` | Request body as string |
| `req.headers` | Request headers table |
| `req.param("name")` | Get a query parameter |
| `req.json()` | Parse body as JSON into a table |

**Response object:**

| Method | Description |
|--------|-------------|
| `res.write(s)` | Write response body |
| `res.json(table)` | Write JSON response |
| `res.status(code)` | Set HTTP status code |
| `res.header(k, v)` | Set response header |
| `res.redirect(url)` | Redirect |

---

## Raylib Game Library

Built-in `rl` library wrapping [raylib](https://www.raylib.com/) via [raylib-go](https://github.com/gen2brain/raylib-go) for 2D/3D games and interactive apps.

```go
rl.initWindow(1200, 800, "Chinese Chess")
rl.setTargetFPS(60)

while !rl.windowShouldClose() {
    // Input
    if rl.isMouseButtonPressed(0) {
        mx, my := rl.getMousePosition()
        handleClick(mx, my)
    }
    if rl.isKeyPressed(rl.KEY_R) { restart() }
    if rl.isKeyPressed(rl.KEY_U) { undo() }

    // Rendering
    rl.beginDrawing()
    rl.clearBackground(rl.RAYWHITE)

    rl.drawRectangleRounded({x:20, y:20, width:760, height:840}, 0.05, 8, {r:210,g:140,b:60,a:255})
    rl.drawText("♟ Chinese Chess", 400, 10, 24, rl.DARKGRAY)
    rl.drawCircle(100, 100, 30, {r:200, g:30, b:30, a:255})

    tex := rl.loadTexture("board.png")
    rl.drawTexture(tex, 0, 0, rl.WHITE)

    rl.endDrawing()
}
rl.closeWindow()
```

**Key constants:** `KEY_A`–`KEY_Z`, `KEY_0`–`KEY_9`, `KEY_LEFT` `KEY_RIGHT` `KEY_UP` `KEY_DOWN`, `KEY_SPACE` `KEY_ESCAPE` `KEY_ENTER` `KEY_BACKSPACE` `KEY_F1`–`KEY_F12`

**Color constants:** `rl.RED` `rl.GREEN` `rl.BLUE` `rl.WHITE` `rl.BLACK` `rl.RAYWHITE` `rl.DARKGRAY` `rl.YELLOW` `rl.ORANGE` `rl.PURPLE` `rl.SKYBLUE` `rl.GOLD` `rl.LIME` `rl.MAROON` and more

**Drawing functions:** `drawText`, `drawRectangle`, `drawRectangleRounded`, `drawCircle`, `drawLine`, `drawTriangle`, `drawTexture`, `drawTextureEx`, `measureText`, `drawFPS`

---

## Embedding in Go

GScript can be embedded in any Go application as a scripting engine.

```go
import "github.com/gscript/gscript/gscript"

vm := gscript.New()

// Execute a script
vm.Exec(`x := 42`)

// Get and set values
vm.Set("name", "Alice")
result, _ := vm.Get("name")

// Call a GScript function from Go
vm.Exec(`func greet(name) { return "Hello, " .. name .. "!" }`)
val, _ := vm.Call("greet", "World")

// Register a Go function into GScript
vm.RegisterFunc("add", func(a, b int) int { return a + b })
vm.Exec(`print(add(3, 4))`)  // 7

// Bind a Go struct
type Player struct {
    Name  string
    Score int
}
vm.BindStruct("Player", Player{})
vm.Exec(`
    p := Player.new()
    p.Name = "Alice"
    p.Score = 100
    print(p.Name, p.Score)
`)

// Pool for concurrent use
pool := gscript.NewPool(4, gscript.WithLibs(gscript.LibBase|gscript.LibString))
pool.Do(func(vm *gscript.VM) {
    vm.Exec(`print("from pool")`)
})
```

**VM options:**

```go
gscript.New(
    gscript.WithVM(),                          // use bytecode VM (3-5x faster)
    gscript.WithLibs(gscript.LibAll),          // select stdlib modules
    gscript.WithRequirePath("./scripts"),       // require() search path
    gscript.WithPrint(func(s string) { ... }), // redirect print output
)
```

**Available lib flags:** `LibString`, `LibTable`, `LibMath`, `LibIO`, `LibOS`, `LibHTTP`, `LibJSON`, `LibBase64`, `LibHash`, `LibFS`, `LibPath`, `LibTime`, `LibNet`, `LibVec`, `LibColor`, `LibRegexp`, `LibUTF8`, `LibBit32`, `LibProcess`, `LibCSV`, `LibURL`, `LibUUID`, `LibBytes`, `LibRL`, `LibAll`, `LibApp` (no game libs), `LibGame` (rl + vec + color), `LibSafe` (no I/O)`

---

## Performance

Benchmarked on Apple M4 Max. Six runtimes compared: GScript tree-walker, GScript bytecode VM, [gopher-lua](https://github.com/yuin/gopher-lua), [starlark-go](https://github.com/google/starlark-go), native [Lua 5.5](https://www.lua.org/) (C), and [LuaJIT 2.1](https://luajit.org/).

> **Note:** Starlark forbids recursion by design, so recursive benchmarks exclude it. Previous benchmark results showing ~5µs for Starlark fib(20) were invalid — the function was failing immediately with a "recursion forbidden" error.

| Scenario | GScript Tree | GScript VM | gopher-lua | starlark-go | Lua 5.5 (C) | LuaJIT |
|---|---:|---:|---:|---:|---:|---:|
| Fib recursive (n=20) | 11,262 µs | **2,710 µs** | 1,035 µs | n/a | 227 µs | 27 µs |
| Fib recursive (n=25) | ~125 ms | **28.6 ms** | 10.9 ms | n/a | 2.5 ms | 297 µs |
| Fib iterative (n=30) | 95 µs | 89 µs | 48 µs | 9 µs | <1 µs | <1 µs |
| Table ops (1000 keys) | 1,341 µs | **529 µs** | 437 µs | 254 µs | 166 µs | 36 µs |
| String concat (100x) | 114 µs | 97 µs | 48 µs | 11 µs | 3 µs | 1 µs |
| Closure creation (1000) | 979 µs | **289 µs** | 152 µs | 207 µs | 86 µs | 42 µs |
| Function calls (10k) | 7,084 µs | **1,324 µs** | 495 µs | 732 µs | 114 µs | 3 µs |
| VM startup | 73 µs | 85 µs | 40 µs | 1 µs | — | — |

**Bytecode VM vs tree-walker:**
- **5.4x faster** on function calls (10k) — the biggest win
- **4.2x faster** on recursive fibonacci — deep recursion exercises the call stack
- **3.4x faster** on closure creation — bytecode upvalue descriptors vs runtime free-variable analysis
- **2.5x faster** on table operations — bytecode instructions avoid repeated AST dispatch
- ~1.1x on tight loops — both backends use the same underlying Value/Table types

**GScript VM vs gopher-lua:** ~2.7x slower. gopher-lua is a mature, heavily optimized Lua 5.1 implementation. The gap could be narrowed with NaN-boxing, computed goto dispatch, and instruction specialization.

**Go interpreters vs native C:** Lua 5.5 (C) is 4-5x faster than Go-based Lua due to lower-level memory management and computed goto. LuaJIT is another 8-40x faster thanks to its tracing JIT compiler.

Run benchmarks yourself:
```bash
go test ./benchmarks/ -bench=. -benchtime=3s    # Go-based runtimes
lua benchmarks/lua/bench_all.lua                 # native Lua 5.x
luajit benchmarks/lua/bench_all.lua              # LuaJIT
```

See [benchmarks/README.md](benchmarks/README.md) for full analysis.

---

## Examples

| File | Description |
|------|-------------|
| `examples/fib.gs` | Fibonacci (recursive) |
| `examples/counter.gs` | Closure counter |
| `examples/class.gs` | OOP class system |
| `examples/functional.gs` | map/filter/reduce, compose, curry, memoize |
| `examples/oop.gs` | 3-level inheritance, mixins, private fields |
| `examples/coroutines.gs` | Generators, producer-consumer, scheduler |
| `examples/data_structures.gs` | Stack, queue, linked list, BST, hash set |
| `examples/algorithms.gs` | Quicksort, BFS/DFS, Dijkstra |
| `examples/string_processing.gs` | Templates, CSV parser, expression lexer |
| `examples/iterators.gs` | Range, filter, map, zip, chained iterators |
| `examples/error_handling.gs` | Result type, custom errors, retry |
| `examples/metatables.gs` | Vector2D, matrix, observable, proxy |
| `examples/game_of_life.gs` | Conway's Game of Life |
| `examples/event_system.gs` | EventEmitter, pub/sub |
| `examples/state_machine.gs` | Traffic light, order processing |
| `examples/webserver.gs` | HTTP router demo |
| `examples/tetris.gs` | Full Tetris game (Raylib) |
| `examples/chess.gs` | **Full Chinese Chess (象棋) game (Raylib)** |

```bash
./gscript examples/game_of_life.gs
./gscript examples/algorithms.gs
./gscript examples/webserver.gs   # then visit localhost:9988
./gscript examples/chess.gs       # requires display (raylib)
```

---

## Project Structure

```
gscript/
├── cmd/gscript/          # CLI entry point (file exec, -e flag, REPL)
├── gscript/              # Public embedding API (VM, Pool, reflect bridge)
├── internal/
│   ├── lexer/            # Tokenizer (45 token types)
│   ├── parser/           # Recursive descent parser (9-level precedence)
│   ├── ast/              # AST node definitions (28 node types)
│   ├── vm/               # Register-based bytecode VM + compiler
│   │   ├── opcode.go     #   42 opcodes, instruction encoding (ABC/ABx/AsBx)
│   │   ├── proto.go      #   FuncProto, Closure, Upvalue, CallFrame types
│   │   ├── compiler.go   #   AST → bytecode compiler (2100 lines)
│   │   └── vm.go         #   VM execution engine (900 lines)
│   └── runtime/          # Tree-walking interpreter + standard library
│       ├── interpreter.go
│       ├── value.go / table.go / closure.go / coroutine.go / environment.go
│       ├── stdlib_string.go   # string.* with Lua pattern support
│       ├── stdlib_table.go    # table.*
│       ├── stdlib_math.go     # math.*
│       ├── stdlib_io.go       # io.*
│       ├── stdlib_os.go       # os.*
│       ├── stdlib_json.go     # json.encode / decode / pretty
│       ├── stdlib_base64.go   # base64.encode / decode / urlEncode
│       ├── stdlib_hash.go     # hash.md5 / sha256 / hmacSHA256
│       ├── stdlib_fs.go       # fs.* (19 functions)
│       ├── stdlib_path.go     # path.* (10 functions)
│       ├── stdlib_time.go     # time.* (13 functions + constants)
│       ├── stdlib_net.go      # net.* HTTP client
│       ├── stdlib_vec.go      # vec.* 2D/3D vectors
│       ├── stdlib_color.go    # color.* RGBA/HSV
│       ├── stdlib_regexp.go   # regexp.* RE2 engine
│       ├── stdlib_utf8.go     # utf8.* Unicode ops
│       ├── stdlib_bit32.go    # bit32.* 32-bit bitwise
│       ├── stdlib_process.go  # process.* run/shell/exec/env/which
│       ├── stdlib_csv.go      # csv.* parse/encode with headers
│       ├── stdlib_url.go      # url.* parse/build/encode/query
│       ├── stdlib_uuid.go     # uuid.* v4 generation
│       ├── stdlib_bytes.go    # bytes.* binary buffer + hex/xor
│       ├── stdlib_http.go     # http.* server
│       └── stdlib_rl.go       # rl.* Raylib (113 functions, audio, textures)
├── benchmarks/           # Performance benchmarks vs gopher-lua, starlark
├── tests/                # Integration tests
├── examples/             # 18+ example programs
└── docs/
    ├── stdlib/           # Standard library reference docs (STDLIB.md + per-lib)
    └── decisions/        # Architecture Decision Records (ADR-001 to ADR-006)
```

---

## Running Tests

```bash
go test ./...                           # all tests
go test -race ./...                     # with race detector
go test ./internal/runtime/... -v       # verbose runtime tests
go test ./internal/runtime/... -run TestClosure
go test ./benchmarks/ -bench=. -benchtime=3s
```

---

## About this Project

This project was created as an **AI capability experiment** to test how well an AI agent can design and implement a complete, production-quality programming language interpreter from scratch — without human-written code.

**Experiment goals:**
- Can an AI autonomously design a multi-component language runtime (lexer → parser → AST → interpreter → stdlib)?
- Can it implement complex semantics like closures with upvalue sharing, metatables with metamethod dispatch, and coroutines via goroutine/channel primitives?
- Can it maintain architectural coherence across 35k+ lines of generated code spanning 60+ files?
- Can it practice TDD — writing tests first, then making them pass?
- Can it debug its own failures and iterate to correctness?
- Can it integrate a C-based game library (raylib via CGO) and write a complete game?

**What was built by AI (Claude):**
- Complete lexer with 45 token types
- Recursive descent parser with 9-level operator precedence
- 28 AST node types
- Tree-walking interpreter with full Lua semantics
- **Register-based bytecode VM** (42 opcodes, AST compiler, 3-5x faster than tree-walker)
- Metatable system with all 14 metamethods
- Lexical closures with upvalue sharing (free variable analysis + `*Upvalue` pointer capture)
- Coroutines implemented via goroutines + channels
- 30+ standard libraries: string, table, math, io, os, json, base64, hash, fs, path, time, net, vec, color, regexp, utf8, bit32, process, csv, url, uuid, bytes, http, rl (raylib)
- Raylib bindings (113 functions: window, drawing, input, audio, textures, fonts)
- Embedding API with reflection-based type bridge and struct binding
- Full Tetris game + full Chinese Chess game using raylib
- 350+ unit and integration tests (including 55 bytecode VM tests)
- Performance benchmarks vs gopher-lua, starlark-go, native Lua 5.5, and LuaJIT

**Conclusion:** An AI agent can build a working, reasonably complete scripting language — including advanced features like closures, metatables, coroutines, and a bytecode compiler/VM — largely autonomously. The bytecode VM demonstrates that the AI can design and implement low-level systems (instruction encoding, register allocation, upvalue management) correctly.

---

## License

MIT
