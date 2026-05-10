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
	InlineMaxSize   int
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

type Tier2PassFunc func(*Function) (*Function, error)

func tier2PassModule(name string, phase Tier2OptimizerPhase, pass Tier2PassFunc) Tier2OptimizerModule {
	return Tier2OptimizerModule{
		Name:  name,
		Phase: phase,
		Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
			return pass(fn)
		},
	}
}

type Tier2OptimizerPlan struct {
	Phases  []Tier2OptimizerPhase
	Modules []Tier2OptimizerModule
}

func newTier2OptimizerPlan(ctx *Tier2OptimizerContext) Tier2OptimizerPlan {
	return Tier2OptimizerPlan{
		Phases: []Tier2OptimizerPhase{
			Tier2PhaseEarlyCanonical,
			Tier2PhaseInlineCall,
			Tier2PhaseCallLower,
			Tier2PhaseTableObjectPrep,
			Tier2PhasePostRewrite,
			Tier2PhaseNumeric,
			Tier2PhaseTableArrayLower,
			Tier2PhaseMatrixNative,
			Tier2PhaseTableFieldLower,
			Tier2PhaseFloatNumeric,
			Tier2PhaseLoopKernel,
			Tier2PhaseLoopPost,
			Tier2PhaseFinalCall,
		},
		Modules: tier2OptimizerModules(ctx),
	}
}

func tier2OptimizerModules(ctx *Tier2OptimizerContext) []Tier2OptimizerModule {
	modules := make([]Tier2OptimizerModule, 0, 64)
	modules = append(modules, tier2EarlyCanonicalModules(ctxGlobals(ctx))...)
	modules = append(modules, tier2InlineCallModules(ctxGlobals(ctx), ctxInlineMaxSize(ctx))...)
	modules = append(modules, tier2CallLoweringModules(ctxProtocolGlobals(ctx))...)
	modules = append(modules, tier2TableObjectPreparationModules(ctxGlobals(ctx))...)
	modules = append(modules, tier2PostRewriteModules()...)
	modules = append(modules, tier2NumericModules()...)
	modules = append(modules, tier2TableArrayNativeLoweringModules()...)
	modules = append(modules, tier2MatrixNativeLoweringModules()...)
	modules = append(modules, tier2TableFieldNativeLoweringModules()...)
	modules = append(modules, tier2FloatNumericModules()...)
	modules = append(modules, tier2LoopKernelModules()...)
	modules = append(modules, tier2LoopPostModules()...)
	modules = append(modules, tier2FinalCallModules(ctxProtocolGlobals(ctx))...)
	return modules
}

func runTier2OptimizerPlan(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext, plan Tier2OptimizerPlan) (*Function, error) {
	var err error
	for _, phase := range plan.Phases {
		fn, err = runTier2OptimizerModulesWithContext(fn, opts, ctx, phase, plan.Modules)
		if err != nil {
			return nil, err
		}
	}
	return fn, nil
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
		tier2PassModule("SimplifyPhis", Tier2PhaseEarlyCanonical, SimplifyPhisPass),
		tier2PassModule("TypeSpecialize", Tier2PhaseEarlyCanonical, TypeSpecializePass),
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
		tier2PassModule("TypeSpecialize (post-intrinsic)", Tier2PhaseEarlyCanonical, TypeSpecializePass),
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
		tier2PostInlinePassModule("SimplifyPhis (post-inline)", SimplifyPhisPass),
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
		tier2PostInlinePassModule("TypeSpecialize (post-inline)", TypeSpecializePass),
	}
}

func tier2PostInlinePassModule(name string, pass Tier2PassFunc) Tier2OptimizerModule {
	return Tier2OptimizerModule{
		Name:  name,
		Phase: Tier2PhaseInlineCall,
		RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
			if ctx == nil || !ctx.InlineApplied {
				return fn, nil
			}
			return pass(fn)
		},
	}
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
		tier2PassModule("CallReturnProjection", Tier2PhaseCallLower, CallReturnProjectionPass),
		tier2PassModule("ConstProp", Tier2PhaseCallLower, ConstPropPass),
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

func tier2PostRewriteModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("CallReturnProjection (post-rewrite)", Tier2PhasePostRewrite, CallReturnProjectionPass),
		tier2PassModule("DCE", Tier2PhasePostRewrite, DCEPass),
		{
			Name:  "TypeSpecialize (post-escape)",
			Phase: Tier2PhasePostRewrite,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return runPostRewriteTypeSpecialize(fn, opts, "post-escape")
			},
		},
	}
}

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

func tier2TableArrayNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("TableArrayLower", Tier2PhaseTableArrayLower, TableArrayLowerPass),
		tier2PassModule("TableArrayLoadTypeSpecialize", Tier2PhaseTableArrayLower, TableArrayLoadTypeSpecializePass),
		tier2PassModule("TableArrayNestedLoad", Tier2PhaseTableArrayLower, TableArrayNestedLoadPass),
	}
}

func tier2TableFieldNativeLoweringModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("FieldSvalsLower", Tier2PhaseTableFieldLower, FieldSvalsLowerPass),
		tier2PassModule("TableArrayStoreLower", Tier2PhaseTableFieldLower, TableArrayStoreLowerPass),
		tier2PassModule("DCE (post-TableArrayStoreLower)", Tier2PhaseTableFieldLower, DCEPass),
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

func tier2FinalCallModules(protocolGlobals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "WholeCallKernelExit (final)",
			Phase: Tier2PhaseFinalCall,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return WholeCallKernelExitPass(protocolGlobals)(fn)
			},
		},
		tier2PassModule("CallReturnProjection (final)", Tier2PhaseFinalCall, CallReturnProjectionPass),
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

func ctxInlineMaxSize(ctx *Tier2OptimizerContext) int {
	if ctx == nil || ctx.InlineMaxSize <= 0 {
		return 40
	}
	return ctx.InlineMaxSize
}
