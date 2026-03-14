package benchmarks

import (
	"testing"
	gs "github.com/gscript/gscript/gscript"
)

func BenchmarkGScriptJITHeavyLoopWarm(b *testing.B) {
	vm := gs.New(gs.WithJIT())
	// Define function and warm up JIT (threshold=10 calls)
	vm.Exec(`
func sumN(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
for i := 1; i <= 15; i++ { sumN(10) }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("sumN", 100000)
	}
}

func BenchmarkGScriptVMHeavyLoopWarm(b *testing.B) {
	vm := gs.New(gs.WithVM())
	vm.Exec(`
func sumN(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("sumN", 100000)
	}
}

func BenchmarkGScriptJITFibIterativeWarm(b *testing.B) {
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
for i := 1; i <= 15; i++ { fib(10) }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("fib", 30)
	}
}

func BenchmarkGScriptVMFibIterativeWarm(b *testing.B) {
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
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("fib", 30)
	}
}

func BenchmarkGScriptVMFunctionCallsWarm(b *testing.B) {
	vm := gs.New(gs.WithVM())
	vm.Exec(`
func add(a, b) {
    return a + b
}
func callMany() {
    x := 0
    for i := 0; i < 10000; i++ {
        x = add(x, 1)
    }
    return x
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("callMany")
	}
}

func BenchmarkGScriptJITFunctionCallsWarm(b *testing.B) {
	vm := gs.New(gs.WithJIT())
	vm.Exec(`
func add(a, b) {
    return a + b
}
func callMany() {
    x := 0
    for i := 0; i < 10000; i++ {
        x = add(x, 1)
    }
    return x
}
for i := 1; i <= 15; i++ { callMany() }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("callMany")
	}
}

func BenchmarkGScriptJITFibRecursiveWarm(b *testing.B) {
	vm := gs.New(gs.WithJIT())
	vm.Exec(`
func fib(n) {
    if n < 2 {
        return n
    }
    return fib(n-1) + fib(n-2)
}
for i := 1; i <= 15; i++ { fib(10) }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("fib", 20)
	}
}

func BenchmarkGScriptVMFibRecursiveWarm(b *testing.B) {
	vm := gs.New(gs.WithVM())
	vm.Exec(`
func fib(n) {
    if n < 2 {
        return n
    }
    return fib(n-1) + fib(n-2)
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("fib", 20)
	}
}
