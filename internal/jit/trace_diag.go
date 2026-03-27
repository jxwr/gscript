//go:build darwin && arm64

package jit

import (
	"fmt"
	"strings"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ════════════════════════════════════════════════════════════════════════════
// TraceDiag — LuaJIT -jdump style one-shot diagnostic
//
// Usage in tests:
//   diag := DiagnoseTrace(trace, regs, proto)
//   t.Log(diag)  // prints the full diagnostic report
//
// Usage from CLI:
//   GS_DUMP=1 gscript run foo.gs   (future: env-var triggered)
// ════════════════════════════════════════════════════════════════════════════

// TraceDiag is a complete diagnostic report for a trace compilation + execution.
type TraceDiag struct {
	// Source
	TraceIRCount int
	LoopPC       int

	// Pipeline
	Pipeline *PipelineDump

	// Execution
	RegsBefore string
	RegsAfter  string
	ExitCode   int // 0=loop-done, 1=side-exit, 2=guard-fail, 3=call-exit, 4=break-exit, 5=max-iter
	ExitPC     int
	Iterations int64

	// Derived
	GuardFail bool
	SideExit  bool
	LoopDone  bool
	CallExit  bool

	// ARM64 disassembly (optional, set ShowASM=true)
	ARM64 string

	// Error
	CompileError error
}

// ExitCodeName returns a human-readable name for the exit code.
func ExitCodeName(code int) string {
	switch code {
	case 0:
		return "loop-done"
	case 1:
		return "side-exit"
	case 2:
		return "guard-fail"
	case 3:
		return "call-exit"
	case 4:
		return "break-exit"
	case 5:
		return "max-iterations"
	default:
		return fmt.Sprintf("unknown(%d)", code)
	}
}

// DiagConfig controls what gets included in the diagnostic.
type DiagConfig struct {
	WatchSlots []int // register slots to display (nil = all non-zero)
	ShowASM    bool  // include ARM64 disassembly
	MaxIter    int   // max iterations (0 = unlimited)
}

// DiagnoseTrace compiles a trace, executes it, and returns a full diagnostic.
// This is the primary debugging entry point — one call gives you everything.
func DiagnoseTrace(trace *Trace, regs []runtime.Value, proto *vm.FuncProto, cfg DiagConfig) *TraceDiag {
	diag := &TraceDiag{
		TraceIRCount: len(trace.IR),
		LoopPC:       trace.LoopPC,
	}

	// Record registers before
	diag.RegsBefore = RegsToString(regs, cfg.WatchSlots)

	// Run pipeline with dump
	ct, dump := CompileWithDump(trace)
	diag.Pipeline = dump

	if ct == nil {
		// Find the error
		for _, s := range dump.Stages {
			if s.Error != nil {
				diag.CompileError = s.Error
				break
			}
		}
		return diag
	}

	// Optional ARM64 disassembly
	if cfg.ShowASM {
		diag.ARM64 = DumpARM64(ct)
	}

	// Execute
	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}
	if ct.innerTrace != nil {
		ctx.InnerCode = uintptr(ct.innerTrace.code.Ptr())
		if len(ct.innerTrace.constants) > 0 {
			ctx.InnerConstants = uintptr(unsafe.Pointer(&ct.innerTrace.constants[0]))
		}
	}
	if cfg.MaxIter > 0 {
		ctx.MaxIterations = int64(cfg.MaxIter)
	}

	callJIT(uintptr(ct.code.Ptr()), uintptr(unsafe.Pointer(&ctx)))

	// Record results
	diag.ExitCode = int(ctx.ExitCode)
	diag.ExitPC = int(ctx.ExitPC)
	diag.Iterations = ctx.IterationCount
	diag.GuardFail = ctx.ExitCode == 2
	diag.SideExit = ctx.ExitCode == 1
	diag.LoopDone = ctx.ExitCode == 0
	diag.CallExit = ctx.ExitCode == 3
	diag.RegsAfter = RegsToString(regs, cfg.WatchSlots)

	return diag
}

// DiagnoseCompiled runs diagnostic on an already-compiled trace.
// Useful when the test builds/compiles manually.
func DiagnoseCompiled(ct *CompiledTrace, regs []runtime.Value, cfg DiagConfig) *TraceDiag {
	diag := &TraceDiag{}
	diag.RegsBefore = RegsToString(regs, cfg.WatchSlots)

	if cfg.ShowASM && ct != nil {
		diag.ARM64 = DumpARM64(ct)
	}

	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}
	if ct.innerTrace != nil {
		ctx.InnerCode = uintptr(ct.innerTrace.code.Ptr())
		if len(ct.innerTrace.constants) > 0 {
			ctx.InnerConstants = uintptr(unsafe.Pointer(&ct.innerTrace.constants[0]))
		}
	}
	if cfg.MaxIter > 0 {
		ctx.MaxIterations = int64(cfg.MaxIter)
	}

	callJIT(uintptr(ct.code.Ptr()), uintptr(unsafe.Pointer(&ctx)))

	diag.ExitCode = int(ctx.ExitCode)
	diag.ExitPC = int(ctx.ExitPC)
	diag.Iterations = ctx.IterationCount
	diag.GuardFail = ctx.ExitCode == 2
	diag.SideExit = ctx.ExitCode == 1
	diag.LoopDone = ctx.ExitCode == 0
	diag.CallExit = ctx.ExitCode == 3
	diag.RegsAfter = RegsToString(regs, cfg.WatchSlots)

	return diag
}

// String returns the complete diagnostic report.
func (d *TraceDiag) String() string {
	var sb strings.Builder

	sb.WriteString("╔══════════════════════════════════════════════════════╗\n")
	sb.WriteString("║              TRACE DIAGNOSTIC REPORT                ║\n")
	sb.WriteString("╚══════════════════════════════════════════════════════╝\n\n")

	// Source info
	fmt.Fprintf(&sb, "Source: %d trace IR instructions, loopPC=%d\n\n", d.TraceIRCount, d.LoopPC)

	// Compile error
	if d.CompileError != nil {
		fmt.Fprintf(&sb, "⚠ COMPILE ERROR: %v\n\n", d.CompileError)
	}

	// Pipeline stages (compact)
	if d.Pipeline != nil {
		sb.WriteString("── Pipeline ──\n")
		for _, stage := range d.Pipeline.Stages {
			status := "ok"
			if stage.Error != nil {
				status = fmt.Sprintf("ERROR: %v", stage.Error)
			}
			detail := ""
			if stage.CodeSize > 0 {
				detail = fmt.Sprintf(" (%d bytes)", stage.CodeSize)
			}
			fmt.Fprintf(&sb, "  %-16s [%s]%s\n", stage.Name, status, detail)
		}
		sb.WriteByte('\n')

		// Show final SSA (after all optimizations, before emit)
		if regAlloc := d.Pipeline.Stage("RegAlloc"); regAlloc != nil {
			sb.WriteString("── Final SSA (after optimizations) ──\n")
			sb.WriteString(regAlloc.SSA)
			sb.WriteByte('\n')
			if regAlloc.RegAlloc != "" {
				sb.WriteString("── Register Allocation ──\n")
				sb.WriteString(regAlloc.RegAlloc)
				sb.WriteByte('\n')
			}
		}

		// Show diffs for passes that changed something
		for i := 1; i < len(d.Pipeline.Stages); i++ {
			prev := &d.Pipeline.Stages[i-1]
			curr := &d.Pipeline.Stages[i]
			if prev.SSA != "" && curr.SSA != "" && prev.SSA != curr.SSA {
				sb.WriteString(d.Pipeline.Diff(prev.Name, curr.Name))
				sb.WriteByte('\n')
			}
		}
	}

	// Execution
	sb.WriteString("── Execution ──\n")
	fmt.Fprintf(&sb, "Exit: %s (code=%d, pc=%d, iterations=%d)\n",
		ExitCodeName(d.ExitCode), d.ExitCode, d.ExitPC, d.Iterations)
	sb.WriteByte('\n')

	sb.WriteString("Registers BEFORE:\n")
	sb.WriteString(d.RegsBefore)
	sb.WriteByte('\n')

	sb.WriteString("Registers AFTER:\n")
	sb.WriteString(d.RegsAfter)

	// ARM64
	if d.ARM64 != "" {
		sb.WriteString("\n── ARM64 ──\n")
		sb.WriteString(d.ARM64)
	}

	return sb.String()
}

// Summary returns a one-line summary of the diagnostic.
func (d *TraceDiag) Summary() string {
	if d.CompileError != nil {
		return fmt.Sprintf("COMPILE ERROR: %v", d.CompileError)
	}
	return fmt.Sprintf("exit=%s pc=%d iter=%d",
		ExitCodeName(d.ExitCode), d.ExitPC, d.Iterations)
}
