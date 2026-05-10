package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// Tier2OptimizerPhase names a coarse-grained extension point in the Tier 2
// optimizer. New native optimizations should enter through a phase module
// instead of being inserted ad hoc into RunTier2Pipeline.
type Tier2OptimizerPhase string

const (
	Tier2PhaseTableObjectPrep Tier2OptimizerPhase = "table_object_prep"
	Tier2PhaseTableArrayLower Tier2OptimizerPhase = "table_array_lower"
	Tier2PhaseTableFieldLower Tier2OptimizerPhase = "table_field_lower"
)

// Tier2OptimizerModule is the smallest pluggable optimization unit. Modules
// are intentionally plain functions with metadata: this keeps hot code out of
// interfaces while giving diagnostics, ordering, and future feature switches a
// single place to hook into.
type Tier2OptimizerModule struct {
	Name  string
	Phase Tier2OptimizerPhase
	Run   func(*Function, *Tier2PipelineOpts) (*Function, error)
}

func runTier2OptimizerModules(fn *Function, opts *Tier2PipelineOpts, phase Tier2OptimizerPhase, modules []Tier2OptimizerModule) (*Function, error) {
	var err error
	for _, module := range modules {
		if module.Phase != phase {
			continue
		}
		if module.Run == nil {
			return nil, fmt.Errorf("%s: missing optimizer module runner", module.Name)
		}
		fn, err = module.Run(fn, opts)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", module.Name, err)
		}
		attachRemarks(fn, opts)
	}
	return fn, nil
}

func tier2TableObjectPreparationModules(globals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "TablePreallocHint",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TablePreallocHintPass(fn)
			},
		},
		{
			Name:  "TypeSpecialize (post-table-prealloc)",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TypeSpecializePass(fn)
			},
		},
		{
			Name:  "FixedShapeTableFacts",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{
					Globals:               globals,
					ArgFacts:              optsFixedShapeArgFacts(opts),
					ArrayElementArgFacts:  optsFixedShapeArrayElementArgFacts(opts),
					ArrayElementPolyFacts: optsFixedShapeArrayElementPolyFacts(opts),
					EntryGuardedArgs:      optsFixedShapeEntryGuards(opts),
				})(fn)
			},
		},
		{
			Name:  "LoadElimination",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return LoadEliminationPass(fn)
			},
		},
		{
			Name:  "FieldLenFold",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FieldLenFoldPass(fn)
			},
		},
		{
			Name:  "EscapeAnalysis",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return EscapeAnalysisPass(fn)
			},
		},
		{
			Name:  "FixedTableConstructorLowering",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FixedTableConstructorLoweringPass(fn)
			},
		},
		{
			Name:  "TablePreallocHint (post-fixed-table-lowering)",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TablePreallocHintPass(fn)
			},
		},
		{
			Name:  "EscapeAnalysis (post-fixed-table-lowering)",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				if !hasFixedTableScalarReplacementCandidate(fn) {
					return fn, nil
				}
				return EscapeAnalysisPass(fn)
			},
		},
		{
			Name:  "RedundantGuardElimination",
			Phase: Tier2PhaseTableObjectPrep,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return RedundantGuardEliminationPass(fn)
			},
		},
	}
}

func runTableObjectPreparation(fn *Function, opts *Tier2PipelineOpts, globals map[string]*vm.FuncProto) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseTableObjectPrep, tier2TableObjectPreparationModules(globals))
}

func runTableArrayNativeLowering(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseTableArrayLower, tier2TableArrayNativeLoweringModules())
}

func tier2TableArrayNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "TableArrayLower",
			Phase: Tier2PhaseTableArrayLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableArrayLowerPass(fn)
			},
		},
		{
			Name:  "TableArrayLoadTypeSpecialize",
			Phase: Tier2PhaseTableArrayLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableArrayLoadTypeSpecializePass(fn)
			},
		},
		{
			Name:  "TableArrayNestedLoad",
			Phase: Tier2PhaseTableArrayLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableArrayNestedLoadPass(fn)
			},
		},
	}
}

func runTableFieldNativeLowering(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseTableFieldLower, tier2TableFieldNativeLoweringModules())
}

func tier2TableFieldNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "FieldSvalsLower",
			Phase: Tier2PhaseTableFieldLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FieldSvalsLowerPass(fn)
			},
		},
		{
			Name:  "TableArrayStoreLower",
			Phase: Tier2PhaseTableFieldLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableArrayStoreLowerPass(fn)
			},
		},
	}
}
