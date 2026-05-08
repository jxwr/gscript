//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

type Tier2DeoptActionKind string

const (
	Tier2DeoptDisableAndFallback Tier2DeoptActionKind = "disable_and_fallback"
	Tier2DeoptRefreshAndFallback Tier2DeoptActionKind = "refresh_and_fallback"
)

type Tier2DeoptAction struct {
	Kind           Tier2DeoptActionKind
	Reason         string
	PreciseResume  bool
	ResumePC       int
	CurrentProfile Tier2SpecializationProfile
	GuardRelaxedPC int
	GuardRelaxedOp string
	GuardFailCount uint64
}

type Tier2DeoptPolicy struct{}

func (Tier2DeoptPolicy) DecideRuntimeDeopt(proto *vm.FuncProto, cf *CompiledFunction, resumePC int) Tier2DeoptAction {
	current := BuildTier2SpecializationProfile(proto)
	action := Tier2DeoptAction{
		Kind:           Tier2DeoptDisableAndFallback,
		Reason:         "tier2: runtime deopt",
		PreciseResume:  resumePC > 0,
		ResumePC:       resumePC,
		CurrentProfile: current,
		GuardRelaxedPC: -1,
		GuardRelaxedOp: "",
	}
	var recompile Tier2RecompilePolicy
	if recompile.ShouldRefreshProfile(cf, current) {
		action.Kind = Tier2DeoptRefreshAndFallback
		action.Reason = "tier2: runtime deopt after feedback matured"
	}
	return action
}

type Tier2GuardDeoptDecision struct {
	SuppressPC     bool
	SuppressGlobal bool
	Reason         string
}

type Tier2GuardDeoptPolicy struct{}

func (Tier2GuardDeoptPolicy) Decide(meta ExitSiteMeta, failCount uint64) Tier2GuardDeoptDecision {
	decision := Tier2GuardDeoptDecision{
		SuppressPC: true,
		Reason:     "tier2: guard deopt; recompile without unstable guard",
	}
	switch meta.Op {
	case "GuardCalleeProto":
		decision.Reason = "tier2: callee guard deopt; recompile without unstable callsite guard"
	case "GuardConstString":
		decision.Reason = "tier2: const-string guard deopt; recompile without unstable string-key guard"
	case "GuardTableKind":
		decision.Reason = "tier2: table-kind guard deopt; recompile without unstable table-kind guard"
		// Table-kind transitions often invalidate several bytecode sites in the
		// same function. A per-PC retry tends to burn compile attempts before
		// reaching the same generic table path, so relax this guard class once
		// the function has demonstrated instability.
		decision.SuppressGlobal = failCount >= 1
	}
	return decision
}
