//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const (
	tier2NewTableCacheMaxArrayHint = tier2FeedbackOuterLoopArrayHint
	newObject2CacheBatch           = 32
)

type newTableCacheEntry struct {
	Values []runtime.Value
	Roots  []*runtime.Table
	Pos    int64
}

var (
	newTableCacheEntrySize      = int(unsafe.Sizeof(newTableCacheEntry{}))
	newTableCacheEntryValuesOff = int(unsafe.Offsetof(newTableCacheEntry{}.Values))
	newTableCacheEntryLenOff    = newTableCacheEntryValuesOff + int(unsafe.Sizeof(uintptr(0)))
	newTableCacheEntryPosOff    = int(unsafe.Offsetof(newTableCacheEntry{}.Pos))
)

func newTableCacheSlotsForFunction(fn *Function) []newTableCacheEntry {
	if fn == nil || fn.nextID <= 0 {
		return nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if newTableCacheBatchSize(instr) > 1 || fixedTableCtor2Cacheable(fn.Proto, instr) {
				return make([]newTableCacheEntry, fn.nextID)
			}
		}
	}
	return nil
}

func newTableCacheBatchSize(instr *Instr) int {
	if instr == nil || instr.Op != OpNewTable {
		return 0
	}
	hashHint, kind := unpackNewTableAux2(instr.Aux2)
	return newTableCacheBatchSizeForHints(instr.Aux, hashHint, kind)
}

func newTableCacheBatchSizeForHints(arrayHint int64, hashHint int, kind runtime.ArrayKind) int {
	if arrayHint == 0 && hashHint == 0 && kind == runtime.ArrayMixed {
		return 64
	}
	if arrayHint <= 0 || hashHint != 0 || kind == runtime.ArrayMixed || arrayHint > tier2NewTableCacheMaxArrayHint {
		return 0
	}
	if arrayHint <= 1024 {
		return 32
	}
	if arrayHint <= 4096 {
		return 8
	}
	return 4
}

func fixedTableCtor2ForInstr(proto *vm.FuncProto, instr *Instr) (*runtime.SmallTableCtor2, bool) {
	if proto == nil || instr == nil || instr.Op != OpNewFixedTable || instr.Aux2 != 2 {
		return nil, false
	}
	ctorIdx := int(instr.Aux)
	if ctorIdx < 0 || ctorIdx >= len(proto.TableCtors2) {
		return nil, false
	}
	return &proto.TableCtors2[ctorIdx].Runtime, true
}

func fixedTableCtor2Cacheable(proto *vm.FuncProto, instr *Instr) bool {
	ctor, ok := fixedTableCtor2ForInstr(proto, instr)
	return ok && cacheableSmallCtor2(ctor)
}

func newTableCacheKindName(kind runtime.ArrayKind) string {
	switch kind {
	case runtime.ArrayMixed:
		return "mixed"
	case runtime.ArrayInt:
		return "int"
	case runtime.ArrayFloat:
		return "float"
	case runtime.ArrayBool:
		return "bool"
	default:
		return fmt.Sprintf("kind%d", kind)
	}
}

func newTableExitReason(instr *Instr) string {
	if instr == nil || instr.Op != OpNewTable {
		return "NewTable"
	}
	hashHint, kind := unpackNewTableAux2(instr.Aux2)
	return fmt.Sprintf("NewTable(array=%d,hash=%d,kind=%s,cache_batch=%d)",
		instr.Aux, hashHint, newTableCacheKindName(kind), newTableCacheBatchSize(instr))
}

func (cf *CompiledFunction) allocateNewTableForExit(instrID int, arrayHint, hashHint int, kind runtime.ArrayKind) *runtime.Table {
	if cf == nil {
		return runtime.NewTableSizedKind(arrayHint, hashHint, kind)
	}
	return allocateNewTableWithCache(cf.NewTableCaches, instrID, arrayHint, hashHint, kind)
}

func (cf *CompiledFunction) allocateFixedTable2ForExit(instrID int, ctor *runtime.SmallTableCtor2, val1, val2 runtime.Value) *runtime.Table {
	if cf == nil {
		return runtime.NewTableFromCtor2(ctor, val1, val2)
	}
	return allocateFixedTable2WithCache(cf.NewTableCaches, instrID, ctor, val1, val2)
}

func allocateNewTableWithCache(caches []newTableCacheEntry, instrID int, arrayHint, hashHint int, kind runtime.ArrayKind) *runtime.Table {
	tbl := runtime.NewTableSizedKind(arrayHint, hashHint, kind)
	if instrID < 0 || instrID >= len(caches) {
		return tbl
	}
	entry := &caches[instrID]
	if entry.Pos < int64(len(entry.Values)) {
		return tbl
	}
	batch := newTableCacheBatchSizeForHints(int64(arrayHint), hashHint, kind)
	if batch <= 1 {
		entry.Values = nil
		entry.Roots = nil
		entry.Pos = 0
		return tbl
	}
	keep := batch - 1
	if cap(entry.Values) < keep {
		entry.Values = make([]runtime.Value, keep)
	} else {
		entry.Values = entry.Values[:keep]
	}
	if cap(entry.Roots) < keep {
		entry.Roots = make([]*runtime.Table, keep)
	} else {
		entry.Roots = entry.Roots[:keep]
	}
	for i := range entry.Values {
		t := runtime.NewTableSizedKind(arrayHint, hashHint, kind)
		entry.Roots[i] = t
		entry.Values[i] = runtime.TableValue(t)
	}
	entry.Pos = 0
	return tbl
}

func allocateFixedTable2WithCache(caches []newTableCacheEntry, instrID int, ctor *runtime.SmallTableCtor2, val1, val2 runtime.Value) *runtime.Table {
	tbl := runtime.NewTableFromCtor2(ctor, val1, val2)
	if !cacheableSmallCtor2(ctor) || val1.IsNil() || val2.IsNil() || instrID < 0 || instrID >= len(caches) {
		return tbl
	}
	entry := &caches[instrID]
	if entry.Pos < int64(len(entry.Values)) {
		return tbl
	}
	keep := newObject2CacheBatch - 1
	if cap(entry.Values) < keep {
		entry.Values = make([]runtime.Value, keep)
	} else {
		entry.Values = entry.Values[:keep]
	}
	if cap(entry.Roots) < keep {
		entry.Roots = make([]*runtime.Table, keep)
	} else {
		entry.Roots = entry.Roots[:keep]
	}
	seed := runtime.IntValue(0)
	for i := range entry.Values {
		t := runtime.NewTableFromCtor2(ctor, seed, seed)
		entry.Roots[i] = t
		entry.Values[i] = runtime.TableValue(t)
	}
	entry.Pos = 0
	return tbl
}
