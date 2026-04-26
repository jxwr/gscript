//go:build darwin && arm64

package methodjit

import "runtime"

var tier2StackReserveSink byte

// ensureTier2NativeStack grows the current goroutine stack before entering
// Tier 2 code. JIT frames are invisible to Go's stack-split checks, so native
// recursive calls need a budget reserved from Go code first.
//
//go:noinline
func ensureTier2NativeStack() {
	var pad [128 << 10]byte
	for i := 0; i < len(pad); i += 4096 {
		pad[i] = byte(i)
		tier2StackReserveSink ^= pad[i]
	}
	runtime.KeepAlive(&pad)
}
