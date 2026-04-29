// graph_builder.go converts GScript bytecode (FuncProto) into CFG-based SSA IR.
// Uses the Braun et al. 2013 algorithm for SSA construction: single forward pass
// over bytecode with lazy phi insertion at join points.
// See graph_builder_ssa.go for the SSA variable resolution functions.

package methodjit

import (
	"github.com/gscript/gscript/internal/vm"
)

// BuildGraph converts a FuncProto's bytecode into CFG SSA IR using the
// Braun et al. (2013) algorithm for single-pass SSA construction.
func BuildGraph(proto *vm.FuncProto) *Function {
	b := &graphBuilder{
		fn: &Function{
			Proto:   proto,
			NumRegs: proto.MaxStack,
		},
		proto:           proto,
		pcToBlock:       make(map[int]*Block),
		currentPC:       -1,
		lastMultiRetReg: -1,
	}
	b.build()
	return b.fn
}

// graphBuilder holds transient state for the SSA construction pass.
type graphBuilder struct {
	fn              *Function
	proto           *vm.FuncProto
	pcToBlock       map[int]*Block // maps PC → Block that starts at that PC
	nextBlock       int            // next block ID
	backEdgeTargets map[int]bool   // PCs that are targets of backward jumps (loop headers)
	currentPC       int            // bytecode PC currently being translated; -1 for synthetic IR
	lastMultiRetReg int            // register set by preceding CALL/VARARG with C=0; -1 = no pending top
}

// --------------------------------------------------------------------
// Block / instruction helpers
// --------------------------------------------------------------------

func (b *graphBuilder) newBlock() *Block {
	blk := &Block{
		ID:   b.nextBlock,
		defs: make(map[int]*Value),
	}
	b.nextBlock++
	b.fn.Blocks = append(b.fn.Blocks, blk)
	return blk
}

func (b *graphBuilder) emit(block *Block, op Op, typ Type, args []*Value, aux, aux2 int64) *Instr {
	instr := &Instr{
		ID:    b.fn.newValueID(),
		Op:    op,
		Type:  typ,
		Args:  args,
		Aux:   aux,
		Aux2:  aux2,
		Block: block,
	}
	instr.setSourceFromPC(b.proto, b.currentPC)
	block.Instrs = append(block.Instrs, instr)
	return instr
}

func (b *graphBuilder) addEdge(from, to *Block) {
	from.Succs = append(from.Succs, to)
	to.Preds = append(to.Preds, from)
}

// --------------------------------------------------------------------
// Build: main algorithm
// --------------------------------------------------------------------

func (b *graphBuilder) build() {
	code := b.proto.Code
	if len(code) == 0 {
		entry := b.newBlock()
		entry.sealed = true
		b.fn.Entry = entry
		b.emit(entry, OpReturn, TypeUnknown, nil, 0, 0)
		return
	}

	// Step 1: Find block boundaries.
	leaders := b.findLeaders()

	// Create blocks for each leader PC.
	for _, pc := range leaders {
		blk := b.newBlock()
		b.pcToBlock[pc] = blk
	}
	b.fn.Entry = b.pcToBlock[0]

	// Step 2: Forward pass — emit instructions and wire edges.
	b.emitBlocks()

	// Step 3: Seal any remaining unsealed blocks (should only be loop headers
	// that were sealed when the back-edge was processed, but just in case).
	for _, blk := range b.fn.Blocks {
		b.sealBlock(blk)
	}

	// Step 4: Cleanup — ensure all blocks are well-formed.
	b.currentPC = -1
	b.cleanup()
}

// cleanup ensures all blocks are well-formed:
// - Every block has at least one instruction (the terminator).
// - Dead blocks (unreachable, no predecessors, not entry) are removed.
func (b *graphBuilder) cleanup() {
	// 1. Ensure every block has a terminator.
	for _, blk := range b.fn.Blocks {
		if len(blk.Instrs) == 0 {
			b.emit(blk, OpReturn, TypeUnknown, nil, 0, 0)
		} else {
			last := blk.Instrs[len(blk.Instrs)-1]
			if !last.Op.IsTerminator() {
				b.emit(blk, OpReturn, TypeUnknown, nil, 0, 0)
			}
		}
	}

	// 2. Remove dead blocks (no predecessors and not the entry block).
	alive := make([]*Block, 0, len(b.fn.Blocks))
	for _, blk := range b.fn.Blocks {
		if blk == b.fn.Entry || len(blk.Preds) > 0 {
			alive = append(alive, blk)
		} else {
			// Remove this block from its successors' predecessor lists.
			for _, succ := range blk.Succs {
				newPreds := make([]*Block, 0, len(succ.Preds))
				for _, p := range succ.Preds {
					if p != blk {
						newPreds = append(newPreds, p)
					}
				}
				succ.Preds = newPreds
			}
		}
	}
	b.fn.Blocks = alive
}

func (b *graphBuilder) findLeaders() []int {
	code := b.proto.Code
	leaderSet := map[int]bool{0: true}
	b.backEdgeTargets = make(map[int]bool)

	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		op := vm.DecodeOp(inst)

		switch op {
		case vm.OP_JMP:
			sbx := vm.DecodesBx(inst)
			target := pc + 1 + sbx
			leaderSet[target] = true
			// Backward jump: target <= pc means it's a loop back-edge.
			if target <= pc {
				b.backEdgeTargets[target] = true
			}
			// Fall-through after JMP is also a leader (if reachable).
			if pc+1 < len(code) {
				leaderSet[pc+1] = true
			}

		case vm.OP_FORPREP:
			sbx := vm.DecodesBx(inst)
			target := pc + 1 + sbx
			leaderSet[target] = true
			if pc+1 < len(code) {
				leaderSet[pc+1] = true
			}

		case vm.OP_FORLOOP:
			sbx := vm.DecodesBx(inst)
			target := pc + 1 + sbx
			leaderSet[target] = true
			// FORLOOP always jumps backward to the loop body.
			if target <= pc {
				b.backEdgeTargets[target] = true
			}
			if pc+1 < len(code) {
				leaderSet[pc+1] = true
			}

		case vm.OP_TFORLOOP:
			sbx := vm.DecodesBx(inst)
			target := pc + 1 + sbx
			leaderSet[target] = true
			if target <= pc {
				b.backEdgeTargets[target] = true
			}
			if pc+1 < len(code) {
				leaderSet[pc+1] = true
			}

		case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET:
			// Comparison + skip: the instruction after the following JMP
			// is a leader, and the JMP target is a leader.
			if pc+1 < len(code) {
				jmpInst := code[pc+1]
				if vm.DecodeOp(jmpInst) == vm.OP_JMP {
					jmpSbx := vm.DecodesBx(jmpInst)
					jmpTarget := pc + 2 + jmpSbx
					leaderSet[jmpTarget] = true
					if jmpTarget <= pc {
						b.backEdgeTargets[jmpTarget] = true
					}
					leaderSet[pc+2] = true
				}
			}

		case vm.OP_RETURN:
			if pc+1 < len(code) {
				leaderSet[pc+1] = true
			}

		case vm.OP_LOADBOOL:
			// LOADBOOL A B C: if C != 0 then skip next instruction.
			c := vm.DecodeC(inst)
			if c != 0 && pc+2 < len(code) {
				leaderSet[pc+2] = true
			}
		}
	}

	// Sort leaders.
	sorted := make([]int, 0, len(leaderSet))
	for pc := range leaderSet {
		if pc >= 0 && pc < len(code) {
			sorted = append(sorted, pc)
		}
	}
	sortInts(sorted)
	return sorted
}

func (b *graphBuilder) emitBlocks() {
	code := b.proto.Code
	numParams := b.proto.NumParams

	// Entry block: load parameters into initial variable defs.
	//
	// If PC=0 is a back-edge target (while-loop at function start), the block
	// at PC=0 is a loop header. In the Braun algorithm, loop headers must remain
	// unsealed until all predecessors are known. We create a synthetic pre-header
	// block that loads the parameters and jumps to the actual loop header. This
	// ensures the loop header's reads of R0, R1, etc. trigger phi insertion
	// (Braun et al. 2013).
	entry := b.fn.Entry
	if b.backEdgeTargets[0] {
		// Create pre-header: becomes the new entry, jumps to the original PC=0 block.
		preHeader := b.newBlock()
		preHeader.sealed = true
		b.fn.Entry = preHeader

		// Load parameters in the pre-header.
		for i := 0; i < numParams; i++ {
			instr := b.emit(preHeader, OpLoadSlot, TypeAny, nil, int64(i), 0)
			b.writeVariable(i, preHeader, instr.Value())
		}

		// Edge from pre-header to the loop header at PC=0.
		b.addEdge(preHeader, entry)
		b.emit(preHeader, OpJump, TypeUnknown, nil, 0, 0)
		// entry (PC=0 block) stays unsealed — it's a loop header.
	} else {
		entry.sealed = true
		for i := 0; i < numParams; i++ {
			instr := b.emit(entry, OpLoadSlot, TypeAny, nil, int64(i), 0)
			b.writeVariable(i, entry, instr.Value())
		}
	}

	// Build a sorted list of block start PCs for quick lookup.
	blockStarts := make([]int, 0, len(b.pcToBlock))
	for pc := range b.pcToBlock {
		blockStarts = append(blockStarts, pc)
	}
	sortInts(blockStarts)

	// Build a map from start PC to next start PC for fast block boundary checks.
	blockEndPC := make(map[int]int) // start PC → exclusive end PC
	for i, startPC := range blockStarts {
		if i+1 < len(blockStarts) {
			blockEndPC[startPC] = blockStarts[i+1]
		} else {
			blockEndPC[startPC] = len(code)
		}
	}

	// Track which blocks we've processed terminators for.
	terminated := make(map[int]bool)

	for _, startPC := range blockStarts {
		block := b.pcToBlock[startPC]
		endPC := blockEndPC[startPC]

		// Reset top-tracking at block boundaries. lastMultiRetReg only
		// applies within a single basic block (C=0 call immediately
		// precedes the B=0 call in the same block).
		b.lastMultiRetReg = -1

		for pc := startPC; pc < endPC; pc++ {
			b.currentPC = pc
			inst := code[pc]
			op := vm.DecodeOp(inst)
			b.currentPC = pc

			switch op {
			case vm.OP_MOVE:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				val := b.readVariable(bOp, block)
				b.writeVariable(a, block, val)

			case vm.OP_LOADNIL:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				for i := a; i <= a+bOp; i++ {
					instr := b.emit(block, OpConstNil, TypeNil, nil, 0, 0)
					b.writeVariable(i, block, instr.Value())
				}

			case vm.OP_LOADBOOL:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				aux := int64(0)
				if bOp != 0 {
					aux = 1
				}
				instr := b.emit(block, OpConstBool, TypeBool, nil, aux, 0)
				b.writeVariable(a, block, instr.Value())
				if c != 0 {
					// Skip next instruction (used for if/else bool patterns).
					pc++
				}

			case vm.OP_LOADINT:
				a := vm.DecodeA(inst)
				sbx := vm.DecodesBx(inst)
				instr := b.emit(block, OpConstInt, TypeInt, nil, int64(sbx), 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_LOADK:
				a := vm.DecodeA(inst)
				bx := vm.DecodeBx(inst)
				val := b.emitConstant(bx, block)
				b.writeVariable(a, block, val)

			case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD, vm.OP_POW:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				lhs := b.resolveRK(bOp, block)
				rhs := b.resolveRK(c, block)
				var irOp Op
				switch op {
				case vm.OP_ADD:
					irOp = OpAdd
				case vm.OP_SUB:
					irOp = OpSub
				case vm.OP_MUL:
					irOp = OpMul
				case vm.OP_DIV:
					irOp = OpDiv
				case vm.OP_MOD:
					irOp = OpMod
				case vm.OP_POW:
					irOp = OpPow
				}
				instr := b.emit(block, irOp, TypeAny, []*Value{lhs, rhs}, 0, 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_UNM:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				val := b.readVariable(bOp, block)
				instr := b.emit(block, OpUnm, TypeAny, []*Value{val}, 0, 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_NOT:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				val := b.readVariable(bOp, block)
				instr := b.emit(block, OpNot, TypeBool, []*Value{val}, 0, 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_LEN:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				val := b.readVariable(bOp, block)
				instr := b.emit(block, OpLen, TypeAny, []*Value{val}, 0, 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_CONCAT:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				args := make([]*Value, 0, c-bOp+1)
				for i := bOp; i <= c; i++ {
					args = append(args, b.readVariable(i, block))
				}
				instr := b.emit(block, OpConcat, TypeString, args, 0, 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				lhs := b.resolveRK(bOp, block)
				rhs := b.resolveRK(c, block)
				var cmpOp Op
				switch op {
				case vm.OP_EQ:
					cmpOp = OpEq
				case vm.OP_LT:
					cmpOp = OpLt
				case vm.OP_LE:
					cmpOp = OpLe
				}

				// R82 Layer 2: consult bytecode-level feedback for OP_LE/LT/EQ
				// to insert GuardType on operands. TypeSpec will then rewrite
				// the generic OpLe to OpLeInt/OpLeFloat, avoiding the buggy
				// raw-int compare path for NaN-boxed floats. The guard itself
				// deopts on type mismatch (rare after mono-typed feedback).
				if b.proto.Feedback != nil && pc < len(b.proto.Feedback) {
					fb := b.proto.Feedback[pc]
					guardFeedbackOperands := op != vm.OP_EQ || (!graphValueIsConstNil(lhs) && !graphValueIsConstNil(rhs))
					if guardFeedbackOperands {
						if lhsType, ok := feedbackToIRType(fb.Left); ok {
							g := b.emit(block, OpGuardType, lhsType, []*Value{lhs}, int64(lhsType), 0)
							lhs = g.Value()
						}
						if rhsType, ok := feedbackToIRType(fb.Right); ok {
							g := b.emit(block, OpGuardType, rhsType, []*Value{rhs}, int64(rhsType), 0)
							rhs = g.Value()
						}
					}
				}

				cond := b.emit(block, cmpOp, TypeBool, []*Value{lhs, rhs}, 0, 0)

				// Next instruction must be OP_JMP.
				pc++
				jmpInst := code[pc]
				jmpSbx := vm.DecodesBx(jmpInst)
				jmpTarget := pc + 1 + jmpSbx
				fallthroughPC := pc + 1

				trueBlock := b.blockForPC(fallthroughPC)
				falseBlock := b.blockForPC(jmpTarget)

				// A=0: skip next if condition is FALSE → branch on TRUE to fallthrough.
				// A=1: skip next if condition is TRUE → branch on TRUE to jump target.
				if a == 0 {
					// Condition true → fallthrough, false → jump target.
					b.addEdge(block, trueBlock)
					b.addEdge(block, falseBlock)
				} else {
					// Condition true → jump target, false → fallthrough.
					b.addEdge(block, falseBlock)
					b.addEdge(block, trueBlock)
				}
				b.emit(block, OpBranch, TypeUnknown, []*Value{cond.Value()}, 0, 0)
				terminated[startPC] = true

			case vm.OP_TEST:
				a := vm.DecodeA(inst)
				c := vm.DecodeC(inst)
				val := b.readVariable(a, block)
				// GuardTruthy tests truthiness of val.
				cond := b.emit(block, OpGuardTruthy, TypeBool, []*Value{val}, 0, 0)

				// Next instruction must be OP_JMP.
				pc++
				jmpInst := code[pc]
				jmpSbx := vm.DecodesBx(jmpInst)
				jmpTarget := pc + 1 + jmpSbx
				fallthroughPC := pc + 1

				trueBlock := b.blockForPC(fallthroughPC)
				falseBlock := b.blockForPC(jmpTarget)

				// C=0: skip next if truthy → truthy goes to jump target (skip means don't fall through)
				// Wait, let me re-read: OP_TEST A C: if bool(R(A)) != bool(C) then PC++ (skip next).
				// So if C=1: skip if NOT truthy → falsy skips the JMP → falsy falls through past JMP.
				// If C=0: skip if truthy → truthy skips the JMP → truthy falls through past JMP.
				if c == 0 {
					// Truthy → skip JMP → fallthrough. Falsy → execute JMP → jump target.
					b.addEdge(block, trueBlock)  // truthy → fallthrough (Succs[0])
					b.addEdge(block, falseBlock) // falsy → jump target (Succs[1])
				} else {
					// Falsy → skip JMP → fallthrough. Truthy → execute JMP → jump target.
					b.addEdge(block, falseBlock) // falsy → fallthrough (Succs[0])
					b.addEdge(block, trueBlock)  // truthy → jump target (Succs[1])
				}
				b.emit(block, OpBranch, TypeUnknown, []*Value{cond.Value()}, 0, 0)
				terminated[startPC] = true

			case vm.OP_TESTSET:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				val := b.readVariable(bOp, block)
				cond := b.emit(block, OpGuardTruthy, TypeBool, []*Value{val}, 0, 0)

				// If the test passes (doesn't skip), R(A) = R(B).
				// We handle this by writing the variable in both successor blocks.
				// For now, write it in the current block (conservative).
				b.writeVariable(a, block, val)

				pc++
				jmpInst := code[pc]
				jmpSbx := vm.DecodesBx(jmpInst)
				jmpTarget := pc + 1 + jmpSbx
				fallthroughPC := pc + 1

				trueBlock := b.blockForPC(fallthroughPC)
				falseBlock := b.blockForPC(jmpTarget)

				if c == 0 {
					b.addEdge(block, trueBlock)
					b.addEdge(block, falseBlock)
				} else {
					b.addEdge(block, falseBlock)
					b.addEdge(block, trueBlock)
				}
				b.emit(block, OpBranch, TypeUnknown, []*Value{cond.Value()}, 0, 0)
				terminated[startPC] = true

			case vm.OP_JMP:
				sbx := vm.DecodesBx(inst)
				target := pc + 1 + sbx
				targetBlock := b.blockForPC(target)
				b.addEdge(block, targetBlock)
				b.emit(block, OpJump, TypeUnknown, nil, 0, 0)
				terminated[startPC] = true

			case vm.OP_RETURN:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				var args []*Value
				if bOp == 1 {
					// Return nothing.
				} else if bOp >= 2 {
					for i := a; i <= a+bOp-2; i++ {
						args = append(args, b.readVariable(i, block))
					}
				} else {
					// bOp == 0: return to top (variable returns).
					// For now, emit return of R(A).
					args = append(args, b.readVariable(a, block))
				}
				b.emit(block, OpReturn, TypeUnknown, args, 0, 0)
				terminated[startPC] = true

			case vm.OP_CALL:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				fn := b.readVariable(a, block)
				var args []*Value
				args = append(args, fn)
				if bOp >= 2 {
					for i := a + 1; i <= a+bOp-1; i++ {
						args = append(args, b.readVariable(i, block))
					}
				} else if bOp == 0 {
					// B==0: arguments run from A+1 to current top, where
					// top is set by a preceding variadic-return op (a
					// CALL/VARARG/TFORCALL with C=0).
					// If a preceding CALL/VARARG with C=0 set lastMultiRetReg,
					// we know the top: args are A+1 through lastMultiRetReg (inclusive).
					if b.lastMultiRetReg >= 0 && b.lastMultiRetReg >= a+1 {
						for i := a + 1; i <= b.lastMultiRetReg; i++ {
							args = append(args, b.readVariable(i, block))
						}
						b.lastMultiRetReg = -1
					} else {
						// Cannot determine top statically. Mark unpromotable.
						b.fn.Unpromotable = true
					}
				}
				instr := b.emit(block, OpCall, TypeAny, args, int64(a), int64(c))
				b.writeVariable(a, block, instr.Value())
				// If C > 2, there are multiple return values.
				if c >= 3 {
					for i := 1; i < c-1; i++ {
						// Model extra return values as dependent on the call.
						// For now, write them as the same call result.
						b.writeVariable(a+i, block, instr.Value())
					}
				}
				// Track "top" for a subsequent B=0 call.
				// C=0 means variable returns; top = A (the call result register).
				// A subsequent B=0 call will read args from its own A+1 through this A.
				if c == 0 {
					b.lastMultiRetReg = a
				}

			case vm.OP_GETGLOBAL:
				a := vm.DecodeA(inst)
				bx := vm.DecodeBx(inst)
				instr := b.emit(block, OpGetGlobal, TypeAny, nil, int64(bx), 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_SETGLOBAL:
				a := vm.DecodeA(inst)
				bx := vm.DecodeBx(inst)
				val := b.readVariable(a, block)
				b.emit(block, OpSetGlobal, TypeUnknown, []*Value{val}, int64(bx), 0)

			case vm.OP_GETUPVAL:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				instr := b.emit(block, OpGetUpval, TypeAny, nil, int64(bOp), 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_SETUPVAL:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				val := b.readVariable(a, block)
				b.emit(block, OpSetUpval, TypeUnknown, []*Value{val}, int64(bOp), 0)

			case vm.OP_NEWTABLE:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				instr := b.emit(block, OpNewTable, TypeTable, nil, int64(bOp), int64(c))
				b.writeVariable(a, block, instr.Value())

			case vm.OP_NEWOBJECT2:
				a := vm.DecodeA(inst)
				ctorIdx := vm.DecodeB(inst)
				valueBase := vm.DecodeC(inst)
				newTable := b.emit(block, OpNewTable, TypeTable, nil, 0, 2)
				tbl := newTable.Value()
				b.writeVariable(a, block, tbl)
				if ctorIdx >= 0 && ctorIdx < len(b.proto.TableCtors2) {
					ctor := b.proto.TableCtors2[ctorIdx]
					if b.fn.FixedTableConstructors == nil {
						b.fn.FixedTableConstructors = make(map[int]FixedTableConstructorFact)
					}
					b.fn.FixedTableConstructors[newTable.ID] = FixedTableConstructorFact{
						Ctor2Index: ctorIdx,
						FieldNames: []string{ctor.Runtime.Key1, ctor.Runtime.Key2},
					}
					val1 := b.readVariable(valueBase, block)
					val2 := b.readVariable(valueBase+1, block)
					b.emit(block, OpSetField, TypeUnknown, []*Value{tbl, val1}, int64(ctor.Key1Const), 0)
					b.emit(block, OpSetField, TypeUnknown, []*Value{tbl, val2}, int64(ctor.Key2Const), 0)
				}

			case vm.OP_GETTABLE:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				tbl := b.readVariable(bOp, block)
				key := b.resolveRK(c, block)
				// Read kind feedback for array specialization.
				var kindAux2 int64
				if b.proto.Feedback != nil && pc < len(b.proto.Feedback) {
					kind := b.proto.Feedback[pc].Kind
					if kind != vm.FBKindUnobserved && kind != vm.FBKindPolymorphic {
						kindAux2 = int64(kind)
					}
				}
				resultType := TypeAny
				if b.proto.Feedback != nil && pc < len(b.proto.Feedback) && b.proto.Feedback[pc].Result == vm.FBTable && kindAux2 == int64(vm.FBKindMixed) {
					resultType = TypeTable
				}
				instr := b.emit(block, OpGetTable, resultType, []*Value{tbl, key}, 0, kindAux2)
				result := instr.Value()
				if b.proto.Feedback != nil && pc < len(b.proto.Feedback) {
					fb := b.proto.Feedback[pc]
					if irType, ok := feedbackToIRType(fb.Result); ok &&
						resultType != irType &&
						!getTableKindImpliesType(kindAux2, irType) &&
						!bytecodeSlotFeedsNilEq(b.proto, pc, a) {
						guard := b.emit(block, OpGuardType, irType, []*Value{result}, int64(irType), 0)
						result = guard.Value()
					}
				}
				b.writeVariable(a, block, result)

			case vm.OP_SETTABLE:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				tbl := b.readVariable(a, block)
				key := b.resolveRK(bOp, block)
				val := b.resolveRK(c, block)
				// Read kind feedback for array specialization.
				var setKindAux2 int64
				if b.proto.Feedback != nil && pc < len(b.proto.Feedback) {
					kind := b.proto.Feedback[pc].Kind
					if kind != vm.FBKindUnobserved && kind != vm.FBKindPolymorphic {
						setKindAux2 = int64(kind)
					}
				}
				b.emit(block, OpSetTable, TypeUnknown, []*Value{tbl, key, val}, 0, setKindAux2)

			case vm.OP_GETFIELD:
				// GETFIELD A B C: R(A) = R(B).Constants[C]
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				tbl := b.readVariable(bOp, block)
				// Capture field cache info: Aux2 = shapeID<<32 | fieldIndex
				var aux2 int64
				if b.proto.FieldCache != nil && pc < len(b.proto.FieldCache) {
					entry := b.proto.FieldCache[pc]
					if entry.ShapeID != 0 && entry.FieldIdx >= 0 {
						aux2 = int64(entry.ShapeID)<<32 | int64(uint32(entry.FieldIdx))
					}
				}
				instr := b.emit(block, OpGetField, TypeAny, []*Value{tbl}, int64(c), aux2)
				result := instr.Value()
				if b.proto.Feedback != nil && pc < len(b.proto.Feedback) {
					if irType, ok := feedbackToIRType(b.proto.Feedback[pc].Result); ok && !bytecodeSlotFeedsNilEq(b.proto, pc, a) {
						guard := b.emit(block, OpGuardType, irType, []*Value{result}, int64(irType), 0)
						result = guard.Value()
					}
				}
				b.writeVariable(a, block, result)

			case vm.OP_SETFIELD:
				// SETFIELD A B C: R(A).Constants[B] = RK(C)
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				tbl := b.readVariable(a, block)
				val := b.resolveRK(c, block)
				// Capture field cache info: Aux2 = shapeID<<32 | fieldIndex
				var aux2 int64
				if b.proto.FieldCache != nil && pc < len(b.proto.FieldCache) {
					entry := b.proto.FieldCache[pc]
					if entry.ShapeID != 0 && entry.FieldIdx >= 0 {
						aux2 = int64(entry.ShapeID)<<32 | int64(uint32(entry.FieldIdx))
					}
				}
				b.emit(block, OpSetField, TypeUnknown, []*Value{tbl, val}, int64(bOp), aux2)

			case vm.OP_SETLIST:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				// Aux = 1-based array start index: (C-1)*50+1
				arrayStart := int64((c-1)*50 + 1)
				tbl := b.readVariable(a, block)
				args := []*Value{tbl}
				for i := 1; i <= bOp; i++ {
					args = append(args, b.readVariable(a+i, block))
				}
				b.emit(block, OpSetList, TypeUnknown, args, arrayStart, 0)

			case vm.OP_APPEND:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				tbl := b.readVariable(a, block)
				val := b.readVariable(bOp, block)
				b.emit(block, OpAppend, TypeUnknown, []*Value{tbl, val}, 0, 0)

			case vm.OP_SELF:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				c := vm.DecodeC(inst)
				tbl := b.readVariable(bOp, block)
				key := b.resolveRK(c, block)
				instr := b.emit(block, OpSelf, TypeAny, []*Value{tbl, key}, 0, 0)
				// R(A+1) = R(B) (the table, for method self)
				b.writeVariable(a+1, block, tbl)
				// R(A) = R(B)[RK(C)] (the method)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_CLOSURE:
				a := vm.DecodeA(inst)
				bx := vm.DecodeBx(inst)
				instr := b.emit(block, OpClosure, TypeFunction, nil, int64(bx), 0)
				b.writeVariable(a, block, instr.Value())

			case vm.OP_CLOSE:
				a := vm.DecodeA(inst)
				b.emit(block, OpClose, TypeUnknown, nil, int64(a), 0)

			case vm.OP_FORPREP:
				a := vm.DecodeA(inst)
				sbx := vm.DecodesBx(inst)

				// R(A) -= R(A+2) (subtract step before loop)
				idx := b.readVariable(a, block)
				step := b.readVariable(a+2, block)
				// Annotate with TypeInt when for-loop vars are known-int.
				// This gives TypeSpec a head start on phi resolution.
				forType := b.inferForLoopType(a, block)
				prepped := b.emit(block, OpSub, forType, []*Value{idx, step}, 0, 1) // Aux2=1: loop counter, safe from int48 overflow
				b.writeVariable(a, block, prepped.Value())

				// Jump to FORLOOP test block.
				target := pc + 1 + sbx
				targetBlock := b.blockForPC(target)
				b.addEdge(block, targetBlock)
				b.emit(block, OpJump, TypeUnknown, nil, 0, 0)
				terminated[startPC] = true

			case vm.OP_FORLOOP:
				a := vm.DecodeA(inst)
				sbx := vm.DecodesBx(inst)

				// R(A) += R(A+2)
				idx := b.readVariable(a, block)
				step := b.readVariable(a+2, block)
				// Annotate with TypeInt when for-loop vars are known-int.
				forType := b.inferForLoopType(a, block)
				incremented := b.emit(block, OpAdd, forType, []*Value{idx, step}, 0, 1) // Aux2=1: loop counter, safe from int48 overflow
				b.writeVariable(a, block, incremented.Value())

				// if R(A) <= R(A+1) { R(A+3) = R(A); jump back }
				limit := b.readVariable(a+1, block)
				cond := b.emit(block, OpLe, TypeBool, []*Value{incremented.Value(), limit}, 0, 0)

				// R(A+3) = R(A) (loop variable exposed to body)
				b.writeVariable(a+3, block, incremented.Value())

				target := pc + 1 + sbx
				bodyBlock := b.blockForPC(target)
				exitPC := pc + 1
				exitBlock := b.blockForPC(exitPC)

				// Branch: true (in range) → body, false → exit.
				b.addEdge(block, bodyBlock)
				b.addEdge(block, exitBlock)
				b.emit(block, OpBranch, TypeUnknown, []*Value{cond.Value()}, 0, 0)
				terminated[startPC] = true

				// Seal the loop body block now that the back-edge is known.
				b.sealBlock(bodyBlock)

			case vm.OP_TFORCALL:
				// Generic for: R(A+3)..R(A+2+C) = R(A)(R(A+1), R(A+2))
				a := vm.DecodeA(inst)
				c := vm.DecodeC(inst)
				fn := b.readVariable(a, block)
				arg1 := b.readVariable(a+1, block)
				arg2 := b.readVariable(a+2, block)
				callInstr := b.emit(block, OpCall, TypeAny, []*Value{fn, arg1, arg2}, 0, 0)
				for i := 0; i < c; i++ {
					b.writeVariable(a+3+i, block, callInstr.Value())
				}

			case vm.OP_TFORLOOP:
				a := vm.DecodeA(inst)
				sbx := vm.DecodesBx(inst)
				// if R(A+1) != nil { R(A) = R(A+1); PC += sBx }
				val := b.readVariable(a+1, block)
				nilVal := b.emit(block, OpConstNil, TypeNil, nil, 0, 0)
				cond := b.emit(block, OpEq, TypeBool, []*Value{val, nilVal.Value()}, 0, 0)

				// Write R(A) = R(A+1) (done before branching, body will see it).
				b.writeVariable(a, block, val)

				target := pc + 1 + sbx
				bodyBlock := b.blockForPC(target)
				exitPC := pc + 1
				exitBlock := b.blockForPC(exitPC)

				// Eq checks for nil. If equal (nil), exit. If not nil, loop back.
				// Succs[0] = loop (not nil), Succs[1] = exit (nil).
				b.addEdge(block, bodyBlock)
				b.addEdge(block, exitBlock)
				// Use NotEq logic: branch on "not nil" → body.
				// Actually, the cond is "val == nil". We want:
				// if cond (is nil) → exit, else → body.
				// So Succs[0] should be exit (cond true), Succs[1] body (cond false).
				// But we wired body first. Let's use a Not.
				notCond := b.emit(block, OpNot, TypeBool, []*Value{cond.Value()}, 0, 0)
				b.emit(block, OpBranch, TypeUnknown, []*Value{notCond.Value()}, 0, 0)
				terminated[startPC] = true
				b.sealBlock(bodyBlock)

			case vm.OP_VARARG:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				// Emit OpVararg so it goes through op-exit. The Go-side handler
				// copies varargs from the VM frame into the register file.
				// Aux = dest register (a), Aux2 = count (b).
				instr := b.emit(block, OpVararg, TypeAny, nil, int64(a), int64(bOp))
				if bOp >= 2 {
					for i := 0; i < bOp-1; i++ {
						b.writeVariable(a+i, block, instr.Value())
					}
				} else {
					b.writeVariable(a, block, instr.Value())
				}
				// Track top for subsequent B=0 call.
				// B=0 means variable count; top = A (the vararg dest register).
				if bOp == 0 {
					b.lastMultiRetReg = a
				}

			case vm.OP_GO:
				// go R(A)(R(A+1)..R(A+B-1))
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				fn := b.readVariable(a, block)
				args := []*Value{fn}
				if bOp >= 2 {
					for i := a + 1; i <= a+bOp-1; i++ {
						args = append(args, b.readVariable(i, block))
					}
				}
				b.emit(block, OpCall, TypeAny, args, 1, 0) // Aux=1 to mark as goroutine

			case vm.OP_MAKECHAN:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				instr := b.emit(block, OpNop, TypeAny, nil, int64(bOp), 0) // placeholder
				b.writeVariable(a, block, instr.Value())

			case vm.OP_SEND:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				ch := b.readVariable(a, block)
				val := b.readVariable(bOp, block)
				b.emit(block, OpNop, TypeUnknown, []*Value{ch, val}, 0, 0)

			case vm.OP_RECV:
				a := vm.DecodeA(inst)
				bOp := vm.DecodeB(inst)
				ch := b.readVariable(bOp, block)
				instr := b.emit(block, OpNop, TypeAny, []*Value{ch}, 0, 0)
				b.writeVariable(a, block, instr.Value())

			default:
				// Unknown opcode — emit a Nop.
				b.emit(block, OpNop, TypeUnknown, nil, int64(op), 0)
			}
		}

		// If block is not terminated, add a fallthrough edge to the next block.
		b.currentPC = -1
		if !terminated[startPC] {
			nextPC := blockEndPC[startPC]
			if nextBlock, ok := b.pcToBlock[nextPC]; ok {
				b.addEdge(block, nextBlock)
				b.emit(block, OpJump, TypeUnknown, nil, 0, 0)
			} else if len(block.Instrs) == 0 || !block.Instrs[len(block.Instrs)-1].Op.IsTerminator() {
				// Implicit return at end of function.
				b.emit(block, OpReturn, TypeUnknown, nil, 0, 0)
			}
		}

		// Seal forward-target blocks whose predecessors are all known.
		// Loop headers are sealed when the back-edge is processed (in FORLOOP/TFORLOOP).
		// All other blocks can be sealed after we finish processing the block that
		// precedes them, since forward-only blocks get all predecessors before
		// we process them.
	}

	// Seal all blocks that are not yet sealed. For non-loop blocks,
	// all predecessors should be known by now.
	for _, blk := range b.fn.Blocks {
		b.sealBlock(blk)
	}
	b.currentPC = -1
}

// inferForLoopType checks if the for-loop's index (R(A)), limit (R(A+1)),
// and step (R(A+2)) are all known-int from their SSA definitions.
// Returns TypeInt if all three are int, TypeAny otherwise.
// This annotates FORPREP/FORLOOP arithmetic directly, giving the TypeSpec
// pass a head start on phi type resolution.
func (b *graphBuilder) inferForLoopType(a int, block *Block) Type {
	vars := [3]*Value{
		b.readVariable(a, block),   // index
		b.readVariable(a+1, block), // limit
		b.readVariable(a+2, block), // step
	}
	for _, v := range vars {
		if v == nil || v.Def == nil {
			return TypeAny
		}
		t := v.Def.Type
		if t != TypeInt {
			return TypeAny
		}
	}
	return TypeInt
}

// feedbackToIRType maps interpreter feedback types to IR types for guard insertion.
// Returns the IR type and true if the feedback is monomorphic and worth guarding.
func feedbackToIRType(fb vm.FeedbackType) (Type, bool) {
	switch fb {
	case vm.FBFloat:
		return TypeFloat, true
	case vm.FBInt:
		return TypeInt, true
	case vm.FBTable:
		return TypeTable, true
	default:
		return TypeAny, false
	}
}

func graphValueIsConstNil(v *Value) bool {
	return v != nil && v.Def != nil && v.Def.Op == OpConstNil
}

func bytecodeSlotFeedsNilEq(proto *vm.FuncProto, pc, slot int) bool {
	if proto == nil || slot < 0 {
		return false
	}
	nilSlots := make(map[int]bool)
	for scanPC := pc + 1; scanPC < len(proto.Code) && scanPC <= pc+3; scanPC++ {
		inst := proto.Code[scanPC]
		switch vm.DecodeOp(inst) {
		case vm.OP_LOADNIL:
			a := vm.DecodeA(inst)
			b := vm.DecodeB(inst)
			if slot >= a && slot <= a+b {
				return false
			}
			for r := a; r <= a+b; r++ {
				nilSlots[r] = true
			}
		case vm.OP_EQ:
			b := vm.DecodeB(inst)
			c := vm.DecodeC(inst)
			return (bytecodeRKIsSlot(b, slot) && bytecodeRKIsKnownNil(proto, c, nilSlots)) ||
				(bytecodeRKIsSlot(c, slot) && bytecodeRKIsKnownNil(proto, b, nilSlots))
		default:
			return false
		}
	}
	return false
}

func bytecodeRKIsSlot(rk, slot int) bool {
	return rk < vm.RKBit && rk == slot
}

func bytecodeRKIsKnownNil(proto *vm.FuncProto, rk int, nilSlots map[int]bool) bool {
	if rk >= vm.RKBit {
		idx := rk - vm.RKBit
		return proto != nil && idx >= 0 && idx < len(proto.Constants) && proto.Constants[idx].IsNil()
	}
	return nilSlots[rk]
}

func getTableKindImpliesType(kindAux2 int64, typ Type) bool {
	switch kindAux2 {
	case int64(vm.FBKindInt):
		return typ == TypeInt
	case int64(vm.FBKindFloat):
		return typ == TypeFloat
	case int64(vm.FBKindBool):
		return typ == TypeBool
	default:
		return false
	}
}
