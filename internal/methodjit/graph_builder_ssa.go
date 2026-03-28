// graph_builder_ssa.go implements SSA variable resolution for the graph builder.
// Core of the Braun et al. 2013 algorithm: readVariable, writeVariable,
// phi insertion at merge points, trivial phi removal, and block sealing.
// Called by graph_builder.go during the bytecode walk.

package methodjit

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// --------------------------------------------------------------------
// Block sealing
// --------------------------------------------------------------------

func (b *graphBuilder) sealBlock(block *Block) {
	if block.sealed {
		return
	}
	for _, ip := range block.incomplete {
		b.addPhiOperands(ip.slot, ip.phi, block)
	}
	block.incomplete = nil
	block.sealed = true
}

// --------------------------------------------------------------------
// SSA variable read/write (Braun et al.)
// --------------------------------------------------------------------

func (b *graphBuilder) writeVariable(slot int, block *Block, val *Value) {
	block.defs[slot] = val
}

func (b *graphBuilder) readVariable(slot int, block *Block) *Value {
	if val, ok := block.defs[slot]; ok {
		return val
	}
	return b.readVariableRecursive(slot, block)
}

func (b *graphBuilder) readVariableRecursive(slot int, block *Block) *Value {
	var val *Value
	if !block.sealed {
		// Block not sealed yet (e.g., loop header) — insert incomplete phi.
		phi := b.newPhi(slot, block)
		block.incomplete = append(block.incomplete, incompletePhi{slot, phi})
		val = phi.Value()
	} else if len(block.Preds) == 0 {
		// Entry block, no predecessors — this is a function parameter or
		// uninitialized register. Emit a LoadSlot.
		instr := b.emit(block, OpLoadSlot, TypeAny, nil, int64(slot), 0)
		val = instr.Value()
	} else if len(block.Preds) == 1 {
		// Single predecessor — no phi needed, recurse.
		val = b.readVariable(slot, block.Preds[0])
	} else {
		// Multiple predecessors — insert phi then add operands.
		phi := b.newPhi(slot, block)
		b.writeVariable(slot, block, phi.Value())
		val = b.addPhiOperands(slot, phi, block)
	}
	b.writeVariable(slot, block, val)
	return val
}

// --------------------------------------------------------------------
// Phi node management
// --------------------------------------------------------------------

func (b *graphBuilder) newPhi(slot int, block *Block) *Instr {
	phi := &Instr{
		ID:    b.fn.newValueID(),
		Op:    OpPhi,
		Type:  TypeAny,
		Block: block,
		Aux:   int64(slot),
	}
	// Phis go at the beginning of the block.
	block.Instrs = append([]*Instr{phi}, block.Instrs...)
	return phi
}

func (b *graphBuilder) addPhiOperands(slot int, phi *Instr, block *Block) *Value {
	for _, pred := range block.Preds {
		val := b.readVariable(slot, pred)
		phi.Args = append(phi.Args, val)
	}
	return b.tryRemoveTrivialPhi(phi)
}

func (b *graphBuilder) tryRemoveTrivialPhi(phi *Instr) *Value {
	var same *Value
	phiVal := phi.Value()
	for _, arg := range phi.Args {
		if arg.ID == phiVal.ID {
			continue // self-reference
		}
		if same != nil && arg.ID != same.ID {
			return phiVal // non-trivial phi: at least two distinct inputs
		}
		same = arg
	}
	if same == nil {
		// All inputs are self — unreachable, return the phi itself.
		return phiVal
	}
	// Trivial phi: replace with `same`. Remove the phi from the block.
	b.removePhi(phi)
	// Replace all uses of phi with same (in defs maps).
	b.replaceValue(phiVal, same)
	return same
}

func (b *graphBuilder) removePhi(phi *Instr) {
	block := phi.Block
	for i, instr := range block.Instrs {
		if instr == phi {
			block.Instrs = append(block.Instrs[:i], block.Instrs[i+1:]...)
			return
		}
	}
}

func (b *graphBuilder) replaceValue(old, new *Value) {
	// Replace in all block defs.
	for _, blk := range b.fn.Blocks {
		for slot, val := range blk.defs {
			if val.ID == old.ID {
				blk.defs[slot] = new
			}
		}
		// Replace in instruction args.
		for _, instr := range blk.Instrs {
			for i, arg := range instr.Args {
				if arg.ID == old.ID {
					instr.Args[i] = new
				}
			}
		}
	}
}

// --------------------------------------------------------------------
// Builder utilities
// --------------------------------------------------------------------

// sortInts sorts a slice of ints in ascending order (simple insertion sort for small slices).
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}

func (b *graphBuilder) blockForPC(pc int) *Block {
	if blk, ok := b.pcToBlock[pc]; ok {
		return blk
	}
	// Should not happen if findLeaders is correct, but be safe.
	blk := b.newBlock()
	b.pcToBlock[pc] = blk
	return blk
}

func (b *graphBuilder) currentBlockForPC(pc int) *Block {
	// Find the block whose start PC is <= pc.
	var best int = -1
	for startPC := range b.pcToBlock {
		if startPC <= pc && startPC > best {
			best = startPC
		}
	}
	if best >= 0 {
		return b.pcToBlock[best]
	}
	return nil
}

// --------------------------------------------------------------------
// RK operand resolution
// --------------------------------------------------------------------

func (b *graphBuilder) resolveRK(rk int, block *Block) *Value {
	if vm.IsRK(rk) {
		return b.emitConstant(vm.RKToConstIdx(rk), block)
	}
	return b.readVariable(rk, block)
}

func (b *graphBuilder) emitConstant(constIdx int, block *Block) *Value {
	k := b.proto.Constants[constIdx]
	switch {
	case k.IsInt():
		instr := b.emit(block, OpConstInt, TypeInt, nil, k.Int(), 0)
		return instr.Value()
	case k.IsFloat():
		instr := b.emit(block, OpConstFloat, TypeFloat, nil, int64(math.Float64bits(k.Float())), 0)
		return instr.Value()
	case k.IsString():
		instr := b.emit(block, OpConstString, TypeString, nil, int64(constIdx), 0)
		return instr.Value()
	case k.IsBool():
		aux := int64(0)
		if k.Bool() {
			aux = 1
		}
		instr := b.emit(block, OpConstBool, TypeBool, nil, aux, 0)
		return instr.Value()
	case k.IsNil():
		instr := b.emit(block, OpConstNil, TypeNil, nil, 0, 0)
		return instr.Value()
	default:
		// Fallback: treat as opaque constant.
		instr := b.emit(block, OpConstNil, TypeAny, nil, 0, 0)
		return instr.Value()
	}
}

// Helper to check if a constant pool entry is a number and return its value.
func constAsFloat(k runtime.Value) (float64, bool) {
	if k.IsInt() {
		return float64(k.Int()), true
	}
	if k.IsFloat() {
		return k.Float(), true
	}
	return 0, false
}
