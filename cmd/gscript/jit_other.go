//go:build !(darwin && arm64)

package main

import (
	"fmt"

	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(_ *bytecodevm.VM, opts jitCLIOptions) (jitStatsReporter, error) {
	// JIT not available on this platform.
	if opts.TimelinePath != "" {
		return nil, fmt.Errorf("JIT timeline unavailable on this platform")
	}
	return nil, nil
}
