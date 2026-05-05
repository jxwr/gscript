//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

func (tm *TieringManager) tier2CompiledFor(proto *vm.FuncProto) (*CompiledFunction, bool) {
	if tm == nil || proto == nil {
		return nil, false
	}
	cf, ok := tm.tier2Compiled[proto]
	return cf, ok
}

func (tm *TieringManager) tier2HasFailed(proto *vm.FuncProto) bool {
	return tm != nil && proto != nil && tm.tier2Failed[proto]
}

func (tm *TieringManager) markTier2Compiled(proto *vm.FuncProto, cf *CompiledFunction) {
	if tm == nil || proto == nil || cf == nil {
		return
	}
	tm.tier2Compiled[proto] = cf
	tm.installTier2(proto, cf)
}

func (tm *TieringManager) markTier2Failed(proto *vm.FuncProto, reason string) {
	if tm == nil || proto == nil {
		return
	}
	tm.tier2Failed[proto] = true
	if reason == "" {
		return
	}
	if tm.tier2FailReason == nil {
		tm.tier2FailReason = make(map[*vm.FuncProto]string)
	}
	tm.tier2FailReason[proto] = reason
}

func (tm *TieringManager) clearTier2Install(proto *vm.FuncProto) {
	if proto == nil {
		return
	}
	delete(tm.tier2Compiled, proto)
	proto.Tier2Promoted = false
	clearFuncProtoDirectEntries(proto)
	proto.Tier2GlobalCachePtr = 0
	proto.Tier2GlobalCacheGenPtr = 0
	proto.Tier2GlobalIndexPtr = 0
}
