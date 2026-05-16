package methodjit

import (
	"os"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

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
					ArgPolyFacts:          optsFixedShapeArgPolyFacts(opts),
					ArrayElementArgFacts:  optsFixedShapeArrayElementArgFacts(opts),
					ArrayElementPolyFacts: optsFixedShapeArrayElementPolyFacts(opts),
					EntryGuardedArgs:      optsFixedShapeEntryGuards(opts),
				})(fn)
			},
		},
	}
}

func tier2InlineCallModules(globals map[string]*vm.FuncProto, maxSize int) []Tier2OptimizerModule {
	modules := []Tier2OptimizerModule{
		tier2PassModule("FieldShapeCallSplitPreInline", Tier2PhaseInlineCall, FieldShapeCallSplitPreInlinePass),
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
					Globals:                  globals,
					MaxSize:                  maxSize,
					MaxRecursion:             8,
					MaxCumulativeSize:        120,
					MaxHotLoopCumulativeSize: 600,
					PreserveSelfCalls:        staticallyCallsOnlySelf(fn.Proto),
				}
				out, err := InlinePassWith(config)(fn)
				if ctx != nil {
					ctx.InlineApplied = err == nil
				}
				return out, err
			},
		},
		tier2PostInlinePassModule("SimplifyPhis (post-inline)", SimplifyPhisPass),
		tier2PostInlinePassModule("SourceFeedbackRefresh (post-inline)", SourceFeedbackRefreshPass),
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
		{
			Name:  "FixedShapeTableFacts (post-inline)",
			Phase: Tier2PhaseInlineCall,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				if ctx == nil || !ctx.InlineApplied {
					return fn, nil
				}
				return FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{
					Globals:               globals,
					ArgFacts:              optsFixedShapeArgFacts(opts),
					ArgPolyFacts:          optsFixedShapeArgPolyFacts(opts),
					ArrayElementArgFacts:  optsFixedShapeArrayElementArgFacts(opts),
					ArrayElementPolyFacts: optsFixedShapeArrayElementPolyFacts(opts),
					EntryGuardedArgs:      optsFixedShapeEntryGuards(opts),
				})(fn)
			},
		},
	}
	return modules
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
				globalArrayFacts := mergeGlobalArrayElementFacts(fn.GlobalArrayElementFacts, collectStableGlobalArrayElementFacts(fn))
				fn.GlobalArrayElementFacts = cloneFixedShapeTableFactMap(globalArrayFacts)
				return AnnotateCallABIsPass(CallABIAnnotationConfig{
					Globals:                 ctxGlobals(ctx),
					NumericGlobalValues:     fn.NumericGlobalValues,
					GlobalArrayElementFacts: globalArrayFacts,
				})(fn)
			},
		},
		tier2PassModule("CallReturnProjection", Tier2PhaseCallLower, CallReturnProjectionPass),
		tier2PassModule("ModularCallFloorReduce", Tier2PhaseCallLower, ModularCallFloorReducePass),
		tier2PassModule("CallResultRangeGuard", Tier2PhaseCallLower, CallResultRangeGuardPass),
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

func optsNumericGlobalValuesByName(fn *Function, opts *Tier2PipelineOpts) map[string]runtime.Value {
	if fn == nil || fn.Proto == nil || opts == nil || len(opts.GlobalConstValues) == 0 {
		return nil
	}
	out := make(map[string]runtime.Value)
	for constIdx, v := range opts.GlobalConstValues {
		if constIdx < 0 || constIdx >= len(fn.Proto.Constants) {
			continue
		}
		c := fn.Proto.Constants[constIdx]
		if !c.IsString() || (!v.IsInt() && !v.IsFloat()) {
			continue
		}
		out[c.Str()] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func tier2PostRewriteModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		tier2PassModule("CallReturnProjection (post-rewrite)", Tier2PhasePostRewrite, CallReturnProjectionPass),
		tier2PassModule("ModularCallFloorReduce (post-rewrite)", Tier2PhasePostRewrite, ModularCallFloorReducePass),
		tier2PassModule("CallResultRangeGuard (post-rewrite)", Tier2PhasePostRewrite, CallResultRangeGuardPass),
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

func tier2FinalCallModules(protocolGlobals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	modules := []Tier2OptimizerModule{
		{
			Name:  "CallABI (final)",
			Phase: Tier2PhaseFinalCall,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				globalArrayFacts := mergeGlobalArrayElementFacts(fn.GlobalArrayElementFacts, collectStableGlobalArrayElementFacts(fn))
				fn.GlobalArrayElementFacts = cloneFixedShapeTableFactMap(globalArrayFacts)
				return AnnotateCallABIsPass(CallABIAnnotationConfig{
					Globals:                 ctxGlobals(ctx),
					NumericGlobalValues:     fn.NumericGlobalValues,
					GlobalArrayElementFacts: globalArrayFacts,
				})(fn)
			},
		},
		{
			Name:  "WholeCallKernelExit (final)",
			Phase: Tier2PhaseFinalCall,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return WholeCallKernelExitPass(protocolGlobals)(fn)
			},
		},
		tier2PassModule("CallReturnProjection (final)", Tier2PhaseFinalCall, CallReturnProjectionPass),
		tier2PassModule("ModularCallFloorReduce (final)", Tier2PhaseFinalCall, ModularCallFloorReducePass),
		tier2PassModule("CallResultRangeGuard (final)", Tier2PhaseFinalCall, CallResultRangeGuardPass),
		tier2PassModule("FieldCallPolyLenFusion", Tier2PhaseFinalCall, FieldCallPolyLenFusionPass),
		tier2PassModule("RangeAnalysis (post-final-call)", Tier2PhaseFinalCall, RangeAnalysisPass),
	}
	if os.Getenv("GSCRIPT_FIELD_SHAPE_SPLIT") == "1" {
		modules = append(modules, tier2PassModule("FieldShapeCallSplit (experimental)", Tier2PhaseFinalCall, FieldShapeCallSplitPass))
	}
	return modules
}
