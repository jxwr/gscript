//go:build darwin && arm64

// overhead_test.go measures the Go-side overhead of the Method JIT's Execute()
// function vs actual JIT execution time. Separates allocation cost, ExecContext
// setup, callJIT transition cost, and full Execute() overhead. This helps
// identify what to optimize when the Go overhead dominates short functions.

package methodjit

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
)

// =====================================================================
// 1. Full Execute() path: alloc + setup + callJIT + teardown
//    Minimal function: func f() { return 42 }
// =====================================================================

func BenchmarkOverhead_Execute(b *testing.B) {
	cf := compileJIT(b, `func f() { return 42 }`)
	args := []runtime.Value{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

// =====================================================================
// 2. Allocation cost only: make([]runtime.Value, 16)
//    This is the minimum allocation Execute() does per call.
// =====================================================================

func BenchmarkOverhead_AllocOnly(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		regs := make([]runtime.Value, 16)
		_ = regs
	}
}

// =====================================================================
// 3. Allocation + nil fill (what Execute actually does)
// =====================================================================

func BenchmarkOverhead_AllocAndFill(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		regs := make([]runtime.Value, 16)
		for j := range regs {
			regs[j] = runtime.NilValue()
		}
		_ = regs
	}
}

// =====================================================================
// 4. Raw callJIT cost: bypass Execute() entirely.
//    Set up ExecContext manually, pre-allocate regs, call callJIT directly.
//    This measures pure Go->JIT->Go transition cost + JIT execution.
// =====================================================================

func BenchmarkOverhead_CallJITRaw(b *testing.B) {
	cf := compileJIT(b, `func f() { return 42 }`)

	// Pre-allocate register file (reused across all iterations).
	regs := make([]runtime.Value, 16)
	for j := range regs {
		regs[j] = runtime.NilValue()
	}

	var ctx ExecContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(&ctx))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx.ExitCode = 0
		jit.CallJIT(codePtr, ctxPtr)
	}
}

// =====================================================================
// 5. VM call for same function (comparison baseline)
// =====================================================================

func BenchmarkOverhead_VMCall(b *testing.B) {
	benchVM(b, `func f() { return 42 }`, nil)
}

// =====================================================================
// 6. Execute with args: func f(a, b) { return a + b }
//    Measures overhead when there are arguments to copy.
// =====================================================================

func BenchmarkOverhead_ExecuteWithArgs(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { return a + b }`)
	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkOverhead_CallJITRawWithArgs(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { return a + b }`)

	regs := make([]runtime.Value, 16)
	for j := range regs {
		regs[j] = runtime.NilValue()
	}

	var ctx ExecContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(&ctx))

	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate arg loading (but no allocation)
		regs[0] = args[0]
		regs[1] = args[1]
		ctx.ExitCode = 0
		jit.CallJIT(codePtr, ctxPtr)
	}
}

// =====================================================================
// 7. Sum(10000) — full Execute vs raw callJIT
//    Measures what fraction of sum(10000) is overhead vs computation.
// =====================================================================

func BenchmarkOverhead_Sum10000_Execute(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	args := []runtime.Value{runtime.IntValue(10000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkOverhead_Sum10000_CallJITRaw(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)

	nregs := cf.numRegs
	if nregs < 16 {
		nregs = 16
	}
	regs := make([]runtime.Value, nregs)
	for j := range regs {
		regs[j] = runtime.NilValue()
	}

	var ctx ExecContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(&ctx))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Load argument: n = 10000
		regs[0] = runtime.IntValue(10000)
		// Clear loop variables to avoid stale state
		for j := 1; j < len(regs); j++ {
			regs[j] = runtime.NilValue()
		}
		ctx.ExitCode = 0
		jit.CallJIT(codePtr, ctxPtr)
	}
}

func BenchmarkOverhead_Sum10000_VM(b *testing.B) {
	benchVM(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10000))
}

// =====================================================================
// 8. Sum(100) — shorter loop, overhead is bigger fraction
// =====================================================================

func BenchmarkOverhead_Sum100_Execute(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	args := []runtime.Value{runtime.IntValue(100)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkOverhead_Sum100_CallJITRaw(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)

	nregs := cf.numRegs
	if nregs < 16 {
		nregs = 16
	}
	regs := make([]runtime.Value, nregs)
	for j := range regs {
		regs[j] = runtime.NilValue()
	}

	var ctx ExecContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(&ctx))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		regs[0] = runtime.IntValue(100)
		for j := 1; j < len(regs); j++ {
			regs[j] = runtime.NilValue()
		}
		ctx.ExitCode = 0
		jit.CallJIT(codePtr, ctxPtr)
	}
}

func BenchmarkOverhead_Sum100_VM(b *testing.B) {
	benchVM(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(100))
}

// =====================================================================
// 9. ExecContext setup cost (no JIT call, no allocation)
//    Measures the cost of filling the ExecContext struct fields.
// =====================================================================

func BenchmarkOverhead_CtxSetup(b *testing.B) {
	cf := compileJIT(b, `func f() { return 42 }`)
	regs := make([]runtime.Value, 16)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var ctx ExecContext
		ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
		if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
			ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
		}
		_ = ctx
	}
}

// =====================================================================
// 10. Result extraction cost: []runtime.Value{regs[0]}
//     Measures the return slice allocation from Execute().
// =====================================================================

func BenchmarkOverhead_ResultAlloc(b *testing.B) {
	var v runtime.Value = runtime.IntValue(42)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := []runtime.Value{v}
		_ = result
	}
}
