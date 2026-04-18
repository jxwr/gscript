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

import "github.com/gscript/gscript/internal/vm"

// TypeSpecializePass performs forward type propagation and replaces generic
// arithmetic/comparison ops with type-specialized variants when operand types
// are known.
//
// Phase 0: Insert speculative type guards on parameters used in numeric
// contexts. This enables downstream specialization of operations like
// `2.0 * size / size` where `size` is a function parameter (LoadSlot).
//
// Phase 1: Forward type propagation (fixed-point over phis).
//
// Phase 2: Replace generic ops with type-specialized variants.
func TypeSpecializePass(fn *Function) (*Function, error) {
	ts := &typeSpecializer{
		types: make(map[int]Type),
	}

	// Phase 0: Insert speculative type guards on untyped parameters.
	ts.insertParamGuards(fn)

	// Phase 1a: Forward type propagation (may need a fixed point for phis).
	ts.runTypePropagation(fn)

	// Phase 1b: Insert float param guards for parameters that Phase 0
	// didn't guard (no ConstInt context) but that Phase 1a reveals are
	// used in float arithmetic contexts.
	ts.insertFloatParamGuards(fn)

	// Phase 1c: Re-run propagation to cascade the new float types.
	ts.runTypePropagation(fn)

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

// runTypePropagation performs forward type propagation until fixed point.
func (ts *typeSpecializer) runTypePropagation(fn *Function) {
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
	case OpSqrt:
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

	// Guards: the guarded type is stored in Aux.
	case OpGuardType:
		return Type(instr.Aux)

	// Table/closure/call produce dynamic types.
	case OpNewTable:
		return TypeTable
	case OpClosure:
		return TypeFunction

	// GetTable with monomorphic Kind feedback (Aux2) returns a typed element.
	// The runtime kind guard at emit_table_array.go:150 deopts on mismatch,
	// so FBKindInt/Float/Bool → TypeInt/Float/Bool is sound. FBKindMixed stays
	// unknown because the mixed array can hold any value type.
	case OpGetTable:
		switch instr.Aux2 {
		case int64(vm.FBKindInt):
			return TypeInt
		case int64(vm.FBKindFloat):
			return TypeFloat
		case int64(vm.FBKindBool):
			return TypeBool
		}
		return TypeUnknown

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

// insertParamGuards inserts speculative GuardType instructions after LoadSlot
// parameters that are used in numeric operations. This allows downstream type
// specialization to fully specialize code like `2.0 * size / size - 1.0`.
//
// For each parameter used in arithmetic/comparison, we insert:
//   v_guard = GuardType v_param is int
// and replace all downstream uses of v_param with v_guard.
// If the guard fails at runtime, the function deopts to the interpreter.
func (ts *typeSpecializer) insertParamGuards(fn *Function) {
	// Collect all LoadSlot instructions and build a use map.
	type paramInfo struct {
		instr *Instr
		block *Block
		index int // position in block.Instrs
	}
	var params []paramInfo

	// Find all LoadSlot params in the entry block (or pre-header).
	entry := fn.Entry
	for i, instr := range entry.Instrs {
		if instr.Op == OpLoadSlot && (instr.Type == TypeAny || instr.Type == TypeUnknown) {
			params = append(params, paramInfo{instr: instr, block: entry, index: i})
		}
	}
	if len(params) == 0 {
		return
	}

	// Build a set of param value IDs for quick lookup.
	paramIDs := make(map[int]bool)
	for _, p := range params {
		paramIDs[p.instr.ID] = true
	}

	// Determine which params are used in int-like contexts: paired with a
	// ConstInt in binary arithmetic/comparison. This is the heuristic for
	// guessing that a parameter is int. For example, `size - 1` (Sub with
	// ConstInt) implies `size` is int.
	intLikeParams := make(map[int]bool) // param value ID -> likely int
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !isNumericOp(instr.Op) || len(instr.Args) < 2 {
				continue
			}
			// Check if one arg is a param and the other is ConstInt.
			for i := 0; i < 2; i++ {
				arg := instr.Args[i]
				other := instr.Args[1-i]
				if arg == nil || other == nil {
					continue
				}
				if !paramIDs[arg.ID] {
					continue
				}
				if other.Def != nil && other.Def.Op == OpConstInt {
					intLikeParams[arg.ID] = true
				}
			}
		}
	}

	// Insert guards for params used in numeric contexts.
	// We insert in reverse order so indices don't shift.
	for i := len(params) - 1; i >= 0; i-- {
		p := params[i]
		if !intLikeParams[p.instr.ID] {
			continue
		}

		// Create GuardType instruction: assume int (most common numeric type).
		guardID := fn.newValueID()
		guard := &Instr{
			ID:    guardID,
			Op:    OpGuardType,
			Type:  TypeInt,
			Args:  []*Value{p.instr.Value()},
			Aux:   int64(TypeInt),
			Block: p.block,
		}

		// Insert guard right after the LoadSlot.
		instrs := p.block.Instrs
		pos := p.index + 1
		newInstrs := make([]*Instr, 0, len(instrs)+1)
		newInstrs = append(newInstrs, instrs[:pos]...)
		newInstrs = append(newInstrs, guard)
		newInstrs = append(newInstrs, instrs[pos:]...)
		p.block.Instrs = newInstrs

		// Replace all uses of the LoadSlot value with the GuardType value
		// (except in the guard itself).
		guardVal := guard.Value()
		replaceValueUses(fn, p.instr.ID, guardVal, guardID)
	}
}

// insertFloatParamGuards inserts speculative GuardType(float) instructions for
// LoadSlot parameters that Phase 0 did not guard (no ConstInt context) but that
// Phase 1a's type propagation reveals are used in float arithmetic contexts.
//
// The heuristic: if a still-unguarded param is used in a numeric op where the
// OTHER operand has inferred TypeFloat, the param is likely float.
func (ts *typeSpecializer) insertFloatParamGuards(fn *Function) {
	type paramInfo struct {
		instr *Instr
		block *Block
		index int
	}

	// Build set of already-guarded param IDs (from Phase 0 int guards).
	guardedParams := make(map[int]bool)
	entry := fn.Entry
	for _, instr := range entry.Instrs {
		if instr.Op == OpGuardType && len(instr.Args) > 0 {
			guardedParams[instr.Args[0].ID] = true
		}
	}

	// Find LoadSlot params in entry block that are still unguarded.
	var params []paramInfo
	for i, instr := range entry.Instrs {
		if instr.Op == OpLoadSlot &&
			(instr.Type == TypeAny || instr.Type == TypeUnknown) &&
			!guardedParams[instr.ID] {
			params = append(params, paramInfo{instr: instr, block: entry, index: i})
		}
	}
	if len(params) == 0 {
		return
	}

	// Build param ID set.
	paramIDs := make(map[int]bool)
	for _, p := range params {
		paramIDs[p.instr.ID] = true
	}

	// Detect float-like params: used in numeric op where the other operand
	// has inferred TypeFloat. Also track int-like usage to avoid false positives
	// (e.g., n used in both "1.0 * i / n" and "i <= n").
	floatLikeParams := make(map[int]bool)
	intLikeParams := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !isNumericOp(instr.Op) {
				continue
			}
			if len(instr.Args) < 2 {
				continue
			}
			for i := 0; i < 2; i++ {
				arg := instr.Args[i]
				other := instr.Args[1-i]
				if arg == nil || other == nil {
					continue
				}
				if !paramIDs[arg.ID] {
					continue
				}
				otherType := ts.argType(other)
				if otherType == TypeFloat {
					floatLikeParams[arg.ID] = true
				}
				if otherType == TypeInt {
					intLikeParams[arg.ID] = true
				}
			}
		}
	}

	// If a param is used in both float AND int contexts, it's likely an int
	// that gets auto-converted in float expressions. Don't speculate float.
	for id := range intLikeParams {
		delete(floatLikeParams, id)
	}

	// Insert guards in reverse order so indices don't shift.
	for i := len(params) - 1; i >= 0; i-- {
		p := params[i]
		if !floatLikeParams[p.instr.ID] {
			continue
		}

		guardID := fn.newValueID()
		guard := &Instr{
			ID:    guardID,
			Op:    OpGuardType,
			Type:  TypeFloat,
			Args:  []*Value{p.instr.Value()},
			Aux:   int64(TypeFloat),
			Block: p.block,
		}

		// Insert guard right after the LoadSlot.
		instrs := p.block.Instrs
		pos := p.index + 1
		newInstrs := make([]*Instr, 0, len(instrs)+1)
		newInstrs = append(newInstrs, instrs[:pos]...)
		newInstrs = append(newInstrs, guard)
		newInstrs = append(newInstrs, instrs[pos:]...)
		p.block.Instrs = newInstrs

		// Replace all uses.
		guardVal := guard.Value()
		replaceValueUses(fn, p.instr.ID, guardVal, guardID)

		// Register the guard's type so re-propagation picks it up.
		ts.types[guardID] = TypeFloat
	}
}

// replaceValueUses replaces all uses of oldID with newVal across all blocks,
// except in the instruction that defines newVal (identified by exceptID).
func replaceValueUses(fn *Function, oldID int, newVal *Value, exceptID int) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.ID == exceptID {
				continue
			}
			for i, arg := range instr.Args {
				if arg != nil && arg.ID == oldID {
					instr.Args[i] = newVal
				}
			}
		}
	}
}

// isNumericOp returns true for ops that require numeric operands.
func isNumericOp(op Op) bool {
	switch op {
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpUnm,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpLt, OpLe, OpLtInt, OpLeInt, OpLtFloat, OpLeFloat:
		return true
	}
	return false
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
