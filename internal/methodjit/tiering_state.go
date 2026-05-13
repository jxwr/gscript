//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

type TierStateStore struct {
	compiled map[*vm.FuncProto]*CompiledFunction
	failed   map[*vm.FuncProto]bool
	reasons  map[*vm.FuncProto]string
}

func newTierStateStore(compiled map[*vm.FuncProto]*CompiledFunction, failed map[*vm.FuncProto]bool, reasons map[*vm.FuncProto]string) *TierStateStore {
	return &TierStateStore{
		compiled: compiled,
		failed:   failed,
		reasons:  reasons,
	}
}

func (s *TierStateStore) compiledFor(proto *vm.FuncProto) (*CompiledFunction, bool) {
	if s == nil || proto == nil {
		return nil, false
	}
	cf, ok := s.compiled[proto]
	return cf, ok
}

func (s *TierStateStore) compiledSnapshot() map[*vm.FuncProto]*CompiledFunction {
	out := make(map[*vm.FuncProto]*CompiledFunction)
	if s == nil {
		return out
	}
	for proto, cf := range s.compiled {
		out[proto] = cf
	}
	return out
}

func (s *TierStateStore) forEachCompiled(fn func(*vm.FuncProto, *CompiledFunction)) {
	if s == nil || fn == nil {
		return
	}
	for proto, cf := range s.compiled {
		fn(proto, cf)
	}
}

func (s *TierStateStore) hasFailed(proto *vm.FuncProto) bool {
	return s != nil && proto != nil && s.failed[proto]
}

func (s *TierStateStore) failReason(proto *vm.FuncProto) string {
	if s == nil || proto == nil {
		return ""
	}
	return s.reasons[proto]
}

func (s *TierStateStore) failReasonSnapshot() map[*vm.FuncProto]string {
	out := make(map[*vm.FuncProto]string)
	if s == nil {
		return out
	}
	for proto, reason := range s.reasons {
		out[proto] = reason
	}
	return out
}

func (s *TierStateStore) markCompiled(proto *vm.FuncProto, cf *CompiledFunction) {
	if s == nil || proto == nil || cf == nil {
		return
	}
	s.compiled[proto] = cf
}

func (s *TierStateStore) markFailed(proto *vm.FuncProto, reason string) {
	if s == nil || proto == nil {
		return
	}
	s.failed[proto] = true
	if reason == "" {
		return
	}
	if s.reasons == nil {
		s.reasons = make(map[*vm.FuncProto]string)
	}
	s.reasons[proto] = reason
}

func (s *TierStateStore) clearCompiled(proto *vm.FuncProto) {
	if s == nil || proto == nil {
		return
	}
	delete(s.compiled, proto)
}

func (s *TierStateStore) markJITDisabled(proto *vm.FuncProto) {
	if proto == nil {
		return
	}
	proto.JITDisabled = true
}

func (s *TierStateStore) temporarilyDisableJIT(proto *vm.FuncProto, body func()) {
	if proto == nil || body == nil {
		return
	}
	oldDisabled := proto.JITDisabled
	proto.JITDisabled = true
	defer func() {
		proto.JITDisabled = oldDisabled
	}()
	body()
}

func (tm *TieringManager) tier2CompiledFor(proto *vm.FuncProto) (*CompiledFunction, bool) {
	if tm == nil || proto == nil {
		return nil, false
	}
	tm.ensureTierStateStore()
	return tm.tierState.compiledFor(proto)
}

func (tm *TieringManager) tier2HasFailed(proto *vm.FuncProto) bool {
	if tm == nil || proto == nil {
		return false
	}
	tm.ensureTierStateStore()
	return tm.tierState.hasFailed(proto)
}

func (tm *TieringManager) tier2FailReasonFor(proto *vm.FuncProto) string {
	if tm == nil || proto == nil {
		return ""
	}
	tm.ensureTierStateStore()
	return tm.tierState.failReason(proto)
}

func (tm *TieringManager) tier2CompiledSnapshot() map[*vm.FuncProto]*CompiledFunction {
	if tm == nil {
		return nil
	}
	tm.ensureTierStateStore()
	return tm.tierState.compiledSnapshot()
}

func (tm *TieringManager) forEachTier2Compiled(fn func(*vm.FuncProto, *CompiledFunction)) {
	if tm == nil || fn == nil {
		return
	}
	tm.ensureTierStateStore()
	tm.tierState.forEachCompiled(fn)
}

func (tm *TieringManager) tier2FailReasonSnapshot() map[*vm.FuncProto]string {
	if tm == nil {
		return nil
	}
	tm.ensureTierStateStore()
	return tm.tierState.failReasonSnapshot()
}

func (tm *TieringManager) markTier2Compiled(proto *vm.FuncProto, cf *CompiledFunction) {
	if tm == nil || proto == nil || cf == nil {
		return
	}
	profile := tm.currentTier2SpeculationProfile(proto)
	cf.SpeculationSnapshot = profile.Snapshot
	if cf.SpecializationVersion.Hash == 0 {
		cf.SpecializationVersion = profile.Version
	}
	tm.ensureTierStateStore()
	tm.tierState.markCompiled(proto, cf)
	tm.installTier2(proto, cf)
	tm.registerTier2SpecDependencies(proto, cf)
	tm.queueSpecDependentsForRefresh(proto, "spec_dependency_compiled")
}

func (tm *TieringManager) markTier2Failed(proto *vm.FuncProto, reason string) {
	if tm == nil || proto == nil {
		return
	}
	tm.ensureTierStateStore()
	tm.tierState.markFailed(proto, reason)
}

func (tm *TieringManager) clearTier2Install(proto *vm.FuncProto) {
	if tm == nil || proto == nil {
		return
	}
	tm.ensureTierStateStore()
	tm.tierState.clearCompiled(proto)
	tm.clearTier2SpecDependenciesForCaller(proto)
	proto.Tier2Promoted = false
	clearFuncProtoDirectEntries(proto)
	proto.Tier2GlobalCachePtr = 0
	proto.Tier2GlobalCacheGenPtr = 0
	proto.Tier2GlobalIndexPtr = 0
}

func (tm *TieringManager) markJITDisabled(proto *vm.FuncProto) {
	if tm == nil || proto == nil {
		return
	}
	tm.ensureTierStateStore()
	tm.tierState.markJITDisabled(proto)
}

func (tm *TieringManager) withJITTemporarilyDisabled(proto *vm.FuncProto, body func()) {
	if tm == nil || proto == nil || body == nil {
		return
	}
	tm.ensureTierStateStore()
	tm.tierState.temporarilyDisableJIT(proto, body)
}

func (tm *TieringManager) ensureTierStateStore() {
	if tm.tierState != nil {
		return
	}
	if tm.tier2Compiled == nil {
		tm.tier2Compiled = make(map[*vm.FuncProto]*CompiledFunction)
	}
	if tm.tier2Failed == nil {
		tm.tier2Failed = make(map[*vm.FuncProto]bool)
	}
	if tm.tier2FailReason == nil {
		tm.tier2FailReason = make(map[*vm.FuncProto]string)
	}
	tm.tierState = newTierStateStore(tm.tier2Compiled, tm.tier2Failed, tm.tier2FailReason)
}
