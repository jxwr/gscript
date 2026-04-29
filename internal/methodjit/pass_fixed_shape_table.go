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
	ShapeID       uint32
	FieldNames    []string
	FieldValueIDs map[string]int
	FieldFacts    map[string]FixedShapeFieldFact
	Guarded       bool
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
// early IR. Ctor2Index indexes FuncProto.TableCtors2 for the current two-field
// constructor form.
type FixedTableConstructorFact struct {
	Ctor2Index int
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
// current function. ArgFacts are guarded callsite facts for callee parameters;
// they must not be used for unconditional value forwarding.
type FixedShapeTableFactsConfig struct {
	Globals  map[string]*vm.FuncProto
	ArgFacts map[int]FixedShapeTableFact
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

func guardedFixedShapeArgFact(fact FixedShapeTableFact) (FixedShapeTableFact, bool) {
	if fact.ShapeID == 0 || len(fact.FieldNames) == 0 {
		return FixedShapeTableFact{}, false
	}
	return FixedShapeTableFact{
		ShapeID:    fact.ShapeID,
		FieldNames: append([]string(nil), fact.FieldNames...),
		Guarded:    true,
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
		ShapeID:    fact.ShapeID,
		FieldNames: append([]string(nil), fact.FieldNames...),
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
	if instr.Aux2 != 2 || len(instr.Args) != 2 {
		return FixedShapeTableFact{}, false
	}
	ctorIdx := int(instr.Aux)
	if ctorIdx < 0 || ctorIdx >= len(fn.Proto.TableCtors2) {
		return FixedShapeTableFact{}, false
	}
	ctor := fn.Proto.TableCtors2[ctorIdx].Runtime
	if ctor.Key1 == ctor.Key2 {
		return FixedShapeTableFact{}, false
	}
	fields := []string{ctor.Key1, ctor.Key2}
	return FixedShapeTableFact{
		ShapeID:    runtime.GetShapeID(fields),
		FieldNames: fields,
		FieldValueIDs: map[string]int{
			ctor.Key1: instr.Args[0].ID,
			ctor.Key2: instr.Args[1].ID,
		},
	}, true
}

func annotateFixedShapeGetFields(fn *Function, facts map[int]FixedShapeTableFact) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetField || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			if instr.Aux2 != 0 {
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
			instr.Aux2 = int64(fact.ShapeID)<<32 | int64(uint32(idx))
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("prefilled fixed-shape field cache for %q", name))
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
