# GScript

GScript is a scripting language with **Go-like syntax and Lua semantics**, implemented in Go and executed via a tree-walking interpreter.

- Syntax close to Go (`:=` declarations, `func`, `for`, `if`)
- Full Lua semantics (table, metatable, closure, coroutine, multiple return values)
- Built-in HTTP server and OpenGL 2D drawing support

```
              ┌──────────────┐
 .gs source → │ Lexer/Parser │ → AST → Interpreter → result
              └──────────────┘
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

// Logic (short-circuit)
true && false   // false
true || false   // true
!true           // false

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
```

#### Variadic functions

```go
func sum(...) {
    total := 0
    for _, v := range ... {
        total = total + v
    }
    return total
}
print(sum(1, 2, 3, 4, 5))  // 15
```

---

### 6. Closures / Upvalues

GScript supports full lexical closures. Multiple closures from the same scope share the same upvalue reference.

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
```

---

### 8. Metatable

All 14 Lua metamethods are supported, enabling operator overloading, OOP inheritance, and more.

```go
// __index for OOP inheritance
Animal := {}
Animal.new = func(name) {
    self := {name: name}
    setmetatable(self, {__index: Animal})
    return self
}
Animal.speak = func(self) {
    return self.name .. " makes a sound"
}

Dog := {}
setmetatable(Dog, {__index: Animal})
Dog.speak = func(self) {
    return self.name .. " says woof!"
}
Dog.new = func(name) {
    self := Animal.new(name)
    setmetatable(self, {__index: Dog})
    return self
}

rex := Dog.new("Rex")
print(rex.speak(rex))  // Rex says woof!
```

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
Vec2.__eq  = func(a, b) { return a.x==b.x && a.y==b.y }

v1 := Vec2.new(1, 2)
v2 := Vec2.new(3, 4)
v3 := v1 + v2
print(v3.x, v3.y)  // 4  6
```

**Supported metamethods:**

| Metamethod | Triggered by |
|------------|-------------|
| `__index` | Reading a missing field |
| `__newindex` | Writing a missing field |
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

_, v := coroutine.resume(co, 1)   // start with 1, v=2
_, v  = coroutine.resume(co, 3)   // send 3, v=6
_, v  = coroutine.resume(co, 5)   // send 5, v=10
```

**API:**

| Function | Description |
|----------|-------------|
| `coroutine.create(f)` | Create a coroutine |
| `coroutine.resume(co, ...)` | Resume execution, returns `ok, values...` |
| `coroutine.yield(...)` | Suspend, returns values passed to next resume |
| `coroutine.wrap(f)` | Returns a function that resumes the coroutine each call |
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

---

## Standard Library

### string

```go
s := "Hello, World!"

string.len(s)               // 13
string.upper(s)             // "HELLO, WORLD!"
string.lower(s)             // "hello, world!"
string.sub(s, 1, 5)         // "Hello"
string.sub(s, -6)           // "orld!"
string.rep("ab", 3)         // "ababab"
string.rep("ab", 3, "-")    // "ab-ab-ab"
string.reverse("hello")     // "olleh"
string.byte("A")            // 65
string.char(65, 66, 67)     // "ABC"

// Find (supports Lua patterns)
string.find("hello world", "world")      // 7  11
string.find("hello", "l+")              // 3  4

// Pattern matching
string.match("2024-03-11", "(%d+)-(%d+)-(%d+)")  // "2024" "03" "11"

// Global substitution
string.gsub("hello world", "o", "0")    // "hell0 w0rld"  2
string.gsub("aaa", "a", "b", 2)         // "bba"  2

// Iterating matches
for word := range string.gmatch("one two three", "%a+") {
    print(word)
}

// Formatting
string.format("%d + %d = %d", 1, 2, 3)      // "1 + 2 = 3"
string.format("%.2f", 3.14159)               // "3.14"
string.format("%s is %d years old", "Alice", 30)

// String methods via metatable
("hello"):upper()     // "HELLO"
("  hi  "):sub(3, 4)  // "hi"
```

**Lua pattern classes:**

| Pattern | Matches |
|---------|---------|
| `%d` | Digits |
| `%a` | Letters |
| `%l` | Lowercase letters |
| `%u` | Uppercase letters |
| `%s` | Whitespace |
| `%w` | Alphanumeric |
| `%p` | Punctuation |
| `.` | Any character |

---

### table

```go
t := {10, 20, 30}

table.insert(t, 40)         // append: {10,20,30,40}
table.insert(t, 2, 15)      // at position: {10,15,20,30,40}
table.remove(t)             // remove last: {10,15,20,30}
table.remove(t, 1)          // remove first: {15,20,30}

table.concat(t, ", ")       // "15, 20, 30"
table.concat(t, "-", 2, 3)  // "20-30"

table.sort(t)                                    // ascending
table.sort(t, func(a, b) { return a > b })       // descending

a, b, c := table.unpack({10, 20, 30})
print(a, b, c)  // 10  20  30
```

---

### math

```go
math.pi           // 3.141592653589793
math.huge         // +Inf
math.maxinteger   // 9223372036854775807

math.abs(-5)      // 5
math.floor(3.7)   // 3
math.ceil(3.2)    // 4
math.sqrt(16)     // 4
math.pow(2, 10)   // 1024

math.sin(math.pi / 2)  // 1
math.cos(0)            // 1
math.atan(1, 1)        // π/4

math.max(1, 5, 3, 2)   // 5
math.min(1, 5, 3, 2)   // 1

math.log(math.exp(1))  // 1
math.log(100, 10)      // 2

math.random()           // random float in [0, 1)
math.random(6)          // random int in [1, 6]
math.random(1, 100)     // random int in [1, 100]
math.randomseed(42)

math.type(1)     // "integer"
math.type(1.0)   // "float"
math.type("x")   // false
```

---

### io

```go
io.write("hello ")    // write without newline
io.write("world\n")

line := io.read()     // read a line
num  := io.read("*n") // read a number
all  := io.read("*a") // read all input

-- File I/O
f := io.open("data.txt", "r")
content := f.read("*a")
f.close()

f2 := io.open("out.txt", "w")
f2.write("hello\n")
f2.close()
```

---

### os

```go
os.time()           // Unix timestamp (seconds)
os.clock()          // CPU time (seconds)
os.date("%Y-%m-%d") // formatted date, e.g. "2024-03-11"
os.getenv("HOME")   // environment variable
os.exit(0)          // exit process
```

---

### Built-in functions

```go
print(...)                 // print tab-separated values with newline
type(v)                    // return type name string
tostring(v)                // convert to string
tonumber(s [, base])       // convert to number
#v                         // length of string or table

pairs(t)                   // iterate all key-value pairs in table
ipairs(t)                  // iterate array part (keys 1, 2, 3, ...)
next(t [, key])            // low-level iterator function
select(n, ...)             // return arguments from index n
unpack(t [, i [, j]])      // unpack table to multiple values

setmetatable(t, mt)        // set metatable
getmetatable(t)            // get metatable
rawget(t, k)               // get without __index
rawset(t, k, v)            // set without __newindex
rawequal(a, b)             // compare without __eq

pcall(f, ...)              // protected call
xpcall(f, handler, ...)    // protected call with message handler
error(msg)                 // raise an error
assert(v [, msg])          // assert condition

require(name)              // load a module
dofile(path)               // execute a file
loadstring(src)            // compile source string to function
```

---

## HTTP Server

Built-in `http` library backed by Go's `net/http`.

```go
// Minimal server
http.listen(":8080", func(req, res) {
    res.write("Hello from GScript!\n")
    res.write("Path: " .. req.path .. "\n")
})
```

```go
// Router
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
| `req.query` | Query parameters table |
| `req.param("name")` | Get a query parameter |
| `req.json()` | Parse body as JSON into a table |

**Response object:**

| Method | Description |
|--------|-------------|
| `res.write(s)` | Write response body |
| `res.writeln(s)` | Write response body with newline |
| `res.json(table)` | Write JSON response |
| `res.status(code)` | Set HTTP status code |
| `res.header(k, v)` | Set response header |
| `res.redirect(url)` | Redirect |

---

## OpenGL Drawing

Built-in `gl` library based on OpenGL 4.1 + GLFW for games and visualization.

```go
// Create window
win := gl.newWindow(800, 600, "My Game")

// Game loop
for !win.shouldClose() {
    win.pollEvents()

    win.clear(0.05, 0.05, 0.1)   // clear with dark blue

    // Filled rectangle (x, y, w, h, r, g, b, a)
    gl.drawRect(100, 100, 200, 150, 1, 0, 0, 1)       // red
    gl.drawRect(400, 200, 100, 100, 0, 1, 0, 0.5)     // semi-transparent green

    // Outlined rectangle (x, y, w, h, r, g, b, lineWidth)
    gl.drawRectOutline(50, 50, 300, 200, 1, 1, 0, 2)

    // Text (text, x, y, scale, r, g, b)
    gl.drawText("Score: 1234", 10, 10, 1.5, 1, 1, 1)

    win.swapBuffers()
}
win.close()
```

**Keyboard input:**

```go
// Held down
if gl.isKeyDown(gl.KEY_LEFT) {
    x = x - 5
}

// Just pressed (single trigger)
if gl.isKeyJustPressed(gl.KEY_SPACE) {
    jump()
}
```

**Key constants:** `KEY_LEFT` `KEY_RIGHT` `KEY_UP` `KEY_DOWN` `KEY_SPACE` `KEY_ESCAPE` `KEY_ENTER` `KEY_A`–`KEY_Z` `KEY_0`–`KEY_9`

---

## Examples

### Fibonacci

```go
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

for i := 0; i <= 20; i++ {
    print(i, fib(i))
}
```

### Closure Counter

```go
func makeCounter(name, start) {
    n := start
    return {
        inc:   func() { n = n + 1; return n },
        dec:   func() { n = n - 1; return n },
        get:   func() { return n },
        reset: func() { n = start },
        name:  name,
    }
}

c := makeCounter("hits", 0)
print(c.inc())   // 1
print(c.inc())   // 2
print(c.get())   // 2
c.reset()
print(c.get())   // 0
```

### OOP Inheritance

```go
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

### Coroutine Generator

```go
func range_gen(from, to) {
    return coroutine.wrap(func() {
        for i := from; i <= to; i++ {
            coroutine.yield(i)
        }
    })
}

sum := 0
gen := range_gen(1, 100)
for {
    v := gen()
    if v == nil { break }
    sum = sum + v
}
print(sum)  // 5050
```

### Tetris (built-in demo)

```bash
./gscript examples/tetris.gs
```

Controls: `←→` move, `↑`/`X` rotate CW, `Z` rotate CCW, `↓` soft drop, `Space` hard drop, `C` hold, `P` pause, `R` restart

---

## Project Structure

```
gscript/
├── cmd/gscript/          # CLI entry point
├── internal/
│   ├── lexer/            # Tokenizer
│   ├── parser/           # Recursive descent parser
│   ├── ast/              # AST node definitions
│   └── runtime/          # Tree-walking interpreter + standard library
│       ├── interpreter.go
│       ├── value.go       # Type system (tagged union)
│       ├── table.go       # Table implementation
│       ├── closure.go     # Closures / Upvalues
│       ├── coroutine.go   # Coroutines
│       ├── stdlib_string.go
│       ├── stdlib_table.go
│       ├── stdlib_math.go
│       ├── stdlib_io.go
│       ├── stdlib_os.go
│       ├── stdlib_http.go
│       └── stdlib_gl.go   # OpenGL drawing
├── tests/                # Integration tests
├── examples/             # Example programs
│   ├── fib.gs
│   ├── counter.gs
│   ├── class.gs
│   ├── webserver.gs
│   └── tetris.gs
└── docs/decisions/       # Architecture decision records
```

---

## Running Tests

```bash
go test ./...               # all tests
go test -race ./...         # with race detector
go test ./internal/runtime/... -v -run TestClosure
```

---

## License

MIT
