package methodjit

import (
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
	var s Tier2FeedbackSnapshot
	if proto == nil {
		return s
	}
	for _, fb := range proto.Feedback {
		if fb.Left != vm.FBUnobserved || fb.Right != vm.FBUnobserved ||
			fb.Result != vm.FBUnobserved || fb.Kind != vm.FBKindUnobserved {
			s.TypeObserved++
		}
	}
	for _, fb := range proto.FieldAccessFeedback {
		if fb.Count > 0 {
			s.FieldObserved++
		}
	}
	for _, fb := range proto.TableKeyFeedback {
		if fb.Count > 0 {
			s.TableKeyObserved++
		}
	}
	for _, fb := range proto.CallSiteFeedback {
		if fb.Count > 0 {
			s.CallObserved++
		}
	}
	return s
}

// Tier2SpeculationPlan is the single read interface from feedback into the
// Tier 2 graph builder. Keeping this boundary explicit makes later runtime
// specialization/recompile work a policy change instead of another set of
// direct vector probes spread across bytecode lowering.
type Tier2SpeculationPlan struct {
	proto    *vm.FuncProto
	Snapshot Tier2FeedbackSnapshot
}

type Tier2CallSiteVMProtoTarget struct {
	Proto *vm.FuncProto
	Count uint32
}

func NewTier2SpeculationPlan(proto *vm.FuncProto) Tier2SpeculationPlan {
	return Tier2SpeculationPlan{
		proto:    proto,
		Snapshot: snapshotTier2Feedback(proto),
	}
}

func (p Tier2SpeculationPlan) TypeFeedback(pc int) (vm.TypeFeedback, bool) {
	if p.proto == nil || pc < 0 || p.proto.Feedback == nil || pc >= len(p.proto.Feedback) {
		return vm.TypeFeedback{}, false
	}
	return p.proto.Feedback[pc], true
}

func (p Tier2SpeculationPlan) ResultGuardType(pc int) (Type, bool) {
	fb, ok := p.TypeFeedback(pc)
	if !ok {
		return TypeUnknown, false
	}
	return feedbackToIRType(fb.Result)
}

func (p Tier2SpeculationPlan) OperandGuardTypes(pc int) (left Type, leftOK bool, right Type, rightOK bool) {
	fb, ok := p.TypeFeedback(pc)
	if !ok {
		return TypeUnknown, false, TypeUnknown, false
	}
	left, leftOK = feedbackToIRType(fb.Left)
	right, rightOK = feedbackToIRType(fb.Right)
	return left, leftOK, right, rightOK
}

func (p Tier2SpeculationPlan) TableKindAux(pc int) int64 {
	fb, ok := p.TypeFeedback(pc)
	if !ok || fb.Kind == vm.FBKindUnobserved || fb.Kind == vm.FBKindPolymorphic {
		return 0
	}
	return int64(fb.Kind)
}

func (p Tier2SpeculationPlan) FieldShapeAux2(pc int) int64 {
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

func (p Tier2SpeculationPlan) StableStringShapeField(pc int, accessKind uint8) (key string, shapeID uint32, fieldIdx int, ok bool) {
	if p.proto == nil || pc < 0 || p.proto.TableKeyFeedback == nil || pc >= len(p.proto.TableKeyFeedback) {
		return "", 0, 0, false
	}
	feedback := p.proto.TableKeyFeedback[pc]
	if accessKind == vm.TableAccessKindSet && (feedback.ValueType == vm.FBAny || feedback.ValueType == vm.FBUnobserved) {
		return "", 0, 0, false
	}
	return feedback.StableStringShapeField()
}

func (p Tier2SpeculationPlan) StableCallSiteVMProtoTarget(pc int, minCount uint32, nArgs, resultArity int) (*vm.FuncProto, bool) {
	if p.proto == nil || pc < 0 || p.proto.CallSiteFeedback == nil || pc >= len(p.proto.CallSiteFeedback) {
		return nil, false
	}
	feedback := p.proto.CallSiteFeedback[pc]
	if feedback.Count < minCount || feedback.Flags&vm.CallSiteArityPolymorphic != 0 ||
		int(feedback.NArgs) != nArgs || int(feedback.ResultArity) != resultArity {
		return nil, false
	}
	return feedback.StableCalleeVMProto()
}

func (p Tier2SpeculationPlan) CallSiteVMProtoTargets(pc int, minCount uint32, nArgs, resultArity int) []Tier2CallSiteVMProtoTarget {
	if p.proto == nil || pc < 0 || p.proto.CallSiteFeedback == nil || pc >= len(p.proto.CallSiteFeedback) {
		return nil
	}
	feedback := p.proto.CallSiteFeedback[pc]
	if feedback.Count < minCount || feedback.Flags&vm.CallSiteArityPolymorphic != 0 ||
		int(feedback.NArgs) != nArgs || int(feedback.ResultArity) != resultArity {
		return nil
	}
	if proto, ok := feedback.StableCalleeVMProto(); ok {
		return []Tier2CallSiteVMProtoTarget{{Proto: proto, Count: feedback.Count}}
	}
	candidates := feedback.VMProtoCandidates()
	if len(candidates) == 0 {
		return nil
	}
	out := make([]Tier2CallSiteVMProtoTarget, 0, len(candidates))
	for _, candidate := range candidates {
		proto := candidate.Proto()
		if proto == nil {
			continue
		}
		out = append(out, Tier2CallSiteVMProtoTarget{
			Proto: proto,
			Count: candidate.Count,
		})
	}
	return out
}

// Tier2RecompilePolicy decides when an already-published Tier 2 body was
// compiled against feedback that was still materially immature. The policy is
// deliberately conservative: structural feedback unlocks table/call lowering,
// while pure type-feedback growth must be large enough to avoid recompile
// churn while a function is still warming.
type Tier2RecompilePolicy struct{}

func (Tier2RecompilePolicy) ShouldRefresh(_ *vm.FuncProto, compiled any, current Tier2FeedbackSnapshot) bool {
	cf, ok := compiled.(*CompiledFunction)
	if !ok || cf == nil {
		return false
	}
	previous := cf.SpeculationSnapshot
	if !previous.lessMatureThan(current) {
		return false
	}
	if previous.totalObserved() == 0 {
		return current.structuralObserved() > 0 || current.TypeObserved >= 4
	}
	if previous.FieldObserved == 0 && current.FieldObserved > 0 {
		return true
	}
	if previous.TableKeyObserved == 0 && current.TableKeyObserved > 0 {
		return true
	}
	if previous.CallObserved == 0 && current.CallObserved > 0 {
		return true
	}
	if current.structuralObserved()-previous.structuralObserved() >= 2 {
		return true
	}
	if current.TypeObserved-previous.TypeObserved >= 8 {
		return true
	}
	return false
}
