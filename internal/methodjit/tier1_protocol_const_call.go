//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

func baselineProtocolConstCallFolds(proto *vm.FuncProto) map[int]ProtocolConstCallFoldFact {
	globals := buildProtoInlineGlobals(proto)
	if len(globals) == 0 {
		globals = buildProtoStableGlobals(proto)
	}
	if len(globals) == 0 {
		return nil
	}
	fn := AnnotateProtocolConstCallFolds(BuildGraph(proto), globals)
	if fn == nil || len(fn.ProtocolConstCallFolds) == 0 {
		return nil
	}
	byPC := make(map[int]ProtocolConstCallFoldFact, len(fn.ProtocolConstCallFolds))
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpCall || !instr.HasSource || instr.SourcePC < 0 {
				continue
			}
			if fact, ok := fn.ProtocolConstCallFolds[instr.ID]; ok {
				byPC[instr.SourcePC] = fact
			}
		}
	}
	if len(byPC) == 0 {
		return nil
	}
	return byPC
}

func emitBaselineProtocolConstCallIfEligible(asm *jit.Assembler, inst uint32, pc int, proto *vm.FuncProto, folds map[int]ProtocolConstCallFoldFact) bool {
	fact, ok := folds[pc]
	if !ok || fact.CalleeProto == nil || len(fact.GuardConsts) != len(fact.GuardProtos) {
		return false
	}
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	if b == 0 || c != 2 {
		return false
	}

	slowLabel := nextLabel("protocol_const_call_slow")
	doneLabel := nextLabel("protocol_const_call_done")

	asm.LDR(jit.X0, mRegCtx, execCtxOffTier2GlobalVerPtr)
	asm.CBZ(jit.X0, slowLabel)
	asm.LDR(jit.X1, jit.X0, 0)
	asm.LDR(jit.X2, mRegCtx, execCtxOffTier2GlobalVer)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)

	asm.LoadImm64(jit.X0, fact.Result)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	asm.Label(slowLabel)
	emitBaselineNativeCall(asm, inst, pc, proto)
	asm.Label(doneLabel)
	return true
}
