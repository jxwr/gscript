//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

func emitBaselineNewObjectN(asm *jit.Assembler, inst uint32, pc int, proto *vm.FuncProto) {
	if !baselineNewObjectNCacheable(proto, inst) {
		emitBaselineOpExit(asm, inst, pc, vm.OP_NEWOBJECTN)
		return
	}

	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	ctor := &proto.TableCtorsN[b].Runtime
	n := len(ctor.Keys)

	exitLabel := nextLabel("newobjectn_exit")
	doneLabel := nextLabel("newobjectn_done")

	asm.LoadImm64(jit.X7, nb64(jit.NB_ValNil))
	for i := 0; i < n; i++ {
		valReg := jit.Reg(int(jit.X8) + i)
		loadSlot(asm, valReg, c+i)
		asm.CMPreg(valReg, jit.X7)
		asm.BCond(jit.CondEQ, exitLabel)
	}

	asm.LDR(jit.X1, mRegCtx, execCtxOffCoroutineCurrentPtr)
	asm.CBZ(jit.X1, exitLabel)
	asm.LDRB(jit.X2, jit.X1, vm.VMCoroutineStackYieldEnabledOffset())
	asm.CBZ(jit.X2, exitLabel)
	asm.LDR(jit.X1, jit.X1, vm.VMCoroutinePooledFixedRecordOffset())
	asm.CBZ(jit.X1, exitLabel)

	asm.LoadImm64(jit.X2, int64(uintptr(unsafe.Pointer(ctor))))
	asm.STR(jit.X2, jit.X1, jit.FixedRecordOffCtor)
	asm.MOVimm16(jit.X2, 0)
	asm.STR(jit.X2, jit.X1, jit.FixedRecordOffMaterialized)
	asm.LoadImm64(jit.X2, int64(ctor.Shape.ID))
	asm.STRW(jit.X2, jit.X1, jit.FixedRecordOffShapeID)
	asm.MOVimm16(jit.X2, uint16(n))
	asm.STRB(jit.X2, jit.X1, jit.FixedRecordOffN)
	for i := 0; i < n; i++ {
		asm.STR(jit.Reg(int(jit.X8)+i), jit.X1, jit.FixedRecordOffValues+i*jit.ValueSize)
	}

	asm.LoadImm64(jit.X2, nb64(jit.NB_TagPtr|(uint64(jit.NB_PtrSubFixedRecord)<<jit.NB_PtrSubShift)))
	asm.ORRreg(jit.X0, jit.X1, jit.X2)
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	asm.Label(exitLabel)
	emitBaselineOpExit(asm, inst, pc, vm.OP_NEWOBJECTN)
	asm.Label(doneLabel)
}
