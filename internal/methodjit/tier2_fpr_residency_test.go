//go:build darwin && arm64

// tier2_fpr_residency_test.go is a READ-ONLY diagnostic harness that reports
// FPR (floating-point register) residency for the five float-heavy benchmarks
// tracked in the Tier 2 Float Loops initiative (opt/initiatives/tier2-float-loops.md).
//
// For each benchmark the test walks the proto tree, compiles every function
// through the Tier 2 pipeline up to register allocation, and reports:
//   1. Per-phi type + whether regalloc placed it in an FPR,
//   2. Per-loop-body-block safeHeaderFPRegs entries (mirrors emit_loop.go's
//      computeSafeHeaderFPRegs logic without touching production code),
//   3. Peak concurrent FPR live-range count across all blocks.
//
// The test performs NO assertions (diagnostic only); all data is emitted via
// t.Logf so we can eyeball it to decide whether Task 2/3 in the plan (freeing
// FPR slots / broadening float specialization) is actually needed.
//
// Run with:
//   go test ./internal/methodjit/ -run TestFPRResidencyReport -v

package methodjit

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestFPRResidencyReport compiles five float-heavy benchmarks through the
// Tier 2 pipeline and logs FPR allocation statistics for every proto.
func TestFPRResidencyReport(t *testing.T) {
	benches := []string{
		"mandelbrot.gs",
		"spectral_norm.gs",
		"math_intensive.gs",
		"nbody.gs",
		"matmul.gs",
	}

	for _, bench := range benches {
		bench := bench
		t.Run(bench, func(t *testing.T) {
			reportBenchmarkFPR(t, bench)
		})
	}
}

// reportBenchmarkFPR loads one benchmark, walks its proto tree, runs each
// proto through the Tier 2 pipeline, and logs FPR residency statistics.
// On any failure for a single proto we log the error and continue; on file
// read / compile failure we skip the benchmark entirely (via t.Fatalf on the
// subtest, so the outer TestFPRResidencyReport keeps running).
func reportBenchmarkFPR(t *testing.T, benchFile string) {
	t.Helper()

	srcBytes, err := os.ReadFile("../../benchmarks/suite/" + benchFile)
	if err != nil {
		t.Fatalf("read %s: %v", benchFile, err)
	}

	top := compileTop(t, string(srcBytes))

	// Walk all protos depth-first and analyze every function.
	type protoInfo struct {
		proto *vm.FuncProto
		depth int
	}
	var protos []protoInfo
	var walk func(p *vm.FuncProto, depth int)
	walk = func(p *vm.FuncProto, depth int) {
		if p == nil {
			return
		}
		protos = append(protos, protoInfo{p, depth})
		for _, sub := range p.Protos {
			walk(sub, depth+1)
		}
	}
	walk(top, 0)

	t.Logf("=== %s: %d protos ===", benchFile, len(protos))

	for _, pi := range protos {
		analyzeProtoFPR(t, benchFile, pi.proto, pi.depth)
	}
}

// analyzeProtoFPR runs BuildGraph + passes + RegAlloc on one proto and logs
// the FPR residency report. Errors are logged (not failed) so one bad proto
// doesn't block the rest.
func analyzeProtoFPR(t *testing.T, benchFile string, proto *vm.FuncProto, depth int) {
	t.Helper()

	name := proto.Name
	if name == "" {
		name = "<main>"
	}
	indent := strings.Repeat("  ", depth)

	// Recover from any panic in the pipeline (keeps us reporting other protos).
	defer func() {
		if r := recover(); r != nil {
			t.Logf("%s- %s PANIC: %v", indent, name, r)
		}
	}()

	fn := BuildGraph(proto)
	if fn == nil || fn.Entry == nil {
		t.Logf("%s- %s: BuildGraph returned empty graph, skip", indent, name)
		return
	}
	if fn.Unpromotable {
		t.Logf("%s- %s: Unpromotable, skip", indent, name)
		return
	}

	var pipeErr error
	if fn, pipeErr = TypeSpecializePass(fn); pipeErr != nil {
		t.Logf("%s- %s: TypeSpecializePass error: %v", indent, name, pipeErr)
		return
	}
	if fn, pipeErr = ConstPropPass(fn); pipeErr != nil {
		t.Logf("%s- %s: ConstPropPass error: %v", indent, name, pipeErr)
		return
	}
	if fn, pipeErr = DCEPass(fn); pipeErr != nil {
		t.Logf("%s- %s: DCEPass error: %v", indent, name, pipeErr)
		return
	}
	if fn, pipeErr = RangeAnalysisPass(fn); pipeErr != nil {
		t.Logf("%s- %s: RangeAnalysisPass error: %v", indent, name, pipeErr)
		return
	}

	alloc := AllocateRegisters(fn)

	// Collect phis.
	type phiRow struct {
		id       int
		blockID  int
		typ      Type
		hasReg   bool
		reg      int
		isFloat  bool
		spilled  bool
	}
	var phis []phiRow
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if instr.Op != OpPhi {
				break
			}
			row := phiRow{
				id:      instr.ID,
				blockID: b.ID,
				typ:     instr.Type,
			}
			if pr, ok := alloc.ValueRegs[instr.ID]; ok {
				row.hasReg = true
				row.reg = pr.Reg
				row.isFloat = pr.IsFloat
			}
			if _, ok := alloc.SpillSlots[instr.ID]; ok {
				row.spilled = true
			}
			phis = append(phis, row)
		}
	}

	// Count stats: total phis, float-typed phis, FPR-assigned phis.
	totalPhis := len(phis)
	floatPhis := 0
	fprPhis := 0
	spilledPhis := 0
	for _, p := range phis {
		if p.typ == TypeFloat {
			floatPhis++
		}
		if p.hasReg && p.isFloat {
			fprPhis++
		}
		if p.spilled {
			spilledPhis++
		}
	}

	// Loop analysis + safe header FPR regs.
	li := computeLoopInfo(fn)
	headerFPRegs := li.computeHeaderExitFPRegs(fn, alloc)
	safeFP := computeSafeHeaderFPRegs(fn, li, alloc, headerFPRegs)

	// Peak concurrent float live-range count across all blocks.
	peakFP := computePeakFPRLiveRanges(fn, alloc)

	// Header.
	t.Logf("%s- %s (numParams=%d maxStack=%d): %d blocks, %d phis (float=%d, FPR=%d, spilled=%d), peakFPRLive=%d",
		indent, name, proto.NumParams, proto.MaxStack,
		len(fn.Blocks), totalPhis, floatPhis, fprPhis, spilledPhis, peakFP)

	if totalPhis == 0 && len(li.loopHeaders) == 0 {
		return // nothing interesting; skip detail
	}

	// Per-phi table.
	if totalPhis > 0 {
		t.Logf("%s  phi table:", indent)
		for _, p := range phis {
			regStr := "none"
			if p.hasReg {
				if p.isFloat {
					regStr = fmt.Sprintf("D%d", p.reg)
				} else {
					regStr = fmt.Sprintf("X%d", p.reg)
				}
			}
			if p.spilled {
				regStr += " (spilled)"
			}
			t.Logf("%s    v%d blk=%d type=%s reg=%s", indent, p.id, p.blockID, p.typ, regStr)
		}
	}

	// Per-loop-body-block safeHeaderFPRegs summary.
	if len(li.loopHeaders) > 0 {
		// Deterministic header order.
		headerIDs := make([]int, 0, len(li.loopHeaders))
		for hid := range li.loopHeaders {
			headerIDs = append(headerIDs, hid)
		}
		sort.Ints(headerIDs)

		t.Logf("%s  loop headers: %d", indent, len(headerIDs))
		for _, hid := range headerIDs {
			bodyBlocks := li.headerBlocks[hid]
			bodyIDs := make([]int, 0, len(bodyBlocks))
			for bid := range bodyBlocks {
				bodyIDs = append(bodyIDs, bid)
			}
			sort.Ints(bodyIDs)

			safeRegs := safeFP[hid]
			safeList := make([]string, 0, len(safeRegs))
			// Deterministic order by FPR number.
			regNums := make([]int, 0, len(safeRegs))
			for r := range safeRegs {
				regNums = append(regNums, r)
			}
			sort.Ints(regNums)
			for _, r := range regNums {
				entry := safeRegs[r]
				safeList = append(safeList, fmt.Sprintf("D%d=v%d", r, entry.ValueID))
			}
			safeStr := "<empty>"
			if len(safeList) > 0 {
				safeStr = strings.Join(safeList, ",")
			}

			t.Logf("%s    header blk=%d body={%s} safeHeaderFPRegs(%d): %s",
				indent, hid,
				joinInts(bodyIDs), len(safeRegs), safeStr)

			// Per-body-block detail: show which FPR-allocated values each
			// non-header body block defines (this is what clobbers the
			// header's live FPRs).
			for _, bid := range bodyIDs {
				if bid == hid {
					continue
				}
				var defs []string
				for _, b := range fn.Blocks {
					if b.ID != bid {
						continue
					}
					for _, instr := range b.Instrs {
						if instr.Op == OpPhi || instr.Op.IsTerminator() {
							continue
						}
						if pr, ok := alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
							defs = append(defs, fmt.Sprintf("D%d<-v%d", pr.Reg, instr.ID))
						}
					}
				}
				if len(defs) > 0 {
					t.Logf("%s      body blk=%d FPR defs: %s", indent, bid, strings.Join(defs, ","))
				}
			}
		}
	}
}

// computePeakFPRLiveRanges returns the maximum number of distinct float SSA
// values that are simultaneously live at any point in the function. We use a
// simple per-block linear-scan: open an interval when a value is defined in
// an FPR, close it at its last use within the block (or at block exit if it
// escapes). This is an approximation that matches regalloc's per-block
// model — values rarely cross blocks in FPRs because regalloc resets per
// block. Inputs:
//   - fn: optimized IR
//   - alloc: regalloc result (only FPR-assigned values count)
func computePeakFPRLiveRanges(fn *Function, alloc *RegAllocation) int {
	// Identify FPR-assigned values.
	isFPR := make(map[int]bool)
	for id, pr := range alloc.ValueRegs {
		if pr.IsFloat {
			isFPR[id] = true
		}
	}
	if len(isFPR) == 0 {
		return 0
	}

	// Compute, per block, the instruction index where each FPR value
	// is last referenced (as arg) within that block; if the value is
	// defined in this block and never used again inside, it dies at
	// its definition. Cross-block liveness is handled conservatively
	// by counting the value as live through the block's end in blocks
	// where it is used but not defined.
	peak := 0
	for _, b := range fn.Blocks {
		// Map valueID -> index of last use within this block.
		lastUseInBlock := make(map[int]int)
		// Map valueID -> index of definition within this block (-1 if not defined here).
		defIdx := make(map[int]int)
		for i, instr := range b.Instrs {
			if isFPR[instr.ID] {
				defIdx[instr.ID] = i
				if _, ok := lastUseInBlock[instr.ID]; !ok {
					lastUseInBlock[instr.ID] = i
				}
			}
			for _, arg := range instr.Args {
				if isFPR[arg.ID] {
					lastUseInBlock[arg.ID] = i
				}
			}
		}
		// Any FPR value used in this block but not defined here is assumed
		// live from block start (index -1) to its last use.
		// Values defined here are live from defIdx to lastUseInBlock.

		// Build events: +1 at birth, -1 just after death.
		type evt struct {
			idx   int
			delta int
		}
		events := make([]evt, 0, 2*len(lastUseInBlock))
		for id, lu := range lastUseInBlock {
			birth := -1
			if d, ok := defIdx[id]; ok {
				birth = d
			}
			// Death is +1 after last use (half-open interval).
			events = append(events, evt{birth, +1})
			events = append(events, evt{lu + 1, -1})
		}
		// Stable sort: process deaths before births at the same index so
		// a value that dies at instr i and a new one born at i can share.
		sort.Slice(events, func(i, j int) bool {
			if events[i].idx != events[j].idx {
				return events[i].idx < events[j].idx
			}
			return events[i].delta < events[j].delta
		})

		live := 0
		for _, e := range events {
			live += e.delta
			if live > peak {
				peak = live
			}
		}
	}
	return peak
}

// joinInts renders an int slice as comma-separated decimal.
func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ",")
}
