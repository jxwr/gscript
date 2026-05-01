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

	// Phase 1d (R89): Insert int param guards for still-unguarded params
	// whose neighbor in a numeric op has inferred TypeInt. Covers the
	// common `i <= n` pattern where n is a param and i is AddInt/SubInt.
	ts.insertIntParamGuards(fn)

	// Phase 1e: Re-run propagation to cascade the new int types.
	ts.runTypePropagation(fn)

	// Phase 1f: Insert int guards for params that seed a loop-carried
	// integer recurrence. This covers x := seed; x = (x*C + K) % M, where
	// the param only appears through a Phi cycle and has no direct ConstInt
	// neighbor for the earlier guard heuristics.
	ts.insertLoopCarriedIntParamGuards(fn)

	// Phase 1g: Re-run propagation to cascade recurrence seed guards.
	ts.runTypePropagation(fn)

	// Phase 1h: When a generic numeric op has one proven float operand
	// and one still-dynamic operand, speculate only that the dynamic side
	// is numeric and convert it to raw float. This preserves int/float
	// widening semantics while letting the operation specialize to the
	// raw-FPR path.
	ts.insertNumToFloatConversions(fn)

	// Phase 1i: Re-run propagation so inserted conversions unlock the
	// downstream float arithmetic/comparison specialization in Phase 2.
	ts.runTypePropagation(fn)

	// Phase 2: Replace generic ops with specialized variants.
	// Also update phi/instr Type fields from inferred types.
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			beforeOp := instr.Op
			beforeType := instr.Type
			ts.specialize(instr)
			if beforeOp != instr.Op {
				functionRemarks(fn).Add("TypeSpec", "changed", block.ID, instr.ID, instr.Op,
					"specialized "+beforeOp.String()+" to "+instr.Op.String())
			} else if isGenericSpecializableOp(beforeOp) {
				functionRemarks(fn).Add("TypeSpec", "missed", block.ID, instr.ID, beforeOp,
					"operands not precise enough for specialization")
			}
			// Update Type field for all instructions with inferred types.
			if t, ok := ts.types[instr.ID]; ok && t != TypeUnknown {
				instr.Type = t
				if beforeType != t && beforeOp == instr.Op {
					functionRemarks(fn).Add("TypeSpec", "changed", block.ID, instr.ID, instr.Op,
						"inferred result type "+t.String())
				}
			}
		}
	}

	return fn, nil
}

// typeSpecializeCouldChange reports whether a later TypeSpecializePass has
// visible work left after another pass rewrote the graph. It is intentionally
// based on the current SSA type lattice rather than on the producer pass:
// escape analysis, load forwarding, scalar promotion, or future rewrites can
// all expose typed operands to still-generic operations.
func typeSpecializeCouldChange(fn *Function) bool {
	if fn == nil {
		return false
	}

	ts := &typeSpecializer{
		types: make(map[int]Type),
	}
	ts.runTypePropagation(fn)

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op == OpNop {
				continue
			}
			if t, ok := ts.types[instr.ID]; ok && t != TypeUnknown && instr.Type != t {
				return true
			}
			if ts.wouldSpecialize(instr) {
				return true
			}
			if ts.wouldInsertNumToFloat(instr) {
				return true
			}
		}
	}
	return false
}

// TableArrayLoadTypeSpecializePass reruns the type-propagation/specialization
// subset needed after TableArrayLower moves kind feedback from OpGetTable.Aux2
// to OpTableArrayLoad.Aux. It deliberately does not run the full TypeSpec guard
// insertion pipeline here: this point is after OverflowBoxing, so unrelated
// generic arithmetic may intentionally remain boxed.
func TableArrayLoadTypeSpecializePass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	affected := tableArrayLoadTypeAffectedValues(fn)
	if len(affected) == 0 {
		return fn, nil
	}

	ts := &typeSpecializer{
		types: make(map[int]Type),
	}
	ts.runTypePropagation(fn)

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || !affected[instr.ID] {
				continue
			}
			beforeOp := instr.Op
			beforeType := instr.Type
			ts.specialize(instr)
			if beforeOp != instr.Op {
				functionRemarks(fn).Add("TypeSpec", "changed", block.ID, instr.ID, instr.Op,
					"specialized "+beforeOp.String()+" to "+instr.Op.String()+" after table-array lowering")
			}
			if t, ok := ts.types[instr.ID]; ok && t != TypeUnknown && instr.Type != t {
				instr.Type = t
			}
			if beforeType != instr.Type && beforeOp == instr.Op {
				functionRemarks(fn).Add("TypeSpec", "changed", block.ID, instr.ID, instr.Op,
					"inferred result type "+instr.Type.String()+" after table-array lowering")
			}
		}
	}
	return fn, nil
}

func tableArrayLoadTypeAffectedValues(fn *Function) map[int]bool {
	affected := make(map[int]bool)
	changed := true
	for changed {
		changed = false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr == nil || instr.Op == OpNop || affected[instr.ID] {
					continue
				}
				if instr.Op == OpTableArrayLoad {
					if _, ok := tableArrayKindElementType(instr.Aux); ok {
						affected[instr.ID] = true
						changed = true
					}
					continue
				}
				for _, arg := range instr.Args {
					if arg != nil && affected[arg.ID] {
						affected[instr.ID] = true
						changed = true
						break
					}
				}
			}
		}
	}
	return affected
}

func (ts *typeSpecializer) wouldSpecialize(instr *Instr) bool {
	if instr == nil {
		return false
	}
	if len(instr.Args) < 2 {
		if instr.Op != OpUnm || len(instr.Args) != 1 {
			return false
		}
		at := ts.argType(instr.Args[0])
		return at == TypeInt || at == TypeFloat
	}

	lt := ts.argType(instr.Args[0])
	rt := ts.argType(instr.Args[1])
	bothInt := lt == TypeInt && rt == TypeInt
	bothNumeric := (lt == TypeInt || lt == TypeFloat) && (rt == TypeInt || rt == TypeFloat)
	hasFloat := lt == TypeFloat || rt == TypeFloat

	switch instr.Op {
	case OpAdd, OpSub, OpMul:
		return bothInt || (bothNumeric && hasFloat)
	case OpMod, OpEq:
		return bothInt
	case OpDiv:
		return bothNumeric
	case OpLt, OpLe:
		return bothInt || (bothNumeric && hasFloat)
	default:
		return false
	}
}

func (ts *typeSpecializer) wouldInsertNumToFloat(instr *Instr) bool {
	if instr == nil || !shouldInsertNumToFloat(instr.Op) || len(instr.Args) < 2 {
		return false
	}
	for argIdx := 0; argIdx < 2; argIdx++ {
		arg := instr.Args[argIdx]
		other := instr.Args[1-argIdx]
		if arg == nil || other == nil {
			continue
		}
		if ts.argType(other) != TypeFloat ||
			!isUnknownNumericCandidate(ts.argType(arg)) ||
			!canSpeculateNumToFloatArg(arg) {
			continue
		}
		if arg.Def != nil && arg.Def.Op == OpNumToFloat {
			continue
		}
		return true
	}
	return false
}

func isGenericSpecializableOp(op Op) bool {
	switch op {
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpUnm, OpEq, OpLt, OpLe:
		return true
	default:
		return false
	}
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
	case OpNumToFloat, OpSqrt:
		return TypeFloat
	case OpFloor:
		return TypeInt
	case OpEqInt, OpLtInt, OpLeInt, OpModZeroInt, OpLtFloat, OpLeFloat:
		return TypeBool
	case OpEq, OpLt, OpLe, OpNot:
		return TypeBool
	case OpLen:
		return TypeInt

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
	case OpGuardIntRange:
		return TypeInt
	case OpGetFieldNumToFloat:
		return TypeFloat

	// Table/closure/call produce dynamic types.
	case OpNewTable, OpNewFixedTable:
		return TypeTable
	case OpClosure:
		return TypeFunction

	// Typed table loads with monomorphic Kind feedback return a typed element.
	// The runtime kind guard at emit_table_array.go:150 deopts on mismatch,
	// so FBKindInt/Float/Bool -> TypeInt/Float/Bool is sound. FBKindMixed
	// stays unknown because the mixed array can hold any value type. After
	// TableArrayLower, the same kind lives in OpTableArrayLoad.Aux.
	case OpGetTable, OpTableArrayLoad:
		kind := instr.Aux2
		if instr.Op == OpTableArrayLoad {
			kind = instr.Aux
		}
		if typ, ok := tableArrayKindElementType(kind); ok {
			return typ
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
			if typeSpecArgIsDynamicallyUnknown(arg) {
				return TypeUnknown
			}
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

func typeSpecArgIsDynamicallyUnknown(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	switch v.Def.Op {
	case OpCall:
		return true
	default:
		return false
	}
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
//
//	v_guard = GuardType v_param is int
//
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
		guard.copySourceFrom(p.instr)

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
		guard.copySourceFrom(p.instr)

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

// insertIntParamGuards (R89) inserts GuardType(TypeInt) for LoadSlot
// parameters that remain unguarded after Phase 0/1b and whose neighbor
// in a numeric op has inferred TypeInt. Covers cases like `i <= n`
// where n is a param and i is a derived int value (AddInt result, Phi
// of ints, etc.) — too narrow for Phase 0's ConstInt-only heuristic.
//
// A param is marked int-like ONLY when it has at least one TypeInt
// neighbor AND no TypeFloat neighbor (avoids speculating int on a
// param that's genuinely float-polymorphic).
func (ts *typeSpecializer) insertIntParamGuards(fn *Function) {
	type paramInfo struct {
		instr *Instr
		block *Block
		index int
	}

	// Already-guarded params (Phase 0 int + Phase 1b float).
	guardedParams := make(map[int]bool)
	entry := fn.Entry
	for _, instr := range entry.Instrs {
		if instr.Op == OpGuardType && len(instr.Args) > 0 {
			guardedParams[instr.Args[0].ID] = true
		}
	}

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

	paramIDs := make(map[int]bool)
	for _, p := range params {
		paramIDs[p.instr.ID] = true
	}

	intLikeParams := make(map[int]bool)
	floatSeenParams := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !isNumericOp(instr.Op) || len(instr.Args) < 2 {
				continue
			}
			for i := 0; i < 2; i++ {
				arg := instr.Args[i]
				other := instr.Args[1-i]
				if arg == nil || other == nil || !paramIDs[arg.ID] {
					continue
				}
				otherType := ts.argType(other)
				switch otherType {
				case TypeInt:
					intLikeParams[arg.ID] = true
				case TypeFloat:
					floatSeenParams[arg.ID] = true
				}
			}
		}
	}

	// Exclude params that also appear in float contexts (polymorphic).
	for id := range floatSeenParams {
		delete(intLikeParams, id)
	}

	for i := len(params) - 1; i >= 0; i-- {
		p := params[i]
		if !intLikeParams[p.instr.ID] {
			continue
		}

		guardID := fn.newValueID()
		guard := &Instr{
			ID:    guardID,
			Op:    OpGuardType,
			Type:  TypeInt,
			Args:  []*Value{p.instr.Value()},
			Aux:   int64(TypeInt),
			Block: p.block,
		}
		guard.copySourceFrom(p.instr)

		instrs := p.block.Instrs
		pos := p.index + 1
		newInstrs := make([]*Instr, 0, len(instrs)+1)
		newInstrs = append(newInstrs, instrs[:pos]...)
		newInstrs = append(newInstrs, guard)
		newInstrs = append(newInstrs, instrs[pos:]...)
		p.block.Instrs = newInstrs

		guardVal := guard.Value()
		replaceValueUses(fn, p.instr.ID, guardVal, guardID)

		ts.types[guardID] = TypeInt
	}
}

// insertNumToFloatConversions converts an unknown operand to float when its
// paired operand is already proven float. The conversion accepts both int and
// float values at runtime, deopting only for non-numeric values, so it is less
// brittle than forcing GuardType(float) on mixed numeric fields.
func (ts *typeSpecializer) insertNumToFloatConversions(fn *Function) {
	for _, block := range fn.Blocks {
		if len(block.Instrs) == 0 {
			continue
		}

		newInstrs := make([]*Instr, 0, len(block.Instrs))
		converted := make(map[int]*Value)
		for _, instr := range block.Instrs {
			if shouldInsertNumToFloat(instr.Op) && len(instr.Args) >= 2 {
				for argIdx := 0; argIdx < 2; argIdx++ {
					arg := instr.Args[argIdx]
					other := instr.Args[1-argIdx]
					if arg == nil || other == nil {
						continue
					}
					if ts.argType(other) != TypeFloat ||
						!isUnknownNumericCandidate(ts.argType(arg)) ||
						!canSpeculateNumToFloatArg(arg) {
						continue
					}
					if arg.Def != nil && arg.Def.Op == OpNumToFloat {
						continue
					}
					if v, ok := converted[arg.ID]; ok {
						instr.Args[argIdx] = v
						continue
					}

					conv := &Instr{
						ID:    fn.newValueID(),
						Op:    OpNumToFloat,
						Type:  TypeFloat,
						Args:  []*Value{arg},
						Block: block,
					}
					conv.copySourceFrom(instr)
					newInstrs = append(newInstrs, conv)
					convVal := conv.Value()
					converted[arg.ID] = convVal
					instr.Args[argIdx] = convVal
					ts.types[conv.ID] = TypeFloat
					functionRemarks(fn).Add("TypeSpec", "changed", block.ID, conv.ID, conv.Op,
						"inserted numeric-to-float conversion for mixed float arithmetic")
				}
			}
			newInstrs = append(newInstrs, instr)
		}
		block.Instrs = newInstrs
	}
}

func shouldInsertNumToFloat(op Op) bool {
	switch op {
	case OpAdd, OpSub, OpMul, OpDiv, OpLt, OpLe:
		return true
	default:
		return false
	}
}

func isUnknownNumericCandidate(t Type) bool {
	return t == TypeUnknown || t == TypeAny
}

func canSpeculateNumToFloatArg(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	switch v.Def.Op {
	case OpGetField, OpGetTable, OpLoadSlot, OpPhi:
		return true
	default:
		return false
	}
}

// insertLoopCarriedIntParamGuards guards LoadSlot params that initialize a Phi
// whose value flows back to itself through integer-only arithmetic. The guard
// is speculative like the other param guards: non-int callers deopt before the
// optimized body runs.
func (ts *typeSpecializer) insertLoopCarriedIntParamGuards(fn *Function) {
	type paramInfo struct {
		instr *Instr
		block *Block
		index int
	}

	entry := fn.Entry
	guardedParams := make(map[int]bool)
	for _, instr := range entry.Instrs {
		if instr.Op == OpGuardType && len(instr.Args) > 0 {
			guardedParams[instr.Args[0].ID] = true
		}
	}

	var params []paramInfo
	paramByID := make(map[int]paramInfo)
	for i, instr := range entry.Instrs {
		if instr.Op != OpLoadSlot ||
			(instr.Type != TypeAny && instr.Type != TypeUnknown) ||
			guardedParams[instr.ID] {
			continue
		}
		info := paramInfo{instr: instr, block: entry, index: i}
		params = append(params, info)
		paramByID[instr.ID] = info
	}
	if len(params) == 0 {
		return
	}

	uses := buildInstrUses(fn)
	needsGuard := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi {
				continue
			}
			for _, arg := range instr.Args {
				if arg == nil || needsGuard[arg.ID] {
					continue
				}
				if _, ok := paramByID[arg.ID]; !ok {
					continue
				}
				if ts.valueFlowsBackThroughIntRecurrence(instr.ID, instr.ID, uses, make(map[int]bool)) {
					needsGuard[arg.ID] = true
				}
			}
		}
	}
	if len(needsGuard) == 0 {
		return
	}

	for i := len(params) - 1; i >= 0; i-- {
		p := params[i]
		if !needsGuard[p.instr.ID] {
			continue
		}

		guardID := fn.newValueID()
		guard := &Instr{
			ID:    guardID,
			Op:    OpGuardType,
			Type:  TypeInt,
			Args:  []*Value{p.instr.Value()},
			Aux:   int64(TypeInt),
			Block: p.block,
		}
		guard.copySourceFrom(p.instr)

		instrs := p.block.Instrs
		pos := p.index + 1
		newInstrs := make([]*Instr, 0, len(instrs)+1)
		newInstrs = append(newInstrs, instrs[:pos]...)
		newInstrs = append(newInstrs, guard)
		newInstrs = append(newInstrs, instrs[pos:]...)
		p.block.Instrs = newInstrs

		guardVal := guard.Value()
		replaceValueUses(fn, p.instr.ID, guardVal, guardID)
		ts.types[guardID] = TypeInt
	}
}

func buildInstrUses(fn *Function) map[int][]*Instr {
	uses := make(map[int][]*Instr)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				if arg != nil {
					uses[arg.ID] = append(uses[arg.ID], instr)
				}
			}
		}
	}
	return uses
}

func (ts *typeSpecializer) valueFlowsBackThroughIntRecurrence(id, phiID int, uses map[int][]*Instr, seen map[int]bool) bool {
	if seen[id] {
		return false
	}
	seen[id] = true
	for _, use := range uses[id] {
		if use.Op == OpPhi && use.ID == phiID {
			return true
		}
		if !isIntRecurrenceOp(use.Op) || !ts.intRecurrenceArgsOK(use, id) {
			continue
		}
		if ts.valueFlowsBackThroughIntRecurrence(use.ID, phiID, uses, seen) {
			return true
		}
	}
	return false
}

func isIntRecurrenceOp(op Op) bool {
	switch op {
	case OpAdd, OpSub, OpMul, OpMod,
		OpAddInt, OpSubInt, OpMulInt, OpModInt:
		return true
	default:
		return false
	}
}

func (ts *typeSpecializer) intRecurrenceArgsOK(instr *Instr, recurrenceID int) bool {
	if len(instr.Args) == 0 {
		return false
	}
	for _, arg := range instr.Args {
		if arg == nil {
			return false
		}
		if arg.ID == recurrenceID {
			continue
		}
		if !ts.isKnownIntValue(arg) {
			return false
		}
	}
	return true
}

func (ts *typeSpecializer) isKnownIntValue(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	if ts.argType(v) == TypeInt {
		return true
	}
	switch v.Def.Op {
	case OpConstInt, OpUnboxInt:
		return true
	case OpGuardType:
		return Type(v.Def.Aux) == TypeInt
	case OpGuardIntRange:
		return true
	default:
		return false
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
