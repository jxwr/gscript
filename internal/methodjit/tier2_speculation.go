package methodjit

import (
	"fmt"
	"hash/fnv"
	"sort"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

// Tier2FeedbackSnapshot captures the feedback maturity seen by one Tier 2
// compile. It is intentionally compact: future refresh policy can compare
// snapshots without having to know every feedback vector's concrete layout.
type Tier2FeedbackSnapshot struct {
	TypeObserved     int
	FieldObserved    int
	TableKeyObserved int
	CallObserved     int
}

func (s Tier2FeedbackSnapshot) totalObserved() int {
	return s.TypeObserved + s.FieldObserved + s.TableKeyObserved + s.CallObserved
}

func (s Tier2FeedbackSnapshot) structuralObserved() int {
	return s.FieldObserved + s.TableKeyObserved + s.CallObserved
}

func (s Tier2FeedbackSnapshot) lessMatureThan(current Tier2FeedbackSnapshot) bool {
	return current.TypeObserved > s.TypeObserved ||
		current.FieldObserved > s.FieldObserved ||
		current.TableKeyObserved > s.TableKeyObserved ||
		current.CallObserved > s.CallObserved
}

func snapshotTier2Feedback(proto *vm.FuncProto) Tier2FeedbackSnapshot {
	return BuildTier2SpecializationProfile(proto).Snapshot
}

type SpecializationGuardKind string

const (
	SpecGuardResultType      SpecializationGuardKind = "result_type"
	SpecGuardOperandType     SpecializationGuardKind = "operand_type"
	SpecGuardTableKind       SpecializationGuardKind = "table_kind"
	SpecGuardFieldShape      SpecializationGuardKind = "field_shape"
	SpecGuardStringShapeKey  SpecializationGuardKind = "string_shape_key"
	SpecGuardCallNative      SpecializationGuardKind = "call_native"
	SpecGuardCallVMProto     SpecializationGuardKind = "call_vm_proto"
	SpecGuardCallPolymorphic SpecializationGuardKind = "call_poly_vm_proto"
)

type SpecializationGuard struct {
	Kind       SpecializationGuardKind
	PC         int
	Slot       string
	Type       Type
	FBType     vm.FeedbackType
	TableKind  uint8
	ShapeID    uint32
	FieldIdx   int
	Key        string
	AccessKind uint8
	Count      uint32

	CalleeNativeKind uint8
	CalleeNativeData uintptr
	CalleeVMProto    *vm.FuncProto
	CalleeVMProtos   []*vm.FuncProto
	NArgs            uint8
	ResultArity      uint8
}

type Tier2SpecializationVersion struct {
	Hash       uint64
	GuardCount int
}

type Tier2SpecializationProfile struct {
	Snapshot Tier2FeedbackSnapshot
	Guards   []SpecializationGuard
	Version  Tier2SpecializationVersion
}

type Tier2SpecializationSummary struct {
	TypeObserved     int                    `json:"type_observed"`
	FieldObserved    int                    `json:"field_observed"`
	TableKeyObserved int                    `json:"table_key_observed"`
	CallObserved     int                    `json:"call_observed"`
	VersionHash      string                 `json:"version_hash"`
	GuardCount       int                    `json:"guard_count"`
	GuardKinds       map[string]int         `json:"guard_kinds,omitempty"`
	SuppressedCount  int                    `json:"suppressed_count,omitempty"`
	SuppressedPCs    []int                  `json:"suppressed_pcs,omitempty"`
	Readiness        Tier2FeedbackReadiness `json:"readiness"`
}

func (p Tier2SpecializationProfile) Summary() Tier2SpecializationSummary {
	kinds := make(map[string]int)
	for _, g := range p.Guards {
		kinds[string(g.Kind)]++
	}
	return Tier2SpecializationSummary{
		TypeObserved:     p.Snapshot.TypeObserved,
		FieldObserved:    p.Snapshot.FieldObserved,
		TableKeyObserved: p.Snapshot.TableKeyObserved,
		CallObserved:     p.Snapshot.CallObserved,
		VersionHash:      fmt.Sprintf("%x", p.Version.Hash),
		GuardCount:       p.Version.GuardCount,
		GuardKinds:       kinds,
		Readiness:        Tier2FeedbackReadiness{Kind: Tier2FeedbackReadyWide},
	}
}

func (p Tier2SpecializationProfile) SummaryForProto(proto *vm.FuncProto) Tier2SpecializationSummary {
	summary := p.Summary()
	summary.Readiness = AnalyzeTier2FeedbackReadiness(proto, p.Snapshot)
	return summary
}

func (p Tier2SpeculationPlan) Summary() Tier2SpecializationSummary {
	summary := p.Profile.SummaryForProto(p.proto)
	if len(p.suppressedGuardPCs) == 0 {
		return summary
	}
	pcs := make([]int, 0, len(p.suppressedGuardPCs))
	for pc, ok := range p.suppressedGuardPCs {
		if ok {
			pcs = append(pcs, pc)
		}
	}
	sort.Ints(pcs)
	summary.SuppressedPCs = pcs
	summary.SuppressedCount = len(pcs)
	return summary
}

func (p Tier2SpecializationProfile) findGuard(pc int, kind SpecializationGuardKind, match func(SpecializationGuard) bool) (SpecializationGuard, bool) {
	for _, g := range p.Guards {
		if g.PC != pc || g.Kind != kind {
			continue
		}
		if match == nil || match(g) {
			return g, true
		}
	}
	return SpecializationGuard{}, false
}

func BuildTier2SpecializationProfile(proto *vm.FuncProto) Tier2SpecializationProfile {
	var profile Tier2SpecializationProfile
	if proto == nil {
		return profile
	}
	for pc, fb := range proto.Feedback {
		observed := false
		if fb.Left != vm.FBUnobserved {
			observed = true
			if typ, ok := feedbackToIRType(fb.Left); ok {
				profile.addGuard(SpecializationGuard{Kind: SpecGuardOperandType, PC: pc, Slot: "left", Type: typ, FBType: fb.Left})
			}
		}
		if fb.Right != vm.FBUnobserved {
			observed = true
			if typ, ok := feedbackToIRType(fb.Right); ok {
				profile.addGuard(SpecializationGuard{Kind: SpecGuardOperandType, PC: pc, Slot: "right", Type: typ, FBType: fb.Right})
			}
		}
		if fb.Result != vm.FBUnobserved {
			observed = true
			if typ, ok := feedbackToIRType(fb.Result); ok {
				profile.addGuard(SpecializationGuard{Kind: SpecGuardResultType, PC: pc, Slot: "result", Type: typ, FBType: fb.Result})
			}
		}
		if fb.Kind != vm.FBKindUnobserved {
			observed = true
			if fb.Kind != vm.FBKindPolymorphic {
				profile.addGuard(SpecializationGuard{Kind: SpecGuardTableKind, PC: pc, TableKind: fb.Kind})
			}
		}
		if observed {
			profile.Snapshot.TypeObserved++
		}
	}
	for pc, fb := range proto.FieldAccessFeedback {
		if fb.Count == 0 {
			continue
		}
		profile.Snapshot.FieldObserved++
		shapeID, fieldIdx, ok := fb.StableShapeField()
		if !ok {
			continue
		}
		guard := SpecializationGuard{
			Kind:       SpecGuardFieldShape,
			PC:         pc,
			ShapeID:    shapeID,
			FieldIdx:   fieldIdx,
			AccessKind: fb.AccessKind,
			Count:      fb.Count,
			FBType:     fb.ValueType,
		}
		if typ, ok := feedbackToIRType(fb.ValueType); ok {
			guard.Type = typ
		}
		profile.addGuard(guard)
	}
	for pc, fb := range proto.TableKeyFeedback {
		if fb.Count == 0 {
			continue
		}
		profile.Snapshot.TableKeyObserved++
		key, shapeID, fieldIdx, ok := fb.StableStringShapeField()
		if !ok {
			continue
		}
		guard := SpecializationGuard{
			Kind:       SpecGuardStringShapeKey,
			PC:         pc,
			ShapeID:    shapeID,
			FieldIdx:   fieldIdx,
			Key:        key,
			AccessKind: fb.AccessKind,
			Count:      fb.Count,
			FBType:     fb.ValueType,
		}
		if typ, ok := feedbackToIRType(fb.ValueType); ok {
			guard.Type = typ
		}
		profile.addGuard(guard)
	}
	for pc, fb := range proto.CallSiteFeedback {
		if fb.Count == 0 {
			continue
		}
		profile.Snapshot.CallObserved++
		if kind, data, ok := fb.StableCalleeNativeIdentity(); ok {
			profile.addGuard(SpecializationGuard{
				Kind:             SpecGuardCallNative,
				PC:               pc,
				Count:            fb.Count,
				CalleeNativeKind: kind,
				CalleeNativeData: data,
				NArgs:            fb.NArgs,
				ResultArity:      fb.ResultArity,
			})
			continue
		}
		if callee, ok := fb.StableCalleeVMProto(); ok {
			profile.addGuard(SpecializationGuard{
				Kind:          SpecGuardCallVMProto,
				PC:            pc,
				Count:         fb.Count,
				CalleeVMProto: callee,
				NArgs:         fb.NArgs,
				ResultArity:   fb.ResultArity,
			})
			continue
		}
		if protos := fb.MaturePolymorphicVMProtos(wholeCallKernelMinStableObservations, int(fb.NArgs), fb.ResultArity); len(protos) > 0 {
			profile.addGuard(SpecializationGuard{
				Kind:           SpecGuardCallPolymorphic,
				PC:             pc,
				Count:          fb.Count,
				CalleeVMProtos: protos,
				NArgs:          fb.NArgs,
				ResultArity:    fb.ResultArity,
			})
		}
	}
	profile.Version = profile.computeVersion()
	return profile
}

func (p *Tier2SpecializationProfile) addGuard(g SpecializationGuard) {
	p.Guards = append(p.Guards, g)
}

func (p Tier2SpecializationProfile) computeVersion() Tier2SpecializationVersion {
	h := fnv.New64a()
	fmt.Fprintf(h, "snapshot:%d/%d/%d/%d;",
		p.Snapshot.TypeObserved, p.Snapshot.FieldObserved,
		p.Snapshot.TableKeyObserved, p.Snapshot.CallObserved)
	for _, g := range p.Guards {
		fmt.Fprintf(h, "%s:%d:%s:%d:%d:%d:%d:%d:%s:%d:%d:%d:%d:",
			g.Kind, g.PC, g.Slot, g.Type, g.FBType, g.TableKind,
			g.ShapeID, g.FieldIdx, g.Key, g.AccessKind, g.Count,
			g.CalleeNativeKind, g.CalleeNativeData)
		if g.CalleeVMProto != nil {
			fmt.Fprintf(h, "vm:%x:%s:", uintptr(unsafe.Pointer(g.CalleeVMProto)), g.CalleeVMProto.Name)
		}
		for _, callee := range g.CalleeVMProtos {
			if callee == nil {
				continue
			}
			fmt.Fprintf(h, "poly:%x:%s:", uintptr(unsafe.Pointer(callee)), callee.Name)
		}
		fmt.Fprintf(h, "arity:%d:%d;", g.NArgs, g.ResultArity)
	}
	return Tier2SpecializationVersion{Hash: h.Sum64(), GuardCount: len(p.Guards)}
}

// Tier2SpeculationPlan is the single read interface from feedback into the
// Tier 2 graph builder. Keeping this boundary explicit makes later runtime
// specialization/recompile work a policy change instead of another set of
// direct vector probes spread across bytecode lowering.
type Tier2SpeculationPlan struct {
	proto                *vm.FuncProto
	suppressedGuardPCs   map[int]bool
	suppressedGuardKinds map[int]map[string]bool
	Snapshot             Tier2FeedbackSnapshot
	Profile              Tier2SpecializationProfile
}

const tier2GlobalGuardSuppressPC = -1

func NewTier2SpeculationPlan(proto *vm.FuncProto) Tier2SpeculationPlan {
	return NewTier2SpeculationPlanWithSuppressedGuards(proto, nil)
}

func NewTier2SpeculationPlanWithSuppressedGuards(proto *vm.FuncProto, suppressed map[int]bool) Tier2SpeculationPlan {
	return NewTier2SpeculationPlanWithSuppressedGuardKinds(proto, suppressed, nil)
}

func NewTier2SpeculationPlanWithSuppressedGuardKinds(proto *vm.FuncProto, suppressed map[int]bool, suppressedKinds map[int]map[string]bool) Tier2SpeculationPlan {
	profile := BuildTier2SpecializationProfile(proto)
	var suppressedCopy map[int]bool
	if len(suppressed) > 0 {
		suppressedCopy = make(map[int]bool, len(suppressed))
		for pc, ok := range suppressed {
			if ok {
				suppressedCopy[pc] = true
			}
		}
	}
	kindsCopy := copySuppressedGuardKinds(suppressedKinds)
	if suppressedCopy == nil && len(kindsCopy) > 0 {
		suppressedCopy = make(map[int]bool, len(kindsCopy))
		for pc := range kindsCopy {
			suppressedCopy[pc] = true
		}
	}
	profile = profile.withSuppressedGuards(suppressedCopy, kindsCopy)
	return Tier2SpeculationPlan{
		proto:                proto,
		suppressedGuardPCs:   suppressedCopy,
		suppressedGuardKinds: kindsCopy,
		Snapshot:             profile.Snapshot,
		Profile:              profile,
	}
}

func (p Tier2SpecializationProfile) withSuppressedGuards(suppressed map[int]bool, suppressedKinds map[int]map[string]bool) Tier2SpecializationProfile {
	if len(suppressed) == 0 && len(suppressedKinds) == 0 {
		return p
	}
	filtered := p
	filtered.Guards = make([]SpecializationGuard, 0, len(p.Guards))
	for _, guard := range p.Guards {
		if specializationGuardSuppressed(guard, suppressed, suppressedKinds) {
			continue
		}
		filtered.Guards = append(filtered.Guards, guard)
	}
	filtered.Version = filtered.computeVersion()
	return filtered
}

func specializationGuardSuppressed(guard SpecializationGuard, suppressed map[int]bool, suppressedKinds map[int]map[string]bool) bool {
	if suppressed != nil && suppressed[guard.PC] {
		return true
	}
	op := specializationGuardOpName(guard.Kind)
	if op == "" || len(suppressedKinds) == 0 {
		return false
	}
	if global := suppressedKinds[tier2GlobalGuardSuppressPC]; len(global) > 0 && (global[op] || global["*"]) {
		return true
	}
	kinds := suppressedKinds[guard.PC]
	return kinds[op] || kinds["*"]
}

func specializationGuardOpName(kind SpecializationGuardKind) string {
	switch kind {
	case SpecGuardResultType, SpecGuardOperandType:
		return "GuardType"
	case SpecGuardTableKind:
		return "GuardTableKind"
	case SpecGuardStringShapeKey:
		return "GuardConstString"
	case SpecGuardCallNative, SpecGuardCallVMProto, SpecGuardCallPolymorphic:
		return "GuardCalleeProto"
	default:
		return ""
	}
}

func (p Tier2SpeculationPlan) GuardSuppressed(pc int) bool {
	return p.suppressedGuardPCs != nil && p.suppressedGuardPCs[pc]
}

func (p Tier2SpeculationPlan) GuardKindSuppressed(pc int, kind string) bool {
	if p.suppressedGuardKinds == nil {
		return p.GuardSuppressed(pc)
	}
	if global := p.suppressedGuardKinds[tier2GlobalGuardSuppressPC]; len(global) > 0 && (global[kind] || global["*"]) {
		return true
	}
	kinds := p.suppressedGuardKinds[pc]
	if len(kinds) == 0 {
		return false
	}
	return kinds[kind] || kinds["*"]
}

func copySuppressedGuardKinds(in map[int]map[string]bool) map[int]map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]map[string]bool, len(in))
	for pc, kinds := range in {
		if len(kinds) == 0 {
			continue
		}
		dst := make(map[string]bool, len(kinds))
		for kind, ok := range kinds {
			if ok {
				dst[kind] = true
			}
		}
		if len(dst) > 0 {
			out[pc] = dst
		}
	}
	return out
}

func (p Tier2SpeculationPlan) SuppressedGuardPCs() map[int]bool {
	if len(p.suppressedGuardPCs) == 0 {
		return nil
	}
	out := make(map[int]bool, len(p.suppressedGuardPCs))
	for pc, ok := range p.suppressedGuardPCs {
		if ok {
			out[pc] = true
		}
	}
	return out
}

func (p Tier2SpeculationPlan) SuppressedGuardKinds() map[int]map[string]bool {
	return copySuppressedGuardKinds(p.suppressedGuardKinds)
}

func (p Tier2SpeculationPlan) TypeFeedback(pc int) (vm.TypeFeedback, bool) {
	if p.proto == nil || pc < 0 || p.proto.Feedback == nil || pc >= len(p.proto.Feedback) {
		return vm.TypeFeedback{}, false
	}
	return p.proto.Feedback[pc], true
}

func (p Tier2SpeculationPlan) ResultGuardType(pc int) (Type, bool) {
	if p.GuardKindSuppressed(pc, "GuardType") {
		return TypeUnknown, false
	}
	if guard, ok := p.Profile.findGuard(pc, SpecGuardResultType, nil); ok && guard.Type != TypeUnknown {
		return guard.Type, true
	}
	fb, ok := p.TypeFeedback(pc)
	if !ok {
		return TypeUnknown, false
	}
	return feedbackToIRType(fb.Result)
}

func (p Tier2SpeculationPlan) OperandGuardTypes(pc int) (left Type, leftOK bool, right Type, rightOK bool) {
	if p.GuardKindSuppressed(pc, "GuardType") {
		return TypeUnknown, false, TypeUnknown, false
	}
	if guard, ok := p.Profile.findGuard(pc, SpecGuardOperandType, func(g SpecializationGuard) bool {
		return g.Slot == "left"
	}); ok && guard.Type != TypeUnknown {
		left, leftOK = guard.Type, true
	}
	if guard, ok := p.Profile.findGuard(pc, SpecGuardOperandType, func(g SpecializationGuard) bool {
		return g.Slot == "right"
	}); ok && guard.Type != TypeUnknown {
		right, rightOK = guard.Type, true
	}
	if leftOK || rightOK {
		return left, leftOK, right, rightOK
	}
	fb, ok := p.TypeFeedback(pc)
	if !ok {
		return TypeUnknown, false, TypeUnknown, false
	}
	left, leftOK = feedbackToIRType(fb.Left)
	right, rightOK = feedbackToIRType(fb.Right)
	return left, leftOK, right, rightOK
}

func (p Tier2SpeculationPlan) TableKindAux(pc int) int64 {
	if p.GuardKindSuppressed(pc, "GuardTableKind") {
		return 0
	}
	if guard, ok := p.Profile.findGuard(pc, SpecGuardTableKind, nil); ok && guard.TableKind != 0 {
		return int64(guard.TableKind)
	}
	fb, ok := p.TypeFeedback(pc)
	if !ok || fb.Kind == vm.FBKindUnobserved || fb.Kind == vm.FBKindPolymorphic {
		return 0
	}
	return int64(fb.Kind)
}

func (p Tier2SpeculationPlan) FieldShapeAux2(pc int) int64 {
	if guard, ok := p.Profile.findGuard(pc, SpecGuardFieldShape, nil); ok && guard.ShapeID != 0 && guard.FieldIdx >= 0 {
		return int64(guard.ShapeID)<<32 | int64(uint32(guard.FieldIdx))
	}
	if p.proto == nil || pc < 0 {
		return 0
	}
	if p.proto.FieldAccessFeedback != nil && pc < len(p.proto.FieldAccessFeedback) {
		feedback := p.proto.FieldAccessFeedback[pc]
		if feedback.Count > 0 {
			shapeID, fieldIdx, ok := feedback.StableShapeField()
			if ok {
				return int64(shapeID)<<32 | int64(uint32(fieldIdx))
			}
			return 0
		}
	}
	if p.proto.FieldCache != nil && pc < len(p.proto.FieldCache) {
		entry := p.proto.FieldCache[pc]
		if entry.ShapeID != 0 && entry.FieldIdx >= 0 {
			return int64(entry.ShapeID)<<32 | int64(uint32(entry.FieldIdx))
		}
	}
	return 0
}

func (p Tier2SpeculationPlan) FieldValueGuardType(pc int) (Type, bool) {
	if p.GuardKindSuppressed(pc, "GuardType") {
		return TypeUnknown, false
	}
	if guard, ok := p.Profile.findGuard(pc, SpecGuardFieldShape, nil); ok {
		return specializationGuardValueType(guard)
	}
	if typ, ok := p.sameFieldWriteValueGuardType(pc); ok {
		return typ, true
	}
	return TypeUnknown, false
}

func (p Tier2SpeculationPlan) sameFieldWriteValueGuardType(pc int) (Type, bool) {
	if p.proto == nil || pc < 0 || pc >= len(p.proto.Code) || p.proto.Feedback == nil {
		return TypeUnknown, false
	}
	inst := p.proto.Code[pc]
	if vm.DecodeOp(inst) != vm.OP_GETFIELD {
		return TypeUnknown, false
	}
	fieldConst := vm.DecodeC(inst)
	observed := TypeUnknown
	for writePC, writeInst := range p.proto.Code {
		if vm.DecodeOp(writeInst) != vm.OP_SETFIELD || vm.DecodeB(writeInst) != fieldConst {
			continue
		}
		if writePC < 0 || writePC >= len(p.proto.Feedback) {
			continue
		}
		typ, ok := feedbackToIRType(p.proto.Feedback[writePC].Result)
		if !ok || typ == TypeUnknown || typ == TypeAny {
			continue
		}
		if observed == TypeUnknown {
			observed = typ
			continue
		}
		if observed != typ {
			return TypeUnknown, false
		}
	}
	if observed == TypeUnknown {
		return TypeUnknown, false
	}
	if observed != TypeInt {
		return TypeUnknown, false
	}
	return observed, true
}

func (p Tier2SpeculationPlan) StableStringShapeField(pc int, accessKind uint8) (key string, shapeID uint32, fieldIdx int, ok bool) {
	if guard, found := p.Profile.findGuard(pc, SpecGuardStringShapeKey, func(g SpecializationGuard) bool {
		return g.AccessKind == 0 || g.AccessKind == accessKind
	}); found && guard.Key != "" && guard.ShapeID != 0 && guard.FieldIdx >= 0 {
		return guard.Key, guard.ShapeID, guard.FieldIdx, true
	}
	if p.proto == nil || pc < 0 || p.proto.TableKeyFeedback == nil || pc >= len(p.proto.TableKeyFeedback) {
		return "", 0, 0, false
	}
	feedback := p.proto.TableKeyFeedback[pc]
	if accessKind == vm.TableAccessKindSet && (feedback.ValueType == vm.FBAny || feedback.ValueType == vm.FBUnobserved) {
		return "", 0, 0, false
	}
	return feedback.StableStringShapeField()
}

func (p Tier2SpeculationPlan) StringShapeValueGuardType(pc int, accessKind uint8) (Type, bool) {
	if p.GuardKindSuppressed(pc, "GuardType") {
		return TypeUnknown, false
	}
	guard, ok := p.Profile.findGuard(pc, SpecGuardStringShapeKey, func(g SpecializationGuard) bool {
		return g.AccessKind == 0 || g.AccessKind == accessKind
	})
	if !ok {
		return TypeUnknown, false
	}
	return specializationGuardValueType(guard)
}

func specializationGuardValueType(guard SpecializationGuard) (Type, bool) {
	if guard.Type != TypeUnknown && guard.Type != TypeAny {
		return guard.Type, true
	}
	return feedbackToIRType(guard.FBType)
}

// Tier2RecompilePolicy decides when an already-published Tier 2 body was
// compiled against feedback that was still materially immature. The policy is
// deliberately conservative: structural feedback unlocks table/call lowering,
// while pure type-feedback growth must be large enough to avoid recompile
// churn while a function is still warming.
type Tier2RecompilePolicy struct{}

func (Tier2RecompilePolicy) ShouldRefresh(_ *vm.FuncProto, compiled any, current Tier2FeedbackSnapshot) bool {
	return (Tier2RecompilePolicy{}).ShouldRefreshProfile(compiled, Tier2SpecializationProfile{Snapshot: current})
}

func (Tier2RecompilePolicy) ShouldRefreshProfileForProto(proto *vm.FuncProto, compiled any, current Tier2SpecializationProfile) bool {
	if (Tier2RecompilePolicy{}).ShouldRefreshProfile(compiled, current) {
		return true
	}
	cf, ok := compiled.(*CompiledFunction)
	if !ok || cf == nil || proto == nil {
		return false
	}
	previousReadiness := AnalyzeTier2FeedbackReadiness(proto, cf.SpeculationSnapshot)
	currentReadiness := AnalyzeTier2FeedbackReadiness(proto, current.Snapshot)
	return currentReadiness.MoreReadyThan(previousReadiness)
}

func (Tier2RecompilePolicy) ShouldRefreshProfile(compiled any, current Tier2SpecializationProfile) bool {
	cf, ok := compiled.(*CompiledFunction)
	if !ok || cf == nil {
		return false
	}
	previous := cf.SpeculationSnapshot
	if cf.SpecializationVersion.Hash != 0 &&
		current.Version.Hash != 0 &&
		cf.SpecializationVersion.Hash != current.Version.Hash &&
		current.Version.GuardCount > cf.SpecializationVersion.GuardCount {
		return true
	}
	if cf.SpecializationVersion.Hash != 0 &&
		current.Version.Hash != 0 &&
		cf.SpecializationVersion.Hash != current.Version.Hash &&
		current.Snapshot.structuralObserved() > 0 &&
		current.Version.GuardCount >= cf.SpecializationVersion.GuardCount {
		return true
	}
	if cf.SpecializationVersion.Hash != 0 &&
		current.Version.Hash != 0 &&
		cf.SpecializationVersion.Hash != current.Version.Hash &&
		previous.structuralObserved() == 0 &&
		current.Snapshot.structuralObserved() > 0 {
		return true
	}
	if !previous.lessMatureThan(current.Snapshot) {
		return false
	}
	if previous.totalObserved() == 0 {
		return current.Snapshot.structuralObserved() > 0 || current.Snapshot.TypeObserved >= 4
	}
	if previous.FieldObserved == 0 && current.Snapshot.FieldObserved > 0 {
		return true
	}
	if previous.TableKeyObserved == 0 && current.Snapshot.TableKeyObserved > 0 {
		return true
	}
	if previous.CallObserved == 0 && current.Snapshot.CallObserved > 0 {
		return true
	}
	if current.Snapshot.structuralObserved()-previous.structuralObserved() >= 2 {
		return true
	}
	if current.Snapshot.TypeObserved-previous.TypeObserved >= 8 {
		return true
	}
	return false
}
