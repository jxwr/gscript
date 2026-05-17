//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

func (ec *emitContext) emitRecordArrayLoopKernel(instr *Instr) {
	if instr == nil || len(instr.Args) < 3 || ec == nil || ec.fn == nil {
		return
	}
	spec, ok := ec.fn.RecordArrayLoopKernels[instr.ID]
	if !ok || !validRecordArrayLoopKernelSpec(spec, len(instr.Args)-3) {
		ec.emitPreciseDeopt(instr)
		return
	}

	asm := ec.asm
	dataReg := ec.resolveRawDataPtr(instr.Args[0].ID, jit.X9)
	if dataReg != jit.X9 {
		asm.MOVreg(jit.X9, dataReg)
	}
	lenReg := ec.resolveRawInt(instr.Args[1].ID, jit.X10)
	if lenReg != jit.X10 {
		asm.MOVreg(jit.X10, lenReg)
	}
	limitReg := ec.resolveRawInt(instr.Args[2].ID, jit.X11)
	if limitReg != jit.X11 {
		asm.MOVreg(jit.X11, limitReg)
	}
	scalarRegs := []jit.FReg{jit.D6, jit.D7}
	for i := 0; i < spec.ScalarCount; i++ {
		src := ec.resolveRawFloat(instr.Args[3+i].ID, scalarRegs[i])
		if src != scalarRegs[i] {
			asm.FMOVd(scalarRegs[i], src)
		}
	}

	deoptLabel := ec.uniqueLabel("record_array_kernel_deopt")
	doneLabel := ec.uniqueLabel("record_array_kernel_done")
	validateLoop := ec.uniqueLabel("record_array_kernel_validate")
	validateDone := ec.uniqueLabel("record_array_kernel_validate_done")
	updateLoop := ec.uniqueLabel("record_array_kernel_loop")

	asm.CMPimm(jit.X11, 0)
	asm.BCond(jit.CondLE, doneLabel)
	asm.CMPreg(jit.X11, jit.X10)
	asm.BCond(jit.CondGE, deoptLabel)

	asm.MOVimm16(jit.X8, 1)
	asm.Label(validateLoop)
	asm.LDRreg(jit.X0, jit.X9, jit.X8)
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	emitCMPWConst(asm, jit.X1, jit.X2, int64(spec.ShapeID))
	asm.BCond(jit.CondNE, deoptLabel)
	asm.CMPreg(jit.X8, jit.X11)
	asm.BCond(jit.CondEQ, validateDone)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(validateLoop)

	fieldRegs := []jit.FReg{jit.D0, jit.D1, jit.D2, jit.D3, jit.D4, jit.D5}
	opRegs := []jit.FReg{jit.D8, jit.D9, jit.D10, jit.D11, jit.D12, jit.D13, jit.D14, jit.D15, jit.D16, jit.D17}
	sourceReg := func(src RecordArrayKernelSource) jit.FReg {
		switch src.Kind {
		case RecordArrayKernelSourceField:
			return fieldRegs[src.Index]
		case RecordArrayKernelSourceScalar:
			return scalarRegs[src.Index]
		case RecordArrayKernelSourceOp:
			return opRegs[src.Index]
		default:
			return jit.D0
		}
	}

	asm.Label(validateDone)
	asm.MOVimm16(jit.X8, 1)
	asm.Label(updateLoop)
	asm.LDRreg(jit.X0, jit.X9, jit.X8)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.LDR(jit.X12, jit.X0, jit.TableOffSvals)
	for i, field := range spec.FieldLoads {
		asm.FLDRd(fieldRegs[i], jit.X12, field*jit.ValueSize)
	}
	for i, op := range spec.Ops {
		dst := opRegs[i]
		switch op.Kind {
		case RecordArrayKernelFloatOpMul:
			asm.FMULd(dst, sourceReg(op.A), sourceReg(op.B))
		case RecordArrayKernelFloatOpFMA:
			asm.FMADDd(dst, sourceReg(op.A), sourceReg(op.B), sourceReg(op.C))
		default:
			ec.emitPreciseDeopt(instr)
			return
		}
	}
	for _, store := range spec.Stores {
		asm.FSTRd(sourceReg(store.Value), jit.X12, store.Field*jit.ValueSize)
	}
	asm.CMPreg(jit.X8, jit.X11)
	asm.BCond(jit.CondEQ, doneLabel)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(updateLoop)

	asm.Label(deoptLabel)
	ec.emitPreciseDeopt(instr)
	asm.Label(doneLabel)
}

func validRecordArrayLoopKernelSpec(spec RecordArrayLoopKernelSpec, scalarArgs int) bool {
	if spec.ShapeID == 0 || spec.ScalarCount < 0 || spec.ScalarCount > 2 || spec.ScalarCount > scalarArgs {
		return false
	}
	if len(spec.FieldLoads) == 0 || len(spec.FieldLoads) > 6 || len(spec.Ops) > 10 || len(spec.Stores) == 0 {
		return false
	}
	for _, field := range spec.FieldLoads {
		if field < 0 || field > spec.MaxField {
			return false
		}
	}
	checkSource := func(src RecordArrayKernelSource, opLimit int) bool {
		switch src.Kind {
		case RecordArrayKernelSourceField:
			return src.Index >= 0 && src.Index < len(spec.FieldLoads)
		case RecordArrayKernelSourceScalar:
			return src.Index >= 0 && src.Index < spec.ScalarCount
		case RecordArrayKernelSourceOp:
			return src.Index >= 0 && src.Index < opLimit
		default:
			return false
		}
	}
	for i, op := range spec.Ops {
		if !checkSource(op.A, i) || !checkSource(op.B, i) {
			return false
		}
		if op.Kind == RecordArrayKernelFloatOpFMA && !checkSource(op.C, i) {
			return false
		}
	}
	for _, store := range spec.Stores {
		if store.Field < 0 || store.Field > spec.MaxField || !checkSource(store.Value, len(spec.Ops)) {
			return false
		}
	}
	return true
}
