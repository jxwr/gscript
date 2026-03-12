//go:build !(darwin && arm64)

package main

import bytecodevm "github.com/gscript/gscript/internal/vm"

func cliEnableJIT(_ *bytecodevm.VM) {
	// JIT not available on this platform.
}
