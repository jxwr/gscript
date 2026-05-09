package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// FixedShapeTableFact describes a table SSA value whose hidden-class shape is
// statically known. FieldValueIDs is populated only for constructors in the
// current Function; call-return facts expose only stable FieldFacts that can be
// interpreted in the caller.
type FixedShapeTableFact struct {
	ShapeID         uint32
	FieldNames      []string
	FieldValueIDs   map[string]int
	FieldFacts      map[string]FixedShapeFieldFact
	FieldTypes      map[string]Type
	FieldRanges     map[string]intRange
	FieldLenRanges  map[string]intRange
	FieldVMProtos   map[string]*vm.FuncProto
	FieldTableFacts map[string]FixedShapeTableFact
	Guarded         bool
	EntryGuarded    bool
}

type FieldPolyShapeCase struct {
	ShapeID  uint32
	FieldIdx int
	Type     Type
	VMProto  *vm.FuncProto
}

type FixedShapeFieldKind uint8

const (
	FixedShapeFieldUnknown FixedShapeFieldKind = iota
	FixedShapeFieldNil
	FixedShapeFieldParam
)

// FixedShapeFieldFact is the caller-safe state for one fixed-shape field.
// MaybeNil covers empty-shape return paths where a missing field reads as nil.
// MaybeMaterialized marks paths where the field value still comes from a real
// runtime value, so consumers must not replace the read with nil.
type FixedShapeFieldFact struct {
	Kind              FixedShapeFieldKind
	ParamIndex        int
	MaybeNil          bool
	MaybeMaterialized bool
}

// FixedTableConstructorFact describes a bytecode-level fixed-field table
// constructor that is still represented as OpNewTable plus OpSetField stores in
// early IR. Exactly one constructor index is non-negative.
type FixedTableConstructorFact struct {
	Ctor2Index int
	CtorNIndex int
	FieldNames []string
}

func (f FixedShapeTableFact) fieldIndex(name string) (int, bool) {
	for i, field := range f.FieldNames {
		if field == name {
			return i, true
		}
	}
	return -1, false
}

func (f FixedShapeTableFact) sameShape(other FixedShapeTableFact) bool {
	if len(f.FieldNames) != len(other.FieldNames) {
		return false
	}
	for i := range f.FieldNames {
		if f.FieldNames[i] != other.FieldNames[i] {
			return false
		}
	}
	return true
}

// FixedShapeTableFactsConfig supplies facts that are safe to consume in the
// current function. ArgFacts are guarded callsite facts for callee parameters.
// EntryGuardedArgs asks codegen to validate those shapes before the optimized
// body so the guarded facts can be consumed as callee-local shape facts.
type FixedShapeTableFactsConfig struct {
	Globals               map[string]*vm.FuncProto
	ArgFacts              map[int]FixedShapeTableFact
	ArrayElementArgFacts  map[int]FixedShapeTableFact
	ArrayElementPolyFacts map[int][]FixedShapeTableFact
	EntryGuardedArgs      bool
}

// FixedShapeTableFactsPass records fixed-shape table facts and uses
// interprocedural return facts from stable global callees to prefill GetField
// shape-cache metadata. It deliberately leaves runtime shape guards intact.
func FixedShapeTableFactsPass(globals map[string]*vm.FuncProto) PassFunc {
	return FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{Globals: globals})
}

// FixedShapeTableFactsPassWith is the configurable fixed-shape pass entry.
func FixedShapeTableFactsPassWith(config FixedShapeTableFactsConfig) PassFunc {
	return func(fn *Function) (*Function, error) {
		if fn == nil || len(fn.Blocks) == 0 {
			return fn, nil
		}
		facts := inferLocalFixedShapeTables(fn)
		if len(facts) == 0 {
			facts = make(map[int]FixedShapeTableFact)
		}
		seedGuardedFixedShapeArgFacts(fn, facts, config.ArgFacts)
		seedGuardedFixedShapeArrayElementArgFacts(fn, facts, config.ArrayElementArgFacts)
		seedGuardedPolyShapeArrayElementArgFacts(fn, facts, config.ArrayElementPolyFacts)
		if config.EntryGuardedArgs {
			markEntryGuardedFixedShapeArgFacts(fn, facts, fn.FixedShapeArgFacts)
		}

		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op != OpCall {
					continue
				}
				_, callee := resolveCallee(instr, fn, InlineConfig{Globals: config.Globals})
				if callee == nil {
					continue
				}
				fact, ok := AnalyzeFixedShapeReturnFact(callee)
				if !ok {
					continue
				}
				facts[instr.ID] = fact
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("call result carries fixed table shape %v", fact.FieldNames))
			}
		}

		if len(facts) == 0 {
			return fn, nil
		}
		fn.FixedShapeTables = facts
		annotateFixedShapeGetFields(fn, facts)
		forwardFixedShapeGetFields(fn, facts)
		return fn, nil
	}
}

func seedGuardedFixedShapeArgFacts(fn *Function, facts map[int]FixedShapeTableFact, argFacts map[int]FixedShapeTableFact) {
	if fn == nil || fn.Proto == nil || len(argFacts) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpLoadSlot || instr.Aux < 0 || int(instr.Aux) >= fn.Proto.NumParams {
				continue
			}
			fact, ok := guardedFixedShapeArgFact(argFacts[int(instr.Aux)])
			if !ok {
				continue
			}
			facts[instr.ID] = fact
			if fn.FixedShapeArgFacts == nil {
				fn.FixedShapeArgFacts = make(map[int]FixedShapeTableFact)
			}
			fn.FixedShapeArgFacts[int(instr.Aux)] = fact
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("parameter %d carries guarded fixed table shape %v", instr.Aux, fact.FieldNames))
		}
	}
}

func seedGuardedFixedShapeArrayElementArgFacts(fn *Function, facts map[int]FixedShapeTableFact, argFacts map[int]FixedShapeTableFact) {
	if fn == nil || fn.Proto == nil || len(argFacts) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetTable || len(instr.Args) < 2 || instr.Args[0] == nil {
				continue
			}
			tableDef := instr.Args[0].Def
			if tableDef == nil || tableDef.Op != OpLoadSlot || tableDef.Aux < 0 || int(tableDef.Aux) >= fn.Proto.NumParams {
				continue
			}
			fact, ok := guardedFixedShapeArgFact(argFacts[int(tableDef.Aux)])
			if !ok {
				continue
			}
			facts[instr.ID] = fact
			if instr.Type == TypeAny || instr.Type == TypeUnknown {
				instr.Type = TypeTable
			}
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("parameter %d array element carries guarded fixed table shape %v", tableDef.Aux, fact.FieldNames))
		}
	}
}

func seedGuardedPolyShapeArrayElementArgFacts(fn *Function, facts map[int]FixedShapeTableFact, argFacts map[int][]FixedShapeTableFact) {
	if fn == nil || fn.Proto == nil || len(argFacts) == 0 {
		return
	}
	valueFacts := make(map[int][]FixedShapeTableFact)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpGetTable:
				if len(instr.Args) < 2 || instr.Args[0] == nil {
					continue
				}
				tableDef := instr.Args[0].Def
				if tableDef == nil || tableDef.Op != OpLoadSlot || tableDef.Aux < 0 || int(tableDef.Aux) >= fn.Proto.NumParams {
					continue
				}
				poly := guardedFixedShapePolyFacts(argFacts[int(tableDef.Aux)])
				if len(poly) == 0 {
					continue
				}
				valueFacts[instr.ID] = poly
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("parameter %d array element carries %d guarded polymorphic shapes", tableDef.Aux, len(poly)))
			case OpGetField:
				if len(instr.Args) == 0 || instr.Args[0] == nil {
					continue
				}
				poly := valueFacts[instr.Args[0].ID]
				if len(poly) == 0 {
					continue
				}
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" {
					continue
				}
				cases, typ := fieldPolyShapeCases(poly, name)
				if len(cases) < 2 {
					continue
				}
				if fn.FieldPolyShapeFacts == nil {
					fn.FieldPolyShapeFacts = make(map[int][]FieldPolyShapeCase)
				}
				fn.FieldPolyShapeFacts[instr.ID] = cases
				if typ != TypeUnknown && typ != TypeAny {
					instr.Type = typ
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("prefilled polymorphic field cache for %q with %d shapes", name, len(cases)))
			}
		}
	}
}

func guardedFixedShapePolyFacts(facts []FixedShapeTableFact) []FixedShapeTableFact {
	if len(facts) < 2 {
		return nil
	}
	out := make([]FixedShapeTableFact, 0, len(facts))
	seen := make(map[uint32]bool, len(facts))
	for _, fact := range facts {
		if fact.ShapeID == 0 || len(fact.FieldNames) == 0 || seen[fact.ShapeID] {
			continue
		}
		fact.Guarded = true
		fact.FieldNames = append([]string(nil), fact.FieldNames...)
		fact.FieldTypes = cloneStringTypeMap(fact.FieldTypes)
		fact.FieldRanges = cloneStringRangeMap(fact.FieldRanges)
		fact.FieldLenRanges = cloneStringRangeMap(fact.FieldLenRanges)
		out = append(out, fact)
		seen[fact.ShapeID] = true
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func fieldPolyShapeCases(facts []FixedShapeTableFact, name string) ([]FieldPolyShapeCase, Type) {
	cases := make([]FieldPolyShapeCase, 0, len(facts))
	typ := TypeUnknown
	for _, fact := range facts {
		idx, ok := fact.fieldIndex(name)
		if !ok {
			return nil, TypeUnknown
		}
		caseType := fact.FieldTypes[name]
		if caseType == TypeUnknown || caseType == TypeAny {
			typ = TypeUnknown
		} else if typ == TypeUnknown {
			typ = caseType
		} else if typ != caseType {
			typ = TypeUnknown
		}
		cases = append(cases, FieldPolyShapeCase{ShapeID: fact.ShapeID, FieldIdx: idx, Type: caseType, VMProto: fact.FieldVMProtos[name]})
	}
	return cases, typ
}

func markEntryGuardedFixedShapeArgFacts(fn *Function, facts map[int]FixedShapeTableFact, argFacts map[int]FixedShapeTableFact) {
	if fn == nil || fn.Proto == nil || len(argFacts) == 0 {
		return
	}
	for paramIdx, fact := range argFacts {
		if paramIdx < 0 || paramIdx >= fn.Proto.NumParams || fact.ShapeID == 0 || len(fact.FieldNames) == 0 {
			continue
		}
		fact.EntryGuarded = true
		if fn.FixedShapeEntryGuards == nil {
			fn.FixedShapeEntryGuards = make(map[int]FixedShapeTableFact)
		}
		fn.FixedShapeEntryGuards[paramIdx] = fact
		if fn.FixedShapeArgFacts == nil {
			fn.FixedShapeArgFacts = make(map[int]FixedShapeTableFact)
		}
		fn.FixedShapeArgFacts[paramIdx] = fact
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op == OpLoadSlot && int(instr.Aux) == paramIdx {
					facts[instr.ID] = fact
				}
			}
		}
	}
}

func guardedFixedShapeArgFact(fact FixedShapeTableFact) (FixedShapeTableFact, bool) {
	if fact.ShapeID == 0 || len(fact.FieldNames) == 0 {
		return FixedShapeTableFact{}, false
	}
	return FixedShapeTableFact{
		ShapeID:         fact.ShapeID,
		FieldNames:      append([]string(nil), fact.FieldNames...),
		FieldTypes:      cloneStringTypeMap(fact.FieldTypes),
		FieldRanges:     cloneStringRangeMap(fact.FieldRanges),
		FieldLenRanges:  cloneStringRangeMap(fact.FieldLenRanges),
		FieldVMProtos:   cloneStringProtoMap(fact.FieldVMProtos),
		FieldTableFacts: cloneFixedShapeTableFactMap(fact.FieldTableFacts),
		Guarded:         true,
	}, true
}

func inferGuardedFixedShapeArgFactsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) map[int]FixedShapeTableFact {
	if target == nil || len(globals) == 0 {
		return nil
	}
	type argFactState struct {
		fact     FixedShapeTableFact
		seen     bool
		conflict bool
	}
	states := make(map[int]argFactState)
	seenCallsite := false
	for _, caller := range uniqueFuncProtos(globals) {
		if caller == nil {
			continue
		}
		fn := BuildGraph(caller)
		if fn == nil || fn.Unpromotable {
			continue
		}
		facts := inferFixedShapeValuesForArgs(fn, globals)
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op != OpCall {
					continue
				}
				_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
				if callee != target {
					continue
				}
				seenCallsite = true
				for i := 1; i < len(instr.Args) && i <= target.NumParams; i++ {
					arg := instr.Args[i]
					if arg == nil {
						continue
					}
					fact, ok := facts[arg.ID]
					if !ok {
						continue
					}
					guarded, ok := guardedFixedShapeArgFact(fact)
					if !ok {
						continue
					}
					paramIdx := i - 1
					state := states[paramIdx]
					if !state.seen {
						state.fact = guarded
						state.seen = true
						states[paramIdx] = state
						continue
					}
					if !state.fact.sameShape(guarded) || state.fact.ShapeID != guarded.ShapeID {
						state.conflict = true
						states[paramIdx] = state
					}
				}
			}
		}
	}
	if !seenCallsite || len(states) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact, len(states))
	for idx, state := range states {
		if state.seen && !state.conflict {
			out[idx] = state.fact
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func inferGuardedFixedShapeArrayElementArgFactsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) map[int]FixedShapeTableFact {
	if target == nil || len(globals) == 0 {
		return nil
	}
	type argFactState struct {
		fact     FixedShapeTableFact
		seen     bool
		conflict bool
	}
	states := make(map[int]argFactState)
	seenCallsite := false
	for _, caller := range uniqueFuncProtos(globals) {
		if caller == nil {
			continue
		}
		fn := BuildGraph(caller)
		if fn == nil || fn.Unpromotable {
			continue
		}
		arrayFacts := inferArrayElementValuesForArgs(fn, globals)
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op != OpCall {
					continue
				}
				_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
				if callee != target {
					continue
				}
				seenCallsite = true
				for i := 1; i < len(instr.Args) && i <= target.NumParams; i++ {
					arg := instr.Args[i]
					if arg == nil {
						continue
					}
					fact, ok := arrayFacts[arg.ID]
					if !ok {
						continue
					}
					guarded, ok := guardedFixedShapeArgFact(fact)
					if !ok {
						continue
					}
					paramIdx := i - 1
					state := states[paramIdx]
					if !state.seen {
						state.fact = guarded
						state.seen = true
						states[paramIdx] = state
						continue
					}
					if !state.fact.sameShape(guarded) || state.fact.ShapeID != guarded.ShapeID {
						state.conflict = true
						states[paramIdx] = state
					}
				}
			}
		}
	}
	if !seenCallsite || len(states) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact, len(states))
	for idx, state := range states {
		if state.seen && !state.conflict {
			out[idx] = state.fact
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledFixedShapeArrayElementArgFactsForProto(target *vm.FuncProto) map[int]FixedShapeTableFact {
	if target == nil || len(target.ArgArrayElementShapeFeedback) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact)
	for idx, feedback := range target.ArgArrayElementShapeFeedback {
		if idx < 0 || idx >= target.NumParams {
			continue
		}
		shapeID, fields, ok := feedback.StableShape()
		if !ok {
			continue
		}
		out[idx] = FixedShapeTableFact{
			ShapeID:         shapeID,
			FieldNames:      append([]string(nil), fields...),
			FieldTypes:      profiledFixedShapeFieldTypes(feedback),
			FieldRanges:     profiledFixedShapeFieldRanges(feedback),
			FieldLenRanges:  profiledFixedShapeFieldLenRanges(feedback),
			FieldTableFacts: profiledNestedFixedShapeTableFacts(feedback),
			Guarded:         true,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledFixedShapeArrayElementPolyFactsForProto(target *vm.FuncProto) map[int][]FixedShapeTableFact {
	if target == nil || len(target.ArgArrayElementShapeFeedback) == 0 {
		return nil
	}
	out := make(map[int][]FixedShapeTableFact)
	for idx, feedback := range target.ArgArrayElementShapeFeedback {
		if idx < 0 || idx >= target.NumParams {
			continue
		}
		shapes := feedback.PolymorphicShapes()
		if len(shapes) < 2 {
			continue
		}
		facts := make([]FixedShapeTableFact, 0, len(shapes))
		for _, shape := range shapes {
			facts = append(facts, FixedShapeTableFact{
				ShapeID:        shape.ShapeID,
				FieldNames:     append([]string(nil), shape.FieldNames...),
				FieldTypes:     profiledShapeCaseFieldTypes(shape),
				FieldRanges:    profiledShapeCaseFieldRanges(shape),
				FieldLenRanges: profiledShapeCaseFieldLenRanges(shape),
				FieldVMProtos:  profiledShapeCaseFieldVMProtos(shape),
				Guarded:        true,
			})
		}
		if len(facts) >= 2 {
			out[idx] = facts
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeFixedShapeTableFacts(preferred, fallback map[int]FixedShapeTableFact) map[int]FixedShapeTableFact {
	if len(preferred) == 0 {
		return fallback
	}
	if len(fallback) == 0 {
		return preferred
	}
	out := make(map[int]FixedShapeTableFact, len(preferred)+len(fallback))
	for idx, fact := range fallback {
		out[idx] = fact
	}
	for idx, fact := range preferred {
		out[idx] = fact
	}
	return out
}

func profiledNestedFixedShapeTableFacts(feedback vm.ArgArrayElementShapeFeedback) map[string]FixedShapeTableFact {
	if len(feedback.Nested) == 0 {
		return nil
	}
	out := make(map[string]FixedShapeTableFact)
	for name, nested := range feedback.Nested {
		shapeID, fields, ok := nested.StableShape()
		if !ok {
			continue
		}
		out[name] = FixedShapeTableFact{
			ShapeID:        shapeID,
			FieldNames:     append([]string(nil), fields...),
			FieldTypes:     profiledFixedShapeFieldTypes(nested),
			FieldRanges:    profiledFixedShapeFieldRanges(nested),
			FieldLenRanges: profiledFixedShapeFieldLenRanges(nested),
			Guarded:        true,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledFixedShapeFieldTypes(feedback vm.ArgArrayElementShapeFeedback) map[string]Type {
	if len(feedback.FieldTypes) == 0 {
		return nil
	}
	out := make(map[string]Type)
	for name, fbType := range feedback.FieldTypes {
		typ, ok := feedbackToIRType(fbType)
		if !ok {
			continue
		}
		out[name] = typ
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledFixedShapeFieldRanges(feedback vm.ArgArrayElementShapeFeedback) map[string]intRange {
	if len(feedback.FieldRanges) == 0 {
		return nil
	}
	out := make(map[string]intRange)
	for name, rangeFeedback := range feedback.FieldRanges {
		min, max, ok := rangeFeedback.StableRange()
		if !ok {
			continue
		}
		out[name] = intRange{min: min, max: max, known: true}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledFixedShapeFieldLenRanges(feedback vm.ArgArrayElementShapeFeedback) map[string]intRange {
	if len(feedback.FieldLenRanges) == 0 {
		return nil
	}
	out := make(map[string]intRange)
	for name, rangeFeedback := range feedback.FieldLenRanges {
		min, max, ok := rangeFeedback.StableRange()
		if !ok {
			continue
		}
		out[name] = intRange{min: min, max: max, known: true}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledShapeCaseFieldTypes(shape vm.ArgArrayElementShapeCase) map[string]Type {
	if len(shape.FieldTypes) == 0 {
		return nil
	}
	out := make(map[string]Type)
	for name, fbType := range shape.FieldTypes {
		typ, ok := feedbackToIRType(fbType)
		if !ok {
			continue
		}
		out[name] = typ
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledShapeCaseFieldRanges(shape vm.ArgArrayElementShapeCase) map[string]intRange {
	if len(shape.FieldRanges) == 0 {
		return nil
	}
	out := make(map[string]intRange)
	for name, rangeFeedback := range shape.FieldRanges {
		min, max, ok := rangeFeedback.StableRange()
		if !ok {
			continue
		}
		out[name] = intRange{min: min, max: max, known: true}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledShapeCaseFieldLenRanges(shape vm.ArgArrayElementShapeCase) map[string]intRange {
	if len(shape.FieldLenRanges) == 0 {
		return nil
	}
	out := make(map[string]intRange)
	for name, rangeFeedback := range shape.FieldLenRanges {
		min, max, ok := rangeFeedback.StableRange()
		if !ok {
			continue
		}
		out[name] = intRange{min: min, max: max, known: true}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledShapeCaseFieldVMProtos(shape vm.ArgArrayElementShapeCase) map[string]*vm.FuncProto {
	if len(shape.FieldVMProtos) == 0 {
		return nil
	}
	out := make(map[string]*vm.FuncProto)
	for name, proto := range shape.FieldVMProtos {
		if proto != nil {
			out[name] = proto
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func inferFixedShapeValuesForArgs(fn *Function, globals map[string]*vm.FuncProto) map[int]FixedShapeTableFact {
	facts := inferLocalFixedShapeTables(fn)
	if len(facts) == 0 {
		facts = make(map[int]FixedShapeTableFact)
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
			if callee == nil {
				continue
			}
			fact, ok := AnalyzeFixedShapeReturnFact(callee)
			if !ok {
				continue
			}
			facts[instr.ID] = fact
		}
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

func inferArrayElementValuesForArgs(fn *Function, globals map[string]*vm.FuncProto) map[int]FixedShapeTableFact {
	out := make(map[int]FixedShapeTableFact)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
			if callee == nil {
				continue
			}
			fact, ok := AnalyzeFixedShapeArrayElementReturnFact(callee, globals)
			if !ok {
				continue
			}
			out[instr.ID] = fact
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uniqueFuncProtos(globals map[string]*vm.FuncProto) []*vm.FuncProto {
	seen := make(map[*vm.FuncProto]bool, len(globals))
	out := make([]*vm.FuncProto, 0, len(globals))
	for _, proto := range globals {
		if proto == nil || seen[proto] {
			continue
		}
		seen[proto] = true
		out = append(out, proto)
	}
	return out
}

// AnalyzeFixedShapeArrayElementReturnFact reports whether proto returns an
// array-like table whose element stores all carry the same fixed table shape.
func AnalyzeFixedShapeArrayElementReturnFact(proto *vm.FuncProto, globals map[string]*vm.FuncProto) (FixedShapeTableFact, bool) {
	if proto == nil {
		return FixedShapeTableFact{}, false
	}
	fn := BuildGraph(proto)
	if fn == nil || fn.Unpromotable {
		return FixedShapeTableFact{}, false
	}
	values := inferFixedShapeValuesForArgs(fn, globals)
	if len(values) == 0 {
		return FixedShapeTableFact{}, false
	}
	returned := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpReturn || len(instr.Args) != 1 || instr.Args[0] == nil {
				continue
			}
			returned[instr.Args[0].ID] = true
		}
	}
	if len(returned) == 0 {
		return FixedShapeTableFact{}, false
	}
	var out FixedShapeTableFact
	seenStore := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if len(instr.Args) == 0 || instr.Args[0] == nil || !returned[instr.Args[0].ID] {
				continue
			}
			switch instr.Op {
			case OpSetTable:
				if len(instr.Args) < 3 || instr.Args[2] == nil {
					return FixedShapeTableFact{}, false
				}
				fact, ok := values[instr.Args[2].ID]
				if !ok || fact.ShapeID == 0 || len(fact.FieldNames) == 0 {
					return FixedShapeTableFact{}, false
				}
				if !seenStore {
					out = withoutFieldValues(fact)
					seenStore = true
					continue
				}
				if out.ShapeID != fact.ShapeID || !out.sameShape(fact) {
					return FixedShapeTableFact{}, false
				}
			case OpAppend, OpSetList, OpSetField:
				return FixedShapeTableFact{}, false
			}
		}
	}
	if !seenStore {
		return FixedShapeTableFact{}, false
	}
	return out, true
}

// AnalyzeFixedShapeReturnFact reports whether every non-empty return in proto
// returns a freshly allocated table with the same ordered static string fields.
func AnalyzeFixedShapeReturnFact(proto *vm.FuncProto) (FixedShapeTableFact, bool) {
	if proto == nil {
		return FixedShapeTableFact{}, false
	}
	fn := BuildGraph(proto)
	if fn == nil || fn.Unpromotable {
		return FixedShapeTableFact{}, false
	}
	facts := inferLocalFixedShapeTables(fn)
	instrByID := fixedShapeInstrByID(fn)
	var out FixedShapeTableFact
	var fieldAgg map[string]fixedShapeFieldAccumulator
	seenReturn := false
	seenEmpty := false
	emptyReturnCount := 0
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpReturn {
				continue
			}
			if len(instr.Args) != 1 || instr.Args[0] == nil {
				return FixedShapeTableFact{}, false
			}
			fact, ok := facts[instr.Args[0].ID]
			if !ok {
				return FixedShapeTableFact{}, false
			}
			if len(fact.FieldNames) == 0 {
				seenEmpty = true
				emptyReturnCount++
				seenReturn = true
				if fieldAgg != nil {
					for _, name := range out.FieldNames {
						fieldAgg[name] = mergeFixedShapeField(fieldAgg[name], FixedShapeFieldFact{
							Kind:     FixedShapeFieldNil,
							MaybeNil: true,
						})
					}
				}
				continue
			}
			if !seenReturn {
				out = withoutFieldValues(fact)
				fieldAgg = make(map[string]fixedShapeFieldAccumulator, len(out.FieldNames))
				for _, name := range out.FieldNames {
					for i := 0; i < emptyReturnCount; i++ {
						fieldAgg[name] = mergeFixedShapeField(fieldAgg[name], FixedShapeFieldFact{
							Kind:     FixedShapeFieldNil,
							MaybeNil: true,
						})
					}
					fieldAgg[name] = mergeFixedShapeField(fieldAgg[name],
						classifyReturnedField(fn, instrByID, fact, name))
				}
				seenReturn = true
				continue
			}
			if len(out.FieldNames) != 0 && !out.sameShape(fact) {
				return FixedShapeTableFact{}, false
			}
			if len(out.FieldNames) == 0 {
				out = withoutFieldValues(fact)
				fieldAgg = make(map[string]fixedShapeFieldAccumulator, len(out.FieldNames))
			}
			for _, name := range out.FieldNames {
				if !fieldAgg[name].seen {
					for i := 0; i < emptyReturnCount; i++ {
						fieldAgg[name] = mergeFixedShapeField(fieldAgg[name], FixedShapeFieldFact{
							Kind:     FixedShapeFieldNil,
							MaybeNil: true,
						})
					}
				}
				fieldAgg[name] = mergeFixedShapeField(fieldAgg[name],
					classifyReturnedField(fn, instrByID, fact, name))
			}
		}
	}
	if !seenReturn {
		return FixedShapeTableFact{}, false
	}
	if seenEmpty {
		out.ShapeID = 0
	}
	if len(fieldAgg) > 0 {
		out.FieldFacts = make(map[string]FixedShapeFieldFact, len(fieldAgg))
		for _, name := range out.FieldNames {
			out.FieldFacts[name] = fieldAgg[name].finish()
		}
	}
	return out, true
}

func withoutFieldValues(fact FixedShapeTableFact) FixedShapeTableFact {
	return FixedShapeTableFact{
		ShapeID:         fact.ShapeID,
		FieldNames:      append([]string(nil), fact.FieldNames...),
		FieldTypes:      cloneStringTypeMap(fact.FieldTypes),
		FieldRanges:     cloneStringRangeMap(fact.FieldRanges),
		FieldVMProtos:   cloneStringProtoMap(fact.FieldVMProtos),
		FieldTableFacts: cloneFixedShapeTableFactMap(fact.FieldTableFacts),
	}
}

type fixedShapeFieldAccumulator struct {
	seen              bool
	kind              FixedShapeFieldKind
	paramIndex        int
	maybeNil          bool
	maybeMaterialized bool
}

func mergeFixedShapeField(acc fixedShapeFieldAccumulator, next FixedShapeFieldFact) fixedShapeFieldAccumulator {
	if !acc.seen {
		return fixedShapeFieldAccumulator{
			seen:              true,
			kind:              next.Kind,
			paramIndex:        next.ParamIndex,
			maybeNil:          next.MaybeNil,
			maybeMaterialized: next.MaybeMaterialized,
		}
	}
	if acc.kind != next.Kind || (acc.kind == FixedShapeFieldParam && acc.paramIndex != next.ParamIndex) {
		acc.kind = FixedShapeFieldUnknown
		acc.paramIndex = 0
	}
	acc.maybeNil = acc.maybeNil || next.MaybeNil
	acc.maybeMaterialized = acc.maybeMaterialized || next.MaybeMaterialized
	return acc
}

func (acc fixedShapeFieldAccumulator) finish() FixedShapeFieldFact {
	if !acc.seen {
		return FixedShapeFieldFact{Kind: FixedShapeFieldUnknown, MaybeMaterialized: true}
	}
	return FixedShapeFieldFact{
		Kind:              acc.kind,
		ParamIndex:        acc.paramIndex,
		MaybeNil:          acc.maybeNil,
		MaybeMaterialized: acc.maybeMaterialized,
	}
}

func classifyReturnedField(fn *Function, instrByID map[int]*Instr, fact FixedShapeTableFact, name string) FixedShapeFieldFact {
	valueID, ok := fact.FieldValueIDs[name]
	if !ok {
		return FixedShapeFieldFact{Kind: FixedShapeFieldNil, MaybeNil: true}
	}
	def := instrByID[valueID]
	if def == nil {
		return FixedShapeFieldFact{Kind: FixedShapeFieldUnknown, MaybeMaterialized: true}
	}
	switch def.Op {
	case OpConstNil:
		return FixedShapeFieldFact{Kind: FixedShapeFieldNil, MaybeNil: true}
	case OpLoadSlot:
		if fn != nil && fn.Proto != nil && def.Aux >= 0 && int(def.Aux) < fn.Proto.NumParams {
			return FixedShapeFieldFact{
				Kind:              FixedShapeFieldParam,
				ParamIndex:        int(def.Aux),
				MaybeMaterialized: true,
			}
		}
	}
	return FixedShapeFieldFact{Kind: FixedShapeFieldUnknown, MaybeMaterialized: true}
}

func inferLocalFixedShapeTables(fn *Function) map[int]FixedShapeTableFact {
	if fn == nil || fn.Proto == nil {
		return nil
	}
	out := make(map[int]FixedShapeTableFact)
	for _, block := range fn.Blocks {
		allocFields := make(map[int][]string)
		allocValues := make(map[int]map[string]int)
		killed := make(map[int]bool)
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpNewTable:
				allocFields[instr.ID] = nil
				allocValues[instr.ID] = make(map[string]int)
				out[instr.ID] = FixedShapeTableFact{}
			case OpNewFixedTable:
				fact, ok := fixedShapeFactForFixedConstructor(fn, instr)
				if ok {
					out[instr.ID] = fact
				}
			case OpSetField:
				if len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
					continue
				}
				allocID := instr.Args[0].ID
				if _, ok := allocValues[allocID]; !ok || killed[allocID] {
					continue
				}
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" || fixedShapeContainsString(allocFields[allocID], name) {
					killed[allocID] = true
					delete(out, allocID)
					continue
				}
				allocFields[allocID] = append(allocFields[allocID], name)
				allocValues[allocID][name] = instr.Args[1].ID
				out[allocID] = FixedShapeTableFact{
					ShapeID:       runtime.GetShapeID(allocFields[allocID]),
					FieldNames:    append([]string(nil), allocFields[allocID]...),
					FieldValueIDs: cloneStringIntMap(allocValues[allocID]),
				}
			case OpSetTable, OpAppend, OpSetList:
				if len(instr.Args) == 0 || instr.Args[0] == nil {
					continue
				}
				allocID := instr.Args[0].ID
				if _, ok := allocValues[allocID]; ok {
					killed[allocID] = true
					delete(out, allocID)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func fixedShapeFactForFixedConstructor(fn *Function, instr *Instr) (FixedShapeTableFact, bool) {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpNewFixedTable {
		return FixedShapeTableFact{}, false
	}
	fieldCount := int(instr.Aux2)
	if fieldCount <= 0 || len(instr.Args) != fieldCount {
		return FixedShapeTableFact{}, false
	}
	var fields []string
	if fieldCount == 2 {
		ctorIdx := int(instr.Aux)
		if ctorIdx < 0 || ctorIdx >= len(fn.Proto.TableCtors2) {
			return FixedShapeTableFact{}, false
		}
		ctor := fn.Proto.TableCtors2[ctorIdx].Runtime
		if ctor.Key1 == ctor.Key2 {
			return FixedShapeTableFact{}, false
		}
		fields = []string{ctor.Key1, ctor.Key2}
	} else {
		ctorIdx := int(instr.Aux)
		if ctorIdx < 0 || ctorIdx >= len(fn.Proto.TableCtorsN) {
			return FixedShapeTableFact{}, false
		}
		ctor := fn.Proto.TableCtorsN[ctorIdx].Runtime
		if len(ctor.Keys) != fieldCount || ctor.Shape == nil {
			return FixedShapeTableFact{}, false
		}
		fields = append([]string(nil), ctor.Keys...)
	}
	values := make(map[string]int, len(fields))
	for i, field := range fields {
		values[field] = instr.Args[i].ID
	}
	return FixedShapeTableFact{
		ShapeID:       runtime.GetShapeID(fields),
		FieldNames:    fields,
		FieldValueIDs: values,
	}, true
}

func annotateFixedShapeGetFields(fn *Function, facts map[int]FixedShapeTableFact) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetField || len(instr.Args) == 0 || instr.Args[0] == nil {
				if instr.Op == OpGuardType && len(instr.Args) > 0 && instr.Args[0] != nil && instr.Type == TypeTable {
					if fact, ok := facts[instr.Args[0].ID]; ok {
						facts[instr.ID] = fact
					}
				}
				continue
			}
			fact, ok := facts[instr.Args[0].ID]
			if !ok || fact.ShapeID == 0 {
				continue
			}
			name := fieldNameFromAux(fn, instr.Aux)
			if name == "" {
				continue
			}
			idx, ok := fact.fieldIndex(name)
			if !ok {
				continue
			}
			if instr.Aux2 == 0 {
				instr.Aux2 = int64(fact.ShapeID)<<32 | int64(uint32(idx))
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("prefilled fixed-shape field cache for %q", name))
			}
			if typ, ok := fact.FieldTypes[name]; ok && typ != TypeUnknown && typ != TypeAny {
				instr.Type = typ
			}
			if r, ok := fact.FieldRanges[name]; ok && r.known {
				if fn.ProfiledIntRanges == nil {
					fn.ProfiledIntRanges = make(map[int]intRange)
				}
				fn.ProfiledIntRanges[instr.ID] = r
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeInt
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("field %q carries guarded int range [%d,%d]", name, r.min, r.max))
			}
			if r, ok := fact.FieldLenRanges[name]; ok && r.known {
				recordProfiledLenRange(fn, instr.ID, r)
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("field %q carries guarded string-len range [%d,%d]", name, r.min, r.max))
			}
			if nested, ok := fact.FieldTableFacts[name]; ok && nested.ShapeID != 0 {
				facts[instr.ID] = nested
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("field %q carries guarded nested fixed table shape %v", name, nested.FieldNames))
			}
		}
	}
}

func forwardFixedShapeGetFields(fn *Function, facts map[int]FixedShapeTableFact) {
	if len(facts) == 0 {
		return
	}
	instrByID := fixedShapeInstrByID(fn)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetField || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			fact, ok := facts[instr.Args[0].ID]
			if !ok || (len(fact.FieldFacts) == 0 && len(fact.FieldNames) != 0) {
				continue
			}
			if fact.Guarded {
				continue
			}
			name := fieldNameFromAux(fn, instr.Aux)
			if name == "" {
				continue
			}
			if len(fact.FieldNames) == 0 {
				if !fixedShapeReadForwardSafe(block, instr) {
					continue
				}
				instr.Op = OpConstNil
				instr.Type = TypeNil
				instr.Args = nil
				instr.Aux = 0
				instr.Aux2 = 0
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, OpGetField,
					fmt.Sprintf("forwarded empty fixed-shape field %q to nil", name))
				continue
			}
			fieldFact, ok := fact.FieldFacts[name]
			if !ok {
				continue
			}
			switch fieldFact.Kind {
			case FixedShapeFieldNil:
				if fieldFact.MaybeMaterialized {
					continue
				}
				if !fixedShapeReadForwardSafe(block, instr) {
					continue
				}
				instr.Op = OpConstNil
				instr.Type = TypeNil
				instr.Args = nil
				instr.Aux = 0
				instr.Aux2 = 0
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, OpGetField,
					fmt.Sprintf("forwarded fixed-shape field %q to nil", name))
			case FixedShapeFieldParam:
				if fieldFact.MaybeNil || fieldFact.ParamIndex < 0 {
					continue
				}
				if !fixedShapeReadForwardSafe(block, instr) {
					continue
				}
				call := instrByID[instr.Args[0].ID]
				if call == nil || call.Op != OpCall || len(call.Args) <= 1+fieldFact.ParamIndex {
					continue
				}
				actual := call.Args[1+fieldFact.ParamIndex]
				if actual == nil || actual.Def == nil {
					continue
				}
				replaceAllUses(fn, instr.ID, actual.Def)
				instr.Op = OpNop
				instr.Args = nil
				instr.Aux = 0
				instr.Aux2 = 0
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, OpGetField,
					fmt.Sprintf("forwarded fixed-shape field %q to call arg %d", name, fieldFact.ParamIndex))
			}
		}
	}
}

func fixedShapeReadForwardSafe(block *Block, get *Instr) bool {
	if block == nil || get == nil || get.Op != OpGetField || len(get.Args) == 0 || get.Args[0] == nil {
		return false
	}
	objID := get.Args[0].ID
	def := get.Args[0].Def
	if def == nil || def.Op != OpCall || def.Block != block {
		return false
	}
	defIdx := -1
	getIdx := -1
	for i, instr := range block.Instrs {
		if instr == def {
			defIdx = i
		}
		if instr == get {
			getIdx = i
		}
	}
	if defIdx < 0 || getIdx <= defIdx {
		return false
	}
	for _, instr := range block.Instrs[defIdx+1 : getIdx] {
		if instr == nil {
			continue
		}
		for argIdx, arg := range instr.Args {
			if arg == nil || arg.ID != objID {
				continue
			}
			switch instr.Op {
			case OpGetField:
				if argIdx == 0 {
					continue
				}
			case OpStoreSlot:
				continue
			}
			return false
		}
	}
	return true
}

func fixedShapeInstrByID(fn *Function) map[int]*Instr {
	out := make(map[int]*Instr)
	if fn == nil {
		return out
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			out[instr.ID] = instr
		}
	}
	return out
}

func fixedShapeContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cloneStringIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringTypeMap(in map[string]Type) map[string]Type {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]Type, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func recordProfiledLenRange(fn *Function, valueID int, r intRange) {
	if fn == nil || valueID == 0 || !r.known {
		return
	}
	if fn.ProfiledLenRanges == nil {
		fn.ProfiledLenRanges = make(map[int]intRange)
	}
	fn.ProfiledLenRanges[valueID] = r
}

func cloneStringRangeMap(in map[string]intRange) map[string]intRange {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]intRange, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringProtoMap(in map[string]*vm.FuncProto) map[string]*vm.FuncProto {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*vm.FuncProto, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneFixedShapeTableFactMap(in map[string]FixedShapeTableFact) map[string]FixedShapeTableFact {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]FixedShapeTableFact, len(in))
	for k, v := range in {
		v.FieldNames = append([]string(nil), v.FieldNames...)
		v.FieldTypes = cloneStringTypeMap(v.FieldTypes)
		v.FieldRanges = cloneStringRangeMap(v.FieldRanges)
		v.FieldLenRanges = cloneStringRangeMap(v.FieldLenRanges)
		v.FieldVMProtos = cloneStringProtoMap(v.FieldVMProtos)
		v.FieldTableFacts = cloneFixedShapeTableFactMap(v.FieldTableFacts)
		out[k] = v
	}
	return out
}
