//go:build darwin && arm64

package main

import (
	"github.com/gscript/gscript/internal/methodjit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(bvm *bytecodevm.VM) {
	// TieringManager: Tier 1 (baseline) + Tier 2 (optimizing) with threshold-based
	// promotion. With default threshold (100), functions must be called 100+ times
	// through the VM path to promote. Tier 1 BLR calls bypass the VM, so most
	// functions stay at Tier 1 until counter integration is added to Tier 1 code.
	tm := methodjit.NewTieringManager()
	bvm.SetMethodJIT(tm)
}
