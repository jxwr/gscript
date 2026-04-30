//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func emitBaselineNewObject2(asm *jit.Assembler, inst uint32, pc int, proto *vm.FuncProto, caches []newTableCacheEntry) {
	if !baselineNewObject2Cacheable(proto, inst) || pc < 0 || pc >= len(caches) {
		emitBaselineOpExit(asm, inst, pc, vm.OP_NEWOBJECT2)
		return
	}

	a := vm.DecodeA(inst)
	c := vm.DecodeC(inst)
	exitLabel := nextLabel("newobject2_exit")
	doneLabel := nextLabel("newobject2_done")
	val1NilLabel := nextLabel("newobject2_val1_nil")
	emptyLabel := nextLabel("newobject2_empty")

	loadSlot(asm, jit.X5, c)
	loadSlot(asm, jit.X6, c+1)
	asm.LoadImm64(jit.X7, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X5, jit.X7)
	asm.BCond(jit.CondEQ, val1NilLabel)
	asm.CMPreg(jit.X6, jit.X7)
	asm.BCond(jit.CondEQ, exitLabel)

	cacheBase := uintptr(unsafe.Pointer(&caches[0]))
	entryOff := pc * newTableCacheEntrySize
	asm.LoadImm64(jit.X2, int64(cacheBase))
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X2, jit.X2, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X3, int64(entryOff))
			asm.ADDreg(jit.X2, jit.X2, jit.X3)
		}
	}

	asm.LDR(jit.X0, jit.X2, newTableCacheEntryValuesOff)
	asm.CBZ(jit.X0, exitLabel)
	asm.LDR(jit.X3, jit.X2, newTableCacheEntryPosOff)
	asm.LDR(jit.X4, jit.X2, newTableCacheEntryLenOff)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondGE, exitLabel)
	asm.LDRreg(jit.X0, jit.X0, jit.X3)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X2, newTableCacheEntryPosOff)

	jit.EmitExtractPtr(asm, jit.X1, jit.X0)
	asm.LDR(jit.X2, jit.X1, jit.TableOffSvals)
	asm.STR(jit.X5, jit.X2, 0)
	asm.STR(jit.X6, jit.X2, jit.ValueSize)
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	asm.Label(val1NilLabel)
	asm.CMPreg(jit.X6, jit.X7)
	asm.BCond(jit.CondEQ, emptyLabel)
	asm.B(exitLabel)

	asm.Label(emptyLabel)
	asm.LoadImm64(jit.X2, int64(cacheBase))
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X2, jit.X2, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X3, int64(entryOff))
			asm.ADDreg(jit.X2, jit.X2, jit.X3)
		}
	}
	asm.LDR(jit.X0, jit.X2, newTableCacheEntryEmptyValuesOff)
	asm.CBZ(jit.X0, exitLabel)
	asm.LDR(jit.X3, jit.X2, newTableCacheEntryEmptyPosOff)
	asm.LDR(jit.X4, jit.X2, newTableCacheEntryEmptyLenOff)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondGE, exitLabel)
	asm.LDRreg(jit.X0, jit.X0, jit.X3)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X2, newTableCacheEntryEmptyPosOff)
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	asm.Label(exitLabel)
	emitBaselineOpExit(asm, inst, pc, vm.OP_NEWOBJECT2)
	asm.Label(doneLabel)
}

func fillBaselineNewObject2Cache(bf *BaselineFunc, pc int, ctor *runtime.SmallTableCtor2, val1, val2 runtime.Value) {
	if bf == nil || pc < 0 || pc >= len(bf.NewTableCaches) || !cacheableSmallCtor2(ctor) {
		return
	}
	if val1.IsNil() && val2.IsNil() {
		fillBaselineNewObject2EmptyCache(bf, pc)
		return
	}
	if val1.IsNil() || val2.IsNil() {
		return
	}
	entry := &bf.NewTableCaches[pc]
	if entry.Pos < int64(len(entry.Values)) {
		return
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
		tbl := runtime.NewTableFromCtor2NonNil(ctor, seed, seed)
		entry.addRoot(tbl)
		entry.Values[i] = runtime.FreshTableValue(tbl)
	}
	entry.Pos = 0
}

func fillBaselineNewObject2EmptyCache(bf *BaselineFunc, pc int) {
	entry := &bf.NewTableCaches[pc]
	if entry.EmptyPos < int64(len(entry.EmptyValues)) {
		return
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
		tbl := runtime.NewTableSized(0, 0)
		entry.addEmptyRoot(tbl)
		entry.EmptyValues[i] = runtime.FreshTableValue(tbl)
	}
	entry.EmptyPos = 0
}
