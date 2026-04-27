//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) emitNewTableCacheFastPath(instr *Instr, doneLabel, missLabel string) bool {
	if ec == nil || instr == nil || instr.ID < 0 || instr.ID >= len(ec.newTableCaches) {
		return false
	}
	if newTableCacheBatchSize(instr) <= 1 {
		return false
	}

	asm := ec.asm
	cacheBase := uintptr(unsafe.Pointer(&ec.newTableCaches[0]))
	entryOff := instr.ID * newTableCacheEntrySize

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
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)
	return true
}
