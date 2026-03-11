package benchmarks

import (
	"fmt"
	"testing"

	gs "github.com/gscript/gscript/gscript"
	lua "github.com/yuin/gopher-lua"
	"go.starlark.net/starlark"
)

// ---------------------------------------------------------------------------
// Fibonacci (recursive, n=20) -- pure computation
// ---------------------------------------------------------------------------

func BenchmarkGScriptFibRecursive(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
fib(20)
`)
	}
}

func BenchmarkGScriptFibRecursive_Warm(b *testing.B) {
	vm := gs.New()
	vm.Exec(`
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("fib", 20)
	}
}

func BenchmarkGopherLuaFibRecursive(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local function fib(n)
    if n < 2 then return n end
    return fib(n-1) + fib(n-2)
end
fib(20)
`)
		L.Close()
	}
}

func BenchmarkStarlarkFibRecursive(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "fib.star", `
def fib(n):
    if n < 2:
        return n
    return fib(n-1) + fib(n-2)

fib(20)
`, nil)
	}
}

// ---------------------------------------------------------------------------
// Fibonacci (iterative, n=30) -- loop performance
// ---------------------------------------------------------------------------

func BenchmarkGScriptFibIterative(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`
func fib(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
fib(30)
`)
	}
}

func BenchmarkGScriptFibIterative_Warm(b *testing.B) {
	vm := gs.New()
	vm.Exec(`
func fib(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("fib", 30)
	}
}

func BenchmarkGopherLuaFibIterative(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local function fib(n)
    local a, b = 0, 1
    for i = 1, n do
        a, b = b, a + b
    end
    return a
end
fib(30)
`)
		L.Close()
	}
}

func BenchmarkStarlarkFibIterative(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "fib.star", `
def fib(n):
    a, b = 0, 1
    for i in range(n):
        a, b = b, a + b
    return a

fib(30)
`, nil)
	}
}

// ---------------------------------------------------------------------------
// Table / dict operations -- create table with 1000 keys, read all keys
// ---------------------------------------------------------------------------

func BenchmarkGScriptTableOps(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`
t := {}
for i := 0; i < 1000; i++ {
    t[tostring(i)] = i
}
sum := 0
for i := 0; i < 1000; i++ {
    sum = sum + t[tostring(i)]
}
`)
	}
}

func BenchmarkGopherLuaTableOps(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local t = {}
for i = 0, 999 do
    t[tostring(i)] = i
end
local sum = 0
for i = 0, 999 do
    sum = sum + t[tostring(i)]
end
`)
		L.Close()
	}
}

func BenchmarkStarlarkTableOps(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "table.star", `
t = {}
for i in range(1000):
    t[str(i)] = i
s = 0
for i in range(1000):
    s = s + t[str(i)]
`, nil)
	}
}

// ---------------------------------------------------------------------------
// String concatenation -- concatenate strings in a loop
// ---------------------------------------------------------------------------

func BenchmarkGScriptStringConcat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`
s := ""
for i := 0; i < 100; i++ {
    s = s .. "x"
}
`)
	}
}

func BenchmarkGopherLuaStringConcat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local s = ""
for i = 1, 100 do
    s = s .. "x"
end
`)
		L.Close()
	}
}

func BenchmarkStarlarkStringConcat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "str.star", `
s = ""
for i in range(100):
    s = s + "x"
`, nil)
	}
}

// ---------------------------------------------------------------------------
// Closure creation -- create 1000 closures
// ---------------------------------------------------------------------------

func BenchmarkGScriptClosureCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`
func make(x) {
    return func() { return x }
}
closures := {}
for i := 1; i <= 1000; i++ {
    closures[i] = make(i)
}
`)
	}
}

func BenchmarkGopherLuaClosureCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local function make(x)
    return function() return x end
end
local closures = {}
for i = 1, 1000 do
    closures[i] = make(i)
end
`)
		L.Close()
	}
}

func BenchmarkStarlarkClosureCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "closure.star", `
def make(x):
    def inner():
        return x
    return inner

closures = []
for i in range(1000):
    closures.append(make(i))
`, nil)
	}
}

// ---------------------------------------------------------------------------
// Function calls -- call a simple function 10000 times
// ---------------------------------------------------------------------------

func BenchmarkGScriptFunctionCalls(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`
func add(a, b) {
    return a + b
}
x := 0
for i := 0; i < 10000; i++ {
    x = add(x, 1)
}
`)
	}
}

func BenchmarkGScriptFunctionCalls_Warm(b *testing.B) {
	vm := gs.New()
	vm.Exec(`
func add(a, b) {
    return a + b
}
func run() {
    x := 0
    for i := 0; i < 10000; i++ {
        x = add(x, 1)
    }
    return x
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("run")
	}
}

func BenchmarkGopherLuaFunctionCalls(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local function add(a, b)
    return a + b
end
local x = 0
for i = 1, 10000 do
    x = add(x, 1)
end
`)
		L.Close()
	}
}

func BenchmarkStarlarkFunctionCalls(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "calls.star", `
def add(a, b):
    return a + b

x = 0
for i in range(10000):
    x = add(x, 1)
`, nil)
	}
}

// ---------------------------------------------------------------------------
// VM startup -- measure time to create a new VM instance
// ---------------------------------------------------------------------------

func BenchmarkGScriptVMStartup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`x := 1`)
	}
}

func BenchmarkGopherLuaVMStartup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`x = 1`)
		L.Close()
	}
}

func BenchmarkStarlarkVMStartup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "startup.star", `x = 1`, nil)
	}
}

// ---------------------------------------------------------------------------
// Summary printer (run with -v to see comparison table)
// ---------------------------------------------------------------------------

func TestPrintBenchmarkInfo(t *testing.T) {
	fmt.Println("=== GScript Performance Benchmark Suite ===")
	fmt.Println()
	fmt.Println("Scenarios tested:")
	fmt.Println("  1. Fibonacci (recursive, n=20)  - pure computation / recursion depth")
	fmt.Println("  2. Fibonacci (iterative, n=30)  - loop performance")
	fmt.Println("  3. Table/dict operations         - 1000-key create + read")
	fmt.Println("  4. String concatenation           - 100 iterations of string append")
	fmt.Println("  5. Closure creation               - create 1000 closures")
	fmt.Println("  6. Function calls                 - call function 10000 times")
	fmt.Println("  7. VM startup                     - create VM + run trivial script")
	fmt.Println()
	fmt.Println("Runtimes compared: GScript, gopher-lua, starlark-go")
	fmt.Println()
	fmt.Println("Run with:  go test ./benchmarks/ -bench=. -benchtime=3s -count=1")
}
