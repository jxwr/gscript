//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sort"

	"github.com/gscript/gscript/internal/vm"
)

type Tier2SpeculationState struct {
	ProtoName       string `json:"proto_name"`
	ProtoID         string `json:"proto_id"`
	Compiled        bool   `json:"compiled"`
	Failed          bool   `json:"failed"`
	FailReason      string `json:"fail_reason,omitempty"`
	VersionHash     string `json:"version_hash,omitempty"`
	GuardCount      int    `json:"guard_count,omitempty"`
	SuppressedCount int    `json:"suppressed_count,omitempty"`
	SuppressedPCs   []int  `json:"suppressed_pcs,omitempty"`
}

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
