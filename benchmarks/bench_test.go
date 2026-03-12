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
// Note: Starlark forbids recursion by design, so it is excluded here.
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

func BenchmarkGScriptVMFibRecursive(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
		vm.Exec(`
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
fib(20)
`)
	}
}

func BenchmarkGScriptJITFibRecursive(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithJIT())
		vm.Exec(`
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
fib(20)
`)
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

// ---------------------------------------------------------------------------
// Fibonacci (recursive, n=25) -- heavier recursion
// ---------------------------------------------------------------------------

func BenchmarkGScriptVMFibRecursive_N25(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
		vm.Exec(`
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
fib(25)
`)
	}
}

func BenchmarkGScriptJITFibRecursive_N25(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithJIT())
		vm.Exec(`
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
fib(25)
`)
	}
}

func BenchmarkGopherLuaFibRecursive_N25(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local function fib(n)
    if n < 2 then return n end
    return fib(n-1) + fib(n-2)
end
fib(25)
`)
		L.Close()
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

func BenchmarkGScriptVMFibIterative(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
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

func BenchmarkGScriptJITFibIterative(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithJIT())
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
// Heavy loop -- sum 1..100000 (JIT shines here: compilation cost amortized)
// ---------------------------------------------------------------------------

func BenchmarkGScriptVMHeavyLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
		vm.Exec(`
func sumN(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
sumN(100000)
`)
	}
}

func BenchmarkGScriptJITHeavyLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithJIT())
		vm.Exec(`
func sumN(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
sumN(100000)
`)
	}
}

func BenchmarkGopherLuaHeavyLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`
local function sumN(n)
    local s = 0
    for i = 1, n do
        s = s + i
    end
    return s
end
sumN(100000)
`)
		L.Close()
	}
}

func BenchmarkStarlarkHeavyLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{Name: "bench"}
		starlark.ExecFile(thread, "sum.star", `
def sumN(n):
    s = 0
    for i in range(1, n+1):
        s = s + i
    return s

sumN(100000)
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

func BenchmarkGScriptVMTableOps(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
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
def run():
    t = {}
    for i in range(1000):
        t[str(i)] = i
    s = 0
    for i in range(1000):
        s = s + t[str(i)]
    return s

run()
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

func BenchmarkGScriptVMStringConcat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
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
def run():
    s = ""
    for i in range(100):
        s = s + "x"
    return s

run()
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

func BenchmarkGScriptVMClosureCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
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

def run():
    closures = []
    for i in range(1000):
        closures.append(make(i))
    return closures

run()
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

func BenchmarkGScriptVMFunctionCalls(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
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

func BenchmarkGScriptJITFunctionCalls(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithJIT())
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

def run():
    x = 0
    for i in range(10000):
        x = add(x, 1)
    return x

run()
`, nil)
	}
}

// ---------------------------------------------------------------------------
// VM startup -- measure time to create a new VM instance
// ---------------------------------------------------------------------------

func BenchmarkGScriptStartup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New()
		vm.Exec(`x := 1`)
	}
}

func BenchmarkGScriptVMStartup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		vm := gs.New(gs.WithVM())
		vm.Exec(`x := 1`)
	}
}

func BenchmarkGopherLuaStartup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		L.DoString(`x = 1`)
		L.Close()
	}
}

func BenchmarkStarlarkStartup(b *testing.B) {
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
	fmt.Println("  1. Fibonacci (recursive, n=20)  - pure computation (no Starlark: recursion forbidden)")
	fmt.Println("  2. Fibonacci (iterative, n=30)  - loop performance")
	fmt.Println("  3. Table/dict operations         - 1000-key create + read")
	fmt.Println("  4. String concatenation           - 100 iterations of string append")
	fmt.Println("  5. Closure creation               - create 1000 closures")
	fmt.Println("  6. Function calls                 - call function 10000 times")
	fmt.Println("  7. VM startup                     - create VM + run trivial script")
	fmt.Println()
	fmt.Println("Runtimes compared: GScript (tree-walker), GScript (bytecode VM), GScript (JIT), gopher-lua, starlark-go")
	fmt.Println()
	fmt.Println("Note: Starlark forbids recursion and top-level for-loops,")
	fmt.Println("      so all Starlark benchmarks wrap code in functions.")
	fmt.Println()
	fmt.Println("Run with:  go test ./benchmarks/ -bench=. -benchtime=3s -count=1")
}
