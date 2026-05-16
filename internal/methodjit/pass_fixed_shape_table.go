package methodjit

import (
	"fmt"
	"sort"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// FixedShapeTableFact describes a table SSA value whose hidden-class shape is
// statically known. FieldValueIDs is populated only for constructors in the
// current Function; call-return facts expose only stable FieldFacts that can be
// interpreted in the caller.
type FixedShapeTableFact struct {
	ShapeID           uint32
	ObservationCount  uint32
	FieldNames        []string
	FieldValueIDs     map[string]int
	FieldFacts        map[string]FixedShapeFieldFact
	FieldTypes        map[string]Type
	FieldRanges       map[string]intRange
	FieldLenRanges    map[string]intRange
	FieldVMProtos     map[string]*vm.FuncProto
	FieldVMClosures   map[string]uintptr
	FieldTableFacts   map[string]FixedShapeTableFact
	StringValueFact   *FixedShapeTableFact
	ArrayElementType  Type
	ArrayElementRange intRange
	Guarded           bool
	EntryGuarded      bool
}

type FieldPolyShapeCase struct {
	ShapeID      uint32
	Count        uint32
	FieldIdx     int
	Type         Type
	VMProto      *vm.FuncProto
	VMClosure    uintptr
	ReceiverFact FixedShapeTableFact
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
	ArgPolyFacts          map[int][]FixedShapeTableFact
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
		seedGuardedPolyShapeArgFacts(fn, config.ArgPolyFacts)
		seedGuardedPolyShapeArrayElementArgFacts(fn, facts, config.ArrayElementPolyFacts)
		seedProfiledDynamicTableValueFacts(fn, facts)
		if config.EntryGuardedArgs {
			markEntryGuardedFixedShapeArgFacts(fn, facts, fn.FixedShapeArgFacts)
		}
		propagateFixedShapePhiFacts(fn, facts)

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
		seedLocalStringMapValueFacts(fn, facts)
		seedLocalFieldTableFacts(fn, facts)
		arrayElementFacts := inferLocalArrayElementTableFacts(fn, facts)
		seedLocalArrayElementTableFacts(fn, facts, arrayElementFacts)

		if len(facts) == 0 && len(fn.FieldPolyShapeFacts) == 0 {
			return fn, nil
		}
		fn.FixedShapeTables = facts
		annotateFixedShapeStringValueAccesses(fn, facts)
		propagateFixedShapePhiFacts(fn, facts)
		annotateFixedShapeGetFields(fn, facts)
		annotateFixedShapeStringValueAccesses(fn, facts)
		propagateFixedShapePhiFacts(fn, facts)
		annotateFixedShapeGetFields(fn, facts)
		annotateFixedShapeSetFields(fn, facts)
		annotateFixedShapeArrayElementAccesses(fn, facts)
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
			if tableKeyProvenInt(instr.Args[1]) && instr.Aux2 == 0 {
				instr.Aux2 = int64(vm.FBKindMixed)
			}
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("parameter %d array element carries guarded fixed table shape %v", tableDef.Aux, fact.FieldNames))
		}
	}
}

func seedGuardedPolyShapeArgFacts(fn *Function, argFacts map[int][]FixedShapeTableFact) {
	if fn == nil || fn.Proto == nil || len(argFacts) == 0 {
		return
	}
	valueFacts := make(map[int][]FixedShapeTableFact)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpLoadSlot:
				if instr.Aux < 0 || int(instr.Aux) >= fn.Proto.NumParams {
					continue
				}
				poly := guardedFixedShapePolyFacts(argFacts[int(instr.Aux)])
				if len(poly) == 0 {
					continue
				}
				valueFacts[instr.ID] = poly
				if fn.FieldPolyShapeReceivers == nil {
					fn.FieldPolyShapeReceivers = make(map[int]bool)
				}
				fn.FieldPolyShapeReceivers[instr.ID] = true
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("parameter %d carries %d guarded polymorphic shapes", instr.Aux, len(poly)))
			case OpGetField, OpGetFieldNumToFloat:
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
				recordFieldPolyShapeCatalog(fn, cases)
				instr.Aux2 = 0
				if typ != TypeUnknown && typ != TypeAny {
					instr.Type = typ
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("prefilled parameter polymorphic field cache for %q with %d shapes", name, len(cases)))
			}
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
				if fn.FieldPolyShapeReceivers == nil {
					fn.FieldPolyShapeReceivers = make(map[int]bool)
				}
				fn.FieldPolyShapeReceivers[instr.ID] = true
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				if tableKeyProvenInt(instr.Args[1]) && instr.Aux2 == 0 {
					instr.Aux2 = int64(vm.FBKindMixed)
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("parameter %d array element carries %d guarded polymorphic shapes", tableDef.Aux, len(poly)))
			case OpGetField, OpGetFieldNumToFloat:
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
				recordFieldPolyShapeCatalog(fn, cases)
				instr.Aux2 = 0
				if typ != TypeUnknown && typ != TypeAny {
					instr.Type = typ
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("prefilled polymorphic field cache for %q with %d shapes", name, len(cases)))
			}
		}
	}
}

func recordFieldPolyShapeCatalog(fn *Function, cases []FieldPolyShapeCase) {
	if fn == nil || len(cases) == 0 {
		return
	}
	if fn.FieldPolyShapeCatalog == nil {
		fn.FieldPolyShapeCatalog = make(map[uint32]FixedShapeTableFact, len(cases))
	}
	for _, c := range cases {
		if c.ShapeID == 0 || c.ReceiverFact.ShapeID != c.ShapeID {
			continue
		}
		fn.FieldPolyShapeCatalog[c.ShapeID] = cloneFixedShapeTableFact(c.ReceiverFact)
	}
}

func recordFixedShapeCatalogFact(fn *Function, fact FixedShapeTableFact) {
	if fn == nil || fact.ShapeID == 0 || len(fact.FieldNames) == 0 {
		return
	}
	if fn.FieldPolyShapeCatalog == nil {
		fn.FieldPolyShapeCatalog = make(map[uint32]FixedShapeTableFact, 1)
	}
	fn.FieldPolyShapeCatalog[fact.ShapeID] = cloneFixedShapeTableFact(fact)
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

func seedProfiledDynamicTableValueFacts(fn *Function, facts map[int]FixedShapeTableFact) {
	if fn == nil || fn.Proto == nil || fn.Proto.TableKeyFeedback == nil {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetTable || !instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.TableKeyFeedback) {
				continue
			}
			feedback := fn.Proto.TableKeyFeedback[instr.SourcePC]
			fact, ok := fixedShapeFactFromProfiledValueShape(feedback.ValueShape)
			if !ok {
				continue
			}
			facts[instr.ID] = fact
			if feedback.ValueType == vm.FBTable && (instr.Type == TypeAny || instr.Type == TypeUnknown) {
				instr.Type = TypeTable
			}
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("dynamic table value carries guarded fixed table shape %v", fact.FieldNames))
		}
	}
}

func fixedShapeFactFromProfiledValueShape(feedback vm.ArgArrayElementShapeFeedback) (FixedShapeTableFact, bool) {
	shapeID, fields, ok := feedback.StableShape()
	if !ok {
		return FixedShapeTableFact{}, false
	}
	return FixedShapeTableFact{
		ShapeID:           shapeID,
		FieldNames:        append([]string(nil), fields...),
		FieldTypes:        profiledFixedShapeFieldTypes(feedback),
		FieldRanges:       profiledFixedShapeFieldRanges(feedback),
		FieldLenRanges:    profiledFixedShapeFieldLenRanges(feedback),
		FieldTableFacts:   profiledNestedFixedShapeTableFacts(feedback),
		StringValueFact:   profiledStringValueFixedShapeTableFact(feedback),
		ArrayElementType:  profiledArrayElementType(feedback),
		ArrayElementRange: profiledArrayElementRange(feedback),
		Guarded:           true,
	}, true
}

func propagateFixedShapePhiFacts(fn *Function, facts map[int]FixedShapeTableFact) {
	if fn == nil || len(facts) == 0 {
		return
	}
	changed := true
	for changed {
		changed = false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op != OpPhi || len(instr.Args) == 0 {
					continue
				}
				if _, exists := facts[instr.ID]; exists {
					continue
				}
				fact, ok := mergeFixedShapePhiArgs(instr, facts)
				if !ok {
					continue
				}
				facts[instr.ID] = fact
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				changed = true
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("phi carries guarded fixed table shape %v", fact.FieldNames))
			}
		}
	}
}

func mergeFixedShapePhiArgs(phi *Instr, facts map[int]FixedShapeTableFact) (FixedShapeTableFact, bool) {
	var merged FixedShapeTableFact
	seen := false
	for _, arg := range phi.Args {
		if arg == nil {
			return FixedShapeTableFact{}, false
		}
		fact, ok := facts[arg.ID]
		if !ok || fact.ShapeID == 0 || len(fact.FieldNames) == 0 {
			return FixedShapeTableFact{}, false
		}
		if !seen {
			merged = cloneFixedShapeTableFact(fact)
			seen = true
			continue
		}
		next, ok := mergeSameShapeFacts(merged, fact)
		if !ok {
			return FixedShapeTableFact{}, false
		}
		merged = next
	}
	if !seen {
		return FixedShapeTableFact{}, false
	}
	merged.Guarded = true
	return merged, true
}

func mergeSameShapeFacts(a, b FixedShapeTableFact) (FixedShapeTableFact, bool) {
	if a.ShapeID != b.ShapeID || !a.sameShape(b) {
		return FixedShapeTableFact{}, false
	}
	out := cloneFixedShapeTableFact(a)
	out.ObservationCount += b.ObservationCount
	out.FieldTypes = mergeFieldTypeFacts(a.FieldTypes, b.FieldTypes)
	out.FieldRanges = mergeFieldRangeFacts(a.FieldRanges, b.FieldRanges)
	out.FieldLenRanges = mergeFieldRangeFacts(a.FieldLenRanges, b.FieldLenRanges)
	out.FieldVMProtos = mergeFieldProtoFacts(a.FieldVMProtos, b.FieldVMProtos)
	out.FieldVMClosures = mergeFieldClosureFacts(a.FieldVMClosures, b.FieldVMClosures)
	out.FieldTableFacts = mergeNestedTableFacts(a.FieldTableFacts, b.FieldTableFacts)
	out.StringValueFact = mergeStringValueFacts(a.StringValueFact, b.StringValueFact)
	if a.ArrayElementType == b.ArrayElementType {
		out.ArrayElementType = a.ArrayElementType
	}
	if a.ArrayElementRange.known && b.ArrayElementRange.known {
		out.ArrayElementRange = intRange{
			min:   minInt64(a.ArrayElementRange.min, b.ArrayElementRange.min),
			max:   maxInt64(a.ArrayElementRange.max, b.ArrayElementRange.max),
			known: true,
		}
	}
	return out, true
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func mergeFieldTypeFacts(a, b map[string]Type) map[string]Type {
	if len(a) == 0 {
		return cloneStringTypeMap(b)
	}
	if len(b) == 0 {
		return cloneStringTypeMap(a)
	}
	out := make(map[string]Type, len(a)+len(b))
	for name, left := range a {
		if left == TypeUnknown || left == TypeAny {
			continue
		}
		right, ok := b[name]
		if !ok {
			out[name] = left
			continue
		}
		if right == TypeUnknown || right == TypeAny {
			out[name] = left
			continue
		}
		if left == right {
			out[name] = left
		}
	}
	for name, right := range b {
		if right == TypeUnknown || right == TypeAny {
			continue
		}
		if _, ok := a[name]; !ok {
			out[name] = right
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeFieldRangeFacts(a, b map[string]intRange) map[string]intRange {
	if len(a) == 0 {
		return cloneStringRangeMap(b)
	}
	if len(b) == 0 {
		return cloneStringRangeMap(a)
	}
	out := make(map[string]intRange)
	for name, left := range a {
		right, ok := b[name]
		if !ok || !left.known || !right.known {
			continue
		}
		if right.min < left.min {
			left.min = right.min
		}
		if right.max > left.max {
			left.max = right.max
		}
		out[name] = left
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeFieldProtoFacts(a, b map[string]*vm.FuncProto) map[string]*vm.FuncProto {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make(map[string]*vm.FuncProto)
	for name, left := range a {
		if right := b[name]; left != nil && left == right {
			out[name] = left
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeFieldClosureFacts(a, b map[string]uintptr) map[string]uintptr {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make(map[string]uintptr)
	for name, left := range a {
		if right := b[name]; left != 0 && left == right {
			out[name] = left
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeNestedTableFacts(a, b map[string]FixedShapeTableFact) map[string]FixedShapeTableFact {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make(map[string]FixedShapeTableFact)
	for name, left := range a {
		right, ok := b[name]
		if !ok || left.ShapeID != right.ShapeID || !left.sameShape(right) {
			continue
		}
		merged, ok := mergeSameShapeFacts(left, right)
		if ok {
			out[name] = merged
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringValueFacts(a, b *FixedShapeTableFact) *FixedShapeTableFact {
	if a == nil || b == nil {
		return nil
	}
	if a.ShapeID != b.ShapeID || !a.sameShape(*b) {
		return nil
	}
	merged, ok := mergeSameShapeFacts(*a, *b)
	if !ok {
		return nil
	}
	return cloneFixedShapeTableFactPtr(merged)
}

func seedLocalStringMapValueFacts(fn *Function, facts map[int]FixedShapeTableFact) {
	if fn == nil || len(facts) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetTable || len(instr.Args) < 3 ||
				instr.Args[0] == nil || instr.Args[1] == nil || instr.Args[2] == nil {
				continue
			}
			if !tableKeyProvenString(fn, instr, instr.Args[1]) {
				continue
			}
			valueFact, ok := facts[instr.Args[2].ID]
			if !ok || valueFact.ShapeID == 0 || len(valueFact.FieldNames) == 0 {
				continue
			}
			tableFact := facts[instr.Args[0].ID]
			stripped := withoutFieldValues(valueFact)
			if tableFact.StringValueFact == nil {
				tableFact.StringValueFact = cloneFixedShapeTableFactPtr(stripped)
			} else if merged := mergeStringValueFacts(tableFact.StringValueFact, &stripped); merged != nil {
				tableFact.StringValueFact = merged
			} else {
				continue
			}
			facts[instr.Args[0].ID] = tableFact
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("local string-map value carries fixed table shape %v", stripped.FieldNames))
		}
	}
}

func seedLocalFieldTableFacts(fn *Function, facts map[int]FixedShapeTableFact) {
	if fn == nil || len(facts) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetField || len(instr.Args) < 2 ||
				instr.Args[0] == nil || instr.Args[1] == nil {
				continue
			}
			receiverFact, ok := facts[instr.Args[0].ID]
			if !ok || receiverFact.ShapeID == 0 {
				continue
			}
			valueFact, ok := facts[instr.Args[1].ID]
			if !ok || !fixedShapeTableFactHasUsableTableFact(valueFact) {
				continue
			}
			name := fixedShapeFieldNameFromAux(fn, instr)
			if name == "" {
				continue
			}
			if _, ok := receiverFact.FieldTableFacts[name]; ok {
				continue
			}
			if receiverFact.FieldTableFacts == nil {
				receiverFact.FieldTableFacts = make(map[string]FixedShapeTableFact)
			}
			receiverFact.FieldTableFacts[name] = withoutFieldValues(valueFact)
			if receiverFact.FieldTypes == nil {
				receiverFact.FieldTypes = make(map[string]Type)
			}
			receiverFact.FieldTypes[name] = TypeTable
			facts[instr.Args[0].ID] = receiverFact
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("field %q carries local table fact", name))
		}
	}
}

func inferLocalArrayElementTableFacts(fn *Function, valueFacts map[int]FixedShapeTableFact) map[int]FixedShapeTableFact {
	if fn == nil || len(valueFacts) == 0 {
		return nil
	}
	states := make(map[int]arrayElementTableFactState)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			switch instr.Op {
			case OpSetTable:
				if len(instr.Args) < 3 || instr.Args[1] == nil || instr.Args[2] == nil || !tableKeyProvenInt(instr.Args[1]) {
					continue
				}
				valueFact, ok := valueFacts[instr.Args[2].ID]
				if !ok || valueFact.ShapeID == 0 || len(valueFact.FieldNames) == 0 {
					continue
				}
				states[instr.Args[0].ID] = mergeArrayElementTableFactState(states[instr.Args[0].ID], valueFact)
			case OpSetList:
				st := states[instr.Args[0].ID]
				for _, arg := range instr.Args[1:] {
					if arg == nil {
						continue
					}
					valueFact, ok := valueFacts[arg.ID]
					if !ok || valueFact.ShapeID == 0 || len(valueFact.FieldNames) == 0 {
						st.conflict = true
						continue
					}
					st = mergeArrayElementTableFactState(st, valueFact)
				}
				states[instr.Args[0].ID] = st
			case OpAppend:
				st := states[instr.Args[0].ID]
				if st.seen {
					st.conflict = true
					states[instr.Args[0].ID] = st
				}
			}
		}
	}
	if len(states) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact)
	for id, st := range states {
		if st.seen && !st.conflict {
			st.fact.Guarded = true
			out[id] = st.fact
		}
	}
	propagateArrayElementFactsThroughGlobals(fn, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

type arrayElementTableFactState struct {
	fact     FixedShapeTableFact
	seen     bool
	conflict bool
}

func mergeArrayElementTableFactState(st arrayElementTableFactState, valueFact FixedShapeTableFact) arrayElementTableFactState {
	valueFact = withoutFieldValues(valueFact)
	if !st.seen {
		st.fact = valueFact
		st.seen = true
		return st
	}
	if st.fact.ShapeID != valueFact.ShapeID || !st.fact.sameShape(valueFact) {
		st.conflict = true
		return st
	}
	if merged, ok := mergeSameShapeFacts(st.fact, valueFact); ok {
		st.fact = merged
	}
	return st
}

func propagateArrayElementFactsThroughGlobals(fn *Function, arrayElementFacts map[int]FixedShapeTableFact) {
	if fn == nil || len(arrayElementFacts) == 0 {
		return
	}
	type state struct {
		fact     FixedShapeTableFact
		seen     bool
		conflict bool
	}
	globals := make(map[int64]state)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetGlobal || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			fact, ok := arrayElementFacts[instr.Args[0].ID]
			st := globals[instr.Aux]
			if !ok {
				st.conflict = true
				globals[instr.Aux] = st
				continue
			}
			if !st.seen {
				st.fact = cloneFixedShapeTableFact(fact)
				st.seen = true
			} else if st.fact.ShapeID != fact.ShapeID || !st.fact.sameShape(fact) {
				st.conflict = true
			}
			globals[instr.Aux] = st
		}
	}
	if len(globals) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpGetGlobal {
				continue
			}
			st := globals[instr.Aux]
			if !st.seen || st.conflict {
				continue
			}
			arrayElementFacts[instr.ID] = cloneFixedShapeTableFact(st.fact)
		}
	}
}

func seedLocalArrayElementTableFacts(fn *Function, facts map[int]FixedShapeTableFact, arrayElementFacts map[int]FixedShapeTableFact) {
	if fn == nil || len(arrayElementFacts) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			var tableValue *Value
			switch instr.Op {
			case OpGetTable:
				if len(instr.Args) < 2 || instr.Args[0] == nil {
					continue
				}
				tableValue = instr.Args[0]
			case OpTableArrayLoad:
				if table, ok := loweredTableArrayLoadTableValue(instr); ok {
					tableValue = table
				}
			default:
				continue
			}
			if tableValue == nil {
				continue
			}
			fact, ok := arrayElementFacts[tableValue.ID]
			if !ok || fact.ShapeID == 0 || len(fact.FieldNames) == 0 {
				continue
			}
			facts[instr.ID] = cloneFixedShapeTableFact(fact)
			if instr.Type == TypeAny || instr.Type == TypeUnknown {
				instr.Type = TypeTable
			}
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("local array element carries guarded fixed table shape %v", fact.FieldNames))
		}
	}
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
		cases = append(cases, FieldPolyShapeCase{
			ShapeID:      fact.ShapeID,
			Count:        fact.ObservationCount,
			FieldIdx:     idx,
			Type:         caseType,
			VMProto:      fact.FieldVMProtos[name],
			VMClosure:    fact.FieldVMClosures[name],
			ReceiverFact: fact,
		})
	}
	sort.SliceStable(cases, func(i, j int) bool {
		return cases[i].Count > cases[j].Count
	})
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
		ShapeID:           fact.ShapeID,
		FieldNames:        append([]string(nil), fact.FieldNames...),
		FieldTypes:        cloneStringTypeMap(fact.FieldTypes),
		FieldRanges:       cloneStringRangeMap(fact.FieldRanges),
		FieldLenRanges:    cloneStringRangeMap(fact.FieldLenRanges),
		FieldVMProtos:     cloneStringProtoMap(fact.FieldVMProtos),
		FieldVMClosures:   cloneStringUintptrMap(fact.FieldVMClosures),
		FieldTableFacts:   cloneFixedShapeTableFactMap(fact.FieldTableFacts),
		StringValueFact:   cloneFixedShapeTableFactPtrFromPtr(fact.StringValueFact),
		ArrayElementType:  fact.ArrayElementType,
		ArrayElementRange: fact.ArrayElementRange,
		Guarded:           true,
	}, true
}

func inferGuardedFixedShapeArgFactsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) map[int]FixedShapeTableFact {
	facts, _ := inferGuardedFixedShapeArgFactsAndConflictsForProto(target, globals)
	return facts
}

func guardedFixedShapeArgConflictParamsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) map[int]bool {
	_, conflicts := inferGuardedFixedShapeArgFactsAndConflictsForProto(target, globals)
	return conflicts
}

func inferGuardedFixedShapeArgFactsAndConflictsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) (map[int]FixedShapeTableFact, map[int]bool) {
	if target == nil || len(globals) == 0 {
		return nil, nil
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
		return nil, nil
	}
	out := make(map[int]FixedShapeTableFact, len(states))
	conflicts := make(map[int]bool)
	for idx, state := range states {
		if state.seen && !state.conflict {
			out[idx] = state.fact
		} else if state.conflict {
			conflicts[idx] = true
		}
	}
	if len(out) == 0 {
		out = nil
	}
	if len(conflicts) == 0 {
		conflicts = nil
	}
	return out, conflicts
}

func inferGuardedFixedShapeArrayElementArgFactsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) map[int]FixedShapeTableFact {
	facts, _ := inferGuardedFixedShapeArrayElementArgFactsAndConflictsForProto(target, globals)
	return facts
}

func guardedFixedShapeArrayElementArgConflictParamsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) map[int]bool {
	_, conflicts := inferGuardedFixedShapeArrayElementArgFactsAndConflictsForProto(target, globals)
	return conflicts
}

func inferGuardedFixedShapeArrayElementArgFactsAndConflictsForProto(target *vm.FuncProto, globals map[string]*vm.FuncProto) (map[int]FixedShapeTableFact, map[int]bool) {
	if target == nil || len(globals) == 0 {
		return nil, nil
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
		return nil, nil
	}
	out := make(map[int]FixedShapeTableFact, len(states))
	conflicts := make(map[int]bool)
	for idx, state := range states {
		if state.seen && !state.conflict {
			out[idx] = state.fact
		} else if state.conflict {
			conflicts[idx] = true
		}
	}
	if len(out) == 0 {
		out = nil
	}
	if len(conflicts) == 0 {
		conflicts = nil
	}
	return out, conflicts
}

func profiledFixedShapeArrayElementArgFactsForProto(target *vm.FuncProto) map[int]FixedShapeTableFact {
	return profiledFixedShapeFactsFromFeedback(target, targetArgArrayElementShapeFeedback(target))
}

func profiledFixedShapeArgFactsForProto(target *vm.FuncProto) map[int]FixedShapeTableFact {
	return profiledFixedShapeFactsFromFeedback(target, targetArgShapeFeedback(target))
}

func profiledFixedShapeArgPolyFactsForProto(target *vm.FuncProto) map[int][]FixedShapeTableFact {
	return profiledFixedShapePolyFactsFromFeedback(target, targetArgShapeFeedback(target))
}

func targetArgArrayElementShapeFeedback(target *vm.FuncProto) vm.ArgArrayElementShapeFeedbackVector {
	if target == nil {
		return nil
	}
	return target.ArgArrayElementShapeFeedback
}

func targetArgShapeFeedback(target *vm.FuncProto) vm.ArgArrayElementShapeFeedbackVector {
	if target == nil {
		return nil
	}
	return target.ArgShapeFeedback
}

func profiledFixedShapeFactsFromFeedback(target *vm.FuncProto, feedbacks vm.ArgArrayElementShapeFeedbackVector) map[int]FixedShapeTableFact {
	if target == nil || len(feedbacks) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact)
	for idx, feedback := range feedbacks {
		if idx < 0 || idx >= target.NumParams {
			continue
		}
		shapeID, fields, ok := feedback.StableShape()
		if !ok {
			continue
		}
		out[idx] = FixedShapeTableFact{
			ShapeID:           shapeID,
			FieldNames:        append([]string(nil), fields...),
			FieldTypes:        profiledFixedShapeFieldTypes(feedback),
			FieldRanges:       profiledFixedShapeFieldRanges(feedback),
			FieldLenRanges:    profiledFixedShapeFieldLenRanges(feedback),
			FieldTableFacts:   profiledNestedFixedShapeTableFacts(feedback),
			StringValueFact:   profiledStringValueFixedShapeTableFact(feedback),
			ArrayElementType:  profiledArrayElementType(feedback),
			ArrayElementRange: profiledArrayElementRange(feedback),
			Guarded:           true,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledFixedShapeArrayElementPolyFactsForProto(target *vm.FuncProto) map[int][]FixedShapeTableFact {
	return profiledFixedShapePolyFactsFromFeedback(target, targetArgArrayElementShapeFeedback(target))
}

func profiledFixedShapePolyFactsFromFeedback(target *vm.FuncProto, feedbacks vm.ArgArrayElementShapeFeedbackVector) map[int][]FixedShapeTableFact {
	if target == nil || len(feedbacks) == 0 {
		return nil
	}
	out := make(map[int][]FixedShapeTableFact)
	for idx, feedback := range feedbacks {
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
				ShapeID:          shape.ShapeID,
				ObservationCount: shape.Count,
				FieldNames:       append([]string(nil), shape.FieldNames...),
				FieldTypes:       profiledShapeCaseFieldTypes(shape),
				FieldRanges:      profiledShapeCaseFieldRanges(shape),
				FieldLenRanges:   profiledShapeCaseFieldLenRanges(shape),
				FieldVMProtos:    profiledShapeCaseFieldVMProtos(shape),
				FieldVMClosures:  profiledShapeCaseFieldVMClosures(shape),
				FieldTableFacts:  profiledNestedFixedShapeTableFacts(feedback),
				StringValueFact:  profiledStringValueFixedShapeTableFact(feedback),
				Guarded:          true,
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

func cloneFixedShapeTableFactIntMap(in map[int]FixedShapeTableFact) map[int]FixedShapeTableFact {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact, len(in))
	for k, v := range in {
		out[k] = v
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
		arrayType := profiledArrayElementType(nested)
		arrayRange := profiledArrayElementRange(nested)
		stringValueFact := profiledStringValueFixedShapeTableFact(nested)
		if !ok && arrayType == TypeUnknown && !arrayRange.known && stringValueFact == nil {
			continue
		}
		out[name] = FixedShapeTableFact{
			ShapeID:           shapeID,
			FieldNames:        append([]string(nil), fields...),
			FieldTypes:        profiledFixedShapeFieldTypes(nested),
			FieldRanges:       profiledFixedShapeFieldRanges(nested),
			FieldLenRanges:    profiledFixedShapeFieldLenRanges(nested),
			FieldTableFacts:   profiledNestedFixedShapeTableFacts(nested),
			StringValueFact:   stringValueFact,
			ArrayElementType:  profiledArrayElementType(nested),
			ArrayElementRange: profiledArrayElementRange(nested),
			Guarded:           true,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledStringValueFixedShapeTableFact(feedback vm.ArgArrayElementShapeFeedback) *FixedShapeTableFact {
	if feedback.StringValueShape == nil {
		return nil
	}
	fact, ok := fixedShapeFactFromProfiledValueShape(*feedback.StringValueShape)
	if !ok {
		return nil
	}
	return cloneFixedShapeTableFactPtr(fact)
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

func profiledShapeCaseFieldVMClosures(shape vm.ArgArrayElementShapeCase) map[string]uintptr {
	if len(shape.FieldVMClosures) == 0 {
		return nil
	}
	out := make(map[string]uintptr)
	for name, closure := range shape.FieldVMClosures {
		if closure != 0 && closure != ^uintptr(0) {
			out[name] = closure
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func profiledArrayElementType(feedback vm.ArgArrayElementShapeFeedback) Type {
	typ, ok := feedbackToIRType(feedback.ArrayElementType)
	if !ok {
		return TypeUnknown
	}
	return typ
}

func profiledArrayElementRange(feedback vm.ArgArrayElementShapeFeedback) intRange {
	min, max, ok := feedback.ArrayElementRange.StableRange()
	if !ok {
		return intRange{}
	}
	return intRange{min: min, max: max, known: true}
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
	seedLocalStringMapValueFacts(fn, facts)
	seedLocalFieldTableFacts(fn, facts)
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
	seedLocalStringMapValueFacts(fn, facts)
	seedLocalFieldTableFacts(fn, facts)
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
		ShapeID:           fact.ShapeID,
		FieldNames:        append([]string(nil), fact.FieldNames...),
		FieldTypes:        cloneStringTypeMap(fact.FieldTypes),
		FieldRanges:       cloneStringRangeMap(fact.FieldRanges),
		FieldLenRanges:    cloneStringRangeMap(fact.FieldLenRanges),
		FieldVMProtos:     cloneStringProtoMap(fact.FieldVMProtos),
		FieldVMClosures:   cloneStringUintptrMap(fact.FieldVMClosures),
		FieldTableFacts:   cloneFixedShapeTableFactMap(fact.FieldTableFacts),
		StringValueFact:   cloneFixedShapeTableFactPtrFromPtr(fact.StringValueFact),
		ArrayElementType:  fact.ArrayElementType,
		ArrayElementRange: fact.ArrayElementRange,
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
		globalTypes := localSetGlobalTypes(block)
		allocFields := make(map[int][]string)
		allocValues := make(map[int]map[string]int)
		allocTypes := make(map[int]map[string]Type)
		allocFieldTableFacts := make(map[int]map[string]FixedShapeTableFact)
		allocStringValueFacts := make(map[int]*FixedShapeTableFact)
		killed := make(map[int]bool)
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpNewTable:
				allocFields[instr.ID] = nil
				allocValues[instr.ID] = make(map[string]int)
				allocTypes[instr.ID] = make(map[string]Type)
				allocFieldTableFacts[instr.ID] = make(map[string]FixedShapeTableFact)
				out[instr.ID] = FixedShapeTableFact{}
			case OpNewFixedTable:
				fact, ok := fixedShapeFactForFixedConstructor(fn, instr, globalTypes)
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
				if instr.Args[1].Def != nil {
					if typ := inferFixedCtorArgType(instr.Args[1].Def, globalTypes, make(map[int]bool)); typ != TypeUnknown && typ != TypeAny {
						allocTypes[allocID][name] = typ
					}
				}
				if valueFact, ok := out[instr.Args[1].ID]; ok && fixedShapeTableFactHasUsableTableFact(valueFact) {
					allocFieldTableFacts[allocID][name] = withoutFieldValues(valueFact)
					allocTypes[allocID][name] = TypeTable
				}
				out[allocID] = FixedShapeTableFact{
					ShapeID:         runtime.GetShapeID(allocFields[allocID]),
					FieldNames:      append([]string(nil), allocFields[allocID]...),
					FieldValueIDs:   cloneStringIntMap(allocValues[allocID]),
					FieldTypes:      cloneStringTypeMap(allocTypes[allocID]),
					FieldTableFacts: cloneFixedShapeTableFactMap(allocFieldTableFacts[allocID]),
				}
			case OpSetTable:
				if len(instr.Args) < 3 || instr.Args[0] == nil || instr.Args[1] == nil || instr.Args[2] == nil {
					continue
				}
				allocID := instr.Args[0].ID
				if _, ok := allocValues[allocID]; !ok || killed[allocID] {
					continue
				}
				valueFact, hasValueFact := out[instr.Args[2].ID]
				if tableKeyProvenString(fn, instr, instr.Args[1]) && hasValueFact && valueFact.ShapeID != 0 && len(valueFact.FieldNames) != 0 {
					stripped := withoutFieldValues(valueFact)
					if existing := allocStringValueFacts[allocID]; existing == nil {
						allocStringValueFacts[allocID] = cloneFixedShapeTableFactPtr(stripped)
					} else if merged := mergeStringValueFacts(existing, &stripped); merged != nil {
						allocStringValueFacts[allocID] = merged
					} else {
						killed[allocID] = true
						delete(out, allocID)
						continue
					}
					fact := out[allocID]
					fact.StringValueFact = cloneFixedShapeTableFactPtrFromPtr(allocStringValueFacts[allocID])
					out[allocID] = fact
					continue
				}
				killed[allocID] = true
				delete(out, allocID)
			case OpAppend, OpSetList:
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

func fixedShapeFactForFixedConstructor(fn *Function, instr *Instr, globalTypes map[int64]Type) (FixedShapeTableFact, bool) {
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
	types := make(map[string]Type, len(fields))
	for i, field := range fields {
		values[field] = instr.Args[i].ID
		if instr.Args[i].Def != nil {
			if typ := inferFixedCtorArgType(instr.Args[i].Def, globalTypes, make(map[int]bool)); typ != TypeUnknown && typ != TypeAny {
				types[field] = typ
			}
		}
	}
	return FixedShapeTableFact{
		ShapeID:       runtime.GetShapeID(fields),
		FieldNames:    fields,
		FieldValueIDs: values,
		FieldTypes:    types,
	}, true
}

func annotateFixedShapeGetFields(fn *Function, facts map[int]FixedShapeTableFact) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if annotateFixedShapeFieldLoad(fn, block, instr, facts) {
				continue
			}
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
			name := fixedShapeFieldNameFromAux(fn, instr)
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
			if nested, ok := fact.FieldTableFacts[name]; ok && fixedShapeTableFactHasUsableTableFact(nested) {
				facts[instr.ID] = nested
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				if nested.ShapeID != 0 {
					functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
						fmt.Sprintf("field %q carries guarded nested fixed table shape %v", name, nested.FieldNames))
				} else {
					functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
						fmt.Sprintf("field %q carries guarded nested array element type %s", name, nested.ArrayElementType))
				}
			}
		}
	}
}

func annotateFixedShapeStringValueAccesses(fn *Function, facts map[int]FixedShapeTableFact) {
	if fn == nil || len(facts) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			var table *Value
			switch instr.Op {
			case OpGetTable:
				if len(instr.Args) < 2 || !tableKeyProvenString(fn, instr, instr.Args[1]) {
					continue
				}
				table = instr.Args[0]
			case OpGetTableStringFormatInt:
				table = instr.Args[0]
			default:
				continue
			}
			fact, ok := facts[table.ID]
			if !ok || fact.StringValueFact == nil || !fixedShapeTableFactHasUsableTableFact(*fact.StringValueFact) {
				continue
			}
			valueFact := cloneFixedShapeTableFact(*fact.StringValueFact)
			facts[instr.ID] = valueFact
			recordFixedShapeCatalogFact(fn, valueFact)
			if instr.Type == TypeAny || instr.Type == TypeUnknown {
				instr.Type = TypeTable
			}
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("string-map value carries guarded fixed table shape %v", valueFact.FieldNames))
		}
	}
}

func tableKeyProvenString(fn *Function, instr *Instr, key *Value) bool {
	if key != nil && key.Def != nil && (key.Def.Type == TypeString || key.Def.Op == OpConstString || key.Def.Op == OpStringFormatInt || key.Def.Op == OpStringFormatConst) {
		return true
	}
	if key != nil && key.Def != nil && isStringFieldCall(fn, key.Def, "format") {
		return true
	}
	proto := instrSourceProto(fn, instr)
	if proto == nil || instr == nil || !instr.HasSource || instr.SourcePC < 0 {
		return false
	}
	return instr.SourcePC < len(proto.Feedback) && proto.Feedback[instr.SourcePC].Right == vm.FBString
}

func annotateFixedShapeFieldLoad(fn *Function, block *Block, instr *Instr, facts map[int]FixedShapeTableFact) bool {
	if instr == nil || (instr.Op != OpFieldLoad && instr.Op != OpFieldLoadNumToFloat) || len(instr.Args) == 0 || instr.Args[0] == nil {
		return false
	}
	svals := instr.Args[0].Def
	if svals == nil || svals.Op != OpFieldSvals || len(svals.Args) == 0 || svals.Args[0] == nil {
		return true
	}
	fact, ok := fixedShapeFactForFieldSvals(fn, facts, svals)
	if !ok || fact.ShapeID == 0 || fact.ShapeID != uint32(svals.Aux) {
		return true
	}
	fieldIdx := int(instr.Aux)
	if fieldIdx < 0 || fieldIdx >= len(fact.FieldNames) {
		return true
	}
	name := fact.FieldNames[fieldIdx]
	if typ, ok := fact.FieldTypes[name]; ok && typ != TypeUnknown && typ != TypeAny && instr.Op == OpFieldLoad {
		instr.Type = typ
	}
	if r, ok := fact.FieldRanges[name]; ok && r.known {
		if fn.ProfiledIntRanges == nil {
			fn.ProfiledIntRanges = make(map[int]intRange)
		}
		fn.ProfiledIntRanges[instr.ID] = r
		if instr.Op == OpFieldLoad && (instr.Type == TypeAny || instr.Type == TypeUnknown) {
			instr.Type = TypeInt
		}
		functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
			fmt.Sprintf("field-load %q carries guarded int range [%d,%d]", name, r.min, r.max))
	}
	if r, ok := fact.FieldLenRanges[name]; ok && r.known {
		recordProfiledLenRange(fn, instr.ID, r)
		functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
			fmt.Sprintf("field-load %q carries guarded string-len range [%d,%d]", name, r.min, r.max))
	}
	if nested, ok := fact.FieldTableFacts[name]; ok && fixedShapeTableFactHasUsableTableFact(nested) && instr.Op == OpFieldLoad {
		facts[instr.ID] = nested
		if instr.Type == TypeAny || instr.Type == TypeUnknown {
			instr.Type = TypeTable
		}
		if nested.ShapeID != 0 {
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("field-load %q carries guarded nested fixed table shape %v", name, nested.FieldNames))
		} else {
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("field-load %q carries guarded nested array element type %s", name, nested.ArrayElementType))
		}
	}
	return true
}

func fixedShapeFactForFieldSvals(fn *Function, facts map[int]FixedShapeTableFact, svals *Instr) (FixedShapeTableFact, bool) {
	if svals == nil || len(svals.Args) == 0 || svals.Args[0] == nil || svals.Aux == 0 {
		return FixedShapeTableFact{}, false
	}
	shapeID := uint32(svals.Aux)
	if fact, ok := facts[svals.Args[0].ID]; ok && fact.ShapeID == shapeID {
		return fact, true
	}
	if fn != nil && fn.FieldPolyShapeCatalog != nil {
		if fact, ok := fn.FieldPolyShapeCatalog[shapeID]; ok && fact.ShapeID == shapeID {
			return fact, true
		}
	}
	if fn == nil || len(fn.FieldPolyShapeFacts) == 0 {
		return FixedShapeTableFact{}, false
	}
	var found FixedShapeTableFact
	for _, cases := range fn.FieldPolyShapeFacts {
		for _, c := range cases {
			if c.ShapeID != shapeID || c.ReceiverFact.ShapeID != shapeID {
				continue
			}
			if found.ShapeID != 0 {
				return FixedShapeTableFact{}, false
			}
			found = c.ReceiverFact
		}
	}
	if found.ShapeID == 0 {
		return FixedShapeTableFact{}, false
	}
	return found, true
}

func annotateFixedShapeSetFields(fn *Function, facts map[int]FixedShapeTableFact) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpSetField || len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
				continue
			}
			if instr.Aux2 != 0 || !valueProvenNonNil(instr.Args[1]) {
				continue
			}
			fact, ok := facts[instr.Args[0].ID]
			if !ok || fact.ShapeID == 0 {
				continue
			}
			name := fixedShapeFieldNameFromAux(fn, instr)
			if name == "" {
				continue
			}
			idx, ok := fact.fieldIndex(name)
			if !ok {
				continue
			}
			instr.Aux2 = int64(fact.ShapeID)<<32 | int64(uint32(idx))
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("prefilled fixed-shape setfield cache for %q", name))
		}
	}
}

func fixedShapeTableFactHasUsableTableFact(fact FixedShapeTableFact) bool {
	return fact.ShapeID != 0 || fact.ArrayElementType != TypeUnknown || fact.ArrayElementRange.known || fact.StringValueFact != nil
}

func fixedShapeFieldNameFromAux(fn *Function, instr *Instr) string {
	if instr == nil {
		return ""
	}
	proto := instrSourceProto(fn, instr)
	if proto == nil || instr.Aux < 0 || int(instr.Aux) >= len(proto.Constants) {
		return fieldNameFromAux(fn, instr.Aux)
	}
	k := proto.Constants[instr.Aux]
	if !k.IsString() {
		return fieldNameFromAux(fn, instr.Aux)
	}
	return k.Str()
}

func annotateFixedShapeArrayElementAccesses(fn *Function, facts map[int]FixedShapeTableFact) {
	if fn == nil || len(facts) == 0 {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || len(instr.Args) < 2 || instr.Args[0] == nil {
				continue
			}
			factValue := instr.Args[0]
			keyArgIdx := 1
			if instr.Op == OpTableArrayLoad {
				if len(instr.Args) < 3 || instr.Args[2] == nil {
					continue
				}
				tableValue, ok := loweredTableArrayLoadTableValue(instr)
				if !ok {
					continue
				}
				factValue = tableValue
				keyArgIdx = 2
			}
			fact, ok := facts[factValue.ID]
			if !ok {
				continue
			}
			kind, ok := fixedShapeArrayElementFBKind(fact)
			if !ok || !tableKeyProvenInt(instr.Args[keyArgIdx]) {
				continue
			}
			switch instr.Op {
			case OpGetTable:
				if instr.Aux2 == 0 {
					instr.Aux2 = kind
				}
				if typ, ok := tableArrayKindElementType(kind); ok && (instr.Type == TypeAny || instr.Type == TypeUnknown) {
					instr.Type = typ
				}
				if r := fact.ArrayElementRange; r.known {
					if fn.ProfiledIntRanges == nil {
						fn.ProfiledIntRanges = make(map[int]intRange)
					}
					fn.ProfiledIntRanges[instr.ID] = r
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("table value carries guarded array element kind %d", kind))
			case OpTableArrayLoad:
				if instr.Aux == 0 || instr.Aux == int64(vm.FBKindMixed) {
					instr.Aux = kind
					setLoweredTableArrayPipelineKind(instr, kind)
				}
				if typ, ok := tableArrayKindElementType(kind); ok && (instr.Type == TypeAny || instr.Type == TypeUnknown) {
					instr.Type = typ
				}
				if r := fact.ArrayElementRange; r.known {
					if fn.ProfiledIntRanges == nil {
						fn.ProfiledIntRanges = make(map[int]intRange)
					}
					fn.ProfiledIntRanges[instr.ID] = r
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("lowered table value carries guarded array element kind %d", kind))
			case OpSetTable:
				if instr.Aux2 == 0 && fixedShapeSetTableValueMatchesArrayKind(instr, kind) {
					instr.Aux2 = kind
					functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
						fmt.Sprintf("table store carries guarded array element kind %d", kind))
				}
			}
		}
	}
}

func loweredTableArrayLoadTableValue(instr *Instr) (*Value, bool) {
	if instr == nil || instr.Op != OpTableArrayLoad || len(instr.Args) < 1 || instr.Args[0] == nil {
		return nil, false
	}
	data := instr.Args[0].Def
	if data == nil || data.Op != OpTableArrayData || len(data.Args) < 1 || data.Args[0] == nil {
		return nil, false
	}
	header := data.Args[0].Def
	if header == nil || header.Op != OpTableArrayHeader || len(header.Args) < 1 || header.Args[0] == nil {
		return nil, false
	}
	return header.Args[0], true
}

func setLoweredTableArrayPipelineKind(load *Instr, kind int64) {
	if load == nil || load.Op != OpTableArrayLoad || len(load.Args) < 2 || load.Args[0] == nil || load.Args[1] == nil {
		return
	}
	data := load.Args[0].Def
	length := load.Args[1].Def
	if data == nil || data.Op != OpTableArrayData || length == nil || length.Op != OpTableArrayLen {
		return
	}
	if len(data.Args) < 1 || data.Args[0] == nil || len(length.Args) < 1 || length.Args[0] == nil {
		return
	}
	header := data.Args[0].Def
	if header == nil || header.Op != OpTableArrayHeader || length.Args[0].ID != header.ID {
		return
	}
	header.Aux = kind
	length.Aux = kind
	data.Aux = kind
}

func fixedShapeArrayElementFBKind(fact FixedShapeTableFact) (int64, bool) {
	switch fact.ArrayElementType {
	case TypeInt:
		return int64(vm.FBKindInt), true
	case TypeFloat:
		return int64(vm.FBKindFloat), true
	case TypeBool:
		return int64(vm.FBKindBool), true
	case TypeAny, TypeUnknown:
		if fact.ArrayElementRange.known {
			return int64(vm.FBKindInt), true
		}
	}
	return 0, false
}

func fixedShapeSetTableValueMatchesArrayKind(instr *Instr, kind int64) bool {
	if instr == nil || len(instr.Args) < 3 || instr.Args[2] == nil || instr.Args[2].Def == nil {
		return false
	}
	switch kind {
	case int64(vm.FBKindInt):
		return callABIValueIsInt(instr.Args[2])
	case int64(vm.FBKindFloat):
		return instr.Args[2].Def.Type == TypeFloat
	case int64(vm.FBKindBool):
		return instr.Args[2].Def.Type == TypeBool
	case int64(vm.FBKindMixed):
		return true
	default:
		return false
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

func cloneStringUintptrMap(in map[string]uintptr) map[string]uintptr {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]uintptr, len(in))
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
		out[k] = cloneFixedShapeTableFact(v)
	}
	return out
}

func cloneFixedShapeTableFactPtr(fact FixedShapeTableFact) *FixedShapeTableFact {
	cloned := cloneFixedShapeTableFact(fact)
	return &cloned
}

func cloneFixedShapeTableFactPtrFromPtr(fact *FixedShapeTableFact) *FixedShapeTableFact {
	if fact == nil {
		return nil
	}
	return cloneFixedShapeTableFactPtr(*fact)
}

func cloneFixedShapeTableFact(fact FixedShapeTableFact) FixedShapeTableFact {
	fact.FieldNames = append([]string(nil), fact.FieldNames...)
	fact.FieldValueIDs = cloneStringIntMap(fact.FieldValueIDs)
	fact.FieldFacts = cloneFixedShapeFieldFactMap(fact.FieldFacts)
	fact.FieldTypes = cloneStringTypeMap(fact.FieldTypes)
	fact.FieldRanges = cloneStringRangeMap(fact.FieldRanges)
	fact.FieldLenRanges = cloneStringRangeMap(fact.FieldLenRanges)
	fact.FieldVMProtos = cloneStringProtoMap(fact.FieldVMProtos)
	fact.FieldVMClosures = cloneStringUintptrMap(fact.FieldVMClosures)
	fact.FieldTableFacts = cloneFixedShapeTableFactMap(fact.FieldTableFacts)
	fact.StringValueFact = cloneFixedShapeTableFactPtrFromPtr(fact.StringValueFact)
	return fact
}

func cloneFixedShapeFieldFactMap(in map[string]FixedShapeFieldFact) map[string]FixedShapeFieldFact {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]FixedShapeFieldFact, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
