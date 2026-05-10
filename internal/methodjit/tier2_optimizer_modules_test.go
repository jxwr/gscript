package methodjit

import "testing"

func assertTier2ModuleOrder(t *testing.T, mods []Tier2OptimizerModule, phase Tier2OptimizerPhase, want []string) {
	t.Helper()
	if len(mods) != len(want) {
		t.Fatalf("%s module count=%d want %d: %v", phase, len(mods), len(want), tier2ModuleNames(mods))
	}
	for i, wantName := range want {
		if mods[i].Phase != phase || mods[i].Name != wantName {
			t.Fatalf("module[%d]=%s/%s want %s/%s; all=%v", i, mods[i].Phase, mods[i].Name, phase, wantName, tier2ModuleNames(mods))
		}
	}
}

func tier2ModuleNames(mods []Tier2OptimizerModule) []string {
	names := make([]string, 0, len(mods))
	for _, mod := range mods {
		names = append(names, mod.Name)
	}
	return names
}

func TestTier2TableObjectPreparationModuleOrder(t *testing.T) {
	mods := tier2TableObjectPreparationModules(nil)
	want := []string{
		"TablePreallocHint",
		"TypeSpecialize (post-table-prealloc)",
		"FixedShapeTableFacts",
		"LoadElimination",
		"FieldLenFold",
		"EscapeAnalysis",
		"FixedTableConstructorLowering",
		"TablePreallocHint (post-fixed-table-lowering)",
		"EscapeAnalysis (post-fixed-table-lowering)",
		"RedundantGuardElimination",
	}
	assertTier2ModuleOrder(t, mods, Tier2PhaseTableObjectPrep, want)
}

func TestTier2OptimizerPlanPhaseOrder(t *testing.T) {
	plan := newTier2OptimizerPlan(&Tier2OptimizerContext{InlineMaxSize: 40})
	want := []Tier2OptimizerPhase{
		Tier2PhaseEarlyCanonical,
		Tier2PhaseInlineCall,
		Tier2PhaseCallLower,
		Tier2PhaseStringNative,
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
	}
	if len(plan.Phases) != len(want) {
		t.Fatalf("phase count=%d want %d: %v", len(plan.Phases), len(want), plan.Phases)
	}
	for i, phase := range want {
		if plan.Phases[i] != phase {
			t.Fatalf("phase[%d]=%s want %s; all=%v", i, plan.Phases[i], phase, plan.Phases)
		}
	}
}

func TestTier2OptimizerPlanCoversModulePhases(t *testing.T) {
	plan := newTier2OptimizerPlan(&Tier2OptimizerContext{InlineMaxSize: 40})
	phases := make(map[Tier2OptimizerPhase]bool, len(plan.Phases))
	for _, phase := range plan.Phases {
		if phases[phase] {
			t.Fatalf("duplicate phase in plan: %s", phase)
		}
		phases[phase] = true
	}
	names := make(map[string]bool, len(plan.Modules))
	for _, module := range plan.Modules {
		if !phases[module.Phase] {
			t.Fatalf("module %s uses phase %s missing from plan", module.Name, module.Phase)
		}
		if names[module.Name] {
			t.Fatalf("duplicate module name: %s", module.Name)
		}
		names[module.Name] = true
	}
}

func TestTier2EarlyCanonicalModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2EarlyCanonicalModules(nil), Tier2PhaseEarlyCanonical, []string{
		"SimplifyPhis",
		"TypeSpecialize",
		"Intrinsic",
		"GlobalConstSpecialization",
		"TypeSpecialize (post-intrinsic)",
		"FixedShapeTableFacts (pre-inline)",
	})
}

func TestTier2InlineCallModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2InlineCallModules(nil, 40), Tier2PhaseInlineCall, []string{
		"Inline",
		"SimplifyPhis (post-inline)",
		"Intrinsic (post-inline)",
		"TypeSpecialize (post-inline)",
	})
}

func TestTier2CallLoweringModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2CallLoweringModules(nil), Tier2PhaseCallLower, []string{
		"CallABI",
		"CallReturnProjection",
		"ConstProp",
		"ProtocolConstCallFold",
		"WholeCallKernelExit",
	})
}

func TestTier2StringNativeModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2StringNativeModules(), Tier2PhaseStringNative, []string{
		"StringNativeCleanup",
	})
}

func TestTier2PostRewriteModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2PostRewriteModules(), Tier2PhasePostRewrite, []string{
		"CallReturnProjection (post-rewrite)",
		"DCE",
		"TypeSpecialize (post-escape)",
	})
}

func TestTier2NumericModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2NumericModules(), Tier2PhaseNumeric, []string{
		"LoopBoundRangeGuard",
		"RangeAnalysis",
		"OverflowBoxing",
		"IntExactDivision",
		"RangeAnalysis (post-IntExactDivision)",
		"ModRangeSimplify",
		"DCE (post-ModRangeSimplify)",
		"ModZeroCompare",
		"DCE (post-ModZeroCompare)",
	})
}

func TestTier2TableNativeLoweringModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2TableArrayNativeLoweringModules(), Tier2PhaseTableArrayLower,
		[]string{"TableArrayLower", "TableArrayLoadTypeSpecialize", "TableArrayNestedLoad"})
	assertTier2ModuleOrder(t, tier2TableFieldNativeLoweringModules(), Tier2PhaseTableFieldLower,
		[]string{"FieldSvalsLower", "ProfiledStringLenFold", "RangeAnalysis (post-TableFieldLower)", "TableArrayStoreLower", "DCE (post-TableArrayStoreLower)"})
	assertTier2ModuleOrder(t, tier2TableLoopKernelModules(), Tier2PhaseLoopKernel,
		[]string{"BoolTableFillLoop", "TableArrayStoreLoopVersion", "TableIntArrayKernel", "BoolTableCountLoop"})
	assertTier2ModuleOrder(t, tier2TableLoopPostLoadElimModules(), Tier2PhaseLoopKernel,
		[]string{"TableArraySwapFusion", "TableIntArrayKernel (post-swap-fusion)"})
}

func TestTier2MatrixNativeLoweringModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2MatrixNativeLoweringModules(), Tier2PhaseMatrixNative, []string{
		"DenseMatrixNestedLoadLower",
		"MatrixLower",
		"LoadElimination (post-MatrixLower)",
		"MatrixRowPtrFactoring",
		"MatrixUnitStride",
	})
}

func TestTier2FloatNumericModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2FloatNumericModules(), Tier2PhaseFloatNumeric, []string{
		"FMAFusion",
		"FloatStrengthReduction",
		"FMAFusion (post-FloatStrengthReduction)",
	})
}

func TestTier2LoopKernelModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2LoopKernelModules(), Tier2PhaseLoopKernel, []string{
		"LICM",
		"BoolTableFillLoop",
		"TableArrayStoreLoopVersion",
		"TableIntArrayKernel",
		"BoolTableCountLoop",
		"FieldNumToFloatFusion (post-LICM)",
		"LoadElimination (post-LICM)",
		"TableArraySwapFusion",
		"TableIntArrayKernel (post-swap-fusion)",
		"DCE (post-LICM LoadElim)",
	})
}

func TestTier2LoopPostModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2LoopPostModules(), Tier2PhaseLoopPost, []string{
		"UnrollAndJam",
		"QuadraticStepStrengthReduction",
		"RangeAnalysis (post-UnrollAndJam)",
		"DCE (post-UnrollAndJam)",
		"LoopRegionVersioning",
		"ScalarPromotion",
		"TableArrayDataPtrFact",
	})
}

func TestTier2FinalCallModuleOrder(t *testing.T) {
	assertTier2ModuleOrder(t, tier2FinalCallModules(nil), Tier2PhaseFinalCall, []string{
		"WholeCallKernelExit (final)",
		"CallReturnProjection (final)",
	})
}
