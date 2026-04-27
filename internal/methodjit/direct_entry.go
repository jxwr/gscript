//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

func setFuncProtoDirectEntry(proto *vm.FuncProto, entry uintptr) bool {
	if proto == nil || proto.DirectEntryPtr == entry {
		return false
	}
	proto.DirectEntryPtr = entry
	proto.DirectEntryVersion++
	return true
}

func setFuncProtoTier2DirectEntries(proto *vm.FuncProto, directEntry, tier2Entry uintptr) bool {
	if proto == nil {
		return false
	}
	if proto.DirectEntryPtr == directEntry && proto.Tier2DirectEntryPtr == tier2Entry {
		return false
	}
	proto.DirectEntryPtr = directEntry
	proto.Tier2DirectEntryPtr = tier2Entry
	proto.DirectEntryVersion++
	return true
}

func clearFuncProtoDirectEntries(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	changed := proto.DirectEntryPtr != 0 || proto.Tier2DirectEntryPtr != 0
	if proto.DirectEntryPtr != 0 || proto.Tier2DirectEntryPtr != 0 {
		proto.DirectEntryVersion++
	}
	proto.DirectEntryPtr = 0
	proto.Tier2DirectEntryPtr = 0
	proto.Tier2NumericEntryPtr = 0
	return changed
}
