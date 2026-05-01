// pass_intrinsic.go recognises call patterns like math.sqrt(x) and rewrites
// them into direct IR ops (OpSqrt), eliminating the OpGetGlobal + OpGetField
// + OpCall sequence. After this pass, common math builtins become single-cycle
// ARM64 instructions.
//
// The OpGetGlobal / OpGetField instructions that produced the callee become
// dead after the rewrite and are removed by DCEPass.

package methodjit

import (
	"strconv"
	"strings"

	"github.com/gscript/gscript/internal/runtime"
)

// IntrinsicPass detects math.sqrt(x) (and similar one-arg numeric intrinsics)
// in OpCall instructions and replaces them with the corresponding specialised
// op. Returns the (possibly modified) function plus a list of human-readable
// notes describing rewrites for debugging.
func IntrinsicPass(fn *Function) (*Function, []string) {
	if fn == nil || fn.Proto == nil {
		return fn, nil
	}
	var notes []string

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			// Common prefix: decode module.field callee pattern.
			if len(instr.Args) < 2 {
				continue
			}
			fnArg := instr.Args[0]
			if fnArg == nil || fnArg.Def == nil {
				continue
			}
			getField := fnArg.Def
			if getField.Op != OpGetField || len(getField.Args) < 1 {
				continue
			}
			tblArg := getField.Args[0]
			if tblArg == nil || tblArg.Def == nil || tblArg.Def.Op != OpGetGlobal {
				continue
			}
			moduleName, ok := constString(fn, tblArg.Def.Aux)
			if !ok {
				continue
			}
			fieldName, ok := constString(fn, getField.Aux)
			if !ok {
				continue
			}

			// math.sqrt(x) — 1-arg float → float.
			if moduleName == "math" && fieldName == "sqrt" && len(instr.Args) == 2 {
				xArg := instr.Args[1]
				instr.Op = OpSqrt
				instr.Type = TypeFloat
				instr.Args = []*Value{xArg}
				instr.Aux = 0
				instr.Aux2 = 0
				notes = append(notes, "intrinsic: math.sqrt → OpSqrt")
				continue
			}

			// math.floor(x) — 1-arg number → int.
			if moduleName == "math" && fieldName == "floor" && len(instr.Args) == 2 {
				xArg := instr.Args[1]
				instr.Op = OpFloor
				instr.Type = TypeInt
				instr.Args = []*Value{xArg}
				instr.Aux = 0
				instr.Aux2 = 0
				notes = append(notes, "intrinsic: math.floor → OpFloor")
				continue
			}

			if moduleName == "string" && fieldName == "format" && len(instr.Args) == 3 {
				if lowerStringFormatConstIntLookup(fn, instr) {
					notes = append(notes, "intrinsic: string.format prefix%d -> StringConstLookup")
					continue
				}
			}

			// R43 Phase 2 DenseMatrix intrinsics.
			// matrix.getf(m, i, j) — 3-arg → float.
			if moduleName == "matrix" && fieldName == "getf" && len(instr.Args) == 4 {
				m, i, j := instr.Args[1], instr.Args[2], instr.Args[3]
				instr.Op = OpMatrixGetF
				instr.Type = TypeFloat
				instr.Args = []*Value{m, i, j}
				instr.Aux = 0
				instr.Aux2 = 0
				notes = append(notes, "intrinsic: matrix.getf → OpMatrixGetF")
				continue
			}
			// matrix.setf(m, i, j, v) — 4-arg → (no return).
			if moduleName == "matrix" && fieldName == "setf" && len(instr.Args) == 5 {
				m, i, j, v := instr.Args[1], instr.Args[2], instr.Args[3], instr.Args[4]
				instr.Op = OpMatrixSetF
				instr.Type = TypeUnknown
				instr.Args = []*Value{m, i, j, v}
				instr.Aux = 0
				instr.Aux2 = 0
				notes = append(notes, "intrinsic: matrix.setf → OpMatrixSetF")
				continue
			}
		}
	}
	return fn, notes
}

func lowerStringFormatConstIntLookup(fn *Function, instr *Instr) bool {
	if fn == nil || instr == nil || len(instr.Args) != 3 {
		return false
	}
	formatArg := instr.Args[1]
	if formatArg == nil || formatArg.Def == nil || formatArg.Def.Op != OpConstString {
		return false
	}
	formatStr, ok := constString(fn, formatArg.Def.Aux)
	if !ok {
		return false
	}
	prefix, ok := simpleTrailingDecimalFormatPrefix(formatStr)
	if !ok {
		return false
	}

	indexArg := instr.Args[2]
	modulus, ok := smallPositiveModuloBound(indexArg)
	if !ok {
		return false
	}

	table := make([]runtime.Value, modulus)
	for i := range table {
		table[i] = runtime.StringValue(prefix + strconv.Itoa(i))
	}
	tableIdx := len(fn.StringConstTables)
	fn.StringConstTables = append(fn.StringConstTables, table)

	instr.Op = OpStringConstLookup
	instr.Type = TypeString
	instr.Args = []*Value{indexArg}
	instr.Aux = int64(tableIdx)
	instr.Aux2 = int64(modulus)
	return true
}

func simpleTrailingDecimalFormatPrefix(formatStr string) (string, bool) {
	if !strings.HasSuffix(formatStr, "%d") {
		return "", false
	}
	prefix := strings.TrimSuffix(formatStr, "%d")
	if prefix == "" || strings.Contains(prefix, "%") {
		return "", false
	}
	return prefix, true
}

func smallPositiveModuloBound(v *Value) (int, bool) {
	if v == nil || v.Def == nil || len(v.Def.Args) != 2 {
		return 0, false
	}
	if v.Def.Op != OpModInt && v.Def.Op != OpMod {
		return 0, false
	}
	divisor := v.Def.Args[1]
	if divisor == nil || divisor.Def == nil || divisor.Def.Op != OpConstInt {
		return 0, false
	}
	modulus := divisor.Def.Aux
	if modulus <= 0 || modulus > 256 {
		return 0, false
	}
	return int(modulus), true
}

// constString returns the string at the given constant-pool index of fn.Proto
// if that constant is a string, else "", false.
func constString(fn *Function, idx int64) (string, bool) {
	if fn == nil || fn.Proto == nil {
		return "", false
	}
	i := int(idx)
	if i < 0 || i >= len(fn.Proto.Constants) {
		return "", false
	}
	v := fn.Proto.Constants[i]
	if !v.IsString() {
		return "", false
	}
	return v.Str(), true
}
