//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func protoConstString(proto *vm.FuncProto, idx int) string {
	if proto == nil || idx < 0 || idx >= len(proto.Constants) {
		return ""
	}
	val := proto.Constants[idx]
	if !val.IsString() {
		return ""
	}
	return val.Str()
}

func hasGenericStringFormatIntCall(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	type slotState struct {
		kind string
	}
	states := make([]slotState, proto.MaxStack+8)
	clear := func(slot int) {
		if slot >= 0 && slot < len(states) {
			states[slot] = slotState{}
		}
	}
	get := func(slot int) slotState {
		if slot >= 0 && slot < len(states) {
			return states[slot]
		}
		return slotState{}
	}
	set := func(slot int, st slotState) {
		if slot >= 0 && slot < len(states) {
			states[slot] = st
		}
	}
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_LOADK:
			s := protoConstString(proto, vm.DecodeBx(inst))
			if s != "" && simpleSingleDecimalIntFormat(s) {
				set(a, slotState{kind: "const_single_int_format"})
			} else if s != "" && simpleConstStringFormatNativeEligible(s) {
				set(a, slotState{kind: "const_native_string_format"})
			} else {
				clear(a)
			}
		case vm.OP_GETGLOBAL:
			if protoConstString(proto, vm.DecodeBx(inst)) == "string" {
				set(a, slotState{kind: "string_global"})
			} else {
				clear(a)
			}
		case vm.OP_GETFIELD:
			if get(vm.DecodeB(inst)).kind == "string_global" && protoConstString(proto, vm.DecodeC(inst)) == "format" {
				set(a, slotState{kind: "string_format"})
			} else {
				clear(a)
			}
		case vm.OP_MOVE:
			set(a, get(vm.DecodeB(inst)))
		case vm.OP_CALL:
			b := vm.DecodeB(inst)
			if b == 3 && get(a).kind == "string_format" &&
				(get(a+1).kind == "const_single_int_format" || callSiteFeedbackHasStableStringFormatInt(proto, pc)) {
				return true
			}
			if b >= 4 && get(a).kind == "string_format" && get(a+1).kind == "const_native_string_format" {
				return true
			}
			c := vm.DecodeC(inst)
			if c == 0 {
				clear(a)
			} else {
				for slot := a; slot <= a+c-2; slot++ {
					clear(slot)
				}
			}
		case vm.OP_FORLOOP:
			clear(a)
			clear(a + 3)
		case vm.OP_FORPREP:
			clear(a)
		case vm.OP_SETGLOBAL, vm.OP_SETTABLE, vm.OP_SETFIELD, vm.OP_SETUPVAL, vm.OP_SETLIST, vm.OP_RETURN:
		default:
			clear(a)
		}
	}
	return false
}

func simpleConstStringFormatNativeEligible(pattern string) bool {
	if pattern == "" {
		return false
	}
	pat, ok := parseStringFormatConstIntPatternNative(pattern)
	return ok && len(pat.specs) >= 1 && len(pat.specs) <= 8
}

func callSiteFeedbackHasStableStringFormatInt(proto *vm.FuncProto, pc int) bool {
	if proto == nil || proto.CallSiteFeedback == nil || pc < 0 || pc >= len(proto.CallSiteFeedback) {
		return false
	}
	cf := proto.CallSiteFeedback[pc]
	kind, data, ok := cf.StableCalleeNativeIdentity()
	if !ok || kind != runtime.NativeKindStdStringFormat || data != uintptr(runtime.StdStringFormatIdentityPtr()) {
		return false
	}
	if cf.NArgs != 2 || cf.Flags&vm.CallSiteArityPolymorphic != 0 || cf.ArgTypes[1] != vm.FBInt {
		return false
	}
	pattern, ok := cf.StableStringArg(0)
	return ok && simpleSingleDecimalIntFormat(pattern)
}
