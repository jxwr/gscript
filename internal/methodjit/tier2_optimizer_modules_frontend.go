package methodjit

import (
	"os"

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
	if os.Getenv("GSCRIPT_FIELD_SHAPE_SPLIT_PREINLINE") == "1" {
		modules = append([]Tier2OptimizerModule{
			tier2PassModule("FieldShapeCallSplitPreInline", Tier2PhaseInlineCall, FieldShapeCallSplitPreInlinePass),
		}, modules...)
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

func tier2FinalCallModules(protocolGlobals map[string]*vm.FuncProto) []Tier2OptimizerModule {
	modules := []Tier2OptimizerModule{
		{
			Name:  "WholeCallKernelExit (final)",
			Phase: Tier2PhaseFinalCall,
			Run: func(fn *Function, opts *Tier2PipelineOpts) (*Function, error) {
				return WholeCallKernelExitPass(protocolGlobals)(fn)
			},
		},
		tier2PassModule("CallReturnProjection (final)", Tier2PhaseFinalCall, CallReturnProjectionPass),
	}
	if os.Getenv("GSCRIPT_FIELD_SHAPE_SPLIT") == "1" {
		modules = append(modules, tier2PassModule("FieldShapeCallSplit (experimental)", Tier2PhaseFinalCall, FieldShapeCallSplitPass))
	}
	return modules
}
