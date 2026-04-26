// pass_constprop.go propagates known constant values through the SSA graph.
// When both operands of an arithmetic instruction are constants, the result
// is computed at compile time and replaced with a constant instruction.
// This reduces runtime computation and enables further optimization (DCE
// can then remove the dead original operands).
//
// The pass tracks a map from value ID to known constant. It processes
// instructions in block order, evaluating constant expressions eagerly.
// Instructions whose results are fully computable are rewritten in-place
// to ConstInt or ConstFloat ops.

package methodjit

import (
	"math"
)

// ConstPropPass propagates known constants through the SSA graph, folding
// arithmetic on constant operands into new constants.
func ConstPropPass(fn *Function) (*Function, error) {
	cp := &constProp{
		constants: make(map[int]constVal),
	}

	// Single forward pass: collect constants and fold.
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			cp.process(instr)
		}
	}

	return fn, nil
}

// constVal holds a known constant value: either int64 or float64.
type constVal struct {
	isInt    bool
	intVal   int64
	floatVal float64
}

// constProp holds the constant propagation state.
type constProp struct {
	constants map[int]constVal // value ID -> known constant
}

// process examines one instruction: records constants and folds operations.
func (cp *constProp) process(instr *Instr) {
	switch instr.Op {
	case OpConstInt:
		cp.constants[instr.ID] = constVal{isInt: true, intVal: instr.Aux}
	case OpConstFloat:
		cp.constants[instr.ID] = constVal{isInt: false, floatVal: math.Float64frombits(uint64(instr.Aux))}

	// Integer arithmetic.
	case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact:
		cp.foldIntBinary(instr)
	case OpNegInt:
		cp.foldIntUnary(instr)

	// Float arithmetic.
	case OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat:
		cp.foldFloatBinary(instr)
	case OpNegFloat:
		cp.foldFloatUnary(instr)

	// Generic arithmetic on known constants.
	case OpAdd:
		cp.foldGenericBinary(instr)
	case OpSub:
		cp.foldGenericBinary(instr)
	case OpMul:
		cp.foldGenericBinary(instr)
	case OpMod:
		cp.foldGenericBinary(instr)
	}
}

// foldIntBinary folds int + int, int - int, int * int, int % int.
func (cp *constProp) foldIntBinary(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	a, aOk := cp.constants[instr.Args[0].ID]
	b, bOk := cp.constants[instr.Args[1].ID]
	if !aOk || !bOk || !a.isInt || !b.isInt {
		return
	}

	var result int64
	switch instr.Op {
	case OpAddInt:
		result = a.intVal + b.intVal
	case OpSubInt:
		result = a.intVal - b.intVal
	case OpMulInt:
		result = a.intVal * b.intVal
	case OpModInt:
		if b.intVal == 0 {
			return // don't fold division by zero
		}
		result = a.intVal % b.intVal
	case OpDivIntExact:
		if b.intVal == 0 || a.intVal%b.intVal != 0 {
			return
		}
		result = a.intVal / b.intVal
	default:
		return
	}

	cp.rewriteAsConstInt(instr, result)
}

// foldIntUnary folds -int.
func (cp *constProp) foldIntUnary(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	a, ok := cp.constants[instr.Args[0].ID]
	if !ok || !a.isInt {
		return
	}
	cp.rewriteAsConstInt(instr, -a.intVal)
}

// foldFloatBinary folds float +/- /* // float.
func (cp *constProp) foldFloatBinary(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	a, aOk := cp.getFloat(instr.Args[0])
	b, bOk := cp.getFloat(instr.Args[1])
	if !aOk || !bOk {
		return
	}

	var result float64
	switch instr.Op {
	case OpAddFloat:
		result = a + b
	case OpSubFloat:
		result = a - b
	case OpMulFloat:
		result = a * b
	case OpDivFloat:
		if b == 0 {
			return // don't fold division by zero
		}
		result = a / b
	default:
		return
	}

	cp.rewriteAsConstFloat(instr, result)
}

// foldFloatUnary folds -float.
func (cp *constProp) foldFloatUnary(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	a, ok := cp.getFloat(instr.Args[0])
	if !ok {
		return
	}
	cp.rewriteAsConstFloat(instr, -a)
}

// foldGenericBinary folds generic OpAdd/OpSub/OpMul/OpMod when both
// operands are known int constants.
func (cp *constProp) foldGenericBinary(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	a, aOk := cp.constants[instr.Args[0].ID]
	b, bOk := cp.constants[instr.Args[1].ID]
	if !aOk || !bOk {
		return
	}

	// Both int: fold to int.
	if a.isInt && b.isInt {
		var result int64
		switch instr.Op {
		case OpAdd:
			result = a.intVal + b.intVal
		case OpSub:
			result = a.intVal - b.intVal
		case OpMul:
			result = a.intVal * b.intVal
		case OpMod:
			if b.intVal == 0 {
				return
			}
			result = a.intVal % b.intVal
		default:
			return
		}
		cp.rewriteAsConstInt(instr, result)
		return
	}

	// Mixed int/float: fold to float.
	af := a.floatVal
	if a.isInt {
		af = float64(a.intVal)
	}
	bf := b.floatVal
	if b.isInt {
		bf = float64(b.intVal)
	}

	var result float64
	switch instr.Op {
	case OpAdd:
		result = af + bf
	case OpSub:
		result = af - bf
	case OpMul:
		result = af * bf
	case OpMod:
		if bf == 0 {
			return
		}
		result = math.Mod(af, bf)
	default:
		return
	}
	cp.rewriteAsConstFloat(instr, result)
}

// getFloat returns the float64 value of a known constant (int or float).
func (cp *constProp) getFloat(v *Value) (float64, bool) {
	c, ok := cp.constants[v.ID]
	if !ok {
		return 0, false
	}
	if c.isInt {
		return float64(c.intVal), true
	}
	return c.floatVal, true
}

// rewriteAsConstInt rewrites an instruction to be a ConstInt.
func (cp *constProp) rewriteAsConstInt(instr *Instr, val int64) {
	instr.Op = OpConstInt
	instr.Type = TypeInt
	instr.Aux = val
	instr.Args = nil
	cp.constants[instr.ID] = constVal{isInt: true, intVal: val}
}

// rewriteAsConstFloat rewrites an instruction to be a ConstFloat.
func (cp *constProp) rewriteAsConstFloat(instr *Instr, val float64) {
	instr.Op = OpConstFloat
	instr.Type = TypeFloat
	instr.Aux = int64(math.Float64bits(val))
	instr.Args = nil
	cp.constants[instr.ID] = constVal{isInt: false, floatVal: val}
}
