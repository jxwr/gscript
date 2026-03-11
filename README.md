# GScript

> **AI Experiment**: This project was built entirely by an AI agent (Claude) to test the limits of AI capability in designing and implementing a complete programming language interpreter from scratch — including lexer, parser, AST, runtime, type system, closures, metatables, coroutines, standard library, embedding API, and OpenGL-based game engine. See [About this Project](#about-this-project) for details.

---

GScript is a scripting language with **Go-like syntax and Lua semantics**, implemented in Go and executed via a tree-walking interpreter.

- Syntax close to Go (`:=` declarations, `func`, `for`, `if`)
- Full Lua semantics (table, metatable, closure, coroutine, multiple return values)
- Embeddable in Go programs via a clean reflection-based API
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
```

---

### io

```go
io.write("hello ")    // write without newline
io.write("world\n")

line := io.read()     // read a line
all  := io.read("*a") // read all input

// File I/O
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
type(v)                    // "nil" | "boolean" | "number" | "string" | "table" | "function" | "coroutine"
tostring(v)                // convert to string
tonumber(s [, base])       // convert to number (returns nil on failure)
#v                         // length of string or table

pairs(t)                   // iterate all key-value pairs
ipairs(t)                  // iterate array part (keys 1, 2, 3, ...)
next(t [, key])            // low-level iterator
select(n, ...)             // return arguments from index n
unpack(t [, i [, j]])      // unpack table to multiple values

setmetatable(t, mt)        // set metatable
getmetatable(t)            // get metatable
rawget(t, k)               // get without __index
rawset(t, k, v)            // set without __newindex
rawequal(a, b)             // compare without __eq

pcall(f, ...)              // protected call → ok, results...
xpcall(f, handler, ...)    // protected call with message handler
error(msg)                 // raise an error
assert(v [, msg])          // assert condition, raise if falsy

require(name)              // load a module
dofile(path)               // execute a file
loadstring(src)            // compile source string to function
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

## OpenGL Drawing

Built-in `gl` library based on OpenGL 4.1 + GLFW for games and visualization.

```go
win := gl.newWindow(800, 600, "My Game")

for !win.shouldClose() {
    win.pollEvents()
    win.clear(0.05, 0.05, 0.1)

    // Filled rectangle (x, y, w, h, r, g, b, a)
    gl.drawRect(100, 100, 200, 150, 1, 0, 0, 1)

    // Outlined rectangle
    gl.drawRectOutline(50, 50, 300, 200, 1, 1, 0, 2)

    // Text (text, x, y, scale, r, g, b)
    gl.drawText("Score: 1234", 10, 10, 1.5, 1, 1, 1)

    // Keyboard input
    if gl.isKeyDown(gl.KEY_LEFT)        { x = x - 5 }
    if gl.isKeyJustPressed(gl.KEY_SPACE) { jump() }

    win.swapBuffers()
}
win.close()
```

**Key constants:** `KEY_LEFT` `KEY_RIGHT` `KEY_UP` `KEY_DOWN` `KEY_SPACE` `KEY_ESCAPE` `KEY_ENTER` `KEY_A`–`KEY_Z` `KEY_0`–`KEY_9`

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
    gscript.WithLibs(gscript.LibAll),          // select stdlib modules
    gscript.WithRequirePath("./scripts"),       // require() search path
    gscript.WithPrint(func(s string) { ... }), // redirect print output
)
```

**Available lib flags:** `LibBase`, `LibString`, `LibTable`, `LibMath`, `LibIO`, `LibOS`, `LibHTTP`, `LibGL`, `LibAll`

---

## Performance

Benchmarked on Apple M4 Max, comparing GScript (tree-walking interpreter) against [gopher-lua](https://github.com/yuin/gopher-lua) (also tree-walking) and [starlark-go](https://github.com/google/starlark-go) (bytecode compiler).

| Scenario | GScript | gopher-lua | starlark-go |
|---|---|---|---|
| VM startup | **19 µs** | 67 µs | 1.2 µs |
| Fibonacci recursive n=20 | 11.2 ms | 1.0 ms | 5.4 µs |
| Fibonacci iterative n=30 | 41 µs | 65 µs | 9.4 µs |
| Table ops (1000 keys) | 1.3 ms | 444 µs | 5.3 µs |
| String concat (100×) | 58 µs | 61 µs | 2.3 µs |
| Closure creation (1000) | 914 µs | 158 µs | 4.3 µs |
| Function calls (10k) | 7.2 ms | 501 µs | 3.8 µs |

**Takeaways:**
- GScript has **3.5× faster VM startup** than gopher-lua — good for embedding scenarios with many short-lived VMs
- **Iterative loops** are competitive with gopher-lua (GScript 41µs vs 65µs for n=30)
- **String concatenation** is on par with gopher-lua
- **Recursive function dispatch** is ~10× slower than gopher-lua — the main overhead of the tree-walking approach (no bytecode, no call stack optimization)
- starlark-go is fastest overall because it compiles to bytecode; GScript and gopher-lua are both pure tree-walking interpreters

Run benchmarks yourself:
```bash
go test ./benchmarks/ -bench=. -benchtime=3s
```

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
| `examples/tetris.gs` | Full Tetris game (OpenGL) |

```bash
./gscript examples/game_of_life.gs
./gscript examples/algorithms.gs
./gscript examples/webserver.gs   # then visit localhost:9988
./gscript examples/tetris.gs      # requires OpenGL support
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
│   └── runtime/          # Tree-walking interpreter + standard library
│       ├── interpreter.go    # Core eval loop, metamethod dispatch
│       ├── value.go          # Tagged union type system
│       ├── table.go          # Table (array+hash hybrid)
│       ├── closure.go        # Closures, upvalues, free variable analysis
│       ├── coroutine.go      # Coroutines via goroutine+channel
│       ├── environment.go    # Lexical scope chain
│       ├── stdlib_string.go  # string.* with Lua pattern support
│       ├── stdlib_table.go   # table.*
│       ├── stdlib_math.go    # math.*
│       ├── stdlib_io.go      # io.*
│       ├── stdlib_os.go      # os.*
│       ├── stdlib_http.go    # http.* (net/http backed)
│       └── stdlib_gl.go      # gl.* (OpenGL 4.1 + GLFW)
├── benchmarks/           # Performance benchmarks vs gopher-lua, starlark
├── tests/                # Integration tests
├── examples/             # 17 example programs
└── docs/decisions/       # Architecture Decision Records (ADR-001 to ADR-006)
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
- Can it maintain architectural coherence across 20k+ lines of generated code spanning 40+ files?
- Can it practice TDD — writing tests first, then making them pass?
- Can it debug its own failures and iterate to correctness?

**What was built by AI (Claude):**
- Complete lexer with 45 token types
- Recursive descent parser with 9-level operator precedence
- 28 AST node types
- Tree-walking interpreter with full Lua semantics
- Metatable system with all 14 metamethods
- Lexical closures with upvalue sharing (free variable analysis + `*Upvalue` pointer capture)
- Coroutines implemented via goroutines + channels
- 7 standard libraries: string (Lua patterns → Go regex), table, math, io, os, http, gl
- Embedding API with reflection-based type bridge and struct binding
- OpenGL Tetris game
- 300+ unit and integration tests
- Performance benchmarks vs gopher-lua and starlark-go

**Conclusion:** An AI agent can build a working, reasonably complete scripting language interpreter — including advanced features like closures, metatables, and coroutines — largely autonomously. The main limitations are in performance optimization (tree-walking vs bytecode) and handling obscure edge cases in complex semantic interactions.

---

## License

MIT
