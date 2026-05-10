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
