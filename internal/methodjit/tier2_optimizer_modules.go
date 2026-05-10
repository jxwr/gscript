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
	Tier2PhaseEarlyCanonical  Tier2OptimizerPhase = "early_canonical"
	Tier2PhaseInlineCall      Tier2OptimizerPhase = "inline_call"
	Tier2PhaseCallLower       Tier2OptimizerPhase = "call_lower"
	Tier2PhaseTableObjectPrep Tier2OptimizerPhase = "table_object_prep"
	Tier2PhasePostRewrite     Tier2OptimizerPhase = "post_rewrite"
	Tier2PhaseNumeric         Tier2OptimizerPhase = "numeric"
	Tier2PhaseTableArrayLower Tier2OptimizerPhase = "table_array_lower"
	Tier2PhaseTableFieldLower Tier2OptimizerPhase = "table_field_lower"
	Tier2PhaseMatrixNative    Tier2OptimizerPhase = "matrix_native"
	Tier2PhaseFloatNumeric    Tier2OptimizerPhase = "float_numeric"
	Tier2PhaseLoopKernel      Tier2OptimizerPhase = "loop_kernel"
	Tier2PhaseLoopPost        Tier2OptimizerPhase = "loop_post"
	Tier2PhaseFinalCall       Tier2OptimizerPhase = "final_call"
)

type Tier2OptimizerContext struct {
	Globals         map[string]*vm.FuncProto
	ProtocolGlobals map[string]*vm.FuncProto
	IntrinsicNotes  []string
	InlineApplied   bool
}

// Tier2OptimizerModule is the smallest pluggable optimization unit. Modules
// are intentionally plain functions with metadata: this keeps hot code out of
// interfaces while giving diagnostics, ordering, and future feature switches a
// single place to hook into.
type Tier2OptimizerModule struct {
	Name           string
	Phase          Tier2OptimizerPhase
	Run            func(*Function, *Tier2PipelineOpts) (*Function, error)
	RunWithContext func(*Function, *Tier2PipelineOpts, *Tier2OptimizerContext) (*Function, error)
}

func runTier2OptimizerModules(fn *Function, opts *Tier2PipelineOpts, phase Tier2OptimizerPhase, modules []Tier2OptimizerModule) (*Function, error) {
	return runTier2OptimizerModulesWithContext(fn, opts, nil, phase, modules)
}

func runTier2OptimizerModulesWithContext(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext, phase Tier2OptimizerPhase, modules []Tier2OptimizerModule) (*Function, error) {
	var err error
	for _, module := range modules {
		if module.Phase != phase {
			continue
		}
		if module.RunWithContext == nil && module.Run == nil {
			return nil, fmt.Errorf("%s: missing optimizer module runner", module.Name)
		}
		if module.RunWithContext != nil {
			fn, err = module.RunWithContext(fn, opts, ctx)
		} else {
			fn, err = module.Run(fn, opts)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", module.Name, err)
		}
		attachRemarks(fn, opts)
	}
	return fn, nil
}

func tier2EarlyCanonicalModules(globals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "SimplifyPhis",
			Phase: Tier2PhaseEarlyCanonical,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return SimplifyPhisPass(fn)
			},
		},
		{
			Name:  "TypeSpecialize",
			Phase: Tier2PhaseEarlyCanonical,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TypeSpecializePass(fn)
			},
		},
		{
			Name:  "Intrinsic",
			Phase: Tier2PhaseEarlyCanonical,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				out, notes := IntrinsicPass(fn)
				if ctx != nil && len(notes) > 0 {
					ctx.IntrinsicNotes = append(ctx.IntrinsicNotes, notes...)
				}
				return out, nil
			},
		},
		{
			Name:  "GlobalConstSpecialization",
			Phase: Tier2PhaseEarlyCanonical,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				if opts == nil || len(opts.GlobalConstValues) == 0 {
					return fn, nil
				}
				return GlobalConstSpecializationPass(opts.GlobalConstValues)(fn)
			},
		},
		{
			Name:  "TypeSpecialize (post-intrinsic)",
			Phase: Tier2PhaseEarlyCanonical,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TypeSpecializePass(fn)
			},
		},
		{
			Name:  "FixedShapeTableFacts (pre-inline)",
			Phase: Tier2PhaseEarlyCanonical,
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
	}
}

func runEarlyCanonicalOptimizations(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
	return runTier2OptimizerModulesWithContext(fn, opts, ctx, Tier2PhaseEarlyCanonical, tier2EarlyCanonicalModules(ctxGlobals(ctx)))
}

func tier2InlineCallModules(globals map[string]*vm.FuncProto, maxSize int) []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "Inline",
			Phase: Tier2PhaseInlineCall,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				if len(globals) == 0 && !hasInlineFeedbackCallee(fn) {
					if countOpHelper(fn, OpCall) > 0 {
						functionRemarks(fn).Add("Inline", "missed", 0, 0, OpCall,
							"inline pass skipped because no inline globals were available")
					}
					if ctx != nil {
						ctx.InlineApplied = false
					}
					return fn, nil
				}
				config := InlineConfig{
					Globals:           globals,
					MaxSize:           maxSize,
					MaxRecursion:      8,
					MaxCumulativeSize: 120,
					PreserveSelfCalls: staticallyCallsOnlySelf(fn.Proto),
				}
				out, err := InlinePassWith(config)(fn)
				if ctx != nil {
					ctx.InlineApplied = err == nil
				}
				return out, err
			},
		},
		{
			Name:  "SimplifyPhis (post-inline)",
			Phase: Tier2PhaseInlineCall,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				if ctx == nil || !ctx.InlineApplied {
					return fn, nil
				}
				return SimplifyPhisPass(fn)
			},
		},
		{
			Name:  "Intrinsic (post-inline)",
			Phase: Tier2PhaseInlineCall,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				if ctx == nil || !ctx.InlineApplied {
					return fn, nil
				}
				out, notes := IntrinsicPass(fn)
				if len(notes) > 0 {
					ctx.IntrinsicNotes = append(ctx.IntrinsicNotes, notes...)
				}
				return out, nil
			},
		},
		{
			Name:  "TypeSpecialize (post-inline)",
			Phase: Tier2PhaseInlineCall,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				if ctx == nil || !ctx.InlineApplied {
					return fn, nil
				}
				return TypeSpecializePass(fn)
			},
		},
	}
}

func runInlineCallOptimizations(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext, maxSize int) (*Function, error) {
	return runTier2OptimizerModulesWithContext(fn, opts, ctx, Tier2PhaseInlineCall, tier2InlineCallModules(ctxGlobals(ctx), maxSize))
}

func tier2CallLoweringModules(protocolGlobals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "CallABI",
			Phase: Tier2PhaseCallLower,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				return AnnotateCallABIsPass(CallABIAnnotationConfig{Globals: ctxGlobals(ctx)})(fn)
			},
		},
		{
			Name:  "CallReturnProjection",
			Phase: Tier2PhaseCallLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return CallReturnProjectionPass(fn)
			},
		},
		{
			Name:  "ConstProp",
			Phase: Tier2PhaseCallLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return ConstPropPass(fn)
			},
		},
		{
			Name:  "ProtocolConstCallFold",
			Phase: Tier2PhaseCallLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return ProtocolConstCallFoldPass(protocolGlobals)(fn)
			},
		},
		{
			Name:  "WholeCallKernelExit",
			Phase: Tier2PhaseCallLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return WholeCallKernelExitPass(protocolGlobals)(fn)
			},
		},
	}
}

func runCallLoweringOptimizations(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
	return runTier2OptimizerModulesWithContext(fn, opts, ctx, Tier2PhaseCallLower, tier2CallLoweringModules(ctxProtocolGlobals(ctx)))
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

func tier2PostRewriteModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "CallReturnProjection (post-rewrite)",
			Phase: Tier2PhasePostRewrite,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return CallReturnProjectionPass(fn)
			},
		},
		{
			Name:  "DCE",
			Phase: Tier2PhasePostRewrite,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return DCEPass(fn)
			},
		},
		{
			Name:  "TypeSpecialize (post-escape)",
			Phase: Tier2PhasePostRewrite,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return runPostRewriteTypeSpecialize(fn, opts, "post-escape")
			},
		},
	}
}

func runPostRewriteOptimizations(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhasePostRewrite, tier2PostRewriteModules())
}

func tier2NumericModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "LoopBoundRangeGuard",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return LoopBoundRangeGuardPass(fn)
			},
		},
		{
			Name:  "RangeAnalysis",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return RangeAnalysisPass(fn)
			},
		},
		{
			Name:  "OverflowBoxing",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return OverflowBoxingPass(fn)
			},
		},
		{
			Name:  "IntExactDivision",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return IntExactDivisionPass(fn)
			},
		},
		{
			Name:  "RangeAnalysis (post-IntExactDivision)",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return RangeAnalysisPass(fn)
			},
		},
		{
			Name:  "ModZeroCompare",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return ModZeroComparePass(fn)
			},
		},
		{
			Name:  "DCE (post-ModZeroCompare)",
			Phase: Tier2PhaseNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return DCEPass(fn)
			},
		},
	}
}

func runNumericOptimizations(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseNumeric, tier2NumericModules())
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
		{
			Name:  "DCE (post-TableArrayStoreLower)",
			Phase: Tier2PhaseTableFieldLower,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return DCEPass(fn)
			},
		},
	}
}

func runMatrixNativeLowering(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseMatrixNative, tier2MatrixNativeLoweringModules())
}

func tier2MatrixNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "DenseMatrixNestedLoadLower",
			Phase: Tier2PhaseMatrixNative,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return DenseMatrixNestedLoadLowerPass(fn)
			},
		},
		{
			Name:  "MatrixLower",
			Phase: Tier2PhaseMatrixNative,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return MatrixLowerPass(fn)
			},
		},
		{
			Name:  "LoadElimination (post-MatrixLower)",
			Phase: Tier2PhaseMatrixNative,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return LoadEliminationPass(fn)
			},
		},
		{
			Name:  "MatrixRowPtrFactoring",
			Phase: Tier2PhaseMatrixNative,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return MatrixRowPtrFactoringPass(fn)
			},
		},
		{
			Name:  "MatrixUnitStride",
			Phase: Tier2PhaseMatrixNative,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return MatrixUnitStridePass(fn)
			},
		},
	}
}

func runFloatNumericOptimizations(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseFloatNumeric, tier2FloatNumericModules())
}

func tier2FloatNumericModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "FMAFusion",
			Phase: Tier2PhaseFloatNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FMAFusionPass(fn)
			},
		},
		{
			Name:  "FloatStrengthReduction",
			Phase: Tier2PhaseFloatNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FloatStrengthReductionPass(fn)
			},
		},
		{
			Name:  "FMAFusion (post-FloatStrengthReduction)",
			Phase: Tier2PhaseFloatNumeric,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FMAFusionPass(fn)
			},
		},
	}
}

func runLoopKernelOptimizations(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseLoopKernel, tier2LoopKernelModules())
}

func tier2LoopKernelModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "LICM",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return LICMPass(fn)
			},
		},
		{
			Name:  "BoolTableFillLoop",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return BoolTableFillLoopPass(fn)
			},
		},
		{
			Name:  "TableArrayStoreLoopVersion",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableArrayStoreLoopVersionPass(fn)
			},
		},
		{
			Name:  "TableIntArrayKernel",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableIntArrayKernelPass(fn)
			},
		},
		{
			Name:  "BoolTableCountLoop",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return BoolTableCountLoopPass(fn)
			},
		},
		{
			Name:  "FieldNumToFloatFusion (post-LICM)",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return FieldNumToFloatFusionPass(fn)
			},
		},
		{
			Name:  "LoadElimination (post-LICM)",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return LoadEliminationPass(fn)
			},
		},
		{
			Name:  "TableArraySwapFusion",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableArraySwapFusionPass(fn)
			},
		},
		{
			Name:  "TableIntArrayKernel (post-swap-fusion)",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableIntArrayKernelPass(fn)
			},
		},
		{
			Name:  "DCE (post-LICM LoadElim)",
			Phase: Tier2PhaseLoopKernel,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return DCEPass(fn)
			},
		},
	}
}

func runLoopPostOptimizations(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseLoopPost, tier2LoopPostModules())
}

func tier2LoopPostModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "UnrollAndJam",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return UnrollAndJamPass(fn)
			},
		},
		{
			Name:  "QuadraticStepStrengthReduction",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return QuadraticStepStrengthReductionPass(fn)
			},
		},
		{
			Name:  "RangeAnalysis (post-UnrollAndJam)",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return RangeAnalysisPass(fn)
			},
		},
		{
			Name:  "DCE (post-UnrollAndJam)",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return DCEPass(fn)
			},
		},
		{
			Name:  "LoopRegionVersioning",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return LoopRegionVersioningPass(fn)
			},
		},
		{
			Name:  "ScalarPromotion",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return ScalarPromotionPass(fn)
			},
		},
		{
			Name:  "TableArrayDataPtrFact",
			Phase: Tier2PhaseLoopPost,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return TableArrayDataPtrFactPass(fn)
			},
		},
	}
}

func runFinalCallOptimizations(fn *Function, opts *Tier2PipelineOpts, protocolGlobals map[string]*vm.FuncProto) (*Function, error) {
	return runTier2OptimizerModules(fn, opts, Tier2PhaseFinalCall, tier2FinalCallModules(protocolGlobals))
}

func tier2FinalCallModules(protocolGlobals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "WholeCallKernelExit (final)",
			Phase: Tier2PhaseFinalCall,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return WholeCallKernelExitPass(protocolGlobals)(fn)
			},
		},
		{
			Name:  "CallReturnProjection (final)",
			Phase: Tier2PhaseFinalCall,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return CallReturnProjectionPass(fn)
			},
		},
	}
}

func ctxGlobals(ctx *Tier2OptimizerContext) map[string]*vm.FuncProto {
	if ctx == nil {
		return nil
	}
	return ctx.Globals
}

func ctxProtocolGlobals(ctx *Tier2OptimizerContext) map[string]*vm.FuncProto {
	if ctx == nil {
		return nil
	}
	return ctx.ProtocolGlobals
}
