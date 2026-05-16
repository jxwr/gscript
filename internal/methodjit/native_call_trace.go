//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"os"
	"strings"

	"github.com/gscript/gscript/internal/vm"
)

func (ec *emitContext) traceNativeCallEmit(instr *Instr, pathKind string, callee *vm.FuncProto, desc *CallABIDescriptor) {
	if ec == nil || instr == nil || !ec.traceNativeCalls {
		return
	}
	caller := (*vm.FuncProto)(nil)
	if ec.fn != nil {
		caller = ec.fn.Proto
	}
	if callee == nil {
		callee = ec.traceResolvedCallee(instr)
	}
	sourcePC := -1
	if instr.HasSource {
		sourcePC = instr.SourcePC
	}
	abi := "none"
	if desc != nil {
		abi = callABIDescSummary(*desc)
	} else if ec.fn != nil && ec.fn.CallABIs != nil {
		if d, ok := ec.fn.CallABIs[instr.ID]; ok {
			abi = callABIDescSummary(d)
		}
	}
	reason := fmt.Sprintf("caller=%s instr=%d source_pc=%d path=%s callee=%s callee_ops=%s call_abi=%s",
		traceProtoName(caller), instr.ID, sourcePC, pathKind, traceProtoName(callee),
		protoNativeCallRiskSummary(callee), abi)
	if remarks := functionRemarks(ec.fn); remarks != nil {
		remarks.Add("NativeCallTrace", "emit", ec.currentBlockID, instr.ID, instr.Op, reason)
	}
	if ec.printNativeCallTrace {
		fmt.Fprintf(os.Stderr, "[R154] tier2 native-call emit %s\n", reason)
	}
}

func (ec *emitContext) traceResolvedCallee(instr *Instr) *vm.FuncProto {
	if ec == nil || instr == nil || ec.fn == nil {
		return nil
	}
	if ec.fn.Proto != nil && ec.isStaticSelfCall(instr) {
		return ec.fn.Proto
	}
	if callee := ec.staticNoDepthCallee(instr); callee != nil {
		return callee
	}
	if callee := ec.rawIntPeerCallee(instr); callee != nil {
		return callee
	}
	if spec := ec.callCalleeFlagSpec(instr); len(spec.protos) == 1 {
		return spec.protos[0]
	}
	return nil
}

func callABIDescSummary(desc CallABIDescriptor) string {
	parts := []string{
		fmt.Sprintf("args=%d", desc.NumArgs),
		fmt.Sprintf("rets=%d", desc.NumRets),
		fmt.Sprintf("return=%v", desc.ReturnRep),
	}
	if desc.TypedPeer {
		parts = append(parts, "kind=typed-peer")
	} else if len(desc.RawIntParams) > 0 || desc.RawIntReturn {
		parts = append(parts, "kind=raw-int")
	}
	if len(desc.ParamReps) > 0 {
		reps := make([]string, 0, len(desc.ParamReps))
		for _, rep := range desc.ParamReps {
			reps = append(reps, fmt.Sprint(rep))
		}
		parts = append(parts, "params=["+strings.Join(reps, ",")+"]")
	} else if len(desc.RawIntParams) > 0 {
		parts = append(parts, fmt.Sprintf("raw_int_params=%v", desc.RawIntParams))
	}
	if desc.Callee != nil {
		parts = append(parts, "callee="+traceProtoName(desc.Callee))
	}
	return strings.Join(parts, " ")
}

func protoNativeCallRiskSummary(proto *vm.FuncProto) string {
	if proto == nil {
		return "unknown"
	}
	seen := make(map[string]bool)
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_NEWTABLE:
			seen["NEWTABLE"] = true
		case vm.OP_NEWOBJECT2:
			seen["NEWOBJECT2"] = true
		case vm.OP_NEWOBJECTN:
			seen["NEWOBJECTN"] = true
		case vm.OP_SETLIST:
			seen["SETLIST"] = true
		case vm.OP_APPEND:
			seen["APPEND"] = true
		case vm.OP_GETTABLE:
			seen["GETTABLE"] = true
		case vm.OP_SETTABLE:
			seen["SETTABLE"] = true
		case vm.OP_GETFIELD:
			seen["GETFIELD"] = true
		case vm.OP_SETFIELD:
			seen["SETFIELD"] = true
		}
	}
	if len(seen) == 0 {
		return "none"
	}
	order := []string{"NEWTABLE", "NEWOBJECT2", "NEWOBJECTN", "SETLIST", "APPEND", "GETTABLE", "SETTABLE", "GETFIELD", "SETFIELD"}
	out := make([]string, 0, len(seen))
	for _, name := range order {
		if seen[name] {
			out = append(out, name)
		}
	}
	return strings.Join(out, ",")
}
