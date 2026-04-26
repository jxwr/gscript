//go:build darwin && arm64

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/gscript/gscript/internal/methodjit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(bvm *bytecodevm.VM, opts jitCLIOptions) (jitStatsReporter, error) {
	// TieringManager: Tier 1 (baseline) + Tier 2 (optimizing) with threshold-based
	// promotion. With default threshold (100), functions must be called 100+ times
	// through the VM path to promote. Tier 1 BLR calls bypass the VM, so most
	// functions stay at Tier 1 until counter integration is added to Tier 1 code.
	tm := methodjit.NewTieringManager()
	reporter := &tieringManagerReporter{tm: tm}
	if opts.TimelinePath != "" {
		timeline, closer, err := openJITTimeline(opts)
		if err != nil {
			return nil, err
		}
		tm.SetTimeline(timeline)
		reporter.timeline = timeline
		reporter.timelineCloser = closer
	}
	bvm.SetMethodJIT(tm)
	return reporter, nil
}

// tieringManagerReporter adapts *methodjit.TieringManager to the jitStatsReporter
// interface used by the CLI's -jit-stats flag.
type tieringManagerReporter struct {
	tm             *methodjit.TieringManager
	timeline       *methodjit.JITTimeline
	timelineCloser io.Closer
}

func (r *tieringManagerReporter) PrintStats(w *os.File) {
	if r == nil || r.tm == nil {
		return
	}
	compiled := r.tm.Tier2Compiled()
	entered := r.tm.Tier2Entered()
	enteredSet := make(map[string]bool, len(entered))
	for _, n := range entered {
		enteredSet[n] = true
	}
	failed := r.tm.Tier2Failed()
	fmt.Fprintln(w, "JIT Statistics:")
	fmt.Fprintf(w, "  Tier 1 compiled: %d functions\n", r.tm.Tier1Count())
	fmt.Fprintf(w, "  Tier 2 attempted: %d\n", r.tm.Tier2Attempted())
	fmt.Fprintf(w, "  Tier 2 compiled: %d functions\n", len(compiled))
	fmt.Fprintf(w, "  Tier 2 entered:  %d functions\n", len(entered))
	for _, name := range compiled {
		display := name
		if display == "" {
			display = "<anonymous>"
		}
		mark := "no"
		if enteredSet[name] {
			mark = "yes"
		}
		fmt.Fprintf(w, "    - %s (entered=%s)\n", display, mark)
	}
	fmt.Fprintf(w, "  Tier 2 failed: %d functions\n", len(failed))
	// Sort failed keys for stable output.
	names := make([]string, 0, len(failed))
	for name := range failed {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		display := name
		if display == "" {
			display = "<anonymous>"
		}
		fmt.Fprintf(w, "    - %s: %s\n", display, failed[name])
	}
}

func (r *tieringManagerReporter) Close() error {
	if r == nil {
		return nil
	}
	var err error
	if r.timeline != nil {
		err = r.timeline.Flush()
	}
	if r.timelineCloser != nil {
		if closeErr := r.timelineCloser.Close(); err == nil {
			err = closeErr
		}
		r.timelineCloser = nil
	}
	return err
}

func openJITTimeline(opts jitCLIOptions) (*methodjit.JITTimeline, io.Closer, error) {
	var w io.Writer
	var closer io.Closer
	if opts.TimelinePath == "-" {
		w = os.Stderr
	} else {
		f, err := os.Create(opts.TimelinePath)
		if err != nil {
			return nil, nil, fmt.Errorf("create JIT timeline: %w", err)
		}
		w = f
		closer = f
	}
	timeline, err := methodjit.NewJITTimeline(w, opts.TimelineFormat)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, nil, err
	}
	return timeline, closer, nil
}
