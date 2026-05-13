//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"strings"

	"github.com/gscript/gscript/internal/vm"
)

// AnnotateCallABIsPass annotates stable raw-int callsite ABI facts after
// inlining and TypeSpec have exposed precise argument types.
func AnnotateCallABIsPass(config CallABIAnnotationConfig) PassFunc {
	return func(fn *Function) (*Function, error) {
		return AnnotateCallABIs(fn, config), nil
	}
}

// AnnotateCallABIs installs CallABIDescriptor entries for non-tail, fixed
// arity global calls whose callee has a specialized ABI and whose actual
// arguments match that ABI.
func AnnotateCallABIs(fn *Function, config CallABIAnnotationConfig) *Function {
	if fn == nil {
		return fn
	}
	globals := callABIMergeGlobals(config.Globals, callABIStableGlobals(fn.Proto))
	fn.CallABIs = nil

	tails := callABITailCalls(fn)
	shiftAddOverflowVersions := make(map[*vm.FuncProto]bool)
	descs := make(map[int]CallABIDescriptor)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			if callABIAnnotateRawIntSelfResult(fn, instr, tails) {
				functionRemarks(fn).Add("CallABI", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("annotated raw-int self call result for %s", fn.Proto.Name))
				continue
			}
			if callABIAnnotateTypedSelfResult(fn, instr, tails) {
				functionRemarks(fn).Add("CallABI", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("annotated typed self call result for %s", fn.Proto.Name))
				continue
			}
			desc, reason := callABIDescriptorFor(fn, instr, globals, tails, shiftAddOverflowVersions, config)
			if desc.Callee == nil {
				if summary := fieldShapeCalleeSummary(fn, instr); summary != "" {
					reason = reason + "; field-shape polymorphic callee set: " + summary
					if abiSummary := fieldShapeCalleeABISummary(fn, instr); abiSummary != "" {
						reason = reason + "; ABI candidates: " + abiSummary
					}
				}
				functionRemarks(fn).Add("CallABI", "missed", block.ID, instr.ID, instr.Op, reason)
				continue
			}
			descs[instr.ID] = desc
			switch desc.ReturnRep {
			case SpecializedABIReturnRawInt:
				instr.Type = TypeInt
			case SpecializedABIReturnRawFloat:
				instr.Type = TypeFloat
			case SpecializedABIReturnRawTablePtr:
				instr.Type = TypeTable
			default:
				instr.Type = TypeInt
			}
			functionRemarks(fn).Add("CallABI", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("annotated %s call ABI for %s", callABIDescriptorKind(desc), desc.Callee.Name))
		}
	}
	if len(descs) > 0 {
		fn.CallABIs = descs
	}
	return fn
}

func callABIDescriptorKind(desc CallABIDescriptor) string {
	if desc.TypedPeer {
		return "typed-peer"
	}
	return "raw-int"
}

func callABIAnnotateRawIntSelfResult(fn *Function, instr *Instr, tails map[int]bool) bool {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpCall {
		return false
	}
	if tails[instr.ID] || !callABIHasExactResultShape(fn, instr, 1) || !callABIIsStaticSelfCall(fn, instr) {
		return false
	}
	abi := AnalyzeRawIntSelfABI(fn.Proto)
	if !abi.Eligible || abi.Return != SpecializedABIReturnRawInt {
		return false
	}
	numArgs := len(instr.Args) - 1
	if numArgs != abi.NumParams {
		return false
	}
	for i := 0; i < numArgs; i++ {
		if !callABIValueIsInt(instr.Args[1+i]) {
			return false
		}
	}
	instr.Type = TypeInt
	return true
}

func callABIAnnotateTypedSelfResult(fn *Function, instr *Instr, tails map[int]bool) bool {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpCall {
		return false
	}
	if tails[instr.ID] || !callABIIsStaticSelfCall(fn, instr) {
		return false
	}
	abi := AnalyzeTypedSelfABI(fn.Proto)
	if !abi.Eligible {
		return false
	}
	wantRets := 1
	if abi.Return == SpecializedABIReturnNone {
		wantRets = 0
	}
	if !callABIHasExactResultShape(fn, instr, wantRets) {
		return false
	}
	numArgs := len(instr.Args) - 1
	if numArgs != abi.NumParams || len(abi.Params) != numArgs {
		return false
	}
	for i := 0; i < numArgs; i++ {
		switch abi.Params[i] {
		case SpecializedABIParamRawInt:
			if !callABIValueIsInt(instr.Args[1+i]) {
				return false
			}
		case SpecializedABIParamRawTablePtr:
			if !callABIValueIsTable(instr.Args[1+i]) &&
				!typedSelfCallArgSlotMatches(fn.Proto, instr.SourcePC, i, SpecializedABIParamRawTablePtr) {
				return false
			}
		default:
			return false
		}
	}
	switch abi.Return {
	case SpecializedABIReturnRawInt:
		instr.Type = TypeInt
	case SpecializedABIReturnRawFloat:
		instr.Type = TypeFloat
	case SpecializedABIReturnRawTablePtr:
		instr.Type = TypeTable
	case SpecializedABIReturnNone:
		// CALL C=1 produces no value. Leave the synthetic SSA call value
		// untyped; codegen only uses the side effect and must not consume a
		// fabricated raw result.
	default:
		return false
	}
	return true
}

func callABIDescriptorFor(fn *Function, instr *Instr, globals map[string]*vm.FuncProto, tails map[int]bool, shiftAddOverflowVersions map[*vm.FuncProto]bool, config CallABIAnnotationConfig) (CallABIDescriptor, string) {
	if instr == nil || instr.Op != OpCall {
		return CallABIDescriptor{}, "not a call"
	}
	if tails[instr.ID] {
		return CallABIDescriptor{}, "tail call"
	}
	_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
	if callee == nil {
		if feedbackCallee, ok := callABIFeedbackCalleeProto(fn, instr); ok {
			callee = feedbackCallee
		}
	}
	if callee == nil {
		if protos := fieldShapeCalleeProtos(fn, instr); len(protos) == 1 {
			callee = protos[0]
		}
	}
	if callee == nil {
		return CallABIDescriptor{}, "callee is not resolved from stable globals or call feedback"
	}
	if fn != nil && callee == fn.Proto {
		return CallABIDescriptor{}, "self call uses separate raw-int result annotation"
	}
	typedPeerReason := ""
	if desc, ok, reason := callABITypedPeerDescriptorFor(fn, instr, callee, config); ok {
		return desc, ""
	} else if reason != "" {
		typedPeerReason = reason
	}
	miss := func(reason string) (CallABIDescriptor, string) {
		if typedPeerReason != "" {
			reason = reason + "; typed-peer: " + typedPeerReason
		}
		return CallABIDescriptor{}, reason
	}
	if !callABIHasExactResultShape(fn, instr, 1) {
		return miss("call does not have fixed arity and one exact result")
	}
	abi := AnalyzeSpecializedABI(callee)
	crossRecursiveNumeric := false
	if !abi.Eligible || abi.Kind != SpecializedABIRawInt || abi.Return != SpecializedABIReturnRawInt {
		crossRecursiveNumeric = qualifiesForNumericCrossRecursiveCandidate(callee)
		if !crossRecursiveNumeric && abi.RejectWhy != "" {
			return miss("callee raw-int ABI rejected: " + abi.RejectWhy)
		}
		if !crossRecursiveNumeric {
			return miss("callee is not raw-int ABI eligible")
		}
	}
	if callABICalleeHasShiftAddOverflowVersion(callee, shiftAddOverflowVersions) {
		return miss("callee may promote raw-int recurrence on overflow")
	}
	numArgs := len(instr.Args) - 1
	if numArgs != callee.NumParams {
		return miss("argument count does not match callee ABI")
	}
	if !crossRecursiveNumeric && len(abi.Params) != numArgs {
		return miss("argument count does not match callee ABI")
	}
	rawParams := make([]bool, numArgs)
	for i := 0; i < numArgs; i++ {
		if !crossRecursiveNumeric && abi.Params[i] != SpecializedABIParamRawInt {
			return miss("callee has non-raw-int ABI parameter")
		}
		if !callABIValueIsInt(instr.Args[1+i]) {
			return miss("actual argument is not TypeInt")
		}
		rawParams[i] = true
	}
	return CallABIDescriptor{
		Callee:       callee,
		NumArgs:      numArgs,
		NumRets:      1,
		RawIntParams: rawParams,
		RawIntReturn: true,
		ParamReps:    specializedRawIntParamReps(numArgs),
		ReturnRep:    SpecializedABIReturnRawInt,
	}, ""
}

func callABITypedPeerDescriptorFor(fn *Function, instr *Instr, callee *vm.FuncProto, config CallABIAnnotationConfig) (CallABIDescriptor, bool, string) {
	if fn == nil || instr == nil || callee == nil {
		return CallABIDescriptor{}, false, ""
	}
	argFacts := callABITypedPeerArgFacts(fn, instr, callee)
	arrayElementArgFacts := profiledFixedShapeArrayElementArgFactsForProto(callee)
	arrayElementArgFacts = mergeFixedShapeTableFacts(arrayElementArgFacts,
		callABITypedPeerArrayElementArgFacts(fn, instr, config.Globals))
	if len(config.Globals) > 0 {
		arrayElementArgFacts = mergeFixedShapeTableFacts(arrayElementArgFacts,
			inferGuardedFixedShapeArrayElementArgFactsForProto(callee, config.Globals))
	}
	abi := AnalyzeTypedPeerABIWithFacts(callee, argFacts, arrayElementArgFacts)
	if !abi.Eligible {
		return CallABIDescriptor{}, false, "callee typed-peer ABI rejected: " + abi.RejectWhy
	}
	wantRets := 1
	switch abi.Return {
	case SpecializedABIReturnRawInt, SpecializedABIReturnRawFloat, SpecializedABIReturnRawTablePtr:
	case SpecializedABIReturnNone:
		wantRets = 0
	default:
		return CallABIDescriptor{}, false, "typed-peer return is not directly representable"
	}
	if !callABIHasExactResultShape(fn, instr, wantRets) {
		if wantRets == 0 {
			return CallABIDescriptor{}, false, "typed-peer requires no exact results"
		}
		return CallABIDescriptor{}, false, "typed-peer requires one exact result"
	}
	numArgs := len(instr.Args) - 1
	if numArgs != callee.NumParams || len(abi.Params) != numArgs {
		return CallABIDescriptor{}, false, "argument count does not match typed-peer ABI"
	}
	params := callABIRefineTypedPeerParamsFromFeedback(fn, instr, abi.Params)
	for _, rep := range params {
		switch rep {
		case SpecializedABIParamRawInt:
			// The typed-peer emitter guards non-TypeInt actuals before
			// unboxing, so global/profiled values can still use the ABI.
		case SpecializedABIParamRawFloat:
			// Float arguments are passed as raw IEEE bits in the same Xn slots
			// used by the typed entry and are guarded by the caller when needed.
		case SpecializedABIParamRawTablePtr:
			// The native call path guards the boxed argument at runtime before
			// passing the raw table pointer. A fixed receiver shape fact is only
			// needed when the callee consumes fields of the parameter itself.
		default:
			return CallABIDescriptor{}, false, "unsupported typed-peer parameter"
		}
	}
	return CallABIDescriptor{
		Callee:    callee,
		NumArgs:   numArgs,
		NumRets:   wantRets,
		TypedPeer: true,
		ParamReps: append([]SpecializedABIParamRep(nil), params...),
		ReturnRep: abi.Return,
		ArgFacts:  cloneCallABIArgFacts(argFacts),
	}, true, ""
}

func callABIRefineTypedPeerParamsFromFeedback(fn *Function, instr *Instr, params []SpecializedABIParamRep) []SpecializedABIParamRep {
	out := append([]SpecializedABIParamRep(nil), params...)
	if instr == nil {
		return out
	}
	for i := range out {
		if i+1 < len(instr.Args) && instr.Args[i+1] != nil &&
			instr.Args[i+1].Def != nil && instr.Args[i+1].Def.Type == TypeFloat &&
			out[i] == SpecializedABIParamRawInt {
			out[i] = SpecializedABIParamRawFloat
		}
	}
	if fn == nil || fn.Proto == nil || instr == nil || !instr.HasSource ||
		instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.CallSiteFeedback) {
		return out
	}
	fb := fn.Proto.CallSiteFeedback[instr.SourcePC]
	if fb.Count < wholeCallKernelMinStableObservations ||
		fb.Flags&(vm.CallSiteCalleePolymorphic|vm.CallSiteArityPolymorphic) != 0 ||
		int(fb.NArgs) != len(instr.Args)-1 ||
		fb.ResultArity != uint8(instr.Aux2) {
		return out
	}
	for i := range out {
		if i >= len(fb.ArgTypes) {
			break
		}
		if out[i] == SpecializedABIParamRawInt && fb.ArgTypes[i] == vm.FBFloat {
			out[i] = SpecializedABIParamRawFloat
		}
	}
	return out
}

func callABITypedPeerArgFacts(fn *Function, instr *Instr, callee *vm.FuncProto) map[int]FixedShapeTableFact {
	cases := fieldShapeCalleeCases(fn, instr)
	if len(cases) != 1 || cases[0].VMProto != callee || cases[0].ReceiverFact.ShapeID == 0 {
		return nil
	}
	return map[int]FixedShapeTableFact{0: cases[0].ReceiverFact}
}

func callABITypedPeerArrayElementArgFacts(fn *Function, instr *Instr, globals map[string]*vm.FuncProto) map[int]FixedShapeTableFact {
	if fn == nil || instr == nil || len(instr.Args) < 2 {
		return nil
	}
	arrayFacts := inferLocalArrayElementTableFacts(fn, fn.FixedShapeTables)
	arrayFacts = mergeFixedShapeTableFacts(arrayFacts, inferArrayElementValuesForArgs(fn, globals))
	if len(arrayFacts) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact)
	for i := 1; i < len(instr.Args); i++ {
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
		out[i-1] = guarded
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneCallABIArgFacts(in map[int]FixedShapeTableFact) map[int]FixedShapeTableFact {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact, len(in))
	for k, v := range in {
		out[k] = cloneFixedShapeTableFact(v)
	}
	return out
}

func specializedRawIntParamReps(n int) []SpecializedABIParamRep {
	if n <= 0 {
		return nil
	}
	out := make([]SpecializedABIParamRep, n)
	for i := range out {
		out[i] = SpecializedABIParamRawInt
	}
	return out
}

func callABIFeedbackCalleeProto(fn *Function, instr *Instr) (*vm.FuncProto, bool) {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpCall ||
		!instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.CallSiteFeedback) {
		return nil, false
	}
	if specGuardKindSuppressed(fn, instr.SourcePC, "GuardCalleeProto") {
		return nil, false
	}
	fb := fn.Proto.CallSiteFeedback[instr.SourcePC]
	if fb.Count < wholeCallKernelMinStableObservations ||
		fb.Flags&(vm.CallSiteCalleePolymorphic|vm.CallSiteArityPolymorphic) != 0 ||
		int(fb.NArgs) != len(instr.Args)-1 ||
		fb.ResultArity != uint8(instr.Aux2) {
		return nil, false
	}
	return fb.StableCalleeVMProto()
}

func fieldShapeCalleeProtos(fn *Function, instr *Instr) []*vm.FuncProto {
	cases := fieldShapeCalleeCases(fn, instr)
	if len(cases) == 0 {
		return nil
	}
	out := make([]*vm.FuncProto, 0, len(cases))
	seen := make(map[*vm.FuncProto]bool, len(cases))
	for _, c := range cases {
		if c.VMProto == nil || seen[c.VMProto] {
			continue
		}
		out = append(out, c.VMProto)
		seen[c.VMProto] = true
	}
	return out
}

func fieldShapeCalleeCases(fn *Function, instr *Instr) []FieldPolyShapeCase {
	if fn == nil || instr == nil || instr.Op != OpCall || len(instr.Args) == 0 ||
		instr.Args[0] == nil || instr.Args[0].Def == nil {
		return nil
	}
	calleeLoad := instr.Args[0].Def
	if calleeLoad.Op != OpGetField {
		return nil
	}
	cases := fn.FieldPolyShapeFacts[calleeLoad.ID]
	if len(cases) == 0 {
		return nil
	}
	return cases
}

func fieldShapeCalleeSummary(fn *Function, instr *Instr) string {
	cases := fieldShapeCalleeCases(fn, instr)
	if len(cases) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cases))
	for _, c := range cases {
		name := "<nil>"
		if c.VMProto != nil {
			name = c.VMProto.Name
		}
		parts = append(parts, fmt.Sprintf("shape=%d field=%d proto=%s", c.ShapeID, c.FieldIdx, name))
	}
	return strings.Join(parts, "; ")
}

func fieldShapeCalleeABISummary(fn *Function, instr *Instr) string {
	cases := fieldShapeCalleeCases(fn, instr)
	if len(cases) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cases))
	for _, c := range cases {
		if c.VMProto == nil {
			parts = append(parts, fmt.Sprintf("shape=%d proto=<nil> abi=missing", c.ShapeID))
			continue
		}
		typed := AnalyzeTypedPeerABIWithArgFacts(c.VMProto, map[int]FixedShapeTableFact{0: c.ReceiverFact})
		if typed.Eligible {
			parts = append(parts, fmt.Sprintf("shape=%d proto=%s abi=typed-peer params=%s return=%s",
				c.ShapeID, c.VMProto.Name, specializedABIParamSummary(typed.Params), specializedABIReturnName(typed.Return)))
			continue
		}
		raw := AnalyzeSpecializedABI(c.VMProto)
		if raw.Eligible {
			parts = append(parts, fmt.Sprintf("shape=%d proto=%s abi=raw-int params=%s return=%s",
				c.ShapeID, c.VMProto.Name, specializedABIParamSummary(raw.Params), specializedABIReturnName(raw.Return)))
			continue
		}
		parts = append(parts, fmt.Sprintf("shape=%d proto=%s abi=boxed reason=%s",
			c.ShapeID, c.VMProto.Name, typed.RejectWhy))
	}
	return strings.Join(parts, "; ")
}

func specGuardKindSuppressed(fn *Function, pc int, kind string) bool {
	if fn == nil || pc < 0 {
		return false
	}
	if fn.SuppressedSpecGuardKinds != nil {
		kinds := fn.SuppressedSpecGuardKinds[pc]
		return kinds[kind] || kinds["*"]
	}
	return fn.SuppressedSpecGuardPCs != nil && fn.SuppressedSpecGuardPCs[pc]
}

func callABIHasExactResultShape(fn *Function, instr *Instr, wantRets int) bool {
	if fn == nil || fn.Proto == nil || instr == nil || len(instr.Args) == 0 {
		return false
	}
	if wantRets < 0 {
		return false
	}
	if n, ok := callExactFixedResultCountFromC(instr.Aux2); !ok || n != wantRets {
		return false
	}
	sourceProto := instrSourceProto(fn, instr)
	if !instr.HasSource || sourceProto == nil || instr.SourcePC < 0 || instr.SourcePC >= len(sourceProto.Code) {
		return false
	}
	inst := sourceProto.Code[instr.SourcePC]
	if vm.DecodeOp(inst) != vm.OP_CALL || vm.DecodeA(inst) != int(instr.Aux) {
		return false
	}
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	if b == 0 {
		return false
	}
	if n, ok := callExactFixedResultCountFromC(int64(c)); !ok || n != wantRets {
		return false
	}
	return b-1 == len(instr.Args)-1
}

func callExactFixedResultCountFromC(c int64) (int, bool) {
	switch {
	case c == 0:
		return 0, false
	case c == 1:
		return 0, true
	case c >= 2:
		return int(c) - 1, true
	default:
		return 0, false
	}
}

func callABIValueIsInt(v *Value) bool {
	return v != nil && v.Def != nil && v.Def.Type == TypeInt
}

func callABIValueIsTable(v *Value) bool {
	return v != nil && v.Def != nil && v.Def.Type == TypeTable
}

func callABIIsStaticSelfCall(fn *Function, instr *Instr) bool {
	if fn == nil || fn.Proto == nil || instr == nil || len(instr.Args) == 0 {
		return false
	}
	fnArg := instr.Args[0]
	if fnArg == nil || fnArg.Def == nil || fnArg.Def.Op != OpGetGlobal {
		return false
	}
	constIdx := int(fnArg.Def.Aux)
	if constIdx < 0 || constIdx >= len(fn.Proto.Constants) {
		return false
	}
	kv := fn.Proto.Constants[constIdx]
	return kv.IsString() && kv.Str() == fn.Proto.Name
}

func callABICalleeHasShiftAddOverflowVersion(callee *vm.FuncProto, memo map[*vm.FuncProto]bool) bool {
	if callee == nil {
		return false
	}
	if memo != nil {
		if cached, ok := memo[callee]; ok {
			return cached
		}
	}
	setResult := func(result bool) bool {
		if memo != nil {
			memo[callee] = result
		}
		return result
	}
	fn := BuildGraph(callee)
	if fn == nil || fn.Entry == nil || fn.Unpromotable {
		return setResult(false)
	}
	passes := []PassFunc{
		SimplifyPhisPass,
		TypeSpecializePass,
		ConstPropPass,
		DCEPass,
		RangeAnalysisPass,
		OverflowBoxingPass,
	}
	var err error
	for _, pass := range passes {
		fn, err = pass(fn)
		if err != nil {
			return setResult(false)
		}
	}
	_, ok := detectShiftAddOverflowVersion(fn)
	return setResult(ok)
}

func callABITailCalls(fn *Function) map[int]bool {
	out := make(map[int]bool)
	if fn == nil {
		return out
	}
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			j := i + 1
			for j < len(block.Instrs) && block.Instrs[j].Op == OpNop {
				j++
			}
			if j >= len(block.Instrs) {
				continue
			}
			next := block.Instrs[j]
			if next.Op == OpReturn && len(next.Args) == 1 && next.Args[0].ID == instr.ID {
				out[instr.ID] = true
			}
		}
	}
	return out
}

func callABIMergeGlobals(primary, secondary map[string]*vm.FuncProto) map[string]*vm.FuncProto {
	if len(primary) == 0 {
		return secondary
	}
	if len(secondary) == 0 {
		return primary
	}
	merged := make(map[string]*vm.FuncProto, len(primary)+len(secondary))
	for name, proto := range primary {
		merged[name] = proto
	}
	for name, proto := range secondary {
		if _, ok := merged[name]; !ok {
			merged[name] = proto
		}
	}
	return merged
}

func callABIStableGlobals(proto *vm.FuncProto) map[string]*vm.FuncProto {
	globals := make(map[string]*vm.FuncProto)
	if proto == nil {
		return globals
	}
	invalid := make(map[string]bool)
	regClosure := make(map[int]*vm.FuncProto)
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_CLOSURE:
			bx := vm.DecodeBx(inst)
			if bx < 0 || bx >= len(proto.Protos) {
				delete(regClosure, a)
				continue
			}
			regClosure[a] = proto.Protos[bx]
		case vm.OP_MOVE:
			b := vm.DecodeB(inst)
			if cl := regClosure[b]; cl != nil {
				regClosure[a] = cl
			} else {
				delete(regClosure, a)
			}
		case vm.OP_SETGLOBAL:
			name := callABIProtoConstString(proto, vm.DecodeBx(inst))
			if name == "" || invalid[name] {
				continue
			}
			cl := regClosure[a]
			if cl == nil {
				invalid[name] = true
				delete(globals, name)
				continue
			}
			if prev := globals[name]; prev != nil && prev != cl {
				invalid[name] = true
				delete(globals, name)
				continue
			}
			globals[name] = cl
		case vm.OP_CLOSE:
			continue
		default:
			delete(regClosure, a)
		}
	}
	return globals
}

func callABIProtoConstString(proto *vm.FuncProto, idx int) string {
	if proto == nil || idx < 0 || idx >= len(proto.Constants) {
		return ""
	}
	kv := proto.Constants[idx]
	if !kv.IsString() {
		return ""
	}
	return kv.Str()
}
