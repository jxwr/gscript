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
	"github.com/gscript/gscript/internal/vm"
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
					notes = append(notes, "intrinsic: string.format finite decimal -> StringConstLookup")
					continue
				}
				if lowerStringFormatInt(fn, instr) {
					notes = append(notes, "intrinsic: string.format(pattern,int) -> StringFormatInt")
					continue
				}
				if lowerStringFormatProfiledConst(fn, instr) {
					notes = append(notes, "intrinsic: profiled string.format(stable-pattern,...) -> StringFormatConst")
					continue
				}
			}
			if moduleName == "string" && fieldName == "format" && len(instr.Args) > 3 {
				if lowerStringFormatConst(fn, instr) {
					notes = append(notes, "intrinsic: string.format(const-pattern,...) -> StringFormatConst")
					continue
				}
				if lowerStringFormatProfiledConst(fn, instr) {
					notes = append(notes, "intrinsic: profiled string.format(stable-pattern,...) -> StringFormatConst")
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
	_, stringNotes := StringNativeCleanupPass(fn)
	notes = append(notes, stringNotes...)
	return fn, notes
}

// StringNativeCleanupPass runs string-specific lowering that can become
// available after earlier rewrites. It is separated from IntrinsicPass so the
// Tier 2 optimizer has an explicit string native phase for future string
// lowering modules.
func StringNativeCleanupPass(fn *Function) (*Function, []string) {
	if fn == nil || fn.Proto == nil {
		return fn, nil
	}
	var notes []string
	notes = append(notes, fuseStringFormatIntGetTable(fn)...)
	notes = append(notes, lowerStringSplitProjections(fn)...)
	notes = append(notes, lowerStringSplitSubstrings(fn)...)
	notes = append(notes, lowerStringSplitSubstringNumbers(fn)...)
	return fn, notes
}

func lowerStringSplitSubstrings(fn *Function) []string {
	if fn == nil || fn.Proto == nil {
		return nil
	}
	var notes []string
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !isStringFieldCall(fn, instr, "sub") || len(instr.Args) < 3 || len(instr.Args) > 4 {
				continue
			}
			arg := instr.Args[1]
			start, ok := constIntArg(instr.Args[2])
			if !ok {
				continue
			}
			var end int64
			hasEnd := false
			if len(instr.Args) == 4 {
				var endOK bool
				end, endOK = constIntArg(instr.Args[3])
				if !endOK {
					continue
				}
				hasEnd = true
			}
			if arg == nil || arg.Def == nil {
				continue
			}
			var spec StringSplitSubSpec
			var args []*Value
			switch arg.Def.Op {
			case OpStringSplitPart:
				if len(arg.Def.Args) != 3 {
					continue
				}
				splitPart := arg.Def
				spec = newStringSplitSubSpec(splitPart.Aux, start, end, hasEnd)
				args = []*Value{splitPart.Args[0], instr.Args[0], splitPart.Args[1], splitPart.Args[2]}
			case OpStringSplitSubstr:
				if len(arg.Def.Args) < 4 {
					continue
				}
				priorIdx := int(arg.Def.Aux)
				if priorIdx < 0 || priorIdx >= len(fn.StringSplitSubSpecs) {
					continue
				}
				var composedOK bool
				spec, composedOK = composeStringSplitSubSpec(fn.StringSplitSubSpecs[priorIdx], start, end, hasEnd)
				if !composedOK {
					continue
				}
				source := arg.Def.Args[len(arg.Def.Args)-2]
				sep := arg.Def.Args[len(arg.Def.Args)-1]
				args = append([]*Value{}, arg.Def.Args[:len(arg.Def.Args)-2]...)
				args = append(args, instr.Args[0], source, sep)
			default:
				continue
			}
			specIdx := len(fn.StringSplitSubSpecs)
			fn.StringSplitSubSpecs = append(fn.StringSplitSubSpecs, spec)
			instr.Op = OpStringSplitSubstr
			instr.Type = TypeString
			instr.Args = args
			instr.Aux = int64(specIdx)
			instr.Aux2 = 0
			notes = append(notes, "intrinsic: string.sub(string.split(...)[const], const) -> StringSplitSubstr")
		}
	}
	return notes
}

func lowerStringSplitSubstringNumbers(fn *Function) []string {
	if fn == nil || fn.Proto == nil {
		return nil
	}
	var notes []string
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !isGlobalCall(fn, instr, "tonumber") || len(instr.Args) != 2 {
				continue
			}
			arg := instr.Args[1]
			if arg == nil || arg.Def == nil || arg.Def.Op != OpStringSplitSubstr || len(arg.Def.Args) < 4 {
				continue
			}
			substr := arg.Def
			instr.Op = OpStringSplitSubstrNumber
			if instr.Type == TypeUnknown {
				instr.Type = TypeAny
			}
			source := substr.Args[len(substr.Args)-2]
			sep := substr.Args[len(substr.Args)-1]
			args := append([]*Value{}, substr.Args[:len(substr.Args)-2]...)
			args = append(args, instr.Args[0], source, sep)
			instr.Args = args
			instr.Aux = substr.Aux
			instr.Aux2 = 0
			notes = append(notes, "intrinsic: tonumber(string.sub(string.split(...)[const], const)) -> StringSplitSubstrNumber")
		}
	}
	return notes
}

func newStringSplitSubSpec(tokenIndex, start, end int64, hasEnd bool) StringSplitSubSpec {
	return StringSplitSubSpec{
		TokenIndex:   tokenIndex,
		Start:        start,
		End:          end,
		HasEnd:       hasEnd,
		SubCallCount: 1,
		FirstStart:   start,
		FirstEnd:     end,
		FirstHasEnd:  hasEnd,
	}
}

func composeStringSplitSubSpec(prior StringSplitSubSpec, start, end int64, hasEnd bool) (StringSplitSubSpec, bool) {
	if prior.SubCallCount != 1 || prior.FirstStart <= 0 || start <= 0 {
		return StringSplitSubSpec{}, false
	}
	if prior.FirstHasEnd && prior.FirstEnd <= 0 {
		return StringSplitSubSpec{}, false
	}
	if hasEnd && end <= 0 {
		return StringSplitSubSpec{}, false
	}
	spec := prior
	spec.SubCallCount = 2
	spec.SecondStart = start
	spec.SecondEnd = end
	spec.SecondHasEnd = hasEnd
	spec.Start = prior.FirstStart + start - 1
	if hasEnd {
		spec.End = prior.FirstStart + end - 1
		spec.HasEnd = true
	} else if prior.FirstHasEnd {
		spec.End = prior.FirstEnd
		spec.HasEnd = true
	} else {
		spec.End = 0
		spec.HasEnd = false
	}
	if prior.FirstHasEnd && (!spec.HasEnd || spec.End > prior.FirstEnd) {
		spec.End = prior.FirstEnd
		spec.HasEnd = true
	}
	return spec, true
}

func constIntArg(v *Value) (int64, bool) {
	if v == nil || v.Def == nil || v.Def.Op != OpConstInt {
		return 0, false
	}
	return v.Def.Aux, true
}

func lowerStringSplitProjections(fn *Function) []string {
	if fn == nil || fn.Proto == nil {
		return nil
	}
	users := instrUsers(fn)
	var notes []string
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !isStringSplitCall(fn, instr) {
				continue
			}
			callUsers := users[instr.ID]
			if len(callUsers) == 0 {
				continue
			}
			type projection struct {
				get   *Instr
				index int64
			}
			projections := make([]projection, 0, len(callUsers))
			eligible := true
			for _, use := range callUsers {
				if use == nil || use.Op != OpGetTable || len(use.Args) != 2 || use.Args[0] == nil || use.Args[0].ID != instr.ID {
					eligible = false
					break
				}
				key := use.Args[1]
				if key == nil || key.Def == nil || key.Def.Op != OpConstInt {
					eligible = false
					break
				}
				projections = append(projections, projection{get: use, index: key.Def.Aux})
			}
			if !eligible {
				continue
			}
			for _, proj := range projections {
				proj.get.Op = OpStringSplitPart
				proj.get.Type = TypeAny
				proj.get.Args = []*Value{instr.Args[0], instr.Args[1], instr.Args[2]}
				proj.get.Aux = proj.index
				proj.get.Aux2 = 0
			}
			instr.Op = OpNop
			instr.Type = TypeUnknown
			instr.Args = nil
			instr.Aux = 0
			instr.Aux2 = 0
			notes = append(notes, "intrinsic: string.split const-index projections -> StringSplitPart")
		}
	}
	return notes
}

func instrUsers(fn *Function) map[int][]*Instr {
	users := make(map[int][]*Instr)
	if fn == nil {
		return users
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				if arg != nil {
					users[arg.ID] = append(users[arg.ID], instr)
				}
			}
		}
	}
	return users
}

func isStringSplitCall(fn *Function, instr *Instr) bool {
	return isStringFieldCall(fn, instr, "split") && len(instr.Args) == 3
}

func isStringFieldCall(fn *Function, instr *Instr, field string) bool {
	if fn == nil || instr == nil || instr.Op != OpCall || len(instr.Args) < 1 {
		return false
	}
	fnArg := instr.Args[0]
	if fnArg == nil || fnArg.Def == nil || fnArg.Def.Op != OpGetField || len(fnArg.Def.Args) != 1 {
		return false
	}
	tblArg := fnArg.Def.Args[0]
	if tblArg == nil || tblArg.Def == nil || tblArg.Def.Op != OpGetGlobal {
		return false
	}
	moduleName, ok := constString(fn, tblArg.Def.Aux)
	if !ok || moduleName != "string" {
		return false
	}
	fieldName, ok := constString(fn, fnArg.Def.Aux)
	return ok && fieldName == field
}

func isGlobalCall(fn *Function, instr *Instr, name string) bool {
	if fn == nil || instr == nil || instr.Op != OpCall || len(instr.Args) < 1 {
		return false
	}
	fnArg := instr.Args[0]
	if fnArg == nil || fnArg.Def == nil || fnArg.Def.Op != OpGetGlobal {
		return false
	}
	globalName, ok := constString(fn, fnArg.Def.Aux)
	return ok && globalName == name
}

func fuseStringFormatIntGetTable(fn *Function) []string {
	if fn == nil {
		return nil
	}
	var notes []string
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpGetTable || len(instr.Args) != 2 {
				continue
			}
			key := instr.Args[1]
			if key == nil || key.Def == nil || key.Def.Op != OpStringFormatInt || len(key.Def.Args) != 3 {
				continue
			}
			fmtInstr := key.Def
			instr.Op = OpGetTableStringFormatInt
			instr.Args = []*Value{instr.Args[0], fmtInstr.Args[0], fmtInstr.Args[1], fmtInstr.Args[2]}
			instr.Aux = fmtInstr.Aux
			notes = append(notes, "intrinsic: gettable(string.format(pattern,int)) -> GetTableStringFormatInt")
		}
	}
	return notes
}

func lowerStringFormatConst(fn *Function, instr *Instr) bool {
	if fn == nil || instr == nil || len(instr.Args) < 4 {
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
	patternIdx := len(fn.StringFormatPatterns)
	fn.StringFormatPatterns = append(fn.StringFormatPatterns, formatStr)
	instr.Op = OpStringFormatConst
	instr.Type = TypeString
	instr.Aux = int64(patternIdx)
	instr.Aux2 = int64(len(instr.Args))
	return true
}

func lowerStringFormatProfiledConst(fn *Function, instr *Instr) bool {
	if fn == nil || fn.Proto == nil || instr == nil || len(instr.Args) < 3 ||
		!instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.CallSiteFeedback) {
		return false
	}
	cf := fn.Proto.CallSiteFeedback[instr.SourcePC]
	kind, data, stable := cf.StableCalleeNativeIdentity()
	if !stable || kind != runtime.NativeKindStdStringFormat || data != uintptr(runtime.StdStringFormatIdentityPtr()) {
		return false
	}
	if cf.Flags&vm.CallSiteArityPolymorphic != 0 || int(cf.NArgs) != len(instr.Args)-1 {
		return false
	}
	formatStr, stable := cf.StableStringArg(0)
	if !stable {
		return false
	}
	patternIdx := len(fn.StringFormatPatterns)
	fn.StringFormatPatterns = append(fn.StringFormatPatterns, formatStr)
	instr.Op = OpStringFormatConst
	instr.Type = TypeString
	instr.Aux = int64(patternIdx)
	instr.Aux2 = int64(len(instr.Args))
	return true
}

func lowerStringFormatInt(fn *Function, instr *Instr) bool {
	cand, ok := stringFormatIntSpecializationCandidate(fn, instr)
	if !ok {
		return false
	}
	patternIdx := len(fn.StringFormatPatterns)
	fn.StringFormatPatterns = append(fn.StringFormatPatterns, cand.Pattern)
	instr.Op = OpStringFormatInt
	instr.Type = TypeString
	instr.Aux = int64(patternIdx)
	instr.Aux2 = 0
	return true
}

func simpleSingleDecimalIntFormat(formatStr string) bool {
	seen := false
	for i := 0; i < len(formatStr); {
		if formatStr[i] != '%' {
			i++
			continue
		}
		if seen {
			return false
		}
		i++
		if i >= len(formatStr) {
			return false
		}
		if formatStr[i] == '%' {
			return false
		}
		if formatStr[i] == '0' {
			i++
		}
		for i < len(formatStr) && formatStr[i] >= '0' && formatStr[i] <= '9' {
			i++
		}
		if i >= len(formatStr) || formatStr[i] != 'd' {
			return false
		}
		seen = true
		i++
	}
	return seen
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
	spec, ok := simpleTrailingDecimalFormatSpec(formatStr)
	if !ok {
		return false
	}

	indexArg := instr.Args[2]
	modulus, ok := smallPositiveIntModuloDivisor(indexArg)
	if !ok {
		return false
	}

	table := make([]runtime.Value, modulus)
	for i := range table {
		table[i] = runtime.StringValue(spec.format(i))
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

type simpleTrailingDecimalSpec struct {
	prefix string
	width  int
	zero   bool
}

func (s simpleTrailingDecimalSpec) format(n int) string {
	if s.width <= 0 {
		return s.prefix + strconv.Itoa(n)
	}
	digits := strconv.Itoa(n)
	if len(digits) >= s.width {
		return s.prefix + digits
	}
	pad := byte(' ')
	if s.zero {
		pad = '0'
	}
	var b strings.Builder
	b.Grow(len(s.prefix) + s.width)
	b.WriteString(s.prefix)
	for i := len(digits); i < s.width; i++ {
		b.WriteByte(pad)
	}
	b.WriteString(digits)
	return b.String()
}

func simpleTrailingDecimalFormatSpec(formatStr string) (simpleTrailingDecimalSpec, bool) {
	if len(formatStr) < 2 || formatStr[len(formatStr)-1] != 'd' {
		return simpleTrailingDecimalSpec{}, false
	}
	percent := strings.LastIndexByte(formatStr, '%')
	if percent < 0 || percent == len(formatStr)-1 {
		return simpleTrailingDecimalSpec{}, false
	}
	prefix := formatStr[:percent]
	if prefix == "" || strings.Contains(prefix, "%") {
		return simpleTrailingDecimalSpec{}, false
	}
	spec := formatStr[percent+1 : len(formatStr)-1]
	out := simpleTrailingDecimalSpec{prefix: prefix}
	if spec == "" {
		return out, true
	}
	if spec[0] == '0' {
		out.zero = true
		spec = spec[1:]
	}
	if spec == "" {
		return simpleTrailingDecimalSpec{}, false
	}
	for i := 0; i < len(spec); i++ {
		if spec[i] < '0' || spec[i] > '9' {
			return simpleTrailingDecimalSpec{}, false
		}
		out.width = out.width*10 + int(spec[i]-'0')
	}
	if out.width <= 0 || out.width > 32 {
		return simpleTrailingDecimalSpec{}, false
	}
	return out, true
}

func smallPositiveIntModuloDivisor(v *Value) (int, bool) {
	if v == nil || v.Def == nil || len(v.Def.Args) != 2 {
		return 0, false
	}
	if v.Def.Op != OpMod && v.Def.Op != OpModInt {
		return 0, false
	}
	divisor := v.Def.Args[1]
	if divisor == nil || divisor.Def == nil || divisor.Def.Op != OpConstInt {
		return 0, false
	}
	modulus := divisor.Def.Aux
	if modulus <= 0 || modulus > 4096 {
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
