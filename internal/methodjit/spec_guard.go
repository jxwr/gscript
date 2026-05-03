//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

type SpecGuardKind uint8

const (
	SpecGuardCalleeNativeIdentity SpecGuardKind = iota + 1
	SpecGuardArgType
	SpecGuardConstString
	SpecGuardTableShape
)

type SpecializationKind uint8

const (
	SpecStringFormatInt SpecializationKind = iota + 1
	SpecFieldShapeLoad
)

// SpecGuard is a composable guard atom used by feedback/static specialization
// candidates. Native code can inline these guards; fallback uses precise deopt.
type SpecGuard struct {
	Kind       SpecGuardKind
	Arg        int
	Type       Type
	NativeKind uint8
	NativeData uintptr
	Const      string
	ShapeID    uint32
	FieldIndex int
}

type SpecializationCandidate struct {
	Kind          SpecializationKind
	InstrID       int
	Pattern       string
	StaticPattern bool
	ShapeID       uint32
	FieldIndex    int
	Guards        []SpecGuard
}

func BuildSpecializationCandidates(fn *Function) []SpecializationCandidate {
	if fn == nil {
		return nil
	}
	var out []SpecializationCandidate
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			if cand, ok := stringFormatIntSpecializationCandidate(fn, instr); ok {
				out = append(out, cand)
				continue
			}
			if cand, ok := fieldShapeLoadSpecializationCandidate(instr); ok {
				out = append(out, cand)
			}
		}
	}
	return out
}

func stringFormatIntSpecializationCandidate(fn *Function, instr *Instr) (SpecializationCandidate, bool) {
	if fn == nil || instr == nil || instr.Op != OpCall || len(instr.Args) != 3 {
		return SpecializationCandidate{}, false
	}
	if !callLooksLikeModuleField(fn, instr, "string", "format") {
		return SpecializationCandidate{}, false
	}
	formatStr, staticPattern, ok := stringFormatIntCandidatePattern(fn, instr)
	if !ok {
		return SpecializationCandidate{}, false
	}
	return SpecializationCandidate{
		Kind:          SpecStringFormatInt,
		InstrID:       instr.ID,
		Pattern:       formatStr,
		StaticPattern: staticPattern,
		Guards: []SpecGuard{
			{
				Kind:       SpecGuardCalleeNativeIdentity,
				Arg:        0,
				Type:       TypeAny,
				NativeKind: runtime.NativeKindStdStringFormat,
				NativeData: uintptr(runtime.StdStringFormatIdentityPtr()),
			},
			{Kind: SpecGuardConstString, Arg: 1, Type: TypeString, Const: formatStr},
			{Kind: SpecGuardArgType, Arg: 2, Type: TypeInt},
		},
	}, true
}

func stringFormatIntCandidatePattern(fn *Function, instr *Instr) (pattern string, static bool, ok bool) {
	if fn == nil || instr == nil || len(instr.Args) != 3 {
		return "", false, false
	}
	formatArg := instr.Args[1]
	if formatArg != nil && formatArg.Def != nil && formatArg.Def.Op == OpConstString {
		formatStr, ok := constString(fn, formatArg.Def.Aux)
		if ok && simpleSingleDecimalIntFormat(formatStr) {
			return formatStr, true, true
		}
	}
	if fn.Proto == nil || !instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.CallSiteFeedback) {
		return "", false, false
	}
	cf := fn.Proto.CallSiteFeedback[instr.SourcePC]
	kind, data, stable := cf.StableCalleeNativeIdentity()
	if !stable || kind != runtime.NativeKindStdStringFormat || data != uintptr(runtime.StdStringFormatIdentityPtr()) {
		return "", false, false
	}
	if cf.NArgs != 2 || cf.Flags&vm.CallSiteArityPolymorphic != 0 || cf.ArgTypes[1] != vm.FBInt {
		return "", false, false
	}
	formatStr, stable := cf.StableStringArg(0)
	if !stable || !simpleSingleDecimalIntFormat(formatStr) {
		return "", false, false
	}
	return formatStr, false, true
}

func callLooksLikeModuleField(fn *Function, instr *Instr, moduleName, fieldName string) bool {
	if fn == nil || instr == nil || len(instr.Args) == 0 {
		return false
	}
	fnArg := instr.Args[0]
	if fnArg == nil || fnArg.Def == nil || fnArg.Def.Op != OpGetField || len(fnArg.Def.Args) == 0 {
		return false
	}
	tblArg := fnArg.Def.Args[0]
	if tblArg == nil || tblArg.Def == nil || tblArg.Def.Op != OpGetGlobal {
		return false
	}
	gotModule, ok := constString(fn, tblArg.Def.Aux)
	if !ok || gotModule != moduleName {
		return false
	}
	gotField, ok := constString(fn, fnArg.Def.Aux)
	return ok && gotField == fieldName
}

func fieldShapeLoadSpecializationCandidate(instr *Instr) (SpecializationCandidate, bool) {
	if instr == nil || instr.Op != OpGetField || instr.Aux2 == 0 {
		return SpecializationCandidate{}, false
	}
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIndex := int(int32(uint32(instr.Aux2)))
	if shapeID == 0 || fieldIndex < 0 {
		return SpecializationCandidate{}, false
	}
	return SpecializationCandidate{
		Kind:       SpecFieldShapeLoad,
		InstrID:    instr.ID,
		ShapeID:    shapeID,
		FieldIndex: fieldIndex,
		Guards: []SpecGuard{
			{
				Kind:       SpecGuardTableShape,
				Arg:        0,
				Type:       TypeTable,
				ShapeID:    shapeID,
				FieldIndex: fieldIndex,
			},
		},
	}, true
}
