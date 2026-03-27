//go:build darwin && arm64

package jit

import (
	"fmt"
	"strings"

	"github.com/gscript/gscript/internal/runtime"
)

// ════════════════════════════════════════════════════════════════════════════
// Pipeline Dump — LLVM-style per-pass SSA dump
//
// Usage:
//   ct, dump := CompileWithDump(trace)
//   t.Log(dump.String())            // full pipeline view
//   t.Log(dump.Diff("BuildSSA", "OptimizeSSA"))  // see what a pass changed
// ════════════════════════════════════════════════════════════════════════════

// PipelineStage records SSA state at one pipeline stage.
type PipelineStage struct {
	Name     string
	SSA      string // SSA IR as string (empty for non-SSA stages)
	RegAlloc string // register allocation dump (only after RegAlloc)
	CodeSize int    // generated code size in bytes (only after Emit)
	Error    error  // compilation error if any
}

// PipelineDump records the full compilation pipeline.
type PipelineDump struct {
	Stages []PipelineStage
}

// SSAToString converts SSAFunc to a compact string representation.
func SSAToString(f *SSAFunc) string {
	if f == nil {
		return "(nil)"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "LoopIdx=%d, %d instructions\n", f.LoopIdx, len(f.Insts))
	for i, inst := range f.Insts {
		marker := ""
		if i == f.LoopIdx {
			marker = "  <-- LOOP"
		}
		fmt.Fprintf(&sb, "  %3d: %s%s\n", i, inst.String(), marker)
	}
	if len(f.Snapshots) > 0 {
		fmt.Fprintf(&sb, "Snapshots: %d\n", len(f.Snapshots))
	}
	return sb.String()
}

// RegMapToString converts RegMap to a compact string.
func RegMapToString(rm *RegMap) string {
	if rm == nil {
		return "(nil)"
	}
	var sb strings.Builder
	if rm.Int != nil && len(rm.Int.slotToReg) > 0 {
		fmt.Fprintf(&sb, "Int GPR: ")
		for slot, reg := range rm.Int.slotToReg {
			fmt.Fprintf(&sb, "s%d→X%d ", slot, reg)
		}
		sb.WriteByte('\n')
	}
	if rm.Float != nil && len(rm.Float.slotToReg) > 0 {
		fmt.Fprintf(&sb, "Float FPR: ")
		for slot, reg := range rm.Float.slotToReg {
			fmt.Fprintf(&sb, "s%d→D%d ", slot, reg)
		}
		sb.WriteByte('\n')
	}
	if rm.FloatRef != nil && len(rm.FloatRef.refToReg) > 0 {
		fmt.Fprintf(&sb, "Float Ref: ")
		for ref, reg := range rm.FloatRef.refToReg {
			fmt.Fprintf(&sb, "r%d→D%d ", ref, reg)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// RegsToString formats register values as a compact string with raw hex.
func RegsToString(regs []runtime.Value, slots []int) string {
	var sb strings.Builder
	if len(slots) > 0 {
		for _, s := range slots {
			if s < len(regs) {
				fmt.Fprintf(&sb, "  [%d]=0x%016x %s\n", s, regs[s].Raw(), formatValue(&regs[s]))
			}
		}
	} else {
		for i := range regs {
			if regs[i].Raw() != 0 {
				fmt.Fprintf(&sb, "  [%d]=0x%016x %s\n", i, regs[i].Raw(), formatValue(&regs[i]))
			}
		}
	}
	return sb.String()
}

// CompileWithDump runs the full compilation pipeline, recording SSA at each stage.
// Pipeline: BuildSSA → OptimizeSSA → ConstHoist → CSE → FMA → RegAlloc → Emit
func CompileWithDump(trace *Trace) (*CompiledTrace, *PipelineDump) {
	dump := &PipelineDump{}

	// Stage 1: BuildSSA
	f := BuildSSA(trace)
	dump.Stages = append(dump.Stages, PipelineStage{
		Name: "BuildSSA",
		SSA:  SSAToString(f),
	})

	// Stage 2: OptimizeSSA (while-loop exit detection)
	f = OptimizeSSA(f)
	dump.Stages = append(dump.Stages, PipelineStage{
		Name: "OptimizeSSA",
		SSA:  SSAToString(f),
	})

	// Stage 3: ConstHoist
	f = ConstHoist(f)
	dump.Stages = append(dump.Stages, PipelineStage{
		Name: "ConstHoist",
		SSA:  SSAToString(f),
	})

	// Stage 4: CSE
	f = CSE(f)
	dump.Stages = append(dump.Stages, PipelineStage{
		Name: "CSE",
		SSA:  SSAToString(f),
	})

	// Stage 5: FMA fusion
	f = FuseMultiplyAdd(f)
	dump.Stages = append(dump.Stages, PipelineStage{
		Name: "FuseMultiplyAdd",
		SSA:  SSAToString(f),
	})

	// Stage 6: RegAlloc + Emit (inside CompileSSA)
	regMap := AllocateRegisters(f)
	dump.Stages = append(dump.Stages, PipelineStage{
		Name:     "RegAlloc",
		SSA:      SSAToString(f),
		RegAlloc: RegMapToString(regMap),
	})

	// Stage 7: Emit — we need to call CompileSSA but it re-does RegAlloc.
	// To avoid double work, call CompileSSA on the optimized f directly.
	ct, err := CompileSSA(f)
	stage := PipelineStage{
		Name:  "Emit",
		Error: err,
	}
	if ct != nil && ct.code != nil {
		stage.CodeSize = ct.code.Size()
		stage.RegAlloc = RegMapToString(ct.regMap)
	}
	dump.Stages = append(dump.Stages, stage)

	return ct, dump
}

// String returns the full pipeline dump as a readable report.
func (d *PipelineDump) String() string {
	var sb strings.Builder
	sb.WriteString("╔══════════════════════════════════════════════════════╗\n")
	sb.WriteString("║              JIT PIPELINE DUMP                      ║\n")
	sb.WriteString("╚══════════════════════════════════════════════════════╝\n\n")

	for i, stage := range d.Stages {
		fmt.Fprintf(&sb, "── Stage %d: %s ", i+1, stage.Name)
		if stage.Error != nil {
			fmt.Fprintf(&sb, "[ERROR: %v] ", stage.Error)
		}
		sb.WriteString("──\n")

		if stage.SSA != "" {
			sb.WriteString(stage.SSA)
		}
		if stage.RegAlloc != "" {
			sb.WriteString(stage.RegAlloc)
		}
		if stage.CodeSize > 0 {
			fmt.Fprintf(&sb, "Code: %d bytes\n", stage.CodeSize)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Stage returns the stage with the given name, or nil.
func (d *PipelineDump) Stage(name string) *PipelineStage {
	for i := range d.Stages {
		if d.Stages[i].Name == name {
			return &d.Stages[i]
		}
	}
	return nil
}

// Diff returns a simple diff between two stages' SSA output.
// Shows lines added (+) and removed (-) between the two stages.
func (d *PipelineDump) Diff(stage1, stage2 string) string {
	s1 := d.Stage(stage1)
	s2 := d.Stage(stage2)
	if s1 == nil || s2 == nil {
		return fmt.Sprintf("(stage not found: %q or %q)", stage1, stage2)
	}
	if s1.SSA == s2.SSA {
		return fmt.Sprintf("%s → %s: (no change)\n", stage1, stage2)
	}

	lines1 := strings.Split(s1.SSA, "\n")
	lines2 := strings.Split(s2.SSA, "\n")

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s → %s:\n", stage1, stage2)

	// Simple line-by-line diff (good enough for SSA IR)
	set1 := make(map[string]int)
	for _, l := range lines1 {
		l = strings.TrimSpace(l)
		if l != "" {
			set1[l]++
		}
	}
	set2 := make(map[string]int)
	for _, l := range lines2 {
		l = strings.TrimSpace(l)
		if l != "" {
			set2[l]++
		}
	}

	for _, l := range lines1 {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if set2[l] < set1[l] {
			fmt.Fprintf(&sb, "  - %s\n", l)
			set2[l]++ // don't report again
		}
	}
	// Reset set1 counts
	set1 = make(map[string]int)
	for _, l := range lines1 {
		l = strings.TrimSpace(l)
		if l != "" {
			set1[l]++
		}
	}
	for _, l := range lines2 {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if set1[l] < set2[l] {
			fmt.Fprintf(&sb, "  + %s\n", l)
			set1[l]++ // don't report again
		}
	}

	return sb.String()
}
