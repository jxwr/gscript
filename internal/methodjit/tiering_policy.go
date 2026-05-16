//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

type GateSeverity string

const (
	GateSeverityInfo  GateSeverity = "info"
	GateSeverityBlock GateSeverity = "block"
	GateSeverityForce GateSeverity = "force"
)

type GateResult struct {
	Allowed  bool
	Gate     string
	Reason   string
	Op       Op
	Severity GateSeverity
}

func allowGate(gate, reason string) GateResult {
	return GateResult{Allowed: true, Gate: gate, Reason: reason, Severity: GateSeverityInfo}
}

func blockGate(gate, reason string) GateResult {
	return GateResult{Allowed: false, Gate: gate, Reason: reason, Severity: GateSeverityBlock}
}

func blockGateOp(gate, reason string, op Op) GateResult {
	return GateResult{Allowed: false, Gate: gate, Reason: reason, Op: op, Severity: GateSeverityBlock}
}

func forceGate(gate, reason string) GateResult {
	return GateResult{Allowed: true, Gate: gate, Reason: reason, Severity: GateSeverityForce}
}

type TieringAction string

const (
	TieringActionReturnCompiled      TieringAction = "return_compiled"
	TieringActionStayInterpreted     TieringAction = "stay_interpreted"
	TieringActionStructuralKernel    TieringAction = "structural_kernel"
	TieringActionFixedTableBuilder   TieringAction = "fixed_table_builder"
	TieringActionDisableTier0        TieringAction = "disable_tier0"
	TieringActionUseTier1            TieringAction = "use_tier1"
	TieringActionUseTier1Tier2Failed TieringAction = "use_tier1_tier2_failed"
	TieringActionPromoteTier2        TieringAction = "promote_tier2"
)

type PromotionReason string

const (
	PromotionReasonCachedCompiled      PromotionReason = "cached_compiled"
	PromotionReasonBelowTier1Threshold PromotionReason = "below_tier1_threshold"
	PromotionReasonStructuralKernel    PromotionReason = "structural_kernel"
	PromotionReasonFixedTableBuilder   PromotionReason = "fixed_table_builder"
	PromotionReasonTier0Policy         PromotionReason = "tier0_policy"
	PromotionReasonNotReadyForTier2    PromotionReason = "not_ready_for_tier2"
	PromotionReasonTier2Failed         PromotionReason = "tier2_failed"
	PromotionReasonSmartTier2          PromotionReason = "smart_tier2"
	PromotionReasonNativeLoopDriver    PromotionReason = "native_loop_driver"
	PromotionReasonRecursivePartition  PromotionReason = "recursive_partition_table_mutation"
	PromotionReasonLoopCallSuppressed  PromotionReason = "loop_call_suppressed"
	PromotionReasonFeedbackRefresh     PromotionReason = "feedback_refresh"
)

type PromotionDecision struct {
	Action                       TieringAction
	Reason                       PromotionReason
	Gate                         GateResult
	Compiled                     *CompiledFunction
	Kernel                       tieringKernelDecision
	Tier0Disable                 tier0DisableDecision
	SuppressedRecursivePartition bool
	PromoteTier2                 bool
}

type PromotionPolicyState struct {
	Manager            *TieringManager
	Compiled           *CompiledFunction
	Tier2Failed        bool
	RecompileRequested bool
}

type PromotionPolicy struct{}

func (p PromotionPolicy) Decide(proto *vm.FuncProto, profile FuncProfile, state PromotionPolicyState) PromotionDecision {
	tm := state.Manager
	if state.Compiled != nil {
		return PromotionDecision{
			Action:   TieringActionReturnCompiled,
			Reason:   PromotionReasonCachedCompiled,
			Gate:     allowGate("tier2_cache", "already compiled at Tier 2"),
			Compiled: state.Compiled,
		}
	}
	if tm.shouldSuppressRecursivePartitionTableMutationTier2(proto, profile) {
		return tier0PolicyDecision("Tier0RecursivePartitionTableMutation", "stay_tier0_recursive_partition_table_mutation", "recursive_partition_table_mutation")
	}
	if d, ok := tm.structuralKernelTieringDecision(proto); ok {
		return PromotionDecision{
			Action: TieringActionStructuralKernel,
			Reason: PromotionReasonStructuralKernel,
			Gate:   blockGate("StructuralKernel", d.reason),
			Kernel: d,
		}
	}
	if !state.Tier2Failed && tm.shouldPromoteNativeLoopDriver(proto, profile) {
		return PromotionDecision{
			Action:       TieringActionPromoteTier2,
			Reason:       PromotionReasonNativeLoopDriver,
			Gate:         forceGate("NativeLoopDriver", "native loop driver should enter Tier 2"),
			PromoteTier2: true,
		}
	}
	if proto.CallCount < BaselineCompileThreshold {
		return PromotionDecision{
			Action: TieringActionStayInterpreted,
			Reason: PromotionReasonBelowTier1Threshold,
			Gate:   blockGate("Tier1Threshold", "below baseline compile threshold"),
		}
	}
	if !state.Tier2Failed && qualifiesForFixedRecursiveTableBuilder(proto) {
		return PromotionDecision{
			Action: TieringActionFixedTableBuilder,
			Reason: PromotionReasonFixedTableBuilder,
			Gate:   forceGate("FixedRecursiveTableBuilder", "compile fixed recursive table builder"),
		}
	}
	if shouldStayTier0CoroutineRuntime(proto, profile) {
		return tier0PolicyDecision("Tier0CoroutineRuntime", "stay_tier0_coroutine_runtime", "coroutine_runtime")
	}
	if shouldStayTier0StringTokenLoop(proto, profile) {
		return tier0PolicyDecision("Tier0StringTokenLoop", "stay_tier0_string_token_loop", "string_token_loop")
	}
	if shouldStayTier0ForProto(proto, profile) {
		return tier0PolicyDecision("Tier0Profile", "stay_tier0_profile", "jit_disabled")
	}
	if shouldStayTier0RecursiveTableWalker(proto, profile) {
		return tier0PolicyDecision("Tier0RecursiveTableWalker", "stay_tier0_recursive_table_walker", "jit_disabled")
	}
	if callee, ok := tm.tier0OnlyLoopCallee(proto, profile); ok {
		return PromotionDecision{
			Action: TieringActionDisableTier0,
			Reason: PromotionReasonTier0Policy,
			Gate:   blockGate("Tier0LoopCallee", "tier1 driver calls tier0-only loop callee"),
			Tier0Disable: tier0DisableDecision{
				reason:         "tier1_driver_tier0_loop_callee",
				fallbackReason: "driver_tier0_loop_callee",
				callee:         callee,
			},
		}
	}
	if state.RecompileRequested && !state.Tier2Failed {
		return PromotionDecision{
			Action:       TieringActionPromoteTier2,
			Reason:       PromotionReasonFeedbackRefresh,
			Gate:         forceGate("FeedbackRefresh", "exit profile requested refreshed Tier 2 compilation"),
			PromoteTier2: true,
		}
	}

	promoteTier2 := shouldPromoteTier2(proto, profile, proto.CallCount)
	suppressedRecursivePartition := tm.shouldSuppressRecursivePartitionTableMutationTier2(proto, profile)
	reason := PromotionReasonNotReadyForTier2
	gate := blockGate("SmartTiering", "not ready for Tier 2")
	if promoteTier2 {
		reason = PromotionReasonSmartTier2
		gate = allowGate("SmartTiering", "profile selected Tier 2")
	}
	if promoteTier2 && tm.shouldSuppressLoopCallTier2(proto, profile) {
		promoteTier2 = false
		reason = PromotionReasonLoopCallSuppressed
		gate = blockGate("LoopCallTier2", "loop call path remains better at Tier 1")
	}
	if promoteTier2 && suppressedRecursivePartition {
		promoteTier2 = false
		reason = PromotionReasonRecursivePartition
		gate = blockGate("RecursivePartitionTableMutation", "recursive partition table mutation")
	}
	if !promoteTier2 {
		return PromotionDecision{
			Action:                       TieringActionUseTier1,
			Reason:                       reason,
			Gate:                         gate,
			SuppressedRecursivePartition: suppressedRecursivePartition,
			PromoteTier2:                 false,
		}
	}
	if state.Tier2Failed {
		return PromotionDecision{
			Action:       TieringActionUseTier1Tier2Failed,
			Reason:       PromotionReasonTier2Failed,
			Gate:         blockGate("Tier2Failed", "previous Tier 2 compilation failed"),
			PromoteTier2: true,
		}
	}
	return PromotionDecision{
		Action:                       TieringActionPromoteTier2,
		Reason:                       reason,
		Gate:                         gate,
		SuppressedRecursivePartition: suppressedRecursivePartition,
		PromoteTier2:                 true,
	}
}

func tier0PolicyDecision(gate, reason, fallbackReason string) PromotionDecision {
	return PromotionDecision{
		Action: TieringActionDisableTier0,
		Reason: PromotionReasonTier0Policy,
		Gate:   blockGate(gate, reason),
		Tier0Disable: tier0DisableDecision{
			reason:         reason,
			fallbackReason: fallbackReason,
		},
	}
}
