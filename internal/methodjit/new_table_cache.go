//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const (
	tier2NewTableCacheMaxArrayHint = tier2MaxFeedbackArrayHint
	newObject2CacheBatch           = 128
	// Fixed-shape constructors are often clustered in row-builder loops; a
	// larger refill batch cuts exit-resume frequency without growing array caches.
	// fixedTableCacheBatch is the cold prewarm size kept small enough that a
	// short-lived script does not pay big up-front allocation.
	// fixedTableCacheRefillBatch is the steady-state refill size; once a
	// monomorphic fixed-record site has fired once, future refills hand back
	// a larger slab so refill-driven Tier 2 exits become rare.
	fixedTableCacheBatch          = 1024
	fixedTableCacheRefillBatch    = 8192
	newTableCacheMaxBatch         = 512
	newTableCacheTargetBytes      = 4 << 20
	newTableCacheLargeTargetBytes = 8 << 20
	newTableCacheLargeArrayHint   = 64 * 1024
	mixedArraySparsePayloadValues = 1025
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
			if newTableCacheBatchSize(instr) > 1 ||
				fixedTableCtor2Cacheable(fn.Proto, instr) ||
				fixedTableCtorNCacheable(fn.Proto, instr) {
				return make([]newTableCacheEntry, fn.nextID)
			}
		}
	}
	return nil
}

func prewarmNewTableCachesForFunction(fn *Function, caches []newTableCacheEntry) {
	if fn == nil || len(caches) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.ID < 0 || instr.ID >= len(caches) {
				continue
			}
			switch instr.Op {
			case OpNewTable:
				batch := newTableCacheBatchSize(instr)
				if batch <= 1 {
					continue
				}
				hashHint, kind := unpackNewTableAux2(instr.Aux2)
				prewarmNewTableCacheEntry(&caches[instr.ID], int(instr.Aux), hashHint, kind, unpackNewTableDenseMixed(instr.Aux2), batch)
			case OpNewFixedTable:
				if ctor, ok := fixedTableCtor2ForInstr(fn.Proto, instr); ok && cacheableSmallCtor2(ctor) {
					prewarmFixedTable2CacheEntry(&caches[instr.ID], ctor, fixedTableCacheBatch, fixedTableSeedValuesForInstr(instr))
					continue
				}
				if ctor, ok := fixedTableCtorNForInstr(fn.Proto, instr); ok && cacheableSmallCtorN(ctor) {
					if fixedRecordCtorNCacheableForFunction(fn, instr, ctor) {
						prewarmFixedRecordNCacheEntry(&caches[instr.ID], ctor, fixedTableCacheBatch, fixedTableSeedValuesForInstr(instr))
					} else {
						prewarmFixedTableNCacheEntry(&caches[instr.ID], ctor, fixedTableCacheBatch, fixedTableSeedValuesForInstr(instr))
					}
				}
			}
		}
	}
}

func prewarmFixedTable2CacheEntry(entry *newTableCacheEntry, ctor *runtime.SmallTableCtor2, batch int, seed []runtime.Value) {
	if entry == nil || ctor == nil || batch <= 1 || len(entry.Values) > 0 {
		return
	}
	if len(seed) != 2 {
		seed = []runtime.Value{runtime.IntValue(0), runtime.IntValue(0)}
	}
	entry.Values = make([]runtime.Value, batch)
	if entry.Roots == nil {
		entry.Roots = make([]unsafe.Pointer, 0, 4)
	} else {
		entry.Roots = entry.Roots[:0]
	}
	for i := range entry.Values {
		t := runtime.NewTableFromCtor2NonNil(ctor, seed[0], seed[1])
		entry.addRoot(t)
		entry.Values[i] = runtime.FreshTableValue(t)
	}
	entry.Pos = 0
}

func prewarmFixedTableNCacheEntry(entry *newTableCacheEntry, ctor *runtime.SmallTableCtorN, batch int, seed []runtime.Value) {
	if entry == nil || ctor == nil || batch <= 1 || len(entry.Values) > 0 {
		return
	}
	seed = normalizeFixedTableSeed(seed, len(ctor.Keys))
	entry.Values = make([]runtime.Value, batch)
	if entry.Roots == nil {
		entry.Roots = make([]unsafe.Pointer, 0, 4)
	} else {
		entry.Roots = entry.Roots[:0]
	}
	for i := range entry.Values {
		t := runtime.NewTableFromCtorNNonNilCache(ctor, seed)
		entry.addRoot(t)
		entry.Values[i] = runtime.FreshTableValue(t)
	}
	entry.Pos = 0
}

func prewarmFixedRecordNCacheEntry(entry *newTableCacheEntry, ctor *runtime.SmallTableCtorN, batch int, seed []runtime.Value) {
	if entry == nil || ctor == nil || batch <= 1 || len(entry.Values) > 0 {
		return
	}
	seed = normalizeFixedTableSeed(seed, len(ctor.Keys))
	entry.Values = make([]runtime.Value, batch)
	for i := range entry.Values {
		if v, ok := runtime.NewFixedRecordValue(ctor, seed); ok {
			entry.Values[i] = v
		} else {
			t := runtime.NewTableFromCtorNNonNil(ctor, seed)
			entry.addRoot(t)
			entry.Values[i] = runtime.FreshTableValue(t)
		}
	}
	entry.Pos = 0
}

func fixedTableSeedValuesForInstr(instr *Instr) []runtime.Value {
	if instr == nil || len(instr.Args) == 0 {
		return nil
	}
	seed := make([]runtime.Value, len(instr.Args))
	for i, arg := range instr.Args {
		seed[i] = fixedTableSeedValueForArg(arg)
	}
	return seed
}

func fixedTableSeedValueForArg(arg *Value) runtime.Value {
	if arg == nil || arg.Def == nil {
		return runtime.IntValue(0)
	}
	switch arg.Def.Type {
	case TypeFloat:
		return runtime.FloatValue(0)
	case TypeBool:
		return runtime.BoolValue(false)
	case TypeString:
		return runtime.StringValue("")
	default:
		return runtime.IntValue(0)
	}
}

func normalizeFixedTableSeed(seed []runtime.Value, n int) []runtime.Value {
	if n <= 0 {
		return nil
	}
	if len(seed) == n {
		return seed
	}
	out := make([]runtime.Value, n)
	for i := range out {
		out[i] = runtime.IntValue(0)
	}
	return out
}

func prewarmNewTableCacheEntry(entry *newTableCacheEntry, arrayHint, hashHint int, kind runtime.ArrayKind, denseMixed bool, batch int) {
	if entry == nil || batch <= 1 || len(entry.Values) > 0 {
		return
	}
	entry.Values = make([]runtime.Value, batch)
	if entry.Roots == nil {
		entry.Roots = make([]unsafe.Pointer, 0, 4)
	} else {
		entry.Roots = entry.Roots[:0]
	}
	for i := range entry.Values {
		tbl := newCachedTable(arrayHint, hashHint, kind, denseMixed)
		entry.addRoot(tbl)
		entry.Values[i] = runtime.FreshTableValue(tbl)
	}
	entry.Pos = 0
}

func newTableCacheBatchSize(instr *Instr) int {
	if instr == nil || instr.Op != OpNewTable {
		return 0
	}
	hashHint, kind := unpackNewTableAux2(instr.Aux2)
	if unpackNewTableDenseMixed(instr.Aux2) {
		return denseMixedNewTableCacheBatchSizeForHints(instr.Aux, hashHint)
	}
	return newTableCacheBatchSizeForHints(instr.Aux, hashHint, kind)
}

func denseMixedNewTableCacheBatchSizeForHints(arrayHint int64, hashHint int) int {
	if arrayHint <= 0 || hashHint != 0 || arrayHint > tier2NewTableCacheMaxArrayHint {
		return 0
	}
	bytesPerTable := (arrayHint + 1) * 8
	if bytesPerTable <= 0 {
		return 0
	}
	targetBytes := int64(newTableCacheTargetBytes)
	if arrayHint >= newTableCacheLargeArrayHint {
		targetBytes = newTableCacheLargeTargetBytes
	}
	batch := int(targetBytes / bytesPerTable)
	if batch > newTableCacheMaxBatch {
		batch = newTableCacheMaxBatch
	}
	if batch <= 1 {
		return 0
	}
	return batch
}

func newTableCacheBatchSizeForHints(arrayHint int64, hashHint int, kind runtime.ArrayKind) int {
	if arrayHint == 0 && hashHint == 0 && kind == runtime.ArrayMixed {
		return 64
	}
	if arrayHint <= 0 || hashHint != 0 || arrayHint > tier2NewTableCacheMaxArrayHint {
		return 0
	}
	elemBytes := int64(8)
	payloadValues := arrayHint + 1
	if kind == runtime.ArrayMixed && payloadValues > mixedArraySparsePayloadValues {
		// Runtime mixed tables cap their eager array allocation and store the
		// larger target in arrayHint, so cache sizing should follow allocated
		// payload rather than the logical maximum index.
		payloadValues = mixedArraySparsePayloadValues
	}
	if kind == runtime.ArrayBool {
		elemBytes = 1
	}
	bytesPerTable := payloadValues * elemBytes
	if bytesPerTable <= 0 {
		return 0
	}
	targetBytes := int64(newTableCacheTargetBytes)
	if arrayHint >= newTableCacheLargeArrayHint {
		targetBytes = newTableCacheLargeTargetBytes
	}
	batch := int(targetBytes / bytesPerTable)
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

func fixedTableCtorNForInstr(proto *vm.FuncProto, instr *Instr) (*runtime.SmallTableCtorN, bool) {
	if proto == nil || instr == nil || instr.Op != OpNewFixedTable || instr.Aux2 <= 2 {
		return nil, false
	}
	ctorIdx := int(instr.Aux)
	if ctorIdx < 0 || ctorIdx >= len(proto.TableCtorsN) {
		return nil, false
	}
	return &proto.TableCtorsN[ctorIdx].Runtime, true
}

func fixedTableCtorNCacheable(proto *vm.FuncProto, instr *Instr) bool {
	ctor, ok := fixedTableCtorNForInstr(proto, instr)
	return ok && cacheableSmallCtorN(ctor)
}

func fixedRecordCtorNCacheableForProto(proto *vm.FuncProto, ctor *runtime.SmallTableCtorN) bool {
	if !cacheableFixedRecordCtorN(ctor) {
		return false
	}
	if proto == nil || ctor.Shape == nil {
		return true
	}
	return !protoUsesShapeForStringKeyAccess(proto, ctor.Shape.ID)
}

func fixedRecordCtorNCacheableForFunction(fn *Function, instr *Instr, ctor *runtime.SmallTableCtorN) bool {
	if fn == nil || instr == nil || !fixedRecordCtorNCacheableForProto(fn.Proto, ctor) {
		return false
	}
	if fn.FixedRecordNewTableSites == nil {
		fn.FixedRecordNewTableSites = computeFixedRecordNewTableSites(fn)
	}
	return fn.FixedRecordNewTableSites[instr.ID]
}

func computeFixedRecordNewTableSites(fn *Function) map[int]bool {
	out := make(map[int]bool)
	if fn == nil {
		return out
	}
	candidates := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpNewFixedTable || instr.Aux2 <= 2 {
				continue
			}
			if ctor, ok := fixedTableCtorNForInstr(fn.Proto, instr); ok && fixedRecordCtorNCacheableForProto(fn.Proto, ctor) {
				candidates[instr.ID] = true
				out[instr.ID] = true
			}
		}
	}
	if len(candidates) == 0 {
		return out
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			for argIdx, arg := range instr.Args {
				if arg == nil || !candidates[arg.ID] {
					continue
				}
				if !fixedRecordUseIsLocalFieldRead(instr, argIdx) {
					out[arg.ID] = false
				}
			}
		}
	}
	return out
}

func fixedRecordUseIsLocalFieldRead(instr *Instr, argIdx int) bool {
	if instr == nil || argIdx != 0 {
		return false
	}
	switch instr.Op {
	case OpGetField, OpFieldSvals:
		return true
	default:
		return false
	}
}

func protoUsesShapeForStringKeyAccess(proto *vm.FuncProto, shapeID uint32) bool {
	if proto == nil || shapeID == 0 {
		return false
	}
	for i := range proto.TableKeyFeedback {
		fb := &proto.TableKeyFeedback[i]
		if fb.Count == 0 || !fb.StringKeySeen {
			continue
		}
		if fb.ShapeID == shapeID {
			return true
		}
	}
	return false
}

func cacheableSmallCtorN(ctor *runtime.SmallTableCtorN) bool {
	return ctor != nil &&
		ctor.Shape != nil &&
		len(ctor.Keys) > 2 &&
		len(ctor.Keys) <= runtime.SmallFieldCap
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
	kindName := newTableCacheKindName(kind)
	if unpackNewTableDenseMixed(instr.Aux2) {
		kindName += ",dense_mixed=true"
	}
	return fmt.Sprintf("NewTable(array=%d,hash=%d,kind=%s,cache_batch=%d)",
		instr.Aux, hashHint, kindName, newTableCacheBatchSize(instr))
}

func (cf *CompiledFunction) allocateNewTableForExit(instrID int, arrayHint, hashHint int, kind runtime.ArrayKind) *runtime.Table {
	if cf == nil {
		return runtime.NewTableSizedKind(arrayHint, hashHint, kind)
	}
	return allocateNewTableWithCache(cf.NewTableCaches, instrID, arrayHint, hashHint, kind, false)
}

func (cf *CompiledFunction) allocateDenseMixedNewTableForExit(instrID int, arrayHint, hashHint int) *runtime.Table {
	if cf == nil {
		return runtime.NewDenseMixedArrayTable(arrayHint, hashHint)
	}
	return allocateNewTableWithCache(cf.NewTableCaches, instrID, arrayHint, hashHint, runtime.ArrayMixed, true)
}

func (cf *CompiledFunction) allocateFixedTable2ForExit(instrID int, ctor *runtime.SmallTableCtor2, val1, val2 runtime.Value) *runtime.Table {
	if cf == nil {
		return runtime.NewTableFromCtor2(ctor, val1, val2)
	}
	return allocateFixedTable2WithCache(cf.NewTableCaches, instrID, ctor, val1, val2)
}

func (cf *CompiledFunction) allocateFixedTableNForExit(instrID int, ctor *runtime.SmallTableCtorN, vals []runtime.Value) *runtime.Table {
	if cf == nil {
		return runtime.NewTableFromCtorN(ctor, vals)
	}
	return allocateFixedTableNWithCache(cf.NewTableCaches, instrID, ctor, vals)
}

func (cf *CompiledFunction) allocateFixedTableNValueForExit(instrID int, ctor *runtime.SmallTableCtorN, vals []runtime.Value) runtime.Value {
	useFixedRecord := fixedRecordCtorNCacheableForProto(nil, ctor)
	if cf != nil {
		useFixedRecord = cf.FixedRecordNewTableSites[instrID]
	}
	if cf == nil {
		if useFixedRecord {
			if v, ok := runtime.NewFixedRecordValue(ctor, vals); ok {
				return v
			}
		}
		return runtime.FreshTableValue(runtime.NewTableFromCtorN(ctor, vals))
	}
	return allocateFixedTableNValueWithCache(cf.NewTableCaches, instrID, ctor, vals, useFixedRecord)
}

func allocateNewTableWithCache(caches []newTableCacheEntry, instrID int, arrayHint, hashHint int, kind runtime.ArrayKind, denseMixed bool) *runtime.Table {
	batch := newTableCacheBatchSizeForHints(int64(arrayHint), hashHint, kind)
	if denseMixed {
		batch = denseMixedNewTableCacheBatchSizeForHints(int64(arrayHint), hashHint)
	}
	return allocateNewTableWithCacheBatch(caches, instrID, arrayHint, hashHint, kind, denseMixed, batch)
}

func allocateBaselineNewTableWithCache(caches []newTableCacheEntry, instrID int, arrayHint, hashHint int, kind runtime.ArrayKind) *runtime.Table {
	return allocateNewTableWithCacheBatch(caches, instrID, arrayHint, hashHint, kind, false, baselineNewTableCacheBatchSizeForHints(arrayHint, hashHint, kind))
}

func allocateNewTableWithCacheBatch(caches []newTableCacheEntry, instrID int, arrayHint, hashHint int, kind runtime.ArrayKind, denseMixed bool, batch int) *runtime.Table {
	tbl := newCachedTable(arrayHint, hashHint, kind, denseMixed)
	if instrID < 0 || instrID >= len(caches) {
		return tbl
	}
	entry := &caches[instrID]
	if entry.Pos < int64(len(entry.Values)) {
		return tbl
	}
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
		t := newCachedTable(arrayHint, hashHint, kind, denseMixed)
		entry.addRoot(t)
		entry.Values[i] = runtime.FreshTableValue(t)
	}
	entry.Pos = 0
	return tbl
}

func newCachedTable(arrayHint, hashHint int, kind runtime.ArrayKind, denseMixed bool) *runtime.Table {
	if denseMixed {
		return runtime.NewDenseMixedArrayTable(arrayHint, hashHint)
	}
	return runtime.NewTableSizedKind(arrayHint, hashHint, kind)
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
	keep := fixedTableCacheRefillBatch - 1
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
	keep := fixedTableCacheRefillBatch - 1
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

func allocateFixedTableNWithCache(caches []newTableCacheEntry, instrID int, ctor *runtime.SmallTableCtorN, vals []runtime.Value) *runtime.Table {
	if cacheableSmallCtorN(ctor) && instrID >= 0 && instrID < len(caches) && fixedTableValuesAllNonNil(vals) {
		return allocateFixedTableNFullWithCache(caches, instrID, ctor, vals)
	}
	return runtime.NewTableFromCtorN(ctor, vals)
}

func allocateFixedTableNValueWithCache(caches []newTableCacheEntry, instrID int, ctor *runtime.SmallTableCtorN, vals []runtime.Value, useFixedRecord bool) runtime.Value {
	if useFixedRecord && fixedTableValuesAllNonNil(vals) {
		if instrID >= 0 && instrID < len(caches) {
			return allocateFixedRecordNFullWithCache(caches, instrID, ctor, vals)
		}
		if v, ok := runtime.NewFixedRecordValue(ctor, vals); ok {
			return v
		}
	}
	return runtime.FreshTableValue(allocateFixedTableNWithCache(caches, instrID, ctor, vals))
}

func fixedTableValuesAllNonNil(vals []runtime.Value) bool {
	if len(vals) == 0 {
		return false
	}
	for _, val := range vals {
		if val.IsNil() {
			return false
		}
	}
	return true
}

func allocateFixedTableNFullWithCache(caches []newTableCacheEntry, instrID int, ctor *runtime.SmallTableCtorN, vals []runtime.Value) *runtime.Table {
	tbl := runtime.NewTableFromCtorNNonNil(ctor, vals)
	entry := &caches[instrID]
	if entry.Pos < int64(len(entry.Values)) {
		return tbl
	}
	keep := fixedTableCacheRefillBatch - 1
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
		t := runtime.NewTableFromCtorNNonNil(ctor, vals)
		entry.addRoot(t)
		entry.Values[i] = runtime.FreshTableValue(t)
	}
	entry.Pos = 0
	return tbl
}

func allocateFixedRecordNFullWithCache(caches []newTableCacheEntry, instrID int, ctor *runtime.SmallTableCtorN, vals []runtime.Value) runtime.Value {
	v, ok := runtime.NewFixedRecordValue(ctor, vals)
	if !ok {
		return runtime.FreshTableValue(allocateFixedTableNWithCache(caches, instrID, ctor, vals))
	}
	entry := &caches[instrID]
	if entry.Pos < int64(len(entry.Values)) {
		return v
	}
	keep := fixedTableCacheRefillBatch - 1
	if cap(entry.Values) < keep {
		entry.Values = make([]runtime.Value, keep)
	} else {
		entry.Values = entry.Values[:keep]
	}
	for i := range entry.Values {
		if cached, ok := runtime.NewFixedRecordCacheValue(ctor, vals); ok {
			entry.Values[i] = cached
		} else {
			t := runtime.NewTableFromCtorNNonNilCache(ctor, vals)
			entry.addRoot(t)
			entry.Values[i] = runtime.FreshTableValue(t)
		}
	}
	entry.Pos = 0
	return v
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
