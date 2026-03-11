# ADR-006: Go Embedding API

## Status
Accepted

## Context

GScript targets game engines as a primary use case. Game engines written in Go need to:
1. Execute GScript code from Go
2. Register Go functions/types as GScript globals
3. Convert values between Go and GScript automatically
4. Bind Go structs as GScript classes with constructors and field/method access
5. Use VM instances concurrently across goroutines

The `internal/runtime` package provides the interpreter core but is not designed for direct public use.

## Decision

We introduce a public `gscript/` package that wraps `internal/runtime` and provides a clean embedding API with five main components:

### 1. VM API (`vm.go`)

The `VM` struct wraps `runtime.Interpreter` and provides:
- `New(opts ...Option) *VM` -- constructor with options pattern
- `Exec(src string) error` -- execute GScript source
- `ExecFile(path string) error` -- execute from file
- `Call(name string, args ...interface{}) ([]interface{}, error)` -- call GScript functions from Go
- `Set(name string, val interface{}) error` / `Get(name string) (interface{}, error)` -- global variables
- `RegisterFunc(name string, fn interface{}) error` -- register Go functions
- `RegisterTable(name string, members map[string]interface{}) error` -- register namespaces
- `BindStruct(name string, proto interface{}) error` -- register Go structs as classes

A VM is NOT goroutine-safe by design. Use `Pool` for concurrent access.

### 2. Reflection-Based Type Bridge (`reflect.go`)

Bidirectional type conversion between Go and GScript using `reflect`:

| Go Type | GScript Type |
|---------|-------------|
| nil | nil |
| bool | boolean |
| int/int8/.../int64 | int |
| uint/uint8/.../uint64 | int |
| float32/float64 | float |
| string | string |
| []T | table (1-based array) |
| map[string]T | table (hash) |
| struct / *struct | table (with metatable) |
| func | function (via wrapGoFunc) |

`ToValue(interface{}) (runtime.Value, error)` converts Go to GScript.
`FromValue(runtime.Value, reflect.Type) (reflect.Value, error)` converts GScript to Go.

Go functions are wrapped automatically: `wrapGoFunc` uses reflection to convert arguments and return values. It supports variadic functions and detects `error` as the last return type.

### 3. Struct Binding (`struct.go`)

`BindStruct` registers a Go struct type as a GScript class:

```go
vm.BindStruct("Vec2", Vec2{})
```

In GScript:
```lua
v := Vec2.new(3, 4)  -- auto-constructor fills exported fields in order
print(v.X)           -- field access via __index metamethod
v.X = 10             -- field set via __newindex metamethod
print(v.Length())     -- method call via __index + reflection
```

Implementation uses a global `goValueRegistry` (map from table pointer to `reflect.Value`) as "userdata" storage. Each struct instance is a GScript table with:
- A metatable containing `__index` (field/method access) and `__newindex` (field set)
- An entry in the global registry mapping the table pointer to the underlying `reflect.Value`

The `reflect.Value` is stored as a pointer-to-struct, making fields settable.

Custom constructors are supported via `BindStructWithConstructor`.

### 4. VMPool (`pool.go`)

`Pool` manages a pool of VM instances for concurrent use:

```go
pool := gscript.NewPool(10, func() *gscript.VM {
    vm := gscript.New()
    vm.BindStruct("Vec2", Vec2{})
    return vm
})

pool.Do(func(vm *gscript.VM) error {
    return vm.Exec(`v := Vec2.new(1, 2)`)
})
```

### 5. Options Pattern (`options.go`)

Configuration via functional options:
- `WithLibs(LibFlags)` -- control which standard libraries are loaded
- `WithRequirePath(string)` -- set module search path
- `WithPrint(func(...interface{}))` -- override print for output capture

`LibSafe` provides a sandboxed subset (string, table, math, coroutine) suitable for untrusted scripts.

### 6. Structured Errors (`error.go`)

All errors are `*gscript.Error` with:
- `Kind` (lex, parse, runtime, script)
- `Message`, `File`, `Line`, `Col`
- `Value` for script-raised errors

## Runtime Change

Added `CallFunction(fn Value, args []Value) ([]Value, error)` as a public method on `Interpreter`, delegating to the existing private `callFunction`. This is the only change to `internal/runtime`.

## Design Trade-offs

1. **Global registry for struct instances**: We use a `sync.RWMutex`-protected map keyed by table pointer. This is simple and works because `*Table` is heap-allocated (stable pointer). The trade-off is that instances are never garbage-collected from the registry. For game engines where entities have bounded lifetimes, this is acceptable. A future improvement could use `runtime.SetFinalizer` or explicit cleanup.

2. **Value-semantics for nested struct access**: Accessing a struct field that is itself a struct (e.g., `entity.Transform.Position`) returns a copy. Mutations to the copy do not affect the original. This is intentional Go semantics. The game engine example works around this by providing explicit getter/setter functions (e.g., `engine.getEntityPos()`, `engine.setEntityPos()`).

3. **Dot vs colon syntax for method calls**: Go struct methods are accessed via `v.Method()` (dot syntax, no implicit self). The `__index` metamethod returns a function with the receiver already bound via reflection. Using colon syntax (`v:Method()`) would pass an extra self argument, causing errors for most Go methods.

4. **No step-limiting**: The `maxSteps` field in options is reserved for future use. Currently there is no instruction-counting mechanism in the tree-walking interpreter.

## File Structure

```
gscript/
  error.go      -- structured errors
  options.go    -- configuration options
  reflect.go    -- bidirectional type conversion
  struct.go     -- struct binding + userdata registry
  vm.go         -- main VM API
  pool.go       -- concurrent VM pool
  gscript_test.go -- comprehensive tests
```

## Test Coverage

30 tests covering:
- Basic VM operations (Exec, Call, Set/Get)
- Go function registration (single/multi return, errors)
- Type conversion (primitives, slices, maps, functions)
- Struct binding (constructor, field access/set, method calls, struct returns, custom constructors)
- Pool (basic, concurrent, Do pattern)
- Error handling (parse errors, runtime errors)
- Options (WithPrint, WithLibs)
