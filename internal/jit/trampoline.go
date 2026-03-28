//go:build darwin && arm64

package jit

// callJIT calls a JIT-compiled function directly via assembly trampoline.
// This is faster than purego for repeated calls since it avoids CGO-like overhead.
//
//go:noescape
func callJIT(fn uintptr, ctx uintptr) int64

// CallJIT is the exported version of callJIT for use by the method JIT.
func CallJIT(fn uintptr, ctx uintptr) int64 {
	return callJIT(fn, ctx)
}
