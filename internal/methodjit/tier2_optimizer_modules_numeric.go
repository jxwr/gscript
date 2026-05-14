package methodjit

func tier2NumericModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("LoopBoundRangeGuard", Tier2PhaseNumeric, LoopBoundRangeGuardPass),
		tier2PassModule("RangeAnalysis", Tier2PhaseNumeric, RangeAnalysisPass),
		{
			Name:  "OverflowBoxing",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				force := map[int]bool(nil)
				if opts != nil {
					force = opts.ForceBoxIntIDs
				}
				return OverflowBoxingPassWith(force)(fn)
			},
		},
		tier2PassModule("IntExactDivision", Tier2PhaseNumeric, IntExactDivisionPass),
		tier2PassModule("RangeAnalysis (post-IntExactDivision)", Tier2PhaseNumeric, RangeAnalysisPass),
		tier2PassModule("ModRangeSimplify", Tier2PhaseNumeric, ModRangeSimplifyPass),
		tier2PassModule("DCE (post-ModRangeSimplify)", Tier2PhaseNumeric, DCEPass),
		tier2PassModule("ModZeroCompare", Tier2PhaseNumeric, ModZeroComparePass),
		tier2PassModule("DCE (post-ModZeroCompare)", Tier2PhaseNumeric, DCEPass),
	}
}

func tier2MatrixNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("DenseMatrixNestedLoadLower", Tier2PhaseMatrixNative, DenseMatrixNestedLoadLowerPass),
		tier2PassModule("MatrixLower", Tier2PhaseMatrixNative, MatrixLowerPass),
		tier2PassModule("LoadElimination (post-MatrixLower)", Tier2PhaseMatrixNative, LoadEliminationPass),
		tier2PassModule("MatrixRowPtrFactoring", Tier2PhaseMatrixNative, MatrixRowPtrFactoringPass),
		tier2PassModule("MatrixUnitStride", Tier2PhaseMatrixNative, MatrixUnitStridePass),
	}
}

func tier2FloatNumericModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("FMAFusion", Tier2PhaseFloatNumeric, FMAFusionPass),
		tier2PassModule("FloatStrengthReduction", Tier2PhaseFloatNumeric, FloatStrengthReductionPass),
		tier2PassModule("FMAFusion (post-FloatStrengthReduction)", Tier2PhaseFloatNumeric, FMAFusionPass),
	}
}

func tier2LoopKernelModules() []Tier2OptimizerModule {
	modules := []Tier2OptimizerModule{
		tier2PassModule("LICM", Tier2PhaseLoopKernel, LICMPass),
		tier2PassModule("LoopGlobalStoreSink", Tier2PhaseLoopKernel, LoopGlobalStoreSinkPass),
	}
	modules = append(modules, tier2TableLoopKernelModules()...)
	modules = append(modules,
		tier2PassModule("FieldNumToFloatFusion (post-LICM)", Tier2PhaseLoopKernel, FieldNumToFloatFusionPass),
		tier2PassModule("LoadElimination (post-LICM)", Tier2PhaseLoopKernel, LoadEliminationPass),
	)
	modules = append(modules, tier2TableLoopPostLoadElimModules()...)
	modules = append(modules, tier2PassModule("DCE (post-LICM LoadElim)", Tier2PhaseLoopKernel, DCEPass))
	return modules
}

func tier2LoopPostModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("UnrollAndJam", Tier2PhaseLoopPost, UnrollAndJamPass),
		tier2PassModule("MatrixRowPtrFactoring (post-UnrollAndJam)", Tier2PhaseLoopPost, MatrixRowPtrFactoringPass),
		tier2PassModule("QuadraticStepStrengthReduction", Tier2PhaseLoopPost, QuadraticStepStrengthReductionPass),
		tier2PassModule("RangeAnalysis (post-UnrollAndJam)", Tier2PhaseLoopPost, RangeAnalysisPass),
		tier2PassModule("DCE (post-UnrollAndJam)", Tier2PhaseLoopPost, DCEPass),
		tier2PassModule("LoopRegionVersioning", Tier2PhaseLoopPost, LoopRegionVersioningPass),
		tier2PassModule("ScalarPromotion", Tier2PhaseLoopPost, ScalarPromotionPass),
		tier2PassModule("TableArrayDataPtrFact", Tier2PhaseLoopPost, TableArrayDataPtrFactPass),
	}
}
