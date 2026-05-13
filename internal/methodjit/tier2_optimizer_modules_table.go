package methodjit

import "github.com/gscript/gscript/internal/vm"

func tier2TableObjectPreparationModules(globals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("TablePreallocHint", Tier2PhaseTableObjectPrep, TablePreallocHintPass),
		tier2PassModule("TypeSpecialize (post-table-prealloc)", Tier2PhaseTableObjectPrep, TypeSpecializePass),
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
		tier2PassModule("LoadElimination", Tier2PhaseTableObjectPrep, LoadEliminationPass),
		tier2PassModule("FieldLenFold", Tier2PhaseTableObjectPrep, FieldLenFoldPass),
		tier2PassModule("StaticTableLenFold", Tier2PhaseTableObjectPrep, StaticTableLenFoldPass),
		tier2PassModule("EscapeAnalysis", Tier2PhaseTableObjectPrep, EscapeAnalysisPass),
		tier2PassModule("FixedTableConstructorLowering", Tier2PhaseTableObjectPrep, FixedTableConstructorLoweringPass),
		tier2PassModule("TablePreallocHint (post-fixed-table-lowering)", Tier2PhaseTableObjectPrep, TablePreallocHintPass),
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
		tier2PassModule("RedundantGuardElimination", Tier2PhaseTableObjectPrep, RedundantGuardEliminationPass),
	}
}

func tier2TableArrayNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("TableArrayLower", Tier2PhaseTableArrayLower, TableArrayLowerPass),
		tier2PassModule("TableArrayLoadTypeSpecialize", Tier2PhaseTableArrayLower, TableArrayLoadTypeSpecializePass),
		tier2PassModule("TableArrayNestedLoad", Tier2PhaseTableArrayLower, TableArrayNestedLoadPass),
	}
}

func tier2TableFieldNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("TableArrayStoreLower", Tier2PhaseTableFieldLower, TableArrayStoreLowerPass),
		tier2PassModule("FieldSvalsLower", Tier2PhaseTableFieldLower, FieldSvalsLowerPass),
		tier2PassModule("ProfiledStringLenFold", Tier2PhaseTableFieldLower, ProfiledStringLenFoldPass),
		tier2PassModule("RangeAnalysis (post-TableFieldLower)", Tier2PhaseTableFieldLower, RangeAnalysisPass),
		tier2PassModule("DCE (post-TableArrayStoreLower)", Tier2PhaseTableFieldLower, DCEPass),
	}
}

func tier2TableLoopKernelModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("BoolTableFillLoop", Tier2PhaseLoopKernel, BoolTableFillLoopPass),
		tier2PassModule("TableArrayStoreLoopVersion", Tier2PhaseLoopKernel, TableArrayStoreLoopVersionPass),
		tier2PassModule("TableIntArrayKernel", Tier2PhaseLoopKernel, TableIntArrayKernelPass),
		tier2PassModule("BoolTableCountLoop", Tier2PhaseLoopKernel, BoolTableCountLoopPass),
	}
}

func tier2TableLoopPostLoadElimModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("TableArraySwapFusion", Tier2PhaseLoopKernel, TableArraySwapFusionPass),
		tier2PassModule("TableIntArrayKernel (post-swap-fusion)", Tier2PhaseLoopKernel, TableIntArrayKernelPass),
	}
}
