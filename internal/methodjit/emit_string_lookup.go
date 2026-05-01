//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) emitStringConstLookup(instr *Instr) {
	if instr == nil || len(instr.Args) != 1 || ec == nil || ec.fn == nil {
		ec.emitDeopt(instr)
		return
	}
	tableIdx := int(instr.Aux)
	if tableIdx < 0 || tableIdx >= len(ec.fn.StringConstTables) {
		ec.emitDeopt(instr)
		return
	}
	table := ec.fn.StringConstTables[tableIdx]
	if len(table) == 0 || len(table) != int(instr.Aux2) {
		ec.emitDeopt(instr)
		return
	}

	asm := ec.asm
	deoptLabel := ec.uniqueLabel("string_lookup_deopt")
	doneLabel := ec.uniqueLabel("string_lookup_done")

	idxReg := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
	if idxReg != jit.X0 {
		asm.MOVreg(jit.X0, idxReg)
	}
	asm.CMPimm(jit.X0, 0)
	asm.BCond(jit.CondLT, deoptLabel)
	emitCmpInt64(asm, jit.X0, int64(len(table)), jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)

	asm.LoadImm64(jit.X1, int64(uintptr(unsafe.Pointer(&table[0]))))
	asm.LDRreg(jit.X0, jit.X1, jit.X0)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}
