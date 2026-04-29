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

		result, err := entry.fn(current)
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
	InlineGlobals      map[string]*vm.FuncProto    // global function protos for inlining
	InlineMaxSize      int                         // max callee bytecode count; 0 → 40
	FixedShapeArgFacts map[int]FixedShapeTableFact // guarded fixed-shape facts for callee params
	Remarks            *OptimizationRemarks        // optional structured optimization diagnostics
}

// RunTier2Pipeline runs the full production Tier 2 optimization pipeline:
//
//	TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp →
//	LoadElim → EscapeAnalysis → DCE → PostRewriteTypeSpec →
//	RangeAnalysis → OverflowBoxing → LICM → FieldNumToFloatFusion →
//	LoadElim → DCE
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

	fn, err = SimplifyPhisPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("SimplifyPhis: %w", err)
	}

	fn, err = TypeSpecializePass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("TypeSpecialize: %w", err)
	}
	attachRemarks(fn, opts)

	fn, intrinsicNotes := IntrinsicPass(fn)
	attachRemarks(fn, opts)

	fn, err = TypeSpecializePass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("TypeSpecialize (post-intrinsic): %w", err)
	}
	attachRemarks(fn, opts)

	// Inline pass: build config from opts.
	maxSize := 40
	var globals map[string]*vm.FuncProto
	if opts != nil {
		globals = opts.InlineGlobals
		if opts.InlineMaxSize > 0 {
			maxSize = opts.InlineMaxSize
		}
	}
	if len(globals) > 0 {
		// R73: MaxRecursion 2 → 3. Deeper recursive inlining for fib/
		// ackermann call trees. Each level ~doubles the inlined body size,
		// so depth=3 means callee body expanded ~8x at the inline site.
		// Combined with R72's inlineMaxCalleeSize=250, fib(15 ops) can
		// unroll to ~120 ops inside main, eliminating BLR chains. Ack has
		// same pattern. hasCallInLoop gate still protects against explosion.
		// R169 (Session 4 / Path I): inline heuristic overhaul. Replaces
		// uniform MaxRecursion=5 (which under-inlines symmetric trees
		// like fib and over-inlines asymmetric ones like ack) with
		// V8-style cumulative-bytecode budget + raised per-callee depth.
		//
		// MaxRecursion=8 lets fib's symmetric tree inline 8 levels deep
		// (vs 5 previously) for additional body fusion.
		// MaxCumulativeSize=120 caps the total inlined bytecode per
		// compilation, preventing ack's asymmetric tree (2 nested calls
		// per level) from blowing up.
		//
		// 5-sample bench medians (R169 vs R163):
		//   fib              0.827s vs 0.866s  -5%   ✓
		//   ackermann        0.393s vs 0.416s  -6%   ✓
		//   binary_trees     1.495s vs 1.448s  +3%   minor regression
		//   others           flat (within noise)
		config := InlineConfig{
			Globals:           globals,
			MaxSize:           maxSize,
			MaxRecursion:      8,
			MaxCumulativeSize: 120,
			PreserveSelfCalls: staticallyCallsOnlySelf(fn.Proto),
		}
		fn, err = InlinePassWith(config)(fn)
		if err != nil {
			return nil, nil, fmt.Errorf("Inline: %w", err)
		}
		attachRemarks(fn, opts)
		fn, err = SimplifyPhisPass(fn)
		if err != nil {
			return nil, nil, fmt.Errorf("SimplifyPhis (post-inline): %w", err)
		}
		attachRemarks(fn, opts)
		var postInlineNotes []string
		fn, postInlineNotes = IntrinsicPass(fn)
		if len(postInlineNotes) > 0 {
			intrinsicNotes = append(intrinsicNotes, postInlineNotes...)
		}
		attachRemarks(fn, opts)
		fn, err = TypeSpecializePass(fn)
		if err != nil {
			return nil, nil, fmt.Errorf("TypeSpecialize (post-inline): %w", err)
		}
		attachRemarks(fn, opts)
	} else if countOpHelper(fn, OpCall) > 0 {
		functionRemarks(fn).Add("Inline", "missed", 0, 0, OpCall,
			"inline pass skipped because no inline globals were available")
	}

	fn, err = AnnotateCallABIsPass(CallABIAnnotationConfig{Globals: globals})(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("CallABI: %w", err)
	}
	attachRemarks(fn, opts)

	fn, err = ConstPropPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("ConstProp: %w", err)
	}

	fn, err = TablePreallocHintPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("TablePreallocHint: %w", err)
	}

	// TablePrealloc can infer local dense array kinds from typed stores even
	// when bytecode feedback is still empty. Re-run typespec so GetTable users
	// see those newly annotated Aux2 kind facts.
	fn, err = TypeSpecializePass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("TypeSpecialize (post-table-prealloc): %w", err)
	}

	fn, err = FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{
		Globals:  globals,
		ArgFacts: optsFixedShapeArgFacts(opts),
	})(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("FixedShapeTableFacts: %w", err)
	}
	attachRemarks(fn, opts)

	fn, err = LoadEliminationPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("LoadElimination: %w", err)
	}
	attachRemarks(fn, opts)

	// R162 (Session 1 / B.5): escape analysis + scalar replacement.
	// Must run AFTER LoadElim (so stored-value forwarding has already
	// happened) and BEFORE DCE (so the OpNop'd alloc/field ops are
	// cleaned up in a single sweep). Currently handles block-local
	// virtual allocations only (R158/R159 MVP); if/else merges and
	// loop-carried virtuals pending R160/R161.
	fn, err = EscapeAnalysisPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("EscapeAnalysis: %w", err)
	}

	fn, err = FixedTableConstructorLoweringPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("FixedTableConstructorLowering: %w", err)
	}
	attachRemarks(fn, opts)

	fn, err = RedundantGuardEliminationPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("RedundantGuardElimination: %w", err)
	}

	fn, err = DCEPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("DCE: %w", err)
	}

	fn, err = runPostRewriteTypeSpecialize(fn, opts, "post-escape")
	if err != nil {
		return nil, nil, err
	}

	fn, err = RangeAnalysisPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("RangeAnalysis: %w", err)
	}

	fn, err = OverflowBoxingPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("OverflowBoxing: %w", err)
	}

	fn, err = IntExactDivisionPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("IntExactDivision: %w", err)
	}

	fn, err = RangeAnalysisPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("RangeAnalysis (post-IntExactDivision): %w", err)
	}

	fn, err = ModZeroComparePass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("ModZeroCompare: %w", err)
	}

	fn, err = DCEPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("DCE (post-ModZeroCompare): %w", err)
	}

	fn, err = TableArrayLowerPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("TableArrayLower: %w", err)
	}

	fn, err = TableArrayLoadTypeSpecializePass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("TableArrayLoadTypeSpecialize: %w", err)
	}
	attachRemarks(fn, opts)

	fn, err = TableArrayNestedLoadPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("TableArrayNestedLoad: %w", err)
	}
	attachRemarks(fn, opts)

	// R45: lower OpMatrixGetF/SetF into OpMatrixFlat + OpMatrixStride +
	// OpMatrixLoadFAt/StoreFAt so LICM can hoist the Flat/Stride ops
	// out of inner loops where m is invariant.
	fn, err = MatrixLowerPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("MatrixLower: %w", err)
	}

	// R53: re-run LoadElimination to CSE the MatrixFlat/MatrixStride ops
	// that MatrixLowerPass just introduced (many per-call-site duplicates
	// on the same matrix; the first LoadElim pass above ran before these
	// existed).
	fn, err = LoadEliminationPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("LoadElimination (post-MatrixLower): %w", err)
	}
	attachRemarks(fn, opts)

	fn, err = DCEPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("DCE (post-LoadElim2): %w", err)
	}

	// R62: UnrollAndJam scaffold (detection only; transform in future rounds).
	// Runs before FMAFusion so that when the transform ships, the new split
	// Phi accumulators are visible to FMA fusion.
	fn, err = UnrollAndJamPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("UnrollAndJam: %w", err)
	}

	// R47: fuse OpAddFloat(x, OpMulFloat(y,z)) → OpFMA(y,z,x) so the
	// emitter produces a single FMADDd instead of FMUL + FADD.
	fn, err = FMAFusionPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("FMAFusion: %w", err)
	}

	fn, err = LICMPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("LICM: %w", err)
	}
	attachRemarks(fn, opts)

	fn, err = FieldNumToFloatFusionPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("FieldNumToFloatFusion (post-LICM): %w", err)
	}
	attachRemarks(fn, opts)

	// LICM can co-locate equivalent facts that originated in different loop
	// blocks. Re-run block-local CSE now that those facts share a preheader,
	// then sweep dead duplicates before register allocation.
	fn, err = LoadEliminationPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("LoadElimination (post-LICM): %w", err)
	}
	attachRemarks(fn, opts)

	fn, err = DCEPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("DCE (post-LICM LoadElim): %w", err)
	}

	fn, err = ScalarPromotionPass(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("ScalarPromotion: %w", err)
	}

	return fn, intrinsicNotes, nil
}

func optsFixedShapeArgFacts(opts *Tier2PipelineOpts) map[int]FixedShapeTableFact {
	if opts == nil {
		return nil
	}
	return opts.FixedShapeArgFacts
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

// NewTier2Pipeline returns a Pipeline pre-loaded with a pass list that
// mirrors the production Tier 2 order. It exists ONLY as a dump helper for
// Diagnose() and related correctness tests that need per-pass IR snapshots.
//
// DO NOT use this for performance diagnostics. It does not accept inline
// globals and is therefore NOT bit-identical to the production
// compileTier2Pipeline. Use TieringManager.CompileForDiagnostics instead,
// which is parity-tested (TestDiag_ProductionParity_*).
//
// This is the pattern R31 and R32 wasted rounds on: a "diagnostic pipeline"
// with subtly different defaults that silently diverges from production.
// See CLAUDE.md rule 5.
func NewTier2Pipeline() *Pipeline {
	pipe := NewPipeline()
	pipe.Add("SimplifyPhis", SimplifyPhisPass)
	pipe.Add("TypeSpecialize", TypeSpecializePass)
	pipe.Add("Intrinsic", func(fn *Function) (*Function, error) {
		result, _ := IntrinsicPass(fn)
		return result, nil
	})
	pipe.Add("TypeSpecialize2", TypeSpecializePass)
	pipe.Add("Inline", InlinePassWith(InlineConfig{MaxSize: 40, MaxRecursion: 2}))
	pipe.Add("SimplifyPhis2", SimplifyPhisPass)
	pipe.Add("TypeSpecialize3", TypeSpecializePass)
	pipe.Add("CallABI", AnnotateCallABIsPass(CallABIAnnotationConfig{}))
	pipe.Add("ConstProp", ConstPropPass)
	pipe.Add("LoadElimination", LoadEliminationPass)
	pipe.Add("DCE", DCEPass)
	pipe.Add("RangeAnalysis", RangeAnalysisPass)
	pipe.Add("OverflowBoxing", OverflowBoxingPass)
	pipe.Add("IntExactDivision", IntExactDivisionPass)
	pipe.Add("RangeAnalysisPostIntExact", RangeAnalysisPass)
	pipe.Add("ModZeroCompare", ModZeroComparePass)
	pipe.Add("DCEPostModZeroCompare", DCEPass)
	pipe.Add("TableArrayLower", TableArrayLowerPass)
	pipe.Add("TableArrayLoadTypeSpecialize", TableArrayLoadTypeSpecializePass)
	pipe.Add("TableArrayNestedLoad", TableArrayNestedLoadPass)
	pipe.Add("LICM", LICMPass)
	pipe.Add("FieldNumToFloatFusion", FieldNumToFloatFusionPass)
	pipe.Add("LoadEliminationPostLICM", LoadEliminationPass)
	pipe.Add("DCEPostLICM", DCEPass)
	pipe.Add("ScalarPromotion", ScalarPromotionPass)
	return pipe
}
