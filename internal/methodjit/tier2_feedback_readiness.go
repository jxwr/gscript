//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

const tier2FeedbackHardHotCallCount = 16

type Tier2FeedbackReadinessKind string

const (
	Tier2FeedbackReadyWide         Tier2FeedbackReadinessKind = "ready_wide"
	Tier2FeedbackProvisionalNarrow Tier2FeedbackReadinessKind = "provisional_narrow"
	Tier2FeedbackDelay             Tier2FeedbackReadinessKind = "delay"
)

type Tier2FeedbackReadiness struct {
	Kind                  Tier2FeedbackReadinessKind `json:"kind"`
	Reason                string                     `json:"reason,omitempty"`
	ExpectedFieldSites    int                        `json:"expected_field_sites,omitempty"`
	ObservedFieldSites    int                        `json:"observed_field_sites,omitempty"`
	ExpectedTableKeySites int                        `json:"expected_table_key_sites,omitempty"`
	ObservedTableKeySites int                        `json:"observed_table_key_sites,omitempty"`
	ExpectedCallSites     int                        `json:"expected_call_sites,omitempty"`
	ObservedCallSites     int                        `json:"observed_call_sites,omitempty"`
	ImmatureFieldSites    int                        `json:"immature_field_sites,omitempty"`
	ImmatureTableKeySites int                        `json:"immature_table_key_sites,omitempty"`
	ImmatureCallSites     int                        `json:"immature_call_sites,omitempty"`
}

func (r Tier2FeedbackReadiness) structuralExpected() int {
	return r.ExpectedFieldSites + r.ExpectedTableKeySites + r.ExpectedCallSites
}

func (r Tier2FeedbackReadiness) structuralObserved() int {
	return r.ObservedFieldSites + r.ObservedTableKeySites + r.ObservedCallSites
}

func (r Tier2FeedbackReadiness) structuralImmature() int {
	return r.ImmatureFieldSites + r.ImmatureTableKeySites + r.ImmatureCallSites
}

func (r Tier2FeedbackReadiness) ShouldDelayInitialTier2(callCount int) bool {
	return r.Kind == Tier2FeedbackDelay && callCount < tier2FeedbackHardHotCallCount
}

func AnalyzeTier2FeedbackReadiness(proto *vm.FuncProto, snapshot Tier2FeedbackSnapshot) Tier2FeedbackReadiness {
	var readiness Tier2FeedbackReadiness
	readiness.Kind = Tier2FeedbackReadyWide
	if proto == nil {
		return readiness
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETFIELD, vm.OP_SETFIELD:
			readiness.ExpectedFieldSites++
		case vm.OP_GETTABLE, vm.OP_SETTABLE:
			readiness.ExpectedTableKeySites++
		case vm.OP_CALL, vm.OP_SELF:
			readiness.ExpectedCallSites++
		}
	}
	readiness.ObservedFieldSites = snapshot.FieldObserved
	readiness.ObservedTableKeySites = snapshot.TableKeyObserved
	readiness.ObservedCallSites = snapshot.CallObserved
	readiness.ImmatureFieldSites = positiveDelta(readiness.ExpectedFieldSites, readiness.ObservedFieldSites)
	readiness.ImmatureTableKeySites = positiveDelta(readiness.ExpectedTableKeySites, readiness.ObservedTableKeySites)
	readiness.ImmatureCallSites = positiveDelta(readiness.ExpectedCallSites, readiness.ObservedCallSites)

	if readiness.structuralExpected() == 0 || readiness.structuralImmature() == 0 {
		readiness.Kind = Tier2FeedbackReadyWide
		return readiness
	}
	if readiness.structuralObserved() == 0 {
		readiness.Kind = Tier2FeedbackDelay
		readiness.Reason = fmt.Sprintf("%d structural bytecode sites have no feedback yet", readiness.structuralExpected())
		return readiness
	}
	readiness.Kind = Tier2FeedbackProvisionalNarrow
	readiness.Reason = fmt.Sprintf("%d structural bytecode sites still lack feedback", readiness.structuralImmature())
	return readiness
}

func positiveDelta(expected, observed int) int {
	if observed >= expected {
		return 0
	}
	return expected - observed
}
