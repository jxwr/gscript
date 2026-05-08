//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sort"

	"github.com/gscript/gscript/internal/vm"
)

type Tier2SpeculationState struct {
	ProtoName            string                 `json:"proto_name"`
	ProtoID              string                 `json:"proto_id"`
	Compiled             bool                   `json:"compiled"`
	Failed               bool                   `json:"failed"`
	FailReason           string                 `json:"fail_reason,omitempty"`
	VersionHash          string                 `json:"version_hash,omitempty"`
	GuardCount           int                    `json:"guard_count,omitempty"`
	SuppressedCount      int                    `json:"suppressed_count,omitempty"`
	SuppressedPCs        []int                  `json:"suppressed_pcs,omitempty"`
	SuppressedKinds      map[string]int         `json:"suppressed_kinds,omitempty"`
	GuardFailures        map[string]uint64      `json:"guard_failures,omitempty"`
	ExitCount            uint64                 `json:"exit_count,omitempty"`
	SuppressedGuardExits uint64                 `json:"suppressed_guard_exits,omitempty"`
	QueuedRecompileExits uint64                 `json:"queued_recompile_exits,omitempty"`
	ExitKinds            map[string]uint64      `json:"exit_kinds,omitempty"`
	TopExitName          string                 `json:"top_exit_name,omitempty"`
	TopExitReason        string                 `json:"top_exit_reason,omitempty"`
	TopExitPC            int                    `json:"top_exit_pc,omitempty"`
	TopExitCount         uint64                 `json:"top_exit_count,omitempty"`
	NextAction           Tier2SpeculationAction `json:"next_action,omitempty"`
	NextTarget           Tier2SpeculationTarget `json:"next_target,omitempty"`
	NextPriority         int                    `json:"next_priority,omitempty"`
}

type Tier2SpeculationAction string

const (
	Tier2SpecActionNone                    Tier2SpeculationAction = ""
	Tier2SpecActionTier2Failed             Tier2SpeculationAction = "tier2_failed"
	Tier2SpecActionRefreshQueued           Tier2SpeculationAction = "refresh_queued"
	Tier2SpecActionSuppressedGuardResidual Tier2SpeculationAction = "suppressed_guard_residual"
	Tier2SpecActionInspectHotExit          Tier2SpeculationAction = "inspect_hot_exit"
	Tier2SpecActionGuardRelaxed            Tier2SpeculationAction = "guard_relaxed"
	Tier2SpecActionMonitor                 Tier2SpeculationAction = "monitor"
)

type Tier2SpeculationTarget string

const (
	Tier2SpecTargetNone               Tier2SpeculationTarget = ""
	Tier2SpecTargetCallSpecialization Tier2SpeculationTarget = "call_specialization"
	Tier2SpecTargetTableFieldExit     Tier2SpeculationTarget = "table_field_exit"
	Tier2SpecTargetTableAccessExit    Tier2SpeculationTarget = "table_access_exit"
	Tier2SpecTargetTableExit          Tier2SpeculationTarget = "table_exit"
	Tier2SpecTargetGuardPolicy        Tier2SpeculationTarget = "guard_policy"
	Tier2SpecTargetDeoptPolicy        Tier2SpeculationTarget = "deopt_policy"
	Tier2SpecTargetGlobalAccessExit   Tier2SpeculationTarget = "global_access_exit"
	Tier2SpecTargetOpExit             Tier2SpeculationTarget = "op_exit"
)

func (tm *TieringManager) Tier2SpeculationStateSnapshot() []Tier2SpeculationState {
	if tm == nil {
		return nil
	}
	tm.ensureTierStateStore()
	protos := make(map[*vm.FuncProto]bool)
	for proto := range tm.tier2Compiled {
		protos[proto] = true
	}
	for proto := range tm.tier2Failed {
		protos[proto] = true
	}
	for proto := range tm.tier2GuardSuppress {
		protos[proto] = true
	}
	for proto := range tm.tier2GuardFailures {
		protos[proto] = true
	}
	out := make([]Tier2SpeculationState, 0, len(protos))
	for proto := range protos {
		if proto == nil {
			continue
		}
		state := Tier2SpeculationState{
			ProtoName:  traceProtoName(proto),
			ProtoID:    traceProtoID(proto),
			Failed:     tm.tier2HasFailed(proto),
			FailReason: tm.tier2FailReasonFor(proto),
		}
		if cf, ok := tm.tier2CompiledFor(proto); ok && cf != nil {
			state.Compiled = true
			state.VersionHash = fmt.Sprintf("%x", cf.SpecializationVersion.Hash)
			state.GuardCount = cf.SpecializationVersion.GuardCount
		}
		if suppressed := tm.tier2SuppressedGuards(proto); len(suppressed) > 0 {
			for pc, ok := range suppressed {
				if ok {
					state.SuppressedPCs = append(state.SuppressedPCs, pc)
				}
			}
			sort.Ints(state.SuppressedPCs)
			state.SuppressedCount = len(state.SuppressedPCs)
		}
		if suppressedKinds := tm.tier2SuppressedGuardKinds(proto); len(suppressedKinds) > 0 {
			state.SuppressedKinds = make(map[string]int)
			for _, kinds := range suppressedKinds {
				for kind, ok := range kinds {
					if ok {
						state.SuppressedKinds[kind]++
					}
				}
			}
		}
		state.GuardFailures = tm.tier2GuardFailureKinds(proto)
		if exits := tm.exitProfile.protoSummary(proto); exits.Total > 0 {
			state.ExitCount = exits.Total
			state.SuppressedGuardExits = exits.SuppressedGuardExits
			state.QueuedRecompileExits = exits.QueuedRecompileExits
			state.ExitKinds = exits.ExitKinds
			if exits.TopExit.Count > 0 {
				state.TopExitName = exits.TopExit.ExitName
				state.TopExitReason = exits.TopExit.Reason
				state.TopExitPC = exits.TopExit.PC
				state.TopExitCount = exits.TopExit.Count
			}
		}
		state.NextAction = tier2SpeculationNextAction(state)
		state.NextTarget = tier2SpeculationNextTarget(state)
		state.NextPriority = tier2SpeculationNextPriority(state)
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProtoName == out[j].ProtoName {
			return out[i].ProtoID < out[j].ProtoID
		}
		return out[i].ProtoName < out[j].ProtoName
	})
	return out
}

func tier2SpeculationNextAction(state Tier2SpeculationState) Tier2SpeculationAction {
	switch {
	case state.Failed:
		return Tier2SpecActionTier2Failed
	case state.QueuedRecompileExits > 0:
		return Tier2SpecActionRefreshQueued
	case state.ExitCount > 0 && state.SuppressedGuardExits == state.ExitCount:
		return Tier2SpecActionSuppressedGuardResidual
	case state.ExitCount > 0:
		return Tier2SpecActionInspectHotExit
	case len(state.GuardFailures) > 0:
		return Tier2SpecActionGuardRelaxed
	case state.Compiled:
		return Tier2SpecActionMonitor
	default:
		return Tier2SpecActionNone
	}
}

func tier2SpeculationNextTarget(state Tier2SpeculationState) Tier2SpeculationTarget {
	if state.NextAction == Tier2SpecActionNone || state.NextAction == Tier2SpecActionMonitor || state.NextAction == Tier2SpecActionTier2Failed {
		return Tier2SpecTargetNone
	}
	switch state.TopExitName {
	case "ExitCallExit":
		return Tier2SpecTargetCallSpecialization
	case "ExitTableExit":
		switch state.TopExitReason {
		case "GetField", "SetField":
			return Tier2SpecTargetTableFieldExit
		case "GetTable", "SetTable":
			return Tier2SpecTargetTableAccessExit
		default:
			return Tier2SpecTargetTableExit
		}
	case "ExitDeopt":
		if exitReasonGuardOp(state.TopExitReason) != "" {
			return Tier2SpecTargetGuardPolicy
		}
		return Tier2SpecTargetDeoptPolicy
	case "ExitGlobalExit":
		return Tier2SpecTargetGlobalAccessExit
	case "ExitOpExit":
		return Tier2SpecTargetOpExit
	default:
		return Tier2SpecTargetNone
	}
}

func tier2SpeculationNextPriority(state Tier2SpeculationState) int {
	actionPriority := tier2SpeculationActionPriority(state.NextAction)
	targetPriority := tier2SpeculationTargetPriority(state.NextTarget)
	if targetPriority > actionPriority {
		return targetPriority
	}
	return actionPriority
}

func tier2SpeculationActionPriority(action Tier2SpeculationAction) int {
	switch action {
	case Tier2SpecActionRefreshQueued:
		return 100
	case Tier2SpecActionInspectHotExit:
		return 80
	case Tier2SpecActionGuardRelaxed:
		return 60
	case Tier2SpecActionSuppressedGuardResidual:
		return 40
	case Tier2SpecActionTier2Failed:
		return 30
	case Tier2SpecActionMonitor:
		return 10
	default:
		return 0
	}
}

func tier2SpeculationTargetPriority(target Tier2SpeculationTarget) int {
	switch target {
	case Tier2SpecTargetCallSpecialization, Tier2SpecTargetTableFieldExit:
		return 90
	case Tier2SpecTargetTableAccessExit, Tier2SpecTargetGuardPolicy:
		return 70
	case Tier2SpecTargetTableExit, Tier2SpecTargetDeoptPolicy, Tier2SpecTargetGlobalAccessExit, Tier2SpecTargetOpExit:
		return 50
	default:
		return 0
	}
}
