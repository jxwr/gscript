//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func baselineNewTableCacheSlotsForProto(proto *vm.FuncProto) []newTableCacheEntry {
	if proto == nil || len(proto.Code) == 0 {
		return nil
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_NEWTABLE:
			if baselineNewTableCacheBatchSize(inst) > 1 {
				return make([]newTableCacheEntry, len(proto.Code))
			}
		case vm.OP_NEWOBJECT2:
			if baselineNewObject2Cacheable(proto, inst) {
				return make([]newTableCacheEntry, len(proto.Code))
			}
		}
	}
	return nil
}

func baselineNewObject2Cacheable(proto *vm.FuncProto, inst uint32) bool {
	if proto == nil || vm.DecodeOp(inst) != vm.OP_NEWOBJECT2 {
		return false
	}
	ctorIdx := vm.DecodeB(inst)
	if ctorIdx < 0 || ctorIdx >= len(proto.TableCtors2) {
		return false
	}
	return cacheableSmallCtor2(&proto.TableCtors2[ctorIdx].Runtime)
}

func cacheableSmallCtor2(ctor *runtime.SmallTableCtor2) bool {
	return ctor != nil && ctor.Key1 != ctor.Key2 && ctor.Shape != nil
}

func baselineNewTableCacheBatchSize(inst uint32) int {
	if vm.DecodeOp(inst) != vm.OP_NEWTABLE {
		return 0
	}
	return newTableCacheBatchSizeForHints(int64(vm.DecodeB(inst)), vm.DecodeC(inst), runtime.ArrayMixed)
}

func emitBaselineNewTable(asm *jit.Assembler, inst uint32, pc int, caches []newTableCacheEntry) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	if !emitBaselineNewTableCacheFastPath(asm, inst, pc, caches, a) {
		emitBaselineOpExitCommon(asm, vm.OP_NEWTABLE, pc, a, b, c)
	}
}

func emitBaselineNewTableCacheFastPath(asm *jit.Assembler, inst uint32, pc int, caches []newTableCacheEntry, dstSlot int) bool {
	if pc < 0 || pc >= len(caches) || baselineNewTableCacheBatchSize(inst) <= 1 {
		return false
	}

	doneLabel := nextLabel("newtable_done")
	missLabel := nextLabel("newtable_cache_miss")
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
	asm.CBZ(jit.X0, missLabel)
	asm.LDR(jit.X3, jit.X2, newTableCacheEntryPosOff)
	asm.LDR(jit.X4, jit.X2, newTableCacheEntryLenOff)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondGE, missLabel)
	asm.LDRreg(jit.X0, jit.X0, jit.X3)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X2, newTableCacheEntryPosOff)
	storeSlot(asm, dstSlot, jit.X0)
	asm.B(doneLabel)

	asm.Label(missLabel)
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	emitBaselineOpExitCommon(asm, vm.OP_NEWTABLE, pc, a, b, c)

	asm.Label(doneLabel)
	return true
}
