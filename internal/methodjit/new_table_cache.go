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
	newObject2CacheBatch           = 128
	newTableCacheMaxBatch          = 128
	newTableCacheTargetBytes       = 1 << 20
)

type newTableCacheEntry struct {
	Values      []runtime.Value
	Roots       []unsafe.Pointer
	Pos         int64
	EmptyValues []runtime.Value
	EmptyRoots  []unsafe.Pointer
	EmptyPos    int64
}

var (
	newTableCacheEntrySize           = int(unsafe.Sizeof(newTableCacheEntry{}))
	newTableCacheEntryValuesOff      = int(unsafe.Offsetof(newTableCacheEntry{}.Values))
	newTableCacheEntryLenOff         = newTableCacheEntryValuesOff + int(unsafe.Sizeof(uintptr(0)))
	newTableCacheEntryPosOff         = int(unsafe.Offsetof(newTableCacheEntry{}.Pos))
	newTableCacheEntryEmptyValuesOff = int(unsafe.Offsetof(newTableCacheEntry{}.EmptyValues))
	newTableCacheEntryEmptyLenOff    = newTableCacheEntryEmptyValuesOff + int(unsafe.Sizeof(uintptr(0)))
	newTableCacheEntryEmptyPosOff    = int(unsafe.Offsetof(newTableCacheEntry{}.EmptyPos))
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
	elemBytes := int64(8)
	if kind == runtime.ArrayBool {
		elemBytes = 1
	}
	bytesPerTable := (arrayHint + 1) * elemBytes
	if bytesPerTable <= 0 {
		return 0
	}
	batch := int(newTableCacheTargetBytes / bytesPerTable)
	if batch > newTableCacheMaxBatch {
		batch = newTableCacheMaxBatch
	}
	if batch <= 1 {
		return 0
	}
	return batch
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
	if entry.Roots == nil {
		entry.Roots = make([]unsafe.Pointer, 0, 4)
	} else {
		entry.Roots = entry.Roots[:0]
	}
	for i := range entry.Values {
		t := runtime.NewTableSizedKind(arrayHint, hashHint, kind)
		entry.addRoot(t)
		entry.Values[i] = runtime.FreshTableValue(t)
	}
	entry.Pos = 0
	return tbl
}

func allocateFixedTable2WithCache(caches []newTableCacheEntry, instrID int, ctor *runtime.SmallTableCtor2, val1, val2 runtime.Value) *runtime.Table {
	if cacheableSmallCtor2(ctor) && instrID >= 0 && instrID < len(caches) {
		if val1.IsNil() && val2.IsNil() {
			return allocateFixedTable2EmptyWithCache(caches, instrID)
		}
		if !val1.IsNil() && !val2.IsNil() {
			return allocateFixedTable2FullWithCache(caches, instrID, ctor, val1, val2)
		}
	}
	return runtime.NewTableFromCtor2(ctor, val1, val2)
}

func allocateFixedTable2FullWithCache(caches []newTableCacheEntry, instrID int, ctor *runtime.SmallTableCtor2, val1, val2 runtime.Value) *runtime.Table {
	tbl := runtime.NewTableFromCtor2NonNil(ctor, val1, val2)
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
	if entry.Roots == nil {
		entry.Roots = make([]unsafe.Pointer, 0, 4)
	} else {
		entry.Roots = entry.Roots[:0]
	}
	seed := runtime.IntValue(0)
	for i := range entry.Values {
		t := runtime.NewTableFromCtor2NonNil(ctor, seed, seed)
		entry.addRoot(t)
		entry.Values[i] = runtime.FreshTableValue(t)
	}
	entry.Pos = 0
	return tbl
}

func allocateFixedTable2EmptyWithCache(caches []newTableCacheEntry, instrID int) *runtime.Table {
	tbl := runtime.NewTableSized(0, 0)
	entry := &caches[instrID]
	if entry.EmptyPos < int64(len(entry.EmptyValues)) {
		return tbl
	}
	keep := newObject2CacheBatch - 1
	if cap(entry.EmptyValues) < keep {
		entry.EmptyValues = make([]runtime.Value, keep)
	} else {
		entry.EmptyValues = entry.EmptyValues[:keep]
	}
	if entry.EmptyRoots == nil {
		entry.EmptyRoots = make([]unsafe.Pointer, 0, 4)
	} else {
		entry.EmptyRoots = entry.EmptyRoots[:0]
	}
	for i := range entry.EmptyValues {
		t := runtime.NewTableSized(0, 0)
		entry.addEmptyRoot(t)
		entry.EmptyValues[i] = runtime.FreshTableValue(t)
	}
	entry.EmptyPos = 0
	return tbl
}

func (entry *newTableCacheEntry) addRoot(t *runtime.Table) {
	entry.addRootTo(t, &entry.Roots)
}

func (entry *newTableCacheEntry) addEmptyRoot(t *runtime.Table) {
	entry.addRootTo(t, &entry.EmptyRoots)
}

func (entry *newTableCacheEntry) addRootTo(t *runtime.Table, roots *[]unsafe.Pointer) {
	root := runtime.TableGCRoot(t)
	if root == nil {
		return
	}
	for _, existing := range *roots {
		if existing == root {
			return
		}
	}
	*roots = append(*roots, root)
}
