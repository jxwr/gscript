// pass_typespec.go specializes type-generic IR operations using forward type
// propagation through the SSA graph. When both operands of an arithmetic or
// comparison instruction are known-int (from ConstInt, result of another int
// op, or a phi whose inputs are all int), the generic op is replaced with its
// type-specialized variant (e.g. OpAdd -> OpAddInt, OpLt -> OpLtInt).
//
// This is the core of speculative optimization for the Method JIT. Future
// versions will also use FeedbackVector data from the interpreter; this
// version uses purely SSA-local type inference.

package methodjit

// TypeSpecializePass performs forward type propagation and replaces generic
// arithmetic/comparison ops with type-specialized variants when operand types
// are known.
func TypeSpecializePass(fn *Function) (*Function, error) {
	ts := &typeSpecializer{
		types: make(map[int]Type),
	}
	// Phase 1: Forward type propagation (may need a fixed point for phis).
	changed := true
	for pass := 0; changed && pass < 10; pass++ {
		changed = false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				newType := ts.inferType(instr)
				if newType != TypeUnknown && ts.types[instr.ID] != newType {
					ts.types[instr.ID] = newType
					changed = true
				}
			}
		}
	}

	// Phase 2: Replace generic ops with specialized variants.
	// Also update phi/instr Type fields from inferred types.
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			ts.specialize(instr)
			// Update Type field for all instructions with inferred types.
			if t, ok := ts.types[instr.ID]; ok && t != TypeUnknown {
				instr.Type = t
			}
		}
	}

	return fn, nil
}

// typeSpecializer holds the inferred type map and does specialization.
type typeSpecializer struct {
	types map[int]Type // value ID -> inferred Type
}

// inferType returns the inferred type for an instruction based on its op and
// the known types of its arguments. Returns TypeUnknown if type cannot be
// determined.
func (ts *typeSpecializer) inferType(instr *Instr) Type {
	switch instr.Op {
	// Constants have known types.
	case OpConstInt:
		return TypeInt
	case OpConstFloat:
		return TypeFloat
	case OpConstBool:
		return TypeBool
	case OpConstNil:
		return TypeNil
	case OpConstString:
		return TypeString

	// Already-specialized ops produce known types.
	case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpNegInt:
		return TypeInt
	case OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat:
		return TypeFloat
	case OpEqInt, OpLtInt, OpLeInt, OpLtFloat, OpLeFloat:
		return TypeBool
	case OpEq, OpLt, OpLe, OpNot:
		return TypeBool

	// Generic arithmetic: infer from operands.
	case OpAdd, OpSub, OpMul, OpMod:
		return ts.inferBinaryNumericType(instr)
	case OpDiv:
		// Division always produces float in GScript (Lua semantics).
		return TypeFloat
	case OpUnm:
		if len(instr.Args) > 0 {
			at := ts.argType(instr.Args[0])
			if at == TypeInt {
				return TypeInt
			}
			if at == TypeFloat {
				return TypeFloat
			}
		}
		return TypeUnknown

	// Phi: if all args have the same type, the phi has that type.
	case OpPhi:
		return ts.inferPhiType(instr)

	// Table/closure/call produce dynamic types.
	case OpNewTable:
		return TypeTable
	case OpClosure:
		return TypeFunction

	default:
		return TypeUnknown
	}
}

// inferBinaryNumericType returns TypeInt if both operands are int,
// TypeFloat if both are numeric (at least one float), TypeUnknown otherwise.
func (ts *typeSpecializer) inferBinaryNumericType(instr *Instr) Type {
	if len(instr.Args) < 2 {
		return TypeUnknown
	}
	lt := ts.argType(instr.Args[0])
	rt := ts.argType(instr.Args[1])
	if lt == TypeInt && rt == TypeInt {
		return TypeInt
	}
	if (lt == TypeInt || lt == TypeFloat) && (rt == TypeInt || rt == TypeFloat) {
		return TypeFloat
	}
	return TypeUnknown
}

// inferPhiType returns a type if all KNOWN phi inputs agree.
// Unknown args (not yet resolved in fixed-point iteration) are skipped.
// This allows loop-carried phis to resolve: on first pass, one arg is
// known (the initial value); on subsequent passes, the loop body's
// result type becomes known and confirms the phi's type.
func (ts *typeSpecializer) inferPhiType(instr *Instr) Type {
	if len(instr.Args) == 0 {
		return TypeUnknown
	}
	result := TypeUnknown
	for _, arg := range instr.Args {
		at := ts.argType(arg)
		if at == TypeUnknown {
			continue // skip unresolved args (will resolve on next iteration)
		}
		if result == TypeUnknown {
			result = at // first known type
		} else if at != result {
			// Conflicting types — widen to float if both numeric, else unknown
			if (result == TypeInt && at == TypeFloat) || (result == TypeFloat && at == TypeInt) {
				result = TypeFloat
			} else {
				return TypeUnknown
			}
		}
	}
	return result
}

// argType returns the inferred type of a Value from the type map.
func (ts *typeSpecializer) argType(v *Value) Type {
	if v == nil {
		return TypeUnknown
	}
	if t, ok := ts.types[v.ID]; ok {
		return t
	}
	// Fall back to the Def instruction's Type field if set.
	if v.Def != nil && v.Def.Type != TypeUnknown && v.Def.Type != TypeAny {
		return v.Def.Type
	}
	return TypeUnknown
}

// specialize replaces a generic op with its type-specialized variant if
// both operands are known-int (or known-float for float specialization).
func (ts *typeSpecializer) specialize(instr *Instr) {
	if len(instr.Args) < 2 {
		// Unary ops.
		if instr.Op == OpUnm && len(instr.Args) == 1 {
			at := ts.argType(instr.Args[0])
			if at == TypeInt {
				instr.Op = OpNegInt
				instr.Type = TypeInt
			} else if at == TypeFloat {
				instr.Op = OpNegFloat
				instr.Type = TypeFloat
			}
		}
		return
	}

	lt := ts.argType(instr.Args[0])
	rt := ts.argType(instr.Args[1])
	bothInt := lt == TypeInt && rt == TypeInt
	bothNumeric := (lt == TypeInt || lt == TypeFloat) && (rt == TypeInt || rt == TypeFloat)
	hasFloat := lt == TypeFloat || rt == TypeFloat

	switch instr.Op {
	case OpAdd:
		if bothInt {
			instr.Op = OpAddInt
			instr.Type = TypeInt
		} else if bothNumeric && hasFloat {
			instr.Op = OpAddFloat
			instr.Type = TypeFloat
		}
	case OpSub:
		if bothInt {
			instr.Op = OpSubInt
			instr.Type = TypeInt
		} else if bothNumeric && hasFloat {
			instr.Op = OpSubFloat
			instr.Type = TypeFloat
		}
	case OpMul:
		if bothInt {
			instr.Op = OpMulInt
			instr.Type = TypeInt
		} else if bothNumeric && hasFloat {
			instr.Op = OpMulFloat
			instr.Type = TypeFloat
		}
	case OpMod:
		if bothInt {
			instr.Op = OpModInt
			instr.Type = TypeInt
		}
	case OpDiv:
		// Division: always float, but specialize to DivFloat if both numeric.
		if bothNumeric {
			instr.Op = OpDivFloat
			instr.Type = TypeFloat
		}
	case OpLt:
		if bothInt {
			instr.Op = OpLtInt
			instr.Type = TypeBool
		} else if bothNumeric && hasFloat {
			instr.Op = OpLtFloat
			instr.Type = TypeBool
		}
	case OpLe:
		if bothInt {
			instr.Op = OpLeInt
			instr.Type = TypeBool
		} else if bothNumeric && hasFloat {
			instr.Op = OpLeFloat
			instr.Type = TypeBool
		}
	case OpEq:
		if bothInt {
			instr.Op = OpEqInt
			instr.Type = TypeBool
		}
	}
}
