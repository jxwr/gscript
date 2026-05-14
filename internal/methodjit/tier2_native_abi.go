//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

const (
	callModeDirect           = 1
	callModeTypedSelf        = 2
	callModeLeafX0           = 3
	callModeTypedPeerClobber = 4

	tier2LeafEntryTag = 1
)

func (ec *emitContext) emitLoadCallMode(dst jit.Reg) {
	ec.asm.LDR(dst, mRegCtx, execCtxOffCallMode)
}

func (ec *emitContext) emitStoreCallMode(src jit.Reg) {
	ec.asm.STR(src, mRegCtx, execCtxOffCallMode)
}

// emitTaggedLeafEntryIfAvailable rewrites entryReg to a tagged leaf-entry
// pointer when protoReg has a published Tier2LeafEntryPtr and its optimized
// Tier 2 body has no nested call/yield/resume operations. Bytecode-level
// LeafNoCall is intentionally not used here: intrinsics may lower bytecode
// calls away before codegen.
func (ec *emitContext) emitTaggedLeafEntryIfAvailable(protoReg, entryReg, tmpReg jit.Reg) {
	notLeafLabel := ec.uniqueLabel("t2call_not_leaf_entry")
	asm := ec.asm
	asm.LDRB(tmpReg, protoReg, funcProtoOffTier2LeafNoCall)
	asm.CBZ(tmpReg, notLeafLabel)
	asm.LDR(tmpReg, protoReg, funcProtoOffTier2LeafEntryPtr)
	asm.CBZ(tmpReg, notLeafLabel)
	asm.ADDimm(entryReg, tmpReg, tier2LeafEntryTag)
	asm.Label(notLeafLabel)
}

// emitDecodeTaggedPeerEntry turns a cached call-entry word into an aligned code
// pointer plus a CallMode value. Untagged entries use the standard direct ABI;
// tagged leaf entries use the Tier2-only boxed-X0 return ABI.
func (ec *emitContext) emitDecodeTaggedPeerEntry(entryReg, modeReg jit.Reg) {
	asm := ec.asm
	asm.MOVimm16(modeReg, callModeDirect)
	untaggedEntryLabel := ec.uniqueLabel("t2call_untagged_entry")
	asm.TBZ(entryReg, 0, untaggedEntryLabel)
	asm.SUBimm(entryReg, entryReg, tier2LeafEntryTag)
	asm.MOVimm16(modeReg, callModeLeafX0)
	asm.Label(untaggedEntryLabel)
}
