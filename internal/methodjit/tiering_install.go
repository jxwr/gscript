//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

func (tm *TieringManager) installTier2(proto *vm.FuncProto, cf *CompiledFunction) {
	proto.Tier2Promoted = true

	// Publish the generic DirectEntryPtr only when legacy native callers can
	// recover by replaying the call. Tier 2 callers have an ExitNativeCallExit
	// resume loop, so they may use the separate Tier2DirectEntryPtr even when
	// replay from pc=0 would be unsafe.
	if cf != nil && cf.DirectEntryOffset > 0 && cf.Tier2DirectEntrySafe && cf.DirectEntrySafe {
		entry := uintptr(cf.Code.Ptr()) + uintptr(cf.DirectEntryOffset)
		setFuncProtoTier2DirectEntries(proto, entry, entry)
	} else if cf != nil && cf.DirectEntryOffset > 0 && cf.Tier2DirectEntrySafe {
		entry := uintptr(cf.Code.Ptr()) + uintptr(cf.DirectEntryOffset)
		setFuncProtoTier2DirectEntries(proto, 0, entry)
	} else {
		setFuncProtoTier2DirectEntries(proto, 0, 0)
	}
	if cf != nil && cf.LeafEntryOffset > 0 && proto.Tier2LeafNoCall && cf.Tier2DirectEntrySafe {
		setFuncProtoTier2LeafEntry(proto, uintptr(cf.Code.Ptr())+uintptr(cf.LeafEntryOffset))
	} else {
		setFuncProtoTier2LeafEntry(proto, 0)
	}
	if cf != nil && cf.NumericEntryOffset > 0 {
		proto.Tier2NumericEntryPtr = uintptr(cf.Code.Ptr()) + uintptr(cf.NumericEntryOffset)
	} else {
		proto.Tier2NumericEntryPtr = 0
	}
	if cf != nil && cf.TypedEntryOffset > 0 && cf.TypedPeerABI.Eligible {
		proto.Tier2TypedEntryPtr = uintptr(cf.Code.Ptr()) + uintptr(cf.TypedEntryOffset)
		proto.Tier2TypedEntryABI = typedABISignature(cf.TypedPeerABI)
	} else {
		proto.Tier2TypedEntryPtr = 0
		proto.Tier2TypedEntryABI = 0
	}
	if cf != nil && len(cf.GlobalCache) > 0 {
		proto.Tier2GlobalCachePtr = uintptr(unsafe.Pointer(&cf.GlobalCache[0]))
		proto.Tier2GlobalCacheGenPtr = uintptr(unsafe.Pointer(&cf.GlobalCacheGen))
	} else {
		proto.Tier2GlobalCachePtr = 0
		proto.Tier2GlobalCacheGenPtr = 0
	}
	tm.prepareTier2GlobalIndexes(proto, cf)
}

func collectCompiledGlobalConsts(cf *CompiledFunction) map[int]bool {
	if cf == nil {
		return nil
	}
	out := make(map[int]bool, len(cf.GlobalCacheConsts)+len(cf.GlobalGuardConsts)+len(cf.NativeSetGlobals))
	for _, constIdx := range cf.GlobalCacheConsts {
		out[constIdx] = true
	}
	for _, constIdx := range cf.GlobalGuardConsts {
		out[constIdx] = true
	}
	for constIdx := range cf.NativeSetGlobals {
		out[constIdx] = true
	}
	return out
}

func (tm *TieringManager) prepareTier2GlobalIndexes(proto *vm.FuncProto, cf *CompiledFunction) (uintptr, *uint32, uint32, bool) {
	if proto != nil {
		proto.Tier2GlobalIndexPtr = 0
	}
	if tm == nil || tm.callVM == nil || proto == nil || cf == nil || len(proto.Constants) == 0 || !protoSupportsIndexedGlobalProtocol(proto) {
		if cf != nil {
			cf.GlobalIndexByConst = nil
		}
		return 0, nil, 0, false
	}
	globalConsts := collectCompiledGlobalConsts(cf)
	if len(globalConsts) == 0 {
		cf.GlobalIndexByConst = nil
		return 0, nil, 0, false
	}
	if len(cf.GlobalIndexByConst) == len(proto.Constants) {
		proto.Tier2GlobalIndexPtr = uintptr(unsafe.Pointer(&cf.GlobalIndexByConst[0]))
		arrayPtr, verPtr, ver, ok := tm.callVM.Tier2GlobalArrayState()
		if ok {
			return arrayPtr, verPtr, ver, true
		}
		cf.GlobalIndexByConst = nil
		proto.Tier2GlobalIndexPtr = 0
	}
	indices, arrayPtr, verPtr, ver, ok := tm.callVM.PrepareTier2GlobalArray(proto.Constants, globalConsts)
	if !ok {
		cf.GlobalIndexByConst = nil
		return 0, nil, 0, false
	}
	cf.GlobalIndexByConst = indices
	if len(indices) > 0 {
		proto.Tier2GlobalIndexPtr = uintptr(unsafe.Pointer(&indices[0]))
	}
	return arrayPtr, verPtr, ver, true
}
