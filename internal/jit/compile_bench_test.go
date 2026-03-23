//go:build darwin && arm64

package jit

import (
	"testing"
	"github.com/gscript/gscript/internal/vm"
)

func BenchmarkJITCompileOnly(b *testing.B) {
	// sumN function bytecodes
	code := []uint32{
		vm.EncodeAsBx(vm.OP_LOADINT, 1, 0),      // R(1) = 0 (s)
		vm.EncodeAsBx(vm.OP_LOADINT, 2, 1),      // R(2) = 1 (init)
		vm.EncodeABC(vm.OP_MOVE, 3, 0, 0),       // R(3) = R(0) (limit=n)
		vm.EncodeAsBx(vm.OP_LOADINT, 4, 1),      // R(4) = 1 (step)
		vm.EncodeAsBx(vm.OP_FORPREP, 2, 2),      // FORPREP R(2) → pc 7
		vm.EncodeABC(vm.OP_ADD, 1, 1, 5),        // R(1) = R(1) + R(5) (s += i)
		// pc 6: (body end, fall through to FORLOOP)
		vm.EncodeAsBx(vm.OP_FORLOOP, 2, -2),     // FORLOOP R(2) → pc 5
		vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),     // return R(1)
	}
	proto := &vm.FuncProto{
		Code:      code,
		NumParams: 1,
		MaxStack:  8,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cg := &Codegen{
			asm:   NewAssembler(),
			proto: proto,
		}
		cf, err := cg.compile()
		if err != nil {
			b.Fatal(err)
		}
		cf.Code.Free()
	}
}
