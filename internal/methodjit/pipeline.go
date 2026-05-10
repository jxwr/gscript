// pipeline.go implements the optimization pass pipeline for the Method JIT.
// A Pipeline is an ordered list of named passes. Each pass transforms a
// Function (CFG SSA IR) and returns the result. Passes can be independently
// enabled/disabled. The pipeline supports dumping IR snapshots between passes
// and running a validator after each pass for debugging.
//
// Usage:
//
//	p := NewPipeline()
//	p.Add("CSE", CSEPass)
//	p.Add("ConstProp", ConstPropPass)
//	p.SetValidator(Validate)  // run after each pass
//	p.EnableDump(true)        // snapshot IR between passes
//	result, err := p.Run(fn)
//	fmt.Println(p.Dump())     // print all snapshots
//	fmt.Println(p.Diff("CSE", "ConstProp"))  // see what changed
package methodjit

import (
	"fmt"
	"strings"
	"time"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// PassFunc is the signature for an optimization pass.
// Takes a Function, returns a (possibly modified) Function and an error.
// Passes MUST NOT modify the input Function in place — return a new one
// or return the same pointer if no changes were made.
type PassFunc func(*Function) (*Function, error)

// Pipeline manages an ordered list of optimization passes.
type Pipeline struct {
	passes    []passEntry
	validator func(*Function) []error // optional: run after each pass
	dump      bool                    // snapshot IR between passes
	snapshots []Snapshot              // recorded snapshots (if dump=true)
	timings   []PipelineStageTiming   // one entry per executed pass
}

// passEntry is one named pass in the pipeline.
type passEntry struct {
	name    string
	fn      PassFunc
	enabled bool
}

// Snapshot records the IR state at one point in the pipeline.
type Snapshot struct {
	Name string // pass name (or "input" for initial state)
	IR   string // Print(fn) output
}

// PipelineStageTiming records one observed pipeline stage or pass.
type PipelineStageTiming struct {
	Name          string `json:"name"`
	DurationNanos int64  `json:"duration_nanos"`
	Error         string `json:"error,omitempty"`
}

func newPipelineStageTiming(name string, duration time.Duration, err error) PipelineStageTiming {
	timing := PipelineStageTiming{
		Name:          name,
		DurationNanos: int64(duration),
	}
	if err != nil {
		timing.Error = err.Error()
	}
	return timing
}

// NewPipeline creates an empty pipeline with no passes.
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Add appends a named pass to the end of the pipeline. Passes are enabled
// by default.
func (p *Pipeline) Add(name string, fn PassFunc) {
	p.passes = append(p.passes, passEntry{
		name:    name,
		fn:      fn,
		enabled: true,
	})
}

// Enable enables a pass by name. No-op if the name is not found.
func (p *Pipeline) Enable(name string) {
	for i := range p.passes {
		if p.passes[i].name == name {
			p.passes[i].enabled = true
			return
		}
	}
}

// Disable disables a pass by name. Disabled passes are skipped during Run.
// No-op if the name is not found.
func (p *Pipeline) Disable(name string) {
	for i := range p.passes {
		if p.passes[i].name == name {
			p.passes[i].enabled = false
			return
		}
	}
}

// SetValidator sets a function that validates the IR after each pass.
// If the validator returns errors, the pipeline stops and returns them.
// Pass nil to remove the validator.
func (p *Pipeline) SetValidator(v func(*Function) []error) {
	p.validator = v
}

// EnableDump enables or disables IR snapshot recording between passes.
// When enabled, Run() captures the IR (via Print) before the first pass
// and after each pass. Use Dump() and Diff() to inspect.
func (p *Pipeline) EnableDump(on bool) {
	p.dump = on
}

// Run executes all enabled passes in order on the given Function.
//
// Steps:
//  1. If dump: snapshot input IR as "input"
//  2. For each enabled pass:
//     a. Call pass function
//     b. If error: return error annotated with pass name
//     c. If dump: snapshot result IR as pass name
//     d. If validator: run validator, if errors: return annotated with pass name
//  3. Return final Function
func (p *Pipeline) Run(fn *Function) (*Function, error) {
	// Reset snapshots from any previous run.
	p.snapshots = nil
	p.timings = nil

	if p.dump {
		p.snapshots = append(p.snapshots, Snapshot{
			Name: "input",
			IR:   Print(fn),
		})
	}

	current := fn
	for _, entry := range p.passes {
		if !entry.enabled {
			continue
		}

		start := time.Now()
		result, err := entry.fn(current)
		p.timings = append(p.timings, newPipelineStageTiming(entry.name, time.Since(start), err))
		if err != nil {
			return nil, fmt.Errorf("pass %q: %w", entry.name, err)
		}
		if result != nil && result.Remarks == nil {
			result.Remarks = current.Remarks
		}

		current = result

		if p.dump {
			p.snapshots = append(p.snapshots, Snapshot{
				Name: entry.name,
				IR:   Print(current),
			})
		}

		if p.validator != nil {
			errs := p.validator(current)
			if len(errs) > 0 {
				msgs := make([]string, len(errs))
				for i, e := range errs {
					msgs[i] = e.Error()
				}
				return nil, fmt.Errorf("validation after pass %q: %s",
					entry.name, strings.Join(msgs, "; "))
			}
		}
	}

	return current, nil
}

// StageTimings returns a defensive copy of timings recorded by the last Run.
func (p *Pipeline) StageTimings() []PipelineStageTiming {
	if len(p.timings) == 0 {
		return nil
	}
	out := make([]PipelineStageTiming, len(p.timings))
	copy(out, p.timings)
	return out
}

// FormatPipelineStageTimings returns a compact, human-readable timing summary.
func FormatPipelineStageTimings(stages []PipelineStageTiming) string {
	if len(stages) == 0 {
		return "(not recorded)\n"
	}
	var total time.Duration
	for _, stage := range stages {
		total += time.Duration(stage.DurationNanos)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "total: %s (%d stages)\n", total, len(stages))
	for _, stage := range stages {
		fmt.Fprintf(&sb, "  %-32s %s", stage.Name, time.Duration(stage.DurationNanos))
		if stage.Error != "" {
			fmt.Fprintf(&sb, " error=%q", stage.Error)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Dump returns all recorded snapshots as a formatted string.
// Each snapshot is separated by a header line: "=== <name> ===".
// Returns an empty string if dumping was not enabled.
func (p *Pipeline) Dump() string {
	if len(p.snapshots) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, snap := range p.snapshots {
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "=== %s ===\n", snap.Name)
		sb.WriteString(snap.IR)
	}
	return sb.String()
}

// Diff returns a simple line-level diff between two named snapshots.
// Lines present only in snapshot a are prefixed with "- ".
// Lines present only in snapshot b are prefixed with "+ ".
// Common lines are prefixed with "  ".
// Returns an error message if either snapshot name is not found.
func (p *Pipeline) Diff(a, b string) string {
	irA := p.findSnapshot(a)
	irB := p.findSnapshot(b)
	if irA == "" && irB == "" {
		return fmt.Sprintf("(snapshots %q and %q not found)", a, b)
	}
	if irA == "" {
		return fmt.Sprintf("(snapshot %q not found)", a)
	}
	if irB == "" {
		return fmt.Sprintf("(snapshot %q not found)", b)
	}

	linesA := strings.Split(irA, "\n")
	linesB := strings.Split(irB, "\n")

	return lineDiff(linesA, linesB)
}

// findSnapshot returns the IR string for the named snapshot, or "" if not found.
func (p *Pipeline) findSnapshot(name string) string {
	for _, snap := range p.snapshots {
		if snap.Name == name {
			return snap.IR
		}
	}
	return ""
}

// lineDiff produces a simple line-level diff between two slices of lines.
// Uses a basic LCS (longest common subsequence) approach for small inputs,
// appropriate for IR dumps which are typically short.
func lineDiff(a, b []string) string {
	// Build LCS table.
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to produce diff.
	var result []string
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && a[i-1] == b[j-1] {
			result = append(result, "  "+a[i-1])
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			result = append(result, "+ "+b[j-1])
			j--
		} else {
			result = append(result, "- "+a[i-1])
			i--
		}
	}

	// Reverse since we built it backwards.
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}

	return strings.Join(result, "\n")
}

// ---------------------------------------------------------------------------
// Production Tier 2 pipeline helpers
// ---------------------------------------------------------------------------

// Tier2PipelineOpts configures the production Tier 2 optimization pipeline.
// A nil *Tier2PipelineOpts uses defaults (MaxSize 40, no globals).
type Tier2PipelineOpts struct {
	InlineGlobals                   map[string]*vm.FuncProto      // global function protos for inlining
	ProtocolGlobals                 map[string]*vm.FuncProto      // stable globals available for guarded protocol folds
	GlobalConstValues               map[int]runtime.Value         // const-pool global name index -> observed numeric value
	InlineMaxSize                   int                           // max callee bytecode count; 0 → 40
	FixedShapeArgFacts              map[int]FixedShapeTableFact   // guarded fixed-shape facts for callee params
	FixedShapeArrayElementArgFacts  map[int]FixedShapeTableFact   // guarded fixed-shape facts for callee param array elements
	FixedShapeArrayElementPolyFacts map[int][]FixedShapeTableFact // guarded polymorphic facts for callee param array elements
	FixedShapeEntryGuards           bool                          // emit callee-entry shape guards for FixedShapeArgFacts
	Remarks                         *OptimizationRemarks          // optional structured optimization diagnostics
}

// RunTier2Pipeline runs the full production Tier 2 optimization pipeline:
//
//	TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp →
//	LoadElim → EscapeAnalysis → DCE → PostRewriteTypeSpec →
//	LoopBoundRangeGuard → RangeAnalysis → OverflowBoxing → FMAFusion →
//	FloatStrengthReduction → FMAFusion → LICM → FieldNumToFloatFusion →
//	LoadElim → DCE → UnrollAndJam → RangeAnalysis → DCE
//
// Returns the optimized function, any intrinsic rewrite notes (non-nil means
// the function uses intrinsics that Tier 1 would execute differently), and an
// error if a pass fails.
//
// If opts is nil, defaults are used (MaxSize: 40, no globals).
func RunTier2Pipeline(fn *Function, opts *Tier2PipelineOpts) (*Function, []string, error) {
	var err error
	if opts != nil && opts.Remarks != nil {
		fn.Remarks = opts.Remarks
	}

	maxSize := 40
	var globals map[string]*vm.FuncProto
	if opts != nil {
		globals = callABIMergeGlobals(opts.InlineGlobals, opts.ProtocolGlobals)
		if opts.InlineMaxSize > 0 {
			maxSize = opts.InlineMaxSize
		}
	}
	protocolGlobals := globals
	if opts != nil && len(opts.ProtocolGlobals) > 0 {
		protocolGlobals = opts.ProtocolGlobals
	}

	ctx := &Tier2OptimizerContext{
		Globals:         globals,
		ProtocolGlobals: protocolGlobals,
		InlineMaxSize:   maxSize,
	}

	fn, err = runTier2OptimizerPlan(fn, opts, ctx, newTier2OptimizerPlan(ctx))
	if err != nil {
		return nil, nil, err
	}

	return fn, ctx.IntrinsicNotes, nil
}

func optsFixedShapeArgFacts(opts *Tier2PipelineOpts) map[int]FixedShapeTableFact {
	if opts == nil {
		return nil
	}
	return opts.FixedShapeArgFacts
}

func optsFixedShapeArrayElementArgFacts(opts *Tier2PipelineOpts) map[int]FixedShapeTableFact {
	if opts == nil {
		return nil
	}
	return opts.FixedShapeArrayElementArgFacts
}

func optsFixedShapeArrayElementPolyFacts(opts *Tier2PipelineOpts) map[int][]FixedShapeTableFact {
	if opts == nil {
		return nil
	}
	return opts.FixedShapeArrayElementPolyFacts
}

func optsFixedShapeEntryGuards(opts *Tier2PipelineOpts) bool {
	return opts != nil && opts.FixedShapeEntryGuards
}

func runPostRewriteTypeSpecialize(fn *Function, opts *Tier2PipelineOpts, stage string) (*Function, error) {
	if !typeSpecializeCouldChange(fn) {
		return fn, nil
	}
	functionRemarks(fn).Add("TypeSpec", "changed", 0, 0, OpNop,
		"reran after "+stage+" rewrite exposed typed SSA values")
	out, err := TypeSpecializePass(fn)
	if err != nil {
		return nil, fmt.Errorf("TypeSpecialize (%s): %w", stage, err)
	}
	attachRemarks(out, opts)
	return out, nil
}

func attachRemarks(fn *Function, opts *Tier2PipelineOpts) {
	if fn != nil && opts != nil && opts.Remarks != nil {
		fn.Remarks = opts.Remarks
	}
}

// NewTier2Pipeline returns a Pipeline pre-loaded with the no-profile/no-globals
// Tier 2 pass list. It exists ONLY as a dump helper for Diagnose() and related
// correctness tests that need per-pass IR snapshots.
//
// DO NOT use this for performance diagnostics. It cannot accept inline globals,
// protocol globals, guarded argument facts, or specialization profiles, so it is
// not bit-identical to production compileTier2Pipeline. Use
// TieringManager.CompileForDiagnostics for production-parity diagnostics.
func NewTier2Pipeline() *Pipeline {
	pipe := NewPipeline()
	ctx := &Tier2OptimizerContext{InlineMaxSize: 40}
	addTier2OptimizerPlanToPipeline(pipe, newTier2OptimizerPlan(ctx), ctx)
	return pipe
}

func addTier2OptimizerPlanToPipeline(pipe *Pipeline, plan Tier2OptimizerPlan, ctx *Tier2OptimizerContext) {
	for _, phase := range plan.Phases {
		for _, module := range plan.Modules {
			if module.Phase != phase {
				continue
			}
			module := module
			pipe.Add(module.Name, func(fn *Function) (*Function, error) {
				if module.RunWithContext != nil {
					return module.RunWithContext(fn, nil, ctx)
				}
				return module.Run(fn, nil)
			})
		}
	}
}
