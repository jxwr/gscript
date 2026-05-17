package methodjit

func tier2NumericModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("LoopBoundRangeGuard", Tier2PhaseNumeric, LoopBoundRangeGuardPass),
		tier2PassModule("ObservedParamRangeGuard", Tier2PhaseNumeric, ObservedParamRangeGuardPass),
		tier2PassModule("ExactGuardConst", Tier2PhaseNumeric, ExactGuardConstPass),
		tier2PassModule("ConstProp (post-ExactGuardConst)", Tier2PhaseNumeric, ConstPropPass),
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
		{
			Name:  "IntExactDivision",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				out, changed, err := runIntExactDivisionIfCandidate(fn)
				if opts != nil {
					opts.LastPassChanged = changed
				}
				return out, err
			},
		},
		{
			Name:  "RangeAnalysis (post-IntExactDivision)",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				if opts != nil && !opts.LastPassChanged {
					functionRemarks(fn).Add("RangeAnalysis", "skipped", 0, 0, OpNop,
						"IntExactDivision had no candidate rewrite")
					return fn, nil
				}
				return RangeAnalysisPass(fn)
			},
		},
		tier2PassModule("ModRangeSimplify", Tier2PhaseNumeric, ModRangeSimplifyPass),
		tier2PassModule("DCE (post-ModRangeSimplify)", Tier2PhaseNumeric, DCEPass),
		tier2PassModule("ModZeroCompare", Tier2PhaseNumeric, ModZeroComparePass),
		tier2PassModule("DCE (post-ModZeroCompare)", Tier2PhaseNumeric, DCEPass),
		tier2PassModule("ConstantPhiBranchThreading", Tier2PhaseNumeric, ConstantPhiBranchThreadingPass),
		tier2PassModule("JumpOnlyThreading", Tier2PhaseNumeric, JumpOnlyThreadingPass),
		tier2PassModule("SimplifyPhis (post-ConstantPhiBranchThreading)", Tier2PhaseNumeric, SimplifyPhisPass),
	}
}

func tier2MatrixNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("DenseMatrixNestedLoadLower", Tier2PhaseMatrixNative, DenseMatrixNestedLoadLowerPass),
		tier2PassModule("MatrixLower", Tier2PhaseMatrixNative, MatrixLowerPass),
		{
			Name:  "LoadElimination (post-MatrixLower)",
			Phase: Tier2PhaseMatrixNative,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				if !hasMatrixNativeIR(fn) {
					return fn, nil
				}
				return LoadEliminationPass(fn)
			},
		},
		tier2PassModule("MatrixRowPtrFactoring", Tier2PhaseMatrixNative, MatrixRowPtrFactoringPass),
		tier2PassModule("MatrixUnitStride", Tier2PhaseMatrixNative, MatrixUnitStridePass),
	}
}

func hasMatrixNativeIR(fn *Function) bool {
	if fn == nil {
		return false
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpMatrixDense, OpMatrixGetF, OpMatrixSetF,
				OpMatrixFlat, OpMatrixStride, OpMatrixLoadFAt, OpMatrixStoreFAt,
				OpMatrixRowPtr, OpMatrixLoadFRow, OpMatrixStoreFRow,
				OpMatrixLoadFRowConst, OpMatrixStoreFRowConst:
				return true
			}
		}
	}
	return false
}

func tier2FloatNumericModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("FMAFusion", Tier2PhaseFloatNumeric, FMAFusionPass),
		tier2PassModule("FloatStrengthReduction", Tier2PhaseFloatNumeric, FloatStrengthReductionPass),
		tier2PassModule("FMAFusion (post-FloatStrengthReduction)", Tier2PhaseFloatNumeric, FMAFusionPass),
		tier2PassModule("ComplexEscapeLoop", Tier2PhaseFloatNumeric, ComplexEscapeLoopPass),
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
		tier2PassModule("ClosureUpvalueScalar", Tier2PhaseLoopKernel, ClosureUpvalueScalarPass),
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
		{
			Name:  "LICM (post-MatrixRowPtrFactoring)",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				if !hasMatrixNativeIR(fn) {
					return fn, nil
				}
				return LICMPass(fn)
			},
		},
		tier2PassModule("QuadraticStepStrengthReduction", Tier2PhaseLoopPost, QuadraticStepStrengthReductionPass),
		tier2PassModule("RangeAnalysis (post-UnrollAndJam)", Tier2PhaseLoopPost, RangeAnalysisPass),
		tier2PassModule("IntAlgebraSimplify", Tier2PhaseLoopPost, IntAlgebraSimplifyPass),
		tier2PassModule("TableArrayStaticBounds (post-RangeAnalysis)", Tier2PhaseLoopPost, TableArrayStaticBoundsPass),
		tier2PassModule("DCE (post-UnrollAndJam)", Tier2PhaseLoopPost, DCEPass),
		tier2PassModule("LoopRegionVersioning", Tier2PhaseLoopPost, LoopRegionVersioningPass),
		tier2PassModule("TableArrayStaticBounds (post-LoopRegionVersioning)", Tier2PhaseLoopPost, TableArrayStaticBoundsPass),
		tier2PassModule("ScalarPromotion", Tier2PhaseLoopPost, ScalarPromotionPass),
		tier2PassModule("TableArrayDataPtrFact", Tier2PhaseLoopPost, TableArrayDataPtrFactPass),
	}
}
