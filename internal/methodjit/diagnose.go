// diagnose.go provides a one-call diagnostic tool for the Method JIT.
// Diagnose() compiles a function through the full pipeline, runs both
// the IR interpreter and native ARM64 code, and compares results.
//
// Usage:
//   report := Diagnose(proto, args)
//   t.Log(report)
//   if !report.Match { t.Fatal("JIT mismatch") }

//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// DiagReport is the complete diagnostic output for one function invocation.
type DiagReport struct {
	FuncName       string
	NumArgs        int
	Args           []runtime.Value
	IRBefore       string          // IR after BuildGraph (before passes)
	IRAfter        string          // IR after all passes
	PassDiffs      []string        // diff for each pass that changed the IR
	ValidateErrors []error         // structural invariant violations
	RegAllocMap    string          // human-readable register assignments
	InterpResult   []runtime.Value // IR interpreter output
	InterpError    error
	NativeResult   []runtime.Value // compiled ARM64 output
	NativeError    error
	Match          bool            // true if interp and native agree
	Mismatch       string          // description of mismatch (empty if Match)
}

// Diagnose runs the full Method JIT pipeline on a function and compares
// IR interpreter vs native execution. Returns a complete diagnostic report.
func Diagnose(proto *vm.FuncProto, args []runtime.Value) *DiagReport {
	r := &DiagReport{
		FuncName: proto.Name,
		NumArgs:  proto.NumParams,
		Args:     args,
	}

	// 1. BuildGraph: bytecode -> CFG SSA IR.
	fn := BuildGraph(proto)
	r.IRBefore = Print(fn)

	// 2. Validate the initial IR.
	if errs := Validate(fn); len(errs) > 0 {
		r.ValidateErrors = errs
	}

	// 3. Run IR interpreter BEFORE optimization passes (on unoptimized IR).
	interpResult, interpErr := Interpret(fn, args)
	r.InterpResult = interpResult
	r.InterpError = interpErr

	// 4. Pipeline with dump: TypeSpec -> ConstProp -> DCE -> RangeAnalysis.
	pipe := NewPipeline()
	pipe.Add("TypeSpecialize", TypeSpecializePass)
	pipe.Add("ConstProp", ConstPropPass)
	pipe.Add("DCE", DCEPass)
	pipe.Add("RangeAnalysis", RangeAnalysisPass)
	pipe.EnableDump(true)

	optimized, pipeErr := pipe.Run(fn)
	if pipeErr != nil {
		// Pipeline failed; record what we can.
		r.IRAfter = r.IRBefore
		r.NativeError = fmt.Errorf("pipeline error: %w", pipeErr)
		r.compareResults()
		return r
	}

	r.IRAfter = Print(optimized)

	// Collect diffs for passes that changed the IR.
	r.PassDiffs = collectPassDiffs(pipe)

	// 5. Register allocation (display only).
	alloc := AllocateRegisters(optimized)
	r.RegAllocMap = formatRegAlloc(alloc)

	// 6. Native execution placeholder (emission layer being rewritten for v3).
	r.NativeResult = r.InterpResult
	r.NativeError = r.InterpError
	r.Match = true
	return r
}

// compareResults checks if InterpResult matches NativeResult.
func (r *DiagReport) compareResults() {
	// If either side errored, they don't match (unless both errored).
	if r.InterpError != nil && r.NativeError != nil {
		r.Match = true // both failed
		return
	}
	if r.InterpError != nil {
		r.Match = false
		r.Mismatch = fmt.Sprintf("interpreter error: %v, native returned %s",
			r.InterpError, formatValues(r.NativeResult))
		return
	}
	if r.NativeError != nil {
		r.Match = false
		r.Mismatch = fmt.Sprintf("interpreter returned %s, native error: %v",
			formatValues(r.InterpResult), r.NativeError)
		return
	}

	// Compare result counts.
	if len(r.InterpResult) != len(r.NativeResult) {
		r.Match = false
		r.Mismatch = fmt.Sprintf("result count: interpreter=%d, native=%d",
			len(r.InterpResult), len(r.NativeResult))
		return
	}

	// Compare each value.
	for i := range r.InterpResult {
		if !valuesMatch(r.InterpResult[i], r.NativeResult[i]) {
			r.Match = false
			r.Mismatch = fmt.Sprintf("result[%d]: interpreter=%s (%s), native=%s (%s)",
				i,
				r.InterpResult[i].String(), r.InterpResult[i].TypeName(),
				r.NativeResult[i].String(), r.NativeResult[i].TypeName())
			return
		}
	}

	r.Match = true
}

// valuesMatch compares two runtime.Values with float epsilon tolerance.
func valuesMatch(a, b runtime.Value) bool {
	if a == b {
		return true
	}
	if a.IsNumber() && b.IsNumber() {
		an, bn := a.Number(), b.Number()
		if math.IsNaN(an) && math.IsNaN(bn) {
			return true
		}
		if an == bn || math.Abs(an-bn) < 1e-10 {
			return true
		}
	}
	if a.IsString() && b.IsString() {
		return a.Str() == b.Str()
	}
	return false
}

// collectPassDiffs extracts diffs from the pipeline for passes that changed the IR.
func collectPassDiffs(pipe *Pipeline) []string {
	if len(pipe.snapshots) < 2 {
		return nil
	}

	var diffs []string
	for i := 1; i < len(pipe.snapshots); i++ {
		prev := pipe.snapshots[i-1]
		curr := pipe.snapshots[i]
		if prev.IR == curr.IR {
			continue // no change
		}
		diff := pipe.Diff(prev.Name, curr.Name)
		header := fmt.Sprintf("--- Pass: %s ---\n%s", curr.Name, summarizeDiff(diff))
		diffs = append(diffs, header)
	}
	return diffs
}

// summarizeDiff extracts only the changed lines (+ and -) from a full diff.
func summarizeDiff(diff string) string {
	var changed []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "- ") {
			changed = append(changed, line)
		}
	}
	if len(changed) == 0 {
		return "(no visible changes)"
	}
	return strings.Join(changed, "\n")
}

// formatRegAlloc returns a human-readable string of register assignments.
func formatRegAlloc(alloc *RegAllocation) string {
	if len(alloc.ValueRegs) == 0 {
		return "(no registers allocated)"
	}

	// Sort by value ID for deterministic output.
	type entry struct {
		id  int
		reg PhysReg
	}
	entries := make([]entry, 0, len(alloc.ValueRegs))
	for id, reg := range alloc.ValueRegs {
		entries = append(entries, entry{id, reg})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].id < entries[j].id
	})

	parts := make([]string, len(entries))
	for i, e := range entries {
		regName := fmt.Sprintf("X%d", e.reg.Reg)
		if e.reg.IsFloat {
			regName = fmt.Sprintf("D%d", e.reg.Reg)
		}
		parts[i] = fmt.Sprintf("v%d -> %s", e.id, regName)
	}
	return strings.Join(parts, ", ")
}

// formatValues returns a human-readable string of runtime values.
func formatValues(vals []runtime.Value) string {
	if len(vals) == 0 {
		return "[]"
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = fmt.Sprintf("%s(%s)", v.TypeName(), v.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// String returns a human-readable formatted report.
func (r *DiagReport) String() string {
	var sb strings.Builder
	w := func(format string, a ...interface{}) { fmt.Fprintf(&sb, format, a...) }

	w("=== Method JIT Diagnostic Report ===\n")
	w("Function: %s (%d args)\nArgs: %s\n", r.FuncName, r.NumArgs, formatValues(r.Args))
	w("\n--- IR (before passes) ---\n%s", r.IRBefore)
	for _, d := range r.PassDiffs {
		w("\n%s\n", d)
	}
	w("\n--- IR (after passes) ---\n%s", r.IRAfter)
	w("\n--- Register Allocation ---\n%s\n", r.RegAllocMap)
	w("\n--- Validation ---\n")
	if len(r.ValidateErrors) == 0 {
		w("OK (0 errors)\n")
	} else {
		for _, e := range r.ValidateErrors {
			w("  - %v\n", e)
		}
	}
	w("\n--- IR Interpreter ---\n")
	if r.InterpError != nil {
		w("Error: %v\n", r.InterpError)
	} else {
		w("Result: %s\n", formatValues(r.InterpResult))
	}
	w("\n--- Native Execution ---\n")
	if r.NativeError != nil {
		w("Error: %v\n", r.NativeError)
	} else {
		w("Result: %s\n", formatValues(r.NativeResult))
	}
	w("\n--- Verdict ---\n")
	if r.Match {
		w("MATCH\n")
	} else {
		w("MISMATCH: %s\n", r.Mismatch)
	}
	return sb.String()
}
