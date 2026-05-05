package vm

import "testing"

func TestResumePayloadIsFieldOnlyAcceptsFieldReadsUntilLoopBoundary(t *testing.T) {
	proto := &FuncProto{Code: []uint32{
		EncodeABC(OP_GETFIELD, 3, 1, 0),
		EncodeAsBx(OP_FORLOOP, 4, -2),
	}}
	if !(&VM{}).ResumePayloadIsFieldOnly(proto, 0, 0, 3) {
		t.Fatal("GETFIELD-only payload use should be eligible")
	}
}

func TestResumePayloadIsFieldOnlyRejectsEscapes(t *testing.T) {
	tests := []struct {
		name string
		code []uint32
	}{
		{
			name: "move payload",
			code: []uint32{EncodeABC(OP_MOVE, 4, 1, 0)},
		},
		{
			name: "return payload",
			code: []uint32{EncodeABC(OP_RETURN, 1, 2, 0)},
		},
		{
			name: "call argument",
			code: []uint32{EncodeABC(OP_CALL, 0, 2, 1)},
		},
		{
			name: "table receiver",
			code: []uint32{EncodeABC(OP_GETTABLE, 4, 1, 2)},
		},
		{
			name: "table store value",
			code: []uint32{EncodeABC(OP_SETFIELD, 4, 0, 1)},
		},
		{
			name: "truth test",
			code: []uint32{EncodeABC(OP_TEST, 1, 0, 1)},
		},
		{
			name: "close captured payload",
			code: []uint32{EncodeABC(OP_CLOSE, 1, 0, 0)},
		},
	}

	vm := &VM{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := &FuncProto{Code: tt.code}
			if vm.ResumePayloadIsFieldOnly(proto, 0, 0, 3) {
				t.Fatal("payload escape should be rejected")
			}
		})
	}
}

func TestResumePayloadIsFieldOnlyRejectsNonPayloadArity(t *testing.T) {
	proto := &FuncProto{Code: []uint32{EncodeABC(OP_GETFIELD, 3, 1, 0)}}
	if (&VM{}).ResumePayloadIsFieldOnly(proto, 0, 0, 4) {
		t.Fatal("resume arity other than ok,payload must not enable pooled payload")
	}
}
