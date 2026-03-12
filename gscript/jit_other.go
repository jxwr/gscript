//go:build !(darwin && arm64)

package gscript

import bytecodevm "github.com/gscript/gscript/internal/vm"

func enableJIT(_ *bytecodevm.VM) {
	// JIT not available on this platform.
}
