//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

func protoHasNoCallLikeOps(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_CALL, vm.OP_YIELD, vm.OP_RESUME, vm.OP_TFORCALL, vm.OP_GO:
			return false
		}
	}
	return true
}

func protoHasNoGlobalOps(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETGLOBAL, vm.OP_SETGLOBAL:
			return false
		}
	}
	return true
}
