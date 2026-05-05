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

func (s *TierStateStore) hasFailed(proto *vm.FuncProto) bool {
	return s != nil && proto != nil && s.failed[proto]
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

func (tm *TieringManager) markTier2Compiled(proto *vm.FuncProto, cf *CompiledFunction) {
	if tm == nil || proto == nil || cf == nil {
		return
	}
	tm.ensureTierStateStore()
	tm.tierState.markCompiled(proto, cf)
	tm.installTier2(proto, cf)
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
	proto.Tier2Promoted = false
	clearFuncProtoDirectEntries(proto)
	proto.Tier2GlobalCachePtr = 0
	proto.Tier2GlobalCacheGenPtr = 0
	proto.Tier2GlobalIndexPtr = 0
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
