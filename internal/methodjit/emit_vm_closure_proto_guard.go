//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) emitIsVMClosureProto(instr *Instr) {
	if len(instr.Args) != 1 {
		return
	}
	want, ok := ec.fn.funcProtoRef(instr.Aux)
	if !ok {
		ec.asm.MOVimm16(jit.X0, 0)
		ec.asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
		ec.storeResultNB(jit.X0, instr.ID)
		return
	}

	falseLabel := ec.uniqueLabel("is_vm_closure_proto_false")
	doneLabel := ec.uniqueLabel("is_vm_closure_proto_done")

	src := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if src != jit.X0 {
		ec.asm.MOVreg(jit.X0, src)
	}

	// Must be a NaN-boxed pointer with the VM-closure pointer subtype.
	ec.asm.LSRimm(jit.X1, jit.X0, 48)
	ec.asm.MOVimm16(jit.X2, jit.NB_TagPtrShr48)
	ec.asm.CMPreg(jit.X1, jit.X2)
	ec.asm.BCond(jit.CondNE, falseLabel)
	ec.asm.LSRimm(jit.X1, jit.X0, uint8(nbPtrSubShift))
	ec.asm.LoadImm64(jit.X2, 0xF)
	ec.asm.ANDreg(jit.X1, jit.X1, jit.X2)
	ec.asm.CMPimm(jit.X1, nbPtrSubVMClosure)
	ec.asm.BCond(jit.CondNE, falseLabel)

	jit.EmitExtractPtr(ec.asm, jit.X0, jit.X0)
	ec.asm.LDR(jit.X1, jit.X0, vmClosureOffProto)
	ec.asm.LoadImm64(jit.X2, int64(uintptr(unsafe.Pointer(want))))
	ec.asm.CMPreg(jit.X1, jit.X2)
	ec.asm.CSET(jit.X0, jit.CondEQ)
	ec.asm.B(doneLabel)

	ec.asm.Label(falseLabel)
	ec.asm.MOVimm16(jit.X0, 0)

	ec.asm.Label(doneLabel)
	ec.asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	ec.storeResultNB(jit.X0, instr.ID)
}
