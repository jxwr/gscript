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

func BenchmarkGScriptJITAckermannWarm(b *testing.B) {
	vm := gs.New(gs.WithJIT())
	vm.Exec(`
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
for i := 1; i <= 15; i++ { ack(2, 2) }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("ack", 3, 4)
	}
}

func BenchmarkGScriptVMAckermannWarm(b *testing.B) {
	vm := gs.New(gs.WithVM())
	vm.Exec(`
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("ack", 3, 4)
	}
}

// --- Math Intensive (Leibniz Pi) ---

func BenchmarkGScriptJITMathLeibnizWarm(b *testing.B) {
	vm := gs.New(gs.WithJIT())
	vm.Exec(`
func leibniz(n) {
    sum := 0.0
    sign := 1.0
    for i := 0; i < n; i++ {
        sum = sum + sign / (2.0 * i + 1.0)
        sign = -sign
    }
    return sum * 4.0
}
for i := 1; i <= 15; i++ { leibniz(100) }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("leibniz", 100000)
	}
}

func BenchmarkGScriptVMMathLeibnizWarm(b *testing.B) {
	vm := gs.New(gs.WithVM())
	vm.Exec(`
func leibniz(n) {
    sum := 0.0
    sign := 1.0
    for i := 0; i < n; i++ {
        sum = sum + sign / (2.0 * i + 1.0)
        sign = -sign
    }
    return sum * 4.0
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("leibniz", 100000)
	}
}

// --- Table Field Access (particle update) ---

func BenchmarkGScriptJITTableFieldWarm(b *testing.B) {
	vm := gs.New(gs.WithJIT())
	vm.Exec(`
particles := {}
for i := 1; i <= 100; i++ {
    particles[i] = {x: 1.0 * i, y: 2.0 * i, z: 3.0 * i, vx: 0.01, vy: 0.02, vz: 0.03}
}
func step(ps, n) {
    for i := 1; i <= n; i++ {
        p := ps[i]
        p.x = p.x + p.vx
        p.y = p.y + p.vy
        p.z = p.z + p.vz
    }
}
for i := 1; i <= 15; i++ { step(particles, 100) }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("step", nil, 100)
	}
}

func BenchmarkGScriptVMTableFieldWarm(b *testing.B) {
	vm := gs.New(gs.WithVM())
	vm.Exec(`
particles := {}
for i := 1; i <= 100; i++ {
    particles[i] = {x: 1.0 * i, y: 2.0 * i, z: 3.0 * i, vx: 0.01, vy: 0.02, vz: 0.03}
}
func step(ps, n) {
    for i := 1; i <= n; i++ {
        p := ps[i]
        p.x = p.x + p.vx
        p.y = p.y + p.vy
        p.z = p.z + p.vz
    }
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("step", nil, 100)
	}
}

// --- Object Creation ---

func BenchmarkGScriptJITObjectCreateWarm(b *testing.B) {
	vm := gs.New(gs.WithJIT())
	vm.Exec(`
func create_objects(n) {
    total := 0.0
    for i := 1; i <= n; i++ {
        obj := {x: 1.0 * i, y: 2.0 * i, z: 3.0 * i}
        total = total + obj.x + obj.y + obj.z
    }
    return total
}
for i := 1; i <= 15; i++ { create_objects(100) }
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("create_objects", 1000)
	}
}

func BenchmarkGScriptVMObjectCreateWarm(b *testing.B) {
	vm := gs.New(gs.WithVM())
	vm.Exec(`
func create_objects(n) {
    total := 0.0
    for i := 1; i <= n; i++ {
        obj := {x: 1.0 * i, y: 2.0 * i, z: 3.0 * i}
        total = total + obj.x + obj.y + obj.z
    }
    return total
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Call("create_objects", 1000)
	}
}
