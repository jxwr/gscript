package vm

import "testing"

func TestInstructionEncoding(t *testing.T) {
	tests := []struct {
		name string
		enc  uint32
		op   Opcode
		a    int
		b    int
		c    int
	}{
		{"LOADNIL 0 5", EncodeABC(OP_LOADNIL, 0, 5, 0), OP_LOADNIL, 0, 5, 0},
		{"ADD 3 1 2", EncodeABC(OP_ADD, 3, 1, 2), OP_ADD, 3, 1, 2},
		{"MOVE 0 255", EncodeABC(OP_MOVE, 0, 255, 0), OP_MOVE, 0, 255, 0},
		{"CALL 5 3 2", EncodeABC(OP_CALL, 5, 3, 2), OP_CALL, 5, 3, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if DecodeOp(tt.enc) != tt.op {
				t.Errorf("op: got %d, want %d", DecodeOp(tt.enc), tt.op)
			}
			if DecodeA(tt.enc) != tt.a {
				t.Errorf("a: got %d, want %d", DecodeA(tt.enc), tt.a)
			}
			if DecodeB(tt.enc) != tt.b {
				t.Errorf("b: got %d, want %d", DecodeB(tt.enc), tt.b)
			}
			if DecodeC(tt.enc) != tt.c {
				t.Errorf("c: got %d, want %d", DecodeC(tt.enc), tt.c)
			}
		})
	}
}

func TestABxEncoding(t *testing.T) {
	inst := EncodeABx(OP_LOADK, 3, 1000)
	if DecodeOp(inst) != OP_LOADK {
		t.Errorf("op: got %v, want LOADK", DecodeOp(inst))
	}
	if DecodeA(inst) != 3 {
		t.Errorf("a: got %d, want 3", DecodeA(inst))
	}
	if DecodeBx(inst) != 1000 {
		t.Errorf("bx: got %d, want 1000", DecodeBx(inst))
	}
}

func TestAsBxEncoding(t *testing.T) {
	// Positive offset
	inst := EncodeAsBx(OP_FORLOOP, 0, 10)
	if DecodesBx(inst) != 10 {
		t.Errorf("sbx: got %d, want 10", DecodesBx(inst))
	}

	// Negative offset
	inst = EncodeAsBx(OP_FORLOOP, 0, -5)
	if DecodesBx(inst) != -5 {
		t.Errorf("sbx: got %d, want -5", DecodesBx(inst))
	}

	// Zero
	inst = EncodeAsBx(OP_JMP, 0, 0)
	if DecodesBx(inst) != 0 {
		t.Errorf("sbx: got %d, want 0", DecodesBx(inst))
	}
}

func TestRKEncoding(t *testing.T) {
	if IsRK(0) {
		t.Error("0 should not be RK")
	}
	if IsRK(255) {
		t.Error("255 should not be RK")
	}
	if !IsRK(256) {
		t.Error("256 should be RK")
	}
	if RKToConstIdx(256) != 0 {
		t.Errorf("RKToConstIdx(256): got %d, want 0", RKToConstIdx(256))
	}
	if ConstToRK(0) != 256 {
		t.Errorf("ConstToRK(0): got %d, want 256", ConstToRK(0))
	}
}

func TestOpName(t *testing.T) {
	if OpName(OP_ADD) != "ADD" {
		t.Errorf("OpName(OP_ADD): got %q, want ADD", OpName(OP_ADD))
	}
	if OpName(OP_RETURN) != "RETURN" {
		t.Errorf("OpName(OP_RETURN): got %q, want RETURN", OpName(OP_RETURN))
	}
}
