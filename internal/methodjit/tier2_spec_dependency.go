//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sort"

	"github.com/gscript/gscript/internal/vm"
)

func specDependencyNames(protos []*vm.FuncProto) []string {
	if len(protos) == 0 {
		return nil
	}
	names := make([]string, 0, len(protos))
	for _, proto := range protos {
		if proto != nil {
			names = append(names, traceProtoName(proto))
		}
	}
	sort.Strings(names)
	return names
}

func specDependencyIDs(protos []*vm.FuncProto) []string {
	if len(protos) == 0 {
		return nil
	}
	ids := make([]string, 0, len(protos))
	for _, proto := range protos {
		if proto != nil {
			ids = append(ids, traceProtoID(proto))
		}
	}
	sort.Strings(ids)
	return ids
}

func sortedSpecDependencyProtos(fn *Function) []*vm.FuncProto {
	if fn == nil {
		return nil
	}
	deps := make(map[*vm.FuncProto]bool, len(fn.SpecDependencyProtos))
	for proto := range fn.SpecDependencyProtos {
		recordSpecDependencyProto(fn, deps, proto)
	}
	for _, cases := range fn.FieldPolyShapeFacts {
		for _, c := range cases {
			recordSpecDependencyProto(fn, deps, c.VMProto)
		}
	}
	if len(deps) == 0 {
		return nil
	}
	out := make([]*vm.FuncProto, 0, len(deps))
	for proto := range deps {
		out = append(out, proto)
	}
	sort.Slice(out, func(i, j int) bool {
		left := traceProtoName(out[i])
		right := traceProtoName(out[j])
		if left == right {
			return fmt.Sprintf("%p", out[i]) < fmt.Sprintf("%p", out[j])
		}
		return left < right
	})
	return out
}

func recordSpecDependencyProto(fn *Function, deps map[*vm.FuncProto]bool, proto *vm.FuncProto) {
	if fn == nil || deps == nil || proto == nil || proto == fn.Proto {
		return
	}
	deps[proto] = true
}

func (tm *TieringManager) registerTier2SpecDependencies(caller *vm.FuncProto, cf *CompiledFunction) {
	if tm == nil || caller == nil || cf == nil {
		return
	}
	tm.clearTier2SpecDependenciesForCaller(caller)
	if len(cf.SpecDependencyProtos) == 0 {
		return
	}
	if tm.specDependents == nil {
		tm.specDependents = make(map[*vm.FuncProto]map[*vm.FuncProto]bool)
	}
	for _, callee := range cf.SpecDependencyProtos {
		if callee == nil || callee == caller {
			continue
		}
		callers := tm.specDependents[callee]
		if callers == nil {
			callers = make(map[*vm.FuncProto]bool)
			tm.specDependents[callee] = callers
		}
		callers[caller] = true
	}
	if deps := specDependencyNames(cf.SpecDependencyProtos); len(deps) > 0 {
		tm.traceEvent("tier2_spec_dependencies_registered", "tier2", caller, map[string]any{
			"dependencies":   deps,
			"dependency_ids": specDependencyIDs(cf.SpecDependencyProtos),
		})
	}
}

func (tm *TieringManager) clearTier2SpecDependenciesForCaller(caller *vm.FuncProto) {
	if tm == nil || caller == nil || len(tm.specDependents) == 0 {
		return
	}
	for callee, callers := range tm.specDependents {
		delete(callers, caller)
		if len(callers) == 0 {
			delete(tm.specDependents, callee)
		}
	}
}

func (tm *TieringManager) queueSpecDependentsForRefresh(callee *vm.FuncProto, reason string) {
	if tm == nil || callee == nil || len(tm.specDependents) == 0 {
		return
	}
	if reason == "" {
		reason = "spec_dependency_feedback_matured"
	}
	callers := tm.specDependents[callee]
	if len(callers) == 0 {
		tm.traceEvent("tier2_spec_dependency_no_dependents", "tier2", callee, map[string]any{
			"reason": reason,
		})
		return
	}
	toQueue := make([]*vm.FuncProto, 0, len(callers))
	for caller := range callers {
		if caller != nil && caller != callee {
			toQueue = append(toQueue, caller)
		}
	}
	sort.Slice(toQueue, func(i, j int) bool {
		left := traceProtoName(toQueue[i])
		right := traceProtoName(toQueue[j])
		if left == right {
			return fmt.Sprintf("%p", toQueue[i]) < fmt.Sprintf("%p", toQueue[j])
		}
		return left < right
	})
	for _, caller := range toQueue {
		cf, ok := tm.tier2CompiledFor(caller)
		if !ok || cf == nil {
			continue
		}
		current := tm.currentTier2SpeculationProfile(caller)
		site := Tier2ExitProfileSite{
			Proto:                exitStatsProtoName(caller),
			PC:                   -1,
			ExitCode:             ExitNormal,
			ExitName:             "SpecDependency",
			Reason:               reason,
			VersionHash:          fmt.Sprintf("%x", cf.SpecializationVersion.Hash),
			VersionGuards:        cf.SpecializationVersion.GuardCount,
			RefreshVersionHash:   fmt.Sprintf("%x", current.Version.Hash),
			RefreshVersionGuards: current.Version.GuardCount,
			RefreshGuardDelta:    current.Version.GuardCount - cf.SpecializationVersion.GuardCount,
		}
		if !tm.recompileQueue.enqueue(caller, reason, site) {
			continue
		}
		tm.clearTier2Install(caller)
		tm.traceEvent("tier2_recompile_queued", "tier2", caller, map[string]any{
			"reason":        reason,
			"callee":        traceProtoName(callee),
			"guards_before": cf.SpecializationVersion.GuardCount,
			"guards_after":  current.Version.GuardCount,
			"version_after": fmt.Sprintf("%x", current.Version.Hash),
			"install":       "cleared",
		})
	}
}
