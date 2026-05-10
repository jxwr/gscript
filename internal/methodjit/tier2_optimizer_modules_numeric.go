package methodjit

func tier2NumericModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("LoopBoundRangeGuard", Tier2PhaseNumeric, LoopBoundRangeGuardPass),
		tier2PassModule("RangeAnalysis", Tier2PhaseNumeric, RangeAnalysisPass),
		tier2PassModule("OverflowBoxing", Tier2PhaseNumeric, OverflowBoxingPass),
		tier2PassModule("IntExactDivision", Tier2PhaseNumeric, IntExactDivisionPass),
		tier2PassModule("RangeAnalysis (post-IntExactDivision)", Tier2PhaseNumeric, RangeAnalysisPass),
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
	return []Tier2OptimizerModule{
		tier2PassModule("LICM", Tier2PhaseLoopKernel, LICMPass),
		tier2PassModule("BoolTableFillLoop", Tier2PhaseLoopKernel, BoolTableFillLoopPass),
		tier2PassModule("TableArrayStoreLoopVersion", Tier2PhaseLoopKernel, TableArrayStoreLoopVersionPass),
		tier2PassModule("TableIntArrayKernel", Tier2PhaseLoopKernel, TableIntArrayKernelPass),
		tier2PassModule("BoolTableCountLoop", Tier2PhaseLoopKernel, BoolTableCountLoopPass),
		tier2PassModule("FieldNumToFloatFusion (post-LICM)", Tier2PhaseLoopKernel, FieldNumToFloatFusionPass),
		tier2PassModule("LoadElimination (post-LICM)", Tier2PhaseLoopKernel, LoadEliminationPass),
		tier2PassModule("TableArraySwapFusion", Tier2PhaseLoopKernel, TableArraySwapFusionPass),
		tier2PassModule("TableIntArrayKernel (post-swap-fusion)", Tier2PhaseLoopKernel, TableIntArrayKernelPass),
		tier2PassModule("DCE (post-LICM LoadElim)", Tier2PhaseLoopKernel, DCEPass),
	}
}

func tier2LoopPostModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("UnrollAndJam", Tier2PhaseLoopPost, UnrollAndJamPass),
		tier2PassModule("QuadraticStepStrengthReduction", Tier2PhaseLoopPost, QuadraticStepStrengthReductionPass),
		tier2PassModule("RangeAnalysis (post-UnrollAndJam)", Tier2PhaseLoopPost, RangeAnalysisPass),
		tier2PassModule("DCE (post-UnrollAndJam)", Tier2PhaseLoopPost, DCEPass),
		tier2PassModule("LoopRegionVersioning", Tier2PhaseLoopPost, LoopRegionVersioningPass),
		tier2PassModule("ScalarPromotion", Tier2PhaseLoopPost, ScalarPromotionPass),
		tier2PassModule("TableArrayDataPtrFact", Tier2PhaseLoopPost, TableArrayDataPtrFactPass),
	}
}
