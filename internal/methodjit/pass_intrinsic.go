// pass_intrinsic.go recognises call patterns like math.sqrt(x) and rewrites
// them into direct IR ops (OpSqrt), eliminating the OpGetGlobal + OpGetField
// + OpCall sequence. After this pass, common math builtins become single-cycle
// ARM64 instructions.
//
// The OpGetGlobal / OpGetField instructions that produced the callee become
// dead after the rewrite and are removed by DCEPass.

package methodjit

// IntrinsicPass detects math.sqrt(x) (and similar one-arg float intrinsics)
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
			// Expect exactly [fnValue, arg] for a 1-arg intrinsic call.
			if len(instr.Args) != 2 {
				continue
			}
			fnArg := instr.Args[0]
			if fnArg == nil || fnArg.Def == nil {
				continue
			}
			// fn must come from OpGetField on an OpGetGlobal.
			getField := fnArg.Def
			if getField.Op != OpGetField || len(getField.Args) < 1 {
				continue
			}
			tblArg := getField.Args[0]
			if tblArg == nil || tblArg.Def == nil || tblArg.Def.Op != OpGetGlobal {
				continue
			}

			// Resolve names via the function's constant pool.
			moduleName, ok := constString(fn, tblArg.Def.Aux)
			if !ok {
				continue
			}
			fieldName, ok := constString(fn, getField.Aux)
			if !ok {
				continue
			}

			// Match math.sqrt.
			if moduleName != "math" || fieldName != "sqrt" {
				continue
			}

			// Rewrite: in-place mutate the OpCall into OpSqrt(x).
			// The GetGlobal/GetField values lose their only user (the OpCall's
			// fnArg) once we drop Args[0]; DCE will collect them.
			xArg := instr.Args[1]
			instr.Op = OpSqrt
			instr.Type = TypeFloat
			instr.Args = []*Value{xArg}
			instr.Aux = 0
			instr.Aux2 = 0
			notes = append(notes, "intrinsic: math.sqrt → OpSqrt")
		}
	}
	return fn, notes
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
