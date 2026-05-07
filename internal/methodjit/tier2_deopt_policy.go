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
	}
	var recompile Tier2RecompilePolicy
	if recompile.ShouldRefreshProfile(cf, current) {
		action.Kind = Tier2DeoptRefreshAndFallback
		action.Reason = "tier2: runtime deopt after feedback matured"
	}
	return action
}
